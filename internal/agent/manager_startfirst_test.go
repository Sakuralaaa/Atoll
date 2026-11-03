package agent_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// apiSpec is a stateless web service: no volumes, no host-published ports.
func apiSpec() api.ResolvedSpec {
	return api.ResolvedSpec{
		ServiceID: "svc_api", ProjectID: "prj_1", DeploymentID: "dep_1", Name: "api",
		Network: "atoll-prj_1", Image: "ghcr.io/me/api:v1",
		Env:     map[string]string{"PORT": "8080"},
		Ports:   []api.Port{{Name: "web", ContainerPort: 8080, Protocol: "http"}, {Name: "grpc", ContainerPort: 9090, Protocol: "tcp"}},
		Configs: []api.ConfigFile{{Path: "/etc/api.yaml", Content: "v: 1\n"}},
		Health:  &api.HealthCheck{HTTPPath: "/healthz"},
	}
}

func indexOf(calls []string, prefix string) int {
	return slices.IndexFunc(calls, func(c string) bool { return strings.HasPrefix(c, prefix) })
}

func lastDeploy(ev *events) api.DeployEvent {
	d := ev.deploys()
	return d[len(d)-1]
}

func TestStartFirstSwitchesWithoutStoppingFirst(t *testing.T) {
	m, f, ev, _ := newManager(t)
	ctx := context.Background()
	if err := m.Apply(ctx, apiSpec()); err != nil {
		t.Fatal(err)
	}
	// First deploy: nothing to keep serving, plain recreate.
	if last := lastDeploy(ev); last.Status != "running" || !strings.Contains(last.Message, "recreate") {
		t.Fatalf("first deploy event %+v", last)
	}
	v2 := apiSpec()
	v2.DeploymentID, v2.Image = "dep_2", "ghcr.io/me/api:v2"
	before := len(f.CallLog())
	if err := m.Apply(ctx, v2); err != nil {
		t.Fatal(err)
	}
	calls := f.CallLog()[before:]
	create := indexOf(calls, "create atoll-svc_api-next ")
	start := indexOf(calls, "start atoll-svc_api-next")
	connect := indexOf(calls, "connect atoll-svc_api-next atoll-ingress aliases=atoll-svc_api")
	stopOld := indexOf(calls, "stop atoll-svc_api")
	rmOld := indexOf(calls, "remove atoll-svc_api")
	rename := indexOf(calls, "rename atoll-svc_api-next atoll-svc_api")
	if create < 0 || start < 0 || connect < 0 || stopOld < 0 || rmOld < 0 || rename < 0 {
		t.Fatalf("missing step in %v", calls)
	}
	if !(create < start && start < connect && connect < stopOld && stopOld < rmOld && rmOld < rename) {
		t.Fatalf("wrong order: %v", calls)
	}
	if strings.Contains(strings.Join(calls, "\n"), "rename atoll-svc_api atoll-svc_api-old") {
		t.Fatalf("start-first must not rename the old container aside: %v", calls)
	}
	c, ok := f.Get("atoll-svc_api")
	if !ok || c.Spec.Image != "ghcr.io/me/api:v2" || c.Info.State != "running" || c.Info.Labels["atoll.deployment"] != "dep_2" {
		t.Fatalf("new container not in place: %+v", c)
	}
	// Project network (with the service alias) from the start, ingress joined at the switch
	// with the container-name alias Caddy dials.
	want := []agent.NetworkAttach{{Name: "atoll-prj_1", Aliases: []string{"api"}}, {Name: "atoll-ingress", Aliases: []string{"atoll-svc_api"}}}
	if !slices.EqualFunc(c.Spec.Networks, want, func(a, b agent.NetworkAttach) bool {
		return a.Name == b.Name && slices.Equal(a.Aliases, b.Aliases)
	}) {
		t.Fatalf("networks = %+v", c.Spec.Networks)
	}
	for _, n := range []string{"atoll-svc_api-next", "atoll-svc_api-old"} {
		if _, ok := f.Get(n); ok {
			t.Fatalf("leftover %s", n)
		}
	}
	if last := lastDeploy(ev); last.Status != "running" || last.DeploymentID != "dep_2" || !strings.Contains(last.Message, "start-first") {
		t.Fatalf("last event %+v", last)
	}
	st, _ := m.Store().Load("svc_api")
	if st.DeploymentID != "dep_2" {
		t.Fatalf("state = %s", st.DeploymentID)
	}
	// Status reports the renamed container as the service.
	cs, err := m.Status(ctx)
	if err != nil || len(cs) != 1 || cs[0].State != "running" || cs[0].DeploymentID != "dep_2" {
		t.Fatalf("status = %+v %v", cs, err)
	}
	// And a third deploy works from the renamed container too.
	v3 := apiSpec()
	v3.DeploymentID, v3.Image = "dep_3", "ghcr.io/me/api:v3"
	if err := m.Apply(ctx, v3); err != nil {
		t.Fatal(err)
	}
	if c, _ := f.Get("atoll-svc_api"); c.Spec.Image != "ghcr.io/me/api:v3" {
		t.Fatalf("third deploy image %s", c.Spec.Image)
	}
}

