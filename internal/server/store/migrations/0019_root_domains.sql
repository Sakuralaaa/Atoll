-- Multi-domain: a list of root domains replaces the single settings.base_domain.
-- settings.base_domain stays as a mirror of the default root domain's name so every
-- existing reader (and a downgrade) keeps working.
CREATE TABLE root_domains (
    name            TEXT PRIMARY KEY,           -- e.g. example.com (lower case, no trailing dot)
    zone_id         TEXT NOT NULL DEFAULT '',   -- Cloudflare zone (discovered by longest suffix match)
    zone_name       TEXT NOT NULL DEFAULT '',
    cert_mode       TEXT NOT NULL DEFAULT 'auto', -- 'global' (follow settings.tls_mode) | 'auto' (Origin CA, fall back to ACME) | 'acme'
    cert_status     TEXT NOT NULL DEFAULT '',   -- 'global' | 'origin' | 'acme' | '' (not decided yet)
    cert_error      TEXT NOT NULL DEFAULT '',   -- why the Origin CA certificate could not be issued
    cert_id         TEXT NOT NULL DEFAULT '',   -- Cloudflare Origin CA certificate id
    cert_expires_at TEXT NOT NULL DEFAULT '',
    origin_cert_pem TEXT NOT NULL DEFAULT '',   -- encrypted
    origin_key_pem  TEXT NOT NULL DEFAULT '',   -- encrypted
    is_default      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL
);

-- The existing single base domain becomes the default root domain and keeps following the
-- global TLS settings, so an upgraded install with one domain behaves exactly as before.
INSERT INTO root_domains(name, zone_id, cert_mode, cert_status, is_default, created_at)
SELECT lower(trim(value, ' .')),
       COALESCE((SELECT trim(value) FROM settings WHERE key = 'cloudflare_zone_id'), ''),
       'global', 'global', 1, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM settings WHERE key = 'base_domain' AND trim(value, ' .') != '';

-- Per-project default root domain ('' = the global default).
ALTER TABLE projects ADD COLUMN root_domain TEXT NOT NULL DEFAULT '';
