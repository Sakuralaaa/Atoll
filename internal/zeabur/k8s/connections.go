// Adapted from zeabur-oss/zeabur (Apache-2.0), crates/zeabur-graph/src/connections.rs
// (shared Secret/ConfigMap with a data store; env values naming a Service by short name or
// cluster DNS). Copyright Zeabur contributors; modified for atoll (key-level references so the
// plan can write ${SERVICE.KEY}; .zeabur.internal hosts). See THIRD_PARTY_NOTICES.md.

package k8s

import (
	"regexp"
	"sort"
	"strings"
)

// Kubernetes records no "calls" relationship between workloads, so a connection is read from
// two kinds of evidence:
//   - a shared Secret or ConfigMap: a service and a data store reading the same object usually
//     share the store's address or credentials, and the service is the caller (not inferred);
//   - a literal env value naming a Kubernetes Service of the namespace by its short name or its
//     cluster DNS name (redis://cache:6379, http://api.shop.svc.cluster.local:8080): text, so
//     the connection is inferred.
// Neither says traffic flows.

// Reference vias (api.ZeaburReference.Via).
const (
	ViaEnvValue        = "env-value"        // literal value names a k8s Service (short or cluster DNS)
	ViaZeaburInternal  = "zeabur-internal"  // literal value names <dns>.zeabur.internal
	ViaSharedSecret    = "shared-secret"    // reads the same Secret as a data store
	ViaSharedConfigMap = "shared-configmap" // reads the same ConfigMap as a data store
)

// hostTokens splits a value into runs of letters, digits, dots and hyphens.
func hostTokens(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		return !(r == '.' || r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	})
}

// envNsRe matches a Zeabur environment namespace label, used when the namespace is unknown.
var envNsRe = regexp.MustCompile(`^environment-[a-z0-9]+$`)

// splitServiceHost splits a host token into the Service name and its DNS suffix when the token
// is name, name.<ns>, name.<ns>.svc or name.<ns>.svc.cluster.local. namespace "" accepts any
// environment-<id> namespace. ok is false for any other shape (api.example.com).
func splitServiceHost(token, namespace string) (name, suffix string, ok bool) {
	head, rest, dotted := strings.Cut(token, ".")
	if !dotted {
		return head, "", true
	}
	ns, tail, _ := strings.Cut(rest, ".")
	if namespace != "" && ns != namespace || namespace == "" && !envNsRe.MatchString(ns) {
		return "", "", false
	}
	switch tail {
	case "", "svc", "svc.cluster.local":
		return head, "." + rest, true
	}
	return "", "", false
}

