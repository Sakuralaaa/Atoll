package server

// Docker adoption (docs/adopt-docker.md): containers the user started on a worker with
// `docker run` or docker compose are taken over in place. The agent scans them (docker.scan);
// one group (a compose project or a standalone container) becomes one Atoll project. The
// rehearsal starts Atoll copies next to the old containers without published ports and with
// scratch data dirs; the cutover stops the old containers (restart policy "no") and starts the
// Atoll copies on the same host ports with the original bind mounts and named volumes mounted
// in place (Volume.HostPath); a failed cutover rolls back by itself. Old containers, their
// images and their volumes are never removed.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/zeabur/k8s"
)

const importKindDocker = "docker"

// adoptImageRepo: local tags of adopted images without a registry digest (never pulled).
const adoptImageRepo = "atoll-adopt/"

// Steps of a docker adoption: four for the rehearsal, three for the cutover, one rollback.
const (
	dstepCheck    = "Check containers and resources"
	dstepImages   = "Prepare images"
	dstepStart    = "Start Atoll copies (no ports, scratch data)"
	dstepVerify   = "Verify"
	dstepStopOld  = "Stop old containers"
	dstepLive     = "Start Atoll services on the old ports and data"
	dstepLiveOK   = "Verify after cutover"
	dstepRollback = "Roll back"
)

var dockerRehearsalSteps = []string{dstepCheck, dstepImages, dstepStart, dstepVerify}
var dockerCutoverSteps = []string{dstepStopOld, dstepLive, dstepLiveOK}

// dockerMeta is the (encrypted) state of a docker adoption.
type dockerMeta struct {
	Group    string          `json:"group"`
	Services []dockerSvcMeta `json:"services"`
	// Stopped lists the old containers a cutover stopped, with the restart policy each had
	// and whether it was running (Start): what a rollback restores. Entries leave the list
	// once a rollback restored them.
	Stopped   []api.DockerContainerCtl `json:"stopped,omitempty"`
	Cutovers  int                      `json:"cutovers"`
	LastError string                   `json:"last_error,omitempty"`
	UpdatedAt time.Time                `json:"updated_at"`
}

type dockerSvcMeta struct {
	ContainerID string          `json:"container_id"`
	Container   string          `json:"container"` // old container name
	Name        string          `json:"name"`      // atoll service name
	AtollID     string          `json:"atoll_id"`
	ImageID     string          `json:"image_id"`
	Pinned      string          `json:"pinned,omitempty"`
	Ports       []api.Port      `json:"ports"`   // live shape: published like the old container
	Volumes     []api.Volume    `json:"volumes"` // live shape: host paths in place
	FileMounts  map[string]bool `json:"file_mounts,omitempty"`
}

func dockerInventoryKey(serverID string) string { return "docker_inventory:" + serverID }

// ---------- GET /servers/{id}/docker ----------

