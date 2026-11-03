package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

type events struct {
	mu   sync.Mutex
	list []api.Envelope
}

func (e *events) emit(t string, p any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, api.Envelope{Type: t, Payload: p})
}

func (e *events) deploys() []api.DeployEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []api.DeployEvent
	for _, x := range e.list {
		if d, ok := x.Payload.(api.DeployEvent); ok {
			out = append(out, d)
		}
	}
	return out
}

func newManager(t *testing.T) (*agent.Manager, *agenttest.Fake, *events, agent.Paths) {
	t.Helper()
	f := agenttest.New()
	ev := &events{}
	p := agent.Paths{Root: t.TempDir()}
	m := agent.NewManager(f, p, ev.emit, nil)
	m.SettleTime = 0
	return m, f, ev, p
}

func webSpec() api.ResolvedSpec {
	return api.ResolvedSpec{
		ServiceID: "svc_web", ProjectID: "prj_1", DeploymentID: "dep_1", Name: "web",
		Network: "atoll-prj_1", Image: "ghcr.io/me/web:v1",
		Command: []string{"/bin/server"}, Args: []string{"--port", "3000"},
		Env: map[string]string{"PORT": "3000", "DATABASE_URL": "postgres://db/app"},
		Ports: []api.Port{
			{Name: "web", ContainerPort: 3000, Protocol: "http"},
			{Name: "ssh", ContainerPort: 22, Protocol: "tcp", PublicPort: 2222},
			{Name: "metrics", ContainerPort: 9100, Protocol: "tcp"},
		},
		Volumes:      []api.Volume{{Name: "data", MountPath: "/data"}},
		Configs:      []api.ConfigFile{{Path: "/etc/app/config.yaml", Content: "a: 1\n"}},
		Health:       &api.HealthCheck{HTTPPath: "/healthz", IntervalSec: 10},
		CPULimit:     0.5,
		MemLimitMB:   256,
		RegistryAuth: &api.RegistryAuth{Server: "ghcr.io", Username: "me", Password: "pw"},
	}
}

func dbSpec(id string) api.ResolvedSpec {
	return api.ResolvedSpec{
		ServiceID: id, ProjectID: "prj_1", DeploymentID: "dep_" + id, Name: id, Network: "atoll-prj_1",
		Image: "postgres:16", Env: map[string]string{"POSTGRES_PASSWORD": "x"},
		Ports:   []api.Port{{Name: "pg", ContainerPort: 5432, Protocol: "tcp"}},
		Volumes: []api.Volume{{Name: "pgdata", MountPath: "/var/lib/postgresql/data"}},
	}
}

