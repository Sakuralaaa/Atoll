package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/coder/websocket"
)

const (
	testS3Secret = "s3-secret-NEVER-LOG"
	testS3Access = "AKIDBACKUPTEST"
)

func backupSettings() api.Settings {
	return api.Settings{BackupEndpoint: "https://acct.r2.cloudflarestorage.com", BackupBucket: "atoll-backups", BackupRegion: "auto",
		BackupAccessKey: testS3Access, BackupSecretKey: testS3Secret}
}

func waitUntil(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// backupProject creates a project with a postgres template service and an app with a volume.
func backupProject(t *testing.T, e *testEnv, nodeID string) (api.Project, api.Service, api.Service) {
	t.Helper()
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "acme-api", "server_id": nodeID}, &p, 201)
	var db, app api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"kind": "postgres", "name": "postgres"}, &db, 201)
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "app", "kind": "image", "image": "ghcr.io/acme/app:1",
		"volumes": []api.Volume{{Name: "uploads", MountPath: "/app/uploads"}},
	}, &app, 201)
	return p, db, app
}

func lastPayload[T any](t *testing.T, a *fakeAgent, typ string) (T, int) {
	t.Helper()
	var v T
	msgs := a.received(typ)
	if len(msgs) == 0 {
		return v, 0
	}
	if err := json.Unmarshal(msgs[len(msgs)-1], &v); err != nil {
		t.Fatal(err)
	}
	return v, len(msgs)
}

func TestBackupSettingsSecrets(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	var st api.Settings
	e.mustDo("PUT", "/api/v1/settings", backupSettings(), &st, 200)
	if len(st.BackupRepoPassword) != 40 || !st.BackupConfigured || !st.HasBackupCredentials || st.BackupSecretKey != "" || st.BackupAccessKey != "" {
		t.Fatalf("first save: %+v", st)
	}
	pw := st.BackupRepoPassword
	if st.BackupPrefix != "atoll" {
		t.Fatalf("default prefix %q", st.BackupPrefix)
	}
	// GET never returns secrets
	var raw json.RawMessage
	e.mustDo("GET", "/api/v1/settings", nil, &raw, 200)
	for _, s := range []string{testS3Secret, testS3Access, pw} {
		if strings.Contains(string(raw), s) {
			t.Fatalf("GET /settings leaks %q: %s", s, raw)
		}
	}
	// second save keeps the password and does not show it again
	st = api.Settings{} // decode into a fresh value: omitempty fields would otherwise keep the old ones
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BackupBucket: "atoll-backups"}, &st, 200)
	if st.BackupRepoPassword != "" || !st.BackupConfigured {
		t.Fatalf("second save: %+v", st)
	}
	// reveal (session only)
	var rev map[string]string
	e.mustDo("GET", "/api/v1/settings/backup/password", nil, &rev, 200)
	if rev["password"] != pw || rev["repository"] != "s3:https://acct.r2.cloudflarestorage.com/atoll-backups/atoll" {
		t.Fatalf("reveal %v", rev)
	}
	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]string{"name": "ci", "scope": "admin"}, &tok, 201)
	if code := e.doWith(http.DefaultClient, tok.Token, "GET", "/api/v1/settings/backup/password", nil, nil); code != 403 {
		t.Fatalf("PAT reveal: %d", code)
	}
	if code := e.do("PUT", "/api/v1/settings", api.Settings{BackupBucket: "Bad_Bucket!"}, nil); code != 400 {
		t.Fatalf("bad bucket: %d", code)
	}
	// stored encrypted, never in the audit log
	var enc string
	e.srv.st.DB.QueryRow(`SELECT value FROM settings WHERE key='backup_secret_key'`).Scan(&enc)
	if enc == "" || strings.Contains(enc, testS3Secret) {
		t.Fatalf("secret stored in clear: %q", enc)
	}
	rows, _ := e.srv.st.DB.Query(`SELECT detail FROM audit_log`)
	defer rows.Close()
	for rows.Next() {
		var d string
		rows.Scan(&d)
		for _, s := range []string{testS3Secret, testS3Access, pw} {
			if strings.Contains(d, s) {
				t.Fatalf("audit log leaks a secret: %s", d)
			}
		}
	}
}

