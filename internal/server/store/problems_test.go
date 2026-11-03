package store

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestProblemSignals(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	now := time.Now()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.CreateNode(ctx, api.Server{ID: "srv_a", Name: "box-a", Role: "worker", Status: "online", CreatedAt: now}))
	must(s.CreateNode(ctx, api.Server{ID: "srv_b", Name: "box-b", Role: "worker", Status: "offline", CreatedAt: now}))
	must(s.CreateNode(ctx, api.Server{ID: "srv_c", Name: "control", Role: "control", Status: "offline", CreatedAt: now}))
	must(s.AddNodeMetrics(ctx, "srv_a", api.NodeMetrics{At: now, DiskUsedGB: 95, DiskTotalGB: 100, MemTotalMB: 1}))
	must(s.CreateProject(ctx, api.Project{ID: "prj_1", Name: "p1", ServerID: "srv_a", CreatedAt: now}, false))
	must(s.CreateProject(ctx, api.Project{ID: "prj_2", Name: "p2", ServerID: "srv_b", CreatedAt: now}, false))
	svc := func(id, pid, status string) {
		must(s.CreateService(ctx, api.Service{ID: id, ProjectID: pid, Name: id, Kind: "image", Image: "nginx", CreatedAt: now}))
		must(s.SetServiceStatus(ctx, id, status, "0/1"))
	}
	svc("svc_ok", "prj_1", "running")
	svc("svc_down", "prj_1", "failed")
	svc("svc_dep", "prj_1", "running")
	svc("svc_off", "prj_2", "failed") // on the offline server: covered by node_offline
	must(s.CreateDeployment(ctx, api.Deployment{ID: "dep_1", ServiceID: "svc_dep", ServerID: "srv_a", Status: "failed", Trigger: "manual",
		Message: "health check failed\nmore", StartedAt: now}, api.ResolvedSpec{}))
	must(s.ReplaceContainerStates(ctx, "srv_a", []api.ContainerState{{ServiceID: "svc_down", State: "restarting", RestartCount: 4}}))
	must(s.SaveBackupPolicy(ctx, api.BackupPolicy{ServiceID: "svc_ok", Enabled: true, Schedule: "0 3 * * *", Keep: api.BackupKeep{Daily: 1}}))
	must(s.SaveBackup(ctx, api.Backup{ID: "bak_1", ServiceID: "svc_ok", Trigger: "schedule", Status: "failed", Error: "repo locked", StartedAt: now}))
	must(s.CreateDomain(ctx, api.Domain{ID: "dom_1", ServiceID: "svc_ok", Hostname: "a.example.com", PortName: "http", Status: "error", CreatedAt: now}))
	must(s.SetDomainStatus(ctx, "dom_1", "error", "DNS record conflicts", ""))

	got, err := s.ProblemSignals(ctx, now)
	must(err)
	var keys []string
	msgs := map[string]string{}
	for _, p := range got {
		keys = append(keys, p.Kind+":"+p.SubjectID)
		msgs[p.Kind] = p.Message
	}
	sort.Strings(keys)
	want := "backup_failed:svc_ok deploy_failed:svc_dep disk_high:srv_a domain_error:dom_1 node_offline:srv_b service_down:svc_down"
	if strings.Join(keys, " ") != want {
		t.Fatalf("signals\n got %v\nwant %s", keys, want)
	}
	if !strings.Contains(msgs[api.ProblemServiceDown], "重启 4 次") || msgs[api.ProblemDeployFailed] != "部署失败：health check failed" ||
		!strings.Contains(msgs[api.ProblemDiskHigh], "95%") || !strings.Contains(msgs[api.ProblemBackupFailed], "repo locked") {
		t.Fatalf("messages %v", msgs)
	}

	// thresholds: deploy/backup fire at once, the rest wait
	ch, err := s.SyncProblems(ctx, now, got)
	must(err)
	fired := map[string]bool{}
	for _, c := range ch {
		fired[c.Problem.Kind] = true
	}
	if len(ch) != 2 || !fired[api.ProblemDeployFailed] || !fired[api.ProblemBackupFailed] {
		t.Fatalf("first sync fired %+v", ch)
	}
	ch, err = s.SyncProblems(ctx, now.Add(11*time.Minute), got)
	must(err)
	if len(ch) != 4 {
		t.Fatalf("second sync fired %d", len(ch))
	}
	todo, err := s.ProblemsToNotify(ctx)
	must(err)
	if len(todo) != 6 {
		t.Fatalf("to notify %d", len(todo))
	}
	for _, c := range todo {
		must(s.MarkProblemNotified(ctx, c.Problem.ID, false, now))
	}
	// everything clears: six recoveries to send, then nothing
	ch, err = s.SyncProblems(ctx, now.Add(12*time.Minute), nil)
	must(err)
	if len(ch) != 6 || !ch[0].Resolved {
		t.Fatalf("resolve %+v", ch)
	}
	if todo, _ = s.ProblemsToNotify(ctx); len(todo) != 6 || !todo[0].Resolved {
		t.Fatalf("recoveries %+v", todo)
	}
	for _, c := range todo {
		must(s.MarkProblemNotified(ctx, c.Problem.ID, true, now))
	}
	if todo, _ = s.ProblemsToNotify(ctx); len(todo) != 0 {
		t.Fatalf("left %+v", todo)
	}
	// the same condition again opens a new row
	ch, err = s.SyncProblems(ctx, now.Add(13*time.Minute), got[:1])
	must(err)
	open, _ := s.listProblems(ctx, `resolved_at IS NULL`, 0)
	if len(open) != 1 {
		t.Fatalf("reopen %+v %+v", ch, open)
	}
}
