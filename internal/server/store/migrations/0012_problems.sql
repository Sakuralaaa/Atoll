-- Problems: conditions the control server watches (a node offline, a disk filling up, a service
-- crash-looping, a failed backup, ...). A row is created when a condition is first observed and
-- only "fires" (firing_at) after it held for its kind's threshold, so short blips never notify.
-- A row that disappears before firing is deleted; one that fired gets resolved_at.
-- notified_at / resolved_notified_at make notifications idempotent (dedup across restarts).
CREATE TABLE problems (
  id                   TEXT PRIMARY KEY,
  kind                 TEXT NOT NULL,
  subject_id           TEXT NOT NULL,
  subject_name         TEXT NOT NULL DEFAULT '',
  project_id           TEXT NOT NULL DEFAULT '',
  server_id            TEXT NOT NULL DEFAULT '',
  message              TEXT NOT NULL DEFAULT '',
  since                TEXT NOT NULL,
  firing_at            TEXT,
  resolved_at          TEXT,
  notified_at          TEXT,
  resolved_notified_at TEXT
);
-- at most one open row per condition
CREATE UNIQUE INDEX problems_open ON problems(kind, subject_id) WHERE resolved_at IS NULL;
