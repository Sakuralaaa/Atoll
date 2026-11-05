-- Manual favorite order (panel sidebar 收藏, drag to reorder). 0 = not a favorite;
-- 1.. = position among the account's favorites. Existing favorites keep the order they
-- were listed in before (by project id).
ALTER TABLE project_prefs ADD COLUMN fav_order INTEGER NOT NULL DEFAULT 0;
UPDATE project_prefs SET fav_order = (
    SELECT COUNT(*) FROM project_prefs p2
    WHERE p2.user_id = project_prefs.user_id AND p2.favorite = 1 AND p2.project_id <= project_prefs.project_id
) WHERE favorite = 1;
