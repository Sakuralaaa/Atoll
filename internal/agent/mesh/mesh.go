// Package mesh runs the cross-server service mesh on a worker.
//
// Services of one project normally share a node and reach each other by service name on the
// docker network atoll-<project_id>. When a project spans servers the control server sends
// each node its MeshPayload:
//
//   - exports: local services consumed elsewhere. A forwarder container in the project network
//     publishes <tailnet_ip>:<host_port> (tailnet IP only, never the public interface) and
//     forwards to <service name>:<port>.
//   - imports: services that run on another server. A forwarder container joins the project
//     network with the service's DNS name as alias and forwards each port to the exporting
//     node's <tailnet_ip>:<host_port>.
//
// The payload is persisted under the agent data dir and reconciled at boot and periodically,
// so cross-server names keep working through control-server outages and worker reboots.
package mesh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/tailnet"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

const (
	// LabelMesh marks forwarder containers; its value is the spec hash.
	LabelMesh = "atoll.mesh"
	// LabelMeshService is the service a forwarder belongs to.
	LabelMeshService = "atoll.mesh.service"

	ExportPrefix = "atoll-mx-"
	ImportPrefix = "atoll-mi-"
)

// Mesh is an agent.Extension handling MsgMesh plus the local reconcile loop.
type Mesh struct {
	RT    agent.Runtime
	Paths agent.Paths
	Log   *slog.Logger

	// Image is the forwarder image ref. Empty: built from Executable on first use.
	Image      string
	Executable func() (string, error)
	// HasIP reports whether the tailnet IP is assigned locally (exports bind to it).
	HasIP    func(ip string) bool
	Interval time.Duration // periodic reconcile (default 30s)
	MemoryMB int           // forwarder memory limit (default 64)
	// PruneNetwork, when set, is offered a project network after the mesh removed a
	// forwarder from it and wants none there any more (agent: Manager.PruneProjectNetwork,
	// which keeps it while a service or container still uses it).
	PruneNetwork func(ctx context.Context, name string)

	mu      sync.Mutex // serializes reconcile and payload changes
	payload api.MeshPayload
	loaded  bool

	errMu   sync.Mutex
	lastErr string
}

func New(rt agent.Runtime, p agent.Paths, log *slog.Logger) *Mesh {
	if log == nil {
		log = slog.Default()
	}
	return &Mesh{RT: rt, Paths: p, Log: log, Executable: os.Executable, HasIP: tailnet.HasIP,
		Interval: 30 * time.Second, MemoryMB: 64}
}

func (m *Mesh) Handles(t string) bool { return t == api.MsgMesh }

// Handle stores the new mesh and reconciles. Output summarizes the forwarders.
func (m *Mesh) Handle(ctx context.Context, t string, raw json.RawMessage) (string, error) {
	var p api.MeshPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", fmt.Errorf("bad mesh payload: %w", err)
	}
	if err := Validate(p); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.save(p); err != nil {
		return "", err
	}
	m.payload, m.loaded = p, true
	err := m.reconcileLocked(ctx)
	out := fmt.Sprintf("%d export(s), %d import(s)", len(p.Exports), len(p.Imports))
	return out, err
}

// Err is the last reconcile problem ("" when healthy).
func (m *Mesh) Err() string {
	m.errMu.Lock()
	defer m.errMu.Unlock()
	return m.lastErr
}

func (m *Mesh) setErr(err error) {
	m.errMu.Lock()
	defer m.errMu.Unlock()
	if err == nil {
		m.lastErr = ""
	} else {
		m.lastErr = err.Error()
	}
}

// Current returns the persisted payload.
func (m *Mesh) Current() api.MeshPayload {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	return m.payload
}

func (m *Mesh) save(p api.MeshPayload) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path := m.Paths.MeshFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Mesh) loadLocked() {
	if m.loaded {
		return
	}
	b, err := os.ReadFile(m.Paths.MeshFile())
	if err == nil {
		var p api.MeshPayload
		if json.Unmarshal(b, &p) == nil && Validate(p) == nil {
			m.payload = p
		} else {
			m.Log.Warn("ignoring unreadable mesh state", "file", m.Paths.MeshFile())
		}
	}
	m.loaded = true
}

// Reconcile converges forwarder containers to the persisted payload.
func (m *Mesh) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	return m.reconcileLocked(ctx)
}

