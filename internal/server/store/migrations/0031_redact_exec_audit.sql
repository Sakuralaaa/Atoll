-- v0.1.45: the audit log no longer keeps the text of `service.exec` commands (it can carry
-- passwords and tokens); new rows store {program, args, shell, sha256}. Rows written before
-- held the full argv as a JSON array: drop the text, keep the row (who, when, which service).
UPDATE audit_log SET detail = '{"redacted":true}' WHERE action = 'service.exec' AND detail LIKE '[%';
-- The "zeabur" panel theme was renamed "lagoon"; the panel maps the old id too.
UPDATE users SET ui_theme = 'lagoon' WHERE ui_theme = 'zeabur';
