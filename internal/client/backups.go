package client

import (
	"context"
	"net/http"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// GetBackupPolicy returns a service's backup policy (defaults when none was saved).
func (c *Client) GetBackupPolicy(ctx context.Context, serviceID string) (*api.BackupPolicy, error) {
	var v api.BackupPolicy
	return &v, c.do(ctx, http.MethodGet, "/services/"+esc(serviceID)+"/backup-policy", nil, &v)
}

// PutBackupPolicy saves a service's backup policy. Send the full policy (start from
// GetBackupPolicy); the computed fields are ignored by the server.
func (c *Client) PutBackupPolicy(ctx context.Context, serviceID string, p api.BackupPolicy) (*api.BackupPolicy, error) {
	body := map[string]any{
		"enabled": p.Enabled, "schedule": p.Schedule, "timezone": p.Timezone,
		"keep": p.Keep, "include_volumes": p.IncludeVolumes,
	}
	var v api.BackupPolicy
	return &v, c.do(ctx, http.MethodPut, "/services/"+esc(serviceID)+"/backup-policy", body, &v)
}

// RunBackup starts a backup of the service now. The answer is the queued run
// (status "running"); the agent reports the outcome.
func (c *Client) RunBackup(ctx context.Context, serviceID string) (*api.Backup, error) {
	var v api.Backup
	return &v, c.do(ctx, http.MethodPost, "/services/"+esc(serviceID)+"/backups", nil, &v)
}

// ListBackups lists a service's backup runs, newest first. refresh reconciles the records
// with the repository first (imports unknown snapshots, marks removed ones pruned).
func (c *Client) ListBackups(ctx context.Context, serviceID string, refresh bool) ([]api.Backup, error) {
	p := "/services/" + esc(serviceID) + "/backups"
	if refresh {
		p += "?refresh=1"
	}
	var v []api.Backup
	return v, c.do(ctx, http.MethodGet, p, nil, &v)
}

// GetBackup returns one backup run (bak_...).
func (c *Client) GetBackup(ctx context.Context, backupID string) (*api.Backup, error) {
	var v api.Backup
	return &v, c.do(ctx, http.MethodGet, "/backups/"+esc(backupID), nil, &v)
}

// RestoreBackup restores a backup run, in place (targetServerID "") or onto another server
// (disaster recovery; only while the project's server is offline). Restores replace data,
// so with an API token the server answers with a pending confirmation: the error is then a
// *ConfirmationRequiredError (see AsConfirmation) and nothing happens until it is approved
// in the panel. With a panel session the restore job starts and is returned.
func (c *Client) RestoreBackup(ctx context.Context, backupID, targetServerID string) (*api.RestoreJob, error) {
	var v api.RestoreJob
	return &v, c.do(ctx, http.MethodPost, "/backups/"+esc(backupID)+"/restore", api.BackupRestoreRequest{TargetServerID: targetServerID}, &v)
}

// ListRestores lists a service's restore jobs.
func (c *Client) ListRestores(ctx context.Context, serviceID string) ([]api.RestoreJob, error) {
	var v []api.RestoreJob
	return v, c.do(ctx, http.MethodGet, "/services/"+esc(serviceID)+"/restores", nil, &v)
}

// GetRestore returns one restore job (rst_...).
func (c *Client) GetRestore(ctx context.Context, restoreID string) (*api.RestoreJob, error) {
	var v api.RestoreJob
	return &v, c.do(ctx, http.MethodGet, "/restores/"+esc(restoreID), nil, &v)
}