// Loop reconciles at start and every Interval (retries exports until the tailnet IP is up,
// restarts crashed forwarders). It never needs the control server.
func (m *Mesh) Loop(ctx context.Context) {
	iv := m.Interval
	if iv <= 0 {
		iv = 30 * time.Second
	}
	for {
		if err := m.Reconcile(ctx); err != nil && ctx.Err() == nil {
			m.Log.Warn("mesh reconcile", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
		}
	}
}

var (
	nameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,251}[a-z0-9])?$`)
	idRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// Validate rejects payloads that could publish outside the tailnet or escape naming.
func Validate(p api.MeshPayload) error {
	if len(p.Exports) > 0 && !tailnet.IsTailnetIP(p.TailnetIP) {
		return fmt.Errorf("mesh: exports need this node's tailnet IP (100.64.0.0/10), got %q", p.TailnetIP)
	}
	checkPorts := func(sid string, ports []api.MeshPort, export bool) error {
		if len(ports) == 0 {
			return fmt.Errorf("mesh: %s has no ports", sid)
		}
		for _, pt := range ports {
			if pt.Protocol != "tcp" && pt.Protocol != "udp" {
				return fmt.Errorf("mesh: %s port %d: protocol must be tcp or udp", sid, pt.Port)
			}
			if pt.Port < 1 || pt.Port > 65535 {
				return fmt.Errorf("mesh: %s: bad port %d", sid, pt.Port)
			}
			if export {
				if pt.HostPort < 1024 || pt.HostPort > 65535 {
					return fmt.Errorf("mesh: %s: bad host port %d", sid, pt.HostPort)
				}
				continue
			}
			host, port, ok := strings.Cut(pt.Target, ":")
			if !ok || !tailnet.IsTailnetIP(host) {
				return fmt.Errorf("mesh: %s: import target %q must be a tailnet address", sid, pt.Target)
			}
			if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("mesh: %s: bad target %q", sid, pt.Target)
			}
		}
		return nil
	}
	seen := map[string]bool{}
	for _, e := range p.Exports {
		if !idRe.MatchString(e.ServiceID) || !idRe.MatchString(e.ProjectID) || !nameRe.MatchString(e.Name) {
			return fmt.Errorf("mesh: invalid export %q/%q", e.ServiceID, e.Name)
		}
		if seen["x"+e.ServiceID] {
			return fmt.Errorf("mesh: duplicate export %s", e.ServiceID)
		}
		seen["x"+e.ServiceID] = true
		if err := checkPorts(e.ServiceID, e.Ports, true); err != nil {
			return err
		}
	}
	for _, i := range p.Imports {
		if !idRe.MatchString(i.ServiceID) || !idRe.MatchString(i.ProjectID) || !nameRe.MatchString(i.Name) {
			return fmt.Errorf("mesh: invalid import %q/%q", i.ServiceID, i.Name)
		}
		for _, a := range i.Aliases {
			if !nameRe.MatchString(a) {
				return fmt.Errorf("mesh: invalid alias %q", a)
			}
		}
		if seen["i"+i.ServiceID] {
			return fmt.Errorf("mesh: duplicate import %s", i.ServiceID)
		}
		seen["i"+i.ServiceID] = true
		if err := checkPorts(i.ServiceID, i.Ports, false); err != nil {
			return err
		}
	}
	return nil
}

func network(projectID, nw string) string {
	if nw != "" {
		return nw
	}
	return "atoll-" + projectID
}

// ExportSpec is the forwarder container of an export. Every published port is bound to the
// tailnet IP only.
func ExportSpec(image, tailnetIP string, e api.MeshExport, memMB int) agent.ContainerSpec {
	cs := agent.ContainerSpec{
		Name:          ExportPrefix + e.ServiceID,
		Image:         image,
		Entrypoint:    Entrypoint,
		Networks:      []agent.NetworkAttach{{Name: network(e.ProjectID, e.Network)}},
		RestartPolicy: "unless-stopped",
		MemoryBytes:   int64(memMB) << 20,
	}
	for _, p := range e.Ports {
		cs.Cmd = append(cs.Cmd, Rule{Proto: p.Protocol, Listen: fmt.Sprintf(":%d", p.HostPort),
			Target: fmt.Sprintf("%s:%d", e.Name, p.Port)}.String())
		cs.Ports = append(cs.Ports, agent.PortBinding{ContainerPort: p.HostPort, HostPort: p.HostPort, Protocol: p.Protocol, HostIP: tailnetIP})
	}
	cs.Labels = labels(e.ProjectID, e.ServiceID, cs)
	return cs
}

// ImportSpec is the forwarder container standing in for a remote service.
func ImportSpec(image string, i api.MeshImport, memMB int) agent.ContainerSpec {
	aliases := append([]string{i.Name}, i.Aliases...)
	cs := agent.ContainerSpec{
		Name:          ImportPrefix + i.ServiceID,
		Image:         image,
		Entrypoint:    Entrypoint,
		Networks:      []agent.NetworkAttach{{Name: network(i.ProjectID, i.Network), Aliases: aliases}},
		RestartPolicy: "unless-stopped",
		MemoryBytes:   int64(memMB) << 20,
	}
	for _, p := range i.Ports {
		cs.Cmd = append(cs.Cmd, Rule{Proto: p.Protocol, Listen: fmt.Sprintf(":%d", p.Port), Target: p.Target}.String())
		cs.ExposedPorts = append(cs.ExposedPorts, agent.PortBinding{ContainerPort: p.Port, Protocol: p.Protocol})
	}
	cs.Labels = labels(i.ProjectID, i.ServiceID, cs)
	return cs
}

func labels(projectID, serviceID string, cs agent.ContainerSpec) map[string]string {
	b, _ := json.Marshal(cs)
	sum := sha256.Sum256(b)
	return map[string]string{
		agent.LabelManaged: "true",
		agent.LabelRole:    "mesh",
		agent.LabelProject: projectID,
		LabelMeshService:   serviceID,
		LabelMesh:          hex.EncodeToString(sum[:8]),
	}
}

// ensureImage returns the forwarder image ref, building it from the agent binary when absent.
func (m *Mesh) ensureImage(ctx context.Context) (string, error) {
	if m.Image != "" {
		if _, err := m.RT.ImageInfo(ctx, m.Image); err == nil {
			return m.Image, nil
		}
	}
	exe, err := m.Executable()
	if err != nil {
		return "", fmt.Errorf("forwarder image: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	sum, err := FileHash(exe)
	if err != nil {
		return "", fmt.Errorf("forwarder image: %w", err)
	}
	ref := ImageRef(sum)
	if _, err := m.RT.ImageInfo(ctx, ref); err == nil {
		m.Image = ref
		return ref, nil
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(BuildImageArchive(pw, exe, ref, m.Paths.TmpDir())) }()
	if _, err := m.RT.LoadImage(ctx, pr); err != nil {
		pr.CloseWithError(err)
		return "", fmt.Errorf("load forwarder image: %w", err)
	}
	m.Log.Info("built forwarder image from the agent binary", "image", ref)
	m.Image = ref
	return ref, nil
}

func (m *Mesh) reconcileLocked(ctx context.Context) error {
	p := m.payload
	var errs []error
	want := map[string]agent.ContainerSpec{}
	if len(p.Exports)+len(p.Imports) > 0 {
		img, err := m.ensureImage(ctx)
		if err != nil {
			m.setErr(err)
			return err
		}
		ipUp := len(p.Exports) == 0 || m.HasIP == nil || m.HasIP(p.TailnetIP)
		for _, e := range p.Exports {
			if !ipUp {
				continue
			}
			cs := ExportSpec(img, p.TailnetIP, e, m.MemoryMB)
			want[cs.Name] = cs
		}
		if !ipUp {
			errs = append(errs, fmt.Errorf("tailnet IP %s is not up yet; %d export(s) wait (is tailscaled running?)", p.TailnetIP, len(p.Exports)))
		}
		for _, i := range p.Imports {
			cs := ImportSpec(img, i, m.MemoryMB)
			want[cs.Name] = cs
		}
	}
	existing, err := m.RT.ListContainers(ctx, LabelMesh)
	if err != nil {
		m.setErr(err)
		return err
	}
	have := map[string]agent.ContainerInfo{}
	dropped := map[string]bool{} // project networks a removed forwarder was on
	for _, c := range existing {
		have[c.Name] = c
		if _, ok := want[c.Name]; ok {
			continue
		}
		// Keep exporters while the tailnet IP is down: they come back by themselves.
		if strings.HasPrefix(c.Name, ExportPrefix) && exportWanted(p, c.Name) {
			continue
		}
		if err := m.RT.RemoveContainer(ctx, c.Name); err != nil && !errors.Is(err, agent.ErrNotFound) {
			errs = append(errs, fmt.Errorf("remove %s: %w", c.Name, err))
		} else {
			m.Log.Info("mesh forwarder removed", "container", c.Name)
			if pid := c.Labels[agent.LabelProject]; pid != "" {
				dropped["atoll-"+pid] = true
			}
		}
	}
	if m.PruneNetwork != nil {
		for _, cs := range want {
			for _, nw := range cs.Networks {
				delete(dropped, nw.Name)
			}
		}
		for nw := range dropped {
			m.PruneNetwork(ctx, nw)
		}
	}
	names := make([]string, 0, len(want))
	for n := range want {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		cs := want[n]
		if c, ok := have[n]; ok && c.Labels[LabelMesh] == cs.Labels[LabelMesh] {
			if c.State != "running" && c.State != "restarting" {
				if err := m.RT.StartContainer(ctx, n); err != nil {
					errs = append(errs, fmt.Errorf("start %s: %w", n, err))
				}
			}
			continue
		} else if ok {
			if err := m.RT.RemoveContainer(ctx, n); err != nil && !errors.Is(err, agent.ErrNotFound) {
				errs = append(errs, fmt.Errorf("replace %s: %w", n, err))
				continue
			}
		}
		for _, nw := range cs.Networks {
			if err := m.RT.EnsureNetwork(ctx, nw.Name); err != nil {
				errs = append(errs, fmt.Errorf("network %s: %w", nw.Name, err))
			}
		}
		if err := m.RT.CreateContainer(ctx, cs); err != nil {
			errs = append(errs, fmt.Errorf("create %s: %w", n, err))
			continue
		}
		if err := m.RT.StartContainer(ctx, n); err != nil {
			errs = append(errs, fmt.Errorf("start %s: %w", n, err))
			continue
		}
		m.Log.Info("mesh forwarder started", "container", n)
	}
	err = errors.Join(errs...)
	m.setErr(err)
	return err
}

func exportWanted(p api.MeshPayload, name string) bool {
	for _, e := range p.Exports {
		if ExportPrefix+e.ServiceID == name {
			return true
		}
	}
	return false
}
