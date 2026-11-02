package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/envresolve"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// containerName is the docker container name for a service.
func containerName(serviceID string) string { return "atoll-" + serviceID }

// buildSpec resolves the full desired spec of svc. Warnings come from env resolution.
func (s *Server) buildSpec(ctx context.Context, svc *api.Service, deploymentID string) (api.ResolvedSpec, []string, error) {
	doms, err := s.st.ListDomains(ctx, "", svc.ProjectID)
	if err != nil {
		return api.ResolvedSpec{}, nil, err
	}
	return s.buildSpecWith(ctx, svc, deploymentID, doms)
}

// buildSpecWith is buildSpec with the project's domains given (all domains of the project).
func (s *Server) buildSpecWith(ctx context.Context, svc *api.Service, deploymentID string, doms []store.DomainRow) (api.ResolvedSpec, []string, error) {
	svcs, err := s.st.ListServices(ctx, svc.ProjectID)
	if err != nil {
		return api.ResolvedSpec{}, nil, err
	}
	in := envresolve.Input{Self: svc.Name}
	for _, o := range svcs {
		if o.ID == svc.ID {
			o = *svc // use the in-flight (possibly patched) version
		}
		vars, err := s.st.ListEnv(ctx, o.ID)
		if err != nil {
			return api.ResolvedSpec{}, nil, err
		}
		dm := map[string]string{}
		for _, d := range doms {
			if d.ServiceID == o.ID && dm[d.PortName] == "" {
				dm[d.PortName] = d.Hostname
			}
		}
		in.Services = append(in.Services, envresolve.Service{Name: o.Name, Vars: vars, Ports: o.Ports, Domains: dm})
	}
	res := envresolve.Resolve(in)
	warnings := res.Warnings
	expand := func(list []string) []string {
		if list == nil {
			return nil
		}
		out := make([]string, len(list))
		for i, a := range list {
			v, w := envresolve.Expand(in, a)
			out[i] = v
			warnings = append(warnings, w...)
		}
		return out
	}
	spec := api.ResolvedSpec{
		ServiceID:    svc.ID,
		ProjectID:    svc.ProjectID,
		DeploymentID: deploymentID,
		Name:         svc.Name,
		Network:      "atoll-" + svc.ProjectID,
		Image:        svc.Image,
		Command:      expand(svc.Command),
		Args:         expand(svc.Args),
		Env:          res.Env,
		Ports:        svc.Ports,
		Volumes:      svc.Volumes,
		Configs:      svc.Configs,
		Health:       svc.Health,
		CPULimit:     svc.CPULimit,
		MemLimitMB:   svc.MemLimitMB,
	}
	if spec.Ports == nil {
		spec.Ports = []api.Port{}
	}
	if spec.Volumes == nil {
		spec.Volumes = []api.Volume{}
	}
	if svc.RegistryCredID != "" {
		var cred api.RegistryAuth
		if err := s.st.GetSecretBlob(ctx, "registry_cred:"+svc.RegistryCredID, &cred); err == nil {
			spec.RegistryAuth = &cred
		} else {
			warnings = append(warnings, fmt.Sprintf("registry credential %s not found; pulling anonymously", svc.RegistryCredID))
		}
	}
	return spec, dedupe(warnings), nil
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// deploy creates a Deployment for svc with a freshly resolved spec and pushes it.
func (s *Server) deploy(ctx context.Context, serviceID, trigger string) (*api.Deployment, error) {
	return s.deployCommit(ctx, serviceID, trigger, "")
}

// deployCommit is deploy; for a service built from git, commit ("" = see prepareBuild) is
// the commit to build (a webhook push).
func (s *Server) deployCommit(ctx context.Context, serviceID, trigger, commit string) (*api.Deployment, error) {
	svc, _, err := s.st.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	depID := newID("dep")
	spec, warnings, err := s.buildSpec(ctx, svc, depID)
	if err != nil {
		return nil, err
	}
	var build *api.BuildPayload
	if svc.Git != nil {
		node, err := s.serviceNode(ctx, svc)
		if err != nil {
			return nil, err
		}
		if build, err = s.prepareBuild(ctx, svc, &spec, node, trigger, commit); err != nil {
			return nil, err
		}
	}
	return s.deploySpecBuild(ctx, svc, spec, trigger, warnings, build)
}

// deploySpec records and sends a prepared spec (used directly by rollback).
func (s *Server) deploySpec(ctx context.Context, svc *api.Service, spec api.ResolvedSpec, trigger string, warnings []string) (*api.Deployment, error) {
	return s.deploySpecBuild(ctx, svc, spec, trigger, warnings, nil)
}

// deploySpecBuild is deploySpec with an optional build (git.go) that runs on the service's
// server before the apply and fills spec.Image.
func (s *Server) deploySpecBuild(ctx context.Context, svc *api.Service, spec api.ResolvedSpec, trigger string, warnings []string, build *api.BuildPayload) (*api.Deployment, error) {
	cur, stopped, err := s.st.GetService(ctx, svc.ID)
	if err != nil {
		return nil, err
	}
	node, err := s.serviceNode(ctx, cur) // current placement (a migration may just have moved it)
	if err != nil {
		return nil, err
	}
	online := s.hub.Online(node)
	if build != nil && !online {
		return nil, errf(409, "server_offline", "%v; builds from source run on the service's server, deploy again once it is back", ErrNodeOffline)
	}
	if stopped {
		// An explicit deploy means the user wants it running again.
		s.st.SetDesiredStopped(ctx, svc.ID, false)
	}
	spec.Stopped = false
	d := api.Deployment{
		ID: spec.DeploymentID, ServiceID: svc.ID, ServerID: node, Status: "queued", Trigger: trigger,
		Message: strings.Join(warnings, "\n"), StartedAt: time.Now().UTC(),
	}
	if !online {
		d.Message = strings.TrimSpace("server offline; will apply when its agent reconnects\n" + d.Message)
	}
	if err := s.st.CreateDeployment(ctx, d, spec); err != nil {
		return nil, err
	}
	s.st.SetServiceStatus(ctx, svc.ID, "deploying", "")
	s.publish("deployment", d)
	s.publishService(ctx, svc.ID)
	if online {
		s.enqueueApply(pendingApply{nodeID: node, serviceID: svc.ID, depID: d.ID, spec: spec, build: build})
	}
	return &d, nil
}

// pendingApply is one deployment waiting to be sent to its agent.
type pendingApply struct {
	nodeID, serviceID, depID string
	spec                     api.ResolvedSpec
	build                    *api.BuildPayload // git service: build first (git.go), then apply the built image
}

// applyQueue serializes the applies of one service: at most one is in flight, and of the
// requests made meanwhile only the newest is kept (it carries the latest desired state:
// env, image, config were all re-resolved when it was created). The ones it replaces never
// reach the agent and are marked superseded. So "set_env, then redeploy right away" runs
// the env deploy and then the redeploy, instead of the redeploy being dropped.
type applyQueue struct {
	busy bool
	next *pendingApply
}

func (s *Server) enqueueApply(p pendingApply) {
	s.applyMu.Lock()
	q := s.applyQ[p.serviceID]
	if q == nil {
		q = &applyQueue{}
		s.applyQ[p.serviceID] = q
	}
	if q.busy {
		dropped := q.next
		q.next = &p
		s.applyMu.Unlock()
		if dropped != nil {
			s.supersedeQueued(s.bg, dropped.depID, dropped.serviceID, p.depID)
		}
		return
	}
	q.busy = true
	s.applyMu.Unlock()
	s.goBG(func(bg context.Context) {
		cur := p
		for {
			s.sendApply(bg, cur)
			s.applyMu.Lock()
			next := q.next
			q.next = nil
			if next == nil || bg.Err() != nil {
				q.busy = false
				if q.next == nil {
					delete(s.applyQ, cur.serviceID)
				}
				s.applyMu.Unlock()
				return
			}
			s.applyMu.Unlock()
			cur = *next
		}
	})
}

// sendApply sends one deployment and waits for the agent to finish it.
func (s *Server) sendApply(ctx context.Context, p pendingApply) {
	if d, err := s.st.GetDeployment(ctx, p.depID); err == nil && d.Status == "superseded" {
		return
	}
	if p.build != nil {
		spec, ok := s.runBuild(ctx, p)
		if !ok {
			return
		}
		p.spec = spec
	}
	res, err := s.hub.Request(ctx, p.nodeID, api.MsgApply, api.ApplyPayload{Spec: p.spec}, s.cfg.TransferTimeout)
	if err == nil && res.OK {
		return
	}
	msg := res.Error
	if err != nil {
		msg = err.Error()
	}
	s.failDeployment(ctx, p.depID, p.serviceID, "apply failed: "+msg)
}

// supersedeQueued marks a deployment that was still waiting behind an in-flight one as
// replaced by a newer request; it is never sent.
func (s *Server) supersedeQueued(ctx context.Context, depID, serviceID, byID string) {
	cur, err := s.st.GetDeployment(ctx, depID)
	if err != nil || !inProgress(cur.Status) {
		return
	}
	s.st.UpdateDeployment(ctx, depID, "superseded", "replaced by newer deployment "+byID+" before it started", "")
	if d, err := s.st.GetDeployment(ctx, depID); err == nil {
		s.publish("deployment", d)
	}
	s.publishService(ctx, serviceID)
}

func (s *Server) failDeployment(ctx context.Context, depID, serviceID, msg string) {
	cur, err := s.st.GetDeployment(ctx, depID)
	if err != nil || cur.Status == "running" || cur.Status == "failed" || cur.Status == "superseded" {
		return
	}
	s.st.UpdateDeployment(ctx, depID, "failed", msg, "")
	s.st.SetServiceStatus(ctx, serviceID, "failed", "0/1")
	if d, err := s.st.GetDeployment(ctx, depID); err == nil {
		s.publish("deployment", d)
	}
	s.publishService(ctx, serviceID)
}

// handleDeployEvent applies agent progress for a deployment.
func (s *Server) handleDeployEvent(ctx context.Context, nodeID string, ev api.DeployEvent) {
	d, err := s.st.GetDeployment(ctx, ev.DeploymentID)
	if err != nil {
		s.log.Warn("deploy event for unknown deployment", "deployment", ev.DeploymentID, "node", nodeID)
		return
	}
	switch ev.Status {
	case "queued", "building", "pulling", "starting", "running", "failed":
	default:
		s.log.Warn("unknown deploy status", "status", ev.Status)
		return
	}
	if d.Status == "superseded" {
		return
	}
	if err := s.st.UpdateDeployment(ctx, d.ID, ev.Status, ev.Message, ev.ImageDigest); err != nil {
		s.log.Error("update deployment", "err", err)
		return
	}
	switch ev.Status {
	case "running":
		s.st.SupersedeOthers(ctx, d.ServiceID, d.ID)
		if newer, _ := s.st.NewerInProgress(ctx, d.ServiceID, d.ID); newer {
			s.st.SetServiceStatus(ctx, d.ServiceID, "deploying", "") // the queued newer one runs next
		} else {
			s.st.SetServiceStatus(ctx, d.ServiceID, "running", "1/1")
		}
	case "failed":
		s.st.SetServiceStatus(ctx, d.ServiceID, "failed", "0/1")
	default:
		s.st.SetServiceStatus(ctx, d.ServiceID, "deploying", "")
	}
	if nd, err := s.st.GetDeployment(ctx, d.ID); err == nil {
		s.publish("deployment", nd)
	}
	s.publishService(ctx, d.ServiceID)
}

func inProgress(status string) bool {
	return status == "queued" || status == "building" || status == "pulling" || status == "starting"
}

// handleStatus stores container states and derives service status.
func (s *Server) handleStatus(ctx context.Context, nodeID string, st api.StatusPayload) {
	if err := s.st.ReplaceContainerStates(ctx, nodeID, st.Containers); err != nil {
		s.log.Error("store container states", "err", err)
		return
	}
	for _, c := range st.Containers {
		svc, stopped, err := s.st.GetService(ctx, c.ServiceID)
		if err != nil {
			continue
		}
		if d, err := s.st.LatestDeployment(ctx, svc.ID); err == nil && inProgress(d.Status) {
			continue // deploy events own the status while a deployment runs
		}
		status, replicas := "unknown", "0/1"
		switch c.State {
		case "running":
			status, replicas = "running", "1/1"
			if c.Health == "unhealthy" {
				status = "failed"
			}
		case "exited", "dead":
			status = "failed"
			if stopped {
				status = "stopped"
			}
		case "restarting":
			status = "failed"
		case "created":
			status = "deploying"
		case "missing":
			if stopped {
				status = "stopped"
			}
		}
		if status != svc.Status || replicas != svc.Replicas {
			s.st.SetServiceStatus(ctx, svc.ID, status, replicas)
			s.publishService(ctx, svc.ID)
		}
	}
}

func (s *Server) publishService(ctx context.Context, id string) {
	if svc, _, err := s.st.GetService(ctx, id); err == nil {
		s.publish("service", svc)
	}
}

// ---------- node state pushes ----------

// ingressFor builds the Caddy route table for a node.
func (s *Server) ingressFor(ctx context.Context, nodeID string) (api.IngressPayload, error) {
	rows, err := s.st.RoutesForNode(ctx, nodeID)
	if err != nil {
		return api.IngressPayload{}, err
	}
	mode, _ := s.st.GetSetting(ctx, "tls_mode")
	if mode == "" {
		mode = "cloudflare-origin"
	}
	email, _ := s.st.GetSetting(ctx, "acme_email")
	out := api.IngressPayload{Routes: []api.IngressRoute{}, TLSMode: mode, ACMEEmail: email, CloudflareOnly: mode == "cloudflare-origin"}
	for _, r := range rows {
		if s.routeHeld(r.Hostname) {
			continue // DNS record not visible yet, or being unbound (provision.go)
		}
		port := 0
		for _, p := range r.Ports {
			if p.Protocol != "http" {
				continue
			}
			if p.Name == r.PortName || (r.PortName == "" && port == 0) {
				port = p.ContainerPort
			}
		}
		if port == 0 {
			continue
		}
		out.Routes = append(out.Routes, api.IngressRoute{Hostname: r.Hostname, Upstream: fmt.Sprintf("%s:%d", containerName(r.ServiceID), port), ServiceID: r.ServiceID})
	}
	return out, nil
}

// pushIngress sends the route table to a node (async, best effort).
func (s *Server) pushIngress(ctx context.Context, nodeID string) {
	ing, err := s.ingressFor(ctx, nodeID)
	if err != nil {
		s.log.Error("ingress", "err", err)
		return
	}
	s.hub.Fire(nodeID, api.MsgIngress, ing)
}

// pushIngressSync sends the route table and waits for the agent's answer. An agent that
// holds atoll-caddy back because Zeabur/k3s still owns 80/443 (before a cutover) keeps the
// table and applies it once the ports are free, so that answer counts as success here.
func (s *Server) pushIngressSync(ctx context.Context, nodeID string) error {
	err := s.pushIngressStrict(ctx, nodeID)
	if isIngressHeld(err) {
		s.log.Info("ingress held on the agent until the Zeabur cutover frees 80/443; routes saved there", "node", nodeID)
		return nil
	}
	return err
}

// pushIngressStrict is pushIngressSync without the held exception (the cutover needs
// atoll-caddy actually serving).
func (s *Server) pushIngressStrict(ctx context.Context, nodeID string) error {
	ing, err := s.ingressFor(ctx, nodeID)
	if err != nil {
		return err
	}
	_, err = s.hub.Call(ctx, nodeID, api.MsgIngress, ing, 0)
	return err
}

// isIngressHeld reports the agent's ingress guard refusal ("not starting atoll-caddy: …").
func isIngressHeld(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not starting atoll-caddy")
}

func (s *Server) tlsPayload(ctx context.Context) *api.TLSPayload {
	cert, _ := s.st.GetSetting(ctx, "origin_cert_pem")
	key, _ := s.st.GetSetting(ctx, "origin_key_pem")
	if cert == "" || key == "" {
		return nil
	}
	return &api.TLSPayload{CertPEM: cert, KeyPEM: key}
}

// pushNodeState sends sync, ingress and TLS to a freshly connected agent.
func (s *Server) pushNodeState(ctx context.Context, nodeID string) {
	specs, err := s.st.DesiredSpecsForNode(ctx, nodeID)
	if err != nil {
		s.log.Error("sync specs", "err", err)
		return
	}
	if _, err := s.hub.Call(ctx, nodeID, api.MsgSync, api.SyncPayload{Specs: specs}, 2*s.cfg.RequestTimeout); err != nil {
		s.log.Warn("sync failed", "node", nodeID, "err", err)
	}
	if tls := s.tlsPayload(ctx); tls != nil {
		if _, err := s.hub.Call(ctx, nodeID, api.MsgTLS, tls, 0); err != nil {
			s.log.Warn("tls push failed", "node", nodeID, "err", err)
		}
	}
	if err := s.pushIngressSync(ctx, nodeID); err != nil {
		s.log.Warn("ingress push failed", "node", nodeID, "err", err)
	}
	s.pushBackupConfig(ctx, nodeID, true) // backups.go: agent schedules backups locally
	s.pushMeshAll(ctx)                    // mesh.go: cross-server service names (this node's tailnet IP may have changed)
}

// serviceNode returns the node a service runs on.
func (s *Server) serviceNode(ctx context.Context, svc *api.Service) (string, error) {
	if svc.ServerID != "" {
		return svc.ServerID, nil // effective placement filled by the store
	}
	p, err := s.st.GetProject(ctx, svc.ProjectID)
	if err != nil {
		return "", err
	}
	return p.ServerID, nil
}

// serviceAction handles start/stop/restart/redeploy.
func (s *Server) serviceAction(ctx context.Context, svc *api.Service, action string) (any, error) {
	nodeID, err := s.serviceNode(ctx, svc)
	if err != nil {
		return nil, err
	}
	switch action {
	case "redeploy":
		return s.deploy(ctx, svc.ID, "redeploy")
	case "start", "stop", "restart":
	default:
		return nil, badRequest("action must be start, stop, restart or redeploy")
	}
	if action == "start" {
		if _, err := s.st.LatestDeployment(ctx, svc.ID); err == store.ErrNotFound {
			return s.deploy(ctx, svc.ID, "create")
		}
	}
	s.st.SetDesiredStopped(ctx, svc.ID, action == "stop")
	if !s.hub.Online(nodeID) {
		return nil, errf(409, "server_offline", "%v; desired state saved and will apply on reconnect", ErrNodeOffline)
	}
	if _, err := s.hub.Call(ctx, nodeID, api.MsgAction, api.ActionPayload{ServiceID: svc.ID, Action: action}, 2*s.cfg.RequestTimeout); err != nil {
		return nil, errf(502, "agent_error", "%v", err)
	}
	if action == "stop" {
		s.st.SetServiceStatus(ctx, svc.ID, "stopped", "0/1")
	}
	s.publishService(ctx, svc.ID)
	s.pushIngress(ctx, nodeID)
	sv, _, err := s.st.GetService(ctx, svc.ID)
	return sv, err
}
