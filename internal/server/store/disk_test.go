package store

import (
	"context"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestDiskUsageStoreAndCleanup(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	if err := s.CreateNode(ctx, api.Server{ID: "srv_a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProject(ctx, api.Project{ID: "prj_a", Name: "a", ServerID: "srv_a"}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateService(ctx, api.Service{ID: "svc_a", ProjectID: "prj_a", Name: "a", Kind: "image", Image: "nginx", CreatedAt: time.Now(),
		Volumes: []api.Volume{{Name: "data", MountPath: "/data"}}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	smp := api.ServiceDiskSample{ServiceID: "svc_a", Volumes: []api.VolumeUsage{{Name: "data", Bytes: 100}, {Name: "old", Bytes: 7}}, RootfsBytes: 5}
	if ok, err := s.SaveDiskSample(ctx, "srv_b", now, smp); err != nil || ok {
		t.Fatalf("sample from another server: %v %v", ok, err)
	}
	if ok, err := s.SaveDiskSample(ctx, "srv_a", now, api.ServiceDiskSample{ServiceID: "svc_gone"}); err != nil || ok {
		t.Fatalf("unknown service: %v %v", ok, err)
	}
	if ok, err := s.SaveDiskSample(ctx, "srv_a", now, smp); err != nil || !ok {
		t.Fatalf("save: %v %v", ok, err)
	}
	h, err := s.DiskHistory(ctx, "svc_a", now.Add(-time.Hour), 60)
	if err != nil || len(h) != 1 || h[0].Bytes != 105 { // unmounted "old" is not in the total
		t.Fatalf("history %+v %v", h, err)
	}
	if err := s.SetVolumeLimit(ctx, "svc_a", "data", api.VolumeLimit{LimitMB: 1, OverLimit: "bogus"}); err == nil {
		t.Fatal("bad over_limit accepted")
	}
	if err := s.SetVolumeLimit(ctx, "svc_a", "data", api.VolumeLimit{LimitMB: 1, OverLimit: api.OverLimitStop}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteService(ctx, "svc_a"); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"volume_usage", "volume_limits", "disk_usage_history", "disk_enforcement"} {
		var n int
		s.DB.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n)
		if n != 0 {
			t.Fatalf("%s keeps %d rows after the service is deleted", tbl, n)
		}
	}
}
