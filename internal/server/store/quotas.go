package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Member quotas (sub-accounts B3). Usage counts every service in the projects on the member's
// assigned servers (the projects it sees), with the CPU and memory limits they were given;
// a service without a limit adds nothing to that dimension and is counted in Unlimited.

// MemberQuota returns user id's quota (zero = none).
func (s *Store) MemberQuota(ctx context.Context, userID int64) (api.MemberQuota, error) {
	var q api.MemberQuota
	err := s.DB.QueryRowContext(ctx, `SELECT cpu, mem_mb, services FROM member_quotas WHERE user_id=?`, userID).Scan(&q.CPU, &q.MemMB, &q.Services)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return q, err
}

// SetMemberQuota stores q for user id; a zero quota removes the row.
func (s *Store) SetMemberQuota(ctx context.Context, userID int64, q api.MemberQuota) error {
	if q == (api.MemberQuota{}) {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM member_quotas WHERE user_id=?`, userID)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO member_quotas(user_id, cpu, mem_mb, services) VALUES(?,?,?,?)
		ON CONFLICT(user_id) DO UPDATE SET cpu=excluded.cpu, mem_mb=excluded.mem_mb, services=excluded.services`, userID, q.CPU, q.MemMB, q.Services)
	return err
}

// MemberUsage sums the services in the projects on user id's servers.
func (s *Store) MemberUsage(ctx context.Context, userID int64) (api.MemberUsage, error) {
	var u api.MemberUsage
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(s.cpu_limit), 0), COALESCE(SUM(s.mem_limit_mb), 0),
			COALESCE(SUM(CASE WHEN s.cpu_limit = 0 OR s.mem_limit_mb = 0 THEN 1 ELSE 0 END), 0)
		FROM services s JOIN projects p ON p.id = s.project_id
		WHERE p.server_id IN (SELECT server_id FROM server_assignments WHERE user_id=?)`, userID).
		Scan(&u.Services, &u.CPU, &u.MemMB, &u.Unlimited)
	return u, err
}

// ProjectUsage sums the services of one project (what a migration moves).
func (s *Store) ProjectUsage(ctx context.Context, projectID string) (api.MemberUsage, error) {
	var u api.MemberUsage
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(cpu_limit), 0), COALESCE(SUM(mem_limit_mb), 0) FROM services WHERE project_id=?`, projectID).
		Scan(&u.Services, &u.CPU, &u.MemMB)
	return u, err
}

// MembersOnServer returns the active members assigned server id.
func (s *Store) MembersOnServer(ctx context.Context, serverID string) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userColsU+` FROM users u JOIN server_assignments a ON a.user_id = u.id
		WHERE a.server_id=? AND u.role=? AND u.disabled=0 ORDER BY u.id`, serverID, RoleMember)
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
