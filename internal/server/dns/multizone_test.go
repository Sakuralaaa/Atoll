package dns

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
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// zonesCF serves several zones, records per zone and the Origin CA endpoint.
type zonesCF struct {
	mu       sync.Mutex
	zones    map[string]string // name -> id
	created  map[string][]string
	calls    []string
	noOrigin bool
}

func (f *zonesCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.RequestURI())
	ok := func(res any) { json.NewEncoder(w).Encode(map[string]any{"success": true, "result": res}) }
	p := r.URL.Path
	switch {
	case p == "/zones":
		n := r.URL.Query().Get("name")
		if id, found := f.zones[n]; found {
			ok([]map[string]string{{"id": id, "name": n}})
		} else {
			ok([]any{})
		}
	case strings.HasSuffix(p, "/dns_records") && r.Method == "GET":
		ok([]any{})
	case strings.HasSuffix(p, "/dns_records") && r.Method == "POST":
		var rec cfRecord
		json.NewDecoder(r.Body).Decode(&rec)
		zone := strings.Split(p, "/")[2]
		f.created[zone] = append(f.created[zone], rec.Name)
		rec.ID = "rec-" + rec.Name
		ok(rec)
	case p == "/certificates" && r.Method == "POST":
		if f.noOrigin {
			w.WriteHeader(403)
			json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 10000, "message": "Authentication error"}}})
			return
		}
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
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "test origin CA"},
			DNSNames: req.Hostnames, NotBefore: time.Now(), NotAfter: time.Now().Add(24 * time.Hour)}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, csr.PublicKey, caKey)
		ok(map[string]string{"id": "cert-1", "certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))})
	default:
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{"success": false})
	}
}

func newZonesCF() *zonesCF {
	return &zonesCF{zones: map[string]string{"example.com": "zc", "example.net": "zn", "apps.example.net": "zapps"}, created: map[string][]string{}}
}

func TestRootFor(t *testing.T) {
	roots := []string{"example.com", "example.net", "apps.example.net"}
	for host, want := range map[string]string{
		"example.com": "example.com", "a.example.com": "example.com", "x.apps.example.net": "apps.example.net",
		"b.example.net": "example.net", "badexample.com": "", "other.org": "",
	} {
		if got := RootFor(host, roots); got != want {
			t.Errorf("RootFor(%s) = %q want %q", host, got, want)
		}
	}
}

func TestFindZoneLongestSuffix(t *testing.T) {
	f := newZonesCF()
	srv := httptest.NewServer(f)
	defer srv.Close()
	cf := &Cloudflare{Token: "tok", BaseURL: srv.URL}
	for dom, want := range map[string]string{"example.net": "zn", "apps.example.net": "zapps", "deep.apps.example.net": "zapps", "sub.example.com": "zc"} {
		id, _, err := cf.FindZone(context.Background(), dom)
		if err != nil || id != want {
			t.Errorf("FindZone(%s) = %q %v want %q", dom, id, err, want)
		}
	}
	if _, _, err := cf.FindZone(context.Background(), "example.org"); err == nil {
		t.Error("want error for unknown zone")
	}
}

func TestUpsertPicksZoneByRoot(t *testing.T) {
	f := newZonesCF()
	srv := httptest.NewServer(f)
	defer srv.Close()
	cf := &Cloudflare{Token: "tok", BaseDomain: "example.com", ZoneID: "zc", BaseURL: srv.URL,
		Roots: []Root{{Name: "example.com", ZoneID: "zc"}, {Name: "example.net"}}}
	ctx := context.Background()
	for _, h := range []string{"a.example.com", "b.example.net", "c.other.org"} {
		if _, err := cf.UpsertA(ctx, h, "203.0.113.5", true); err != nil {
			t.Fatal(h, err)
		}
	}
	if strings.Join(f.created["zc"], ",") != "a.example.com,c.other.org" || strings.Join(f.created["zn"], ",") != "b.example.net" {
		t.Fatalf("created %v", f.created)
	}
}

func TestIssueOriginCert(t *testing.T) {
	f := newZonesCF()
	srv := httptest.NewServer(f)
	defer srv.Close()
	cf := &Cloudflare{Token: "tok", BaseURL: srv.URL}
	c, err := cf.IssueOriginCert(context.Background(), "example.net")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "cert-1" || !strings.Contains(c.CertPEM, "BEGIN CERTIFICATE") || !strings.Contains(c.KeyPEM, "PRIVATE KEY") || c.NotAfter.IsZero() {
		t.Fatalf("%+v", c)
	}
	f.noOrigin = true
	if _, err := cf.IssueOriginCert(context.Background(), "example.net"); err == nil || !strings.Contains(err.Error(), "Authentication error") {
		t.Fatalf("want auth error, got %v", err)
	}
}
