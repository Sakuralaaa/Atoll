-- Backup verification: weekly repository checks (restic check --read-data-subset, kind 'check',
-- no service) and monthly restore drills per backup policy (kind 'drill'). Run and scheduled by
-- the agents; the control server records what they report and raises backup_verify_failed.
CREATE TABLE backup_verifications (
  id          TEXT PRIMARY KEY,
  kind        TEXT NOT NULL,             -- check | drill
  service_id  TEXT NOT NULL DEFAULT '',  -- drill only
  server_id   TEXT NOT NULL DEFAULT '',
  trigger     TEXT NOT NULL,             -- schedule | manual
  status      TEXT NOT NULL,             -- running | done | failed | skipped
  subset      TEXT NOT NULL DEFAULT '',
  steps       TEXT NOT NULL DEFAULT '[]', -- []api.BackupVerifyStep JSON
  error       TEXT NOT NULL DEFAULT '',
  warnings    TEXT NOT NULL DEFAULT '[]',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  started_at  TEXT NOT NULL,
  finished_at TEXT
);
CREATE INDEX backup_verifications_latest ON backup_verifications(kind, service_id, started_at);
