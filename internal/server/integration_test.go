package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atoll-sh/atoll/internal/api"
	"github.com/coder/websocket"
)

// ---------- test harness ----------

type testEnv struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
	hc  *http.Client // session client (cookie jar)
	cf  *fakeCloudflare
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	cf := &fakeCloudflare{records: map[string]string{}}
	cfts := httptest.NewServer(cf)
	t.Cleanup(cfts.Close)
	srv, err := New(Config{DataDir: t.TempDir(), CloudflareBaseURL: cfts.URL, DeployWaitTimeout: 10 * time.Second,
		VerifyTimeout: 10 * time.Second, TransferTimeout: 10 * time.Second, RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.CloseClientConnections()
		ts.Close()
		srv.Close()
	})
	jar, _ := cookiejar.New(nil)
	e := &testEnv{t: t, srv: srv, ts: ts, hc: &http.Client{Jar: jar, Timeout: 20 * time.Second}, cf: cf}
	return e
}

// do sends a JSON request with the session client and decodes the answer into out.
func (e *testEnv) do(method, path string, body, out any) int {
	e.t.Helper()
	return e.doWith(e.hc, "", method, path, body, out)
}

func (e *testEnv) doWith(hc *http.Client, bearer, method, path string, body, out any) int {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := hc.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

func (e *testEnv) mustDo(method, path string, body, out any, want int) {
	e.t.Helper()
	var raw json.RawMessage
	code := e.do(method, path, body, &raw)
	if code != want {
		e.t.Fatalf("%s %s: status %d, want %d: %s", method, path, code, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *testEnv) setup() {
	e.mustDo("POST", "/api/v1/auth/setup", map[string]string{"username": "admin", "password": "correct-horse"}, nil, 200)
}

// ---------- fake Cloudflare ----------

type fakeCloudflare struct {
	mu      sync.Mutex
	records map[string]string // hostname -> content
	created []string
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(result any) {
		raw, _ := json.Marshal(result)
		json.NewEncoder(w).Encode(map[string]any{"success": true, "errors": []any{}, "result": json.RawMessage(raw)})
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/zones":
		reply([]map[string]string{{"id": "zone1", "name": r.URL.Query().Get("name")}})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/dns_records"):
		name := r.URL.Query().Get("name")
		out := []map[string]any{}
		if c, ok := f.records[name]; ok {
			out = append(out, map[string]any{"id": "rec-" + name, "type": "A", "name": name, "content": c, "proxied": true})
		}
		reply(out)
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/dns_records"):
		var rec struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		json.NewDecoder(r.Body).Decode(&rec)
		f.records[rec.Name] = rec.Content
		f.created = append(f.created, rec.Name)
		reply(map[string]any{"id": "rec-" + rec.Name, "name": rec.Name, "content": rec.Content})
	case r.Method == "PUT":
		reply(map[string]any{"id": "rec-x"})
	default:
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 404, "message": "not found"}}})
	}
}

// ---------- fake agent ----------

type fakeAgent struct {
	t     *testing.T
	ws    *websocket.Conn
	mu    sync.Mutex
	msgs  []api.Envelope // received requests (payload as json.RawMessage)
	ctrs  map[string]api.ContainerState
	inv   api.ZeaburInventory
	dbBad bool // answer zeabur.db with match:false
	wmu   sync.Mutex
}

