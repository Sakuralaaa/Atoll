package api

import "time"

// Database major-version upgrades (v0.1.49). PostgreSQL's on-disk format changes between
// majors, so switching the image tag alone leaves the service unable to start. An upgrade
// stops the service, dumps the old data with the old image, loads the dump into a fresh data
// dir with the new image, checks that the same databases and tables came back, and only then
// deploys the new image. The old data dir is kept on the node until the user removes it, so
// the upgrade can be rolled back.
const (
	MsgDBUpgrade         = "db.upgrade"          // server→agent DBUpgradePayload; result Output = JSON DBUpgradeResult
	MsgDBUpgradeRollback = "db.upgrade.rollback" // server→agent DBUpgradeRef; puts the kept old data back
	MsgDBUpgradeCleanup  = "db.upgrade.cleanup"  // server→agent DBUpgradeRef; deletes the kept old data and dump
)

// DBUpgradeKinds are the service kinds with a one-click major upgrade.
var DBUpgradeKinds = map[string]bool{"postgres": true}

// DBUpgradePayload asks the agent to convert a service's data to a new image.
type DBUpgradePayload struct {
	ServiceID    string        `json:"service_id"`
	Kind         string        `json:"kind"`
	Image        string        `json:"image"` // new image, pinned to a digest when known
	RegistryAuth *RegistryAuth `json:"registry_auth,omitempty"`
}

// DBUpgradeRef names the service of a rollback or cleanup.
type DBUpgradeRef struct {
	ServiceID string `json:"service_id"`
}

// DBUpgradeResult is the agent's report of a conversion.
type DBUpgradeResult struct {
	DumpBytes int64  `json:"dump_bytes"`
	Before    string `json:"before"` // e.g. "databases=3 tables=41" with the old image
	After     string `json:"after"`  // the same counts with the new image
	DataDir   string `json:"data_dir,omitempty"`
}

// DB upgrade statuses.
const (
	DBUpgradeRunning    = "running"
	DBUpgradeDone       = "done"
	DBUpgradeFailed     = "failed"      // nothing changed: the service runs the old image on the old data
	DBUpgradeRolledBack = "rolled_back" // the new version was deployed and then rolled back
)

// DBUpgradeStep is one step of an upgrade as shown in the panel.
type DBUpgradeStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pending | running | done | failed | skipped
	Detail string `json:"detail,omitempty"`
}

// DBUpgrade is the latest upgrade of a service (GET /services/{id}/db-upgrade).
type DBUpgrade struct {
	ServiceID  string          `json:"service_id"`
	Status     string          `json:"status"`
	FromImage  string          `json:"from_image"`
	ToImage    string          `json:"to_image"`
	Steps      []DBUpgradeStep `json:"steps"`
	Error      string          `json:"error,omitempty"`
	OldKept    bool            `json:"old_kept,omitempty"` // the old data is still on the node (rollback possible)
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	By         string          `json:"by,omitempty"`
}

// DBUpgradeInfo answers GET /services/{id}/db-upgrade.
type DBUpgradeInfo struct {
	Supported bool       `json:"supported"`        // the kind has a one-click upgrade and the agent can run it
	Reason    string     `json:"reason,omitempty"` // why not, when Supported is false
	Last      *DBUpgrade `json:"last,omitempty"`
}

// DBUpgradeRequest starts an upgrade to Tag (same repository as the current image).
type DBUpgradeRequest struct {
	Tag string `json:"tag"`
}
