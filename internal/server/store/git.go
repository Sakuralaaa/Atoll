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
	Token         string // plaintext token ("" = none)
	SSHKey        string // plaintext private key ("" = none)
	WebhookSecret string // plaintext
	LastImage     string
	LastNode      string
	LastPollAt    time.Time
}

// GitSecrets are the write-only parts of a git source for PutServiceGit: nil keeps the
// stored value, "" clears it.
type GitSecrets struct {
	Token  *string
	SSHKey *string
}

// GetServiceGit returns the git source of a service, ErrNotFound when it pulls an image.
func (s *Store) GetServiceGit(ctx context.Context, serviceID string) (*GitRow, error) {
	var g GitRow
	var args, tok, hook, key, detected, polled string
	err := s.DB.QueryRowContext(ctx, `SELECT repo, branch, context, dockerfile, build_args, token, webhook_secret, last_commit, last_image, last_node,
		provider, username, ssh_key, known_hosts, poll_interval_sec, builder, stack, install_cmd, build_cmd, start_cmd, detected, last_poll_at
		FROM service_git WHERE service_id=?`, serviceID).Scan(&g.Repo, &g.Branch, &g.Context, &g.Dockerfile, &args, &tok, &hook, &g.LastCommit, &g.LastImage, &g.LastNode,
		&g.Provider, &g.Username, &key, &g.KnownHosts, &g.PollIntervalSec, &g.Builder, &g.Stack, &g.InstallCommand, &g.BuildCommand, &g.StartCommand, &detected, &polled)
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
	if key != "" {
		if g.SSHKey, err = s.Box.Decrypt(key); err != nil {
			return nil, err
		}
	}
	if hook != "" {
		if g.WebhookSecret, err = s.Box.Decrypt(hook); err != nil {
			return nil, err
		}
	}
	if detected != "" {
		var d api.GitDetected
		if json.Unmarshal([]byte(detected), &d) == nil {
			g.Detected = &d
		}
	}
	if polled != "" {
		g.LastPollAt = parseTS(polled)
	}
	g.HasToken = g.Token != ""
	g.HasSSHKey = g.SSHKey != ""
	g.GitSource.Token, g.GitSource.SSHKey = "", ""
	return &g, nil
}

// PutServiceGit creates or replaces a service's git source. Secrets: nil keeps the stored
// one, "" clears it. webhookSecret "" keeps the stored one. The last-build fields are kept.
func (s *Store) PutServiceGit(ctx context.Context, serviceID string, g api.GitSource, sec GitSecrets, webhookSecret string) error {
	enc := func(v *string) (string, int, error) {
		if v == nil {
			return "", 1, nil
		}
		if *v == "" {
			return "", 0, nil
		}
		e, err := s.Box.Encrypt(*v)
		return e, 0, err
	}
	tokEnc, keepTok, err := enc(sec.Token)
	if err != nil {
		return err
	}
	keyEnc, keepKey, err := enc(sec.SSHKey)
	if err != nil {
		return err
	}
	hookEnc := ""
	if webhookSecret != "" {
		if hookEnc, err = s.Box.Encrypt(webhookSecret); err != nil {
			return err
		}
	}
	args := g.BuildArgs
	if args == nil {
		args = map[string]string{}
	}
	provider, builder := g.Provider, g.Builder
	if provider == "" {
		provider = "github"
	}
	if builder == "" {
		builder = "auto"
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO service_git(service_id, repo, branch, context, dockerfile, build_args, token, webhook_secret, updated_at,
			provider, username, ssh_key, known_hosts, poll_interval_sec, builder, stack, install_cmd, build_cmd, start_cmd)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(service_id) DO UPDATE SET repo=excluded.repo, branch=excluded.branch, context=excluded.context,
			dockerfile=excluded.dockerfile, build_args=excluded.build_args,
			token=CASE WHEN ?=1 THEN service_git.token ELSE excluded.token END,
			ssh_key=CASE WHEN ?=1 THEN service_git.ssh_key ELSE excluded.ssh_key END,
			webhook_secret=CASE WHEN excluded.webhook_secret='' THEN service_git.webhook_secret ELSE excluded.webhook_secret END,
			provider=excluded.provider, username=excluded.username, known_hosts=excluded.known_hosts,
			poll_interval_sec=excluded.poll_interval_sec, builder=excluded.builder, stack=excluded.stack,
			install_cmd=excluded.install_cmd, build_cmd=excluded.build_cmd, start_cmd=excluded.start_cmd,
			updated_at=excluded.updated_at`,
		serviceID, g.Repo, g.Branch, g.Context, g.Dockerfile, mustJSON(args), tokEnc, hookEnc, TS(time.Now()),
		provider, g.Username, keyEnc, g.KnownHosts, g.PollIntervalSec, builder, g.Stack, g.InstallCommand, g.BuildCommand, g.StartCommand,
		keepTok, keepKey)
	return mapErr(err)
}

// SetGitDetected stores what the last build or detection found (nil clears it).
func (s *Store) SetGitDetected(ctx context.Context, serviceID string, d *api.GitDetected) error {
	v := ""
	if d != nil {
		v = mustJSON(d)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE service_git SET detected=? WHERE service_id=?`, v, serviceID)
	return err
}

// SetGitPolled records when the branch head of a polled service was last checked.
func (s *Store) SetGitPolled(ctx context.Context, serviceID string, at time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE service_git SET last_poll_at=? WHERE service_id=?`, TS(at), serviceID)
	return err
}

// PolledGitServices lists the ids of services whose git source polls its branch.
func (s *Store) PolledGitServices(ctx context.Context) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT service_id FROM service_git WHERE poll_interval_sec>0 ORDER BY service_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
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
