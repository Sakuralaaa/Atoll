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

// index holds the namespace's objects plus the reference-based attribution (attribution.go):
// resources attach to the workloads that reference them; names are only a fallback for
// objects no workload references.
type index struct {
	ns        string
	workloads map[string]Object // service id → Deployment/StatefulSet
	byRef     map[WorkloadRef]string
	services  map[string]Object // k8s Service by name
	pvcs      map[string]Object
	cms       map[string]Object
	ingresses []Object
	pods      map[string][]Object // service id → its pods
	rels      map[WorkloadRef][]Relation
	attr      NamespaceAttribution
	all       []Object // every workload (Workloads order)
}

func serviceIDOf(o Object) string {
	if o.Kind != "Deployment" && o.Kind != "StatefulSet" {
		return ""
	}
	if id := o.Spec.Template.Metadata.Labels["zeabur_service_id"]; id != "" {
		return id
	}
	if m := workloadNameRe.FindStringSubmatch(o.Metadata.Name); m != nil {
		return m[1]
	}
	return ""
}

func buildIndex(ns string, objs []Object) index {
	ix := index{ns: ns, workloads: map[string]Object{}, byRef: map[WorkloadRef]string{}, services: map[string]Object{},
		pvcs: map[string]Object{}, cms: map[string]Object{}, pods: map[string][]Object{}, rels: map[WorkloadRef][]Relation{}}
	var pods []Object
	for _, o := range objs {
		switch o.Kind {
		case "Deployment", "StatefulSet":
			id := serviceIDOf(o)
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
			pods = append(pods, o)
		}
	}
	for id, w := range ix.workloads {
		ix.byRef[refOf(w)] = id
	}
	ix.all = Workloads(objs)
	rels := relationsOf(objs, ix.all)
	for _, r := range rels {
		ix.rels[r.Workload] = append(ix.rels[r.Workload], r)
	}
	ix.attr = attributionOf(objs, ix.all, rels)
	for _, p := range pods {
		id := p.Metadata.Labels["zeabur_service_id"]
		if id == "" {
			for wid, w := range ix.workloads {
				if matchesSelector(labelSelector(w.Spec.Selector), p.Metadata.Labels) {
					id = wid
					break
				}
			}
		}
		if id != "" {
			ix.pods[id] = append(ix.pods[id], p)
		}
	}
	for id, ps := range ix.pods {
		// Running pods first: the image digest is read from the first one.
		sort.SliceStable(ps, func(i, j int) bool { return ps[i].Status.Phase == "Running" && ps[j].Status.Phase != "Running" })
		ix.pods[id] = ps
	}
	return ix
}

