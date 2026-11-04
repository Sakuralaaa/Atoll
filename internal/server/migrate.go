package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

func (s *Server) handleMigrate(w http.ResponseWriter, r *http.Request) error {
	p, err := s.st.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req struct {
		ServerID string `json:"server_id"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	to, err := s.st.GetNode(r.Context(), req.ServerID)
	if err != nil {
		return badRequest("unknown target server_id %q", req.ServerID)
	}
	if to.ID == p.ServerID {
		return badRequest("project already runs on %s", to.Name)
	}
	from, _ := s.st.GetNode(r.Context(), p.ServerID)
	fromName := p.ServerID
	if from != nil {
		fromName = from.Name
	}
	base := s.publicURL(r.Context(), r)
	return s.requireConfirm(w, r, "project.migrate", p.ID, fmt.Sprintf("Move project %s from %s to %s (services stop during data copy; DNS switches)", p.Name, fromName, to.Name),
		req, func(ctx context.Context) (int, any, error) {
			job, err := s.startMigration(ctx, p.ID, req.ServerID, base, principalFrom(r.Context()).actor())
			return 200, job, err
		})
}

func (s *Server) handleGetMigration(w http.ResponseWriter, r *http.Request) error {
	m, err := s.st.GetMigration(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, m)
	return nil
}

func (s *Server) handleListMigrations(w http.ResponseWriter, r *http.Request) error {
	ms, err := s.st.ListMigrations(r.Context(), r.URL.Query().Get("project_id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, ms)
	return nil
}

// startMigration validates and launches a project move in the background.
func (s *Server) startMigration(ctx context.Context, projectID, toID, base, actor string) (*api.MigrationJob, error) {
	p, err := s.st.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if p.ServerID == toID {
		return nil, badRequest("project already runs on that server")
	}
	if svcs, err := s.st.ListServices(ctx, projectID); err == nil {
		for _, v := range svcs {
			if hasHostPath(v.Volumes) {
				return nil, errf(409, "host_path", "service %s mounts host paths adopted from Docker in place; Atoll cannot move that data to another server", v.Name)
			}
		}
	}
	if _, err := s.st.GetNode(ctx, toID); err != nil {
		return nil, badRequest("unknown target server")
	}
	if !s.hub.Online(p.ServerID) {
		return nil, errf(409, "server_offline", "source server is offline; data cannot be exported")
	}
	if !s.hub.Online(toID) {
		return nil, errf(409, "server_offline", "target server is offline")
	}
	if !s.lockJob(projectID) {
		return nil, errf(409, "busy", "another migration or import is running for this project")
	}
	job := api.MigrationJob{ID: newID("mig"), ProjectID: projectID, FromServer: p.ServerID, ToServer: toID, Status: "running", Steps: []api.ImportStep{}, CreatedAt: time.Now().UTC()}
	if err := s.st.SaveMigration(ctx, job); err != nil {
		s.unlockJob(projectID)
		return nil, err
	}
	s.st.Audit(ctx, actor, "project.migrate", projectID, map[string]string{"from": p.ServerID, "to": toID, "job": job.ID})
	return s.launchMigration(job, base), nil
}

// startServiceMigration moves one service (container + volumes) to another server. The rest
// of the project stays and reaches it under the same name over the tailnet mesh.
func (s *Server) startServiceMigration(ctx context.Context, serviceID, toID, base, actor string) (*api.MigrationJob, error) {
	svc, _, err := s.st.GetService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	from := svc.ServerID
	if from == toID {
		return nil, badRequest("service already runs on that server")
	}
	if hasHostPath(svc.Volumes) {
		return nil, errf(409, "host_path", "service %s mounts host paths adopted from Docker in place; Atoll cannot move that data to another server", svc.Name)
	}
	if _, err := s.st.GetNode(ctx, toID); err != nil {
		return nil, badRequest("unknown target server")
	}
	if err := s.checkPlacement(ctx, svc.ProjectID, svc.ID, toID); err != nil {
		return nil, err
	}
	if !s.hub.Online(from) {
		return nil, errf(409, "server_offline", "source server is offline; data cannot be exported")
	}
	if !s.hub.Online(toID) {
		return nil, errf(409, "server_offline", "target server is offline")
	}
	if !s.lockJob(svc.ProjectID) {
		return nil, errf(409, "busy", "another migration or import is running for this project")
	}
	job := api.MigrationJob{ID: newID("mig"), ProjectID: svc.ProjectID, ServiceID: svc.ID, FromServer: from, ToServer: toID, Status: "running",
		Steps: []api.ImportStep{}, CreatedAt: time.Now().UTC()}
	if err := s.st.SaveMigration(ctx, job); err != nil {
		s.unlockJob(svc.ProjectID)
		return nil, err
	}
	s.st.Audit(ctx, actor, "service.migrate", svc.ID, map[string]string{"from": from, "to": toID, "job": job.ID})
	return s.launchMigration(job, base), nil
}

func (s *Server) handleServiceMigrate(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.ServiceMigrateRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	to, err := s.st.GetNode(r.Context(), req.ServerID)
	if err != nil {
		return badRequest("unknown target server_id %q", req.ServerID)
	}
	if to.ID == svc.ServerID {
		return badRequest("service already runs on %s", to.Name)
	}
	if err := s.checkPlacement(r.Context(), svc.ProjectID, svc.ID, to.ID); err != nil {
		return err
	}
	fromName := svc.ServerID
	if n, err := s.st.GetNode(r.Context(), svc.ServerID); err == nil {
		fromName = n.Name
	}
	base := s.publicURL(r.Context(), r)
	return s.requireConfirm(w, r, "service.migrate", svc.ID, fmt.Sprintf("Move service %s from %s to %s (it stops during the data copy; the project reaches it over the tailnet)", svc.Name, fromName, to.Name),
		req, func(ctx context.Context) (int, any, error) {
			job, err := s.startServiceMigration(ctx, svc.ID, req.ServerID, base, principalFrom(r.Context()).actor())
			return 200, job, err
		})
}

func (s *Server) launchMigration(job api.MigrationJob, base string) *api.MigrationJob {
	s.publish("migration", job)
	snapshot := job
	s.goBG(func(bg context.Context) {
		defer s.unlockJob(job.ProjectID)
		s.runMigration(bg, &job, base)
		s.pushMeshAll(bg) // names of moved services now resolve across servers
	})
	return &snapshot
}

func (s *Server) runMigration(ctx context.Context, job *api.MigrationJob, base string) {
	st := &stepper{steps: &job.Steps, save: func() {
		s.st.SaveMigration(ctx, *job)
		s.publish("migration", *job)
	}}
	finish := func(err error) {
		now := time.Now().UTC()
		job.FinishedAt = &now
		job.Status = "done"
		if err != nil {
			job.Status = "failed"
			job.Error = err.Error()
		}
		st.save()
	}
	from, to := job.FromServer, job.ToServer
	all, err := s.st.ListServices(ctx, job.ProjectID)
	if err != nil {
		finish(err)
		return
	}
	// The services that move: one (service migration) or every service on the source server
	// (project migration; services placed on other servers stay where they are).
	var svcs []api.Service
	for _, v := range all {
		if (job.ServiceID != "" && v.ID == job.ServiceID) || (job.ServiceID == "" && v.ServerID == from) {
			svcs = append(svcs, v)
		}
	}
	if job.ServiceID != "" && len(svcs) == 0 {
		finish(fmt.Errorf("service %s not found", job.ServiceID))
		return
	}
	setNode := func(node string) error {
		if job.ServiceID != "" {
			return s.st.SetServiceServer(ctx, job.ServiceID, node)
		}
		return s.st.SetProjectServer(ctx, job.ProjectID, node)
	}
	// pre-plan all steps so the UI shows the whole path
	for _, n := range []string{"Stop services on source"} {
		st.idx(n)
	}
	for _, v := range svcs {
		for _, vol := range v.Volumes {
			st.idx(fmt.Sprintf("Copy volume %s/%s", v.Name, vol.Name))
		}
	}
	for _, n := range []string{"Start services on target", "Switch DNS", "Update ingress", "Remove from source"} {
		st.idx(n)
	}
	st.save()

	wasRunning := map[string]bool{}
	for _, v := range svcs {
		if _, stopped, err := s.st.GetService(ctx, v.ID); err == nil && !stopped {
			wasRunning[v.ID] = true
		}
	}
	restartSource := func() {
		for _, v := range svcs {
			if wasRunning[v.ID] {
				s.hub.Call(ctx, from, api.MsgAction, api.ActionPayload{ServiceID: v.ID, Action: "start"}, 0)
			}
		}
	}

	if err := st.do("Stop services on source", func() (string, error) {
		for _, v := range svcs {
			if _, err := s.hub.Call(ctx, from, api.MsgAction, api.ActionPayload{ServiceID: v.ID, Action: "stop"}, 2*s.cfg.RequestTimeout); err != nil {
				return "", fmt.Errorf("stop %s: %w", v.Name, err)
			}
		}
		return fmt.Sprintf("%d service(s) stopped", len(svcs)), nil
	}); err != nil {
		restartSource()
		finish(err)
		return
	}

	for _, v := range svcs {
		for _, vol := range v.Volumes {
			name := fmt.Sprintf("Copy volume %s/%s", v.Name, vol.Name)
			if err := st.do(name, func() (string, error) { return s.relayVolume(ctx, from, to, v.ID, vol.Name, base) }); err != nil {
				restartSource()
				finish(err)
				return
			}
		}
	}

	// The moved services' domains. Their hostnames stay out of every route table from the
	// moment the placement switches until DNS points at the target: a deploy on the target
	// pushes its route table, and Caddy there would otherwise ask the CA for a certificate
	// while the name still resolves to the source (HTTP-01 fails, retry backs off).
	moved := map[string]bool{}
	for _, v := range svcs {
		moved[v.ID] = true
	}
	var doms []store.DomainRow
	var hosts []string
	if all, err := s.st.ListDomains(ctx, job.ServiceID, job.ProjectID); err == nil {
		for _, d := range all {
			if moved[d.ServiceID] {
				doms = append(doms, d)
				hosts = append(hosts, d.Hostname)
			}
		}
	}
	releaseHosts := s.holdRoutes(hosts)
	defer releaseHosts()

	// switch the placement and deploy on target
	if err := setNode(to); err != nil {
		releaseHosts()
		restartSource()
		finish(err)
		return
	}
	revert := func(cause error) {
		releaseHosts() // DNS never moved: the source serves the names again
		setNode(from)
		for _, v := range svcs {
			s.hub.Call(ctx, to, api.MsgRemove, api.RemovePayload{ServiceID: v.ID, KeepVolumes: true}, 0)
		}
		restartSource()
		s.pushIngress(ctx, to)
		s.pushIngress(ctx, from)
		what := "project stays"
		if job.ServiceID != "" {
			what = "service stays"
		}
		finish(fmt.Errorf("%w (rolled back: %s on source server)", cause, what))
	}
	if err := st.do("Start services on target", func() (string, error) {
		var ids []string
		for _, v := range svcs {
			d, err := s.deploy(ctx, v.ID, "migrate")
			if err != nil {
				return "", err
			}
			ids = append(ids, d.ID)
			if !wasRunning[v.ID] {
				s.st.SetDesiredStopped(ctx, v.ID, true)
			}
		}
		if err := s.waitDeployments(ctx, ids, s.cfg.DeployWaitTimeout); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d deployment(s) running", len(ids)), nil
	}); err != nil {
		revert(err)
		return
	}
	// DNS first: the record is switched to the target, and the step waits until public
	// resolvers see it (proxied: Cloudflare's edge follows the record at once; direct: the
	// name must resolve to the target's IP), bounded by DNSWaitTimeout.
	var provisioning []api.Domain
	toIP := ""
	if n, err := s.st.GetNode(ctx, to); err == nil {
		toIP = n.PublicIPv4
	}
	dnsErr := st.do("Switch DNS", func() (string, error) {
		if len(doms) == 0 {
			return "no domains", nil
		}
		var failed []string
		manual := false
		for _, d := range doms {
			dd := d.Domain
			if s.upsertDNS(ctx, &dd, to, statusProvisioning) {
				provisioning = append(provisioning, dd)
			}
			if dd.Status == "error" {
				failed = append(failed, dd.Hostname+": "+dd.Error)
			}
			if dd.Status == "pending" {
				manual = true
			}
		}
		late := s.waitAllResolve(ctx, provisioning, toIP)
		if len(failed) > 0 {
			return "", errors.New("DNS update failed for " + fmt.Sprint(failed))
		}
		if manual {
			return fmt.Sprintf("DNS not managed by Atoll: point %d record(s) at %s manually", len(doms), orUnknown(toIP)), nil
		}
		msg := fmt.Sprintf("%d record(s) now point at target", len(doms))
		if len(late) > 0 {
			msg += fmt.Sprintf(" (not visible yet after %s: %s; routed anyway)", s.cfg.DNSWaitTimeout, strings.Join(late, ", "))
		}
		return msg, nil
	})
	// Then the routes: pushed to the target (whose Caddy now gets a certificate for a name
	// that resolves to it), and only after that withdrawn from the source.
	releaseHosts()
	st.do("Update ingress", func() (string, error) {
		if err := s.pushIngressSync(ctx, to); err != nil {
			return "", err
		}
		if s.hub.Online(from) {
			if err := s.pushIngressSync(ctx, from); err != nil {
				return "", fmt.Errorf("routes pushed to target; withdrawing them from source: %w", err)
			}
		}
		return "routes moved to target", nil
	})
	// HTTPS on the target is confirmed in the background (provisioning -> active), as for a
	// new domain: behind Cloudflare the first requests may see a 525 until the target's
	// certificate is issued.
	for _, d := range provisioning {
		d := d
		s.goBG(func(bg context.Context) { s.confirmHTTPS(bg, d) })
	}
	st.do("Remove from source", func() (string, error) {
		for _, v := range svcs {
			if _, err := s.hub.Call(ctx, from, api.MsgRemove, api.RemovePayload{ServiceID: v.ID, KeepVolumes: true}, 2*s.cfg.RequestTimeout); err != nil {
				return "", err
			}
		}
		return "containers removed; source volumes kept under /var/lib/atoll/volumes for manual cleanup", nil
	})
	finish(dnsErr)
}

// relayVolume exports a volume from one node into a server blob and imports it on another.
func (s *Server) relayVolume(ctx context.Context, from, to, serviceID, volume, base string) (string, error) {
	blobID, putURL, getURL, err := s.newBlob(ctx, base)
	if err != nil {
		return "", err
	}
	// Each agent reaches the blob relay through the URL it is connected with (tailnet or public).
	putURL = s.agentURL(ctx, from, base, putURL)
	getURL = s.agentURL(ctx, to, base, getURL)
	defer s.removeBlob(blobID)
	if _, err := s.hub.Call(ctx, from, api.MsgVolumeExport, api.VolumeTransferPayload{ServiceID: serviceID, Volume: volume, URL: putURL}, s.cfg.TransferTimeout); err != nil {
		return "", fmt.Errorf("export: %w", err)
	}
	size := int64(0)
	if fi, err := statFile(s.blobPath(blobID)); err == nil {
		size = fi
	}
	if _, err := s.hub.Call(ctx, to, api.MsgVolumeImport, api.VolumeTransferPayload{ServiceID: serviceID, Volume: volume, URL: getURL}, s.cfg.TransferTimeout); err != nil {
		return "", fmt.Errorf("import: %w", err)
	}
	return fmt.Sprintf("%s transferred", humanBytes(size)), nil
}
