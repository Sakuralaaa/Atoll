package server

// Backup verification: the weekly repository check and the monthly restore drill. Agents run
// and schedule both (see internal/agent/backup/verify.go); the control server picks the one
// node that runs the scheduled check, works out schedules that keep clear of the backups,
// records the results and raises backup_verify_failed (store.ProblemSignals).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/cronexpr"
)

const (
	settingCheckSchedule = "backup_check_schedule"
	settingCheckSubset   = "backup_check_subset"
	settingDrillSchedule = "backup_drill_schedule"
)

// Tunables (tests shorten them).
var (
	verifyStaleAfter = 24 * time.Hour       // a running record without result is failed after this
	verifyKeep       = 365 * 24 * time.Hour // older records are pruned (the newest per kind/service stays)
)

// verifyHours is the preference order of automatic slots (night first, at :30).
var verifyHours = []int{4, 5, 6, 3, 2, 1, 0, 23, 22, 21, 7, 8, 20, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19}

// verifyGap: a slot at least this far from every backup is as good as any other.
const verifyGap = 3 * time.Hour

const (
	autoCheckFormat = "30 %d * * 0" // weekly, Sunday
	autoDrillFormat = "30 %d 1 * *" // monthly, the 1st
)

// busyCron is a schedule an automatic slot keeps away from.
type busyCron struct {
	sc  *cronexpr.Schedule
	loc *time.Location
}

func cronDistance(b busyCron, t time.Time) time.Duration {
	d := time.Duration(1<<62 - 1)
	if p := b.sc.Prev(t, b.loc); !p.IsZero() {
		d = min(d, t.Sub(p))
	}
	if n := b.sc.Next(t, b.loc); !n.IsZero() {
		d = min(d, n.Sub(t))
	}
	return d
}

// autoSlot picks the slot of format (one %d for the hour) whose next occurrences after ref
// lie farthest from every busy schedule, preferring verifyHours' order once verifyGap is met.
func autoSlot(format string, busy []busyCron, loc *time.Location, ref time.Time, occurrences int) string {
	best, bestScore := fmt.Sprintf(format, verifyHours[0]), time.Duration(-1)
	for _, h := range verifyHours {
		expr := fmt.Sprintf(format, h)
		sc := cronexpr.MustParse(expr)
		score, t := verifyGap, ref
		for i := 0; i < occurrences; i++ {
			if t = sc.Next(t, loc); t.IsZero() {
				break
			}
			for _, b := range busy {
				score = min(score, cronDistance(b, t))
			}
		}
		if score > bestScore {
			best, bestScore = expr, score
		}
		if score >= verifyGap {
			break
		}
	}
	return best
}

// busyBackups returns the enabled backup schedules ("" nodeID = every node).
func (s *Server) busyBackups(ctx context.Context, nodeID string) []busyCron {
	rows, _ := s.st.ListBackupPolicies(ctx, nodeID)
	var out []busyCron
	for _, r := range rows {
		if !r.Enabled {
			continue
		}
		sc, err := cronexpr.Parse(r.Schedule)
		if err != nil {
			continue
		}
		loc, err := cronexpr.LoadLocation(r.Timezone)
		if err != nil {
			continue
		}
		out = append(out, busyCron{sc, loc})
	}
	return out
}

// verifyRef is the reference the automatic slots are evaluated from: the start of the current
// month, so the choice does not move from one tick to the next.
func verifyRef(loc *time.Location) time.Time {
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
}

func verifyLoc() (string, *time.Location) {
	tz := serverTimezone()
	loc, err := cronexpr.LoadLocation(tz)
	if err != nil {
		return "UTC", time.UTC
	}
	return tz, loc
}

// checkSchedule is the effective weekly check cron ("" = off).
func (s *Server) checkSchedule(ctx context.Context) string {
	v, _ := s.st.GetSetting(ctx, settingCheckSchedule)
	switch v {
	case "off":
		return ""
	case "":
		_, loc := verifyLoc()
		return autoSlot(autoCheckFormat, s.busyBackups(ctx, ""), loc, verifyRef(loc), 5)
	}
	return v
}

// drillSchedule is the effective monthly drill cron of a node ("" = off).
func (s *Server) drillSchedule(ctx context.Context, nodeID string) string {
	v, _ := s.st.GetSetting(ctx, settingDrillSchedule)
	switch v {
	case "off":
		return ""
	case "":
		_, loc := verifyLoc()
		busy := s.busyBackups(ctx, nodeID)
		if c := s.checkSchedule(ctx); c != "" {
			busy = append(busy, busyCron{cronexpr.MustParse(c), loc})
		}
		return autoSlot(autoDrillFormat, busy, loc, verifyRef(loc), 3)
	}
	return v
}

