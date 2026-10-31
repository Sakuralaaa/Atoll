# Atoll API contract (v1)

Types: `internal/api/types.go` (Go) — the web app mirrors them in `web/src/api/types.ts`.
All JSON. Errors: non-2xx with `ErrorBody`.

## Components
- `atoll-server` (control node): REST API + embedded web UI (`web/dist` via `go:embed`), SQLite (modernc.org/sqlite, WAL), agent WebSocket hub, Cloudflare DNS, MCP-over-HTTP not required (MCP is in the CLI, stdio).
  Listens on `ATOLL_LISTEN` (default `:7300`). Data dir `ATOLL_DATA` (default `/var/lib/atoll-server`), secrets encrypted with AES-GCM key in `<data>/master.key`.
- `atoll-agent` (each worker VPS): connects out to the server over WebSocket, runs containers with the Docker Engine API, keeps desired state in `/var/lib/atoll/state/*.json` so it keeps working when the server is offline, configures a Caddy container (`atoll-caddy`) via its admin API for ingress, collects metrics.
- `atoll` CLI: talks to REST with a PAT; `atoll mcp` serves MCP over stdio; `atoll import zeabur` migrates from Zeabur.

## Auth
- Panel: `POST /api/v1/auth/login {username,password}` → httpOnly cookie `atoll_session`. `POST /api/v1/auth/logout`, `GET /api/v1/auth/me`.
  First run: if no user exists, `POST /api/v1/auth/setup {username,password}` creates the owner (only allowed while users table is empty). `GET /api/v1/auth/status` → `{setup_required: bool}`.
- API/CLI: `Authorization: Bearer atl_pat_...`. Scopes: `read` (GET only), `deploy` (everything except destructive), `admin`.
- Destructive actions with a PAT (delete service/project/server, migrate, add/delete domain) → `202 Confirmation`; approved in panel via `POST /api/v1/confirmations/{id}/approve|reject`. Session (panel) requests execute directly.

## REST endpoints (/api/v1)
| Method | Path | Body / notes |
|---|---|---|
| GET | /servers | []Server (with latest Usage) |
| POST | /servers/join-token | → JoinToken (valid 1h, single use) |
| GET/PATCH/DELETE | /servers/{id} | PATCH {name, provider, region} |
| GET | /servers/{id}/metrics?range=1h\|24h\|7d | []NodeMetrics |
| GET | /servers/{id}/containers | []ContainerState |
| GET/POST | /projects | POST {name, server_id, description} |
| GET/PATCH/DELETE | /projects/{id} | GET includes services |
| GET/POST | /projects/{id}/services | POST Service (kind+image or template kind) |
| GET/PATCH/DELETE | /services/{id} | PATCH partial Service → triggers redeploy |
| POST | /services/{id}/actions | {action: start\|stop\|restart\|redeploy} |
| POST | /services/{id}/rollback | {deployment_id} |
| POST | /projects/{id}/migrate | {server_id}: stop → volume export/import via server relay → start on target → DNS switch |
| GET/PUT | /services/{id}/env | []EnvVar (PUT replaces all; secret values "" keep old) ; `?reveal=1` shows secrets (session only) |
| GET | /services/{id}/env/references | []EnvVar exposed by other services in project |
| GET/POST | /services/{id}/domains | POST {hostname, port_name, proxied} |
| DELETE | /domains/{id} | |
| GET | /domains | all domains |
| GET | /services/{id}/deployments | []Deployment |
| GET | /services/{id}/metrics?range= | []ContainerMetrics |
| GET (WS) | /services/{id}/logs?tail=200&follow=1 | stream of LogLine JSON frames |
| POST | /services/{id}/exec | {cmd: []string} → ResultPayload |
| GET | /image-updates?available=1 | []ImageUpdate for every service (see "Image updates") |
| GET | /services/{id}/image-update | ImageUpdate |
| POST | /services/{id}/image-update/check | ask the registry now → ImageUpdate |
| PUT | /services/{id}/image-update/policy | ImageUpdatePolicy {policy: notify\|auto\|off, window: "HH:MM-HH:MM" UTC} → ImageUpdate |
| POST | /services/{id}/image-update/apply | ImageUpdateApplyRequest {tag?} → Deployment (trigger "image-update") |
| GET/PUT | /services/{id}/backup-policy | BackupPolicy (see "Backups"); PUT accepts partial bodies |
| GET | /services/{id}/backups?refresh=1 | []Backup, newest first; `refresh=1` reconciles with the repository first |
| POST | /services/{id}/backups | back up now → 202 Backup (status "running") |
| GET | /backups/{id} | Backup (id = `bak_...` or a restic snapshot id, full or 8-char) |
| POST | /backups/{id}/restore | BackupRestoreRequest {target_server_id?} → RestoreJob (session) or 202 Confirmation (API token) |
| GET | /services/{id}/restores, /restores/{id} | []RestoreJob / RestoreJob |
| POST | /settings/backup/test | admin → BackupTestResult (an agent checks / initialises the repository) |
| GET | /settings/backup/password | session only → the restic repository password |
| DELETE | /settings/backup | admin: clears the target (the repository password is kept) |
| GET/POST/DELETE | /tokens, /tokens/{id} | APIToken |
| GET | /confirmations?status=pending | |
| POST | /confirmations/{id}/approve, /reject | session only |
| GET/PUT | /settings | Settings |
| GET/POST | /imports | POST ImportPlan → ImportJob (creates project+services, not started) |
| GET | /imports/{id} | ImportJob |
| PUT | /imports/{id}/volumes/{service_name}/{volume} | raw tar.gz body, streamed to target agent via volume.import |
| POST | /imports/{id}/start | deploy all services |
| GET | /templates | db templates (postgres, mysql, redis, mongo) |
| GET | /events (WS) | live updates {type: "service"|"server"|"deployment"|"confirmation"|"image_update"|"backup"|"restore", payload} |

