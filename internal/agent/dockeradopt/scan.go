package dockeradopt

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Skip reports whether a container is not the user's to adopt: Atoll's own containers
// (atoll.* labels or the atoll- name prefix) and compose one-off `run` containers.
func Skip(c Container) bool {
	for k := range c.Labels {
		if strings.HasPrefix(k, "atoll.") {
			return true
		}
	}
	if strings.HasPrefix(c.Name, "atoll-") {
		return true
	}
	return strings.EqualFold(c.Labels[labelComposeOneoff], "true")
}

// FileInfo answers whether a host path exists and is a regular file (replaceable in tests).
type FileInfo func(p string) (exists, isFile bool)

// reservedPorts mirrors the server's reserved public ports (internal/server/validate.go).
func reservedPort(p int) string {
	switch {
	case p == 80 || p == 443:
		return "Atoll's ingress (Caddy) serves 80/443; route the sites through Atoll domains instead"
	case p == 22:
		return "SSH"
	case p == 7301:
		return "the Atoll agent endpoint on the tailnet"
	case p == 41641:
		return "Tailscale WireGuard"
	case p >= 41000 && p <= 41999:
		return "the Atoll private-network mesh range 41000-41999"
	}
	return ""
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitPort parses "80/tcp".
func splitPort(s string) (int, string) {
	num, proto, _ := strings.Cut(s, "/")
	n, _ := strconv.Atoi(num)
	if proto == "" {
		proto = "tcp"
	}
	return n, proto
}

// Classify turns one container (and the image it runs) into what the server adopts:
// configuration relative to the image, risk tier with the reasons, and warnings.
func Classify(c Container, img *Image, paths agent.Paths, fi FileInfo) api.DockerContainer {
	d := api.DockerContainer{
		ID: c.ID, Name: c.Name, Service: c.Labels[labelComposeService], Image: c.Image, ImageID: c.ImageID,
		State: c.State, RestartPolicy: c.RestartPolicy, NetworkMode: c.NetworkMode, Networks: c.Networks,
		User: c.User, Env: []api.EnvVar{}, Ports: []api.DockerPort{}, Mounts: []api.DockerMount{},
	}
	manual := func(f string, a ...any) { d.RiskReasons = append(d.RiskReasons, fmt.Sprintf(f, a...)) }
	warn := func(f string, a ...any) { d.Warnings = append(d.Warnings, fmt.Sprintf(f, a...)) }

	// image: pin the registry digest of the running image when one is known
	if img != nil {
		repo := repoOf(c.Image)
		for _, rd := range img.RepoDigests {
			r, _, _ := strings.Cut(rd, "@")
			if normRepo(r) == normRepo(repo) {
				d.PinnedImage = rd
				break
			}
		}
		if d.PinnedImage == "" {
			warn("image %s has no registry digest (built or loaded locally): Atoll runs the same local image under an atoll-adopt/ tag, so this service cannot move to another server", c.Image)
		}
	} else {
		manual("its image %s is no longer on this server", c.Image)
	}

	// env: only what the container adds to (or changes in) the image's env
	imgEnv := map[string]bool{}
	if img != nil {
		for _, e := range img.Env {
			imgEnv[e] = true
		}
	}
	for _, e := range c.Env {
		if imgEnv[e] {
			continue
		}
		k, v, _ := strings.Cut(e, "=")
		if k == "" {
			continue
		}
		d.Env = append(d.Env, api.EnvVar{Key: k, Value: v})
	}

	// command: Atoll's Command replaces ENTRYPOINT, Args replaces CMD (like docker)
	var ie, ic []string
	if img != nil {
		ie, ic = img.Entrypoint, img.Cmd
	}
	if !sameList(c.Entrypoint, ie) {
		d.Command = append([]string{}, c.Entrypoint...)
		d.Args = append([]string{}, c.Cmd...)
		if len(d.Command) == 0 {
			manual("clears the image's ENTRYPOINT, which Atoll cannot express")
		}
	} else if !sameList(c.Cmd, ic) {
		d.Args = append([]string{}, c.Cmd...)
	}
	if img != nil && c.User == img.User {
		d.User = ""
	}
	if img != nil && c.WorkingDir != "" && c.WorkingDir != img.WorkingDir {
		manual("sets its working directory to %s (docker -w / compose working_dir), which Atoll cannot set", c.WorkingDir)
	}
	if c.Health != nil && (img == nil || img.Health == nil || !sameList(c.Health.Test, img.Health.Test)) {
		d.Health = &api.HealthCheck{Cmd: c.Health.Test, IntervalSec: int(c.Health.Interval.Seconds())}
	}

	// networking
	switch nm := c.NetworkMode; {
	case nm == "host":
		manual("uses the host network (network_mode: host): its ports cannot be moved behind Atoll's network")
	case nm == "none":
		manual("has no network (network_mode: none)")
	case strings.HasPrefix(nm, "container:") || strings.HasPrefix(nm, "service:"):
		manual("shares another container's network namespace (%s)", nm)
	}
	if len(c.Links) > 0 {
		warn("legacy --link entries are not kept; services reach each other by service name")
	}

	// ports: published bindings (0.0.0.0 and :: merged), then the exposed-only ones
	keys := make([]string, 0, len(c.Bindings))
	for k := range c.Bindings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	published := map[string]bool{}
	for _, k := range keys {
		cp, proto := splitPort(k)
		seen := map[string]bool{}
		for _, b := range c.Bindings[k] {
			hp, _ := strconv.Atoi(b.HostPort)
			if hp == 0 {
				warn("port %s is published on a random host port; Atoll keeps it internal", k)
				continue
			}
			ip := b.HostIP
			if ip == "0.0.0.0" || ip == "::" || ip == "[::]" {
				ip = ""
			}
			key := fmt.Sprintf("%d/%s", hp, ip)
			if seen[key] {
				continue
			}
			seen[key] = true
			published[k] = true
			if why := reservedPort(hp); why != "" && ip != "127.0.0.1" && ip != "::1" {
				manual("publishes host port %d, reserved for %s", hp, why)
			}
			d.Ports = append(d.Ports, api.DockerPort{ContainerPort: cp, HostPort: hp, Protocol: proto, HostIP: ip})
		}
	}
	exp := append([]string{}, c.Exposed...)
	sort.Strings(exp)
	for _, k := range exp {
		if published[k] {
			continue
		}
		cp, proto := splitPort(k)
		if cp > 0 {
			d.Ports = append(d.Ports, api.DockerPort{ContainerPort: cp, Protocol: proto})
		}
	}

	// mounts
	for _, m := range c.Mounts {
		dm := api.DockerMount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, ReadOnly: !m.RW, Driver: m.Driver}
		switch m.Type {
		case "bind":
			if isSocket(m.Source) {
				manual("mounts the container runtime socket %s: it controls the host's Docker", m.Source)
				continue
			}
			if err := agent.CheckHostPath(paths, path.Clean(m.Source), !m.RW); err != nil {
				manual("bind-mounts %s from the host: %v", m.Source, err)
				continue
			}
			exists, isFile := fi(m.Source)
			if !exists {
				manual("bind-mounts %s, which does not exist on the host", m.Source)
				continue
			}
			dm.IsFile = isFile
		case "volume":
			if m.Driver != "" && m.Driver != "local" {
				manual("uses volume %s with driver %q; only local volumes can be mounted in place", m.Name, m.Driver)
				continue
			}
			if err := agent.CheckHostPath(paths, path.Clean(m.Source), !m.RW); err != nil || m.Source == "" {
				manual("volume %s has an unusable mountpoint %q", m.Name, m.Source)
				continue
			}
			if exists, _ := fi(m.Source); !exists {
				manual("volume %s: its mountpoint %s does not exist", m.Name, m.Source)
				continue
			}
		case "tmpfs":
			warn("tmpfs at %s is not kept (writes go to the container's own filesystem)", m.Destination)
			continue
		default:
			manual("has a %s mount at %s", m.Type, m.Destination)
			continue
		}
		d.Mounts = append(d.Mounts, dm)
	}
	for _, t := range c.Tmpfs {
		warn("tmpfs at %s is not kept (writes go to the container's own filesystem)", t)
	}
	if len(c.VolumesFrom) > 0 {
		manual("uses --volumes-from %s", strings.Join(c.VolumesFrom, ", "))
	}

	// privileges: never adopted automatically
	if c.Privileged {
		manual("runs privileged (full access to the host)")
	}
	if len(c.CapAdd) > 0 {
		manual("adds Linux capabilities (%s)", strings.Join(c.CapAdd, ", "))
	}
	if len(c.Devices) > 0 {
		manual("has host devices (%s)", strings.Join(c.Devices, ", "))
	}
	if c.DeviceRequests > 0 {
		manual("requests devices such as GPUs (--gpus)")
	}
	for _, o := range c.SecurityOpt {
		if strings.Contains(o, "unconfined") || o == "label=disable" || o == "label:disable" {
			manual("disables a security profile (security_opt %s)", o)
		} else if !strings.HasPrefix(o, "no-new-privileges") {
			warn("security_opt %s is not kept", o)
		}
	}
	for _, ns := range []struct{ name, mode string }{{"PID", c.PidMode}, {"IPC", c.IpcMode}, {"UTS", c.UTSMode}, {"user", c.UsernsMode}} {
		if ns.mode == "host" {
			manual("shares the host's %s namespace", ns.name)
		}
	}
	if strings.HasPrefix(c.IpcMode, "container:") {
		manual("shares another container's IPC namespace (%s)", c.IpcMode)
	}
	if c.Runtime != "" && c.Runtime != "runc" && c.Runtime != "io.containerd.runc.v2" {
		manual("uses the %s runtime", c.Runtime)
	}

	// kept differently or not at all (adoptable, but worth knowing)
	if len(c.Sysctls) > 0 {
		warn("sysctls are not kept")
	}
	if len(c.ExtraHosts) > 0 {
		warn("extra_hosts (%s) are not kept", strings.Join(c.ExtraHosts, ", "))
	}
	if len(c.DNS) > 0 {
		warn("custom DNS servers are not kept")
	}
	if c.LogDriver != "" && c.LogDriver != "json-file" && c.LogDriver != "local" {
		warn("log driver %s is replaced by Atoll's json-file logs", c.LogDriver)
	}
	if c.ReadonlyRootfs {
		warn("read-only root filesystem is not kept")
	}
	if c.ShmSizeBytes > 64<<20 {
		warn("shm_size %d MB is not kept (Atoll uses Docker's 64 MB)", c.ShmSizeBytes>>20)
	}
	if c.Ulimits > 0 {
		warn("ulimits are not kept")
	}
	if c.Init {
		warn("--init is not kept")
	}
	switch strings.SplitN(c.RestartPolicy, ":", 2)[0] {
	case "", "no":
		warn("restart policy %q: Atoll restarts its copy automatically (unless-stopped)", c.RestartPolicy)
	}
	if c.State != "running" {
		warn("container is %s now; Atoll starts its copy at cutover", c.State)
	}
	for k := range c.Labels {
		if strings.HasPrefix(k, "traefik.") || strings.HasPrefix(k, "caddy") || k == "com.centurylinklabs.watchtower.enable" {
			warn("labels for other tools (traefik/caddy-docker-proxy/watchtower…) are not kept")
			break
		}
	}
	if c.MemoryBytes > 0 {
		d.MemLimitMB = int(c.MemoryBytes >> 20)
	}
	if c.NanoCPUs > 0 {
		d.CPULimit = float64(c.NanoCPUs) / 1e9
	}
	sort.Strings(d.Warnings)
	d.Warnings = dedupe(d.Warnings)
	d.Risk = api.DockerRiskAuto
	if len(d.RiskReasons) > 0 {
		d.Risk = api.DockerRiskManual
	}
	return d
}

