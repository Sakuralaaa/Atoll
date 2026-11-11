package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent/caddy"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Firewall is implemented by firewall.Manager.
type Firewall interface {
	Apply(ctx context.Context, cloudflareOnly bool) error
}

// PortGuard reports why host ports 80/443 cannot be taken (nil = free to bind).
type PortGuard func(ctx context.Context) error

// Ingress manages the atoll-caddy container and its config. The full JSON config is written to
// <data>/caddy/etc/caddy.json, which Caddy loads on every start, so routing survives reboots
// and server outages with no agent involvement; live changes go through the admin socket.
type Ingress struct {
	rt       Runtime
	paths    Paths
	log      *slog.Logger
	firewall Firewall
	Image    string
	// Guard is consulted before atoll-caddy is created/started. Extra guards (e.g. "Zeabur
	// ingress-controller still running") are added with AddGuard.
	guards []PortGuard
	// LoadConfig pushes config to a running Caddy; replaceable in tests.
	LoadConfig func(ctx context.Context, cfg []byte) error
	// AdminGrace is how long a freshly started (or restarting) atoll-caddy gets to open its
	// admin socket before the agent falls back to restarting it. At boot Docker starts caddy
	// at the same moment the agent reconciles, so the socket is often not there yet.
	AdminGrace time.Duration

	mu sync.Mutex
	// noOriginWarned is set once the "no origin certificate" warning was logged, so an agent
	// without Cloudflare does not repeat it on every ingress sync; a certificate arriving (or
	// a TLS mode that needs none) clears it.
	noOriginWarned bool
}

func NewIngress(rt Runtime, p Paths, fw Firewall, log *slog.Logger) *Ingress {
	if log == nil {
		log = slog.Default()
	}
	in := &Ingress{rt: rt, paths: p, log: log, firewall: fw, Image: "caddy:2", AdminGrace: 20 * time.Second}
	in.guards = []PortGuard{HostPortsFree(80, 443)}
	admin := caddy.Admin{SocketPath: filepath.Join(p.CaddyDir(), "run", "admin.sock")}
	in.LoadConfig = admin.Load
	return in
}

// AddGuard registers another precondition for binding 80/443.
func (in *Ingress) AddGuard(g PortGuard) { in.guards = append(in.guards, g) }

// SetGuards replaces all guards (tests).
func (in *Ingress) SetGuards(g ...PortGuard) { in.guards = g }

// HostPortsFree checks that nothing on the host is listening on the given TCP ports.
func HostPortsFree(ports ...int) PortGuard {
	return func(ctx context.Context) error {
		for _, p := range ports {
			ln, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
			if err != nil {
				return fmt.Errorf("host port %d is busy (another web server or Zeabur ingress?): %w", p, err)
			}
			ln.Close()
		}
		return nil
	}
}

func (in *Ingress) tlsPaths() (cert, key string) {
	return filepath.Join(in.paths.TLSDir(), "origin.crt"), filepath.Join(in.paths.TLSDir(), "origin.key")
}

func (in *Ingress) configFile() string {
	return filepath.Join(in.paths.CaddyDir(), "etc", "caddy.json")
}

