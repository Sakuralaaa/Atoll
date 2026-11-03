package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// DiskHighRatio is the used/total disk ratio at which disk_high is observed.
const DiskHighRatio = 0.9

// ProblemSignals reads the conditions that hold right now (no ids, no timestamps). The caller
// passes them to SyncProblems, which applies each kind's threshold.
func (s *Store) ProblemSignals(ctx context.Context, now time.Time) ([]api.Problem, error) {
	var out []api.Problem
	offline := map[string]bool{} // a service on a disconnected server is covered by node_offline

	// workers: offline agents, and the newest metrics sample for a disk filling up
	rows, err := s.r.QueryContext(ctx, `SELECT n.id, n.name, n.status, n.last_seen_at,
			COALESCE(m.at, 0), COALESCE(m.disk_used_gb, 0), COALESCE(m.disk_total_gb, 0)
		FROM nodes n LEFT JOIN node_metrics m ON m.node_id = n.id
			AND m.at = (SELECT MAX(at) FROM node_metrics WHERE node_id = n.id)
		WHERE n.role = 'worker'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name, status, seen string
		var at int64
		var used, total float64
		if err := rows.Scan(&id, &name, &status, &seen, &at, &used, &total); err != nil {
			rows.Close()
			return nil, err
		}
		switch status {
		case "offline":
			offline[id] = true
			msg := "agent 已断开"
			if t := parseTS(seen); !t.IsZero() {
				msg += "，最后在线 " + t.UTC().Format("2006-01-02 15:04 UTC")
			}
			out = append(out, api.Problem{Kind: api.ProblemNodeOffline, SubjectID: id, SubjectName: name, ServerID: id, Message: msg})
		case "online":
			// a sample older than 10 minutes says nothing about now
			if total > 0 && used/total >= DiskHighRatio && now.Sub(time.Unix(at, 0)) < 10*time.Minute {
				out = append(out, api.Problem{Kind: api.ProblemDiskHigh, SubjectID: id, SubjectName: name, ServerID: id,
					Message: fmt.Sprintf("磁盘已用 %.0f%%（%.1f / %.1f GB）", used/total*100, used, total)})
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// services that should run: latest deployment, status, container state, latest backup
	rows, err = s.r.QueryContext(ctx, `SELECT s.id, s.name, s.project_id, `+EffNode+`, s.status,
			COALESCE((SELECT d.status || char(31) || d.message FROM deployments d WHERE d.service_id = s.id ORDER BY d.started_at DESC LIMIT 1), ''),
			COALESCE((SELECT c.state FROM container_states c WHERE c.service_id = s.id ORDER BY c.updated_at DESC LIMIT 1), ''),
			COALESCE((SELECT b.status || char(31) || b.error FROM backups b WHERE b.service_id = s.id AND b.status IN ('done','failed')
				ORDER BY b.started_at DESC LIMIT 1), ''),
			COALESCE((SELECT bp.enabled FROM backup_policies bp WHERE bp.service_id = s.id), 0),
			COALESCE((SELECT v.status || char(31) || v.error FROM backup_verifications v WHERE v.kind = 'drill' AND v.service_id = s.id
				AND v.status IN ('done','failed') ORDER BY v.started_at DESC LIMIT 1), '')
		FROM services s JOIN projects p ON p.id = s.project_id
		WHERE s.desired_stopped = 0`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, name, pid, node, status, dep, cstate, bak, drill string
		var backupOn bool
		if err := rows.Scan(&id, &name, &pid, &node, &status, &dep, &cstate, &bak, &backupOn, &drill); err != nil {
			rows.Close()
			return nil, err
		}
		base := api.Problem{SubjectID: id, SubjectName: name, ProjectID: pid, ServerID: node}
		if offline[node] {
			continue
		}
		depStatus, depMsg, _ := strings.Cut(dep, "\x1f")
		switch {
		case depStatus == "failed":
			p := base
			p.Kind, p.Message = api.ProblemDeployFailed, firstLine("部署失败", depMsg)
			out = append(out, p)
		case status == "failed":
			p := base
			p.Kind, p.Message = api.ProblemServiceDown, serviceDownText(cstate)
			out = append(out, p)
		}
		if bs, be, _ := strings.Cut(bak, "\x1f"); backupOn && bs == "failed" {
			p := base
			p.Kind, p.Message = api.ProblemBackupFailed, firstLine("备份失败", be)
			out = append(out, p)
		}
		if ds, de, _ := strings.Cut(drill, "\x1f"); backupOn && ds == "failed" {
			p := base
			p.Kind, p.Message = api.ProblemBackupVerifyFailed, firstLine("恢复演练失败", de)
			out = append(out, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// the newest repository check, while any backup policy is enabled (not tied to the node that
	// ran it: the repository stays unverified while that node is offline)
	var chk string
	var anyOn bool
	if err := s.r.QueryRowContext(ctx, `SELECT COALESCE((SELECT v.status || char(31) || v.error || char(31) || v.server_id FROM backup_verifications v
			WHERE v.kind = 'check' AND v.status IN ('done','failed') ORDER BY v.started_at DESC LIMIT 1), ''),
		EXISTS(SELECT 1 FROM backup_policies WHERE enabled = 1)`).Scan(&chk, &anyOn); err != nil {
		return nil, err
	}
	if parts := strings.SplitN(chk, "\x1f", 3); anyOn && len(parts) == 3 && parts[0] == "failed" {
		out = append(out, api.Problem{Kind: api.ProblemBackupVerifyFailed, SubjectID: api.BackupRepoSubjectID, SubjectName: "备份仓库",
			ServerID: parts[2], Message: firstLine("仓库校验失败", parts[1])})
	}

	// domains that could not be provisioned
	rows, err = s.r.QueryContext(ctx, `SELECT d.id, d.hostname, s.project_id, `+EffNode+`, d.error
		FROM domains d JOIN services s ON s.id = d.service_id JOIN projects p ON p.id = s.project_id
		WHERE d.status = 'error'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, host, pid, node, msg string
		if err := rows.Scan(&id, &host, &pid, &node, &msg); err != nil {
			return nil, err
		}
		out = append(out, api.Problem{Kind: api.ProblemDomainError, SubjectID: id, SubjectName: host, ProjectID: pid, ServerID: node,
			Message: firstLine("域名配置失败", msg)})
	}
	return out, rows.Err()
}

