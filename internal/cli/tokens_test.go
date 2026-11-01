package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestParsePerms(t *testing.T) {
	got, err := ParsePerms([]string{"deploy, READ", "migrate", "deploy"})
	if err != nil || !reflect.DeepEqual(got, []string{"read", "deploy", "migrate"}) {
		t.Fatalf("got %v %v", got, err)
	}
	if got, _ := ParsePerms([]string{"all"}); !reflect.DeepEqual(got, api.AllPermissions) {
		t.Fatalf("all: %v", got)
	}
	if _, err := ParsePerms([]string{"read,root"}); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("want unknown permission error, got %v", err)
	}
}

func TestBuildTokenRequest(t *testing.T) {
	req, err := BuildTokenRequest(" ci ", TokenCreateFlags{Perms: []string{"deploy"}, Projects: []string{"prj_1"}, RequireApproval: true})
	if err != nil {
		t.Fatal(err)
	}
	if req.Name != "ci" || !reflect.DeepEqual(req.Permissions, []string{"read", "deploy"}) || !req.RequireApproval || req.Projects[0] != "prj_1" {
		t.Fatalf("req %+v", req)
	}
	if req, _ := BuildTokenRequest("ro", TokenCreateFlags{}); !reflect.DeepEqual(req.Permissions, []string{"read"}) {
		t.Fatalf("default perms %v", req.Permissions)
	}
	if _, err := BuildTokenRequest("x", TokenCreateFlags{Perms: []string{"tokens"}, Projects: []string{"prj_1"}}); err == nil {
		t.Fatal("global permission with --project must be refused")
	}
}

func TestBuildTokenUpdate(t *testing.T) {
	cur := api.APIToken{ID: "tok_1", Permissions: []string{"read", "deploy", "domains"}}
	u, err := BuildTokenUpdate(cur, TokenEditFlags{AddPerms: []string{"migrate"}, RemovePerms: []string{"domains"}})
	if err != nil || !reflect.DeepEqual(*u.Permissions, []string{"read", "deploy", "migrate"}) || u.Name != nil || u.Projects != nil || u.RequireApproval != nil {
		t.Fatalf("u %+v err %v", u, err)
	}
	u, err = BuildTokenUpdate(cur, TokenEditFlags{AllProjects: true, ApprovalSet: true, RequireApproval: false})
	if err != nil || u.Projects == nil || len(*u.Projects) != 0 || u.RequireApproval == nil || *u.RequireApproval {
		t.Fatalf("u %+v err %v", u, err)
	}
	b, _ := json.Marshal(u)
	if !strings.Contains(string(b), `"projects":[]`) || !strings.Contains(string(b), `"require_approval":false`) {
		// omitempty on a pointer keeps an explicit empty list / false
		t.Fatalf("body %s", b)
	}
	if _, err := BuildTokenUpdate(cur, TokenEditFlags{}); err == nil {
		t.Fatal("empty edit must be refused")
	}
	if _, err := BuildTokenUpdate(cur, TokenEditFlags{RemovePerms: []string{"read"}}); err == nil {
		t.Fatal("removing read must be refused")
	}
	if _, err := BuildTokenUpdate(cur, TokenEditFlags{AllProjects: true, ProjectsSet: true}); err == nil {
		t.Fatal("--project with --all-projects must be refused")
	}
}

func TestTokenCreateCLI(t *testing.T) {
	f, srv := newFake(t)
	r := runCLI(t, srv.URL, "", "token", "create", "ci", "--perm", "deploy,migrate", "--perm", "domains", "--project", "web", "--require-approval")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	var req api.TokenRequest
	json.Unmarshal(f.bodies["POST /tokens"], &req)
	if req.Name != "ci" || !reflect.DeepEqual(req.Permissions, []string{"read", "deploy", "migrate", "domains"}) || !reflect.DeepEqual(req.Projects, []string{"prj_1"}) || !req.RequireApproval {
		t.Fatalf("request %+v", req)
	}
	if !strings.Contains(r.out, "atl_pat_secretvalue") || !strings.Contains(r.out, "not shown again") {
		t.Fatalf("out %q", r.out)
	}
	r = runCLI(t, srv.URL, "", "token", "create", "bad", "--perm", "superuser")
	if r.e == nil || f.called("POST /tokens") && strings.Contains(string(f.bodies["POST /tokens"]), "bad") {
		t.Fatalf("unknown permission must fail before the request: %v", r.e)
	}
}

func TestTokenEditCLI(t *testing.T) {
	f, srv := newFake(t)
	r := runCLI(t, srv.URL, "", "token", "edit", "ci", "--add-perm", "migrate", "--require-approval=false")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	var body map[string]any
	json.Unmarshal(f.bodies["PATCH /tokens/tok_1"], &body)
	if _, ok := body["name"]; ok {
		t.Fatalf("name must not be sent: %v", body)
	}
	if body["require_approval"] != false || !reflect.DeepEqual(body["permissions"], []any{"read", "deploy", "migrate"}) {
		t.Fatalf("body %v", body)
	}
	if r := runCLI(t, srv.URL, "", "token", "edit", "ci"); r.e == nil {
		t.Fatal("edit without changes must fail")
	}
}

func TestMissingPermissionMessage(t *testing.T) {
	f, srv := newFake(t)
	f.denyPerm = "migrate"
	f.services = []api.Service{{ID: "svc_1", Name: "api", ProjectID: "prj_1", ServerID: "srv_1"}}
	r := runCLI(t, srv.URL, "", "services", "migrate", "api", "--to", "s2")
	if r.e == nil {
		t.Fatal("want error")
	}
	msg := ExplainError(r.e).Error()
	if !strings.Contains(msg, `lacks the "migrate" permission`) || !strings.Contains(msg, "--add-perm migrate") {
		t.Fatalf("message %q", msg)
	}
}

