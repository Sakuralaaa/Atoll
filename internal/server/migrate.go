package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/atoll-sh/atoll/internal/api"
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
	s.publish("migration", job)
	snapshot := job
	s.goBG(func(bg context.Context) {
		defer s.unlockJob(projectID)
		s.runMigration(bg, &job, base)
	})
	return &snapshot, nil
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
	svcs, err := s.st.ListServices(ctx, job.ProjectID)
	if err != nil {
		finish(err)
		return
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
	for _, n := range []string{"Start services on target", "Update ingress", "Switch DNS", "Remove from source"} {
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

	// switch the project and deploy on target
	if err := s.st.SetProjectServer(ctx, job.ProjectID, to); err != nil {
		restartSource()
		finish(err)
		return
	}
	revert := func(cause error) {
		s.st.SetProjectServer(ctx, job.ProjectID, from)
		for _, v := range svcs {
			s.hub.Call(ctx, to, api.MsgRemove, api.RemovePayload{ServiceID: v.ID, KeepVolumes: true}, 0)
		}
		restartSource()
		s.pushIngress(ctx, to)
		s.pushIngress(ctx, from)
		finish(fmt.Errorf("%w (rolled back: project stays on source server)", cause))
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
	st.do("Update ingress", func() (string, error) {
		if err := s.pushIngressSync(ctx, to); err != nil {
			return "", err
		}
		s.pushIngress(ctx, from)
		return "routes moved to target", nil
	})
	dnsErr := st.do("Switch DNS", func() (string, error) {
		doms, err := s.st.ListDomains(ctx, "", job.ProjectID)
		if err != nil {
			return "", err
		}
		if len(doms) == 0 {
			return "no domains", nil
		}
		var failed []string
		manual := false
		for _, d := range doms {
			dd := d.Domain
			s.applyDNS(ctx, &dd, to)
			if dd.Status == "error" {
				failed = append(failed, dd.Hostname+": "+dd.Error)
			}
			if dd.Status == "pending" {
				manual = true
			}
		}
		if len(failed) > 0 {
			return "", errors.New("DNS update failed for " + fmt.Sprint(failed))
		}
		if manual {
			n, _ := s.st.GetNode(ctx, to)
			return fmt.Sprintf("DNS not managed by Atoll: point %d record(s) at %s manually", len(doms), orUnknown(n.PublicIPv4)), nil
		}
		return fmt.Sprintf("%d record(s) now point at target", len(doms)), nil
	})
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


