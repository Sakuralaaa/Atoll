// Package firewall restricts inbound 80/443 to Cloudflare's IP ranges with a dedicated
// nftables table ("inet atoll_fw").
//
// Safety: the table only ever drops packets whose destination port is 80 or 443 and whose
// destination is a local address. Its chain policy is accept, so SSH and every other port
// are untouched, and removing the table restores the previous behaviour exactly.
// The chain hooks prerouting before Docker's DNAT so published container ports are covered.
package firewall

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/cfips"
)

const (
	Table   = "atoll_fw"
	cfIPv4  = "https://www.cloudflare.com/ips-v4"
	cfIPv6  = "https://www.cloudflare.com/ips-v6"
	refresh = 24 * time.Hour
)

// Fallback ranges used only when the live list was never fetched (no cached copy).
var fallbackV4 = cfips.V4
var fallbackV6 = cfips.V6

// Private/internal sources that are always allowed (docker bridges, LAN, Tailscale CGNAT).
var internalV4 = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "127.0.0.0/8"}
var internalV6 = []string{"fc00::/7", "fe80::/10", "::1/128"}

// Ranges is a Cloudflare IP list snapshot.
type Ranges struct {
	V4        []string  `json:"v4"`
	V6        []string  `json:"v6"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Manager applies and maintains the rules. The zero value is not usable; use New.
type Manager struct {
	Enabled bool
	Dir     string // cache dir for cloudflare-ips.json
	HTTP    *http.Client
	Log     *slog.Logger
	// Run executes `nft -f -` with the given script; replaceable in tests.
	Run func(ctx context.Context, script string) error
	// URLs are overridable in tests.
	V4URL, V6URL string

	mu     sync.Mutex
	active bool // cloudflare-only currently requested
}

func New(enabled bool, dir string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		Enabled: enabled, Dir: dir, Log: log,
		HTTP:  &http.Client{Timeout: 20 * time.Second},
		Run:   runNft,
		V4URL: cfIPv4, V6URL: cfIPv6,
	}
}

func runNft(ctx context.Context, script string) error {
	path, err := exec.LookPath("nft")
	if err != nil {
		return fmt.Errorf("nft not installed: %w", err)
	}
	cmd := exec.CommandContext(ctx, path, "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) cachePath() string { return filepath.Join(m.Dir, "cloudflare-ips.json") }

// Apply turns Cloudflare-only mode on or off. Disabled managers never touch nftables.
func (m *Manager) Apply(ctx context.Context, cloudflareOnly bool) error {
	if !m.Enabled {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = cloudflareOnly
	if !cloudflareOnly {
		return m.Run(ctx, RemoveScript())
	}
	r := m.ranges(ctx, false)
	script, err := Script(r)
	if err != nil {
		return err
	}
	return m.Run(ctx, script)
}

// Loop refreshes the Cloudflare list daily and re-applies while active.
func (m *Manager) Loop(ctx context.Context) {
	if !m.Enabled {
		return
	}
	t := time.NewTicker(refresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			if m.active {
				r := m.ranges(ctx, true)
				if s, err := Script(r); err == nil {
					if err := m.Run(ctx, s); err != nil {
						m.Log.Error("firewall refresh", "err", err)
					}
				}
			}
			m.mu.Unlock()
		}
	}
}

// ranges returns the cached list (if fresh and !force), else fetches; on fetch failure it
// keeps the last good copy, and only then falls back to the built-in list.
func (m *Manager) ranges(ctx context.Context, force bool) Ranges {
	cached, cerr := m.loadCache()
	if cerr == nil && !force && time.Since(cached.FetchedAt) < refresh {
		return cached
	}
	fresh, err := m.fetch(ctx)
	if err == nil {
		if b, err := json.Marshal(fresh); err == nil {
			_ = os.MkdirAll(m.Dir, 0o700)
			tmp := m.cachePath() + ".tmp"
			if os.WriteFile(tmp, b, 0o600) == nil {
				_ = os.Rename(tmp, m.cachePath())
			}
		}
		return fresh
	}
	m.Log.Warn("fetch cloudflare ranges failed, using last good copy", "err", err)
	if cerr == nil {
		return cached
	}
	return Ranges{V4: fallbackV4, V6: fallbackV6}
}

func (m *Manager) loadCache() (Ranges, error) {
	var r Ranges
	b, err := os.ReadFile(m.cachePath())
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if len(r.V4) == 0 {
		return r, errors.New("empty cache")
	}
	return r, nil
}

func (m *Manager) fetch(ctx context.Context) (Ranges, error) {
	v4, err := m.fetchList(ctx, m.V4URL, true)
	if err != nil {
		return Ranges{}, err
	}
	v6, err := m.fetchList(ctx, m.V6URL, false)
	if err != nil {
		return Ranges{}, err
	}
	return Ranges{V4: v4, V6: v6, FetchedAt: time.Now().UTC()}, nil
}

func (m *Manager) fetchList(ctx context.Context, url string, v4 bool) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return ParseList(io.LimitReader(resp.Body, 1<<20), v4)
}

// ParseList parses one CIDR per line; every line must be a valid prefix of the right family.
func ParseList(r io.Reader, v4 bool) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p, err := netip.ParsePrefix(line)
		if err != nil {
			return nil, fmt.Errorf("bad prefix %q", line)
		}
		if p.Addr().Is4() != v4 {
			return nil, fmt.Errorf("prefix %q has wrong family", line)
		}
		out = append(out, p.Masked().String())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("empty list")
	}
	return out, nil
}

// Script returns an atomic nft script that (re)creates the atoll table.
func Script(r Ranges) (string, error) {
	v4, err := validated(r.V4, true)
	if err != nil {
		return "", err
	}
	v6, err := validated(r.V6, false)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	// "add table" + "delete table" makes the replace idempotent and atomic within one transaction.
	fmt.Fprintf(&b, "add table inet %s\ndelete table inet %s\n", Table, Table)
	fmt.Fprintf(&b, "table inet %s {\n", Table)
	writeSet(&b, "cf4", "ipv4_addr", v4)
	writeSet(&b, "cf6", "ipv6_addr", v6)
	writeSet(&b, "int4", "ipv4_addr", internalV4)
	writeSet(&b, "int6", "ipv6_addr", internalV6)
	b.WriteString(`	chain prerouting {
		type filter hook prerouting priority -150; policy accept;
		fib daddr type != local return
		meta l4proto != { tcp, udp } return
		th dport != { 80, 443 } return
		iifname "lo" return
		ip saddr @cf4 return
		ip6 saddr @cf6 return
		ip saddr @int4 return
		ip6 saddr @int6 return
		counter drop
	}
}
`)
	return b.String(), nil
}

// RemoveScript deletes the atoll table if present.
func RemoveScript() string {
	return fmt.Sprintf("add table inet %s\ndelete table inet %s\n", Table, Table)
}

func validated(list []string, v4 bool) ([]string, error) {
	if len(list) == 0 {
		return nil, errors.New("empty cloudflare range list")
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		p, err := netip.ParsePrefix(s)
		if err != nil || p.Addr().Is4() != v4 {
			return nil, fmt.Errorf("invalid prefix %q", s)
		}
		out = append(out, p.Masked().String())
	}
	return out, nil
}

func writeSet(b *strings.Builder, name, typ string, elems []string) {
	fmt.Fprintf(b, "\tset %s {\n\t\ttype %s; flags interval; auto-merge;\n\t\telements = { %s }\n\t}\n",
		name, typ, strings.Join(elems, ", "))
}
