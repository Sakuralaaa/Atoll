package agent

// Git access for builds from source: which remote to fetch, how to reach it without being
// pointed into this server's own network (netguard), and the credentials (a token header or
// an SSH deploy key) handed to git without ever appearing in argv or the build log.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/netguard"
)

// gitAuth is what a fetch needs from a BuildPayload or a GitHeadPayload.
type gitAuth struct {
	ServiceID, Repo, Provider, Username, Token, SSHKey, KnownHosts string
}

func buildAuth(p api.BuildPayload) gitAuth {
	return gitAuth{p.ServiceID, p.Repo, p.Provider, p.Username, p.Token, p.SSHKey, p.KnownHosts}
}

// gitRemote is a parsed repository URL.
type gitRemote struct {
	fetchURL string // what git fetches
	ssh      bool
	host     string // hostname without brackets
	port     string // effective port
	prefix   string // https://host[:port]/ (extraheader scope)
}

// legacyGitHub reports whether repo is the historical GitHub form (https://github.com/O/R),
// fetched as <repo>.git and never re-resolved (github.com is a fixed public host).
func legacyGitHub(repo string) bool { return strings.HasPrefix(repo, "https://github.com/") }

func parseRemote(repo string) (gitRemote, error) {
	if strings.ContainsAny(repo, " \t\r\n\x00") {
		return gitRemote{}, errors.New("repo URL contains whitespace")
	}
	u, err := url.Parse(repo)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return gitRemote{}, fmt.Errorf("invalid repo URL")
	}
	r := gitRemote{host: strings.ToLower(u.Hostname()), port: u.Port()}
	if strings.HasPrefix(r.host, "-") || strings.HasPrefix(strings.Trim(u.Path, "/"), "-") || strings.Contains(u.Path, "/-") {
		return gitRemote{}, fmt.Errorf("invalid repo URL")
	}
	switch u.Scheme {
	case "https":
		if u.User != nil {
			return gitRemote{}, errors.New("repo URL must not carry credentials")
		}
		if r.port == "" {
			r.port = "443"
		}
		hp := u.Host
		r.prefix = "https://" + hp + "/"
	case "ssh":
		r.ssh = true
		if u.User == nil || strings.HasPrefix(u.User.Username(), "-") {
			return gitRemote{}, errors.New("ssh repo URL needs a user (ssh://git@host/path)")
		}
		if _, pw := u.User.Password(); pw {
			return gitRemote{}, errors.New("repo URL must not carry credentials")
		}
		if r.port == "" {
			r.port = "22"
		}
	default:
		return gitRemote{}, fmt.Errorf("repo must be an https:// or ssh:// URL")
	}
	if n, err := strconv.Atoi(r.port); err != nil || n < 1 || n > 65535 {
		return gitRemote{}, errors.New("invalid port in repo URL")
	}
	r.fetchURL = repo
	if legacyGitHub(repo) {
		r.fetchURL = strings.TrimSuffix(repo, ".git") + ".git"
	}
	return r, nil
}

// resolveRemote resolves the remote's host once and returns the first address netguard
// allows; git is then pinned to it (no second lookup that DNS rebinding could change).
func (b *Builder) resolveRemote(ctx context.Context, r gitRemote) (string, error) {
	if err := netguard.CheckHost(r.host); err != nil {
		return "", fmt.Errorf("repo host %s: %w", r.host, err)
	}
	lookup := b.LookupHost
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		}
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(r.host); err == nil {
		addrs = []netip.Addr{a}
	} else if addrs, err = lookup(ctx, r.host); err != nil {
		return "", fmt.Errorf("resolve repo host %s: %w", r.host, err)
	}
	var lastErr error
	for _, a := range addrs {
		if err := netguard.CheckIP(a); err != nil {
			lastErr = err
			continue
		}
		return a.Unmap().String(), nil
	}
	if lastErr == nil {
		lastErr = errors.New("no addresses")
	}
	return "", fmt.Errorf("repo host %s resolves to no allowed address: %w", r.host, lastErr)
}

// gitAccess returns the environment git runs with for this remote, the secrets the log must
// hide, and a cleanup removing the key files. secDir is a private (0700) directory.
func (b *Builder) gitAccess(ctx context.Context, a gitAuth, r gitRemote, secDir string) (env, secrets []string, err error) {
	env = []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true", "LC_ALL=C"}
	var cfg [][2]string
	user := a.Username
	if user == "" {
		user = defaultUser(a.Provider)
	}
	secrets = authSecrets(a)
	if a.Token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte(user + ":" + a.Token))
		scope := r.prefix
		if legacyGitHub(a.Repo) {
			scope = "https://github.com/"
		}
		cfg = append(cfg, [2]string{"http." + scope + ".extraheader", "AUTHORIZATION: basic " + basic})
	}
	legacy := legacyGitHub(a.Repo) && a.SSHKey == ""
	if !legacy {
		ip, err := b.resolveRemote(ctx, r)
		if err != nil {
			return nil, nil, err
		}
		cfg = append(cfg, [2]string{"protocol.allow", "never"}, [2]string{"protocol.https.allow", "always"},
			[2]string{"protocol.ssh.allow", "always"}, [2]string{"http.followRedirects", "false"})
		if r.ssh {
			sshCmd, err := b.sshCommand(a, r, ip, secDir)
			if err != nil {
				return nil, nil, err
			}
			env = append(env, "GIT_SSH_COMMAND="+sshCmd, "GIT_SSH_VARIANT=ssh")
		} else {
			cfg = append(cfg, [2]string{"http.curloptResolve", r.host + ":" + r.port + ":" + ip})
		}
	}
	if len(cfg) > 0 {
		env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(cfg)))
		for i, kv := range cfg {
			env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, kv[0]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, kv[1]))
		}
	}
	return env, secrets, nil
}

