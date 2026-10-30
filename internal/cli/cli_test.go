package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// fakePanel is a tiny in-memory Atoll API.
type fakePanel struct {
	t         *testing.T
	mu        sync.Mutex
	calls     []string          // "METHOD /path"
	bodies    map[string][]byte // last body per "METHOD /path"
	services  []api.Service
	cfStatus  string // status returned by GET /confirmations
	impStatus string
}

func init() { importPoll = 10 * time.Millisecond }

func newFake(t *testing.T) (*fakePanel, *httptest.Server) {
	f := &fakePanel{t: t, bodies: map[string][]byte{}, cfStatus: "pending", impStatus: "live"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakePanel) called(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == key {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (f *fakePanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer atl_pat_test" {
		writeJSON(w, 401, api.ErrorBody{Error: "unauthorized"})
		return
	}
	key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, key)
	f.bodies[key] = body
	f.mu.Unlock()
	cf := api.Confirmation{ID: "cfm_aaaaaaaaaaaa", Action: "service.migrate", Summary: "move web to s2", Status: "pending"}
	switch key {
	case "GET /auth/me":
		writeJSON(w, 200, map[string]any{"username": "chen"})
	case "GET /servers":
		writeJSON(w, 200, []api.Server{{ID: "srv_1", Name: "vps1", PublicIPv4: "203.0.113.7"}, {ID: "srv_2", Name: "s2"}})
	case "GET /servers/srv_1":
		writeJSON(w, 200, api.Server{ID: "srv_1", Name: "vps1", PublicIPv4: "203.0.113.7"})
	case "GET /projects":
		writeJSON(w, 200, []api.Project{{ID: "prj_1", Name: "web", ServerID: "srv_1"}})
	case "GET /projects/prj_1":
		svcs := f.services
		if svcs == nil {
			svcs = []api.Service{}
		}
		writeJSON(w, 200, api.Project{ID: "prj_1", Name: "web", ServerID: "srv_1", Services: svcs})
	case "GET /projects/prj_1/services":
		writeJSON(w, 200, append([]api.Service{}, f.services...))
	case "POST /projects/prj_1/services":
		var s api.Service
		json.Unmarshal(body, &s)
		s.ID = "svc_new"
		writeJSON(w, 201, s)
	case "GET /services/svc_1/env", "GET /services/svc_new/env":
		writeJSON(w, 200, []api.EnvVar{})
	case "PUT /services/svc_1/env", "PUT /services/svc_new/env":
		w.Write(body)
	case "PATCH /services/svc_1":
		s := f.services[0]
		var p map[string]any
		json.Unmarshal(body, &p)
		if img, ok := p["image"].(string); ok {
			s.Image = img
		}
		writeJSON(w, 200, s)
	case "POST /projects/prj_1/migrate":
		writeJSON(w, 202, cf)
	case "GET /confirmations":
		cf.Status = f.cfStatus
		writeJSON(w, 200, []api.Confirmation{cf})
	case "GET /imports/imp_1":
		f.mu.Lock()
		st := f.impStatus
		f.mu.Unlock()
		writeJSON(w, 200, api.ImportJob{ID: "imp_1", Status: st, Plan: api.ImportPlan{TargetServer: "srv_1", ProjectName: "shop"}})
	case "POST /imports/imp_1/cleanup":
		f.mu.Lock()
		f.impStatus = "cleaned"
		f.mu.Unlock()
		writeJSON(w, 200, api.ImportJob{ID: "imp_1", Status: "cleaning"})
	case "POST /imports/imp_1/rehearse":
		f.mu.Lock()
		f.impStatus = "rehearsed"
		f.mu.Unlock()
		writeJSON(w, 200, api.ImportJob{ID: "imp_1", Status: "rehearsing", Steps: []api.ImportStep{{Name: "images", Status: "running"}}})
	default:
		writeJSON(w, 404, api.ErrorBody{Error: "not found: " + key})
	}
}

type run struct {
	out, err string
	e        error
}

func runCLI(t *testing.T, srvURL, stdin string, args ...string) run {
	t.Helper()
	var out, errb bytes.Buffer
	app := &App{Version: "test", In: strings.NewReader(stdin), Out: &out, Err: &errb}
	root := NewRoot(app)
	cfg := filepath.Join(t.TempDir(), "config.json")
	full := append([]string{"--config", cfg}, args...)
	if srvURL != "" {
		full = append(full, "--url", srvURL, "--token", "atl_pat_test")
	}
	root.SetArgs(full)
	err := root.ExecuteContext(context.Background())
	return run{out.String(), errb.String(), err}
}

func TestLoginWritesConfig0600(t *testing.T) {
	_, srv := newFake(t)
	dir := t.TempDir()
	cfg := filepath.Join(dir, "atoll", "config.json")
	var out bytes.Buffer
	app := &App{Version: "test", In: strings.NewReader(""), Out: &out, Err: io.Discard}
	root := NewRoot(app)
	root.SetArgs([]string{"--config", cfg, "login", "--url", srv.URL + "/", "--token", "atl_pat_test"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("config mode %v, want 0600", st.Mode().Perm())
	}
	c, err := LoadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != srv.URL || c.Token != "atl_pat_test" || c.User != "chen" {
		t.Fatalf("config %+v", c)
	}
	if !strings.Contains(out.String(), "as chen") {
		t.Fatalf("output %q", out.String())
	}
}

func TestLoginBadToken(t *testing.T) {
	_, srv := newFake(t)
	cfg := filepath.Join(t.TempDir(), "config.json")
	app := &App{Version: "test", In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}
	root := NewRoot(app)
	root.SetArgs([]string{"--config", cfg, "login", "--url", srv.URL, "--token", "atl_pat_wrong"})
	if err := root.Execute(); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(cfg); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("config must not be written on failed login")
	}
}

func TestDeployCreates(t *testing.T) {
	f, srv := newFake(t)
	r := runCLI(t, srv.URL, "", "deploy", "ghcr.io/me/app:v1", "-p", "web", "--port", "8080", "-e", "API_TOKEN=x")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if !f.called("POST /projects/prj_1/services") || !f.called("PUT /services/svc_new/env") {
		t.Fatalf("calls %v", f.calls)
	}
	var s api.Service
	json.Unmarshal(f.bodies["POST /projects/prj_1/services"], &s)
	if s.Name != "app" || s.Image != "ghcr.io/me/app:v1" || len(s.Ports) != 1 || s.Ports[0].Name != "web" || s.Ports[0].ContainerPort != 8080 {
		t.Fatalf("service %+v", s)
	}
	var env []api.EnvVar
	json.Unmarshal(f.bodies["PUT /services/svc_new/env"], &env)
	if len(env) != 1 || !env[0].Secret {
		t.Fatalf("env %+v (API_TOKEN must be secret)", env)
	}
	if !strings.Contains(r.out, "created service app") {
		t.Fatalf("out %q", r.out)
	}
}

func TestDeployUpdates(t *testing.T) {
	f, srv := newFake(t)
	f.services = []api.Service{{ID: "svc_1", Name: "app", Kind: "image", Image: "ghcr.io/me/app:v1"}}
	r := runCLI(t, srv.URL, "", "deploy", "ghcr.io/me/app:v2", "-p", "web")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if f.called("POST /projects/prj_1/services") {
		t.Fatal("must not create a second service")
	}
	if !f.called("PATCH /services/svc_1") {
		t.Fatalf("calls %v", f.calls)
	}
	if !strings.Contains(string(f.bodies["PATCH /services/svc_1"]), "app:v2") {
		t.Fatalf("patch %s", f.bodies["PATCH /services/svc_1"])
	}
	if !strings.Contains(r.out, "updated service app") {
		t.Fatalf("out %q", r.out)
	}
}

func TestConfirmationPending(t *testing.T) {
	_, srv := newFake(t)
	r := runCLI(t, srv.URL, "", "migrate", "web", "--to", "s2")
	var pe *pendingError
	if !errors.As(r.e, &pe) {
		t.Fatalf("want pendingError, got %v", r.e)
	}
	if !strings.Contains(pe.Error(), "cfm_aaaaaaaaaaaa") {
		t.Fatalf("message %q should name the confirmation", pe.Error())
	}
	if strings.Contains(r.out, "started") {
		t.Fatal("must not claim the migration started")
	}
}

func TestConfirmationWait(t *testing.T) {
	f, srv := newFake(t)
	f.cfStatus = "approved"
	r := runCLI(t, srv.URL, "", "--wait", "migrate", "web", "--to", "s2")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if !strings.Contains(r.err, "Waiting for approval") || !strings.Contains(r.out, "started") {
		t.Fatalf("out %q err %q", r.out, r.err)
	}
}

func TestConfirmationWaitRejected(t *testing.T) {
	f, srv := newFake(t)
	f.cfStatus = "rejected"
	r := runCLI(t, srv.URL, "", "--wait", "migrate", "web", "--to", "s2")
	if r.e == nil || !strings.Contains(r.e.Error(), "rejected") {
		t.Fatalf("want rejected error, got %v", r.e)
	}
}

func TestZeaburCleanupTypedConfirm(t *testing.T) {
	f, srv := newFake(t)
	r := runCLI(t, srv.URL, "vps\n", "zeabur", "cleanup", "imp_1")
	if r.e == nil || !strings.Contains(r.e.Error(), "does not match") {
		t.Fatalf("want mismatch error, got %v", r.e)
	}
	if f.called("POST /imports/imp_1/cleanup") {
		t.Fatal("cleanup must not be sent when the typed name does not match")
	}
	// The match is exact (case-sensitive), also for --confirm.
	r = runCLI(t, srv.URL, "", "zeabur", "cleanup", "imp_1", "--confirm", "VPS1")
	if r.e == nil || f.called("POST /imports/imp_1/cleanup") {
		t.Fatal("case-mismatched confirm must be refused")
	}
	r = runCLI(t, srv.URL, "vps1\n", "zeabur", "cleanup", "imp_1", "--no-backup")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	var req api.ZeaburCleanupRequest
	json.Unmarshal(f.bodies["POST /imports/imp_1/cleanup"], &req)
	if req.Confirm != "vps1" || req.KeepBackup {
		t.Fatalf("cleanup body %+v", req)
	}
	if !strings.Contains(r.out, "removed from vps1") {
		t.Fatalf("out %q", r.out)
	}
}

func TestZeaburStepNeedsYes(t *testing.T) {
	f, srv := newFake(t)
	r := runCLI(t, srv.URL, "n\n", "zeabur", "rehearse", "imp_1")
	if r.e == nil || f.called("POST /imports/imp_1/rehearse") {
		t.Fatal("answering n must not start the rehearsal")
	}
	r = runCLI(t, srv.URL, "", "zeabur", "rehearse", "imp_1", "--yes")
	if r.e != nil || !f.called("POST /imports/imp_1/rehearse") {
		t.Fatal(r.e, r.err)
	}
	if !strings.Contains(r.out, "Rehearsal finished") || !strings.Contains(r.out, "zeabur cutover imp_1") {
		t.Fatalf("out %q", r.out)
	}
}

func TestMCPDestructiveReturnsPending(t *testing.T) {
	_, srv := newFake(t)
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", ConfigPath: filepath.Join(t.TempDir(), "c.json")}
	NewMCPServer(app) // sets NewClient
	var migrate func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	names := map[string]bool{}
	for _, st := range MCPTools(app) {
		names[st.Tool.Name] = true
		if st.Tool.Name == "migrate" {
			migrate = st.Handler
		}
	}
	for _, n := range []string{"list_servers", "list_projects", "list_services", "deploy", "set_env", "get_logs", "restart", "redeploy", "rollback", "add_domain", "migrate", "get_deployment", "zeabur_scan", "zeabur_import_status"} {
		if !names[n] {
			t.Errorf("missing tool %s", n)
		}
	}
	var req mcp.CallToolRequest
	req.Params.Name = "migrate"
	req.Params.Arguments = map[string]any{"project": "web", "server": "s2"}
	res, err := migrate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || len(res.Content) == 0 {
		t.Fatalf("result %+v", res)
	}
	txt := res.Content[0].(mcp.TextContent).Text
	if !strings.Contains(txt, "PENDING CONFIRMATION cfm_aaaaaaaaaaaa") {
		t.Fatalf("text %q", txt)
	}
}