## Agent endpoints
- `POST /agent/v1/join` JoinRequest → JoinResponse
- `GET  /agent/v1/connect` (WebSocket, `Authorization: Bearer <node_token>`) — Envelope frames, see types.go.
- `PUT  /agent/v1/blobs/{token}` / `GET /agent/v1/blobs/{token}` — volume tar.gz relay for migration/import (one-time tokens, stored in `<data>/blobs`, deleted after import).

## Conventions
- IDs: 12-char lowercase base32 with prefix: `srv_`, `prj_`, `svc_`, `dep_`, `dom_`, `tok_`, `cfm_`, `imp_`, `bak_`, `rst_`.
- Docker: container name `atoll-<service_id>`, labels `atoll.service=<id>`, `atoll.project=<id>`, `atoll.deployment=<id>`; network per project `atoll-<project_id>` with alias = service name; every container with an http port also joins `atoll-ingress`, where `atoll-caddy` runs. Restart policy `unless-stopped`.
- Volumes: bind mounts under `/var/lib/atoll/volumes/<service_id>/<volume_name>`.
- Env references: `${KEY}` resolves own vars then exposed vars of other services in project; `${SERVICE.KEY}` for explicit; built-ins: `${<SERVICE>_HOST}` (service name), `${<PORTNAME>_PORT}`, `${ATOLL_<PORTNAME>_DOMAIN}`, `${ATOLL_<PORTNAME>_URL}`, `PORT` = first port, `PASSWORD` random 32 chars for db templates.
- Times UTC RFC3339.

