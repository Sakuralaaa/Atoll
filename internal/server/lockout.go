package server

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/cfips"
)

// ---------- brute-force protection ----------
//
// Failed password and second-factor attempts are counted per client IP and per
// account name. After `free` failures a key is locked for base·2^(n-free), capped at
// max. A key is forgotten 24 h after its last failure; a full successful login
// resets both keys.

type backoffPolicy struct {
	free int
	base time.Duration
	max  time.Duration
}

var (
	ipBackoff      = backoffPolicy{free: 5, base: 30 * time.Second, max: time.Hour}
	accountBackoff = backoffPolicy{free: 10, base: 30 * time.Second, max: 15 * time.Minute}
)

const lockoutForget = 24 * time.Hour

type lockEntry struct {
	fails int
	last  time.Time
	until time.Time
}

type lockout struct {
	mu      sync.Mutex
	entries map[string]*lockEntry
	now     func() time.Time
}

func newLockout() *lockout {
	return &lockout{entries: map[string]*lockEntry{}, now: time.Now}
}

// wait returns how long the caller has to wait before the next attempt for any of keys.
func (l *lockout) wait(keys ...string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var w time.Duration
	for _, k := range keys {
		e := l.entries[k]
		if e == nil {
			continue
		}
		if now.Sub(e.last) > lockoutForget {
			delete(l.entries, k)
			continue
		}
		if d := e.until.Sub(now); d > w {
			w = d
		}
	}
	return w
}

// fail records a failure for key and returns the lock it now carries (0 = none).
func (l *lockout) fail(key string, p backoffPolicy) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.entries) > 50000 {
		l.pruneLocked(now)
	}
	e := l.entries[key]
	if e == nil || now.Sub(e.last) > lockoutForget {
		e = &lockEntry{}
		l.entries[key] = e
	}
	e.fails++
	e.last = now
	if e.fails < p.free {
		return 0
	}
	d := p.base
	for i := p.free; i < e.fails && d < p.max; i++ {
		d *= 2
	}
	if d > p.max {
		d = p.max
	}
	e.until = now.Add(d)
	return d
}

func (l *lockout) reset(keys ...string) {
	l.mu.Lock()
	for _, k := range keys {
		delete(l.entries, k)
	}
	l.mu.Unlock()
}

func (l *lockout) pruneLocked(now time.Time) {
	for k, e := range l.entries {
		if now.Sub(e.last) > lockoutForget {
			delete(l.entries, k)
		}
	}
}

func (l *lockout) prune() {
	l.mu.Lock()
	l.pruneLocked(l.now())
	l.mu.Unlock()
}

func ipKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "ip:" + ip
	}
	a = a.Unmap()
	if a.Is6() {
		// one client usually owns a whole /64
		p, _ := a.Prefix(64)
		return "ip:" + p.String()
	}
	return "ip:" + a.String()
}

func accountKey(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) > 64 {
		name = name[:64]
	}
	return "user:" + name
}

// ---------- client IP ----------

// DefaultTrustedProxies are the peers whose X-Forwarded-For is believed: a TLS proxy
// (Caddy) on the same host.
var DefaultTrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

// ParseTrustedProxies parses a comma-separated list of IPs/CIDRs ("" = defaults, "none" = trust nobody).
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultTrustedProxies, nil
	}
	if s == "none" {
		return []netip.Prefix{}, nil
	}
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !strings.Contains(f, "/") {
			a, err := netip.ParseAddr(f)
			if err != nil {
				return nil, err
			}
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(f)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func trusted(list []netip.Prefix, ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// clientIP is the address of the real client, used for every audit entry, session record and
// login backoff key (keep it the only place that reads these headers):
//
//   - the TCP peer, unless the peer is a trusted proxy;
//   - behind a trusted proxy (Caddy on loopback), the right-most X-Forwarded-For entry that is
//     not itself trusted, i.e. whoever connected to that proxy;
//   - when that hop is a Cloudflare edge address, CF-Connecting-IP: with the panel domain proxied
//     by Cloudflare, Caddy (which does not trust Cloudflare) records the edge as the client, an
//     address that changes per request and is not the user's. The header is believed only in this
//     case, so a client that reaches Caddy directly cannot pick its own address with it.
func (s *Server) clientIP(r *http.Request) string {
	tp := s.cfg.TrustedProxies
	if tp == nil {
		tp = DefaultTrustedProxies
	}
	return clientIPFrom(r, tp)
}

func clientIPFrom(r *http.Request, tp []netip.Prefix) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	if !trusted(tp, peer) {
		return peer
	}
	ip := forwardedHop(r, tp, peer)
	if a, err := netip.ParseAddr(ip); err == nil && cfips.Contains(a) {
		if cf, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
			return cf.Unmap().String()
		}
	}
	return ip
}

// forwardedHop walks X-Forwarded-For from the right past trusted proxies.
func forwardedHop(r *http.Request, tp []netip.Prefix, peer string) string {
	hops := []string{}
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, f := range strings.Split(h, ",") {
			if f = strings.TrimSpace(f); f != "" {
				hops = append(hops, f)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		h := hops[i]
		if _, err := netip.ParseAddr(h); err != nil {
			return peer // garbage: do not trust anything further left
		}
		if !trusted(tp, h) {
			return h
		}
	}
	if len(hops) > 0 {
		return hops[0]
	}
	return peer
}
