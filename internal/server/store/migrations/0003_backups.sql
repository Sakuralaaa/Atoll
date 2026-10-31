-- Scheduled backups (restic to an S3 bucket, executed by agents).
CREATE TABLE backup_policies (
  service_id      TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  enabled         INTEGER NOT NULL DEFAULT 0,
  schedule        TEXT NOT NULL DEFAULT '0 3 * * *',
  timezone        TEXT NOT NULL DEFAULT 'UTC',
  keep_daily      INTEGER NOT NULL DEFAULT 7,
  keep_weekly     INTEGER NOT NULL DEFAULT 4,
  keep_monthly    INTEGER NOT NULL DEFAULT 6,
  include_volumes INTEGER NOT NULL DEFAULT 1,
  updated_at      TEXT NOT NULL
);

CREATE TABLE backups (
  id          TEXT PRIMARY KEY,
  service_id  TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  server_id   TEXT NOT NULL DEFAULT '',
  trigger     TEXT NOT NULL,
  status      TEXT NOT NULL,
  snapshots   TEXT NOT NULL DEFAULT '[]', -- []api.BackupSnapshot JSON
  bytes       INTEGER NOT NULL DEFAULT 0,
  added       INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  error       TEXT NOT NULL DEFAULT '',
  warnings    TEXT NOT NULL DEFAULT '[]',
  started_at  TEXT NOT NULL,
  finished_at TEXT
);
CREATE INDEX backups_service ON backups(service_id, started_at);

CREATE TABLE backup_restores (
  id          TEXT PRIMARY KEY,
  backup_id   TEXT NOT NULL,
  service_id  TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  project_id  TEXT NOT NULL,
  from_server TEXT NOT NULL DEFAULT '',
  to_server   TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL,
  steps       TEXT NOT NULL DEFAULT '[]',
  error       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  finished_at TEXT
);
CREATE INDEX backup_restores_service ON backup_restores(service_id, created_at);
