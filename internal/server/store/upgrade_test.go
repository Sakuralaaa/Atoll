package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// openAt builds a database the way an older release left it: only the migrations up to
// and including upto ("0023_user_ui.sql" = v0.1.40). seed then writes rows through raw
// SQL, as that release would have, before the current Open migrates the rest.
func openAt(t *testing.T, dir, upto string, seed func(db *sql.DB)) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "atoll.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE schema_migrations (name TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	entries, _ := migrationFS.ReadDir("migrations")
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if n > upto {
			break
		}
		body, _ := migrationFS.ReadFile("migrations/" + n)
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		db.Exec(`INSERT INTO schema_migrations VALUES(?,?)`, n, TS(time.Now()))
	}
	seed(db)
}

// A v0.1.40 database (migrations through 0023) upgrades in place: existing users stay
// admins, its audit rows, projects and services stay readable.
func TestUpgradeFromV0140(t *testing.T) {
	dir := t.TempDir()
	now := TS(time.Now())
	openAt(t, dir, "0023_user_ui.sql", func(db *sql.DB) {
		for _, q := range []string{
			`INSERT INTO users(id, username, password_hash, created_at) VALUES(1,'admin','x','` + now + `')`,
			`INSERT INTO nodes(id, name, created_at) VALUES('srv_a','a','` + now + `')`,
			`INSERT INTO projects(id, name, server_id, created_at) VALUES('prj_a','a','srv_a','` + now + `')`,
			`INSERT INTO services(id, project_id, name, kind, image, created_at) VALUES('svc_a','prj_a','web','app','nginx','` + now + `')`,
			`INSERT INTO audit_log(actor, action, target, created_at, ip) VALUES('user:admin','project.create','prj_a','` + now + `','203.0.113.1')`,
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	})
	s := openTest(t, dir)
	defer s.Close()
	ctx := context.Background()
	u, err := s.UserByName(ctx, "admin")
	if err != nil || u.IsMember() || u.Disabled {
		t.Fatalf("admin after upgrade %+v %v", u, err)
	}
	rows, err := s.ListAudit(ctx, AuditFilter{Limit: 10, User: "admin"})
	if err != nil || len(rows) != 1 || rows[0].UserID != 0 || rows[0].UserName != "" || rows[0].IP != "203.0.113.1" {
		t.Fatalf("old audit row %+v %v", rows, err)
	}
	if _, _, err := s.GetService(ctx, "svc_a"); err != nil {
		t.Fatalf("service after upgrade: %v", err)
	}
}

// A v0.1.41 database (members, migrations through 0024) upgrades: its member keeps its
// servers and permissions and has no quota (0026), its audit rows stay readable (0025).
func TestUpgradeFromV0141Members(t *testing.T) {
	dir := t.TempDir()
	now := TS(time.Now())
	openAt(t, dir, "0024_members.sql", func(db *sql.DB) {
		for _, q := range []string{
			`INSERT INTO users(id, username, password_hash, created_at) VALUES(1,'admin','x','` + now + `')`,
			`INSERT INTO users(id, username, password_hash, created_at, role, permissions) VALUES(2,'bob','x','` + now + `','member','read,deploy')`,
			`INSERT INTO nodes(id, name, created_at) VALUES('srv_a','a','` + now + `')`,
			`INSERT INTO server_assignments(server_id, user_id, created_at) VALUES('srv_a',2,'` + now + `')`,
			`INSERT INTO projects(id, name, server_id, created_at) VALUES('prj_a','a','srv_a','` + now + `')`,
			`INSERT INTO services(id, project_id, name, kind, image, created_at, cpu_limit, mem_limit_mb) VALUES('svc_a','prj_a','web','image','nginx','` + now + `',0.5,256)`,
			`INSERT INTO root_domains(name, created_at) VALUES('example.com','` + now + `')`,
			`INSERT INTO domains(id, service_id, hostname, port_name, status, created_at) VALUES('dom_a','svc_a','example.com','web','active','` + now + `')`,
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	})
	s := openTest(t, dir)
	defer s.Close()
	ctx := context.Background()
	u, err := s.UserByName(ctx, "bob")
	if err != nil || !u.IsMember() || len(u.Permissions) != 2 {
		t.Fatalf("member after upgrade %+v %v", u, err)
	}
	if q, err := s.MemberQuota(ctx, u.ID); err != nil || q != (api.MemberQuota{}) {
		t.Fatalf("quota after upgrade %+v %v", q, err)
	}
	if us, err := s.MemberUsage(ctx, u.ID); err != nil || us.Services != 1 || us.CPU != 0.5 || us.MemMB != 256 {
		t.Fatalf("usage after upgrade %+v %v", us, err)
	}
	sc, err := s.MemberScopeOf(ctx, u.ID)
	if err != nil || !sc.Projects["prj_a"] {
		t.Fatalf("scope after upgrade %+v %v", sc, err)
	}
	// B7 (0027): an upgraded member may use no configured root, but keeps what it bound
	if rs, err := s.MemberRootDomains(ctx, u.ID); err != nil || len(rs) != 0 {
		t.Fatalf("root domains after upgrade %v %v", rs, err)
	}
	if ds, err := s.ListDomains(ctx, "svc_a", ""); err != nil || len(ds) != 1 || ds[0].Hostname != "example.com" {
		t.Fatalf("domains after upgrade %+v %v", ds, err)
	}
	if err := s.SetMemberRootDomains(ctx, u.ID, []string{"example.com"}); err != nil {
		t.Fatal(err)
	}
	if rs, _ := s.MemberRootDomains(ctx, u.ID); len(rs) != 1 {
		t.Fatalf("set root domains %v", rs)
	}
	if err := s.DeleteRootDomain(ctx, "example.com"); err != nil {
		t.Fatal(err)
	}
	if rs, _ := s.MemberRootDomains(ctx, u.ID); len(rs) != 0 {
		t.Fatalf("root domains after the root was deleted %v", rs)
	}
}
