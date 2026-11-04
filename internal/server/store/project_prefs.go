package store

import (
	"context"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ProjectPrefs returns the user's favorite projects (sorted by id) and pinned projects in
// their pinned order.
func (s *Store) ProjectPrefs(ctx context.Context, userID int64) (api.ProjectPrefs, error) {
	out := api.ProjectPrefs{Favorites: []string{}, Pinned: []string{}}
	rows, err := s.DB.QueryContext(ctx, `SELECT project_id, favorite, pin_order FROM project_prefs
		WHERE user_id=? AND (favorite=1 OR pin_order>0) ORDER BY pin_order, project_id`, userID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var fav, pin int
		if err := rows.Scan(&id, &fav, &pin); err != nil {
			return out, err
		}
		if fav == 1 {
			out.Favorites = append(out.Favorites, id)
		}
		if pin > 0 {
			out.Pinned = append(out.Pinned, id)
		}
	}
	return out, rows.Err()
}

// SetProjectPrefs replaces the user's favorites and/or pins (a nil list is left as is).
// Ids that are not existing projects are skipped.
func (s *Store) SetProjectPrefs(ctx context.Context, userID int64, favorites, pinned []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	upsert := func(id, col string, v int) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO project_prefs(user_id, project_id, `+col+`)
			SELECT ?, id, ? FROM projects WHERE id=?
			ON CONFLICT(user_id, project_id) DO UPDATE SET `+col+`=excluded.`+col, userID, v, id)
		return err
	}
	if favorites != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE project_prefs SET favorite=0 WHERE user_id=?`, userID); err != nil {
			return err
		}
		for _, id := range favorites {
			if err := upsert(id, "favorite", 1); err != nil {
				return err
			}
		}
	}
	if pinned != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE project_prefs SET pin_order=0 WHERE user_id=?`, userID); err != nil {
			return err
		}
		seen := map[string]bool{}
		n := 0
		for _, id := range pinned {
			if seen[id] {
				continue
			}
			seen[id] = true
			n++
			if err := upsert(id, "pin_order", n); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_prefs WHERE user_id=? AND favorite=0 AND pin_order=0`, userID); err != nil {
		return err
	}
	return tx.Commit()
}
