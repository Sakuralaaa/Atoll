package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// The spec hash decides whether an agent recreates a running container (boot reconcile,
// sync on reconnect, start). It is pinned to the value v0.1.4 agents computed for the same
// spec: changing the hash function (or ResolvedSpec's JSON form) would recreate every
// deployed service when the agent is upgraded. Change it only with a migration that keeps
// existing containers.
func TestSpecHashStableAcrossUpgrade(t *testing.T) {
	for _, c := range []struct {
		spec api.ResolvedSpec
		want string
	}{{webSpec(), "0b82be5410bde670"}, {dbSpec("svc_db"), "c3140595bf1fd8f3"}} {
		if got := agent.SpecHash(c.spec); got != c.want {
			t.Errorf("%s: spec hash %s, want %s (an agent upgrade would recreate deployed containers)", c.spec.ServiceID, got, c.want)
		}
	}
}

// Routes reach the agent as an ingress message only: binding and unbinding hostnames
// reloads Caddy and never touches the service's container, and an agent restart (as after
// an upgrade) leaves the container as it is.
func TestRouteChangesDoNotRecreateServiceContainer(t *testing.T) {
	ctx := context.Background()
	m, f, _, p := newManager(t)
	if err := m.Apply(ctx, webSpec()); err != nil {
		t.Fatal(err)
	}
	in := agent.NewIngress(f, p, nil, nil)
	in.SetGuards()
	l := &loads{}
	in.LoadConfig = l.load
	before := len(f.CallLog())
	for _, hosts := range [][]string{{"app.example.com"}, {"app.example.com", "www.example.com"}, {"www.example.com"}, nil} {
		if err := in.Apply(ctx, payload(hosts...)); err != nil {
			t.Fatal(err)
		}
	}
	// restart: boot reconcile from local state, then the server's sync with the same spec
	m2 := agent.NewManager(f, p, func(string, any) {}, nil)
	if err := m2.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m2.Sync(ctx, []api.ResolvedSpec{webSpec()}); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.CallLog()[before:] {
		if strings.Contains(c, "atoll-svc_web") {
			t.Fatalf("service container touched by a route change or restart: %q in %q", c, f.CallLog()[before:])
		}
	}
	if c, ok := f.Get("atoll-svc_web"); !ok || c.Info.State != "running" {
		t.Fatalf("service container: %+v", c)
	}
}
