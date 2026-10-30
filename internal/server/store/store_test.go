package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/secrets"
)

func openTest(t *testing.T, dir string) *Store {
	t.Helper()
	key, err := secrets.LoadOrCreateKey(filepath.Join(dir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "atoll.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()
	openTest(t, dir).Close()
	s := openTest(t, dir) // reopen: migrations must not re-run
	defer s.Close()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n < 2 {
		t.Fatalf("schema_migrations: %d %v", n, err)
	}
}

func TestOwnerSessionsAndPassword(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	defer s.Close()
	u, err := s.CreateOwner(ctx, "admin", "hash1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOwner(ctx, "other", "x"); err != ErrConflict {
		t.Fatalf("second owner: %v", err)
	}
	if err := s.CreateSession(ctx, "sess", u.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionUser(ctx, "sess"); err != nil || got.Username != "admin" {
		t.Fatalf("session user: %v %v", got, err)
	}
	if err := s.CreateSession(ctx, "old", u.ID, -time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "old"); err == nil {
		t.Fatal("expired session accepted")
	}
	if _, err := s.SetPassword(ctx, "", "hash2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UserByName(ctx, "admin"); got.PasswordHash != "hash2" {
		t.Fatalf("hash not updated: %q", got.PasswordHash)
	}
	if _, err := s.SessionUser(ctx, "sess"); err == nil {
		t.Fatal("sessions must be revoked by SetPassword")
	}
	if _, err := s.SetPassword(ctx, "nobody", "h"); err != ErrNotFound {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestSecretSettingsEncryptedAtRest(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	defer s.Close()
	if err := s.SetSetting(ctx, "cloudflare_token", "cf-plain-token"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "base_domain", "example.com"); err != nil {
		t.Fatal(err)
	}
	var raw string
	s.DB.QueryRow(`SELECT value FROM settings WHERE key='cloudflare_token'`).Scan(&raw)
	if raw == "" || strings.Contains(raw, "cf-plain-token") {
		t.Fatalf("token stored in clear: %q", raw)
	}
	if v, _ := s.GetSetting(ctx, "cloudflare_token"); v != "cf-plain-token" {
		t.Fatalf("decrypt: %q", v)
	}
	if v, _ := s.GetSetting(ctx, "missing"); v != "" {
		t.Fatalf("missing setting: %q", v)
	}
	inv := api.ZeaburInventory{K3s: true, Projects: []api.ZeaburProject{{Namespace: "environment-x", Services: []api.ZeaburService{{Env: []api.EnvVar{{Key: "PW", Value: "hunter2"}}}}}}}
	if err := s.SetSecretBlob(ctx, "zeabur_inventory:srv_1", inv); err != nil {
		t.Fatal(err)
	}
	s.DB.QueryRow(`SELECT value FROM settings WHERE key='zeabur_inventory:srv_1'`).Scan(&raw)
	if strings.Contains(raw, "hunter2") {
		t.Fatal("inventory stored in clear")
	}
	var back api.ZeaburInventory
	if err := s.GetSecretBlob(ctx, "zeabur_inventory:srv_1", &back); err != nil || back.Projects[0].Services[0].Env[0].Value != "hunter2" {
		t.Fatalf("inventory roundtrip: %+v %v", back, err)
	}
}

func TestImportRoundtrip(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, t.TempDir())
	defer s.Close()
	j := api.ImportJob{ID: "imp_1", Status: "rehearsed", CreatedAt: time.Now().UTC(), Namespace: "environment-x",
		Plan:   api.ImportPlan{Source: "zeabur-local", TargetServer: "srv_1", Services: []api.ImportService{{Name: "web", Env: []api.EnvVar{{Key: "K", Value: "secret-v"}}}}},
		Steps:  []api.ImportStep{{Name: "a", Status: "done"}},
		Verify: []api.ImportVerify{{Service: "db", Table: "users", SourceRows: 3, TargetRows: 3}}}
	meta := map[string]string{"namespace": "environment-x"}
	if err := s.SaveImport(ctx, j, "zeabur-local", meta); err != nil {
		t.Fatal(err)
	}
	var raw string
	s.DB.QueryRow(`SELECT plan || meta FROM imports WHERE id='imp_1'`).Scan(&raw)
	if strings.Contains(raw, "secret-v") || strings.Contains(raw, "environment-x") {
		t.Fatal("plan/meta stored in clear")
	}
	var m map[string]string
	got, kind, err := s.GetImport(ctx, "imp_1", &m)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "zeabur-local" || got.Mode != "zeabur-local" || got.ServerID != "srv_1" || got.Namespace != "environment-x" ||
		len(got.Verify) != 1 || got.Verify[0].TargetRows != 3 || m["namespace"] != "environment-x" || got.Plan.Services[0].Env[0].Value != "secret-v" {
		t.Fatalf("roundtrip: %+v kind=%s meta=%v", got, kind, m)
	}
	j.Status = "live"
	if err := s.SaveImport(ctx, j, "zeabur-local", meta); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListImports(ctx)
	if err != nil || len(list) != 1 || list[0].Status != "live" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, _, err := s.GetImport(ctx, "nope", nil); err != ErrNotFound {
		t.Fatalf("missing import: %v", err)
	}
}
