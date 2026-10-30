package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

const nodeCols = `id, name, role, status, public_ipv4, public_ipv6, tailnet_host, provider, region, os, arch,
	cpu_cores, mem_mb, disk_gb, agent_version, last_seen_at, created_at`

func scanNode(row interface{ Scan(...any) error }) (*api.Server, error) {
	var n api.Server
	var last, c string
	err := row.Scan(&n.ID, &n.Name, &n.Role, &n.Status, &n.PublicIPv4, &n.PublicIPv6, &n.TailnetHost, &n.Provider, &n.Region,
		&n.OS, &n.Arch, &n.CPUCores, &n.MemMB, &n.DiskGB, &n.AgentVersion, &last, &c)
	if err != nil {
		return nil, mapErr(err)
	}
	n.LastSeenAt = parseTS(last)
	n.CreatedAt = parseTS(c)
	return &n, nil
}

// CreateNode inserts a node.
func (s *Store) CreateNode(ctx context.Context, n api.Server) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO nodes(`+nodeCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Name, n.Role, n.Status, n.PublicIPv4, n.PublicIPv6, n.TailnetHost, n.Provider, n.Region, n.OS, n.Arch,
		n.CPUCores, n.MemMB, n.DiskGB, n.AgentVersion, TS(n.LastSeenAt), TS(n.CreatedAt))
	return mapErr(err)
}

// GetNode returns a node.
func (s *Store) GetNode(ctx context.Context, id string) (*api.Server, error) {
	return scanNode(s.DB.QueryRowContext(ctx, `SELECT `+nodeCols+` FROM nodes WHERE id=?`, id))
}

// ListNodes returns all nodes.
func (s *Store) ListNodes(ctx context.Context) ([]api.Server, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+nodeCols+` FROM nodes ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Server{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// UpdateNodeMeta updates user-editable fields.
func (s *Store) UpdateNodeMeta(ctx context.Context, id, name, provider, region string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE nodes SET name=?, provider=?, region=? WHERE id=?`, name, provider, region, id)
	return err
}

// UpdateNodeHello records facts reported by the agent.
func (s *Store) UpdateNodeHello(ctx context.Context, id string, h api.HelloPayload) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE nodes SET public_ipv4=COALESCE(NULLIF(?, ''), public_ipv4), public_ipv6=?, tailnet_host=?, os=?, arch=?,
		cpu_cores=?, mem_mb=?, disk_gb=?, agent_version=?, docker_version=?, status='online', last_seen_at=? WHERE id=?`,
		h.PublicIPv4, h.PublicIPv6, h.TailnetHost, h.OS, h.Arch, h.CPUCores, h.MemMB, h.DiskGB, h.AgentVersion, h.DockerVersion, TS(time.Now()), id)
	return err
}

// SetNodeStatus sets status and optionally bumps last_seen_at.
func (s *Store) SetNodeStatus(ctx context.Context, id, status string, seen bool) error {
	if seen {
		_, err := s.DB.ExecContext(ctx, `UPDATE nodes SET status=?, last_seen_at=? WHERE id=?`, status, TS(time.Now()), id)
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE nodes SET status=? WHERE id=?`, status, id)
	return err
}

// TouchNode bumps last_seen_at.
func (s *Store) TouchNode(ctx context.Context, id string) {
	s.DB.ExecContext(ctx, `UPDATE nodes SET last_seen_at=? WHERE id=?`, TS(time.Now()), id)
}

// DeleteNode removes a node and its tokens/state.
func (s *Store) DeleteNode(ctx context.Context, id string) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM container_states WHERE node_id=?`, id); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM node_metrics WHERE node_id=?`, id); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `DELETE FROM nodes WHERE id=?`, id)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- container states ----------

// ReplaceContainerStates stores the latest container list of a node.
func (s *Store) ReplaceContainerStates(ctx context.Context, nodeID string, cs []api.ContainerState) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM container_states WHERE node_id=?`, nodeID); err != nil {
		return err
	}
	now := TS(time.Now())
	for _, c := range cs {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO container_states(node_id, service_id, state, updated_at) VALUES(?,?,?,?)`,
			nodeID, c.ServiceID, mustJSON(c), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ContainerStates lists the latest states for a node.
func (s *Store) ContainerStates(ctx context.Context, nodeID string) ([]api.ContainerState, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT state FROM container_states WHERE node_id=? ORDER BY service_id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.ContainerState{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c api.ContainerState
		if json.Unmarshal([]byte(raw), &c) == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

// ContainerState returns one service's latest state and when it was reported.
func (s *Store) ContainerState(ctx context.Context, serviceID string) (*api.ContainerState, time.Time, error) {
	var raw, at string
	err := s.DB.QueryRowContext(ctx, `SELECT state, updated_at FROM container_states WHERE service_id=? ORDER BY updated_at DESC LIMIT 1`, serviceID).Scan(&raw, &at)
	if err != nil {
		return nil, time.Time{}, mapErr(err)
	}
	var c api.ContainerState
	err = json.Unmarshal([]byte(raw), &c)
	return &c, parseTS(at), err
}
