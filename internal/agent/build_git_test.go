package agent_test

import (
	"context"
	"encoding/base64"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func lookupTo(ips ...string) func(context.Context, string) ([]netip.Addr, error) {
	return func(context.Context, string) ([]netip.Addr, error) {
		var out []netip.Addr
		for _, s := range ips {
			out = append(out, netip.MustParseAddr(s))
		}
		return out, nil
	}
}

func envValue(env []string, key string) string {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, key+"="); ok {
			return v
		}
	}
	return ""
}

// gitConfig returns the GIT_CONFIG_* entries of env as key -> value.
func gitConfig(env []string) map[string]string {
	out := map[string]string{}
	for _, e := range env {
		if k, ok := strings.CutPrefix(e, "GIT_CONFIG_KEY_"); ok {
			i, key, _ := strings.Cut(k, "=")
			out[key] = envValue(env, "GIT_CONFIG_VALUE_"+i)
		}
	}
	return out
}

func TestBuildGitLabHTTPSPinnedAndAuthed(t *testing.T) {
	b, _, c, ev, _ := newBuilder(t)
	b.LookupHost = lookupTo("192.0.2.10")
	const tok = "glpat-SECRETSECRET123"
	basic := base64.StdEncoding.EncodeToString([]byte("oauth2:" + tok))
	c.fetchOut = []string{"fatal: auth " + basic + " " + tok}
	pl := buildPayload()
	pl.Repo, pl.Provider, pl.Builder, pl.Token = "https://gitlab.example.com:8443/group/sub/app.git", "gitlab", "dockerfile", tok
	if _, err := b.Build(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	fetch := c.callsOf("git", "fetch")
	if len(fetch) != 1 || !strings.Contains(strings.Join(fetch[0].args, " "), "https://gitlab.example.com:8443/group/sub/app.git refs/heads/main") {
		t.Fatalf("fetch %+v", fetch)
	}
	cfg := gitConfig(fetch[0].env)
	if cfg["http.curloptResolve"] != "gitlab.example.com:8443:192.0.2.10" || cfg["http.followRedirects"] != "false" ||
		cfg["protocol.allow"] != "never" || cfg["protocol.https.allow"] != "always" ||
		cfg["http.https://gitlab.example.com:8443/.extraheader"] != "AUTHORIZATION: basic "+basic {
		t.Fatalf("git config %v", cfg)
	}
	if logs := ev.all(); strings.Contains(logs, tok) || strings.Contains(logs, basic) {
		t.Fatalf("secret in log: %s", logs)
	}
}

func TestBuildRefusesInternalHosts(t *testing.T) {
	for repo, ips := range map[string][]string{
		"https://git.internal.example/a/b":   {"127.0.0.1"},
		"https://169.254.169.254/a/b":        nil,
		"ssh://git@meta.example/a/b":         {"169.254.169.254", "fe80::1"},
		"https://localhost/a/b":              nil,
		"https://metadata.google.internal/x": nil,
	} {
		b, _, c, _, _ := newBuilder(t)
		b.LookupHost = lookupTo(ips...)
		pl := buildPayload()
		pl.Repo, pl.Provider, pl.Builder = repo, "git", "auto"
		if _, err := b.Build(context.Background(), pl); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("%s: err %v", repo, err)
		}
		if f := c.callsOf("git", "fetch"); len(f) != 0 {
			t.Errorf("%s: fetched anyway", repo)
		}
	}
}

func TestBuildSSHDeployKey(t *testing.T) {
	b, _, c, ev, p := newBuilder(t)
	b.LookupHost = lookupTo("::ffff:198.51.100.7")
	keyBody := "b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW"
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\n" + keyBody + "\n-----END OPENSSH PRIVATE KEY-----\n"
	var keyFile string
	var keyMode os.FileMode
	c.fetchHook = func(env []string) {
		f := strings.Fields(envValue(env, "GIT_SSH_COMMAND"))
		for i, x := range f {
			if x == "-i" && i+1 < len(f) {
				keyFile = f[i+1]
				if st, err := os.Stat(keyFile); err == nil {
					keyMode = st.Mode().Perm()
				}
			}
		}
	}
	c.fetchOut = []string{"debug: " + keyBody}
	pl := buildPayload()
	pl.Repo, pl.Provider, pl.Builder, pl.SSHKey = "ssh://git@git.example.com:2222/me/app.git", "git", "dockerfile", key
	if _, err := b.Build(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	fetch := c.callsOf("git", "fetch")
	ssh := envValue(fetch[0].env, "GIT_SSH_COMMAND")
	for _, want := range []string{"HostName=198.51.100.7", "HostKeyAlias=git.example.com", "StrictHostKeyChecking=accept-new",
		"UserKnownHostsFile=" + p.Root + "/build-known-hosts/svc_app", "BatchMode=yes", "ProxyCommand=none"} {
		if !strings.Contains(ssh, want) {
			t.Fatalf("GIT_SSH_COMMAND %q lacks %q", ssh, want)
		}
	}
	if gitConfig(fetch[0].env)["protocol.ext.allow"] != "" || gitConfig(fetch[0].env)["protocol.allow"] != "never" {
		t.Fatal("protocol allow-list missing")
	}
	if keyFile == "" || keyMode != 0o600 {
		t.Fatalf("key file %q mode %v", keyFile, keyMode)
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatalf("key file left behind: %v", err)
	}
	if strings.Contains(ev.all(), keyBody) {
		t.Fatal("key in build log")
	}

	// pinned known_hosts: strict checking against the given lines
	pl.KnownHosts = "git.example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample"
	if _, err := b.Build(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	fetch = c.callsOf("git", "fetch")
	if ssh := envValue(fetch[len(fetch)-1].env, "GIT_SSH_COMMAND"); !strings.Contains(ssh, "StrictHostKeyChecking=yes") {
		t.Fatalf("pinned: %s", ssh)
	}
}

func TestBuildOldServerPayloadStillGitHubOnly(t *testing.T) {
	b, _, _, _, _ := newBuilder(t)
	pl := buildPayload()
	pl.Repo = "https://gitlab.com/a/b" // no provider/builder: what an old control server could never send
	if _, err := b.Build(context.Background(), pl); err == nil || !strings.Contains(err.Error(), "GitHub") {
		t.Fatalf("err %v", err)
	}
	_ = api.BuildPayload{}
}
