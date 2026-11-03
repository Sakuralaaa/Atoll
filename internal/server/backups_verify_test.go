package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/cronexpr"
)

func TestAutoSlotKeepsClearOfBackups(t *testing.T) {
	ref := time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC)
	if got := autoSlot(autoCheckFormat, nil, time.UTC, ref, 5); got != "30 4 * * 0" {
		t.Fatalf("no backups: %s", got)
	}
	busy := []busyCron{{cronexpr.MustParse("0 4 * * *"), time.UTC}, {cronexpr.MustParse("0 5 * * *"), time.UTC}}
	// 04:30, 05:30, 03:30 are 30 min away, 06:30 and 02:30 1h30, 01:30 2h30, 00:30 3h30
	if got := autoSlot(autoCheckFormat, busy, time.UTC, ref, 5); got != "30 0 * * 0" {
		t.Fatalf("busy nights: %s", got)
	}
	// a backup in another zone counts at its own wall clock: 03:00 Shanghai = 19:00 UTC
	sh, _ := cronexpr.LoadLocation("Asia/Shanghai")
	if got := autoSlot(autoCheckFormat, []busyCron{{cronexpr.MustParse("0 3 * * *"), sh}}, time.UTC, ref, 5); got != "30 4 * * 0" {
		t.Fatalf("other zone: %s", got)
	}
	// hourly backups: nothing is 3h away, the first preference wins
	if got := autoSlot(autoDrillFormat, []busyCron{{cronexpr.MustParse("0 * * * *"), time.UTC}}, time.UTC, ref, 3); got != "30 4 1 * *" {
		t.Fatalf("hourly: %s", got)
	}
}

