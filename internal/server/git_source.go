package server

// Git sources beyond GitHub (docs/api.md "Builds from source"): GitLab, Gitea/Forgejo and any
// other git server over https or ssh, builds without a Dockerfile, polling. Validation of the
// source a user gives, and which agents can build it (HelloPayload.BuildSources).

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/netguard"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

var (
	gitPathRe   = regexp.MustCompile(`^[A-Za-z0-9_.~+-]+(/[A-Za-z0-9_.~+-]+)*$`)
	gitUserRe   = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,100}$`)
	gitHostRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}))*$`)
	scpLikeRe   = regexp.MustCompile(`^([A-Za-z0-9._-]+)@([A-Za-z0-9.-]+):([^/].*)$`)
	gitStacks   = map[string]bool{"node": true, "bun": true, "python": true, "go": true, "static": true}
	gitBuilders = map[string]bool{"auto": true, "dockerfile": true, "generated": true}
	gitProvs    = map[string]bool{"github": true, "gitlab": true, "gitea": true, "git": true}
)

// gitURL is a parsed repository URL.
type gitURL struct {
	scheme string // https or ssh
	host   string // hostname (no port, no brackets)
	port   string // "" = default
	user   string // ssh user
	path   string // without leading slash
}

func (u gitURL) String() string {
	hp := u.host
	if strings.Contains(hp, ":") {
		hp = "[" + hp + "]"
	}
	if u.port != "" {
		hp += ":" + u.port
	}
	if u.scheme == "ssh" {
		return "ssh://" + u.user + "@" + hp + "/" + u.path
	}
	return "https://" + hp + "/" + u.path
}