// scanDocker asks the agent for a fresh inventory (cached encrypted), falling back to the
// cached copy when fresh is false or the agent is offline.
func (s *Server) scanDocker(ctx context.Context, serverID string, fresh bool) (*api.DockerInventory, error) {
	var inv api.DockerInventory
	if !fresh || !s.hub.Online(serverID) {
		if err := s.st.GetSecretBlob(ctx, dockerInventoryKey(serverID), &inv); err == nil {
			return &inv, nil
		}
		if !s.hub.Online(serverID) {
			return nil, errf(409, "server_offline", "%v; no earlier scan is cached", ErrNodeOffline)
		}
	}
	out, err := s.hub.Call(ctx, serverID, api.MsgDockerScan, nil, 3*time.Minute)
	if err != nil {
		if strings.Contains(err.Error(), "unsupported message type") {
			return nil, errf(409, "agent_outdated", "this server's atoll-agent is too old for Docker adoption; upgrade it first")
		}
		return nil, errf(502, "agent_error", "%v", err)
	}
	if err := json.Unmarshal([]byte(out), &inv); err != nil {
		return nil, errf(502, "agent_error", "invalid docker.scan result: %v", err)
	}
	if inv.Groups == nil {
		inv.Groups = []api.DockerGroup{}
	}
	if err := s.st.SetSecretBlob(ctx, dockerInventoryKey(serverID), inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

// maskDockerInventory hides env values (secrets become a fingerprint).
func maskDockerInventory(inv *api.DockerInventory) *api.DockerInventory {
	c := *inv
	c.Groups = make([]api.DockerGroup, len(inv.Groups))
	for i, g := range inv.Groups {
		g.Containers = append([]api.DockerContainer(nil), g.Containers...)
		for j, ct := range g.Containers {
			env := make([]api.EnvVar, len(ct.Env))
			for k, e := range ct.Env {
				e.Value = k8s.Mask(e.Key, e.Value)
				env[k] = e
			}
			ct.Env = env
			g.Containers[j] = ct
		}
		c.Groups[i] = g
	}
	return &c
}

func (s *Server) handleDockerScan(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if _, err := s.st.GetNode(r.Context(), id); err != nil {
		return err
	}
	inv, err := s.scanDocker(r.Context(), id, r.URL.Query().Get("cached") != "1")
	if err != nil {
		return err
	}
	writeJSON(w, 200, maskDockerInventory(inv))
	return nil
}

func findGroup(inv *api.DockerInventory, key string) *api.DockerGroup {
	for i := range inv.Groups {
		if inv.Groups[i].Key == key {
			return &inv.Groups[i]
		}
	}
	return nil
}

func findContainer(inv *api.DockerInventory, name string) *api.DockerContainer {
	for i := range inv.Groups {
		for j := range inv.Groups[i].Containers {
			if inv.Groups[i].Containers[j].Name == name {
				return &inv.Groups[i].Containers[j]
			}
		}
	}
	return nil
}

// escapeRefs keeps a literal "${" of an adopted value from being expanded by Atoll's env
// resolver ("$${" is its escape).
func escapeRefs(s string) string { return strings.ReplaceAll(s, "${", "$${") }

func escapeList(l []string) []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = escapeRefs(s)
	}
	return out
}

// uniqueName returns base, or base-2, base-3… when taken.
func uniqueName(base string, taken map[string]bool) string {
	n := base
	for i := 2; taken[n]; i++ {
		suf := fmt.Sprintf("-%d", i)
		b := base
		if len(b)+len(suf) > 63 {
			b = strings.Trim(b[:63-len(suf)], "-")
		}
		n = b + suf
	}
	taken[n] = true
	return n
}

// dockerPorts maps a container's ports to Atoll ports (live shape: published like before).
func dockerPorts(c api.DockerContainer) []api.Port {
	out := []api.Port{}
	seen := map[string]bool{}
	for _, p := range c.Ports {
		proto := p.Protocol
		if proto != "udp" {
			proto = "tcp"
		}
		suf := ""
		if proto == "udp" {
			suf = "-udp"
		}
		name := fmt.Sprintf("c%d%s", p.ContainerPort, suf)
		if p.HostPort > 0 {
			name = fmt.Sprintf("p%d%s", p.HostPort, suf)
			if p.HostIP != "" {
				name = fmt.Sprintf("p%d%s-%s", p.HostPort, suf, sanitizeName(p.HostIP))
			}
		}
		name = uniqueName(name, seen)
		if len(name) > 31 {
			continue // cannot happen with real port numbers
		}
		out = append(out, api.Port{Name: name, ContainerPort: p.ContainerPort, Protocol: proto, PublicPort: p.HostPort, HostIP: p.HostIP})
	}
	return out
}

// dockerVolumes maps a container's mounts to Atoll volumes mounted in place.
func dockerVolumes(c api.DockerContainer) ([]api.Volume, map[string]bool) {
	out := []api.Volume{}
	files := map[string]bool{}
	seen := map[string]bool{}
	for _, m := range c.Mounts {
		base := m.Name
		if base == "" || len(base) == 64 { // anonymous volume: name it after its mount path
			base = path.Base(m.Destination)
		}
		name := uniqueName(sanitizeName(base), seen)
		out = append(out, api.Volume{Name: name, MountPath: m.Destination, HostPath: path.Clean(m.Source), ReadOnly: m.ReadOnly})
		if m.IsFile {
			files[name] = true
		}
	}
	return out, files
}

