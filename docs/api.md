# Atoll API contract (v1)

Types: `internal/api/types.go` (Go) — the web app mirrors them in `web/src/api/types.ts`.
All JSON. Errors: non-2xx with `ErrorBody`.

## Components
- `atoll-server` (control node): REST API + embedded web UI (`web/dist` via `go:embed`), SQLite (modernc.org/sqlite, WAL), agent WebSocket hub, Cloudflare DNS, MCP-over-HTTP not required (MCP is in the CLI, stdio).
  Listens on `ATOLL_LISTEN` (default `:7300`). Data dir `ATOLL_DATA` (default `/var/lib/atoll-server`), secrets encrypted with AES-GCM key in `<data>/master.key`.
- `atoll-agent` (each worker VPS): connects out to the server over WebSocket, runs containers with the Docker Engine API, keeps desired state in `/var/lib/atoll/state/*.json` so it keeps working when the server is offline, configures a Caddy container (`atoll-caddy`) via its admin API for ingress, collects metrics.
- `atoll` CLI: talks to REST with a PAT; `atoll mcp` serves MCP over stdio; `atoll import zeabur` migrates from Zeabur.

## Auth
- Panel: `POST /api/v1/auth/login {username,password}` → httpOnly cookie `atoll_session` (`Secure` behind HTTPS, `SameSite=Strict`, 30 days). `POST /api/v1/auth/logout`, `GET /api/v1/auth/me`.
  First run: if no user exists, `POST /api/v1/auth/setup {username,password}` creates the owner (only allowed while users table is empty). `GET /api/v1/auth/status` → `{setup_required: bool}`.
- API/CLI: `Authorization: Bearer atl_pat_...`. Scopes: `read` (GET only), `deploy` (everything except destructive), `admin`. Tokens never need a second factor.
- Destructive actions with a PAT (delete service/project/server, migrate, add/delete domain) → `202 Confirmation`; approved in panel via `POST /api/v1/confirmations/{id}/approve|reject`. Session (panel) requests execute directly.

### Two-factor login (TOTP)
RFC 6238: HMAC-SHA1, 30 s, 6 digits, ±1 step accepted; each step is accepted once (replay guard). The secret is AES-GCM encrypted with `master.key`; the 10 one-time recovery codes are stored as SHA-256 hashes.

Login with 2FA on is two steps:
1. `POST /auth/login {username,password}` → `200 LoginChallenge {two_factor_required:true, pending_token, expires_at}` (no cookie yet; valid 5 min, 5 code attempts).
2. `POST /auth/login/2fa {pending_token, code}` → session cookie + `{username,...}`. `code` is the 6-digit TOTP or a recovery code (`xxxxx-xxxxx`, case and dashes ignored). Errors: `401 invalid_code`, `401 challenge_expired` (start again with the password).

Management (panel session only; PATs get 403):
| Method | Path | Body → answer |
|---|---|---|
| GET | /auth/2fa | → TwoFactorStatus `{enabled, enabled_at, recovery_codes_left}` |
| POST | /auth/2fa/enroll | `{password}` → TwoFactorEnrollment `{secret, otpauth_uri}` (pending until confirmed; 409 if already on) |
| POST | /auth/2fa/enable | `{code}` → RecoveryCodes `{recovery_codes: [10]}`; signs out every other session |
| POST | /auth/2fa/recovery-codes | `{password, code}` (TOTP) → new RecoveryCodes; old ones stop working |
| POST | /auth/2fa/disable | `{password, code}` (TOTP or recovery code) → 204; signs out every other session. With 2FA off it cancels a pending enrollment |
| GET | /auth/sessions | → []SessionInfo `{id, ip, user_agent, current, created_at, expires_at}` |
| DELETE | /auth/sessions/{id} | sign out one session |
| POST | /auth/sessions/revoke-others | → `{revoked: n}` |

Lost authenticator: on the control host run `atoll-server admin reset-2fa [--data DIR] <user>` — turns 2FA off, deletes the recovery codes and signs the user out everywhere.

### Brute-force protection
Failed password, TOTP and recovery-code attempts (login and the re-auth checks above) are counted per client IP (IPv6 per /64) and per account name. IP: 5 free failures, then a lock of 30 s doubling per further failure up to 1 h. Account: 10 free, then 30 s doubling up to 15 min. Locked requests get `429 rate_limited` with `Retry-After` — even with the right password. Counters are forgotten 24 h after the last failure and reset by a full successful login. Every failure is logged (`WARN login failed user=… ip=… reason=…`).

