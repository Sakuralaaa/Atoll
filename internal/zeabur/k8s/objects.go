// Package k8s holds the pure (no I/O) parsing of a Zeabur-managed k3s namespace:
// `kubectl get deploy,sts,svc,pvc,cm,ingress,pods -o yaml` → []api.ZeaburService, plus the
// variable helpers (expansion, `.zeabur.internal` rewrite, Zeabur built-in translation,
// secret masking). It is shared by the CLI importer and can be imported by atoll-agent
// (in-place takeover scan) and atoll-server (plan building).
//
// Object model (https://zeabur.com/blogs/migrate-from-zeabur.md, "Platform model"):
// namespace environment-<envID>; Deployment service-<serviceID> (older: StatefulSet or
// deployment-<id>); Service service-<id> labelled app.kubernetes.io/name=<dnsName>; env
// inline and fully expanded; one ConfigMap per config file mounted with subPath; PVC
// <volumeID>-service-<serviceID> on local-path under /var/lib/rancher/k3s/storage/;
// Ingress domain-<domainID>; public TCP/UDP via NodePort.
package k8s

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// Object is a union of the fields atoll reads from the kinds it cares about.
type Object struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   Meta              `yaml:"metadata"`
	Spec       Spec              `yaml:"spec"`
	Status     Status            `yaml:"status"`
	Data       map[string]string `yaml:"data"` // ConfigMap only; always cleared for Secrets
	// Type is a Secret's type (Opaque, kubernetes.io/service-account-token, helm.sh/release.v1, …).
	Type  string   `yaml:"type"`
	Items []Object `yaml:"items"`
}

type Meta struct {
	Name            string            `yaml:"name"`
	Namespace       string            `yaml:"namespace"`
	Labels          map[string]string `yaml:"labels"`
	Annotations     map[string]string `yaml:"annotations"`
	Generation      int64             `yaml:"generation"`
	OwnerReferences []OwnerReference  `yaml:"ownerReferences"`
}

type OwnerReference struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Name       string `yaml:"name"`
	Controller bool   `yaml:"controller"`
}

// controllerOf returns the owner reference marked controller: true, if any.
func controllerOf(m Meta) *OwnerReference {
	for i := range m.OwnerReferences {
		if m.OwnerReferences[i].Controller {
			return &m.OwnerReferences[i]
		}
	}
	return nil
}

type Spec struct {
	// Deployment / StatefulSet
	Replicas *int        `yaml:"replicas"`
	Template PodTemplate `yaml:"template"`
	Paused   bool        `yaml:"paused"`
	// StatefulSet
	VolumeClaimTemplates []ClaimTemplate `yaml:"volumeClaimTemplates"`
	// CronJob: spec.jobTemplate.spec.template
	JobTemplate *struct {
		Spec struct {
			Template PodTemplate `yaml:"template"`
		} `yaml:"spec"`
	} `yaml:"jobTemplate"`
	Suspend     *bool `yaml:"suspend"`     // Job, CronJob
	Completions *int  `yaml:"completions"` // Job
	// Pod
	PodSpec `yaml:",inline"`
	// Service
	Type     string        `yaml:"type"`
	Selector yaml.Node     `yaml:"selector"`
	Ports    []ServicePort `yaml:"ports"`
	// PVC
	VolumeName string       `yaml:"volumeName"`
	Resources  ResourceReqs `yaml:"resources"`
	// Ingress
	DefaultBackend *IngressBackend `yaml:"defaultBackend"`
	Rules          []IngressRule   `yaml:"rules"`
	TLS            []struct {
		Hosts []string `yaml:"hosts"`
	} `yaml:"tls"`
}

type PodTemplate struct {
	Metadata Meta    `yaml:"metadata"`
	Spec     PodSpec `yaml:"spec"`
}

type PodSpec struct {
	InitContainers      []Container `yaml:"initContainers"`
	Containers          []Container `yaml:"containers"`
	EphemeralContainers []Container `yaml:"ephemeralContainers"`
	Volumes             []PodVolume `yaml:"volumes"`
	ImagePullSecrets    []struct {
		Name string `yaml:"name"`
	} `yaml:"imagePullSecrets"`
}

