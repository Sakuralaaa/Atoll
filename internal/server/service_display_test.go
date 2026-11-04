package server

import (
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestServiceDisplayNameNoRedeploy(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "postgresql", "kind": "image", "image": "postgres:16"}, &svc, 201)
	deps := func() int {
		var d []api.Deployment
		e.mustDo("GET", "/api/v1/services/"+svc.ID+"/deployments", nil, &d, 200)
		return len(d)
	}
	before := deps()

	var got api.Service
	e.mustDo("PATCH", "/api/v1/services/"+svc.ID, map[string]any{"display_name": "  主数据库 "}, &got, 200)
	if got.DisplayName != "主数据库" || got.Name != "postgresql" {
		t.Fatalf("got %q / %q", got.DisplayName, got.Name)
	}
	// the panel sends the whole form back with only the label edited: still no redeploy
	e.mustDo("PATCH", "/api/v1/services/"+svc.ID, map[string]any{"display_name": "DB", "name": "postgresql", "image": "postgres:16"}, &got, 200)
	if got.DisplayName != "DB" || deps() != before {
		t.Fatalf("label %q, deployments %d -> %d", got.DisplayName, before, deps())
	}
	var p2 api.Project
	e.mustDo("GET", "/api/v1/projects/"+p.ID, nil, &p2, 200)
	if len(p2.Services) != 1 || p2.Services[0].DisplayName != "DB" {
		t.Fatalf("project services %+v", p2.Services)
	}
	// a real change alongside it still redeploys
	got = api.Service{}
	e.mustDo("PATCH", "/api/v1/services/"+svc.ID, map[string]any{"display_name": "", "image": "postgres:17"}, &got, 200)
	if got.DisplayName != "" || deps() != before+1 {
		t.Fatalf("label %q, deployments %d", got.DisplayName, deps())
	}
	if code := e.do("PATCH", "/api/v1/services/"+svc.ID, map[string]any{"display_name": strings.Repeat("长", 65)}, nil); code != 400 {
		t.Fatalf("long label: %d", code)
	}
}

func TestProjectDisplayName(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var a, b api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop-zeabur", "display_name": " 商城 ", "server_id": nodeID}, &a, 201)
	if a.DisplayName != "商城" || a.Name != "shop-zeabur" {
		t.Fatalf("create: %q / %q", a.DisplayName, a.Name)
	}
	// labels need not be unique; names still are
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop-old", "server_id": nodeID}, &b, 201)
	e.mustDo("PATCH", "/api/v1/projects/"+b.ID, map[string]any{"display_name": "商城"}, &b, 200)
	if b.DisplayName != "商城" || b.Name != "shop-old" {
		t.Fatalf("patch: %q / %q", b.DisplayName, b.Name)
	}
	if code := e.do("PATCH", "/api/v1/projects/"+b.ID, map[string]any{"name": "shop-zeabur"}, nil); code != 409 {
		t.Fatalf("rename onto taken name: %d", code)
	}
	var list []api.Project
	e.mustDo("GET", "/api/v1/projects", nil, &list, 200)
	n := 0
	for _, p := range list {
		if p.DisplayName == "商城" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("list labels: %+v", list)
	}
	if code := e.do("PATCH", "/api/v1/projects/"+b.ID, map[string]any{"display_name": strings.Repeat("长", 65)}, nil); code != 400 {
		t.Fatalf("long label: %d", code)
	}
}
