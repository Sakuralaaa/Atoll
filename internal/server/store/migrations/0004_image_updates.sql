-- Image update detection: per-service policy and the last registry check.
CREATE TABLE image_updates (
  service_id     TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  policy         TEXT NOT NULL DEFAULT 'notify', -- notify | auto | off
  maint_window   TEXT NOT NULL DEFAULT '',       -- "HH:MM-HH:MM" UTC, '' = any time
  image          TEXT NOT NULL DEFAULT '',       -- ref the stored check result is for
  latest_digest  TEXT NOT NULL DEFAULT '',
  newer_tag      TEXT NOT NULL DEFAULT '',
  major_tag      TEXT NOT NULL DEFAULT '',
  error          TEXT NOT NULL DEFAULT '',
  checked_at     TEXT NOT NULL DEFAULT '',
  next_check_at  TEXT NOT NULL DEFAULT '',
  failures       INTEGER NOT NULL DEFAULT 0,
  last_auto_digest TEXT NOT NULL DEFAULT ''
);