// rehearsalShape: no published ports; writable directory mounts replaced by the service's
// own (scratch) volume dirs, so the copy never writes to the live data while the old
// container runs. Read-only mounts and single files stay in place (configuration).
func (m dockerSvcMeta) rehearsalShape() ([]api.Port, []api.Volume) {
	ports := make([]api.Port, len(m.Ports))
	for i, p := range m.Ports {
		p.PublicPort, p.HostIP = 0, ""
		ports[i] = p
	}
	vols := make([]api.Volume, len(m.Volumes))
	for i, v := range m.Volumes {
		if !v.ReadOnly && !m.FileMounts[v.Name] {
			v.HostPath = ""
		}
		vols[i] = v
	}
	return ports, vols
}

// ---------- POST /imports/docker ----------

func (s *Server) handleDockerAdopt(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var req api.DockerAdoptRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.ServerID == "" || req.Group == "" {
		return badRequest("server_id and group are required")
	}
	node, err := s.st.GetNode(ctx, req.ServerID)
	if err != nil {
		return badRequest("unknown server_id %q", req.ServerID)
	}
	inv, err := s.scanDocker(ctx, req.ServerID, true)
	if err != nil {
		return err
	}
	g := findGroup(inv, req.Group)
	if g == nil {
		return badRequest("group %q not found on %s (scan with GET /servers/%s/docker)", req.Group, node.Name, node.ID)
	}
	if jobs, err := s.st.ListImports(ctx); err == nil {
		for _, j := range jobs {
			if j.Mode == importKindDocker && j.ServerID == req.ServerID && j.Group == req.Group &&
				j.Status != zsRolledBack && j.Status != zsFailed && j.Status != zsPlanned {
				return errf(409, "conflict", "import %s already adopted this group (status %s); roll it back first", j.ID, j.Status)
			}
		}
	}
	var selected []api.DockerContainer
	var plan api.ImportPlan
	if len(req.Containers) == 0 {
		for _, c := range g.Containers {
			if c.Risk == api.DockerRiskAuto {
				selected = append(selected, c)
			}
		}
	} else {
		byName := map[string]api.DockerContainer{}
		for _, c := range g.Containers {
			byName[c.Name] = c
		}
		for _, n := range req.Containers {
			c, ok := byName[n]
			if !ok {
				return badRequest("container %q is not in group %s", n, g.Key)
			}
			if c.Risk != api.DockerRiskAuto {
				return errf(400, "manual_required", "container %s cannot be adopted automatically: %s", n, strings.Join(c.RiskReasons, "; "))
			}
			selected = append(selected, c)
		}
	}
	if len(selected) == 0 {
		return errf(400, "manual_required", "group %s has no container that can be adopted automatically", g.Key)
	}
	picked := map[string]bool{}
	for _, c := range selected {
		picked[c.Name] = true
	}
	for _, c := range g.Containers {
		if !picked[c.Name] {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s is not adopted and keeps running as it is", c.Name))
		}
	}
	plan.Warnings = append(plan.Warnings, g.Warnings...)

	// validate everything before creating anything
	taken := map[string]bool{}
	type planned struct {
		spec api.Service
		env  []api.EnvVar
		meta dockerSvcMeta
	}
	var todo []planned
	for _, c := range selected {
		base := c.Service
		if base == "" {
			base = c.Name
		}
		name := uniqueName(sanitizeName(base), taken)
		img := c.PinnedImage
		if img == "" {
			img = c.Image
		}
		vols, files := dockerVolumes(c)
		m := dockerSvcMeta{ContainerID: c.ID, Container: c.Name, Name: name, ImageID: c.ImageID, Pinned: c.PinnedImage,
			Ports: dockerPorts(c), Volumes: vols, FileMounts: files}
		live := api.Service{Name: name, Kind: "image", Image: img, Command: escapeList(c.Command), Args: escapeList(c.Args), User: c.User,
			Ports: m.Ports, Volumes: m.Volumes, Health: c.Health, MemLimitMB: c.MemLimitMB, CPULimit: c.CPULimit}
		if name != base && len(base) <= 64 {
			live.DisplayName = base
		}
		if err := validateServiceSpec(&live); err != nil {
			return badRequest("container %s: %v", c.Name, err)
		}
		m.Ports, m.Volumes = live.Ports, live.Volumes
		var env []api.EnvVar
		var dropped []string
		for _, e := range c.Env {
			if !envKeyRe.MatchString(e.Key) || len(e.Key) > 256 || len(e.Value) > 64<<10 {
				dropped = append(dropped, e.Key)
				continue
			}
			env = append(env, api.EnvVar{Key: e.Key, Value: escapeRefs(e.Value)})
		}
		if len(dropped) > 0 {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: env variables Atoll cannot store were dropped: %s", c.Name, strings.Join(dropped, ", ")))
		}
		if err := validateEnv(env); err != nil {
			return badRequest("container %s: %v", c.Name, err)
		}
		for _, w := range c.Warnings {
			plan.Warnings = append(plan.Warnings, c.Name+": "+w)
		}
		rehearsal := live
		rehearsal.Ports, rehearsal.Volumes = m.rehearsalShape()
		todo = append(todo, planned{spec: rehearsal, env: env, meta: m})
		plan.Services = append(plan.Services, api.ImportService{SourceServiceID: c.ID, Name: name, Kind: "image", Image: img,
			Command: live.Command, Args: live.Args, User: live.User, Ports: m.Ports, Volumes: m.Volumes, Env: []api.EnvVar{}, Domains: []string{}})
	}
	if mem, _ := s.resourceDefaults(ctx); mem > 0 {
		for _, t := range todo {
			if t.spec.MemLimitMB == 0 {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("Adopted services without a memory limit get the default of %d MB (Settings); raise it per service before cutover if a service needs more", mem))
				break
			}
		}
	}
	name := strings.TrimSpace(req.ProjectName)
	if name == "" {
		name = g.Name
	}
	p, err := s.createProject(ctx, name, req.ServerID, fmt.Sprintf("Adopted from Docker %s %s on this server", g.Kind, g.Name), false)
	if err != nil {
		return err
	}
	meta := dockerMeta{Group: g.Key, UpdatedAt: time.Now().UTC()}
	for _, t := range todo {
		svc, err := s.createService(ctx, p, t.spec, t.env, false)
		if err != nil {
			s.execDeleteProject(ctx, p.ID, "system")
			return err
		}
		t.meta.AtollID, t.meta.Name = svc.ID, svc.Name
		meta.Services = append(meta.Services, t.meta)
	}
	plan.Source, plan.SourceProject, plan.ProjectName, plan.TargetServer = "docker", g.Key, name, req.ServerID
	job := &api.ImportJob{ID: newID("imp"), Plan: plan, ProjectID: p.ID, Status: zsPlanned, CreatedAt: time.Now().UTC(),
		Mode: importKindDocker, ServerID: req.ServerID, Group: g.Key}
	for _, n := range append(append([]string{}, dockerRehearsalSteps...), dockerCutoverSteps...) {
		job.Steps = append(job.Steps, api.ImportStep{Name: n, Status: "pending"})
	}
	s.saveImport(ctx, job, importKindDocker, meta)
	s.st.Audit(ctx, principalFrom(ctx).actor(), "import.docker", job.ID, map[string]string{"project": p.ID, "group": g.Key})
	writeJSON(w, 201, maskJob(job))
	return nil
}

