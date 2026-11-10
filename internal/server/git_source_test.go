package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestParseGitURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://gitlab.com/group/sub/app.git":    "https://gitlab.com/group/sub/app.git",
		"git@codeberg.org:me/app.git":             "ssh://git@codeberg.org/me/app.git",
		"ssh://git@git.example.com:2222/me/app/":  "ssh://git@git.example.com:2222/me/app",
		"https://Git.Example.com:8443/team/app":   "https://git.example.com:8443/team/app",
		"ssh://deploy@[2001:db8::1]:22/srv/r.git": "ssh://deploy@[2001:db8::1]:22/srv/r.git",
	} {
		u, err := parseGitURL(raw)
		if err != nil || u.String() != want {
			t.Errorf("parseGitURL(%q) = %q, %v; want %q", raw, u.String(), err, want)
		}
	}
	for _, bad := range []string{"http://gitlab.com/a/b", "https://user:pw@gitlab.com/a/b", "https://gitlab.com/", "https://gitlab.com/a/../b",
		"ssh://git@host/-oProxyCommand=x", "ext::sh -c id", "file:///etc/passwd", "https://gitlab.com/a/b?x=1", "ssh://-oProxy@host/a"} {
		if _, err := parseGitURL(bad); err == nil {
			t.Errorf("parseGitURL(%q) accepted", bad)
		}
	}
	for host, want := range map[string]string{"github.com": "github", "gitlab.com": "gitlab", "gitlab.corp.io": "gitlab", "codeberg.org": "gitea",
		"forgejo.example.org": "gitea", "git.example.com": "git"} {
		if got := inferProvider(host); got != want {
			t.Errorf("inferProvider(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestValidateGitSourceExtras(t *testing.T) {
	g := api.GitSource{Repo: "https://gitlab.com/acme/app.git", Token: "glpat-x", PollIntervalSec: 300, Builder: "Generated", Stack: "node",
		BuildCommand: "pnpm build"}
	if err := validateGitSource(&g); err != nil {
		t.Fatal(err)
	}
	if g.Provider != "gitlab" || g.Builder != "generated" || g.Branch != "main" || g.Repo != "https://gitlab.com/acme/app.git" {
		t.Fatalf("normalized %+v", g)
	}
	gh := api.GitSource{Repo: "https://github.com/acme/app.git"}
	if err := validateGitSource(&gh); err != nil || gh.Repo != "https://github.com/acme/app" || gh.Provider != "github" || gitNeedsSources(gh, false) {
		t.Fatalf("github stays legacy: %+v %v", gh, err)
	}
	for name, bad := range map[string]api.GitSource{
		"poll":       {Repo: "https://gitlab.com/a/b", PollIntervalSec: 10},
		"builder":    {Repo: "https://gitlab.com/a/b", Builder: "nixpacks"},
		"stack":      {Repo: "https://gitlab.com/a/b", Stack: "ruby"},
		"cmd":        {Repo: "https://gitlab.com/a/b", StartCommand: "a\nb"},
		"key-https":  {Repo: "https://gitlab.com/a/b", SSHKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nx\n-----END OPENSSH PRIVATE KEY-----"},
		"token-ssh":  {Repo: "git@gitlab.com:a/b.git", Token: "t"},
		"bad-key":    {Repo: "git@gitlab.com:a/b.git", SSHKey: "hello"},
		"provider":   {Repo: "https://gitlab.com/a/b", Provider: "bitbucket"},
		"gh-as-lab":  {Repo: "https://github.com/a/b", Provider: "gitlab"},
		"knownhosts": {Repo: "git@gitlab.com:a/b.git", KnownHosts: "gitlab.com"},
	} {
		if err := validateGitSource(&bad); err == nil {
			t.Errorf("%s: accepted %+v", name, bad)
		}
	}
}

// capableAgent joins a fake agent that announces BuildSources.
func capableAgent(t *testing.T, e *testEnv) (*fakeAgent, string) {
	t.Helper()
	hello := api.HelloPayload{AgentVersion: "test", Hostname: "builder", OS: "linux", Arch: "amd64", CPUCores: 2, MemMB: 4096, DiskGB: 80,
		PublicIPv4: "203.0.113.9", BuildSources: true}
	return startFakeAgentHello(t, e, api.ZeaburInventory{}, hello)
}

func TestGitLabServiceOnCapableAgent(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := capableAgent(t, e)
	gitBuilds(agent)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "lab", "server_id": nodeID}, &p, 201)
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----"
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "app", "kind": "image",
		"git":   map[string]any{"repo": "git@gitlab.com:acme/app.git", "ssh_key": key, "builder": "generated", "stack": "node", "start_command": "node server.js"},
		"ports": []api.Port{{Name: "web", ContainerPort: 3000, Protocol: "http"}},
	}, &svc, 201)
	if svc.Git == nil || svc.Git.Provider != "gitlab" || !svc.Git.HasSSHKey || svc.Git.SSHKey != "" || svc.Git.Repo != "ssh://git@gitlab.com/acme/app.git" {
		t.Fatalf("created git %+v", svc.Git)
	}
	if svc.Webhook == nil || !strings.Contains(svc.Webhook.URL, "/api/v1/hooks/gitlab/"+svc.ID) || svc.Webhook.Provider != "gitlab" {
		t.Fatalf("webhook %+v", svc.Webhook)
	}
	waitDepStatus(t, e, latestDep(t, e, svc.ID).ID, "running")
	b := agent.builds()
	if len(b) != 1 || b[0].SSHKey != key+"\n" || b[0].Provider != "gitlab" || b[0].Username != "oauth2" || b[0].Builder != "generated" ||
		b[0].Stack != "node" || b[0].StartCommand != "node server.js" || b[0].Port != 3000 {
		t.Fatalf("build payload %+v", b)
	}
	// GET never returns the key
	var got map[string]any
	e.mustDo("GET", "/api/v1/services/"+svc.ID, nil, &got, 200)
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatalf("key leaked: %s", raw)
	}
}

