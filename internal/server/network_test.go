package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/coder/websocket"
)

const testAuthKey = "tskey-auth-kTEST1CNTRL-0123456789abcdef"

// startNetAgent joins and connects a fake agent with the given hostname and tailnet state.
func startNetAgent(t *testing.T, e *testEnv, hostname, tailnetIP string, nn *api.NodeNetwork) (*fakeAgent, string) {
	t.Helper()
	var jt api.JoinToken
	e.mustDo("POST", "/api/v1/servers/join-token", nil, &jt, 201)
	hello := api.HelloPayload{AgentVersion: "test", Hostname: hostname, OS: "linux", Arch: "amd64", CPUCores: 2, MemMB: 2048, DiskGB: 20,
		PublicIPv4: "203.0.113.10", TailnetIP: tailnetIP, Network: nn}
	var jr api.JoinResponse
	if code := e.doWith(http.DefaultClient, "", "POST", "/agent/v1/join", api.JoinRequest{JoinToken: jt.Token, Hello: hello}, &jr); code != 201 {
		t.Fatalf("join: %d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	wsURL := "ws" + strings.TrimPrefix(e.ts.URL, "http") + "/agent/v1/connect"
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + jr.NodeToken}}})
	if err != nil {
		t.Fatal(err)
	}
	ws.SetReadLimit(64 << 20)
	a := &fakeAgent{t: t, ws: ws, ctrs: map[string]api.ContainerState{}}
	a.send(ctx, api.Envelope{Type: api.MsgHello, Payload: hello})
	go a.loop(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for !e.srv.hub.Online(jr.NodeID) {
		if time.Now().After(deadline) {
			t.Fatal("agent never came online")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return a, jr.NodeID
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// lastMesh is the newest mesh payload an agent received.
func lastMesh(a *fakeAgent) *api.MeshPayload {
	ms := a.received(api.MsgMesh)
	if len(ms) == 0 {
		return nil
	}
	var p api.MeshPayload
	json.Unmarshal(ms[len(ms)-1], &p)
	return &p
}

func enableTailscale(t *testing.T, e *testEnv) {
	t.Helper()
	e.mustDo("PUT", "/api/v1/settings", map[string]any{"network_provider": "tailscale", "tailscale_auth_key": testAuthKey,
		"tailnet_control_url": "http://100.100.0.1:7301", "public_url": "https://panel.example.com"}, nil, 200)
}

func TestTailscaleSettingsWriteOnly(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	var raw json.RawMessage
	if code := e.do("PUT", "/api/v1/settings", map[string]any{"network_provider": "tailscale"}, &raw); code != 400 {
		t.Fatalf("provider without key: %d %s", code, raw)
	}
	if code := e.do("PUT", "/api/v1/settings", map[string]any{"network_provider": "tailscale", "tailscale_auth_key": "not-a-key"}, &raw); code != 400 {
		t.Fatalf("bad key accepted: %d", code)
	}
	if code := e.do("PUT", "/api/v1/settings", map[string]any{"network_provider": "wireguard"}, &raw); code != 400 {
		t.Fatalf("unknown provider accepted: %d", code)
	}
	e.mustDo("PUT", "/api/v1/settings", map[string]any{"network_provider": "tailscale", "tailscale_auth_key": testAuthKey, "tailnet_name": "Tail1234.ts.net."}, &raw, 200)
	if strings.Contains(string(raw), testAuthKey) {
		t.Fatal("PUT response leaks the auth key")
	}
	var st api.Settings
	e.mustDo("GET", "/api/v1/settings", nil, &raw, 200)
	json.Unmarshal(raw, &st)
	if strings.Contains(string(raw), testAuthKey) || st.TailscaleAuthKey != "" || !st.HasTailscaleAuthKey || st.NetworkProvider != "tailscale" || st.TailnetName != "tail1234.ts.net" {
		t.Fatalf("settings %s", raw)
	}
	if !st.AgentPublic {
		t.Fatal("agent endpoints are public by default")
	}
	// encrypted at rest
	var stored string
	e.srv.st.DB.QueryRow(`SELECT value FROM settings WHERE key='tailscale_auth_key'`).Scan(&stored)
	if stored == "" || strings.Contains(stored, "tskey") {
		t.Fatalf("auth key stored in clear: %q", stored)
	}
	// other settings saves keep the key and the provider
	e.mustDo("PUT", "/api/v1/settings", map[string]any{"base_domain": "example.com"}, &st, 200)
	if !st.HasTailscaleAuthKey || st.NetworkProvider != "tailscale" {
		t.Fatalf("unrelated save dropped network settings: %+v", st)
	}
	e.mustDo("PUT", "/api/v1/settings", map[string]any{"network_provider": "none", "clear_tailscale_auth_key": true}, &st, 200)
	if st.HasTailscaleAuthKey || st.NetworkProvider != "" {
		t.Fatalf("not cleared: %+v", st)
	}
}

func TestBootstrapAndPing(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	enableTailscale(t, e)
	var jt api.JoinToken
	e.mustDo("POST", "/api/v1/servers/join-token", nil, &jt, 201)
	if strings.Contains(jt.Command, "tskey") {
		t.Fatal("the join command must not carry the auth key")
	}
	var br api.BootstrapResponse
	if code := e.doWith(http.DefaultClient, "", "POST", "/agent/v1/bootstrap", api.BootstrapRequest{JoinToken: jt.Token}, &br); code != 200 {
		t.Fatalf("bootstrap %d", code)
	}
	if br.NetworkProvider != "tailscale" || br.TailscaleAuthKey != testAuthKey || br.ControlURL != "http://100.100.0.1:7301" || br.PublicURL != "https://panel.example.com" {
		t.Fatalf("bootstrap %+v", br)
	}
	// env format for install-agent.sh
	resp, err := http.Post(e.ts.URL+"/agent/v1/bootstrap?format=env", "application/json", strings.NewReader(`{"join_token":"`+jt.Token+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"ATOLL_NETWORK=tailscale\n", "ATOLL_TS_AUTHKEY=" + testAuthKey + "\n", "ATOLL_CONTROL_URL=http://100.100.0.1:7301\n"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("env body %q misses %q", body, want)
		}
	}
	if code := e.doWith(http.DefaultClient, "", "POST", "/agent/v1/bootstrap", api.BootstrapRequest{JoinToken: "atl_join_nope"}, nil); code != 401 {
		t.Fatalf("bad token: %d", code)
	}
	// bootstrap does not consume the token: the join still works, then the token is spent
	var jr api.JoinResponse
	if code := e.doWith(http.DefaultClient, "", "POST", "/agent/v1/join", api.JoinRequest{JoinToken: jt.Token, Hello: api.HelloPayload{Hostname: "n1"}}, &jr); code != 201 {
		t.Fatalf("join after bootstrap: %d", code)
	}
	if code := e.doWith(http.DefaultClient, "", "POST", "/agent/v1/bootstrap", api.BootstrapRequest{JoinToken: jt.Token}, nil); code != 401 {
		t.Fatalf("used token still bootstraps: %d", code)
	}
	// ping
	var pr struct {
		OK     bool   `json:"ok"`
		NodeID string `json:"node_id"`
	}
	if code := e.doWith(http.DefaultClient, "", "GET", "/agent/v1/ping", nil, &pr); code != 200 || !pr.OK || pr.NodeID != "" {
		t.Fatalf("anonymous ping %d %+v", code, pr)
	}
	if code := e.doWith(http.DefaultClient, jr.NodeToken, "GET", "/agent/v1/ping", nil, &pr); code != 200 || pr.NodeID != jr.NodeID {
		t.Fatalf("node ping %d %+v", code, pr)
	}
	if code := e.doWith(http.DefaultClient, "atl_node_wrong", "GET", "/agent/v1/ping", nil, nil); code != 401 {
		t.Fatalf("bad node token ping: %d", code)
	}
	// network settings for `atoll-agent tailnet up` need the node token
	if code := e.doWith(http.DefaultClient, "", "GET", "/agent/v1/network", nil, nil); code != 401 {
		t.Fatalf("network without token: %d", code)
	}
	br = api.BootstrapResponse{}
	if code := e.doWith(http.DefaultClient, jr.NodeToken, "GET", "/agent/v1/network", nil, &br); code != 200 || br.TailscaleAuthKey != testAuthKey {
		t.Fatalf("network %d %+v", code, br)
	}
}

func TestBootstrapWithoutProvider(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	var jt api.JoinToken
	e.mustDo("POST", "/api/v1/servers/join-token", nil, &jt, 201)
	var br api.BootstrapResponse
	if code := e.doWith(http.DefaultClient, "", "POST", "/agent/v1/bootstrap", api.BootstrapRequest{JoinToken: jt.Token}, &br); code != 200 {
		t.Fatalf("bootstrap %d", code)
	}
	if br.NetworkProvider != "" || br.TailscaleAuthKey != "" || br.ControlURL != "" {
		t.Fatalf("provider off must not hand out network settings: %+v", br)
	}
}

func TestAgentEndpointsTailnetOnly(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	e.srv.SetNetworkOptions(NetOptions{AgentTailnetOnly: true})
	var eb api.ErrorBody
	if code := e.doWith(http.DefaultClient, "", "POST", "/agent/v1/join", api.JoinRequest{JoinToken: "atl_join_x"}, &eb); code != 404 || eb.Code != "tailnet_only" {
		t.Fatalf("public join in tailnet-only mode: %d %+v", code, eb)
	}
	if code := e.doWith(http.DefaultClient, "", "GET", "/agent/v1/ping", nil, nil); code != 404 {
		t.Fatalf("public ping: %d", code)
	}
	// bootstrap stays public (answers 401 for a bad token, not 404)
	if code := e.doWith(http.DefaultClient, "", "POST", "/agent/v1/bootstrap", api.BootstrapRequest{JoinToken: "atl_join_x"}, nil); code != 401 {
		t.Fatalf("bootstrap: %d", code)
	}
	// the panel API is unaffected
	if code := e.do("GET", "/api/v1/servers", nil, nil); code != 200 {
		t.Fatalf("panel: %d", code)
	}
	// the tailnet listener serves the agent endpoints, nothing else
	at := httptest.NewServer(e.srv.AgentHandler())
	defer at.Close()
	resp, _ := http.Get(at.URL + "/agent/v1/ping")
	if resp.StatusCode != 200 {
		t.Fatalf("tailnet ping: %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp, _ = http.Get(at.URL + "/api/v1/servers")
	if resp.StatusCode != 404 {
		t.Fatalf("panel API on the agent listener: %d", resp.StatusCode)
	}
	resp.Body.Close()
	var st api.Settings
	e.mustDo("GET", "/api/v1/settings", nil, &st, 200)
	if st.AgentPublic {
		t.Fatal("settings must report agent_public=false")
	}
}

func TestAgentMovedToTailnetOnHello(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	enableTailscale(t, e)
	a, _ := startNetAgent(t, e, "vps-a", "100.64.0.2", &api.NodeNetwork{Provider: "tailscale", State: "running", ControlURL: "https://panel.example.com"})
	waitFor(t, "network message", func() bool { return len(a.received(api.MsgNetwork)) > 0 })
	var p api.NetworkPayload
	json.Unmarshal(a.received(api.MsgNetwork)[0], &p)
	if p.ControlURL != "http://100.100.0.1:7301" || p.FallbackURL != "https://panel.example.com" || p.AuthKey != "" {
		t.Fatalf("network payload %+v", p)
	}
	// a node without a tailnet IP is left alone
	b, _ := startNetAgent(t, e, "vps-b", "", &api.NodeNetwork{State: "not_installed", ControlURL: "https://panel.example.com"})
	time.Sleep(300 * time.Millisecond)
	if len(b.received(api.MsgNetwork)) != 0 {
		t.Fatal("node off the tailnet must not be switched")
	}
}

func TestServerNetworkJoinAction(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	a, id := startNetAgent(t, e, "vps-a", "", &api.NodeNetwork{State: "not_installed", ControlURL: "https://panel.example.com"})
	var eb api.ErrorBody
	if code := e.do("POST", "/api/v1/servers/"+id+"/network", api.ServerNetworkRequest{Action: "join"}, &eb); code != 409 || eb.Code != "network_disabled" {
		t.Fatalf("join without provider: %d %+v", code, eb)
	}
	enableTailscale(t, e)
	a.mu.Lock()
	a.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type != api.MsgNetwork {
			return api.ResultPayload{}, false
		}
		out, _ := json.Marshal(api.NetworkResult{Switched: true, ControlURL: "http://100.100.0.1:7301", TailnetIP: "100.64.0.9", State: "running"})
		return api.ResultPayload{OK: true, Output: string(out)}, true
	}
	a.mu.Unlock()
	var res api.NetworkResult
	e.mustDo("POST", "/api/v1/servers/"+id+"/network", api.ServerNetworkRequest{Action: "join"}, &res, 200)
	if !res.Switched || res.TailnetIP != "100.64.0.9" {
		t.Fatalf("result %+v", res)
	}
	msgs := a.received(api.MsgNetwork)
	var p api.NetworkPayload
	json.Unmarshal(msgs[len(msgs)-1], &p)
	if p.AuthKey != testAuthKey || p.ControlURL != "http://100.100.0.1:7301" {
		t.Fatalf("join payload %+v", p)
	}
	if code := e.do("POST", "/api/v1/servers/"+id+"/network", api.ServerNetworkRequest{Action: "dance"}, nil); code != 400 {
		t.Fatalf("bad action: %d", code)
	}
	// audit log never holds the key
	var n int
	e.srv.st.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%tskey%'`).Scan(&n)
	if n != 0 {
		t.Fatal("auth key in audit log")
	}
}

func TestCrossServerServiceMesh(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	nnA := &api.NodeNetwork{Provider: "tailscale", State: "running", ControlURL: "http://100.100.0.1:7301", ViaTailnet: true}
	a, idA := startNetAgent(t, e, "vps-a", "100.64.0.2", nnA)
	b, idB := startNetAgent(t, e, "vps-b", "100.64.0.3", nnA)
	c, idC := startNetAgent(t, e, "vps-c", "", &api.NodeNetwork{State: "not_installed"})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": idA}, &p, 201)
	var apiSvc, db api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "api", "image": "nginx:1.27", "ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}},
		"env": []api.EnvVar{{Key: "CACHE", Value: "${CACHE_HOST}:6379"}, {Key: "DB", Value: "postgres://db.zeabur.internal:5432"}},
	}, &apiSvc, 201)
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "db", "image": "postgres:16", "ports": []api.Port{{Name: "pg", ContainerPort: 5432, Protocol: "tcp"}},
	}, &db, 201)
	if apiSvc.ServerID != idA {
		t.Fatalf("service server_id %q, want project server", apiSvc.ServerID)
	}
	cacheReq := map[string]any{"name": "cache", "image": "redis:7", "server_id": idB,
		"ports": []api.Port{{Name: "redis", ContainerPort: 6379, Protocol: "tcp"}, {Name: "stats", ContainerPort: 9125, Protocol: "udp"}}}
	var eb api.ErrorBody
	if code := e.do("POST", "/api/v1/projects/"+p.ID+"/services", cacheReq, &eb); code != 409 || eb.Code != "tailnet_required" {
		t.Fatalf("cross-server placement without provider: %d %+v", code, eb)
	}
	enableTailscale(t, e)
	cacheReq["server_id"] = idC
	if code := e.do("POST", "/api/v1/projects/"+p.ID+"/services", cacheReq, &eb); code != 409 || !strings.Contains(eb.Error, "vps-c") {
		t.Fatalf("placement on a node off the tailnet: %d %+v", code, eb)
	}
	cacheReq["server_id"] = idB
	var cache api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", cacheReq, &cache, 201)
	if cache.ServerID != idB {
		t.Fatalf("cache placed on %q", cache.ServerID)
	}
	waitFor(t, "apply on B", func() bool { return len(b.received(api.MsgApply)) > 0 })
	var ap api.ApplyPayload
	json.Unmarshal(b.received(api.MsgApply)[0], &ap)
	if ap.Spec.ServiceID != cache.ID || ap.Spec.Network != "atoll-"+p.ID {
		t.Fatalf("B apply %+v", ap.Spec)
	}
	for _, raw := range a.received(api.MsgApply) {
		var x api.ApplyPayload
		json.Unmarshal(raw, &x)
		if x.Spec.ServiceID == cache.ID {
			t.Fatal("cache must not be applied on A")
		}
	}
	// mesh: A imports cache from B; B exports cache and imports api + db from A
	waitFor(t, "mesh on A", func() bool { m := lastMesh(a); return m != nil && len(m.Imports) == 1 })
	waitFor(t, "mesh on B", func() bool { m := lastMesh(b); return m != nil && len(m.Imports) == 2 })
	ma, mb := lastMesh(a), lastMesh(b)
	if ma.TailnetIP != "100.64.0.2" || len(ma.Exports) != 2 {
		t.Fatalf("A mesh %+v", ma)
	}
	imp := ma.Imports[0]
	if imp.Name != "cache" || imp.ServerID != idB || imp.Network != "atoll-"+p.ID || len(imp.Ports) != 2 {
		t.Fatalf("A import %+v", imp)
	}
	if mb.TailnetIP != "100.64.0.3" || len(mb.Exports) != 1 || mb.Exports[0].Name != "cache" {
		t.Fatalf("B mesh %+v", mb)
	}
	exp := mb.Exports[0]
	for i, pt := range exp.Ports {
		if pt.HostPort < 41000 || pt.HostPort > 41999 {
			t.Fatalf("host port %d outside the mesh range", pt.HostPort)
		}
		if imp.Ports[i].Target != "100.64.0.3:"+itoa(pt.HostPort) || imp.Ports[i].Protocol != pt.Protocol || imp.Ports[i].Port != pt.Port {
			t.Fatalf("import %+v does not match export %+v", imp.Ports[i], pt)
		}
	}
	if exp.Ports[1].Protocol != "udp" {
		t.Fatalf("udp port lost: %+v", exp.Ports)
	}
	names := map[string]string{}
	for _, i := range mb.Imports {
		names[i.Name] = i.Ports[0].Target
	}
	if !strings.HasPrefix(names["api"], "100.64.0.2:") || !strings.HasPrefix(names["db"], "100.64.0.2:") {
		t.Fatalf("B imports %+v", names)
	}
	if m := lastMesh(c); m != nil && len(m.Exports)+len(m.Imports) > 0 {
		t.Fatalf("C has nothing of this project: %+v", m)
	}
	// env references resolve to the same names (the import alias), placement-agnostic
	var redeploy api.Service
	e.mustDo("PATCH", "/api/v1/services/"+apiSvc.ID, map[string]any{"image": "nginx:1.28"}, &redeploy, 200)
	waitFor(t, "api redeploy", func() bool {
		for _, raw := range a.received(api.MsgApply) {
			var x api.ApplyPayload
			json.Unmarshal(raw, &x)
			if x.Spec.ServiceID == apiSvc.ID && x.Spec.Image == "nginx:1.28" {
				if x.Spec.Env["CACHE"] != "cache:6379" {
					t.Fatalf("CACHE=%q", x.Spec.Env["CACHE"])
				}
				return true
			}
		}
		return false
	})
	// host ports are stable across recomputation
	plan, _, err := e.srv.MeshPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plan[idB].Exports[0].Ports[0].HostPort != exp.Ports[0].HostPort {
		t.Fatal("host port changed between plans")
	}
	// PATCH server_id is refused (that's a migration)
	if code := e.do("PATCH", "/api/v1/services/"+cache.ID, map[string]any{"server_id": idA}, nil); code != 400 {
		t.Fatalf("PATCH server_id: %d", code)
	}
	// B still runs a service of A's project: it cannot be deleted
	if code := e.do("DELETE", "/api/v1/servers/"+idB, nil, &eb); code != 409 {
		t.Fatalf("delete B: %d", code)
	}
	// deleting the cache collapses the mesh again
	e.mustDo("DELETE", "/api/v1/services/"+cache.ID, nil, nil, 204)
	waitFor(t, "mesh cleared on A", func() bool { m := lastMesh(a); return m != nil && len(m.Imports)+len(m.Exports) == 0 })
	waitFor(t, "mesh cleared on B", func() bool { m := lastMesh(b); return m != nil && len(m.Imports)+len(m.Exports) == 0 })
	var n int
	e.srv.st.DB.QueryRow(`SELECT COUNT(*) FROM mesh_ports`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d stale mesh port allocations", n)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestServiceMigrateUpdatesMesh(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	enableTailscale(t, e)
	nn := &api.NodeNetwork{Provider: "tailscale", State: "running", ControlURL: "http://100.100.0.1:7301", ViaTailnet: true}
	a, idA := startNetAgent(t, e, "vps-a", "100.64.0.2", nn)
	b, idB := startNetAgent(t, e, "vps-b", "100.64.0.3", nn)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": idA}, &p, 201)
	var web, db api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "web", "image": "nginx:1.27",
		"ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}}}, &web, 201)
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "db", "image": "postgres:16",
		"ports":   []api.Port{{Name: "pg", ContainerPort: 5432, Protocol: "tcp"}},
		"volumes": []api.Volume{{Name: "data", MountPath: "/var/lib/postgresql/data"}}}, &db, 201)
	waitFor(t, "db running on A", func() bool {
		var s api.Service
		e.mustDo("GET", "/api/v1/services/"+db.ID, nil, &s, 200)
		return s.Status == "running"
	})
	var job api.MigrationJob
	e.mustDo("POST", "/api/v1/services/"+db.ID+"/migrate", api.ServiceMigrateRequest{ServerID: idB}, &job, 200)
	if job.ServiceID != db.ID || job.FromServer != idA || job.ToServer != idB {
		t.Fatalf("job %+v", job)
	}
	waitFor(t, "migration done", func() bool {
		e.mustDo("GET", "/api/v1/migrations/"+job.ID, nil, &job, 200)
		if job.Status == "failed" {
			t.Fatalf("migration failed: %s %+v", job.Error, job.Steps)
		}
		return job.Status == "done"
	})
	var got api.Service
	e.mustDo("GET", "/api/v1/services/"+db.ID, nil, &got, 200)
	if got.ServerID != idB {
		t.Fatalf("db now on %q", got.ServerID)
	}
	e.mustDo("GET", "/api/v1/services/"+web.ID, nil, &got, 200)
	if got.ServerID != idA {
		t.Fatal("web must stay on A")
	}
	var pp api.Project
	e.mustDo("GET", "/api/v1/projects/"+p.ID, nil, &pp, 200)
	if pp.ServerID != idA {
		t.Fatal("project server must not change")
	}
	// volume exported on A, imported on B; container removed from A only
	if len(a.received(api.MsgVolumeExport)) != 1 || len(b.received(api.MsgVolumeImport)) != 1 {
		t.Fatal("volume relay missing")
	}
	var rm api.RemovePayload
	json.Unmarshal(a.received(api.MsgRemove)[0], &rm)
	if rm.ServiceID != db.ID || !rm.KeepVolumes || len(b.received(api.MsgRemove)) != 0 {
		t.Fatalf("remove %+v", rm)
	}
	// blob URLs use each agent's tailnet control URL
	var ex api.VolumeTransferPayload
	json.Unmarshal(a.received(api.MsgVolumeExport)[0], &ex)
	if !strings.HasPrefix(ex.URL, "http://100.100.0.1:7301/agent/v1/blobs/") {
		t.Fatalf("export URL %q", ex.URL)
	}
	waitFor(t, "A imports db from B", func() bool {
		m := lastMesh(a)
		return m != nil && len(m.Imports) == 1 && m.Imports[0].Name == "db" && strings.HasPrefix(m.Imports[0].Ports[0].Target, "100.64.0.3:")
	})
	waitFor(t, "B imports web from A", func() bool {
		m := lastMesh(b)
		return m != nil && len(m.Imports) == 1 && m.Imports[0].Name == "web" && len(m.Exports) == 1
	})
	// moving it back collapses the mesh
	e.mustDo("POST", "/api/v1/services/"+db.ID+"/migrate", api.ServiceMigrateRequest{ServerID: idA}, &job, 200)
	waitFor(t, "migration back", func() bool {
		e.mustDo("GET", "/api/v1/migrations/"+job.ID, nil, &job, 200)
		return job.Status == "done" || job.Status == "failed"
	})
	if job.Status != "done" {
		t.Fatalf("migrate back: %s", job.Error)
	}
	waitFor(t, "mesh empty", func() bool { m := lastMesh(a); return m != nil && len(m.Imports)+len(m.Exports) == 0 })
}

