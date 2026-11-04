package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// rewindRootDomains undoes migration 0019 so the next Open runs it against old data.
func rewindRootDomains(t *testing.T, s *Store) {
	t.Helper()
	for _, q := range []string{`DROP TABLE root_domains`, `ALTER TABLE projects DROP COLUMN root_domain`, `DELETE FROM schema_migrations WHERE name='0019_root_domains.sql'`} {
		if _, err := s.DB.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
}

func TestMigrationMovesBaseDomainIntoList(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	ctx := context.Background()
	rewindRootDomains(t, s)
	s.SetSetting(ctx, "base_domain", "Example.com.")
	s.SetSetting(ctx, "cloudflare_zone_id", "zone123")
	s.Close()
	s = openTest(t, dir)
	defer s.Close()
	rs, err := s.ListRootDomains(ctx)
	if err != nil || len(rs) != 1 {
		t.Fatalf("%v %+v", err, rs)
	}
	r := rs[0]
	if r.Name != "example.com" || !r.Default || r.ZoneID != "zone123" || r.CertMode != "global" || r.CertStatus != "global" {
		t.Fatalf("%+v", r)
	}
	if v, _ := s.GetSetting(ctx, "base_domain"); v != "Example.com." {
		t.Fatalf("base_domain must be left as it was, got %q", v)
	}
}

func TestMigrationWithoutBaseDomain(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	defer s.Close()
	rs, err := s.ListRootDomains(context.Background())
	if err != nil || len(rs) != 0 {
		t.Fatalf("%v %+v", err, rs)
	}
}

func TestRootDomainCRUD(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.CreateRootDomain(ctx, api.RootDomain{Name: "example.com", CertMode: "global", CertStatus: "global"}))
	must(s.CreateRootDomain(ctx, api.RootDomain{Name: "example.net", CertMode: "auto"}))
	if err := s.CreateRootDomain(ctx, api.RootDomain{Name: "example.net"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("dup: %v", err)
	}
	if v, _ := s.GetSetting(ctx, "base_domain"); v != "example.com" {
		t.Fatalf("first domain is default: %q", v)
	}
	must(s.SetRootDomainCert(ctx, "example.net", "origin", "", "cert1", nil, "CERT", "KEY", false))
	r, err := s.GetRootDomain(ctx, "example.net")
	must(err)
	if r.CertPEM != "CERT" || r.KeyPEM != "KEY" || r.CertStatus != "origin" || r.CertID != "cert1" {
		t.Fatalf("%+v", r)
	}
	var raw string
	s.DB.QueryRow(`SELECT origin_key_pem FROM root_domains WHERE name='example.net'`).Scan(&raw)
	if raw == "KEY" {
		t.Fatal("key stored in clear")
	}
	must(s.SetDefaultRootDomain(ctx, "example.net"))
	if v, _ := s.GetSetting(ctx, "base_domain"); v != "example.net" {
		t.Fatalf("base_domain mirror: %q", v)
	}
	rs, _ := s.ListRootDomains(ctx)
	if rs[0].Name != "example.net" || !rs[0].Default || rs[1].Default {
		t.Fatalf("%+v", rs)
	}
	must(s.CreateNode(ctx, api.Server{ID: "srv_a", Name: "a", Role: "worker", Status: "online", CreatedAt: time.Now()}))
	must(s.CreateProject(ctx, api.Project{ID: "prj_1", Name: "p", ServerID: "srv_a", RootDomain: "example.net", CreatedAt: time.Now()}, false))
	must(s.DeleteRootDomain(ctx, "example.net"))
	if v, _ := s.GetSetting(ctx, "base_domain"); v != "example.com" {
		t.Fatalf("default moves to remaining root: %q", v)
	}
	p, _ := s.GetProject(ctx, "prj_1")
	if p.RootDomain != "" {
		t.Fatalf("project reference not cleared: %q", p.RootDomain)
	}
	must(s.RenameRootDomain(ctx, "example.com", "example.org"))
	if v, _ := s.GetSetting(ctx, "base_domain"); v != "example.org" {
		t.Fatalf("rename mirror: %q", v)
	}
	must(s.DeleteRootDomain(ctx, "example.org"))
	if v, _ := s.GetSetting(ctx, "base_domain"); v != "" {
		t.Fatalf("last delete clears base_domain: %q", v)
	}
}