The client IP is the TCP peer, except when the peer is a trusted proxy: then it is the right-most `X-Forwarded-For` entry that is not itself trusted. Trusted proxies default to loopback (Caddy from `install-server.sh --domain`); set `--trusted-proxies` / `ATOLL_TRUSTED_PROXIES` (comma-separated IPs/CIDRs, or `none`) for another setup.

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
| POST | /services/{id}/actions | {action: start\|stop\|restart\|redeploy} (redeploy → Deployment) |
| POST | /services/{id}/rollback | {deployment_id} |
| POST | /projects/{id}/migrate | {server_id}: stop → volume export/import via server relay → start on target → DNS switch |
| GET/PUT | /services/{id}/env | []EnvVar (PUT replaces all; secret values "" keep old) ; `?reveal=1` shows secrets (session only) |
| GET | /services/{id}/env/references | []EnvVar exposed by other services in project |
| GET/POST | /services/{id}/domains | POST {hostname, port_name, proxied}. Status: with a Cloudflare token the record is created first and the domain is `provisioning` (route held back from Caddy until the name resolves publicly, ≤90s; then `active` once HTTPS answers, ≤3 min, with a note in `error` if it never did); without a token `active` when the name already resolves to the server, else `pending` with the record to create; `error` when the DNS API failed |
| DELETE | /domains/{id} | removes the route from Caddy first, then the DNS record (on a DNS error the route is restored and the domain stays bound) |
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
| GET | /templates | []Template: db templates (postgres, mysql, redis, mongo); create one with POST /projects/{id}/services `{kind, name, env?}` |
| GET | /events (WS) | live updates {type: "service"|"server"|"deployment"|"confirmation"|"image_update"|"backup"|"restore", payload} |

