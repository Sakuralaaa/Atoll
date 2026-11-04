-- Panel-only label for a project; name stays the unique handle used by the CLI and tokens.
ALTER TABLE projects ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