func TestBackupVerifyCheckDrillProblemsAndSettings(t *testing.T) {
	e := newTestEnvWith(t, func(c *Config) { c.ProblemsTick = time.Hour })
	e.setup()
	sink := &hookSink{}
	hook := httptest.NewServer(sink)
	t.Cleanup(hook.Close)
	e.mustDo("PUT", "/api/v1/settings/notifications", api.NotificationSettings{NotifyResolved: true,
		Channels: []api.NotificationChannel{{Type: "webhook", Name: "ops", Enabled: true, URL: hook.URL}}}, nil, 200)
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	_, db, _ := backupProject(t, e, nodeID)
	ctx := context.Background()

	if code := e.do("POST", "/api/v1/backup-verify/check", nil, nil); code != 409 {
		t.Fatalf("check without target: %d", code)
	}
	e.mustDo("PUT", "/api/v1/settings", backupSettings(), nil, 200)
	e.mustDo("PUT", "/api/v1/services/"+db.ID+"/backup-policy", map[string]any{"enabled": true, "schedule": "0 4 * * *", "timezone": "UTC"}, nil, 200)

	// the node got verification schedules that keep clear of its 04:00 backup
	waitUntil(t, "verify config", func() bool {
		cfg, _ := lastPayload[api.BackupConfigPayload](t, agent, api.MsgBackupConfig)
		return cfg.Verify != nil && len(cfg.Targets) == 1 && cfg.Targets[0].Enabled
	})
	cfg, _ := lastPayload[api.BackupConfigPayload](t, agent, api.MsgBackupConfig)
	v := cfg.Verify
	if !v.RunCheck || v.CheckSubset != "5%" || v.CheckSchedule == "" || v.DrillSchedule == "" || v.Timezone == "" {
		t.Fatalf("verify config %+v", v)
	}
	for _, c := range []string{v.CheckSchedule, v.DrillSchedule} {
		if strings.HasPrefix(c, "30 4 ") || strings.HasPrefix(c, "30 3 ") {
			t.Fatalf("slot %q next to the 04:00 backup", c)
		}
	}
	var st api.BackupVerifyStatus
	e.mustDo("GET", "/api/v1/services/"+db.ID+"/backup-verify", nil, &st, 200)
	if !st.RepoConfigured || st.CheckServerID != nodeID || st.NextCheckAt == nil || st.NextDrillAt == nil || st.Check != nil || st.Drill != nil {
		t.Fatalf("status %+v", st)
	}

	// check now
	var chk api.BackupVerification
	e.mustDo("POST", "/api/v1/backup-verify/check", nil, &chk, 202)
	if chk.Kind != "check" || chk.Status != "running" || chk.ServerID != nodeID || chk.Subset != "5%" {
		t.Fatalf("check %+v", chk)
	}
	p, n := lastPayload[api.BackupVerifyPayload](t, agent, api.MsgBackupVerify)
	if n != 1 || p.ID != chk.ID || p.Kind != "check" || p.Repo.SecretKey != testS3Secret || p.Target != nil {
		t.Fatalf("payload %+v", p)
	}
	if code := e.do("POST", "/api/v1/backup-verify/check", nil, nil); code != 409 {
		t.Fatalf("second check while running: %d", code)
	}
	fin := time.Now().UTC()
	agent.send(ctx, api.Envelope{Type: api.MsgBackupVerifyEvent, Payload: api.BackupVerifyEvent{ID: chk.ID, Kind: "check", Trigger: "manual", Status: "failed",
		Subset: "5%", Error: "restic check (5% of the data): exit status 1: Fatal: repository contains errors", StartedAt: chk.StartedAt, FinishedAt: &fin,
		Steps: []api.BackupVerifyStep{{Part: "check", Status: "failed", Error: "pack 1 damaged"}}}})
	waitUntil(t, "check failed", func() bool {
		e.mustDo("GET", "/api/v1/backup-verify", nil, &st, 200)
		return st.Check != nil && st.Check.Status == "failed"
	})
	// a late "running" frame does not reopen it
	agent.send(ctx, api.Envelope{Type: api.MsgBackupVerifyEvent, Payload: api.BackupVerifyEvent{ID: chk.ID, Kind: "check", Status: "running", StartedAt: chk.StartedAt}})

	// drill now
	var drill api.BackupVerification
	e.mustDo("POST", "/api/v1/services/"+db.ID+"/backup-drill", nil, &drill, 202)
	dp, _ := lastPayload[api.BackupVerifyPayload](t, agent, api.MsgBackupVerify)
	if drill.Kind != "drill" || drill.ServiceID != db.ID || dp.Target == nil || dp.Target.ServiceID != db.ID || dp.Target.Kind != "postgres" {
		t.Fatalf("drill %+v payload %+v", drill, dp)
	}
	agent.send(ctx, api.Envelope{Type: api.MsgBackupVerifyEvent, Payload: api.BackupVerifyEvent{ID: drill.ID, Kind: "drill", ServiceID: db.ID, Status: "failed",
		Error: "dump: query exited 1: relation does not exist", StartedAt: drill.StartedAt, FinishedAt: &fin}})
	// an agent-scheduled drill of a deleted service is ignored
	agent.send(ctx, api.Envelope{Type: api.MsgBackupVerifyEvent, Payload: api.BackupVerifyEvent{ID: "bvf_ghost", Kind: "drill", ServiceID: "svc_gone", Status: "done", StartedAt: fin}})
	waitUntil(t, "drill failed", func() bool {
		e.mustDo("GET", "/api/v1/services/"+db.ID+"/backup-verify", nil, &st, 200)
		return st.Drill != nil && st.Drill.Status == "failed"
	})
	var list []api.BackupVerification
	e.mustDo("GET", "/api/v1/backup-verifications", nil, &list, 200)
	if len(list) != 2 || list[0].ID != drill.ID || list[1].ID != chk.ID || list[1].Status != "failed" || list[1].Trigger != "manual" {
		t.Fatalf("list %+v", list)
	}
	e.mustDo("GET", "/api/v1/backup-verifications?kind=check", nil, &list, 200)
	if len(list) != 1 || list[0].Steps[0].Error != "pack 1 damaged" {
		t.Fatalf("checks %+v", list)
	}

	// problems: one for the repository, one for the service; notified once with the Chinese label
	now := time.Now()
	e.srv.evaluateProblems(ctx, now)
	var probs []api.Problem
	e.mustDo("GET", "/api/v1/problems", nil, &probs, 200)
	subjects := map[string]api.Problem{}
	for _, p := range probs {
		if p.Kind == api.ProblemBackupVerifyFailed {
			subjects[p.SubjectID] = p
		}
	}
	if len(subjects) != 2 || subjects[api.BackupRepoSubjectID].SubjectName != "备份仓库" || !strings.Contains(subjects[db.ID].Message, "恢复演练失败") ||
		!strings.Contains(subjects[api.BackupRepoSubjectID].Message, "仓库校验失败") {
		t.Fatalf("problems %+v", probs)
	}
	if problemKindText(api.ProblemBackupVerifyFailed) != "备份校验失败" {
		t.Fatal("kind text")
	}
	e.srv.evaluateProblems(ctx, now.Add(time.Minute))
	fired := 0
	for _, ev := range sink.events() {
		if ev == "firing:backup_verify_failed" {
			fired++
		}
	}
	if fired != 2 {
		t.Fatalf("notifications %v", sink.events())
	}

	// a passing scheduled check (reported by the agent on its own) resolves the repository problem
	later := fin.Add(time.Minute)
	agent.send(ctx, api.Envelope{Type: api.MsgBackupVerifyEvent, Payload: api.BackupVerifyEvent{ID: "bvf_sched0001", Kind: "check", Trigger: "schedule", Status: "done",
		Subset: "5%", StartedAt: later, FinishedAt: &later}})
	waitUntil(t, "scheduled check", func() bool {
		e.mustDo("GET", "/api/v1/backup-verify", nil, &st, 200)
		return st.Check != nil && st.Check.ID == "bvf_sched0001"
	})
	if st.Check.Trigger != "schedule" {
		t.Fatalf("trigger %+v", st.Check)
	}
	e.srv.evaluateProblems(ctx, now.Add(2*time.Minute))
	resolved := false
	for _, ev := range sink.events() {
		resolved = resolved || ev == "resolved:backup_verify_failed"
	}
	if !resolved {
		t.Fatalf("no recovery notification %v", sink.events())
	}
	e.mustDo("GET", "/api/v1/problems", nil, &probs, 200)
	for _, p := range probs {
		if p.SubjectID == api.BackupRepoSubjectID {
			t.Fatalf("repository problem still open %+v", p)
		}
	}

	// the agent refuses a duplicate: 409 and no record left behind
	agent.mu.Lock()
	agent.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type == api.MsgBackupVerify {
			return api.ResultPayload{OK: true, Output: "busy"}, true
		}
		return api.ResultPayload{}, false
	}
	agent.mu.Unlock()
	if code := e.do("POST", "/api/v1/backup-verify/check", nil, nil); code != 409 {
		t.Fatalf("busy agent: %d", code)
	}
	e.mustDo("GET", "/api/v1/backup-verifications?kind=check", nil, &list, 200)
	if len(list) != 2 {
		t.Fatalf("busy run recorded: %+v", list)
	}

	// settings: validation, off, explicit, auto
	for _, bad := range []api.Settings{{BackupCheckSchedule: "61 * * * *"}, {BackupDrillSchedule: "0 0 30 2 *"}, {BackupCheckSubset: "150%"}} {
		if code := e.do("PUT", "/api/v1/settings", bad, nil); code != 400 {
			t.Fatalf("bad settings %+v: %d", bad, code)
		}
	}
	var sres api.Settings
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BackupCheckSchedule: "off", BackupDrillSchedule: "15 2 2 * *", BackupCheckSubset: "1/10"}, &sres, 200)
	if sres.BackupCheckSchedule != "off" || sres.BackupDrillSchedule != "15 2 2 * *" || sres.BackupCheckSubset != "1/10" {
		t.Fatalf("settings %+v", sres)
	}
	waitUntil(t, "pushed", func() bool {
		cfg, _ := lastPayload[api.BackupConfigPayload](t, agent, api.MsgBackupConfig)
		return cfg.Verify != nil && cfg.Verify.CheckSchedule == "" && cfg.Verify.DrillSchedule == "15 2 2 * *" && cfg.Verify.CheckSubset == "1/10"
	})
	st = api.BackupVerifyStatus{} // omitempty fields would otherwise keep the old values
	e.mustDo("GET", "/api/v1/backup-verify", nil, &st, 200)
	if st.CheckSchedule != "" || st.NextCheckAt != nil || st.CheckSubset != "1/10" {
		t.Fatalf("status after off %+v", st)
	}
	sres = api.Settings{}
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BackupCheckSchedule: "auto", BackupCheckSubset: "auto"}, &sres, 200)
	if sres.BackupCheckSchedule != "" || sres.BackupCheckSubset != "" {
		t.Fatalf("auto %+v", sres)
	}
	e.mustDo("GET", "/api/v1/backup-verify", nil, &st, 200)
	if st.CheckSchedule == "" || st.CheckSubset != "5%" {
		t.Fatalf("status after auto %+v", st)
	}

	// stale running records fail
	old := api.BackupVerification{ID: "bvf_stale0001", Kind: "check", Trigger: "schedule", Status: "running", StartedAt: time.Now().Add(-48 * time.Hour)}
	e.srv.st.SaveVerification(ctx, old)
	e.srv.verifyHousekeeping(ctx, true)
	got, err := e.srv.st.GetVerification(ctx, old.ID)
	if err != nil || got.Status != "failed" {
		t.Fatalf("stale %+v %v", got, err)
	}
}