## In-place Zeabur takeover (primary import path)
Target user: their OWN VPS registered to Zeabur (unmanaged, single-node k3s), services built by Zeabur, data on that VPS. Atoll takes the same machine over — no server-to-server move.
1. Install atoll-agent on that VPS (join command). Agent detects k3s (`k3s kubectl`), `GET /servers/{id}/zeabur` shows Zeabur projects/services found locally (types `ZeaburInventory`).
2. `POST /imports/zeabur-local` (`domain_map: {old: new}`; `old: ""` drops a host) creates an Atoll project on that server with services mapped 1:1: env from pod spec (expanded), `<x>.zeabur.internal` → service name, Zeabur built-ins translated, ZEABUR_* ids dropped, ports, volumes, configmaps, custom domains (zeabur.app → warning).
3. Rehearse: agent exports Zeabur-built images from containerd (`k3s ctr -n k8s.io images export` | `docker load`, tag `atoll-import/<svc>:<digest12>`); registry images (Docker Hub, GHCR...) are pulled by their original ref with the tag kept (`zeabur.image` with `pull: true`), falling back to the containerd copy tagged with that same ref when the pull fails; copies PVC dirs locally (apps) and dump/restore DBs (same major version), starts containers WITHOUT public ingress, verifies row counts. Zeabur keeps serving.
4. Cutover (user confirms): scale Zeabur app workloads to 0 (DBs keep running until final dump), final DB sync + volume rsync, scale Zeabur's operator (operator-system) to 0 so it does not revert the park, park Zeabur ingress-controller DaemonSet, start atoll-caddy on 80/443 with the same hostnames. DNS unchanged (same IP) — custom domain A records already point here; only *.zeabur.app URLs die. DNS provider optional: every *.zeabur.app host needs a replacement (explicit `domain_map` entry or `base_domain`); with a Cloudflare token the cutover creates the A records, without one hosts that already resolve to the server go active and the rest stay `pending` with the record to create (the cutover still succeeds). A cutover after a rollback starts the stopped Atoll services again first.
5. Rollback: stop atoll services, restore ingress-controller and the operator, scale Zeabur workloads back to recorded replicas.
6. Later, after user approval: remove Zeabur components / k3s (`/usr/local/bin/k3s-uninstall.sh`) to free RAM — explicit confirmation, never automatic.
Risk: both stacks run side by side during rehearsal (RAM/disk) — scan reports free mem/disk; refuse rehearsal if disk free < 1.2× data size.