// Any failure before the switch leaves the old container serving: never stopped or renamed.
func TestStartFirstFailureKeepsOldServing(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		setup      func(f *agenttest.Fake, image string)
	}{
		{"exits", "exited", func(f *agenttest.Fake, image string) { f.ExitOnStart[image] = 1 }},
		{"unhealthy", "healthcheck failing", func(f *agenttest.Fake, image string) { f.HealthOnStart[image] = "unhealthy" }},
		{"restart loop", "keeps restarting", func(f *agenttest.Fake, image string) { f.RestartsOnStart[image] = 2 }},
		{"create fails", "create container", func(f *agenttest.Fake, _ string) { f.CreateErr = errors.New("no space") }},
		{"connect fails", "connect new container", func(f *agenttest.Fake, _ string) { f.ConnectErr = errors.New("boom") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, f, ev, _ := newManager(t)
			ctx := context.Background()
			if err := m.Apply(ctx, apiSpec()); err != nil {
				t.Fatal(err)
			}
			v2 := apiSpec()
			v2.DeploymentID, v2.Image = "dep_2", "ghcr.io/me/api:bad"
			f.Images[v2.Image] = agent.ImageInfo{ID: "sha256:bad"}
			tc.setup(f, v2.Image)
			before := len(f.CallLog())
			err := m.Apply(ctx, v2)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			for _, c := range f.CallLog()[before:] {
				if c == "stop atoll-svc_api" || c == "remove atoll-svc_api" || strings.HasPrefix(c, "rename atoll-svc_api ") {
					t.Fatalf("old container touched: %s", c)
				}
			}
			c, _ := f.Get("atoll-svc_api")
			if c.Spec.Image != "ghcr.io/me/api:v1" || c.Info.State != "running" {
				t.Fatalf("old container not serving: %s %s", c.Spec.Image, c.Info.State)
			}
			if _, ok := f.Get("atoll-svc_api-next"); ok {
				t.Fatal("leftover -next container")
			}
			if st, _ := m.Store().Load("svc_api"); st.DeploymentID != "dep_1" {
				t.Fatalf("state should roll back to dep_1, got %s", st.DeploymentID)
			}
			if last := lastDeploy(ev); last.Status != "failed" || last.DeploymentID != "dep_2" || !strings.Contains(last.Message, tc.want) {
				t.Fatalf("last event %+v", last)
			}
		})
	}
}

// Volumes, host-published ports, a stopped old container and a stopped desired state keep
// the stop-first recreate.
func TestStrategyFallsBackToRecreate(t *testing.T) {
	for _, tc := range []struct {
		name, why string
		mod       func(s *api.ResolvedSpec)
		prep      func(f *agenttest.Fake)
	}{
		{"volume", "has volumes", func(s *api.ResolvedSpec) { s.Volumes = []api.Volume{{Name: "data", MountPath: "/data"}} }, nil},
		{"host port", "host ports", func(s *api.ResolvedSpec) {
			s.Ports = append(s.Ports, api.Port{Name: "ssh", ContainerPort: 22, Protocol: "tcp", PublicPort: 2222})
		}, nil},
		{"old stopped", "no running container", func(*api.ResolvedSpec) {}, func(f *agenttest.Fake) { f.SetState("atoll-svc_api", "exited") }},
		{"desired stopped", "desired state is stopped", func(s *api.ResolvedSpec) { s.Stopped = true }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, f, ev, _ := newManager(t)
			ctx := context.Background()
			v1 := apiSpec()
			tc.mod(&v1)
			v1.Stopped = false
			if err := m.Apply(ctx, v1); err != nil {
				t.Fatal(err)
			}
			if tc.prep != nil {
				tc.prep(f)
			}
			v2 := apiSpec()
			tc.mod(&v2)
			v2.DeploymentID, v2.Image = "dep_2", "ghcr.io/me/api:v2"
			before := len(f.CallLog())
			if err := m.Apply(ctx, v2); err != nil {
				t.Fatal(err)
			}
			calls := f.CallLog()[before:]
			if indexOf(calls, "create atoll-svc_api-next") >= 0 {
				t.Fatalf("start-first used: %v", calls)
			}
			if indexOf(calls, "rename atoll-svc_api atoll-svc_api-old") < 0 {
				t.Fatalf("old container not moved aside: %v", calls)
			}
			if last := lastDeploy(ev); last.Status != "running" {
				t.Fatalf("last event %+v", last)
			}
			if !slices.ContainsFunc(ev.deploys(), func(d api.DeployEvent) bool {
				return d.DeploymentID == "dep_2" && d.Status == "starting" && strings.Contains(d.Message, "strategy: recreate") && strings.Contains(d.Message, tc.why)
			}) {
				t.Fatalf("no recreate strategy note in %+v", ev.deploys())
			}
		})
	}
}

func TestRemoveDropsLeftoverNextContainer(t *testing.T) {
	m, f, _, _ := newManager(t)
	ctx := context.Background()
	if err := m.Apply(ctx, apiSpec()); err != nil {
		t.Fatal(err)
	}
	cs, err := agent.BuildContainerSpec(agent.Paths{Root: t.TempDir()}, apiSpec())
	if err != nil {
		t.Fatal(err)
	}
	cs.Name = "atoll-svc_api-next"
	cs.Networks = cs.Networks[:1]
	if err := f.CreateContainer(ctx, cs); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, "svc_api", true); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Get("atoll-svc_api-next"); ok {
		t.Fatal("-next container left after remove")
	}
}
