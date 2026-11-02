-- Panel sessions also end after a stretch without use (Store.SessionIdle), not only at
-- their fixed 30-day expiry. '' = not seen since this migration (counted from now on).
ALTER TABLE sessions ADD COLUMN last_seen_at TEXT NOT NULL DEFAULT '';
