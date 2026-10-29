package firewall

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestScriptOnlyTouchesWebPorts(t *testing.T) {
	s, err := Script(Ranges{V4: fallbackV4, V6: fallbackV6})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "policy accept") {
		t.Fatal("chain policy must be accept")
	}
	if strings.Count(s, "drop") != 1 {
		t.Fatal("exactly one drop rule expected")
	}
	// Every port mentioned must be 80 or 443 — never 22 or anything else.
	for _, m := range regexp.MustCompile(`dport[^\n]*`).FindAllString(s, -1) {
		if m != "dport != { 80, 443 } return" {
			t.Fatalf("unexpected port rule %q", m)
		}
	}
	// Drop must come after the port filter.
	if strings.Index(s, "th dport") > strings.Index(s, "drop") {
		t.Fatal("port guard must precede drop")
	}
	if !strings.Contains(s, "173.245.48.0/20") || !strings.Contains(s, "2606:4700::/32") {
		t.Fatal("ranges missing")
	}
}

func TestScriptRejectsBadInput(t *testing.T) {
	if _, err := Script(Ranges{V4: []string{"1.2.3.4/33"}, V6: fallbackV6}); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Script(Ranges{V4: nil, V6: fallbackV6}); err == nil {
		t.Fatal("empty list must be rejected (would drop all web traffic)")
	}
}

func TestApplyFetchCacheAndFallback(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "down", 500)
			return
		}
		if strings.HasSuffix(r.URL.Path, "v4") {
			w.Write([]byte("198.51.100.0/24\n"))
		} else {
			w.Write([]byte("2001:db8::/32\n"))
		}
	}))
	defer srv.Close()
	var scripts []string
	m := New(true, t.TempDir(), nil)
	m.V4URL, m.V6URL = srv.URL+"/v4", srv.URL+"/v6"
	m.Run = func(_ context.Context, s string) error { scripts = append(scripts, s); return nil }

	if err := m.Apply(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scripts[0], "198.51.100.0/24") {
		t.Fatal("fetched range not used")
	}
	// Source down: last good cached copy must be used (forced refresh).
	fail = true
	r := m.ranges(context.Background(), true)
	if len(r.V4) != 1 || r.V4[0] != "198.51.100.0/24" {
		t.Fatalf("expected cached copy, got %v", r.V4)
	}
	if err := m.Apply(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if scripts[len(scripts)-1] != RemoveScript() {
		t.Fatal("disable must remove the table")
	}
	// No cache + source down: built-in list.
	m2 := New(true, t.TempDir(), nil)
	m2.V4URL, m2.V6URL = srv.URL+"/v4", srv.URL+"/v6"
	if r := m2.ranges(context.Background(), false); len(r.V4) != len(fallbackV4) {
		t.Fatal("expected fallback")
	}
}

func TestDisabledNeverRuns(t *testing.T) {
	m := New(false, t.TempDir(), nil)
	m.Run = func(context.Context, string) error { t.Fatal("must not run"); return nil }
	if err := m.Apply(context.Background(), true); err != nil {
		t.Fatal(err)
	}
}
