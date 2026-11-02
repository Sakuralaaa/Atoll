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
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
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
	// CreatedRecords: DNS records the cutover created (hostname -> record ID), deleted again
	// by a rollback after the routes are withdrawn. Records that existed before are left alone.
	CreatedRecords map[string]string `json:"created_records,omitempty"`
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
	// held: remapped hostnames kept out of the route table during a cutover until their DNS
	// record is in place (handed over to provisionDomain, or released by configureDNS).
	held map[string]bool
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
