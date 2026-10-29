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
	Data       map[string]string `yaml:"data"` // ConfigMap
	Items      []Object          `yaml:"items"`
}

type Meta struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

type Spec struct {
	// Deployment / StatefulSet
	Replicas *int        `yaml:"replicas"`
	Template PodTemplate `yaml:"template"`
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
	Rules []IngressRule `yaml:"rules"`
	TLS   []struct {
		Hosts []string `yaml:"hosts"`
	} `yaml:"tls"`
}

type PodTemplate struct {
	Metadata Meta    `yaml:"metadata"`
	Spec     PodSpec `yaml:"spec"`
}

type PodSpec struct {
	Containers []Container `yaml:"containers"`
	Volumes    []PodVolume `yaml:"volumes"`
}

type Container struct {
	Name         string        `yaml:"name"`
	Image        string        `yaml:"image"`
	Command      []string      `yaml:"command"`
	Args         []string      `yaml:"args"`
	Env          []EnvEntry    `yaml:"env"`
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
	Name      string     `yaml:"name"`
	Value     string     `yaml:"value"`
	ValueFrom *yaml.Node `yaml:"valueFrom"`
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
			Path    string `yaml:"path"`
			Backend struct {
				Service struct {
					Name string `yaml:"name"`
					Port struct {
						Number int    `yaml:"number"`
						Name   string `yaml:"name"`
					} `yaml:"port"`
				} `yaml:"service"`
				ServiceName string    `yaml:"serviceName"` // networking/v1beta1
				ServicePort yaml.Node `yaml:"servicePort"`
			} `yaml:"backend"`
		} `yaml:"paths"`
	} `yaml:"http"`
}

type Status struct {
	Capacity          map[string]string `yaml:"capacity"` // PVC
	Phase             string            `yaml:"phase"`
	ContainerStatuses []struct {
		Name    string `yaml:"name"`
		Image   string `yaml:"image"`
		ImageID string `yaml:"imageID"`
		Ready   bool   `yaml:"ready"`
	} `yaml:"containerStatuses"`
	ReadyReplicas int `yaml:"readyReplicas"`
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
	return []Object{o}
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
