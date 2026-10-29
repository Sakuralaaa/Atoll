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

	"github.com/atoll-sh/atoll/internal/api"
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

	mu    sync.Mutex
	locks map[string]*sync.Mutex

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
	if err := m.store.Save(spec); err != nil {
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
		if err := os.MkdirAll(m.paths.VolumeDir(spec.ServiceID, v.Name), 0o755); err != nil {
			return err
		}
	}
	return m.writeConfigs(spec)
}

func (m *Manager) writeConfigs(spec api.ResolvedSpec) error {
	keep := map[string]bool{}
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
		keep[hp] = true
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

// recreate pulls the image, then replaces the container (stop old → start new). On a failed
// create/start the old container is restored and, if given, prev is written back as state.
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
	m.deployEvent(spec, "pulling", "pulling "+spec.Image, "")
	for _, n := range cs.Networks {
		if err := m.rt.EnsureNetwork(ctx, n.Name); err != nil {
			return fail("network "+n.Name, err)
		}
	}
	if perr := m.rt.PullImage(ctx, spec.Image, spec.RegistryAuth); perr != nil {
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

	m.deployEvent(spec, "starting", "", digest)
	// Recreate: volumes are single-writer, so the old container is stopped before the new starts.
	m.cleanupOld(ctx, sid)
	old, ierr := m.rt.InspectContainer(ctx, name)
	hadOld := ierr == nil
	oldRunning := hadOld && (old.State == "running" || old.State == "restarting")
	if ierr != nil && !errors.Is(ierr, ErrNotFound) {
		return fail("inspect container", ierr)
	}
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
	if m.SettleTime > 0 {
		select {
		case <-time.After(m.SettleTime):
		case <-ctx.Done():
		}
	}
	info, err := m.rt.InspectContainer(ctx, name)
	if err != nil {
		rollback()
		return fail("inspect new container", err)
	}
	if info.State == "exited" || info.State == "dead" {
		msg := fmt.Sprintf("container exited with code %d", info.ExitCode)
		if tail := m.tailLogs(ctx, name, 20); tail != "" {
			msg += "\n" + tail
		}
		rollback()
		return fail(msg, nil)
	}
	m.cleanupOld(ctx, sid)
	m.pruneConfigs(spec)

	msg := ""
	if cs.Health != nil {
		h := m.waitHealthy(ctx, name)
		switch h {
		case "unhealthy":
			m.deployEvent(spec, "failed", "healthcheck failing", digest)
			return errors.New("healthcheck failing")
		case "healthy":
		default:
			msg = "health: " + h
		}
	}
	m.log.Info("deployed", "service", sid, "deployment", spec.DeploymentID, "image", spec.Image, "digest", digest)
	m.deployEvent(spec, "running", msg, digest)
	return nil
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
	for _, n := range []string{ContainerName(serviceID), oldName(serviceID)} {
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
	return nil
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
		err := m.store.Save(s)
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
