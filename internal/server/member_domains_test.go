package server

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Sub-accounts B7: a member binds only strict subdomains of the roots an admin allowed it;
// hostnames outside every configured root stay allowed; admins are not limited
// (member_domains.go).
func TestMemberRootDomainLimits(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	e.srv.cfg.LookupHost = func(ctx context.Context, host string) ([]string, error) {
		return nil, fmt.Errorf("no such host %s", host)
	}
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	for _, n := range []string{"example.com", "example.org"} {
		if err := e.srv.st.CreateRootDomain(ctx, api.RootDomain{Name: n, CertMode: "global"}); err != nil {
			t.Fatal(err)
		}
	}
	var prj api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "web", "server_id": nodeID}, &prj, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+prj.ID+"/services", map[string]any{
		"name": "app", "kind": "image", "image": "traefik/whoami",
		"ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}},
	}, &svc, 201)
	mc := memberClient(t, e, "bob", []string{api.PermRead, api.PermDeploy, api.PermDomains}, []string{nodeID})
	bob, err := e.srv.st.UserByName(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	userPath := "/api/v1/users/" + strconv.FormatInt(bob.ID, 10)

	bind := func(body map[string]any) (int, string) {
		t.Helper()
		var er struct{ Code string }
		body["proxied"] = false
		code := e.doWith(mc, "", "POST", "/api/v1/services/"+svc.ID+"/domains", body, &er)
		return code, er.Code
	}
	expect := func(what string, body map[string]any, wantCode int, wantErr string) {
		t.Helper()
		if code, ec := bind(body); code != wantCode || (wantErr != "" && ec != wantErr) {
			t.Fatalf("%s: %d %s (want %d %s)", what, code, ec, wantCode, wantErr)
		}
	}

	// upgraded / new member: no configured root allowed, and GET /root-domains is empty
	var roots []map[string]any
	if code := e.doWith(mc, "", "GET", "/api/v1/root-domains", nil, &roots); code != 200 || len(roots) != 0 {
		t.Fatalf("member roots before any grant: %d %+v", code, roots)
	}
	expect("subdomain of a root not granted", map[string]any{"hostname": "a.example.com"}, 403, "member_root_denied")
	expect("own external domain", map[string]any{"hostname": "blog.mine.net"}, 201, "")

	// admin: unknown root refused, then grant example.com
	if code := e.do("PATCH", userPath, map[string]any{"root_domains": []string{"nope.example"}}, nil); code != 400 {
		t.Fatalf("unknown root granted: %d", code)
	}
	var uv userView
	e.mustDo("PATCH", userPath, map[string]any{"root_domains": []string{"Example.com."}}, &uv, 200)
	if len(uv.RootDomains) != 1 || uv.RootDomains[0] != "example.com" {
		t.Fatalf("user view root_domains %+v", uv)
	}
	if code := e.doWith(mc, "", "GET", "/api/v1/root-domains", nil, &roots); code != 200 || len(roots) != 1 || roots[0]["name"] != "example.com" {
		t.Fatalf("member roots after the grant: %d %+v", code, roots)
	}

	expect("granted subdomain", map[string]any{"hostname": "a.example.com"}, 201, "")
	expect("prefix with explicit root", map[string]any{"hostname": "b", "root_domain": "example.com"}, 201, "")
	expect("apex", map[string]any{"hostname": "example.com"}, 403, "member_domain_apex")
	expect("apex via root_domain", map[string]any{"hostname": "", "root_domain": "example.com"}, 403, "member_domain_apex")
	expect("wildcard", map[string]any{"hostname": "*.example.com"}, 403, "member_domain_wildcard")
	expect("other root", map[string]any{"hostname": "c.example.org"}, 403, "member_root_denied")
	expect("other root by prefix", map[string]any{"hostname": "c", "root_domain": "example.org"}, 403, "member_root_denied")
	expect("taken hostname", map[string]any{"hostname": "a.example.com"}, 409, "")

	// project default root
	if code := e.doWith(mc, "", "PATCH", "/api/v1/projects/"+prj.ID, map[string]any{"root_domain": "example.org"}, nil); code != 403 {
		t.Fatalf("member sets a project root it was not given: %d", code)
	}
	if code := e.doWith(mc, "", "PATCH", "/api/v1/projects/"+prj.ID, map[string]any{"root_domain": "example.com"}, nil); code != 200 {
		t.Fatalf("member sets its own root: %d", code)
	}
	expect("bare name under the project root", map[string]any{"hostname": "d"}, 201, "")
	if code := e.doWith(mc, "", "POST", "/api/v1/projects", map[string]any{"name": "p2", "server_id": nodeID, "root_domain": "example.org"}, nil); code != 403 {
		t.Fatalf("member creates a project with a root it was not given: %d", code)
	}
	// an admin-chosen root stays when the member edits something else
	e.mustDo("PATCH", "/api/v1/projects/"+prj.ID, map[string]any{"root_domain": "example.org"}, nil, 200)
	if code := e.doWith(mc, "", "PATCH", "/api/v1/projects/"+prj.ID, map[string]any{"description": "x", "root_domain": "example.org"}, nil); code != 200 {
		t.Fatalf("member keeps an admin-chosen root: %d", code)
	}
	expect("bare name under an admin-chosen root", map[string]any{"hostname": "e"}, 403, "member_root_denied")

	// one-click apps are refused before their project exists
	var before []api.Project
	e.mustDo("GET", "/api/v1/projects", nil, &before, 200)
	for _, h := range []string{"x.example.org", "example.com", "*.example.com"} {
		if code := e.doWith(mc, "", "POST", "/api/v1/app-templates/umami", map[string]any{"server_id": nodeID, "name": "u", "hostname": h}, nil); code != 403 {
			t.Fatalf("app template with %s: %d", h, code)
		}
	}
	var after []api.Project
	e.mustDo("GET", "/api/v1/projects", nil, &after, 200)
	if len(after) != len(before) {
		t.Fatalf("a refused app left a project behind: %d -> %d", len(before), len(after))
	}

	// admins are not limited
	var d api.Domain
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/domains", map[string]any{"hostname": "example.org", "proxied": false}, &d, 201)

	// taking the grant back leaves bound domains in place
	uv = userView{}
	e.mustDo("PATCH", userPath, map[string]any{"root_domains": []string{}}, &uv, 200)
	if len(uv.RootDomains) != 0 {
		t.Fatalf("grant not cleared %+v", uv)
	}
	ds, err := e.srv.st.ListDomains(ctx, svc.ID, "")
	if err != nil || len(ds) != 5 { // blog.mine.net, a, b, d .example.com, example.org
		t.Fatalf("bound domains after revoking: %d %v", len(ds), err)
	}
	expect("after revoking", map[string]any{"hostname": "f.example.com"}, 403, "member_root_denied")
}
