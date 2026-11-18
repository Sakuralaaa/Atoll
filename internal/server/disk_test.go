package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

const mb = int64(1 << 20)

func diskSample(sid string, rootfs int64, vols ...api.VolumeUsage) api.DiskUsagePayload {
	return api.DiskUsagePayload{At: time.Now().UTC(), Services: []api.ServiceDiskSample{{ServiceID: sid, Volumes: vols, RootfsBytes: rootfs}}}
}

func TestServiceDiskUsageAndLimits(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	hello := api.HelloPayload{AgentVersion: "test", Hostname: "box", OS: "linux", Arch: "amd64", CPUCores: 2, MemMB: 4096, DiskGB: 80, PublicIPv4: "203.0.113.7", DiskUsage: true}
	_, nodeID := startFakeAgentHello(t, e, api.ZeaburInventory{}, hello)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "db", "kind": "image", "image": "postgres:18",
		"volumes": []api.Volume{{Name: "data", MountPath: "/var/lib/postgresql"}},
	}, &svc, 201)

	// nothing measured yet: supported (the agent says so), no numbers
	var d api.ServiceDisk
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/disk", nil, &d, 200)
	if !d.Supported || d.MeasuredAt != nil || len(d.Volumes) != 1 || d.Volumes[0].Measured {
		t.Fatalf("before measuring: %+v", d)
	}

	// a measurement: the mounted volume, data of an unmounted one, the writable layer
	e.srv.handleDiskUsage(ctx, nodeID, diskSample(svc.ID, 10*mb, api.VolumeUsage{Name: "data", Bytes: 300 * mb}, api.VolumeUsage{Name: "old", Bytes: 50 * mb}))
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/disk?range=24h", nil, &d, 200)
	if d.MeasuredAt == nil || d.TotalBytes != 310*mb || d.RootfsBytes != 10*mb || len(d.Volumes) != 2 {
		t.Fatalf("after measuring: %+v", d)
	}
	if v := d.Volumes[0]; v.Name != "data" || v.Bytes != 300*mb || !v.Measured || v.MountPath != "/var/lib/postgresql" {
		t.Fatalf("data volume: %+v", v)
	}
	if v := d.Volumes[1]; v.Name != "old" || !v.Orphan || v.Bytes != 50*mb {
		t.Fatalf("orphan volume: %+v", v)
	}
	if len(d.History) != 1 || d.History[0].Bytes != 310*mb {
		t.Fatalf("history: %+v", d.History)
	}

	// another server cannot report for this service
	_, other := startFakeAgentAs(t, e, api.ZeaburInventory{}, "other", "203.0.113.8")
	e.srv.handleDiskUsage(ctx, other, diskSample(svc.ID, 0, api.VolumeUsage{Name: "data", Bytes: 1}))
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/disk", nil, &d, 200)
	if d.Volumes[0].Bytes != 300*mb {
		t.Fatalf("sample from another server was stored: %+v", d.Volumes[0])
	}

	// validation
	if code := e.do("PUT", "/api/v1/services/"+svc.ID+"/volumes/nope/limit", api.VolumeLimit{LimitMB: 100}, nil); code != 404 {
		t.Fatalf("unknown volume: %d", code)
	}
	if code := e.do("PUT", "/api/v1/services/"+svc.ID+"/volumes/data/limit", api.VolumeLimit{LimitMB: -1}, nil); code != 400 {
		t.Fatalf("negative limit: %d", code)
	}
	if code := e.do("PUT", "/api/v1/services/"+svc.ID+"/volumes/data/limit", api.VolumeLimit{LimitMB: 100, OverLimit: "delete"}, nil); code != 400 {
		t.Fatalf("bad action: %d", code)
	}

	// an alert-only limit below the usage: the problem fires, the service keeps running
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/volumes/data/limit", api.VolumeLimit{LimitMB: 250, OverLimit: api.OverLimitAlert}, &d, 200)
	if d.Volumes[0].LimitMB != 250 || d.Volumes[0].OverLimit != api.OverLimitAlert {
		t.Fatalf("limit not returned: %+v", d.Volumes[0])
	}
	e.srv.handleDiskUsage(ctx, nodeID, diskSample(svc.ID, 10*mb, api.VolumeUsage{Name: "data", Bytes: 300 * mb}))
	if stopped, _ := e.srv.st.DesiredStopped(ctx, svc.ID); stopped {
		t.Fatal("alert-only limit stopped the service")
	}
	probs, err := e.srv.st.ProblemSignals(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if p := findProblem(probs, api.ProblemVolumeFull, svc.ID); p == nil || !strings.Contains(p.Message, "卷 data") || strings.Contains(p.Message, "已停止") {
		t.Fatalf("volume_full: %+v", p)
	}

	// default action (stop): the next measurement over the limit stops the service
	d = api.ServiceDisk{}
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/volumes/data/limit", api.VolumeLimit{LimitMB: 250}, &d, 200)
	if d.Volumes[0].OverLimit != api.OverLimitStop {
		t.Fatalf("default over_limit: %+v", d.Volumes[0])
	}
	e.srv.handleDiskUsage(ctx, nodeID, diskSample(svc.ID, 10*mb, api.VolumeUsage{Name: "data", Bytes: 300 * mb}))
	if stopped, _ := e.srv.st.DesiredStopped(ctx, svc.ID); !stopped {
		t.Fatal("service over a stop limit kept running")
	}
	probs, _ = e.srv.st.ProblemSignals(ctx, time.Now())
	if p := findProblem(probs, api.ProblemVolumeFull, svc.ID); p == nil || !strings.Contains(p.Message, "服务已停止") {
		t.Fatalf("volume_full after stop: %+v", p)
	}

	// a manual start gets a grace period: the next measurement does not stop it again
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/actions", map[string]string{"action": "start"}, nil, 200)
	e.srv.handleDiskUsage(ctx, nodeID, diskSample(svc.ID, 10*mb, api.VolumeUsage{Name: "data", Bytes: 300 * mb}))
	if stopped, _ := e.srv.st.DesiredStopped(ctx, svc.ID); stopped {
		t.Fatal("stopped again during the grace period")
	}
	if g, _ := e.srv.st.GetDiskEnforcement(ctx, svc.ID); time.Until(g.GraceUntil) < 50*time.Minute {
		t.Fatalf("grace: %+v", g)
	}

	// below the warn ratio: no problem; removing the limit clears it
	e.srv.handleDiskUsage(ctx, nodeID, diskSample(svc.ID, 10*mb, api.VolumeUsage{Name: "data", Bytes: 100 * mb}))
	probs, _ = e.srv.st.ProblemSignals(ctx, time.Now())
	if p := findProblem(probs, api.ProblemVolumeFull, svc.ID); p != nil {
		t.Fatalf("volume_full below 80%%: %+v", p)
	}
	d = api.ServiceDisk{}
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/volumes/data/limit", api.VolumeLimit{LimitMB: 0}, &d, 200)
	if d.Volumes[0].LimitMB != 0 {
		t.Fatalf("limit not removed: %+v", d.Volumes[0])
	}
}