func startFakeAgent(t *testing.T, e *testEnv, inv api.ZeaburInventory) (*fakeAgent, string) {
	t.Helper()
	var jt api.JoinToken
	e.mustDo("POST", "/api/v1/servers/join-token", nil, &jt, 201)
	hello := api.HelloPayload{AgentVersion: "test", Hostname: "zeabur-box", OS: "linux", Arch: "amd64", CPUCores: 2, MemMB: 4096, DiskGB: 80, PublicIPv4: "203.0.113.7"}
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
	a := &fakeAgent{t: t, ws: ws, ctrs: map[string]api.ContainerState{}, inv: inv}
	a.send(ctx, api.Envelope{Type: api.MsgHello, Payload: hello})
	go a.loop(ctx)
	go func() { // periodic status, like the real agent
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				a.sendStatus(ctx)
			}
		}
	}()
	// wait until online
	deadline := time.Now().Add(5 * time.Second)
	for !e.srv.hub.Online(jr.NodeID) {
		if time.Now().After(deadline) {
			t.Fatal("agent never came online")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return a, jr.NodeID
}

func (a *fakeAgent) send(ctx context.Context, env api.Envelope) {
	raw, _ := json.Marshal(env)
	a.wmu.Lock()
	defer a.wmu.Unlock()
	a.ws.Write(ctx, websocket.MessageText, raw)
}

func (a *fakeAgent) sendStatus(ctx context.Context) {
	a.mu.Lock()
	cs := []api.ContainerState{}
	for _, c := range a.ctrs {
		cs = append(cs, c)
	}
	a.mu.Unlock()
	a.send(ctx, api.Envelope{Type: api.MsgStatus, Payload: api.StatusPayload{Containers: cs}})
}

func (a *fakeAgent) received(typ string) []json.RawMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []json.RawMessage
	for _, m := range a.msgs {
		if m.Type == typ {
			out = append(out, m.Payload.(json.RawMessage))
		}
	}
	return out
}

func (a *fakeAgent) loop(ctx context.Context) {
	for {
		_, data, err := a.ws.Read(ctx)
		if err != nil {
			return
		}
		var env inEnvelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		a.mu.Lock()
		a.msgs = append(a.msgs, api.Envelope{Type: env.Type, ID: env.ID, Payload: env.Payload})
		a.mu.Unlock()
		go a.answer(ctx, env)
	}
}

func (a *fakeAgent) answer(ctx context.Context, env inEnvelope) {
	res := api.ResultPayload{OK: true}
	switch env.Type {
	case api.MsgApply:
		var p api.ApplyPayload
		json.Unmarshal(env.Payload, &p)
		a.mu.Lock()
		a.ctrs[p.Spec.ServiceID] = api.ContainerState{ServiceID: p.Spec.ServiceID, DeploymentID: p.Spec.DeploymentID, State: "running", StartedAt: time.Now().UTC()}
		a.mu.Unlock()
		a.send(ctx, api.Envelope{Type: api.MsgResult, ID: env.ID, Payload: res})
		a.send(ctx, api.Envelope{Type: api.MsgDeploy, Payload: api.DeployEvent{DeploymentID: p.Spec.DeploymentID, ServiceID: p.Spec.ServiceID, Status: "running"}})
		a.sendStatus(ctx)
		return
	case api.MsgAction:
		var p api.ActionPayload
		json.Unmarshal(env.Payload, &p)
		a.mu.Lock()
		c := a.ctrs[p.ServiceID]
		c.ServiceID = p.ServiceID
		c.State = map[string]string{"stop": "exited", "start": "running", "restart": "running"}[p.Action]
		a.ctrs[p.ServiceID] = c
		a.mu.Unlock()
	case api.MsgZeaburScan:
		inv := a.inv
		inv.ScannedAt = time.Now().UTC()
		raw, _ := json.Marshal(inv)
		res.Output = string(raw)
	case api.MsgZeaburImage:
		var p api.ZeaburImagePayload
		json.Unmarshal(env.Payload, &p)
		res.Output = "atoll-import/" + p.ServiceID + ":0123456789ab"
	case api.MsgZeaburVolume:
		res.Output = `{"method":"copy","bytes":2048,"files":3,"dest":"/var/lib/atoll/volumes/x"}`
	case api.MsgZeaburDB:
		a.mu.Lock()
		bad := a.dbBad
		a.mu.Unlock()
		out := map[string]any{"kind": "postgres", "bytes": 4096, "source_counts": map[string]int{"app.public.users": 42, "app.public.posts": 7},
			"target_counts": map[string]int{"app.public.users": 42, "app.public.posts": 7}, "match": true}
		if bad {
			out["target_counts"] = map[string]int{"app.public.users": 41, "app.public.posts": 7}
			out["match"] = false
			out["mismatches"] = []string{"app.public.users: 42 != 41"}
		}
		raw, _ := json.Marshal(out)
		res.Output = string(raw)
	case api.MsgZeaburScale:
		res.Output = `{}`
	case api.MsgZeaburIngress:
		res.Output = "ok"
	case api.MsgZeaburUninstall:
		res.Output = `{"script_ok":true,"backups":["/var/lib/atoll/zeabur-backup/a.tgz"]}`
	}
	a.send(ctx, api.Envelope{Type: api.MsgResult, ID: env.ID, Payload: res})
}

