package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
)

func TestBuilderEnvIdleStopAndDailyCache(t *testing.T) {
	l := agent.BuildLimitsFromEnv(func(string) string { return "" })
	if l.IdleStop != 10*time.Minute || l.DailyCacheKeepGB != 5 {
		t.Fatalf("defaults %+v", l)
	}
	for v, want := range map[string]time.Duration{"3": 3 * time.Minute, "90s": 90 * time.Second, "0": 0, "off": 0, "junk": 10 * time.Minute} {
		env := map[string]string{"ATOLL_BUILDER_IDLE_STOP": v, "ATOLL_BUILD_CACHE_DAILY_GB": "2"}
		l := agent.BuildLimitsFromEnv(func(k string) string { return env[k] })
		if l.IdleStop != want || l.DailyCacheKeepGB != 2 {
			t.Fatalf("%q: %+v", v, l)
		}
	}
}

// Removing a service drops its build images and leftover checkout dirs, not other services'.
func TestRemoveServiceDropsBuildImages(t *testing.T) {
	m, f, _, p := newManager(t)
	b, _, _, _, _ := newBuilder(t)
	b.RT, b.Paths = f, p
	m.OnRemoved = b.RemoveService
	ctx := context.Background()

	spec := webSpec()
	spec.Image, spec.LocalImage = agent.BuildImageTag(spec.ServiceID, testSHA), true
	f.AddImage(spec.Image, time.Now())
	f.AddImage(agent.BuildImageTag(spec.ServiceID, strings.Repeat("a", 40)), time.Now())
	other := agent.BuildImageTag("svc_other", testSHA)
	f.AddImage(other, time.Now())
	for _, d := range []string{spec.ServiceID + "-123", spec.ServiceID + "-x-456", "svc_other-789"} {
		if err := os.MkdirAll(filepath.Join(p.BuildsDir(), d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Apply(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, spec.ServiceID, false); err != nil {
		t.Fatal(err)
	}
	imgs, _ := f.ListImages(ctx, agent.BuildImageRepo+spec.ServiceID)
	if len(imgs) != 0 {
		t.Fatalf("build images left: %+v", imgs)
	}
	if _, err := f.ImageInfo(ctx, other); err != nil {
		t.Fatal("other service's build image removed")
	}
	ents, _ := os.ReadDir(p.BuildsDir())
	var left []string
	for _, e := range ents {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != "svc_other-789,"+spec.ServiceID+"-x-456" {
		t.Fatalf("build dirs left %v", left)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

const curBuilder = "atoll-builder-m2048-s256"

func cappedBuilder(t *testing.T) (*agent.Builder, *fakeCmd, *clock) {
	b, _, c, _, _ := newBuilder(t)
	b.Limits = agent.BuildLimits{Dedicated: true, MemoryMB: 2048, CPUShares: 256, CacheKeepGB: 10, DailyCacheKeepGB: 5, IdleStop: 10 * time.Minute}
	ck := &clock{t: time.Unix(1_800_000_000, 0)}
	b.Now = ck.now
	return b, c, ck
}

func TestBuilderStopsWhenIdle(t *testing.T) {
	b, c, ck := cappedBuilder(t)
	ctx := context.Background()
	b.StopIdle(ctx) // no builder yet
	if n := len(c.callsOf("docker", "buildx stop")); n != 0 {
		t.Fatalf("stopped a builder that was never used: %d", n)
	}
	if _, err := b.Build(ctx, buildPayload()); err != nil {
		t.Fatal(err)
	}
	ck.add(9 * time.Minute)
	b.StopIdle(ctx)
	if n := len(c.callsOf("docker", "buildx stop")); n != 0 {
		t.Fatal("stopped before the idle time")
	}
	ck.add(2 * time.Minute)
	b.StopIdle(ctx)
	b.StopIdle(ctx) // already stopped: not again
	if s := c.callsOf("docker", "buildx stop"); len(s) != 1 || s[0].args[2] != curBuilder {
		t.Fatalf("stops %+v", s)
	}
	// the next build reuses the (stopped) builder: buildx starts it, no re-create
	c.sha = strings.Repeat("b", 40)
	if _, err := b.Build(ctx, buildPayload()); err != nil {
		t.Fatal(err)
	}
	if n := len(c.callsOf("docker", "buildx create")); n != 0 {
		t.Fatalf("builder re-created %d times", n)
	}
	ck.add(11 * time.Minute)
	b.StopIdle(ctx)
	if n := len(c.callsOf("docker", "buildx stop")); n != 2 {
		t.Fatalf("not stopped again after the second build: %d", n)
	}
}

func TestBuilderNotStoppedDuringBuild(t *testing.T) {
	b, c, ck := cappedBuilder(t)
	ctx := context.Background()
	if _, err := b.Build(ctx, buildPayload()); err != nil {
		t.Fatal(err)
	}
	c.buildBlock = make(chan struct{})
	c.sha = strings.Repeat("c", 40)
	done := make(chan error, 1)
	go func() { _, err := b.Build(ctx, buildPayload()); done <- err }()
	<-c.started
	<-c.started
	ck.add(time.Hour)
	b.StopIdle(ctx)
	b.Maintain(ctx)
	close(c.buildBlock)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := len(c.callsOf("docker", "buildx stop")) + len(c.callsOf("docker", "buildx rm")); n != 0 {
		t.Fatalf("touched the builder during a build: %d", n)
	}
}

func TestBuilderRemovesOrphans(t *testing.T) {
	b, c, _ := cappedBuilder(t)
	c.lsOut = []string{
		"NAME/NODE                    DRIVER/ENDPOINT    STATUS    BUILDKIT   PLATFORMS",
		"atoll-builder-m1024-s256     docker-container",
		" \\_ atoll-builder-m1024-s2560 unix:///var/run/docker.sock   stopped",
		curBuilder + "*    docker-container",
		"  " + curBuilder + "0     unix:///var/run/docker.sock   running   v0.18.1",
		"mine                         docker-container",
		"default                      docker",
	}
	if _, err := b.Build(context.Background(), buildPayload()); err != nil {
		t.Fatal(err)
	}
	rms := c.callsOf("docker", "buildx rm")
	if len(rms) != 1 || rms[0].args[len(rms[0].args)-1] != "atoll-builder-m1024-s256" {
		t.Fatalf("rm %+v", rms)
	}
	// once per agent run
	c.sha = strings.Repeat("d", 40)
	if _, err := b.Build(context.Background(), buildPayload()); err != nil {
		t.Fatal(err)
	}
	if n := len(c.callsOf("docker", "buildx ls")); n != 1 {
		t.Fatalf("listed builders %d times", n)
	}
}

func TestBuilderDailyMaintain(t *testing.T) {
	b, c, _ := cappedBuilder(t)
	c.lsOut = []string{"atoll-builder-m512-s256  docker-container", curBuilder + "  docker-container"}
	has := true
	b.HasSourceServices = func() bool { return has }
	ctx := context.Background()

	b.Maintain(ctx) // source services remain: old builder removed, cache trimmed, builder stopped
	rms := c.callsOf("docker", "buildx rm")
	if len(rms) != 1 || !strings.Contains(strings.Join(rms[0].args, " "), "atoll-builder-m512-s256") {
		t.Fatalf("rm %+v", rms)
	}
	pr := c.callsOf("docker", "buildx prune")
	if len(pr) != 1 || !strings.Contains(strings.Join(pr[0].args, " "), "--builder "+curBuilder+" -f --keep-storage 5gb") {
		t.Fatalf("prune %+v", pr)
	}
	if s := c.callsOf("docker", "buildx stop"); len(s) != 1 {
		t.Fatalf("stop %+v", s)
	}

	has = false
	b.Maintain(ctx) // nothing builds from source here any more: the builder goes with its cache
	rms = c.callsOf("docker", "buildx rm")
	if len(rms) != 3 || rms[2].args[len(rms[2].args)-1] != curBuilder {
		t.Fatalf("rm %+v", rms)
	}
	// a later build creates it again
	c.inspectErr = errors.New("no builder")
	if _, err := b.Build(ctx, buildPayload()); err != nil {
		t.Fatal(err)
	}
	if n := len(c.callsOf("docker", "buildx create")); n != 1 {
		t.Fatalf("not re-created: %d", n)
	}

	// limits off: every atoll builder is an orphan; no builder at all: no-op
	c2 := newFakeCmd(nil)
	c2.lsOut = []string{curBuilder + "  docker-container"}
	b2 := &agent.Builder{Cmd: c2, HasCommand: func(string) bool { return true }}
	b2.Maintain(ctx)
	if rm := c2.callsOf("docker", "buildx rm"); len(rm) != 1 || len(c2.callsOf("docker", "buildx prune")) != 0 {
		t.Fatalf("limits off %+v", c2.calls)
	}
}

func TestAgentHasSourceServices(t *testing.T) {
	a := agent.New(&agent.Config{DataDir: t.TempDir(), NodeID: "n1"}, nil, nil, nil)
	if a.Builds.HasSourceServices() {
		t.Fatal("empty node has source services")
	}
	spec := webSpec()
	if err := a.Mgr.Store().Save(spec); err != nil {
		t.Fatal(err)
	}
	if a.Builds.HasSourceServices() {
		t.Fatal("image service counted as source build")
	}
	spec.ServiceID, spec.LocalImage = "svc_src", true
	if err := a.Mgr.Store().Save(spec); err != nil {
		t.Fatal(err)
	}
	if !a.Builds.HasSourceServices() {
		t.Fatal("source build not seen")
	}
}
