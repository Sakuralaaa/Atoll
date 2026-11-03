package store

import (
	"context"
	"time"
)

// Retention says how long history is kept. Zero disables that rule.
type Retention struct {
	Audit         time.Duration // audit_log rows
	Confirmations time.Duration // decided/expired confirmations (pending ones are never removed)
	Imports       time.Duration // finished imports: plan imports done/failed, zeabur takeovers cleaned/rolled_back
	Deployments   int           // finished deployments kept per service (the newest N; running/in-flight always kept)
}

// DefaultRetention keeps half a year of audit log and 50 deployments per service.
var DefaultRetention = Retention{Audit: 180 * 24 * time.Hour, Confirmations: 90 * 24 * time.Hour, Imports: 180 * 24 * time.Hour, Deployments: 50}

// PruneResult counts the rows PruneHistory removed.
type PruneResult struct {
	Audit, Confirmations, Imports, Deployments, Sessions int64
}

// PruneHistory removes history older than r (and expired sessions).
func (s *Store) PruneHistory(ctx context.Context, now time.Time, r Retention) (PruneResult, error) {
	var out PruneResult
	exec := func(n *int64, q string, args ...any) error {
		res, err := s.DB.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		c, _ := res.RowsAffected()
		*n += c
		return nil
	}
	if r.Audit > 0 {
		if err := exec(&out.Audit, `DELETE FROM audit_log WHERE created_at < ?`, TS(now.Add(-r.Audit))); err != nil {
			return out, err
		}
	}
	if r.Confirmations > 0 {
		if err := exec(&out.Confirmations, `DELETE FROM confirmations WHERE status <> 'pending' AND created_at < ?`, TS(now.Add(-r.Confirmations))); err != nil {
			return out, err
		}
	}
	if r.Imports > 0 {
		if err := exec(&out.Imports, `DELETE FROM imports WHERE created_at < ? AND (
			(kind = 'plan' AND status IN ('done','failed')) OR (kind = 'zeabur-local' AND status IN ('cleaned','rolled_back')))`,
			TS(now.Add(-r.Imports))); err != nil {
			return out, err
		}
	}
	if r.Deployments > 0 {
		// Keep the newest N per service, and never one still in flight or the running one.
		if err := exec(&out.Deployments, `DELETE FROM deployments WHERE status IN ('failed','superseded') AND id IN (
			SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY service_id ORDER BY started_at DESC) AS n FROM deployments) WHERE n > ?)`,
			r.Deployments); err != nil {
			return out, err
		}
	}
	if err := exec(&out.Sessions, `DELETE FROM sessions WHERE expires_at < ?`, TS(now)); err != nil {
		return out, err
	}
	return out, nil
}
