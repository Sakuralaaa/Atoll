package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// PATCH /domains/{id} flips the Cloudflare proxy flag: the DNS record is rewritten, the
// domain is stored grey, and under an Origin CA root it now reports (and gets) ACME.
func TestPatchDomainProxied(t *testing.T) {
	f := newMDFixture(t)
	e := f.e
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "tok"}, nil, 200)
	e.cf.mu.Lock()
	e.cf.originCA = originCASigner
	e.cf.mu.Unlock()
	e.mustDo("POST", "/api/v1/root-domains", api.RootDomainRequest{Name: "example.net"}, nil, 201)
	d := f.addDomain(map[string]any{"hostname": "shop.example.net"})
	waitDomainActive(t, e, d.ID)

	var list []api.Domain
	e.mustDo("GET", "/api/v1/domains", nil, &list, 200)
	if len(list) != 1 || !list[0].Proxied || list[0].Cert != "origin" {
		t.Fatalf("before: %+v", list)
	}
	if code := e.do("PATCH", "/api/v1/domains/"+d.ID, map[string]any{}, nil); code != 400 {
		t.Fatalf("empty patch: %d", code)
	}
	e.cf.mu.Lock()
	e.cf.reqs = nil
	e.cf.mu.Unlock()
	var out api.Domain
	e.mustDo("PATCH", "/api/v1/domains/"+d.ID, map[string]any{"proxied": false}, &out, 200)
	if out.Proxied || out.Cert != "acme" {
		t.Fatalf("after: %+v", out)
	}
	e.cf.mu.Lock()
	calls := strings.Join(e.cf.reqs, "\n")
	e.cf.mu.Unlock()
	if !strings.Contains(calls, "PUT /zones/") {
		t.Fatalf("no DNS update: %s", calls)
	}
	waitDomainActive(t, e, d.ID)
	ing := f.ingress(t)
	if len(ing.Routes) != 1 || !ing.Routes[0].Direct {
		t.Fatalf("ingress: %+v", ing.Routes)
	}
	e.mustDo("PATCH", "/api/v1/domains/"+d.ID, map[string]any{"proxied": true}, &out, 200)
	if !out.Proxied || out.Cert != "origin" {
		t.Fatalf("back: %+v", out)
	}
	waitDomainActive(t, e, d.ID)
}

// GET /cloudflare/token-check reports the SSL permission only when a root issues Origin
// CA certificates, and "missing" when Cloudflare refuses the probe with 1016.
func TestCloudflareTokenCheck(t *testing.T) {
	f := newMDFixture(t)
	e := f.e
	var c api.CloudflareTokenCheck
	e.mustDo("GET", "/api/v1/cloudflare/token-check", nil, &c, 200)
	if c.Configured || c.DNS != "missing" || c.SSL != "" {
		t.Fatalf("no token: %+v", c)
	}
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "tok"}, nil, 200)
	e.mustDo("GET", "/api/v1/cloudflare/token-check", nil, &c, 200)
	if !c.Configured || c.DNS != "ok" || c.SSL != "" {
		t.Fatalf("global root: %+v", c)
	}
	e.cf.mu.Lock()
	e.cf.originCA = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 1016, "message": "User does not have permission"}}})
	}
	e.cf.mu.Unlock()
	e.mustDo("POST", "/api/v1/root-domains", api.RootDomainRequest{Name: "example.net"}, nil, 201)
	e.mustDo("GET", "/api/v1/cloudflare/token-check", nil, &c, 200)
	if c.SSL != "missing" || len(c.SSLRoots) != 1 || !strings.Contains(c.SSLError, "1016") {
		t.Fatalf("auto root without permission: %+v", c)
	}
	e.cf.mu.Lock()
	e.cf.originCA = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 1010, "message": "Failed to validate CSR"}}})
	}
	e.cf.mu.Unlock()
	e.mustDo("GET", "/api/v1/cloudflare/token-check", nil, &c, 200)
	if c.SSL != "missing" {
		t.Fatalf("cached answer expected: %+v", c)
	}
	e.mustDo("GET", "/api/v1/cloudflare/token-check?refresh=1", nil, &c, 200)
	if c.SSL != "ok" {
		t.Fatalf("with permission: %+v", c)
	}
}
