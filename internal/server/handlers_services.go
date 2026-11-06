package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/templates"
)

// ---------- services ----------

type createServiceRequest struct {
	api.Service
	Env []api.EnvVar `json:"env,omitempty"` // optional initial env (extension)
	// RegistryAuth (write-only) pulls a private image: stored encrypted as the service's
	// registry credential (registry_cred_id), never returned.
	RegistryAuth *api.RegistryAuth `json:"registry_auth,omitempty"`
}

// registryHost is the registry server of an image ref ("" = Docker Hub).
func registryHost(image string) string {
	first, _, ok := strings.Cut(image, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return first
	}
	return ""
}

func (s *Server) handleListServices(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.st.GetProject(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	svcs, err := s.servicesWithDomains(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, svcs)
	return nil
}

func (s *Server) handleCreateService(w http.ResponseWriter, r *http.Request) error {
	p, err := s.st.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req createServiceRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	placement := strings.TrimSpace(req.ServerID)
	if placement != "" && placement != p.ServerID {
		if !memberMayUse(r, placement) {
			return badRequest("unknown server_id %q", placement)
		}
		if _, err := s.st.GetNode(r.Context(), placement); err != nil {
			return badRequest("unknown server_id %q", placement)
		}
		if err := s.checkPlacement(r.Context(), p.ID, "", placement); err != nil { // network.go
			return err
		}
	}
	if req.Git != nil {
		node := placement
		if node == "" {
			node = p.ServerID
		}
		if !s.hub.Online(node) {
			return errf(409, "server_offline", "%v; builds from source run on the service's server", ErrNodeOffline)
		}
	}
	if err := checkHostPaths(req.Service.Volumes, nil); err != nil {
		return err
	}
	req.Service.RegistryCredID = "" // only set from registry_auth below
	if ra := req.RegistryAuth; ra != nil && req.Git == nil && (ra.Username != "" || ra.Password != "") {
		cred := api.RegistryAuth{Server: strings.TrimSpace(ra.Server), Username: strings.TrimSpace(ra.Username), Password: ra.Password}
		if cred.Username == "" || cred.Password == "" {
			return badRequest("registry_auth needs both username and password")
		}
		if cred.Server == "" {
			cred.Server = registryHost(strings.TrimSpace(req.Service.Image))
		}
		if cred.Server == "" {
			cred.Server = "https://index.docker.io/v1/"
		}
		id := newID("reg")
		if err := s.st.SetSecretBlob(r.Context(), "registry_cred:"+id, cred); err != nil {
			return err
		}
		req.Service.RegistryCredID = id
	}
	svc, err := s.createService(r.Context(), p, req.Service, req.Env, true)
	if err != nil {
		return err
	}
	if placement != "" && placement != p.ServerID {
		if err := s.st.SetServiceServer(r.Context(), svc.ID, placement); err != nil {
			return err
		}
	}
	if _, err := s.deploy(r.Context(), svc.ID, "create"); err != nil {
		return err
	}
	s.goBG(func(bg context.Context) { s.pushMeshAll(bg) })
	audit := map[string]string{"name": svc.Name, "image": svc.Image}
	if svc.Git != nil {
		audit["git"] = svc.Git.Repo + "@" + svc.Git.Branch
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "service.create", svc.ID, audit)
	svc, _, err = s.st.GetService(r.Context(), svc.ID)
	if err != nil {
		return err
	}
	if svc.Git != nil { // the webhook secret is shown once, here (later: panel session only)
		if g, err := s.st.GetServiceGit(r.Context(), svc.ID); err == nil {
			wh := s.webhookInfo(r.Context(), r, svc.ID, g, true)
			svc.Webhook = &wh
		}
	}
	writeJSON(w, 201, svc)
	return nil
}

// createService validates and stores a service (template kinds get image,
// ports, volumes and env from the template). It does not deploy.
func (s *Server) createService(ctx context.Context, p *api.Project, v api.Service, env []api.EnvVar, applyTemplate bool) (*api.Service, error) {
	v.Name = strings.ToLower(strings.TrimSpace(v.Name))
	dn, err := checkDisplayName(v.DisplayName) // service_display.go
	if err != nil {
		return nil, err
	}
	v.DisplayName = dn
	if v.Kind == "" {
		v.Kind = "image"
	}
	if t, ok := templates.Get(v.Kind); ok && applyTemplate {
		if v.Name == "" {
			v.Name = v.Kind
		}
		if v.Image == "" {
			v.Image = t.Image
		}
		if len(v.Ports) == 0 {
			v.Ports = append([]api.Port(nil), t.Ports...)
		}
		if len(v.Volumes) == 0 {
			v.Volumes = append([]api.Volume(nil), t.Volumes...)
		}
		if len(v.Command) == 0 {
			v.Command = t.Command
		}
		tenv := templates.Instantiate(t, v.Name)
		// caller-provided env overrides template keys
		over := map[string]bool{}
		for _, e := range env {
			over[e.Key] = true
		}
		merged := []api.EnvVar{}
		for _, e := range tenv {
			if !over[e.Key] {
				merged = append(merged, e)
			}
		}
		env = append(merged, env...)
	}
	var git *api.GitSource
	if v.Git != nil {
		if v.Kind != "image" {
			return nil, badRequest("a git source only applies to kind \"image\" (not %q)", v.Kind)
		}
		g := *v.Git
		if err := validateGitSource(&g); err != nil {
			return nil, err
		}
		git, v.Git, v.Image, v.RegistryCredID = &g, &g, "", ""
	}
	v.Webhook, v.Source = nil, ""
	noCPU, noMem := v.CPULimit == -1, v.MemLimitMB == -1
	s.withDefaultLimits(ctx, &v)                               // resource_defaults.go
	if err := s.quotaFill(ctx, &v, noCPU, noMem); err != nil { // quotas.go
		return nil, err
	}
	if err := validateServiceSpec(&v); err != nil {
		return nil, err
	}
	if err := s.checkQuota(ctx, api.MemberUsage{Services: 1, CPU: v.CPULimit, MemMB: v.MemLimitMB}, api.MemberUsage{}); err != nil {
		return nil, err
	}
	if err := validateEnv(env); err != nil {
		return nil, err
	}
	v.ID = newID("svc")
	v.ProjectID = p.ID
	v.Status = "deploying"
	v.Replicas = "0/1"
	v.CreatedAt = time.Now().UTC()
	v.Domains = nil
	if err := s.st.CreateService(ctx, v); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil, errf(409, "conflict", "a service named %q already exists in this project", v.Name)
		}
		return nil, err
	}
	if err := s.st.ReplaceEnv(ctx, v.ID, env); err != nil {
		return nil, err
	}
	if git != nil {
		if _, err := s.saveGitSource(ctx, v.ID, *git, true); err != nil {
			s.st.DeleteService(ctx, v.ID)
			return nil, err
		}
		v.Git = &api.GitSource{Repo: git.Repo, Branch: git.Branch, Context: git.Context, Dockerfile: git.Dockerfile,
			BuildArgs: git.BuildArgs, HasToken: git.Token != ""}
		v.Source = "git"
	}
	s.publishService(ctx, v.ID)
	return &v, nil
}

