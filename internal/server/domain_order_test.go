package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// logIngress makes a fake agent log every route table it receives (tagged with node) and
// every container removal into l.
func logIngress(a *fakeAgent, l *provLog, node string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		switch env.Type {
		case api.MsgIngress:
			var p api.IngressPayload
			json.Unmarshal(env.Payload, &p)
			hosts := []string{}
			for _, r := range p.Routes {
				hosts = append(hosts, r.Hostname)
			}
			l.add(node + "ingress [" + strings.Join(hosts, " ") + "]")
		case api.MsgRemove:
			var p api.RemovePayload
			json.Unmarshal(env.Payload, &p)
			l.add(node + "remove " + p.ServiceID)
		}
		return api.ResultPayload{}, false
	}
}

func isIngress(ev, node string) bool { return strings.HasPrefix(ev, node+"ingress [") }

func ingressHas(ev, host string) bool {
	i := strings.Index(ev, "ingress [")
	return i >= 0 && slices.Contains(strings.Fields(strings.Trim(ev[i+len("ingress "):], "[]")), host)
}

// A Zeabur cutover creates the record for a remapped *.zeabur.app host while that host is
// held out of Caddy, routes it only once it resolves publicly, and confirms HTTPS
// (provisioning -> active). The custom domain, which already points here, is routed at
// once. A rollback withdraws the routes first and only then deletes the record it created.
func TestZeaburCutoverProvisionsNewDomains(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	l := &provLog{visible: map[string]string{}, certOK: map[string]bool{}}
	e.cf.mu.Lock()
	e.cf.onEvent = l.add
	e.cf.mu.Unlock()
	// public DNS lags behind the zone until the test lets it through
	e.srv.cfg.ResolvePublic = func(ctx context.Context, host string) ([]string, error) {
		l.mu.Lock()
		ip, ok := l.visible[host]
		l.mu.Unlock()
		if !ok {
			return nil, context.DeadlineExceeded
		}
		l.add("resolved " + host)
		return []string{ip}, nil
	}
	e.srv.cfg.DNSWaitTimeout = 5 * time.Second
	agent, nodeID := startFakeAgent(t, e, zeaburInventory())
	logIngress(agent, l, "")
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "cf-token", CloudflareZoneID: "zone1"}, nil, 200)
	var job api.ImportJob
	e.mustDo("POST", "/api/v1/imports/zeabur-local", api.ZeaburLocalImportRequest{ServerID: nodeID, Namespace: "environment-abc123"}, &job, 201)
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rehearse", nil, nil, 200)
	waitJob(t, e, job.ID, "rehearsed")

	const newHost, custom = "myblog.example.com", "blog.example.org"
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cutover", nil, nil, 200)
	if j := waitJob(t, e, job.ID, "live", "rolled_back", "failed"); j.Status != "live" {
		t.Fatalf("cutover: %s %+v", j.Status, j.Steps)
	}
	domStatus := func(host string) api.Domain {
		var ds []api.Domain
		e.mustDo("GET", "/api/v1/domains", nil, &ds, 200)
		for _, d := range ds {
			if d.Hostname == host {
				return d
			}
		}
		return api.Domain{}
	}
	if d := domStatus(newHost); d.Status != "provisioning" {
		t.Fatalf("new name before DNS is visible: %+v", d)
	}
	time.Sleep(150 * time.Millisecond)
	ev := l.snapshot()
	routedCustom := false
	for _, x := range ev {
		if ingressHas(x, newHost) {
			t.Fatalf("new name routed before it resolves: %q", ev)
		}
		routedCustom = routedCustom || ingressHas(x, custom)
	}
	if !routedCustom || l.index("cf:create "+newHost) < 0 {
		t.Fatalf("custom domain not routed or record not created: %q", ev)
	}

	l.set(func() { l.visible[newHost] = "198.51.100.20" })
	waitFor(t, "new name routed", func() bool {
		for _, x := range l.snapshot() {
			if ingressHas(x, newHost) {
				return true
			}
		}
		return false
	})
	ev = l.snapshot()
	first := slices.IndexFunc(ev, func(x string) bool { return ingressHas(x, newHost) })
	if c, r := l.index("cf:create "+newHost), l.index("resolved "+newHost); !(c < r && r < first) {
		t.Fatalf("order create=%d resolved=%d route=%d: %q", c, r, first, ev)
	}
	waitFor(t, "new name active", func() bool { return domStatus(newHost).Status == "active" })

	// rollback: routes withdrawn, then the record the cutover created is deleted
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rollback", nil, nil, 200)
	if j := waitJob(t, e, job.ID, "rolled_back", "failed"); j.Status != "rolled_back" {
		t.Fatalf("rollback: %+v", j.Steps)
	}
	ev = l.snapshot()
	del := l.index("cf:delete " + newHost)
	if del < 0 {
		t.Fatalf("record created by the cutover not deleted: %q", ev)
	}
	lastIng := ""
	for _, x := range ev[:del] {
		if isIngress(x, "") {
			lastIng = x
		}
	}
	if lastIng != "ingress []" {
		t.Fatalf("routes still served when the record was deleted (%q): %q", lastIng, ev)
	}
	if l.index("cf:delete "+custom) >= 0 {
		t.Fatalf("custom domain's record touched: %q", ev)
	}
	if d := domStatus(newHost); d.Status != "pending" {
		t.Fatalf("after rollback: %+v", d)
	}

	// a second cutover creates the record again and provisions it the same way
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cutover", nil, nil, 200)
	if j := waitJob(t, e, job.ID, "live", "rolled_back", "failed"); j.Status != "live" {
		t.Fatalf("second cutover: %+v", j.Steps)
	}
	waitFor(t, "new name active again", func() bool { return domStatus(newHost).Status == "active" })
	e.cf.mu.Lock()
	created := slices.Clone(e.cf.created)
	e.cf.mu.Unlock()
	if len(created) != 2 {
		t.Fatalf("records created: %v", created)
	}
}

