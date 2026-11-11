package store

import (
	"context"
	"strings"
	"testing"
)

// Migration 0031 drops the command text of service.exec rows written before v0.1.45.
func TestMigrationRedactsOldExecAudit(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	ctx := context.Background()
	s.Audit(ctx, "admin", "service.exec", "svc_1", []string{"sh", "-c", "PGPASSWORD=hunter2 psql"})
	s.Audit(ctx, "admin", "service.exec", "svc_1", map[string]any{"program": "ls", "args": 1, "sha256": "ab"})
	s.Audit(ctx, "admin", "service.create", "svc_1", []string{"keep", "me"})
	if _, err := s.DB.Exec(`DELETE FROM schema_migrations WHERE name='0031_redact_exec_audit.sql'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, dir)
	defer s.Close()
	rows, err := s.ListAudit(ctx, AuditFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var redacted, kept, other int
	for _, r := range rows {
		if strings.Contains(r.Detail, "hunter2") {
			t.Fatalf("old exec command survived: %s", r.Detail)
		}
		switch {
		case r.Action == "service.exec" && r.Detail == `{"redacted":true}`:
			redacted++
		case r.Action == "service.exec" && strings.Contains(r.Detail, `"sha256"`):
			kept++
		case r.Action == "service.create" && strings.Contains(r.Detail, "keep"):
			other++
		}
	}
	if redacted != 1 || kept != 1 || other != 1 {
		t.Fatalf("redacted=%d kept=%d other=%d", redacted, kept, other)
	}
}
