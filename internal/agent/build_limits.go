package agent

// Resource limits for builds from source. A `docker build` on the default builder runs inside
// dockerd with no cgroup of its own, so a heavy Dockerfile can starve or OOM-kill the
// services already running on the same server. When limits are on, builds run in a dedicated
// BuildKit container (buildx docker-container driver) whose memory and CPU are capped, and
// the image is loaded back into Docker (--load). If that builder cannot be created (no buildx,
// no network to pull moby/buildkit), the agent falls back to the plain builder and says so in
// the build log.
//
// Environment (atoll-agent):
//
//	ATOLL_BUILD_LIMITS=off      plain `docker build`, no dedicated builder
//	ATOLL_BUILD_MEMORY=2g       memory cap (default: 75% of RAM, at least 1 GiB)
//	ATOLL_BUILD_CPUS=1.5        hard CPU cap (default: none; builds get a low CPU weight instead)
//	ATOLL_BUILD_MIN_FREE_GB=3   refuse to start a build below this much free disk (0 = off)
//	ATOLL_BUILD_CACHE_GB=10     build cache kept after each build (0 = never prune)
//	ATOLL_DOCKER_ROOT=/var/lib/docker   where the free-disk check looks

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

// BuildLimits caps what one build may use. The zero value means no limits (tests).
type BuildLimits struct {
	Dedicated   bool    // run builds in a capped buildx builder
	MemoryMB    int     // builder memory cap
	CPUs        float64 // hard CPU cap (0 = none)
	CPUShares   int     // relative CPU weight (1024 = normal)
	MinFreeGB   float64 // free disk required to start
	CacheKeepGB int     // build cache kept after a build (0 = no prune)
	DockerRoot  string  // filesystem checked for free space
}

// buildCPUShares gives builds a quarter of a normal container's weight: they still use idle
// CPU in full, but running services win when both want it.
const buildCPUShares = 256

// BuildLimitsFromEnv reads the ATOLL_BUILD_* settings, filling defaults from the machine.
func BuildLimitsFromEnv(getenv func(string) string) BuildLimits {
	l := BuildLimits{Dedicated: true, CPUShares: buildCPUShares, MinFreeGB: 3, CacheKeepGB: 10, DockerRoot: "/var/lib/docker"}
	if v := strings.ToLower(strings.TrimSpace(getenv("ATOLL_BUILD_LIMITS"))); v == "off" || v == "0" || v == "false" || v == "no" {
		l.Dedicated = false
	}
	if mb, ok := parseMemMB(getenv("ATOLL_BUILD_MEMORY")); ok {
		l.MemoryMB = mb
	} else if vm, err := mem.VirtualMemory(); err == nil && vm.Total > 0 {
		l.MemoryMB = max(1024, int(vm.Total*3/4>>20))
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(getenv("ATOLL_BUILD_CPUS")), 64); err == nil && f > 0 {
		l.CPUs = f
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(getenv("ATOLL_BUILD_MIN_FREE_GB")), 64); err == nil && f >= 0 {
		l.MinFreeGB = f
	}
	if n, err := strconv.Atoi(strings.TrimSpace(getenv("ATOLL_BUILD_CACHE_GB"))); err == nil && n >= 0 {
		l.CacheKeepGB = n
	}
	if v := strings.TrimSpace(getenv("ATOLL_DOCKER_ROOT")); v != "" {
		l.DockerRoot = v
	}
	return l
}

// parseMemMB reads "2048", "2048m", "2g" or "1.5g" as megabytes.
func parseMemMB(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "g"), strings.HasSuffix(s, "gb"):
		mult, s = 1024, strings.TrimRight(s, "gb")
	case strings.HasSuffix(s, "m"), strings.HasSuffix(s, "mb"):
		s = strings.TrimRight(s, "mb")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return int(f * mult), true
}