// sshCommand writes the deploy key (0600) and the known hosts into secDir and returns the
// GIT_SSH_COMMAND that connects to the resolved address while checking the key of the host
// name. Without pinned known_hosts the first key seen is kept per service (TOFU).
func (b *Builder) sshCommand(a gitAuth, r gitRemote, ip, secDir string) (string, error) {
	opts := []string{"ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "ProxyCommand=none", "-o", "ProxyJump=none", "-o", "PermitLocalCommand=no", "-o", "ConnectTimeout=20",
		"-o", "HostName=" + ip, "-o", "HostKeyAlias=" + r.host}
	if a.SSHKey != "" {
		kf := filepath.Join(secDir, "id_key")
		key := strings.TrimRight(a.SSHKey, "\n") + "\n"
		if err := os.WriteFile(kf, []byte(key), 0o600); err != nil {
			return "", err
		}
		opts = append(opts, "-i", kf)
	} else {
		opts = append(opts, "-o", "IdentityFile=/dev/null")
	}
	if strings.TrimSpace(a.KnownHosts) != "" {
		kh := filepath.Join(secDir, "known_hosts")
		if err := os.WriteFile(kh, []byte(strings.TrimSpace(a.KnownHosts)+"\n"), 0o600); err != nil {
			return "", err
		}
		opts = append(opts, "-o", "UserKnownHostsFile="+kh, "-o", "StrictHostKeyChecking=yes")
	} else {
		dir := filepath.Join(b.Paths.Root, "build-known-hosts")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		opts = append(opts, "-o", "UserKnownHostsFile="+filepath.Join(dir, a.ServiceID), "-o", "StrictHostKeyChecking=accept-new")
	}
	return strings.Join(opts, " "), nil
}

// authSecrets are the strings of a's credentials that must never reach a log: the token,
// its basic-auth header value, and the deploy key (whole and line by line).
func authSecrets(a gitAuth) []string {
	var out []string
	if a.Token != "" {
		user := a.Username
		if user == "" {
			user = defaultUser(a.Provider)
		}
		out = append(out, a.Token, base64.StdEncoding.EncodeToString([]byte(user+":"+a.Token)))
	}
	if k := strings.TrimSpace(a.SSHKey); k != "" {
		out = append(out, k)
		for _, l := range strings.Split(k, "\n") {
			if l = strings.TrimSpace(l); len(l) >= 16 && !strings.HasPrefix(l, "-----") {
				out = append(out, l)
			}
		}
	}
	return out
}

func defaultUser(provider string) string {
	switch provider {
	case "", "github":
		return "x-access-token"
	case "gitlab":
		return "oauth2"
	}
	return "git"
}

// redactor replaces every secret (longest first) with ***.
func redactor(secrets []string) func(string) string {
	var list []string
	for _, s := range secrets {
		if strings.TrimSpace(s) != "" {
			list = append(list, s)
		}
	}
	if len(list) == 0 {
		return func(s string) string { return s }
	}
	for i := range list { // longest first, so a key's lines go after the whole key
		for j := i + 1; j < len(list); j++ {
			if len(list[j]) > len(list[i]) {
				list[i], list[j] = list[j], list[i]
			}
		}
	}
	return func(s string) string {
		for _, x := range list {
			s = strings.ReplaceAll(s, x, "***")
		}
		return s
	}
}

// containerMounts are the extra bind mounts git in a container needs (key files, TOFU file).
func containerMounts(env []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_SSH_COMMAND=") {
			continue
		}
		f := strings.Fields(strings.TrimPrefix(kv, "GIT_SSH_COMMAND="))
		for i, x := range f {
			p := ""
			switch {
			case x == "-i" && i+1 < len(f):
				p = f[i+1]
			case strings.HasPrefix(x, "UserKnownHostsFile="):
				p = strings.TrimPrefix(x, "UserKnownHostsFile=")
			}
			if d := filepath.Dir(p); p != "" && !seen[d] {
				seen[d] = true
				out = append(out, "-v", d+":"+d)
			}
		}
	}
	return out
}
