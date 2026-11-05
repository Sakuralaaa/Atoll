-- Per-account panel look: an uploaded avatar (a small data: URL the panel resized to
-- 128x128, '' = initials) and the order of the sidebar navigation ('' = default order).
ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN nav_order TEXT NOT NULL DEFAULT '';
