-- Manual service order inside a project (panel sidebar drag-and-drop). 0 = unordered;
-- services are listed by position, then name, so new services land at the end of the
-- ordered ones only once the order is saved again.
ALTER TABLE services ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
