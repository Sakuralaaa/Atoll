package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "master.key")
	k, err := LoadOrCreateKey(p)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	k2, err := LoadOrCreateKey(p)
	if err != nil || string(k) != string(k2) {
		t.Fatal("key not stable")
	}
	b, _ := New(k)
	c, err := b.Encrypt("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if c == "hunter2" {
		t.Fatal("not encrypted")
	}
	p2, err := b.Decrypt(c)
	if err != nil || p2 != "hunter2" {
		t.Fatalf("got %q %v", p2, err)
	}
	if _, err := b.Decrypt(c[:len(c)-2] + "AA"); err == nil {
		t.Fatal("tamper not detected")
	}
}

func TestLoadTightensLooseKeyPerm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "master.key")
	if _, err := LoadOrCreateKey(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(p); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode not tightened: %v", st.Mode())
	}
}
