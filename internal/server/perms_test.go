package server

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestTokenPermissions(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	newProject := func(name string) api.Project {
		var p api.Project
		e.mustDo("POST", "/api/v1/projects", map[string]string{"name": name, "server_id": nodeID}, &p, 201)
		return p
	}
	newToken := func(body map[string]any) api.APIToken {
		var tok api.APIToken
		e.mustDo("POST", "/api/v1/tokens", body, &tok, 201)
		return tok
	}

	// deploy-only token: deleting is 403 missing_permission, never a pending approval
	deploy := newToken(map[string]any{"name": "ci", "scope": "deploy"})
	p1 := newProject("one")
	var errBody struct {
		Code string `json:"code"`
	}
	if code := e.doWith(http.DefaultClient, deploy.Token, "DELETE", "/api/v1/projects/"+p1.ID, nil, &errBody); code != 403 || errBody.Code != "missing_permission" {
		t.Fatalf("delete without permission: %d %+v", code, errBody)
	}
	var cs []api.Confirmation
	e.mustDo("GET", "/api/v1/confirmations", nil, &cs, 200)
	if len(cs) != 0 {
		t.Fatalf("confirmation created for a token without permission: %+v", cs)
	}

	// token with delete runs it directly
	del := newToken(map[string]any{"name": "cleaner", "permissions": []string{"delete"}})
	if code := e.doWith(http.DefaultClient, del.Token, "DELETE", "/api/v1/projects/"+p1.ID, nil, nil); code != 204 {
		t.Fatalf("delete with permission: %d", code)
	}
	if code := e.do("GET", "/api/v1/projects/"+p1.ID, nil, nil); code != 404 {
		t.Fatalf("project still there: %d", code)
	}

	// require_approval: 202 + pending confirmation, project survives until approved
	p2 := newProject("two")
	careful := newToken(map[string]any{"name": "careful", "permissions": []string{"delete"}, "require_approval": true})
	var c api.Confirmation
	if code := e.doWith(http.DefaultClient, careful.Token, "DELETE", "/api/v1/projects/"+p2.ID, nil, &c); code != 202 || c.Action != "project.delete" {
		t.Fatalf("require_approval delete: %d %+v", code, c)
	}
	e.mustDo("GET", "/api/v1/projects/"+p2.ID, nil, nil, 200)
	e.mustDo("POST", "/api/v1/confirmations/"+c.ID+"/approve", nil, nil, 200)

	// /auth/me reports the token's permissions
	var me meResponse
	if code := e.doWith(http.DefaultClient, careful.Token, "GET", "/api/v1/auth/me", nil, &me); code != 200 ||
		!slices.Equal(me.Permissions, []string{"read", "delete"}) || !me.RequireApproval || me.Auth != "token" {
		t.Fatalf("me: %d %+v", code, me)
	}

	// project-scoped token: only sees and touches its project
	p3, p4 := newProject("three"), newProject("four")
	scoped := newToken(map[string]any{"name": "scoped", "permissions": []string{"deploy", "delete"}, "projects": []string{p3.ID}})
	var ps []api.Project
	if code := e.doWith(http.DefaultClient, scoped.Token, "GET", "/api/v1/projects", nil, &ps); code != 200 || len(ps) != 1 || ps[0].ID != p3.ID {
		t.Fatalf("scoped list: %d %+v", code, ps)
	}
	if code := e.doWith(http.DefaultClient, scoped.Token, "GET", "/api/v1/projects/"+p4.ID, nil, nil); code != 403 {
		t.Fatalf("scoped other project: %d", code)
	}
	if code := e.doWith(http.DefaultClient, scoped.Token, "POST", "/api/v1/projects", map[string]string{"name": "x", "server_id": nodeID}, nil); code != 403 {
		t.Fatalf("scoped create project: %d", code)
	}
	if code := e.doWith(http.DefaultClient, scoped.Token, "DELETE", "/api/v1/projects/"+p3.ID, nil, nil); code != 204 {
		t.Fatalf("scoped delete own project: %d", code)
	}
	// global permissions cannot be project-scoped
	if code := e.do("POST", "/api/v1/tokens", map[string]any{"name": "bad", "permissions": []string{"settings"}, "projects": []string{p4.ID}}, nil); code != 400 {
		t.Fatalf("global perm with project scope: %d", code)
	}
}

func TestTokenNoEscalation(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	var mgr api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "mgr", "permissions": []string{"deploy", "tokens"}}, &mgr, 201)

	// can hand out what it holds
	var child api.APIToken
	if code := e.doWith(http.DefaultClient, mgr.Token, "POST", "/api/v1/tokens", map[string]any{"name": "child", "permissions": []string{"deploy"}}, &child); code != 201 {
		t.Fatalf("create subset: %d", code)
	}
	// but not more
	if code := e.doWith(http.DefaultClient, mgr.Token, "POST", "/api/v1/tokens", map[string]any{"name": "evil", "permissions": []string{"delete"}}, nil); code != 403 {
		t.Fatalf("create escalated: %d", code)
	}
	if code := e.doWith(http.DefaultClient, mgr.Token, "POST", "/api/v1/tokens", map[string]any{"name": "evil", "scope": "admin"}, nil); code != 403 {
		t.Fatalf("create admin: %d", code)
	}
	if code := e.doWith(http.DefaultClient, mgr.Token, "PATCH", "/api/v1/tokens/"+child.ID, map[string]any{"permissions": []string{"deploy", "settings"}}, nil); code != 403 {
		t.Fatalf("patch escalated: %d", code)
	}
	if code := e.doWith(http.DefaultClient, mgr.Token, "PATCH", "/api/v1/tokens/"+mgr.ID, map[string]any{"permissions": []string{"deploy", "tokens", "delete"}}, nil); code != 403 {
		t.Fatalf("self-escalation: %d", code)
	}
	// allowed change
	var got api.APIToken
	if code := e.doWith(http.DefaultClient, mgr.Token, "PATCH", "/api/v1/tokens/"+child.ID, map[string]any{"name": "child2", "permissions": []string{"read"}, "require_approval": true}, &got); code != 200 ||
		got.Name != "child2" || !slices.Equal(got.Permissions, []string{"read"}) || got.Scope != "read" || !got.RequireApproval {
		t.Fatalf("patch: %d %+v", code, got)
	}
	// the panel session can grant anything
	var admin api.APIToken
	e.mustDo("PATCH", "/api/v1/tokens/"+child.ID, map[string]any{"permissions": []string{"admin"}}, &admin, 200)
	if admin.Scope != "admin" || len(admin.Permissions) != len(api.AllPermissions) {
		t.Fatalf("session patch: %+v", admin)
	}
}
