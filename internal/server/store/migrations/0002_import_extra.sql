-- Visible job fields of in-place takeovers (namespace, verify) as plain JSON (no secrets).
ALTER TABLE imports ADD COLUMN extra TEXT NOT NULL DEFAULT '{}';