// MentionedServices returns the names in known that value names as a host in namespace
// (sorted): a token that is the name itself, or the name followed by .<namespace>,
// .<namespace>.svc or .<namespace>.svc.cluster.local.
func MentionedServices(value, namespace string, known map[string]bool) []string {
	seen := map[string]bool{}
	for _, tok := range hostTokens(value) {
		if name, _, ok := splitServiceHost(tok, namespace); ok && known[name] {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// configRef is one way a workload's main container reads a Secret/ConfigMap.
type configRef struct {
	Object ObjectRef
	Key    string // the object key ("" for envFrom/volume: the whole object)
	Env    string // the env var it lands in ("" for volume)
	Prefix string // envFrom prefix
	From   bool   // envFrom import
}

// configRefs lists the Secret/ConfigMap reads of a pod's regular containers.
func configRefs(w Object) []configRef {
	t := podTemplate(w)
	if t == nil {
		return nil
	}
	var out []configRef
	for _, c := range t.Spec.Containers {
		for _, e := range c.Env {
			vs := e.Source()
			if vs == nil {
				continue
			}
			if r := vs.SecretKeyRef; r != nil && r.Name != "" {
				out = append(out, configRef{Object: ObjectRef{"Secret", r.Name}, Key: r.Key, Env: e.Name})
			}
			if r := vs.ConfigMapKeyRef; r != nil && r.Name != "" {
				out = append(out, configRef{Object: ObjectRef{"ConfigMap", r.Name}, Key: r.Key, Env: e.Name})
			}
		}
		for _, ef := range c.EnvFrom {
			if ef.SecretRef != nil && ef.SecretRef.Name != "" {
				out = append(out, configRef{Object: ObjectRef{"Secret", ef.SecretRef.Name}, Prefix: ef.Prefix, From: true})
			}
			if ef.ConfigMapRef != nil && ef.ConfigMapRef.Name != "" {
				out = append(out, configRef{Object: ObjectRef{"ConfigMap", ef.ConfigMapRef.Name}, Prefix: ef.Prefix, From: true})
			}
		}
	}
	for _, v := range t.Spec.Volumes {
		if v.Secret != nil && v.Secret.SecretName != "" {
			out = append(out, configRef{Object: ObjectRef{"Secret", v.Secret.SecretName}})
		}
		if v.ConfigMap != nil && v.ConfigMap.Name != "" {
			out = append(out, configRef{Object: ObjectRef{"ConfigMap", v.ConfigMap.Name}})
		}
	}
	return out
}

// targetKeyFor finds the store's env var holding object/key: an env valueFrom of the same
// key, or an envFrom import of the object (prefix + key).
func targetKeyFor(store []configRef, obj ObjectRef, key string) string {
	if key == "" {
		return ""
	}
	for _, r := range store {
		if r.Object == obj && r.Env != "" && r.Key == key {
			return r.Env
		}
	}
	for _, r := range store {
		if r.Object == obj && r.From && envNameRe.MatchString(r.Prefix+key) {
			return r.Prefix + key
		}
	}
	return ""
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Evidence is why a Connection was drawn.
type Evidence struct {
	Basis     string     `json:"basis"` // "envValue" | "sharedObject"
	Container string     `json:"container,omitempty"`
	Variable  string     `json:"variable,omitempty"`
	Service   string     `json:"service,omitempty"` // the k8s Service named in the value
	Object    *ObjectRef `json:"object,omitempty"`  // the shared Secret/ConfigMap
}

// Connection is "From appears to call To". Inferred when every evidence is env text.
type Connection struct {
	From     WorkloadRef `json:"from"`
	To       WorkloadRef `json:"to"`
	Inferred bool        `json:"inferred"`
	Evidence []Evidence  `json:"evidence"`
}

// Connections returns the connections between the workloads of a namespace, sorted by source
// then target, each with its evidence sorted and deduplicated.
func Connections(namespace string, objs []Object) []Connection {
	all := Workloads(objs)
	rels := relationsOf(objs, all)
	selected := map[string][]WorkloadRef{}
	for _, r := range rels {
		if r.Kind == RelSelector {
			selected[r.Object.Name] = append(selected[r.Object.Name], r.Workload)
		}
	}
	known := map[string]bool{}
	for n := range selected {
		known[n] = true
	}
	type key struct{ from, to WorkloadRef }
	found := map[key][]Evidence{}
	add := func(from, to WorkloadRef, e Evidence) {
		if from != to {
			found[key{from, to}] = append(found[key{from, to}], e)
		}
	}
	for _, w := range all {
		t := podTemplate(w)
		if t == nil {
			continue
		}
		for _, c := range allContainers(t.Spec) {
			for _, e := range c.Env {
				if e.ValueFrom != nil || e.Value == "" {
					continue
				}
				for _, svc := range MentionedServices(e.Value, namespace, known) {
					for _, to := range selected[svc] {
						add(refOf(w), to, Evidence{Basis: "envValue", Container: c.Name, Variable: e.Name, Service: svc})
					}
				}
			}
		}
	}
	type store struct {
		ref  WorkloadRef
		objs map[ObjectRef]bool
	}
	objsOf := func(w Object) map[ObjectRef]bool {
		m := map[ObjectRef]bool{}
		for _, r := range configRefs(w) {
			m[r.Object] = true
		}
		return m
	}
	var stores []store
	for _, w := range all {
		if DataStoreOf(w) != "" {
			stores = append(stores, store{refOf(w), objsOf(w)})
		}
	}
	for _, w := range all {
		if DataStoreOf(w) != "" {
			continue
		}
		for o := range objsOf(w) {
			for _, s := range stores {
				if s.objs[o] {
					o := o
					add(refOf(w), s.ref, Evidence{Basis: "sharedObject", Object: &o})
				}
			}
		}
	}
	out := make([]Connection, 0, len(found))
	for k, ev := range found {
		sort.Slice(ev, func(i, j int) bool { return evidenceKey(ev[i]) < evidenceKey(ev[j]) })
		var dedup []Evidence
		inferred := true
		for i, e := range ev {
			if i > 0 && evidenceKey(e) == evidenceKey(ev[i-1]) {
				continue
			}
			dedup = append(dedup, e)
			inferred = inferred && e.Basis == "envValue"
		}
		out = append(out, Connection{From: k.from, To: k.to, Inferred: inferred, Evidence: dedup})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.From != b.From {
			return a.From.Kind+"/"+a.From.Name < b.From.Kind+"/"+b.From.Name
		}
		return a.To.Kind+"/"+a.To.Name < b.To.Kind+"/"+b.To.Name
	})
	return out
}

func evidenceKey(e Evidence) string {
	k := e.Basis + "\x00" + e.Container + "\x00" + e.Variable + "\x00" + e.Service
	if e.Object != nil {
		k += "\x00" + e.Object.String()
	}
	return k
}
