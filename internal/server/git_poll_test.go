package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// headHook answers MsgGitHead with *head and passes other frames to the previous hook.
func headHook(a *fakeAgent, head *string, mu *sync.Mutex) {
	a.mu.Lock()
	prev := a.hook
	a.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type == api.MsgGitHead {
			mu.Lock()
			h := *head
			mu.Unlock()
			raw, _ := json.Marshal(api.GitHeadResult{Commit: h})
			return api.ResultPayload{OK: true, Output: string(raw)}, true
		}
		if prev != nil {
			return prev(env)
		}
		return api.ResultPayload{}, false
	}
	a.mu.Unlock()
}

func pollWait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestGitPollDeploysNewCommit(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := capableAgent(t, e)
	gitBuilds(agent)
	var mu sync.Mutex
	head := gitHead
	headHook(agent, &head, &mu)
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "poll", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "app", "kind": "image",
		"git": map[string]any{"repo": "https://git.example.com/me/app.git", "poll_interval_sec": 60}}, &svc, 201)
	if svc.Git.PollIntervalSec != 60 || svc.Git.Provider != "git" {
		t.Fatalf("git %+v", svc.Git)
	}
	waitDepStatus(t, e, latestDep(t, e, svc.ID).ID, "running")
	ctx := context.Background()
	now := time.Now()

	// head unchanged: nothing to do
	e.srv.runDueGitPolls(ctx, now)
	pollWait(t, "first poll", func() bool { return len(agent.received(api.MsgGitHead)) == 1 })
	pollWait(t, "poll done", func() bool { e.srv.gitPoll.mu.Lock(); defer e.srv.gitPoll.mu.Unlock(); return len(e.srv.gitPoll.inflight) == 0 })
	if n := len(agent.builds()); n != 1 {
		t.Fatalf("builds after unchanged poll: %d", n)
	}
	// not due yet
	e.srv.runDueGitPolls(ctx, now.Add(30*time.Second))
	time.Sleep(50 * time.Millisecond)
	if n := len(agent.received(api.MsgGitHead)); n != 1 {
		t.Fatalf("polled before the interval: %d", n)
	}
	// the branch moved: a "poll" deployment builds exactly that commit
	mu.Lock()
	head = gitPush
	mu.Unlock()
	e.srv.runDueGitPolls(ctx, now.Add(61*time.Second))
	pollWait(t, "poll deploy", func() bool { return len(agent.builds()) == 2 })
	d := latestDep(t, e, svc.ID)
	waitDepStatus(t, e, d.ID, "running")
	if d.Trigger != "poll" || agent.builds()[1].Commit != gitPush {
		t.Fatalf("poll deployment %+v build %+v", d, agent.builds()[1])
	}
	hp := agent.received(api.MsgGitHead)
	var hpl api.GitHeadPayload
	json.Unmarshal(hp[0], &hpl)
	if hpl.Repo != "https://git.example.com/me/app.git" || hpl.Branch != "main" || hpl.ServiceID != svc.ID {
		t.Fatalf("head payload %+v", hpl)
	}
}