func (s *Server) checkSubset(ctx context.Context) string {
	if v, _ := s.st.GetSetting(ctx, settingCheckSubset); v != "" {
		return v
	}
	return api.DefaultBackupCheckSubset
}

// checkNode is the node that runs the scheduled check: the previous choice while it stays
// online, else the oldest online node. "" when no node is online and none was chosen.
func (s *Server) checkNode(ctx context.Context) string {
	bs := s.backups()
	bs.mu.Lock()
	cur := bs.checkNode
	bs.mu.Unlock()
	if cur != "" && s.hub.Online(cur) {
		return cur
	}
	nodes, _ := s.st.ListNodes(ctx)
	for _, n := range nodes {
		if s.hub.Online(n.ID) {
			bs.mu.Lock()
			bs.checkNode = n.ID
			bs.mu.Unlock()
			return n.ID
		}
	}
	return cur
}

// verifyConfigFor is the verification part of a node's backup config.
func (s *Server) verifyConfigFor(ctx context.Context, nodeID string) *api.BackupVerifyConfig {
	tz, _ := verifyLoc()
	return &api.BackupVerifyConfig{
		RunCheck: s.checkNode(ctx) == nodeID, CheckSchedule: s.checkSchedule(ctx), CheckSubset: s.checkSubset(ctx),
		DrillSchedule: s.drillSchedule(ctx, nodeID), Timezone: tz,
	}
}

// ---------- settings ----------

// applyVerifySettings validates and stores the verification fields of PUT /settings.
func (s *Server) applyVerifySettings(ctx context.Context, req api.Settings) (bool, error) {
	set := map[string]string{}
	for _, f := range []struct{ key, val, name string }{
		{settingCheckSchedule, req.BackupCheckSchedule, "backup_check_schedule"},
		{settingDrillSchedule, req.BackupDrillSchedule, "backup_drill_schedule"},
	} {
		v := strings.TrimSpace(f.val)
		switch v {
		case "":
			continue
		case "auto":
			set[f.key] = ""
		case "off":
			set[f.key] = "off"
		default:
			sc, err := cronexpr.Parse(v)
			if err != nil {
				return false, badRequest("%s: %v", f.name, err)
			}
			if sc.Never() {
				return false, badRequest("%s %q never fires", f.name, v)
			}
			set[f.key] = v
		}
	}
	switch v := strings.TrimSpace(req.BackupCheckSubset); {
	case v == "":
	case v == "auto":
		set[settingCheckSubset] = ""
	case !api.ValidCheckSubset(v):
		return false, badRequest(`backup_check_subset must be a percentage ("5%%"), a fraction ("1/20") or a size ("500M")`)
	default:
		set[settingCheckSubset] = v
	}
	for k, v := range set { // the panel sends the stored values back unchanged
		if cur, _ := s.st.GetSetting(ctx, k); cur == v {
			delete(set, k)
		}
	}
	if len(set) == 0 {
		return false, nil
	}
	for k, v := range set {
		if err := s.st.SetSetting(ctx, k, v); err != nil {
			return false, err
		}
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "settings.backup_verify", "", set)
	return true, nil
}

func (s *Server) fillVerifySettings(ctx context.Context, st *api.Settings) {
	st.BackupCheckSchedule, _ = s.st.GetSetting(ctx, settingCheckSchedule)
	st.BackupCheckSubset, _ = s.st.GetSetting(ctx, settingCheckSubset)
	st.BackupDrillSchedule, _ = s.st.GetSetting(ctx, settingDrillSchedule)
}

// ---------- status ----------

func nextFire(expr, tz string) *time.Time {
	if expr == "" {
		return nil
	}
	sc, err := cronexpr.Parse(expr)
	if err != nil {
		return nil
	}
	loc, err := cronexpr.LoadLocation(tz)
	if err != nil {
		return nil
	}
	n := sc.Next(time.Now(), loc)
	if n.IsZero() {
		return nil
	}
	n = n.UTC()
	return &n
}

func (s *Server) latestVerification(ctx context.Context, kind, serviceID string) *api.BackupVerification {
	v, err := s.st.LatestVerification(ctx, kind, serviceID)
	if err != nil {
		return nil
	}
	return v
}

