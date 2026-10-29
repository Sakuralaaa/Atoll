package k8s

import (
	"bufio"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/atoll-sh/atoll/internal/api"
)

// StorageRoot is where k3s local-path keeps PVC data on Zeabur servers.
const StorageRoot = "/var/lib/rancher/k3s/storage"

// HostFacts are host-level readings that complement the namespace objects.
type HostFacts struct {
	// DirBytes maps a directory (full path or basename under StorageRoot) to its size,
	// from ParseDu(`du -sb /var/lib/rancher/k3s/storage/*`).
	DirBytes map[string]int64
	// CachedImages is the set of refs from ParseCtrImages(`k3s ctr -n k8s.io images ls -q`).
	CachedImages map[string]bool
}

// ParseDu parses `du -sb DIR/*` (or `du -sk`, multiply yourself) output into path → bytes.
func ParseDu(out string) map[string]int64 {
	m := map[string]int64{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			continue
		}
		p := strings.Join(f[1:], " ")
		m[p] = n
		m[path.Base(p)] = n
	}
	return m
}

// ParseCtrImages parses `k3s ctr -n k8s.io images ls` (with or without -q) output.
func ParseCtrImages(out string) map[string]bool {
	m := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || f[0] == "REF" {
			continue
		}
		m[f[0]] = true
		if d := DigestOf(line); d != "" {
			m[d] = true
		}
	}
	return m
}

var workloadNameRe = regexp.MustCompile(`^(?:service|deployment)-([a-f0-9]{24})$`)
var pvcNameRe = regexp.MustCompile(`^(.+)-service-([a-f0-9]{24})$`)

// Namespace is the parsed content of one environment-<envID> namespace.
type Namespace struct {
	Name    string
	Objects []Object
}

type index struct {
	workloads map[string]Object // service id → Deployment/StatefulSet
	services  map[string]Object // k8s Service by name
	pvcs      map[string]Object
	cms       map[string]Object
	ingresses []Object
	pods      map[string]Object // service id → a pod
}

func buildIndex(objs []Object) index {
	ix := index{workloads: map[string]Object{}, services: map[string]Object{}, pvcs: map[string]Object{}, cms: map[string]Object{}, pods: map[string]Object{}}
	for _, o := range objs {
		switch o.Kind {
		case "Deployment", "StatefulSet":
			id := o.Spec.Template.Metadata.Labels["zeabur_service_id"]
			if id == "" {
				if m := workloadNameRe.FindStringSubmatch(o.Metadata.Name); m != nil {
					id = m[1]
				}
			}
			if id == "" {
				continue
			}
			if prev, ok := ix.workloads[id]; ok && prev.Metadata.Name == "service-"+id {
				continue // prefer the canonical service-<id> name
			}
			ix.workloads[id] = o
		case "Service":
			ix.services[o.Metadata.Name] = o
		case "PersistentVolumeClaim":
			ix.pvcs[o.Metadata.Name] = o
		case "ConfigMap":
			ix.cms[o.Metadata.Name] = o
		case "Ingress":
			ix.ingresses = append(ix.ingresses, o)
		case "Pod":
			if id := o.Metadata.Labels["zeabur_service_id"]; id != "" {
				if prev, ok := ix.pods[id]; !ok || (prev.Status.Phase != "Running" && o.Status.Phase == "Running") {
					ix.pods[id] = o
				}
			}
		}
	}
	return ix
}

