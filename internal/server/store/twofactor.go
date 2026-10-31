package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// TOTPState is a user's two-factor state. Secrets are ciphertext (Store.Box).
type TOTPState struct {
	Enabled   bool
	EnabledAt *time.Time
	Secret    string // encrypted, confirmed secret ("" when disabled)
	Pending   string // encrypted secret awaiting confirmation
	LastStep  int64
}

// TOTPState loads a user's two-factor state.
func (s *Store) TOTPState(ctx context.Context, userID int64) (*TOTPState, error) {
	var st TOTPState
	var en int
	var at string
	err := s.DB.QueryRowContext(ctx, `SELECT totp_enabled, totp_enabled_at, totp_secret, totp_pending, totp_last_step FROM users WHERE id=?`, userID).
		Scan(&en, &at, &st.Secret, &st.Pending, &st.LastStep)
	if err != nil {
		return nil, mapErr(err)
	}
	st.Enabled = en == 1
	if at != "" {
		t := parseTS(at)
		st.EnabledAt = &t
	}
	return &st, nil
}

// SetTOTPPending stores an encrypted secret that still has to be confirmed with a code.
func (s *Store) SetTOTPPending(ctx context.Context, userID int64, enc string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET totp_pending=? WHERE id=?`, enc, userID)
	return err
}

// AdvanceTOTPStep records step as the last accepted TOTP step. It reports false when
// a code at this step (or a later one) was already used — a replay.
func (s *Store) AdvanceTOTPStep(ctx context.Context, userID, step int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET totp_last_step=? WHERE id=? AND totp_last_step < ?`, step, userID, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func insertRecoveryCodes(ctx context.Context, tx *sql.Tx, userID int64, hashes []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id=?`, userID); err != nil {
		return err
	}
	now := TS(time.Now())
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recovery_codes(user_id, code_hash, created_at) VALUES(?,?,?)`, userID, h, now); err != nil {
			return err
		}
	}
	return nil
}

func deleteSessionsExcept(ctx context.Context, tx *sql.Tx, userID int64, keepHash string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, userID, keepHash)
	return err
}

// EnableTOTP promotes the pending secret, stores fresh recovery code hashes and signs
// out every other session of the user (keepHash survives; "" = none).
func (s *Store) EnableTOTP(ctx context.Context, userID int64, step int64, codeHashes []string, keepHash string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE users SET totp_secret=totp_pending, totp_pending='', totp_enabled=1, totp_enabled_at=?, totp_last_step=?
		WHERE id=? AND totp_pending<>''`, TS(time.Now()), step, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := insertRecoveryCodes(ctx, tx, userID, codeHashes); err != nil {
		return err
	}
	if err := deleteSessionsExcept(ctx, tx, userID, keepHash); err != nil {
		return err
	}
	return tx.Commit()
}

// DisableTOTP turns 2FA off, forgets the secret and recovery codes and signs out every
// other session (keepHash survives; "" = all).
func (s *Store) DisableTOTP(ctx context.Context, userID int64, keepHash string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET totp_secret='', totp_pending='', totp_enabled=0, totp_enabled_at='', totp_last_step=0 WHERE id=?`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM recovery_codes WHERE user_id=?`, userID); err != nil {
		return err
	}
	if err := deleteSessionsExcept(ctx, tx, userID, keepHash); err != nil {
		return err
	}
	return tx.Commit()
}

// ReplaceRecoveryCodes swaps all of a user's recovery codes for new hashes.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertRecoveryCodes(ctx, tx, userID, hashes); err != nil {
		return err
	}
	return tx.Commit()
}

// ConsumeRecoveryCode marks an unused recovery code as used; false if there is none.
func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID int64, hash string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE recovery_codes SET used_at=? WHERE user_id=? AND code_hash=? AND used_at IS NULL`, TS(time.Now()), userID, hash)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RecoveryCodesLeft counts unused recovery codes.
func (s *Store) RecoveryCodesLeft(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM recovery_codes WHERE user_id=? AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

// ResetTOTPByName disables 2FA for username ("" = owner) and signs that user out everywhere.
func (s *Store) ResetTOTPByName(ctx context.Context, username string) (*User, error) {
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
	return u, s.DisableTOTP(ctx, u.ID, "")
}

// ---------- session metadata ----------

// SessionRow describes one live panel session.
type SessionRow struct {
	Hash      string
	CreatedAt time.Time
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

// CreateSessionInfo stores a session token hash with the client's IP and user agent.
func (s *Store) CreateSessionInfo(ctx context.Context, hash string, userID int64, ttl time.Duration, ip, ua string) error {
	if len(ua) > 300 {
		ua = ua[:300]
	}
	now := time.Now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions(token_hash, user_id, created_at, expires_at, ip, user_agent) VALUES(?,?,?,?,?,?)`,
		hash, userID, TS(now), TS(now.Add(ttl)), ip, ua)
	return err
}

// ListSessions lists a user's unexpired sessions, newest first.
func (s *Store) ListSessions(ctx context.Context, userID int64) ([]SessionRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT token_hash, created_at, expires_at, ip, user_agent FROM sessions
		WHERE user_id=? AND expires_at > ? ORDER BY created_at DESC`, userID, TS(time.Now()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionRow{}
	for rows.Next() {
		var r SessionRow
		var c, e string
		if err := rows.Scan(&r.Hash, &c, &e, &r.IP, &r.UserAgent); err != nil {
			return nil, err
		}
		r.CreatedAt, r.ExpiresAt = parseTS(c), parseTS(e)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteUserSessionByPrefix removes the user's session whose token hash starts with prefix
// (the public session id). Exactly one row must match.
func (s *Store) DeleteUserSessionByPrefix(ctx context.Context, userID int64, prefix string) error {
	if len(prefix) < 12 || strings.ContainsAny(prefix, "%_") {
		return ErrNotFound
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND substr(token_hash, 1, ?) = ?`, userID, len(prefix), prefix)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOtherSessions signs a user out of every session except keepHash.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID int64, keepHash string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=? AND token_hash<>?`, userID, keepHash)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
