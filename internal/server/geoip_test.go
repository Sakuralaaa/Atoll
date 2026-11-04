package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestSplitASOrgAndPublic(t *testing.T) {
	for in, want := range map[string][2]string{
		"AS31898 Oracle Corporation": {"AS31898", "Oracle Corporation"},
		"AS36352":                    {"AS36352", ""},
		"HostPapa":                   {"", "HostPapa"},
	} {
		if a, o := splitASOrg(in); a != want[0] || o != want[1] {
			t.Errorf("splitASOrg(%q) = %q, %q", in, a, o)
		}
	}
	for ip, want := range map[string]bool{"1.1.1.1": true, "10.0.0.1": false, "100.101.1.2": false, "203.0.113.7": false, "": false, "2606:4700::1111": true} {
		if geoPublic(ip) != want {
			t.Errorf("geoPublic(%q) != %v", ip, want)
		}
	}
}

func TestGeoIPRefresh(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	e := newTestEnvWith(t, func(c *Config) {
		c.GeoLookup = func(ctx context.Context, ip string) (*api.NodeGeo, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return &api.NodeGeo{Country: "US", City: "Los Angeles", ASN: "AS31898", Org: "Oracle Corporation"}, nil
		}
	})
	e.setup()
	ctx := context.Background()
	if err := e.srv.st.CreateNode(ctx, api.Server{ID: "srv_geo", Name: "geo", Role: "worker", Status: "offline", PublicIPv4: "1.1.1.1", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	e.srv.refreshGeo(ctx, map[string]time.Time{})
	n, err := e.srv.st.GetNode(ctx, "srv_geo")
	if err != nil || n.Geo == nil || n.Geo.Country != "US" || n.Geo.IP != "1.1.1.1" || n.Geo.Org != "Oracle Corporation" {
		t.Fatalf("geo %+v err %v", n.Geo, err)
	}
	e.srv.refreshGeo(ctx, map[string]time.Time{}) // cached: same IP, fresh
	mu.Lock()
	if calls != 1 {
		t.Fatalf("lookups %d, want 1", calls)
	}
	mu.Unlock()

	var st api.Settings
	e.mustDo("GET", "/api/v1/settings", nil, &st, 200)
	if st.GeoIP != "on" {
		t.Fatalf("geoip default %q", st.GeoIP)
	}
	st.GeoIP = "off"
	e.mustDo("PUT", "/api/v1/settings", st, nil, 200)
	e.srv.st.UpdateNodeGeo(ctx, "srv_geo", nil)
	e.srv.refreshGeo(ctx, map[string]time.Time{})
	if n, _ := e.srv.st.GetNode(ctx, "srv_geo"); n.Geo != nil {
		t.Fatal("looked up while geoip is off")
	}
	if code := e.do("PUT", "/api/v1/settings", map[string]any{"geoip": "maybe"}, nil); code != 400 {
		t.Fatalf("bad geoip value: %d", code)
	}
}