// verifyStatus builds BackupVerifyStatus (svc nil: the repository check only).
func (s *Server) verifyStatus(ctx context.Context, svc *api.Service) (api.BackupVerifyStatus, error) {
	_, ok := s.backupRepo(ctx)
	tz, _ := verifyLoc()
	out := api.BackupVerifyStatus{RepoConfigured: ok, CheckSchedule: s.checkSchedule(ctx), CheckSubset: s.checkSubset(ctx), Timezone: tz,
		Check: s.latestVerification(ctx, api.BackupVerifyCheck, "")}
	if ok {
		out.CheckServerID = s.checkNode(ctx)
		if out.CheckServerID != "" {
			out.NextCheckAt = nextFire(out.CheckSchedule, tz)
		}
	}
	if svc == nil {
		out.DrillSchedule = s.drillSchedule(ctx, "")
		return out, nil
	}
	node, _ := s.serviceNode(ctx, svc)
	out.DrillSchedule = s.drillSchedule(ctx, node)
	out.Drill = s.latestVerification(ctx, api.BackupVerifyDrill, svc.ID)
	p, err := s.policyFor(ctx, svc)
	if err != nil {
		return out, err
	}
	if ok && p.Enabled {
		out.NextDrillAt = nextFire(out.DrillSchedule, tz)
	}
	return out, nil
}

