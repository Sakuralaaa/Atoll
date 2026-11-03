-- The last panel-started agent upgrade per server (was in memory only, lost on restart).
CREATE TABLE agent_upgrades (
  node_id     TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
  state       TEXT NOT NULL,
  from_ver    TEXT NOT NULL DEFAULT '',
  to_ver      TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  rolled_back INTEGER NOT NULL DEFAULT 0,
  started_at  TEXT NOT NULL,
  finished_at TEXT
);
