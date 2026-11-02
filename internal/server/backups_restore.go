package server

// Backups: restore jobs.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ---------- restore ----------

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	b, err := s.st.GetBackup(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.BackupRestoreRequest
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			return err
		}
	}
	svc, _, err := s.st.GetService(ctx, b.ServiceID)
	if err != nil {
		return err
	}
	if err := s.checkRestore(ctx, b, svc, req); err != nil {
		return err
	}
	from, _ := s.serviceNode(ctx, svc)
	where := "in place"
	if req.TargetServerID != "" && req.TargetServerID != from {
		to, _ := s.st.GetNode(ctx, req.TargetServerID)
		where = "onto server " + to.Name + " (project moves there; DNS switches)"
	}
	summary := fmt.Sprintf("Restore %s from backup %s (%s) %s — current data is replaced", svc.Name, b.ID, b.StartedAt.Format("2006-01-02 15:04 MST"), where)
	return s.requireConfirm(w, r, "backup.restore", b.ID, summary, req, func(ctx context.Context) (int, any, error) {
		job, err := s.startRestore(ctx, b.ID, req, principalFrom(r.Context()).actor())
		return 200, job, err
	})
}

func (s *Server) checkRestore(ctx context.Context, b *api.Backup, svc *api.Service, req api.BackupRestoreRequest) error {
	if b.Status != "done" {
		return errf(409, "not_restorable", "backup %s is %s", b.ID, b.Status)
	}
	live := 0
	for _, sn := range b.Snapshots {
		if !sn.Pruned {
			live++
		}
	}
	if live == 0 {
		return errf(409, "not_restorable", "all snapshots of backup %s were removed by retention", b.ID)
	}
	if _, ok := s.backupRepo(ctx); !ok {
		return errf(409, "backup_not_configured", "no backup target configured")
	}
	from, err := s.serviceNode(ctx, svc)
	if err != nil {
		return err
	}
	to := req.TargetServerID
	if to == "" {
		to = from
	}
	if _, err := s.st.GetNode(ctx, to); err != nil {
		return badRequest("unknown target_server_id %q", req.TargetServerID)
	}
	if !s.hub.Online(to) {
		return errf(409, "server_offline", "target server is offline")
	}
	if to != from && s.hub.Online(from) {
		return errf(409, "source_online", "the project's server is online: restore in place, or move the project with migrate first (restoring to another server is for when the server is gone)")
	}
	return nil
}

func (s *Server) startRestore(ctx context.Context, backupID string, req api.BackupRestoreRequest, actor string) (*api.RestoreJob, error) {
	b, err := s.st.GetBackup(ctx, backupID)
	if err != nil {
		return nil, err
	}
	svc, _, err := s.st.GetService(ctx, b.ServiceID)
	if err != nil {
		return nil, err
	}
	if err := s.checkRestore(ctx, b, svc, req); err != nil {
		return nil, err
	}
	from, _ := s.serviceNode(ctx, svc)
	to := req.TargetServerID
	if to == "" {
		to = from
	}
	lock := svc.ID
	if to != from {
		lock = svc.ProjectID
	}
	if !s.lockJob(lock) {
		return nil, errf(409, "busy", "another restore, migration or import is running for this service/project")
	}
	job := api.RestoreJob{ID: newID("rst"), BackupID: b.ID, ServiceID: svc.ID, ProjectID: svc.ProjectID, FromServer: from, ToServer: to,
		Status: "running", Steps: []api.ImportStep{}, CreatedAt: time.Now().UTC()}
	if err := s.st.SaveRestore(ctx, job); err != nil {
		s.unlockJob(lock)
		return nil, err
	}
	s.st.Audit(ctx, actor, "backup.restore", svc.ID, map[string]string{"backup": b.ID, "job": job.ID, "from": from, "to": to})
	s.publish("restore", job)
	snapshot := job
	s.goBG(func(bg context.Context) {
		defer s.unlockJob(lock)
		s.runRestore(bg, &job, b)
	})
	return &snapshot, nil
}

