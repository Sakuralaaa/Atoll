package server

import (
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestServiceOrder(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "order", "server_id": nodeID}, &p, 201)
	ids := map[string]string{}
	for _, n := range []string{"alpha", "beta", "gamma"} {
		var s api.Service
		e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": n, "kind": "image", "image": "busybox:1"}, &s, 201)
		ids[n] = s.ID
	}
	names := func() []string {
		var got api.Project
		e.mustDo("GET", "/api/v1/projects/"+p.ID, nil, &got, 200)
		var out []string
		for _, s := range got.Services {
			out = append(out, s.Name)
		}
		return out
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("order %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("order %v, want %v", got, want)
			}
		}
	}
	eq(names(), "alpha", "beta", "gamma")
	e.mustDo("PUT", "/api/v1/projects/"+p.ID+"/service-order", map[string]any{"services": []string{ids["gamma"], ids["alpha"]}}, nil, 200)
	eq(names(), "gamma", "alpha", "beta") // unlisted services follow by name
	if code := e.do("PUT", "/api/v1/projects/"+p.ID+"/service-order", map[string]any{"services": []string{"svc_nope"}}, nil); code != 400 {
		t.Fatalf("foreign id: %d", code)
	}
	if code := e.do("PUT", "/api/v1/projects/"+p.ID+"/service-order", map[string]any{"services": []string{ids["beta"], ids["beta"]}}, nil); code != 400 {
		t.Fatalf("duplicate id: %d", code)
	}
}