// parseGitURL accepts https://host[:port]/path, ssh://user@host[:port]/path and the scp-like
// user@host:path form (returned as ssh://).
func parseGitURL(raw string) (gitURL, error) {
	raw = strings.TrimSpace(raw)
	if m := scpLikeRe.FindStringSubmatch(raw); m != nil && !strings.Contains(raw, "://") {
		raw = "ssh://" + m[1] + "@" + m[2] + "/" + m[3]
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "ssh") || u.Host == "" {
		return gitURL{}, badRequest("git repo must be an https:// or SSH URL (ssh://git@host/path or git@host:path), got %q", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return gitURL{}, badRequest("git repo URL must not have a query or fragment")
	}
	g := gitURL{scheme: u.Scheme, host: strings.ToLower(u.Hostname()), port: u.Port()}
	if u.User != nil {
		if _, hasPw := u.User.Password(); hasPw || u.Scheme == "https" {
			return gitURL{}, badRequest("do not put credentials in the repo URL; give a token in git.token or a deploy key in git.ssh_key (stored encrypted)")
		}
		g.user = u.User.Username()
	}
	if g.scheme == "ssh" {
		if g.user == "" {
			g.user = "git"
		}
		if !gitUserRe.MatchString(g.user) || strings.HasPrefix(g.user, "-") {
			return gitURL{}, badRequest("invalid ssh user %q in the repo URL", g.user)
		}
	}
	if g.port != "" {
		if n, err := strconv.Atoi(g.port); err != nil || n < 1 || n > 65535 {
			return gitURL{}, badRequest("invalid port in the repo URL")
		}
	}
	if net.ParseIP(g.host) == nil && !gitHostRe.MatchString(g.host) {
		return gitURL{}, badRequest("invalid host %q in the repo URL", g.host)
	}
	g.path = strings.Trim(u.Path, "/")
	if g.path == "" || !gitPathRe.MatchString(g.path) {
		return gitURL{}, badRequest("git repo URL needs a repository path like /OWNER/REPO (got %q)", raw)
	}
	for _, seg := range strings.Split(g.path, "/") {
		if seg == "." || seg == ".." || strings.HasPrefix(seg, "-") {
			return gitURL{}, badRequest("invalid path segment %q in the repo URL", seg)
		}
	}
	return g, nil
}

// inferProvider guesses the forge from the host.
func inferProvider(host string) string {
	h := strings.ToLower(host)
	switch {
	case h == "github.com":
		return "github"
	case h == "gitlab.com" || strings.HasPrefix(h, "gitlab."):
		return "gitlab"
	case h == "codeberg.org" || h == "gitea.com" || strings.HasPrefix(h, "gitea.") || strings.HasPrefix(h, "forgejo."):
		return "gitea"
	}
	return "git"
}

// normalizeGitSourceURL validates g.Repo and g.Provider and normalizes them. GitHub https
// URLs keep their historical form https://github.com/OWNER/REPO (what older agents build).
func normalizeGitSourceURL(g *api.GitSource) (gitURL, error) {
	g.Provider = strings.ToLower(strings.TrimSpace(g.Provider))
	if g.Provider != "" && !gitProvs[g.Provider] {
		return gitURL{}, badRequest("git.provider must be github, gitlab, gitea or git (got %q)", g.Provider)
	}
	u, err := parseGitURL(g.Repo)
	if err != nil {
		return gitURL{}, err
	}
	if err := checkGitHost(u.host); err != nil {
		return gitURL{}, err
	}
	if g.Provider == "" {
		g.Provider = inferProvider(u.host)
	}
	if u.host == "github.com" {
		if g.Provider != "github" {
			return gitURL{}, badRequest("github.com repositories use provider github")
		}
		if u.scheme == "https" {
			repo, err := normalizeRepo(g.Repo)
			if err != nil {
				return gitURL{}, err
			}
			g.Repo = repo
			return u, nil
		}
	}
	g.Repo = u.String()
	return u, nil
}

// validateGitExtras checks the fields added for other forges and generated builds.
func validateGitExtras(g *api.GitSource, u gitURL) error {
	g.Username = strings.TrimSpace(g.Username)
	if g.Username != "" && !gitUserRe.MatchString(g.Username) {
		return badRequest("git.username %q is not valid", g.Username)
	}
	if g.Token != "" && u.scheme == "ssh" {
		return badRequest("git.token is for https URLs; an SSH URL authenticates with git.ssh_key")
	}
	g.SSHKey = strings.TrimSpace(g.SSHKey)
	if g.SSHKey != "" {
		if u.scheme != "ssh" {
			return badRequest("git.ssh_key is for SSH repo URLs (ssh://git@host/path or git@host:path)")
		}
		if len(g.SSHKey) > 16<<10 || !strings.HasPrefix(g.SSHKey, "-----BEGIN ") || !strings.Contains(g.SSHKey, "PRIVATE KEY-----") ||
			strings.ContainsRune(g.SSHKey, 0) {
			return badRequest("git.ssh_key must be a PEM/OpenSSH private key (-----BEGIN ... PRIVATE KEY-----)")
		}
		g.SSHKey += "\n"
	}
	g.KnownHosts = strings.TrimSpace(g.KnownHosts)
	if g.KnownHosts != "" {
		if len(g.KnownHosts) > 16<<10 || strings.ContainsRune(g.KnownHosts, 0) {
			return badRequest("git.known_hosts is too long or contains NUL")
		}
		for _, l := range strings.Split(g.KnownHosts, "\n") {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "#") {
				continue
			}
			if len(strings.Fields(l)) < 3 {
				return badRequest("git.known_hosts line %q is not \"host keytype key\"", l)
			}
		}
	}
	if p := g.PollIntervalSec; p != 0 && (p < 60 || p > 86400) {
		return badRequest("git.poll_interval_sec must be 0 (off) or 60–86400")
	}
	g.Builder = strings.ToLower(strings.TrimSpace(g.Builder))
	if g.Builder == "" {
		g.Builder = "auto"
	}
	if !gitBuilders[g.Builder] {
		return badRequest("git.builder must be auto, dockerfile or generated (got %q)", g.Builder)
	}
	g.Stack = strings.ToLower(strings.TrimSpace(g.Stack))
	if g.Stack != "" && !gitStacks[g.Stack] {
		return badRequest("git.stack must be node, bun, python, go or static (got %q)", g.Stack)
	}
	for name, c := range map[string]*string{"install_command": &g.InstallCommand, "build_command": &g.BuildCommand, "start_command": &g.StartCommand} {
		*c = strings.TrimSpace(*c)
		if len(*c) > 1000 || strings.ContainsAny(*c, "\x00\r\n") {
			return badRequest("git.%s must be one line of at most 1000 characters", name)
		}
	}
	g.HasSSHKey, g.Detected = false, nil
	return nil
}

