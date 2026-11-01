package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

type createBody struct {
	api.Service
	Env []api.EnvVar `json:"env"`
}

func (f *fakePanel) lastCreate(t *testing.T) createBody {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	var b createBody
	if err := json.Unmarshal(f.bodies["POST /projects/prj_1/services"], &b); err != nil {
		t.Fatalf("create body: %v", err)
	}
	return b
}

func TestTemplateKindAliases(t *testing.T) {
	for in, want := range map[string]string{"postgres": "postgres", "PostgreSQL": "postgres", "mysql": "mysql", "redis": "redis", "mongodb": "mongo", "mongo": "mongo"} {
		if got, err := TemplateKind(in); err != nil || got != want {
			t.Errorf("TemplateKind(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := TemplateKind("oracle"); err == nil {
		t.Fatal("unknown template accepted")
	}
}

func TestTemplatesLsCLI(t *testing.T) {
	_, srv := newFake(t)
	r := runCLI(t, srv.URL, "", "templates", "ls")
	if r.e != nil || !strings.Contains(r.out, "postgres") || !strings.Contains(r.out, "postgres:16-alpine") || !strings.Contains(r.out, "database:27017/tcp") {
		t.Fatalf("templates ls: %v\n%s", r.e, r.out)
	}
}

func TestDeployTemplateCLI(t *testing.T) {
	f, srv := newFake(t)
	r := runCLI(t, srv.URL, "", "deploy", "--template", "mongodb", "-p", "web")
	if r.e != nil || !strings.Contains(r.out, "created") {
		t.Fatalf("deploy --template: %v %s %s", r.e, r.out, r.err)
	}
	b := f.lastCreate(t)
	if b.Kind != "mongo" || b.Name != "mongo" || b.Image != "" || len(b.Env) != 0 {
		t.Fatalf("create body %+v", b)
	}

	r = runCLI(t, srv.URL, "", "templates", "deploy", "postgres", "db", "-p", "web", "-e", "POSTGRES_DATABASE=app")
	if r.e != nil {
		t.Fatalf("templates deploy: %v %s", r.e, r.err)
	}
	b = f.lastCreate(t)
	if b.Kind != "postgres" || b.Name != "db" || len(b.Env) != 1 || b.Env[0].Key != "POSTGRES_DATABASE" || b.Env[0].Value != "app" {
		t.Fatalf("create body %+v", b)
	}

	// An existing service of that name is never overwritten.
	f.services = []api.Service{{ID: "svc_1", Name: "db", ProjectID: "prj_1", ServerID: "srv_1"}}
	r = runCLI(t, srv.URL, "", "deploy", "--template", "postgres", "db", "-p", "web")
	if r.e == nil || !strings.Contains(r.e.Error(), "already has a service") {
		t.Fatalf("existing name: %v", r.e)
	}
	if r := runCLI(t, srv.URL, "", "deploy", "--template", "oracle", "-p", "web"); r.e == nil {
		t.Fatal("unknown template accepted")
	}
	if r := runCLI(t, srv.URL, "", "deploy", "--template", "redis", "-p", "web", "--port", "80"); r.e == nil {
		t.Fatal("--port with --template accepted")
	}
	// Plain image deploy still needs its IMAGE argument.
	if r := runCLI(t, srv.URL, "", "deploy", "-p", "web"); r.e == nil {
		t.Fatal("deploy without image or template accepted")
	}
}

func TestMCPTemplateTools(t *testing.T) {
	ro := toolNames(t, []string{"read"})
	if !ro["list_templates"] || ro["deploy_template"] {
		t.Fatalf("read tools %v", ro)
	}
	dep := toolNames(t, []string{"read", "deploy"})
	if !dep["deploy_template"] {
		t.Fatalf("deploy tools %v", dep)
	}

	f, srv := newFake(t)
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", ConfigPath: filepath.Join(t.TempDir(), "c.json")}
	isErr, txt := mcpCall(t, app, "list_templates", nil)
	if isErr || !strings.Contains(txt, `"kind": "postgres"`) || !strings.Contains(txt, "POSTGRES_CONNECTION_STRING") || strings.Contains(txt, "${PASSWORD}") {
		t.Fatalf("list_templates: %v %s", isErr, txt)
	}
	isErr, txt = mcpCall(t, app, "deploy_template", map[string]any{"project": "web", "template": "postgres", "name": "pg", "env": map[string]any{"POSTGRES_DATABASE": "app"}})
	if isErr || !strings.Contains(txt, `"action": "created"`) {
		t.Fatalf("deploy_template: %v %s", isErr, txt)
	}
	if b := f.lastCreate(t); b.Kind != "postgres" || b.Name != "pg" || len(b.Env) != 1 {
		t.Fatalf("create body %+v", b)
	}
	if isErr, txt := mcpCall(t, app, "deploy_template", map[string]any{"project": "web", "template": "sqlite"}); !isErr || !strings.Contains(txt, "unknown template") {
		t.Fatalf("bad template: %v %s", isErr, txt)
	}
}
