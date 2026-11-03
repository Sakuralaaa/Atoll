package server

// Backups: per-service policies.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/cronexpr"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// ---------- policies ----------

func (s *Server) defaultPolicy(svc *api.Service) api.BackupPolicy {
	return api.BackupPolicy{ServiceID: svc.ID, Schedule: defaultBackupSchedule, Timezone: serverTimezone(), Keep: defaultBackupKeep, IncludeVolumes: true}
}

// backupParts says what a run of svc snapshots.
func backupParts(svc *api.Service, p api.BackupPolicy) []string {
	var parts []string
	if backupDBKinds[svc.Kind] {
		parts = append(parts, "dump")
		if p.IncludeVolumes && len(svc.Volumes) > 0 {
			parts = append(parts, "volumes")
		}
		return parts
	}
	if len(svc.Volumes) > 0 {
		parts = append(parts, "volumes")
	}
	return parts
}

func (s *Server) policyFor(ctx context.Context, svc *api.Service) (api.BackupPolicy, error) {
	p, err := s.st.GetBackupPolicy(ctx, svc.ID)
	if errors.Is(err, store.ErrNotFound) {
		d := s.defaultPolicy(svc)
		return d, nil
	}
	if err != nil {
		return api.BackupPolicy{}, err
	}
	return *p, nil
}

// decoratePolicy fills the computed fields.
func (s *Server) decoratePolicy(ctx context.Context, svc *api.Service, p *api.BackupPolicy) {
	_, p.RepoConfigured = s.backupRepo(ctx)
	p.Parts = backupParts(svc, *p)
	p.NextRunAt = nil
	if p.Enabled {
		if sc, err := cronexpr.Parse(p.Schedule); err == nil {
			if loc, err := cronexpr.LoadLocation(p.Timezone); err == nil {
				if n := sc.Next(time.Now(), loc); !n.IsZero() {
					n = n.UTC()
					p.NextRunAt = &n
				}
			}
		}
	}
}

func validatePolicy(svc *api.Service, p *api.BackupPolicy) error {
	p.Schedule = strings.TrimSpace(p.Schedule)
	if p.Schedule == "" {
		p.Schedule = defaultBackupSchedule
	}
	sc, err := cronexpr.Parse(p.Schedule)
	if err != nil {
		return badRequest("%v", err)
	}
	if sc.Never() {
		return badRequest("schedule %q never fires", p.Schedule)
	}
	p.Timezone = strings.TrimSpace(p.Timezone)
	if p.Timezone == "" {
		p.Timezone = serverTimezone()
	}
	if _, err := cronexpr.LoadLocation(p.Timezone); err != nil {
		return badRequest("unknown timezone %q", p.Timezone)
	}
	k := p.Keep
	if k.Daily < 0 || k.Weekly < 0 || k.Monthly < 0 || k.Daily > 1000 || k.Weekly > 1000 || k.Monthly > 1000 {
		return badRequest("keep values must be between 0 and 1000")
	}
	if k.Daily+k.Weekly+k.Monthly == 0 {
		return badRequest("retention must keep at least one snapshot (keep.daily, keep.weekly or keep.monthly)")
	}
	if !backupDBKinds[svc.Kind] {
		p.IncludeVolumes = true // volumes are the backup
	}
	if p.Enabled && len(backupParts(svc, *p)) == 0 {
		return badRequest("service %s has no volumes and is not a database template: nothing to back up", svc.Name)
	}
	return nil
}

func (s *Server) handleGetBackupPolicy(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	p, err := s.policyFor(r.Context(), svc)
	if err != nil {
		return err
	}
	s.decoratePolicy(r.Context(), svc, &p)
	writeJSON(w, 200, p)
	return nil
}

