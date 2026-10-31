// Package registry is a small, dependency-free client for the OCI distribution API,
// used by atoll-server to detect image updates: it resolves a tag to its manifest
// digest with HEAD (which does not count against Docker Hub's pull limit) and lists
// tags. Anonymous bearer tokens (Docker Hub, GHCR, most registries) and basic auth
// with stored credentials are supported.
package registry

import (
	"fmt"
	"strings"
)

// DockerHub is the canonical registry name of Docker Hub; its API lives on dockerHubAPI.
const (
	DockerHub    = "docker.io"
	dockerHubAPI = "registry-1.docker.io"
)

// Ref is a parsed image reference.
type Ref struct {
	Registry   string // "docker.io", "ghcr.io", "registry.example.com:5000"
	Repository string // "library/nginx", "owner/app"
	Tag        string // "" when only a digest is given
	Digest     string // "sha256:..." when pinned
	// Name is the reference without tag/digest as the user wrote it ("nginx", "ghcr.io/o/a").
	Name string
}

// ParseRef parses docker-style references: nginx, nginx:1.27, library/nginx:1,
// ghcr.io/o/a:v1, host:5000/a/b@sha256:..., a:tag@sha256:...
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("empty image reference")
	}
	if strings.ContainsAny(s, " \t\n") || strings.Contains(s, "://") {
		return Ref{}, fmt.Errorf("invalid image reference %q", s)
	}
	var r Ref
	if i := strings.Index(s, "@"); i >= 0 {
		r.Digest = s[i+1:]
		s = s[:i]
		if !strings.Contains(r.Digest, ":") {
			return Ref{}, fmt.Errorf("invalid digest in %q", s)
		}
	}
	// a tag is a ':' after the last '/'
	if i := strings.LastIndex(s, ":"); i > strings.LastIndex(s, "/") {
		r.Tag = s[i+1:]
		s = s[:i]
	}
	if s == "" {
		return Ref{}, fmt.Errorf("invalid image reference")
	}
	r.Name = s
	first, rest, hasSlash := strings.Cut(s, "/")
	if hasSlash && (strings.ContainsAny(first, ".:") || first == "localhost") {
		r.Registry, r.Repository = first, rest
	} else {
		r.Registry, r.Repository = DockerHub, s
	}
	if r.Registry == "index.docker.io" || r.Registry == dockerHubAPI {
		r.Registry = DockerHub
	}
	if r.Registry == DockerHub && !strings.Contains(r.Repository, "/") {
		r.Repository = "library/" + r.Repository
	}
	if r.Tag == "" && r.Digest == "" {
		r.Tag = "latest"
	}
	if r.Repository != strings.ToLower(r.Repository) {
		return Ref{}, fmt.Errorf("repository must be lowercase: %q", r.Repository)
	}
	return r, nil
}

// String renders the reference as Name[:Tag][@Digest].
func (r Ref) String() string {
	s := r.Name
	if r.Tag != "" {
		s += ":" + r.Tag
	}
	if r.Digest != "" {
		s += "@" + r.Digest
	}
	return s
}

// WithTag returns Name:tag (no digest).
func (r Ref) WithTag(tag string) string { return r.Name + ":" + tag }

// Pinned returns Name@digest, the ref the agent pulls for a digest-exact deployment.
func (r Ref) Pinned(digest string) string { return r.Name + "@" + digest }

// Key identifies the repository on its registry ("ghcr.io/o/a").
func (r Ref) Key() string { return r.Registry + "/" + r.Repository }

// apiHost is the host that serves /v2/ for this registry.
func (r Ref) apiHost() string {
	if r.Registry == DockerHub {
		return dockerHubAPI
	}
	return r.Registry
}

// ChangelogURL derives a human page for the repository: GitHub releases for GHCR,
// the Docker Hub page for Docker Hub images. "" when nothing can be derived.
func (r Ref) ChangelogURL() string {
	switch r.Registry {
	case "ghcr.io":
		parts := strings.Split(r.Repository, "/")
		if len(parts) >= 2 {
			return "https://github.com/" + parts[0] + "/" + parts[1] + "/releases"
		}
	case DockerHub:
		if strings.HasPrefix(r.Repository, "library/") {
			return "https://hub.docker.com/_/" + strings.TrimPrefix(r.Repository, "library/")
		}
		return "https://hub.docker.com/r/" + r.Repository + "/tags"
	}
	return ""
}