// ---------- tests ----------

func TestHealthzAndSPA(t *testing.T) {
	e := newTestEnv(t)
	var h map[string]string
	if code := e.do("GET", "/healthz", nil, &h); code != 200 || h["status"] != "ok" {
		t.Fatalf("healthz %d %v", code, h)
	}
	resp, err := http.Get(e.ts.URL + "/projects/prj_x")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "<html") {
		t.Fatalf("spa fallback: %d %s", resp.StatusCode, b)
	}
	var eb api.ErrorBody
	if code := e.do("GET", "/api/v1/does-not-exist", nil, &eb); code != 401 && code != 404 {
		t.Fatalf("unknown api path: %d", code)
	}
	if code := e.do("GET", "/downloads/atoll-agent-linux-amd64", nil, nil); code != 404 {
		t.Fatalf("download without binary: %d", code)
	}
}

func TestProjectDeployResolvesEnv(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	var api1, web api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "api", "kind": "image", "image": "nginx:1.27",
		"ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}},
		"env":   []api.EnvVar{{Key: "GREETING", Value: "hello", Exposed: true}},
	}, &api1, 201)
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "web", "kind": "image", "image": "nginx:1.27",
		"ports": []api.Port{{Name: "web", ContainerPort: 8080, Protocol: "http"}},
		"env":   []api.EnvVar{{Key: "MSG", Value: "${GREETING}@${API_HOST}:${PORT}"}},
	}, &web, 201)
	// find web's apply
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, raw := range agent.received(api.MsgApply) {
			var ap api.ApplyPayload
			json.Unmarshal(raw, &ap)
			if ap.Spec.ServiceID == web.ID {
				if got := ap.Spec.Env["MSG"]; got != "hello@api:8080" {
					t.Fatalf("resolved MSG = %q", got)
				}
				if ap.Spec.Network != "atoll-"+p.ID {
					t.Fatalf("network %q", ap.Spec.Network)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no apply for web service")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func zeaburInventory() api.ZeaburInventory {
	const ns = "environment-abc123"
	return api.ZeaburInventory{K3s: true, K3sVersion: "v1.30.4+k3s1", IngressRunning: true, DiskFreeGB: 50, MemFreeMB: 2048,
		Projects: []api.ZeaburProject{{Namespace: ns, ProjectID: "p1", ProjectName: "blog", EnvironmentName: "production",
			Services: []api.ZeaburService{
				{ServiceID: "aaaaaaaaaaaaaaaaaaaaaaaa", Name: "postgresql", Workload: "statefulset/service-aaaaaaaaaaaaaaaaaaaaaaaa", Replicas: 1,
					Role: "database", Kind: "postgres", DataStore: "postgres", Image: "postgres:16", ImageID: "docker.io/library/postgres@sha256:" + strings.Repeat("1", 64),
					Ports:       []api.Port{{Name: "database", ContainerPort: 5432, Protocol: "tcp"}},
					Env:         []api.EnvVar{{Key: "POSTGRES_PASSWORD", Value: "s3cret-pw", Secret: true}, {Key: "POSTGRES_USER", Value: "root"}, {Key: "ZEABUR_SERVICE_ID", Value: "aaaaaaaaaaaaaaaaaaaaaaaa"}},
					Volumes:     []api.ZeaburVolume{{PVC: "data-service-aaaaaaaaaaaaaaaaaaaaaaaa", MountPath: "/var/lib/postgresql/data", HostDir: "/var/lib/rancher/k3s/storage/pvc-1_" + ns + "_data", Bytes: 1 << 20}},
					K8sServices: []string{"service-aaaaaaaaaaaaaaaaaaaaaaaa"}, Domains: []string{}},
				{ServiceID: "bbbbbbbbbbbbbbbbbbbbbbbb", Name: "web", Workload: "deployment/service-bbbbbbbbbbbbbbbbbbbbbbbb", Replicas: 1,
					Role: "app", Kind: "image", Image: "registry.zeabur.cloud/u/web:latest", ImageID: "sha256:" + strings.Repeat("2", 64), ZeaburRegistry: true,
					Ports: []api.Port{{Name: "web", ContainerPort: 8080, Protocol: "http"}},
					Env: []api.EnvVar{
						{Key: "DATABASE_URL", Value: "postgresql://root:s3cret-pw@postgresql.zeabur.internal:5432/zeabur", Secret: true},
						{Key: "PUBLIC_URL", Value: "https://myblog.zeabur.app"},
						{Key: "ZEABUR_PROJECT_ID", Value: "p1"},
						{Key: "ZEABUR_WEB_URL", Value: "https://myblog.zeabur.app"},
					},
					Volumes:     []api.ZeaburVolume{{PVC: "uploads-service-bbbbbbbbbbbbbbbbbbbbbbbb", MountPath: "/app/uploads", HostDir: "/var/lib/rancher/k3s/storage/pvc-2_" + ns + "_uploads", Bytes: 4096}},
					K8sServices: []string{"service-bbbbbbbbbbbbbbbbbbbbbbbb"}, Domains: []string{"myblog.zeabur.app", "blog.example.org"}},
			}}},
	}
}

func waitJob(t *testing.T, e *testEnv, id string, want ...string) api.ImportJob {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var j api.ImportJob
		e.mustDo("GET", "/api/v1/imports/"+id, nil, &j, 200)
		for _, w := range want {
			if j.Status == w {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s stuck in %s (want %v): %+v", id, j.Status, want, j.Steps)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestZeaburTakeover(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, zeaburInventory())

	// scan: env values masked
	var inv api.ZeaburInventory
	e.mustDo("GET", "/api/v1/servers/"+nodeID+"/zeabur", nil, &inv, 200)
	if len(inv.Projects) != 1 || len(inv.Projects[0].Services) != 2 {
		t.Fatalf("inventory %+v", inv)
	}
	raw, _ := json.Marshal(inv)
	if strings.Contains(string(raw), "s3cret-pw") {
		t.Fatal("scan leaked a secret value")
	}

	req := api.ZeaburLocalImportRequest{ServerID: nodeID, Namespace: "environment-abc123"}
	// *.zeabur.app hosts need base_domain + Cloudflare token
	var eb api.ErrorBody
	if code := e.do("POST", "/api/v1/imports/zeabur-local", req, &eb); code != 400 || !strings.Contains(eb.Error, "设置") {
		t.Fatalf("missing settings: %d %+v", code, eb)
	}
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "cf-token", CloudflareZoneID: "zone1"}, nil, 200)

	var job api.ImportJob
	e.mustDo("POST", "/api/v1/imports/zeabur-local", req, &job, 201)
	if job.Mode != "zeabur-local" || job.ServerID != nodeID || job.Namespace != "environment-abc123" || job.Status != "planned" {
		t.Fatalf("job fields: %+v", job)
	}
	if len(job.Steps) != 10 {
		t.Fatalf("want 10 steps, got %d", len(job.Steps))
	}
	// env translated (check in the service's revealed env)
	var proj api.Project
	e.mustDo("GET", "/api/v1/projects/"+job.ProjectID, nil, &proj, 200)
	var webID string
	for _, sv := range proj.Services {
		if sv.Name == "web" {
			webID = sv.ID
		}
	}
	if webID == "" {
		t.Fatalf("no web service: %+v", proj.Services)
	}
	var env []api.EnvVar
	e.mustDo("GET", "/api/v1/services/"+webID+"/env?reveal=1", nil, &env, 200)
	got := map[string]string{}
	for _, v := range env {
		got[v.Key] = v.Value
	}
	if got["DATABASE_URL"] != "postgresql://root:s3cret-pw@postgresql:5432/zeabur" {
		t.Fatalf("DATABASE_URL = %q", got["DATABASE_URL"])
	}
	if got["PUBLIC_URL"] != "https://myblog.example.com" {
		t.Fatalf("PUBLIC_URL = %q", got["PUBLIC_URL"])
	}
	if _, ok := got["ZEABUR_PROJECT_ID"]; ok {
		t.Fatal("ZEABUR_PROJECT_ID not dropped")
	}
	warn := strings.Join(job.Plan.Warnings, "\n")
	if !strings.Contains(warn, "OAuth") || strings.Contains(warn, "s3cret-pw") {
		t.Fatalf("warnings: %s", warn)
	}
	if !strings.Contains(warn, "rewrote hostnames in DATABASE_URL") {
		t.Fatalf("rewrite keys not recorded: %s", warn)
	}
	// cleanup before cutover is refused
	if code := e.do("POST", "/api/v1/imports/"+job.ID+"/cleanup", api.ZeaburCleanupRequest{Confirm: "zeabur-box"}, nil); code != 409 {
		t.Fatalf("early cleanup: %d", code)
	}

	// rehearse
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rehearse", nil, nil, 200)
	job = waitJob(t, e, job.ID, "rehearsed", "failed")
	if job.Status != "rehearsed" {
		t.Fatalf("rehearse failed: %+v", job.Steps)
	}
	if len(job.Verify) != 2 || job.Verify[0].SourceRows != job.Verify[0].TargetRows {
		t.Fatalf("verify %+v", job.Verify)
	}
	if n := len(agent.received(api.MsgZeaburImage)); n != 1 {
		t.Fatalf("zeabur.image calls: %d", n)
	}
	var web api.Service
	e.mustDo("GET", "/api/v1/services/"+webID, nil, &web, 200)
	if !strings.HasPrefix(web.Image, "atoll-import/") {
		t.Fatalf("web image not switched to the exported image: %s", web.Image)
	}
	// ingress held during rehearsal: no routes pushed for this project
	for _, raw := range agent.received(api.MsgIngress) {
		var ing api.IngressPayload
		json.Unmarshal(raw, &ing)
		if len(ing.Routes) > 0 {
			t.Fatalf("routes pushed during rehearsal: %+v", ing.Routes)
		}
	}
	if len(agent.received(api.MsgZeaburScale)) != 0 {
		t.Fatal("rehearsal must not touch Zeabur workloads")
	}

	// cutover
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cutover", nil, nil, 200)
	job = waitJob(t, e, job.ID, "live", "rolled_back", "failed")
	if job.Status != "live" {
		t.Fatalf("cutover: %s %+v", job.Status, job.Steps)
	}
	scales := agent.received(api.MsgZeaburScale)
	if len(scales) != 2 {
		t.Fatalf("scale calls %d", len(scales))
	}
	parks := agent.received(api.MsgZeaburIngress)
	if len(parks) != 1 || !strings.Contains(string(parks[0]), `"park":true`) {
		t.Fatalf("ingress park: %s", parks)
	}
	ings := agent.received(api.MsgIngress)
	var last api.IngressPayload
	json.Unmarshal(ings[len(ings)-1], &last)
	if len(last.Routes) != 2 {
		t.Fatalf("routes after cutover: %+v", last.Routes)
	}
	e.cf.mu.Lock()
	created := append([]string(nil), e.cf.created...)
	e.cf.mu.Unlock()
	if len(created) != 1 || created[0] != "myblog.example.com" {
		t.Fatalf("cloudflare records created: %v", created)
	}

	// rollback
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rollback", nil, nil, 200)
	job = waitJob(t, e, job.ID, "rolled_back", "failed")
	if job.Status != "rolled_back" {
		t.Fatalf("rollback: %+v", job.Steps)
	}
	scales = agent.received(api.MsgZeaburScale)
	restored := 0
	for _, raw := range scales {
		var p api.ZeaburScalePayload
		json.Unmarshal(raw, &p)
		if p.Replicas == -1 {
			restored++
		}
	}
	if restored != 2 {
		t.Fatalf("scale-back calls: %d", restored)
	}
	parks = agent.received(api.MsgZeaburIngress)
	if len(parks) != 2 || !strings.Contains(string(parks[1]), `"park":false`) {
		t.Fatalf("ingress restore: %s", parks)
	}

	// cut over again, then clean up (session only, exact server name)
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cutover", nil, nil, 200)
	job = waitJob(t, e, job.ID, "live", "rolled_back", "failed")
	if job.Status != "live" {
		t.Fatalf("second cutover: %+v", job.Steps)
	}
	if code := e.do("POST", "/api/v1/imports/"+job.ID+"/cleanup", api.ZeaburCleanupRequest{Confirm: "wrong"}, nil); code != 400 {
		t.Fatalf("cleanup wrong confirm: %d", code)
	}
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cleanup", api.ZeaburCleanupRequest{Confirm: "zeabur-box", KeepBackup: true}, nil, 200)
	job = waitJob(t, e, job.ID, "cleaned", "live")
	if job.Status != "cleaned" {
		t.Fatalf("cleanup: %+v", job)
	}
	if code := e.do("POST", "/api/v1/imports/"+job.ID+"/rollback", nil, nil); code != 409 {
		t.Fatalf("rollback after cleanup: %d", code)
	}
}

func TestZeaburCutoverAutoRollback(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, zeaburInventory())
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "cf-token", CloudflareZoneID: "zone1"}, nil, 200)
	var job api.ImportJob
	e.mustDo("POST", "/api/v1/imports/zeabur-local", api.ZeaburLocalImportRequest{ServerID: nodeID, Namespace: "environment-abc123"}, &job, 201)
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rehearse", nil, nil, 200)
	waitJob(t, e, job.ID, "rehearsed")
	agent.mu.Lock()
	agent.dbBad = true
	agent.mu.Unlock()
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cutover", nil, nil, 200)
	job = waitJob(t, e, job.ID, "rolled_back", "failed", "live")
	if job.Status != "rolled_back" {
		t.Fatalf("want automatic rollback, got %s", job.Status)
	}
	failed := false
	for _, s := range job.Steps {
		if s.Name == stepFinalSync && s.Status == "failed" && strings.Contains(s.Detail, "verification failed") {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("final sync step not failed: %+v", job.Steps)
	}
	// app was scaled down and must have been restored; ingress was never parked
	restored := 0
	for _, raw := range agent.received(api.MsgZeaburScale) {
		var p api.ZeaburScalePayload
		json.Unmarshal(raw, &p)
		if p.Replicas == -1 {
			restored++
		}
	}
	if restored != 1 {
		t.Fatalf("restored workloads: %d", restored)
	}
	if n := len(agent.received(api.MsgZeaburIngress)); n != 0 {
		t.Fatalf("ingress touched %d times", n)
	}
}

