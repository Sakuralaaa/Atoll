package agent

// Cleanup around builds from source, so nothing outlives what it was built for:
//
//   - When a service is removed from this server (delete, project delete, migration away,
//     sync of a stale service) its build images atoll-build/<service_id>:* and any leftover
//     checkout dirs of its builds are removed (Builder.RemoveService, called by the Manager).
//   - The dedicated buildx builder (a BuildKit container, ~90 MiB RAM idle) is stopped after
//     it has been idle for ATOLL_BUILDER_IDLE_STOP (default 10 minutes; `docker buildx stop`
//     keeps its cache, and the next build starts it again on its own).
//   - Daily (with the image prune): builders named atoll-builder-* other than the current one
//     (left behind when the limits, and so the name, changed) are removed; when no service on
//     this server is built from source any more, the current builder is removed too, with
//     its cache (`docker buildx rm`); otherwise its cache is trimmed to
//     ATOLL_BUILD_CACHE_DAILY_GB (default 5 GB) and it is stopped again.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBuilderIdleStop is how long the dedicated builder may sit idle before it is stopped.
const DefaultBuilderIdleStop = 10 * time.Minute

// DefaultDailyCacheKeepGB is the build cache the daily maintenance keeps.
const DefaultDailyCacheKeepGB = 5

// builderPrefix is the name prefix of every builder the agent creates (see builderName).
const builderPrefix = "atoll-builder-"

// parseIdleStop reads ATOLL_BUILDER_IDLE_STOP: minutes ("10") or a Go duration ("90s",
// "1h"); "0"/"off" never stops the builder. ok is false for an empty or invalid value.
func parseIdleStop(s string) (time.Duration, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "":
		return 0, false
	case "off", "never", "false", "no":
		return 0, true
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil && n >= 0 {
		return time.Duration(n * float64(time.Minute)), true
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return d, true
	}
	return 0, false
}

// buildDirRe matches the checkout dirs os.MkdirTemp(BuildsDir, serviceID+"-") creates.
var buildDirRe = regexp.MustCompile(`^(.+)-[0-9]+$`)

// RemoveService removes a removed service's build images and leftover build dirs (best
// effort: an image a container still uses stays, errors are logged).
func (b *Builder) RemoveService(ctx context.Context, serviceID string) {
	b.init()
	if validID(serviceID) != nil {
		return
	}
	os.Remove(filepath.Join(b.Paths.Root, "build-known-hosts", serviceID)) // TOFU pin (build_git.go)
	imgs, err := b.RT.ListImages(ctx, BuildImageRepo+strings.ToLower(serviceID))
	if err != nil {
		b.Log.Warn("list build images of removed service", "service", serviceID, "err", err)
	}
	for _, im := range imgs {
		if err := b.RT.RemoveImage(ctx, im.Ref); err != nil && !errors.Is(err, ErrNotFound) {
			b.Log.Warn("remove build image of removed service", "image", im.Ref, "err", err)
			continue
		}
		b.Log.Info("removed build image of removed service", "image", im.Ref)
	}
	b.bstate.actMu.Lock()
	active := b.bstate.active
	b.bstate.actMu.Unlock()
	if active == serviceID {
		return // its running build cleans its own checkout when it ends
	}
	ents, _ := os.ReadDir(b.Paths.BuildsDir())
	for _, e := range ents {
		if m := buildDirRe.FindStringSubmatch(e.Name()); e.IsDir() && m != nil && m[1] == serviceID {
			if err := os.RemoveAll(filepath.Join(b.Paths.BuildsDir(), e.Name())); err != nil {
				b.Log.Warn("remove build dir", "dir", e.Name(), "err", err)
			}
		}
	}
}

func (b *Builder) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// tryLock takes the build slot without waiting; false while a build runs.
func (b *Builder) tryLock() bool {
	select {
	case b.slot <- struct{}{}:
		return true
	default:
		return false
	}
}

func (b *Builder) docker(ctx context.Context, out func(string), args ...string) error {
	if out == nil {
		out = func(string) {}
	}
	return b.Cmd.Run(ctx, "", nil, out, "docker", args...)
}

