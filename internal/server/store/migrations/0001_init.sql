-- atoll-server schema v1. Times are stored as RFC3339Nano UTC text.

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  email         TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL,
  totp_secret   TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
);

CREATE TABLE teams (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL
);

CREATE TABLE team_members (
  team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role    TEXT NOT NULL DEFAULT 'owner',
  PRIMARY KEY (team_id, user_id)
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL
);

CREATE TABLE api_tokens (
  id           TEXT PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  scope        TEXT NOT NULL,
  prefix       TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,
  last_used_at TEXT,
  created_at   TEXT NOT NULL
);

CREATE TABLE nodes (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  role          TEXT NOT NULL DEFAULT 'worker',
  status        TEXT NOT NULL DEFAULT 'pending',
  public_ipv4   TEXT NOT NULL DEFAULT '',
  public_ipv6   TEXT NOT NULL DEFAULT '',
  tailnet_host  TEXT NOT NULL DEFAULT '',
  provider      TEXT NOT NULL DEFAULT '',
  region        TEXT NOT NULL DEFAULT '',
  os            TEXT NOT NULL DEFAULT '',
  arch          TEXT NOT NULL DEFAULT '',
  cpu_cores     INTEGER NOT NULL DEFAULT 0,
  mem_mb        INTEGER NOT NULL DEFAULT 0,
  disk_gb       INTEGER NOT NULL DEFAULT 0,
  labels        TEXT NOT NULL DEFAULT '{}',
  agent_version TEXT NOT NULL DEFAULT '',
  docker_version TEXT NOT NULL DEFAULT '',
  last_seen_at  TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL
);

