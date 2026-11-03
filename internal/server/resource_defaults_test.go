package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestDefaultResourceLimits(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ag, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var st api.Settings
	e.mustDo("GET", "/api/v1/settings", nil, &st, 200)
	if st.DefaultMemLimitMB == nil || *st.DefaultMemLimitMB != 1024 || st.DefaultCPULimit == nil || *st.DefaultCPULimit != 0 {
		t.Fatalf("defaults = %v %v, want 1024 MB, CPU 0", st.DefaultMemLimitMB, st.DefaultCPULimit)
	}
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	create := func(body map[string]any) api.Service {
		t.Helper()
		var v api.Service
		e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", body, &v, 201)
		return v
	}
	img := create(map[string]any{"name": "img", "kind": "image", "image": "nginx:1.27"})
	if img.MemLimitMB != 1024 || img.CPULimit != 0 {
		t.Fatalf("image service limits = %d MB / %v cores", img.MemLimitMB, img.CPULimit)
	}
	pg := create(map[string]any{"kind": "postgres"})
	if pg.MemLimitMB != 1024 {
		t.Fatalf("template service mem = %d", pg.MemLimitMB)
	}
	own := create(map[string]any{"name": "own", "kind": "image", "image": "nginx:1.27", "mem_limit_mb": 256, "cpu_limit": 0.5})
	if own.MemLimitMB != 256 || own.CPULimit != 0.5 {
		t.Fatalf("given limits replaced: %d MB / %v", own.MemLimitMB, own.CPULimit)
	}
	none := create(map[string]any{"name": "none", "kind": "image", "image": "nginx:1.27", "mem_limit_mb": -1, "cpu_limit": -1})
	if none.MemLimitMB != 0 || none.CPULimit != 0 {
		t.Fatalf("-1 should mean no limit: %d MB / %v", none.MemLimitMB, none.CPULimit)
	}
	// The deploy spec carries the limit to the agent.
	deadline := time.Now().Add(5 * time.Second)
	for found := false; !found; {
		for _, raw := range ag.received(api.MsgApply) {
			var ap api.ApplyPayload
			json.Unmarshal(raw, &ap)
			if ap.Spec.ServiceID == img.ID {
				if ap.Spec.MemLimitMB != 1024 {
					t.Fatalf("spec mem_limit_mb = %d", ap.Spec.MemLimitMB)
				}
				found = true
			}
		}
		if !found && time.Now().After(deadline) {
			t.Fatal("no apply for img")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Change the defaults: memory unlimited, 1.5 cores.
	zero, cpu := 0, 1.5
	st.DefaultMemLimitMB, st.DefaultCPULimit = &zero, &cpu
	var out api.Settings
	e.mustDo("PUT", "/api/v1/settings", st, &out, 200)
	if *out.DefaultMemLimitMB != 0 || *out.DefaultCPULimit != 1.5 {
		t.Fatalf("saved defaults = %v %v", *out.DefaultMemLimitMB, *out.DefaultCPULimit)
	}
	later := create(map[string]any{"name": "later", "kind": "image", "image": "nginx:1.27"})
	if later.MemLimitMB != 0 || later.CPULimit != 1.5 {
		t.Fatalf("new defaults not applied: %d MB / %v", later.MemLimitMB, later.CPULimit)
	}
	// Existing services keep what they had.
	var again api.Service
	e.mustDo("GET", "/api/v1/services/"+img.ID, nil, &again, 200)
	if again.MemLimitMB != 1024 || again.CPULimit != 0 {
		t.Fatalf("existing service changed: %d MB / %v", again.MemLimitMB, again.CPULimit)
	}

	// A PUT without the fields keeps them; invalid values are refused.
	e.mustDo("PUT", "/api/v1/settings", map[string]any{"tls_mode": "cloudflare-origin"}, &out, 200)
	if *out.DefaultMemLimitMB != 0 || *out.DefaultCPULimit != 1.5 {
		t.Fatalf("PUT without defaults changed them: %v %v", *out.DefaultMemLimitMB, *out.DefaultCPULimit)
	}
	for _, bad := range []map[string]any{{"default_mem_limit_mb": 3}, {"default_mem_limit_mb": -1}, {"default_cpu_limit": -0.5}, {"default_cpu_limit": 5000}} {
		if code := e.do("PUT", "/api/v1/settings", bad, nil); code != 400 {
			t.Fatalf("PUT %v: status %d, want 400", bad, code)
		}
	}
}
