package server

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// A project's services can be placed on different servers. A member assigned only to the
// project's home server must not learn about or operate on services placed elsewhere.
func TestMemberCrossServerProjectScope(t *testing.T) {
	e := newMemberEnv(t, api.PermRead, api.PermDeploy, api.PermDelete, api.PermMigrate)
	ctx := context.Background()
	now := time.Now().UTC()
	mkService := func(id, name, serverID string) {
		t.Helper()
		v := api.Service{ID: id, ProjectID: "prj_a", Name: name, Kind: "image", Image: "nginx:1.27", Status: "running", CreatedAt: now,
			Ports: []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}}}
		if err := e.srv.st.CreateService(ctx, v); err != nil {
			t.Fatal(err)
		}
		if err := e.srv.st.SetServiceServer(ctx, id, serverID); err != nil {
			t.Fatal(err)
		}
	}
	mkService("svc_a", "visible", "srv_a")
	mkService("svc_b", "hidden", "srv_b")
	if err := e.srv.st.CreateDomain(ctx, api.Domain{ID: "dom_b", ServiceID: "svc_b", Hostname: "hidden.example.test", PortName: "web", Status: "active", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	wantVisible := func(project api.Project) {
		t.Helper()
		if len(project.Services) != 1 || project.Services[0].ID != "svc_a" {
			t.Fatalf("project exposed services outside assigned servers: %+v", project.Services)
		}
	}
	var projects []api.Project
	if code := e.mdo("GET", "/api/v1/projects", nil, &projects); code != 200 || len(projects) != 1 || projects[0].ID != "prj_a" {
		t.Fatalf("projects: %d %+v", code, projects)
	}
	wantVisible(projects[0])
	var project api.Project
	if code := e.mdo("GET", "/api/v1/projects/prj_a", nil, &project); code != 200 {
		t.Fatalf("project detail: %d", code)
	}
	wantVisible(project)
	var services []api.Service
	if code := e.mdo("GET", "/api/v1/projects/prj_a/services", nil, &services); code != 200 || len(services) != 1 || services[0].ID != "svc_a" {
		t.Fatalf("project services: %d %+v", code, services)
	}
	var domains []api.Domain
	if code := e.mdo("GET", "/api/v1/domains", nil, &domains); code != 200 || slices.ContainsFunc(domains, func(d api.Domain) bool { return d.ID == "dom_b" || d.Hostname == "hidden.example.test" }) {
		t.Fatalf("global domains leaked an unassigned service: %d %+v", code, domains)
	}
	var updates []api.ImageUpdate
	if code := e.mdo("GET", "/api/v1/image-updates", nil, &updates); code != 200 || slices.ContainsFunc(updates, func(u api.ImageUpdate) bool { return u.ServiceID == "svc_b" }) {
		t.Fatalf("image updates leaked an unassigned service: %d %+v", code, updates)
	}

	// Operations that affect every service in the project must fail closed.
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{"DELETE", "/api/v1/projects/prj_a", nil},
		{"POST", "/api/v1/projects/prj_a/migrate", map[string]string{"server_id": "srv_b"}},
		{"PUT", "/api/v1/projects/prj_a/service-order", map[string][]string{"services": {"svc_a"}}},
	} {
		if code := e.mdo(tc.method, tc.path, tc.body, nil); code != 404 {
			t.Errorf("%s %s: got %d, want hidden (404)", tc.method, tc.path, code)
		}
	}
	// The project and both services remain intact after denied operations.
	var admin api.Project
	e.mustDo("GET", "/api/v1/projects/prj_a", nil, &admin, 200)
	if len(admin.Services) != 2 {
		t.Fatalf("denied project operation changed services: %+v", admin.Services)
	}
	var all []api.Service
	if code := e.do("GET", "/api/v1/projects/prj_a/services", nil, &all); code != 200 || len(all) != 2 {
		t.Fatalf("denied project operation changed services: %d %+v", code, all)
	}
}
