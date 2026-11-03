package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// slowApplies makes the fake agent behave like the real one for MsgApply, but each apply
// blocks until released: pulling event, wait, running event, then the result frame.
func slowApplies(a *fakeAgent) (started chan string, release chan struct{}) {
	started = make(chan string, 16)
	release = make(chan struct{}, 16)
	a.mu.Lock()
	a.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type != api.MsgApply {
			return api.ResultPayload{}, false
		}
		var p api.ApplyPayload
		json.Unmarshal(env.Payload, &p)
		ctx := context.Background()
		ev := func(st string) {
			a.send(ctx, api.Envelope{Type: api.MsgDeploy, Payload: api.DeployEvent{DeploymentID: p.Spec.DeploymentID, ServiceID: p.Spec.ServiceID, Status: st}})
		}
		ev("pulling")
		started <- p.Spec.DeploymentID
		<-release
		a.mu.Lock()
		a.ctrs[p.Spec.ServiceID] = api.ContainerState{ServiceID: p.Spec.ServiceID, DeploymentID: p.Spec.DeploymentID, State: "running", StartedAt: time.Now().UTC()}
		a.mu.Unlock()
		ev("running")
		return api.ResultPayload{OK: true}, true
	}
	a.mu.Unlock()
	return started, release
}

func waitStarted(t *testing.T, ch chan string) string {
	t.Helper()
	select {
	case id := <-ch:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("no apply reached the agent")
	}
	return ""
}

func waitDepStatus(t *testing.T, e *testEnv, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		d, err := e.srv.st.GetDeployment(context.Background(), id)
		if err == nil && d.Status == want {
			return
		}
		if time.Now().After(deadline) {
			st := "?"
			if d != nil {
				st = d.Status
			}
			t.Fatalf("deployment %s is %s, want %s", id, st, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func newQueueService(t *testing.T) (*testEnv, *fakeAgent, api.Service, chan string, chan struct{}) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	started, release := slowApplies(agent)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "whoami", "kind": "image", "image": "traefik/whoami:v1.10",
		"ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}},
	}, &svc, 201)
	first := waitStarted(t, started)
	release <- struct{}{}
	waitDepStatus(t, e, first, "running")
	return e, agent, svc, started, release
}

// set_env followed immediately by redeploy: the redeploy (the newest request) must run after
// the env deploy finishes, and end up the running deployment — not "superseded" and dropped.
func TestRedeployDuringEnvDeployRunsAfterIt(t *testing.T) {
	e, agent, svc, started, release := newQueueService(t)

	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/env", []api.EnvVar{{Key: "WHOAMI_NAME", Value: "e2e"}}, nil, 200)
	envDep := waitStarted(t, started) // env deploy is in flight on the agent
	var redep api.Deployment
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/actions", map[string]string{"action": "redeploy"}, &redep, 200)
	if redep.ID == envDep {
		t.Fatal("redeploy reused the env deployment")
	}
	select {
	case id := <-started:
		t.Fatalf("apply %s sent while %s still in flight", id, envDep)
	case <-time.After(200 * time.Millisecond):
	}

	release <- struct{}{} // env deploy finishes
	waitDepStatus(t, e, envDep, "running")
	if d, _ := e.srv.st.GetDeployment(context.Background(), redep.ID); d.Status == "superseded" {
		t.Fatal("queued redeploy was superseded by the older env deploy finishing")
	}
	if got := waitStarted(t, started); got != redep.ID {
		t.Fatalf("next apply = %s, want redeploy %s", got, redep.ID)
	}
	release <- struct{}{}
	waitDepStatus(t, e, redep.ID, "running")
	waitDepStatus(t, e, envDep, "superseded")

	var ap api.ApplyPayload
	applies := agent.received(api.MsgApply)
	json.Unmarshal(applies[len(applies)-1], &ap)
	if ap.Spec.DeploymentID != redep.ID || ap.Spec.Env["WHOAMI_NAME"] != "e2e" {
		t.Fatalf("last apply %s env=%v", ap.Spec.DeploymentID, ap.Spec.Env)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, _, _ := e.srv.st.GetService(context.Background(), svc.ID)
		if s.Status == "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service status %s", s.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Several requests while one is in flight coalesce: only the newest is sent next, the ones in
// between are superseded without ever reaching the agent.
func TestDeployRequestsCoalesceToNewest(t *testing.T) {
	e, agent, svc, started, release := newQueueService(t)
	before := len(agent.received(api.MsgApply))

	var d1, d2, d3 api.Deployment
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/actions", map[string]string{"action": "redeploy"}, &d1, 200)
	if got := waitStarted(t, started); got != d1.ID {
		t.Fatalf("first apply %s", got)
	}
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/actions", map[string]string{"action": "redeploy"}, &d2, 200)
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/actions", map[string]string{"action": "redeploy"}, &d3, 200)
	waitDepStatus(t, e, d2.ID, "superseded")

	release <- struct{}{}
	if got := waitStarted(t, started); got != d3.ID {
		t.Fatalf("after in-flight apply, sent %s; want newest %s", got, d3.ID)
	}
	release <- struct{}{}
	waitDepStatus(t, e, d3.ID, "running")
	waitDepStatus(t, e, d1.ID, "superseded")
	if n := len(agent.received(api.MsgApply)) - before; n != 2 {
		t.Fatalf("%d applies sent, want 2 (d2 coalesced away)", n)
	}
}

// PUT env with ?redeploy=0 only saves the variables: no new deployment starts.
func TestPutEnvNoRedeploy(t *testing.T) {
	e, _, svc, started, _ := newQueueService(t)
	var deps []api.Deployment
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/deployments", nil, &deps, 200)
	before := len(deps)
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/env?redeploy=0", []api.EnvVar{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}}, nil, 200)
	select {
	case id := <-started:
		t.Fatalf("deployment %s started, want none", id)
	case <-time.After(300 * time.Millisecond):
	}
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/deployments", nil, &deps, 200)
	if len(deps) != before {
		t.Fatalf("deployments: %d, want %d", len(deps), before)
	}
	var vars []api.EnvVar
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/env", nil, &vars, 200)
	if len(vars) != 2 {
		t.Fatalf("env: %+v", vars)
	}
}
