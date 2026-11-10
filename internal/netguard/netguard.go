// Package netguard keeps outbound requests that a user can aim (webhooks, git remotes,
// registries, S3 endpoints) away from addresses that reach the host itself or a cloud
// provider's instance metadata service, which hands out the machine's credentials.
//
// Three levels, strictest first:
//
//   - PublicOnlyDialer / CheckPublicIP: public unicast only. Also refuses private ranges
//     (RFC 1918, fc00::/7) and 100.64.0.0/10 (CGNAT, tailnets). For URLs any panel user
//     (including sub-accounts) may set: their webhooks must not probe the operator's network.
//   - CheckHost / CheckIP / Dialer: refuses loopback, link-local, unspecified, multicast,
//     0.0.0.0/8, IPv4-mapped IPv6 literals, the metadata addresses and localhost names.
//     Private ranges stay allowed (an admin's LAN webhook, a self-hosted git server).
//   - CheckMetadataHost / NoMetadataDialer: refuses the cloud metadata endpoints only.
//     For endpoints an admin configures that legitimately live on a private network or the
//     same host: an in-house S3 (MinIO on 10.x or 127.0.0.1:9000) or a private registry.
//     Blocking private addresses there would break real setups; the metadata service is
//     never a valid S3 or registry endpoint, so that alone is always refused.
//
// The dialers check the address actually dialled, after DNS resolution and on every
// redirect, so a public name that resolves (or rebinds) to a blocked address is refused too.
// CheckHost-style functions validate a host up front for a clear error at save time.
package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is wrapped by every refusal.
var ErrBlocked = errors.New("address not allowed")

// metadataIPs are cloud instance metadata services: AWS/GCP/Azure/most clouds
// (169.254.169.254), Alibaba Cloud (100.100.100.200), AWS IPv6 IMDS (fd00:ec2::254).
var metadataIPs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("100.100.100.200"),
	netip.MustParseAddr("fd00:ec2::254"),
}

// metadataNames resolve to a metadata service (or the host) on common clouds.
var metadataNames = []string{"metadata.google.internal", "metadata.goog", "metadata"}

var (
	thisNet = netip.MustParsePrefix("0.0.0.0/8")
	cgnat   = netip.MustParsePrefix("100.64.0.0/10")
	nat64   = netip.MustParsePrefix("64:ff9b::/96")
)

func blocked(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBlocked, fmt.Sprintf(format, a...))
}

func normName(host string) string {
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func isMetadataIP(a netip.Addr) bool {
	a = a.Unmap()
	for _, m := range metadataIPs {
		if a == m {
			return true
		}
	}
	return false
}

func isMetadataName(h string) bool {
	for _, n := range metadataNames {
		if h == n {
			return true
		}
	}
	return false
}

func isLocalName(h string) bool {
	return h == "localhost" || strings.HasSuffix(h, ".localhost") || h == "localhost.localdomain"
}

// CheckMetadataIP refuses a cloud metadata address (IPv4-mapped forms included).
func CheckMetadataIP(a netip.Addr) error {
	if isMetadataIP(a) {
		return blocked("%s is a cloud metadata address", a)
	}
	return nil
}

// CheckMetadataHost refuses a host (name or literal, optional :port) that is a cloud
// metadata endpoint. It does not resolve DNS; use NoMetadataDialer for the dial itself.
func CheckMetadataHost(host string) error {
	h := normName(host)
	if isMetadataName(h) {
		return blocked("%s is a cloud metadata endpoint", h)
	}
	if a, err := netip.ParseAddr(h); err == nil {
		return CheckMetadataIP(a)
	}
	return nil
}

// CheckIP refuses loopback, link-local, unspecified, multicast, 0.0.0.0/8 and metadata
// addresses. IPv4-mapped IPv6 addresses are judged as the IPv4 address they carry.
func CheckIP(a netip.Addr) error {
	a = a.WithZone("")
	if err := CheckMetadataIP(a); err != nil {
		return err
	}
	u := a.Unmap()
	switch {
	case !u.IsValid():
		return blocked("invalid address")
	case u.IsLoopback():
		return blocked("%s is a loopback address", a)
	case u.IsLinkLocalUnicast(), u.IsLinkLocalMulticast():
		return blocked("%s is a link-local address", a)
	case u.IsUnspecified():
		return blocked("%s is the unspecified address", a)
	case u.IsMulticast(), u.IsInterfaceLocalMulticast():
		return blocked("%s is a multicast address", a)
	case u.Is4() && thisNet.Contains(u):
		return blocked("%s is in 0.0.0.0/8", a)
	case u.Is4() && u == netip.AddrFrom4([4]byte{255, 255, 255, 255}):
		return blocked("%s is the broadcast address", a)
	}
	return nil
}

// CheckPublicIP is CheckIP that also refuses private ranges, 100.64.0.0/10 and NAT64.
func CheckPublicIP(a netip.Addr) error {
	if err := CheckIP(a); err != nil {
		return err
	}
	u := a.Unmap()
	switch {
	case u.IsPrivate():
		return blocked("%s is a private address", a)
	case u.Is4() && cgnat.Contains(u):
		return blocked("%s is in 100.64.0.0/10 (CGNAT / tailnet)", a)
	case u.Is6() && nat64.Contains(u):
		return blocked("%s is a NAT64 address", a)
	case !u.IsGlobalUnicast():
		return blocked("%s is not a public unicast address", a)
	}
	return nil
}

// CheckHost validates a host (name or IP literal, optional :port, optional [brackets])
// at the CheckIP level: localhost names, metadata names and IPv4-mapped IPv6 literals are
// refused outright. A name is not resolved here; the dialers check what it resolves to.
func CheckHost(host string) error {
	h := normName(host)
	if h == "" {
		return blocked("empty host")
	}
	if isLocalName(h) {
		return blocked("%s is a local name", h)
	}
	if isMetadataName(h) {
		return blocked("%s is a cloud metadata endpoint", h)
	}
	if a, err := netip.ParseAddr(h); err == nil {
		if a.Is4In6() {
			return blocked("%s: IPv4-mapped IPv6 literals are not accepted", h)
		}
		return CheckIP(a)
	}
	return nil
}

func control(check func(netip.Addr) error) func(string, string, syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return blocked("unparsable dial address %q", address)
		}
		return check(ap.Addr())
	}
}

func dialer(check func(netip.Addr) error) *net.Dialer {
	return &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: control(check)}
}

// PublicOnlyDialer dials public unicast addresses only (see CheckPublicIP).
func PublicOnlyDialer() *net.Dialer { return dialer(CheckPublicIP) }

// Dialer refuses what CheckIP refuses and allows private ranges.
func Dialer() *net.Dialer { return dialer(CheckIP) }

// NoMetadataDialer refuses cloud metadata addresses only (admin-configured S3, registries).
func NoMetadataDialer() *net.Dialer { return dialer(CheckMetadataIP) }
