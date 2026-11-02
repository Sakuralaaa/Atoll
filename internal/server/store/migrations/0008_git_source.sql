-- Builds from source: a service with a row here is built from Git on its own server.
-- token and webhook_secret are AES-GCM ciphertext (master.key), like secret env values.
CREATE TABLE service_git (
  service_id     TEXT PRIMARY KEY REFERENCES services(id) ON DELETE CASCADE,
  repo           TEXT NOT NULL,
  branch         TEXT NOT NULL DEFAULT 'main',
  context        TEXT NOT NULL DEFAULT '',
  dockerfile     TEXT NOT NULL DEFAULT 'Dockerfile',
  build_args     TEXT NOT NULL DEFAULT '{}',
  token          TEXT NOT NULL DEFAULT '',
  webhook_secret TEXT NOT NULL DEFAULT '',
  last_commit    TEXT NOT NULL DEFAULT '', -- last successful build
  last_image     TEXT NOT NULL DEFAULT '',
  last_node      TEXT NOT NULL DEFAULT '', -- server the last image was built on
  updated_at     TEXT NOT NULL DEFAULT ''
);

ALTER TABLE deployments ADD COLUMN commit_sha TEXT NOT NULL DEFAULT '';

-- Build output per deployment (only the newest lines of a long build are kept).
CREATE TABLE deployment_logs (
  deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
  seq           INTEGER NOT NULL,
  at            TEXT NOT NULL,
  line          TEXT NOT NULL,
  PRIMARY KEY (deployment_id, seq)
);
