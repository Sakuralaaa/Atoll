-- v0.1.48: disk usage of services and soft volume limits. Only new tables: services keep running
-- unchanged, no volume has a limit until one is set.

-- newest measurement per volume dir of a service ('' = the container's writable layer)
CREATE TABLE volume_usage (
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  volume     TEXT NOT NULL,
  bytes      INTEGER NOT NULL,
  at         INTEGER NOT NULL,
  PRIMARY KEY (service_id, volume)
);

-- total of mounted volumes + writable layer, one row per 10-minute bucket (kept 8 days)
CREATE TABLE disk_usage_history (
  service_id TEXT NOT NULL,
  at         INTEGER NOT NULL,
  bytes      INTEGER NOT NULL,
  PRIMARY KEY (service_id, at)
);

CREATE TABLE volume_limits (
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  volume     TEXT NOT NULL,
  limit_mb   INTEGER NOT NULL,
  over_limit TEXT NOT NULL DEFAULT 'stop',
  PRIMARY KEY (service_id, volume)
);

-- when the control server last stopped a service for a full volume, and the grace a manual
-- start gives before it is enforced again
CREATE TABLE disk_enforcement (
  service_id   TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  stopped_at   INTEGER NOT NULL DEFAULT 0,
  grace_until  INTEGER NOT NULL DEFAULT 0
);
