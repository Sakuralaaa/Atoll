package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent/caddy"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// originCASigner answers POST /certificates like Cloudflare's Origin CA.
func originCASigner(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hostnames []string `json:"hostnames"`
		CSR       string   `json:"csr"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	block, _ := pem.Decode([]byte(req.CSR))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		w.WriteHeader(400)
		return
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test origin CA"},
		DNSNames: req.Hostnames, NotBefore: time.Now(), NotAfter: time.Now().Add(15 * 365 * 24 * time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, csr.PublicKey, caKey)
	json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]string{
		"id": "origin-" + req.Hostnames[0], "certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}})
}

type mdFixture struct {
	e      *testEnv
	nodeID string
	prj    api.Project
	svc    api.Service
}

func newMDFixture(t *testing.T) *mdFixture {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	f := &mdFixture{e: e, nodeID: nodeID}
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "web", "server_id": nodeID}, &f.prj, 201)
	e.mustDo("POST", "/api/v1/projects/"+f.prj.ID+"/services", map[string]any{
		"name": "app", "kind": "image", "image": "traefik/whoami",
		"ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}},
	}, &f.svc, 201)
	return f
}

func (f *mdFixture) addDomain(body map[string]any) api.Domain {
	var d api.Domain
	f.e.mustDo("POST", "/api/v1/services/"+f.svc.ID+"/domains", body, &d, 201)
	return d
}

func (f *mdFixture) ingress(t *testing.T) api.IngressPayload {
	t.Helper()
	ing, err := f.e.srv.ingressFor(context.Background(), f.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	return ing
}

func rawGET(e *testEnv, path string) string {
	var raw json.RawMessage
	e.mustDo("GET", path, nil, &raw, 200)
	return string(raw)
}

// An install upgraded with one base domain must behave byte for byte as before: the
// ingress payload, the Caddy config built from it, the settings and project JSON and the
// Cloudflare API calls for a new domain are identical before and after migration 0019
// moves base_domain into the root domain list.
func TestSingleRootDomainUpgradeIsByteIdentical(t *testing.T) {
	for _, tc := range []struct{ name, zone, tls string }{
		{"zone-lookup-origin", "", "cloudflare-origin"},
		{"zone-id-acme", "zone-from-settings", "acme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMDFixture(t)
			e, ctx := f.e, context.Background()
			st := e.srv.st
			// The pre-upgrade state: base_domain etc. in settings, no root domain rows.
			for k, v := range map[string]string{"base_domain": "example.com", "cloudflare_token": "tok",
				"cloudflare_zone_id": tc.zone, "tls_mode": tc.tls, "acme_email": "ops@example.com",
				"origin_cert_pem": "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n", "origin_key_pem": "-----BEGIN PRIVATE KEY-----\nx\n-----END PRIVATE KEY-----\n"} {
				if err := st.SetSetting(ctx, k, v); err != nil {
					t.Fatal(err)
				}
			}
			f.addDomain(map[string]any{"hostname": "www.example.com"})
			f.addDomain(map[string]any{"hostname": "other.example.org"})
			type snap struct{ ingress, caddyCfg, settings, project, cfCalls string }
			take := func(host string) snap {
				e.cf.mu.Lock()
				e.cf.reqs = nil
				e.cf.mu.Unlock()
				d := f.addDomain(map[string]any{"hostname": host})
				waitDomainActive(t, e, d.ID)
				e.cf.mu.Lock()
				calls := strings.Join(e.cf.reqs, "\n")
				e.cf.mu.Unlock()
				ing := f.ingress(t)
				b, _ := json.Marshal(ing)
				cfg, err := caddy.Build(caddy.Input{Routes: ing.Routes, TLSMode: ing.TLSMode, ACMEEmail: ing.ACMEEmail, HaveOriginCert: true})
				if err != nil {
					t.Fatal(err)
				}
				// the domain just added differs only by its prefix: normalise it away
				norm := func(s string) string {
					return domID.ReplaceAllString(strings.ReplaceAll(strings.ReplaceAll(s, "before", "X"), "after", "X"), "dom_X")
				}
				return snap{norm(string(b)), norm(string(cfg)), rawGET(e, "/api/v1/settings"), norm(rawGET(e, "/api/v1/projects/"+f.prj.ID)), norm(calls)}
			}
			before := take("before")

			// The upgrade: run migration 0019's data step against these settings.
			mig, err := os.ReadFile("store/migrations/0019_root_domains.sql")
			if err != nil {
				t.Fatal(err)
			}
			body := string(mig)
			ins := body[strings.Index(body, "INSERT INTO root_domains"):]
			ins = ins[:strings.Index(ins, ";")]
			if _, err := st.DB.Exec(ins); err != nil {
				t.Fatal(err)
			}
			var roots []api.RootDomain
			e.mustDo("GET", "/api/v1/root-domains", nil, &roots, 200)
			if len(roots) != 1 || roots[0].Name != "example.com" || !roots[0].Default || roots[0].CertMode != "global" {
				t.Fatalf("migrated roots %+v", roots)
			}
			// drop the "before" domain so both snapshots see the same set of routes
			var doms []api.Domain
			e.mustDo("GET", "/api/v1/services/"+f.svc.ID+"/domains", nil, &doms, 200)
			for _, d := range doms {
				if d.Hostname == "before.example.com" {
					st.DeleteDomain(ctx, d.ID)
				}
			}
			after := take("after")
			if before.ingress != after.ingress {
				t.Errorf("ingress payload differs\n%s\n%s", before.ingress, after.ingress)
			}
			if strings.Contains(after.ingress, `"domains"`) {
				t.Errorf("single global root must not send domains: %s", after.ingress)
			}
			if before.caddyCfg != after.caddyCfg {
				t.Errorf("caddy config differs")
			}
			if before.settings != after.settings {
				t.Errorf("settings differ\n%s\n%s", before.settings, after.settings)
			}
			if before.project != after.project {
				i := 0
				for i < len(before.project) && i < len(after.project) && before.project[i] == after.project[i] {
					i++
				}
				t.Errorf("project differs at %d: %q vs %q", i, before.project[i:], after.project[i:])
			}
			if before.cfCalls != after.cfCalls {
				t.Errorf("cloudflare calls differ\n%s\n---\n%s", before.cfCalls, after.cfCalls)
			}
			// a legacy settings save with the same base domain changes nothing
			e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", TLSMode: tc.tls, CloudflareZoneID: tc.zone, ACMEEmail: "ops@example.com"}, nil, 200)
			e.mustDo("GET", "/api/v1/root-domains", nil, &roots, 200)
			if len(roots) != 1 || roots[0].CertMode != "global" {
				t.Fatalf("roots after settings save %+v", roots)
			}
		})
	}
}

func waitDomainActive(t *testing.T, e *testEnv, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, err := e.srv.st.GetDomain(context.Background(), id)
		if err == nil && d.Status != statusProvisioning {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("domain %s still provisioning", id)
}

func TestRootDomainsAPI(t *testing.T) {
	f := newMDFixture(t)
	e := f.e
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "tok"}, nil, 200)
	var roots []api.RootDomain
	e.mustDo("GET", "/api/v1/root-domains", nil, &roots, 200)
	if len(roots) != 1 || roots[0].Name != "example.com" || roots[0].CertMode != "global" || !roots[0].Default {
		t.Fatalf("first root from settings: %+v", roots)
	}

	// auto mode without Origin CA permission: ACME with the reason recorded
	var net1 api.RootDomain
	e.mustDo("POST", "/api/v1/root-domains", api.RootDomainRequest{Name: "Example.NET."}, &net1, 201)
	if net1.Name != "example.net" || net1.CertMode != "auto" || net1.CertStatus != "acme" || net1.CertError == "" || net1.ZoneID == "" || net1.Default {
		t.Fatalf("auto without permission: %+v", net1)
	}
	if code := e.do("POST", "/api/v1/root-domains", api.RootDomainRequest{Name: "example.net"}, nil); code != 409 {
		t.Fatalf("duplicate: %d", code)
	}
	if code := e.do("POST", "/api/v1/root-domains", api.RootDomainRequest{Name: "localhost"}, nil); code != 400 {
		t.Fatalf("bad name: %d", code)
	}

	// with permission: refresh issues an Origin CA certificate
	e.cf.mu.Lock()
	e.cf.originCA = originCASigner
	e.cf.mu.Unlock()
	net1 = api.RootDomain{}
	e.mustDo("POST", "/api/v1/root-domains/example.net/refresh", nil, &net1, 200)
	if net1.CertStatus != "origin" || net1.CertError != "" || net1.CertExpiresAt == nil {
		t.Fatalf("after refresh: %+v\n%s", net1, strings.Join(e.cf.reqs, "\n"))
	}
	var org api.RootDomain
	e.mustDo("POST", "/api/v1/root-domains", api.RootDomainRequest{Name: "example.org", CertMode: "acme"}, &org, 201)
	if org.CertStatus != "acme" {
		t.Fatalf("acme root: %+v", org)
	}

	// project default root + suffix picker
	e.mustDo("PATCH", "/api/v1/projects/"+f.prj.ID, map[string]any{"root_domain": "example.net"}, &f.prj, 200)
	if f.prj.RootDomain != "example.net" {
		t.Fatalf("project root: %+v", f.prj)
	}
	if code := e.do("PATCH", "/api/v1/projects/"+f.prj.ID, map[string]any{"root_domain": "nope.example"}, nil); code != 400 {
		t.Fatalf("unknown project root: %d", code)
	}
	if d := f.addDomain(map[string]any{"hostname": "shop"}); d.Hostname != "shop.example.net" {
		t.Fatalf("bare host uses project root: %s", d.Hostname)
	}
	if d := f.addDomain(map[string]any{"hostname": "docs", "root_domain": "example.org"}); d.Hostname != "docs.example.org" {
		t.Fatalf("picked suffix: %s", d.Hostname)
	}
	if d := f.addDomain(map[string]any{"hostname": "api.v2", "root_domain": "example.com"}); d.Hostname != "api.v2.example.com" {
		t.Fatalf("multi-label prefix: %s", d.Hostname)
	}
	if code := e.do("POST", "/api/v1/services/"+f.svc.ID+"/domains", map[string]any{"hostname": "x", "root_domain": "unknown.example"}, nil); code != 400 {
		t.Fatalf("unknown root: %d", code)
	}

	// ingress: per-root certificates, the global root stays implicit, and an ACME host
	// opens the firewall to everyone
	ing := f.ingress(t)
	got := map[string]api.IngressDomain{}
	for _, d := range ing.Domains {
		got[d.Name] = d
	}
	if len(got) != 2 || got["example.net"].Mode != "origin" || !strings.Contains(got["example.net"].KeyPEM, "PRIVATE KEY") || got["example.org"].Mode != "acme" {
		t.Fatalf("ingress domains %+v", ing.Domains)
	}
	if ing.CloudflareOnly {
		t.Fatal("a route under an ACME root needs the firewall open")
	}
	// the ingress pushed to the agent must render
	if _, err := caddy.Build(caddy.Input{Routes: ing.Routes, TLSMode: ing.TLSMode, Domains: []caddy.Domain{{Name: "example.net", Mode: "origin", HaveCert: true}, {Name: "example.org", Mode: "acme"}}}); err != nil {
		t.Fatal(err)
	}

	// default switching mirrors base_domain
	e.mustDo("PATCH", "/api/v1/root-domains/example.net", api.RootDomainRequest{Default: ptr(true)}, nil, 200)
	var st api.Settings
	e.mustDo("GET", "/api/v1/settings", nil, &st, 200)
	if st.BaseDomain != "example.net" {
		t.Fatalf("base_domain mirror: %q", st.BaseDomain)
	}
	// delete guards
	if code := e.do("DELETE", "/api/v1/root-domains/example.net", nil, nil); code != 409 {
		t.Fatalf("delete default: %d", code)
	}
	if code := e.do("DELETE", "/api/v1/root-domains/example.org", nil, nil); code != 409 {
		t.Fatalf("delete in use: %d", code)
	}
	var doms []api.Domain
	e.mustDo("GET", "/api/v1/services/"+f.svc.ID+"/domains", nil, &doms, 200)
	for _, d := range doms {
		if strings.HasSuffix(d.Hostname, ".example.org") {
			e.mustDo("DELETE", "/api/v1/domains/"+d.ID, nil, nil, 204)
		}
	}
	e.mustDo("DELETE", "/api/v1/root-domains/example.org", nil, nil, 204)
	// switching a root to global drops it from the agents' list
	e.mustDo("PATCH", "/api/v1/root-domains/example.net", api.RootDomainRequest{CertMode: "global"}, nil, 200)
	if ing := f.ingress(t); len(ing.Domains) != 0 {
		t.Fatalf("all-global roots must send no domains: %+v", ing.Domains)
	}
	// tokens without the settings permission cannot change the list
	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "ci", "permissions": []string{"deploy"}}, &tok, 201)
	if code := e.doWith(http.DefaultClient, tok.Token, "POST", "/api/v1/root-domains", api.RootDomainRequest{Name: "example.info"}, nil); code != 403 {
		t.Fatalf("token add: %d", code)
	}
}

var domID = regexp.MustCompile(`dom_[a-z0-9]+|"hostname":"X\.example\.com","port_name":"web","proxied":true,"status":"active","created_at":"[^"]+"`)

