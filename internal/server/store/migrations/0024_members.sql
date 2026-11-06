-- Sub-accounts (notes/subaccounts.md). Every existing user stays an admin, so without
-- members nothing changes. A member sees only the servers assigned to it (and the
-- projects on them); permissions holds its api.Perm* list (admins ignore it).
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN permissions TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0;

CREATE TABLE server_assignments (
  server_id  TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY (server_id, user_id)
);
CREATE INDEX server_assignments_user ON server_assignments(user_id);