// An agent that predates disk usage: not supported, and the limit still saves.
func TestServiceDiskOldAgent(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "app", "kind": "image", "image": "nginx:1.27", "volumes": []api.Volume{{Name: "data", MountPath: "/data"}},
	}, &svc, 201)
	var d api.ServiceDisk
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/disk", nil, &d, 200)
	if d.Supported || d.TotalBytes != 0 || d.RootfsBytes != -1 {
		t.Fatalf("old agent: %+v", d)
	}
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/volumes/data/limit", api.VolumeLimit{LimitMB: 1024}, nil, 200)
	if got := e.srv.agentDiskUsage(nodeID); got {
		t.Fatal("old agent reported as supporting disk usage")
	}
}

func TestMemberServiceDisk(t *testing.T) {
	e := newMemberEnv(t, api.PermRead)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, s := range []api.Service{
		{ID: "svc_mine", ProjectID: "prj_a", Name: "mine", Kind: "image", Image: "nginx:1.27", Volumes: []api.Volume{{Name: "data", MountPath: "/data"}}, CreatedAt: now},
		{ID: "svc_other", ProjectID: "prj_b", Name: "other", Kind: "image", Image: "nginx:1.27", Volumes: []api.Volume{{Name: "data", MountPath: "/data"}}, CreatedAt: now},
	} {
		if err := e.srv.st.CreateService(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if code := e.mdo("GET", "/api/v1/services/svc_mine/disk", nil, nil); code != 200 {
		t.Fatalf("own service disk: %d", code)
	}
	if code := e.mdo("GET", "/api/v1/services/svc_other/disk", nil, nil); code != 403 && code != 404 {
		t.Fatalf("other server's service disk: %d", code)
	}
	if code := e.mdo("PUT", "/api/v1/services/svc_mine/volumes/data/limit", api.VolumeLimit{LimitMB: 10}, nil); code != 403 {
		t.Fatalf("read-only member set a limit: %d", code)
	}
}

func findProblem(ps []api.Problem, kind, subject string) *api.Problem {
	for i := range ps {
		if ps[i].Kind == kind && ps[i].SubjectID == subject {
			return &ps[i]
		}
	}
	return nil
}