func TestGitSourcesOldAgent(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{}) // no BuildSources
	gitBuilds(agent)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "old", "server_id": nodeID}, &p, 201)
	var er map[string]any
	if code := e.do("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "lab", "kind": "image",
		"git": map[string]any{"repo": "https://gitlab.com/acme/app"}}, &er); code != 409 || !strings.Contains(string(mustMarshal(er)), "agent_too_old") {
		t.Fatalf("gitlab on old agent: %d %v", code, er)
	}
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "gh", "kind": "image",
		"git": map[string]any{"repo": "https://github.com/acme/app", "token": "ghp_x"}}, &svc, 201)
	waitDepStatus(t, e, latestDep(t, e, svc.ID).ID, "running")
	raws := agent.received(api.MsgBuild)
	if len(raws) != 1 {
		t.Fatalf("builds %d", len(raws))
	}
	for _, f := range []string{"provider", "builder", "username", "port", "ssh_key"} {
		if strings.Contains(string(raws[0]), `"`+f+`"`) {
			t.Fatalf("legacy payload carries %s: %s", f, raws[0])
		}
	}
	if code := e.do("PATCH", "/api/v1/services/"+svc.ID, map[string]any{"git": map[string]any{"poll_interval_sec": 600}}, &er); code != 409 {
		t.Fatalf("poll on old agent: %d %v", code, er)
	}
	if code := e.do("PATCH", "/api/v1/services/"+svc.ID, map[string]any{"git": map[string]any{"branch": "dev"}}, nil); code != 200 {
		t.Fatalf("legacy patch: %d", code)
	}
}

func TestAgentUpgradeHint(t *testing.T) {
	if h := agentUpgradeHint("no Dockerfile at Dockerfile in x@1. Builds without a Dockerfile (automatic detection such as Railpack/Nixpacks) are not supported yet: add"); !strings.Contains(h, "upgrade the atoll-agent") {
		t.Fatal(h)
	}
	if h := agentUpgradeHint("docker build: exit 1"); h != "docker build: exit 1" {
		t.Fatal(h)
	}
}

func mustMarshal(v any) []byte { b, _ := json.Marshal(v); return b }
