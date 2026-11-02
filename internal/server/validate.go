package server

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

var (
	dnsLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	envKeyRe   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	hostnameRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,62}$`)
	portNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
)

// sanitizeName turns any string into a DNS label ("My App_1" -> "my-app-1").
func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	if out == "" {
		out = "svc"
	}
	return out
}

// reservedPublicPort names what a host port is kept for, or "" when a service may publish
// it. Docker-published ports bypass host firewalls (ufw), so a service that grabbed one of
// these would shadow SSH or Atoll's own private-network traffic.
func reservedPublicPort(port int) string {
	switch {
	case port == 22:
		return "SSH"
	case port == 7301:
		return "Atoll agent endpoint on the tailnet"
	case port == 41641:
		return "Tailscale WireGuard"
	case port >= 41000 && port <= 41999:
		return "Atoll private-network mesh range 41000-41999"
	}
	return ""
}

func validateServiceSpec(v *api.Service) error {
	return validateServiceSpecKeeping(v, nil)
}

// validateServiceSpecKeeping is validateServiceSpec, except that reserved public ports the
// service already published (keep) are still accepted, so an existing service stays editable.
func validateServiceSpecKeeping(v *api.Service, keep []api.Port) error {
	grandfathered := map[int]bool{}
	for _, p := range keep {
		if p.PublicPort > 0 {
			grandfathered[p.PublicPort] = true
		}
	}
	if !dnsLabelRe.MatchString(v.Name) {
		return badRequest("service name %q must be a lowercase DNS label (a-z, 0-9, '-', max 63)", v.Name)
	}
	if strings.TrimSpace(v.Image) == "" && v.Git == nil {
		return badRequest("image is required (or a git source to build from)")
	}
	if strings.ContainsAny(v.Image, " \t\n") {
		return badRequest("image must not contain whitespace")
	}
	seen := map[string]bool{}
	for i := range v.Ports {
		p := &v.Ports[i]
		if p.Protocol == "" {
			p.Protocol = "http"
		}
		if p.Name == "" {
			p.Name = fmt.Sprintf("port%d", p.ContainerPort)
			if p.Protocol == "http" && !seen["web"] {
				p.Name = "web"
			}
		}
		if !portNameRe.MatchString(p.Name) {
			return badRequest("port name %q must match %s", p.Name, portNameRe)
		}
		if seen[p.Name] {
			return badRequest("duplicate port name %q", p.Name)
		}
		seen[p.Name] = true
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return badRequest("port %q: container_port must be 1-65535", p.Name)
		}
		switch p.Protocol {
		case "http":
			if p.PublicPort != 0 {
				return badRequest("port %q: public_port is only for tcp/udp; http goes through ingress", p.Name)
			}
		case "tcp", "udp":
			if p.PublicPort < 0 || p.PublicPort > 65535 || p.PublicPort == 80 || p.PublicPort == 443 {
				return badRequest("port %q: public_port must be 0 (internal) or 1-65535 except 80/443", p.Name)
			}
			if why := reservedPublicPort(p.PublicPort); why != "" && !grandfathered[p.PublicPort] {
				return badRequest("port %q: public_port %d is reserved (%s)", p.Name, p.PublicPort, why)
			}
		default:
			return badRequest("port %q: protocol must be http, tcp or udp", p.Name)
		}
	}
	vseen := map[string]bool{}
	for _, vol := range v.Volumes {
		if !dnsLabelRe.MatchString(vol.Name) {
			return badRequest("volume name %q must be a lowercase DNS label", vol.Name)
		}
		if vseen[vol.Name] {
			return badRequest("duplicate volume %q", vol.Name)
		}
		vseen[vol.Name] = true
		if !path.IsAbs(vol.MountPath) || path.Clean(vol.MountPath) == "/" {
			return badRequest("volume %q: mount_path must be an absolute path other than /", vol.Name)
		}
	}
	for _, c := range v.Configs {
		if !path.IsAbs(c.Path) {
			return badRequest("config path %q must be absolute", c.Path)
		}
		if len(c.Content) > 1<<20 {
			return badRequest("config %q larger than 1 MiB", c.Path)
		}
	}
	if v.CPULimit < 0 || v.MemLimitMB < 0 {
		return badRequest("limits must be >= 0")
	}
	return nil
}

func validateEnv(vars []api.EnvVar) error {
	seen := map[string]bool{}
	for _, e := range vars {
		if !envKeyRe.MatchString(e.Key) || len(e.Key) > 256 {
			return badRequest("invalid env key %q", e.Key)
		}
		if seen[e.Key] {
			return badRequest("duplicate env key %q", e.Key)
		}
		seen[e.Key] = true
		if len(e.Value) > 64<<10 {
			return badRequest("env %q value larger than 64 KiB", e.Key)
		}
	}
	return nil
}

func maskEnv(vars []api.EnvVar) []api.EnvVar {
	out := make([]api.EnvVar, len(vars))
	for i, e := range vars {
		if e.Secret {
			e.Value = ""
		}
		out[i] = e
	}
	return out
}

// normalizeHostname lowercases, trims and expands a bare label with the base domain.
func normalizeHostname(h, base string) (string, error) {
	h = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".")
	if h == "" {
		return "", badRequest("hostname required")
	}
	if !strings.Contains(h, ".") {
		if base == "" {
			return "", badRequest("%q is not a full hostname and no base domain is configured in Settings", h)
		}
		h = h + "." + base
	}
	if !hostnameRe.MatchString(h) || len(h) > 253 {
		return "", badRequest("invalid hostname %q", h)
	}
	return h, nil
}
