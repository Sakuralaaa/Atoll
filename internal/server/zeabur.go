package server

// In-place Zeabur takeover (docs/api.md "In-place Zeabur takeover"): the agent runs on the
// user's own VPS that Zeabur manages with single-node k3s. The server scans it, creates an
// Atoll project mirroring the selected namespace, rehearses the move (images, volumes,
// databases, containers started without public ingress), cuts over (Zeabur scaled to 0,
// final sync, Zeabur ingress parked, atoll-caddy takes 80/443), rolls back on request or on
// failure, and finally — after explicit confirmation — removes Zeabur's k3s.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
	"github.com/Sakuralaaa/Atoll/internal/zeabur/k8s"
)

// Job statuses of a zeabur-local import.
const (
	zsPlanned     = "planned"
	zsRehearsing  = "rehearsing"
	zsRehearsed   = "rehearsed"
	zsCuttingOver = "cutting_over"
	zsLive        = "live"
	zsRollingBack = "rolling_back"
	zsRolledBack  = "rolled_back"
	zsCleaning    = "cleaning"
	zsCleaned     = "cleaned"
	zsFailed      = "failed"
)

// Step names: the first six are the rehearsal, the last four the cutover.
const (
	stepResources = "Check resources"
	stepImages    = "Import images"
	stepVolumes   = "Copy volumes"
	stepDatabases = "Copy databases"
	stepStart     = "Start services (no public ingress)"
	stepVerify    = "Verify"

	stepScaleDown = "Stop Zeabur services"
	stepFinalSync = "Final data sync"
	stepIngress   = "Switch ingress to Atoll"
	stepDNS       = "Configure DNS"
)

var rehearsalSteps = []string{stepResources, stepImages, stepVolumes, stepDatabases, stepStart, stepVerify}
var cutoverSteps = []string{stepScaleDown, stepFinalSync, stepIngress, stepDNS}

// dbKinds are the Atoll kinds the agent can dump/restore with zeabur.db.
var dbKinds = map[string]bool{"postgres": true, "mysql": true, "mongo": true, "redis": true}

