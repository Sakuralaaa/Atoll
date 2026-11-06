package server

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// B3: an admin caps a member's CPU, memory and number of services; creating or changing
// services, one-click apps and migrations are checked; admins are never limited.
func TestMemberQuotas(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	if err := e.srv.st.CreateNode(ctx, api.Server{ID: "srv_b", Name: "b"}); err != nil {
		t.Fatal(err)
	}
	mc := memberClient(t, e, "bob", []string{api.PermRead, api.PermDeploy, api.PermMigrate}, []string{nodeID, "srv_b"})
	mdo := func(method, path string, body, out any) int { return e.doWith(mc, "", method, path, body, out) }
	var users []userView
	e.mustDo("GET", "/api/v1/users", nil, &users, 200)
	var bob userView
	for _, u := range users {
		if u.Username == "bob" {
			bob = u
		}
	}
	if bob.Quota != nil || bob.Usage == nil || bob.Usage.Services != 0 {
		t.Fatalf("no quota yet %+v", bob)
	}
	uid := strconv.FormatInt(bob.ID, 10)
	e.mustDo("PATCH", "/api/v1/users/"+uid, map[string]any{"quota": map[string]any{"cpu": -1}}, nil, 400)
	e.mustDo("PATCH", "/api/v1/users/"+uid, map[string]any{"quota": map[string]any{"cpu": 2, "mem_mb": 1024, "services": 2}}, &bob, 200)
	if bob.Quota == nil || bob.Quota.CPU != 2 || bob.Quota.Services != 2 {
		t.Fatalf("quota %+v", bob.Quota)
	}

	var proj api.Project
	if code := mdo("POST", "/api/v1/projects", map[string]string{"name": "mine", "server_id": nodeID}, &proj); code != 201 {
		t.Fatalf("project %d", code)
	}
	svcs := "/api/v1/projects/" + proj.ID + "/services"
	var er struct{ Code, Error string }
	var s1 api.Service
	if code := mdo("POST", svcs, map[string]any{"name": "a", "image": "nginx", "cpu_limit": 1, "mem_limit_mb": 512}, &s1); code != 201 {
		t.Fatalf("first service %d", code)
	}
	if code := mdo("POST", svcs, map[string]any{"name": "b", "image": "nginx", "cpu_limit": 1.5, "mem_limit_mb": 256}, &er); code != 403 || er.Code != "quota_exceeded" {
		t.Fatalf("over the CPU quota: %d %+v", code, er)
	}
	if code := mdo("POST", svcs, map[string]any{"name": "b", "image": "nginx", "cpu_limit": 0.5, "mem_limit_mb": 1024}, &er); code != 403 || er.Code != "quota_exceeded" {
		t.Fatalf("over the memory quota: %d %+v", code, er)
	}
	if code := mdo("POST", svcs, map[string]any{"name": "b", "image": "nginx", "cpu_limit": -1, "mem_limit_mb": 256}, &er); code != 403 || er.Code != "quota_limit_required" {
		t.Fatalf("no CPU limit under a quota: %d %+v", code, er)
	}
	var s2 api.Service
	if code := mdo("POST", svcs, map[string]any{"name": "b", "image": "nginx", "mem_limit_mb": 256}, &s2); code != 201 || s2.CPULimit != memberDefaultCPU {
		t.Fatalf("service without a CPU limit gets the member default: %d %+v", code, s2) // 1 + 1 = 2 cores
	}
	if code := mdo("POST", svcs, map[string]any{"name": "c", "image": "nginx", "cpu_limit": 0, "mem_limit_mb": 6}, &er); code != 403 || er.Code != "quota_exceeded" {
		t.Fatalf("over the service count: %d %+v", code, er)
	}
	// changing limits: up is checked, down is always fine, "no limit" is refused
	if code := mdo("PATCH", "/api/v1/services/"+s1.ID, map[string]any{"cpu_limit": 1.5}, &er); code != 403 || er.Code != "quota_exceeded" {
		t.Fatalf("raising the CPU limit over the quota: %d %+v", code, er)
	}
	if code := mdo("PATCH", "/api/v1/services/"+s1.ID, map[string]any{"cpu_limit": 0.5}, nil); code != 200 {
		t.Fatalf("lowering the CPU limit: %d", code)
	}
	if code := mdo("PATCH", "/api/v1/services/"+s1.ID, map[string]any{"cpu_limit": 0}, &er); code != 403 || er.Code != "quota_limit_required" {
		t.Fatalf("removing the CPU limit: %d %+v", code, er)
	}
	var me meResponse
	if code := mdo("GET", "/api/v1/auth/me", nil, &me); code != 200 || me.Quota == nil || me.Usage == nil || me.Usage.Services != 2 || me.Usage.CPU != 1.5 || me.Usage.MemMB != 768 {
		t.Fatalf("me quota/usage %d %+v %+v", code, me.Quota, me.Usage)
	}
	// one-click app over the service count: refused, nothing left behind
	if code := mdo("POST", "/api/v1/app-templates/umami", map[string]string{"server_id": nodeID, "name": "stats"}, &er); code != 403 || er.Code != "quota_exceeded" {
		t.Fatalf("app over the quota: %d %+v", code, er)
	}
	var mine []api.Project
	if mdo("GET", "/api/v1/projects", nil, &mine); len(mine) != 1 {
		t.Fatalf("app left a project behind: %+v", mine)
	}
	// a rollback restores old limits: not past the quota, not "no limit"
	for i, lim := range []struct {
		cpu  float64
		mem  int
		code string
	}{{3, 256, "quota_exceeded"}, {0, 256, "quota_limit_required"}} {
		dep := api.Deployment{ID: "dep_q" + strconv.Itoa(i), ServiceID: s1.ID, ServerID: nodeID, Status: "superseded", Trigger: "manual", StartedAt: time.Now()}
		if err := e.srv.st.CreateDeployment(ctx, dep, api.ResolvedSpec{Image: "nginx:old", CPULimit: lim.cpu, MemLimitMB: lim.mem}); err != nil {
			t.Fatal(err)
		}
		if code := mdo("POST", "/api/v1/services/"+s1.ID+"/rollback", map[string]string{"deployment_id": dep.ID}, &er); code != 403 || er.Code != lim.code {
			t.Fatalf("rollback to %+v: %d %+v", lim, code, er)
		}
	}
	// the admin is not limited, also inside the member's project
	e.mustDo("POST", svcs, map[string]any{"name": "admin-extra", "image": "nginx", "cpu_limit": 4}, nil, 201)

	// migration: carol shares srv_b with a one-service quota; moving bob's project there
	// would put it in her projects too
	memberClient(t, e, "carol", []string{api.PermRead}, []string{"srv_b"})
	var carol userView
	e.mustDo("GET", "/api/v1/users", nil, &users, 200)
	for _, u := range users {
		if u.Username == "carol" {
			carol = u
		}
	}
	e.mustDo("PATCH", "/api/v1/users/"+strconv.FormatInt(carol.ID, 10), map[string]any{"quota": map[string]any{"services": 1}}, nil, 200)
	if code := mdo("POST", "/api/v1/projects/"+proj.ID+"/migrate", map[string]string{"server_id": "srv_b"}, &er); code != 403 || er.Code != "quota_exceeded" {
		t.Fatalf("migration over another member's quota: %d %+v", code, er)
	}
	// clearing the quota lifts every limit
	var cleared userView
	e.mustDo("PATCH", "/api/v1/users/"+uid, map[string]any{"quota": map[string]any{}}, &cleared, 200)
	if cleared.Quota != nil {
		t.Fatalf("cleared quota %+v", cleared.Quota)
	}
	if code := mdo("POST", svcs, map[string]any{"name": "d", "image": "nginx", "cpu_limit": -1}, nil); code != 201 {
		t.Fatalf("no quota: %d", code)
	}
}
