package store

import (
	"context"
	"testing"
	"time"
)

// Panel reads go through the read pool, so a long write (one connection) does not stall them.
func TestReadsDoNotWaitForWriter(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES('held', 'x')`); err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := s.ListNodes(rctx); err != nil {
		t.Fatalf("read blocked by an open write transaction: %v", err)
	}
	if _, err := s.ListAudit(rctx, AuditFilter{}); err != nil {
		t.Fatalf("audit read blocked: %v", err)
	}
	// the read pool refuses writes
	if _, err := s.r.ExecContext(ctx, `DELETE FROM settings`); err == nil {
		t.Fatal("read pool accepted a write")
	}
}