func TestApplyCreatesExpectedContainer(t *testing.T) {
	m, f, ev, p := newManager(t)
	if err := m.Apply(context.Background(), webSpec()); err != nil {
		t.Fatal(err)
	}
	c, ok := f.Get("atoll-svc_web")
	if !ok {
		t.Fatal("container not created")
	}
	s := c.Spec
	if c.Info.State != "running" || s.RestartPolicy != "unless-stopped" || s.Image != "ghcr.io/me/web:v1" {
		t.Fatalf("bad container: state=%s restart=%s image=%s", c.Info.State, s.RestartPolicy, s.Image)
	}
	for k, v := range map[string]string{"atoll.service": "svc_web", "atoll.project": "prj_1", "atoll.deployment": "dep_1", "atoll.managed": "true"} {
		if s.Labels[k] != v {
			t.Errorf("label %s=%q want %q", k, s.Labels[k], v)
		}
	}
	if s.Labels["atoll.spec-hash"] == "" {
		t.Error("missing spec hash label")
	}
	wantNets := []agent.NetworkAttach{{Name: "atoll-prj_1", Aliases: []string{"web"}}, {Name: "atoll-ingress"}}
	if !reflect.DeepEqual(s.Networks, wantNets) {
		t.Errorf("networks = %+v", s.Networks)
	}
	if !reflect.DeepEqual(s.Env, []string{"DATABASE_URL=postgres://db/app", "PORT=3000"}) {
		t.Errorf("env = %v", s.Env)
	}
	if !reflect.DeepEqual(s.Entrypoint, []string{"/bin/server"}) || !reflect.DeepEqual(s.Cmd, []string{"--port", "3000"}) {
		t.Errorf("entrypoint/cmd = %v %v", s.Entrypoint, s.Cmd)
	}
	wantMounts := []agent.Mount{
		{Source: filepath.Join(p.Root, "volumes/svc_web/data"), Target: "/data"},
		{Source: filepath.Join(p.Root, "configs/svc_web/etc/app/config.yaml"), Target: "/etc/app/config.yaml", ReadOnly: true},
	}
	if !reflect.DeepEqual(s.Mounts, wantMounts) {
		t.Errorf("mounts = %+v", s.Mounts)
	}
	if !reflect.DeepEqual(s.Ports, []agent.PortBinding{{ContainerPort: 22, HostPort: 2222, Protocol: "tcp"}}) {
		t.Errorf("published ports = %+v (http ports must never be published)", s.Ports)
	}
	if s.NanoCPUs != 500_000_000 || s.MemoryBytes != 256<<20 {
		t.Errorf("limits = %d %d", s.NanoCPUs, s.MemoryBytes)
	}
	if s.Health == nil || s.Health.Test[0] != "CMD-SHELL" || !strings.Contains(s.Health.Test[1], "http://127.0.0.1:3000/healthz") {
		t.Errorf("health = %+v", s.Health)
	}
	if b, err := os.ReadFile(filepath.Join(p.Root, "configs/svc_web/etc/app/config.yaml")); err != nil || string(b) != "a: 1\n" {
		t.Errorf("config file: %q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(p.Root, "volumes/svc_web/data")); err != nil || !fi.IsDir() {
		t.Error("volume dir not created")
	}
	fi, err := os.Stat(p.StateFile("svc_web"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file: %v %v", fi, err)
	}
	if !slices.Contains(f.CallLog(), "pull ghcr.io/me/web:v1 auth=me") {
		t.Error("image not pulled with registry auth")
	}
	var statuses []string
	for _, d := range ev.deploys() {
		statuses = append(statuses, d.Status)
	}
	if !reflect.DeepEqual(statuses, []string{"pulling", "starting", "running"}) {
		t.Errorf("deploy events = %v", statuses)
	}
	if last := ev.deploys()[2]; last.ImageDigest == "" || last.DeploymentID != "dep_1" {
		t.Errorf("running event = %+v", last)
	}
}

func TestApplyCrashRestoresPreviousVersion(t *testing.T) {
	m, f, ev, _ := newManager(t)
	ctx := context.Background()
	v1 := webSpec()
	if err := m.Apply(ctx, v1); err != nil {
		t.Fatal(err)
	}
	v2 := webSpec()
	v2.DeploymentID, v2.Image = "dep_2", "ghcr.io/me/web:broken"
	f.ExitOnStart["ghcr.io/me/web:broken"] = 1
	if err := m.Apply(ctx, v2); err == nil {
		t.Fatal("expected failure")
	}
	c, _ := f.Get("atoll-svc_web")
	if c.Spec.Image != "ghcr.io/me/web:v1" || c.Info.State != "running" {
		t.Fatalf("old container not restored: %s %s", c.Spec.Image, c.Info.State)
	}
	if _, ok := f.Get("atoll-svc_web-old"); ok {
		t.Fatal("leftover -old container")
	}
	st, _ := m.Store().Load("svc_web")
	if st.DeploymentID != "dep_1" {
		t.Fatalf("state should roll back to dep_1, got %s", st.DeploymentID)
	}
	d := ev.deploys()
	if last := d[len(d)-1]; last.Status != "failed" || last.DeploymentID != "dep_2" || !strings.Contains(last.Message, "exited") {
		t.Fatalf("last event %+v", last)
	}
}

// A new version that fails its healthcheck, or comes up only because the restart policy keeps
// restarting it, is rolled back to the old container like one that exits.
func TestApplyUnhealthyOrRestartLoopRestoresPreviousVersion(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		setup      func(f *agenttest.Fake, image string)
	}{
		{"unhealthy", "healthcheck failing", func(f *agenttest.Fake, image string) { f.HealthOnStart[image] = "unhealthy" }},
		{"restart loop", "keeps restarting", func(f *agenttest.Fake, image string) { f.RestartsOnStart[image] = 3 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, f, ev, _ := newManager(t)
			ctx := context.Background()
			if err := m.Apply(ctx, webSpec()); err != nil {
				t.Fatal(err)
			}
			v2 := webSpec()
			v2.DeploymentID, v2.Image = "dep_2", "ghcr.io/me/web:bad"
			tc.setup(f, v2.Image)
			err := m.Apply(ctx, v2)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			c, _ := f.Get("atoll-svc_web")
			if c.Spec.Image != "ghcr.io/me/web:v1" || c.Info.State != "running" {
				t.Fatalf("old container not restored: %s %s", c.Spec.Image, c.Info.State)
			}
			if _, ok := f.Get("atoll-svc_web-old"); ok {
				t.Fatal("leftover -old container")
			}
			if st, _ := m.Store().Load("svc_web"); st.DeploymentID != "dep_1" {
				t.Fatalf("state should roll back to dep_1, got %s", st.DeploymentID)
			}
			d := ev.deploys()
			if last := d[len(d)-1]; last.Status != "failed" || last.DeploymentID != "dep_2" || !strings.Contains(last.Message, tc.want) {
				t.Fatalf("last event %+v", last)
			}
		})
	}
}

func TestApplyPullFailureLeavesOldRunning(t *testing.T) {
	m, f, _, _ := newManager(t)
	ctx := context.Background()
	if err := m.Apply(ctx, webSpec()); err != nil {
		t.Fatal(err)
	}
	v2 := webSpec()
	v2.DeploymentID, v2.Image = "dep_2", "ghcr.io/me/web:v2"
	f.PullErr["ghcr.io/me/web:v2"] = errors.New("manifest unknown")
	before := len(f.CallLog())
	if err := m.Apply(ctx, v2); err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range f.CallLog()[before:] {
		if strings.HasPrefix(c, "stop") || strings.HasPrefix(c, "remove") || strings.HasPrefix(c, "rename") {
			t.Fatalf("old container touched before image was pulled: %s", c)
		}
	}
}

func TestReconcileOnBootFromStateDir(t *testing.T) {
	m, f, _, _ := newManager(t)
	ctx := context.Background()
	stopped := dbSpec("svc_db")
	stopped.Stopped = true
	for _, s := range []api.ResolvedSpec{webSpec(), stopped} {
		if err := m.Store().Save(s); err != nil {
			t.Fatal(err)
		}
	}
	// Server offline and registry unreachable, but images are cached locally.
	f.Images["ghcr.io/me/web:v1"] = agent.ImageInfo{ID: "sha256:local1"}
	f.Images["postgres:16"] = agent.ImageInfo{ID: "sha256:local2"}
	f.PullErr["ghcr.io/me/web:v1"] = errors.New("network down")
	f.PullErr["postgres:16"] = errors.New("network down")
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	web, ok1 := f.Get("atoll-svc_web")
	db, ok2 := f.Get("atoll-svc_db")
	if !ok1 || !ok2 {
		t.Fatal("containers not recreated from state")
	}
	if web.Info.State != "running" || db.Info.State != "created" {
		t.Fatalf("states web=%s db=%s (stopped spec must not start)", web.Info.State, db.Info.State)
	}
	// Second boot: unchanged → no recreate; a crashed container is just started.
	f.SetState("atoll-svc_web", "exited")
	before := len(f.CallLog())
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	calls := f.CallLog()[before:]
	if !reflect.DeepEqual(calls, []string{"start atoll-svc_web"}) {
		t.Fatalf("second reconcile calls = %v", calls)
	}
}

func TestReconcileWithoutLocalImageFails(t *testing.T) {
	m, f, _, _ := newManager(t)
	_ = m.Store().Save(webSpec())
	f.PullErr["ghcr.io/me/web:v1"] = errors.New("network down")
	if err := m.Reconcile(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestSyncRemovesStaleKeepsVolumes(t *testing.T) {
	m, f, _, p := newManager(t)
	ctx := context.Background()
	a, b := dbSpec("svc_a"), dbSpec("svc_b")
	for _, s := range []api.ResolvedSpec{a, b} {
		if err := m.Apply(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	before := len(f.CallLog())
	if err := m.Sync(ctx, []api.ResolvedSpec{a}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Get("atoll-svc_b"); ok {
		t.Fatal("stale container not removed")
	}
	if _, ok := f.Get("atoll-svc_a"); !ok {
		t.Fatal("desired container removed")
	}
	for _, c := range f.CallLog()[before:] {
		if strings.Contains(c, "svc_a") {
			t.Fatalf("unchanged service touched by sync: %s", c)
		}
	}
	if _, err := os.Stat(p.VolumeDir("svc_b", "pgdata")); err != nil {
		t.Fatal("sync must keep volumes")
	}
	if _, err := m.Store().Load("svc_b"); !errors.Is(err, agent.ErrNotFound) {
		t.Fatal("stale state not deleted")
	}
	// Changed spec in sync → recreated.
	a2 := a
	a2.DeploymentID = "dep_a2"
	if err := m.Sync(ctx, []api.ResolvedSpec{a2}); err != nil {
		t.Fatal(err)
	}
	c, _ := f.Get("atoll-svc_a")
	if c.Spec.Labels["atoll.deployment"] != "dep_a2" {
		t.Fatal("changed spec not applied")
	}
}

func TestRemoveAndActions(t *testing.T) {
	m, f, _, p := newManager(t)
	ctx := context.Background()
	if err := m.Apply(ctx, dbSpec("svc_a")); err != nil {
		t.Fatal(err)
	}
	if err := m.Action(ctx, "svc_a", "stop"); err != nil {
		t.Fatal(err)
	}
	if st, _ := m.Store().Load("svc_a"); !st.Stopped {
		t.Fatal("stop not persisted")
	}
	if c, _ := f.Get("atoll-svc_a"); c.Info.State != "exited" {
		t.Fatal("not stopped")
	}
	// reboot reconcile must keep it stopped
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if c, _ := f.Get("atoll-svc_a"); c.Info.State != "exited" {
		t.Fatal("reconcile started a stopped service")
	}
	if err := m.Action(ctx, "svc_a", "start"); err != nil {
		t.Fatal(err)
	}
	if st, _ := m.Store().Load("svc_a"); st.Stopped {
		t.Fatal("start not persisted")
	}
	if err := m.Action(ctx, "svc_a", "restart"); err != nil {
		t.Fatal(err)
	}
	if err := m.Action(ctx, "svc_a", "explode"); err == nil {
		t.Fatal("unknown action accepted")
	}
	if err := m.Remove(ctx, "svc_a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.VolumeDir("svc_a", "pgdata")); err != nil {
		t.Fatal("keep_volumes ignored")
	}
	_ = m.Apply(ctx, dbSpec("svc_a"))
	if err := m.Remove(ctx, "svc_a", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.VolumesDir("svc_a")); !os.IsNotExist(err) {
		t.Fatal("volumes not deleted")
	}
	if _, ok := f.Get("atoll-svc_a"); ok {
		t.Fatal("container not removed")
	}
}

func TestStatusReportsMissing(t *testing.T) {
	m, _, _, _ := newManager(t)
	ctx := context.Background()
	_ = m.Apply(ctx, dbSpec("svc_a"))
	_ = m.Store().Save(dbSpec("svc_b"))
	st, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || st[0].State != "running" || st[0].ImageDigest == "" || st[1].State != "missing" {
		t.Fatalf("status = %+v", st)
	}
}

func TestInvalidIDsRejected(t *testing.T) {
	m, _, _, _ := newManager(t)
	s := dbSpec("../etc")
	if err := m.Apply(context.Background(), s); err == nil {
		t.Fatal("path traversal id accepted")
	}
	s = dbSpec("svc_x")
	s.Volumes = []api.Volume{{Name: "..", MountPath: "/x"}}
	if err := m.Apply(context.Background(), s); err == nil {
		t.Fatal("bad volume name accepted")
	}
}
