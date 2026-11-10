package cli

// `atoll deploy --git` flags for other forges, SSH deploy keys, polling and builds without a
// Dockerfile (docs/api.md "Builds from source").

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func (g *GitFlags) registerSources(f *pflag.FlagSet) {
	f.StringVar(&g.Provider, "provider", "", "forge of --git: github, gitlab, gitea (also Forgejo) or git (default: from the host)")
	f.StringVar(&g.Username, "git-username", "", "username sent with the token (default oauth2 on GitLab, git elsewhere)")
	f.StringVar(&g.SSHKeyFile, "ssh-key-file", "", "private deploy key file for an SSH --git URL (stored encrypted, never shown again)")
	f.StringVar(&g.KnownHosts, "known-hosts-file", "", "known_hosts lines pinning the SSH host key (default: trust the first key seen)")
	f.StringVar(&g.Poll, "poll", "", "check the branch for new commits this often (60s–24h, e.g. 5m; 0 = off) instead of a webhook")
	f.StringVar(&g.Builder, "builder", "", "auto (Dockerfile if present, otherwise generated), dockerfile or generated")
	f.StringVar(&g.Stack, "stack", "", "stack of a generated build: node, bun, python, go or static (default: detected)")
	f.StringVar(&g.InstallCmd, "install-cmd", "", "install command of a generated build (e.g. \"pnpm install\")")
	f.StringVar(&g.BuildCmd, "build-cmd", "", "build command of a generated build (e.g. \"npm run build\")")
	f.StringVar(&g.StartCmd, "start-cmd", "", "start command of a generated build (e.g. \"node server.js\")")
}

// checkGitURL accepts https:// URLs, ssh:// URLs and the scp-like git@host:path form.
func checkGitURL(repo string) error {
	switch {
	case strings.HasPrefix(repo, "https://"):
		if strings.Contains(strings.SplitN(strings.TrimPrefix(repo, "https://"), "/", 2)[0], "@") {
			return fmt.Errorf("--git must not contain credentials; use --git-token-env VAR for a private repository")
		}
	case strings.HasPrefix(repo, "ssh://"):
		if u := strings.SplitN(strings.TrimPrefix(repo, "ssh://"), "@", 2)[0]; strings.Contains(u, ":") {
			return fmt.Errorf("--git must not contain a password; use --ssh-key-file")
		}
	case strings.Contains(repo, "@") && strings.Contains(repo, ":") && !strings.Contains(repo, "://"):
	default:
		return fmt.Errorf("--git must be an https:// URL or an SSH URL (ssh://git@host/OWNER/REPO or git@host:OWNER/REPO)")
	}
	return nil
}

// parsePoll reads --poll: seconds or a Go duration.
func parsePoll(v string) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "0" || v == "off" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		d, derr := time.ParseDuration(v)
		if derr != nil {
			return 0, fmt.Errorf("--poll %q: give seconds or a duration like 5m", v)
		}
		n = int(d / time.Second)
	}
	if n < 60 || n > 86400 {
		return 0, fmt.Errorf("--poll must be between 1m and 24h (or 0 to turn it off)")
	}
	return n, nil
}

func (g *GitFlags) applySources(src *api.GitSource) error {
	src.Provider, src.Username = strings.TrimSpace(g.Provider), strings.TrimSpace(g.Username)
	src.Builder, src.Stack = strings.TrimSpace(g.Builder), strings.TrimSpace(g.Stack)
	src.InstallCommand, src.BuildCommand, src.StartCommand = strings.TrimSpace(g.InstallCmd), strings.TrimSpace(g.BuildCmd), strings.TrimSpace(g.StartCmd)
	n, err := parsePoll(g.Poll)
	if err != nil {
		return err
	}
	src.PollIntervalSec = n
	if g.SSHKeyFile != "" {
		raw, err := os.ReadFile(g.SSHKeyFile)
		if err != nil {
			return fmt.Errorf("--ssh-key-file: %w", err)
		}
		if !strings.Contains(string(raw), "PRIVATE KEY-----") {
			return fmt.Errorf("--ssh-key-file %s is not a private key", g.SSHKeyFile)
		}
		src.SSHKey = string(raw)
	}
	if g.KnownHosts != "" {
		raw, err := os.ReadFile(g.KnownHosts)
		if err != nil {
			return fmt.Errorf("--known-hosts-file: %w", err)
		}
		src.KnownHosts = string(raw)
	}
	return nil
}

// sourcesChanged compares the fields of this file (only those set in want).
func sourcesChanged(cur, want *api.GitSource) bool {
	if want.SSHKey != "" || (want.KnownHosts != "" && strings.TrimSpace(want.KnownHosts) != cur.KnownHosts) {
		return true
	}
	for _, f := range [][2]string{{want.Provider, cur.Provider}, {want.Username, cur.Username}, {want.Builder, orDefault(cur.Builder, "auto")},
		{want.Stack, cur.Stack}, {want.InstallCommand, cur.InstallCommand}, {want.BuildCommand, cur.BuildCommand}, {want.StartCommand, cur.StartCommand}} {
		if f[0] != "" && f[0] != f[1] {
			return true
		}
	}
	return want.PollIntervalSec != 0 && want.PollIntervalSec != cur.PollIntervalSec
}

// sourcesPatch adds the given fields of this file to a partial git PATCH.
func sourcesPatch(g *api.GitSource, m map[string]any) {
	for k, v := range map[string]string{"provider": g.Provider, "username": g.Username, "ssh_key": g.SSHKey, "known_hosts": g.KnownHosts,
		"builder": g.Builder, "stack": g.Stack, "install_command": g.InstallCommand, "build_command": g.BuildCommand, "start_command": g.StartCommand} {
		if v != "" {
			m[k] = v
		}
	}
	if g.PollIntervalSec != 0 {
		m["poll_interval_sec"] = g.PollIntervalSec
	}
}
