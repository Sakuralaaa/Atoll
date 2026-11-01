// Package tailnet is the agent side of the Tailscale network provider: it detects this host's
// tailnet address, installs Tailscale from the official repository when asked, brings it up
// with an auth key and verifies that the control server answers over the tailnet.
//
// Defaults never touch the host's DNS or routes (--accept-dns=false --accept-routes=false):
// workers often run other VPN/proxy software (xray, warp, hysteria) that must keep working.
package tailnet

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// CGNAT is the range Tailscale assigns node addresses from.
var CGNAT = netip.MustParsePrefix("100.64.0.0/10")

// IsTailnetIP reports whether s is an IPv4 address in 100.64.0.0/10.
func IsTailnetIP(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && a.Is4() && CGNAT.Contains(a)
}

// Runner executes a command and returns its combined output. Swapped in tests.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs real commands.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// Interfaces lists (interface name, address) pairs. Swapped in tests.
var Interfaces = func() []IfAddr {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []IfAddr
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				out = append(out, IfAddr{Name: i.Name, Addr: p.Addr().String()})
			}
		}
	}
	return out
}

type IfAddr struct{ Name, Addr string }

// LocalIP returns this host's tailnet IPv4 from the interface list: an address in
// 100.64.0.0/10 on a tailscale* interface (any interface as a fallback). "" when absent.
func LocalIP() string {
	var any string
	for _, a := range Interfaces() {
		if !IsTailnetIP(a.Addr) {
			continue
		}
		if strings.HasPrefix(a.Name, "tailscale") {
			return a.Addr
		}
		if any == "" {
			any = a.Addr
		}
	}
	return any
}

// HasIP reports whether ip is currently assigned to a local interface.
func HasIP(ip string) bool {
	for _, a := range Interfaces() {
		if a.Addr == ip {
			return true
		}
	}
	return false
}

// Status is what `tailscale status --json` says about this node.
type Status struct {
	Installed    bool
	BackendState string // Running | NeedsLogin | Stopped | Starting | NoState
	IP           string
	DNSName      string
	Tailnet      string // MagicDNS suffix
}

// State maps the backend state to NodeNetwork.State.
func (s Status) State() string {
	if !s.Installed {
		return "not_installed"
	}
	switch s.BackendState {
	case "Running":
		return "running"
	case "NeedsLogin", "NeedsMachineAuth":
		return "needs_login"
	case "Stopped":
		return "stopped"
	case "Starting":
		return "starting"
	}
	return "unknown"
}

// Client drives the local tailscale CLI.
type Client struct {
	Run    Runner
	Lookup func(string) (string, error) // exec.LookPath
}

func New() *Client { return &Client{Run: ExecRunner, Lookup: exec.LookPath} }

// Status reads `tailscale status --json`; a missing binary is Installed=false, not an error.
func (c *Client) Status(ctx context.Context) Status {
	if _, err := c.Lookup("tailscale"); err != nil {
		return Status{}
	}
	st := Status{Installed: true}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := c.Run(ctx, "tailscale", "status", "--json")
	if len(out) == 0 && err != nil {
		st.BackendState = "NoState"
		st.IP = LocalIP()
		return st
	}
	parsed := ParseStatus(out)
	parsed.Installed = true
	if parsed.IP == "" {
		parsed.IP = LocalIP()
	}
	return parsed
}

