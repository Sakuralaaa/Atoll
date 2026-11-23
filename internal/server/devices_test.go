package server

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// waitApplySpec waits for an apply whose spec satisfies ok and returns it.
func waitApplySpec(t *testing.T, a *fakeAgent, ok func(api.ResolvedSpec) bool) api.ResolvedSpec {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range slices.Backward(a.received(api.MsgApply)) {
			var p api.ApplyPayload
			if json.Unmarshal(raw, &p) == nil && ok(p.Spec) {
				return p.Spec
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no matching apply")
	return api.ResolvedSpec{}
}

func TestServiceDevicesDeploy(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	hello := api.HelloPayload{AgentVersion: "test", Hostname: "box", OS: "linux", Arch: "amd64", CPUCores: 2, MemMB: 4096, DiskGB: 80, PublicIPv4: "203.0.113.7", Devices: true}
	agent, nodeID := startFakeAgentHello(t, e, api.ZeaburInventory{}, hello)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "phone", "server_id": nodeID}, &p, 201)

	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "android", "kind": "image",
		"image": "ghcr.io/sakuralaaa/android-playstore:latest", "devices": []string{"/dev/kvm", "/dev/kvm"}, "shm_size_mb": 2048}, &svc, 201)
	if !slices.Equal(svc.Devices, []string{"/dev/kvm"}) || svc.ShmSizeMB != 2048 {
		t.Fatalf("stored: %v %d", svc.Devices, svc.ShmSizeMB)
	}
	spec := waitApplySpec(t, agent, func(s api.ResolvedSpec) bool { return s.ServiceID == svc.ID })
	if !slices.Equal(spec.Devices, []string{"/dev/kvm"}) || spec.ShmSizeMB != 2048 {
		t.Fatalf("applied: %v %d", spec.Devices, spec.ShmSizeMB)
	}

	// edit: add TUN, then clear everything
	e.mustDo("PATCH", "/api/v1/services/"+svc.ID, map[string]any{"devices": []string{"/dev/kvm", "/dev/net/tun"}}, &svc, 200)
	waitApplySpec(t, agent, func(s api.ResolvedSpec) bool { return len(s.Devices) == 2 && s.ShmSizeMB == 2048 })
	var cleared api.Service
	e.mustDo("PATCH", "/api/v1/services/"+svc.ID, map[string]any{"devices": []string{}, "shm_size_mb": 0}, &cleared, 200)
	if len(cleared.Devices) != 0 || cleared.ShmSizeMB != 0 {
		t.Fatalf("cleared: %v %d", cleared.Devices, cleared.ShmSizeMB)
	}
	waitApplySpec(t, agent, func(s api.ResolvedSpec) bool { return s.ServiceID == svc.ID && len(s.Devices) == 0 && s.ShmSizeMB == 0 })

	// only allowlisted devices, sane /dev/shm
	for _, bad := range []map[string]any{
		{"devices": []string{"/dev/sda"}},
		{"devices": []string{"/dev/mem"}},
		{"devices": []string{"/dev/../dev/sda"}},
		{"devices": []string{"/dev/kvm/../sda"}},
		{"shm_size_mb": -1},
		{"shm_size_mb": api.MaxShmSizeMB + 1},
	} {
		if code := e.do("PATCH", "/api/v1/services/"+svc.ID, bad, nil); code != 400 {
			t.Fatalf("%v: %d", bad, code)
		}
	}
}

func TestServiceDevicesNeedNewAgent(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{}) // hello without Devices: an older agent
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "phone", "server_id": nodeID}, &p, 201)
	if code := e.do("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "android", "kind": "image",
		"image": "ghcr.io/sakuralaaa/android-playstore:latest", "devices": []string{"/dev/kvm"}}, nil); code != 409 {
		t.Fatalf("create with devices on an old agent: %d", code)
	}
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "app", "kind": "image", "image": "nginx:1"}, &svc, 201)
	if code := e.do("PATCH", "/api/v1/services/"+svc.ID, map[string]any{"shm_size_mb": 1024}, nil); code != 409 {
		t.Fatalf("patch shm on an old agent: %d", code)
	}
	got, _, _ := e.srv.st.GetService(t.Context(), svc.ID)
	if got.ShmSizeMB != 0 {
		t.Fatal("refused edit was saved")
	}
}

func TestAllowedDevice(t *testing.T) {
	for d, want := range map[string]bool{
		"/dev/kvm": true, "/dev/net/tun": true, "/dev/fuse": true, "/dev/dri": true, "/dev/dri/renderD128": true, "/dev/dri/card0": true,
		"/dev/sda": false, "/dev/mem": false, "/dev/kvm/": false, "/dev//kvm": false, "/dev/dri/../sda": false, "dev/kvm": false, "": false, "/dev/kvm2": false,
	} {
		if api.AllowedDevice(d) != want {
			t.Errorf("%q: want %v", d, want)
		}
	}
}