CREATE TABLE join_tokens (
  token_hash TEXT PRIMARY KEY,
  expires_at TEXT NOT NULL,
  used_at    TEXT,
  node_id    TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE node_tokens (
  token_hash TEXT PRIMARY KEY,
  node_id    TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL
);

CREATE TABLE projects (
  id          TEXT PRIMARY KEY,
  team_id     INTEGER REFERENCES teams(id),
  name        TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  server_id   TEXT NOT NULL REFERENCES nodes(id),
  -- 1 while an in-place takeover has not cut over: containers run, but no ingress routes are pushed
  ingress_hold INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL
);

CREATE TABLE registry_creds (
  id                 TEXT PRIMARY KEY,
  name               TEXT NOT NULL,
  registry           TEXT NOT NULL,
  username           TEXT NOT NULL,
  password_encrypted TEXT NOT NULL,
  created_at         TEXT NOT NULL
);

-- ports/volumes/configs/health are JSON (api.Port etc.); they are always read and
-- written together with the service, so separate tables buy nothing yet.
CREATE TABLE services (
  id               TEXT PRIMARY KEY,
  project_id       TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name             TEXT NOT NULL,
  kind             TEXT NOT NULL,
  image            TEXT NOT NULL,
  command          TEXT NOT NULL DEFAULT '[]',
  args             TEXT NOT NULL DEFAULT '[]',
  ports            TEXT NOT NULL DEFAULT '[]',
  volumes          TEXT NOT NULL DEFAULT '[]',
  configs          TEXT NOT NULL DEFAULT '[]',
  health           TEXT NOT NULL DEFAULT 'null',
  cpu_limit        REAL NOT NULL DEFAULT 0,
  mem_limit_mb     INTEGER NOT NULL DEFAULT 0,
  registry_cred_id TEXT NOT NULL DEFAULT '',
  status           TEXT NOT NULL DEFAULT 'unknown',
  replicas         TEXT NOT NULL DEFAULT '0/1',
  desired_stopped  INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT NOT NULL,
  UNIQUE (project_id, name)
);

CREATE TABLE env_vars (
  id         INTEGER PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  key        TEXT NOT NULL,
  value      TEXT NOT NULL, -- AES-GCM ciphertext ("v1:...") when is_secret
  is_secret  INTEGER NOT NULL DEFAULT 0,
  exposed    INTEGER NOT NULL DEFAULT 0,
  position   INTEGER NOT NULL DEFAULT 0,
  UNIQUE (service_id, key)
);

CREATE TABLE domains (
  id           TEXT PRIMARY KEY,
  service_id   TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  hostname     TEXT NOT NULL UNIQUE,
  port_name    TEXT NOT NULL,
  proxied      INTEGER NOT NULL DEFAULT 1,
  status       TEXT NOT NULL DEFAULT 'pending',
  error        TEXT NOT NULL DEFAULT '',
  cf_record_id TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL
);

CREATE TABLE deployments (
  id           TEXT PRIMARY KEY,
  service_id   TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  server_id    TEXT NOT NULL,
  image_digest TEXT NOT NULL DEFAULT '',
  spec         TEXT NOT NULL, -- api.ResolvedSpec JSON, secrets encrypted as a whole
  status       TEXT NOT NULL,
  trigger      TEXT NOT NULL,
  message      TEXT NOT NULL DEFAULT '',
  started_at   TEXT NOT NULL,
  finished_at  TEXT
);
CREATE INDEX deployments_service ON deployments(service_id, started_at);

CREATE TABLE container_states (
  node_id    TEXT NOT NULL,
  service_id TEXT NOT NULL,
  state      TEXT NOT NULL, -- api.ContainerState JSON
  updated_at TEXT NOT NULL,
  PRIMARY KEY (node_id, service_id)
);

CREATE TABLE confirmations (
  id         TEXT PRIMARY KEY,
  action     TEXT NOT NULL,
  target     TEXT NOT NULL,
  summary    TEXT NOT NULL,
  payload    TEXT NOT NULL DEFAULT '{}',
  status     TEXT NOT NULL DEFAULT 'pending',
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL,
  decided_at TEXT,
  decided_by TEXT NOT NULL DEFAULT ''
);

-- Metrics: one row per minute bucket; n = samples averaged into the row.
CREATE TABLE node_metrics (
  node_id       TEXT NOT NULL,
  at            INTEGER NOT NULL, -- unix seconds, minute aligned
  n             INTEGER NOT NULL DEFAULT 1,
  cpu_pct       REAL NOT NULL,
  mem_used_mb   REAL NOT NULL,
  mem_total_mb  REAL NOT NULL,
  disk_used_gb  REAL NOT NULL,
  disk_total_gb REAL NOT NULL,
  net_rx_bps    REAL NOT NULL,
  net_tx_bps    REAL NOT NULL,
  load1         REAL NOT NULL,
  PRIMARY KEY (node_id, at)
);

CREATE TABLE container_metrics (
  service_id TEXT NOT NULL,
  at         INTEGER NOT NULL,
  n          INTEGER NOT NULL DEFAULT 1,
  cpu_pct    REAL NOT NULL,
  mem_mb     REAL NOT NULL,
  net_rx_bps REAL NOT NULL,
  net_tx_bps REAL NOT NULL,
  PRIMARY KEY (service_id, at)
);

CREATE TABLE imports (
  id         TEXT PRIMARY KEY,
  plan       TEXT NOT NULL,
  project_id TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL,
  steps      TEXT NOT NULL DEFAULT '[]',
  kind       TEXT NOT NULL DEFAULT 'plan', -- 'plan' (CLI upload) | 'zeabur-local' (in-place takeover)
  meta       TEXT NOT NULL DEFAULT '{}',   -- kind-specific state, encrypted for zeabur-local
  created_at TEXT NOT NULL
);

CREATE TABLE migrations (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL,
  from_server TEXT NOT NULL,
  to_server   TEXT NOT NULL,
  status      TEXT NOT NULL,
  steps       TEXT NOT NULL DEFAULT '[]',
  error       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  finished_at TEXT
);

-- One-time blob relay tokens (volume transfer). mode: 'put' | 'get'.
CREATE TABLE blob_tokens (
  token_hash TEXT PRIMARY KEY,
  blob_id    TEXT NOT NULL,
  mode       TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at    TEXT
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL -- encrypted for secret keys
);

CREATE TABLE audit_log (
  id         INTEGER PRIMARY KEY,
  actor      TEXT NOT NULL,
  action     TEXT NOT NULL,
  target     TEXT NOT NULL,
  detail     TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
