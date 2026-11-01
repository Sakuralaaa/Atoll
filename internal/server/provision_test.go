package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// provLog is one ordered event log shared by the fake DNS provider, the fake public
// resolver, the fake HTTPS probe and the fake agent's ingress handler.
type provLog struct {
	mu        sync.Mutex
	events    []string
	visible   map[string]string // host -> address public resolvers return (absent = NXDOMAIN)
	certOK    map[string]bool
	deleteErr error
}

func (l *provLog) add(ev string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
}

func (l *provLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

func (l *provLog) index(ev string) int { return slices.Index(l.snapshot(), ev) }

func (l *provLog) set(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f()
}

// fakeProvider is a dns.Provider that logs calls.
type fakeProvider struct{ l *provLog }

func (p fakeProvider) UpsertA(_ context.Context, host, ip string, proxied bool) (string, error) {
	p.l.add("dns:upsert " + host)
	return "rec-" + host, nil
}

func (p fakeProvider) Delete(_ context.Context, host, id string) error {
	p.l.mu.Lock()
	err := p.l.deleteErr
	p.l.mu.Unlock()
	if err != nil {
		return err
	}
	p.l.add("dns:delete " + host)
	return nil
}

// provEnv is a test env with a managed DNS provider, a fake agent and a service with an http port.
type provEnv struct {
	*testEnv
	l     *provLog
	agent *fakeAgent
	svc   api.Service
}

func newProvEnv(t *testing.T) *provEnv {
	e := newTestEnv(t)
	l := &provLog{visible: map[string]string{}, certOK: map[string]bool{}}
	e.srv.cfg.DNSProvider = fakeProvider{l}
	e.srv.cfg.ResolvePublic = func(ctx context.Context, host string) ([]string, error) {
		l.mu.Lock()
		ip, ok := l.visible[host]
		l.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("NXDOMAIN %s", host)
		}
		l.add("resolved " + host)
		return []string{ip}, nil
	}
	e.srv.cfg.ProbeHTTPS = func(ctx context.Context, host string) error {
		l.mu.Lock()
		ok := l.certOK[host]
		l.mu.Unlock()
		if !ok {
			return errors.New("HTTP 525 from Cloudflare")
		}
		l.add("https " + host)
		return nil
	}
	e.srv.cfg.ProvisionPoll = 10 * time.Millisecond
	e.srv.cfg.DNSWaitTimeout = 5 * time.Second
	e.srv.cfg.CertWaitTimeout = 5 * time.Second
	e.setup()
	e.srv.st.SetSetting(context.Background(), "tls_mode", "acme")
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	agent.mu.Lock()
	agent.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type == api.MsgIngress {
			var p api.IngressPayload
			json.Unmarshal(env.Payload, &p)
			hosts := []string{}
			for _, r := range p.Routes {
				hosts = append(hosts, r.Hostname)
			}
			l.add("ingress [" + strings.Join(hosts, " ") + "]")
		}
		return api.ResultPayload{}, false
	}
	agent.mu.Unlock()
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "web", "server_id": nodeID}, &p, 201)
	pe := &provEnv{testEnv: e, l: l, agent: agent}
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "app", "kind": "image", "image": "traefik/whoami",
		"ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}},
	}, &pe.svc, 201)
	return pe
}

func (e *provEnv) addDomain(host string, proxied bool) api.Domain {
	var d api.Domain
	e.mustDo("POST", "/api/v1/services/"+e.svc.ID+"/domains", map[string]any{"hostname": host, "proxied": proxied}, &d, 201)
	return d
}

func (e *provEnv) domain(host string) api.Domain {
	var ds []api.Domain
	e.mustDo("GET", "/api/v1/domains", nil, &ds, 200)
	for _, d := range ds {
		if d.Hostname == host {
			return d
		}
	}
	return api.Domain{}
}

