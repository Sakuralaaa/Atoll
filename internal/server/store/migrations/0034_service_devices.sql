-- v0.1.50: host devices (docker --device, e.g. /dev/kvm) and /dev/shm size per service.
-- Only new columns with defaults: existing services keep running unchanged.
ALTER TABLE services ADD COLUMN devices TEXT NOT NULL DEFAULT '[]';
ALTER TABLE services ADD COLUMN shm_size_mb INTEGER NOT NULL DEFAULT 0;