// Extract turns one namespace's objects into ZeaburServices (sorted by name). PVCs whose
// service has no workload (suspended services) are returned as services with Workload "".
func Extract(ns Namespace, facts HostFacts) []api.ZeaburService {
	ix := buildIndex(ns.Objects)
	var out []api.ZeaburService
	seen := map[string]bool{}
	for id, w := range ix.workloads {
		seen[id] = true
		out = append(out, extractOne(ns.Name, id, w, ix, facts))
	}
	// Suspended services: PVC without workload.
	orphans := map[string][]string{}
	for name := range ix.pvcs {
		if m := pvcNameRe.FindStringSubmatch(name); m != nil && !seen[m[2]] {
			orphans[m[2]] = append(orphans[m[2]], name)
		}
	}
	for id, pvcs := range orphans {
		sort.Strings(pvcs)
		s := api.ZeaburService{ServiceID: id, Name: dnsName(id, ix), Role: "other", Kind: "image", Ports: []api.Port{}, Env: []api.EnvVar{}, Domains: []string{}}
		for _, p := range pvcs {
			s.Volumes = append(s.Volumes, volumeFor(ns.Name, p, "", ix, facts))
		}
		s.Warnings = append(s.Warnings, "no workload found: the service is probably suspended (data kept in PVC). Restart it on Zeabur (`zeabur service restart --id "+id+"`) to inventory its spec, or migrate the data only")
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func dnsName(id string, ix index) string {
	if s, ok := ix.services["service-"+id]; ok {
		if n := s.Metadata.Labels["app.kubernetes.io/name"]; n != "" {
			return n
		}
	}
	return "service-" + id
}

func volumeFor(ns, pvcName, mount string, ix index, facts HostFacts) api.ZeaburVolume {
	v := api.ZeaburVolume{PVC: pvcName, MountPath: mount}
	if pvc, ok := ix.pvcs[pvcName]; ok && pvc.Spec.VolumeName != "" {
		v.HostDir = StorageRoot + "/" + pvc.Spec.VolumeName + "_" + ns + "_" + pvcName
	}
	if facts.DirBytes != nil {
		if n, ok := facts.DirBytes[v.HostDir]; ok && v.HostDir != "" {
			v.Bytes = n
		} else {
			// match by suffix _<ns>_<pvc> when the pv name is unknown
			suffix := "_" + ns + "_" + pvcName
			for p, n := range facts.DirBytes {
				if strings.HasSuffix(p, suffix) && strings.HasPrefix(p, "/") {
					v.Bytes = n
					if v.HostDir == "" {
						v.HostDir = p
					}
					break
				}
			}
		}
	}
	return v
}

func extractOne(ns, id string, w Object, ix index, facts HostFacts) api.ZeaburService {
	s := api.ZeaburService{
		ServiceID: id,
		Name:      dnsName(id, ix),
		Workload:  strings.ToLower(w.Kind) + "/" + w.Metadata.Name,
		Replicas:  1,
		Ports:     []api.Port{},
		Env:       []api.EnvVar{},
		Volumes:   []api.ZeaburVolume{},
		Domains:   []string{},
	}
	if w.Spec.Replicas != nil {
		s.Replicas = *w.Spec.Replicas
	}
	pod := w.Spec.Template.Spec
	if len(pod.Containers) == 0 {
		s.Warnings = append(s.Warnings, "workload has no containers")
		return s
	}
	if len(pod.Containers) > 1 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("workload has %d containers; only %q is imported", len(pod.Containers), pod.Containers[0].Name))
	}
	c := pod.Containers[0]
	s.Image = c.Image
	s.Command, s.Args = c.Command, c.Args
	if p, ok := ix.pods[id]; ok {
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Name == c.Name || len(p.Status.ContainerStatuses) == 1 {
				s.ImageID = cs.ImageID
				break
			}
		}
	}
	s.ZeaburRegistry = IsZeaburRegistry(c.Image)
	if facts.CachedImages != nil {
		d := DigestOf(s.ImageID)
		s.ImageCached = facts.CachedImages[c.Image] || (d != "" && facts.CachedImages[d])
	}
	s.Kind, s.Role = DetectKind(c.Image)
	if s.ZeaburRegistry {
		s.Kind, s.Role = "image", "app"
		if !s.ImageCached && facts.CachedImages != nil {
			s.Warnings = append(s.Warnings, "built image is not in the node's containerd cache; it cannot be exported and must be rebuilt from source")
		}
	} else if IsFloating(c.Image) && DigestOf(s.ImageID) == "" {
		s.Warnings = append(s.Warnings, "image tag is floating and the running digest is unknown; pin a version")
	}

	// env
	for _, e := range c.Env {
		if e.ValueFrom != nil {
			s.Warnings = append(s.Warnings, fmt.Sprintf("env %s uses valueFrom (not a plain value); set it manually", e.Name))
			continue
		}
		s.Env = append(s.Env, api.EnvVar{Key: e.Name, Value: e.Value, Secret: IsSecret(e.Name, e.Value)})
	}

	// ports, matched to the k8s Service (NodePort) and Ingress (http)
	httpPorts := map[int]bool{}
	ksvcName := "service-" + id
	for _, ing := range ix.ingresses {
		for _, r := range ing.Spec.Rules {
			for _, p := range r.HTTP.Paths {
				be := p.Backend.Service.Name
				if be == "" {
					be = p.Backend.ServiceName
				}
				if be != ksvcName {
					continue
				}
				if r.Host != "" && !contains(s.Domains, r.Host) {
					s.Domains = append(s.Domains, r.Host)
				}
				port := p.Backend.Service.Port.Number
				if port == 0 {
					port = targetPortInt(p.Backend.ServicePort)
				}
				if port == 0 {
					name := p.Backend.Service.Port.Name
					if name == "" {
						name = nodeString(p.Backend.ServicePort)
					}
					if ksvc, ok := ix.services[ksvcName]; ok {
						for _, sp := range ksvc.Spec.Ports {
							if sp.Name == name {
								port = sp.Port
							}
						}
					}
				}
				if ksvc, ok := ix.services[ksvcName]; ok {
					for _, sp := range ksvc.Spec.Ports {
						if sp.Port == port {
							if tp := targetPortInt(sp.TargetPort); tp > 0 {
								port = tp
							}
						}
					}
				}
				httpPorts[port] = true
			}
		}
	}
	sort.Strings(s.Domains)
	nodePorts := map[int]int{}
	if ksvc, ok := ix.services[ksvcName]; ok {
		for _, sp := range ksvc.Spec.Ports {
			tp := targetPortInt(sp.TargetPort)
			if tp == 0 {
				tp = sp.Port
			}
			if sp.NodePort > 0 {
				nodePorts[tp] = sp.NodePort
				s.NodePorts = append(s.NodePorts, sp.NodePort)
			}
		}
	}
	// Some NodePort services are separate objects selecting the same pod.
	for name, ksvc := range ix.services {
		if name == ksvcName || ksvc.Spec.Type != "NodePort" || !strings.Contains(name, id) {
			continue
		}
		for _, sp := range ksvc.Spec.Ports {
			if sp.NodePort > 0 {
				tp := targetPortInt(sp.TargetPort)
				if tp == 0 {
					tp = sp.Port
				}
				nodePorts[tp] = sp.NodePort
				s.NodePorts = append(s.NodePorts, sp.NodePort)
			}
		}
	}
	sort.Ints(s.NodePorts)
	for i, p := range c.Ports {
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("port%d", i)
		}
		proto := "tcp"
		switch {
		case strings.EqualFold(p.Protocol, "UDP"):
			proto = "udp"
		case httpPorts[p.ContainerPort]:
			proto = "http"
		case s.Role == "app" && (name == "web" || name == "http") && len(nodePorts) == 0:
			proto = "http"
		}
		port := api.Port{Name: SanitizeName(name), ContainerPort: p.ContainerPort, Protocol: proto}
		if np, ok := nodePorts[p.ContainerPort]; ok && proto != "http" {
			port.PublicPort = np
		}
		s.Ports = append(s.Ports, port)
	}
	if len(s.NodePorts) > 0 {
		s.Warnings = append(s.Warnings, "has public TCP/UDP NodePorts; decide whether they are still needed and add access control")
	}

	// volumes and config files
	vols := map[string]PodVolume{}
	for _, v := range pod.Volumes {
		vols[v.Name] = v
	}
	for _, m := range c.VolumeMounts {
		v, ok := vols[m.Name]
		if !ok {
			continue
		}
		switch {
		case v.PersistentVolumeClaim != nil:
			s.Volumes = append(s.Volumes, volumeFor(ns, v.PersistentVolumeClaim.ClaimName, m.MountPath, ix, facts))
		case v.ConfigMap != nil:
			cm, ok := ix.cms[v.ConfigMap.Name]
			if !ok {
				s.Warnings = append(s.Warnings, fmt.Sprintf("config file %s: ConfigMap %s not found", m.MountPath, v.ConfigMap.Name))
				continue
			}
			key := m.SubPath
			for _, it := range v.ConfigMap.Items {
				if it.Path == m.SubPath && it.Key != "" {
					key = it.Key
				}
			}
			if key == "" && len(cm.Data) == 1 {
				for k := range cm.Data {
					key = k
				}
			}
			if content, ok := cm.Data[key]; ok {
				s.Configs = append(s.Configs, api.ConfigFile{Path: m.MountPath, Content: content})
			} else if m.SubPath == "" {
				for k, content := range cm.Data {
					s.Configs = append(s.Configs, api.ConfigFile{Path: path.Join(m.MountPath, k), Content: content})
				}
			}
			for _, cf := range s.Configs {
				if strings.Contains(cf.Content, "zeaburextension") {
					s.Warnings = append(s.Warnings, fmt.Sprintf("config %s contains the Zeabur-only `zeaburextension` Caddy directive; the official caddy image fails with it — remove it", cf.Path))
				}
			}
		}
	}
	sort.Slice(s.Configs, func(i, j int) bool { return s.Configs[i].Path < s.Configs[j].Path })
	for _, cf := range s.Configs {
		if strings.Contains(cf.Content, ".zeabur.internal") {
			s.Warnings = append(s.Warnings, fmt.Sprintf("config %s references .zeabur.internal hosts; they are rewritten to service names on import", cf.Path))
		}
	}
	for _, d := range s.Domains {
		if strings.HasSuffix(d, ".zeabur.app") {
			s.Warnings = append(s.Warnings, d+" is a Zeabur-generated domain and stops resolving at shutdown; update OAuth callbacks/webhooks that use it")
		}
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// ScanNamespaces groups objects by namespace and labels them with project info from the
// Namespace objects (labels zeabur_project_id/name, zeabur_environment_name).
func ScanNamespaces(objs []Object, facts HostFacts) []api.ZeaburProject {
	byNS := map[string][]Object{}
	nsMeta := map[string]Meta{}
	for _, o := range objs {
		if o.Kind == "Namespace" {
			nsMeta[o.Metadata.Name] = o.Metadata
			continue
		}
		if strings.HasPrefix(o.Metadata.Namespace, "environment-") {
			byNS[o.Metadata.Namespace] = append(byNS[o.Metadata.Namespace], o)
		}
	}
	for name := range nsMeta {
		if strings.HasPrefix(name, "environment-") {
			if _, ok := byNS[name]; !ok {
				byNS[name] = nil
			}
		}
	}
	var out []api.ZeaburProject
	for ns, list := range byNS {
		m := nsMeta[ns]
		out = append(out, api.ZeaburProject{
			Namespace:       ns,
			ProjectID:       m.Labels["zeabur_project_id"],
			ProjectName:     m.Labels["zeabur_project_name"],
			EnvironmentName: m.Labels["zeabur_environment_name"],
			Services:        Extract(Namespace{Name: ns, Objects: list}, facts),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace < out[j].Namespace })
	return out
}
