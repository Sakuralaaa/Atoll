package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

const (
	gitHead = "1111111111111111111111111111111111111111" // branch head the fake agent builds
	gitPush = "2222222222222222222222222222222222222222" // commit of a webhook push
)

// fakeBuilds makes the fake agent answer MsgBuild like the real one: a couple of build log
// lines, then the built image (or fail with failMsg).
type fakeBuilds struct {
	a       *fakeAgent
	failMsg string
}

func gitBuilds(a *fakeAgent) *fakeBuilds {
	fb := &fakeBuilds{a: a}
	a.mu.Lock()
	a.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type != api.MsgBuild {
			return api.ResultPayload{}, false
		}
		var p api.BuildPayload
		json.Unmarshal(env.Payload, &p)
		ctx := context.Background()
		a.send(ctx, api.Envelope{Type: api.MsgBuildLog, Payload: api.BuildLogEvent{DeploymentID: p.DeploymentID, ServiceID: p.ServiceID,
			Lines: []string{"$ git fetch --depth 1 " + p.Repo + ".git", "#1 [internal] load build definition from Dockerfile"}}})
		a.mu.Lock()
		fail := fb.failMsg
		a.mu.Unlock()
		if fail != "" {
			return api.ResultPayload{OK: false, Error: fail}, true
		}
		commit := p.Commit
		if commit == "" {
			commit = gitHead
		}
		raw, _ := json.Marshal(api.BuildResult{Image: "atoll-build/" + p.ServiceID + ":" + commit[:12], Commit: commit})
		return api.ResultPayload{OK: true, Output: string(raw)}, true
	}
	a.mu.Unlock()
	return fb
}

func (a *fakeAgent) builds() []api.BuildPayload {
	var out []api.BuildPayload
	for _, raw := range a.received(api.MsgBuild) {
		var p api.BuildPayload
		json.Unmarshal(raw, &p)
		out = append(out, p)
	}
	return out
}

func (a *fakeAgent) appliesOf(depID string) []api.ResolvedSpec {
	var out []api.ResolvedSpec
	for _, raw := range a.received(api.MsgApply) {
		var ap api.ApplyPayload
		json.Unmarshal(raw, &ap)
		if ap.Spec.DeploymentID == depID {
			out = append(out, ap.Spec)
		}
	}
	return out
}

func latestDep(t *testing.T, e *testEnv, svcID string) api.Deployment {
	t.Helper()
	var ds []api.Deployment
	e.mustDo("GET", "/api/v1/services/"+svcID+"/deployments", nil, &ds, 200)
	if len(ds) == 0 {
		t.Fatal("no deployments")
	}
	return ds[0]
}

// newGitService creates a git service (private repo: with a token) and waits for its first
// build + deploy.
func newGitService(t *testing.T) (*testEnv, *fakeAgent, *fakeBuilds, api.Service) {
	t.Helper()
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	fb := gitBuilds(agent)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "app", "kind": "image",
		"git":   map[string]any{"repo": "https://github.com/example/app.git", "token": "ghp_testtoken", "build_args": map[string]string{"MODE": "prod"}},
		"ports": []api.Port{{Name: "web", ContainerPort: 8080, Protocol: "http"}},
	}, &svc, 201)
	dep := latestDep(t, e, svc.ID)
	waitDepStatus(t, e, dep.ID, "running")
	return e, agent, fb, svc
}

