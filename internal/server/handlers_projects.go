package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
	"github.com/Sakuralaaa/Atoll/internal/server/templates"
)

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) error {
	ps, err := s.st.ListProjects(r.Context(), r.URL.Query().Get("server_id"))
	if err != nil {
		return err
	}
	for i := range ps {
		if ps[i].Services, err = s.servicesWithDomains(r.Context(), ps[i].ID); err != nil {
			return err
		}
	}
	writeJSON(w, 200, ps)
	return nil
}

func (s *Server) servicesWithDomains(ctx context.Context, projectID string) ([]api.Service, error) {
	svcs, err := s.st.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	doms, err := s.st.ListDomains(ctx, "", projectID)
	if err != nil {
		return nil, err
	}
	for i := range svcs {
		for _, d := range doms {
			if d.ServiceID == svcs[i].ID {
				svcs[i].Domains = append(svcs[i].Domains, d.Domain)
			}
		}
	}
	return svcs, nil
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name        string `json:"name"`
		ServerID    string `json:"server_id"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	p, err := s.createProject(r.Context(), req.Name, req.ServerID, req.Description, false)
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "project.create", p.ID, map[string]string{"name": p.Name})
	writeJSON(w, 201, p)
	return nil
}

func (s *Server) createProject(ctx context.Context, name, serverID, desc string, hold bool) (*api.Project, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 {
		return nil, badRequest("project name must be 1-100 chars")
	}
	if serverID == "" {
		ns, err := s.st.ListNodes(ctx)
		if err != nil {
			return nil, err
		}
		if len(ns) != 1 {
			return nil, badRequest("server_id is required (%d servers registered)", len(ns))
		}
		serverID = ns[0].ID
	}
	if _, err := s.st.GetNode(ctx, serverID); err != nil {
		return nil, badRequest("unknown server_id %q", serverID)
	}
	p := api.Project{ID: newID("prj"), Name: name, Description: strings.TrimSpace(desc), ServerID: serverID, CreatedAt: time.Now().UTC()}
	if err := s.st.CreateProject(ctx, p, hold); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil, errf(409, "conflict", "a project named %q already exists", name)
		}
		return nil, err
	}
	s.publish("project", p)
	return &p, nil
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.st.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if p.Services, err = s.servicesWithDomains(r.Context(), p.ID); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}

func (s *Server) handlePatchProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.st.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		ServerID    *string `json:"server_id"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.ServerID != nil && *req.ServerID != p.ServerID {
		return badRequest("to move a project to another server use POST /api/v1/projects/%s/migrate", p.ID)
	}
	if req.Name != nil {
		p.Name = strings.TrimSpace(*req.Name)
		if p.Name == "" || len(p.Name) > 100 {
			return badRequest("project name must be 1-100 chars")
		}
	}
	if req.Description != nil {
		p.Description = strings.TrimSpace(*req.Description)
	}
	if err := s.st.UpdateProject(r.Context(), p.ID, p.Name, p.Description); err != nil {
		return err
	}
	s.publish("project", p)
	writeJSON(w, 200, p)
	return nil
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) error {
	p, err := s.st.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	svcs, _ := s.st.ListServices(r.Context(), p.ID)
	return s.requireConfirm(w, r, "project.delete", p.ID,
		fmt.Sprintf("Delete project %s and its %d service(s), volumes and DNS records", p.Name, len(svcs)), nil,
		func(ctx context.Context) (int, any, error) {
			return 204, nil, s.execDeleteProject(ctx, p.ID, principalFrom(r.Context()).actor())
		})
}

func (s *Server) execDeleteProject(ctx context.Context, id, actor string) error {
	p, err := s.st.GetProject(ctx, id)
	if err != nil {
		return err
	}
	svcs, err := s.st.ListServices(ctx, id)
	if err != nil {
		return err
	}
	for i := range svcs {
		s.teardownService(ctx, &svcs[i], p.ServerID, false)
	}
	if err := s.st.DeleteProject(ctx, id); err != nil {
		return err
	}
	s.pushIngress(ctx, p.ServerID)
	s.st.Audit(ctx, actor, "project.delete", id, map[string]string{"name": p.Name})
	s.publish("project", map[string]any{"id": id, "deleted": true})
	return nil
}

// teardownService removes DNS records and the container (best effort; logs failures).
func (s *Server) teardownService(ctx context.Context, svc *api.Service, nodeID string, keepVolumes bool) {
	doms, _ := s.st.ListDomains(ctx, svc.ID, "")
	prov := s.dnsProvider(ctx)
	for _, d := range doms {
		if err := prov.Delete(ctx, d.Hostname, d.RecordID); err != nil && err != dns.ErrNotConfigured {
			s.log.Warn("dns delete", "host", d.Hostname, "err", err)
		}
	}
	if s.hub.Online(nodeID) {
		if _, err := s.hub.Call(ctx, nodeID, api.MsgRemove, api.RemovePayload{ServiceID: svc.ID, KeepVolumes: keepVolumes}, 2*s.cfg.RequestTimeout); err != nil {
			s.log.Warn("agent remove", "service", svc.ID, "err", err)
		}
	}
}

// ---------- services ----------

type createServiceRequest struct {
	api.Service
	Env []api.EnvVar `json:"env,omitempty"` // optional initial env (extension)
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
	svc, err := s.createService(r.Context(), p, req.Service, req.Env, true)
	if err != nil {
		return err
	}
	if _, err := s.deploy(r.Context(), svc.ID, "create"); err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "service.create", svc.ID, map[string]string{"name": svc.Name, "image": svc.Image})
	svc, _, err = s.st.GetService(r.Context(), svc.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 201, svc)
	return nil
}

// createService validates and stores a service (template kinds get image,
// ports, volumes and env from the template). It does not deploy.
func (s *Server) createService(ctx context.Context, p *api.Project, v api.Service, env []api.EnvVar, applyTemplate bool) (*api.Service, error) {
	v.Name = strings.ToLower(strings.TrimSpace(v.Name))
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
	if err := validateServiceSpec(&v); err != nil {
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
	for _, f := range []string{"id", "project_id", "kind", "status", "replicas", "created_at", "domains"} {
		delete(raw, f)
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
	if err := validateServiceSpec(&next); err != nil {
		return err
	}
	if err := s.st.UpdateServiceSpec(r.Context(), next); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return errf(409, "conflict", "a service named %q already exists in this project", next.Name)
		}
		return err
	}
	if _, err := s.deploy(r.Context(), svc.ID, "redeploy"); err != nil {
		return err
	}
	if _, has := raw["ports"]; has {
		if nodeID, err := s.serviceNode(r.Context(), svc); err == nil {
			s.pushIngress(r.Context(), nodeID)
		}
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "service.update", svc.ID, keys(raw))
	out, _, err := s.st.GetService(r.Context(), svc.ID)
	if err != nil {
		return err
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
	s.teardownService(ctx, svc, nodeID, false)
	if err := s.st.DeleteService(ctx, id); err != nil {
		return err
	}
	s.pushIngress(ctx, nodeID)
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
	// Restore the service's spec fields so later redeploys keep the rolled-back version.
	svc.Image = spec.Image
	svc.Ports, svc.Volumes, svc.Configs, svc.Health = spec.Ports, spec.Volumes, spec.Configs, spec.Health
	svc.CPULimit, svc.MemLimitMB = spec.CPULimit, spec.MemLimitMB
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
