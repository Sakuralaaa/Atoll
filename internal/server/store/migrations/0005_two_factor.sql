-- Login 2FA (TOTP) and session metadata.
-- users.totp_secret (from 0001) holds the encrypted, confirmed secret.
ALTER TABLE users ADD COLUMN totp_enabled    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_enabled_at TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_pending    TEXT    NOT NULL DEFAULT ''; -- encrypted secret awaiting confirmation
ALTER TABLE users ADD COLUMN totp_last_step  INTEGER NOT NULL DEFAULT 0;  -- last accepted time step (replay guard)

CREATE TABLE recovery_codes (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash  TEXT    NOT NULL,
  used_at    TEXT,
  created_at TEXT    NOT NULL,
  PRIMARY KEY (user_id, code_hash)
);

ALTER TABLE sessions ADD COLUMN ip         TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT '';
