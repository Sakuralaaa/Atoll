package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Host-path volumes (api.Volume.HostPath) mount an existing host directory or file in place:
// Docker adoption keeps an adopted container's bind mounts and named volumes where they are.
// Atoll never deletes, backs up or migrates such a path.

// deniedHostPaths are never mounted from a spec, whatever the control server sends: they
// would hand a service the host (or Atoll itself).
var deniedHostPaths = []string{"/etc", "/proc", "/sys", "/dev", "/boot", "/root", "/run", "/var/run", "/usr", "/bin", "/sbin", "/lib", "/lib64"}

// readOnlyHostPaths may be mounted read-only although they sit under a denied dir: the
// host's time zone, which many compose files bind (/etc/localtime:/etc/localtime:ro).
var readOnlyHostPaths = []string{"/etc/localtime", "/etc/timezone", "/usr/share/zoneinfo"}

// CheckHostPath validates a host path of a spec volume (readOnly: mounted read-only).
func CheckHostPath(p Paths, hp string, readOnly bool) error {
	if !filepath.IsAbs(hp) || filepath.Clean(hp) != hp || strings.ContainsRune(hp, 0) {
		return fmt.Errorf("host path %q must be a clean absolute path", hp)
	}
	if hp == "/" {
		return fmt.Errorf("host path / is not allowed")
	}
	if strings.HasSuffix(hp, "docker.sock") || strings.HasSuffix(hp, "containerd.sock") {
		return fmt.Errorf("host path %s: container runtime sockets are never mounted", hp)
	}
	if readOnly {
		for _, a := range readOnlyHostPaths {
			if hp == a || strings.HasPrefix(hp, a+"/") {
				return nil
			}
		}
	}
	for _, d := range deniedHostPaths {
		if hp == d || strings.HasPrefix(hp, d+"/") {
			return fmt.Errorf("host path %s is under %s, which Atoll never mounts", hp, d)
		}
	}
	if root := filepath.Clean(p.Root); root != "" && root != "." && (hp == root || strings.HasPrefix(hp, root+"/")) {
		return fmt.Errorf("host path %s is inside Atoll's data dir", hp)
	}
	if hp == "/var/lib/docker" || hp == "/var/lib/docker/volumes" {
		return fmt.Errorf("host path %s is Docker's own data dir", hp)
	}
	return nil
}

// HasHostPath reports whether any volume of spec is mounted in place from the host.
func HasHostPath(s api.ResolvedSpec) bool {
	for _, v := range s.Volumes {
		if v.HostPath != "" {
			return true
		}
	}
	return false
}

// ErrHostPathVolumes explains why a volume operation refuses an adopted service.
func ErrHostPathVolumes(serviceID string) error {
	return fmt.Errorf("service %s mounts adopted host paths in place (Docker adoption); Atoll does not back up, restore or move them — copy that data yourself", serviceID)
}
