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

	"github.com/atoll-sh/atoll/internal/api"
)

var update = flag.Bool("update", false, "rewrite golden files")

var routes = []api.IngressRoute{
	{Hostname: "Web.Example.com", Upstream: "atoll-svc_web:3000", ServiceID: "svc_web"},
	{Hostname: "api.example.com", Upstream: "atoll-svc_api:8080", ServiceID: "svc_api"},
	{Hostname: "api.example.com", Upstream: "atoll-svc_dup:1", ServiceID: "svc_dup"}, // dup ignored
}

func TestBuildGolden(t *testing.T) {
	cases := map[string]Input{
		"origin":         {Routes: routes, TLSMode: "cloudflare-origin", HaveOriginCert: true},
		"origin_no_cert": {Routes: routes, TLSMode: "cloudflare-origin"},
		"acme":           {Routes: routes, TLSMode: "acme", ACMEEmail: "ops@example.com"},
		"empty":          {TLSMode: "acme"},
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
