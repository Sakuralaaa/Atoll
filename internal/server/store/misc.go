package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ---------- confirmations ----------

// CreateConfirmation stores a pending confirmation with its action payload.
func (s *Store) CreateConfirmation(ctx context.Context, c api.Confirmation, payload any) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO confirmations(id, action, target, summary, payload, status, created_by, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		c.ID, c.Action, c.Target, c.Summary, mustJSON(payload), c.Status, c.CreatedBy, TS(c.CreatedAt))
	return err
}

// GetConfirmation returns a confirmation and its raw payload.
func (s *Store) GetConfirmation(ctx context.Context, id string) (*api.Confirmation, json.RawMessage, error) {
	var c api.Confirmation
	var payload, at string
	err := s.DB.QueryRowContext(ctx, `SELECT id, action, target, summary, payload, status, created_by, created_at FROM confirmations WHERE id=?`, id).
		Scan(&c.ID, &c.Action, &c.Target, &c.Summary, &payload, &c.Status, &c.CreatedBy, &at)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	c.CreatedAt = parseTS(at)
	return &c, json.RawMessage(payload), nil
}

// ListConfirmations lists confirmations, filtered by status when non-empty.
func (s *Store) ListConfirmations(ctx context.Context, status string) ([]api.Confirmation, error) {
	q := `SELECT id, action, target, summary, status, created_by, created_at FROM confirmations`
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY created_at DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Confirmation{}
	for rows.Next() {
		var c api.Confirmation
		var at string
		if err := rows.Scan(&c.ID, &c.Action, &c.Target, &c.Summary, &c.Status, &c.CreatedBy, &at); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTS(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// DecideConfirmation moves a pending confirmation to status; false if it was not pending.
func (s *Store) DecideConfirmation(ctx context.Context, id, status, by string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE confirmations SET status=?, decided_at=?, decided_by=? WHERE id=? AND status='pending'`,
		status, TS(time.Now()), by, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ExpireConfirmations marks pending confirmations older than ttl as expired.
func (s *Store) ExpireConfirmations(ctx context.Context, ttl time.Duration) {
	s.DB.ExecContext(ctx, `UPDATE confirmations SET status='expired' WHERE status='pending' AND created_at < ?`, TS(time.Now().Add(-ttl)))
}

// ---------- settings ----------

var secretSettings = map[string]bool{"cloudflare_token": true, "origin_cert_pem": true, "origin_key_pem": true,
	"backup_access_key": true, "backup_secret_key": true, "backup_repo_password": true,
	"tailscale_auth_key": true, "control_backup_passphrase": true}

// GetSetting returns a setting ("" if unset), decrypting secret keys.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if secretSettings[key] {
		return s.Box.Decrypt(v)
	}
	return v, nil
}

// SetSetting writes a setting, encrypting secret keys.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if secretSettings[key] && value != "" {
		var err error
		if value, err = s.Box.Encrypt(value); err != nil {
			return err
		}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// SetSecretBlob stores an arbitrary encrypted JSON value under key.
func (s *Store) SetSecretBlob(ctx context.Context, key string, v any) error {
	enc, err := s.encryptJSON(v)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, enc)
	return err
}

// GetSecretBlob reads a value stored with SetSecretBlob.
func (s *Store) GetSecretBlob(ctx context.Context, key string, v any) error {
	var enc string
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&enc); err != nil {
		return mapErr(err)
	}
	return s.decryptJSON(enc, v)
}

// ---------- metrics ----------

// AddNodeMetrics folds a sample into its minute bucket (running average).
func (s *Store) AddNodeMetrics(ctx context.Context, nodeID string, m api.NodeMetrics) error {
	at := m.At
	if at.IsZero() {
		at = time.Now()
	}
	b := at.UTC().Truncate(time.Minute).Unix()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO node_metrics(node_id, at, n, cpu_pct, mem_used_mb, mem_total_mb, disk_used_gb, disk_total_gb, net_rx_bps, net_tx_bps, load1)
		VALUES(?,?,1,?,?,?,?,?,?,?,?) ON CONFLICT(node_id, at) DO UPDATE SET
		cpu_pct=(cpu_pct*n+excluded.cpu_pct)/(n+1), mem_used_mb=(mem_used_mb*n+excluded.mem_used_mb)/(n+1),
		mem_total_mb=excluded.mem_total_mb, disk_used_gb=excluded.disk_used_gb, disk_total_gb=excluded.disk_total_gb,
		net_rx_bps=(net_rx_bps*n+excluded.net_rx_bps)/(n+1), net_tx_bps=(net_tx_bps*n+excluded.net_tx_bps)/(n+1),
		load1=(load1*n+excluded.load1)/(n+1), n=n+1`,
		nodeID, b, m.CPUPct, m.MemUsedMB, m.MemTotalMB, m.DiskUsedGB, m.DiskTotalGB, m.NetRxBps, m.NetTxBps, m.Load1)
	return err
}

// AddContainerMetrics folds a container sample into its minute bucket.
func (s *Store) AddContainerMetrics(ctx context.Context, m api.ContainerMetrics) error {
	at := m.At
	if at.IsZero() {
		at = time.Now()
	}
	b := at.UTC().Truncate(time.Minute).Unix()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO container_metrics(service_id, at, n, cpu_pct, mem_mb, net_rx_bps, net_tx_bps) VALUES(?,?,1,?,?,?,?)
		ON CONFLICT(service_id, at) DO UPDATE SET cpu_pct=(cpu_pct*n+excluded.cpu_pct)/(n+1), mem_mb=(mem_mb*n+excluded.mem_mb)/(n+1),
		net_rx_bps=(net_rx_bps*n+excluded.net_rx_bps)/(n+1), net_tx_bps=(net_tx_bps*n+excluded.net_tx_bps)/(n+1), n=n+1`,
		m.ServiceID, b, m.CPUPct, m.MemMB, m.NetRxBps, m.NetTxBps)
	return err
}

// NodeMetricsRange returns samples since `since`, averaged into buckets of `step` seconds.
func (s *Store) NodeMetricsRange(ctx context.Context, nodeID string, since time.Time, step int64) ([]api.NodeMetrics, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT (at/?)*?, AVG(cpu_pct), AVG(mem_used_mb), MAX(mem_total_mb), MAX(disk_used_gb), MAX(disk_total_gb),
		AVG(net_rx_bps), AVG(net_tx_bps), AVG(load1) FROM node_metrics WHERE node_id=? AND at>=? GROUP BY at/? ORDER BY 1`,
		step, step, nodeID, since.Unix(), step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.NodeMetrics{}
	for rows.Next() {
		var m api.NodeMetrics
		var at int64
		var used, total float64
		if err := rows.Scan(&at, &m.CPUPct, &used, &total, &m.DiskUsedGB, &m.DiskTotalGB, &m.NetRxBps, &m.NetTxBps, &m.Load1); err != nil {
			return nil, err
		}
		m.At = time.Unix(at, 0).UTC()
		m.MemUsedMB, m.MemTotalMB = int(used), int(total)
		out = append(out, m)
	}
	return out, rows.Err()
}

// LatestNodeMetrics returns the newest bucket for a node.
func (s *Store) LatestNodeMetrics(ctx context.Context, nodeID string) (*api.NodeMetrics, error) {
	var m api.NodeMetrics
	var at int64
	var used, total float64
	err := s.r.QueryRowContext(ctx, `SELECT at, cpu_pct, mem_used_mb, mem_total_mb, disk_used_gb, disk_total_gb, net_rx_bps, net_tx_bps, load1
		FROM node_metrics WHERE node_id=? ORDER BY at DESC LIMIT 1`, nodeID).
		Scan(&at, &m.CPUPct, &used, &total, &m.DiskUsedGB, &m.DiskTotalGB, &m.NetRxBps, &m.NetTxBps, &m.Load1)
	if err != nil {
		return nil, mapErr(err)
	}
	m.At = time.Unix(at, 0).UTC()
	m.MemUsedMB, m.MemTotalMB = int(used), int(total)
	return &m, nil
}

// ContainerMetricsRange returns container samples bucketed by step seconds.
func (s *Store) ContainerMetricsRange(ctx context.Context, serviceID string, since time.Time, step int64) ([]api.ContainerMetrics, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT (at/?)*?, AVG(cpu_pct), AVG(mem_mb), AVG(net_rx_bps), AVG(net_tx_bps)
		FROM container_metrics WHERE service_id=? AND at>=? GROUP BY at/? ORDER BY 1`, step, step, serviceID, since.Unix(), step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.ContainerMetrics{}
	for rows.Next() {
		m := api.ContainerMetrics{ServiceID: serviceID}
		var at int64
		if err := rows.Scan(&at, &m.CPUPct, &m.MemMB, &m.NetRxBps, &m.NetTxBps); err != nil {
			return nil, err
		}
		m.At = time.Unix(at, 0).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// PruneMetrics drops node samples older than nodeTTL and container samples older than ctrTTL.
func (s *Store) PruneMetrics(ctx context.Context, nodeTTL, ctrTTL time.Duration) error {
	now := time.Now()
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM node_metrics WHERE at < ?`, now.Add(-nodeTTL).Unix()); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM container_metrics WHERE at < ?`, now.Add(-ctrTTL).Unix())
	return err
}

// ---------- imports ----------

// importExtra holds the non-secret job fields that have no column of their own.
type importExtra struct {
	Namespace string             `json:"namespace,omitempty"`
	Verify    []api.ImportVerify `json:"verify,omitempty"`
}

// SaveImport upserts an import job; meta is stored encrypted (it may contain env values).
func (s *Store) SaveImport(ctx context.Context, j api.ImportJob, kind string, meta any) error {
	enc, err := s.encryptJSON(meta)
	if err != nil {
		return err
	}
	plan, err := s.encryptJSON(j.Plan)
	if err != nil {
		return err
	}
	extra := importExtra{Namespace: j.Namespace, Verify: j.Verify}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO imports(id, plan, project_id, status, steps, kind, meta, extra, created_at) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET plan=excluded.plan, project_id=excluded.project_id, status=excluded.status, steps=excluded.steps, meta=excluded.meta, extra=excluded.extra`,
		j.ID, plan, j.ProjectID, j.Status, mustJSON(j.Steps), kind, enc, mustJSON(extra), TS(j.CreatedAt))
	return err
}

