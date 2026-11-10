-- Builds from any git server and without a Dockerfile (docs/api.md "Builds from source").
-- ssh_key is AES-GCM ciphertext (master.key) like token; detected is JSON (api.GitDetected).
ALTER TABLE service_git ADD COLUMN provider TEXT NOT NULL DEFAULT 'github';
ALTER TABLE service_git ADD COLUMN username TEXT NOT NULL DEFAULT '';
ALTER TABLE service_git ADD COLUMN ssh_key TEXT NOT NULL DEFAULT '';
ALTER TABLE service_git ADD COLUMN known_hosts TEXT NOT NULL DEFAULT '';
ALTER TABLE service_git ADD COLUMN poll_interval_sec INTEGER NOT NULL DEFAULT 0;
ALTER TABLE service_git ADD COLUMN builder TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE service_git ADD COLUMN stack TEXT NOT NULL DEFAULT '';
ALTER TABLE service_git ADD COLUMN install_cmd TEXT NOT NULL DEFAULT '';
ALTER TABLE service_git ADD COLUMN build_cmd TEXT NOT NULL DEFAULT '';
ALTER TABLE service_git ADD COLUMN start_cmd TEXT NOT NULL DEFAULT '';
ALTER TABLE service_git ADD COLUMN detected TEXT NOT NULL DEFAULT '';
ALTER TABLE service_git ADD COLUMN last_poll_at TEXT NOT NULL DEFAULT '';
