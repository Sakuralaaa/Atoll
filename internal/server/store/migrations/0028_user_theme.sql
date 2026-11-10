-- Panel look B8: the account's preset theme id ("" = the panel default). The id is the
-- panel's own; unknown ids are kept so a newer panel's themes survive an older one.
ALTER TABLE users ADD COLUMN ui_theme TEXT NOT NULL DEFAULT '';
