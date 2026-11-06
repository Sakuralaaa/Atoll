package store

import "context"

// Member root domains (sub-accounts B7, migration 0027).

// MemberRootDomains returns the configured root domains user id may use (a root deleted
// since is ignored).
func (s *Store) MemberRootDomains(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT m.root FROM member_root_domains m JOIN root_domains r ON r.name = m.root
		WHERE m.user_id=? ORDER BY m.root`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SetMemberRootDomains replaces user id's allowed roots.
func (s *Store) SetMemberRootDomains(ctx context.Context, userID int64, roots []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM member_root_domains WHERE user_id=?`, userID); err != nil {
		return err
	}
	for _, r := range roots {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO member_root_domains(user_id, root) VALUES(?,?)`, userID, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}
