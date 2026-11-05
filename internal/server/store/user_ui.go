package store

import (
	"context"
	"strings"
)

// UserUI is an account's panel look: its avatar and sidebar order.
type UserUI struct {
	Avatar   string   // data: URL, or "" for initials
	NavOrder []string // sidebar entry keys in display order; empty = default
}

// UserUI returns the account's avatar and sidebar order.
func (s *Store) UserUI(ctx context.Context, userID int64) (UserUI, error) {
	var u UserUI
	var order string
	if err := s.DB.QueryRowContext(ctx, `SELECT avatar, nav_order FROM users WHERE id=?`, userID).Scan(&u.Avatar, &order); err != nil {
		return u, mapErr(err)
	}
	if order != "" {
		u.NavOrder = strings.Split(order, ",")
	}
	return u, nil
}

// SetAvatar stores the account's avatar ("" clears it).
func (s *Store) SetAvatar(ctx context.Context, userID int64, avatar string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET avatar=? WHERE id=?`, avatar, userID)
	return err
}

// SetNavOrder stores the account's sidebar order (nil resets it).
func (s *Store) SetNavOrder(ctx context.Context, userID int64, keys []string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET nav_order=? WHERE id=?`, strings.Join(keys, ","), userID)
	return err
}
