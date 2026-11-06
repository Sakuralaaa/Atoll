package api

import (
	"encoding/json"
	"time"
)

// ---------- 中控 / Control: the atoll-server host itself (docs/control.md) ----------
//
// The control server is never a worker: these endpoints manage atoll-server (status, logs,
// self-update, encrypted control backups). Workloads keep running while it is down.

// ControlStatus is GET /api/v1/control (read permission).
type ControlStatus struct {
	// Name is the display name set in the panel; "" means none (the UI shows Host.Hostname).
	Name          string    `json:"name,omitempty"`
	Version       string    `json:"version"`
	Commit        string    `json:"commit,omitempty"`
	GoVersion     string    `json:"go_version"`
	StartedAt     time.Time `json:"started_at"`
	UptimeSeconds int64     `json:"uptime_seconds"`

	Host ControlHost `json:"host"`

	CPUPct      float64 `json:"cpu_pct"`
	Load1       float64 `json:"load1"`
	Load5       float64 `json:"load5"`
	Load15      float64 `json:"load15"`
	MemUsedMB   int     `json:"mem_used_mb"`
	MemTotalMB  int     `json:"mem_total_mb"`
	DiskUsedGB  float64 `json:"disk_used_gb"`  // filesystem holding the data dir
	DiskTotalGB float64 `json:"disk_total_gb"` // filesystem holding the data dir

	DataDir       string `json:"data_dir"`
	DataDirBytes  int64  `json:"data_dir_bytes"` // cached for a few minutes
	DatabaseBytes int64  `json:"database_bytes"` // atoll.db + WAL

	PublicURL       string          `json:"public_url"`
	Tailnet         *ControlTailnet `json:"tailnet,omitempty"`
	AgentsConnected int             `json:"agents_connected"`
	AgentsTotal     int             `json:"agents_total"`

	LastBackup *ControlBackup `json:"last_backup,omitempty"`

	LatestRelease   *ReleaseInfo `json:"latest_release,omitempty"`
	UpdateAvailable bool         `json:"update_available"`
	ReleaseError    string       `json:"release_error,omitempty"`

	SelfUpdate       bool   `json:"self_update_supported"` // running under systemd with systemd-run
	UpdateInProgress bool   `json:"update_in_progress"`
	UpdatePhase      string `json:"update_phase,omitempty"`
	// UpdateTarget is the version being installed while UpdateInProgress. Version == UpdateTarget
	// means the new binary is already serving and only the helper's health report is outstanding.
	UpdateTarget    string        `json:"update_target,omitempty"`
	LastUpdate      *UpdateResult `json:"last_update_result,omitempty"`
	RollbackVersion string        `json:"rollback_version,omitempty"` // version of atoll-server.prev, when present
}

// ControlHost describes the machine atoll-server runs on.
type ControlHost struct {
	Hostname      string `json:"hostname"`
	OS            string `json:"os"` // e.g. "ubuntu 24.04"
	Kernel        string `json:"kernel"`
	Arch          string `json:"arch"`
	CPUCores      int    `json:"cpu_cores"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

// ControlTailnet is the control server's private-network state.
type ControlTailnet struct {
	IP         string `json:"ip,omitempty"`
	ControlURL string `json:"control_url,omitempty"` // URL agents use over the tailnet
	Listen     string `json:"listen,omitempty"`      // bound agent listener
	ListenErr  string `json:"listen_error,omitempty"`
}

// ReleaseInfo is a GitHub release of Atoll.
type ReleaseInfo struct {
	Tag         string    `json:"tag"`
	Name        string    `json:"name,omitempty"`
	URL         string    `json:"url,omitempty"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
}

// UpdateResult is what the self-update helper reports (update or rollback).
type UpdateResult struct {
	Mode        string    `json:"mode"` // "update" | "rollback"
	FromVersion string    `json:"from_version,omitempty"`
	ToVersion   string    `json:"to_version,omitempty"`
	OK          bool      `json:"ok"`
	RolledBack  bool      `json:"rolled_back,omitempty"` // the new binary failed health and the previous one was restored
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
}

// ReauthFields are accepted by every control action that needs a recent re-authentication:
// the current password, plus the TOTP code when two-factor login is on. They may be left out
// when POST /control/reauth succeeded in the last few minutes for this session.
type ReauthFields struct {
	Password string `json:"password,omitempty"`
	Code     string `json:"code,omitempty"`
}

