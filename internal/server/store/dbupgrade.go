package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// SaveDBUpgrade replaces the latest database upgrade of its service.
func (s *Store) SaveDBUpgrade(ctx context.Context, u api.DBUpgrade) error {
	steps, _ := json.Marshal(u.Steps)
	var fin any
	if u.FinishedAt != nil {
		fin = u.FinishedAt.Unix()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO db_upgrades (service_id, status, from_image, to_image, steps, error, old_kept, started_at, finished_at, by_actor)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(service_id) DO UPDATE SET status=excluded.status, from_image=excluded.from_image, to_image=excluded.to_image,
		steps=excluded.steps, error=excluded.error, old_kept=excluded.old_kept, started_at=excluded.started_at,
		finished_at=excluded.finished_at, by_actor=excluded.by_actor`,
		u.ServiceID, u.Status, u.FromImage, u.ToImage, string(steps), u.Error, boolInt(u.OldKept), u.StartedAt.Unix(), fin, u.By)
	return err
}

// GetDBUpgrade returns the latest database upgrade of a service, nil when there was none.
func (s *Store) GetDBUpgrade(ctx context.Context, serviceID string) (*api.DBUpgrade, error) {
	var u api.DBUpgrade
	var steps string
	var kept int
	var started int64
	var fin sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT service_id, status, from_image, to_image, steps, error, old_kept, started_at, finished_at, by_actor
		FROM db_upgrades WHERE service_id=?`, serviceID).Scan(&u.ServiceID, &u.Status, &u.FromImage, &u.ToImage, &steps, &u.Error, &kept, &started, &fin, &u.By)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(steps), &u.Steps)
	if u.Steps == nil {
		u.Steps = []api.DBUpgradeStep{}
	}
	u.OldKept = kept != 0
	u.StartedAt = time.Unix(started, 0).UTC()
	if fin.Valid {
		t := time.Unix(fin.Int64, 0).UTC()
		u.FinishedAt = &t
	}
	return &u, nil
}

// InterruptDBUpgrades marks upgrades still running when the control server stopped as failed.
func (s *Store) InterruptDBUpgrades(ctx context.Context, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE db_upgrades SET status=?, error=?, old_kept=1, finished_at=? WHERE status=?`,
		api.DBUpgradeFailed, "interrupted: the control server restarted during the upgrade; if the service does not run, roll back to put the old data back", now.Unix(), api.DBUpgradeRunning)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
