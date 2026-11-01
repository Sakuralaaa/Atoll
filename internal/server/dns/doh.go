package dns

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultDoHEndpoints are public DNS-over-HTTPS resolvers speaking the JSON API
// (application/dns-json). Asking two operators keeps one resolver's cache from
// deciding on its own that a fresh record exists.
var DefaultDoHEndpoints = []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/resolve"}

// ErrNoAnswer means every resolver that answered said the name has no A/AAAA record yet.
var ErrNoAnswer = errors.New("dns: no A/AAAA answer yet")

// DoH resolves names through public DNS-over-HTTPS resolvers, so the answer reflects
// what the rest of the internet (and an ACME CA) sees rather than this host's resolver.
type DoH struct {
	Endpoints []string     // defaults to DefaultDoHEndpoints
	HTTP      *http.Client // defaults to a client with a 10s timeout
}

type dohAnswer struct {
	Status int `json:"Status"` // DNS RCODE: 0 NOERROR, 3 NXDOMAIN
	Answer []struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	} `json:"Answer"`
}

// Resolve returns the A and AAAA addresses for host. Every endpoint that answers must
// return at least one address; endpoints that cannot be reached are skipped, and if
// none can be reached the last transport error is returned. A resolver that answers
// with no address yields ErrNoAnswer.
func (d DoH) Resolve(ctx context.Context, host string) ([]string, error) {
	eps := d.Endpoints
	if len(eps) == 0 {
		eps = DefaultDoHEndpoints
	}
	hc := d.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	var (
		out     []string
		seen    = map[string]bool{}
		lastErr error
		answers int
	)
	for _, ep := range eps {
		var got []string
		reached := false
		for _, typ := range []string{"A", "AAAA"} {
			addrs, err := dohQuery(ctx, hc, ep, host, typ)
			if err != nil {
				lastErr = err
				continue
			}
			reached = true
			got = append(got, addrs...)
		}
		if !reached {
			continue
		}
		answers++
		if len(got) == 0 {
			return nil, fmt.Errorf("%w (%s via %s)", ErrNoAnswer, host, ep)
		}
		for _, a := range got {
			if !seen[a] {
				seen[a] = true
				out = append(out, a)
			}
		}
	}
	if answers == 0 {
		if lastErr == nil {
			lastErr = errors.New("dns: no DoH endpoint configured")
		}
		return nil, lastErr
	}
	return out, nil
}

func dohQuery(ctx context.Context, hc *http.Client, endpoint, host, typ string) ([]string, error) {
	q := url.Values{"name": {host}, "type": {typ}}
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+sep+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("doh %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh %s: HTTP %d", endpoint, resp.StatusCode)
	}
	var a dohAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("doh %s: %w", endpoint, err)
	}
	want := 1 // A
	if typ == "AAAA" {
		want = 28
	}
	var out []string
	for _, rr := range a.Answer {
		if rr.Type == want && rr.Data != "" {
			out = append(out, rr.Data)
		}
	}
	return out, nil
}
