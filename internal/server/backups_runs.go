package server

// Backups: the scheduler watchdog and backup runs.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/cronexpr"
)

// ---------- scheduler (watchdog) ----------

func (s *Server) startBackupScheduler() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(backupTick)
		defer t.Stop()
		n := 0
		for {
			select {
			case <-s.bg.Done():
				return
			case now := <-t.C:
				n++
				if n%backupConfigEvery == 1 {
					s.pushBackupConfigAll(s.bg, false)
				}
				s.backupCatchup(s.bg, now)
				if k, _ := s.st.FailStaleBackups(s.bg, backupStaleAfter); k > 0 {
					s.log.Warn("backups without result marked failed", "count", k)
				}
				s.verifyHousekeeping(s.bg, n%360 == 1) // backups_verify.go

			}
		}
	}()
}

// backupCatchup requests one run for every enabled policy whose last scheduled time passed
// more than backupCatchupGrace ago without a backup record (agent was down, or its local
// schedule was missing). The agent skips it when it did run (Since).
func (s *Server) backupCatchup(ctx context.Context, now time.Time) []string {
	repo, ok := s.backupRepo(ctx)
	if !ok {
		return nil
	}
	rows, err := s.st.ListBackupPolicies(ctx, "")
	if err != nil {
		return nil
	}
	var sent []string
	bs := s.backups()
	for _, row := range rows {
		if !row.Enabled || !s.hub.Online(row.NodeID) {
			continue
		}
		sc, err := cronexpr.Parse(row.Schedule)
		if err != nil {
			continue
		}
		loc, err := cronexpr.LoadLocation(row.Timezone)
		if err != nil {
			continue
		}
		prev := sc.Prev(now, loc)
		if prev.IsZero() || now.Sub(prev) < backupCatchupGrace {
			continue
		}
		// a policy saved after that fire time has nothing to catch up
		if row.UpdatedAt != nil && row.UpdatedAt.After(prev) {
			continue
		}
		if _, err := s.st.LatestBackupSince(ctx, row.ServiceID, prev); err == nil {
			continue
		}
		bs.mu.Lock()
		already := bs.catchupSent[row.ServiceID].Equal(prev)
		if !already {
			bs.catchupSent[row.ServiceID] = prev
		}
		bs.mu.Unlock()
		if already {
			continue
		}
		svc, _, err := s.st.GetService(ctx, row.ServiceID)
		if err != nil {
			continue
		}
		since := prev.UTC()
		p := api.BackupRunPayload{BackupID: newID("bak"), Trigger: "catchup", Target: targetFor(svc, row.BackupPolicy), Repo: repo, Since: &since}
		s.hub.Fire(row.NodeID, api.MsgBackupRun, p)
		s.log.Info("backup catch-up requested", "service", svc.ID, "scheduled", since)
		sent = append(sent, svc.ID)
	}
	return sent
}

// ---------- runs ----------

func (s *Server) handleRunBackup(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	b, err := s.runBackupNow(ctx, svc, principalFrom(ctx).actor())
	if err != nil {
		return err
	}
	writeJSON(w, 202, b)
	return nil
}

func (s *Server) runBackupNow(ctx context.Context, svc *api.Service, actor string) (*api.Backup, error) {
	repo, ok := s.backupRepo(ctx)
	if !ok {
		return nil, errf(409, "backup_not_configured", "configure a backup target in Settings → 备份存储 first")
	}
	p, err := s.policyFor(ctx, svc)
	if err != nil {
		return nil, err
	}
	if len(backupParts(svc, p)) == 0 {
		return nil, badRequest("service %s has no volumes and is not a database template: nothing to back up", svc.Name)
	}
	node, err := s.serviceNode(ctx, svc)
	if err != nil {
		return nil, err
	}
	if !s.hub.Online(node) {
		return nil, errf(409, "server_offline", "%v", ErrNodeOffline)
	}
	b := api.Backup{ID: newID("bak"), ServiceID: svc.ID, ServerID: node, Trigger: "manual", Status: "running",
		Snapshots: []api.BackupSnapshot{}, StartedAt: time.Now().UTC()}
	if err := s.st.SaveBackup(ctx, b); err != nil {
		return nil, err
	}
	payload := api.BackupRunPayload{BackupID: b.ID, Trigger: "manual", Target: targetFor(svc, p), Repo: repo}
	if _, err := s.hub.Call(ctx, node, api.MsgBackupRun, payload, 0); err != nil {
		now := time.Now().UTC()
		b.Status, b.Error, b.FinishedAt = "failed", err.Error(), &now
		s.st.SaveBackup(ctx, b)
		return nil, errf(502, "agent_error", "%v", err)
	}
	s.st.Audit(ctx, actor, "backup.run", svc.ID, map[string]string{"backup": b.ID})
	s.publish("backup", b)
	return &b, nil
}

