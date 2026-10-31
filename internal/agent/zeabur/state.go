package zeabur

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is /var/lib/atoll/zeabur-takeover.json: everything changed on Zeabur's k3s, so it can
// be reversed. It is written before each change is made.
type State struct {
	// Workloads by "<namespace>/<kind>/<name>": original replica count before atoll scaled it.
	Workloads map[string]WorkloadRecord `json:"workloads"`
	// Parked ingress DaemonSets with their original nodeSelector.
	ParkedDaemonSets []ParkedDaemonSet `json:"parked_daemonsets,omitempty"`
	// Node labels set to stop k3s ServiceLB (svclb) pods, with the previous value ("" = absent).
	NodeLabels    []NodeLabelRecord `json:"node_labels,omitempty"`
	IngressParked bool              `json:"ingress_parked"`
	// ParkedOperators are controller-manager Deployments ("<ns>/deployment/<name>") scaled to
	// 0 so they do not revert the parked ingress; their replicas are in Workloads.
	ParkedOperators []string `json:"parked_operators,omitempty"`
	// CutoverAt is set when the Zeabur ingress was parked (the last cutover step on this host).
	CutoverAt    *time.Time `json:"cutover_at,omitempty"`
	K3sRemoved   bool       `json:"k3s_removed"`
	K3sRemovedAt *time.Time `json:"k3s_removed_at,omitempty"`
	BackupDir    string     `json:"backup_dir,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type WorkloadRecord struct {
	Namespace        string    `json:"namespace"`
	Workload         string    `json:"workload"` // deployment/service-<id>
	OriginalReplicas int       `json:"original_replicas"`
	CurrentReplicas  int       `json:"current_replicas"`
	ChangedAt        time.Time `json:"changed_at"`
}

type ParkedDaemonSet struct {
	Namespace            string            `json:"namespace"`
	Name                 string            `json:"name"`
	OriginalNodeSelector map[string]string `json:"original_node_selector"`
}

type NodeLabelRecord struct {
	Node     string `json:"node"`
	Key      string `json:"key"`
	Previous string `json:"previous"` // "" = label was absent
	Had      bool   `json:"had"`
}

// StateFile guards concurrent read-modify-write of the takeover state.
type StateFile struct {
	Path string
	mu   sync.Mutex
}

func (s *StateFile) Load() (State, error) {
	var st State
	b, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		st.Workloads = map[string]WorkloadRecord{}
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	if st.Workloads == nil {
		st.Workloads = map[string]WorkloadRecord{}
	}
	return st, nil
}

// Update loads, applies fn, and writes atomically (only if fn returns nil).
func (s *StateFile) Update(fn func(*State) error) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.Load()
	if err != nil {
		return st, err
	}
	if err := fn(&st); err != nil {
		return st, err
	}
	st.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return st, err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return st, err
	}
	tmp := s.Path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return st, err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return st, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return st, err
	}
	f.Close()
	return st, os.Rename(tmp, s.Path)
}