// ParseStatus parses `tailscale status --json` output (which may be preceded by warnings).
func ParseStatus(b []byte) Status {
	if i := bytes.IndexByte(b, '{'); i > 0 {
		b = b[i:]
	}
	var raw struct {
		BackendState   string `json:"BackendState"`
		MagicDNSSuffix string `json:"MagicDNSSuffix"`
		CurrentTailnet *struct {
			MagicDNSSuffix string `json:"MagicDNSSuffix"`
		} `json:"CurrentTailnet"`
		Self *struct {
			DNSName      string   `json:"DNSName"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Self"`
	}
	var st Status
	if json.Unmarshal(b, &raw) != nil {
		return st
	}
	st.BackendState = raw.BackendState
	st.Tailnet = raw.MagicDNSSuffix
	if raw.CurrentTailnet != nil && raw.CurrentTailnet.MagicDNSSuffix != "" {
		st.Tailnet = raw.CurrentTailnet.MagicDNSSuffix
	}
	if raw.Self != nil {
		st.DNSName = strings.TrimSuffix(raw.Self.DNSName, ".")
		for _, ip := range raw.Self.TailscaleIPs {
			if IsTailnetIP(ip) {
				st.IP = ip
				break
			}
		}
	}
	return st
}

//go:embed install-tailscale.sh
var installScript string

// InstallScript is the shell snippet that installs Tailscale (official install.sh, falling back
// to the official apt repository on Debian/Ubuntu). install-agent.sh carries the same logic.
func InstallScript() string { return installScript }

// Install installs Tailscale when the binary is missing and enables tailscaled.
func (c *Client) Install(ctx context.Context) (string, error) {
	if _, err := c.Lookup("tailscale"); err == nil {
		out, _ := c.Run(ctx, "systemctl", "enable", "--now", "tailscaled")
		return strings.TrimSpace(string(out)), nil
	}
	f, err := os.CreateTemp("", "atoll-tailscale-*.sh")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(installScript); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	out, err := c.Run(ctx, "sh", f.Name())
	if err != nil {
		return tail(string(out), 2000), fmt.Errorf("install tailscale: %w", err)
	}
	return tail(string(out), 2000), nil
}

var hostRe = regexp.MustCompile(`[^a-z0-9-]+`)

// Hostname is the tailscale --hostname for a node: atoll-<host>, DNS-label safe.
func Hostname(host string) string {
	h := hostRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(host)), "-")
	h = strings.Trim(h, "-")
	if h == "" {
		h = "node"
	}
	if !strings.HasPrefix(h, "atoll-") {
		h = "atoll-" + h
	}
	if len(h) > 63 {
		h = strings.TrimRight(h[:63], "-")
	}
	return h
}

// UpArgs are the `tailscale up` arguments Atoll uses. The auth key is passed as a flag value;
// callers must never log the returned slice.
func UpArgs(authKey, hostname string, reset bool) []string {
	args := []string{"up", "--authkey=" + authKey, "--hostname=" + hostname,
		"--ssh=false", "--accept-dns=false", "--accept-routes=false"}
	if reset {
		args = append(args, "--reset")
	}
	return args
}

// Up brings tailscale up with authKey unless it already runs. It waits for a tailnet IP.
func (c *Client) Up(ctx context.Context, authKey, hostname string) (Status, error) {
	st := c.Status(ctx)
	if !st.Installed {
		return st, errors.New("tailscale is not installed")
	}
	if st.BackendState == "Running" && st.IP != "" {
		return st, nil // already on a tailnet: never re-login over an existing setup
	}
	if authKey == "" {
		return st, errors.New("tailscale is not logged in and no auth key is configured (Settings → Private network)")
	}
	uctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	out, err := c.Run(uctx, "tailscale", UpArgs(authKey, hostname, false)...)
	if err != nil && strings.Contains(string(out), "requires mentioning all non-default flags") {
		out, err = c.Run(uctx, "tailscale", UpArgs(authKey, hostname, true)...)
	}
	if err != nil {
		return c.Status(ctx), fmt.Errorf("tailscale up failed: %s", Redact(tail(strings.TrimSpace(string(out)), 500), authKey))
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		st = c.Status(ctx)
		if st.BackendState == "Running" && st.IP != "" {
			return st, nil
		}
		if time.Now().After(deadline) {
			return st, fmt.Errorf("tailscale did not reach Running (state %s)", st.BackendState)
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// Redact removes secret from s.
func Redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// IsTailnetURL reports whether u points at a tailnet address (100.64/10 or *.ts.net).
func IsTailnetURL(u string) bool {
	host := u
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return IsTailnetIP(host) || strings.HasSuffix(strings.ToLower(host), ".ts.net")
}

// PingResult is GET /agent/v1/ping.
type PingResult struct {
	OK     bool   `json:"ok"`
	NodeID string `json:"node_id,omitempty"`
}

// Probe checks that baseURL answers /agent/v1/ping and accepts nodeToken as this node. It
// retries until ctx ends (new tailnet peers can take a few seconds to become reachable).
func Probe(ctx context.Context, hc *http.Client, baseURL, nodeToken, nodeID string) error {
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	u := strings.TrimRight(baseURL, "/") + "/agent/v1/ping"
	var last error
	for {
		last = probeOnce(ctx, hc, u, nodeToken, nodeID)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s not reachable: %w", baseURL, last)
		case <-time.After(2 * time.Second):
		}
	}
}

func probeOnce(ctx context.Context, hc *http.Client, u, token, nodeID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("ping answered %s", resp.Status)
	}
	var pr PingResult
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return fmt.Errorf("ping: not an Atoll server: %w", err)
	}
	if !pr.OK {
		return errors.New("ping: server not ok")
	}
	if nodeID != "" && pr.NodeID != nodeID {
		return fmt.Errorf("ping: server did not accept this node's token (got node %q)", pr.NodeID)
	}
	return nil
}
