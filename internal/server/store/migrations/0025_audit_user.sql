-- Sub-accounts B6: who did it. Every authenticated request now records the user behind it
-- (an admin or member, through its session or one of its API tokens). Older rows keep 0/''
-- and the API derives the name from actor "user:<name>" where it can.
ALTER TABLE audit_log ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE audit_log ADD COLUMN user_name TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN user_role TEXT NOT NULL DEFAULT '';
CREATE INDEX audit_log_user ON audit_log(user_id);
