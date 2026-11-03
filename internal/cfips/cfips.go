// Package cfips holds Cloudflare's published edge ranges (https://www.cloudflare.com/ips/).
// The agent firewall refreshes them live and uses these only as a fallback; the control server
// uses them to recognise a request that reached its proxy from Cloudflare.
package cfips

import "net/netip"

// V4 and V6 are a snapshot of https://www.cloudflare.com/ips-v4 and ips-v6.
var V4 = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
	"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
}
var V6 = []string{
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
	"2a06:98c0::/29", "2c0f:f248::/32",
}

var prefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range append(append([]string{}, V4...), V6...) {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// Contains reports whether ip is a Cloudflare edge address.
func Contains(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range prefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}
