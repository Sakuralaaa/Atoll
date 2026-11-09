package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) error {
	ps, err := s.st.ListProjects(r.Context(), r.URL.Query().Get("server_id"))
	if err != nil {
		return err
	}
	pr := principalFrom(r.Context())
	ps = slices.DeleteFunc(ps, func(x api.Project) bool { return !pr.inProject(x.ID) })
	for i := range ps {
		if ps[i].Services, err = s.servicesWithDomainsFor(r.Context(), ps[i].ID, pr); err != nil {
			return err
		}
	}
	writeJSON(w, 200, ps)
	return nil
}

func (s *Server) servicesWithDomains(ctx context.Context, projectID string) ([]api.Service, error) {
	return s.servicesWithDomainsFor(ctx, projectID, nil)
}

// servicesWithDomainsFor filters nested services and domains to the caller's actual server
// assignments. A project can contain services placed on different servers.
func (s *Server) servicesWithDomainsFor(ctx context.Context, projectID string, pr *principal) ([]api.Service, error) {
	svcs, err := s.st.ListServices(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if pr != nil && pr.Member {
		svcs = slices.DeleteFunc(svcs, func(v api.Service) bool { return !memberSeesService(pr, &v) })
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
		DisplayName string `json:"display_name"`
		ServerID    string `json:"server_id"`
		Description string `json:"description"`
		RootDomain  string `json:"root_domain"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	dn, err := checkDisplayName(req.DisplayName)
	if err != nil {
		return err
	}
	root, err := s.checkProjectRoot(r.Context(), req.RootDomain) // rootdomains.go
	if err != nil {
		return err
	}
	if err := s.checkMemberRoot(r.Context(), root); err != nil { // member_domains.go
		return err
	}
	if !memberMayUse(r, req.ServerID) {
		return badRequest("unknown server_id %q", req.ServerID)
	}
	p, err := s.createProject(r.Context(), req.Name, req.ServerID, req.Description, false)
	if err != nil {
		return err
	}
	if dn != "" {
		p.DisplayName = dn
		if err := s.st.UpdateProject(r.Context(), p.ID, p.Name, p.DisplayName, p.Description); err != nil {
			return err
		}
	}
	if root != "" {
		p.RootDomain = root
		if err := s.st.SetProjectRootDomain(r.Context(), p.ID, root); err != nil {
			return err
		}
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
	if p.Services, err = s.servicesWithDomainsFor(r.Context(), p.ID, principalFrom(r.Context())); err != nil {
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
		DisplayName *string `json:"display_name"`
		Description *string `json:"description"`
		ServerID    *string `json:"server_id"`
		RootDomain  *string `json:"root_domain"`
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
	if req.DisplayName != nil {
		if p.DisplayName, err = checkDisplayName(*req.DisplayName); err != nil {
			return err
		}
	}
	if req.Description != nil {
		p.Description = strings.TrimSpace(*req.Description)
	}
	if req.RootDomain != nil {
		was := p.RootDomain
		if p.RootDomain, err = s.checkProjectRoot(r.Context(), *req.RootDomain); err != nil {
			return err
		}
		if p.RootDomain != was { // a member keeps a root an admin chose, but cannot pick one it was not given
			if err := s.checkMemberRoot(r.Context(), p.RootDomain); err != nil {
				return err
			}
		}
	}
	if err := s.st.UpdateProject(r.Context(), p.ID, p.Name, p.DisplayName, p.Description); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return errf(409, "conflict", "a project named %q already exists", p.Name)
		}
		return err
	}
	if req.RootDomain != nil {
		if err := s.st.SetProjectRootDomain(r.Context(), p.ID, p.RootDomain); err != nil {
			return err
		}
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
	pr := principalFrom(r.Context())
	if err := s.memberProjectServicesVisible(r.Context(), pr, p.ID); err != nil {
		return err
	}
	svcs, err := s.st.ListServices(r.Context(), p.ID)
	if err != nil {
		return err
	}
	return s.requireConfirm(w, r, "project.delete", p.ID,
		fmt.Sprintf("Delete project %s and its %d service(s), volumes and DNS records", p.Name, len(svcs)), nil,
		func(ctx context.Context) (int, any, error) {
			if err := s.memberProjectServicesVisible(ctx, pr, p.ID); err != nil {
				return 0, nil, err
			}
			return 204, nil, s.execDeleteProject(ctx, p.ID, pr.actor())
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
	nodes := map[string]bool{p.ServerID: true}
	var targets []serviceOnNode
	for i := range svcs {
		node := svcs[i].ServerID
		if node == "" {
			node = p.ServerID
		}
		nodes[node] = true
		targets = append(targets, serviceOnNode{svc: &svcs[i], node: node})
	}
	release := s.teardownServices(ctx, targets, false)
	defer release()
	if err := s.st.DeleteProject(ctx, id); err != nil {
		return err
	}
	for n := range nodes {
		s.pushIngress(ctx, n)
	}
	s.goBG(func(bg context.Context) { s.pushMeshAll(bg) })
	s.st.Audit(ctx, actor, "project.delete", id, map[string]string{"name": p.Name})
	s.publish("project", map[string]any{"id": id, "deleted": true})
	return nil
}

type serviceOnNode struct {
	svc  *api.Service
	node string
}

// teardownServices takes services off the air in the safe order (best effort; failures are
// logged): their hostnames leave the route tables first (held, then the tables are pushed to
// each node and acknowledged), then the containers are removed, and only then are the DNS
// records deleted — so a record never outlives the route by sending visitors (or a CA) to a
// Caddy that is still asking for a certificate, and a route never points at a name that no
// longer resolves. The returned func releases the holds; call it once the rows are deleted.
func (s *Server) teardownServices(ctx context.Context, svcs []serviceOnNode, keepVolumes bool) (release func()) {
	var doms []store.DomainRow
	var hosts []string
	nodes := map[string]bool{}
	for _, t := range svcs {
		ds, _ := s.st.ListDomains(ctx, t.svc.ID, "")
		for _, d := range ds {
			doms = append(doms, d)
			hosts = append(hosts, d.Hostname)
		}
		nodes[t.node] = true
	}
	release = s.holdRoutes(hosts)
	if len(hosts) > 0 {
		for n := range nodes {
			if !s.hub.Online(n) {
				continue
			}
			if err := s.pushIngressSync(ctx, n); err != nil {
				s.log.Warn("teardown: route removal push failed", "node", n, "err", err)
			}
		}
	}
	for _, t := range svcs {
		if !s.hub.Online(t.node) {
			continue
		}
		if _, err := s.hub.Call(ctx, t.node, api.MsgRemove, api.RemovePayload{ServiceID: t.svc.ID, KeepVolumes: keepVolumes}, 2*s.cfg.RequestTimeout); err != nil {
			s.log.Warn("agent remove", "service", t.svc.ID, "err", err)
		}
	}
	prov := s.dnsProvider(ctx)
	for _, d := range doms {
		if err := prov.Delete(ctx, d.Hostname, d.RecordID); err != nil && !errors.Is(err, dns.ErrNotConfigured) {
			s.log.Warn("dns delete", "host", d.Hostname, "err", err)
		}
	}
	return release
}