func firstLine(prefix, msg string) string {
	msg, _, _ = strings.Cut(strings.TrimSpace(msg), "\n")
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if msg == "" {
		return prefix
	}
	return prefix + "：" + msg
}

func serviceDownText(stateJSON string) string {
	var c api.ContainerState
	if json.Unmarshal([]byte(stateJSON), &c) != nil {
		return "服务未在运行"
	}
	switch {
	case c.State == "restarting" || (c.State == "running" && c.RestartCount > 0 && c.Health != "healthy"):
		return fmt.Sprintf("容器反复重启（已重启 %d 次）", c.RestartCount)
	case c.Health == "unhealthy":
		return "健康检查失败"
	case c.State == "exited" || c.State == "dead":
		return "容器已退出"
	case c.State == "missing":
		return "容器不存在"
	}
	return "服务未在运行"
}

// ProblemChange is one notification-worthy transition SyncProblems made.
type ProblemChange struct {
	Problem  api.Problem
	Resolved bool
}

// SyncProblems reconciles open problems with the conditions observed now: new ones start
// pending, pending ones fire once they held for their threshold, open ones no longer observed
// resolve (fired) or vanish (never fired). Resolved rows older than 30 days are pruned.
func (s *Store) SyncProblems(ctx context.Context, now time.Time, observed []api.Problem) ([]ProblemChange, error) {
	open, err := s.listProblems(ctx, `resolved_at IS NULL`, 0)
	if err != nil {
		return nil, err
	}
	key := func(p api.Problem) string { return p.Kind + "\x00" + p.SubjectID }
	byKey := map[string]api.Problem{}
	for _, p := range open {
		byKey[key(p)] = p
	}
	var changes []ProblemChange
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for _, o := range observed {
		k := key(o)
		if seen[k] {
			continue
		}
		seen[k] = true
		cur, ok := byKey[k]
		if !ok {
			cur = o
			cur.ID, cur.Since = "prb_"+randHex(8), now
			if _, err := tx.ExecContext(ctx, `INSERT INTO problems(id, kind, subject_id, subject_name, project_id, server_id, message, since)
				VALUES(?,?,?,?,?,?,?,?)`, cur.ID, cur.Kind, cur.SubjectID, cur.SubjectName, cur.ProjectID, cur.ServerID, cur.Message, TS(now)); err != nil {
				return nil, err
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE problems SET subject_name=?, project_id=?, server_id=?, message=? WHERE id=?`,
			o.SubjectName, o.ProjectID, o.ServerID, o.Message, cur.ID); err != nil {
			return nil, err
		}
		if cur.FiringAt == nil && now.Sub(cur.Since) >= api.ProblemThreshold(cur.Kind) {
			if _, err := tx.ExecContext(ctx, `UPDATE problems SET firing_at=? WHERE id=?`, TS(now), cur.ID); err != nil {
				return nil, err
			}
			fired := o
			fired.ID, fired.Since, fired.FiringAt = cur.ID, cur.Since, &now
			changes = append(changes, ProblemChange{Problem: fired})
		}
	}
	for k, p := range byKey {
		if seen[k] {
			continue
		}
		if p.FiringAt == nil { // a blip that never fired leaves no trace
			if _, err := tx.ExecContext(ctx, `DELETE FROM problems WHERE id=?`, p.ID); err != nil {
				return nil, err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE problems SET resolved_at=? WHERE id=?`, TS(now), p.ID); err != nil {
			return nil, err
		}
		p.ResolvedAt = &now
		changes = append(changes, ProblemChange{Problem: p, Resolved: true})
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM problems WHERE resolved_at IS NOT NULL AND resolved_at < ?`, TS(now.Add(-30*24*time.Hour))); err != nil {
		return nil, err
	}
	return changes, tx.Commit()
}

// ListProblems returns firing problems, newest first; withResolved adds ones resolved in the
// last 24 hours.
func (s *Store) ListProblems(ctx context.Context, withResolved bool) ([]api.Problem, error) {
	all, err := s.listProblems(ctx, `firing_at IS NOT NULL`, 500)
	if err != nil {
		return nil, err
	}
	out := []api.Problem{}
	for _, p := range all {
		if p.ResolvedAt == nil || (withResolved && time.Since(*p.ResolvedAt) < 24*time.Hour) {
			out = append(out, p)
		}
	}
	return out, nil
}

// ProblemsToNotify returns fired problems not yet notified, and resolved ones whose firing was
// notified but whose recovery was not.
func (s *Store) ProblemsToNotify(ctx context.Context) ([]ProblemChange, error) {
	list, err := s.listProblems(ctx, `firing_at IS NOT NULL AND (
		(resolved_at IS NULL AND notified_at IS NULL) OR
		(resolved_at IS NOT NULL AND notified_at IS NOT NULL AND resolved_notified_at IS NULL))`, 200)
	if err != nil {
		return nil, err
	}
	out := make([]ProblemChange, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- { // oldest first
		out = append(out, ProblemChange{Problem: list[i], Resolved: list[i].ResolvedAt != nil})
	}
	return out, nil
}

// MarkProblemNotified records that the firing (or, with resolved, the recovery) went out.
func (s *Store) MarkProblemNotified(ctx context.Context, id string, resolved bool, now time.Time) error {
	col := "notified_at"
	if resolved {
		col = "resolved_notified_at"
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE problems SET `+col+`=? WHERE id=?`, TS(now), id)
	return err
}

func (s *Store) listProblems(ctx context.Context, where string, limit int) ([]api.Problem, error) {
	q := `SELECT id, kind, subject_id, subject_name, project_id, server_id, message, since, firing_at, resolved_at
		FROM problems WHERE ` + where + ` ORDER BY since DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.r.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []api.Problem
	for rows.Next() {
		var p api.Problem
		var since string
		var firing, resolved sql.NullString
		if err := rows.Scan(&p.ID, &p.Kind, &p.SubjectID, &p.SubjectName, &p.ProjectID, &p.ServerID, &p.Message, &since, &firing, &resolved); err != nil {
			return nil, err
		}
		p.Since, p.FiringAt, p.ResolvedAt = parseTS(since), parseTSPtr(firing), parseTSPtr(resolved)
		out = append(out, p)
	}
	return out, rows.Err()
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
