-- Panel label of a service; '' = show its name. Unlike name it is not a hostname, so changing it
-- redeploys nothing.
ALTER TABLE services ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