func TestBackupPolicyRunAndEvents(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	_, db, app := backupProject(t, e, nodeID)

	var pol api.BackupPolicy
	e.mustDo("GET", "/api/v1/services/"+db.ID+"/backup-policy", nil, &pol, 200)
	if pol.Enabled || pol.Schedule != "0 3 * * *" || pol.Keep != (api.BackupKeep{Daily: 7, Weekly: 4, Monthly: 6}) || pol.RepoConfigured {
		t.Fatalf("default policy %+v", pol)
	}
	if strings.Join(pol.Parts, ",") != "dump,volumes" {
		t.Fatalf("parts %v", pol.Parts)
	}
	// run now without a target configured
	if code := e.do("POST", "/api/v1/services/"+db.ID+"/backups", nil, nil); code != 409 {
		t.Fatalf("run without target: %d", code)
	}
	e.mustDo("PUT", "/api/v1/settings", backupSettings(), nil, 200)

	if code := e.do("PUT", "/api/v1/services/"+db.ID+"/backup-policy", map[string]any{"enabled": true, "schedule": "61 * * * *"}, nil); code != 400 {
		t.Fatalf("bad cron: %d", code)
	}
	if code := e.do("PUT", "/api/v1/services/"+db.ID+"/backup-policy", map[string]any{"enabled": true, "keep": api.BackupKeep{}}, nil); code != 400 {
		t.Fatalf("empty retention: %d", code)
	}
	e.mustDo("PUT", "/api/v1/services/"+db.ID+"/backup-policy", map[string]any{"enabled": true, "schedule": "30 2 * * *", "timezone": "Asia/Shanghai"}, &pol, 200)
	if !pol.Enabled || pol.NextRunAt == nil || pol.Keep.Daily != 7 || pol.Timezone != "Asia/Shanghai" {
		t.Fatalf("saved policy %+v", pol)
	}
	if loc, _ := time.LoadLocation("Asia/Shanghai"); pol.NextRunAt.In(loc).Hour() != 2 || pol.NextRunAt.In(loc).Minute() != 30 {
		t.Fatalf("next run %s", pol.NextRunAt)
	}
	// the agent got its policies (it schedules locally)
	waitUntil(t, "backup.config", func() bool {
		cfg, _ := lastPayload[api.BackupConfigPayload](t, agent, api.MsgBackupConfig)
		return cfg.Repo != nil && len(cfg.Targets) == 1 && cfg.Targets[0].Enabled
	})
	cfg, _ := lastPayload[api.BackupConfigPayload](t, agent, api.MsgBackupConfig)
	if tg := cfg.Targets[0]; tg.ServiceID != db.ID || tg.Kind != "postgres" || tg.Schedule != "30 2 * * *" || cfg.Repo.SecretKey != testS3Secret || cfg.Repo.Password == "" {
		t.Fatalf("config %+v", tg)
	}

	// run now
	var b api.Backup
	e.mustDo("POST", "/api/v1/services/"+db.ID+"/backups", nil, &b, 202)
	if b.Status != "running" || b.Trigger != "manual" {
		t.Fatalf("run: %+v", b)
	}
	run, n := lastPayload[api.BackupRunPayload](t, agent, api.MsgBackupRun)
	if n != 1 || run.BackupID != b.ID || run.Target.ServiceID != db.ID || run.Repo.Bucket != "atoll-backups" {
		t.Fatalf("backup.run %+v", run)
	}
	// agent reports
	fin := time.Now().UTC()
	ctx := context.Background()
	agent.send(ctx, api.Envelope{Type: api.MsgBackupEvent, Payload: api.BackupEvent{BackupID: b.ID, ServiceID: db.ID, Trigger: "manual", Status: "done",
		Snapshots: []api.BackupSnapshot{{ID: "aaaa1111bbbb2222", Part: "dump", Bytes: 2048, Added: 512}, {ID: "cccc3333dddd4444", Part: "volumes", Bytes: 8192, Added: 1024}},
		Bytes:     10240, Added: 1536, DurationMs: 4200, StartedAt: b.StartedAt, FinishedAt: &fin}})
	var list []api.Backup
	waitUntil(t, "backup done", func() bool {
		e.mustDo("GET", "/api/v1/services/"+db.ID+"/backups", nil, &list, 200)
		return len(list) == 1 && list[0].Status == "done"
	})
	if list[0].Bytes != 10240 || list[0].Trigger != "manual" || len(list[0].Snapshots) != 2 {
		t.Fatalf("recorded %+v", list[0])
	}
	// a late "running" frame must not reopen it
	agent.send(ctx, api.Envelope{Type: api.MsgBackupEvent, Payload: api.BackupEvent{BackupID: b.ID, ServiceID: db.ID, Status: "running", StartedAt: b.StartedAt}})
	// lookup by snapshot id
	var got api.Backup
	e.mustDo("GET", "/api/v1/backups/aaaa1111", nil, &got, 200)
	if got.ID != b.ID || got.Status != "done" {
		t.Fatalf("by snapshot: %+v", got)
	}
	// a scheduled run reported by the agent on its own, which prunes the first backup
	agent.send(ctx, api.Envelope{Type: api.MsgBackupEvent, Payload: api.BackupEvent{BackupID: "bak_agentmade01", ServiceID: db.ID, Trigger: "schedule", Status: "done",
		Snapshots: []api.BackupSnapshot{{ID: "eeee5555", Part: "dump"}}, StartedAt: fin, FinishedAt: &fin, Pruned: []string{"aaaa1111bbbb2222", "cccc3333dddd4444"}}})
	waitUntil(t, "pruned", func() bool {
		e.mustDo("GET", "/api/v1/services/"+db.ID+"/backups", nil, &list, 200)
		return len(list) == 2 && list[1].Status == "pruned"
	})
	if list[0].ID != "bak_agentmade01" || list[0].Trigger != "schedule" {
		t.Fatalf("agent-made backup %+v", list[0])
	}
	if code := e.do("POST", "/api/v1/backups/"+b.ID+"/restore", nil, nil); code != 409 {
		t.Fatalf("restore of pruned backup: %d", code)
	}

	// an app without volumes cannot be enabled
	var bare api.Service
	e.mustDo("POST", "/api/v1/projects/"+app.ProjectID+"/services", map[string]any{"name": "worker", "kind": "image", "image": "busybox:1"}, &bare, 201)
	if code := e.do("PUT", "/api/v1/services/"+bare.ID+"/backup-policy", map[string]any{"enabled": true}, nil); code != 400 {
		t.Fatalf("enable without volumes: %d", code)
	}
}

