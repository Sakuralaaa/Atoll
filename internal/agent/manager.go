package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// EmitFunc sends an agent→server event (deploy, log, status...). It must not block for long;
// when the server is offline the event may be queued or dropped.
type EmitFunc func(msgType string, payload any)

// Manager owns the containers of this node. All operations on one service are serialized.
type Manager struct {
	rt    Runtime
	paths Paths
	store *StateStore
	emit  EmitFunc
	log   *slog.Logger

	// SettleTime is how long a freshly started container must stay up to count as running.
	SettleTime time.Duration
	// HealthWait bounds how long apply waits for a healthcheck to pass before reporting.
	HealthWait  time.Duration
	StopTimeout time.Duration
	// OnChange is called after any container change (used to push a status frame).
	OnChange func()
	// OnRemoved is called after a service was removed from this node (build images cleanup).
	OnRemoved func(ctx context.Context, serviceID string)

	mu    sync.Mutex
	locks map[string]*sync.Mutex

	// netMu orders "persist a spec" against "remove a project network nobody uses": a spec
	// saved before the cleanup's check keeps the network; one saved after re-creates it.
	netMu sync.Mutex

	digestMu sync.Mutex
	digests  map[string]string // image id -> repo digest
}

func NewManager(rt Runtime, p Paths, emit EmitFunc, log *slog.Logger) *Manager {
	if emit == nil {
		emit = func(string, any) {}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		rt: rt, paths: p, store: NewStateStore(p), emit: emit, log: log,
		SettleTime: 5 * time.Second, HealthWait: 60 * time.Second, StopTimeout: 30 * time.Second,
		locks: map[string]*sync.Mutex{}, digests: map[string]string{},
	}
}

func (m *Manager) Store() *StateStore { return m.store }

