// Adapted from zeabur-oss/zeabur (Apache-2.0), crates/zeabur-graph/src/attribution.rs,
// crates/zeabur-graph/src/workload.rs (workloads, containers) and
// crates/zeabur-graph/src/snapshot.rs (Secrets carry metadata and type only).
// Copyright Zeabur contributors; modified for atoll. See THIRD_PARTY_NOTICES.md.

package k8s

import (
	"sort"
	"strings"
)

// Which workloads each resource of a namespace belongs to, decided by references alone:
// ownerReferences say which controller garbage-collects an object, not which workload uses it,
// and a ConfigMap two Deployments import has no ownerReference to either.

const (
	rootCAConfigMap         = "kube-root-ca.crt"
	serviceAccountTokenType = "kubernetes.io/service-account-token"
	helmReleaseSecretType   = "helm.sh/release.v1"
)

// WorkloadRef names a workload by kind and name.
type WorkloadRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// workloadKinds are the kinds that run pods, in alphabetical order (the sort order).
var workloadKinds = map[string]bool{"CronJob": true, "DaemonSet": true, "Deployment": true, "Job": true, "StatefulSet": true}

// ObjectRef names a namespaced object by kind and name.
type ObjectRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func (o ObjectRef) String() string { return o.Kind + "/" + o.Name }

// RelationKind is how a workload reaches an object.
type RelationKind string

const (
	RelEnvReference    RelationKind = "envReference"    // env[].valueFrom.{secret,configMap}KeyRef
	RelEnvImport       RelationKind = "envImport"       // envFrom[]
	RelVolume          RelationKind = "volume"          // volumes[] incl. projected sources
	RelImagePullSecret RelationKind = "imagePullSecret" // imagePullSecrets[]
	RelClaimTemplate   RelationKind = "claimTemplate"   // StatefulSet PVC <template>-<set>-<ordinal>
	RelSelector        RelationKind = "selector"        // Service selector matches pod labels
	RelRoute           RelationKind = "route"           // Ingress → Service → workload
)

// Relation is one reference from a workload to an object. A reference counts whether or not
// the object exists: a dangling reference is still what the workload says.
type Relation struct {
	Workload WorkloadRef
	Object   ObjectRef
	Kind     RelationKind
}

// Ownership of a resource: no owner is an orphan, one owns it, more share it.
type Ownership string

const (
	Orphan Ownership = "orphan"
	Owned  Ownership = "owned"
	Shared Ownership = "shared"
)

// Attribution is who uses one resource; Owners sorted by kind then name, without duplicates.
type Attribution struct {
	Ownership Ownership     `json:"ownership"`
	Owners    []WorkloadRef `json:"owners"`
}

// NamespaceAttribution is the attribution of every ConfigMap, Secret, PVC, Service and Ingress.
type NamespaceAttribution struct {
	ConfigMaps             map[string]Attribution `json:"configMaps"`
	Secrets                map[string]Attribution `json:"secrets"`
	PersistentVolumeClaims map[string]Attribution `json:"persistentVolumeClaims"`
	Services               map[string]Attribution `json:"services"`
	Ingresses              map[string]Attribution `json:"ingresses"`
	Workloads              []WorkloadRef          `json:"workloads"`
}

// attribute turns a list of owners into an Attribution.
func attribute(owners []WorkloadRef) Attribution {
	sortRefs(owners)
	out := []WorkloadRef{}
	for i, o := range owners {
		if i == 0 || o != owners[i-1] {
			out = append(out, o)
		}
	}
	a := Attribution{Owners: out}
	switch len(out) {
	case 0:
		a.Ownership = Orphan
	case 1:
		a.Ownership = Owned
	default:
		a.Ownership = Shared
	}
	return a
}

func sortRefs(rs []WorkloadRef) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Kind != rs[j].Kind {
			return rs[i].Kind < rs[j].Kind
		}
		return rs[i].Name < rs[j].Name
	})
}

// IsMachinerySecret reports whether a Secret holds cluster or tool state rather than a
// person's configuration: a service account token or a Helm release record. Only its type
// is read.
func IsMachinerySecret(o Object) bool {
	return o.Type == serviceAccountTokenType || o.Type == helmReleaseSecretType
}

// IsWorkload reports whether o is one of the five pod-running kinds.
func IsWorkload(o Object) bool { return workloadKinds[o.Kind] }

