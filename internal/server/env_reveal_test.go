package server

import (
	"net/http"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ?reveal=1: panel sessions and tokens with "exec" (scoped to the service's project, no
// require_approval) get secret values; everyone else gets 403 and masked lists still work.
func TestEnvRevealPermissions(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p, other api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "other", "server_id": nodeID}, &other, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "api", "kind": "image", "image": "nginx:1.27"}, &svc, 201)
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/env?redeploy=0", []api.EnvVar{{Key: "PLAIN", Value: "x"}, {Key: "TOKEN", Value: "s3cr3t", Secret: true}}, nil, 200)
	path := "/api/v1/services/" + svc.ID + "/env"
	secretOf := func(vars []api.EnvVar) string {
		for _, v := range vars {
			if v.Key == "TOKEN" {
				return v.Value
			}
		}
		return "?"
	}
	newToken := func(body map[string]any) string {
		var tok api.APIToken
		e.mustDo("POST", "/api/v1/tokens", body, &tok, 201)
		return tok.Token
	}

	var vars []api.EnvVar
	e.mustDo("GET", path+"?reveal=1", nil, &vars, 200)
	if secretOf(vars) != "s3cr3t" {
		t.Fatalf("session reveal: %+v", vars)
	}

	var errBody struct {
		Code string `json:"code"`
	}
	deploy := newToken(map[string]any{"name": "ci", "scope": "deploy"})
	if code := e.doWith(http.DefaultClient, deploy, "GET", path+"?reveal=1", nil, &errBody); code != 403 || errBody.Code != "missing_permission" {
		t.Fatalf("deploy token reveal: %d %+v", code, errBody)
	}
	vars = nil
	if code := e.doWith(http.DefaultClient, deploy, "GET", path, nil, &vars); code != 200 || secretOf(vars) != "" {
		t.Fatalf("deploy token masked list: %d %+v", code, vars)
	}

	exec := newToken(map[string]any{"name": "ops", "permissions": []string{"exec"}})
	vars = nil
	if code := e.doWith(http.DefaultClient, exec, "GET", path+"?reveal=1", nil, &vars); code != 200 || secretOf(vars) != "s3cr3t" {
		t.Fatalf("exec token reveal: %d %+v", code, vars)
	}

	careful := newToken(map[string]any{"name": "careful", "permissions": []string{"exec"}, "require_approval": true})
	errBody.Code = ""
	if code := e.doWith(http.DefaultClient, careful, "GET", path+"?reveal=1", nil, &errBody); code != 403 || errBody.Code != "approval_required" {
		t.Fatalf("require_approval reveal: %d %+v", code, errBody)
	}

	scopedOther := newToken(map[string]any{"name": "elsewhere", "permissions": []string{"exec"}, "projects": []string{other.ID}})
	if code := e.doWith(http.DefaultClient, scopedOther, "GET", path+"?reveal=1", nil, nil); code != 403 {
		t.Fatalf("other-project token reveal: %d", code)
	}
	scoped := newToken(map[string]any{"name": "here", "permissions": []string{"exec"}, "projects": []string{p.ID}})
	vars = nil
	if code := e.doWith(http.DefaultClient, scoped, "GET", path+"?reveal=1", nil, &vars); code != 200 || secretOf(vars) != "s3cr3t" {
		t.Fatalf("scoped token reveal: %d %+v", code, vars)
	}
}