// k8sServices returns the Kubernetes Services of a Zeabur service: those whose selector matches
// its pod template (by reference), plus — only for Services that select no workload — the
// canonical service-<id> and NodePort Services named after the id (name fallback).
func (ix index) k8sServices(id string, w Object) []string {
	set := map[string]bool{}
	for _, r := range ix.rels[refOf(w)] {
		if r.Kind == RelSelector {
			set[r.Object.Name] = true
		}
	}
	unclaimed := func(name string) bool {
		a, ok := ix.attr.Services[name]
		return ok && a.Ownership == Orphan
	}
	if unclaimed("service-" + id) {
		set["service-"+id] = true
	}
	for name, svc := range ix.services {
		if svc.Spec.Type == "NodePort" && strings.Contains(name, id) && unclaimed(name) {
			set[name] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Extract turns one namespace's objects into ZeaburServices (sorted by name). PVCs whose
// service has no workload (suspended services) are returned as services with Workload "".
func Extract(ns Namespace, facts HostFacts) []api.ZeaburService {
	out, _ := ExtractNamespace(ns, facts)
	return out
}

// ExtractNamespace is Extract plus namespace-level warnings (orphan PVCs that belong to no
// service, workloads that are not Zeabur services).
func ExtractNamespace(ns Namespace, facts HostFacts) ([]api.ZeaburService, []string) {
	ix := buildIndex(ns.Name, ns.Objects)
	var out []api.ZeaburService
	var nsWarnings []string
	for id, w := range ix.workloads {
		out = append(out, extractOne(ns.Name, id, w, ix, facts))
	}
	for _, w := range ix.all {
		if _, ok := ix.byRef[refOf(w)]; !ok {
			nsWarnings = append(nsWarnings, fmt.Sprintf("%s/%s is not a Zeabur service (no zeabur_service_id label or service-<id> name); it is not imported", strings.ToLower(w.Kind), w.Metadata.Name))
		}
	}
	// PVCs no workload references: suspended services (their workload was deleted), volumes
	// detached from a live service, or leftovers.
	orphans := map[string][]string{}
	pvcNames := make([]string, 0, len(ix.attr.PersistentVolumeClaims))
	for name, a := range ix.attr.PersistentVolumeClaims {
		if a.Ownership == Orphan {
			pvcNames = append(pvcNames, name)
		}
	}
	sort.Strings(pvcNames)
	for _, name := range pvcNames {
		m := pvcNameRe.FindStringSubmatch(name)
		switch {
		case m == nil:
			nsWarnings = append(nsWarnings, fmt.Sprintf("PVC %s is not mounted by any workload and does not follow Zeabur naming; its data is not migrated automatically", name))
		case ix.workloads[m[2]].Kind != "":
			for i := range out {
				if out[i].ServiceID == m[2] {
					out[i].Warnings = append(out[i].Warnings, fmt.Sprintf("PVC %s belongs to this service by name but is not mounted; its data is not migrated automatically", name))
				}
			}
		default:
			orphans[m[2]] = append(orphans[m[2]], name)
		}
	}
	for id, pvcs := range orphans {
		s := api.ZeaburService{ServiceID: id, Name: dnsName(id, ix), Role: "other", Kind: "image", Status: StatusSuspended, Ports: []api.Port{}, Env: []api.EnvVar{}, Domains: []string{}}
		for _, p := range pvcs {
			s.Volumes = append(s.Volumes, volumeFor(ns.Name, p, "", ix, facts))
		}
		s.Warnings = append(s.Warnings, "no workload found: the service is probably suspended (data kept in PVC). Restart it on Zeabur (`zeabur service restart --id "+id+"`) to inventory its spec, or migrate the data only")
		out = append(out, s)
	}
	addReferences(out, ix)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nsWarnings
}

func dnsName(id string, ix index) string {
	if s, ok := ix.services["service-"+id]; ok {
		if n := s.Metadata.Labels["app.kubernetes.io/name"]; n != "" {
			return n
		}
	}
	if w, ok := ix.workloads[id]; ok {
		for _, name := range ix.k8sServices(id, w) {
			if n := ix.services[name].Metadata.Labels["app.kubernetes.io/name"]; n != "" {
				return n
			}
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

// resolveEnv returns the container's effective env: envFrom ConfigMap imports first, then env
// (which wins), configMapKeyRef resolved from the ConfigMap. Secret-backed values are never
// read; they and downward-API fields become warnings.
func resolveEnv(c Container, ix index) ([]api.EnvVar, []string) {
	var env []api.EnvVar
	var warns []string
	pos := map[string]int{}
	set := func(k, v string) {
		e := api.EnvVar{Key: k, Value: v, Secret: IsSecret(k, v)}
		if i, ok := pos[k]; ok {
			env[i] = e
			return
		}
		pos[k] = len(env)
		env = append(env, e)
	}
	for _, ef := range c.EnvFrom {
		if ef.ConfigMapRef != nil {
			cm, ok := ix.cms[ef.ConfigMapRef.Name]
			if !ok {
				warns = append(warns, fmt.Sprintf("envFrom ConfigMap %s not found", ef.ConfigMapRef.Name))
				continue
			}
			keys := make([]string, 0, len(cm.Data))
			for k := range cm.Data {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if envNameRe.MatchString(ef.Prefix + k) {
					set(ef.Prefix+k, cm.Data[k])
				}
			}
		}
		if ef.SecretRef != nil {
			warns = append(warns, fmt.Sprintf("envFrom imports Secret %s; its values are not read — set those variables manually", ef.SecretRef.Name))
		}
	}
	for _, e := range c.Env {
		vs := e.Source()
		switch {
		case vs == nil:
			set(e.Name, e.Value)
		case vs.ConfigMapKeyRef != nil:
			if v, ok := ix.cms[vs.ConfigMapKeyRef.Name].Data[vs.ConfigMapKeyRef.Key]; ok {
				set(e.Name, v)
			} else {
				warns = append(warns, fmt.Sprintf("env %s uses valueFrom ConfigMap %s key %s, which was not found; set it manually", e.Name, vs.ConfigMapKeyRef.Name, vs.ConfigMapKeyRef.Key))
			}
		case vs.SecretKeyRef != nil:
			warns = append(warns, fmt.Sprintf("env %s uses valueFrom Secret %s (key %s); its value is not read — set it manually", e.Name, vs.SecretKeyRef.Name, vs.SecretKeyRef.Key))
		default:
			warns = append(warns, fmt.Sprintf("env %s uses valueFrom (not a plain value); set it manually", e.Name))
		}
	}
	return env, warns
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
	s.Status = ScanStatus(&w, ix.pods[id])
	svcNames := ix.k8sServices(id, w)
	s.K8sServices = svcNames
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
	if ps := ix.pods[id]; len(ps) > 0 {
		p := ps[0]
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
	} else {
		ds := DataStoreOf(w)
		s.DataStore = string(ds)
		if ds == StoreOther && s.Role == "app" {
			// StatefulSet with persistent storage: treat as a data store, no known dump.
			s.Role = "database"
		}
		if IsFloating(c.Image) && DigestOf(s.ImageID) == "" {
			s.Warnings = append(s.Warnings, "image tag is floating and the running digest is unknown; pin a version")
		}
	}

	// env
	env, envWarns := resolveEnv(c, ix)
	s.Env = append(s.Env, env...)
	s.Warnings = append(s.Warnings, envWarns...)

	// ports, matched to the k8s Services of this workload (NodePort) and Ingresses routing to
	// them (http)
	svcSet := map[string]bool{}
	for _, n := range svcNames {
		svcSet[n] = true
	}
	httpPorts := map[int]bool{}
	resolvePort := func(svcName string, be IngressBackend) int {
		port := be.Service.Port.Number
		if port == 0 {
			port = targetPortInt(be.ServicePort)
		}
		ksvc, ok := ix.services[svcName]
		if port == 0 && ok {
			name := be.Service.Port.Name
			if name == "" {
				name = nodeString(be.ServicePort)
			}
			for _, sp := range ksvc.Spec.Ports {
				if sp.Name == name {
					port = sp.Port
				}
			}
		}
		if ok {
			for _, sp := range ksvc.Spec.Ports {
				if sp.Port == port {
					if tp := targetPortInt(sp.TargetPort); tp > 0 {
						port = tp
					}
				}
			}
		}
		return port
	}
	for _, ing := range ix.ingresses {
		if b := ing.Spec.DefaultBackend; b != nil && svcSet[b.ServiceNameAny()] {
			httpPorts[resolvePort(b.ServiceNameAny(), *b)] = true
			for _, t := range ing.Spec.TLS {
				for _, h := range t.Hosts {
					if h != "" && !contains(s.Domains, h) {
						s.Domains = append(s.Domains, h)
					}
				}
			}
		}
		for _, r := range ing.Spec.Rules {
			for _, p := range r.HTTP.Paths {
				be := p.Backend.ServiceNameAny()
				if !svcSet[be] {
					continue
				}
				if r.Host != "" && !contains(s.Domains, r.Host) {
					s.Domains = append(s.Domains, r.Host)
				}
				httpPorts[resolvePort(be, p.Backend)] = true
			}
		}
	}
	sort.Strings(s.Domains)
	nodePorts := map[int]int{}
	for _, name := range svcNames {
		for _, sp := range ix.services[name].Spec.Ports {
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

	// volumes and config files, followed from the mounts to the referenced objects
	vols := map[string]PodVolume{}
	for _, v := range pod.Volumes {
		vols[v.Name] = v
	}
	claimed := map[string][]string{} // claim template → PVCs it created
	for _, r := range ix.rels[refOf(w)] {
		if r.Kind == RelClaimTemplate {
			for _, t := range w.Spec.VolumeClaimTemplates {
				if CreatedByClaimTemplate(r.Object.Name, t.Metadata.Name, w.Metadata.Name) {
					claimed[t.Metadata.Name] = append(claimed[t.Metadata.Name], r.Object.Name)
				}
			}
		}
	}
	mountedPVC := map[string]bool{}
	for _, m := range c.VolumeMounts {
		v, ok := vols[m.Name]
		if !ok {
			// StatefulSet claim template: the controller adds the volume per pod.
			for _, t := range w.Spec.VolumeClaimTemplates {
				if t.Metadata.Name != m.Name {
					continue
				}
				pvc := t.Metadata.Name + "-" + w.Metadata.Name + "-0"
				if len(claimed[t.Metadata.Name]) > 1 {
					s.Warnings = append(s.Warnings, fmt.Sprintf("claim template %s has %d PVCs (one per replica); only %s is migrated", t.Metadata.Name, len(claimed[t.Metadata.Name]), pvc))
				}
				if _, ok := ix.pvcs[pvc]; !ok {
					s.Warnings = append(s.Warnings, fmt.Sprintf("claim template %s has no PVC %s yet (not provisioned); nothing to migrate for %s", t.Metadata.Name, pvc, m.MountPath))
				}
				s.Volumes = append(s.Volumes, volumeFor(ns, pvc, m.MountPath, ix, facts))
				mountedPVC[pvc] = true
			}
			continue
		}
		switch {
		case v.PersistentVolumeClaim != nil:
			s.Volumes = append(s.Volumes, volumeFor(ns, v.PersistentVolumeClaim.ClaimName, m.MountPath, ix, facts))
			mountedPVC[v.PersistentVolumeClaim.ClaimName] = true
			if a := ix.attr.PersistentVolumeClaims[v.PersistentVolumeClaim.ClaimName]; a.Ownership == Shared {
				s.Warnings = append(s.Warnings, fmt.Sprintf("PVC %s is shared by %d workloads; Atoll volumes are per service, so the data is copied, not shared", v.PersistentVolumeClaim.ClaimName, len(a.Owners)))
			}
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
		case v.Secret != nil:
			s.Warnings = append(s.Warnings, fmt.Sprintf("%s is mounted from Secret %s; its files are not read — recreate them as config files", m.MountPath, v.Secret.SecretName))
		}
	}
	for _, r := range ix.rels[refOf(w)] {
		if r.Object.Kind == "PersistentVolumeClaim" && !mountedPVC[r.Object.Name] && r.Kind == RelVolume {
			s.Warnings = append(s.Warnings, fmt.Sprintf("PVC %s is referenced by the workload but not mounted in the imported container; its data is not migrated automatically", r.Object.Name))
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
	if !s.ZeaburRegistry {
		s.Warnings = append(s.Warnings, DurabilityWarnings(w)...)
	}
	return s
}

// addReferences fills References: literal env values naming another service's k8s Service
// (short or cluster DNS) or <dns>.zeabur.internal, and Secrets/ConfigMaps shared with a data
// store (key-level, so the plan can write ${STORE.KEY}).
func addReferences(svcs []api.ZeaburService, ix index) {
	known := map[string]bool{}
	byK8s := map[string]string{}
	byDNS := map[string]string{}
	for _, s := range svcs {
		for _, n := range s.K8sServices {
			known[n] = true
			byK8s[n] = s.ServiceID
		}
		if s.Workload != "" {
			byDNS[s.Name] = s.ServiceID
		}
	}
	type store struct {
		id   string
		refs []configRef
	}
	var stores []store
	for id, w := range ix.workloads {
		if DataStoreOf(w) != "" {
			stores = append(stores, store{id, configRefs(w)})
		}
	}
	sort.Slice(stores, func(i, j int) bool { return stores[i].id < stores[j].id })
	for i := range svcs {
		s := &svcs[i]
		seen := map[api.ZeaburReference]bool{}
		add := func(r api.ZeaburReference) {
			if r.Target != s.ServiceID && !seen[r] {
				seen[r] = true
				s.References = append(s.References, r)
			}
		}
		for _, e := range s.Env {
			for _, n := range MentionedServices(e.Value, ix.ns, known) {
				add(api.ZeaburReference{Key: e.Key, Target: byK8s[n], Via: ViaEnvValue, Object: n, Inferred: true})
			}
			for _, m := range internalRe.FindAllStringSubmatch(e.Value, -1) {
				if id, ok := byDNS[m[1]]; ok {
					add(api.ZeaburReference{Key: e.Key, Target: id, Via: ViaZeaburInternal, Object: m[1] + ".zeabur.internal", Inferred: true})
				}
			}
		}
		w, ok := ix.workloads[s.ServiceID]
		var mine []configRef
		if ok && DataStoreOf(w) == "" {
			mine = configRefs(w)
		}
		for _, r := range mine {
			for _, st := range stores {
				shared := false
				for _, sr := range st.refs {
					shared = shared || sr.Object == r.Object
				}
				if !shared {
					continue
				}
				via := ViaSharedSecret
				if r.Object.Kind == "ConfigMap" {
					via = ViaSharedConfigMap
				}
				add(api.ZeaburReference{Key: r.Env, Target: st.id, TargetKey: targetKeyFor(st.refs, r.Object, r.Key), Via: via, Object: r.Object.String()})
			}
		}
		sort.SliceStable(s.References, func(a, b int) bool {
			x, y := s.References[a], s.References[b]
			if x.Key != y.Key {
				return x.Key < y.Key
			}
			return x.Target < y.Target
		})
	}
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
		})
		out[len(out)-1].Services, out[len(out)-1].Warnings = ExtractNamespace(Namespace{Name: ns, Objects: list}, facts)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace < out[j].Namespace })
	return out
}