// Workloads returns the workloads among objs, sorted by kind then name. A Job controlled by a
// CronJob is one of its runs, and a Job controlled by a Zeabur Builder BuildRun is a build;
// neither is a workload of its own.
func Workloads(objs []Object) []Object {
	var out []Object
	for _, o := range objs {
		if !IsWorkload(o) {
			continue
		}
		if o.Kind == "Job" {
			if c := controllerOf(o.Metadata); c != nil && (c.Kind == "CronJob" ||
				(c.Kind == "BuildRun" && strings.HasPrefix(c.APIVersion, "builder.zeabur.com/"))) {
				continue
			}
		}
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Metadata.Name < out[j].Metadata.Name
	})
	return out
}

func refOf(w Object) WorkloadRef { return WorkloadRef{Kind: w.Kind, Name: w.Metadata.Name} }

// podTemplate returns the workload's pod template; one level deeper for a CronJob.
func podTemplate(w Object) *PodTemplate {
	if w.Kind == "CronJob" {
		if w.Spec.JobTemplate == nil {
			return nil
		}
		return &w.Spec.JobTemplate.Spec.Template
	}
	return &w.Spec.Template
}

// allContainers lists init, regular, then ephemeral containers: missing one list would leave
// its references invisible.
func allContainers(p PodSpec) []Container {
	out := make([]Container, 0, len(p.InitContainers)+len(p.Containers)+len(p.EphemeralContainers))
	out = append(out, p.InitContainers...)
	out = append(out, p.Containers...)
	return append(out, p.EphemeralContainers...)
}

// Relations returns every reference from a workload to a resource of the namespace, sorted,
// without duplicates: env valueFrom and envFrom of every container, volumes (also projected
// sources), imagePullSecrets, the PVCs a StatefulSet's claim templates created, the Services
// whose selector matches a workload's pod labels, and the Ingresses routing to those Services.
func Relations(objs []Object) []Relation {
	return relationsOf(objs, Workloads(objs))
}

func relationsOf(objs []Object, all []Object) []Relation {
	var out []Relation
	var claims []string
	for _, o := range objs {
		if o.Kind == "PersistentVolumeClaim" {
			claims = append(claims, o.Metadata.Name)
		}
	}
	sort.Strings(claims)
	for _, w := range all {
		owner := refOf(w)
		add := func(kind, name string, how RelationKind) {
			if name != "" {
				out = append(out, Relation{Workload: owner, Object: ObjectRef{kind, name}, Kind: how})
			}
		}
		if t := podTemplate(w); t != nil {
			for _, c := range allContainers(t.Spec) {
				for _, e := range c.Env {
					vs := e.Source()
					if vs == nil {
						continue
					}
					if vs.SecretKeyRef != nil {
						add("Secret", vs.SecretKeyRef.Name, RelEnvReference)
					}
					if vs.ConfigMapKeyRef != nil {
						add("ConfigMap", vs.ConfigMapKeyRef.Name, RelEnvReference)
					}
				}
				for _, ef := range c.EnvFrom {
					if ef.SecretRef != nil {
						add("Secret", ef.SecretRef.Name, RelEnvImport)
					}
					if ef.ConfigMapRef != nil {
						add("ConfigMap", ef.ConfigMapRef.Name, RelEnvImport)
					}
				}
			}
			for _, v := range t.Spec.Volumes {
				if v.Secret != nil {
					add("Secret", v.Secret.SecretName, RelVolume)
				}
				if v.ConfigMap != nil {
					add("ConfigMap", v.ConfigMap.Name, RelVolume)
				}
				if v.PersistentVolumeClaim != nil {
					add("PersistentVolumeClaim", v.PersistentVolumeClaim.ClaimName, RelVolume)
				}
				if v.Projected != nil {
					for _, s := range v.Projected.Sources {
						if s.Secret != nil {
							add("Secret", s.Secret.Name, RelVolume)
						}
						if s.ConfigMap != nil {
							add("ConfigMap", s.ConfigMap.Name, RelVolume)
						}
					}
				}
			}
			for _, s := range t.Spec.ImagePullSecrets {
				add("Secret", s.Name, RelImagePullSecret)
			}
		}
		if w.Kind == "StatefulSet" {
			for _, tmpl := range w.Spec.VolumeClaimTemplates {
				prefix := tmpl.Metadata.Name + "-" + w.Metadata.Name + "-"
				i := sort.SearchStrings(claims, prefix)
				for ; i < len(claims) && strings.HasPrefix(claims[i], prefix); i++ {
					if CreatedByClaimTemplate(claims[i], tmpl.Metadata.Name, w.Metadata.Name) {
						add("PersistentVolumeClaim", claims[i], RelClaimTemplate)
					}
				}
			}
		}
	}

	// Services select workloads by pod-template labels; a Service without a selector selects
	// nothing (its endpoints are managed by hand).
	selected := map[string][]WorkloadRef{}
	for _, svc := range objs {
		if svc.Kind != "Service" {
			continue
		}
		sel := labelSelector(svc.Spec.Selector)
		if len(sel) == 0 {
			continue
		}
		for _, w := range all {
			t := podTemplate(w)
			if t == nil {
				continue
			}
			if matchesSelector(sel, t.Metadata.Labels) {
				out = append(out, Relation{Workload: refOf(w), Object: ObjectRef{"Service", svc.Metadata.Name}, Kind: RelSelector})
				selected[svc.Metadata.Name] = append(selected[svc.Metadata.Name], refOf(w))
			}
		}
	}
	for _, ing := range objs {
		if ing.Kind != "Ingress" {
			continue
		}
		for _, be := range IngressBackends(ing) {
			for _, w := range selected[be] {
				out = append(out, Relation{Workload: w, Object: ObjectRef{"Ingress", ing.Metadata.Name}, Kind: RelRoute})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return relLess(out[i], out[j]) })
	dedup := out[:0]
	for i, r := range out {
		if i == 0 || r != out[i-1] {
			dedup = append(dedup, r)
		}
	}
	return dedup
}

func relLess(a, b Relation) bool {
	switch {
	case a.Workload != b.Workload:
		if a.Workload.Kind != b.Workload.Kind {
			return a.Workload.Kind < b.Workload.Kind
		}
		return a.Workload.Name < b.Workload.Name
	case a.Object != b.Object:
		if a.Object.Kind != b.Object.Kind {
			return a.Object.Kind < b.Object.Kind
		}
		return a.Object.Name < b.Object.Name
	}
	return a.Kind < b.Kind
}

func matchesSelector(sel, labels map[string]string) bool {
	if len(sel) == 0 {
		return false
	}
	for k, v := range sel {
		if lv, ok := labels[k]; !ok || lv != v {
			return false
		}
	}
	return true
}

// CreatedByClaimTemplate reports whether claim is named <template>-<set>-<ordinal>. The rest
// after the prefix must be the ordinal alone, or a set named "db" would claim the PVCs of one
// named "db-replica".
func CreatedByClaimTemplate(claim, template, set string) bool {
	rest, ok := strings.CutPrefix(claim, template+"-"+set+"-")
	if !ok || rest == "" {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return false
		}
	}
	return true
}

