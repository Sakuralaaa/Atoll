-- 中控 / control: who did what from where (filled from the request context by Store.Audit)
ALTER TABLE audit_log ADD COLUMN ip TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN token_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
CREATE INDEX audit_log_action ON audit_log(action);
CREATE INDEX audit_log_actor ON audit_log(actor);