// stopIdleLocked stops the ready builder when it has been idle for IdleStop. Caller holds
// the build slot.
func (b *Builder) stopIdleLocked(ctx context.Context, force bool) {
	b.bstate.mu.Lock()
	name, stopped, last := b.bstate.name, b.bstate.stopped, b.bstate.lastUsed
	b.bstate.mu.Unlock()
	idle := b.Limits.IdleStop
	if name == "" || stopped || idle <= 0 || (!force && b.now().Sub(last) < idle) {
		return
	}
	if err := b.docker(ctx, nil, "buildx", "stop", name); err != nil {
		b.Log.Warn("stop idle builder", "builder", name, "err", err)
		return
	}
	b.bstate.mu.Lock()
	b.bstate.stopped = true
	b.bstate.mu.Unlock()
	b.Log.Info("stopped idle builder", "builder", name)
}

// StopIdle stops the dedicated builder when it has been idle long enough (no-op while a
// build runs).
func (b *Builder) StopIdle(ctx context.Context) {
	b.init()
	if !b.tryLock() {
		return
	}
	defer func() { <-b.slot }()
	b.stopIdleLocked(ctx, false)
}

// IdleLoop runs StopIdle until ctx ends (every minute, or a quarter of IdleStop if shorter).
func (b *Builder) IdleLoop(ctx context.Context) {
	b.init()
	idle := b.Limits.IdleStop
	if !b.Limits.Dedicated || idle <= 0 {
		return
	}
	every := min(time.Minute, max(idle/4, 10*time.Millisecond))
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.StopIdle(ctx)
		}
	}
}

// listBuilders returns the names of the buildx builders on this host (top-level rows of
// `docker buildx ls`; node rows are indented).
func (b *Builder) listBuilders(ctx context.Context) ([]string, error) {
	var names []string
	err := b.docker(ctx, func(line string) {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			return
		}
		f := strings.Fields(line)
		if len(f) == 0 || f[0] == "NAME/NODE" {
			return
		}
		names = append(names, strings.TrimSuffix(f[0], "*"))
	}, "buildx", "ls")
	return names, err
}

// removeOrphansLocked removes atoll builders other than keep ("" = all of them). Caller
// holds the build slot.
func (b *Builder) removeOrphansLocked(ctx context.Context, keep string) {
	names, err := b.listBuilders(ctx)
	if err != nil {
		return // no buildx: nothing of ours to clean
	}
	for _, n := range names {
		if !strings.HasPrefix(n, builderPrefix) || n == keep {
			continue
		}
		if err := b.docker(ctx, nil, "buildx", "rm", "-f", n); err != nil {
			b.Log.Warn("remove old builder", "builder", n, "err", err)
			continue
		}
		b.Log.Info("removed old builder", "builder", n)
	}
}

// Maintain is the daily builder cleanup (run with the image prune; skipped while a build
// runs): old builders go; the current one goes too when no service here builds from
// source, otherwise its cache is trimmed and it is stopped again.
func (b *Builder) Maintain(ctx context.Context) {
	b.init()
	if !b.tryLock() {
		return
	}
	defer func() { <-b.slot }()
	keep := ""
	if b.Limits.Dedicated {
		keep = b.Limits.builderName()
	}
	b.removeOrphansLocked(ctx, keep)
	if keep == "" {
		return
	}
	if b.docker(ctx, nil, "buildx", "inspect", keep) != nil {
		return // not created (yet)
	}
	if b.HasSourceServices != nil && !b.HasSourceServices() {
		if err := b.docker(ctx, nil, "buildx", "rm", "-f", keep); err != nil {
			b.Log.Warn("remove unused builder", "builder", keep, "err", err)
			return
		}
		b.bstate.mu.Lock()
		b.bstate.name, b.bstate.stopped = "", false
		b.bstate.mu.Unlock()
		b.Log.Info("removed builder and its cache: no service on this server builds from source", "builder", keep)
		return
	}
	if gb := b.Limits.DailyCacheKeepGB; gb > 0 {
		if err := b.docker(ctx, nil, "buildx", "prune", "--builder", keep, "-f", "--keep-storage", strconv.Itoa(gb)+"gb"); err != nil {
			b.Log.Warn("daily build cache prune", "builder", keep, "err", err)
		}
	}
	// The builder exists (maybe from before an agent restart, maybe just booted by the
	// prune) and nothing is building: stop it until the next build.
	b.bstate.mu.Lock()
	b.bstate.name, b.bstate.stopped = keep, false
	b.bstate.mu.Unlock()
	b.stopIdleLocked(ctx, true)
}