func (s *Server) handleGetService(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	doms, err := s.st.ListDomains(r.Context(), svc.ID, "")
	if err != nil {
		return err
	}
	for _, d := range doms {
		svc.Domains = append(svc.Domains, d.Domain)
	}
	writeJSON(w, 200, svc)
	return nil
}

func (s *Server) handlePatchService(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := decode(r, &raw); err != nil {
		return err
	}
	if v, ok := raw["server_id"]; ok {
		var to string
		json.Unmarshal(v, &to)
		if to != "" && to != svc.ServerID {
			return badRequest("to move a service to another server use POST /api/v1/services/%s/migrate", svc.ID)
		}
	}
	for _, f := range []string{"id", "project_id", "kind", "status", "replicas", "created_at", "domains", "server_id", "source", "webhook"} {
		delete(raw, f)
	}
	if done, err := s.patchDisplayName(w, r, raw, svc); done || err != nil { // service_display.go
		return err
	}
	gitRaw, gitInPatch := raw["git"]
	if svc.Git != nil && !gitInPatch {
		if v, ok := raw["image"]; ok {
			var img string
			json.Unmarshal(v, &img)
			if img != svc.Image {
				return badRequest("the image of a service built from git is set by its builds; to deploy a registry image instead PATCH {\"git\": null, \"image\": \"...\"}")
			}
		}
	}
	if gitInPatch && string(gitRaw) != "null" && svc.Git != nil {
		// partial git patch: fields not given keep their value (the token stays unless given or cleared)
		var over, base map[string]json.RawMessage
		if err := json.Unmarshal(gitRaw, &over); err != nil {
			return badRequest("invalid git: %v", err)
		}
		cur := *svc.Git
		cur.HasToken, cur.LastCommit = false, ""
		b, _ := json.Marshal(cur)
		json.Unmarshal(b, &base)
		for k, v := range over {
			base[k] = v
		}
		raw["git"], _ = json.Marshal(base)
	}
	merged, _ := json.Marshal(svc)
	var cur map[string]json.RawMessage
	json.Unmarshal(merged, &cur)
	for k, v := range raw {
		cur[k] = v
	}
	b, _ := json.Marshal(cur)
	var next api.Service
	if err := json.Unmarshal(b, &next); err != nil {
		return badRequest("invalid service fields: %v", err)
	}
	next.Name = strings.ToLower(strings.TrimSpace(next.Name))
	next.Webhook, next.Source = nil, ""
	// git source changes (git.go): set/replace, or null to go back to a registry image
	var gitNext *api.GitSource
	switch {
	case !gitInPatch:
	case next.Git == nil:
		if svc.Git != nil && (strings.TrimSpace(next.Image) == "" || strings.HasPrefix(next.Image, BuildImageRepo)) {
			return badRequest("to stop building from git give the registry image to deploy instead: {\"git\": null, \"image\": \"...\"}")
		}
	default:
		if svc.Kind != "image" {
			return badRequest("a git source only applies to kind \"image\" (not %q)", svc.Kind)
		}
		g := *next.Git
		if err := validateGitSource(&g); err != nil {
			return err
		}
		gitNext = &g
		if svc.Git == nil {
			next.Image, next.RegistryCredID = "", ""
		}
	}
	if gitNext != nil {
		nodeID, err := s.serviceNode(r.Context(), svc)
		if err != nil {
			return err
		}
		if !s.hub.Online(nodeID) {
			return errf(409, "server_offline", "%v; builds from source run on the service's server", ErrNodeOffline)
		}
	}
	if err := checkHostPaths(next.Volumes, svc.Volumes); err != nil {
		return err
	}
	if err := validateServiceSpecKeeping(&next, svc.Ports); err != nil {
		return err
	}
	if next.CPULimit != svc.CPULimit || next.MemLimitMB != svc.MemLimitMB { // quotas.go
		if err := s.quotaLimits(r.Context(), next); err != nil {
			return err
		}
		if err := s.checkQuota(r.Context(), api.MemberUsage{CPU: next.CPULimit, MemMB: next.MemLimitMB}, api.MemberUsage{CPU: svc.CPULimit, MemMB: svc.MemLimitMB}); err != nil {
			return err
		}
	}
	if err := s.st.UpdateServiceSpec(r.Context(), next); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return errf(409, "conflict", "a service named %q already exists in this project", next.Name)
		}
		return err
	}
	newGit := false
	switch {
	case gitNext != nil:
		newGit = svc.Git == nil
		if _, err := s.saveGitSource(r.Context(), svc.ID, *gitNext, newGit); err != nil {
			return err
		}
	case gitInPatch && next.Git == nil && svc.Git != nil:
		if err := s.st.DeleteServiceGit(r.Context(), svc.ID); err != nil {
			return err
		}
	}
	if _, err := s.deploy(r.Context(), svc.ID, "redeploy"); err != nil {
		return err
	}
	if _, has := raw["ports"]; has {
		if nodeID, err := s.serviceNode(r.Context(), svc); err == nil {
			s.pushIngress(r.Context(), nodeID)
		}
	}
	s.goBG(func(bg context.Context) { s.pushMeshAll(bg) }) // name or ports may have changed
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "service.update", svc.ID, keys(raw))
	out, _, err := s.st.GetService(r.Context(), svc.ID)
	if err != nil {
		return err
	}
	if newGit { // first switch to git: the webhook secret is shown once, here
		if g, err := s.st.GetServiceGit(r.Context(), svc.ID); err == nil {
			wh := s.webhookInfo(r.Context(), r, svc.ID, g, true)
			out.Webhook = &wh
		}
	}
	writeJSON(w, 200, out)
	return nil
}

