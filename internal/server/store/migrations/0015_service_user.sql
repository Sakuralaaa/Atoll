-- Container user override (docker run --user): uid[:gid] or a name; '' = the image's own USER.
-- Set by the Zeabur importer from the pod's securityContext.runAsUser (Zeabur runs most pods as root).
ALTER TABLE services ADD COLUMN run_as TEXT NOT NULL DEFAULT '';
