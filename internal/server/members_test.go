package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
	"golang.org/x/crypto/bcrypt"
)

// A member sees only projects on its servers; panel-wide routes are refused; disabling
// it ends its session at once (notes/subaccounts.md).
func TestMemberScope(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	st := e.srv.st
	for _, id := range []string{"srv_a", "srv_b"} {
		if err := st.CreateNode(ctx, api.Server{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []api.Project{{ID: "prj_a", Name: "a", ServerID: "srv_a"}, {ID: "prj_b", Name: "b", ServerID: "srv_b"}} {
		if err := st.CreateProject(ctx, p, false); err != nil {
			t.Fatal(err)
		}
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("member-pass"), bcrypt.MinCost)
	m, err := st.CreateMember(ctx, "bob", string(hash), []string{api.PermRead, api.PermDeploy, api.PermSettings}, []string{"srv_a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	mc := &http.Client{Jar: jar}
	if code := e.doWith(mc, "", "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "member-pass"}, nil); code != 200 {
		t.Fatalf("login %d", code)
	}
	var me meResponse
	if code := e.doWith(mc, "", "GET", "/api/v1/auth/me", nil, &me); code != 200 || me.Role != "member" || len(me.Servers) != 1 {
		t.Fatalf("me %d %+v", code, me)
	}
	for _, x := range me.Permissions {
		if x == api.PermSettings {
			t.Fatal("settings permission must never reach a member")
		}
	}
	var projects []api.Project
	if code := e.doWith(mc, "", "GET", "/api/v1/projects", nil, &projects); code != 200 || len(projects) != 1 || projects[0].ID != "prj_a" {
		t.Fatalf("projects %d %+v", code, projects)
	}
	want := map[string]int{
		"GET /api/v1/projects/prj_a":      200,
		"GET /api/v1/projects/prj_b":      404,
		"GET /api/v1/settings":            403,
		"GET /api/v1/tokens":              403,
		"GET /api/v1/servers/srv_b":       404,
		"GET /api/v1/servers/srv_a":       200,
		"POST /api/v1/servers/join-token": 403,
	}
	for k, code := range want {
		method, path, _ := cutSpace(k)
		if got := e.doWith(mc, "", method, path, nil, nil); got != code {
			t.Errorf("%s: %d, want %d", k, got, code)
		}
	}
	var servers []api.Server
	if code := e.doWith(mc, "", "GET", "/api/v1/servers", nil, &servers); code != 200 || len(servers) != 1 || servers[0].ID != "srv_a" {
		t.Fatalf("servers %d %+v", code, servers)
	}
	if code := e.doWith(mc, "", "POST", "/api/v1/projects", map[string]string{"name": "c", "server_id": "srv_b"}, nil); code != 400 {
		t.Fatalf("project on an unassigned server: %d", code)
	}
	if code := e.doWith(mc, "", "POST", "/api/v1/projects/prj_a/migrate", map[string]string{"server_id": "srv_b"}, nil); code == 200 || code == 202 {
		t.Fatalf("migrate to an unassigned server: %d", code)
	}
	yes := true
	if err := st.UpdateMember(ctx, m.ID, store.MemberUpdate{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if code := e.doWith(mc, "", "GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Fatalf("disabled member still signed in: %d", code)
	}
	if code := e.doWith(mc, "", "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "member-pass"}, nil); code != 403 {
		t.Fatalf("disabled login %d", code)
	}
	// the admin is unaffected
	if code := e.do("GET", "/api/v1/settings", nil, nil); code != 200 {
		t.Fatalf("admin settings %d", code)
	}
}

func cutSpace(s string) (string, string, bool) {
	for i := range s {
		if s[i] == ' ' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}

// Admin creates, changes and deletes a member through the API; a member cannot.
func TestUserManagementAPI(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	if err := e.srv.st.CreateNode(ctx, api.Server{ID: "srv_a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	var u userView
	e.mustDo("POST", "/api/v1/users", map[string]any{"username": "bob", "password": "member-pass", "servers": []string{"srv_a"}}, &u, 201)
	if u.Role != "member" || len(u.Servers) != 1 || !u.MustChangePassword || len(u.Permissions) != 2 {
		t.Fatalf("created %+v", u)
	}
	e.mustDo("POST", "/api/v1/users", map[string]any{"username": "eve", "password": "member-pass", "permissions": []string{"servers"}}, nil, 400)
	e.mustDo("POST", "/api/v1/users", map[string]any{"username": "eve", "password": "member-pass", "servers": []string{"srv_nope"}}, nil, 400)
	e.mustDo("POST", "/api/v1/users", map[string]any{"username": "bob", "password": "member-pass"}, nil, 409)
	var list []userView
	e.mustDo("GET", "/api/v1/users", nil, &list, 200)
	if len(list) != 2 || list[0].Role != "admin" {
		t.Fatalf("list %+v", list)
	}
	// admin accounts are not editable here
	e.mustDo("PATCH", "/api/v1/users/"+i64s(list[0].ID), map[string]any{"disabled": true}, nil, 409)
	// a member cannot manage accounts
	jar, _ := cookiejar.New(nil)
	mc := &http.Client{Jar: jar}
	if code := e.doWith(mc, "", "POST", "/api/v1/auth/login", map[string]string{"username": "bob", "password": "member-pass"}, nil); code != 200 {
		t.Fatalf("login %d", code)
	}
	if code := e.doWith(mc, "", "GET", "/api/v1/users", nil, nil); code != 403 {
		t.Fatalf("member list users %d", code)
	}
	var pu userView
	e.mustDo("PATCH", "/api/v1/users/"+i64s(u.ID), map[string]any{"servers": []string{}, "permissions": []string{"exec"}}, &pu, 200)
	if len(pu.Servers) != 0 || len(pu.Permissions) != 2 {
		t.Fatalf("patched %+v", pu)
	}
	e.mustDo("DELETE", "/api/v1/users/"+i64s(u.ID), nil, nil, 204)
	if code := e.doWith(mc, "", "GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Fatalf("deleted member still signed in: %d", code)
	}
}

func i64s(n int64) string { return strconv.FormatInt(n, 10) }

func TestMemberSeesEvent(t *testing.T) {
	e := newTestEnv(t)
	p := &principal{Member: true, Servers: map[string]bool{"srv_a": true}, Projects: map[string]bool{"prj_a": true}}
	cases := map[string]bool{
		`{"type":"server","payload":{"id":"srv_a"}}`:                                           true,
		`{"type":"server","payload":{"id":"srv_b"}}`:                                           false,
		`{"type":"project","payload":{"id":"prj_a"}}`:                                          true,
		`{"type":"project","payload":{"id":"prj_b"}}`:                                          false,
		`{"type":"service","payload":{"id":"svc_1","project_id":"prj_a"}}`:                     true,
		`{"type":"service","payload":{"id":"svc_1","project_id":"prj_a","server_id":"srv_b"}}`: false,
		`{"type":"deployment","payload":{"service_id":"svc_missing"}}`:                         false,
		`{"type":"confirmation","payload":{"project_id":"prj_a"}}`:                             false,
		`{"type":"whatever","payload":{}}`:                                                     false,
	}
	for msg, want := range cases {
		if got := e.srv.memberSeesEvent(context.Background(), p, []byte(msg)); got != want {
			t.Errorf("%s: %v, want %v", msg, got, want)
		}
	}
}
