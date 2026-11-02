package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// User is an account that can log into the panel.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// CountUsers returns the number of users.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateOwner creates the first user and its team inside one transaction; it
// fails with ErrConflict if any user exists.
func (s *Store) CreateOwner(ctx context.Context, username, hash string) (*User, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrConflict
	}
	now := time.Now()
	res, err := tx.ExecContext(ctx, `INSERT INTO users(username, password_hash, created_at) VALUES(?,?,?)`, username, hash, TS(now))
	if err != nil {
		return nil, mapErr(err)
	}
	uid, _ := res.LastInsertId()
	res, err = tx.ExecContext(ctx, `INSERT INTO teams(name) VALUES('default')`)
	if err != nil {
		return nil, err
	}
	tid, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `INSERT INTO team_members(team_id, user_id, role) VALUES(?,?, 'owner')`, tid, uid); err != nil {
		return nil, err
	}
	return &User{ID: uid, Username: username, PasswordHash: hash, CreatedAt: now}, tx.Commit()
}

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var c string
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &c); err != nil {
		return nil, mapErr(err)
	}
	u.CreatedAt = parseTS(c)
	return &u, nil
}

// UserByName looks a user up by username.
func (s *Store) UserByName(ctx context.Context, name string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE username=?`, name))
}

// UserByID looks a user up by id.
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE id=?`, id))
}

// CreateSession stores a session token hash.
func (s *Store) CreateSession(ctx context.Context, hash string, userID int64, ttl time.Duration) error {
	now := time.Now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions(token_hash, user_id, created_at, expires_at) VALUES(?,?,?,?)`,
		hash, userID, TS(now), TS(now.Add(ttl)))
	return err
}

// SessionUser returns the user for a valid session.
func (s *Store) SessionUser(ctx context.Context, hash string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT u.id, u.username, u.password_hash, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash=? AND s.expires_at > ?`, hash, TS(time.Now())))
}

// DeleteSession removes a session.
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hash)
	return err
}

// ---------- API tokens ----------

// TokenAuth is the result of looking up a PAT.
type TokenAuth struct {
	TokenID         string
	Name            string
	Scope           string
	Permissions     []string
	Projects        []string
	RequireApproval bool
	User            *User
}

func splitList(s string) []string {
	out := []string{}
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// CreateAPIToken stores a PAT.
func (s *Store) CreateAPIToken(ctx context.Context, userID int64, t api.APIToken, hash string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO api_tokens(id, user_id, name, scope, prefix, token_hash, created_at, permissions, projects, require_approval) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		t.ID, userID, t.Name, t.Scope, t.Prefix, hash, TS(t.CreatedAt), strings.Join(t.Permissions, ","), strings.Join(t.Projects, ","), b2i(t.RequireApproval))
	return mapErr(err)
}

const tokenCols = `id, name, scope, prefix, last_used_at, created_at, permissions, projects, require_approval`

func scanToken(row interface{ Scan(...any) error }) (*api.APIToken, error) {
	var t api.APIToken
	var last sql.NullString
	var c, perms, projects string
	var ra int
	if err := row.Scan(&t.ID, &t.Name, &t.Scope, &t.Prefix, &last, &c, &perms, &projects, &ra); err != nil {
		return nil, mapErr(err)
	}
	t.LastUsedAt = parseTSPtr(last)
	t.CreatedAt = parseTS(c)
	t.Permissions = splitList(perms)
	t.Projects = splitList(projects)
	t.RequireApproval = ra == 1
	return &t, nil
}

// ListAPITokens lists PATs (never the secret).
func (s *Store) ListAPITokens(ctx context.Context) ([]api.APIToken, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+tokenCols+` FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.APIToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// GetAPIToken returns one PAT (never the secret).
func (s *Store) GetAPIToken(ctx context.Context, id string) (*api.APIToken, error) {
	return scanToken(s.DB.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM api_tokens WHERE id=?`, id))
}

// UpdateAPIToken stores a token's name, scope summary, permissions, projects and approval flag.
func (s *Store) UpdateAPIToken(ctx context.Context, t api.APIToken) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE api_tokens SET name=?, scope=?, permissions=?, projects=?, require_approval=? WHERE id=?`,
		t.Name, t.Scope, strings.Join(t.Permissions, ","), strings.Join(t.Projects, ","), b2i(t.RequireApproval), t.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAPIToken removes a PAT.
func (s *Store) DeleteAPIToken(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM api_tokens WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AuthAPIToken resolves a PAT hash and bumps last_used_at.
func (s *Store) AuthAPIToken(ctx context.Context, hash string) (*TokenAuth, error) {
	var ta TokenAuth
	var uid int64
	var perms, projects string
	var ra int
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, scope, user_id, permissions, projects, require_approval FROM api_tokens WHERE token_hash=?`, hash).
		Scan(&ta.TokenID, &ta.Name, &ta.Scope, &uid, &perms, &projects, &ra)
	if err != nil {
		return nil, mapErr(err)
	}
	ta.Permissions, ta.Projects, ta.RequireApproval = splitList(perms), splitList(projects), ra == 1
	u, err := s.UserByID(ctx, uid)
	if err != nil {
		return nil, err
	}
	ta.User = u
	s.DB.ExecContext(ctx, `UPDATE api_tokens SET last_used_at=? WHERE id=?`, TS(time.Now()), ta.TokenID)
	return &ta, nil
}

// ---------- join & node tokens ----------

// CreateJoinToken stores a join token hash.
func (s *Store) CreateJoinToken(ctx context.Context, hash string, expires time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO join_tokens(token_hash, expires_at, created_at) VALUES(?,?,?)`, hash, TS(expires), TS(time.Now()))
	return err
}

// ConsumeJoinToken atomically marks a valid, unused join token as used by nodeID.
func (s *Store) ConsumeJoinToken(ctx context.Context, hash, nodeID string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE join_tokens SET used_at=?, node_id=? WHERE token_hash=? AND used_at IS NULL AND expires_at > ?`,
		TS(time.Now()), nodeID, hash, TS(time.Now()))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateNodeToken stores a node token hash.
func (s *Store) CreateNodeToken(ctx context.Context, hash, nodeID string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO node_tokens(token_hash, node_id, created_at) VALUES(?,?,?)`, hash, nodeID, TS(time.Now()))
	return err
}

// NodeByToken resolves a node token hash.
func (s *Store) NodeByToken(ctx context.Context, hash string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT node_id FROM node_tokens WHERE token_hash=?`, hash).Scan(&id)
	return id, mapErr(err)
}

// SetPassword replaces a user's password hash and signs out all of their sessions.
// An empty username picks the owner (lowest id).
func (s *Store) SetPassword(ctx context.Context, username, hash string) (*User, error) {
	var u *User
	var err error
	if username == "" {
		u, err = scanUser(s.DB.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users ORDER BY id LIMIT 1`))
	} else {
		u, err = s.UserByName(ctx, username)
	}
	if err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, hash, u.ID); err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, u.ID); err != nil {
		return nil, err
	}
	u.PasswordHash = hash
	return u, nil
}

// ChangePassword replaces a user's password hash and signs out every session except
// keepHash, in one transaction. It returns how many sessions were signed out.
func (s *Store) ChangePassword(ctx context.Context, userID int64, hash, keepHash string) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash=? WHERE id=?`, hash, userID)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotFound
	}
	res, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, userID, keepHash)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, tx.Commit()
}
