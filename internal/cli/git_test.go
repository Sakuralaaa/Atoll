package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

const testWebhookSecret = "whsec-test-value"

// gitRoutes adds the builds-from-source routes to the fake panel.
func gitRoutes(f *fakePanel) {
	var polls int32
	f.extra = func(w http.ResponseWriter, r *http.Request, key string, body []byte) bool {
		switch {
		case key == "POST /projects/prj_1/services":
			var s api.Service
			json.Unmarshal(body, &s)
			s.ID = "svc_new"
			if s.Git != nil {
				s.Source = "git"
				s.Git.Token = ""
				s.Webhook = &api.GitWebhook{URL: "https://atoll.example.com/api/v1/hooks/github/svc_new", Secret: testWebhookSecret, HasSecret: true, ContentType: "application/json", Events: "push", Branch: orDefault(s.Git.Branch, "main")}
			}
			writeJSON(w, 201, s)
		case key == "PATCH /services/svc_1":
			s := f.services[0]
			var p map[string]json.RawMessage
			json.Unmarshal(body, &p)
			if raw, ok := p["image"]; ok {
				json.Unmarshal(raw, &s.Image)
			}
			if raw, ok := p["git"]; ok {
				if string(raw) == "null" {
					s.Git = nil
				} else {
					first := s.Git == nil
					var g api.GitSource
					json.Unmarshal(raw, &g)
					s.Git = &g
					if first {
						s.Webhook = &api.GitWebhook{URL: "https://atoll.example.com/api/v1/hooks/github/svc_1", Secret: testWebhookSecret, HasSecret: true, Branch: "main"}
					}
				}
			}
			writeJSON(w, 200, s)
		case key == "POST /services/svc_1/actions":
			w.WriteHeader(204)
		case key == "GET /services/svc_1/deployments":
			t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			writeJSON(w, 200, []api.Deployment{
				{ID: "dep_2", ServiceID: "svc_1", Status: "building", StartedAt: t0.Add(time.Minute)},
				{ID: "dep_1", ServiceID: "svc_1", Status: "superseded", StartedAt: t0},
			})
		case strings.HasPrefix(key, "GET /services/svc_1/deployments/") && strings.HasSuffix(key, "/logs"):
			dep := strings.TrimSuffix(strings.TrimPrefix(key, "GET /services/svc_1/deployments/"), "/logs")
			after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
			all := []api.DeploymentLogLine{{Seq: 1, Line: "#1 load Dockerfile"}, {Seq: 2, Line: "#2 RUN go build"}, {Seq: 3, Line: "#3 exporting"}}
			n := atomic.AddInt32(&polls, 1)
			status := "building"
			avail := 2
			if n > 1 {
				status, avail = "running", 3
			}
			var out []api.DeploymentLogLine
			for _, l := range all[:avail] {
				if l.Seq > after {
					out = append(out, l)
				}
			}
			writeJSON(w, 200, api.DeploymentLogs{DeploymentID: dep, Status: status, Lines: out})
		case key == "GET /services/svc_1/webhook":
			writeJSON(w, 200, api.GitWebhook{URL: "https://atoll.example.com/api/v1/hooks/github/svc_1", HasSecret: true, ContentType: "application/json", Events: "push", Branch: "main"})
		default:
			return false
		}
		return true
	}
}

