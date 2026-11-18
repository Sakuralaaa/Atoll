package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/registry"
)

func waitDBUpgrade(t *testing.T, e *testEnv, serviceID string) api.DBUpgrade {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var info api.DBUpgradeInfo
		e.mustDo("GET", "/api/v1/services/"+serviceID+"/db-upgrade", nil, &info, 200)
		if info.Last != nil && info.Last.Status != api.DBUpgradeRunning {
			return *info.Last
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("upgrade did not finish")
	return api.DBUpgrade{}
}

// startDBUpgrade starts an upgrade, retrying while the image check that follows the previous
// one still holds the service's update lock.
func startDBUpgrade(t *testing.T, e *testEnv, serviceID, tag string) api.DBUpgrade {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var u api.DBUpgrade
		code := e.do("POST", "/api/v1/services/"+serviceID+"/db-upgrade", api.DBUpgradeRequest{Tag: tag}, &u)
		if code == 202 {
			return u
		}
		if code != 409 || time.Now().After(deadline) {
			t.Fatalf("start upgrade to %s: %d", tag, code)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDBUpgradeFlow(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	reg := newMiniRegistry(t)
	rc := registry.New()
	rc.PlainHTTP = func(string) bool { return true }
	rc.DigestTTL, rc.TagsTTL = 0, 0
	e.srv.upd.reg = rc
	for tag, d := range map[string]string{"17": "sha256:1700", "18": "sha256:1800", "19": "sha256:1900", "20": "sha256:2000", "21": "sha256:2100"} {
		reg.set(tag, d)
	}
	reg.tags = []string{"17", "18", "19", "20", "21"}

	hello := api.HelloPayload{AgentVersion: "test", Hostname: "box", OS: "linux", Arch: "amd64", CPUCores: 2, MemMB: 4096, DiskGB: 80, PublicIPv4: "203.0.113.7", DBUpgrade: true}
	agent, nodeID := startFakeAgentHello(t, e, api.ZeaburInventory{}, hello)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	var db, app api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"kind": "postgres", "name": "postgres"}, &db, 201)
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "app", "kind": "image", "image": "ghcr.io/acme/app:1"}, &app, 201)
	svc, _, _ := e.srv.st.GetService(ctx, db.ID)
	base := reg.host() + "/acme/acme-api"
	svc.Image = base + ":18"
	if err := e.srv.st.UpdateServiceSpec(ctx, *svc); err != nil {
		t.Fatal(err)
	}

	var info api.DBUpgradeInfo
	e.mustDo("GET", "/api/v1/services/"+db.ID+"/db-upgrade", nil, &info, 200)
	if !info.Supported || info.Last != nil {
		t.Fatalf("info: %+v", info)
	}
	e.mustDo("GET", "/api/v1/services/"+app.ID+"/db-upgrade", nil, &info, 200)
	if info.Supported || !strings.Contains(info.Reason, "PostgreSQL") {
		t.Fatalf("app info: %+v", info)
	}
	if code := e.do("POST", "/api/v1/services/"+app.ID+"/db-upgrade", api.DBUpgradeRequest{Tag: "2"}, nil); code != 409 {
		t.Fatalf("app upgrade: %d", code)
	}
	for _, tag := range []string{"17", "18", "latest", "19:x"} {
		if code := e.do("POST", "/api/v1/services/"+db.ID+"/db-upgrade", api.DBUpgradeRequest{Tag: tag}, nil); code != 400 {
			t.Fatalf("tag %q: %d", tag, code)
		}
	}

	var failApply atomic.Value
	failApply.Store("")
	agent.mu.Lock()
	agent.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		switch env.Type {
		case api.MsgDBUpgrade:
			return api.ResultPayload{OK: true, Output: `{"dump_bytes":4096,"before":"databases=2 tables=5","after":"databases=2 tables=5"}`}, true
		case api.MsgDBUpgradeRollback, api.MsgDBUpgradeCleanup:
			return api.ResultPayload{OK: true}, true
		case api.MsgApply:
			var ap api.ApplyPayload
			json.Unmarshal(env.Payload, &ap)
			if f := failApply.Load().(string); f != "" && strings.Contains(ap.Spec.Image, f) {
				return api.ResultPayload{OK: false, Error: "container exited"}, true
			}
		}
		return api.ResultPayload{}, false
	}
	agent.mu.Unlock()

	// 18 → 19: converted, deployed, old data kept
	var u api.DBUpgrade
	u = startDBUpgrade(t, e, db.ID, "19")
	if u.Status != api.DBUpgradeRunning || u.FromImage != base+":18" || u.ToImage != base+":19" {
		t.Fatalf("started: %+v", u)
	}
	u = waitDBUpgrade(t, e, db.ID)
	if u.Status != api.DBUpgradeDone || !u.OldKept || u.Steps[0].Status != "done" || u.Steps[1].Status != "done" || !strings.Contains(u.Steps[0].Detail, "tables=5") {
		t.Fatalf("done: %+v", u)
	}
	pl, n := lastPayload[api.DBUpgradePayload](t, agent, api.MsgDBUpgrade)
	if n != 1 || pl.Kind != "postgres" || pl.Image != base+"@sha256:1900" || pl.ServiceID != db.ID {
		t.Fatalf("agent payload %+v (%d)", pl, n)
	}
	if svc, _, _ = e.srv.st.GetService(ctx, db.ID); svc.Image != base+":19" {
		t.Fatalf("image after upgrade: %s", svc.Image)
	}

	// the kept data blocks the next upgrade until it is deleted
	if code := e.do("POST", "/api/v1/services/"+db.ID+"/db-upgrade", api.DBUpgradeRequest{Tag: "20"}, nil); code != 409 {
		t.Fatalf("upgrade with kept data: %d", code)
	}
	u = api.DBUpgrade{}
	e.mustDo("DELETE", "/api/v1/services/"+db.ID+"/db-upgrade/old", nil, &u, 200)
	if u.OldKept {
		t.Fatal("still kept after cleanup")
	}
	if _, n := lastPayload[api.DBUpgradeRef](t, agent, api.MsgDBUpgradeCleanup); n != 1 {
		t.Fatal("cleanup not sent")
	}
	if code := e.do("POST", "/api/v1/services/"+db.ID+"/db-upgrade/rollback", nil, nil); code != 409 {
		t.Fatalf("rollback with nothing kept: %d", code)
	}

	// 19 → 20: the new version does not start, so the server rolls back on its own
	failApply.Store("sha256:2000")
	u = startDBUpgrade(t, e, db.ID, "20")
	u = waitDBUpgrade(t, e, db.ID)
	if u.Status != api.DBUpgradeRolledBack || u.OldKept || u.Steps[1].Status != "failed" {
		t.Fatalf("failed deploy: %+v", u)
	}
	if _, n := lastPayload[api.DBUpgradeRef](t, agent, api.MsgDBUpgradeRollback); n != 1 {
		t.Fatal("rollback not sent")
	}
	if svc, _, _ = e.srv.st.GetService(ctx, db.ID); svc.Image != base+":19" {
		t.Fatalf("image after automatic rollback: %s", svc.Image)
	}

	// 19 → 21, then rolled back by the user
	failApply.Store("")
	u = startDBUpgrade(t, e, db.ID, "21")
	if u = waitDBUpgrade(t, e, db.ID); u.Status != api.DBUpgradeDone {
		t.Fatalf("21: %+v", u)
	}
	u = api.DBUpgrade{}
	e.mustDo("POST", "/api/v1/services/"+db.ID+"/db-upgrade/rollback", nil, &u, 200)
	if u.Status != api.DBUpgradeRolledBack || u.OldKept {
		t.Fatalf("manual rollback: %+v", u)
	}
	if svc, _, _ = e.srv.st.GetService(ctx, db.ID); svc.Image != base+":19" {
		t.Fatalf("image after manual rollback: %s", svc.Image)
	}

	// a failed conversion changes nothing
	agent.mu.Lock()
	agent.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type == api.MsgDBUpgrade {
			return api.ResultPayload{OK: false, Error: "the converted data differs: databases=2 tables=5 before, databases=2 tables=4 after (nothing changed)"}, true
		}
		return api.ResultPayload{}, false
	}
	agent.mu.Unlock()
	u = startDBUpgrade(t, e, db.ID, "20")
	u = waitDBUpgrade(t, e, db.ID)
	if u.Status != api.DBUpgradeFailed || u.OldKept || !strings.Contains(u.Error, "differs") || u.Steps[1].Status != "skipped" {
		t.Fatalf("failed conversion: %+v", u)
	}
	if svc, _, _ = e.srv.st.GetService(ctx, db.ID); svc.Image != base+":19" {
		t.Fatalf("image after failed conversion: %s", svc.Image)
	}
}