func TestBackupCatchupWatchdog(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	_, db, _ := backupProject(t, e, nodeID)
	e.mustDo("PUT", "/api/v1/settings", backupSettings(), nil, 200)
	e.mustDo("PUT", "/api/v1/services/"+db.ID+"/backup-policy", map[string]any{"enabled": true, "schedule": "0 3 * * *", "timezone": "UTC"}, nil, 200)
	// policy saved two days ago
	e.srv.st.DB.Exec(`UPDATE backup_policies SET updated_at=? WHERE service_id=?`, time.Now().Add(-48*time.Hour).UTC().Format(time.RFC3339Nano), db.ID)
	ctx := context.Background()

	now := time.Date(2030, 1, 2, 3, 10, 0, 0, time.UTC) // 10 min after the slot: within grace
	if sent := e.srv.backupCatchup(ctx, now); len(sent) != 0 {
		t.Fatalf("catch-up inside grace: %v", sent)
	}
	now = now.Add(time.Hour)
	if sent := e.srv.backupCatchup(ctx, now); len(sent) != 1 || sent[0] != db.ID {
		t.Fatalf("expected catch-up, got %v", sent)
	}
	waitUntil(t, "catch-up run", func() bool { _, n := lastPayload[api.BackupRunPayload](t, agent, api.MsgBackupRun); return n == 1 })
	run, _ := lastPayload[api.BackupRunPayload](t, agent, api.MsgBackupRun)
	if run.Trigger != "catchup" || run.Since == nil || !run.Since.Equal(time.Date(2030, 1, 2, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("catch-up payload %+v", run)
	}
	if sent := e.srv.backupCatchup(ctx, now.Add(time.Minute)); len(sent) != 0 {
		t.Fatalf("catch-up must be requested once per slot: %v", sent)
	}
	// next day the agent's own run was recorded: nothing to catch up
	day3 := time.Date(2030, 1, 3, 3, 0, 30, 0, time.UTC)
	e.srv.st.SaveBackup(ctx, api.Backup{ID: "bak_agentrun0001", ServiceID: db.ID, ServerID: nodeID, Trigger: "schedule", Status: "done", StartedAt: day3})
	if sent := e.srv.backupCatchup(ctx, day3.Add(2*time.Hour)); len(sent) != 0 {
		t.Fatalf("already backed up: %v", sent)
	}
	// offline nodes are skipped
	agent.ws.Close(websocket.StatusNormalClosure, "bye")
	waitUntil(t, "offline", func() bool { return !e.srv.hub.Online(nodeID) })
	if sent := e.srv.backupCatchup(ctx, time.Date(2030, 1, 5, 9, 0, 0, 0, time.UTC)); len(sent) != 0 {
		t.Fatalf("offline: %v", sent)
	}
}

// doneBackup records a finished backup with a dump + volume snapshot.
func doneBackup(t *testing.T, e *testEnv, svcID, nodeID, id string) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Hour)
	if err := e.srv.st.SaveBackup(context.Background(), api.Backup{ID: id, ServiceID: svcID, ServerID: nodeID, Trigger: "schedule", Status: "done",
		Snapshots: []api.BackupSnapshot{{ID: id + "-dump", Part: "dump"}, {ID: id + "-vol", Part: "volumes"}}, StartedAt: at, FinishedAt: &at}); err != nil {
		t.Fatal(err)
	}
}