func TestGitFlagsSource(t *testing.T) {
	env := map[string]string{"GH_TOK": "ghp_secret123"}
	get := func(k string) string { return env[k] }
	g := GitFlags{Repo: "https://github.com/me/app", Branch: "dev", Context: "svc", Dockerfile: "Dockerfile.prod", BuildArgs: []string{"A=1", "B=x=y"}, TokenEnv: "GH_TOK"}
	src, err := g.Source(get)
	if err != nil {
		t.Fatal(err)
	}
	if src.Repo != "https://github.com/me/app" || src.Branch != "dev" || src.Context != "svc" || src.Dockerfile != "Dockerfile.prod" || src.Token != "ghp_secret123" || src.BuildArgs["A"] != "1" || src.BuildArgs["B"] != "x=y" {
		t.Fatalf("source %+v", src)
	}
	for _, bad := range []GitFlags{
		{Repo: "http://gitlab.com/me/app"},
		{Repo: "ftp://gitlab.com/me/app"},
		{Repo: "ssh://git:pw@gitlab.com/me/app"},
		{Repo: "https://gitlab.com/me/app", Poll: "10s"},
		{Repo: "https://gitlab.com/me/app", SSHKeyFile: "/nonexistent/key"},
		{Repo: "https://user:pw@github.com/me/app"},
		{Repo: "https://github.com/me/app", TokenEnv: "MISSING"},
		{Repo: "https://github.com/me/app", TokenEnv: "ghp_literaltoken"},
		{Repo: "https://github.com/me/app", BuildArgs: []string{"novalue"}},
	} {
		if _, err := bad.Source(get); err == nil {
			t.Errorf("want error for %+v", bad)
		}
	}
}

func TestRepoNameGitChangedPatch(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/me/app":         "app",
		"https://github.com/me/My_App.git":  "my-app",
		"https://github.com/me/site.io/":    "site-io",
		"https://github.com/org/api-server": "api-server",
	} {
		if got := repoName(in); got != want {
			t.Errorf("repoName(%q) = %q, want %q", in, got, want)
		}
	}
	cur := &api.GitSource{Repo: "https://github.com/me/app", BuildArgs: map[string]string{"A": "1"}, HasToken: true}
	same := []*api.GitSource{
		{Repo: "https://github.com/me/app.git"},
		{Repo: "https://github.com/Me/App", Branch: "main", Dockerfile: "Dockerfile"},
		{Repo: "https://github.com/me/app", BuildArgs: map[string]string{"A": "1"}},
	}
	for _, w := range same {
		if gitChanged(cur, w) {
			t.Errorf("gitChanged(%+v) = true, want false", w)
		}
	}
	changed := []*api.GitSource{
		{Repo: "https://github.com/me/other"},
		{Repo: "https://github.com/me/app", Branch: "dev"},
		{Repo: "https://github.com/me/app", Context: "web"},
		{Repo: "https://github.com/me/app", Dockerfile: "Dockerfile.prod"},
		{Repo: "https://github.com/me/app", BuildArgs: map[string]string{"A": "2"}},
		{Repo: "https://github.com/me/app", BuildArgs: map[string]string{}},
		{Repo: "https://github.com/me/app", Token: "t"},
	}
	for _, w := range changed {
		if !gitChanged(cur, w) {
			t.Errorf("gitChanged(%+v) = false, want true", w)
		}
	}
	if !gitChanged(nil, same[0]) {
		t.Error("switching an image service to git is a change")
	}
	p := gitPatch(&api.GitSource{Repo: "https://github.com/me/app", Branch: "dev"})
	if len(p) != 2 || p["branch"] != "dev" {
		t.Fatalf("patch %v (only the given fields)", p)
	}
}

func TestDeployGitCreates(t *testing.T) {
	f, srv := newFake(t)
	gitRoutes(f)
	t.Setenv("TEST_ATOLL_GH_TOKEN", "ghp_fromenv")
	r := runCLI(t, srv.URL, "", "deploy", "--git", "https://github.com/me/My_App.git", "--branch", "dev", "--context", "svc", "--dockerfile", "Dockerfile.prod",
		"--build-arg", "VERSION=1", "--build-arg", "MODE=prod", "--git-token-env", "TEST_ATOLL_GH_TOKEN", "-e", "API_KEY=x", "-p", "web", "--port", "3000")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if f.called("PUT /services/svc_new/env") {
		t.Fatal("env must go into the create request (a separate PUT would trigger a second build)")
	}
	var body struct {
		api.Service
		Env []api.EnvVar `json:"env"`
	}
	raw := f.bodies["POST /projects/prj_1/services"]
	json.Unmarshal(raw, &body)
	g := body.Git
	if g == nil || g.Repo != "https://github.com/me/My_App.git" || g.Branch != "dev" || g.Context != "svc" || g.Dockerfile != "Dockerfile.prod" || g.Token != "ghp_fromenv" || g.BuildArgs["VERSION"] != "1" || g.BuildArgs["MODE"] != "prod" {
		t.Fatalf("git %+v", g)
	}
	if body.Image != "" || body.Name != "my-app" || body.Kind != "image" || len(body.Ports) != 1 || body.Ports[0].ContainerPort != 3000 {
		t.Fatalf("service %s", raw)
	}
	if len(body.Env) != 1 || body.Env[0].Key != "API_KEY" || !body.Env[0].Secret {
		t.Fatalf("env %+v", body.Env)
	}
	for _, want := range []string{"created service my-app", "hooks/github/svc_new", testWebhookSecret, "shown only this once", "atoll logs my-app --build -f"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output lacks %q:\n%s", want, r.out)
		}
	}
	if strings.Contains(r.out+r.err, "ghp_fromenv") {
		t.Fatal("the git token must never be printed")
	}
}