func TestDBUpgradeOldAgent(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	var db api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"kind": "postgres", "name": "postgres"}, &db, 201)
	var info api.DBUpgradeInfo
	e.mustDo("GET", "/api/v1/services/"+db.ID+"/db-upgrade", nil, &info, 200)
	if info.Supported || !strings.Contains(info.Reason, "too old") {
		t.Fatalf("info: %+v", info)
	}
	if code := e.do("POST", "/api/v1/services/"+db.ID+"/db-upgrade", api.DBUpgradeRequest{Tag: "99"}, nil); code != 409 {
		t.Fatalf("start: %d", code)
	}
}

func TestMajorUpgradeRef(t *testing.T) {
	for _, c := range []struct{ image, tag, want string }{
		{"postgres:18-alpine", "19-alpine", "postgres:19-alpine"},
		{"postgres:16", "17", "postgres:17"},
		{"postgres:16.4", "17.0", "postgres:17.0"},
		{"postgres:latest", "17", ""},
		{"postgres:17", "16", ""},
		{"postgres:17", "17.2", ""},
	} {
		ref, err := majorUpgradeRef(c.image, c.tag)
		got := ""
		if err == nil {
			got = ref.WithTag(ref.Tag)
		}
		if !strings.HasSuffix(got, strings.TrimPrefix(c.want, "docker.io/")) || (c.want == "") != (got == "") {
			t.Errorf("%s → %s: got %q (%v), want %q", c.image, c.tag, got, err, c.want)
		}
	}
}
