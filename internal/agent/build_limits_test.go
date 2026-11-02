package agent_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/agent"
)

func TestBuildLimitsFromEnv(t *testing.T) {
	env := map[string]string{"ATOLL_BUILD_MEMORY": "1.5g", "ATOLL_BUILD_CPUS": "2", "ATOLL_BUILD_MIN_FREE_GB": "5", "ATOLL_BUILD_CACHE_GB": "0"}
	l := agent.BuildLimitsFromEnv(func(k string) string { return env[k] })
	if !l.Dedicated || l.MemoryMB != 1536 || l.CPUs != 2 || l.MinFreeGB != 5 || l.CacheKeepGB != 0 || l.CPUShares != 256 {
		t.Fatalf("limits %+v", l)
	}
	env = map[string]string{"ATOLL_BUILD_LIMITS": "off", "ATOLL_BUILD_MEMORY": "512m"}
	l = agent.BuildLimitsFromEnv(func(k string) string { return env[k] })
	if l.Dedicated || l.MemoryMB != 512 || l.MinFreeGB != 3 || l.CacheKeepGB != 10 {
		t.Fatalf("limits off %+v", l)
	}
	l = agent.BuildLimitsFromEnv(func(string) string { return "" })
	if l.MemoryMB < 1024 {
		t.Fatalf("default memory %d MB, want at least 1024", l.MemoryMB)
	}
}

func TestBuildUsesCappedBuilder(t *testing.T) {
	b, f, c, _, _ := newBuilder(t)
	b.Limits = agent.BuildLimits{Dedicated: true, MemoryMB: 2048, CPUShares: 256, CPUs: 1.5, CacheKeepGB: 10}
	c.inspectErr = errors.New("no builder")
	res, err := b.Build(context.Background(), buildPayload())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.ImageInfo(context.Background(), res.Image); err != nil {
		t.Fatalf("image not loaded: %v", err)
	}
	creates := c.callsOf("docker", "buildx create")
	if len(creates) != 1 {
		t.Fatalf("creates %+v", creates)
	}
	for _, want := range []string{"--driver docker-container", "memory=2048m", "memory-swap=2048m", "cpu-shares=256", "cpu-quota=150000", "atoll-builder-m2048-s256-c1500"} {
		if !strings.Contains(strings.Join(creates[0].args, " "), want) {
			t.Fatalf("create args %q lack %q", creates[0].args, want)
		}
	}
	builds := c.callsOf("docker", "buildx build")
	if len(builds) != 1 || !slices.Contains(builds[0].args, "--load") || !slices.Contains(builds[0].args, "atoll-builder-m2048-s256-c1500") {
		t.Fatalf("builds %+v", builds)
	}
	if p := c.callsOf("docker", "buildx prune"); len(p) != 1 || !strings.Contains(strings.Join(p[0].args, " "), "--keep-storage 10gb") {
		t.Fatalf("cache prune %+v", p)
	}
	// second build reuses the builder without inspecting or creating again
	c.sha = strings.Repeat("b", 40)
	if _, err := b.Build(context.Background(), buildPayload()); err != nil {
		t.Fatal(err)
	}
	if n := len(c.callsOf("docker", "buildx create")) + len(c.callsOf("docker", "buildx inspect")); n != 2 {
		t.Fatalf("builder set up %d times", n)
	}
}

func TestBuildFallsBackWithoutBuildx(t *testing.T) {
	b, _, c, ev, _ := newBuilder(t)
	b.Limits = agent.BuildLimits{Dedicated: true, MemoryMB: 1024}
	c.inspectErr = errors.New("unknown command buildx")
	c.createErr = errors.New("unknown command buildx")
	if _, err := b.Build(context.Background(), buildPayload()); err != nil {
		t.Fatal(err)
	}
	if n := len(c.callsOf("docker", "buildx build")); n != 0 {
		t.Fatalf("buildx build ran %d times", n)
	}
	plain := 0
	for _, k := range c.callsOf("docker", "build") {
		if k.args[0] == "build" {
			plain++
		}
	}
	if plain != 1 {
		t.Fatalf("plain builds = %d", plain)
	}
	if !strings.Contains(ev.all(), "without limits") {
		t.Fatalf("fallback not logged:\n%s", ev.all())
	}
}

func TestBuildRefusesLowDisk(t *testing.T) {
	b, _, c, _, p := newBuilder(t)
	b.Limits = agent.BuildLimits{MinFreeGB: 1 << 20, DockerRoot: p.Root} // a petabyte
	_, err := b.Build(context.Background(), buildPayload())
	if err == nil || !strings.Contains(err.Error(), "GiB free") {
		t.Fatalf("err = %v", err)
	}
	if n := len(c.callsOf("git", "fetch")); n != 0 {
		t.Fatalf("fetched despite low disk")
	}
}
