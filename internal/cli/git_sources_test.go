package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestGitSourceFlags(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	os.WriteFile(key, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----\n"), 0o600)
	kh := filepath.Join(dir, "kh")
	os.WriteFile(kh, []byte("git.example.com ssh-ed25519 AAAA\n"), 0o600)
	g := GitFlags{Repo: "git@git.example.com:me/app.git", SSHKeyFile: key, KnownHosts: kh, Poll: "5m", Builder: "generated", Stack: "node",
		StartCmd: "node server.js", Provider: "gitea"}
	src, err := g.Source(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if src.PollIntervalSec != 300 || src.SSHKey == "" || src.KnownHosts == "" || src.Builder != "generated" || src.Stack != "node" ||
		src.StartCommand != "node server.js" || src.Provider != "gitea" {
		t.Fatalf("source %+v", src)
	}
	if repoName(src.Repo) != "app" || repoName("git@host:solo.git") != "solo" {
		t.Fatal("repoName of SSH URLs")
	}
	m := gitPatch(src)
	if m["poll_interval_sec"] != 300 || m["ssh_key"] == nil || m["stack"] != "node" || m["provider"] != "gitea" {
		t.Fatalf("patch %v", m)
	}
	cur := &api.GitSource{Repo: src.Repo, Provider: "gitea", Builder: "generated", Stack: "node", StartCommand: "node server.js", PollIntervalSec: 300}
	if gitChanged(cur, &api.GitSource{Repo: src.Repo, Stack: "node"}) {
		t.Fatal("unchanged stack counted as change")
	}
	if !gitChanged(cur, &api.GitSource{Repo: src.Repo, StartCommand: "bun x"}) || !gitChanged(cur, &api.GitSource{Repo: src.Repo, PollIntervalSec: 600}) {
		t.Fatal("changed fields not detected")
	}
	for in, want := range map[string]int{"": 0, "0": 0, "60": 60, "1h": 3600} {
		if n, err := parsePoll(in); err != nil || n != want {
			t.Errorf("parsePoll(%q) = %d %v", in, n, err)
		}
	}
}