// ---------- shared ----------

// importKind returns the kind of an import job (plan | zeabur-local | docker).
func (s *Server) importKind(ctx context.Context, id string) (string, error) {
	_, kind, err := s.st.GetImport(ctx, id, nil)
	return kind, err
}

func (s *Server) loadDockerJob(ctx context.Context, id string) (*api.ImportJob, *dockerMeta, error) {
	var meta dockerMeta
	j, kind, err := s.st.GetImport(ctx, id, &meta)
	if err != nil {
		return nil, nil, err
	}
	if kind != importKindDocker {
		return nil, nil, badRequest("import %s is not a Docker adoption", id)
	}
	return j, &meta, nil
}

// drun is one background operation on a docker adoption.
type drun struct {
	s    *Server
	ctx  context.Context
	job  *api.ImportJob
	meta *dockerMeta
	node string
	st   *stepper
}

func (d *drun) save() {
	d.meta.UpdatedAt = time.Now().UTC()
	d.s.saveImport(d.ctx, d.job, importKindDocker, *d.meta)
}

func (d *drun) setStatus(st string) {
	d.job.Status = st
	d.save()
}

func (d *drun) resetSteps(names []string) {
	for _, n := range names {
		i := d.st.idx(n)
		d.job.Steps[i].Status, d.job.Steps[i].Detail = "pending", ""
	}
	d.save()
}

