-- v0.1.49: database major-version upgrades. Only a new table: one row per service, the latest
-- upgrade (steps as JSON), so the panel can show its progress and offer a rollback.
CREATE TABLE db_upgrades (
  service_id  TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  status      TEXT NOT NULL,
  from_image  TEXT NOT NULL,
  to_image    TEXT NOT NULL,
  steps       TEXT NOT NULL DEFAULT '[]',
  error       TEXT NOT NULL DEFAULT '',
  old_kept    INTEGER NOT NULL DEFAULT 0,
  started_at  INTEGER NOT NULL,
  finished_at INTEGER,
  by_actor    TEXT NOT NULL DEFAULT ''
);
