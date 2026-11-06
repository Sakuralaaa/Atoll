package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/coder/websocket"
)

type frame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// fakeServer is the control server side of /agent/v1/connect.
type fakeServer struct {
	t      *testing.T
	token  string
	srv    *httptest.Server
	frames chan frame
	authMu sync.Mutex
	auths  []string
	connMu sync.Mutex
	conn   *websocket.Conn
	ready  chan struct{}
	once   sync.Once
	// log lines read while waiting for something else, in arrival order: a line can
	// overtake the logs.open result, and dropping it made the session test flaky.
	backlog []frame
}

func newFakeServer(t *testing.T, token string) *fakeServer {
	fs := &fakeServer{t: t, token: token, frames: make(chan frame, 1024), ready: make(chan struct{})}
	fs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent/v1/ping" { // control-URL verification before a switch
			if r.Header.Get("Authorization") != "Bearer "+fs.token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"ok":true,"node_id":"srv_test"}`))
			return
		}
		if r.URL.Path != "/agent/v1/connect" {
			http.NotFound(w, r)
			return
		}
		fs.authMu.Lock()
		fs.auths = append(fs.auths, r.Header.Get("Authorization"))
		fs.authMu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+fs.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(64 << 20)
		fs.connMu.Lock()
		fs.conn = c
		fs.connMu.Unlock()
		fs.once.Do(func() { close(fs.ready) })
		for {
			_, b, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var f frame
			if json.Unmarshal(b, &f) == nil {
				fs.frames <- f
			}
		}
	}))
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *fakeServer) send(typ, id string, payload any) {
	fs.t.Helper()
	b, _ := json.Marshal(api.Envelope{Type: typ, ID: id, Payload: payload})
	fs.connMu.Lock()
	c := fs.conn
	fs.connMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		fs.t.Fatal(err)
	}
}

// wait returns the first frame matching pred; non-matching frames are dropped.
func (fs *fakeServer) wait(what string, pred func(frame) bool) frame {
	fs.t.Helper()
	for i, f := range fs.backlog {
		if pred(f) {
			fs.backlog = append(fs.backlog[:i:i], fs.backlog[i+1:]...)
			return f
		}
	}
	timeout := time.After(10 * time.Second)
	for {
		select {
		case f := <-fs.frames:
			if pred(f) {
				return f
			}
			if f.Type == api.MsgLogLine { // stream data is never stale; other frames are
				fs.backlog = append(fs.backlog, f)
			}
		case <-timeout:
			fs.t.Fatalf("timeout waiting for %s", what)
		}
	}
}

func (fs *fakeServer) result(id string) api.ResultPayload {
	fs.t.Helper()
	f := fs.wait("result "+id, func(f frame) bool { return f.Type == api.MsgResult && f.ID == id })
	var r api.ResultPayload
	if err := json.Unmarshal(f.Payload, &r); err != nil {
		fs.t.Fatal(err)
	}
	return r
}

func newTestAgent(t *testing.T, serverURL, token string) (*agent.Agent, *agenttest.Fake) {
	t.Helper()
	f := agenttest.New()
	cfg := &agent.Config{ServerURL: serverURL, NodeID: "srv_test", NodeToken: token, DataDir: t.TempDir()}
	a := agent.New(cfg, f, nil, nil)
	a.Mgr.SettleTime = 0
	a.StatusCheck = 20 * time.Millisecond
	a.MetricsEvery = time.Hour
	a.MaxBackoff = 50 * time.Millisecond
	a.HelloFunc = func(context.Context) api.HelloPayload {
		return api.HelloPayload{NodeID: cfg.NodeID, AgentVersion: agent.Version, Hostname: "vps1", OS: "linux", Arch: "amd64", DockerVersion: "28.5.1-fake"}
	}
	a.Ingress.SetGuards()
	a.Ingress.LoadConfig = func(context.Context, []byte) error { return nil }
	return a, f
}

// webRunning reports whether f is a status frame listing svc_web as running.
func webRunning(f frame) bool {
	if f.Type != api.MsgStatus {
		return false
	}
	var st api.StatusPayload
	json.Unmarshal(f.Payload, &st)
	for _, c := range st.Containers {
		if c.ServiceID == "svc_web" && c.State == "running" {
			return true
		}
	}
	return false
}

func TestWebSocketSession(t *testing.T) {
	const token = "atl_node_secret"
	fs := newFakeServer(t, token)
	a, f := newTestAgent(t, fs.srv.URL, token)
	f.LogLines = []agent.LogRecord{
		{At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Stream: "stdout", Line: "listening on :3000"},
		{At: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), Stream: "stderr", Line: "warn: x"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// hello is the first frame, sent with the bearer token.
	hf := fs.wait("hello", func(frame) bool { return true })
	if hf.Type != api.MsgHello {
		t.Fatalf("first frame %q", hf.Type)
	}
	var hello api.HelloPayload
	json.Unmarshal(hf.Payload, &hello)
	if hello.NodeID != "srv_test" || hello.Hostname != "vps1" {
		t.Fatalf("hello %+v", hello)
	}
	fs.authMu.Lock()
	if len(fs.auths) == 0 || fs.auths[0] != "Bearer "+token {
		t.Fatalf("auth header %v", fs.auths)
	}
	fs.authMu.Unlock()

	// apply -> deploy events, then a result.
	spec := webSpec()
	spec.RegistryAuth = nil
	fs.send(api.MsgApply, "r1", api.ApplyPayload{Spec: spec})
	var statuses []string
	sawWebRunning := false
	for {
		fr := fs.wait("deploy/result", func(f frame) bool {
			if webRunning(f) {
				sawWebRunning = true
			}
			return f.Type == api.MsgDeploy || (f.Type == api.MsgResult && f.ID == "r1")
		})
		if fr.Type == api.MsgResult {
			var r api.ResultPayload
			json.Unmarshal(fr.Payload, &r)
			if !r.OK {
				t.Fatalf("apply failed: %+v", r)
			}
			break
		}
		var d api.DeployEvent
		json.Unmarshal(fr.Payload, &d)
		if d.DeploymentID != "dep_1" || d.ServiceID != "svc_web" {
			t.Fatalf("deploy event %+v", d)
		}
		statuses = append(statuses, d.Status)
	}
	if len(statuses) == 0 || statuses[len(statuses)-1] != "running" {
		t.Fatalf("deploy statuses %v", statuses)
	}

	// status reflects the new container. The change-driven status frame can arrive
	// before the apply result (and be skipped by the loop above), so it may already be seen.
	if !sawWebRunning {
		fs.wait("status with svc_web", webRunning)
	}

	// exec
	fs.send(api.MsgExec, "r2", api.ExecPayload{ServiceID: "svc_web", Cmd: []string{"echo", "hi"}})
	if r := fs.result("r2"); !r.OK || !strings.Contains(r.Output, "ran echo hi") {
		t.Fatalf("exec %+v", r)
	}
	f.ExecFunc = func(_ string, _ []string, _ io.Reader, w io.Writer) int { io.WriteString(w, "boom"); return 3 }
	fs.send(api.MsgExec, "r3", api.ExecPayload{ServiceID: "svc_web", Cmd: []string{"false"}})
	if r := fs.result("r3"); r.OK || !strings.Contains(r.Error, "exit code 3") || r.Output != "boom" {
		t.Fatalf("exec failure %+v", r)
	}
	fs.send(api.MsgExec, "r4", api.ExecPayload{ServiceID: "svc_missing", Cmd: []string{"ls"}})
	if r := fs.result("r4"); r.OK {
		t.Fatal("exec on missing service must fail")
	}

	// logs
	fs.send(api.MsgLogsOpen, "r5", api.LogsOpenPayload{StreamID: "st1", ServiceID: "svc_web", Tail: 10, Follow: true})
	if r := fs.result("r5"); !r.OK {
		t.Fatalf("logs.open %+v", r)
	}
	var lines []api.LogLine
	for len(lines) < 2 {
		fr := fs.wait("log", func(f frame) bool { return f.Type == api.MsgLogLine })
		var le api.LogEvent
		json.Unmarshal(fr.Payload, &le)
		if le.StreamID != "st1" {
			t.Fatalf("stream id %q", le.StreamID)
		}
		lines = append(lines, le.Line)
	}
	if lines[0].Line != "listening on :3000" || lines[1].Stream != "stderr" || lines[0].ServiceID != "svc_web" {
		t.Fatalf("log lines %+v", lines)
	}
	fs.send(api.MsgLogsClose, "r6", api.LogsOpenPayload{StreamID: "st1"})
	if r := fs.result("r6"); !r.OK {
		t.Fatalf("logs.close %+v", r)
	}

	// unknown types and bad payloads come back as errors, not crashes.
	fs.send("nope", "r7", nil)
	if r := fs.result("r7"); r.OK || !strings.Contains(r.Error, "unsupported") {
		t.Fatalf("unknown %+v", r)
	}
	fs.send(api.MsgApply, "r8", nil)
	if r := fs.result("r8"); r.OK {
		t.Fatal("missing payload accepted")
	}

	// action stop -> status shows exited. The status frame is change-driven and can come
	// before the action's result; fs.result would drop it and the wait below would then
	// time out (seen in CI), so watch for both in one loop.
	fs.send(api.MsgAction, "r9", api.ActionPayload{ServiceID: "svc_web", Action: "stop"})
	exited := func(f frame) bool {
		if f.Type != api.MsgStatus {
			return false
		}
		var st api.StatusPayload
		json.Unmarshal(f.Payload, &st)
		return len(st.Containers) == 1 && st.Containers[0].State == "exited"
	}
	gotResult, gotExited := false, false
	for !gotResult || !gotExited {
		fr := fs.wait("stop result / status exited", func(f frame) bool {
			return (!gotResult && f.Type == api.MsgResult && f.ID == "r9") || (!gotExited && exited(f))
		})
		if fr.Type == api.MsgResult {
			var r api.ResultPayload
			json.Unmarshal(fr.Payload, &r)
			if !r.OK {
				t.Fatalf("stop %+v", r)
			}
			gotResult = true
			continue
		}
		gotExited = true
	}
}

func TestSessionRejectedTokenRetries(t *testing.T) {
	fs := newFakeServer(t, "right")
	a, _ := newTestAgent(t, fs.srv.URL, "wrong")
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	a.Run(ctx) // must return when ctx ends, retrying meanwhile
	fs.authMu.Lock()
	defer fs.authMu.Unlock()
	if len(fs.auths) < 2 {
		t.Fatalf("expected reconnect attempts, got %d", len(fs.auths))
	}
	for _, h := range fs.auths {
		if h != "Bearer wrong" {
			t.Fatalf("header %q", h)
		}
	}
}

func TestDeployEventsQueuedWhileOffline(t *testing.T) {
	const token = "tok"
	fs := newFakeServer(t, token)
	a, _ := newTestAgent(t, fs.srv.URL, token)
	// Apply before any connection: deploy events must be queued and flushed after hello.
	spec := dbSpec("svc_db")
	if err := a.Mgr.Apply(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	if f := fs.wait("hello", func(frame) bool { return true }); f.Type != api.MsgHello {
		t.Fatalf("first frame %s", f.Type)
	}
	f := fs.wait("queued deploy", func(f frame) bool { return f.Type == api.MsgDeploy })
	var d api.DeployEvent
	json.Unmarshal(f.Payload, &d)
	if d.ServiceID != "svc_db" {
		t.Fatalf("deploy %+v", d)
	}
}
