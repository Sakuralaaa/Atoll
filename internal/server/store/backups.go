package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ---------- backup policies ----------

// GetBackupPolicy returns the stored policy of a service (ErrNotFound when none was saved).
func (s *Store) GetBackupPolicy(ctx context.Context, serviceID string) (*api.BackupPolicy, error) {
	var p api.BackupPolicy
	var enabled, incl int
	var at string
	err := s.DB.QueryRowContext(ctx, `SELECT service_id, enabled, schedule, timezone, keep_daily, keep_weekly, keep_monthly, include_volumes, updated_at
		FROM backup_policies WHERE service_id=?`, serviceID).
		Scan(&p.ServiceID, &enabled, &p.Schedule, &p.Timezone, &p.Keep.Daily, &p.Keep.Weekly, &p.Keep.Monthly, &incl, &at)
	if err != nil {
		return nil, mapErr(err)
	}
	p.Enabled, p.IncludeVolumes = enabled == 1, incl == 1
	t := parseTS(at)
	p.UpdatedAt = &t
	return &p, nil
}

// SaveBackupPolicy upserts a policy.
func (s *Store) SaveBackupPolicy(ctx context.Context, p api.BackupPolicy) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO backup_policies(service_id, enabled, schedule, timezone, keep_daily, keep_weekly, keep_monthly, include_volumes, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(service_id) DO UPDATE SET enabled=excluded.enabled, schedule=excluded.schedule, timezone=excluded.timezone,
		keep_daily=excluded.keep_daily, keep_weekly=excluded.keep_weekly, keep_monthly=excluded.keep_monthly, include_volumes=excluded.include_volumes, updated_at=excluded.updated_at`,
		p.ServiceID, b2i(p.Enabled), p.Schedule, p.Timezone, p.Keep.Daily, p.Keep.Weekly, p.Keep.Monthly, b2i(p.IncludeVolumes), TS(time.Now()))
	return err
}

// BackupPolicyRow is a policy with the node its service runs on.
type BackupPolicyRow struct {
	api.BackupPolicy
	NodeID string
}

// ListBackupPolicies returns every stored policy ("" nodeID = all nodes) with the service's node.
func (s *Store) ListBackupPolicies(ctx context.Context, nodeID string) ([]BackupPolicyRow, error) {
	q := `SELECT b.service_id, b.enabled, b.schedule, b.timezone, b.keep_daily, b.keep_weekly, b.keep_monthly, b.include_volumes, b.updated_at, COALESCE(NULLIF(v.server_id, ''), p.server_id)
		FROM backup_policies b JOIN services v ON v.id=b.service_id JOIN projects p ON p.id=v.project_id`
	args := []any{}
	if nodeID != "" {
		q += ` WHERE COALESCE(NULLIF(v.server_id, ''), p.server_id)=?`
		args = append(args, nodeID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY b.service_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupPolicyRow
	for rows.Next() {
		var r BackupPolicyRow
		var enabled, incl int
		var at string
		if err := rows.Scan(&r.ServiceID, &enabled, &r.Schedule, &r.Timezone, &r.Keep.Daily, &r.Keep.Weekly, &r.Keep.Monthly, &incl, &at, &r.NodeID); err != nil {
			return nil, err
		}
		r.Enabled, r.IncludeVolumes = enabled == 1, incl == 1
		t := parseTS(at)
		r.UpdatedAt = &t
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---------- backups ----------

const backupCols = `id, service_id, server_id, trigger, status, snapshots, bytes, added, duration_ms, error, warnings, started_at, finished_at`

func scanBackup(sc interface{ Scan(...any) error }) (*api.Backup, error) {
	var b api.Backup
	var snaps, warns, at string
	var fin sql.NullString
	if err := sc.Scan(&b.ID, &b.ServiceID, &b.ServerID, &b.Trigger, &b.Status, &snaps, &b.Bytes, &b.Added, &b.DurationMs, &b.Error, &warns, &at, &fin); err != nil {
		return nil, mapErr(err)
	}
	json.Unmarshal([]byte(snaps), &b.Snapshots)
	if b.Snapshots == nil {
		b.Snapshots = []api.BackupSnapshot{}
	}
	json.Unmarshal([]byte(warns), &b.Warnings)
	b.StartedAt = parseTS(at)
	b.FinishedAt = parseTSPtr(fin)
	return &b, nil
}

// SaveBackup upserts a backup record. A finished record is never moved back to "running"
// (events can arrive out of order after a reconnect).
func (s *Store) SaveBackup(ctx context.Context, b api.Backup) error {
	if b.Snapshots == nil {
		b.Snapshots = []api.BackupSnapshot{}
	}
	if b.Warnings == nil {
		b.Warnings = []string{}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO backups(`+backupCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET server_id=excluded.server_id, status=excluded.status, snapshots=excluded.snapshots, bytes=excluded.bytes,
		added=excluded.added, duration_ms=excluded.duration_ms, error=excluded.error, warnings=excluded.warnings, finished_at=excluded.finished_at
		WHERE NOT (backups.status IN ('done','failed','pruned') AND excluded.status='running')`,
		b.ID, b.ServiceID, b.ServerID, b.Trigger, b.Status, mustJSON(b.Snapshots), b.Bytes, b.Added, b.DurationMs, b.Error, mustJSON(b.Warnings),
		TS(b.StartedAt), nullTS(b.FinishedAt))
	return err
}

// GetBackup returns a backup by id, or by one of its restic snapshot ids (full or 8-char short id).
func (s *Store) GetBackup(ctx context.Context, id string) (*api.Backup, error) {
	b, err := scanBackup(s.DB.QueryRowContext(ctx, `SELECT `+backupCols+` FROM backups WHERE id=?`, id))
	if err == nil || err != ErrNotFound || len(id) < 8 {
		return b, err
	}
	// snapshot id lookup
	rows, err := s.DB.QueryContext(ctx, `SELECT `+backupCols+` FROM backups WHERE snapshots LIKE ? ORDER BY started_at DESC LIMIT 5`, `%"id":"`+likeEscape(id)+`%`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		for _, sn := range b.Snapshots {
			if len(sn.ID) >= len(id) && sn.ID[:len(id)] == id {
				return b, nil
			}
		}
	}
	return nil, ErrNotFound
}

func likeEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' || c == '_' || c == '"' || c == '\\' {
			continue // ids are hex/base32; drop anything that could widen the pattern
		}
		out = append(out, c)
	}
	return string(out)
}

// ListBackups lists a service's backups, newest first.
func (s *Store) ListBackups(ctx context.Context, serviceID string, limit int) ([]api.Backup, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+backupCols+` FROM backups WHERE service_id=? ORDER BY started_at DESC LIMIT ?`, serviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// LatestBackupSince returns the newest running/done backup of a service started at or after t.
func (s *Store) LatestBackupSince(ctx context.Context, serviceID string, t time.Time) (*api.Backup, error) {
	return scanBackup(s.DB.QueryRowContext(ctx, `SELECT `+backupCols+` FROM backups WHERE service_id=? AND started_at>=? AND status IN ('running','done')
		ORDER BY started_at DESC LIMIT 1`, serviceID, TS(t)))
}

// MarkSnapshotsPruned flags snapshots removed by retention; a backup whose snapshots are all
// gone becomes "pruned".
func (s *Store) MarkSnapshotsPruned(ctx context.Context, serviceID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	gone := map[string]bool{}
	for _, id := range ids {
		gone[id] = true
	}
	list, err := s.ListBackups(ctx, serviceID, 1000)
	if err != nil {
		return err
	}
	for _, b := range list {
		changed, left := false, 0
		for i := range b.Snapshots {
			if gone[b.Snapshots[i].ID] && !b.Snapshots[i].Pruned {
				b.Snapshots[i].Pruned, changed = true, true
			}
			if !b.Snapshots[i].Pruned {
				left++
			}
		}
		if !changed {
			continue
		}
		status := b.Status
		if left == 0 && status == "done" {
			status = "pruned"
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE backups SET snapshots=?, status=? WHERE id=?`, mustJSON(b.Snapshots), status, b.ID); err != nil {
			return err
		}
	}
	return nil
}

// FailStaleBackups marks backups still "running" after maxAge as failed.
func (s *Store) FailStaleBackups(ctx context.Context, maxAge time.Duration) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE backups SET status='failed', error='no result from the agent (interrupted?)', finished_at=?
		WHERE status='running' AND started_at < ?`, TS(time.Now()), TS(time.Now().Add(-maxAge)))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------- restores ----------

// SaveRestore upserts a restore job.
func (s *Store) SaveRestore(ctx context.Context, j api.RestoreJob) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO backup_restores(id, backup_id, service_id, project_id, from_server, to_server, status, steps, error, created_at, finished_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status, steps=excluded.steps, error=excluded.error, finished_at=excluded.finished_at`,
		j.ID, j.BackupID, j.ServiceID, j.ProjectID, j.FromServer, j.ToServer, j.Status, mustJSON(j.Steps), j.Error, TS(j.CreatedAt), nullTS(j.FinishedAt))
	return err
}

const restoreCols = `id, backup_id, service_id, project_id, from_server, to_server, status, steps, error, created_at, finished_at`

func scanRestore(sc interface{ Scan(...any) error }) (*api.RestoreJob, error) {
	var j api.RestoreJob
	var steps, at string
	var fin sql.NullString
	if err := sc.Scan(&j.ID, &j.BackupID, &j.ServiceID, &j.ProjectID, &j.FromServer, &j.ToServer, &j.Status, &steps, &j.Error, &at, &fin); err != nil {
		return nil, mapErr(err)
	}
	json.Unmarshal([]byte(steps), &j.Steps)
	if j.Steps == nil {
		j.Steps = []api.ImportStep{}
	}
	j.CreatedAt = parseTS(at)
	j.FinishedAt = parseTSPtr(fin)
	return &j, nil
}

// GetRestore loads a restore job.
func (s *Store) GetRestore(ctx context.Context, id string) (*api.RestoreJob, error) {
	return scanRestore(s.DB.QueryRowContext(ctx, `SELECT `+restoreCols+` FROM backup_restores WHERE id=?`, id))
}

// ListRestores lists a service's restore jobs, newest first.
func (s *Store) ListRestores(ctx context.Context, serviceID string) ([]api.RestoreJob, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+restoreCols+` FROM backup_restores WHERE service_id=? ORDER BY created_at DESC LIMIT 50`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.RestoreJob{}
	for rows.Next() {
		j, err := scanRestore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}
