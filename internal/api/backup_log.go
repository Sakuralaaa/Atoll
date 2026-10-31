package api

import "log/slog"

// LogValue keeps BackupRepo credentials out of structured logs.
func (r BackupRepo) LogValue() slog.Value {
	return slog.GroupValue(slog.String("repository", r.URL()), slog.String("credentials", "redacted"))
}