func (s *Server) startDockerOp(ctx context.Context, id string, allowed []string, busy string, fn func(d *drun) error) (*api.ImportJob, error) {
	j, meta, err := s.loadDockerJob(ctx, id)
	if err != nil {
		return nil, err
	}
	ok := false
	for _, a := range allowed {
		ok = ok || j.Status == a
	}
	if !ok {
		return nil, errf(409, "bad_state", "import is %s; this step needs status %s", j.Status, strings.Join(allowed, " or "))
	}
	if !s.hub.Online(j.ServerID) {
		return nil, errf(409, "server_offline", "%v", ErrNodeOffline)
	}
	if !s.lockJob(j.ID) {
		return nil, errf(409, "busy", "import is busy")
	}
	if !s.lockJob(j.ProjectID) {
		s.unlockJob(j.ID)
		return nil, errf(409, "busy", "another operation is running on this project")
	}
	d := &drun{s: s, ctx: s.bg, job: j, meta: meta, node: j.ServerID}
	d.st = &stepper{steps: &j.Steps, save: d.save}
	d.setStatus(busy)
	snapshot := maskJob(j)
	s.goBG(func(bg context.Context) {
		defer s.unlockJob(j.ID)
		defer s.unlockJob(j.ProjectID)
		if err := fn(d); err != nil {
			d.meta.LastError = err.Error()
			d.save()
			s.log.Warn("docker adoption step failed", "job", j.ID, "err", err)
		}
	})
	return snapshot, nil
}

// setShape writes the rehearsal (live=false) or live shape of one service's ports and volumes.
func (d *drun) setShape(m dockerSvcMeta, live bool) error {
	svc, _, err := d.s.st.GetService(d.ctx, m.AtollID)
	if err != nil {
		return fmt.Errorf("%s: %w", m.Name, err)
	}
	if live {
		svc.Ports, svc.Volumes = m.Ports, m.Volumes
	} else {
		svc.Ports, svc.Volumes = m.rehearsalShape()
	}
	return d.s.st.UpdateServiceSpec(d.ctx, *svc)
}

func (d *drun) serviceIDs() []string {
	ids := make([]string, len(d.meta.Services))
	for i, m := range d.meta.Services {
		ids[i] = m.AtollID
	}
	return ids
}

func (d *drun) deployAll() error {
	var ids []string
	for _, m := range d.meta.Services {
		dep, err := d.s.deploy(d.ctx, m.AtollID, "import")
		if err != nil {
			return fmt.Errorf("%s: %w", m.Name, err)
		}
		ids = append(ids, dep.ID)
	}
	return d.s.waitDeployments(d.ctx, ids, d.s.cfg.DeployWaitTimeout)
}

// stopAtoll stops the job's Atoll services (desired state stopped).
func (d *drun) stopAtoll() []string {
	s, ctx := d.s, d.ctx
	var errs []string
	for _, m := range d.meta.Services {
		s.st.SetDesiredStopped(ctx, m.AtollID, true)
		if _, err := s.hub.Call(ctx, d.node, api.MsgAction, api.ActionPayload{ServiceID: m.AtollID, Action: "stop"}, 2*s.cfg.RequestTimeout); err != nil {
			if !strings.Contains(err.Error(), "not found") {
				errs = append(errs, "stop "+m.Name+": "+err.Error())
			}
			continue
		}
		s.st.SetServiceStatus(ctx, m.AtollID, "stopped", "0/1")
		s.publishService(ctx, m.AtollID)
	}
	return errs
}