func TestAgentURLRewrite(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, tn := startNetAgent(t, e, "vps-a", "100.64.0.2", &api.NodeNetwork{ControlURL: "http://100.100.0.1:7301/", ViaTailnet: true})
	_, pub := startNetAgent(t, e, "vps-b", "", &api.NodeNetwork{ControlURL: "https://panel.example.com"})
	ctx := context.Background()
	if u := e.srv.agentURL(ctx, tn, "https://panel.example.com", "https://panel.example.com/agent/v1/blobs/x"); u != "http://100.100.0.1:7301/agent/v1/blobs/x" {
		t.Fatal(u)
	}
	if u := e.srv.agentURL(ctx, pub, "https://panel.example.com", "https://panel.example.com/agent/v1/blobs/x"); u != "https://panel.example.com/agent/v1/blobs/x" {
		t.Fatal(u)
	}
}

func TestParseMeshPortsAndListen(t *testing.T) {
	if lo, hi, err := ParseMeshPorts(""); err != nil || lo != 41000 || hi != 41999 {
		t.Fatal(lo, hi, err)
	}
	if lo, hi, err := ParseMeshPorts("42000-42010"); err != nil || lo != 42000 || hi != 42010 {
		t.Fatal(lo, hi, err)
	}
	for _, bad := range []string{"80-90", "5-", "42010-42000", "x"} {
		if _, _, err := ParseMeshPorts(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if a, err := resolveListen("100.64.0.1:7301"); err != nil || a != "100.64.0.1:7301" {
		t.Fatal(a, err)
	}
	if _, err := resolveListen("nope"); err == nil {
		t.Fatal("bad listen accepted")
	}
}

func TestNetworkRoutePermissions(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	enableTailscale(t, e)
	nn := &api.NodeNetwork{Provider: "tailscale", State: "running", ControlURL: "http://100.100.0.1:7301", ViaTailnet: true}
	_, idA := startNetAgent(t, e, "vps-a", "100.64.0.2", nn)
	_, idB := startNetAgent(t, e, "vps-b", "100.64.0.3", nn)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": idA}, &p, 201)
	newToken := func(body map[string]any) string {
		var tok api.APIToken
		e.mustDo("POST", "/api/v1/tokens", body, &tok, 201)
		return tok.Token
	}
	deploy := newToken(map[string]any{"name": "ci", "scope": "deploy"})

	// service create with server_id only needs deploy
	var svc api.Service
	if code := e.doWith(http.DefaultClient, deploy, "POST", "/api/v1/projects/"+p.ID+"/services",
		map[string]any{"name": "cache", "image": "redis:7", "server_id": idB}, &svc); code != 201 || svc.ServerID != idB {
		t.Fatalf("deploy token placing a service: %d %+v", code, svc)
	}

	// network actions and service migrate need servers / migrate
	var eb api.ErrorBody
	if code := e.doWith(http.DefaultClient, deploy, "POST", "/api/v1/servers/"+idA+"/network", api.ServerNetworkRequest{Action: "dance"}, &eb); code != 403 || eb.Code != "missing_permission" {
		t.Fatalf("network without servers: %d %+v", code, eb)
	}
	eb = api.ErrorBody{}
	if code := e.doWith(http.DefaultClient, deploy, "POST", "/api/v1/services/"+svc.ID+"/migrate", api.ServiceMigrateRequest{ServerID: idA}, &eb); code != 403 || eb.Code != "missing_permission" {
		t.Fatalf("migrate without migrate: %d %+v", code, eb)
	}
	var cs []api.Confirmation
	e.mustDo("GET", "/api/v1/confirmations", nil, &cs, 200)
	if len(cs) != 0 {
		t.Fatalf("confirmation created for a token without permission: %+v", cs)
	}

	// with the permission the request reaches the handler directly (validation answers)
	servers := newToken(map[string]any{"name": "ops", "permissions": []string{"servers"}})
	if code := e.doWith(http.DefaultClient, servers, "POST", "/api/v1/servers/"+idA+"/network", api.ServerNetworkRequest{Action: "dance"}, nil); code != 400 {
		t.Fatalf("network with servers: %d", code)
	}
	mig := newToken(map[string]any{"name": "mover", "permissions": []string{"migrate"}})
	if code := e.doWith(http.DefaultClient, mig, "POST", "/api/v1/services/"+svc.ID+"/migrate", api.ServiceMigrateRequest{ServerID: idB}, nil); code != 400 {
		t.Fatalf("migrate with migrate (same server): %d", code)
	}

	// require_approval: 202 + pending confirmation
	careful := newToken(map[string]any{"name": "careful", "permissions": []string{"migrate"}, "require_approval": true})
	var c api.Confirmation
	if code := e.doWith(http.DefaultClient, careful, "POST", "/api/v1/services/"+svc.ID+"/migrate", api.ServiceMigrateRequest{ServerID: idA}, &c); code != 202 || c.Action != "service.migrate" {
		t.Fatalf("require_approval migrate: %d %+v", code, c)
	}

	// project-scoped tokens: service migrate is scoped to the service's project, network is global
	scoped := newToken(map[string]any{"name": "scoped", "permissions": []string{"migrate"}, "projects": []string{p.ID}})
	if code := e.doWith(http.DefaultClient, scoped, "POST", "/api/v1/services/"+svc.ID+"/migrate", api.ServiceMigrateRequest{ServerID: idB}, nil); code != 400 {
		t.Fatalf("scoped migrate own service: %d", code)
	}
}
