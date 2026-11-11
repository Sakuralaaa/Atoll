package api

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------- Backup verification (see docs/api.md "Backup verification") ----------
// Two jobs prove that backups can actually be restored:
//   - check: `restic check --read-data-subset` on the shared repository, weekly. Exactly one
//     node (chosen by the control server) runs it on schedule, so nodes sharing the repository
//     never run it at the same time; restic's exclusive lock guards the rare overlap.
//   - drill: a restore rehearsal per backup policy, monthly, on the service's own node. The
//     newest snapshot of each part is restored into a temp dir (volumes: files must come back,
//     non-empty) or loaded into a temporary container of the service's own image (dump: the
//     database must accept the dump and answer a query). Running services and their data are
//     never touched; every temporary resource is removed afterwards.
// Like backups, both are scheduled by the agent itself and reported as MsgBackupVerifyEvent.

const (
	BackupVerifyCheck = "check"
	BackupVerifyDrill = "drill"

	// DefaultBackupCheckSubset is the share of pack files a scheduled check reads.
	DefaultBackupCheckSubset = "5%"
	// BackupRepoSubjectID is the problem subject of a failed repository check.
	BackupRepoSubjectID = "backup_repo"

	// server -> agent
	MsgBackupVerify = "backup.verify" // BackupVerifyPayload: queue a check / drill now; result Output "queued" | "busy"
	// agent -> server
	MsgBackupVerifyEvent = "backup_verify" // BackupVerifyEvent
)

// BackupVerifyConfig is the verification part of BackupConfigPayload (nil = none scheduled).
type BackupVerifyConfig struct {
	// RunCheck: this node runs the scheduled repository check (only one node does).
	RunCheck      bool   `json:"run_check"`
	CheckSchedule string `json:"check_schedule"` // 5-field cron; "" = no scheduled check
	CheckSubset   string `json:"check_subset"`   // --read-data-subset value, e.g. "5%", "1/20", "500M"
	DrillSchedule string `json:"drill_schedule"` // 5-field cron; "" = no scheduled drills
	Timezone      string `json:"timezone"`       // IANA zone both schedules are read in
}

// BackupVerifyPayload asks the agent to run one check or one drill now.
type BackupVerifyPayload struct {
	ID      string        `json:"id"`      // "bvf_..."
	Kind    string        `json:"kind"`    // "check" | "drill"
	Trigger string        `json:"trigger"` // "manual"
	Repo    BackupRepo    `json:"repo"`
	Subset  string        `json:"subset,omitempty"` // check only
	Target  *BackupTarget `json:"target,omitempty"` // drill only
}

// ServiceID is the drilled service ("" for a check).
func (p BackupVerifyPayload) ServiceID() string {
	if p.Target != nil {
		return p.Target.ServiceID
	}
	return ""
}

// BackupVerifyStep is one part of a verification: the repository check, or the restore of
// one snapshot part in a drill.
type BackupVerifyStep struct {
	Part       string `json:"part"` // "check" | "volumes" | "dump"
	SnapshotID string `json:"snapshot_id,omitempty"`
	Status     string `json:"status"`           // "done" | "failed" | "skipped"
	Detail     string `json:"detail,omitempty"` // e.g. "files=120 bytes=5242880", "databases=2 tables=14"
	Files      int64  `json:"files,omitempty"`  // volumes: restored regular files
	Bytes      int64  `json:"bytes,omitempty"`  // volumes: restored bytes; dump: dump size
	Error      string `json:"error,omitempty"`
}

// BackupVerifyEvent reports a verification's start (status "running") and outcome
// ("done" | "failed" | "skipped": another check held the repository lock).
type BackupVerifyEvent struct {
	ID         string             `json:"id"`
	Kind       string             `json:"kind"`
	ServiceID  string             `json:"service_id,omitempty"`
	Trigger    string             `json:"trigger"`
	Status     string             `json:"status"`
	Subset     string             `json:"subset,omitempty"`
	Steps      []BackupVerifyStep `json:"steps,omitempty"`
	Error      string             `json:"error,omitempty"`
	Warnings   []string           `json:"warnings,omitempty"`
	DurationMs int64              `json:"duration_ms"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt *time.Time         `json:"finished_at,omitzero"`
}

// BackupVerification is one recorded check or drill (GET /backup-verifications).
type BackupVerification struct {
	ID         string             `json:"id"` // "bvf_..."
	Kind       string             `json:"kind"`
	ServiceID  string             `json:"service_id,omitempty"` // drill only
	ServerID   string             `json:"server_id"`
	Trigger    string             `json:"trigger"` // "schedule" | "manual"
	Status     string             `json:"status"`  // "running" | "done" | "failed" | "skipped"
	Subset     string             `json:"subset,omitempty"`
	Steps      []BackupVerifyStep `json:"steps"`
	Error      string             `json:"error,omitempty"`
	Warnings   []string           `json:"warnings,omitempty"`
	DurationMs int64              `json:"duration_ms"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt *time.Time         `json:"finished_at,omitzero"`
}

// BackupVerifyStatus is GET /backup-verify (repository check only) and
// GET /services/{id}/backup-verify (plus that service's drill).
type BackupVerifyStatus struct {
	Check          *BackupVerification `json:"check,omitempty"` // newest repository check
	Drill          *BackupVerification `json:"drill,omitempty"` // newest drill of the service
	CheckSchedule  string              `json:"check_schedule"`  // effective cron ("" = off)
	CheckSubset    string              `json:"check_subset"`
	DrillSchedule  string              `json:"drill_schedule"` // effective cron ("" = off)
	Timezone       string              `json:"timezone"`
	CheckServerID  string              `json:"check_server_id,omitempty"` // node that runs the scheduled check
	NextCheckAt    *time.Time          `json:"next_check_at,omitzero"`
	NextDrillAt    *time.Time          `json:"next_drill_at,omitzero"` // service endpoint, when backups are enabled
	RepoConfigured bool                `json:"repo_configured"`
}

var checkSubsetRe = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?%|[1-9][0-9]*/[1-9][0-9]*|[1-9][0-9]*(?:\.[0-9]+)?[KMGT])$`)

// ValidCheckSubset reports whether s is a restic --read-data-subset value Atoll accepts:
// a percentage in (0, 100] ("5%", "2.5%"), a fraction n/t with n <= t ("1/20"), or a size
// ("500M", "2G").
func ValidCheckSubset(s string) bool {
	if !checkSubsetRe.MatchString(s) {
		return false
	}
	if p, ok := strings.CutSuffix(s, "%"); ok {
		f, err := strconv.ParseFloat(p, 64)
		return err == nil && f > 0 && f <= 100
	}
	if n, t, ok := strings.Cut(s, "/"); ok {
		a, err1 := strconv.Atoi(n)
		b, err2 := strconv.Atoi(t)
		return err1 == nil && err2 == nil && a <= b
	}
	return true
}
