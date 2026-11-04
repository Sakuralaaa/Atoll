package caddy

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

var update = flag.Bool("update", false, "rewrite golden files")

var routes = []api.IngressRoute{
	{Hostname: "Web.Example.com", Upstream: "atoll-svc_web:3000", ServiceID: "svc_web"},
	{Hostname: "api.example.com", Upstream: "atoll-svc_api:8080", ServiceID: "svc_api"},
	{Hostname: "api.example.com", Upstream: "atoll-svc_dup:1", ServiceID: "svc_dup"}, // dup ignored
}

var multiRoutes = append(append([]api.IngressRoute{}, routes...),
	api.IngressRoute{Hostname: "shop.example.net", Upstream: "atoll-svc_shop:80", ServiceID: "svc_shop"},
	api.IngressRoute{Hostname: "example.net", Upstream: "atoll-svc_apex:80", ServiceID: "svc_apex"},
	api.IngressRoute{Hostname: "docs.example.org", Upstream: "atoll-svc_docs:80", ServiceID: "svc_docs"},
)

// Without per-domain certificates the config must not change at all (the pre-multi-domain
// goldens above are untouched); an explicitly empty Domains list is the same.
func TestBuildNoDomainsUnchanged(t *testing.T) {
	for _, in := range []Input{
		{Routes: routes, TLSMode: "cloudflare-origin", HaveOriginCert: true},
		{Routes: routes, TLSMode: "acme", ACMEEmail: "ops@example.com"},
	} {
		a, _ := Build(in)
		in.Domains = []Domain{}
		b, _ := Build(in)
		if string(a) != string(b) {
			t.Fatal("empty Domains changed the config")
		}
	}
}

func TestBuildGolden(t *testing.T) {
	cases := map[string]Input{
		"origin":         {Routes: routes, TLSMode: "cloudflare-origin", HaveOriginCert: true},
		"origin_no_cert": {Routes: routes, TLSMode: "cloudflare-origin"},
		"acme":           {Routes: routes, TLSMode: "acme", ACMEEmail: "ops@example.com"},
		"empty":          {TLSMode: "acme"},
		// multi-domain: example.com follows the global origin cert, example.net has its own
		// origin cert, example.org uses ACME.
		"multi": {Routes: multiRoutes, TLSMode: "cloudflare-origin", HaveOriginCert: true, ACMEEmail: "ops@example.com",
			Domains: []Domain{{Name: "example.net", Mode: "origin", HaveCert: true}, {Name: "example.org", Mode: "acme"}}},
		"multi_http": {Routes: multiRoutes, TLSMode: "cloudflare-origin",
			Domains: []Domain{{Name: "example.net", Mode: "origin", HaveCert: true}, {Name: "example.org", Mode: "origin"}}},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(got) {
				t.Fatal("invalid json")
			}
			golden := filepath.Join("testdata", name+".golden.json")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -update)", err)
			}
			if string(got) != string(want) {
				t.Errorf("mismatch for %s\n--- got\n%s\n--- want\n%s", name, got, want)
			}
		})
	}
}

func TestBuildRejectsBadUpstream(t *testing.T) {
	_, err := Build(Input{TLSMode: "acme", Routes: []api.IngressRoute{{Hostname: "a.b", Upstream: "nohostport"}}})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, err := Build(Input{TLSMode: "bogus"}); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestAdminLoadOverUnixSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "admin.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	var gotBody, gotHost string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotHost = string(b), r.Host
		if r.URL.Path != "/load" || r.Method != http.MethodPost {
			http.Error(w, "bad", 400)
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()
	if err := (Admin{SocketPath: sock}).Load(context.Background(), []byte(`{"apps":{}}`)); err != nil {
		t.Fatal(err)
	}
	if gotBody != `{"apps":{}}` || gotHost != "127.0.0.1" {
		t.Fatalf("body=%q host=%q", gotBody, gotHost)
	}
}