func keys(m map[string]json.RawMessage) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (s *Server) handleDeleteService(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return s.requireConfirm(w, r, "service.delete", svc.ID, fmt.Sprintf("Delete service %s (container, volumes, domains)", svc.Name), nil,
		func(ctx context.Context) (int, any, error) {
			return 204, nil, s.execDeleteService(ctx, svc.ID, principalFrom(r.Context()).actor())
		})
}

func (s *Server) execDeleteService(ctx context.Context, id, actor string) error {
	svc, _, err := s.st.GetService(ctx, id)
	if err != nil {
		return err
	}
	nodeID, err := s.serviceNode(ctx, svc)
	if err != nil {
		return err
	}
	release := s.teardownServices(ctx, []serviceOnNode{{svc: svc, node: nodeID}}, false)
	defer release()
	if err := s.st.DeleteService(ctx, id); err != nil {
		return err
	}
	s.pushIngress(ctx, nodeID)
	s.goBG(func(bg context.Context) { s.pushMeshAll(bg) })
	s.st.Audit(ctx, actor, "service.delete", id, map[string]string{"name": svc.Name})
	s.publish("service", map[string]any{"id": id, "project_id": svc.ProjectID, "deleted": true})
	return nil
}

func (s *Server) handleServiceAction(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	out, err := s.serviceAction(r.Context(), svc, req.Action)
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "service."+req.Action, svc.ID, nil)
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req struct {
		DeploymentID string `json:"deployment_id"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	old, err := s.st.GetDeployment(r.Context(), req.DeploymentID)
	if err != nil || old.ServiceID != svc.ID {
		return badRequest("deployment %q does not belong to this service", req.DeploymentID)
	}
	spec, err := s.st.DeploymentSpec(r.Context(), old.ID)
	if err != nil {
		return err
	}
	if spec.Image == "" {
		return badRequest("deployment %s has no image to roll back to (its build did not finish)", old.ID)
	}
	// A rollback restores the old limits too: under a member quota that must not raise them
	// past it or take them away (quotas.go).
	if spec.CPULimit != svc.CPULimit || spec.MemLimitMB != svc.MemLimitMB {
		next := *svc
		next.CPULimit, next.MemLimitMB = spec.CPULimit, spec.MemLimitMB
		if err := s.quotaLimits(r.Context(), next); err != nil {
			return err
		}
		if err := s.checkQuota(r.Context(), api.MemberUsage{CPU: next.CPULimit, MemMB: next.MemLimitMB}, api.MemberUsage{CPU: svc.CPULimit, MemMB: svc.MemLimitMB}); err != nil {
			return err
		}
	}
	// Restore the service's spec fields so later redeploys keep the rolled-back version.
	svc.Image = spec.Image
	svc.Ports, svc.Volumes, svc.Configs, svc.Health = spec.Ports, spec.Volumes, spec.Configs, spec.Health
	svc.CPULimit, svc.MemLimitMB, svc.User = spec.CPULimit, spec.MemLimitMB, spec.User
	if err := s.st.UpdateServiceSpec(r.Context(), *svc); err != nil {
		return err
	}
	spec.Image = pinRollbackImage(spec.Image, old.ImageDigest) // a tag may have moved since (imageupdates.go)
	spec.DeploymentID = newID("dep")
	d, err := s.deploySpec(r.Context(), svc, *spec, "rollback", []string{"rollback to " + old.ID})
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "service.rollback", svc.ID, map[string]string{"to": old.ID})
	writeJSON(w, 200, d)
	return nil
}

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.st.GetService(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	ds, err := s.st.ListDeployments(r.Context(), r.PathValue("id"), 50)
	if err != nil {
		return err
	}
	writeJSON(w, 200, ds)
	return nil
}

func (s *Server) handleServiceMetrics(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.st.GetService(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	since, step, err := rangeStep(r.URL.Query().Get("range"), 24*time.Hour)
	if err != nil {
		return err
	}
	ms, err := s.st.ContainerMetricsRange(r.Context(), r.PathValue("id"), since, step)
	if err != nil {
		return err
	}
	writeJSON(w, 200, ms)
	return nil
}

// handleServiceOrder saves the order of a project's services (PUT /projects/{id}/service-order
// {"services": [ids first to last]}); ids that are not services of the project are refused.
func (s *Server) handleServiceOrder(w http.ResponseWriter, r *http.Request) error {
	p, err := s.st.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req struct {
		Services []string `json:"services"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	svcs, err := s.st.ListServices(r.Context(), p.ID)
	if err != nil {
		return err
	}
	own := map[string]bool{}
	for _, v := range svcs {
		own[v.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range req.Services {
		if !own[id] {
			return badRequest("service %q is not in project %s", id, p.ID)
		}
		if seen[id] {
			return badRequest("service %q is listed twice", id)
		}
		seen[id] = true
	}
	if err := s.st.SetServiceOrder(r.Context(), p.ID, req.Services); err != nil {
		return err
	}
	if p.Services, err = s.servicesWithDomains(r.Context(), p.ID); err != nil {
		return err
	}
	s.publish("project", p)
	writeJSON(w, 200, p)
	return nil
}