func waitRestore(t *testing.T, e *testEnv, id string) api.RestoreJob {
	t.Helper()
	var j api.RestoreJob
	waitUntil(t, "restore "+id, func() bool {
		e.mustDo("GET", "/api/v1/restores/"+id, nil, &j, 200)
		return j.Status != "running"
	})
	return j
}

func TestRestoreInPlaceNeedsConfirmationWithToken(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	_, db, _ := backupProject(t, e, nodeID)
	e.mustDo("PUT", "/api/v1/settings", backupSettings(), nil, 200)
	doneBackup(t, e, db.ID, nodeID, "bak_restoreme01")
	agent.mu.Lock()
	agent.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type == api.MsgBackupRestore {
			return api.ResultPayload{OK: true, Output: `{"restored":["dump"],"bytes":4096,"moved_aside":"/var/lib/atoll/backup/pre-restore/x"}`}, true
		}
		return api.ResultPayload{}, false
	}
	agent.mu.Unlock()

	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "mcp", "permissions": []string{"deploy", "restore"}, "require_approval": true}, &tok, 201)
	var c api.Confirmation
	if code := e.doWith(http.DefaultClient, tok.Token, "POST", "/api/v1/backups/bak_restoreme01/restore", map[string]string{}, &c); code != 202 || c.Action != "backup.restore" {
		t.Fatalf("restore with PAT: %d %+v", code, c)
	}
	if n := len(agent.received(api.MsgBackupRestore)); n != 0 {
		t.Fatal("restore ran before approval")
	}
	var job api.RestoreJob
	e.mustDo("POST", "/api/v1/confirmations/"+c.ID+"/approve", nil, nil, 200)
	var jobs []api.RestoreJob
	waitUntil(t, "restore job", func() bool {
		e.mustDo("GET", "/api/v1/services/"+db.ID+"/restores", nil, &jobs, 200)
		return len(jobs) == 1
	})
	job = waitRestore(t, e, jobs[0].ID)
	if job.Status != "done" || len(job.Steps) != 1 || !strings.Contains(job.Steps[0].Detail, "restored dump") {
		t.Fatalf("job %+v", job)
	}
	p, _ := lastPayload[api.BackupRestorePayload](t, agent, api.MsgBackupRestore)
	if p.Target.ServiceID != db.ID || p.SourceServiceID != db.ID || len(p.Snapshots) != 2 || p.Repo.Password == "" {
		t.Fatalf("restore payload %+v", p)
	}
	// panel sessions execute directly
	e.mustDo("POST", "/api/v1/backups/bak_restoreme01/restore", map[string]string{}, &job, 200)
	if job.ID == "" || job.ToServer != nodeID {
		t.Fatalf("session restore %+v", job)
	}
	waitRestore(t, e, job.ID)
}

func TestRestoreToAnotherServerAfterLoss(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agentA, nodeA := startFakeAgent(t, e, api.ZeaburInventory{})
	agentB, nodeB := startFakeAgent(t, e, api.ZeaburInventory{})
	p, db, app := backupProject(t, e, nodeA)
	e.mustDo("PUT", "/api/v1/settings", backupSettings(), nil, 200)
	e.mustDo("PUT", "/api/v1/services/"+db.ID+"/backup-policy", map[string]any{"enabled": true}, nil, 200)
	doneBackup(t, e, db.ID, nodeA, "bak_drtest00001")

	req := api.BackupRestoreRequest{TargetServerID: nodeB}
	if code := e.do("POST", "/api/v1/backups/bak_drtest00001/restore", req, nil); code != 409 {
		t.Fatalf("cross-server restore while source online: %d", code)
	}
	// the VPS is gone; wait for both WebSocket readers and hub cleanup before restoring.
	agentA.closeAndWait(e, nodeA)

	var job api.RestoreJob
	e.mustDo("POST", "/api/v1/backups/bak_drtest00001/restore", req, &job, 200)
	job = waitRestore(t, e, job.ID)
	if job.Status != "done" {
		t.Fatalf("DR restore: %s %+v", job.Error, job.Steps)
	}
	names := []string{}
	for _, s := range job.Steps {
		names = append(names, s.Name)
	}
	if strings.Join(names, "|") != "Move project to target server|Restore data|Switch DNS|Update ingress" {
		t.Fatalf("steps %v", names)
	}
	var got api.Project
	e.mustDo("GET", "/api/v1/projects/"+p.ID, nil, &got, 200)
	if got.ServerID != nodeB {
		t.Fatalf("project still on %s", got.ServerID)
	}
	applied := map[string]bool{}
	for _, raw := range agentB.received(api.MsgApply) {
		var ap api.ApplyPayload
		json.Unmarshal(raw, &ap)
		applied[ap.Spec.ServiceID] = true
	}
	if !applied[db.ID] || !applied[app.ID] {
		t.Fatalf("services not created on target: %v", applied)
	}
	rp, n := lastPayload[api.BackupRestorePayload](t, agentB, api.MsgBackupRestore)
	if n != 1 || rp.Target.ServiceID != db.ID {
		t.Fatalf("restore on B: %+v", rp)
	}
	waitUntil(t, "policy pushed to B", func() bool {
		cfg, _ := lastPayload[api.BackupConfigPayload](t, agentB, api.MsgBackupConfig)
		return len(cfg.Targets) == 1 && cfg.Targets[0].ServiceID == db.ID
	})
}