func TestDeployGitFlagErrors(t *testing.T) {
	f, srv := newFake(t)
	gitRoutes(f)
	cases := [][]string{
		{"deploy", "ghcr.io/me/app:v1", "--git", "https://github.com/me/app", "-p", "web"},
		{"deploy", "ghcr.io/me/app:v1", "--branch", "dev", "-p", "web"},
		{"deploy", "-p", "web"},
		{"deploy", "--template", "postgres", "--git", "https://github.com/me/app", "-p", "web"},
		{"deploy", "--git", "https://github.com/me/app", "--git-token-env", "TEST_ATOLL_UNSET_VAR", "-p", "web"},
	}
	for _, args := range cases {
		if r := runCLI(t, srv.URL, "", args...); r.e == nil {
			t.Errorf("want error for %v", args)
		}
	}
	if f.called("POST /projects/prj_1/services") || f.called("PATCH /services/svc_1") {
		t.Fatalf("nothing may be sent on a flag error: %v", f.calls)
	}
}

func TestDeployGitExisting(t *testing.T) {
	f, srv := newFake(t)
	gitRoutes(f)
	f.services = []api.Service{{ID: "svc_1", Name: "app", ProjectID: "prj_1", Kind: "image", Source: "git", Image: "atoll-build/svc_1:abc", Git: &api.GitSource{Repo: "https://github.com/me/app", Branch: "main", HasToken: true}}}

	// unchanged source: redeploy (builds the branch head)
	r := runCLI(t, srv.URL, "", "deploy", "--git", "https://github.com/me/app", "-p", "web")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if f.called("PATCH /services/svc_1") || !f.called("POST /services/svc_1/actions") {
		t.Fatalf("calls %v", f.calls)
	}
	if !strings.Contains(string(f.bodies["POST /services/svc_1/actions"]), "redeploy") || !strings.Contains(r.out, "redeployed service app") {
		t.Fatalf("action %s out %q", f.bodies["POST /services/svc_1/actions"], r.out)
	}

	// changed branch: partial git patch, no image, no read-only fields
	f.calls = nil
	r = runCLI(t, srv.URL, "", "deploy", "--git", "https://github.com/me/app", "--branch", "dev", "-p", "web")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	patch := string(f.bodies["PATCH /services/svc_1"])
	if !strings.Contains(patch, `"branch":"dev"`) || strings.Contains(patch, "image") || strings.Contains(patch, "has_token") || strings.Contains(patch, "token") {
		t.Fatalf("patch %s", patch)
	}
	if f.called("POST /services/svc_1/actions") || !strings.Contains(r.out, "updated service app") {
		t.Fatalf("calls %v out %q", f.calls, r.out)
	}
}