## Agent endpoints
- `POST /agent/v1/join` JoinRequest → JoinResponse
- `GET  /agent/v1/connect` (WebSocket, `Authorization: Bearer <node_token>`) — Envelope frames, see types.go.
- `PUT  /agent/v1/blobs/{token}` / `GET /agent/v1/blobs/{token}` — volume tar.gz relay for migration/import (one-time tokens, stored in `<data>/blobs`, deleted after import).
- `POST /agent/v1/bootstrap` / `GET /agent/v1/network` / `GET /agent/v1/ping` — private network, see [Private network (Tailscale)](#private-network-tailscale).

## Private network (Tailscale)
The panel stays on its public HTTPS URL. Machine-to-machine traffic — agent ↔ control server and service ↔ service across servers — can go over a Tailscale tailnet instead.

**Who needs Tailscale.** The control server and the worker nodes only. Your own PCs do not: the panel, the `atoll` CLI and MCP keep using the public URL. If a PC of yours does run Tailscale next to Clash/Mihomo in TUN mode, add `IP-CIDR,100.64.0.0/10,DIRECT` before the final rule so tailnet traffic is not sent to the proxy.

**Tailscale admin console, once.**
- ACL policy (merge into yours; the default allow-all policy already permits this):
  ```json
  {
    "tagOwners": { "tag:atoll": ["autogroup:admin"] },
    "acls": [
      { "action": "accept", "src": ["tag:atoll"], "dst": ["tag:atoll:7301,41000-41999"] }
    ]
  }
  ```
  7301 is the control server's agent listener (`ATOLL_AGENT_LISTEN`), 41000–41999 the cross-server service ports (`ATOLL_MESH_PORTS`).
- Auth key (Settings → Keys → Generate auth key): **reusable**, **not ephemeral** (nodes must survive restarts), **pre-approved**, tag **`tag:atoll`**. Tagged nodes do not expire; the key's own expiry only limits when new nodes can join.

**Control server.** `install-server.sh --tailscale-authkey tskey-auth-... [--agent-tailnet-only]` installs Tailscale (official install script, else the official apt repo), runs `tailscale up --hostname=atoll-control --ssh=false --accept-dns=false --accept-routes=false` (skipped when the host already is on a tailnet), writes `ATOLL_AGENT_LISTEN=tailscale:7301` (and `ATOLL_AGENT_TAILNET_ONLY=1`) to `/etc/atoll/server.env`, orders the unit after `tailscaled.service` and checks `http://<tailnet IP>:7301/agent/v1/ping`. Then in the panel, **Settings → Private network**: provider Tailscale, paste the same auth key (stored encrypted with `master.key`, never returned or logged). The tailnet control URL defaults to `http://<control tailnet IP>:7301`; if you set it by hand it must be the **100.x IP**, not a MagicDNS name — workers run `--accept-dns=false`.

**Worker nodes.** The usual `install-agent.sh --server https://panel... --token atl_join_...` command:
1. `POST /agent/v1/bootstrap?format=env` with the join token (checked, not consumed);
2. if the provider is Tailscale: installs Tailscale, `tailscale up --authkey=… --hostname=atoll-<hostname> --ssh=false --accept-dns=false --accept-routes=false` (skipped when already up; the key is never printed), waits for `<control tailnet URL>/agent/v1/ping`;
3. joins through the tailnet URL and keeps `--server` in `fallback_urls`. If any tailnet step fails it warns and joins over `--server`; move it later with **Servers → Join tailnet** or `atoll-agent tailnet up`;
4. orders `atoll-agent.service` after `tailscaled.service`. Re-running the installer with either URL counts as already joined.
Flags: `--tailscale-authkey KEY` (use this key instead of the server's), `--tailscale-hostname NAME`, `--no-tailnet`.

`--ssh=false --accept-dns=false --accept-routes=false`: Atoll never enables Tailscale SSH, never rewrites the host's DNS and never installs subnet routes, so joining cannot break the host's existing networking.

**Endpoints.**
- `POST /agent/v1/bootstrap` `{join_token}` → BootstrapResponse `{network_provider, tailscale_auth_key?, tailnet_name?, control_url?, public_url}`. With `?format=env`: `KEY=value` lines `ATOLL_NETWORK`, `ATOLL_TS_AUTHKEY`, `ATOLL_TAILNET`, `ATOLL_CONTROL_URL`, `ATOLL_PUBLIC_URL`. 401 `invalid_join_token`. Always public.
- `GET /agent/v1/network` (node token) — the same, for `atoll-agent tailnet up`.
- `GET /agent/v1/ping` → `{ok: true}`; with a node token `{ok: true, node_id}` (401 for a bad token). Agents verify a new control URL with it before switching.

**Tailnet-only agents.** `ATOLL_AGENT_TAILNET_ONLY=1` makes the public listener answer `/agent/*` with 404 `tailnet_only`, except `POST /agent/v1/bootstrap`. Do the same in front of it: `install-server.sh --domain … --agent-tailnet-only` writes this into its Caddy site; with your own Caddy add
```
@agent {
	path /agent/*
	not path /agent/v1/bootstrap
}
respond @agent 404
```
Nodes that are not on the tailnet then cannot connect, so move every node first.

## Conventions
- IDs: 12-char lowercase base32 with prefix: `srv_`, `prj_`, `svc_`, `dep_`, `dom_`, `tok_`, `cfm_`, `imp_`, `bak_`, `rst_`.
- Docker: container name `atoll-<service_id>`, labels `atoll.service=<id>`, `atoll.project=<id>`, `atoll.deployment=<id>`; network per project `atoll-<project_id>` with alias = service name (the agent removes it when the project's last service on that node is removed, unless a container is still attached); every container with an http port also joins `atoll-ingress`, where `atoll-caddy` runs. Restart policy `unless-stopped`.
- Deployments of one service are applied one at a time. A deploy requested while another is in flight (e.g. `set_env` then `redeploy` right away) is queued and sent when the in-flight one finishes; of several queued requests only the newest is sent, the ones it replaced become `superseded` without running. A finished deployment supersedes only OLDER ones, never a newer queued one.
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

**CLI / MCP.** `atoll backup list SERVICE [--refresh]`, `atoll backup run SERVICE`, `atoll backup restore SNAPSHOT [--to SERVER]` (SNAPSHOT = `bak_...` or restic snapshot id; needs the `restore` permission and runs immediately; only a token with `require_approval` gets a pending confirmation — the CLI then exits 3, `--wait` waits for approval), `atoll backup policy SERVICE [--enable|--disable] [--schedule CRON] [--timezone TZ] [--keep-daily N --keep-weekly N --keep-monthly N]` (no flags shows the policy). SERVICE is a name, `svc_...` or `project/name` (or `-p PROJECT`). MCP tools: `backup_list` (read-only), `backup_run`, `backup_restore` (`restore` permission; runs immediately unless the token requires approval, a pending confirmation is never bypassed).

**Limitations.**
- The control server's own database and `master.key` are not backed up. Copy the control server's data dir (`ATOLL_DATA`, `/var/lib/atoll/` with the installer, which contains `master.key`) off the box yourself; without `master.key` the stored secrets cannot be decrypted. Keep the restic repository password too.
- Postgres dumps (`pg_dumpall --clean --if-exists`) are restored with `psql` with `ON_ERROR_STOP` off: individual statement errors do not abort the restore. Check the restore step detail and the database afterwards.
- Redis assumes RDB persistence with data in `/data`: the dump is an RDB file (`redis-cli --rdb`) written back as `/data/dump.rdb`, and an existing AOF is removed so it does not win over the restored RDB.
- Cross-server recovery restores one service per job; restore the project's other services from their own backups.
- The agent host needs the docker CLI (restic and the dump tools run in containers).

## CLI & MCP permissions
The CLI and `atoll mcp` act with the API token from `atoll login` (or `$ATOLL_URL` / `$ATOLL_TOKEN`). What they may do is the token's permission set (`read`, `deploy`, `delete`, `migrate`, `domains`, `restore`, `servers`, `settings`, `tokens`), optionally limited to projects. Without a permission the server answers `403 missing_permission` and the CLI names the permission to grant (`atoll token edit TOKEN --add-perm PERM`); with it the operation runs directly. Only a token created with `require_approval` gets `202 Confirmation` for destructive operations (CLI exit code 3, `--wait` waits for the panel approval). `403 escalation` means a token tried to hand out more than it holds; `403 project_scope` that the token is limited to other projects.

**Tokens.** `atoll token ls`, `atoll token create NAME [--perm read,deploy,...] [--project PROJECT]... [--require-approval]` (default `read`; `--perm all` for every permission; `servers`/`settings`/`tokens` cannot be combined with `--project`; the secret is printed once), `atoll token edit TOKEN [--name N] [--perm ...|--add-perm ...|--rm-perm ...] [--project P...|--all-projects] [--require-approval[=false]]`, `atoll token rm TOKEN` (TOKEN = id, name or prefix). All need the `tokens` permission.

**Placement & network.** `atoll deploy IMAGE -p PROJECT --server SERVER` places a new service on another server (`POST /projects/{id}/services` with `server_id`); `atoll services migrate SERVICE --to SERVER [-p PROJECT]` moves one service (`POST /services/{id}/migrate`, `migrate` permission); `atoll migrate PROJECT --to SERVER` moves a whole project. `atoll services rm SERVICE [-p PROJECT]` and `atoll projects rm PROJECT` delete (`delete` permission). `atoll servers network join|switch|public SERVER` runs `POST /servers/{id}/network` (`servers` permission).

**Database templates.** `atoll templates ls` (`GET /templates`, `read`) lists postgres, mysql, redis and mongo with their exposed variables. `atoll deploy --template postgres|mysql|redis|mongodb [NAME] -p PROJECT [-e K=V] [--server SERVER]` (same as `atoll templates deploy TEMPLATE [NAME] -p PROJECT`) creates a NEW service of that kind (`POST /projects/{id}/services` with `kind`, `name` and optional `env` overriding template keys; `deploy` permission). NAME defaults to the kind; an existing service with that name is an error, never overwritten. MCP: `list_templates`, `deploy_template`.

**MCP.** At startup `atoll mcp` reads `/auth/me` and only registers the tools the token's permissions allow (servers that predate permissions: mapped from the legacy scope; when `/auth/me` fails every tool is registered and calls report the error):

| Permission | Tools |
|---|---|
| read | `list_servers`, `list_projects`, `list_services`, `get_logs`, `get_deployment`, `zeabur_scan`, `zeabur_import_status`, `list_updates`, `backup_list`, `list_templates` |
| deploy | `deploy` (`domains` also needs `domains`; `server` places a new service), `deploy_template` (new database service from a template), `set_env`, `restart`, `redeploy`, `rollback`, `backup_run` |
| domains | `add_domain` |
| delete | `delete_service`, `delete_project` (container, volumes and domains are removed) |
| migrate | `migrate` (project), `service_migrate` (one service) |
| restore | `backup_restore` |

Destructive tools say in their description that they run immediately when permitted. A call the server refuses with `missing_permission` returns a `PERMISSION DENIED` tool error naming the permission; `PENDING CONFIRMATION` is returned only for tokens with `require_approval`.

**Audit.** Every API-token request to a route that needs `delete`, `migrate`, `domains`, `restore`, `servers`, `settings` or `tokens` is written to the audit log as `api.<permission>` with the token id and the result (`ok`, the error, or `denied: ...` when the token lacks the permission).