func (m *Manager) lock(serviceID string) func() {
	m.mu.Lock()
	l, ok := m.locks[serviceID]
	if !ok {
		l = &sync.Mutex{}
		m.locks[serviceID] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (m *Manager) changed() {
	if m.OnChange != nil {
		m.OnChange()
	}
}

func (m *Manager) deployEvent(s api.ResolvedSpec, status, msg, digest string) {
	if s.DeploymentID == "" {
		return
	}
	m.emit(api.MsgDeploy, api.DeployEvent{
		DeploymentID: s.DeploymentID, ServiceID: s.ServiceID, Status: status, Message: msg, ImageDigest: digest,
	})
}

// Apply is an explicit deploy from the server: persist, always pull, recreate the container.
func (m *Manager) Apply(ctx context.Context, spec api.ResolvedSpec) error {
	defer m.lock(spec.ServiceID)()
	prev, prevErr := m.store.Load(spec.ServiceID)
	var prevPtr *api.ResolvedSpec
	if prevErr == nil {
		prevPtr = &prev
	}
	if err := m.saveSpec(spec); err != nil {
		m.deployEvent(spec, "failed", "persist state: "+err.Error(), "")
		return err
	}
	err := m.recreate(ctx, spec, true, prevPtr)
	m.changed()
	return err
}

// converge brings one service to its persisted spec without recreating when unchanged.
// Used on boot and for sync. Caller holds the service lock.
func (m *Manager) converge(ctx context.Context, spec api.ResolvedSpec) error {
	name := ContainerName(spec.ServiceID)
	info, err := m.rt.InspectContainer(ctx, name)
	switch {
	case err == nil && info.Labels[LabelSpecHash] == SpecHash(spec):
		m.cleanupOld(ctx, spec.ServiceID)
		cleanupNext(ctx, m, spec.ServiceID)
		if spec.Stopped && (info.State == "running" || info.State == "restarting") {
			return m.rt.StopContainer(ctx, name, m.StopTimeout)
		}
		if !spec.Stopped && info.State != "running" && info.State != "restarting" {
			// Ensure networks/dirs exist (e.g. after a host rebuild) before starting.
			if err := m.prepareHost(ctx, spec); err != nil {
				return err
			}
			return m.rt.StartContainer(ctx, name)
		}
		return nil
	case err != nil && !errors.Is(err, ErrNotFound):
		return err
	}
	return m.recreate(ctx, spec, false, nil)
}

func (m *Manager) prepareHost(ctx context.Context, spec api.ResolvedSpec) error {
	cs, err := BuildContainerSpec(m.paths, spec)
	if err != nil {
		return err
	}
	for _, n := range cs.Networks {
		if err := m.rt.EnsureNetwork(ctx, n.Name); err != nil {
			return fmt.Errorf("network %s: %w", n.Name, err)
		}
	}
	for _, v := range spec.Volumes {
		if v.HostPath != "" {
			// mounted in place: it must exist already, Atoll never creates it
			if _, err := os.Stat(v.HostPath); err != nil {
				return fmt.Errorf("volume %s: host path: %w", v.Name, err)
			}
			continue
		}
		if err := os.MkdirAll(m.paths.VolumeDir(spec.ServiceID, v.Name), 0o755); err != nil {
			return err
		}
	}
	return m.writeConfigs(spec)
}

func (m *Manager) writeConfigs(spec api.ResolvedSpec) error {
	for _, c := range spec.Configs {
		hp, err := configHostPath(m.paths, spec.ServiceID, c.Path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(hp), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(hp, []byte(c.Content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// pruneConfigs removes config files no longer in the spec.
func (m *Manager) pruneConfigs(spec api.ResolvedSpec) {
	keep := map[string]bool{}
	for _, c := range spec.Configs {
		if hp, err := configHostPath(m.paths, spec.ServiceID, c.Path); err == nil {
			keep[hp] = true
		}
	}
	root := m.paths.ConfigsDir(spec.ServiceID)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !keep[p] {
			_ = os.Remove(p)
		}
		return nil
	})
}

func oldName(serviceID string) string { return ContainerName(serviceID) + "-old" }

func (m *Manager) cleanupOld(ctx context.Context, serviceID string) {
	if err := m.rt.RemoveContainer(ctx, oldName(serviceID)); err != nil && !errors.Is(err, ErrNotFound) {
		m.log.Warn("remove old container", "service", serviceID, "err", err)
	}
}

// recreate pulls the image, then replaces the container. With volumes or host ports (or no
// running container) it stops the old one first (strategy recreate); otherwise it starts the
// new one next to it and switches over once it is healthy (strategy start-first, see
// startFirst). When the new container fails to create or start, exits or restart-loops within
// the settle time, or fails its healthcheck, the old container is kept or restored and, if
// given, prev is written back as state.
func (m *Manager) recreate(ctx context.Context, spec api.ResolvedSpec, explicit bool, prev *api.ResolvedSpec) (err error) {
	sid := spec.ServiceID
	name := ContainerName(sid)
	fail := func(msg string, e error) error {
		full := msg
		if e != nil {
			full = msg + ": " + e.Error()
		}
		m.log.Error("deploy failed", "service", sid, "deployment", spec.DeploymentID, "err", full)
		m.deployEvent(spec, "failed", full, "")
		return errors.New(full)
	}

	cs, err := BuildContainerSpec(m.paths, spec)
	if err != nil {
		return fail("invalid spec", err)
	}
	if !spec.LocalImage {
		m.deployEvent(spec, "pulling", "pulling "+spec.Image, "")
	}
	for _, n := range cs.Networks {
		if err := m.rt.EnsureNetwork(ctx, n.Name); err != nil {
			return fail("network "+n.Name, err)
		}
	}
	if spec.LocalImage {
		// Built on this server from source: there is nothing to pull.
		if _, ierr := m.rt.ImageInfo(ctx, spec.Image); ierr != nil {
			return fail("image "+spec.Image+" is not on this server (built images are kept for the last few builds only); redeploy to build it again", ierr)
		}
	} else if perr := m.rt.PullImage(ctx, spec.Image, spec.RegistryAuth); perr != nil {
		if explicit {
			return fail("pull "+spec.Image, perr)
		}
		// Offline boot/sync: fall back to the local copy if there is one.
		if _, ierr := m.rt.ImageInfo(ctx, spec.Image); ierr != nil {
			return fail("pull "+spec.Image, perr)
		}
		m.log.Warn("pull failed, using local image", "image", spec.Image, "err", perr)
	}
	img, err := m.rt.ImageInfo(ctx, spec.Image)
	if err != nil {
		return fail("inspect image", err)
	}
	digest := img.Digest
	if digest == "" {
		digest = img.ID
	}
	if err := m.prepareHost(ctx, spec); err != nil {
		return fail("prepare host", err)
	}

	cleanupNext(ctx, m, sid)
	old, ierr := m.rt.InspectContainer(ctx, name)
	if ierr != nil && !errors.Is(ierr, ErrNotFound) {
		return fail("inspect container", ierr)
	}
	hadOld := ierr == nil
	oldRunning := hadOld && (old.State == "running" || old.State == "restarting")
	strategy, why := deployStrategy(spec, cs, oldRunning)
	note := strategyNote(strategy, why)
	m.log.Info("deploy strategy", "service", sid, "deployment", spec.DeploymentID, "strategy", strategy, "reason", why)
	m.deployEvent(spec, "starting", note, digest)
	if strategy == StrategyStartFirst {
		return m.startFirst(ctx, spec, cs, digest, note, prev, fail)
	}

	// Recreate: volumes are single-writer (and host ports can be bound once), so the old
	// container is stopped before the new starts.
	m.cleanupOld(ctx, sid)
	if hadOld {
		if err := m.rt.RenameContainer(ctx, name, oldName(sid)); err != nil {
			return fail("rename old container", err)
		}
		if oldRunning {
			if err := m.rt.StopContainer(ctx, oldName(sid), m.StopTimeout); err != nil && !errors.Is(err, ErrNotFound) {
				m.log.Warn("stop old container", "service", sid, "err", err)
			}
		}
	}
	rollback := func() {
		_ = m.rt.RemoveContainer(context.Background(), name)
		if !hadOld {
			return
		}
		if err := m.rt.RenameContainer(context.Background(), oldName(sid), name); err != nil {
			m.log.Error("rollback rename", "service", sid, "err", err)
			return
		}
		if oldRunning {
			if err := m.rt.StartContainer(context.Background(), name); err != nil {
				m.log.Error("rollback start", "service", sid, "err", err)
			}
		}
		if prev != nil {
			_ = m.store.Save(*prev)
		}
	}

	if err := m.rt.CreateContainer(ctx, cs); err != nil {
		rollback()
		return fail("create container", err)
	}
	if spec.Stopped {
		m.cleanupOld(ctx, sid)
		m.pruneConfigs(spec)
		m.deployEvent(spec, "running", "created (desired state: stopped)", digest)
		return nil
	}
	if err := m.rt.StartContainer(ctx, name); err != nil {
		rollback()
		return fail("start container", err)
	}
	// The old container stays (stopped, renamed) until the new one has proven itself, so a
	// failing healthcheck still rolls back to it.
	healthMsg, bad := m.proveNew(ctx, name, cs)
	if bad != "" {
		rollback()
		return fail(bad, nil)
	}
	m.cleanupOld(ctx, sid)
	m.pruneConfigs(spec)
	m.log.Info("deployed", "service", sid, "deployment", spec.DeploymentID, "image", spec.Image, "digest", digest, "strategy", strategy)
	m.deployEvent(spec, "running", joinMsg(note, healthMsg), digest)
	return nil
}

// Deploy strategies (named in the deploy events and agent log).
const (
	// StrategyRecreate stops the old container before the new one starts (brief downtime).
	StrategyRecreate = "recreate"
	// StrategyStartFirst starts the new container next to the old one and switches traffic
	// once it is healthy (no downtime).
	StrategyStartFirst = "start-first"
)

// deployStrategy picks how to replace a service's container. Start-first needs a running old
// container to keep serving, and nothing only one container can hold: no volumes (single
// writer) and no host-published ports (the new container could not bind them).
func deployStrategy(spec api.ResolvedSpec, cs ContainerSpec, oldRunning bool) (strategy, why string) {
	switch {
	case spec.Stopped:
		return StrategyRecreate, "desired state is stopped"
	case !oldRunning:
		return StrategyRecreate, "no running container to keep serving"
	case len(spec.Volumes) > 0:
		return StrategyRecreate, "has volumes (single writer)"
	case len(cs.Ports) > 0:
		return StrategyRecreate, "publishes host ports"
	}
	return StrategyStartFirst, "no volumes, no host ports"
}

func strategyNote(strategy, why string) string {
	if strategy == StrategyStartFirst {
		return "strategy: start-first (zero downtime; " + why + ")"
	}
	return "strategy: recreate (stop old first; " + why + ")"
}

func joinMsg(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// nextName is the temporary name of a start-first container until it takes over.
func nextName(serviceID string) string { return ContainerName(serviceID) + "-next" }

func cleanupNext(ctx context.Context, m *Manager, serviceID string) {
	if err := m.rt.RemoveContainer(ctx, nextName(serviceID)); err != nil && !errors.Is(err, ErrNotFound) {
		m.log.Warn("remove leftover next container", "service", serviceID, "err", err)
	}
}

// proveNew waits out the settle time and the healthcheck of a freshly started container.
// It returns a health note for the deploy event, or a failure message (with the container's
// last log lines) when it exited, restart-looped or failed its healthcheck.
func (m *Manager) proveNew(ctx context.Context, name string, cs ContainerSpec) (note, failMsg string) {
	if m.SettleTime > 0 {
		select {
		case <-time.After(m.SettleTime):
		case <-ctx.Done():
		}
	}
	// withLogs adds the new container's last log lines, read before it is removed.
	withLogs := func(msg string) string {
		if tail := m.tailLogs(ctx, name, 20); tail != "" {
			msg += "\n" + tail
		}
		return msg
	}
	check := func() string {
		info, err := m.rt.InspectContainer(ctx, name)
		if err != nil {
			return "inspect new container: " + err.Error()
		}
		switch {
		case info.State == "exited" || info.State == "dead":
			return withLogs(fmt.Sprintf("container exited with code %d", info.ExitCode))
		case info.State == "restarting" || info.RestartCount > 0:
			// up after the settle time only because the restart policy brought it back
			return withLogs(fmt.Sprintf("container keeps restarting (%d restarts, last exit code %d)", info.RestartCount, info.ExitCode))
		}
		return ""
	}
	if bad := check(); bad != "" {
		return "", bad
	}
	if cs.Health == nil {
		return "", ""
	}
	switch h := m.waitHealthy(ctx, name); h {
	case "unhealthy":
		return "", withLogs("healthcheck failing")
	case "healthy":
	default:
		note = "health: " + h
	}
	// A container that crashed and came back while the healthcheck was pending is not proven.
	if bad := check(); bad != "" {
		return "", bad
	}
	return note, ""
}

// startFirst replaces a running container without downtime. The new container is created as
// <name>-next on the project network only (it can reach its dependencies, and shares the
// service's DNS alias there) and proven: settle time, no restarts, healthcheck. Only then is
// it joined to atoll-ingress with the network alias "atoll-<service>", the name Caddy's routes
// dial, so the name resolves to both containers and new connections that the old one refuses
// fall through to the new one. The old container is then stopped gracefully and removed, and
// the new one renamed to the service's container name (the alias keeps the name resolvable
// throughout). Any failure before the old container is stopped removes the new one and leaves
// the old one serving untouched.
func (m *Manager) startFirst(ctx context.Context, spec api.ResolvedSpec, cs ContainerSpec, digest, note string,
	prev *api.ResolvedSpec, fail func(string, error) error) error {
	sid := spec.ServiceID
	name := ContainerName(sid)
	next := nextName(sid)
	m.cleanupOld(ctx, sid)

	staged := cs
	staged.Name = next
	staged.Networks = nil
	var late []NetworkAttach // joined after the new container is proven
	for i, n := range cs.Networks {
		if i > 0 && n.Name == IngressNetwork {
			late = append(late, NetworkAttach{Name: n.Name, Aliases: appendUnique(n.Aliases, name)})
			continue
		}
		staged.Networks = append(staged.Networks, n)
	}
	abort := func() {
		if err := m.rt.RemoveContainer(context.Background(), next); err != nil && !errors.Is(err, ErrNotFound) {
			m.log.Error("start-first: remove new container", "service", sid, "err", err)
		}
		if prev != nil {
			_ = m.store.Save(*prev)
		}
	}
	if err := m.rt.CreateContainer(ctx, staged); err != nil {
		abort()
		return fail("create container", err)
	}
	if err := m.rt.StartContainer(ctx, next); err != nil {
		abort()
		return fail("start container", err)
	}
	healthMsg, bad := m.proveNew(ctx, next, cs)
	if bad != "" {
		abort()
		return fail(bad+" (the previous container kept serving)", nil)
	}
	// Switch: the new container becomes reachable under the name the routes dial.
	for _, n := range late {
		if err := m.rt.ConnectNetwork(ctx, next, n.Name, n.Aliases); err != nil {
			abort()
			return fail("connect new container to "+n.Name, err)
		}
	}
	// Drain the old one: a graceful stop lets in-flight requests finish while new
	// connections already have the new container to go to.
	if err := m.rt.StopContainer(ctx, name, m.StopTimeout); err != nil && !errors.Is(err, ErrNotFound) {
		m.log.Warn("start-first: stop old container", "service", sid, "err", err)
	}
	if err := m.rt.RemoveContainer(ctx, name); err != nil && !errors.Is(err, ErrNotFound) {
		// Keep both: rename the old one out of the way so the new one can take the name.
		m.log.Warn("start-first: remove old container", "service", sid, "err", err)
		if rerr := m.rt.RenameContainer(ctx, name, oldName(sid)); rerr != nil {
			m.log.Error("start-first: rename old container", "service", sid, "err", rerr)
		}
	}
	if err := m.rt.RenameContainer(ctx, next, name); err != nil {
		// The new container serves traffic (through its alias) but under its temporary name;
		// the next converge recreates it under the right one.
		m.log.Error("start-first: rename new container", "service", sid, "err", err)
		m.deployEvent(spec, "failed", "rename new container: "+err.Error(), "")
		return fmt.Errorf("rename new container: %w", err)
	}
	m.cleanupOld(ctx, sid)
	m.pruneConfigs(spec)
	m.log.Info("deployed", "service", sid, "deployment", spec.DeploymentID, "image", spec.Image, "digest", digest, "strategy", StrategyStartFirst)
	m.deployEvent(spec, "running", joinMsg(note, healthMsg), digest)
	return nil
}

func appendUnique(list []string, v string) []string {
	out := append([]string(nil), list...)
	for _, x := range out {
		if x == v {
			return out
		}
	}
	return append(out, v)
}

func (m *Manager) waitHealthy(ctx context.Context, name string) string {
	deadline := time.Now().Add(m.HealthWait)
	for {
		info, err := m.rt.InspectContainer(ctx, name)
		if err != nil {
			return "unknown"
		}
		if info.Health == "healthy" || info.Health == "unhealthy" || info.Health == "" || time.Now().After(deadline) {
			if info.Health == "" {
				return "healthy"
			}
			return info.Health
		}
		select {
		case <-ctx.Done():
			return info.Health
		case <-time.After(2 * time.Second):
		}
	}
}

func (m *Manager) tailLogs(ctx context.Context, name string, n int) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var lines []string
	_ = m.rt.Logs(ctx, name, n, false, func(r LogRecord) { lines = append(lines, r.Line) })
	return strings.Join(lines, "\n")
}

// Remove deletes a service's container, state and configs; volumes too unless keepVolumes.
func (m *Manager) Remove(ctx context.Context, serviceID string, keepVolumes bool) error {
	if err := validID(serviceID); err != nil {
		return err
	}
	defer m.lock(serviceID)()
	defer m.changed()
	return m.removeLocked(ctx, serviceID, keepVolumes)
}

func (m *Manager) removeLocked(ctx context.Context, serviceID string, keepVolumes bool) error {
	// The project network to drop once this was its last service: from the persisted spec,
	// or the container's project label when only the container is left.
	netName := ""
	if spec, err := m.store.Load(serviceID); err == nil {
		netName = projectNetwork(spec)
	} else if info, err := m.rt.InspectContainer(ctx, ContainerName(serviceID)); err == nil && info.Labels[LabelProject] != "" {
		netName = "atoll-" + info.Labels[LabelProject]
	}
	for _, n := range []string{ContainerName(serviceID), oldName(serviceID), nextName(serviceID)} {
		if err := m.rt.StopContainer(ctx, n, m.StopTimeout); err != nil && !errors.Is(err, ErrNotFound) {
			m.log.Warn("stop before remove", "container", n, "err", err)
		}
		if err := m.rt.RemoveContainer(ctx, n); err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("remove %s: %w", n, err)
		}
	}
	if err := m.store.Delete(serviceID); err != nil {
		return err
	}
	_ = os.RemoveAll(m.paths.ConfigsDir(serviceID))
	if !keepVolumes {
		if err := os.RemoveAll(m.paths.VolumesDir(serviceID)); err != nil {
			return err
		}
	}
	m.log.Info("removed service", "service", serviceID, "keep_volumes", keepVolumes)
	m.PruneProjectNetwork(ctx, netName)
	if m.OnRemoved != nil {
		m.OnRemoved(ctx, serviceID)
	}
	return nil
}

// saveSpec persists a desired spec (see netMu).
func (m *Manager) saveSpec(spec api.ResolvedSpec) error {
	m.netMu.Lock()
	defer m.netMu.Unlock()
	return m.store.Save(spec)
}

// PruneProjectNetwork removes a project's docker network when no persisted service on this
// node uses it any more (the project's last service here was removed, e.g. project delete;
// or the mesh dropped the project's last forwarder).
// A network that still has containers attached (a mesh proxy, a container started outside
// atoll) is left alone; failures are logged, never fatal to the removal.
func (m *Manager) PruneProjectNetwork(ctx context.Context, name string) {
	if name == "" || name == IngressNetwork || !strings.HasPrefix(name, "atoll-") {
		return
	}
	m.netMu.Lock()
	defer m.netMu.Unlock()
	specs, _ := m.store.List()
	for _, s := range specs {
		if projectNetwork(s) == name {
			return
		}
	}
	switch err := m.rt.RemoveNetwork(ctx, name); {
	case err == nil:
		m.log.Info("removed project network", "network", name)
	case errors.Is(err, ErrNotFound):
	case errors.Is(err, ErrNetworkInUse):
		m.log.Info("project network still in use, kept", "network", name, "err", err)
	default:
		m.log.Warn("remove project network", "network", name, "err", err)
	}
}

// Action handles start/stop/restart and persists the desired Stopped flag.
func (m *Manager) Action(ctx context.Context, serviceID, action string) error {
	defer m.lock(serviceID)()
	defer m.changed()
	spec, err := m.store.Load(serviceID)
	if err != nil {
		return fmt.Errorf("service %s: %w", serviceID, err)
	}
	name := ContainerName(serviceID)
	switch action {
	case "stop":
		spec.Stopped = true
		if err := m.store.Save(spec); err != nil {
			return err
		}
		if err := m.rt.StopContainer(ctx, name, m.StopTimeout); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		return nil
	case "start", "restart":
		spec.Stopped = false
		if err := m.store.Save(spec); err != nil {
			return err
		}
		info, err := m.rt.InspectContainer(ctx, name)
		if errors.Is(err, ErrNotFound) || (err == nil && info.Labels[LabelSpecHash] != SpecHash(spec)) {
			return m.converge(ctx, spec)
		}
		if err != nil {
			return err
		}
		if action == "restart" && info.State == "running" {
			return m.rt.RestartContainer(ctx, name, m.StopTimeout)
		}
		return m.rt.StartContainer(ctx, name)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

// Sync makes this node match the full desired state: converge every spec and remove atoll
// containers (and state) for services not in the set. Volumes are always kept.
func (m *Manager) Sync(ctx context.Context, specs []api.ResolvedSpec) error {
	defer m.changed()
	want := map[string]bool{}
	var errs []error
	for _, s := range specs {
		want[s.ServiceID] = true
		unlock := m.lock(s.ServiceID)
		err := m.saveSpec(s)
		if err == nil {
			err = m.converge(ctx, s)
		}
		unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.ServiceID, err))
		}
	}
	stale := map[string]bool{}
	containers, err := m.rt.ListContainers(ctx, LabelService)
	if err != nil {
		errs = append(errs, err)
	}
	for _, c := range containers {
		if sid := c.Labels[LabelService]; sid != "" && !want[sid] {
			stale[sid] = true
		}
	}
	stored, _ := m.store.List()
	for _, s := range stored {
		if !want[s.ServiceID] {
			stale[s.ServiceID] = true
		}
	}
	for sid := range stale {
		if validID(sid) != nil {
			continue
		}
		unlock := m.lock(sid)
		if err := m.removeLocked(ctx, sid, true); err != nil {
			errs = append(errs, err)
		}
		unlock()
	}
	return errors.Join(errs...)
}

// Reconcile runs at boot with only local state: every persisted service is brought back to
// its spec. Unknown atoll containers are left alone (only the server's sync removes them).
func (m *Manager) Reconcile(ctx context.Context) error {
	defer m.changed()
	specs, errs := m.store.List()
	for _, s := range specs {
		unlock := m.lock(s.ServiceID)
		if err := m.converge(ctx, s); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.ServiceID, err))
		}
		unlock()
	}
	m.log.Info("boot reconcile done", "services", len(specs), "errors", len(errs))
	return errors.Join(errs...)
}