func TestDeployImageOverGitService(t *testing.T) {
	f, srv := newFake(t)
	gitRoutes(f)
	f.services = []api.Service{{ID: "svc_1", Name: "app", ProjectID: "prj_1", Kind: "image", Source: "git", Image: "atoll-build/svc_1:abc", Git: &api.GitSource{Repo: "https://github.com/me/app"}}}
	r := runCLI(t, srv.URL, "", "deploy", "ghcr.io/me/app:v3", "-p", "web")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	patch := string(f.bodies["PATCH /services/svc_1"])
	if !strings.Contains(patch, `"git":null`) || !strings.Contains(patch, "ghcr.io/me/app:v3") {
		t.Fatalf("patch %s", patch)
	}
}

func TestDeployImageServiceSwitchesToGit(t *testing.T) {
	f, srv := newFake(t)
	gitRoutes(f)
	f.services = []api.Service{{ID: "svc_1", Name: "app", ProjectID: "prj_1", Kind: "image", Image: "ghcr.io/me/app:v1"}}
	r := runCLI(t, srv.URL, "", "deploy", "--git", "https://github.com/me/app", "-p", "web")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if !strings.Contains(string(f.bodies["PATCH /services/svc_1"]), `"repo":"https://github.com/me/app"`) {
		t.Fatalf("patch %s", f.bodies["PATCH /services/svc_1"])
	}
	if !strings.Contains(r.out, testWebhookSecret) {
		t.Fatalf("first switch to git must print the webhook secret: %q", r.out)
	}
}

func TestServicesWebhookCLI(t *testing.T) {
	f, srv := newFake(t)
	gitRoutes(f)
	f.services = []api.Service{{ID: "svc_1", Name: "app", ProjectID: "prj_1", Git: &api.GitSource{Repo: "https://github.com/me/app"}}}
	r := runCLI(t, srv.URL, "", "services", "webhook", "app", "-p", "web")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	for _, want := range []string{"hooks/github/svc_1", "application/json", "push", "main", "panel session"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output lacks %q:\n%s", want, r.out)
		}
	}
}

func TestLogsBuildFollow(t *testing.T) {
	old := buildPoll
	buildPoll = 5 * time.Millisecond
	defer func() { buildPoll = old }()
	f, srv := newFake(t)
	gitRoutes(f)
	f.services = []api.Service{{ID: "svc_1", Name: "app", ProjectID: "prj_1"}}
	r := runCLI(t, srv.URL, "", "logs", "app", "-p", "web", "--build", "-f")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if !f.called("GET /services/svc_1/deployments/dep_2/logs") {
		t.Fatalf("should read the newest deployment: %v", f.calls)
	}
	// each line exactly once across the polls
	for _, l := range []string{"#1 load Dockerfile", "#2 RUN go build", "#3 exporting"} {
		if strings.Count(r.out, l) != 1 {
			t.Errorf("line %q printed %d times:\n%s", l, strings.Count(r.out, l), r.out)
		}
	}
	if !strings.Contains(r.err, "dep_2: running") {
		t.Fatalf("stderr %q", r.err)
	}
	// a second positional argument is only accepted with --build
	if r := runCLI(t, srv.URL, "", "logs", "app", "dep_1", "-p", "web"); r.e == nil {
		t.Fatal("want arg error without --build")
	}
}

func mcpTool(t *testing.T, app *App, name string) (mcp.Tool, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	t.Helper()
	for _, st := range MCPTools(app) {
		if st.Tool.Name == name {
			return st.Tool, st.Handler
		}
	}
	t.Fatalf("no tool %s", name)
	return mcp.Tool{}, nil
}

func callTool(t *testing.T, h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), name string, args map[string]any) (string, bool) {
	t.Helper()
	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res.Content[0].(mcp.TextContent).Text, res.IsError
}