func TestGitBuildThenApply(t *testing.T) {
	e, agent, _, svc := newGitService(t)
	if svc.Source != "git" || svc.Git == nil || svc.Git.Repo != "https://github.com/example/app" || svc.Git.Branch != "main" ||
		svc.Git.Dockerfile != "Dockerfile" || !svc.Git.HasToken || svc.Git.Token != "" {
		t.Fatalf("created service git = %+v source %q", svc.Git, svc.Source)
	}
	if svc.Webhook == nil || svc.Webhook.Secret == "" || !strings.HasSuffix(svc.Webhook.URL, "/api/v1/hooks/github/"+svc.ID) {
		t.Fatalf("webhook shown at create = %+v", svc.Webhook)
	}

	builds := agent.builds()
	if len(builds) != 1 {
		t.Fatalf("builds = %d", len(builds))
	}
	b := builds[0]
	if b.Repo != "https://github.com/example/app" || b.Branch != "main" || b.Commit != "" || b.Token != "ghp_testtoken" ||
		b.BuildArgs["MODE"] != "prod" || b.TimeoutSec != 1200 || b.Keep != 3 {
		t.Fatalf("build payload %+v", b)
	}
	d := latestDep(t, e, svc.ID)
	if d.Trigger != "create" || d.Commit != gitHead {
		t.Fatalf("deployment %+v", d)
	}
	img := "atoll-build/" + svc.ID + ":" + gitHead[:12]
	specs := agent.appliesOf(d.ID)
	if len(specs) != 1 || specs[0].Image != img || !specs[0].LocalImage || specs[0].RegistryAuth != nil {
		t.Fatalf("apply after build = %+v", specs)
	}
	var got api.Service
	e.mustDo("GET", "/api/v1/services/"+svc.ID, nil, &got, 200)
	if got.Image != img || got.Git.LastCommit != gitHead || got.Webhook != nil {
		t.Fatalf("service after build: image %q git %+v webhook %+v", got.Image, got.Git, got.Webhook)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "ghp_testtoken") || strings.Contains(string(raw), svc.Webhook.Secret) {
		t.Fatalf("secret leaked in service: %s", raw)
	}

	// build log stored per deployment
	deadline := time.Now().Add(5 * time.Second)
	for {
		var logs api.DeploymentLogs
		e.mustDo("GET", "/api/v1/services/"+svc.ID+"/deployments/"+d.ID+"/logs", nil, &logs, 200)
		all := ""
		for _, l := range logs.Lines {
			all += l.Line + "\n"
		}
		if strings.Contains(all, "load build definition") && strings.Contains(all, "atoll: built "+img) {
			var more api.DeploymentLogs
			e.mustDo("GET", "/api/v1/services/"+svc.ID+"/deployments/"+d.ID+"/logs?after="+itoa(int(logs.Lines[len(logs.Lines)-1].Seq)), nil, &more, 200)
			if len(more.Lines) != 0 {
				t.Fatalf("after=last returned %d lines", len(more.Lines))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("build log = %q", all)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// webhook secret: a panel session sees it, an API token only has_secret
	var wh api.GitWebhook
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/webhook", nil, &wh, 200)
	if wh.Secret != svc.Webhook.Secret || wh.Branch != "main" || wh.Events != "push" {
		t.Fatalf("session webhook %+v", wh)
	}
	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]string{"name": "ci", "scope": "deploy"}, &tok, 201)
	var whTok api.GitWebhook
	if code := e.doWith(http.DefaultClient, tok.Token, "GET", "/api/v1/services/"+svc.ID+"/webhook", nil, &whTok); code != 200 {
		t.Fatalf("token webhook: %d", code)
	}
	if whTok.Secret != "" || !whTok.HasSecret || whTok.URL != wh.URL {
		t.Fatalf("token webhook %+v", whTok)
	}
	if code := e.doWith(http.DefaultClient, tok.Token, "POST", "/api/v1/services/"+svc.ID+"/webhook/rotate", nil, nil); code != 403 {
		t.Fatalf("token rotate: %d", code)
	}

	// a user redeploy builds the branch head again
	var redep api.Deployment
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/actions", map[string]string{"action": "redeploy"}, &redep, 200)
	waitDepStatus(t, e, redep.ID, "running")
	if n := len(agent.builds()); n != 2 {
		t.Fatalf("redeploy builds = %d, want 2", n)
	}
}

func TestGitBuildFailureFailsDeployment(t *testing.T) {
	e, agent, fb, svc := newGitService(t)
	agent.mu.Lock()
	fb.failMsg = "no Dockerfile at Dockerfile in https://github.com/example/app@111111111111"
	agent.mu.Unlock()
	applies := len(agent.received(api.MsgApply))

	var redep api.Deployment
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/actions", map[string]string{"action": "redeploy"}, &redep, 200)
	waitDepStatus(t, e, redep.ID, "failed")
	d, _ := e.srv.st.GetDeployment(context.Background(), redep.ID)
	if !strings.Contains(d.Message, "build failed: no Dockerfile") {
		t.Fatalf("message %q", d.Message)
	}
	if n := len(agent.received(api.MsgApply)); n != applies {
		t.Fatalf("apply sent after a failed build (%d -> %d)", applies, n)
	}
	var logs api.DeploymentLogs
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/deployments/"+redep.ID+"/logs", nil, &logs, 200)
	if logs.Status != "failed" || len(logs.Lines) == 0 || !strings.Contains(logs.Lines[len(logs.Lines)-1].Line, "build failed") {
		t.Fatalf("logs %+v", logs)
	}
	// the failed build left no image: the desired spec of the server stays the last good one
	specs, err := e.srv.st.DesiredSpecsForNode(context.Background(), d.ServerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range specs {
		if sp.ServiceID == svc.ID && sp.Image != "atoll-build/"+svc.ID+":"+gitHead[:12] {
			t.Fatalf("desired image after failed build = %q", sp.Image)
		}
	}
}

func TestGitEnvChangeReusesImage(t *testing.T) {
	e, agent, _, svc := newGitService(t)
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/env", []api.EnvVar{{Key: "LOG_LEVEL", Value: "debug"}}, nil, 200)
	d := latestDep(t, e, svc.ID)
	if d.Trigger != "env" {
		t.Fatalf("latest deployment trigger %q", d.Trigger)
	}
	waitDepStatus(t, e, d.ID, "running")
	if n := len(agent.builds()); n != 1 {
		t.Fatalf("env change built again (%d builds)", n)
	}
	specs := agent.appliesOf(d.ID)
	if len(specs) != 1 || specs[0].Image != "atoll-build/"+svc.ID+":"+gitHead[:12] || !specs[0].LocalImage || specs[0].Env["LOG_LEVEL"] != "debug" {
		t.Fatalf("env apply = %+v", specs)
	}
}

func signHook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postHook(t *testing.T, e *testEnv, svcID, event, sig string, body []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/hooks/github/"+svcID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestGitHubWebhook(t *testing.T) {
	e, agent, _, svc := newGitService(t)
	secret := svc.Webhook.Secret
	push := func(ref, after string) []byte {
		b, _ := json.Marshal(map[string]any{"ref": ref, "after": after, "deleted": false, "repository": map[string]string{"full_name": "example/app"}})
		return b
	}
	body := push("refs/heads/main", gitPush)

	// signature
	if code, _ := postHook(t, e, svc.ID, "push", "", body); code != 401 {
		t.Fatalf("missing signature: %d", code)
	}
	if code, _ := postHook(t, e, svc.ID, "push", signHook("wrong-secret", body), body); code != 401 {
		t.Fatalf("wrong secret: %d", code)
	}
	if code, _ := postHook(t, e, svc.ID, "push", signHook(secret, body)+"00", body); code != 401 {
		t.Fatalf("bad digest: %d", code)
	}
	if code, _ := postHook(t, e, svc.ID, "push", signHook(secret, body), push("refs/heads/main", gitHead)); code != 401 {
		t.Fatalf("signature of another body: %d", code)
	}
	if code, _ := postHook(t, e, "svc_nope", "push", signHook(secret, body), body); code != 404 {
		t.Fatalf("unknown service: %d", code)
	}
	if n := len(agent.builds()); n != 1 {
		t.Fatalf("unsigned hooks triggered builds (%d)", n)
	}

	// ping and non-push events are acknowledged and ignored
	ping := []byte(`{"zen":"Keep it logically awesome.","hook_id":1}`)
	if code, out := postHook(t, e, svc.ID, "ping", signHook(secret, ping), ping); code != 200 || out["event"] != "ping" {
		t.Fatalf("ping: %d %v", code, out)
	}
	issue := []byte(`{"action":"opened"}`)
	if code, out := postHook(t, e, svc.ID, "issues", signHook(secret, issue), issue); code != 202 || out["ignored"] != true {
		t.Fatalf("issues event: %d %v", code, out)
	}
	// pushes to other branches, tags and branch deletions are ignored
	for _, b := range [][]byte{push("refs/heads/dev", gitPush), push("refs/tags/v1.0.0", gitPush), push("refs/heads/main", strings.Repeat("0", 40))} {
		if code, out := postHook(t, e, svc.ID, "push", signHook(secret, b), b); code != 202 || out["ignored"] != true {
			t.Fatalf("push %s: %d %v", b, code, out)
		}
	}
	if n := len(agent.builds()); n != 1 {
		t.Fatalf("ignored events triggered builds (%d)", n)
	}

	// a signed push to the branch builds and deploys exactly the pushed commit
	code, out := postHook(t, e, svc.ID, "push", signHook(secret, body), body)
	if code != 202 || out["commit"] != gitPush {
		t.Fatalf("push: %d %v", code, out)
	}
	depID, _ := out["deployment_id"].(string)
	waitDepStatus(t, e, depID, "running")
	builds := agent.builds()
	if len(builds) != 2 || builds[1].Commit != gitPush || builds[1].DeploymentID != depID {
		t.Fatalf("builds after push = %+v", builds)
	}
	d, _ := e.srv.st.GetDeployment(context.Background(), depID)
	if d.Trigger != "webhook" || d.Commit != gitPush {
		t.Fatalf("webhook deployment %+v", d)
	}
	if specs := agent.appliesOf(depID); len(specs) != 1 || specs[0].Image != "atoll-build/"+svc.ID+":"+gitPush[:12] {
		t.Fatalf("webhook apply = %+v", specs)
	}

	// rotating the secret invalidates the old one
	var wh api.GitWebhook
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/webhook/rotate", nil, &wh, 200)
	if wh.Secret == "" || wh.Secret == secret {
		t.Fatalf("rotate: %+v", wh)
	}
	if code, _ := postHook(t, e, svc.ID, "push", signHook(secret, body), body); code != 401 {
		t.Fatalf("old secret after rotate: %d", code)
	}
}
