package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// MeshService is one service with its effective node, for mesh planning.
type MeshService struct {
	ServiceID string
	ProjectID string
	Name      string
	NodeID    string
	Ports     []PortSpec
}

// PortSpec is the subset of api.Port the mesh needs.
type PortSpec struct {
	Name          string `json:"name"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
}

// MeshServices lists every service with its effective node, ordered by project and name.
func (s *Store) MeshServices(ctx context.Context) ([]MeshService, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id, s.project_id, s.name, `+EffNode+`, s.ports FROM services s
		JOIN projects p ON p.id=s.project_id ORDER BY s.project_id, s.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MeshService
	for rows.Next() {
		var m MeshService
		var ports string
		if err := rows.Scan(&m.ServiceID, &m.ProjectID, &m.Name, &m.NodeID, &ports); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(ports), &m.Ports)
		out = append(out, m)
	}
	return out, rows.Err()
}

// MeshPort returns the host port allocated for (service, port, protocol) on nodeID,
// allocating the lowest free port in [lo, hi] on that node when there is none (or when the
// service moved to another node).
func (s *Store) MeshPort(ctx context.Context, nodeID, serviceID string, port int, proto string, lo, hi int) (int, error) {
	var node string
	var hp int
	err := s.DB.QueryRowContext(ctx, `SELECT node_id, host_port FROM mesh_ports WHERE service_id=? AND port=? AND protocol=?`,
		serviceID, port, proto).Scan(&node, &hp)
	if err == nil && node == nodeID {
		return hp, nil
	}
	if err == nil {
		s.DB.ExecContext(ctx, `DELETE FROM mesh_ports WHERE service_id=? AND port=? AND protocol=?`, serviceID, port, proto)
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT host_port FROM mesh_ports WHERE node_id=? AND protocol=?`, nodeID, proto)
	if err != nil {
		return 0, err
	}
	used := map[int]bool{}
	for rows.Next() {
		var p int
		if rows.Scan(&p) == nil {
			used[p] = true
		}
	}
	rows.Close()
	for p := lo; p <= hi; p++ {
		if used[p] {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO mesh_ports(service_id, port, protocol, node_id, host_port) VALUES(?,?,?,?,?)`,
			serviceID, port, proto, nodeID, p); err != nil {
			return 0, err
		}
		return p, nil
	}
	return 0, fmt.Errorf("no free mesh port in %d-%d on node %s", lo, hi, nodeID)
}

// PruneMeshPorts drops allocations whose (service, port, protocol) is not in keep.
func (s *Store) PruneMeshPorts(ctx context.Context, keep map[string]bool) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT service_id, port, protocol FROM mesh_ports`)
	if err != nil {
		return err
	}
	type key struct {
		sid   string
		port  int
		proto string
	}
	var drop []key
	for rows.Next() {
		var k key
		if rows.Scan(&k.sid, &k.port, &k.proto) == nil && !keep[MeshKey(k.sid, k.port, k.proto)] {
			drop = append(drop, k)
		}
	}
	rows.Close()
	for _, k := range drop {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM mesh_ports WHERE service_id=? AND port=? AND protocol=?`, k.sid, k.port, k.proto); err != nil {
			return err
		}
	}
	return nil
}

// MeshKey identifies one exported port.
func MeshKey(serviceID string, port int, proto string) string {
	return fmt.Sprintf("%s/%d/%s", serviceID, port, proto)
}

// JoinTokenValid reports whether a join token exists, is unused and unexpired (not consumed).
func (s *Store) JoinTokenValid(ctx context.Context, hash string) bool {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM join_tokens WHERE token_hash=? AND used_at IS NULL AND expires_at > ?`,
		hash, TS(time.Now())).Scan(&n)
	return err == nil && n > 0
}