// ClaimTemplate is one StatefulSet volumeClaimTemplate.
type ClaimTemplate struct {
	Metadata Meta `yaml:"metadata"`
	Spec     struct {
		Resources ResourceReqs `yaml:"resources"`
	} `yaml:"spec"`
}

type Container struct {
	Name         string        `yaml:"name"`
	Image        string        `yaml:"image"`
	Command      []string      `yaml:"command"`
	Args         []string      `yaml:"args"`
	Env          []EnvEntry    `yaml:"env"`
	EnvFrom      []EnvFrom     `yaml:"envFrom"`
	Ports        []ContPort    `yaml:"ports"`
	VolumeMounts []VolumeMount `yaml:"volumeMounts"`
	StartupProbe *Probe        `yaml:"startupProbe"`
	ReadyProbe   *Probe        `yaml:"readinessProbe"`
	Resources    ResourceReqs  `yaml:"resources"`
}

type Probe struct {
	HTTPGet *struct {
		Path string `yaml:"path"`
	} `yaml:"httpGet"`
	Exec *struct {
		Command []string `yaml:"command"`
	} `yaml:"exec"`
	PeriodSeconds int `yaml:"periodSeconds"`
}

type EnvEntry struct {
	Name      string       `yaml:"name"`
	Value     string       `yaml:"value"`
	ValueFrom *ValueSource `yaml:"valueFrom"`
}

// KeyRef is a secretKeyRef / configMapKeyRef.
type KeyRef struct {
	Name     string `yaml:"name"`
	Key      string `yaml:"key"`
	Optional bool   `yaml:"optional"`
}

// ValueSource is the decoded valueFrom of an env entry.
type ValueSource struct {
	SecretKeyRef    *KeyRef   `yaml:"secretKeyRef"`
	ConfigMapKeyRef *KeyRef   `yaml:"configMapKeyRef"`
	FieldRef        *struct{} `yaml:"fieldRef"`
	ResourceRef     *struct{} `yaml:"resourceFieldRef"`
}

// Source returns ValueFrom; nil for a literal value.
func (e EnvEntry) Source() *ValueSource { return e.ValueFrom }

type EnvFrom struct {
	Prefix    string `yaml:"prefix"`
	SecretRef *struct {
		Name string `yaml:"name"`
	} `yaml:"secretRef"`
	ConfigMapRef *struct {
		Name string `yaml:"name"`
	} `yaml:"configMapRef"`
}

type ContPort struct {
	Name          string `yaml:"name"`
	ContainerPort int    `yaml:"containerPort"`
	Protocol      string `yaml:"protocol"`
}

type VolumeMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	SubPath   string `yaml:"subPath"`
	ReadOnly  bool   `yaml:"readOnly"`
}

type PodVolume struct {
	Name                  string `yaml:"name"`
	PersistentVolumeClaim *struct {
		ClaimName string `yaml:"claimName"`
	} `yaml:"persistentVolumeClaim"`
	ConfigMap *struct {
		Name  string `yaml:"name"`
		Items []struct {
			Key  string `yaml:"key"`
			Path string `yaml:"path"`
		} `yaml:"items"`
	} `yaml:"configMap"`
	Secret *struct {
		SecretName string `yaml:"secretName"`
	} `yaml:"secret"`
	Projected *struct {
		Sources []struct {
			Secret *struct {
				Name string `yaml:"name"`
			} `yaml:"secret"`
			ConfigMap *struct {
				Name string `yaml:"name"`
			} `yaml:"configMap"`
		} `yaml:"sources"`
	} `yaml:"projected"`
	EmptyDir    *struct{} `yaml:"emptyDir"`
	HostPath    *struct{} `yaml:"hostPath"`
	Ephemeral   *struct{} `yaml:"ephemeral"`
	DownwardAPI *struct{} `yaml:"downwardAPI"`
}

type ServicePort struct {
	Name       string    `yaml:"name"`
	Port       int       `yaml:"port"`
	TargetPort yaml.Node `yaml:"targetPort"`
	NodePort   int       `yaml:"nodePort"`
	Protocol   string    `yaml:"protocol"`
}

