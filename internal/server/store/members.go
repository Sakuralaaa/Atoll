package store

import (
	"context"
	"strings"
	"time"
)

// Sub-accounts (notes/subaccounts.md): members and the servers assigned to them.

// ListUsers returns every account, oldest first.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// CreateMember adds a member account (role member) with its permissions and servers.
func (s *Store) CreateMember(ctx context.Context, username, hash string, perms, servers []string, mustChange bool) (*User, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now()
	mc := 0
	if mustChange {
		mc = 1
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users(username, password_hash, created_at, role, permissions, must_change_password) VALUES(?,?,?,?,?,?)`,
		username, hash, TS(now), RoleMember, strings.Join(perms, ","), mc)
	if err != nil {
		return nil, mapErr(err)
	}
	uid, _ := res.LastInsertId()
	for _, sid := range servers {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_assignments(server_id, user_id, created_at) VALUES(?,?,?)`, sid, uid, TS(now)); err != nil {
			return nil, mapErr(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.UserByID(ctx, uid)
}

// MemberUpdate changes a member; nil fields stay.
type MemberUpdate struct {
	Permissions  *[]string
	Servers      *[]string
	Disabled     *bool
	PasswordHash *string
	MustChange   *bool
}

// UpdateMember applies u to member id. Disabling it or changing its password also drops
// its sessions and API tokens, so the change takes effect on the next request.
func (s *Store) UpdateMember(ctx context.Context, id int64, u MemberUpdate) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id=?`, id).Scan(&role); err != nil {
		return mapErr(err)
	}
	if role != RoleMember {
		return ErrConflict
	}
	revoke := false
	if u.Permissions != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET permissions=? WHERE id=?`, strings.Join(*u.Permissions, ","), id); err != nil {
			return err
		}
	}
	if u.Disabled != nil {
		d := 0
		if *u.Disabled {
			d, revoke = 1, true
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET disabled=? WHERE id=?`, d, id); err != nil {
			return err
		}
	}
	if u.PasswordHash != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, *u.PasswordHash, id); err != nil {
			return err
		}
		revoke = true
	}
	if u.MustChange != nil {
		mc := 0
		if *u.MustChange {
			mc = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET must_change_password=? WHERE id=?`, mc, id); err != nil {
			return err
		}
	}
	if u.Servers != nil {
		if _, err := tx.ExecContext(ctx, `DELETE FROM server_assignments WHERE user_id=?`, id); err != nil {
			return err
		}
		now := TS(time.Now())
		for _, sid := range *u.Servers {
			if _, err := tx.ExecContext(ctx, `INSERT INTO server_assignments(server_id, user_id, created_at) VALUES(?,?,?)`, sid, id, now); err != nil {
				return mapErr(err)
			}
		}
	}
	if revoke {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM api_tokens WHERE user_id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteMember removes a member account (its sessions, tokens and assignments cascade).
func (s *Store) DeleteMember(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id=? AND role=?`, id, RoleMember)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AssignedServers returns the server ids assigned to user id.
func (s *Store) AssignedServers(ctx context.Context, userID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT server_id FROM server_assignments WHERE user_id=? ORDER BY server_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MemberScope is what a member may reach: its servers and the projects on them.
type MemberScope struct {
	Servers  map[string]bool
	Projects map[string]bool
}

// MemberScopeOf reads user id's servers and the projects whose server is one of them.
// It is read on every request, so an admin's change applies at once.
func (s *Store) MemberScopeOf(ctx context.Context, userID int64) (MemberScope, error) {
	sc := MemberScope{Servers: map[string]bool{}, Projects: map[string]bool{}}
	srv, err := s.AssignedServers(ctx, userID)
	if err != nil {
		return sc, err
	}
	for _, id := range srv {
		sc.Servers[id] = true
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id FROM projects p JOIN server_assignments a ON a.server_id = p.server_id WHERE a.user_id=?`, userID)
	if err != nil {
		return sc, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return sc, err
		}
		sc.Projects[id] = true
	}
	return sc, rows.Err()
}