// SaveTLS persists the origin cert/key and reloads Caddy if an ingress config exists.
func (in *Ingress) SaveTLS(ctx context.Context, t api.TLSPayload) error {
	if t.CertPEM == "" || t.KeyPEM == "" {
		return errors.New("tls: cert_pem and key_pem are required")
	}
	cert, key := in.tlsPaths()
	if err := writeFileAtomic(cert, []byte(t.CertPEM), 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(key, []byte(t.KeyPEM), 0o600); err != nil {
		return err
	}
	p, err := in.load()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return in.apply(ctx, p)
}

// Apply persists the payload and makes Caddy + firewall match it.
func (in *Ingress) Apply(ctx context.Context, p api.IngressPayload) error {
	if err := in.saveDomainCerts(&p); err != nil {
		return err
	}
	// The key material lives in the TLS dir only; the persisted payload keeps the rest.
	stored := p
	stored.Domains = nil
	for _, d := range p.Domains {
		d.CertPEM, d.KeyPEM = "", ""
		stored.Domains = append(stored.Domains, d)
	}
	b, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(in.paths.IngressFile(), b, 0o600); err != nil {
		return err
	}
	return in.apply(ctx, p)
}

// Restore re-applies the persisted ingress at boot (no-op if none was ever received).
func (in *Ingress) Restore(ctx context.Context) error {
	p, err := in.load()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return in.apply(ctx, p)
}

func (in *Ingress) load() (api.IngressPayload, error) {
	var p api.IngressPayload
	b, err := os.ReadFile(in.paths.IngressFile())
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(b, &p)
}

// noteOriginCert warns once (per missing spell) that routes in cloudflare-origin mode are
// served over HTTP only because no origin certificate has been pushed. Caller holds in.mu.
func (in *Ingress) noteOriginCert(p api.IngressPayload) {
	c, _ := in.tlsPaths()
	missing := (p.TLSMode == "cloudflare-origin" || p.TLSMode == "") && len(p.Routes) > 0 && fileMissing(c)
	if !missing {
		in.noOriginWarned = false
		return
	}
	if in.noOriginWarned {
		in.log.Debug("cloudflare-origin mode, still no origin certificate; serving HTTP only")
		return
	}
	in.noOriginWarned = true
	in.log.Warn("cloudflare-origin mode but no origin certificate yet; serving HTTP only (logged once until it changes)")
}

// Render builds the caddy JSON for a payload given the TLS files on disk.
func (in *Ingress) Render(p api.IngressPayload) ([]byte, error) {
	cert, key := in.tlsPaths()
	_, e1 := os.Stat(cert)
	_, e2 := os.Stat(key)
	ci := caddy.Input{
		Routes: p.Routes, TLSMode: p.TLSMode, ACMEEmail: p.ACMEEmail, HaveOriginCert: e1 == nil && e2 == nil,
	}
	for _, d := range p.Domains {
		c, k := in.domainCertPaths(d.Name)
		ci.Domains = append(ci.Domains, caddy.Domain{Name: d.Name, Mode: d.Mode, HaveCert: !fileMissing(c) && !fileMissing(k)})
	}
	return caddy.Build(ci)
}

// domainCertPaths are the host paths of a root domain's own certificate (mounted into
// atoll-caddy at caddy.DomainCertPaths).
func (in *Ingress) domainCertPaths(name string) (cert, key string) {
	d := filepath.Join(in.paths.TLSDir(), "domains")
	return filepath.Join(d, name+".crt"), filepath.Join(d, name+".key")
}

// validRootName keeps a root domain name safe to use as a file name.
func validRootName(n string) bool {
	if n == "" || len(n) > 253 || strings.HasPrefix(n, ".") || strings.Contains(n, "..") {
		return false
	}
	for _, r := range n {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// saveDomainCerts writes the per-root-domain certificates of p and removes the files of
// roots that no longer carry one.
func (in *Ingress) saveDomainCerts(p *api.IngressPayload) error {
	keep := map[string]bool{}
	out := p.Domains[:0]
	for _, d := range p.Domains {
		if !validRootName(d.Name) {
			in.log.Warn("ingress: ignoring root domain with an unusable name", "name", d.Name)
			continue
		}
		out = append(out, d)
		if d.Mode != "origin" || d.CertPEM == "" || d.KeyPEM == "" {
			continue
		}
		c, k := in.domainCertPaths(d.Name)
		if err := os.MkdirAll(filepath.Dir(c), 0o700); err != nil {
			return err
		}
		if err := writeFileAtomic(c, []byte(d.CertPEM), 0o600); err != nil {
			return err
		}
		if err := writeFileAtomic(k, []byte(d.KeyPEM), 0o600); err != nil {
			return err
		}
		keep[d.Name+".crt"], keep[d.Name+".key"] = true, true
	}
	p.Domains = out
	ents, _ := os.ReadDir(filepath.Join(in.paths.TLSDir(), "domains"))
	for _, e := range ents {
		if !keep[e.Name()] {
			os.Remove(filepath.Join(in.paths.TLSDir(), "domains", e.Name()))
		}
	}
	return nil
}

func (in *Ingress) apply(ctx context.Context, p api.IngressPayload) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	cfg, err := in.Render(p)
	if err != nil {
		return err
	}
	in.noteOriginCert(p)
	if err := writeFileAtomic(in.configFile(), cfg, 0o644); err != nil {
		return err
	}
	if err := in.ensureCaddy(ctx, cfg, len(p.Routes) > 0); err != nil {
		return err
	}
	if in.firewall != nil {
		if err := in.firewall.Apply(ctx, p.CloudflareOnly); err != nil {
			return fmt.Errorf("ingress applied, firewall failed: %w", err)
		}
	}
	return nil
}

func fileMissing(p string) bool { _, err := os.Stat(p); return err != nil }

func (in *Ingress) caddySpec() ContainerSpec {
	d := in.paths.CaddyDir()
	return ContainerSpec{
		Name:  CaddyContainer,
		Image: in.Image,
		Cmd:   []string{"caddy", "run", "--config", caddy.ConfigPath},
		Labels: map[string]string{
			LabelManaged: "true", LabelRole: "ingress", LabelSpecHash: "caddy-v1-" + in.Image,
		},
		Networks: []NetworkAttach{{Name: IngressNetwork}},
		// Every site goes through Caddy: under memory pressure the kernel kills a service first.
		// (Applied when the container is next recreated, e.g. a new Caddy image.)
		OomScoreAdj: -500,
		Mounts: []Mount{
			{Source: filepath.Join(d, "data"), Target: "/data"},
			{Source: filepath.Join(d, "config"), Target: "/config"},
			{Source: filepath.Join(d, "etc"), Target: filepath.Dir(caddy.ConfigPath), ReadOnly: true},
			{Source: filepath.Join(d, "run"), Target: filepath.Dir(caddy.AdminSocket)},
			{Source: in.paths.TLSDir(), Target: filepath.Dir(caddy.TLSCertPath), ReadOnly: true},
		},
		Ports: []PortBinding{
			{ContainerPort: 80, HostPort: 80, Protocol: "tcp"},
			{ContainerPort: 443, HostPort: 443, Protocol: "tcp"},
			{ContainerPort: 443, HostPort: 443, Protocol: "udp"},
		},
		RestartPolicy: "unless-stopped",
	}
}

func (in *Ingress) checkGuards(ctx context.Context) error {
	for _, g := range in.guards {
		if err := g(ctx); err != nil {
			return fmt.Errorf("not starting %s: %w", CaddyContainer, err)
		}
	}
	return nil
}

// loadWithGrace pushes cfg over the admin socket. A caddy that has only just started (or is
// restarting) is given AdminGrace to open the socket instead of being restarted at once:
// restarting it then only interrupts the start Docker already did, e.g. right after a reboot.
func (in *Ingress) loadWithGrace(ctx context.Context, cfg []byte, info ContainerInfo) error {
	err := in.LoadConfig(ctx, cfg)
	if err == nil || in.AdminGrace <= 0 {
		return err
	}
	fresh := info.State == "restarting" || (!info.StartedAt.IsZero() && time.Since(info.StartedAt) < in.AdminGrace)
	if !fresh {
		return err
	}
	deadline := time.Now().Add(in.AdminGrace)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		if err = in.LoadConfig(ctx, cfg); err == nil {
			return nil
		}
	}
	return err
}

// ensureCaddy makes atoll-caddy run with cfg. With no routes a missing or stopped caddy is
// left alone: there is nothing to serve, and 80/443 may belong to someone else (Zeabur's
// ingress after a takeover rollback).
func (in *Ingress) ensureCaddy(ctx context.Context, cfg []byte, haveRoutes bool) error {
	spec := in.caddySpec()
	if !haveRoutes {
		info, err := in.rt.InspectContainer(ctx, CaddyContainer)
		if errors.Is(err, ErrNotFound) || (err == nil && info.State != "running" && info.State != "restarting") {
			return nil
		}
	}
	for _, m := range spec.Mounts {
		if err := os.MkdirAll(m.Source, 0o755); err != nil {
			return err
		}
	}
	info, err := in.rt.InspectContainer(ctx, CaddyContainer)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil && info.Labels[LabelSpecHash] == spec.Labels[LabelSpecHash] {
		if info.State == "running" || info.State == "restarting" {
			if lerr := in.loadWithGrace(ctx, cfg, info); lerr != nil {
				in.log.Warn("caddy admin load failed; restarting container to pick up config file", "err", lerr)
				return in.rt.RestartContainer(ctx, CaddyContainer, 10*time.Second)
			}
			return nil
		}
		// Stopped caddy: it would bind 80/443 on start.
		if err := in.checkGuards(ctx); err != nil {
			return err
		}
		return in.rt.StartContainer(ctx, CaddyContainer)
	}
	if err == nil { // outdated caddy container (image changed): recreate
		if err := in.rt.RemoveContainer(ctx, CaddyContainer); err != nil {
			return err
		}
	}
	if err := in.checkGuards(ctx); err != nil {
		return err
	}
	if err := in.rt.EnsureNetwork(ctx, IngressNetwork); err != nil {
		return err
	}
	if _, ierr := in.rt.ImageInfo(ctx, in.Image); ierr != nil {
		if err := in.rt.PullImage(ctx, in.Image, nil); err != nil {
			return fmt.Errorf("pull %s: %w", in.Image, err)
		}
	}
	if err := in.rt.CreateContainer(ctx, spec); err != nil {
		return err
	}
	if err := in.rt.StartContainer(ctx, CaddyContainer); err != nil {
		// Don't leave a container behind that restarts into a port conflict.
		_ = in.rt.RemoveContainer(context.Background(), CaddyContainer)
		return err
	}
	in.log.Info("started ingress", "container", CaddyContainer)
	return nil
}