func isSocket(p string) bool {
	b := path.Base(p)
	return strings.HasSuffix(b, ".sock") || p == "/var/run/docker.sock" || p == "/run/docker.sock"
}

func dedupe(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// repoOf strips the tag and digest from an image reference.
func repoOf(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref
}

// normRepo drops the implicit docker.io/ and library/ prefixes.
func normRepo(r string) string {
	r = strings.TrimPrefix(r, "docker.io/")
	r = strings.TrimPrefix(r, "index.docker.io/")
	return strings.TrimPrefix(r, "library/")
}

// Group groups classified containers by compose project; standalone containers form their
// own group. byName holds every container of the host (for cross-group findings).
func Group(cs []api.DockerContainer, raw map[string]Container) []api.DockerGroup {
	groups := map[string]*api.DockerGroup{}
	var order []string
	for _, d := range cs {
		rc := raw[d.ID]
		key, kind, name := "container:"+d.Name, "standalone", d.Name
		if p := rc.Labels[labelComposeProject]; p != "" {
			key, kind, name = "compose:"+p, "compose", p
		}
		g := groups[key]
		if g == nil {
			g = &api.DockerGroup{Key: key, Kind: kind, Name: name}
			groups[key] = g
			order = append(order, key)
		}
		// take the compose labels from whichever container carries them:
		// scan order is not stable, and not every container has them
		if kind == "compose" {
			if g.WorkingDir == "" {
				g.WorkingDir = rc.Labels[labelComposeDir]
			}
			if g.ConfigFiles == "" {
				g.ConfigFiles = rc.Labels[labelComposeFiles]
			}
		}
		g.Containers = append(g.Containers, d)
	}
	sort.Strings(order)
	// networks -> groups using them (user-defined only)
	netGroups := map[string]map[string]bool{}
	for _, k := range order {
		for _, c := range groups[k].Containers {
			for _, n := range c.Networks {
				if n == "bridge" || n == "host" || n == "none" {
					continue
				}
				if netGroups[n] == nil {
					netGroups[n] = map[string]bool{}
				}
				netGroups[n][k] = true
			}
		}
	}
	out := make([]api.DockerGroup, 0, len(order))
	for _, k := range order {
		g := groups[k]
		sort.Slice(g.Containers, func(i, j int) bool { return g.Containers[i].Name < g.Containers[j].Name })
		names := map[string]bool{}
		for _, c := range g.Containers {
			names[c.Name] = true
			if c.Risk == api.DockerRiskAuto {
				g.Adoptable = true
			}
		}
		for _, c := range g.Containers {
			if c.Risk == api.DockerRiskManual {
				g.Warnings = append(g.Warnings, fmt.Sprintf("%s needs manual handling and stays as it is: %s", c.Name, strings.Join(c.RiskReasons, "; ")))
			}
			// env values naming a container (not a service) of the group break: Atoll
			// only gives each service its service name on the project network
			for _, e := range c.Env {
				for _, o := range g.Containers {
					if o.Name != c.Name && o.Service != "" && o.Service != o.Name && strings.Contains(e.Value, o.Name) {
						g.Warnings = append(g.Warnings, fmt.Sprintf("%s: %s names container %s; after adoption use the service name %s", c.Name, e.Key, o.Name, o.Service))
					}
				}
			}
		}
		var shared []string
		for n, gs := range netGroups {
			if !gs[k] || len(gs) < 2 {
				continue
			}
			var others []string
			for o := range gs {
				if o != k {
					others = append(others, strings.SplitN(o, ":", 2)[1])
				}
			}
			sort.Strings(others)
			shared = append(shared, fmt.Sprintf("network %s is shared with %s", n, strings.Join(others, ", ")))
		}
		sort.Strings(shared)
		for _, s := range shared {
			g.Warnings = append(g.Warnings, s+"; after adoption those containers no longer reach this group by name")
		}
		g.Warnings = dedupe(g.Warnings)
		out = append(out, *g)
	}
	return out
}