## Image updates
Server-side, no agent changes. `internal/server/registry` (OCI distribution API, no deps) + `internal/server/imageupdates.go`.
- Schedule: a loop ticks every minute and checks services whose last check is older than `Settings.image_check_interval_hours` (default 6, 1–168). Checks run one at a time. `POST .../image-update/check` checks now.
- Check: parse the service image (Docker Hub `nginx:1.27` → `registry-1.docker.io/library/nginx`), `HEAD /v2/<repo>/manifests/<tag>` with Accept = OCI index, Docker manifest list, OCI manifest, Docker v2 manifest; read `Docker-Content-Digest` (GET + sha256 only if a registry omits the header). Auth: answer the 401 challenge — Bearer (anonymous token from `realm?service=&scope=repository:<repo>:pull`, Docker Hub/GHCR/most registries; basic auth on the token request when the service has `registry_cred_id`) or Basic. Tokens cached until `expires_in`.
- Semver-like tags (`[v]X[.Y[.Z]][-suffix]`) also list tags (`/tags/list`, Link pagination, ≤10 pages) and report `newer_tag` (newest higher with the same major and the same shape/suffix: `1.27-alpine` only compares with `X.Y-alpine`) and `major_tag` (newest higher major, shown separately, does not set `update_available`). A one-part tag (`16`) floats inside its major, so only major bumps are reported.
- Rate limits: HEAD only (does not count against Docker Hub pulls), digests cached 10 min and tag lists 30 min per repo, a 429 puts that registry host in backoff (Retry-After, else 1m doubling to 1h) during which no request is sent. A failed check retries after 5m doubling up to the interval.
- Compare: `current_digest` = agent-reported `ContainerState.image_digest` (repo digest; fallback last running deployment's digest). Compared at read time, so a finished deploy flips the status without another registry call. Status: `up_to_date | update_available | unknown | error | unsupported | disabled`. `unsupported`: `atoll-import/...` local images and digest-only refs.
- Policy per service (`image_updates` table, migration 0004): `notify` (default), `auto`, `off`. `auto` redeploys only a new digest of the same tag (never a tag bump), pinned as `name@sha256:...` in the deployment spec while the service keeps its tag ref, and only inside the optional daily UTC window. Each digest is auto-deployed at most once (`last_auto_digest`). Auto-update pauses after a rollback (latest deployment trigger `rollback`) until an update is applied manually, and skips while a deployment is in progress or the server is offline.
- Apply: `{}` → current tag pinned to its fresh digest; `{tag}` → validates the tag in the registry, switches the service image to `name:tag` and deploys it pinned. Every apply is a normal deployment, so `POST /services/{id}/rollback` returns to the previous one; a rollback is pinned to the digest the old deployment reported (`name@sha256:...`) because its tag may have moved since. If a service image is digest-only after rolling back to such a deployment, the checker keeps following the tag it recorded.
- Events: `{type: "image_update", payload: ImageUpdate}` after each check, policy change and apply.
- CLI: `atoll updates [--available] [-p PROJECT]`, `atoll updates --check SERVICE`, `atoll updates --apply SERVICE [--tag TAG]`, `atoll updates policy SERVICE notify|auto|off [--window 02:00-05:00]`. MCP tool `list_updates` (read-only).

## Backups
Restic to any S3-compatible bucket (Cloudflare R2, Backblaze B2, AWS S3, MinIO). Server: `internal/server/backups.go`; agent: `internal/agent/backup`. Types at the end of `internal/api/types.go`.

**Data path.** Backup data never transits the control server. Each agent runs `restic` locally in a container (`docker run --rm restic/restic:<pinned>`) and talks to the bucket directly. Database templates (postgres, mysql, mongo, redis) are dumped with the database's own tool inside the running container and streamed into `restic backup --stdin` (`part=dump`); volume dirs (`/var/lib/atoll/volumes/<service_id>/...`) are bind-mounted read-only and snapshotted (`part=volumes`). Snapshots are tagged `atoll`, `svc=<service_id>`, `part=dump|volumes`, `project=<project_id>`; retention is `restic forget --group-by tags` per service and part.

**Offline control.** The agent persists its repository settings and every policy of its node (`<agent data>/backup/config.json`) and runs the schedule itself, so backups continue while the control server is offline. Results are `backup` events that are queued while the agent is disconnected and delivered on reconnect. The server's scheduler is only a watchdog: it re-pushes the config and asks for a catch-up run (`trigger: "catchup"`, skipped when the agent already ran since) when a scheduled run is more than ~20 min overdue.

**Repository settings** (`PUT /settings`, write-only secrets like the Cloudflare token): `backup_endpoint` (host or URL, no path), `backup_bucket`, `backup_prefix` (default `atoll`), `backup_region` (`auto` for R2), `backup_access_key`, `backup_secret_key`, `backup_repo_password`. When no repository password is stored, the first save generates one and returns it once in the PUT response; `GET /settings/backup/password` (session only) shows it again. Store it somewhere safe: without it the snapshots are unreadable. `GET /settings` reports `has_backup_credentials`, `has_backup_password`, `backup_configured`.

**Shapes.**
- `BackupPolicy` `{service_id, enabled, schedule, timezone, keep: {daily, weekly, monthly}, include_volumes, updated_at?, next_run_at?, repo_configured, parts}`. `schedule` is 5-field cron (default `0 3 * * *`) read in `timezone` (IANA, default the control server's zone). Default keep 7/4/6; at least one must be > 0 and each ≤ 1000. `include_volumes` adds the volume dirs to a database's dump; for non-database services the volumes are the backup (forced on). `next_run_at`, `repo_configured`, `parts` are computed. PUT starts from the current policy, so partial bodies work (a body with `keep` replaces all three values).
- `Backup` `{id: "bak_...", service_id, server_id, trigger: schedule|manual|catchup|imported, status: running|done|failed|pruned, snapshots: [BackupSnapshot], bytes, added, duration_ms, error?, warnings?, started_at, finished_at?}`.
- `BackupSnapshot` `{id (full restic id), part: dump|volumes, bytes (logical size), added (new data after dedup + compression), pruned?}`.
- `BackupRestoreRequest` `{target_server_id?}`; `RestoreJob` `{id: "rst_...", backup_id, service_id, project_id, from_server_id, to_server_id, status: running|done|failed, steps: [ImportStep], error?, created_at, finished_at?}`.
- `BackupTestResult` `{ok, server_id, message}`.
- `POST /services/{id}/backups` errors: 409 `backup_not_configured`, 409 `server_offline`, 400 when the service has no volumes and is not a database template, 502 `agent_error`.

**Agent messages** (Envelope frames, see types.go):
- server → agent: `backup.config` (BackupConfigPayload: repo or null + every target of this node; the agent persists it and schedules locally), `backup.run` (BackupRunPayload; result Output `queued` | `skipped`), `backup.restore` (BackupRestorePayload, blocking; result Output = JSON BackupRestoreResult `{restored, bytes, warnings?, moved_aside?}`), `backup.snapshots` (BackupSnapshotsPayload; result Output = JSON []ResticSnapshot), `backup.test` (BackupRepo; checks and initialises the repository).
- agent → server: `backup` (BackupEvent: status `running`, then `done` | `failed`, with snapshots, sizes, warnings and `pruned` snapshot ids removed by retention).

**Restore semantics.** Restore replaces data, so it goes through confirmation: with an API token (CLI, MCP) `POST /backups/{id}/restore` only creates a pending Confirmation (action `backup.restore`) that the user approves in the panel; a panel session starts the RestoreJob directly. Only `done` backups with at least one unpruned snapshot are restorable.
- In place (no target, or the service's own server): the agent stops the container and moves the current volume dir aside as a pre-restore safety copy (one generation, `<agent data>/backup/pre-restore/<service_id>`, reported in the restore step detail). Databases are re-initialised empty by their image and loaded from the dump; other services get their volume dirs restored. If the restore fails, the previous data is put back and the container restarted.
- Onto another server (disaster recovery): allowed only while the project's server is offline (409 `source_online` otherwise; use `migrate` for a live move). The whole project is reassigned to the target, every service is created there from its stored config, then this one service's data is restored, ingress synced and DNS switched. If creating the services fails, the project stays on its original server.

**CLI / MCP.** `atoll backup list SERVICE [--refresh]`, `atoll backup run SERVICE`, `atoll backup restore SNAPSHOT [--to SERVER]` (SNAPSHOT = `bak_...` or restic snapshot id; exits 3 while the confirmation is pending, `--wait` waits for approval), `atoll backup policy SERVICE [--enable|--disable] [--schedule CRON] [--timezone TZ] [--keep-daily N --keep-weekly N --keep-monthly N]` (no flags shows the policy). SERVICE is a name, `svc_...` or `project/name` (or `-p PROJECT`). MCP tools: `backup_list` (read-only), `backup_run`, `backup_restore` (only ever creates a pending confirmation; never bypassed).

**Limitations.**
- The control server's own database and `master.key` are not backed up. Copy the control server's data dir (`ATOLL_DATA`, `/var/lib/atoll/` with the installer, which contains `master.key`) off the box yourself; without `master.key` the stored secrets cannot be decrypted. Keep the restic repository password too.
- Postgres dumps (`pg_dumpall --clean --if-exists`) are restored with `psql` with `ON_ERROR_STOP` off: individual statement errors do not abort the restore. Check the restore step detail and the database afterwards.
- Redis assumes RDB persistence with data in `/data`: the dump is an RDB file (`redis-cli --rdb`) written back as `/data/dump.rdb`, and an existing AOF is removed so it does not win over the restored RDB.
- Cross-server recovery restores one service per job; restore the project's other services from their own backups.
- The agent host needs the docker CLI (restic and the dump tools run in containers).
