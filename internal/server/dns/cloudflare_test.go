package dns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fakeCF struct {
	mu      sync.Mutex
	records map[string]cfRecord
	n       int
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 9109, "message": "Invalid access token"}}})
		return
	}
	ok := func(res any) { json.NewEncoder(w).Encode(map[string]any{"success": true, "result": res}) }
	p := r.URL.Path
	switch {
	case p == "/zones" && r.Method == "GET":
		if r.URL.Query().Get("name") == "example.com" {
			ok([]map[string]string{{"id": "z1", "name": "example.com"}})
		} else {
			ok([]any{})
		}
	case p == "/zones/z1/dns_records" && r.Method == "GET":
		var out []cfRecord
		for _, rec := range f.records {
			if rec.Name == r.URL.Query().Get("name") {
				out = append(out, rec)
			}
		}
		ok(out)
	case p == "/zones/z1/dns_records" && r.Method == "POST":
		var rec cfRecord
		json.NewDecoder(r.Body).Decode(&rec)
		f.n++
		rec.ID = "r" + string(rune('0'+f.n))
		f.records[rec.ID] = rec
		ok(rec)
	case strings.HasPrefix(p, "/zones/z1/dns_records/"):
		id := strings.TrimPrefix(p, "/zones/z1/dns_records/")
		if _, exists := f.records[id]; !exists {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 81044, "message": "not found"}}})
			return
		}
		if r.Method == "DELETE" {
			delete(f.records, id)
			ok(map[string]string{"id": id})
			return
		}
		var rec cfRecord
		json.NewDecoder(r.Body).Decode(&rec)
		rec.ID = id
		f.records[id] = rec
		ok(rec)
	default:
		w.WriteHeader(404)
	}
}

func TestCloudflareUpsertAndDelete(t *testing.T) {
	f := &fakeCF{records: map[string]cfRecord{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	cf := &Cloudflare{Token: "tok", BaseDomain: "example.com", BaseURL: srv.URL}
	ctx := context.Background()

	id, err := cf.UpsertA(ctx, "app.example.com", "1.2.3.4", true)
	if err != nil || id == "" {
		t.Fatalf("create: %q %v", id, err)
	}
	id2, err := cf.UpsertA(ctx, "app.example.com", "5.6.7.8", true)
	if err != nil || id2 != id {
		t.Fatalf("update: %q %v", id2, err)
	}
	if len(f.records) != 1 || f.records[id].Content != "5.6.7.8" || !f.records[id].Proxied {
		t.Fatalf("records %+v", f.records)
	}
	if err := cf.Delete(ctx, "app.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if len(f.records) != 0 {
		t.Fatal("not deleted")
	}
	if err := cf.Delete(ctx, "app.example.com", "gone"); err != nil {
		t.Fatalf("missing record should be ignored: %v", err)
	}
}

func TestCloudflareErrors(t *testing.T) {
	f := &fakeCF{records: map[string]cfRecord{}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	bad := &Cloudflare{Token: "wrong", ZoneID: "z1", BaseURL: srv.URL}
	if _, err := bad.UpsertA(context.Background(), "a.example.com", "1.1.1.1", true); err == nil || !strings.Contains(err.Error(), "Invalid access token") {
		t.Fatalf("want auth error, got %v", err)
	}
	none := &Cloudflare{}
	if _, err := none.UpsertA(context.Background(), "a", "1.1.1.1", true); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
	nozone := &Cloudflare{Token: "tok", BaseDomain: "other.org", BaseURL: srv.URL}
	if _, err := nozone.UpsertA(context.Background(), "a.other.org", "1.1.1.1", true); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want zone error, got %v", err)
	}
}
