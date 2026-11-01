package dns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
)

// fakeDoH answers the JSON DoH API from a name -> type -> addresses table.
type fakeDoH struct {
	mu   sync.Mutex
	recs map[string]map[string][]string
}

func (f *fakeDoH) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Accept") != "application/dns-json" {
		w.WriteHeader(400)
		return
	}
	name, typ := r.URL.Query().Get("name"), r.URL.Query().Get("type")
	f.mu.Lock()
	addrs := f.recs[name][typ]
	f.mu.Unlock()
	code := map[string]int{"A": 1, "AAAA": 28}[typ]
	ans := []map[string]any{}
	status := 3
	if len(addrs) > 0 {
		status = 0
		ans = append(ans, map[string]any{"name": name, "type": 5, "data": "edge.example.net."}) // CNAME is skipped
	}
	for _, a := range addrs {
		ans = append(ans, map[string]any{"name": name, "type": code, "data": a})
	}
	json.NewEncoder(w).Encode(map[string]any{"Status": status, "Answer": ans})
}

func TestDoHResolve(t *testing.T) {
	a := &fakeDoH{recs: map[string]map[string][]string{"app.example.com": {"A": {"203.0.113.7"}, "AAAA": {"2001:db8::7"}}}}
	b := &fakeDoH{recs: map[string]map[string][]string{}}
	ta, tb := httptest.NewServer(a), httptest.NewServer(b)
	defer ta.Close()
	defer tb.Close()
	ctx := context.Background()

	got, err := DoH{Endpoints: []string{ta.URL}}.Resolve(ctx, "app.example.com")
	if err != nil || !slices.Equal(got, []string{"203.0.113.7", "2001:db8::7"}) {
		t.Fatalf("one resolver: %v %v", got, err)
	}
	// the second resolver has not seen the record yet: not ready
	if _, err := (DoH{Endpoints: []string{ta.URL, tb.URL}}).Resolve(ctx, "app.example.com"); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("lagging resolver: %v", err)
	}
	b.mu.Lock()
	b.recs["app.example.com"] = map[string][]string{"A": {"203.0.113.7"}}
	b.mu.Unlock()
	if got, err := (DoH{Endpoints: []string{ta.URL, tb.URL}}).Resolve(ctx, "app.example.com"); err != nil || len(got) != 2 {
		t.Fatalf("both resolvers: %v %v", got, err)
	}
	// an unreachable resolver is skipped; none reachable is an error
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	if got, err := (DoH{Endpoints: []string{dead.URL, ta.URL}}).Resolve(ctx, "app.example.com"); err != nil || len(got) != 2 {
		t.Fatalf("one dead resolver: %v %v", got, err)
	}
	if _, err := (DoH{Endpoints: []string{dead.URL}}).Resolve(ctx, "app.example.com"); err == nil || errors.Is(err, ErrNoAnswer) {
		t.Fatalf("all dead: %v", err)
	}
	if _, err := (DoH{Endpoints: []string{ta.URL}}).Resolve(ctx, "missing.example.com"); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("nxdomain: %v", err)
	}
}
