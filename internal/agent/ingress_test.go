package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
	"github.com/Sakuralaaa/Atoll/internal/agent/caddy"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

type fwRecorder struct {
	mu    sync.Mutex
	calls []bool
}

func (f *fwRecorder) Apply(_ context.Context, cfOnly bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cfOnly)
	return nil
}

type loads struct {
	mu   sync.Mutex
	cfgs [][]byte
	err  error
}

func (l *loads) load(_ context.Context, b []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cfgs = append(l.cfgs, append([]byte(nil), b...))
	return l.err
}

func newIngress(t *testing.T) (*agent.Ingress, *agenttest.Fake, *loads, *fwRecorder, agent.Paths) {
	t.Helper()
	f := agenttest.New()
	p := agent.Paths{Root: t.TempDir()}
	fw := &fwRecorder{}
	in := agent.NewIngress(f, p, fw, nil)
	in.SetGuards() // the real host-port guard would probe :80 on the test machine
	l := &loads{}
	in.LoadConfig = l.load
	return in, f, l, fw, p
}

func payload(hosts ...string) api.IngressPayload {
	p := api.IngressPayload{TLSMode: "acme", ACMEEmail: "ops@example.com", CloudflareOnly: true}
	for _, h := range hosts {
		p.Routes = append(p.Routes, api.IngressRoute{Hostname: h, Upstream: "atoll-svc_web:3000", ServiceID: "svc_web"})
	}
	return p
}

func TestIngressCaddyContainerSpec(t *testing.T) {
	in, f, l, fw, p := newIngress(t)
	ctx := context.Background()
	if err := in.Apply(ctx, payload("app.example.com")); err != nil {
		t.Fatal(err)
	}
	c, ok := f.Get(agent.CaddyContainer)
	if !ok || c.Info.State != "running" {
		t.Fatalf("caddy not running: %+v", c)
	}
	s := c.Spec
	if s.Image != "caddy:2" || s.RestartPolicy != "unless-stopped" || !reflect.DeepEqual(s.Cmd, []string{"caddy", "run", "--config", caddy.ConfigPath}) {
		t.Fatalf("spec %+v", s)
	}
	if s.Labels[agent.LabelManaged] != "true" || s.Labels[agent.LabelRole] != "ingress" || s.Labels[agent.LabelSpecHash] == "" {
		t.Fatalf("labels %v", s.Labels)
	}
	if !reflect.DeepEqual(s.Networks, []agent.NetworkAttach{{Name: agent.IngressNetwork}}) {
		t.Fatalf("networks %+v", s.Networks)
	}
	wantPorts := []agent.PortBinding{{ContainerPort: 80, HostPort: 80, Protocol: "tcp"}, {ContainerPort: 443, HostPort: 443, Protocol: "tcp"}, {ContainerPort: 443, HostPort: 443, Protocol: "udp"}}
	if !reflect.DeepEqual(s.Ports, wantPorts) {
		t.Fatalf("ports %+v", s.Ports)
	}
	targets := map[string]agent.Mount{}
	for _, m := range s.Mounts {
		targets[m.Target] = m
		if _, err := os.Stat(m.Source); err != nil {
			t.Fatalf("mount source %s not created", m.Source)
		}
	}
	if m := targets[filepath.Dir(caddy.ConfigPath)]; !m.ReadOnly || m.Source != filepath.Join(p.CaddyDir(), "etc") {
		t.Fatalf("config mount %+v", m)
	}
	if m := targets[filepath.Dir(caddy.TLSCertPath)]; !m.ReadOnly || m.Source != p.TLSDir() {
		t.Fatalf("tls mount %+v", m)
	}
	for _, d := range []string{"/data", "/config", filepath.Dir(caddy.AdminSocket)} {
		if _, ok := targets[d]; !ok {
			t.Fatalf("missing mount %s", d)
		}
	}
	if !slices.Contains(f.CallLog(), "pull caddy:2 auth=") {
		t.Fatal("caddy image not pulled")
	}
	// Config on disk is what Caddy loads at boot; ingress payload is persisted for Restore.
	b, err := os.ReadFile(filepath.Join(p.CaddyDir(), "etc", "caddy.json"))
	if err != nil || !json.Valid(b) || !strings.Contains(string(b), "app.example.com") {
		t.Fatalf("caddy.json: %v %s", err, b)
	}
	if _, err := os.Stat(p.IngressFile()); err != nil {
		t.Fatal("ingress payload not persisted")
	}
	if len(l.cfgs) != 0 {
		t.Fatal("fresh container reads the file; no admin load expected")
	}
	if !reflect.DeepEqual(fw.calls, []bool{true}) {
		t.Fatalf("firewall %v", fw.calls)
	}
}