// handleBackupEvent records an agent's backup progress / result.
func (s *Server) handleBackupEvent(ctx context.Context, nodeID string, ev api.BackupEvent) {
	if ev.BackupID == "" || ev.ServiceID == "" {
		return
	}
	b := api.Backup{ID: ev.BackupID, ServiceID: ev.ServiceID, ServerID: nodeID, Trigger: ev.Trigger, Status: ev.Status,
		Snapshots: ev.Snapshots, Bytes: ev.Bytes, Added: ev.Added, DurationMs: ev.DurationMs, Error: ev.Error,
		Warnings: ev.Warnings, StartedAt: ev.StartedAt, FinishedAt: ev.FinishedAt}
	if old, err := s.st.GetBackup(ctx, ev.BackupID); err == nil && old.ID == ev.BackupID {
		b.Trigger = old.Trigger
		if b.StartedAt.IsZero() {
			b.StartedAt = old.StartedAt
		}
	}
	if b.Trigger == "" {
		b.Trigger = "schedule"
	}
	if b.StartedAt.IsZero() {
		b.StartedAt = time.Now().UTC()
	}
	if err := s.st.SaveBackup(ctx, b); err != nil {
		if !strings.Contains(err.Error(), "FOREIGN KEY") {
			s.log.Warn("save backup", "backup", b.ID, "err", err)
		}
		return
	}
	if err := s.st.MarkSnapshotsPruned(ctx, ev.ServiceID, ev.Pruned); err != nil {
		s.log.Warn("mark pruned", "service", ev.ServiceID, "err", err)
	}
	if b.Status == "failed" {
		s.log.Warn("backup failed", "service", b.ServiceID, "backup", b.ID, "err", b.Error)
	}
	if cur, err := s.st.GetBackup(ctx, b.ID); err == nil {
		s.publish("backup", cur)
	}
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	if r.URL.Query().Get("refresh") == "1" {
		if err := s.refreshSnapshots(ctx, svc); err != nil {
			return err
		}
	}
	list, err := s.st.ListBackups(ctx, svc.ID, 200)
	if err != nil {
		return err
	}
	writeJSON(w, 200, list)
	return nil
}

// refreshSnapshots reconciles records with the repository: snapshots unknown to the database
// are imported, recorded ones missing from the repository are marked pruned.
func (s *Server) refreshSnapshots(ctx context.Context, svc *api.Service) error {
	repo, ok := s.backupRepo(ctx)
	if !ok {
		return errf(409, "backup_not_configured", "no backup target configured")
	}
	node, _ := s.serviceNode(ctx, svc)
	if !s.hub.Online(node) {
		node = ""
		nodes, _ := s.st.ListNodes(ctx)
		pr := principalFrom(ctx)
		for _, n := range nodes {
			if pr != nil && pr.Member && !pr.Servers[n.ID] {
				continue // a member's request only runs on its own servers
			}
			if s.hub.Online(n.ID) {
				node = n.ID
				break
			}
		}
	}
	if node == "" {
		return errf(409, "server_offline", "no online server to read the repository")
	}
	out, err := s.hub.Call(ctx, node, api.MsgBackupSnapshots, api.BackupSnapshotsPayload{Repo: repo, ServiceID: svc.ID}, 3*time.Minute)
	if err != nil {
		return errf(502, "agent_error", "%v", err)
	}
	var snaps []api.ResticSnapshot
	if err := json.Unmarshal([]byte(out), &snaps); err != nil {
		return errf(502, "agent_error", "bad snapshot list: %v", err)
	}
	inRepo := map[string]api.ResticSnapshot{}
	for _, sn := range snaps {
		inRepo[sn.ID] = sn
	}
	list, err := s.st.ListBackups(ctx, svc.ID, 1000)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	var gone []string
	for _, b := range list {
		for _, sn := range b.Snapshots {
			known[sn.ID] = true
			if _, ok := inRepo[sn.ID]; !ok && !sn.Pruned {
				gone = append(gone, sn.ID)
			}
		}
	}
	if err := s.st.MarkSnapshotsPruned(ctx, svc.ID, gone); err != nil {
		return err
	}
	for _, sn := range snaps {
		if known[sn.ID] {
			continue
		}
		part := "volumes"
		for _, t := range sn.Tags {
			if strings.HasPrefix(t, "part=") {
				part = strings.TrimPrefix(t, "part=")
			}
		}
		at := sn.Time.UTC()
		b := api.Backup{ID: newID("bak"), ServiceID: svc.ID, ServerID: "", Trigger: "imported", Status: "done",
			Snapshots: []api.BackupSnapshot{{ID: sn.ID, Part: part}}, StartedAt: at, FinishedAt: &at,
			Warnings: []string{"found in the repository (host " + sn.Hostname + "); size unknown"}}
		if err := s.st.SaveBackup(ctx, b); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleGetBackup(w http.ResponseWriter, r *http.Request) error {
	b, err := s.st.GetBackup(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, b)
	return nil
}