// ControlReauthResponse is POST /api/v1/control/reauth.
type ControlReauthResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
}

// ControlUpdateRequest is POST /api/v1/control/update. Version "" = latest release.
type ControlUpdateRequest struct {
	ReauthFields
	Version string `json:"version,omitempty"`
}

// ControlUpdateResponse answers update and rollback (202: the work continues in the background;
// follow it through GET /control).
type ControlUpdateResponse struct {
	Mode    string `json:"mode"`
	Version string `json:"version,omitempty"`
	Phase   string `json:"phase"`
	Message string `json:"message,omitempty"`
}

// ControlLogs is GET /api/v1/control/logs.
type ControlLogs struct {
	Available bool     `json:"available"`
	Source    string   `json:"source"` // "journald"
	Lines     []string `json:"lines"`
	Message   string   `json:"message,omitempty"`
}

// ControlBackup is one encrypted control backup bundle.
type ControlBackup struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Trigger   string    `json:"trigger"` // "scheduled" | "manual" | "pre-update"
	Version   string    `json:"version"`
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256,omitempty"` // of the encrypted file
	Local     bool      `json:"local"`            // kept in <data>/control-backups
	Remote    bool      `json:"remote"`           // uploaded to the backup target
	RemoteKey string    `json:"remote_key,omitempty"`
	Error     string    `json:"error,omitempty"` // upload problem (the local copy is still fine)
}

// ControlBackupList is GET /api/v1/control/backups.
type ControlBackupList struct {
	Backups        []ControlBackup `json:"backups"`
	RemoteEnabled  bool            `json:"remote_enabled"` // backup target configured in Settings
	RemoteError    string          `json:"remote_error,omitempty"`
	KeepLocal      int             `json:"keep_local"`
	KeepRemote     int             `json:"keep_remote"`
	NextScheduled  *time.Time      `json:"next_scheduled,omitempty"`
	PassphraseFrom string          `json:"passphrase_from"` // "control_backup_passphrase" | "backup_repo_password" | "generated"
}

// ControlExportRequest is POST /api/v1/control/export. The passphrase (min 12 characters)
// encrypts the bundle and is never stored.
type ControlExportRequest struct {
	ReauthFields
	Passphrase string `json:"passphrase"`
}

// ControlPassphraseRequest is POST /api/v1/control/backups/passphrase: with Passphrase set it
// replaces the passphrase scheduled/local bundles are encrypted with; without, it reveals it.
type ControlPassphraseRequest struct {
	ReauthFields
	Passphrase string `json:"passphrase,omitempty"`
}

// ControlPassphrase is the answer of POST /control/backups/passphrase.
type ControlPassphrase struct {
	Passphrase string `json:"passphrase"`
	From       string `json:"from"`
}

// ControlBundleManifest is manifest.json inside a control bundle.
type ControlBundleManifest struct {
	Format    int       `json:"format"` // 1
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Hostname  string    `json:"hostname"`
	PublicURL string    `json:"public_url,omitempty"`
	Files     []string  `json:"files"`
}

// AuditEntry is one row of GET /api/v1/audit.
type AuditEntry struct {
	ID        int64  `json:"id"`
	Actor     string `json:"actor"`      // "user:<name>" | "token:<id>" | "cli" | "system" | ...
	ActorKind string `json:"actor_kind"` // "session" | "token" | "cli" | "system"
	Action    string `json:"action"`
	Target    string `json:"target"`
	IP        string `json:"ip,omitempty"`
	TokenID   string `json:"token_id,omitempty"`
	SessionID string `json:"session_id,omitempty"` // first 16 hex chars of the session hash (as in /auth/sessions)
	// The account behind the entry (session user or token owner), admin or member. Rows
	// written before v0.1.42 carry only the name, derived from actor "user:<name>".
	UserID    int64           `json:"user_id,omitempty"`
	UserName  string          `json:"user_name,omitempty"`
	UserRole  string          `json:"user_role,omitempty"` // "admin" | "member"
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}

// AuditPage is GET /api/v1/audit. NextBefore is the cursor for the next (older) page.
type AuditPage struct {
	Entries    []AuditEntry `json:"entries"`
	NextBefore int64        `json:"next_before,omitempty"`
}

// ControlPatch is PATCH /control: settings of the control server itself.
type ControlPatch struct {
	Name *string `json:"name,omitempty"` // display name; "" clears it (hostname is shown)
}