// gitNeedsSources reports whether building g needs an agent with BuildSources (anything
// beyond a GitHub https repo with an optional token and a Dockerfile).
func gitNeedsSources(g api.GitSource, hasSSHKey bool) bool {
	return !strings.HasPrefix(g.Repo, "https://github.com/") || hasSSHKey || g.SSHKey != "" || g.KnownHosts != "" ||
		g.PollIntervalSec > 0 || g.Builder == "generated" || g.Stack != "" || g.InstallCommand != "" ||
		g.BuildCommand != "" || g.StartCommand != "" || (g.Username != "" && g.Username != "x-access-token")
}

// agentBuildSources reports whether the node's connected agent announced BuildSources.
func (s *Server) agentBuildSources(nodeID string) bool {
	c := s.hub.get(nodeID)
	return c != nil && c.buildSources
}

const agentTooOldBuildMsg = "upgrade the atoll-agent on this server (Servers → Upgrade agent) to build from GitLab/Gitea/any git server, over SSH, with polling or without a Dockerfile"

// checkBuildCaps refuses a source an online, older agent cannot build (409 agent_too_old).
// An offline server is left to the deploy (which needs it online anyway).
func (s *Server) checkBuildCaps(nodeID string, g api.GitSource, hasSSHKey bool) error {
	if !gitNeedsSources(g, hasSSHKey) || !s.hub.Online(nodeID) || s.agentBuildSources(nodeID) {
		return nil
	}
	return errf(409, "agent_too_old", "%s", agentTooOldBuildMsg)
}

// defaultGitUsername is the https username a token is sent with.
func defaultGitUsername(provider string) string {
	switch provider {
	case "github":
		return "x-access-token"
	case "gitlab":
		return "oauth2"
	}
	return "git"
}

// fillBuildSources adds the fields of new agents to a build payload.
func fillBuildSources(b *api.BuildPayload, g *store.GitRow, port int) {
	b.Provider, b.Username, b.SSHKey, b.KnownHosts = g.Provider, g.Username, g.SSHKey, g.KnownHosts
	if b.Username == "" {
		b.Username = defaultGitUsername(g.Provider)
	}
	b.Builder, b.Stack = g.Builder, g.Stack
	if b.Builder == "" {
		b.Builder = "auto"
	}
	b.InstallCommand, b.BuildCommand, b.StartCommand = g.InstallCommand, g.BuildCommand, g.StartCommand
	b.Port = port
}

// agentUpgradeHint turns an older agent's answer into an upgrade hint.
func agentUpgradeHint(msg string) string {
	switch {
	case strings.Contains(msg, "unsupported message type"):
		return "this server's atoll-agent is too old for this; " + agentTooOldBuildMsg
	case strings.Contains(msg, "repo must be a GitHub https URL"):
		return msg + " (" + agentTooOldBuildMsg + ")"
	case strings.Contains(msg, "no Dockerfile at") && strings.Contains(msg, "not supported yet"):
		return msg + " (" + agentTooOldBuildMsg + ")"
	}
	return msg
}

// checkGitHost refuses repository hosts that point at a server itself or a cloud metadata
// service (the build server re-checks what the name resolves to before fetching).
func checkGitHost(host string) error {
	if err := netguard.CheckHost(host); err != nil {
		return badRequest("git repo host not allowed: %v", err)
	}
	return nil
}

func firstPort(spec api.ResolvedSpec) int {
	if len(spec.Ports) > 0 {
		return spec.Ports[0].ContainerPort
	}
	return 0
}