func (e *provEnv) waitStatus(host, status string) api.Domain {
	e.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		d := e.domain(host)
		if d.Status == status {
			return d
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("%s: status %q, want %q; events %q", host, d.Status, status, e.l.snapshot())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// firstIngressWith returns the index of the first route table pushed that contains host.
func (e *provEnv) firstIngressWith(host string) int {
	for i, ev := range e.l.snapshot() {
		if strings.HasPrefix(ev, "ingress [") && slices.Contains(strings.Fields(strings.Trim(ev[len("ingress "):], "[]")), host) {
			return i
		}
	}
	return -1
}

func (e *provEnv) waitIngressWith(host string) int {
	e.t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		if i := e.firstIngressWith(host); i >= 0 {
			return i
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("no route for %s pushed; events %q", host, e.l.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A new managed domain: DNS record first, route pushed only once the name resolves
// publicly, "provisioning" until HTTPS works, then "active". Removal: route, then record.
func TestDomainProvisioningOrder(t *testing.T) {
	e := newProvEnv(t)
	const host = "app.example.com"
	if d := e.addDomain(host, true); d.Status != "provisioning" || d.Error != "" {
		t.Fatalf("add: %+v", d)
	}
	if e.l.index("dns:upsert "+host) < 0 {
		t.Fatalf("no DNS record created: %q", e.l.snapshot())
	}
	// other route pushes (another domain, a redeploy) must not leak the held hostname
	e.l.set(func() { e.l.visible["other.example.com"] = "198.51.100.20"; e.l.certOK["other.example.com"] = true })
	e.addDomain("other.example.com", true)
	e.waitIngressWith("other.example.com")
	time.Sleep(150 * time.Millisecond)
	if i := e.firstIngressWith(host); i >= 0 {
		t.Fatalf("route pushed before DNS resolved: %q", e.l.snapshot())
	}
	if d := e.domain(host); d.Status != "provisioning" {
		t.Fatalf("before DNS: %+v", d)
	}

	e.l.set(func() { e.l.visible[host] = "198.51.100.20" }) // proxied: any edge address will do
	ing := e.waitIngressWith(host)
	if up, res := e.l.index("dns:upsert "+host), e.l.index("resolved "+host); !(up < res && res < ing) {
		t.Fatalf("order upsert=%d resolved=%d ingress=%d: %q", up, res, ing, e.l.snapshot())
	}
	time.Sleep(100 * time.Millisecond)
	if d := e.domain(host); d.Status != "provisioning" {
		t.Fatalf("route pushed but certificate not served yet: %+v", d)
	}
	e.l.set(func() { e.l.certOK[host] = true })
	if d := e.waitStatus(host, "active"); d.Error != "" {
		t.Fatalf("active: %+v", d)
	}

	// removal: the route leaves Caddy before the DNS record is deleted
	d := e.domain(host)
	e.mustDo("DELETE", "/api/v1/domains/"+d.ID, nil, nil, 204)
	ev := e.l.snapshot()
	del := slices.Index(ev, "dns:delete "+host)
	if del < 0 {
		t.Fatalf("record not deleted: %q", ev)
	}
	last := ""
	for _, x := range ev[:del] {
		if strings.HasPrefix(x, "ingress [") {
			last = x
		}
	}
	if strings.Contains(last, host) || !strings.Contains(last, "other.example.com") {
		t.Fatalf("route table before DNS delete %q; events %q", last, ev)
	}
	if got := e.domain(host); got.ID != "" {
		t.Fatalf("still listed: %+v", got)
	}
}

// DNS that never shows up does not block the route forever, and an HTTPS probe that never
// passes still ends in "active", with a note.
func TestDomainProvisioningTimeouts(t *testing.T) {
	e := newProvEnv(t)
	e.srv.cfg.DNSWaitTimeout = 200 * time.Millisecond
	e.srv.cfg.CertWaitTimeout = 200 * time.Millisecond
	const host = "slow.example.com"
	e.addDomain(host, true)
	e.waitIngressWith(host)
	d := e.waitStatus(host, "active")
	if !strings.Contains(d.Error, "HTTPS not confirmed") {
		t.Fatalf("note: %+v", d)
	}
}

// A direct (unproxied) record must resolve to the server itself, and with an Origin CA
// certificate there is nothing a public HTTPS probe could confirm.
func TestDomainProvisioningUnproxiedWaitsForServerIP(t *testing.T) {
	e := newProvEnv(t)
	e.srv.st.SetSetting(context.Background(), "tls_mode", "cloudflare-origin")
	const host = "direct.example.com"
	e.l.set(func() { e.l.visible[host] = "198.51.100.9" }) // stale record elsewhere
	e.addDomain(host, false)
	time.Sleep(150 * time.Millisecond)
	if e.firstIngressWith(host) >= 0 {
		t.Fatalf("pushed while the name pointed elsewhere: %q", e.l.snapshot())
	}
	e.l.set(func() { e.l.visible[host] = "203.0.113.7" })
	e.waitIngressWith(host)
	if d := e.waitStatus(host, "active"); d.Error != "" {
		t.Fatalf("%+v", d)
	}
	if e.l.index("https "+host) >= 0 {
		t.Fatalf("origin-CA direct domain probed: %q", e.l.snapshot())
	}
}

// When the DNS record cannot be deleted the domain stays bound and routed.
func TestDomainDeleteDNSFailureKeepsRoute(t *testing.T) {
	e := newProvEnv(t)
	const host = "keep.example.com"
	e.l.set(func() { e.l.visible[host] = "198.51.100.20"; e.l.certOK[host] = true })
	d := e.addDomain(host, true)
	e.waitStatus(host, "active")
	e.l.set(func() { e.l.deleteErr = errors.New("cloudflare: HTTP 500") })
	if code := e.do("DELETE", "/api/v1/domains/"+d.ID, nil, nil); code != 502 {
		t.Fatalf("delete: %d", code)
	}
	if got := e.domain(host); got.ID != d.ID {
		t.Fatalf("domain gone: %+v", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ev := e.l.snapshot()
		if last := ev[len(ev)-1]; strings.HasPrefix(last, "ingress [") && strings.Contains(last, host) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("route not restored: %q", ev)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A restart during provisioning resumes it, and keeps the hostname out of the routes
// until DNS is visible.
func TestDomainProvisioningResumesAfterRestart(t *testing.T) {
	e := newProvEnv(t)
	ctx := context.Background()
	d := api.Domain{ID: "dom_resume", ServiceID: e.svc.ID, Hostname: "resume.example.com", PortName: "web", Proxied: true, Status: statusProvisioning, CreatedAt: time.Now().UTC()}
	if err := e.srv.st.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	e.srv.holdRoute(d.Hostname) // what New does for rows it finds provisioning
	e.srv.resumeProvisioning(ctx, []api.Domain{d})
	time.Sleep(100 * time.Millisecond)
	if e.firstIngressWith(d.Hostname) >= 0 {
		t.Fatalf("pushed before DNS: %q", e.l.snapshot())
	}
	e.l.set(func() { e.l.visible[d.Hostname] = "198.51.100.20"; e.l.certOK[d.Hostname] = true })
	e.waitIngressWith(d.Hostname)
	e.waitStatus(d.Hostname, "active")
}

// appliesFor counts the MsgApply (container recreate) sent for a service.
func (e *provEnv) appliesFor(serviceID string) int {
	n := 0
	for _, raw := range e.agent.received(api.MsgApply) {
		var ap api.ApplyPayload
		json.Unmarshal(raw, &ap)
		if ap.Spec.ServiceID == serviceID {
			n++
		}
	}
	return n
}

func (e *provEnv) waitApplies(serviceID string, want int) {
	e.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.appliesFor(serviceID) < want {
		if time.Now().After(deadline) {
			e.t.Fatalf("%s: %d applies, want %d", serviceID, e.appliesFor(serviceID), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Binding and unbinding a domain (provisioning included) only changes the route table: the
// service's container is not redeployed. A service that explicitly references the domain
// in its env is redeployed, since its configuration really changed.
func TestDomainChangeIsRouteOnly(t *testing.T) {
	e := newProvEnv(t)
	e.waitApplies(e.svc.ID, 1) // initial deploy
	var ref api.Service
	e.mustDo("POST", "/api/v1/projects/"+e.svc.ProjectID+"/services", map[string]any{
		"name": "worker", "kind": "image", "image": "traefik/whoami",
		"env": []api.EnvVar{{Key: "APP_URL", Value: "${APP.ATOLL_WEB_URL}"}},
	}, &ref, 201)
	e.waitApplies(ref.ID, 1)

	const host = "app.example.com"
	e.l.set(func() { e.l.visible[host] = "198.51.100.20"; e.l.certOK[host] = true })
	e.addDomain(host, true)
	e.waitIngressWith(host)
	e.waitStatus(host, "active")
	e.waitApplies(ref.ID, 2) // explicit ${APP.ATOLL_WEB_URL} changed
	var last api.ApplyPayload
	raws := e.agent.received(api.MsgApply)
	json.Unmarshal(raws[len(raws)-1], &last)
	if last.Spec.ServiceID != ref.ID || last.Spec.Env["APP_URL"] != "https://"+host {
		t.Fatalf("referencing service redeploy: %+v", last.Spec)
	}

	// a second domain on the same port changes nothing the containers see
	const host2 = "www.example.com"
	e.l.set(func() { e.l.visible[host2] = "198.51.100.20"; e.l.certOK[host2] = true })
	e.addDomain(host2, true)
	e.waitStatus(host2, "active")

	d2 := e.domain(host2)
	e.mustDo("DELETE", "/api/v1/domains/"+d2.ID, nil, nil, 204)
	time.Sleep(200 * time.Millisecond)
	if n := e.appliesFor(e.svc.ID); n != 1 {
		t.Fatalf("domain changes recreated the service's container: %d applies", n)
	}
	if n := e.appliesFor(ref.ID); n != 2 {
		t.Fatalf("second domain on the same port redeployed the referencing service: %d applies", n)
	}

	d := e.domain(host)
	e.mustDo("DELETE", "/api/v1/domains/"+d.ID, nil, nil, 204)
	e.waitApplies(ref.ID, 3) // reference no longer resolves
	time.Sleep(200 * time.Millisecond)
	if n := e.appliesFor(e.svc.ID); n != 1 {
		t.Fatalf("domain removal recreated the service's container: %d applies", n)
	}
	lastIng := ""
	for _, ev := range e.l.snapshot() {
		if strings.HasPrefix(ev, "ingress [") {
			lastIng = ev
		}
	}
	if lastIng != "ingress []" {
		t.Fatalf("route table after removals %q; events %q", lastIng, e.l.snapshot())
	}
}