func (m *Manager) digestOf(ctx context.Context, imageID string) string {
	m.digestMu.Lock()
	d, ok := m.digests[imageID]
	m.digestMu.Unlock()
	if ok {
		return d
	}
	info, err := m.rt.ImageInfo(ctx, imageID)
	if err != nil {
		return ""
	}
	d = info.Digest
	if d == "" {
		d = info.ID
	}
	m.digestMu.Lock()
	m.digests[imageID] = d
	m.digestMu.Unlock()
	return d
}

// Status reports every atoll service container, plus "missing" for persisted services
// without a container.
func (m *Manager) Status(ctx context.Context) ([]api.ContainerState, error) {
	containers, err := m.rt.ListContainers(ctx, LabelService)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []api.ContainerState
	for _, c := range containers {
		sid := c.Labels[LabelService]
		if sid == "" || c.Name != ContainerName(sid) {
			continue // skip "-old" leftovers
		}
		seen[sid] = true
		out = append(out, api.ContainerState{
			ServiceID: sid, DeploymentID: c.Labels[LabelDeployment], State: c.State, Health: c.Health,
			ImageDigest: m.digestOf(ctx, c.ImageID), RestartCount: c.RestartCount, StartedAt: c.StartedAt,
		})
	}
	specs, _ := m.store.List()
	for _, s := range specs {
		if !seen[s.ServiceID] {
			out = append(out, api.ContainerState{ServiceID: s.ServiceID, DeploymentID: s.DeploymentID, State: "missing"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServiceID < out[j].ServiceID })
	return out, nil
}