func TestServiceMigrateAndNetworkCLI(t *testing.T) {
	f, srv := newFake(t)
	f.services = []api.Service{{ID: "svc_1", Name: "api", ProjectID: "prj_1", ServerID: "srv_1"}}
	r := runCLI(t, srv.URL, "", "services", "migrate", "api", "--to", "s2")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if b := string(f.bodies["POST /services/svc_1/migrate"]); !strings.Contains(b, `"server_id":"srv_2"`) || !strings.Contains(r.out, "mig_1") {
		t.Fatalf("body %s out %q", b, r.out)
	}
	r = runCLI(t, srv.URL, "", "server", "network", "switch", "s2")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if b := string(f.bodies["POST /servers/srv_2/network"]); b != `{"action":"switch"}` || !strings.Contains(r.out, "switched") {
		t.Fatalf("body %s out %q", b, r.out)
	}
	if r := runCLI(t, srv.URL, "", "server", "network", "bogus", "s2"); r.e == nil {
		t.Fatal("unknown network action must fail")
	}
}

func TestDeployServerFlag(t *testing.T) {
	f, srv := newFake(t)
	r := runCLI(t, srv.URL, "", "deploy", "nginx:1", "-p", "web", "--server", "s2")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	var s api.Service
	json.Unmarshal(f.bodies["POST /projects/prj_1/services"], &s)
	if s.ServerID != "srv_2" {
		t.Fatalf("server_id %q", s.ServerID)
	}
	// the project's own server is the default: no server_id sent
	r = runCLI(t, srv.URL, "", "deploy", "redis:7", "-p", "web", "--server", "vps1")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	json.Unmarshal(f.bodies["POST /projects/prj_1/services"], &s)
	if strings.Contains(string(f.bodies["POST /projects/prj_1/services"]), "server_id") {
		t.Fatalf("body %s", f.bodies["POST /projects/prj_1/services"])
	}
}

func TestEveryMCPToolHasPermission(t *testing.T) {
	app := &App{Version: "test"}
	for _, st := range MCPTools(app) {
		if MCPToolPermission(st.Tool.Name) == "" {
			t.Errorf("tool %s has no permission mapping", st.Tool.Name)
		}
	}
}

func toolNames(t *testing.T, perms []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, st := range FilterMCPTools(MCPTools(&App{Version: "test"}), perms) {
		out[st.Tool.Name] = true
	}
	return out
}

func TestFilterMCPTools(t *testing.T) {
	ro := toolNames(t, []string{"read"})
	if !ro["list_services"] || !ro["get_logs"] || ro["deploy"] || ro["migrate"] || ro["add_domain"] || ro["backup_restore"] {
		t.Fatalf("read-only tools %v", ro)
	}
	dep := toolNames(t, []string{"read", "deploy"})
	if !dep["deploy"] || !dep["restart"] || dep["migrate"] || dep["service_migrate"] || dep["add_domain"] {
		t.Fatalf("deploy tools %v", dep)
	}
	mig := toolNames(t, []string{"read", "migrate"})
	if !mig["migrate"] || !mig["service_migrate"] || mig["deploy"] {
		t.Fatalf("migrate tools %v", mig)
	}
	all := toolNames(t, api.AllPermissions)
	if len(all) != len(MCPTools(&App{Version: "test"})) {
		t.Fatalf("all permissions should expose every tool, got %d", len(all))
	}
}

func TestMCPServerHidesToolsByMe(t *testing.T) {
	f, srv := newFake(t)
	f.me = map[string]any{"username": "chen", "auth": "token", "scope": "deploy", "permissions": []string{"read", "deploy"}}
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", ConfigPath: filepath.Join(t.TempDir(), "c.json")}
	s := NewMCPServer(app)
	tools := s.ListTools()
	if tools["deploy"] == nil || tools["list_projects"] == nil {
		t.Fatalf("deploy/read tools missing: %v", tools)
	}
	for _, n := range []string{"migrate", "service_migrate", "add_domain", "backup_restore"} {
		if tools[n] != nil {
			t.Errorf("tool %s must be hidden without its permission", n)
		}
	}
	// an older server without "permissions" maps the legacy scope
	f.me = map[string]any{"username": "chen", "scope": "admin"}
	if NewMCPServer(app).ListTools()["backup_restore"] == nil {
		t.Fatal("admin scope should expose every tool")
	}
}

func TestMCPMissingPermissionText(t *testing.T) {
	f, srv := newFake(t)
	f.denyPerm = "migrate"
	f.services = []api.Service{{ID: "svc_1", Name: "api", ProjectID: "prj_1", ServerID: "srv_1"}}
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", ConfigPath: filepath.Join(t.TempDir(), "c.json")}
	NewMCPServer(app)
	var h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	for _, st := range MCPTools(app) {
		if st.Tool.Name == "service_migrate" {
			h = st.Handler
		}
	}
	var req mcp.CallToolRequest
	req.Params.Name = "service_migrate"
	req.Params.Arguments = map[string]any{"service": "api", "server": "s2"}
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	txt := res.Content[0].(mcp.TextContent).Text
	if !res.IsError || !strings.Contains(txt, "PERMISSION DENIED") || !strings.Contains(txt, `"migrate"`) {
		t.Fatalf("result %v %q", res.IsError, txt)
	}
}