func TestIngressAdminReload(t *testing.T) {
	in, f, l, _, _ := newIngress(t)
	ctx := context.Background()
	if err := in.Apply(ctx, payload("a.example.com")); err != nil {
		t.Fatal(err)
	}
	before := len(f.CallLog())
	if err := in.Apply(ctx, payload("a.example.com", "b.example.com")); err != nil {
		t.Fatal(err)
	}
	if len(l.cfgs) != 1 || !strings.Contains(string(l.cfgs[0]), "b.example.com") {
		t.Fatalf("admin load not called with new config: %d", len(l.cfgs))
	}
	for _, c := range f.CallLog()[before:] {
		if strings.HasPrefix(c, "create") || strings.HasPrefix(c, "remove") || strings.HasPrefix(c, "restart") {
			t.Fatalf("running caddy must be reloaded, not touched: %s", c)
		}
	}
	// Admin socket broken: fall back to a container restart (picks up the file).
	l.err = errors.New("dial unix: no such file")
	if err := in.Apply(ctx, payload("c.example.com")); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.CallLog(), "restart "+agent.CaddyContainer) {
		t.Fatal("expected restart fallback")
	}
	// Image change recreates the container.
	in.Image = "caddy:2.9"
	if err := in.Apply(ctx, payload("c.example.com")); err != nil {
		t.Fatal(err)
	}
	if c, _ := f.Get(agent.CaddyContainer); c.Spec.Image != "caddy:2.9" || c.Info.State != "running" {
		t.Fatalf("not recreated: %+v", c.Spec.Image)
	}
}

func TestIngressGuardBlocks(t *testing.T) {
	in, f, _, fw, p := newIngress(t)
	ctx := context.Background()
	in.SetGuards(func(context.Context) error { return errors.New("Zeabur/k3s ingress is still running") })
	err := in.Apply(ctx, payload("a.example.com"))
	if err == nil || !strings.Contains(err.Error(), "not starting atoll-caddy") {
		t.Fatalf("err %v", err)
	}
	if _, ok := f.Get(agent.CaddyContainer); ok {
		t.Fatal("caddy created despite guard")
	}
	if len(fw.calls) != 0 {
		t.Fatal("firewall applied despite guard")
	}
	// Payload was persisted, so Restore at boot tries again and is blocked again.
	if _, err := os.Stat(p.IngressFile()); err != nil {
		t.Fatal(err)
	}
	if err := in.Restore(ctx); err == nil {
		t.Fatal("restore must be blocked too")
	}

	// A stopped caddy (e.g. after a Zeabur rollback) must not be restarted while blocked.
	in.SetGuards()
	if err := in.Apply(ctx, payload("a.example.com")); err != nil {
		t.Fatal(err)
	}
	f.SetState(agent.CaddyContainer, "exited")
	in.SetGuards(func(context.Context) error { return errors.New("busy") })
	if err := in.Restore(ctx); err == nil {
		t.Fatal("stopped caddy restarted despite guard")
	}
	if c, _ := f.Get(agent.CaddyContainer); c.Info.State != "exited" {
		t.Fatalf("state %s", c.Info.State)
	}
	in.SetGuards()
	if err := in.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if c, _ := f.Get(agent.CaddyContainer); c.Info.State != "running" {
		t.Fatalf("state %s", c.Info.State)
	}
}

func TestHostPortsFreeDetectsListener(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Skip("cannot listen:", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	err = agent.HostPortsFree(port)(context.Background())
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("busy port not detected: %v", err)
	}
	ln.Close()
	if err := agent.HostPortsFree(port)(context.Background()); err != nil {
		t.Fatalf("free port reported busy: %v", err)
	}
}

func TestIngressDefaultGuardIsHostPorts(t *testing.T) {
	// The default Ingress must refuse to start caddy when :80 is held by someone else.
	f := agenttest.New()
	in := agent.NewIngress(f, agent.Paths{Root: t.TempDir()}, nil, nil)
	in.LoadConfig = func(context.Context, []byte) error { return nil }
	ln, err := net.Listen("tcp", ":80")
	if err != nil {
		// Either something already holds :80 or we lack privileges; in both cases the
		// guard must still refuse.
		if err := in.Apply(context.Background(), payload("a.example.com")); err == nil {
			t.Fatal("guard let caddy start while :80 unavailable")
		}
		return
	}
	defer ln.Close()
	if err := in.Apply(context.Background(), payload("a.example.com")); err == nil {
		t.Fatal("guard let caddy start while :80 busy")
	}
}