// GetImport loads a job; meta (may be nil) is filled from the encrypted column.
func (s *Store) GetImport(ctx context.Context, id string, meta any) (*api.ImportJob, string, error) {
	var j api.ImportJob
	var plan, steps, kind, m, extra, at string
	err := s.DB.QueryRowContext(ctx, `SELECT id, plan, project_id, status, steps, kind, meta, extra, created_at FROM imports WHERE id=?`, id).
		Scan(&j.ID, &plan, &j.ProjectID, &j.Status, &steps, &kind, &m, &extra, &at)
	if err != nil {
		return nil, "", mapErr(err)
	}
	if err := s.decryptJSON(plan, &j.Plan); err != nil {
		return nil, "", err
	}
	json.Unmarshal([]byte(steps), &j.Steps)
	var ex importExtra
	json.Unmarshal([]byte(extra), &ex)
	j.Namespace, j.Verify = ex.Namespace, ex.Verify
	j.Mode = kind
	j.ServerID = j.Plan.TargetServer
	j.CreatedAt = parseTS(at)
	if meta != nil {
		if err := s.decryptJSON(m, meta); err != nil {
			return nil, "", err
		}
	}
	return &j, kind, nil
}

// ListImports lists jobs, newest first. Env values in plans are masked by the caller.
func (s *Store) ListImports(ctx context.Context) ([]api.ImportJob, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM imports ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []api.ImportJob{}
	for _, id := range ids {
		j, _, err := s.GetImport(ctx, id, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, nil
}

// ---------- project migrations ----------

// SaveMigration upserts a migration job.
func (s *Store) SaveMigration(ctx context.Context, m api.MigrationJob) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO migrations(id, project_id, from_server, to_server, status, steps, error, created_at, finished_at)
		VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status, steps=excluded.steps, error=excluded.error, finished_at=excluded.finished_at`,
		m.ID, m.ProjectID, m.FromServer, m.ToServer, m.Status, mustJSON(m.Steps), m.Error, TS(m.CreatedAt), nullTS(m.FinishedAt))
	return err
}

// GetMigration loads a migration job.
func (s *Store) GetMigration(ctx context.Context, id string) (*api.MigrationJob, error) {
	var m api.MigrationJob
	var steps, at string
	var fin sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, project_id, from_server, to_server, status, steps, error, created_at, finished_at FROM migrations WHERE id=?`, id).
		Scan(&m.ID, &m.ProjectID, &m.FromServer, &m.ToServer, &m.Status, &steps, &m.Error, &at, &fin)
	if err != nil {
		return nil, mapErr(err)
	}
	json.Unmarshal([]byte(steps), &m.Steps)
	m.CreatedAt = parseTS(at)
	m.FinishedAt = parseTSPtr(fin)
	return &m, nil
}