// Moving a project to another server switches the DNS record first, waits until it
// resolves to the target (direct record), then pushes the routes to the target, and only
// then withdraws them from the source.
func TestMigrationSwitchesDNSBeforeRoutes(t *testing.T) {
	e := newProvEnv(t)
	e.srv.st.SetSetting(context.Background(), "tls_mode", "acme")
	const host = "move.example.com"
	const ipA, ipB = "203.0.113.7", "198.51.100.30"
	e.l.set(func() { e.l.visible[host] = ipA; e.l.certOK[host] = true })
	e.addDomain(host, false)
	e.waitStatus(host, "active")
	logIngress(e.agent, e.l, "A:")
	b, idB := startFakeAgentAs(t, e.testEnv, api.ZeaburInventory{}, "box-b", ipB)
	logIngress(b, e.l, "B:")
	start := len(e.l.snapshot())

	// public DNS follows the record a little later
	go func() {
		for !slices.Contains(e.l.snapshot()[start:], "dns:upsert "+host) {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(150 * time.Millisecond)
		e.l.set(func() { e.l.visible[host] = ipB })
	}()
	var job api.MigrationJob
	e.mustDo("POST", "/api/v1/projects/"+e.svc.ProjectID+"/migrate", map[string]string{"server_id": idB}, &job, 200)
	waitFor(t, "migration done", func() bool {
		e.mustDo("GET", "/api/v1/migrations/"+job.ID, nil, &job, 200)
		if job.Status == "failed" {
			t.Fatalf("migration failed: %s %+v", job.Error, job.Steps)
		}
		return job.Status == "done"
	})
	var names []string
	for _, s := range job.Steps {
		names = append(names, s.Name)
	}
	if strings.Join(names, "|") != "Stop services on source|Start services on target|Switch DNS|Update ingress|Remove from source" {
		t.Fatalf("steps %q", names)
	}

	ev := e.l.snapshot()
	upsert := -1
	for i := start; i < len(ev); i++ {
		if ev[i] == "dns:upsert "+host {
			upsert = i
			break
		}
	}
	firstB := slices.IndexFunc(ev, func(x string) bool { return isIngress(x, "B:") && ingressHas(x, host) })
	if upsert < 0 || firstB < 0 || firstB < upsert {
		t.Fatalf("upsert=%d first target route=%d: %q", upsert, firstB, ev)
	}
	// the target got the route only once the name resolved to it
	if !e.l.resolvedTo(host, ipB, firstB) {
		t.Fatalf("target routed before %s resolved to it: %q", host, ev)
	}
	// the source kept serving the name until the target had it, then lost it
	for i := start; i < firstB; i++ {
		if isIngress(ev[i], "A:") && !ingressHas(ev[i], host) {
			t.Fatalf("source route withdrawn before the target was routed: %q", ev)
		}
	}
	withdrawn := false
	for _, x := range ev[firstB:] {
		withdrawn = withdrawn || (isIngress(x, "A:") && !ingressHas(x, host))
	}
	if !withdrawn {
		t.Fatalf("source route never withdrawn: %q", ev)
	}
	if d := e.waitStatus(host, "active"); d.Error != "" {
		t.Fatalf("after move: %+v", d)
	}
}

// Deleting a service or a project: routes leave Caddy and containers are removed before
// the DNS records are deleted.
func TestDeleteRemovesRoutesBeforeDNS(t *testing.T) {
	for _, what := range []string{"service", "project"} {
		t.Run(what, func(t *testing.T) {
			e := newProvEnv(t)
			const host = "gone.example.com"
			e.l.set(func() { e.l.visible[host] = "198.51.100.20"; e.l.certOK[host] = true })
			e.addDomain(host, true)
			e.waitStatus(host, "active")
			logIngress(e.agent, e.l, "")
			if what == "service" {
				e.mustDo("DELETE", "/api/v1/services/"+e.svc.ID, nil, nil, 204)
			} else {
				e.mustDo("DELETE", "/api/v1/projects/"+e.svc.ProjectID, nil, nil, 204)
			}
			ev := e.l.snapshot()
			del := slices.Index(ev, "dns:delete "+host)
			rm := slices.Index(ev, "remove "+e.svc.ID)
			if del < 0 || rm < 0 || rm > del {
				t.Fatalf("remove=%d dns delete=%d: %q", rm, del, ev)
			}
			lastIng := ""
			for _, x := range ev[:del] {
				if isIngress(x, "") {
					lastIng = x
				}
			}
			if lastIng == "" || ingressHas(lastIng, host) {
				t.Fatalf("route table before DNS delete %q: %q", lastIng, ev)
			}
		})
	}
}
