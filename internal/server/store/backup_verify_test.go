package store

import (
	"context"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestVerificationsSaveLatestAndPrune(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.CreateNode(ctx, api.Server{ID: "srv_a", Name: "a", Role: "worker", Status: "online", CreatedAt: now}))
	must(s.CreateProject(ctx, api.Project{ID: "prj_1", Name: "p1", ServerID: "srv_a", CreatedAt: now}, false))
	must(s.CreateService(ctx, api.Service{ID: "svc_db", ProjectID: "prj_1", Name: "db", Kind: "postgres", CreatedAt: now}))
	at := func(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }
	fin := now
	must(s.SaveVerification(ctx, api.BackupVerification{ID: "bvf_c1", Kind: "check", Trigger: "schedule", Status: "done", StartedAt: at(400), FinishedAt: &fin}))
	must(s.SaveVerification(ctx, api.BackupVerification{ID: "bvf_c2", Kind: "check", Trigger: "schedule", Status: "failed", Error: "boom", StartedAt: at(390), FinishedAt: &fin}))
	must(s.SaveVerification(ctx, api.BackupVerification{ID: "bvf_d1", Kind: "drill", ServiceID: "svc_db", Trigger: "manual", Status: "done", StartedAt: at(380), FinishedAt: &fin,
		Steps: []api.BackupVerifyStep{{Part: "dump", Status: "done", Detail: "databases=1 tables=3"}}}))
	must(s.SaveVerification(ctx, api.BackupVerification{ID: "bvf_d2", Kind: "drill", ServiceID: "svc_gone", Trigger: "schedule", Status: "done", StartedAt: at(1)}))
	// a late running frame never reopens a finished record
	must(s.SaveVerification(ctx, api.BackupVerification{ID: "bvf_c2", Kind: "check", Status: "running", StartedAt: at(390)}))
	v, err := s.LatestVerification(ctx, "check", "")
	if err != nil || v.ID != "bvf_c2" || v.Status != "failed" || v.Error != "boom" {
		t.Fatalf("latest %+v %v", v, err)
	}
	d, err := s.LatestVerification(ctx, "drill", "svc_db")
	if err != nil || len(d.Steps) != 1 || d.Steps[0].Detail != "databases=1 tables=3" {
		t.Fatalf("drill %+v %v", d, err)
	}
	if _, err := s.LatestVerification(ctx, "drill", "svc_none"); err != ErrNotFound {
		t.Fatalf("missing: %v", err)
	}
	// older than a year goes, except the newest of each kind/service; drills of deleted services go
	n, err := s.PruneVerifications(ctx, now, 365*24*time.Hour)
	if err != nil || n != 2 {
		t.Fatalf("pruned %d %v", n, err)
	}
	list, _ := s.ListVerifications(ctx, "", "", 0)
	if len(list) != 2 || list[0].ID != "bvf_d1" || list[1].ID != "bvf_c2" {
		t.Fatalf("left %+v", list)
	}
}