// GET /backup-verify
func (s *Server) handleBackupVerifyStatus(w http.ResponseWriter, r *http.Request) error {
	out, err := s.verifyStatus(r.Context(), nil)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// GET /services/{id}/backup-verify
func (s *Server) handleServiceVerifyStatus(w http.ResponseWriter, r *http.Request) error {
	svc, _, err := s.st.GetService(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	out, err := s.verifyStatus(r.Context(), svc)
	if err != nil {
		return err
	}
	if p := principalFrom(r.Context()); p != nil && p.Member {
		// the repository check is panel-wide (and runs on a server it may not have): admin only
		out.Check, out.CheckServerID, out.NextCheckAt, out.CheckSchedule, out.CheckSubset = nil, "", nil, "", ""
	}
	writeJSON(w, 200, out)
	return nil
}

// GET /backup-verifications?kind=check|drill&service_id=&limit=
func (s *Server) handleListVerifications(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "" && kind != api.BackupVerifyCheck && kind != api.BackupVerifyDrill {
		return badRequest("kind must be check or drill")
	}
	pr := principalFrom(ctx)
	svcID := q.Get("service_id")
	if svcID != "" {
		svc, _, err := s.st.GetService(ctx, svcID)
		if err != nil {
			return err
		}
		if !pr.inProject(svc.ProjectID) {
			return errf(404, "not_found", "service not found")
		}
		svcID = svc.ID
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.st.ListVerifications(ctx, kind, svcID, limit)
	if err != nil {
		return err
	}
	out := list[:0]
	proj := map[string]string{}
	for _, v := range list { // a project-scoped token sees the drills of its projects only
		if v.ServiceID == "" && pr.Member {
			continue // repository checks are the admin's
		}
		if v.ServiceID != "" {
			pid, seen := proj[v.ServiceID]
			if !seen {
				if svc, _, err := s.st.GetService(ctx, v.ServiceID); err == nil && memberSeesService(pr, svc) {
					pid = svc.ProjectID
				}
				proj[v.ServiceID] = pid
			}
			if pid == "" || !pr.inProject(pid) {
				continue
			}
		}
		out = append(out, v)
	}
	writeJSON(w, 200, out)
	return nil
}

// ---------- run now ----------

// startVerification records a running verification, asks node to run it and returns the record.
func (s *Server) startVerification(ctx context.Context, node string, p api.BackupVerifyPayload) (*api.BackupVerification, error) {
	if running, err := s.st.LatestVerification(ctx, p.Kind, p.ServiceID()); err == nil && running.Status == "running" &&
		time.Since(running.StartedAt) < verifyStaleAfter {
		return nil, errf(409, "verify_in_progress", "a %s started %s ago is still running (%s)", p.Kind, time.Since(running.StartedAt).Round(time.Second), running.ID)
	}
	if !s.hub.Online(node) {
		return nil, errf(409, "server_offline", "%v", ErrNodeOffline)
	}
	v := api.BackupVerification{ID: p.ID, Kind: p.Kind, ServiceID: p.ServiceID(), ServerID: node, Trigger: "manual", Status: "running",
		Subset: p.Subset, Steps: []api.BackupVerifyStep{}, StartedAt: time.Now().UTC()}
	if err := s.st.SaveVerification(ctx, v); err != nil {
		return nil, err
	}
	out, err := s.hub.Call(ctx, node, api.MsgBackupVerify, p, 0)
	if err != nil || strings.TrimSpace(out) == "busy" {
		s.st.DeleteVerification(ctx, v.ID)
		if err != nil {
			return nil, errf(502, "agent_error", "%v", err)
		}
		return nil, errf(409, "verify_in_progress", "a %s is already running on that server", p.Kind)
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "backup.verify", p.ServiceID(), map[string]string{"kind": p.Kind, "id": v.ID})
	s.publish("backup_verify", v)
	return &v, nil
}

// POST /backup-verify/check {server_id?}: run the repository check now.
func (s *Server) handleRunCheck(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ServerID string `json:"server_id"`
		Subset   string `json:"subset"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			return err
		}
	}
	ctx := r.Context()
	repo, ok := s.backupRepo(ctx)
	if !ok {
		return errf(409, "backup_not_configured", "configure a backup target in Settings → 备份存储 first")
	}
	subset := strings.TrimSpace(req.Subset)
	if subset == "" {
		subset = s.checkSubset(ctx)
	}
	if !api.ValidCheckSubset(subset) {
		return badRequest(`subset must be a percentage ("5%%"), a fraction ("1/20") or a size ("500M")`)
	}
	node := req.ServerID
	if node == "" {
		node = s.checkNode(ctx)
	} else if _, err := s.st.GetNode(ctx, node); err != nil {
		return err
	}
	if node == "" {
		return errf(409, "server_offline", "no online server to run the check (checks run on the agents)")
	}
	v, err := s.startVerification(ctx, node, api.BackupVerifyPayload{ID: newID("bvf"), Kind: api.BackupVerifyCheck, Trigger: "manual", Repo: repo, Subset: subset})
	if err != nil {
		return err
	}
	writeJSON(w, 202, v)
	return nil
}

// POST /services/{id}/backup-drill: run a restore drill of the service now.
func (s *Server) handleRunDrill(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	repo, ok := s.backupRepo(ctx)
	if !ok {
		return errf(409, "backup_not_configured", "configure a backup target in Settings → 备份存储 first")
	}
	p, err := s.policyFor(ctx, svc)
	if err != nil {
		return err
	}
	if len(backupParts(svc, p)) == 0 {
		return badRequest("service %s has no volumes and is not a database template: nothing to restore", svc.Name)
	}
	node, err := s.serviceNode(ctx, svc)
	if err != nil {
		return err
	}
	tg := targetFor(svc, p)
	v, err := s.startVerification(ctx, node, api.BackupVerifyPayload{ID: newID("bvf"), Kind: api.BackupVerifyDrill, Trigger: "manual", Repo: repo, Target: &tg})
	if err != nil {
		return err
	}
	writeJSON(w, 202, v)
	return nil
}

// handleBackupVerifyEvent records an agent's check / drill progress and result.
func (s *Server) handleBackupVerifyEvent(ctx context.Context, nodeID string, ev api.BackupVerifyEvent) {
	if ev.ID == "" || (ev.Kind != api.BackupVerifyCheck && ev.Kind != api.BackupVerifyDrill) || ev.Status == "busy" {
		return
	}
	if ev.Kind == api.BackupVerifyDrill {
		if ev.ServiceID == "" {
			return
		}
		if _, _, err := s.st.GetService(ctx, ev.ServiceID); err != nil {
			return // deleted meanwhile
		}
	} else {
		ev.ServiceID = ""
	}
	v := api.BackupVerification{ID: ev.ID, Kind: ev.Kind, ServiceID: ev.ServiceID, ServerID: nodeID, Trigger: ev.Trigger, Status: ev.Status,
		Subset: ev.Subset, Steps: ev.Steps, Error: ev.Error, Warnings: ev.Warnings, DurationMs: ev.DurationMs, StartedAt: ev.StartedAt, FinishedAt: ev.FinishedAt}
	if old, err := s.st.GetVerification(ctx, ev.ID); err == nil {
		if old.ServerID != nodeID { // an agent reports only the verifications it was asked to run
			s.log.Warn("backup verification event from another server ignored", "id", ev.ID, "node", nodeID)
			return
		}
		v.Kind, v.ServiceID, v.Trigger = old.Kind, old.ServiceID, old.Trigger
		if v.StartedAt.IsZero() {
			v.StartedAt = old.StartedAt
		}
	}
	if v.Trigger == "" {
		v.Trigger = "schedule"
	}
	if v.StartedAt.IsZero() {
		v.StartedAt = time.Now().UTC()
	}
	if err := s.st.SaveVerification(ctx, v); err != nil {
		s.log.Warn("save backup verification", "id", v.ID, "err", err)
		return
	}
	if v.Status == "failed" {
		s.log.Warn("backup verification failed", "kind", v.Kind, "service", v.ServiceID, "id", v.ID, "err", v.Error)
	}
	if cur, err := s.st.GetVerification(ctx, v.ID); err == nil {
		s.publish("backup_verify", cur)
	}
}

// verifyHousekeeping fails records the agent never finished and prunes old ones.
func (s *Server) verifyHousekeeping(ctx context.Context, prune bool) {
	if k, _ := s.st.FailStaleVerifications(ctx, verifyStaleAfter); k > 0 {
		s.log.Warn("backup verifications without result marked failed", "count", k)
	}
	if prune {
		if _, err := s.st.PruneVerifications(ctx, time.Now(), verifyKeep); err != nil && !errors.Is(err, context.Canceled) {
			s.log.Warn("prune backup verifications", "err", err)
		}
	}
}
