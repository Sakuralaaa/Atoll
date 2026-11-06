package server

import (
	"net/http"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/templates"
)

// One-click apps (templates/apps.go): GET lists them, POST creates a project with the
// app's services, binds an optional domain to the main service and deploys everything.

func (s *Server) handleListAppTemplates(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, templates.Apps())
	return nil
}

func (s *Server) handleCreateFromAppTemplate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	tpl, ok := templates.App(r.PathValue("id"))
	if !ok {
		return errf(404, "not_found", "unknown app template %q", r.PathValue("id"))
	}
	var req api.AppTemplateRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	req.Hostname = strings.TrimSpace(req.Hostname)
	if !memberMayUse(r, req.ServerID) {
		return badRequest("unknown server_id %q", req.ServerID) // same answer as POST /projects
	}
	if req.Name == "" {
		req.Name = tpl.ID
	}
	if tpl.NeedsDomain && req.Hostname == "" {
		return badRequest("%s needs a domain: it only works behind its public HTTPS address", tpl.Name)
	}
	extra := map[string]string{}
	for name, h := range req.Domains {
		if h = strings.TrimSpace(h); h == "" {
			continue
		}
		ok := false
		for _, ts := range tpl.Services {
			ok = ok || (ts.Name == name && ts.Domain)
		}
		if !ok {
			return badRequest("domains: service %q of %s takes no domain", name, tpl.Name)
		}
		extra[name] = h
	}
	p := principalFrom(ctx)
	// an admin session may bind; a member session and every token need the domains
	// permission (a member's own permissions, notes/subaccounts.md)
	if (req.Hostname != "" || len(extra) > 0) && (!p.Session || p.Member) {
		if !p.Perms[api.PermDomains] {
			return errf(403, "missing_permission", "binding a domain needs the \"domains\" permission; leave hostname empty and bind it later")
		}
		if p.RequireApproval {
			return errf(403, "approval_required", "this token needs approval for domain changes; leave hostname empty and bind the domain separately")
		}
	}
	if err := s.checkMemberTemplateHosts(ctx, req.Hostname, req.RootDomain, extra); err != nil {
		return err // before the project exists, so a refused domain leaves nothing behind
	}
	inputs, err := templates.ResolveInputs(tpl, req.Inputs)
	if err != nil {
		return badRequest("%s", err.Error())
	}
	dn, err := checkDisplayName(req.DisplayName)
	if err != nil {
		return err
	}
	proj, err := s.createProject(ctx, req.Name, req.ServerID, tpl.Name, false)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		_ = s.execDeleteProject(ctx, proj.ID, "system")
		return err
	}
	if dn != "" {
		proj.DisplayName = dn
		if err := s.st.UpdateProject(ctx, proj.ID, proj.Name, proj.DisplayName, proj.Description); err != nil {
			return fail(err)
		}
	}
	out := api.AppTemplateResult{Project: proj}
	mainID := ""
	extraIDs := map[string]string{} // service name -> id, for req.Domains
	for _, ts := range tpl.Services {
		v := api.Service{Name: ts.Name, Kind: "image", Image: ts.Image, Command: ts.Command, MemLimitMB: ts.MemLimitMB,
			Ports: append([]api.Port(nil), ts.Ports...), Volumes: append([]api.Volume(nil), ts.Volumes...)}
		for _, c := range ts.Configs {
			v.Configs = append(v.Configs, api.ConfigFile{Path: c.Path, Content: templates.Fill(c.Content, inputs)})
		}
		if ts.Template != "" {
			v.Kind = ts.Template
		}
		vars := append([]api.EnvVar(nil), ts.Env...)
		if req.Hostname != "" {
			vars = append(vars, ts.DomainEnv...)
		}
		for i := range vars {
			vars[i].Value = templates.Fill(vars[i].Value, inputs)
		}
		svc, err := s.createService(ctx, proj, v, vars, true)
		if err != nil {
			return fail(err)
		}
		if ts.Main {
			mainID = svc.ID
		}
		if ts.Domain {
			extraIDs[ts.Name] = svc.ID
		}
		out.Services = append(out.Services, *svc)
	}
	// Bound before the first deploy, so the services start with ${ATOLL_WEB_URL} resolved.
	if req.Hostname != "" && mainID != "" {
		d, err := s.execAddDomain(ctx, mainID, domainRequest{Hostname: req.Hostname, RootDomain: req.RootDomain, PortName: "web", Proxied: req.Proxied}, p.actor())
		if err != nil {
			out.DomainError = err.Error()
		} else {
			out.Domain = d
		}
	}
	for _, ts := range tpl.Services {
		h, ok := extra[ts.Name]
		if !ok || extraIDs[ts.Name] == "" {
			continue
		}
		port := ts.DomainPort
		if port == "" {
			for _, pt := range ts.Ports {
				if pt.Protocol == "http" {
					port = pt.Name
					break
				}
			}
		}
		root := req.RootDomain
		if strings.Contains(h, ".") {
			root = "" // a full hostname (the UI sends these), not a prefix of req.RootDomain
		}
		d, err := s.execAddDomain(ctx, extraIDs[ts.Name], domainRequest{Hostname: h, RootDomain: root, PortName: port, Proxied: req.Proxied}, p.actor())
		if err != nil {
			if out.DomainErrors == nil {
				out.DomainErrors = map[string]string{}
			}
			out.DomainErrors[ts.Name] = err.Error()
		} else {
			out.ExtraDomains = append(out.ExtraDomains, *d)
		}
	}
	for _, svc := range out.Services {
		if _, err := s.deploy(ctx, svc.ID, "create"); err != nil {
			s.log.Warn("app template: deploy", "service", svc.ID, "err", err)
		}
	}
	s.st.Audit(ctx, p.actor(), "project.create", proj.ID, map[string]string{"name": proj.Name, "template": tpl.ID})
	writeJSON(w, 201, out)
	return nil
}
