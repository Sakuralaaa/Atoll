package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/atoll-sh/atoll/internal/api"
)

// StateStore persists every ResolvedSpec the server sends to <data>/state/<service_id>.json
// so the agent can rebuild the node without the server.
type StateStore struct{ paths Paths }

func NewStateStore(p Paths) *StateStore { return &StateStore{paths: p} }

// validID guards file paths built from server-provided IDs.
func validID(id string) error {
	if id == "" || len(id) > 64 || strings.ContainsAny(id, "/\\.\x00 ") {
		return fmt.Errorf("invalid id %q", id)
	}
	return nil
}

func (s *StateStore) Save(spec api.ResolvedSpec) error {
	if err := validID(spec.ServiceID); err != nil {
		return err
	}
	b, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	// 0600: specs contain env secrets and registry credentials.
	return writeFileAtomic(s.paths.StateFile(spec.ServiceID), b, 0o600)
}

func (s *StateStore) Load(serviceID string) (api.ResolvedSpec, error) {
	var spec api.ResolvedSpec
	if err := validID(serviceID); err != nil {
		return spec, err
	}
	b, err := os.ReadFile(s.paths.StateFile(serviceID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return spec, ErrNotFound
		}
		return spec, err
	}
	err = json.Unmarshal(b, &spec)
	return spec, err
}

func (s *StateStore) Delete(serviceID string) error {
	if err := validID(serviceID); err != nil {
		return err
	}
	err := os.Remove(s.paths.StateFile(serviceID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// List returns all persisted specs sorted by service ID. Corrupt files are skipped and reported.
func (s *StateStore) List() ([]api.ResolvedSpec, []error) {
	entries, err := os.ReadDir(s.paths.StateDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{err}
	}
	var out []api.ResolvedSpec
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.paths.StateDir(), name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var spec api.ResolvedSpec
		if err := json.Unmarshal(b, &spec); err != nil || spec.ServiceID == "" {
			errs = append(errs, fmt.Errorf("state %s: corrupt: %v", name, err))
			continue
		}
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServiceID < out[j].ServiceID })
	return out, errs
}