// IngressBackends returns the names of the Services an Ingress routes to (default backend
// first, then rules in order; duplicates possible).
func IngressBackends(ing Object) []string {
	var out []string
	if b := ing.Spec.DefaultBackend; b != nil && b.ServiceNameAny() != "" {
		out = append(out, b.ServiceNameAny())
	}
	for _, r := range ing.Spec.Rules {
		for _, p := range r.HTTP.Paths {
			if n := p.Backend.ServiceNameAny(); n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}

// Attribute computes the attribution of every ConfigMap (except kube-root-ca.crt), Secret
// (except machinery), PVC, Service and Ingress of the namespace.
func Attribute(objs []Object) NamespaceAttribution {
	all := Workloads(objs)
	return attributionOf(objs, all, relationsOf(objs, all))
}

func attributionOf(objs []Object, all []Object, rels []Relation) NamespaceAttribution {
	owners := map[ObjectRef][]WorkloadRef{}
	for _, r := range rels {
		owners[r.Object] = append(owners[r.Object], r.Workload)
	}
	na := NamespaceAttribution{
		ConfigMaps: map[string]Attribution{}, Secrets: map[string]Attribution{},
		PersistentVolumeClaims: map[string]Attribution{}, Services: map[string]Attribution{},
		Ingresses: map[string]Attribution{}, Workloads: []WorkloadRef{},
	}
	for _, o := range objs {
		n := o.Metadata.Name
		at := func() Attribution {
			return attribute(append([]WorkloadRef(nil), owners[ObjectRef{o.Kind, n}]...))
		}
		switch o.Kind {
		case "ConfigMap":
			if n != rootCAConfigMap {
				na.ConfigMaps[n] = at()
			}
		case "Secret":
			if !IsMachinerySecret(o) {
				na.Secrets[n] = at()
			}
		case "PersistentVolumeClaim":
			na.PersistentVolumeClaims[n] = at()
		case "Service":
			na.Services[n] = at()
		case "Ingress":
			na.Ingresses[n] = at()
		}
	}
	for _, w := range all {
		na.Workloads = append(na.Workloads, refOf(w))
	}
	return na
}
