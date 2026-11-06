package server

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/cli"
	"github.com/Sakuralaaa/Atoll/internal/client"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// B2: a member manages its own API tokens; they never exceed the member, and every request
// with one is narrowed to what the member may do right now.
func TestMemberAPITokens(t *testing.T) {
	e := newMemberEnv(t, api.PermRead, api.PermDeploy)
	ctx := context.Background()
	var adminTok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "admin-ci", "permissions": []string{"read", "deploy", "delete"}}, &adminTok, 201)

	var tok, scoped api.APIToken
	if code := e.mdo("POST", "/api/v1/tokens", map[string]any{"name": "ci", "permissions": []string{"read", "deploy"}}, &tok); code != 201 || tok.Token == "" || tok.Owner != "bob" || tok.OwnerRole != "member" {
		t.Fatalf("member token %d %+v", code, tok)
	}
	for _, c := range []struct {
		body map[string]any
		want int
	}{
		{map[string]any{"name": "x", "permissions": []string{"read", "exec"}}, 403},    // not the member's
		{map[string]any{"name": "x", "permissions": []string{"read", "servers"}}, 403}, // never a member's
		{map[string]any{"name": "x", "permissions": []string{"read", "tokens"}}, 403},
		{map[string]any{"name": "x", "projects": []string{"prj_b"}}, 400}, // not visible: unknown
		{map[string]any{"name": "x", "projects": []string{"prj_nope"}}, 400},
		{map[string]any{"name": "x", "require_approval": true}, 400}, // approvals are the admin's
	} {
		if code := e.mdo("POST", "/api/v1/tokens", c.body, nil); code != c.want {
			t.Errorf("create %v: %d, want %d", c.body, code, c.want)
		}
	}
	if code := e.mdo("POST", "/api/v1/tokens", map[string]any{"name": "scoped", "projects": []string{"prj_a"}}, &scoped); code != 201 {
		t.Fatalf("project-scoped member token %d", code)
	}
	var mine []api.APIToken
	if code := e.mdo("GET", "/api/v1/tokens", nil, &mine); code != 200 || len(mine) != 2 {
		t.Fatalf("member lists %d %+v", code, mine)
	}
	for _, x := range mine {
		if x.Owner != "bob" {
			t.Fatalf("member sees another's token %+v", x)
		}
	}
	if code := e.mdo("PATCH", "/api/v1/tokens/"+adminTok.ID, map[string]any{"name": "mine now"}, nil); code != 404 {
		t.Fatalf("member patches admin token: %d", code)
	}
	if code := e.mdo("DELETE", "/api/v1/tokens/"+adminTok.ID, nil, nil); code != 404 {
		t.Fatalf("member deletes admin token: %d", code)
	}
	if code := e.mdo("PATCH", "/api/v1/tokens/"+scoped.ID, map[string]any{"permissions": []string{"read", "delete"}}, nil); code != 403 {
		t.Fatalf("member widens its token: %d", code)
	}
	if code := e.mdo("DELETE", "/api/v1/tokens/"+scoped.ID, nil, nil); code != 204 {
		t.Fatalf("member deletes its token: %d", code)
	}
	var all []api.APIToken
	e.mustDo("GET", "/api/v1/tokens", nil, &all, 200)
	if len(all) != 2 || !slices.ContainsFunc(all, func(x api.APIToken) bool { return x.ID == tok.ID && x.Owner == "bob" && x.OwnerRole == "member" }) {
		t.Fatalf("admin lists %+v", all)
	}

	// the member's token: the member's reach, no more
	as := func(method, path string, body, out any) int {
		return e.doWith(http.DefaultClient, tok.Token, method, path, body, out)
	}
	var me meResponse
	if code := as("GET", "/api/v1/auth/me", nil, &me); code != 200 || me.Role != "member" || !slices.Equal(me.Permissions, []string{"read", "deploy"}) || !slices.Equal(me.Servers, []string{"srv_a"}) {
		t.Fatalf("token me %d %+v", code, me)
	}
	want := map[string]int{
		"GET /api/v1/projects/prj_a":       200,
		"GET /api/v1/projects/prj_b":       404,
		"DELETE /api/v1/projects/prj_a":    403, // delete is not the member's
		"GET /api/v1/settings":             403,
		"GET /api/v1/tokens":               403, // a token cannot manage tokens
		"POST /api/v1/tokens":              403,
		"GET /api/v1/audit":                403,
		"GET /api/v1/servers/srv_b":        404,
		"PATCH /api/v1/servers/srv_a":      403,
		"POST /api/v1/servers/join-token":  403,
		"GET /api/v1/users":                403,
		"POST /api/v1/projects/prj_b/move": 404,
	}
	for k, code := range want {
		method, path, _ := cutSpace(k)
		var body any
		if method != "GET" {
			body = map[string]any{}
		}
		if got := as(method, path, body, nil); got != code && !(code == 404 && got == 405) {
			t.Errorf("token %s: %d, want %d", k, got, code)
		}
	}
	// CLI and MCP with that token: same reach; MCP offers only the member's tools
	c := client.New(e.ts.URL, tok.Token)
	projects, err := c.ListProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0].ID != "prj_a" {
		t.Fatalf("cli projects %+v %v", projects, err)
	}
	cm, err := c.Me(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range cli.FilterMCPTools(cli.MCPTools(&cli.App{MCPNoLegacy: true}), cm.Permissions()) {
		names = append(names, x.Tool.Name)
	}
	if !slices.Contains(names, "service_redeploy") || slices.Contains(names, "project_delete") || slices.Contains(names, "service_exec") || slices.Contains(names, "backup_restore") {
		t.Fatalf("mcp tools for a read+deploy member: %v", names)
	}

	// the admin narrows the member: the token follows on the next request
	st := e.srv.st
	ro, none := []string{api.PermRead}, []string{}
	if err := st.UpdateMember(ctx, e.member.ID, store.MemberUpdate{Permissions: &ro}); err != nil {
		t.Fatal(err)
	}
	if as("GET", "/api/v1/auth/me", nil, &me); !slices.Equal(me.Permissions, []string{"read"}) {
		t.Fatalf("token perms after narrowing %+v", me.Permissions)
	}
	if err := st.UpdateMember(ctx, e.member.ID, store.MemberUpdate{Servers: &none}); err != nil {
		t.Fatal(err)
	}
	var left []api.Project
	if code := as("GET", "/api/v1/projects", nil, &left); code != 200 || len(left) != 0 {
		t.Fatalf("projects after unassigning %d %+v", code, left)
	}
	if code := as("GET", "/api/v1/projects/prj_a", nil, nil); code != 404 {
		t.Fatalf("unassigned project still reachable: %d", code)
	}
	// disabled: the token stops at once (UpdateMember also drops it; check the auth path too)
	yes := true
	if err := st.UpdateMember(ctx, e.member.ID, store.MemberUpdate{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if code := as("GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Fatalf("disabled member's token: %d", code)
	}
	if code := e.doWith(http.DefaultClient, adminTok.Token, "GET", "/api/v1/projects/prj_b", nil, nil); code != 200 {
		t.Fatalf("admin token after the member changes: %d", code) // unaffected
	}
}