func TestCutoverWithTokenNeedsConfirmation(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, zeaburInventory())
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "cf-token", CloudflareZoneID: "zone1"}, nil, 200)
	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]string{"name": "ci", "scope": "deploy"}, &tok, 201)
	var job api.ImportJob
	if code := e.doWith(http.DefaultClient, tok.Token, "POST", "/api/v1/imports/zeabur-local", api.ZeaburLocalImportRequest{ServerID: nodeID, Namespace: "environment-abc123"}, &job); code != 201 {
		t.Fatalf("import with PAT: %d", code)
	}
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rehearse", nil, nil, 200)
	waitJob(t, e, job.ID, "rehearsed")
	var c api.Confirmation
	if code := e.doWith(http.DefaultClient, tok.Token, "POST", "/api/v1/imports/"+job.ID+"/cutover", nil, &c); code != 202 || c.Action != "import.cutover" {
		t.Fatalf("cutover with PAT: %d %+v", code, c)
	}
	if code := e.doWith(http.DefaultClient, tok.Token, "POST", "/api/v1/imports/"+job.ID+"/cleanup", api.ZeaburCleanupRequest{Confirm: "zeabur-box"}, nil); code != 403 {
		t.Fatalf("cleanup with PAT: %d", code)
	}
	e.mustDo("POST", fmt.Sprintf("/api/v1/confirmations/%s/approve", c.ID), nil, nil, 200)
	job = waitJob(t, e, job.ID, "live", "rolled_back", "failed")
	if job.Status != "live" {
		t.Fatalf("approved cutover: %+v", job.Steps)
	}
}
