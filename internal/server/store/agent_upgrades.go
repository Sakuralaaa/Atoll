package store

import (
	"context"
	"database/sql"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// SaveAgentUpgrade stores the last panel-started agent upgrade of a node.
func (s *Store) SaveAgentUpgrade(ctx context.Context, nodeID string, u api.AgentUpgrade) error {
	var fin any
	if !u.FinishedAt.IsZero() {
		fin = TS(u.FinishedAt)
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO agent_upgrades(node_id, state, from_ver, to_ver, error, rolled_back, started_at, finished_at)
		VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET state=excluded.state, from_ver=excluded.from_ver, to_ver=excluded.to_ver,
		error=excluded.error, rolled_back=excluded.rolled_back, started_at=excluded.started_at, finished_at=excluded.finished_at`,
		nodeID, u.State, u.From, u.To, u.Error, u.RolledBack, TS(u.StartedAt), fin)
	return err
}

// ListAgentUpgrades returns the stored upgrade per node id.
func (s *Store) ListAgentUpgrades(ctx context.Context) (map[string]api.AgentUpgrade, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT node_id, state, from_ver, to_ver, error, rolled_back, started_at, finished_at FROM agent_upgrades`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]api.AgentUpgrade{}
	for rows.Next() {
		var id, started string
		var fin sql.NullString
		var u api.AgentUpgrade
		if err := rows.Scan(&id, &u.State, &u.From, &u.To, &u.Error, &u.RolledBack, &started, &fin); err != nil {
			return nil, err
		}
		u.StartedAt = parseTS(started)
		if fin.Valid {
			u.FinishedAt = parseTS(fin.String)
		}
		out[id] = u
	}
	return out, rows.Err()
}
