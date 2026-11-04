-- Per-account project favorites and pins (panel 我的项目), synced across devices.
-- pin_order: 0 = not pinned, 1.. = position among the pinned projects.
CREATE TABLE project_prefs (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id TEXT    NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    favorite   INTEGER NOT NULL DEFAULT 0,
    pin_order  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, project_id)
);
