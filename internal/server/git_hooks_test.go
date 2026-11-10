package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func postForgeHook(t *testing.T, e *testEnv, kind, svcID string, headers map[string]string, body []byte) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/hooks/"+kind+"/"+svcID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
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

func forgeService(t *testing.T, repo string) (*testEnv, *fakeAgent, api.Service) {
	t.Helper()
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := capableAgent(t, e)
	gitBuilds(agent)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "forge", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "app", "kind": "image", "git": map[string]any{"repo": repo}}, &svc, 201)
	waitDepStatus(t, e, latestDep(t, e, svc.ID).ID, "running")
	return e, agent, svc
}

func TestGitLabWebhook(t *testing.T) {
	e, agent, svc := forgeService(t, "https://gitlab.com/acme/app")
	secret := svc.Webhook.Secret
	body, _ := json.Marshal(map[string]any{"object_kind": "push", "ref": "refs/heads/main", "after": gitPush})
	h := func(tok, ev, id string) map[string]string {
		return map[string]string{"X-Gitlab-Token": tok, "X-Gitlab-Event": ev, "X-Gitlab-Event-UUID": id}
	}
	if code, _ := postForgeHook(t, e, "gitlab", svc.ID, h("wrong", "Push Hook", "u1"), body); code != 401 {
		t.Fatalf("wrong token: %d", code)
	}
	if code, _ := postForgeHook(t, e, "gitlab", svc.ID, h("", "Push Hook", "u1"), body); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, out := postForgeHook(t, e, "gitlab", svc.ID, h(secret, "Tag Push Hook", "u0"), body); code != 202 || out["ignored"] != true {
		t.Fatalf("tag push: %d %v", code, out)
	}
	code, out := postForgeHook(t, e, "gitlab", svc.ID, h(secret, "Push Hook", "u1"), body)
	if code != 202 || out["commit"] != gitPush {
		t.Fatalf("push: %d %v", code, out)
	}
	waitDepStatus(t, e, out["deployment_id"].(string), "running")
	if b := agent.builds(); b[len(b)-1].Commit != gitPush {
		t.Fatalf("built %+v", b[len(b)-1])
	}
	if code, out := postForgeHook(t, e, "gitlab", svc.ID, h(secret, "Push Hook", "u1"), body); code != 202 || out["ignored"] != true {
		t.Fatalf("replay: %d %v", code, out)
	}
	other, _ := json.Marshal(map[string]any{"ref": "refs/heads/dev", "after": gitPush})
	if code, out := postForgeHook(t, e, "gitlab", svc.ID, h(secret, "Push Hook", "u2"), other); code != 202 || out["ignored"] != true {
		t.Fatalf("other branch: %d %v", code, out)
	}
}

func TestGiteaWebhook(t *testing.T) {
	e, _, svc := forgeService(t, "https://codeberg.org/acme/app")
	secret := svc.Webhook.Secret
	body, _ := json.Marshal(map[string]any{"ref": "refs/heads/main", "after": gitPush})
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	if code, _ := postForgeHook(t, e, "gitea", svc.ID, map[string]string{"X-Gitea-Signature": sig + "00", "X-Gitea-Event": "push"}, body); code != 401 {
		t.Fatalf("bad sig: %d", code)
	}
	if code, _ := postForgeHook(t, e, "gitea", svc.ID, map[string]string{"X-Gitea-Event": "push"}, body); code != 401 {
		t.Fatalf("no sig: %d", code)
	}
	code, out := postForgeHook(t, e, "gitea", svc.ID, map[string]string{"X-Forgejo-Signature": sig, "X-Forgejo-Event": "push", "X-Forgejo-Delivery": "d1"}, body)
	if code != 202 || out["commit"] != gitPush {
		t.Fatalf("forgejo push: %d %v", code, out)
	}
	waitDepStatus(t, e, out["deployment_id"].(string), "running")
	if code, out := postForgeHook(t, e, "gitea", svc.ID, map[string]string{"X-Gitea-Signature": sig, "X-Gitea-Event": "push", "X-Gitea-Delivery": "d1"}, body); code != 202 || out["ignored"] != true {
		t.Fatalf("replay: %d %v", code, out)
	}
	var wh api.GitWebhook
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/webhook", nil, &wh, 200)
	if wh.Provider != "gitea" || !bytes.Contains([]byte(wh.URL), []byte("/api/v1/hooks/gitea/"+svc.ID)) {
		t.Fatalf("webhook info %+v", wh)
	}
}
