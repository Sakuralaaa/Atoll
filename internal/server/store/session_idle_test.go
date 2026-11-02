package store

import (
	"context"
	"testing"
	"time"
)

func TestSessionIdleExpiry(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	u, err := s.CreateOwner(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, "fresh", u.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "fresh"); err != nil {
		t.Fatalf("fresh session: %v", err)
	}
	var seen string
	s.DB.QueryRow(`SELECT last_seen_at FROM sessions WHERE token_hash='fresh'`).Scan(&seen)
	if seen == "" {
		t.Fatal("last_seen_at not recorded")
	}

	if err := s.CreateSession(ctx, "idle", u.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	s.DB.Exec(`UPDATE sessions SET last_seen_at=? WHERE token_hash='idle'`, TS(time.Now().Add(-SessionIdle-time.Minute)))
	if _, err := s.SessionUser(ctx, "idle"); err == nil {
		t.Fatal("idle session still valid")
	}
	var n int
	s.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash='idle'`).Scan(&n)
	if n != 0 {
		t.Fatal("idle session not deleted")
	}
}
