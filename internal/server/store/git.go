package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// GitRow is a service's git source with its secrets decrypted (server-internal only).
type GitRow struct {
	api.GitSource
	Token         string // plaintext GitHub token ("" = none)
	WebhookSecret string // plaintext
	LastImage     string
	LastNode      string
}

// GetServiceGit returns the git source of a service, ErrNotFound when it pulls an image.
func (s *Store) GetServiceGit(ctx context.Context, serviceID string) (*GitRow, error) {
	var g GitRow
	var args, tok, hook string
	err := s.DB.QueryRowContext(ctx, `SELECT repo, branch, context, dockerfile, build_args, token, webhook_secret, last_commit, last_image, last_node
		FROM service_git WHERE service_id=?`, serviceID).Scan(&g.Repo, &g.Branch, &g.Context, &g.Dockerfile, &args, &tok, &hook, &g.LastCommit, &g.LastImage, &g.LastNode)
	if err != nil {
		return nil, mapErr(err)
	}
	json.Unmarshal([]byte(args), &g.BuildArgs)
	if len(g.BuildArgs) == 0 {
		g.BuildArgs = nil
	}
	if tok != "" {
		if g.Token, err = s.Box.Decrypt(tok); err != nil {
			return nil, err
		}
	}
	if hook != "" {
		if g.WebhookSecret, err = s.Box.Decrypt(hook); err != nil {
			return nil, err
		}
	}
	g.HasToken = g.Token != ""
	g.GitSource.Token = ""
	return &g, nil
}

// PutServiceGit creates or replaces a service's git source. token: nil keeps the stored one,
// "" clears it. webhookSecret "" keeps the stored one. The last-build fields are kept.
func (s *Store) PutServiceGit(ctx context.Context, serviceID string, g api.GitSource, token *string, webhookSecret string) error {
	tokEnc, hookEnc := "", ""
	var err error
	if token != nil && *token != "" {
		if tokEnc, err = s.Box.Encrypt(*token); err != nil {
			return err
		}
	}
	if webhookSecret != "" {
		if hookEnc, err = s.Box.Encrypt(webhookSecret); err != nil {
			return err
		}
	}
	args := g.BuildArgs
	if args == nil {
		args = map[string]string{}
	}
	keepTok := 0
	if token == nil {
		keepTok = 1
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO service_git(service_id, repo, branch, context, dockerfile, build_args, token, webhook_secret, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(service_id) DO UPDATE SET repo=excluded.repo, branch=excluded.branch, context=excluded.context,
			dockerfile=excluded.dockerfile, build_args=excluded.build_args,
			token=CASE WHEN ?=1 THEN service_git.token ELSE excluded.token END,
			webhook_secret=CASE WHEN excluded.webhook_secret='' THEN service_git.webhook_secret ELSE excluded.webhook_secret END,
			updated_at=excluded.updated_at`,
		serviceID, g.Repo, g.Branch, g.Context, g.Dockerfile, mustJSON(args), tokEnc, hookEnc, TS(time.Now()), keepTok)
	return mapErr(err)
}

// DeleteServiceGit makes the service pull its image again.
func (s *Store) DeleteServiceGit(ctx context.Context, serviceID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM service_git WHERE service_id=?`, serviceID)
	return err
}

// SetWebhookSecret replaces the webhook secret.
func (s *Store) SetWebhookSecret(ctx context.Context, serviceID, secret string) error {
	enc, err := s.Box.Encrypt(secret)
	if err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE service_git SET webhook_secret=? WHERE service_id=?`, enc, serviceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetGitBuilt records a successful build: its commit, the local image and where it was built.
func (s *Store) SetGitBuilt(ctx context.Context, serviceID, commit, image, node string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE service_git SET last_commit=?, last_image=?, last_node=? WHERE service_id=?`, commit, image, node, serviceID)
	return err
}

// attachGit fills Source/Git of services (secrets left out).
func (s *Store) attachGit(ctx context.Context, svcs ...*api.Service) error {
	for _, v := range svcs {
		v.Source = "image"
		g, err := s.GetServiceGit(ctx, v.ID)
		if err == ErrNotFound {
			continue
		}
		if err != nil {
			return err
		}
		src := g.GitSource
		v.Git = &src
		v.Source = "git"
	}
	return nil
}

// ---------- deployment build results and logs ----------

// SetDeploymentBuild stores the spec completed by a build (its image) and the commit built.
func (s *Store) SetDeploymentBuild(ctx context.Context, depID string, spec api.ResolvedSpec, commit string) error {
	enc, err := s.encryptJSON(spec)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE deployments SET spec=?, commit_sha=? WHERE id=?`, enc, commit, depID)
	return err
}

// MaxDeploymentLogLines is how many build log lines are kept per deployment (the newest).
const MaxDeploymentLogLines = 5000

// AppendDeploymentLog appends lines to a deployment's build log, keeping the newest
// MaxDeploymentLogLines.
func (s *Store) AppendDeploymentLog(ctx context.Context, depID string, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM deployment_logs WHERE deployment_id=?`, depID).Scan(&last); err != nil {
		return err
	}
	seq := last.Int64
	now := TS(time.Now())
	for _, l := range lines {
		seq++
		if _, err := tx.ExecContext(ctx, `INSERT INTO deployment_logs(deployment_id, seq, at, line) VALUES(?,?,?,?)`, depID, seq, now, l); err != nil {
			return mapErr(err)
		}
	}
	if seq > MaxDeploymentLogLines {
		if _, err := tx.ExecContext(ctx, `DELETE FROM deployment_logs WHERE deployment_id=? AND seq<=?`, depID, seq-MaxDeploymentLogLines); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeploymentLogs returns the stored lines after seq `after`; truncated when older lines were dropped.
func (s *Store) DeploymentLogs(ctx context.Context, depID string, after int64) ([]api.DeploymentLogLine, bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT seq, at, line FROM deployment_logs WHERE deployment_id=? AND seq>? ORDER BY seq`, depID, after)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []api.DeploymentLogLine{}
	for rows.Next() {
		var l api.DeploymentLogLine
		var at string
		if err := rows.Scan(&l.Seq, &at, &l.Line); err != nil {
			return nil, false, err
		}
		l.At = parseTS(at)
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	var first sql.NullInt64
	s.DB.QueryRowContext(ctx, `SELECT MIN(seq) FROM deployment_logs WHERE deployment_id=?`, depID).Scan(&first)
	return out, first.Valid && first.Int64 > 1, nil
}

// PruneDeploymentLogs drops the build logs of all but the newest keep deployments of a service.
func (s *Store) PruneDeploymentLogs(ctx context.Context, serviceID string, keep int) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM deployment_logs WHERE deployment_id IN (
		SELECT id FROM deployments WHERE service_id=? ORDER BY started_at DESC, rowid DESC LIMIT -1 OFFSET ?)`, serviceID, keep)
	return err
}

// SetServiceImage records the image a git service runs (the last build).
func (s *Store) SetServiceImage(ctx context.Context, serviceID, image string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE services SET image=? WHERE id=?`, image, serviceID)
	return err
}
