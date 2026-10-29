package zeabur

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The Zeabur CLI's --json output mirrors its GraphQL models (keys like "_id", "name",
// "region": {"id": "server-..."}); field casing varies between versions. Decoding is
// therefore tolerant: keys are matched case-insensitively and a leading "_" is ignored.

// Project is a Zeabur project from `project list`.
type Project struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Region struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"region"`
}

// DedicatedServerID returns the server id when the project runs on a dedicated server
// (Region.ID = "server-<serverID>"), else "" (shared cluster).
func (p Project) DedicatedServerID() string {
	if strings.HasPrefix(p.Region.ID, "server-") {
		return strings.TrimPrefix(p.Region.ID, "server-")
	}
	return ""
}

// Server is a dedicated server from `server get`.
type Server struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IP        string `json:"ip"`
	IsManaged bool   `json:"isManaged"`
	Provider  string `json:"provider"`
}

// Service is a Zeabur service from `service list` / `service get`.
type Service struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Template string `json:"template"` // GIT | PREBUILT | ...
	Status   string `json:"status"`
	Spec     struct {
		Source struct {
			Image string `json:"image"`
		} `json:"source"`
	} `json:"spec"`
	GitTrigger *struct {
		Branch       string `json:"branch"`
		Provider     string `json:"provider"`
		RepoID       any    `json:"repoID"`
		RepoFullName string `json:"repoFullName"`
	} `json:"gitTrigger"`
	CustomBuildCommand string `json:"customBuildCommand"`
	CustomStartCommand string `json:"customStartCommand"`
	RootDirectory      string `json:"rootDirectory"`
}

// Domain is one entry of `domain list`.
type Domain struct {
	Domain      string `json:"domain"`
	IsGenerated bool   `json:"isGenerated"`
	PortName    string `json:"portName"`
}

// Variables is `variable list` output: own variables and those received from other services.
type Variables struct {
	Own      map[string]string
	Readonly map[string]string
}

// normalize rewrites object keys: strips a leading "_" ("_id" → "id").
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[strings.TrimPrefix(k, "_")] = normalize(val)
		}
		return out
	case []any:
		for i := range t {
			t[i] = normalize(t[i])
		}
		return t
	}
	return v
}

// jsonPayload returns the JSON part of CLI output (log lines before it are dropped).
func jsonPayload(out []byte) []byte {
	s := bytes.TrimSpace(out)
	for i, b := range s {
		if b == '{' || b == '[' {
			return s[i:]
		}
	}
	return s
}

func decodeNormalized(out []byte, dst any) error {
	var raw any
	if err := json.Unmarshal(jsonPayload(out), &raw); err != nil {
		return fmt.Errorf("parse zeabur json: %w", err)
	}
	b, _ := json.Marshal(normalize(raw))
	return json.Unmarshal(b, dst)
}

// decodeList accepts an array or an object wrapping one array (e.g. {"projects":[...]}).
func decodeList[T any](out []byte) ([]T, error) {
	var raw any
	if err := json.Unmarshal(jsonPayload(out), &raw); err != nil {
		return nil, fmt.Errorf("parse zeabur json: %w", err)
	}
	raw = normalize(raw)
	if m, ok := raw.(map[string]any); ok {
		for _, k := range sortedKeys(m) {
			if arr, ok := m[k].([]any); ok {
				raw = arr
				break
			}
		}
		if _, still := raw.(map[string]any); still {
			raw = []any{m}
		}
	}
	b, _ := json.Marshal(raw)
	var list []T
	return list, json.Unmarshal(b, &list)
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// toStringMap accepts {"K":"v"}, {"K":{"value":"v"}} or [{"key":"K","value":"v"}].
func toStringMap(v any) map[string]string {
	out := map[string]string{}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			switch vv := val.(type) {
			case string:
				out[k] = vv
			case map[string]any:
				if s, ok := vv["value"].(string); ok {
					out[k] = s
				} else if s, ok := vv["default"].(string); ok {
					out[k] = s
				}
			case nil:
				out[k] = ""
			default:
				out[k] = fmt.Sprint(vv)
			}
		}
	case []any:
		for _, it := range t {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			k := firstString(m, "key", "name", "Key", "Name")
			if k == "" {
				continue
			}
			out[k] = firstString(m, "value", "Value")
		}
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok {
			return s
		}
	}
	return ""
}

// ParseVariables parses `--json variable list --id ...`.
func ParseVariables(out []byte) (Variables, error) {
	var raw any
	if err := json.Unmarshal(jsonPayload(out), &raw); err != nil {
		return Variables{}, fmt.Errorf("parse zeabur variables: %w", err)
	}
	v := Variables{Own: map[string]string{}, Readonly: map[string]string{}}
	m, ok := normalize(raw).(map[string]any)
	if !ok {
		v.Own = toStringMap(raw)
		return v, nil
	}
	found := false
	for k, val := range m {
		switch strings.ToLower(k) {
		case "variables", "vars", "env":
			v.Own, found = toStringMap(val), true
		case "readonlyvariables", "readonly_variables", "readonlyvars":
			v.Readonly, found = toStringMap(val), true
		}
	}
	if !found {
		v.Own = toStringMap(m)
	}
	return v, nil
}

func ParseProjects(out []byte) ([]Project, error) { return decodeList[Project](out) }
func ParseServices(out []byte) ([]Service, error) { return decodeList[Service](out) }
func ParseDomains(out []byte) ([]Domain, error)   { return decodeList[Domain](out) }

func ParseServer(out []byte) (Server, error) {
	var s Server
	return s, decodeNormalized(out, &s)
}

func ParseService(out []byte) (Service, error) {
	var s Service
	return s, decodeNormalized(out, &s)
}

// ---------- project export (Zeabur template spec, YAML or JSON) ----------

// ExportSpec is the subset of `project export` atoll reads: ports, volumes, configs and which
// variables are exposed. Values are unexpanded.
type ExportSpec struct {
	Spec struct {
		Services []ExportService `yaml:"services"`
	} `yaml:"spec"`
}

type ExportService struct {
	Name     string `yaml:"name"`
	Template string `yaml:"template"`
	Spec     struct {
		Source struct {
			Image string `yaml:"image"`
		} `yaml:"source"`
		Ports []struct {
			ID   string `yaml:"id"`
			Port int    `yaml:"port"`
			Type string `yaml:"type"`
		} `yaml:"ports"`
		Volumes []struct {
			ID  string `yaml:"id"`
			Dir string `yaml:"dir"`
		} `yaml:"volumes"`
		Env map[string]struct {
			Default string `yaml:"default"`
			Expose  bool   `yaml:"expose"`
		} `yaml:"env"`
		Configs []struct {
			Path     string `yaml:"path"`
			Template string `yaml:"template"`
		} `yaml:"configs"`
	} `yaml:"spec"`
}

// ParseExport parses `project export` output (YAML or JSON).
func ParseExport(out []byte) (*ExportSpec, error) {
	var e ExportSpec
	body := out
	if i := bytes.Index(out, []byte("apiVersion")); i > 0 {
		body = out[i:]
	}
	if err := yaml.Unmarshal(body, &e); err != nil {
		return nil, fmt.Errorf("parse zeabur project export: %w", err)
	}
	return &e, nil
}

// ExposedKeys returns, per service name, the variables marked expose: true.
func (e *ExportSpec) ExposedKeys() map[string][]string {
	out := map[string][]string{}
	for _, s := range e.Spec.Services {
		for k, v := range s.Spec.Env {
			if v.Expose {
				out[s.Name] = append(out[s.Name], k)
			}
		}
		sort.Strings(out[s.Name])
	}
	return out
}