type ResourceReqs struct {
	Requests map[string]string `yaml:"requests"`
	Limits   map[string]string `yaml:"limits"`
}

type IngressRule struct {
	Host string `yaml:"host"`
	HTTP struct {
		Paths []struct {
			Path    string         `yaml:"path"`
			Backend IngressBackend `yaml:"backend"`
		} `yaml:"paths"`
	} `yaml:"http"`
}

type IngressBackend struct {
	Service struct {
		Name string `yaml:"name"`
		Port struct {
			Number int    `yaml:"number"`
			Name   string `yaml:"name"`
		} `yaml:"port"`
	} `yaml:"service"`
	ServiceName string    `yaml:"serviceName"` // networking/v1beta1
	ServicePort yaml.Node `yaml:"servicePort"`
}

// ServiceNameAny is the backend Service of either Ingress API version.
func (b IngressBackend) ServiceNameAny() string {
	if b.Service.Name != "" {
		return b.Service.Name
	}
	return b.ServiceName
}

type Status struct {
	Capacity          map[string]string `yaml:"capacity"` // PVC
	Phase             string            `yaml:"phase"`
	Reason            string            `yaml:"reason"` // Pod
	ContainerStatuses []ContainerStatus `yaml:"containerStatuses"`
	ReadyReplicas     int               `yaml:"readyReplicas"`
	// Deployment / StatefulSet rollout
	ObservedGeneration int64       `yaml:"observedGeneration"`
	UpdatedReplicas    int         `yaml:"updatedReplicas"`
	Conditions         []Condition `yaml:"conditions"`
	// Job
	Active    int `yaml:"active"`
	Succeeded int `yaml:"succeeded"`
	Failed    int `yaml:"failed"`
}

type ContainerStatus struct {
	Name         string `yaml:"name"`
	Image        string `yaml:"image"`
	ImageID      string `yaml:"imageID"`
	Ready        bool   `yaml:"ready"`
	RestartCount int    `yaml:"restartCount"`
	State        struct {
		Waiting *struct {
			Reason string `yaml:"reason"`
		} `yaml:"waiting"`
		Terminated *struct {
			Reason   string `yaml:"reason"`
			ExitCode int    `yaml:"exitCode"`
		} `yaml:"terminated"`
	} `yaml:"state"`
}

type Condition struct {
	Type   string `yaml:"type"`
	Status string `yaml:"status"`
	Reason string `yaml:"reason"`
}

// Parse reads kubectl YAML or JSON output: a single object, a List, or several
// `---`-separated documents. Lists are flattened.
func Parse(data []byte) ([]Object, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var out []Object
	for {
		var o Object
		err := dec.Decode(&o)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse kubernetes yaml: %w", err)
		}
		out = append(out, flatten(o)...)
	}
	return out, nil
}

func flatten(o Object) []Object {
	if len(o.Items) > 0 || o.Kind == "List" || (len(o.Kind) > 4 && o.Kind[len(o.Kind)-4:] == "List") {
		var out []Object
		for _, it := range o.Items {
			out = append(out, flatten(it)...)
		}
		return out
	}
	if o.Kind == "" && o.Metadata.Name == "" {
		return nil
	}
	if o.Kind == "Secret" {
		// Secret values never enter atoll: only name and type are kept.
		o.Data = nil
	}
	return []Object{o}
}

// labelSelector decodes a selector node: a Service's plain map, or a workload's
// {matchLabels: …} (matchExpressions are ignored).
func labelSelector(n yaml.Node) map[string]string {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	var ml struct {
		MatchLabels map[string]string `yaml:"matchLabels"`
	}
	if n.Decode(&ml) == nil && ml.MatchLabels != nil {
		return ml.MatchLabels
	}
	var m map[string]string
	if n.Decode(&m) != nil {
		return nil
	}
	return m
}

// targetPortInt returns the numeric targetPort, or 0 when it is a name.
func targetPortInt(n yaml.Node) int {
	var i int
	if n.Kind == yaml.ScalarNode && n.Decode(&i) == nil {
		return i
	}
	return 0
}

func nodeString(n yaml.Node) string {
	var s string
	if n.Kind == yaml.ScalarNode && n.Decode(&s) == nil {
		return s
	}
	return ""
}