func ptr[T any](v T) *T { return &v }

// PUT /settings base_domain keeps its old meaning on top of the list.
func TestLegacyBaseDomainSettings(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	roots := func() []api.RootDomain {
		var rs []api.RootDomain
		e.mustDo("GET", "/api/v1/root-domains", nil, &rs, 200)
		return rs
	}
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com"}, nil, 200)
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.org"}, nil, 200) // edit = rename
	if rs := roots(); len(rs) != 1 || rs[0].Name != "example.org" || rs[0].CertMode != "global" {
		t.Fatalf("rename: %+v", rs)
	}
	e.mustDo("POST", "/api/v1/root-domains", api.RootDomainRequest{Name: "example.net"}, nil, 201)
	// a stale form naming an unknown root is refused, nothing else is saved
	if code := e.do("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", ACMEEmail: "x@example.com"}, nil); code != 400 {
		t.Fatalf("unknown base with a list: %d", code)
	}
	var st api.Settings
	e.mustDo("GET", "/api/v1/settings", nil, &st, 200)
	if st.ACMEEmail != "" || st.BaseDomain != "example.org" {
		t.Fatalf("refused PUT saved something: %+v", st)
	}
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.net"}, nil, 200) // switch default
	if rs := roots(); rs[0].Name != "example.net" || !rs[0].Default {
		t.Fatalf("default switch: %+v", rs)
	}
	e.mustDo("PUT", "/api/v1/settings", api.Settings{}, nil, 200) // several roots: empty keeps them
	if len(roots()) != 2 {
		t.Fatal("empty base_domain must not drop roots when there are several")
	}
}