// builderName encodes the limits, so changing them creates a fresh builder instead of
// silently reusing one with the old caps.
func (l BuildLimits) builderName() string {
	n := fmt.Sprintf("atoll-builder-m%d-s%d", l.MemoryMB, l.CPUShares)
	if l.CPUs > 0 {
		n += fmt.Sprintf("-c%d", int(l.CPUs*1000))
	}
	return n
}

func (l BuildLimits) driverOpts() []string {
	var o []string
	if l.MemoryMB > 0 {
		o = append(o, fmt.Sprintf("memory=%dm", l.MemoryMB), fmt.Sprintf("memory-swap=%dm", l.MemoryMB))
	}
	if l.CPUShares > 0 {
		o = append(o, fmt.Sprintf("cpu-shares=%d", l.CPUShares))
	}
	if l.CPUs > 0 {
		o = append(o, "cpu-period=100000", fmt.Sprintf("cpu-quota=%d", int(l.CPUs*100000)))
	}
	return o
}

// checkBuildDisk refuses to start a build that would fill the disk Docker writes to.
func (b *Builder) checkBuildDisk() error {
	l := b.Limits
	if l.MinFreeGB <= 0 {
		return nil
	}
	root := l.DockerRoot
	if _, err := os.Stat(root); err != nil {
		root = b.Paths.BuildsDir()
	}
	u, err := disk.Usage(root)
	if err != nil {
		return nil // unknown: do not block builds on a failed statfs
	}
	free := float64(u.Free) / (1 << 30)
	if free < l.MinFreeGB {
		return fmt.Errorf("only %.1f GiB free on %s (builds need %.0f GiB; free space with `docker system prune` or lower ATOLL_BUILD_MIN_FREE_GB)", free, root, l.MinFreeGB)
	}
	return nil
}

// builderState remembers the dedicated builder once it works (or that it cannot be made).
type builderState struct {
	mu     sync.Mutex
	name   string // ready builder
	failed string // name that failed to create; retried after an agent restart
}

// dedicatedBuilder returns the capped builder's name, creating it on first use, or "" to use
// the plain builder.
func (b *Builder) dedicatedBuilder(ctx context.Context, lg *buildLog) string {
	l := b.Limits
	if !l.Dedicated {
		return ""
	}
	name := l.builderName()
	b.bstate.mu.Lock()
	defer b.bstate.mu.Unlock()
	if b.bstate.name == name {
		return name
	}
	if b.bstate.failed == name {
		return ""
	}
	quiet := func(string) {}
	if err := b.Cmd.Run(ctx, "", nil, quiet, "docker", "buildx", "inspect", name); err == nil {
		b.bstate.name = name
		return name
	}
	args := []string{"buildx", "create", "--name", name, "--driver", "docker-container"}
	for _, o := range l.driverOpts() {
		args = append(args, "--driver-opt", o)
	}
	args = append(args, "--bootstrap")
	if err := b.run(ctx, lg, "", nil, nil, "docker", args...); err != nil {
		if ctx.Err() != nil {
			return ""
		}
		b.bstate.failed = name
		lg.line("atoll-agent: could not create the resource-limited builder, using the default builder without limits (set ATOLL_BUILD_LIMITS=off to skip this): " + firstLine(err.Error()))
		b.Log.Warn("build limits unavailable", "err", err)
		return ""
	}
	b.bstate.name = name
	return name
}

// pruneBuildCache trims BuildKit's cache to CacheKeepGB after a build (best effort).
func (b *Builder) pruneBuildCache(ctx context.Context, builder string) {
	if b.Limits.CacheKeepGB <= 0 {
		return
	}
	keep := fmt.Sprintf("%dgb", b.Limits.CacheKeepGB)
	args := []string{"builder", "prune", "-f", "--keep-storage", keep}
	if builder != "" {
		args = []string{"buildx", "prune", "--builder", builder, "-f", "--keep-storage", keep}
	}
	if err := b.Cmd.Run(ctx, "", nil, func(string) {}, "docker", args...); err != nil {
		b.Log.Warn("build cache prune", "err", err)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
