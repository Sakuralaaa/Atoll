package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestPruneHistory(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	now := time.Now()
	old, recent := TS(now.Add(-200*24*time.Hour)), TS(now.Add(-time.Hour))
	q := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.DB.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(sql, err)
		}
	}
	count := func(sql string) int {
		t.Helper()
		var n int
		if err := s.DB.QueryRowContext(ctx, sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	q(`INSERT INTO audit_log(actor, action, target, created_at) VALUES('a','x','t',?),('a','x','t',?)`, old, recent)
	q(`INSERT INTO confirmations(id, action, target, summary, status, created_by, created_at) VALUES
		('c1','a','t','s','approved','u',?),('c2','a','t','s','pending','u',?),('c3','a','t','s','expired','u',?)`, old, old, recent)
	q(`INSERT INTO imports(id, plan, status, kind, created_at) VALUES
		('i1','{}','done','plan',?),('i2','{}','live','zeabur-local',?),('i3','{}','cleaned','zeabur-local',?),('i4','{}','failed','plan',?)`, old, old, old, recent)
	if err := s.CreateNode(ctx, api.Server{ID: "srv_a", Name: "box", Role: "worker", Status: "online", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProject(ctx, api.Project{ID: "prj_1", Name: "p", ServerID: "srv_a", CreatedAt: now}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateService(ctx, api.Service{ID: "svc_1", ProjectID: "prj_1", Name: "web", Kind: "image", Image: "nginx", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	// 6 deployments, oldest first: superseded x4, failed, running (newest).
	for i := range 6 {
		st := "superseded"
		switch i {
		case 4:
			st = "failed"
		case 5:
			st = "running"
		}
		q(`INSERT INTO deployments(id, service_id, server_id, spec, status, trigger, started_at) VALUES(?,?,?,?,?,?,?)`,
			fmt.Sprintf("dep_%d", i), "svc_1", "srv_a", "{}", st, "manual", TS(now.Add(time.Duration(i-10)*time.Minute)))
	}
	q(`INSERT INTO deployments(id, service_id, server_id, spec, status, trigger, started_at) VALUES('dep_old_run','svc_1','srv_a','{}','running','manual',?)`, old)

	r := DefaultRetention
	r.Deployments = 3
	n, err := s.PruneHistory(ctx, now, r)
	if err != nil {
		t.Fatal(err)
	}
	if n.Audit != 1 || n.Confirmations != 1 || n.Imports != 2 {
		t.Fatalf("pruned %+v", n)
	}
	if count(`SELECT COUNT(*) FROM confirmations WHERE id='c2'`) != 1 {
		t.Fatal("a pending confirmation was removed")
	}
	if count(`SELECT COUNT(*) FROM imports WHERE id='i2'`) != 1 {
		t.Fatal("a live zeabur takeover was removed")
	}
	// newest 3: dep_5 (running), dep_4 (failed), dep_3; dep_old_run is running, never removed.
	if got := count(`SELECT COUNT(*) FROM deployments`); got != 4 || n.Deployments != 3 {
		t.Fatalf("deployments left %d, pruned %d", got, n.Deployments)
	}
	if count(`SELECT COUNT(*) FROM deployments WHERE id IN ('dep_5','dep_4','dep_3','dep_old_run')`) != 4 {
		t.Fatal("wrong deployments removed")
	}
	if n, _ := s.PruneHistory(ctx, now, r); n != (PruneResult{}) {
		t.Fatalf("second prune removed %+v", n)
	}
}
