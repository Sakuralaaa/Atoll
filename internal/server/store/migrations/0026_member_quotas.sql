-- Sub-accounts B3: resource quotas per member (0 = no limit). A member without a row has no
-- quota, so existing members keep working exactly as before.
CREATE TABLE member_quotas (
  user_id  INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  cpu      REAL NOT NULL DEFAULT 0,
  mem_mb   INTEGER NOT NULL DEFAULT 0,
  services INTEGER NOT NULL DEFAULT 0
);