// refresh re-reads the old containers: compose may have recreated one (new ID) since the
// import. A container that is gone fails the step.
func (d *drun) refresh() (*api.DockerInventory, []string, error) {
	inv, err := d.s.scanDocker(d.ctx, d.node, true)
	if err != nil {
		return nil, nil, err
	}
	var notes []string
	for i := range d.meta.Services {
		m := &d.meta.Services[i]
		c := findContainer(inv, m.Container)
		if c == nil {
			return nil, nil, fmt.Errorf("container %s is gone from the server", m.Container)
		}
		if c.ID != m.ContainerID {
			notes = append(notes, m.Container+" was recreated")
			m.ContainerID = c.ID
		}
		if c.ImageID != m.ImageID {
			notes = append(notes, m.Container+" runs a new image now")
			m.ImageID, m.Pinned = c.ImageID, c.PinnedImage
		}
	}
	d.save()
	return inv, notes, nil
}

// ---------- rehearse ----------

// estimateDockerMB: memory the Atoll copies need next to the old containers (each service not
// already running in Atoll counts its old container's current use, or 128 MB).
func estimateDockerMB(meta *dockerMeta, inv *api.DockerInventory, running func(string) bool) (int, []string) {
	total := 0
	var parts []string
	for _, m := range meta.Services {
		if running(m.AtollID) {
			continue
		}
		mb, how := 0, "container usage"
		if c := findContainer(inv, m.Container); c != nil {
			mb = c.MemUsageMB
		}
		if mb <= 0 {
			mb, how = rehearseMemDefaultAppMB, "default"
		}
		total += mb
		parts = append(parts, fmt.Sprintf("%s %d MB (%s)", m.Name, mb, how))
	}
	return total, parts
}

var dockerRehearseFrom = []string{zsPlanned, zsRehearsed, zsFailed, zsRolledBack}

func (s *Server) handleDockerRehearse(w http.ResponseWriter, r *http.Request, req api.ZeaburRehearseRequest) error {
	ctx := r.Context()
	id := r.PathValue("id")
	if !req.IgnoreMemory {
		j, meta, err := s.loadDockerJob(ctx, id)
		if err != nil {
			return err
		}
		ok := false
		for _, a := range dockerRehearseFrom {
			ok = ok || j.Status == a
		}
		if ok && s.hub.Online(j.ServerID) {
			inv, err := s.scanDocker(ctx, j.ServerID, true)
			if err != nil {
				return err
			}
			if inv.MemFreeMB > 0 {
				need, parts := estimateDockerMB(meta, inv, func(atollID string) bool {
					svc, stopped, err := s.st.GetService(ctx, atollID)
					return err == nil && !stopped && svc.Status == "running"
				})
				if need > inv.MemFreeMB {
					return errf(409, "insufficient_memory", "rehearsal needs about %d MB of memory (%s) but only %d MB is available on the server; the old containers keep running next to the Atoll copies. Free memory or pass ignore_memory to override", need, strings.Join(parts, ", "), inv.MemFreeMB)
				}
			}
		}
	}
	j, err := s.startDockerOp(ctx, id, dockerRehearseFrom, zsRehearsing, (*drun).rehearse)
	if err != nil {
		return err
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "import.rehearse", j.ID, map[string]any{"ignore_memory": req.IgnoreMemory})
	writeJSON(w, 200, j)
	return nil
}

func (d *drun) rehearse() error {
	d.resetSteps(dockerRehearsalSteps)
	if err := d.doRehearse(); err != nil {
		d.setStatus(zsFailed)
		return err
	}
	d.setStatus(zsRehearsed)
	return nil
}

