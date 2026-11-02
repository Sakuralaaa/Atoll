package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/server"
)

// export on one data dir, restore into another through the CLI entry points.
func TestExportRestoreCLI(t *testing.T) {
	src := t.TempDir()
	srv, err := server.New(server.Config{DataDir: src})
	if err != nil {
		t.Fatal(err)
	}
	srv.Close()
	if _, _, err := server.ResetPassword(src, "", ""); err == nil {
		t.Fatal("expected no user yet")
	}
	pass := filepath.Join(t.TempDir(), "pass")
	os.WriteFile(pass, []byte("a long enough passphrase\n"), 0o600)
	out := filepath.Join(t.TempDir(), "c.atollbak")
	env := filepath.Join(t.TempDir(), "server.env")
	os.WriteFile(env, []byte("ATOLL_PUBLIC_URL=https://panel.example.com\n"), 0o600)
	if code := exportCmd([]string{"--data", src, "--out", out, "--passphrase-file", pass, "--env-file", env, "--caddyfile", "/nonexistent"}); code != 0 {
		t.Fatalf("export exit %d", code)
	}
	dst := filepath.Join(t.TempDir(), "atoll")
	newEnv := filepath.Join(t.TempDir(), "etc", "server.env")
	if code := restoreCmd([]string{"--data", dst, "--bundle", out, "--passphrase-file", pass, "--env-file", newEnv}); code != 0 {
		t.Fatalf("restore exit %d", code)
	}
	a, _ := os.ReadFile(filepath.Join(src, "master.key"))
	b, _ := os.ReadFile(filepath.Join(dst, "master.key"))
	if len(a) == 0 || string(a) != string(b) {
		t.Fatal("master.key not restored")
	}
	if e, _ := os.ReadFile(newEnv); !strings.Contains(string(e), "ATOLL_PUBLIC_URL=https://panel.example.com") {
		t.Fatalf("env %q", e)
	}
	// existing database without --force: refused
	if code := restoreCmd([]string{"--data", dst, "--bundle", out, "--passphrase-file", pass}); code == 0 {
		t.Fatal("restore over an existing database without --force")
	}
	// wrong passphrase
	os.WriteFile(pass, []byte("not the passphrase at all\n"), 0o600)
	if code := restoreCmd([]string{"--data", t.TempDir(), "--bundle", out, "--passphrase-file", pass}); code == 0 {
		t.Fatal("wrong passphrase accepted")
	}
	if p, err := readPassphrase(pass); err != nil || p != "not the passphrase at all" {
		t.Fatalf("readPassphrase %q %v", p, err)
	}
}
