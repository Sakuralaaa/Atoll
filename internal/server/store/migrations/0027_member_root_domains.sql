-- Sub-accounts B7: the configured root domains a member may put hostnames under (only
-- strict subdomains, never the root itself or a wildcard). A member without rows may use
-- none of them, so members upgraded from v0.1.40/41 keep the domains they have but can
-- only add hostnames outside the configured roots until an admin grants one.
CREATE TABLE member_root_domains (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  root    TEXT NOT NULL REFERENCES root_domains(name) ON DELETE CASCADE,
  PRIMARY KEY (user_id, root)
);