func (d *drun) doRehearse() error {
	s, ctx := d.s, d.ctx
	if err := d.st.do(dstepCheck, func() (string, error) {
		inv, notes, err := d.refresh()
		if err != nil {
			return "", err
		}
		msg := fmt.Sprintf("%d container(s) found; %.1f GB disk free, %d MB memory free", len(d.meta.Services), inv.DiskFreeGB, inv.MemFreeMB)
		if len(notes) > 0 {
			msg += "; " + strings.Join(notes, ", ")
		}
		return msg, nil
	}); err != nil {
		return err
	}
	if err := d.st.do(dstepImages, func() (string, error) {
		pinned, local := 0, 0
		for _, m := range d.meta.Services {
			out, err := s.hub.Call(ctx, d.node, api.MsgDockerImage, api.DockerImagePayload{ServiceID: m.AtollID, ImageID: m.ImageID, Pinned: m.Pinned}, s.cfg.TransferTimeout)
			if err != nil {
				return "", fmt.Errorf("%s: %w", m.Name, err)
			}
			var res api.DockerImageResult
			if err := json.Unmarshal([]byte(out), &res); err != nil || res.Ref == "" {
				return "", fmt.Errorf("%s: agent returned no image reference", m.Name)
			}
			svc, _, err := s.st.GetService(ctx, m.AtollID)
			if err != nil {
				return "", err
			}
			if svc.Image != res.Ref {
				svc.Image = res.Ref
				if err := s.st.UpdateServiceSpec(ctx, *svc); err != nil {
					return "", err
				}
			}
			if res.Local {
				local++
			} else {
				pinned++
			}
		}
		return fmt.Sprintf("%d image(s) pinned to their registry digest, %d kept as local image(s)", pinned, local), nil
	}); err != nil {
		return err
	}
	since := time.Now()
	if err := d.st.do(dstepStart, func() (string, error) {
		for _, m := range d.meta.Services {
			if err := d.setShape(m, false); err != nil {
				return "", err
			}
		}
		if err := d.deployAll(); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d Atoll copy(ies) started without published ports; writable data dirs are scratch copies, the old containers keep serving", len(d.meta.Services)), nil
	}); err != nil {
		return err
	}
	return d.st.do(dstepVerify, func() (string, error) {
		if err := s.waitHealthy(ctx, d.serviceIDs(), since, s.cfg.VerifyTimeout); err != nil {
			return "", err
		}
		// the copies ran on scratch data: stop them again to free memory; the cutover starts
		// them with the real data and ports
		if errs := d.stopAtoll(); len(errs) > 0 {
			return "", fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return "all Atoll copies started and stayed healthy; stopped again until cutover", nil
	})
}

// ---------- cutover ----------

func (s *Server) handleDockerCutover(w http.ResponseWriter, r *http.Request) error {
	j, meta, err := s.loadDockerJob(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var names []string
	for _, m := range meta.Services {
		names = append(names, m.Container)
	}
	summary := fmt.Sprintf("Cut over Docker %s to Atoll: stop %s (kept, restart policy set to no) and start the Atoll services on the same ports and data", j.Group, strings.Join(names, ", "))
	return s.requireConfirm(w, r, "import.cutover", j.ID, summary, api.ZeaburCutoverRequest{}, func(ctx context.Context) (int, any, error) {
		out, err := s.startDockerCutover(ctx, j.ID)
		return 200, out, err
	})
}

func (s *Server) startDockerCutover(ctx context.Context, id string) (*api.ImportJob, error) {
	return s.startDockerOp(ctx, id, []string{zsRehearsed, zsRolledBack}, zsCuttingOver, (*drun).cutover)
}

func (d *drun) cutover() error {
	d.resetSteps(dockerCutoverSteps)
	err := d.doCutover()
	if err == nil {
		d.meta.Cutovers++
		d.meta.LastError = ""
		d.setStatus(zsLive)
		return nil
	}
	// a failed cutover rolls back by itself: the old containers serve again
	d.setStatus(zsRollingBack)
	if rerr := d.doRollback(); rerr != nil {
		d.setStatus(zsFailed)
		return fmt.Errorf("cutover failed: %v; rollback failed too: %v", err, rerr)
	}
	d.setStatus(zsRolledBack)
	return fmt.Errorf("cutover failed and was rolled back: %v", err)
}

func (d *drun) doCutover() error {
	s, ctx := d.s, d.ctx
	if err := d.st.do(dstepStopOld, func() (string, error) {
		inv, _, err := d.refresh()
		if err != nil {
			return "", err
		}
		// record what a rollback restores before touching anything; an entry left over from
		// an incomplete rollback keeps its original policy
		have := map[string]bool{}
		for _, c := range d.meta.Stopped {
			have[c.Name] = true
		}
		var ctl []api.DockerContainerCtl
		for _, m := range d.meta.Services {
			c := findContainer(inv, m.Container)
			if !have[m.Container] {
				d.meta.Stopped = append(d.meta.Stopped, api.DockerContainerCtl{ID: c.ID, Name: c.Name, RestartPolicy: c.RestartPolicy, Start: c.State == "running" || c.State == "restarting"})
			}
			ctl = append(ctl, api.DockerContainerCtl{ID: c.ID, Name: c.Name})
		}
		d.save()
		if _, err := s.hub.Call(ctx, d.node, api.MsgDockerContainers, api.DockerContainersPayload{Action: "stop", Containers: ctl}, 5*time.Minute); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d old container(s) stopped and kept (restart policy no)", len(ctl)), nil
	}); err != nil {
		return err
	}
	since := time.Now()
	if err := d.st.do(dstepLive, func() (string, error) {
		for _, m := range d.meta.Services {
			if err := d.setShape(m, true); err != nil {
				return "", err
			}
		}
		if err := d.deployAll(); err != nil {
			return "", err
		}
		ports, vols := 0, 0
		for _, m := range d.meta.Services {
			for _, p := range m.Ports {
				if p.PublicPort > 0 {
					ports++
				}
			}
			vols += len(m.Volumes)
		}
		return fmt.Sprintf("%d service(s) running: %d host port(s) published, %d mount(s) in place", len(d.meta.Services), ports, vols), nil
	}); err != nil {
		return err
	}
	return d.st.do(dstepLiveOK, func() (string, error) {
		if err := s.waitHealthy(ctx, d.serviceIDs(), since, s.cfg.VerifyTimeout); err != nil {
			return "", err
		}
		return "all services healthy on the old ports and data", nil
	})
}

// ---------- rollback ----------

func (s *Server) handleDockerRollback(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	j, err := s.startDockerOp(ctx, r.PathValue("id"), []string{zsLive, zsFailed, zsRehearsed}, zsRollingBack, func(d *drun) error {
		if err := d.doRollback(); err != nil {
			d.setStatus(zsFailed)
			return err
		}
		d.setStatus(zsRolledBack)
		return nil
	})
	if err != nil {
		return err
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "import.rollback", j.ID, nil)
	writeJSON(w, 200, j)
	return nil
}

// doRollback stops the Atoll services (back to the rehearsal shape, so they hold no host
// port) and starts the old containers again with their original restart policy.
func (d *drun) doRollback() error {
	return d.st.do(dstepRollback, func() (string, error) {
		var errs []string
		for _, m := range d.meta.Services {
			if err := d.setShape(m, false); err != nil {
				errs = append(errs, err.Error())
			}
		}
		errs = append(errs, d.stopAtoll()...)
		restored := 0
		if len(d.meta.Stopped) > 0 {
			out, err := d.s.hub.Call(d.ctx, d.node, api.MsgDockerContainers, api.DockerContainersPayload{Action: "start", Containers: d.meta.Stopped}, 5*time.Minute)
			if err != nil {
				errs = append(errs, err.Error())
			} else {
				var res api.DockerContainersResult
				json.Unmarshal([]byte(out), &res)
				restored = len(d.meta.Stopped)
				d.meta.Stopped = nil
			}
		}
		for _, n := range dockerCutoverSteps {
			i := d.st.idx(n)
			if d.job.Steps[i].Status == "done" || d.job.Steps[i].Status == "running" || d.job.Steps[i].Status == "failed" {
				d.job.Steps[i].Detail = strings.TrimSpace(d.job.Steps[i].Detail + " (rolled back)")
				if d.job.Steps[i].Status != "failed" {
					d.job.Steps[i].Status = "skipped"
				}
			}
		}
		d.save()
		if len(errs) > 0 {
			sort.Strings(errs)
			return "", fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return fmt.Sprintf("Atoll services stopped; %d old container(s) restored", restored), nil
	})
}