func TestMCPDeployGitSchema(t *testing.T) {
	tl, _ := mcpTool(t, &App{Version: "test"}, "deploy")
	props := tl.InputSchema.Properties
	for _, k := range []string{"image", "repo", "branch", "context", "dockerfile", "build_args"} {
		if _, ok := props[k]; !ok {
			t.Errorf("deploy schema lacks %s", k)
		}
	}
	if _, ok := props["token"]; ok {
		t.Error("deploy must not accept a git token over MCP")
	}
	for _, k := range tl.InputSchema.Required {
		if k == "image" {
			t.Error("image must be optional (repo is the alternative)")
		}
	}
	bl, _ := mcpTool(t, &App{Version: "test"}, "get_build_logs")
	if bl.Annotations.ReadOnlyHint == nil || !*bl.Annotations.ReadOnlyHint {
		t.Error("get_build_logs must be read-only")
	}
	if MCPToolPermission("get_build_logs") != api.PermRead || MCPToolPermission("deploy") != api.PermDeploy {
		t.Fatal("permissions: get_build_logs=read, deploy=deploy")
	}
	ro := toolNames(t, []string{"read"})
	if !ro["get_build_logs"] || ro["deploy"] {
		t.Fatalf("read-only tools %v", ro)
	}
}

func TestMCPDeployGitCall(t *testing.T) {
	f, srv := newFake(t)
	gitRoutes(f)
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", ConfigPath: filepath.Join(t.TempDir(), "c.json")}
	NewMCPServer(app)
	_, h := mcpTool(t, app, "deploy")

	for _, args := range []map[string]any{
		{"project": "web"},
		{"project": "web", "image": "ghcr.io/me/app:v1", "repo": "https://github.com/me/app"},
		{"project": "web", "repo": "https://github.com/me/app", "token": "ghp_x"},
		{"project": "web", "image": "ghcr.io/me/app:v1", "branch": "dev"},
	} {
		if txt, isErr := callTool(t, h, "deploy", args); !isErr {
			t.Errorf("want error for %v, got %s", args, txt)
		}
	}
	if f.called("POST /projects/prj_1/services") {
		t.Fatal("nothing may be created on bad arguments")
	}
	txt, isErr := callTool(t, h, "deploy", map[string]any{"project": "web", "repo": "https://github.com/me/api", "branch": "dev", "build_args": map[string]any{"V": "2"}, "env": map[string]any{"PORT": "8080"}})
	if isErr {
		t.Fatal(txt)
	}
	var body struct {
		api.Service
		Env []api.EnvVar `json:"env"`
	}
	json.Unmarshal(f.bodies["POST /projects/prj_1/services"], &body)
	if body.Git == nil || body.Git.Repo != "https://github.com/me/api" || body.Git.Branch != "dev" || body.Git.BuildArgs["V"] != "2" || body.Git.Token != "" || body.Name != "api" || len(body.Env) != 1 {
		t.Fatalf("body %s", f.bodies["POST /projects/prj_1/services"])
	}
	if !strings.Contains(txt, testWebhookSecret) {
		t.Fatalf("the one-time webhook secret must reach the agent: %s", txt)
	}
}

func TestMCPGetBuildLogs(t *testing.T) {
	f, srv := newFake(t)
	gitRoutes(f)
	f.services = []api.Service{{ID: "svc_1", Name: "app", ProjectID: "prj_1"}}
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", ConfigPath: filepath.Join(t.TempDir(), "c.json")}
	NewMCPServer(app)
	_, h := mcpTool(t, app, "get_build_logs")
	txt, isErr := callTool(t, h, "get_build_logs", map[string]any{"service": "app", "project": "web"})
	if isErr {
		t.Fatal(txt)
	}
	var bl BuildLogs
	if err := json.Unmarshal([]byte(txt), &bl); err != nil {
		t.Fatal(err, txt)
	}
	if bl.DeploymentID != "dep_2" || bl.Status != "building" || bl.LastSeq != 2 || !strings.Contains(bl.Log, "RUN go build") {
		t.Fatalf("logs %+v", bl)
	}
	txt, _ = callTool(t, h, "get_build_logs", map[string]any{"service": "app", "project": "web", "deployment_id": "dep_1", "after": 2})
	json.Unmarshal([]byte(txt), &bl)
	if bl.DeploymentID != "dep_1" || bl.Status != "running" || bl.LastSeq != 3 || strings.Contains(bl.Log, "RUN go build") || !strings.Contains(bl.Log, "exporting") {
		t.Fatalf("logs after 2: %+v", bl)
	}
}
