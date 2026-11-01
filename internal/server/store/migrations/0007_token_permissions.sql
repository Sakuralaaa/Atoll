-- Fine-grained API token permissions (comma-separated), optional project scope and
-- an opt-in "still ask in the panel" flag. Existing tokens keep what their scope allowed:
-- read → read; deploy → read,deploy; admin → everything.
ALTER TABLE api_tokens ADD COLUMN permissions      TEXT    NOT NULL DEFAULT '';
ALTER TABLE api_tokens ADD COLUMN projects         TEXT    NOT NULL DEFAULT '';
ALTER TABLE api_tokens ADD COLUMN require_approval INTEGER NOT NULL DEFAULT 0;
UPDATE api_tokens SET permissions = CASE scope
  WHEN 'read'   THEN 'read'
  WHEN 'deploy' THEN 'read,deploy'
  ELSE 'read,deploy,delete,migrate,domains,restore,servers,settings,tokens'
END;
