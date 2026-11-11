package api

import "time"

// ---------- Backups (restic to any S3 bucket; see docs/api.md "Backups") ----------
// Data never transits the control server: each agent runs `restic` in a container and talks
// to the bucket directly. The agent keeps its policies locally and runs the schedule itself,
// so backups continue while the control server is offline; results are reported as
// MsgBackupEvent frames (queued while disconnected).

// BackupKeep is the restic `forget` retention (keep-daily / keep-weekly / keep-monthly).
type BackupKeep struct {
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
}

// BackupPolicy is GET/PUT /api/v1/services/{id}/backup-policy.
type BackupPolicy struct {
	ServiceID string     `json:"service_id"`
	Enabled   bool       `json:"enabled"`
	Schedule  string     `json:"schedule"` // 5-field cron, default "0 3 * * *"
	Timezone  string     `json:"timezone"` // IANA zone the schedule is read in; default the control server's zone
	Keep      BackupKeep `json:"keep"`
	// IncludeVolumes also snapshots the service's volume dirs. Database templates always back
	// up a logical dump; for other services the volumes are the backup (forced on).
	IncludeVolumes bool       `json:"include_volumes"`
	UpdatedAt      *time.Time `json:"updated_at,omitzero"`
	// read-only, computed
	NextRunAt      *time.Time `json:"next_run_at,omitzero"`
	RepoConfigured bool       `json:"repo_configured"`
	Parts          []string   `json:"parts,omitempty"` // what a run backs up: "dump" and/or "volumes"
}

// BackupSnapshot is one restic snapshot written by a backup run.
type BackupSnapshot struct {
	ID     string `json:"id"`               // restic snapshot id (full)
	Part   string `json:"part"`             // "dump" | "volumes"
	Bytes  int64  `json:"bytes"`            // logical size (total_bytes_processed)
	Added  int64  `json:"added"`            // new data uploaded (data_added, after dedup + compression)
	Pruned bool   `json:"pruned,omitempty"` // removed by retention
}

// Backup is one backup run of a service (GET /api/v1/services/{id}/backups).
type Backup struct {
	ID         string           `json:"id"` // "bak_..."
	ServiceID  string           `json:"service_id"`
	ServerID   string           `json:"server_id"`
	Trigger    string           `json:"trigger"` // "schedule" | "manual" | "catchup" | "imported"
	Status     string           `json:"status"`  // "running" | "done" | "failed" | "pruned"
	Snapshots  []BackupSnapshot `json:"snapshots"`
	Bytes      int64            `json:"bytes"`
	Added      int64            `json:"added"`
	DurationMs int64            `json:"duration_ms"`
	Error      string           `json:"error,omitempty"`
	Warnings   []string         `json:"warnings,omitempty"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt *time.Time       `json:"finished_at,omitzero"`
}

// BackupRestoreRequest is the body of POST /api/v1/backups/{id}/restore. An empty target (or
// the service's own server) restores in place. Another server is disaster recovery: the
// project's server must be offline; the whole project is moved to the target (services
// created there from their stored config), then this backup's data is restored.
type BackupRestoreRequest struct {
	TargetServerID string `json:"target_server_id,omitempty"`
}

// RestoreJob tracks a restore (GET /api/v1/restores/{id}).
type RestoreJob struct {
	ID         string       `json:"id"` // "rst_..."
	BackupID   string       `json:"backup_id"`
	ServiceID  string       `json:"service_id"`
	ProjectID  string       `json:"project_id"`
	FromServer string       `json:"from_server_id"`
	ToServer   string       `json:"to_server_id"`
	Status     string       `json:"status"` // "running" | "done" | "failed"
	Steps      []ImportStep `json:"steps"`
	Error      string       `json:"error,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	FinishedAt *time.Time   `json:"finished_at,omitzero"`
}

// BackupTestResult is POST /api/v1/settings/backup/test.
type BackupTestResult struct {
	OK       bool   `json:"ok"`
	ServerID string `json:"server_id"` // agent that ran the check
	Message  string `json:"message"`
}

const (
	// server -> agent
	MsgBackupConfig    = "backup.config"    // BackupConfigPayload: repo + every policy of this node; agent persists it and schedules locally
	MsgBackupRun       = "backup.run"       // BackupRunPayload: queue a run now; result Output "queued" | "skipped"; progress via MsgBackupEvent
	MsgBackupRestore   = "backup.restore"   // BackupRestorePayload: blocking; result Output = JSON BackupRestoreResult
	MsgBackupSnapshots = "backup.snapshots" // BackupSnapshotsPayload: result Output = JSON []ResticSnapshot
	MsgBackupTest      = "backup.test"      // BackupRepo: check (and init) the repository; result Output = message

	// agent -> server
	MsgBackupEvent = "backup" // BackupEvent
)

