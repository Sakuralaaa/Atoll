package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// CreateDeployment stores a deployment and its resolved spec (encrypted: it holds env secrets).
func (s *Store) CreateDeployment(ctx context.Context, d api.Deployment, spec api.ResolvedSpec) error {
	enc, err := s.encryptJSON(spec)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO deployments(id, service_id, server_id, image_digest, spec, status, trigger, message, started_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, d.ID, d.ServiceID, d.ServerID, d.ImageDigest, enc, d.Status, d.Trigger, d.Message, TS(d.StartedAt))
	return err
}

const depCols = `id, service_id, server_id, image_digest, status, trigger, message, started_at, finished_at`

func scanDep(row interface{ Scan(...any) error }) (*api.Deployment, error) {
	var d api.Deployment
	var st string
	var fin sql.NullString
	if err := row.Scan(&d.ID, &d.ServiceID, &d.ServerID, &d.ImageDigest, &d.Status, &d.Trigger, &d.Message, &st, &fin); err != nil {
		return nil, mapErr(err)
	}
	d.StartedAt = parseTS(st)
	d.FinishedAt = parseTSPtr(fin)
	return &d, nil
}

// GetDeployment returns a deployment.
func (s *Store) GetDeployment(ctx context.Context, id string) (*api.Deployment, error) {
	return scanDep(s.DB.QueryRowContext(ctx, `SELECT `+depCols+` FROM deployments WHERE id=?`, id))
}

// DeploymentSpec returns the resolved spec stored with a deployment.
func (s *Store) DeploymentSpec(ctx context.Context, id string) (*api.ResolvedSpec, error) {
	var enc string
	if err := s.DB.QueryRowContext(ctx, `SELECT spec FROM deployments WHERE id=?`, id).Scan(&enc); err != nil {
		return nil, mapErr(err)
	}
	var spec api.ResolvedSpec
	return &spec, s.decryptJSON(enc, &spec)
}

// ListDeployments returns newest first.
func (s *Store) ListDeployments(ctx context.Context, serviceID string, limit int) ([]api.Deployment, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+depCols+` FROM deployments WHERE service_id=? ORDER BY started_at DESC LIMIT ?`, serviceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []api.Deployment{}
	for rows.Next() {
		d, err := scanDep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// LatestDeployment returns the newest deployment of a service.
func (s *Store) LatestDeployment(ctx context.Context, serviceID string) (*api.Deployment, error) {
	return scanDep(s.DB.QueryRowContext(ctx, `SELECT `+depCols+` FROM deployments WHERE service_id=? ORDER BY started_at DESC LIMIT 1`, serviceID))
}

// UpdateDeployment applies an agent event. Terminal statuses set finished_at.
func (s *Store) UpdateDeployment(ctx context.Context, id, status, message, digest string) error {
	var fin any
	if status == "running" || status == "failed" || status == "superseded" {
		fin = TS(time.Now())
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE deployments SET status=?, message=CASE WHEN ?='' THEN message ELSE ? END,
		image_digest=CASE WHEN ?='' THEN image_digest ELSE ? END, finished_at=COALESCE(?, finished_at) WHERE id=?`,
		status, message, message, digest, digest, fin, id)
	return err
}

// SupersedeOthers marks the service's deployments created before keepID that are running or
// still in progress as superseded. Newer deployments are never touched: a request made while
// keepID was in flight is the newest desired state and still has to run.
func (s *Store) SupersedeOthers(ctx context.Context, serviceID, keepID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE deployments SET status='superseded', finished_at=COALESCE(finished_at, ?)
		WHERE service_id=? AND id<>? AND status IN ('running','queued','pulling','starting')
		AND rowid < (SELECT rowid FROM deployments WHERE id=?)`, TS(time.Now()), serviceID, keepID, keepID)
	return err
}

// NewerInProgress reports whether the service has a deployment created after id that has
// not finished yet.
func (s *Store) NewerInProgress(ctx context.Context, serviceID, id string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments WHERE service_id=? AND status IN ('queued','pulling','starting')
		AND rowid > (SELECT rowid FROM deployments WHERE id=?)`, serviceID, id).Scan(&n)
	return n > 0, err
}

// DesiredSpecsForNode returns, for every service on nodeID, the spec of its
// latest deployment with Stopped set from desired state.
func (s *Store) DesiredSpecsForNode(ctx context.Context, nodeID string) ([]api.ResolvedSpec, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id, s.desired_stopped FROM services s JOIN projects p ON p.id=s.project_id WHERE `+EffNode+`=? ORDER BY s.id`, nodeID)
	if err != nil {
		return nil, err
	}
	type pair struct {
		id      string
		stopped bool
	}
	var ids []pair
	for rows.Next() {
		var p pair
		var st int
		if err := rows.Scan(&p.id, &st); err != nil {
			rows.Close()
			return nil, err
		}
		p.stopped = st == 1
		ids = append(ids, p)
	}
	rows.Close()
	out := []api.ResolvedSpec{}
	for _, p := range ids {
		d, err := s.LatestDeployment(ctx, p.id)
		if err != nil {
			continue // never deployed
		}
		spec, err := s.DeploymentSpec(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		spec.Stopped = p.stopped
		out = append(out, *spec)
	}
	return out, nil
}

var _ = json.Marshal
