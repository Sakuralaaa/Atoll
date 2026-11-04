package server

// GeoIP of each server's public address, for the flag and provider icon in the panel
// (the same idea as Komari's probe: ask ipinfo.io, keep the ISO country code). Lookups
// run on the control server, at most one every couple of seconds, and are cached in the
// node row (nodes.geo) until the address changes or the record is a month old.
// settings.geoip = "off" stops them.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

const (
	geoRefreshEvery = 6 * time.Hour
	geoMaxAge       = 30 * 24 * time.Hour
	geoRetryAfter   = 6 * time.Hour // a failed lookup is not retried sooner
)

var geoHTTP = &http.Client{Timeout: 8 * time.Second}

// geoEnabled reports whether GeoIP lookups are on (default on).
func (s *Server) geoEnabled(ctx context.Context) bool {
	v, _ := s.st.GetSetting(ctx, "geoip")
	return v != "off"
}

// geoLookup asks the configured lookup (tests) or ipinfo.io, then ip-api.com.
func (s *Server) geoLookup(ctx context.Context, ip string) (*api.NodeGeo, error) {
	if s.cfg.GeoLookup != nil {
		return s.cfg.GeoLookup(ctx, ip)
	}
	g, err := lookupIPInfo(ctx, ip)
	if err != nil {
		var err2 error
		if g, err2 = lookupIPAPI(ctx, ip); err2 != nil {
			return nil, fmt.Errorf("ipinfo: %v; ip-api: %v", err, err2)
		}
	}
	return g, nil
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := geoHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func lookupIPInfo(ctx context.Context, ip string) (*api.NodeGeo, error) {
	var r struct{ Country, City, Org string }
	if err := getJSON(ctx, "https://ipinfo.io/"+ip+"/json", &r); err != nil {
		return nil, err
	}
	if r.Country == "" {
		return nil, fmt.Errorf("no country for %s", ip)
	}
	asn, org := splitASOrg(r.Org)
	return &api.NodeGeo{IP: ip, Country: strings.ToUpper(r.Country), City: r.City, ASN: asn, Org: org, Source: "ipinfo.io"}, nil
}

func lookupIPAPI(ctx context.Context, ip string) (*api.NodeGeo, error) {
	var r struct {
		Status, Message, CountryCode, City, ISP, AS string
	}
	if err := getJSON(ctx, "http://ip-api.com/json/"+ip+"?fields=status,message,countryCode,city,isp,as", &r); err != nil {
		return nil, err
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("%s", r.Message)
	}
	asn, org := splitASOrg(r.AS)
	if org == "" {
		org = r.ISP
	}
	return &api.NodeGeo{IP: ip, Country: strings.ToUpper(r.CountryCode), City: r.City, ASN: asn, Org: org, Source: "ip-api.com"}, nil
}

// splitASOrg splits "AS31898 Oracle Corporation" into its ASN and owner.
func splitASOrg(v string) (asn, org string) {
	v = strings.TrimSpace(v)
	if a, b, ok := strings.Cut(v, " "); ok && strings.HasPrefix(a, "AS") {
		return a, strings.TrimSpace(b)
	}
	if strings.HasPrefix(v, "AS") && !strings.Contains(v, " ") {
		return v, ""
	}
	return "", v
}

// geoPublic reports whether ip is worth looking up: a global unicast address outside
// private, CGNAT/tailnet and documentation ranges (tests use 203.0.113.0/24).
func geoPublic(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range []string{"100.64.0.0/10", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		if netip.MustParsePrefix(p).Contains(a) {
			return false
		}
	}
	return true
}

func nodeGeoIP(n api.Server) string {
	if geoPublic(n.PublicIPv4) {
		return n.PublicIPv4
	}
	if geoPublic(n.PublicIPv6) {
		return n.PublicIPv6
	}
	return ""
}

// kickGeo asks the GeoIP loop to look at the nodes now (a node connected or changed address).
func (s *Server) kickGeo() {
	select {
	case s.geoKick <- struct{}{}:
	default:
	}
}

func (s *Server) startGeoIP() {
	s.goBG(func(ctx context.Context) {
		t := time.NewTicker(geoRefreshEvery)
		defer t.Stop()
		failed := map[string]time.Time{}
		for {
			s.refreshGeo(ctx, failed)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-s.geoKick:
			}
		}
	})
}

// refreshGeo looks up nodes whose record is missing, stale or for another address.
func (s *Server) refreshGeo(ctx context.Context, failed map[string]time.Time) {
	if !s.geoEnabled(ctx) {
		return
	}
	nodes, err := s.st.ListNodes(ctx)
	if err != nil {
		return
	}
	first := true
	for _, n := range nodes {
		ip := nodeGeoIP(n)
		if ip == "" || (n.Geo != nil && n.Geo.IP == ip && time.Since(n.Geo.At) < geoMaxAge) {
			continue
		}
		if at, ok := failed[ip]; ok && time.Since(at) < geoRetryAfter {
			continue
		}
		if !first { // stay well inside the free tiers' rate limits
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
		first = false
		g, err := s.geoLookup(ctx, ip)
		if err != nil {
			failed[ip] = time.Now()
			s.log.Info("geoip lookup failed", "node", n.ID, "ip", ip, "err", err)
			continue
		}
		g.IP, g.At = ip, time.Now().UTC()
		if err := s.st.UpdateNodeGeo(ctx, n.ID, g); err == nil {
			s.publishServer(ctx, n.ID)
		}
	}
}
