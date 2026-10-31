package store

import (
	"context"
	"errors"
	"time"
)

// ImageUpdateRow is the stored policy and last check of one service's image.
type ImageUpdateRow struct {
	ServiceID      string
	Policy         string
	Window         string
	Image          string // ref the check result belongs to
	LatestDigest   string
	NewerTag       string
	MajorTag       string
	Error          string
	CheckedAt      time.Time // zero = never
	NextCheckAt    time.Time // zero = due now
	Failures       int
	LastAutoDigest string
}

const iuCols = `service_id, policy, maint_window, image, latest_digest, newer_tag, major_tag, error, checked_at, next_check_at, failures, last_auto_digest`

func scanIU(row interface{ Scan(...any) error }) (*ImageUpdateRow, error) {
	var r ImageUpdateRow
	var ca, na string
	if err := row.Scan(&r.ServiceID, &r.Policy, &r.Window, &r.Image, &r.LatestDigest, &r.NewerTag, &r.MajorTag, &r.Error, &ca, &na, &r.Failures, &r.LastAutoDigest); err != nil {
		return nil, mapErr(err)
	}
	if ca != "" {
		r.CheckedAt = parseTS(ca)
	}
	if na != "" {
		r.NextCheckAt = parseTS(na)
	}
	return &r, nil
}

func tsOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return TS(t)
}

// GetImageUpdate returns the row for a service, or a default (policy notify, never
// checked) when none is stored yet.
func (s *Store) GetImageUpdate(ctx context.Context, serviceID string) (*ImageUpdateRow, error) {
	r, err := scanIU(s.DB.QueryRowContext(ctx, `SELECT `+iuCols+` FROM image_updates WHERE service_id=?`, serviceID))
	if errors.Is(err, ErrNotFound) {
		return &ImageUpdateRow{ServiceID: serviceID, Policy: "notify"}, nil
	}
	return r, err
}

// ListImageUpdates returns every stored row keyed by service id.
func (s *Store) ListImageUpdates(ctx context.Context) (map[string]*ImageUpdateRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+iuCols+` FROM image_updates`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*ImageUpdateRow{}
	for rows.Next() {
		r, err := scanIU(rows)
		if err != nil {
			return nil, err
		}
		out[r.ServiceID] = r
	}
	return out, rows.Err()
}

// SaveImageUpdate upserts the full row.
func (s *Store) SaveImageUpdate(ctx context.Context, r ImageUpdateRow) error {
	if r.Policy == "" {
		r.Policy = "notify"
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO image_updates(`+iuCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(service_id) DO UPDATE SET policy=excluded.policy, maint_window=excluded.maint_window, image=excluded.image,
		latest_digest=excluded.latest_digest, newer_tag=excluded.newer_tag, major_tag=excluded.major_tag, error=excluded.error,
		checked_at=excluded.checked_at, next_check_at=excluded.next_check_at, failures=excluded.failures, last_auto_digest=excluded.last_auto_digest`,
		r.ServiceID, r.Policy, r.Window, r.Image, r.LatestDigest, r.NewerTag, r.MajorTag, r.Error,
		tsOrEmpty(r.CheckedAt), tsOrEmpty(r.NextCheckAt), r.Failures, r.LastAutoDigest)
	return mapErr(err)
}

// ImageCheckTargets lists every service id with its image, in a stable order.
func (s *Store) ImageCheckTargets(ctx context.Context) ([][2]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, image FROM services ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var id, img string
		if err := rows.Scan(&id, &img); err != nil {
			return nil, err
		}
		out = append(out, [2]string{id, img})
	}
	return out, rows.Err()
}
