package server

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/Sakuralaaa/Atoll/install"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

const repoRaw = "https://raw.githubusercontent.com/Sakuralaaa/Atoll"

var semverTagRe = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// releaseTag returns this build's release tag, or "" for a dev build.
func releaseTag() string {
	if semverTagRe.MatchString(Version) {
		return Version
	}
	return ""
}

// installScriptURL is where a node fetches install-<name>.sh from. Over https the control
// server serves its own embedded copy, so the script always matches the server's version.
// Over plain http a script could be swapped in transit (no --sha256 pin would help, the
// script does the checking), so it comes from GitHub at this version's tag instead.
func installScriptURL(base, name string) string {
	if strings.HasPrefix(base, "https://") {
		return strings.TrimRight(base, "/") + "/install-" + name + ".sh"
	}
	ref := "main"
	if t := releaseTag(); t != "" {
		ref = "refs/tags/" + t
	}
	return repoRaw + "/" + ref + "/install/install-" + name + ".sh"
}

// agentInstallCommand is the one-liner that installs (or upgrades) atoll-agent against base.
// It pins the SHA-256 of every agent binary this server hosts and, on a release build, the
// GitHub fallback to this server's version. extra goes right after --server (e.g. --token).
func (s *Server) agentInstallCommand(base string, extra ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "curl -fsSL %s | sudo sh -s -- --server %s", installScriptURL(base, "agent"), base)
	for _, e := range extra {
		b.WriteString(" " + e)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		if sum := s.hostedAgentSum(arch); sum != "" {
			fmt.Fprintf(&b, " --sha256-%s %s", arch, sum)
		}
	}
	if t := releaseTag(); t != "" {
		b.WriteString(" --version " + t)
	}
	return b.String()
}

// handleInstallScript serves the install scripts embedded in this binary.
func handleInstallScript(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(body)
	}
}

// handleAgentInstallCommand returns the command that installs or upgrades an agent on an
// already-joined server (no join token): what the panel shows for agents that cannot
// upgrade themselves.
func (s *Server) handleAgentInstallCommand(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, api.InstallCommand{Command: s.agentInstallCommand(s.publicURL(r.Context(), r)), Version: Version})
	return nil
}

var (
	agentScript  = handleInstallScript(install.AgentScript)
	serverScript = handleInstallScript(install.ServerScript)
)
