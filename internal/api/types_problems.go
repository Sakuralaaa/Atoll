package api

import "time"

// Problem kinds the control server watches. Each fires only after holding for its threshold
// (ProblemThreshold), so a short blip never notifies.
const (
	ProblemNodeOffline  = "node_offline"  // a worker's agent is disconnected
	ProblemDiskHigh     = "disk_high"     // a server's disk is >= 90% full
	ProblemServiceDown  = "service_down"  // a service that should run is failed / crash-looping
	ProblemDeployFailed = "deploy_failed" // the latest deployment of a service failed
	ProblemBackupFailed = "backup_failed" // the latest backup of a service failed
	ProblemDomainError  = "domain_error"  // a domain could not be provisioned (DNS / certificate)
	// ProblemBackupVerifyFailed: the newest repository check (subject BackupRepoSubjectID) or
	// the newest restore drill of a service (subject = service id) failed.
	ProblemBackupVerifyFailed = "backup_verify_failed"
)

// ProblemThreshold is how long a condition must hold before it fires.
func ProblemThreshold(kind string) time.Duration {
	switch kind {
	case ProblemNodeOffline:
		return 2 * time.Minute
	case ProblemDiskHigh:
		return 5 * time.Minute
	case ProblemServiceDown:
		return 3 * time.Minute
	case ProblemDomainError:
		return 10 * time.Minute
	}
	return 0 // deploy_failed, backup_failed, backup_verify_failed: already the end state of a finished job
}

// Problem is one watched condition. Pending ones (FiringAt nil) are not returned by the API.
type Problem struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	SubjectID   string     `json:"subject_id"` // server id for node_*/disk_*, service id otherwise
	SubjectName string     `json:"subject_name"`
	ProjectID   string     `json:"project_id,omitempty"`
	ServerID    string     `json:"server_id,omitempty"`
	Message     string     `json:"message"`
	Since       time.Time  `json:"since"`
	FiringAt    *time.Time `json:"firing_at,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

// NotificationChannel is one place problems are sent to. Secrets (BotToken, Password, and the
// webhook URL, which often embeds a token) are write-only: GET returns them empty with Has* set,
// and PUT with an empty value keeps the stored one.
type NotificationChannel struct {
	ID      string `json:"id"`   // stable id; PUT generates one for a new channel
	Type    string `json:"type"` // "webhook" | "telegram" | "email"
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
	// webhook: POST JSON {"event":"firing"|"resolved"|"test","problem":Problem,"text":"..."}
	URL    string `json:"url,omitempty"`
	HasURL bool   `json:"has_url,omitempty"`
	// telegram
	BotToken    string `json:"bot_token,omitempty"`
	HasBotToken bool   `json:"has_bot_token,omitempty"`
	ChatID      string `json:"chat_id,omitempty"`
	// email (SMTP; port 465 = implicit TLS, otherwise STARTTLS when offered)
	SMTPHost    string `json:"smtp_host,omitempty"`
	SMTPPort    int    `json:"smtp_port,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"has_password,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"` // comma separated
}

// NotificationSettings: GET/PUT /settings/notifications.
type NotificationSettings struct {
	Channels []NotificationChannel `json:"channels"`
	// NotifyResolved also sends a message when a problem clears (default true).
	NotifyResolved bool `json:"notify_resolved"`
	// Quiet hours "HH:MM" in Timezone (IANA, default UTC). Inside them only node_offline is sent;
	// other problems still firing when they end are sent then. Both empty = no quiet hours.
	QuietStart string `json:"quiet_start,omitempty"`
	QuietEnd   string `json:"quiet_end,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
}

// NotificationTestRequest: POST /settings/notifications/test. Channel is tested as given
// (unsaved edits work); an empty secret falls back to the stored channel with the same ID.
type NotificationTestRequest struct {
	Channel NotificationChannel `json:"channel"`
}
