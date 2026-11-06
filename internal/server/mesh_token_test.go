package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func findExport(m *api.MeshPayload, name string) *api.MeshExport {
	if m == nil {
		return nil
	}
	for i := range m.Exports {
		if m.Exports[i].Name == name {
			return &m.Exports[i]
		}
	}
	return nil
}

func findImport(m *api.MeshPayload, name string) *api.MeshImport {
	if m == nil {
		return nil
	}
	for i := range m.Imports {
		if m.Imports[i].Name == name {
			return &m.Imports[i]
		}
	}
	return nil
}

// TestMeshTokenRollout: tokens roll out in stages as agents announce support, enforcement
// waits for every node of the service, and a downgrade turns it off again.
func TestMeshTokenRollout(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	nn := &api.NodeNetwork{Provider: "tailscale", State: "running", ControlURL: "http://100.100.0.1:7301", ViaTailnet: true}
	helloA := netHello("vps-a", "100.64.0.2", nn)
	helloA.MeshToken = true
	helloB := netHello("vps-b", "100.64.0.3", nn) // old agent: no mesh_token in its hello
	a, idA, tokA := startNetAgentHello(t, e, helloA)
	b, idB, tokB := startNetAgentHello(t, e, helloB)
	enableTailscale(t, e)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": idA}, &p, 201)
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "api", "image": "nginx:1.27", "ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}}}, nil, 201)
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "cache", "image": "redis:7", "server_id": idB,
		"ports": []api.Port{{Name: "redis", ContainerPort: 6379, Protocol: "tcp"}, {Name: "stats", ContainerPort: 9125, Protocol: "udp"}}}, nil, 201)
	want := e.srv.meshToken(p.ID)
	if len(want) != 32 || want == e.srv.meshToken("other") {
		t.Fatalf("token %q", want)
	}

	// Stage: A supports tokens, B is old. A's export carries the token (accept mode), B's
	// import of it gets the token once A acknowledged (B ignores it), nothing is enforced and
	// B's export stays plain.
	waitFor(t, "A export token", func() bool {
		x := findExport(lastMesh(a), "api")
		i := findImport(lastMesh(b), "api")
		return x != nil && x.Token == want && i != nil && i.Token == want
	})
	if x := findExport(lastMesh(a), "api"); x.RequireToken {
		t.Fatal("enforced while the importing node is an old agent")
	}
	if x := findExport(lastMesh(b), "cache"); x == nil || x.Token != "" || x.RequireToken {
		t.Fatalf("old exporter got a token: %+v", x)
	}
	if i := findImport(lastMesh(a), "cache"); i == nil || i.Token != "" {
		t.Fatalf("import from an old exporter must not send the handshake: %+v", i)
	}

	// B upgraded: everything converges to enforced.
	helloB.MeshToken = true
	b = connectNetAgent(t, e, idB, tokB, helloB)
	waitFor(t, "enforced both ways", func() bool {
		xa, xb := findExport(lastMesh(a), "api"), findExport(lastMesh(b), "cache")
		ia, ib := findImport(lastMesh(a), "cache"), findImport(lastMesh(b), "api")
		return xa != nil && xa.RequireToken && xb != nil && xb.RequireToken && xb.Token == want &&
			ia != nil && ia.Token == want && ib != nil && ib.Token == want
	})
	// B's exporter was never told to require before A's importer had the token.
	sawImportToken := false
	for _, raw := range a.received(api.MsgMesh) {
		var m api.MeshPayload
		json.Unmarshal(raw, &m)
		if i := findImport(&m, "cache"); i != nil && i.Token != "" {
			sawImportToken = true
		}
	}
	if !sawImportToken {
		t.Fatal("A never got the import token")
	}

	// Tokens never leave through the HTTP API or the stored state.
	for _, path := range []string{"/api/v1/servers", "/api/v1/servers/" + idA, "/api/v1/servers/" + idB, "/api/v1/projects/" + p.ID, "/api/v1/projects"} {
		var raw json.RawMessage
		e.mustDo("GET", path, nil, &raw, 200)
		if strings.Contains(string(raw), want) {
			t.Fatalf("%s leaks the mesh token", path)
		}
	}
	var stored string
	e.srv.st.DB.QueryRow(`SELECT value FROM settings WHERE key=?`, meshTokenStateKey).Scan(&stored)
	if stored == "" || strings.Contains(stored, want) {
		t.Fatalf("token state %q", stored)
	}

	// A control server restart (state reloaded from settings) keeps enforcement as it was.
	before, _, err := e.srv.MeshPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := e.srv.netst()
	n.tokMu.Lock()
	n.tok = nil
	n.tokMu.Unlock()
	after, _, err := e.srv.MeshPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bj, _ := json.Marshal(before)
	aj, _ := json.Marshal(after)
	if string(bj) != string(aj) {
		t.Fatalf("plan changed after reload:\n%s\n%s", bj, aj)
	}

	// A downgraded: B stops enforcing, A's export and B's handshake to A go away.
	helloA.MeshToken = false
	a = connectNetAgent(t, e, idA, tokA, helloA)
	waitFor(t, "enforcement off after downgrade", func() bool {
		xb := findExport(lastMesh(b), "cache")
		ib := findImport(lastMesh(b), "api")
		return xb != nil && !xb.RequireToken && xb.Token == want && ib != nil && ib.Token == ""
	})
	if x := findExport(lastMesh(a), "api"); x != nil && (x.Token != "" || x.RequireToken) {
		// the newest payload A got after the reconnect
		waitFor(t, "A export plain", func() bool {
			x := findExport(lastMesh(a), "api")
			return x != nil && x.Token == "" && !x.RequireToken
		})
	}
}