func TestBackupTestEndpoint(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	if code := e.do("POST", "/api/v1/settings/backup/test", nil, nil); code != 400 {
		t.Fatalf("test without target: %d", code)
	}
	e.mustDo("PUT", "/api/v1/settings", backupSettings(), nil, 200)
	agent.mu.Lock()
	agent.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type == api.MsgBackupTest {
			var r api.BackupRepo
			json.Unmarshal(env.Payload, &r)
			if r.SecretKey != "override-secret" {
				return api.ResultPayload{OK: false, Error: "wrong repository password"}, true
			}
			return api.ResultPayload{OK: true, Output: "repository created at " + r.URL()}, true
		}
		return api.ResultPayload{}, false
	}
	agent.mu.Unlock()
	var res api.BackupTestResult
	e.mustDo("POST", "/api/v1/settings/backup/test", map[string]any{"backup_secret_key": "override-secret"}, &res, 200)
	if !res.OK || res.ServerID != nodeID || !strings.Contains(res.Message, "created") {
		t.Fatalf("test %+v", res)
	}
	e.mustDo("POST", "/api/v1/settings/backup/test", nil, &res, 200)
	if res.OK || !strings.Contains(res.Message, "wrong") {
		t.Fatalf("failing test %+v", res)
	}
}

var _ = io.Discard

func TestRestoreToAnotherServerReportsIngressFailure(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agentA, nodeA := startFakeAgent(t, e, api.ZeaburInventory{})
	agentB, nodeB := startFakeAgent(t, e, api.ZeaburInventory{})
	_, db, _ := backupProject(t, e, nodeA)
	e.mustDo("PUT", "/api/v1/settings", backupSettings(), nil, 200)
	e.mustDo("PUT", "/api/v1/services/"+db.ID+"/backup-policy", map[string]any{"enabled": true}, nil, 200)
	doneBackup(t, e, db.ID, nodeA, "bak_ingressfail0001")
	agentA.closeAndWait(e, nodeA)

	// Fail only after the target has received the restore request; unrelated setup ingress
	// messages still succeed.
	failIngress := atomic.Bool{}
	agentB.mu.Lock()
	agentB.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type == api.MsgBackupRestore {
			failIngress.Store(true)
			return api.ResultPayload{}, false
		}
		if env.Type == api.MsgIngress && failIngress.Load() {
			return api.ResultPayload{OK: false, Error: "route push rejected"}, true
		}
		return api.ResultPayload{}, false
	}
	agentB.mu.Unlock()

	var job api.RestoreJob
	e.mustDo("POST", "/api/v1/backups/bak_ingressfail0001/restore", api.BackupRestoreRequest{TargetServerID: nodeB}, &job, 200)
	job = waitRestore(t, e, job.ID)
	if job.Status != "failed" || !strings.Contains(job.Error, "route push rejected") {
		t.Fatalf("restore should report failed ingress push: %+v", job)
	}
	var ingress *api.ImportStep
	for i := range job.Steps {
		if job.Steps[i].Name == "Update ingress" {
			ingress = &job.Steps[i]
			break
		}
	}
	if ingress == nil || ingress.Status != "failed" || !strings.Contains(ingress.Detail, "route push rejected") {
		t.Fatalf("ingress failure step not recorded: %+v", job.Steps)
	}
}
