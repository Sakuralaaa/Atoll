package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ---------- backup verifications (checks and restore drills) ----------

const verifyCols = `id, kind, service_id, server_id, trigger, status, subset, steps, error, warnings, duration_ms, started_at, finished_at`

func scanVerification(sc interface{ Scan(...any) error }) (*api.BackupVerification, error) {
	var v api.BackupVerification
	var steps, warns, at string
	var fin sql.NullString
	if err := sc.Scan(&v.ID, &v.Kind, &v.ServiceID, &v.ServerID, &v.Trigger, &v.Status, &v.Subset, &steps, &v.Error, &warns, &v.DurationMs, &at, &fin); err != nil {
		return nil, mapErr(err)
	}
	json.Unmarshal([]byte(steps), &v.Steps)
	if v.Steps == nil {
		v.Steps = []api.BackupVerifyStep{}
	}
	json.Unmarshal([]byte(warns), &v.Warnings)
	v.StartedAt = parseTS(at)
	v.FinishedAt = parseTSPtr(fin)
	return &v, nil
}

// SaveVerification upserts a verification record. A finished record is never moved back to
// "running" (events can arrive out of order after a reconnect); kind, service and start time
// are kept from the first write.
func (s *Store) SaveVerification(ctx context.Context, v api.BackupVerification) error {
	if v.Steps == nil {
		v.Steps = []api.BackupVerifyStep{}
	}
	if v.Warnings == nil {
		v.Warnings = []string{}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO backup_verifications(`+verifyCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET server_id=excluded.server_id, status=excluded.status, subset=excluded.subset, steps=excluded.steps,
		error=excluded.error, warnings=excluded.warnings, duration_ms=excluded.duration_ms, finished_at=excluded.finished_at
		WHERE NOT (backup_verifications.status IN ('done','failed','skipped') AND excluded.status='running')`,
		v.ID, v.Kind, v.ServiceID, v.ServerID, v.Trigger, v.Status, v.Subset, mustJSON(v.Steps), v.Error, mustJSON(v.Warnings), v.DurationMs,
		TS(v.StartedAt), nullTS(v.FinishedAt))
	return err
}

// GetVerification returns one record.
func (s *Store) GetVerification(ctx context.Context, id string) (*api.BackupVerification, error) {
	return scanVerification(s.DB.QueryRowContext(ctx, `SELECT `+verifyCols+` FROM backup_verifications WHERE id=?`, id))
}

// ListVerifications lists records newest first. kind "" = both; serviceID "" = any (with kind
// "check" there is none).
func (s *Store) ListVerifications(ctx context.Context, kind, serviceID string, limit int) ([]api.BackupVerification, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT ` + verifyCols + ` FROM backup_verifications WHERE 1=1`
	var args []any
	if kind != "" {
		q += ` AND kind=?`
		args = append(args, kind)
	}
	if serviceID != "" {
		q += ` AND service_id=?`
		args = append(args, serviceID)
	}
	rows, err := s.r.QueryContext(ctx, q+` ORDER BY started_at DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.BackupVerification{}
	for rows.Next() {
		v, err := scanVerification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// LatestVerification returns the newest record of a kind (and service), ErrNotFound if none.
func (s *Store) LatestVerification(ctx context.Context, kind, serviceID string) (*api.BackupVerification, error) {
	return scanVerification(s.DB.QueryRowContext(ctx, `SELECT `+verifyCols+` FROM backup_verifications WHERE kind=? AND service_id=?
		ORDER BY started_at DESC LIMIT 1`, kind, serviceID))
}

// DeleteVerification removes a record (a run the agent refused to start).
func (s *Store) DeleteVerification(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM backup_verifications WHERE id=?`, id)
	return err
}

// FailStaleVerifications marks records still "running" after maxAge as failed.
func (s *Store) FailStaleVerifications(ctx context.Context, maxAge time.Duration) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE backup_verifications SET status='failed', error='no result from the agent (interrupted?)', finished_at=?
		WHERE status='running' AND started_at < ?`, TS(time.Now()), TS(time.Now().Add(-maxAge)))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneVerifications removes records older than maxAge (the newest of each kind and service
// is kept, so the panel always shows the last result) and drills of deleted services.
func (s *Store) PruneVerifications(ctx context.Context, now time.Time, maxAge time.Duration) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM backup_verifications WHERE
		(kind='drill' AND service_id NOT IN (SELECT id FROM services)) OR
		(started_at < ? AND status <> 'running' AND id NOT IN (
			SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY kind, service_id ORDER BY started_at DESC) AS n FROM backup_verifications) WHERE n = 1))`,
		TS(now.Add(-maxAge)))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