// BackupRepo is the restic repository on an S3-compatible bucket. Carries secrets: never log
// it (it implements slog.LogValuer and fmt.Stringer with redaction).
type BackupRepo struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix,omitempty"`
	Region    string `json:"region,omitempty"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Password  string `json:"password"`
}

// URL is the restic repository string, e.g. s3:https://acct.r2.cloudflarestorage.com/bucket/atoll.
func (r BackupRepo) URL() string {
	ep := r.Endpoint
	if ep != "" && !hasScheme(ep) {
		ep = "https://" + ep
	}
	for len(ep) > 0 && ep[len(ep)-1] == '/' {
		ep = ep[:len(ep)-1]
	}
	u := "s3:" + ep + "/" + r.Bucket
	if p := trimSlashes(r.Prefix); p != "" {
		u += "/" + p
	}
	return u
}

// String redacts the credentials.
func (r BackupRepo) String() string { return r.URL() + " (credentials redacted)" }

// GoString redacts the credentials (%#v).
func (r BackupRepo) GoString() string { return r.String() }

func hasScheme(s string) bool {
	return len(s) > 7 && (s[:7] == "http://" || (len(s) > 8 && s[:8] == "https://"))
}

func trimSlashes(s string) string {
	for len(s) > 0 && s[0] == '/' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// BackupTarget is one service's backup policy as the agent runs it.
type BackupTarget struct {
	ServiceID      string     `json:"service_id"`
	ProjectID      string     `json:"project_id"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"` // service kind: postgres|mysql|mongo|redis get a logical dump
	Volumes        []string   `json:"volumes"`
	IncludeVolumes bool       `json:"include_volumes"`
	Enabled        bool       `json:"enabled"`
	Schedule       string     `json:"schedule"`
	Timezone       string     `json:"timezone"`
	Keep           BackupKeep `json:"keep"`
}

type BackupConfigPayload struct {
	Repo    *BackupRepo    `json:"repo,omitempty"` // nil = no target configured (agent stops scheduling)
	Targets []BackupTarget `json:"targets"`
	// Verify schedules repository checks and restore drills (nil: an older server, none).
	Verify *BackupVerifyConfig `json:"verify,omitempty"`
}

type BackupRunPayload struct {
	BackupID string       `json:"backup_id"`
	Trigger  string       `json:"trigger"` // "manual" | "catchup"
	Target   BackupTarget `json:"target"`
	Repo     BackupRepo   `json:"repo"`
	// Since (catch-up only): skip when this agent already ran the service at or after Since.
	Since *time.Time `json:"since,omitzero"`
}

// BackupEvent reports a run's progress (status "running") and outcome ("done" | "failed").
type BackupEvent struct {
	BackupID   string           `json:"backup_id"`
	ServiceID  string           `json:"service_id"`
	Trigger    string           `json:"trigger"`
	Status     string           `json:"status"`
	Snapshots  []BackupSnapshot `json:"snapshots,omitempty"`
	Bytes      int64            `json:"bytes"`
	Added      int64            `json:"added"`
	DurationMs int64            `json:"duration_ms"`
	Error      string           `json:"error,omitempty"`
	Warnings   []string         `json:"warnings,omitempty"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt *time.Time       `json:"finished_at,omitzero"`
	Pruned     []string         `json:"pruned,omitempty"` // snapshot ids removed by retention
}

// BackupRestorePayload restores snapshots of SourceServiceID into the local service Target.
type BackupRestorePayload struct {
	Target          BackupTarget     `json:"target"`
	SourceServiceID string           `json:"source_service_id"`
	Snapshots       []BackupSnapshot `json:"snapshots"`
	Repo            BackupRepo       `json:"repo"`
}

type BackupRestoreResult struct {
	Restored []string `json:"restored"` // parts restored
	Bytes    int64    `json:"bytes"`
	Warnings []string `json:"warnings,omitempty"`
	// MovedAside is where the replaced data was kept (one generation).
	MovedAside string `json:"moved_aside,omitempty"`
}

type BackupSnapshotsPayload struct {
	Repo      BackupRepo `json:"repo"`
	ServiceID string     `json:"service_id"`
}

// ResticSnapshot is one entry of `restic snapshots --json`.
type ResticSnapshot struct {
	ID       string    `json:"id"`
	ShortID  string    `json:"short_id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags"`
	Paths    []string  `json:"paths"`
	// Summary is set by restic >= 0.17 (absent for older snapshots).
	Summary *ResticSnapshotSummary `json:"summary,omitempty"`
}

// ResticSnapshotSummary is the backup summary restic stores in a snapshot.
type ResticSnapshotSummary struct {
	TotalFilesProcessed int64 `json:"total_files_processed"`
	TotalBytesProcessed int64 `json:"total_bytes_processed"`
}