// TestMeshTokenStages drives applyMeshTokens directly: one stage per acknowledgement.
func TestMeshTokenStages(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	mk := func() map[string]*api.MeshPayload {
		return map[string]*api.MeshPayload{
			"n1": {TailnetIP: "100.64.0.2", Exports: []api.MeshExport{{ServiceID: "s1", ProjectID: "p1", Name: "db",
				Ports: []api.MeshPort{{Port: 5432, Protocol: "tcp", HostPort: 41000}}}}},
			"n2": {TailnetIP: "100.64.0.3", Imports: []api.MeshImport{{ServiceID: "s1", ProjectID: "p1", Name: "db", ServerID: "n1",
				Ports: []api.MeshPort{{Port: 5432, Protocol: "tcp", Target: "100.64.0.2:41000"}}}}},
			"n3": {TailnetIP: "100.64.0.4", Imports: []api.MeshImport{{ServiceID: "s1", ProjectID: "p1", Name: "db", ServerID: "n1",
				Ports: []api.MeshPort{{Port: 5432, Protocol: "tcp", Target: "100.64.0.2:41000"}}}}},
		}
	}
	plan := func() map[string]*api.MeshPayload {
		p := mk()
		e.srv.applyMeshTokens(ctx, p)
		return p
	}
	ack := func(p map[string]*api.MeshPayload, ids ...string) {
		for _, id := range ids {
			e.srv.recordMeshAck(ctx, id, p[id])
		}
	}
	tok := e.srv.meshToken("p1")

	// nobody announced support: the plan is exactly the pre-token one (old payload compat)
	p := plan()
	bj, _ := json.Marshal(p)
	mj, _ := json.Marshal(mk())
	if string(bj) != string(mj) || strings.Contains(string(bj), "token") {
		t.Fatalf("plan without capable nodes changed: %s", bj)
	}
	e.srv.setMeshCap(ctx, "n1", true)
	e.srv.setMeshCap(ctx, "n2", true)
	e.srv.setMeshCap(ctx, "n3", false)
	p = plan()
	if x := p["n1"].Exports[0]; x.Token != tok || x.RequireToken || p["n2"].Imports[0].Token != "" {
		t.Fatalf("stage 1: %+v %+v", x, p["n2"].Imports[0])
	}
	ack(p, "n1", "n2", "n3")
	p = plan()
	if p["n2"].Imports[0].Token != tok || p["n3"].Imports[0].Token != tok || p["n1"].Exports[0].RequireToken {
		t.Fatalf("stage 2: %+v", p)
	}
	ack(p, "n1", "n2", "n3")
	if p = plan(); p["n1"].Exports[0].RequireToken {
		t.Fatal("enforced while n3 runs an old agent")
	}
	// n3 upgrades: its earlier acknowledgement (as an old agent) does not count
	e.srv.setMeshCap(ctx, "n3", true)
	if p = plan(); p["n1"].Exports[0].RequireToken {
		t.Fatal("enforced before n3 acknowledged as a new agent")
	}
	ack(p, "n1", "n2", "n3")
	if p = plan(); !p["n1"].Exports[0].RequireToken {
		t.Fatalf("not enforced with every node ready: %+v", p["n1"].Exports[0])
	}
	// A server added to the project (known, capable, no forwarder for s1 yet) and a brand-new
	// service on known nodes do not walk enforcement back: their forwarders start with tokens.
	e.srv.setMeshCap(ctx, "n4", true)
	e.srv.recordMeshAck(ctx, "n4", &api.MeshPayload{})
	base := mk
	mk = func() map[string]*api.MeshPayload {
		m := base()
		m["n4"] = &api.MeshPayload{TailnetIP: "100.64.0.5", Imports: []api.MeshImport{{ServiceID: "s1", ProjectID: "p1", Name: "db", ServerID: "n1",
			Ports: []api.MeshPort{{Port: 5432, Protocol: "tcp", Target: "100.64.0.2:41000"}}}},
			Exports: []api.MeshExport{{ServiceID: "s2", ProjectID: "p1", Name: "cache", Ports: []api.MeshPort{{Port: 6379, Protocol: "tcp", HostPort: 41001}}}}}
		m["n2"].Imports = append(m["n2"].Imports, api.MeshImport{ServiceID: "s2", ProjectID: "p1", Name: "cache", ServerID: "n4",
			Ports: []api.MeshPort{{Port: 6379, Protocol: "tcp", Target: "100.64.0.5:41001"}}})
		return m
	}
	p = plan()
	if !p["n1"].Exports[0].RequireToken || p["n4"].Imports[0].Token != tok {
		t.Fatalf("new importer walked enforcement back: %+v %+v", p["n1"].Exports[0], p["n4"].Imports[0])
	}
	if x := p["n4"].Exports[0]; x.Token != tok || !x.RequireToken || p["n2"].Imports[1].Token != tok {
		t.Fatalf("new service not enforced from the start: %+v %+v", x, p["n2"].Imports[1])
	}
	ack(p, "n1", "n2", "n3", "n4")
	if p = plan(); !p["n1"].Exports[0].RequireToken || !p["n4"].Exports[0].RequireToken {
		t.Fatal("enforcement lost after acknowledgements")
	}
	// a downgrade drops the node's acknowledgements at once
	e.srv.setMeshCap(ctx, "n2", false)
	if p = plan(); p["n1"].Exports[0].RequireToken {
		t.Fatal("still enforced after n2 was downgraded")
	}
	if x := p["n4"].Exports[0]; x.RequireToken || x.Token != tok {
		t.Fatalf("s2 still enforced with its importer n2 downgraded: %+v", x)
	}
	// exporter downgraded: no tokens anywhere for its service
	e.srv.setMeshCap(ctx, "n1", false)
	p = plan()
	if p["n1"].Exports[0].Token != "" || p["n2"].Imports[0].Token != "" || p["n3"].Imports[0].Token != "" || p["n4"].Imports[0].Token != "" {
		t.Fatalf("tokens left after the exporter was downgraded: %+v", p)
	}
	// removed nodes are pruned from the state
	p = map[string]*api.MeshPayload{"n1": {}}
	e.srv.applyMeshTokens(ctx, p)
	n := e.srv.netst()
	n.tokMu.Lock()
	_, has := n.tok.Caps["n2"]
	n.tokMu.Unlock()
	if has {
		t.Fatal("caps of a removed node kept")
	}
}
