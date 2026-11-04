package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The probes read the answer to a request Cloudflare must reject: an authentication
// error means the permission is missing, a validation error that it is there.
func TestPermissionProbes(t *testing.T) {
	var dnsCode, certCode, certStatus int
	created := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fail := func(status, code int) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": code, "message": "x"}}})
		}
		switch {
		case r.URL.Path == "/zones":
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]string{{"id": "z1", "name": "example.com"}}})
		case r.URL.Path == "/zones/z1/dns_records" && r.Method == "POST":
			if dnsCode == 10000 {
				fail(403, 10000)
				return
			}
			fail(400, dnsCode)
		case r.URL.Path == "/certificates":
			fail(certStatus, certCode)
		default:
			created++
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	c := &Cloudflare{Token: "tok", BaseDomain: "example.com", BaseURL: ts.URL}
	ctx := context.Background()
	for _, tc := range []struct {
		dnsCode, certStatus, certCode int
		dns, ssl                      string
	}{
		{9005, 400, 1010, PermOK, PermOK},
		{10000, 403, 1016, PermMissing, PermMissing},
		{9005, 400, 1016, PermOK, PermMissing},
		{9005, 404, 7003, PermOK, PermUnknown},
	} {
		dnsCode, certStatus, certCode = tc.dnsCode, tc.certStatus, tc.certCode
		if got, err := c.ProbeDNSEdit(ctx, "example.com"); got != tc.dns {
			t.Errorf("dns %d: %s (%v), want %s", tc.dnsCode, got, err, tc.dns)
		}
		if got, err := c.ProbeOriginCA(ctx, "example.com"); got != tc.ssl {
			t.Errorf("ssl %d/%d: %s (%v), want %s", tc.certStatus, tc.certCode, got, err, tc.ssl)
		}
	}
	if created != 0 {
		t.Fatalf("unexpected requests: %d", created)
	}
	if got, _ := (&Cloudflare{}).ProbeOriginCA(ctx, "example.com"); got != PermMissing {
		t.Fatalf("no token: %s", got)
	}
}
