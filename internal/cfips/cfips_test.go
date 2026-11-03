package cfips

import (
	"net/netip"
	"testing"
)

func TestContains(t *testing.T) {
	for ip, want := range map[string]bool{
		"162.158.1.2": true, "172.70.0.1": true, "::ffff:104.16.0.1": true, "2606:4700::1": true,
		"203.0.113.9": false, "127.0.0.1": false, "2001:db8::1": false,
	} {
		if got := Contains(netip.MustParseAddr(ip)); got != want {
			t.Errorf("%s: %v want %v", ip, got, want)
		}
	}
}
