package store

import (
	"context"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ProjectPrefs returns the user's favorite projects in their saved order and pinned projects
// in their pinned order.
func (s *Store) ProjectPrefs(ctx context.Context, userID int64) (api.ProjectPrefs, error) {
	out := api.ProjectPrefs{Favorites: []string{}, Pinned: []string{}}
	list := func(q string) ([]string, error) {
		ids := []string{}
		rows, err := s.DB.QueryContext(ctx, q, userID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	var err error
	if out.Favorites, err = list(`SELECT project_id FROM project_prefs WHERE user_id=? AND favorite=1 ORDER BY fav_order, project_id`); err != nil {
		return out, err
	}
	if out.Pinned, err = list(`SELECT project_id FROM project_prefs WHERE user_id=? AND pin_order>0 ORDER BY pin_order, project_id`); err != nil {
		return out, err
	}
	return out, nil
}

// SetProjectPrefs replaces the user's favorites and/or pins (a nil list is left as is).
// Both lists are saved in the order given (duplicates dropped), so a newly starred project
// sent at the end of the list lands at the end of the favorites. Ids that are not existing
// projects are skipped.
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
		if _, err := tx.ExecContext(ctx, `UPDATE project_prefs SET favorite=0, fav_order=0 WHERE user_id=?`, userID); err != nil {
			return err
		}
		seen := map[string]bool{}
		n := 0
		for _, id := range favorites {
			if seen[id] {
				continue
			}
			seen[id] = true
			n++
			if _, err := tx.ExecContext(ctx, `INSERT INTO project_prefs(user_id, project_id, favorite, fav_order)
				SELECT ?, id, 1, ? FROM projects WHERE id=?
				ON CONFLICT(user_id, project_id) DO UPDATE SET favorite=1, fav_order=excluded.fav_order`, userID, n, id); err != nil {
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