func TestSaveTLSReloads(t *testing.T) {
	in, _, l, _, p := newIngress(t)
	ctx := context.Background()
	if err := in.SaveTLS(ctx, api.TLSPayload{}); err == nil {
		t.Fatal("empty cert accepted")
	}
	// No ingress yet: just stored.
	if err := in.SaveTLS(ctx, api.TLSPayload{CertPEM: "CERT", KeyPEM: "KEY"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(p.TLSDir(), "origin.key"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key perm %v %v", fi, err)
	}
	pl := payload("a.example.com")
	pl.TLSMode = "cloudflare-origin"
	if err := in.Apply(ctx, pl); err != nil {
		t.Fatal(err)
	}
	if err := in.SaveTLS(ctx, api.TLSPayload{CertPEM: "CERT2", KeyPEM: "KEY2"}); err != nil {
		t.Fatal(err)
	}
	if len(l.cfgs) != 1 || !strings.Contains(string(l.cfgs[0]), caddy.TLSCertPath) {
		t.Fatalf("tls reload %d", len(l.cfgs))
	}
}

// With no routes there is nothing to serve: a missing or stopped caddy is left alone, so
// withdrawing routes after a Zeabur rollback succeeds while Zeabur owns 80/443 again.
func TestIngressNoRoutesLeavesCaddyAlone(t *testing.T) {
	in, f, _, _, _ := newIngress(t)
	ctx := context.Background()
	in.SetGuards(func(context.Context) error { return errors.New("Zeabur/k3s ingress is still running") })
	if err := in.Apply(ctx, payload()); err != nil {
		t.Fatalf("empty payload: %v", err)
	}
	if _, ok := f.Get(agent.CaddyContainer); ok {
		t.Fatal("caddy created without routes")
	}
	in.SetGuards()
	if err := in.Apply(ctx, payload("a.example.com")); err != nil {
		t.Fatal(err)
	}
	f.SetState(agent.CaddyContainer, "exited")
	in.SetGuards(func(context.Context) error { return errors.New("busy") })
	if err := in.Apply(ctx, payload()); err != nil {
		t.Fatalf("withdraw routes with stopped caddy: %v", err)
	}
	if c, _ := f.Get(agent.CaddyContainer); c.Info.State != "exited" {
		t.Fatalf("stopped caddy restarted: %s", c.Info.State)
	}
}

// flakyLoads fails the first n admin loads (socket not there yet), then succeeds.
type flakyLoads struct {
	mu    sync.Mutex
	n     int
	calls int
}

func (l *flakyLoads) load(context.Context, []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.calls <= l.n {
		return errors.New("dial unix admin.sock: connect: no such file or directory")
	}
	return nil
}

// After a reboot Docker starts atoll-caddy while the agent reconciles; the admin socket
// appears a moment later. The agent must wait for it instead of restarting caddy.
func TestIngressFreshCaddyGetsAdminGrace(t *testing.T) {
	in, f, _, _, _ := newIngress(t)
	ctx := context.Background()
	if err := in.Apply(ctx, payload("a.example.com")); err != nil {
		t.Fatal(err)
	}
	in.AdminGrace = 3 * time.Second
	f.Containers[agent.CaddyContainer].Info.StartedAt = time.Now() // just started by Docker
	fl := &flakyLoads{n: 2}
	in.LoadConfig = fl.load
	before := len(f.CallLog())
	if err := in.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if fl.calls != 3 {
		t.Fatalf("admin load calls = %d, want 3 (two failures then success)", fl.calls)
	}
	for _, c := range f.CallLog()[before:] {
		if strings.HasPrefix(c, "restart") {
			t.Fatalf("fresh caddy restarted instead of waiting for its admin socket: %s", c)
		}
	}

	// Restarting caddy whose socket never comes: fall back to a restart after the grace.
	in.AdminGrace = 600 * time.Millisecond
	f.SetState(agent.CaddyContainer, "restarting")
	in.LoadConfig = (&flakyLoads{n: 1 << 30}).load
	start := time.Now()
	if err := in.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.CallLog()[before:], "restart "+agent.CaddyContainer) {
		t.Fatal("expected restart fallback once the grace ran out")
	}
	if d := time.Since(start); d < 500*time.Millisecond {
		t.Fatalf("restarted after %v, before the grace ran out", d)
	}
}
