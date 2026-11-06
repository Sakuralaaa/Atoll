package server

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"golang.org/x/crypto/bcrypt"
)

// memberClient creates a member with perms on servers and returns its signed-in client.
func memberClient(t *testing.T, e *testEnv, name string, perms, servers []string) *http.Client {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("member-pass"), bcrypt.MinCost)
	if _, err := e.srv.st.CreateMember(context.Background(), name, string(hash), perms, servers, false); err != nil {
		t.Fatal(err)
	}
	jar, _ := cookiejar.New(nil)
	mc := &http.Client{Jar: jar}
	if code := e.doWith(mc, "", "POST", "/api/v1/auth/login", map[string]string{"username": name, "password": "member-pass"}, nil); code != 200 {
		t.Fatalf("login %s: %d", name, code)
	}
	return mc
}

// One-click apps for members: only on an assigned server, only with deploy, and a domain
// in the same request needs the domains permission (app_templates.go).
func TestMemberAppTemplates(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	if err := e.srv.st.CreateNode(context.Background(), api.Server{ID: "srv_b", Name: "b"}); err != nil {
		t.Fatal(err)
	}
	mc := memberClient(t, e, "bob", []string{api.PermRead, api.PermDeploy}, []string{nodeID})
	var er struct{ Code string }
	if code := e.doWith(mc, "", "POST", "/api/v1/app-templates/umami", map[string]string{"server_id": "srv_b", "name": "x"}, &er); code != 400 {
		t.Fatalf("app on an unassigned server: %d", code)
	}
	if code := e.doWith(mc, "", "POST", "/api/v1/app-templates/umami", map[string]string{"server_id": nodeID, "name": "y", "hostname": "y.example.com"}, &er); code != 403 || er.Code != "missing_permission" {
		t.Fatalf("domain without the domains permission: %d %+v", code, er)
	}
	var res api.AppTemplateResult
	if code := e.doWith(mc, "", "POST", "/api/v1/app-templates/umami", map[string]string{"server_id": nodeID, "name": "stats"}, &res); code != 201 || res.Project == nil || res.Project.ServerID != nodeID {
		t.Fatalf("app on its own server: %d %+v", code, res)
	}
	var projects []api.Project
	if code := e.doWith(mc, "", "GET", "/api/v1/projects", nil, &projects); code != 200 || len(projects) != 1 || projects[0].ID != res.Project.ID {
		t.Fatalf("member projects after the app: %d %+v", code, projects)
	}
	// nothing was left behind on the refused requests
	var all []api.Project
	e.mustDo("GET", "/api/v1/projects", nil, &all, 200)
	if len(all) != 1 {
		t.Fatalf("admin sees %d projects, want 1", len(all))
	}
	// read-only member: no deploy, no app
	ro := memberClient(t, e, "carol", []string{api.PermRead}, []string{nodeID})
	if code := e.doWith(ro, "", "POST", "/api/v1/app-templates/umami", map[string]string{"server_id": nodeID, "name": "z"}, &er); code != 403 || er.Code != "missing_permission" {
		t.Fatalf("read-only member: %d %+v", code, er)
	}
}

// GET /root-domains answers a member with names only (rootdomains.go).
func TestMemberRootDomainsNamesOnly(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	if err := e.srv.st.CreateNode(ctx, api.Server{ID: "srv_a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.st.CreateRootDomain(ctx, api.RootDomain{Name: "example.com", ZoneID: "zone123", ZoneName: "example.com", CertMode: "auto"}); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.st.SetRootDomainCert(ctx, "example.com", "acme", "token lacks SSL permission", "", nil, "", "", false); err != nil {
		t.Fatal(err)
	}
	var admin []map[string]any
	e.mustDo("GET", "/api/v1/root-domains", nil, &admin, 200)
	if len(admin) != 1 || admin[0]["zone_id"] != "zone123" || admin[0]["cert_error"] == nil {
		t.Fatalf("admin view: %+v", admin)
	}
	mc := memberClient(t, e, "bob", []string{api.PermRead, api.PermDeploy}, []string{"srv_a"})
	bob, _ := e.srv.st.UserByName(ctx, "bob")
	if err := e.srv.st.SetMemberRootDomains(ctx, bob.ID, []string{"example.com"}); err != nil { // B7
		t.Fatal(err)
	}
	var got []map[string]any
	if code := e.doWith(mc, "", "GET", "/api/v1/root-domains", nil, &got); code != 200 || len(got) != 1 {
		t.Fatalf("member root domains: %d %+v", code, got)
	}
	if got[0]["name"] != "example.com" || got[0]["default"] != true || len(got[0]) != 2 {
		t.Fatalf("member must see only name/default: %+v", got[0])
	}
	// writes stay admin-only
	if code := e.doWith(mc, "", "POST", "/api/v1/root-domains", map[string]string{"name": "example.org"}, nil); code != 403 {
		t.Fatalf("member creates a root domain: %d", code)
	}
	if code := e.doWith(mc, "", "DELETE", "/api/v1/root-domains/example.com", nil, nil); code != 403 {
		t.Fatalf("member deletes a root domain: %d", code)
	}
}