// ListMigrations lists migrations of a project ("" = all), newest first.
func (s *Store) ListMigrations(ctx context.Context, projectID string) ([]api.MigrationJob, error) {
	q, args := `SELECT id FROM migrations`, []any{}
	if projectID != "" {
		q += ` WHERE project_id=?`
		args = append(args, projectID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY created_at DESC LIMIT 100`, args...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []api.MigrationJob{}
	for _, id := range ids {
		m, err := s.GetMigration(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, nil
}

// ---------- blob tokens ----------

// CreateBlobToken stores a one-time token for blob put/get.
func (s *Store) CreateBlobToken(ctx context.Context, hash, blobID, mode string, ttl time.Duration) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO blob_tokens(token_hash, blob_id, mode, expires_at) VALUES(?,?,?,?)`, hash, blobID, mode, TS(time.Now().Add(ttl)))
	return err
}

// ConsumeBlobToken marks a token used and returns its blob id.
func (s *Store) ConsumeBlobToken(ctx context.Context, hash, mode string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT blob_id FROM blob_tokens WHERE token_hash=? AND mode=? AND used_at IS NULL AND expires_at > ?`,
		hash, mode, TS(time.Now())).Scan(&id)
	if err != nil {
		return "", mapErr(err)
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE blob_tokens SET used_at=? WHERE token_hash=? AND used_at IS NULL`, TS(time.Now()), hash)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return id, nil
}