// zeaburMeta is the (encrypted) state of a zeabur-local import.
type zeaburMeta struct {
	Namespace string          `json:"namespace"`
	Services  []zeaburSvcMeta `json:"services"`
	// DomainMap: old *.zeabur.app host -> new host (only hosts that got a replacement).
	DomainMap map[string]string `json:"domain_map,omitempty"`
	// Scaled lists workloads this job scaled down (namespace/workload), restored on rollback.
	Scaled []string `json:"scaled,omitempty"`
	// IngressParked is the cutover marker: Zeabur's ingress was parked successfully.
	IngressParked bool `json:"ingress_parked"`
	// Cutovers counts successful cutovers; cleanup requires at least one.
	Cutovers         int       `json:"cutovers"`
	RollbackDisabled bool      `json:"rollback_disabled"`
	LastError        string    `json:"last_error,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type zeaburSvcMeta struct {
	SourceServiceID string          `json:"source_service_id"`
	Name            string          `json:"name"` // atoll service name
	AtollID         string          `json:"atoll_id"`
	Workload        string          `json:"workload"`
	Kind            string          `json:"kind"`
	Image           string          `json:"image"` // running Zeabur image (what zeabur.image exports)
	ZeaburRegistry  bool            `json:"zeabur_registry"`
	Volumes         []zeaburVolMeta `json:"volumes,omitempty"`
}

type zeaburVolMeta struct {
	PVC     string `json:"pvc"`
	HostDir string `json:"host_dir"`
	Volume  string `json:"volume"` // atoll volume name
	Bytes   int64  `json:"bytes"`
}

func (m zeaburSvcMeta) isDB() bool { return dbKinds[m.Kind] }

// dbResult mirrors the agent's zeabur.db Output.
type dbResult struct {
	Kind       string           `json:"kind"`
	Databases  []string         `json:"databases,omitempty"`
	Bytes      int64            `json:"bytes"`
	Source     map[string]int64 `json:"source_counts,omitempty"`
	Target     map[string]int64 `json:"target_counts,omitempty"`
	Match      bool             `json:"match"`
	Mismatches []string         `json:"mismatches,omitempty"`
	Warnings   []string         `json:"warnings,omitempty"`
}

func inventoryKey(serverID string) string { return "zeabur_inventory:" + serverID }

// ---------- GET /servers/{id}/zeabur ----------

// scanZeabur asks the agent for a fresh inventory (cached encrypted), falling back to the
// cached copy when the agent is offline.
func (s *Server) scanZeabur(ctx context.Context, serverID string, fresh bool) (*api.ZeaburInventory, error) {
	var inv api.ZeaburInventory
	if !fresh || !s.hub.Online(serverID) {
		if err := s.st.GetSecretBlob(ctx, inventoryKey(serverID), &inv); err == nil {
			return &inv, nil
		}
		if !s.hub.Online(serverID) {
			return nil, errf(409, "server_offline", "%v; no earlier scan is cached", ErrNodeOffline)
		}
	}
	out, err := s.hub.Call(ctx, serverID, api.MsgZeaburScan, nil, 3*time.Minute)
	if err != nil {
		if strings.Contains(err.Error(), "k3s is not installed") {
			return nil, errf(404, "no_k3s", "no Zeabur (k3s) installation found on this server")
		}
		return nil, errf(502, "agent_error", "%v", err)
	}
	if err := json.Unmarshal([]byte(out), &inv); err != nil {
		return nil, errf(502, "agent_error", "invalid zeabur.scan result: %v", err)
	}
	if inv.Projects == nil {
		inv.Projects = []api.ZeaburProject{}
	}
	if err := s.st.SetSecretBlob(ctx, inventoryKey(serverID), inv); err != nil {
		return nil, err
	}
	return &inv, nil
}

// maskInventory hides env values: secrets become a fingerprint, long values are shortened.
func maskInventory(inv *api.ZeaburInventory) *api.ZeaburInventory {
	c := *inv
	c.Projects = make([]api.ZeaburProject, len(inv.Projects))
	for i, p := range inv.Projects {
		p.Services = append([]api.ZeaburService(nil), p.Services...)
		for j, sv := range p.Services {
			env := make([]api.EnvVar, len(sv.Env))
			for k, e := range sv.Env {
				e.Value = k8s.Mask(e.Key, e.Value)
				env[k] = e
			}
			sv.Env = env
			p.Services[j] = sv
		}
		c.Projects[i] = p
	}
	return &c
}

func (s *Server) handleZeaburScan(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if _, err := s.st.GetNode(r.Context(), id); err != nil {
		return err
	}
	inv, err := s.scanZeabur(r.Context(), id, r.URL.Query().Get("cached") != "1")
	if err != nil {
		return err
	}
	if !inv.K3s {
		return errf(404, "no_k3s", "no Zeabur (k3s) installation found on this server")
	}
	writeJSON(w, 200, maskInventory(inv))
	return nil
}

// ---------- POST /imports/zeabur-local ----------

func (s *Server) handleZeaburLocalImport(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var req api.ZeaburLocalImportRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.ServerID == "" || req.Namespace == "" {
		return badRequest("server_id and namespace are required")
	}
	node, err := s.st.GetNode(ctx, req.ServerID)
	if err != nil {
		return badRequest("unknown server_id %q", req.ServerID)
	}
	inv, err := s.scanZeabur(ctx, req.ServerID, false)
	if err != nil {
		return err
	}
	if !inv.K3s {
		return errf(404, "no_k3s", "no Zeabur (k3s) installation found on this server")
	}
	var proj *api.ZeaburProject
	for i := range inv.Projects {
		if inv.Projects[i].Namespace == req.Namespace {
			proj = &inv.Projects[i]
		}
	}
	if proj == nil {
		return badRequest("namespace %q not found on %s (rescan with GET /servers/%s/zeabur)", req.Namespace, node.Name, node.ID)
	}
	// refuse a second active takeover of the same namespace
	if jobs, err := s.st.ListImports(ctx); err == nil {
		for _, j := range jobs {
			if j.Mode == importKindZeabur && j.ServerID == req.ServerID && j.Namespace == req.Namespace &&
				j.Status != zsRolledBack && j.Status != zsFailed && j.Status != zsPlanned {
				return errf(409, "conflict", "import %s already took over this namespace (status %s); roll it back first", j.ID, j.Status)
			}
		}
	}
	selected := []api.ZeaburService{}
	if len(req.ServiceIDs) == 0 {
		selected = append(selected, proj.Services...)
	} else {
		byID := map[string]api.ZeaburService{}
		for _, sv := range proj.Services {
			byID[sv.ServiceID] = sv
		}
		for _, id := range req.ServiceIDs {
			sv, ok := byID[id]
			if !ok {
				return badRequest("service %q not found in namespace %s", id, req.Namespace)
			}
			selected = append(selected, sv)
		}
	}
	if len(selected) == 0 {
		return badRequest("namespace %s has no services", req.Namespace)
	}

	// *.zeabur.app hosts die with Zeabur: each needs a replacement, either given explicitly in
	// domain_map (old=new, or old= to drop it) or derived from base_domain. DNS is optional:
	// with a Cloudflare token the cutover creates the records, without one the user does.
	base, _ := s.st.GetSetting(ctx, "base_domain")
	req.DomainMap = lowerKeys(req.DomainMap)
	var unmapped []string
	for _, sv := range selected {
		for _, d := range sv.Domains {
			if k8s.IsGeneratedDomain(d) {
				if _, ok := req.DomainMap[strings.ToLower(d)]; ok {
					continue // explicitly mapped or dropped
				}
				unmapped = append(unmapped, d)
			}
		}
	}
	if len(unmapped) > 0 && base == "" {
		return errf(400, "settings_required", "这些服务使用 *.zeabur.app 域名，接管后会失效 (%s)：请在 设置 (Settings) 中填写根域名 (base_domain)，或用 domain_map / --domain old=new 逐个指定新域名（old= 表示丢弃）", strings.Join(dedupe(unmapped), ", "))
	}
	for o, n := range req.DomainMap {
		if n == "" {
			continue
		}
		h, err := normalizeHostname(n, base)
		if err != nil {
			return badRequest("domain_map %s: %v", o, err)
		}
		req.DomainMap[o] = h
	}

	name := strings.TrimSpace(req.ProjectName)
	if name == "" {
		name = proj.ProjectName
		if name == "" {
			name = req.Namespace
		}
	}
	plan := k8s.BuildPlan(selected, k8s.PlanOptions{
		SourceProject: proj.ProjectID, ProjectName: name, TargetServer: req.ServerID, ServerIPv4: node.PublicIPv4,
		BaseDomain: base, DomainMap: req.DomainMap, Namespace: req.Namespace,
	})
	plan.Source = "zeabur-local"
	if len(plan.Services) == 0 {
		return badRequest("no running service to take over (suspended services carry only volumes)")
	}
	bySrc := map[string]api.ZeaburService{}
	for _, sv := range selected {
		bySrc[sv.ServiceID] = sv
	}
	// drop leftover ZEABUR* platform vars and record rewrites by key only (never values)
	for i := range plan.Services {
		is := &plan.Services[i]
		orig := map[string]string{}
		for _, e := range bySrc[is.SourceServiceID].Env {
			orig[e.Key] = e.Value
		}
		var kept []api.EnvVar
		var dropped, rewritten []string
		for _, e := range is.Env {
			if strings.HasPrefix(e.Key, "ZEABUR") && !strings.HasPrefix(e.Value, "${ATOLL_") {
				dropped = append(dropped, e.Key)
				continue
			}
			if ov, ok := orig[e.Key]; ok && ov != e.Value {
				rewritten = append(rewritten, e.Key)
			}
			kept = append(kept, e)
		}
		for k := range orig {
			if strings.HasPrefix(k, "ZEABUR") && !hasKey(is.Env, k) {
				dropped = append(dropped, k)
			}
		}
		if kept == nil {
			kept = []api.EnvVar{}
		}
		is.Env = kept
		if len(dropped) > 0 {
			sort.Strings(dropped)
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: dropped Zeabur platform variables %s", is.Name, strings.Join(dedupe(dropped), ", ")))
		}
		if len(rewritten) > 0 {
			sort.Strings(rewritten)
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: rewrote hostnames in %s", is.Name, strings.Join(rewritten, ", ")))
		}
	}
	// domain map actually applied (old -> new)
	var hosts []string
	for _, sv := range selected {
		hosts = append(hosts, sv.Domains...)
	}
	dm := map[string]string{}
	for o, n := range k8s.DefaultDomainMap(hosts, base, req.DomainMap) {
		if n != "" {
			dm[o] = n
		}
	}
	if len(dm) > 0 {
		var pairs []string
		for o, n := range dm {
			pairs = append(pairs, o+" → "+n)
		}
		sort.Strings(pairs)
		plan.Warnings = append(plan.Warnings, "Update by hand any OAuth callback URLs, webhooks and payment provider settings (Stripe, GitHub, Google…) that use these URLs: "+strings.Join(pairs, ", "))
	}

	// validate everything before creating anything
	specs := make([]api.Service, len(plan.Services))
	for i, is := range plan.Services {
		v := api.Service{Name: is.Name, Kind: is.Kind, Image: is.Image, Command: is.Command, Args: is.Args, Ports: is.Ports, Volumes: is.Volumes, Configs: is.Configs}
		if v.Kind == "" {
			v.Kind = "image"
		}
		if err := validateServiceSpec(&v); err != nil {
			return badRequest("service %s: %v", is.Name, err)
		}
		if err := validateEnv(is.Env); err != nil {
			return badRequest("service %s: %v", is.Name, err)
		}
		specs[i] = v
	}
	p, err := s.createProject(ctx, name, req.ServerID, fmt.Sprintf("Taken over from Zeabur project %s (%s) on this server", proj.ProjectName, req.Namespace), true)
	if err != nil {
		return err
	}
	meta := zeaburMeta{Namespace: req.Namespace, DomainMap: dm, UpdatedAt: time.Now().UTC()}
	for i, is := range plan.Services {
		svc, err := s.createService(ctx, p, specs[i], is.Env, false)
		if err != nil {
			s.execDeleteProject(ctx, p.ID, "system")
			return err
		}
		zs := bySrc[is.SourceServiceID]
		m := zeaburSvcMeta{SourceServiceID: zs.ServiceID, Name: svc.Name, AtollID: svc.ID, Workload: zs.Workload, Kind: svc.Kind,
			Image: zs.Image, ZeaburRegistry: zs.ZeaburRegistry && is.ImageNeedsPush}
		for vi, zv := range zs.Volumes {
			m.Volumes = append(m.Volumes, zeaburVolMeta{PVC: zv.PVC, HostDir: zv.HostDir, Volume: k8s.VolumeName(zv.PVC, zs.ServiceID, vi), Bytes: zv.Bytes})
		}
		meta.Services = append(meta.Services, m)
		for _, h := range is.Domains {
			if err := s.createPendingDomain(ctx, svc, h, "routed by Atoll after cutover"); err != nil {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: domain %s skipped: %v", is.Name, h, err))
			}
		}
	}
	job := &api.ImportJob{ID: newID("imp"), Plan: plan, ProjectID: p.ID, Status: zsPlanned, CreatedAt: time.Now().UTC(),
		Mode: importKindZeabur, ServerID: req.ServerID, Namespace: req.Namespace}
	for _, n := range append(append([]string{}, rehearsalSteps...), cutoverSteps...) {
		job.Steps = append(job.Steps, api.ImportStep{Name: n, Status: "pending"})
	}
	s.saveImport(ctx, job, importKindZeabur, meta)
	s.st.Audit(ctx, principalFrom(ctx).actor(), "import.zeabur_local", job.ID, map[string]string{"project": p.ID, "namespace": req.Namespace})
	writeJSON(w, 201, maskJob(job))
	return nil
}

// lowerKeys returns m with lower-cased keys (hostnames are case-insensitive).
func lowerKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(strings.TrimSuffix(strings.TrimSpace(k), "."))] = strings.TrimSpace(v)
	}
	return out
}

func hasKey(env []api.EnvVar, k string) bool {
	for _, e := range env {
		if e.Key == k {
			return true
		}
	}
	return false
}

// ---------- shared helpers ----------

// loadZeaburJob loads a zeabur-local job and its meta.
func (s *Server) loadZeaburJob(ctx context.Context, id string) (*api.ImportJob, *zeaburMeta, error) {
	var meta zeaburMeta
	j, kind, err := s.st.GetImport(ctx, id, &meta)
	if err != nil {
		return nil, nil, err
	}
	if kind != importKindZeabur {
		return nil, nil, badRequest("import %s is a CLI plan import; use /start", id)
	}
	return j, &meta, nil
}

// zrun is one background operation on a zeabur-local job.
type zrun struct {
	s    *Server
	ctx  context.Context
	job  *api.ImportJob
	meta *zeaburMeta
	node string
	st   *stepper
}

func (s *Server) newZrun(ctx context.Context, j *api.ImportJob, m *zeaburMeta) *zrun {
	z := &zrun{s: s, ctx: ctx, job: j, meta: m, node: j.ServerID}
	z.st = &stepper{steps: &j.Steps, save: z.save}
	return z
}

func (z *zrun) save() {
	z.meta.UpdatedAt = time.Now().UTC()
	z.s.saveImport(z.ctx, z.job, importKindZeabur, *z.meta)
}

func (z *zrun) setStatus(st string) {
	z.job.Status = st
	z.save()
}

func (z *zrun) resetSteps(names []string) {
	for _, n := range names {
		i := z.st.idx(n)
		z.job.Steps[i].Status, z.job.Steps[i].Detail = "pending", ""
	}
	z.save()
}

// startJob validates state, locks the job and runs fn in the background.
func (s *Server) startZeaburOp(ctx context.Context, id string, allowed []string, busyStatus string, fn func(z *zrun) error) (*api.ImportJob, error) {
	j, meta, err := s.loadZeaburJob(ctx, id)
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
	z := s.newZrun(s.bg, j, meta)
	z.setStatus(busyStatus)
	snapshot := maskJob(j)
	s.goBG(func(bg context.Context) {
		defer s.unlockJob(j.ID)
		defer s.unlockJob(j.ProjectID)
		if err := fn(z); err != nil {
			z.meta.LastError = err.Error()
			z.save()
			s.log.Warn("zeabur import step failed", "job", j.ID, "err", err)
		}
	})
	return snapshot, nil
}

// ---------- rehearse ----------

func (s *Server) handleRehearse(w http.ResponseWriter, r *http.Request) error {
	j, err := s.startZeaburOp(r.Context(), r.PathValue("id"), []string{zsPlanned, zsRehearsed, zsFailed, zsRolledBack}, zsRehearsing, (*zrun).rehearse)
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "import.rehearse", j.ID, nil)
	writeJSON(w, 200, j)
	return nil
}

func (z *zrun) rehearse() error {
	z.resetSteps(rehearsalSteps)
	err := z.doRehearse()
	if err != nil {
		z.setStatus(zsFailed)
		return err
	}
	z.setStatus(zsRehearsed)
	return nil
}

func (z *zrun) doRehearse() error {
	s, ctx := z.s, z.ctx
	if err := z.st.do(stepResources, func() (string, error) {
		var total int64
		for _, sv := range z.meta.Services {
			for _, v := range sv.Volumes {
				total += v.Bytes
			}
		}
		inv, err := s.scanZeabur(ctx, z.node, true)
		if err != nil {
			return "", err
		}
		free := int64(inv.DiskFreeGB * 1e9)
		if free > 0 && float64(free) < 1.2*float64(total) {
			return "", fmt.Errorf("not enough disk: %s of data needs %s free, only %s available", humanBytes(total), humanBytes(int64(1.2*float64(total))), humanBytes(free))
		}
		return fmt.Sprintf("%s of data, %.1f GB disk free, %d MB memory free", humanBytes(total), inv.DiskFreeGB, inv.MemFreeMB), nil
	}); err != nil {
		return err
	}
	if err := z.st.do(stepImages, func() (string, error) {
		n, pulled := 0, 0
		for _, sv := range z.meta.Services {
			if sv.Image == "" {
				continue
			}
			// Zeabur-built images only exist in containerd; registry images are pulled by
			// their original ref (tag kept), with containerd as the agent's fallback.
			pull := !sv.ZeaburRegistry
			ref, err := s.hub.Call(ctx, z.node, api.MsgZeaburImage, api.ZeaburImagePayload{Image: sv.Image, ServiceID: sv.AtollID, Pull: pull}, s.cfg.TransferTimeout)
			if err != nil {
				return "", fmt.Errorf("%s: %w", sv.Name, err)
			}
			ref = strings.TrimSpace(ref)
			if ref == "" {
				return "", fmt.Errorf("%s: agent returned no image reference", sv.Name)
			}
			if err := z.setImage(sv.AtollID, ref); err != nil {
				return "", err
			}
			if pull {
				pulled++
			} else {
				n++
			}
		}
		return fmt.Sprintf("%d Zeabur-built image(s) exported from containerd; %d registry image(s) pulled with their original tag", n, pulled), nil
	}); err != nil {
		return err
	}
	if err := z.st.do(stepVolumes, func() (string, error) { return z.copyVolumes() }); err != nil {
		return err
	}
	if err := z.st.do(stepDatabases, func() (string, error) {
		if err := z.deployAndWait(true); err != nil {
			return "", err
		}
		msg, mismatched, err := z.copyDatabases()
		if err != nil {
			return "", err
		}
		if mismatched > 0 {
			msg += fmt.Sprintf("; %d table(s) differ because Zeabur is still serving writes — the final sync at cutover must match", mismatched)
		}
		return msg, nil
	}); err != nil {
		return err
	}
	since := time.Now()
	if err := z.st.do(stepStart, func() (string, error) {
		if err := z.deployAndWait(false); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d service(s) running without public ingress; Zeabur keeps serving", len(z.meta.Services)), nil
	}); err != nil {
		return err
	}
	return z.st.do(stepVerify, func() (string, error) {
		if err := s.waitHealthy(ctx, z.serviceIDs(nil), since, s.cfg.VerifyTimeout); err != nil {
			return "", err
		}
		return fmt.Sprintf("all services healthy; %d table/keyspace count(s) recorded", len(z.job.Verify)), nil
	})
}

func (z *zrun) serviceIDs(filter func(zeaburSvcMeta) bool) []string {
	var ids []string
	for _, sv := range z.meta.Services {
		if filter == nil || filter(sv) {
			ids = append(ids, sv.AtollID)
		}
	}
	return ids
}

func (z *zrun) setImage(serviceID, ref string) error {
	svc, _, err := z.s.st.GetService(z.ctx, serviceID)
	if err != nil {
		return err
	}
	if svc.Image == ref {
		return nil
	}
	svc.Image = ref
	return z.s.st.UpdateServiceSpec(z.ctx, *svc)
}

// deployAndWait deploys the database services (dbs=true) or the others and waits for them.
func (z *zrun) deployAndWait(dbs bool) error {
	var ids []string
	for _, sv := range z.meta.Services {
		if sv.isDB() != dbs {
			continue
		}
		d, err := z.s.deploy(z.ctx, sv.AtollID, "import")
		if err != nil {
			return fmt.Errorf("%s: %w", sv.Name, err)
		}
		ids = append(ids, d.ID)
	}
	return z.s.waitDeployments(z.ctx, ids, z.s.cfg.DeployWaitTimeout)
}

// copyVolumes mirrors the PVC dirs of non-database services (databases are dumped instead).
func (z *zrun) copyVolumes() (string, error) {
	n, total := 0, int64(0)
	for _, sv := range z.meta.Services {
		if sv.isDB() {
			continue
		}
		for _, v := range sv.Volumes {
			if v.HostDir == "" {
				continue
			}
			out, err := z.s.hub.Call(z.ctx, z.node, api.MsgZeaburVolume, api.ZeaburVolumePayload{HostDir: v.HostDir, ServiceID: sv.AtollID, Volume: v.Volume}, z.s.cfg.TransferTimeout)
			if err != nil {
				return "", fmt.Errorf("%s/%s: %w", sv.Name, v.Volume, err)
			}
			var res struct {
				Bytes int64 `json:"bytes"`
			}
			json.Unmarshal([]byte(out), &res)
			total += res.Bytes
			n++
		}
	}
	if n == 0 {
		return "no app volumes", nil
	}
	return fmt.Sprintf("%d volume(s), %s copied", n, humanBytes(total)), nil
}

// copyDatabases dumps every database service from its Zeabur pod and restores it into the
// Atoll container, recording row counts in job.Verify. It returns how many tables differ.
func (z *zrun) copyDatabases() (string, int, error) {
	var verify []api.ImportVerify
	n, mismatched := 0, 0
	for _, sv := range z.meta.Services {
		if !sv.isDB() {
			continue
		}
		res, err := z.dbSync(sv)
		if err != nil {
			return "", 0, err
		}
		n++
		verify = append(verify, verifyRows(sv.Name, res)...)
		if !res.Match {
			mismatched += max(1, len(res.Mismatches))
		}
	}
	z.job.Verify = verify
	z.save()
	if n == 0 {
		return "no databases", 0, nil
	}
	return fmt.Sprintf("%d database(s) dumped and restored, %d table count(s) compared", n, len(verify)), mismatched, nil
}

func (z *zrun) dbSync(sv zeaburSvcMeta) (*dbResult, error) {
	out, err := z.s.hub.Call(z.ctx, z.node, api.MsgZeaburDB, api.ZeaburDBPayload{Namespace: z.meta.Namespace, SourceServiceID: sv.SourceServiceID, Kind: sv.Kind, TargetServiceID: sv.AtollID}, z.s.cfg.TransferTimeout)
	var res dbResult
	if out != "" {
		if jerr := json.Unmarshal([]byte(out), &res); jerr != nil && err == nil {
			return nil, fmt.Errorf("%s: invalid zeabur.db result: %v", sv.Name, jerr)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sv.Name, err)
	}
	return &res, nil
}

func verifyRows(service string, res *dbResult) []api.ImportVerify {
	keys := map[string]bool{}
	for k := range res.Source {
		keys[k] = true
	}
	for k := range res.Target {
		keys[k] = true
	}
	ks := make([]string, 0, len(keys))
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	out := make([]api.ImportVerify, 0, len(ks))
	for _, k := range ks {
		out = append(out, api.ImportVerify{Service: service, Table: k, SourceRows: res.Source[k], TargetRows: res.Target[k]})
	}
	return out
}

// ---------- cutover ----------

func (s *Server) handleCutover(w http.ResponseWriter, r *http.Request) error {
	j, meta, err := s.loadZeaburJob(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if meta.RollbackDisabled {
		return errf(409, "bad_state", "Zeabur was removed from this server; nothing to cut over")
	}
	node, _ := s.st.GetNode(r.Context(), j.ServerID)
	nodeName := j.ServerID
	if node != nil {
		nodeName = node.Name
	}
	summary := fmt.Sprintf("Cut over Zeabur namespace %s on %s to Atoll: Zeabur services scale to 0, final data sync, Zeabur ingress parked, Atoll takes ports 80/443", j.Namespace, nodeName)
	return s.requireConfirm(w, r, "import.cutover", j.ID, summary, nil, func(ctx context.Context) (int, any, error) {
		out, err := s.startCutover(ctx, j.ID, principalFrom(r.Context()).actor())
		return 200, out, err
	})
}

// startCutover launches the cutover in the background (also called for approved confirmations).
func (s *Server) startCutover(ctx context.Context, id, actor string) (*api.ImportJob, error) {
	j, err := s.startZeaburOp(ctx, id, []string{zsRehearsed, zsRolledBack}, zsCuttingOver, (*zrun).cutover)
	if err != nil {
		return nil, err
	}
	s.st.Audit(ctx, actor, "import.cutover", id, nil)
	return j, nil
}

func (z *zrun) cutover() error {
	z.resetSteps(cutoverSteps)
	err := z.doCutover()
	if err == nil {
		z.meta.Cutovers++
		z.setStatus(zsLive)
		return nil
	}
	// automatic rollback: Zeabur serves again
	z.setStatus(zsRollingBack)
	if rerr := z.doRollback(); rerr != nil {
		z.setStatus(zsFailed)
		return fmt.Errorf("cutover failed (%v) and rollback failed: %w", err, rerr)
	}
	z.setStatus(zsRolledBack)
	return fmt.Errorf("cutover failed, rolled back automatically: %w", err)
}

// startStopped starts every Atoll service of the job that is stopped (after a rollback) and
// waits until they are healthy.
func (z *zrun) startStopped() error {
	s, ctx := z.s, z.ctx
	since := time.Now()
	var started []string
	for _, sv := range z.meta.Services {
		svc, stopped, err := s.st.GetService(ctx, sv.AtollID)
		if err != nil {
			return err
		}
		if !stopped {
			continue
		}
		if _, err := s.serviceAction(ctx, svc, "start"); err != nil {
			return fmt.Errorf("start %s: %w", sv.Name, err)
		}
		started = append(started, sv.AtollID)
	}
	if len(started) == 0 {
		return nil
	}
	return s.waitHealthy(ctx, started, since, s.cfg.VerifyTimeout)
}

func (z *zrun) scale(sv zeaburSvcMeta, replicas int) error {
	if sv.Workload == "" {
		return nil
	}
	_, err := z.s.hub.Call(z.ctx, z.node, api.MsgZeaburScale, api.ZeaburScalePayload{Namespace: z.meta.Namespace, Workload: sv.Workload, Replicas: replicas}, 3*time.Minute)
	if err != nil {
		return fmt.Errorf("scale %s: %w", sv.Workload, err)
	}
	if replicas == 0 {
		key := z.meta.Namespace + "/" + sv.Workload
		for _, k := range z.meta.Scaled {
			if k == key {
				return nil
			}
		}
		z.meta.Scaled = append(z.meta.Scaled, key)
		z.save()
	}
	return nil
}

func (z *zrun) doCutover() error {
	s, ctx := z.s, z.ctx
	if err := z.st.do(stepScaleDown, func() (string, error) {
		// A rollback stops Atoll's copies (desired_stopped): start them again first, or the
		// final DB sync has no target and the ingress has no routes to serve.
		if err := z.startStopped(); err != nil {
			return "", err
		}
		n := 0
		for _, sv := range z.meta.Services {
			if sv.isDB() || sv.Workload == "" {
				continue
			}
			if err := z.scale(sv, 0); err != nil {
				return "", err
			}
			n++
		}
		return fmt.Sprintf("%d Zeabur app workload(s) scaled to 0; databases keep running until the final dump", n), nil
	}); err != nil {
		return err
	}
	if err := z.st.do(stepFinalSync, func() (string, error) {
		var verify []api.ImportVerify
		var bad []string
		for _, sv := range z.meta.Services {
			if !sv.isDB() {
				continue
			}
			res, err := z.dbSync(sv)
			if err != nil {
				return "", err
			}
			verify = append(verify, verifyRows(sv.Name, res)...)
			if !res.Match {
				m := res.Mismatches
				if len(m) == 0 {
					m = []string{"row counts differ"}
				}
				bad = append(bad, sv.Name+": "+strings.Join(m, "; "))
			}
		}
		z.job.Verify = verify
		z.save()
		if len(bad) > 0 {
			return "", fmt.Errorf("database verification failed after final sync: %s", strings.Join(bad, " | "))
		}
		// the databases are copied: stop Zeabur's copies too
		for _, sv := range z.meta.Services {
			if sv.isDB() {
				if err := z.scale(sv, 0); err != nil {
					return "", err
				}
			}
		}
		// final volume mirror with the Atoll app containers stopped
		apps := z.serviceIDs(func(m zeaburSvcMeta) bool { return !m.isDB() && len(m.Volumes) > 0 })
		for _, id := range apps {
			s.hub.Call(ctx, z.node, api.MsgAction, api.ActionPayload{ServiceID: id, Action: "stop"}, 2*s.cfg.RequestTimeout)
		}
		vol, err := z.copyVolumes()
		for _, id := range apps {
			if _, serr := s.hub.Call(ctx, z.node, api.MsgAction, api.ActionPayload{ServiceID: id, Action: "start"}, 2*s.cfg.RequestTimeout); serr != nil && err == nil {
				err = fmt.Errorf("restart %s: %w", id, serr)
			}
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("databases match (%d table count(s)); %s", len(verify), vol), nil
	}); err != nil {
		return err
	}
	if err := z.st.do(stepIngress, func() (string, error) {
		// Marked before the call: a park that fails halfway (DaemonSet patched, operator
		// stopped, ports still busy) must still be undone by the rollback.
		z.meta.IngressParked = true
		z.save()
		if _, err := s.hub.Call(ctx, z.node, api.MsgZeaburIngress, api.ZeaburIngressPayload{Park: true}, 5*time.Minute); err != nil {
			return "", fmt.Errorf("park Zeabur ingress: %w", err)
		}
		if err := s.st.SetIngressHold(ctx, z.job.ProjectID, false); err != nil {
			return "", err
		}
		if err := s.pushIngressSync(ctx, z.node); err != nil {
			return "", fmt.Errorf("start Atoll ingress: %w", err)
		}
		return "Zeabur ingress parked; atoll-caddy serves 80/443", nil
	}); err != nil {
		return err
	}
	return z.st.do(stepDNS, func() (string, error) { return z.configureDNS() })
}

// configureDNS creates proxied A records for remapped *.zeabur.app hosts that have none yet;
// custom domains already point at this server and are left alone. Without a DNS provider the
// records are the user's job: hosts that already resolve to this server go active, the others
// stay pending with the record to create, and the cutover still succeeds (Caddy serves them as
// soon as DNS points here).
func (z *zrun) configureDNS() (string, error) {
	s, ctx := z.s, z.ctx
	doms, err := s.st.ListDomains(ctx, "", z.job.ProjectID)
	if err != nil {
		return "", err
	}
	node, err := s.st.GetNode(ctx, z.node)
	if err != nil {
		return "", err
	}
	remapped := map[string]bool{}
	for _, n := range z.meta.DomainMap {
		remapped[n] = true
	}
	prov := s.dnsProvider(ctx)
	finder, _ := prov.(dns.Finder)
	created, kept, custom, resolved := 0, 0, 0, 0
	var bad, manual []string
	for _, d := range doms {
		dd := d.Domain
		if !remapped[dd.Hostname] {
			custom++
			s.st.SetDomainStatus(ctx, dd.ID, "active", "", "")
			continue
		}
		if finder != nil {
			recs, err := finder.FindA(ctx, dd.Hostname)
			if err != nil {
				bad = append(bad, dd.Hostname+": "+err.Error())
				continue
			}
			if len(recs) > 0 {
				kept++
				s.st.SetDomainStatus(ctx, dd.ID, "active", "", recs[0].ID)
				continue
			}
		}
		recID, err := prov.UpsertA(ctx, dd.Hostname, node.PublicIPv4, true)
		if errors.Is(err, dns.ErrNotConfigured) {
			if z.resolvesHere(dd.Hostname, node.PublicIPv4) {
				resolved++
				s.st.SetDomainStatus(ctx, dd.ID, "active", "", "")
				continue
			}
			note := fmt.Sprintf("no DNS provider configured: create A %s -> %s", dd.Hostname, orUnknown(node.PublicIPv4))
			s.st.SetDomainStatus(ctx, dd.ID, "pending", note, "")
			manual = append(manual, dd.Hostname)
			continue
		}
		if err != nil {
			s.st.SetDomainStatus(ctx, dd.ID, "error", err.Error(), "")
			bad = append(bad, dd.Hostname+": "+err.Error())
			continue
		}
		created++
		s.st.SetDomainStatus(ctx, dd.ID, "active", "", recID)
	}
	if len(bad) > 0 {
		return "", fmt.Errorf("DNS failed: %s", strings.Join(bad, "; "))
	}
	msg := fmt.Sprintf("%d record(s) created, %d already present, %d custom domain(s) unchanged (same IP)", created, kept, custom)
	if resolved > 0 {
		msg += fmt.Sprintf(", %d already resolve to this server", resolved)
	}
	if len(manual) > 0 {
		msg += fmt.Sprintf("; no DNS provider: create A records to %s for %s", orUnknown(node.PublicIPv4), strings.Join(manual, ", "))
	}
	return msg, nil
}

// resolvesHere reports whether host already resolves to ip (best effort, short timeout).
func (z *zrun) resolvesHere(host, ip string) bool {
	return z.s.resolvesTo(z.ctx, host, ip)
}

// ---------- rollback ----------

func (s *Server) handleZeaburRollback(w http.ResponseWriter, r *http.Request) error {
	_, meta, err := s.loadZeaburJob(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if meta.RollbackDisabled {
		return errf(409, "rollback_disabled", "Zeabur was removed from this server; rollback is no longer possible")
	}
	j, err := s.startZeaburOp(r.Context(), r.PathValue("id"), []string{zsLive, zsFailed, zsRehearsed}, zsRollingBack, func(z *zrun) error {
		if err := z.doRollback(); err != nil {
			z.setStatus(zsFailed)
			return err
		}
		z.setStatus(zsRolledBack)
		return nil
	})
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "import.rollback", j.ID, nil)
	writeJSON(w, 200, j)
	return nil
}

// doRollback stops Atoll's copies, restores Zeabur's ingress and scales Zeabur back.
// It keeps going after errors so as much as possible is restored, then reports them all.
func (z *zrun) doRollback() error {
	s, ctx := z.s, z.ctx
	var errs []string
	if err := s.st.SetIngressHold(ctx, z.job.ProjectID, true); err != nil {
		errs = append(errs, err.Error())
	}
	if err := s.pushIngressSync(ctx, z.node); err != nil {
		errs = append(errs, "withdraw Atoll routes: "+err.Error())
	}
	for _, sv := range z.meta.Services {
		s.st.SetDesiredStopped(ctx, sv.AtollID, true)
		if _, err := s.hub.Call(ctx, z.node, api.MsgAction, api.ActionPayload{ServiceID: sv.AtollID, Action: "stop"}, 2*s.cfg.RequestTimeout); err != nil {
			errs = append(errs, "stop "+sv.Name+": "+err.Error())
		} else {
			s.st.SetServiceStatus(ctx, sv.AtollID, "stopped", "0/1")
			s.publishService(ctx, sv.AtollID)
		}
	}
	if z.meta.IngressParked {
		if _, err := s.hub.Call(ctx, z.node, api.MsgZeaburIngress, api.ZeaburIngressPayload{Park: false}, 5*time.Minute); err != nil {
			errs = append(errs, "restore Zeabur ingress: "+err.Error())
		} else {
			z.meta.IngressParked = false
		}
	}
	// databases first, then apps
	order := append([]zeaburSvcMeta{}, z.meta.Services...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].isDB() && !order[j].isDB() })
	scaled := map[string]bool{}
	for _, k := range z.meta.Scaled {
		scaled[k] = true
	}
	var left []string
	for _, sv := range order {
		key := z.meta.Namespace + "/" + sv.Workload
		if sv.Workload == "" || !scaled[key] {
			continue
		}
		if _, err := s.hub.Call(ctx, z.node, api.MsgZeaburScale, api.ZeaburScalePayload{Namespace: z.meta.Namespace, Workload: sv.Workload, Replicas: -1}, 3*time.Minute); err != nil {
			errs = append(errs, "scale back "+sv.Workload+": "+err.Error())
			left = append(left, key)
		}
	}
	z.meta.Scaled = left
	// record the outcome on the cutover steps (no extra steps: the UI shows 6 + 4)
	for _, n := range cutoverSteps {
		i := z.st.idx(n)
		if z.job.Steps[i].Status == "done" || z.job.Steps[i].Status == "running" {
			z.job.Steps[i].Status = "skipped"
			z.job.Steps[i].Detail = "rolled back"
		}
	}
	z.save()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// ---------- cleanup ----------

func (s *Server) handleZeaburCleanup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var req api.ZeaburCleanupRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if p := principalFrom(ctx); !p.Session && p.RequireApproval {
		return errf(403, "approval_required", "removing Zeabur cannot be queued for approval; run it from the panel or with a token without require_approval")
	}
	j, meta, err := s.loadZeaburJob(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	if j.Status != zsLive || meta.Cutovers < 1 {
		return errf(409, "bad_state", "Zeabur can only be removed after a successful cutover (import is %s)", j.Status)
	}
	node, err := s.st.GetNode(ctx, j.ServerID)
	if err != nil {
		return err
	}
	if req.Confirm != node.Name {
		return badRequest("confirm must equal the server name %q exactly", node.Name)
	}
	actor := principalFrom(ctx).actor()
	out, err := s.startZeaburOp(ctx, j.ID, []string{zsLive}, zsCleaning, func(z *zrun) error {
		res, err := s.hub.Call(z.ctx, z.node, api.MsgZeaburUninstall, api.ZeaburUninstallPayload{KeepBackup: req.KeepBackup}, s.cfg.TransferTimeout)
		if err != nil {
			z.setStatus(zsLive)
			return err
		}
		var u struct {
			Backups  []string `json:"backups"`
			ScriptOK bool     `json:"script_ok"`
			Warnings []string `json:"warnings"`
		}
		json.Unmarshal([]byte(res), &u)
		note := fmt.Sprintf("Zeabur k3s removed from %s (%d PVC backup(s) kept under /var/lib/atoll/zeabur-backup)", node.Name, len(u.Backups))
		z.job.Plan.Warnings = append(z.job.Plan.Warnings, note)
		z.job.Plan.Warnings = append(z.job.Plan.Warnings, u.Warnings...)
		z.meta.RollbackDisabled = true
		z.meta.Scaled = nil
		z.meta.IngressParked = false
		z.setStatus(zsCleaned)
		s.st.Audit(z.ctx, actor, "import.cleanup.done", j.ID, map[string]any{"script_ok": u.ScriptOK})
		return nil
	})
	if err != nil {
		return err
	}
	s.st.Audit(ctx, actor, "import.cleanup", j.ID, map[string]any{"keep_backup": req.KeepBackup})
	writeJSON(w, 200, out)
	return nil
}
