package server

import (
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestGetDeploymentLongPoll(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "api", "kind": "image", "image": "nginx:1.27",
		"ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}},
	}, &svc, 201)
	var deps []api.Deployment
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/deployments", nil, &deps, 200)
	if len(deps) == 0 {
		t.Fatal("no deployment")
	}
	base := "/api/v1/services/" + svc.ID + "/deployments/" + deps[0].ID

	var d api.Deployment
	e.mustDo("GET", base+"?wait=10s", nil, &d, 200)
	if d.Status != "running" {
		t.Fatalf("after wait: %+v", d)
	}

	// not finished: the wait runs out and the current state comes back
	e.srv.st.UpdateDeployment(e.srv.bg, deps[0].ID, "pulling", "", "")
	start := time.Now()
	e.mustDo("GET", base+"?wait=300ms", nil, &d, 200)
	if d.Status != "pulling" || time.Since(start) < 250*time.Millisecond || time.Since(start) > 5*time.Second {
		t.Fatalf("pending: %s after %s", d.Status, time.Since(start))
	}
	// and it answers as soon as the deployment finishes
	go func() {
		time.Sleep(300 * time.Millisecond)
		e.srv.st.UpdateDeployment(e.srv.bg, deps[0].ID, "failed", "boom", "")
	}()
	e.mustDo("GET", base+"?wait=30s", nil, &d, 200)
	if d.Status != "failed" || d.Message != "boom" {
		t.Fatalf("finished: %+v", d)
	}

	e.mustDo("GET", base+"?wait=soon", nil, nil, 400)
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/deployments/dep_nope", nil, nil, 404)
}