func (s *Server) runRestore(ctx context.Context, job *api.RestoreJob, b *api.Backup) {
	st := &stepper{steps: &job.Steps, save: func() {
		s.st.SaveRestore(ctx, *job)
		s.publish("restore", *job)
	}}
	finish := func(err error) {
		now := time.Now().UTC()
		job.FinishedAt = &now
		job.Status = "done"
		if err != nil {
			job.Status, job.Error = "failed", err.Error()
		}
		st.save()
		s.publishService(ctx, job.ServiceID)
	}
	repo, ok := s.backupRepo(ctx)
	if !ok {
		finish(errors.New("backup target is no longer configured"))
		return
	}
	cross := job.ToServer != job.FromServer
	if cross {
		for _, n := range []string{"Move project to target server", "Restore data", "Switch DNS", "Update ingress"} {
			st.idx(n)
		}
	} else {
		st.idx("Restore data")
	}
	st.save()

	var releaseHosts = func() {}
	defer func() { releaseHosts() }()
	if cross {
		svcs, err := s.st.ListServices(ctx, job.ProjectID)
		if err != nil {
			finish(err)
			return
		}
		// Hold the project's hostnames: the target must not request certificates for names
		// that still resolve to the source until DNS has switched.
		if doms, err := s.st.ListDomains(ctx, "", job.ProjectID); err == nil {
			var hosts []string
			for _, d := range doms {
				hosts = append(hosts, d.Hostname)
			}
			releaseHosts = s.holdRoutes(hosts)
		}
		if err := st.do("Move project to target server", func() (string, error) {
			if err := s.st.SetProjectServer(ctx, job.ProjectID, job.ToServer); err != nil {
				return "", err
			}
			var ids []string
			for _, v := range svcs {
				d, err := s.deploy(ctx, v.ID, "restore")
				if err != nil {
					return "", err
				}
				ids = append(ids, d.ID)
			}
			if err := s.waitDeployments(ctx, ids, s.cfg.DeployWaitTimeout); err != nil {
				return "", err
			}
			return fmt.Sprintf("%d service(s) created on target from stored config; restore other services from their own backups", len(svcs)), nil
		}); err != nil {
			s.st.SetProjectServer(ctx, job.ProjectID, job.FromServer)
			for _, v := range svcs {
				s.hub.Call(ctx, job.ToServer, api.MsgRemove, api.RemovePayload{ServiceID: v.ID, KeepVolumes: true}, 0)
			}
			finish(fmt.Errorf("%w (project stays assigned to its original server)", err))
			return
		}
		s.pushBackupConfig(ctx, job.ToServer, true)
	}

	svc, _, err := s.st.GetService(ctx, job.ServiceID)
	if err != nil {
		finish(err)
		return
	}
	pol, _ := s.policyFor(ctx, svc)
	var snaps []api.BackupSnapshot
	for _, sn := range b.Snapshots {
		if !sn.Pruned {
			snaps = append(snaps, sn)
		}
	}
	err = st.do("Restore data", func() (string, error) {
		out, err := s.hub.Call(ctx, job.ToServer, api.MsgBackupRestore, api.BackupRestorePayload{
			Target: targetFor(svc, pol), SourceServiceID: b.ServiceID, Snapshots: snaps, Repo: repo,
		}, s.cfg.TransferTimeout)
		if err != nil {
			return "", err
		}
		var res api.BackupRestoreResult
		if json.Unmarshal([]byte(out), &res) != nil {
			return strings.TrimSpace(out), nil
		}
		detail := fmt.Sprintf("restored %s (%s)", strings.Join(res.Restored, " + "), humanBytes(res.Bytes))
		if res.MovedAside != "" {
			detail += "; previous data kept at " + res.MovedAside
		}
		if len(res.Warnings) > 0 {
			detail += "; " + strings.Join(res.Warnings, "; ")
		}
		return detail, nil
	})
	if err != nil || !cross {
		finish(err)
		return
	}
	// DNS first (wait until the names resolve to the target), then routes, then HTTPS
	// confirmed in the background — same order as a migration.
	var provisioning []api.Domain
	toIP := ""
	if n, err := s.st.GetNode(ctx, job.ToServer); err == nil {
		toIP = n.PublicIPv4
	}
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
			if s.upsertDNS(ctx, &dd, job.ToServer, statusProvisioning) {
				provisioning = append(provisioning, dd)
			}
			switch dd.Status {
			case "error":
				failed = append(failed, dd.Hostname+": "+dd.Error)
			case "pending":
				manual = true
			}
		}
		late := s.waitAllResolve(ctx, provisioning, toIP)
		if len(failed) > 0 {
			return "", errors.New("DNS update failed for " + strings.Join(failed, ", "))
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
	releaseHosts()
	st.do("Update ingress", func() (string, error) {
		if err := s.pushIngressSync(ctx, job.ToServer); err != nil {
			return "", err
		}
		return "routes now served by target", nil
	})
	for _, d := range provisioning {
		d := d
		s.goBG(func(bg context.Context) { s.confirmHTTPS(bg, d) })
	}
	finish(dnsErr)
}

func (s *Server) handleGetRestore(w http.ResponseWriter, r *http.Request) error {
	j, err := s.st.GetRestore(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, j)
	return nil
}

func (s *Server) handleListRestores(w http.ResponseWriter, r *http.Request) error {
	if _, _, err := s.st.GetService(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	list, err := s.st.ListRestores(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, list)
	return nil
}
