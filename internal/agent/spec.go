package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atoll-sh/atoll/internal/api"
)

// ContainerName is the docker container name of a service.
func ContainerName(serviceID string) string { return "atoll-" + serviceID }

// SpecHash fingerprints the parts of a spec that require recreating the container.
// Stopped (an action, not a config change) and registry credentials are excluded.
func SpecHash(s api.ResolvedSpec) string {
	s.Stopped = false
	s.RegistryAuth = nil
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func hasHTTPPort(s api.ResolvedSpec) bool {
	for _, p := range s.Ports {
		if p.Protocol == "http" || p.Protocol == "" {
			return true
		}
	}
	return false
}

func validVolumeName(n string) error {
	if n == "" || n == "." || n == ".." || strings.ContainsAny(n, "/\\\x00") {
		return fmt.Errorf("invalid volume name %q", n)
	}
	return nil
}

// configHostPath maps an absolute in-container config path to its host file.
func configHostPath(p Paths, serviceID, containerPath string) (string, error) {
	clean := path.Clean("/" + containerPath)
	if clean == "/" || !path.IsAbs(containerPath) {
		return "", fmt.Errorf("config path %q must be an absolute file path", containerPath)
	}
	return filepath.Join(p.ConfigsDir(serviceID), filepath.FromSlash(clean)), nil
}

// BuildContainerSpec turns a ResolvedSpec into the container definition.
//
// Command/Args follow Kubernetes semantics: Command replaces the image ENTRYPOINT,
// Args replaces CMD.
func BuildContainerSpec(p Paths, s api.ResolvedSpec) (ContainerSpec, error) {
	if err := validID(s.ServiceID); err != nil {
		return ContainerSpec{}, err
	}
	if s.Image == "" {
		return ContainerSpec{}, fmt.Errorf("spec %s has no image", s.ServiceID)
	}
	network := s.Network
	if network == "" {
		network = "atoll-" + s.ProjectID
	}
	cs := ContainerSpec{
		Name:       ContainerName(s.ServiceID),
		Image:      s.Image,
		Entrypoint: s.Command,
		Cmd:        s.Args,
		Labels: map[string]string{
			LabelManaged:    "true",
			LabelService:    s.ServiceID,
			LabelProject:    s.ProjectID,
			LabelDeployment: s.DeploymentID,
			LabelSpecHash:   SpecHash(s),
		},
		RestartPolicy: "unless-stopped",
	}
	aliases := []string{}
	if s.Name != "" {
		aliases = append(aliases, s.Name)
	}
	cs.Networks = append(cs.Networks, NetworkAttach{Name: network, Aliases: aliases})
	if hasHTTPPort(s) {
		cs.Networks = append(cs.Networks, NetworkAttach{Name: IngressNetwork})
	}

	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cs.Env = append(cs.Env, k+"="+s.Env[k])
	}

	for _, port := range s.Ports {
		switch port.Protocol {
		case "tcp", "udp":
			pb := PortBinding{ContainerPort: port.ContainerPort, HostPort: port.PublicPort, Protocol: port.Protocol}
			if port.PublicPort > 0 {
				cs.Ports = append(cs.Ports, pb)
			} else {
				cs.ExposedPorts = append(cs.ExposedPorts, pb)
			}
		default: // http: reached through atoll-caddy on atoll-ingress, never published
			cs.ExposedPorts = append(cs.ExposedPorts, PortBinding{ContainerPort: port.ContainerPort, Protocol: "tcp"})
		}
	}

	for _, v := range s.Volumes {
		if err := validVolumeName(v.Name); err != nil {
			return cs, err
		}
		cs.Mounts = append(cs.Mounts, Mount{Source: p.VolumeDir(s.ServiceID, v.Name), Target: v.MountPath})
	}
	for _, c := range s.Configs {
		hp, err := configHostPath(p, s.ServiceID, c.Path)
		if err != nil {
			return cs, err
		}
		cs.Mounts = append(cs.Mounts, Mount{Source: hp, Target: path.Clean(c.Path), ReadOnly: true})
	}

	if s.CPULimit > 0 {
		cs.NanoCPUs = int64(s.CPULimit * 1e9)
	}
	if s.MemLimitMB > 0 {
		cs.MemoryBytes = int64(s.MemLimitMB) * 1024 * 1024
	}
	cs.Health = buildHealth(s)
	return cs, nil
}

func buildHealth(s api.ResolvedSpec) *HealthSpec {
	h := s.Health
	if h == nil || (len(h.Cmd) == 0 && h.HTTPPath == "") {
		return nil
	}
	interval := time.Duration(h.IntervalSec) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	hs := &HealthSpec{Interval: interval, Timeout: 10 * time.Second, Retries: 3, Start: 30 * time.Second}
	if len(h.Cmd) > 0 {
		switch h.Cmd[0] {
		case "CMD", "CMD-SHELL", "NONE":
			hs.Test = h.Cmd
		default:
			hs.Test = append([]string{"CMD"}, h.Cmd...)
		}
		return hs
	}
	port := 0
	for _, p := range s.Ports {
		if p.Protocol == "http" || p.Protocol == "" {
			port = p.ContainerPort
			break
		}
	}
	if port == 0 {
		return nil
	}
	pth := h.HTTPPath
	if !strings.HasPrefix(pth, "/") {
		pth = "/" + pth
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, pth)
	q := shellQuote(url)
	// Images ship either wget (busybox/alpine) or curl; fail if neither succeeds.
	hs.Test = []string{"CMD-SHELL", fmt.Sprintf("wget -q -O /dev/null %s || curl -fsS -o /dev/null %s || exit 1", q, q)}
	return hs
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
