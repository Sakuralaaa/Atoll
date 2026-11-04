package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultCloudflareBase is the Cloudflare v4 API root.
const DefaultCloudflareBase = "https://api.cloudflare.com/client/v4"

// Cloudflare implements Provider with plain net/http.
type Cloudflare struct {
	Token      string
	ZoneID     string // optional; looked up from BaseDomain when empty
	BaseDomain string
	BaseURL    string // defaults to DefaultCloudflareBase
	HTTP       *http.Client
	// Roots are the configured root domains (multi-domain). A hostname under one of them
	// (longest name wins) is managed in that root's zone; any other hostname uses
	// ZoneID/BaseDomain as before.
	Roots []Root

	mu    sync.Mutex
	zones map[string]string // root name -> discovered zone id
}

// Root is a root domain and its Cloudflare zone id ("" = discover by suffix).
type Root struct {
	Name   string
	ZoneID string
}

// RootFor returns the longest root domain in roots that host equals or is under ("" = none).
func RootFor(host string, roots []string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	best := ""
	for _, r := range roots {
		if (host == r || strings.HasSuffix(host, "."+r)) && len(r) > len(best) {
			best = r
		}
	}
	return best
}

// zoneFor returns the zone hostname's records live in.
func (c *Cloudflare) zoneFor(ctx context.Context, hostname string) (string, error) {
	names := make([]string, len(c.Roots))
	for i, r := range c.Roots {
		names[i] = r.Name
	}
	root := RootFor(hostname, names)
	if root == "" {
		return c.zone(ctx)
	}
	for _, r := range c.Roots {
		if r.Name != root {
			continue
		}
		if r.ZoneID != "" {
			return r.ZoneID, nil
		}
		if root == c.BaseDomain {
			return c.zone(ctx) // the pre-multi-domain lookup, unchanged
		}
	}
	c.mu.Lock()
	id := c.zones[root]
	c.mu.Unlock()
	if id != "" {
		return id, nil
	}
	id, _, err := c.FindZone(ctx, root)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.zones == nil {
		c.zones = map[string]string{}
	}
	c.zones[root] = id
	c.mu.Unlock()
	return id, nil
}

// FindZone returns the zone this token can see whose name is the longest suffix of domain
// (domain itself, then each parent): apps.example.com lives in example.com unless the
// account also has a zone apps.example.com.
func (c *Cloudflare) FindZone(ctx context.Context, domain string) (id, name string, err error) {
	if c.Token == "" {
		return "", "", ErrNotConfigured
	}
	domain = strings.ToLower(strings.Trim(domain, "."))
	labels := strings.Split(domain, ".")
	for i := 0; i < len(labels)-1; i++ {
		cand := strings.Join(labels[i:], ".")
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(cand), nil, &zones); err != nil {
			return "", "", err
		}
		for _, z := range zones {
			if strings.EqualFold(z.Name, cand) {
				return z.ID, cand, nil
			}
		}
	}
	return "", "", fmt.Errorf("cloudflare: no zone for %q is visible to this token", domain)
}

type cfEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment,omitempty"`
}

func (c *Cloudflare) do(ctx context.Context, method, path string, body any, out any) error {
	base := c.BaseURL
	if base == "" {
		base = DefaultCloudflareBase
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env cfEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("cloudflare: %s %s: HTTP %d: %s", method, path, resp.StatusCode, truncate(string(raw), 200))
	}
	if !env.Success || resp.StatusCode >= 300 {
		var msgs []string
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%d %s", e.Code, e.Message))
		}
		return fmt.Errorf("cloudflare: %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.Join(msgs, "; "))
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func (c *Cloudflare) zone(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ZoneID != "" {
		return c.ZoneID, nil
	}
	if c.BaseDomain == "" {
		return "", fmt.Errorf("cloudflare: neither zone id nor base domain configured")
	}
	var zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(c.BaseDomain), nil, &zones); err != nil {
		return "", err
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("cloudflare: zone %q not found for this token", c.BaseDomain)
	}
	c.ZoneID = zones[0].ID
	return c.ZoneID, nil
}

func (c *Cloudflare) find(ctx context.Context, zone, hostname string) ([]cfRecord, error) {
	var recs []cfRecord
	q := url.Values{"type": {"A"}, "name": {hostname}}
	err := c.do(ctx, http.MethodGet, "/zones/"+zone+"/dns_records?"+q.Encode(), nil, &recs)
	return recs, err
}

// UpsertA implements Provider.
func (c *Cloudflare) UpsertA(ctx context.Context, hostname, ipv4 string, proxied bool) (string, error) {
	if c.Token == "" {
		return "", ErrNotConfigured
	}
	if ipv4 == "" {
		return "", fmt.Errorf("server has no public IPv4 to point %s at", hostname)
	}
	zone, err := c.zoneFor(ctx, hostname)
	if err != nil {
		return "", err
	}
	recs, err := c.find(ctx, zone, hostname)
	if err != nil {
		return "", err
	}
	body := cfRecord{Type: "A", Name: hostname, Content: ipv4, TTL: 1, Proxied: proxied, Comment: "managed by atoll"}
	var out cfRecord
	if len(recs) > 0 {
		err = c.do(ctx, http.MethodPut, "/zones/"+zone+"/dns_records/"+recs[0].ID, body, &out)
	} else {
		err = c.do(ctx, http.MethodPost, "/zones/"+zone+"/dns_records", body, &out)
	}
	return out.ID, err
}

// Delete implements Provider.
func (c *Cloudflare) Delete(ctx context.Context, hostname, id string) error {
	if c.Token == "" {
		return ErrNotConfigured
	}
	zone, err := c.zoneFor(ctx, hostname)
	if err != nil {
		return err
	}
	ids := []string{}
	if id != "" {
		ids = append(ids, id)
	} else {
		recs, err := c.find(ctx, zone, hostname)
		if err != nil {
			return err
		}
		for _, r := range recs {
			ids = append(ids, r.ID)
		}
	}
	for _, rid := range ids {
		err := c.do(ctx, http.MethodDelete, "/zones/"+zone+"/dns_records/"+rid, nil, nil)
		if err != nil && !strings.Contains(err.Error(), "HTTP 404") {
			return err
		}
	}
	return nil
}

// FindA implements Finder.
func (c *Cloudflare) FindA(ctx context.Context, hostname string) ([]Record, error) {
	if c.Token == "" {
		return nil, ErrNotConfigured
	}
	zone, err := c.zoneFor(ctx, hostname)
	if err != nil {
		return nil, err
	}
	recs, err := c.find(ctx, zone, hostname)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(recs))
	for _, r := range recs {
		out = append(out, Record{ID: r.ID, Content: r.Content, Proxied: r.Proxied})
	}
	return out, nil
}