func (s *Server) handlePutBackupPolicy(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	cur, err := s.policyFor(ctx, svc)
	if err != nil {
		return err
	}
	// start from the current policy so partial bodies work
	req := cur
	req.Keep = api.BackupKeep{}
	var raw map[string]json.RawMessage
	if err := decode(r, &raw); err != nil {
		return err
	}
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &req); err != nil {
		return badRequest("invalid policy: %v", err)
	}
	if _, ok := raw["keep"]; !ok {
		req.Keep = cur.Keep
	}
	req.ServiceID = svc.ID
	if err := validatePolicy(svc, &req); err != nil {
		return err
	}
	if err := s.st.SaveBackupPolicy(ctx, req); err != nil {
		return err
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "backup.policy", svc.ID, map[string]any{"enabled": req.Enabled, "schedule": req.Schedule, "timezone": req.Timezone, "keep": req.Keep})
	if node, err := s.serviceNode(ctx, svc); err == nil {
		s.pushBackupConfig(ctx, node, true)
	}
	p, err := s.policyFor(ctx, svc)
	if err != nil {
		return err
	}
	s.decoratePolicy(ctx, svc, &p)
	writeJSON(w, 200, p)
	return nil
}

// targetFor is the agent view of a service's policy.
func targetFor(svc *api.Service, p api.BackupPolicy) api.BackupTarget {
	t := api.BackupTarget{
		ServiceID: svc.ID, ProjectID: svc.ProjectID, Name: svc.Name, Kind: svc.Kind, Volumes: []string{},
		IncludeVolumes: p.IncludeVolumes || !backupDBKinds[svc.Kind], Enabled: p.Enabled,
		Schedule: p.Schedule, Timezone: p.Timezone, Keep: p.Keep,
	}
	for _, v := range svc.Volumes {
		t.Volumes = append(t.Volumes, v.Name)
	}
	return t
}

// backupConfigFor builds the MsgBackupConfig payload of a node.
func (s *Server) backupConfigFor(ctx context.Context, nodeID string) (api.BackupConfigPayload, error) {
	cfg := api.BackupConfigPayload{Targets: []api.BackupTarget{}}
	if repo, ok := s.backupRepo(ctx); ok {
		cfg.Repo = &repo
		cfg.Verify = s.verifyConfigFor(ctx, nodeID) // backups_verify.go
	}
	rows, err := s.st.ListBackupPolicies(ctx, nodeID)
	if err != nil {
		return cfg, err
	}
	for _, row := range rows {
		svc, _, err := s.st.GetService(ctx, row.ServiceID)
		if err != nil {
			continue
		}
		cfg.Targets = append(cfg.Targets, targetFor(svc, row.BackupPolicy))
	}
	return cfg, nil
}

// pushBackupConfig sends the node its policies; unchanged configs are skipped unless force.
func (s *Server) pushBackupConfig(ctx context.Context, nodeID string, force bool) {
	if !s.hub.Online(nodeID) {
		return
	}
	cfg, err := s.backupConfigFor(ctx, nodeID)
	if err != nil {
		s.log.Warn("backup config", "node", nodeID, "err", err)
		return
	}
	raw, _ := json.Marshal(cfg)
	sum := sha256.Sum256(raw)
	h := hex.EncodeToString(sum[:])
	bs := s.backups()
	bs.mu.Lock()
	same := bs.configHash[nodeID] == h
	if !same || force {
		bs.configHash[nodeID] = h
	}
	bs.mu.Unlock()
	if same && !force {
		return
	}
	s.goBG(func(bg context.Context) {
		if _, err := s.hub.Call(bg, nodeID, api.MsgBackupConfig, cfg, 0); err != nil {
			s.log.Warn("backup config push failed", "node", nodeID, "err", err)
			bs.mu.Lock()
			delete(bs.configHash, nodeID) // retry next tick
			bs.mu.Unlock()
		}
	})
}

func (s *Server) pushBackupConfigAll(ctx context.Context, force bool) {
	nodes, _ := s.st.ListNodes(ctx)
	for _, n := range nodes {
		s.pushBackupConfig(ctx, n.ID, force)
	}
}
