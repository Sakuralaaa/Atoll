package dns

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// Permission probe results.
const (
	PermOK      = "ok"
	PermMissing = "missing"
	PermUnknown = "unknown"
)

// Cloudflare cannot tell an API token which permissions it holds (that needs "API Tokens:
// Read"), so the probes below send a write request with a body Cloudflare must reject:
// a token without the permission is refused with an authentication error before the
// body is looked at, a token with it gets a validation error. Nothing is ever created.

// ProbeDNSEdit reports whether the token may edit DNS records in the zone of host.
func (c *Cloudflare) ProbeDNSEdit(ctx context.Context, host string) (string, error) {
	if c.Token == "" {
		return PermMissing, ErrNotConfigured
	}
	zone, err := c.zoneFor(ctx, host)
	if err != nil {
		return classifyProbe(err)
	}
	// "content" is not an IPv4 address: Cloudflare answers 9005 (invalid content).
	body := map[string]any{"type": "A", "name": "_atoll-permission-probe." + strings.Trim(host, "."), "content": "not-an-ip", "ttl": 1}
	err = c.do(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", body, nil)
	if err == nil {
		// accepted after all (should not happen): the permission is there; clean up
		if recs, ferr := c.find(ctx, zone, "_atoll-permission-probe."+strings.Trim(host, ".")); ferr == nil {
			for _, r := range recs {
				c.do(ctx, http.MethodDelete, "/zones/"+zone+"/dns_records/"+r.ID, nil, nil)
			}
		}
		return PermOK, nil
	}
	return classifyProbe(err)
}

// ProbeOriginCA reports whether the token may issue Origin CA certificates for root
// ("SSL and Certificates: Edit"). The request carries no valid CSR.
func (c *Cloudflare) ProbeOriginCA(ctx context.Context, root string) (string, error) {
	if c.Token == "" {
		return PermMissing, ErrNotConfigured
	}
	root = strings.ToLower(strings.Trim(root, "."))
	body := map[string]any{"hostnames": []string{root}, "requested_validity": 7, "request_type": "origin-ecc", "csr": "not-a-csr"}
	var out struct {
		ID string `json:"id"`
	}
	err := c.do(ctx, http.MethodPost, "/certificates", body, &out)
	if err == nil {
		if out.ID != "" {
			c.RevokeOriginCert(ctx, out.ID)
		}
		return PermOK, nil
	}
	return classifyProbe(err)
}

// IsPermissionError reports whether a Cloudflare API error means the token lacks a
// permission (HTTP 401/403, code 10000 authentication error, 1016 / 9109 not allowed).
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	for _, k := range []string{"HTTP 401", "HTTP 403", ": 10000 ", ": 1016 ", ": 9109 ", "; 10000 ", "; 1016 ", "; 9109 ", "uthentication", "no zone for", "not found for this token"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// classifyProbe turns the error of a probe request into a result: a permission error is
// "missing", any other API answer (HTTP 4xx validation error) means the request got past
// the permission check, and a network failure leaves it unknown.
func classifyProbe(err error) (string, error) {
	switch {
	case errors.Is(err, ErrNotConfigured):
		return PermMissing, err
	case IsPermissionError(err):
		return PermMissing, err
	case strings.Contains(err.Error(), "HTTP 404"):
		return PermUnknown, err // not an answer about permissions
	case strings.Contains(err.Error(), "HTTP 4"):
		return PermOK, nil
	}
	return PermUnknown, err
}
