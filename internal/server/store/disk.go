package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// DiskHistoryBucket is the width of one disk_usage_history row.
const DiskHistoryBucket = 600 // seconds

// DiskHistoryTTL is how long disk history is kept.
const DiskHistoryTTL = 8 * 24 * time.Hour

// rootfsKey is the volume_usage row of the container's writable layer.
const rootfsKey = ""

// SaveDiskSample replaces the newest measurement of a service whose effective server is nodeID
// (samples for services of other servers, or unknown ones, are ignored and return false) and
// records its total in the history. mounted lists the service's mounted volume names; only they
// count towards the total.
func (s *Store) SaveDiskSample(ctx context.Context, nodeID string, at time.Time, smp api.ServiceDiskSample) (bool, error) {
	var node string
	err := s.DB.QueryRowContext(ctx, `SELECT `+EffNode+` FROM services s JOIN projects p ON p.id = s.project_id WHERE s.id=?`, smp.ServiceID).Scan(&node)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if node != nodeID {
		return false, nil
	}
	svc, _, err := s.GetService(ctx, smp.ServiceID)
	if err != nil {
		return false, err
	}
	mounted := map[string]bool{}
	for _, v := range svc.Volumes {
		if v.HostPath == "" {
			mounted[v.Name] = true
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM volume_usage WHERE service_id=?`, smp.ServiceID); err != nil {
		return false, err
	}
	ts := at.Unix()
	var total int64
	for _, v := range smp.Volumes {
		if v.Name == rootfsKey {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO volume_usage(service_id, volume, bytes, at) VALUES(?,?,?,?)`, smp.ServiceID, v.Name, v.Bytes, ts); err != nil {
			return false, err
		}
		if mounted[v.Name] {
			total += v.Bytes
		}
	}
	if smp.RootfsBytes >= 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO volume_usage(service_id, volume, bytes, at) VALUES(?,?,?,?)`, smp.ServiceID, rootfsKey, smp.RootfsBytes, ts); err != nil {
			return false, err
		}
		total += smp.RootfsBytes
	}
	b := ts / DiskHistoryBucket * DiskHistoryBucket
	if _, err := tx.ExecContext(ctx, `INSERT INTO disk_usage_history(service_id, at, bytes) VALUES(?,?,?)
		ON CONFLICT(service_id, at) DO UPDATE SET bytes=excluded.bytes`, smp.ServiceID, b, total); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// VolumeUsageRow is one stored measurement ("" volume = writable layer).
type VolumeUsageRow struct {
	Volume string
	Bytes  int64
	At     time.Time
}

func (s *Store) VolumeUsage(ctx context.Context, serviceID string) ([]VolumeUsageRow, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT volume, bytes, at FROM volume_usage WHERE service_id=? ORDER BY volume`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VolumeUsageRow
	for rows.Next() {
		var r VolumeUsageRow
		var at int64
		if err := rows.Scan(&r.Volume, &r.Bytes, &at); err != nil {
			return nil, err
		}
		r.At = time.Unix(at, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// DiskHistory returns the totals since `since`, one point per step seconds (the newest sample
// of each step).
func (s *Store) DiskHistory(ctx context.Context, serviceID string, since time.Time, step int64) ([]api.DiskPoint, error) {
	if step < DiskHistoryBucket {
		step = DiskHistoryBucket
	}
	rows, err := s.r.QueryContext(ctx, `SELECT (at/?)*?, bytes FROM disk_usage_history h
		WHERE service_id=? AND at>=? AND at = (SELECT MAX(at) FROM disk_usage_history WHERE service_id=h.service_id AND at/? = h.at/?)
		ORDER BY 1`, step, step, serviceID, since.Unix(), step, step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.DiskPoint{}
	for rows.Next() {
		var at, b int64
		if err := rows.Scan(&at, &b); err != nil {
			return nil, err
		}
		out = append(out, api.DiskPoint{At: time.Unix(at, 0).UTC(), Bytes: b})
	}
	return out, rows.Err()
}

// VolumeLimits returns the limits of a service by volume name.
func (s *Store) VolumeLimits(ctx context.Context, serviceID string) (map[string]api.VolumeLimit, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT volume, limit_mb, over_limit FROM volume_limits WHERE service_id=?`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]api.VolumeLimit{}
	for rows.Next() {
		var v string
		var l api.VolumeLimit
		if err := rows.Scan(&v, &l.LimitMB, &l.OverLimit); err != nil {
			return nil, err
		}
		out[v] = l
	}
	return out, rows.Err()
}

// SetVolumeLimit sets (LimitMB > 0) or removes a volume's limit.
func (s *Store) SetVolumeLimit(ctx context.Context, serviceID, volume string, l api.VolumeLimit) error {
	if l.LimitMB <= 0 {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM volume_limits WHERE service_id=? AND volume=?`, serviceID, volume)
		return err
	}
	if l.OverLimit != api.OverLimitAlert && l.OverLimit != api.OverLimitStop {
		return fmt.Errorf("over_limit must be stop or alert")
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO volume_limits(service_id, volume, limit_mb, over_limit) VALUES(?,?,?,?)
		ON CONFLICT(service_id, volume) DO UPDATE SET limit_mb=excluded.limit_mb, over_limit=excluded.over_limit`,
		serviceID, volume, l.LimitMB, l.OverLimit)
	return err
}

// DiskEnforcement is when a service was last stopped for a full volume and until when a manual
// start keeps it from being stopped again.
type DiskEnforcement struct {
	StoppedAt  time.Time
	GraceUntil time.Time
}

func (s *Store) GetDiskEnforcement(ctx context.Context, serviceID string) (DiskEnforcement, error) {
	var a, b int64
	err := s.r.QueryRowContext(ctx, `SELECT stopped_at, grace_until FROM disk_enforcement WHERE service_id=?`, serviceID).Scan(&a, &b)
	if err == sql.ErrNoRows {
		return DiskEnforcement{}, nil
	}
	var e DiskEnforcement
	if a > 0 {
		e.StoppedAt = time.Unix(a, 0).UTC()
	}
	if b > 0 {
		e.GraceUntil = time.Unix(b, 0).UTC()
	}
	return e, err
}

// MarkDiskStopped records that the service was stopped for a full volume.
func (s *Store) MarkDiskStopped(ctx context.Context, serviceID string, at time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO disk_enforcement(service_id, stopped_at) VALUES(?,?)
		ON CONFLICT(service_id) DO UPDATE SET stopped_at=excluded.stopped_at`, serviceID, at.Unix())
	return err
}

// SetDiskGrace keeps the service from being stopped for a full volume until `until`.
func (s *Store) SetDiskGrace(ctx context.Context, serviceID string, until time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO disk_enforcement(service_id, grace_until) VALUES(?,?)
		ON CONFLICT(service_id) DO UPDATE SET grace_until=excluded.grace_until`, serviceID, until.Unix())
	return err
}

// PruneDiskHistory drops history older than DiskHistoryTTL.
func (s *Store) PruneDiskHistory(ctx context.Context, now time.Time) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM disk_usage_history WHERE at < ?`, now.Add(-DiskHistoryTTL).Unix())
	return err
}

// VolumeOver is a limited volume at or above api.VolumeWarnRatio of its limit.
type VolumeOver struct {
	ServiceID, ServiceName, ProjectID, ServerID string
	Volume                                      string
	Bytes                                       int64
	LimitMB                                     int
	OverLimit                                   string
	Stopped                                     bool // the service is stopped (desired)
}

func (s *Store) VolumesNearLimit(ctx context.Context) ([]VolumeOver, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT s.id, s.name, s.project_id, `+EffNode+`, l.volume, u.bytes, l.limit_mb, l.over_limit, s.desired_stopped
		FROM volume_limits l JOIN volume_usage u ON u.service_id = l.service_id AND u.volume = l.volume
		JOIN services s ON s.id = l.service_id JOIN projects p ON p.id = s.project_id
		WHERE u.bytes >= l.limit_mb * 1048576 * ?
		ORDER BY s.id, l.volume`, api.VolumeWarnRatio)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VolumeOver
	for rows.Next() {
		var v VolumeOver
		if err := rows.Scan(&v.ServiceID, &v.ServiceName, &v.ProjectID, &v.ServerID, &v.Volume, &v.Bytes, &v.LimitMB, &v.OverLimit, &v.Stopped); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// MountedVolumesNearLimit is VolumesNearLimit without volumes the service no longer mounts.
func (s *Store) MountedVolumesNearLimit(ctx context.Context) ([]VolumeOver, error) {
	all, err := s.VolumesNearLimit(ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	mounted := map[string]map[string]bool{}
	for _, v := range all {
		m, ok := mounted[v.ServiceID]
		if !ok {
			m = map[string]bool{}
			if svc, _, err := s.GetService(ctx, v.ServiceID); err == nil {
				for _, x := range svc.Volumes {
					if x.HostPath == "" {
						m[x.Name] = true
					}
				}
			}
			mounted[v.ServiceID] = m
		}
		if m[v.Volume] {
			out = append(out, v)
		}
	}
	return out, nil
}

// HumanMB formats bytes as MB below 10 GB and GB above ("850 MB", "12.4 GB").
func HumanMB(b int64) string {
	mb := float64(b) / (1 << 20)
	if mb >= 10*1024 {
		return fmt.Sprintf("%.1f GB", mb/1024)
	}
	return fmt.Sprintf("%.0f MB", mb)
}
