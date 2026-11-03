# Atoll API contract (v1)

Types: `internal/api/types.go` (Go) — the web app mirrors them in `web/src/api/types.ts`.
All JSON. Errors: non-2xx with `ErrorBody`.

## Components
- `atoll-server` (control node): REST API + embedded web UI (`web/dist` via `go:embed`), SQLite (modernc.org/sqlite, WAL), agent WebSocket hub, Cloudflare DNS, MCP-over-HTTP not required (MCP is in the CLI, stdio).
  Listens on `ATOLL_LISTEN` (default `:7300`). Data dir `ATOLL_DATA` (default `/var/lib/atoll-server`), secrets encrypted with AES-GCM key in `<data>/master.key`.
- `atoll-agent` (each worker VPS): connects out to the server over WebSocket, runs containers with the Docker Engine API, keeps desired state in `/var/lib/atoll/state/*.json` so it keeps working when the server is offline, configures a Caddy container (`atoll-caddy`) via its admin API for ingress, collects metrics.
- `atoll` CLI: talks to REST with a PAT; `atoll mcp` serves MCP over stdio; `atoll import zeabur` migrates from Zeabur.

## Auth
- Panel: `POST /api/v1/auth/login {username,password}` → httpOnly cookie `atoll_session` (`Secure` behind HTTPS, `SameSite=Strict`, 30 days; a session unused for 7 days expires earlier, `sessions.last_seen_at` is refreshed at most every 5 minutes). `POST /api/v1/auth/logout`, `GET /api/v1/auth/me`.
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
| POST | /auth/password | `{current_password, new_password, code?}` → `{revoked_sessions: n}`. Panel session only (API tokens get `403 session_required`). `new_password` 8-72 chars (same rule as setup and `reset-password`) and different from the current one (`400`). Wrong current password → `401 invalid_credentials`; with 2FA on, `code` (current TOTP, not a recovery code) is required → `401 code_required` / `401 invalid_code`. Failures count toward the login lockout below (`429` while locked). Keeps the calling session and signs out every other one; audited as `auth.password_change`. Panel: Settings → 账号与安全 → 修改密码 |
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
| POST | /servers/join-token | → JoinToken (valid 1h, single use); `command` pins the hosted agent binaries with `--sha256-amd64/--sha256-arm64` and, on a release build, `--version <server version>` |
| GET | /servers/agent-install-command | → InstallCommand `{command, version}`: the same one-liner without a token, for re-running the installer on a joined server (old agents that cannot upgrade themselves) |
| GET/PATCH/DELETE | /servers/{id} | PATCH {name, provider, region} |
| GET | /servers/{id}/metrics?range=1h\|24h\|7d | []NodeMetrics |
| GET | /servers/{id}/containers | []ContainerState |
| POST | /servers/{id}/agent-upgrade | → 202 AgentUpgrade. Upgrades the agent to the version this control server hosts (`agent_latest`). 409 codes: `offline`, `up_to_date`, `busy`, `not_hosted`, `agent_too_old` (agents before v0.1.6: run install-agent.sh once; the message carries the command), `upgrade_failed` (the agent refused: checksum, version, no systemd) |
| POST | /servers/agent-upgrade | {ids} (empty = every online server on another version) → [{id, upgrade?, code?, error?}]; three at a time |
| GET/POST | /projects | POST {name, server_id, description} |
| GET/PATCH/DELETE | /projects/{id} | GET includes services |
| GET/POST | /projects/{id}/services | POST Service (kind+image or template kind). `mem_limit_mb` / `cpu_limit` left out (0) take the defaults from Settings (`default_mem_limit_mb`, default 1024; `default_cpu_limit`, default 0 = no limit); `-1` asks for no limit explicitly. Applies to every way a service is created (image, template, git build, imports) |
| GET/PATCH/DELETE | /services/{id} | PATCH partial Service → triggers redeploy |
| POST | /services/{id}/actions | {action: start\|stop\|restart\|redeploy} (redeploy → Deployment) |
| POST | /services/{id}/rollback | {deployment_id} |
| POST | /projects/{id}/migrate | {server_id}: stop → volume export/import via server relay → start on target → DNS switch (waits until names resolve to the target) → ingress update (target first, then source withdrawn) → remove from source; HTTPS confirmed in background (`provisioning` → `active`) |
| GET/PUT | /services/{id}/env | []EnvVar (PUT replaces all; secret values "" keep old) ; `?reveal=1` shows secrets (panel session, or an API token with `exec` and without `require_approval`: else `403 missing_permission` / `approval_required`; audited as `env.reveal`); PUT redeploys a deployed service, `?redeploy=0` only saves |
| GET | /services/{id}/env/references | []EnvVar exposed by other services in project |
| GET/POST | /services/{id}/domains | POST {hostname, port_name, proxied}. Status: with a Cloudflare token the record is created first and the domain is `provisioning` (route held back from Caddy until the name resolves publicly, ≤90s; then `active` once HTTPS answers, ≤3 min, with a note in `error` if it never did); without a token `active` when the name already resolves to the server, else `pending` with the record to create; `error` when the DNS API failed |
| DELETE | /services/{id}, /projects/{id} | routes withdrawn (agent ack) → containers removed → DNS records deleted |
| DELETE | /domains/{id} | removes the route from Caddy first, then the DNS record (on a DNS error the route is restored and the domain stays bound) |
| GET | /domains | all domains |
| GET | /services/{id}/deployments | []Deployment |
| GET | /services/{id}/deployments/{dep}?wait=DURATION | Deployment. With `wait` (at most 90s) it long-polls: answers once the deployment is `running`, `failed` or `superseded`, or with its current state when the wait runs out. Loop on it to wait for a deploy |
| GET | /services/{id}/deployments/{dep}/logs?after=SEQ | DeploymentLogs: build output of a deployment (see "Builds from source") |
| GET | /services/{id}/webhook | GitWebhook of a git service (secret: panel session only) |
| POST | /services/{id}/webhook/rotate | session only → GitWebhook with a new secret (shown once) |
| POST | /hooks/github/{service_id} | GitHub push webhook, no login: verified by `X-Hub-Signature-256` |
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
| GET | /backup-verify | BackupVerifyStatus: newest repository check, schedules, the node that runs the check (see "Backup verification") |
| POST | /backup-verify/check | `deploy` permission (global) {server_id?, subset?} → 202 BackupVerification (kind "check", status "running") |
| GET | /backup-verifications?kind=check\|drill&service_id=&limit=50 | []BackupVerification, newest first (project-scoped tokens see the drills of their projects) |
| GET | /services/{id}/backup-verify | BackupVerifyStatus with the service's newest restore drill and `next_drill_at` |
| POST | /services/{id}/backup-drill | restore drill now → 202 BackupVerification (kind "drill", status "running") |
| GET | /problems[?resolved=1] | → []Problem: firing problems (and, with resolved=1, ones resolved in the last 24h). See "Problems & notifications" |
| GET / PUT | /settings/notifications | `settings` permission. NotificationSettings: channels (webhook / telegram / email), notify_resolved, quiet hours. Secrets are write-only (`has_*`) and an empty value keeps the stored one |
| POST | /settings/notifications/test | `settings` permission. {channel} → {ok:true}, or 502 `notify_failed` with the channel's error. Works for unsaved edits; empty secrets fall back to the stored channel with the same id |
| POST | /settings/backup/test | admin → BackupTestResult (an agent checks / initialises the repository) |
| GET | /settings/backup/password | session only → the restic repository password |
| DELETE | /settings/backup | admin: clears the target (the repository password is kept) |
| GET/POST/DELETE | /tokens, /tokens/{id} | APIToken |
| GET | /confirmations?status=pending | |
| POST | /confirmations/{id}/approve, /reject | session only |
| GET/PUT | /settings | Settings. `default_mem_limit_mb` (0 = no limit, else 6–1048576) and `default_cpu_limit` (cores, 0 = no limit, ≤ 1024) are the limits new services get when created without their own; GET always returns both, a PUT leaving one out (or null) keeps it. Changing them never touches existing services |
| GET/POST | /imports | POST ImportPlan → ImportJob (creates project+services, not started) |
| GET | /imports/{id} | ImportJob |
| PUT | /imports/{id}/volumes/{service_name}/{volume} | raw tar.gz body, streamed to target agent via volume.import |
| POST | /imports/{id}/start | deploy all services |
| GET | /templates | []Template: db templates (postgres, mysql, redis, mongo); create one with POST /projects/{id}/services `{kind, name, env?}` |
| GET | /events (WS) | live updates {type: "service"|"server"|"deployment"|"build_log"|"confirmation"|"image_update"|"backup"|"backup_verify"|"restore", payload} |

## Install scripts
- `GET /install-agent.sh`, `GET /install-server.sh` serve the scripts embedded in this build (`install/embed.go`), so a node installs with the scripts of the server's own version. Commands built by the server use the panel-hosted script when the public URL is https; over plain http they fetch the script of the server's release tag from GitHub instead (a script fetched over http could be swapped in transit, which no `--sha256` pin would catch).
- Any other path that names a file (`/x.sh`, `/assets/old.js`) and any unknown `/api/` or `/agent/` path is a 404, never the panel's `index.html` (the panel routes by URL hash).
- `install-agent.sh --version TAG` pins the GitHub fallback download (used when the server hosts no agent binary) to that release.

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
- IDs: 12-char lowercase base32 with prefix: `srv_`, `prj_`, `svc_`, `dep_`, `dom_`, `tok_`, `cfm_`, `imp_`, `bak_`, `rst_`, `bvf_` (backup verification).
- Docker: container name `atoll-<service_id>`, labels `atoll.service=<id>`, `atoll.project=<id>`, `atoll.deployment=<id>`; network per project `atoll-<project_id>` with alias = service name (the agent removes it when the project's last service on that node is removed, unless a container is still attached); every container with an http port also joins `atoll-ingress`, where `atoll-caddy` runs. Restart policy `unless-stopped`.
- Replacing a container (agent): a service with **no volumes and no host-published ports** whose container is running is deployed **start-first** (zero downtime): the new container starts as `atoll-<service_id>-next` on the project network (sharing the service-name alias), must stay up through the settle time without restarts and pass its healthcheck, then joins `atoll-ingress` with the network alias `atoll-<service_id>` (the name Caddy's routes dial), the old container is stopped gracefully and removed, and the new one is renamed to `atoll-<service_id>`. If the new container fails before the switch it is removed and the old one keeps serving untouched. Everything else (volumes are single-writer, host ports can only be bound once, no running container, desired state stopped) is deployed **recreate**: old renamed to `-old` and stopped, new started, rolled back to the old one on failure. The deployment `message` names the strategy (`strategy: start-first (...)` / `strategy: recreate (...)`), as does the agent log (`deploy strategy`).
- Deployments of one service are applied one at a time. A deploy requested while another is in flight (e.g. `env_set` then `redeploy` right away) is queued and sent when the in-flight one finishes; of several queued requests only the newest is sent, the ones it replaced become `superseded` without running. A finished deployment supersedes only OLDER ones, never a newer queued one.
- Volumes: bind mounts under `/var/lib/atoll/volumes/<service_id>/<volume_name>`.
- Env references: `${KEY}` resolves own vars then exposed vars of other services in project; `${SERVICE.KEY}` for explicit; built-ins: `${<SERVICE>_HOST}` (service name), `${<PORTNAME>_PORT}`, `${ATOLL_<PORTNAME>_DOMAIN}`, `${ATOLL_<PORTNAME>_URL}`, `PORT` = first port, `PASSWORD` random 32 chars for db templates. Binding or unbinding a domain only updates the route table (Caddy reload, the container keeps running); services whose env or command/args explicitly reference `${ATOLL_<PORTNAME>_DOMAIN}`/`_URL` (own or `${SERVICE.…}`) are redeployed when the value changes, while the implicitly injected `ATOLL_<PORTNAME>_DOMAIN`/`_URL` of an unreferencing service is refreshed at its next deploy.
- Times UTC RFC3339.

## In-place Zeabur takeover (primary import path)
Target user: their OWN VPS registered to Zeabur (unmanaged, single-node k3s), services built by Zeabur, data on that VPS. Atoll takes the same machine over — no server-to-server move.
1. Install atoll-agent on that VPS (join command). Agent detects k3s (`k3s kubectl`), `GET /servers/{id}/zeabur` shows Zeabur projects/services found locally (types `ZeaburInventory`).
2. `POST /imports/zeabur-local` (`domain_map: {old: new}`; `old: ""` drops a host) creates an Atoll project on that server with services mapped 1:1: env from pod spec (expanded), `<x>.zeabur.internal` → service name, Zeabur built-ins translated, ZEABUR_* ids dropped, ports, volumes, configmaps, custom domains (zeabur.app → warning).
3. Rehearse: agent exports Zeabur-built images from containerd (`k3s ctr -n k8s.io images export` | `docker load`, tag `atoll-import/<svc>:<digest12>`); registry images (Docker Hub, GHCR...) are pulled by their original ref with the tag kept (`zeabur.image` with `pull: true`), falling back to the containerd copy tagged with that same ref when the pull fails; copies PVC dirs locally (apps) and dump/restore DBs (same major version), starts containers WITHOUT public ingress, verifies row counts. Zeabur keeps serving.
4. Cutover (user confirms): scale Zeabur app workloads to 0 (DBs keep running until final dump), final DB sync + volume rsync, scale Zeabur's operator (operator-system) to 0 so it does not revert the park, park Zeabur ingress-controller DaemonSet, start atoll-caddy on 80/443 with the same hostnames. DNS unchanged (same IP) — custom domain A records already point here; only *.zeabur.app URLs die. DNS provider optional: every *.zeabur.app host needs a replacement (explicit `domain_map` entry or `base_domain`); with a Cloudflare token the cutover creates the A records and the new hosts go through the normal provisioning path (route held until the name resolves publicly, then `active` once HTTPS answers), without one hosts that already resolve to the server go active and the rest stay `pending` with the record to create (the cutover still succeeds). A cutover after a rollback starts the stopped Atoll services again first.
5. Rollback: withdraw Atoll routes (agent ack), then delete only the DNS records the cutover created (pre-existing records untouched; domains back to `pending`), stop atoll services, restore ingress-controller and the operator, scale Zeabur workloads back to recorded replicas.
6. Later, after user approval: remove Zeabur components / k3s (`/usr/local/bin/k3s-uninstall.sh`) to free RAM — explicit confirmation, never automatic.
Risk: both stacks run side by side during rehearsal (RAM/disk) — scan reports free mem/disk; refuse rehearsal if disk free < 1.2× data size.

## Problems & notifications

The control server evaluates conditions every 30s and keeps them in the `problems` table (migration 0012). A condition must hold for a while before it **fires**, so blips never notify; one that clears before firing leaves no trace.

| kind | subject | fires after |
|---|---|---|
| `node_offline` | server | 2 min disconnected |
| `disk_high` | server | 5 min at ≥ 90% (newest metrics sample, < 10 min old) |
| `service_down` | service | 3 min failed / crash-looping / unhealthy (not when desired stopped, not while its server is offline) |
| `deploy_failed` | service | at once; clears with the next successful deploy |
| `backup_failed` | service | at once, when backups are enabled; clears with the next successful backup |
| `backup_verify_failed` | `backup_repo` (the repository check, 「备份仓库」) or service (its restore drill) | at once, when the newest check / drill failed and backups are enabled (any policy for the check, the service's own for a drill); clears with the next passing one. Label 「备份校验失败」 |
| `domain_error` | domain | 10 min in `error` |

Each firing problem is sent once to every enabled channel, and once more when it resolves (`notify_resolved`, default on). Delivery is idempotent across restarts; a channel that fails is retried on the next tick. During quiet hours (`quiet_start`–`quiet_end` in `timezone`, may wrap midnight) only `node_offline` is sent; problems still firing when they end are sent then. Webhooks receive `{"event":"firing"|"resolved"|"test","problem":Problem,"text":"…"}`. Resolved problems are kept 30 days.

CLI: `atoll status` (servers online + problems; `--resolved`; `--check` exits 5 when anything fires). MCP: `problem_list`.

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
- CLI: `atoll updates [--available] [-p PROJECT]`, `atoll updates --check SERVICE`, `atoll updates --apply SERVICE [--tag TAG]`, `atoll updates policy SERVICE notify|auto|off [--window 02:00-05:00]`. MCP tool `update_list` (read-only).

## Builds from source
A service of kind `image` can be built from a GitHub repository instead of pulling a registry image. Server: `internal/server/git.go`; agent: `internal/agent/build.go`; types `GitSource`, `GitWebhook`, `BuildPayload`, `DeploymentLogs` in `internal/api/types.go`.
- Source: `POST /projects/{id}/services` (or `PATCH /services/{id}`) with `git: {repo, branch?, context?, dockerfile?, build_args?, token?}` and no `image`. `repo` must be `https://github.com/OWNER/REPO` (`.git` accepted, no credentials in the URL); `branch` defaults to `main`; `context` is a subdirectory of the repo (default root); `dockerfile` is relative to the context (default `Dockerfile`); paths may not leave the repository. `build_args` become `--build-arg` (not secret: visible in the image history). `token` (GitHub token for a private repo) is write-only, stored encrypted; responses carry `has_token` only. A PATCH of `git` is partial (omitted fields and the token are kept; `clear_token: true` removes it); `{"git": null, "image": "..."}` switches back to a registry image. Services report `source: "image"|"git"` and `git.last_commit`.
- Where: every build runs on the service's own server (the target worker), never on the control server; that server must be online (409 `server_offline` otherwise) and needs `git` and `docker` with BuildKit (`install-agent.sh` installs them). One build at a time per server (others wait their turn, up to the build timeout). Build timeout 20 min (`Config.BuildTimeout`).
- Limits (agent, `internal/agent/build_limits.go`): builds run in a dedicated buildx builder (`docker-container` driver, named `atoll-builder-m<MB>-s<shares>[-c<millicpu>]`) with a memory cap (default 75% of RAM, min 1 GiB) and `cpu-shares=256`, then `--load` into Docker; if the builder cannot be created the agent uses plain `docker build` and logs why. A build does not start with less than 3 GiB free on the Docker root; the BuildKit cache is pruned to 10 GB after each build. Env on the node (`/etc/atoll/agent.env`): `ATOLL_BUILD_LIMITS=off`, `ATOLL_BUILD_MEMORY`, `ATOLL_BUILD_CPUS`, `ATOLL_BUILD_MIN_FREE_GB`, `ATOLL_BUILD_CACHE_GB`, `ATOLL_DOCKER_ROOT`.
- Build: shallow fetch of one commit (the token is passed as an HTTP header via git config env, never in argv or logs; any occurrence in the output is replaced by `***`), `docker build`, tagged `atoll-build/<service_id>:<commit12>` and deployed as a local image (`ResolvedSpec.local_image`, never pulled). The deployment is `building` meanwhile and records `commit` when done. No Dockerfile at `<context>/<dockerfile>` is an error (the deployment fails with that message): automatic detection (Railpack/Nixpacks style) is not supported.
- What gets built: create, redeploy (and PATCH) build the branch head; a webhook push builds exactly the pushed commit. Other deploys (env change, migration, restore...) reuse the last built image when it is on the target server, otherwise rebuild the commit that was running. A failed build fails the deployment; the running container is untouched.
- Retention: each server keeps the 3 newest build images of a service (`Config.BuildKeep`) plus the one its container runs; older ones are removed after each build. Build logs are kept for the 10 newest deployments of a service (at most 5000 lines each, newest kept, `truncated: true`).
- Logs: `GET /services/{id}/deployments/{dep}/logs?after=SEQ` returns the stored lines after `SEQ` (poll with the last `seq` seen); live lines are also published on `/events` as `{type: "build_log", payload: BuildLogEvent}`.
- Webhook: on GitHub add a push webhook with the URL `<public URL>/api/v1/hooks/github/<service_id>`, content type `application/json` and the service's secret. Requests must carry a valid `X-Hub-Signature-256` (HMAC-SHA256 of the body with the secret), else 401. `ping` → 200; other events, pushes to other branches or tags, and branch deletions → 202 `{ignored: true, reason}`; a push to the configured branch → 202 `{deployment_id, commit}`. A delivery whose `X-GitHub-Delivery` id was already handled in the last 24 h → 202 `{ignored: true, reason}` (no second build). The secret is returned once when the git source is created (`webhook` in the create / first-switch response), afterwards only to a panel session (`GET /services/{id}/webhook`; API tokens get `has_secret` only); `POST /services/{id}/webhook/rotate` (session) issues a new one.
- CLI: `atoll deploy --git https://github.com/OWNER/REPO [--branch B] [--context DIR] [--dockerfile F] [--build-arg K=V]... [--git-token-env VAR] -p PROJECT [--name NAME] [--port ...] [-e K=V]` (IMAGE is then omitted; the name defaults to the repository name). A new service is created with its env in the same request (one build) and prints the webhook URL and secret once. For an existing service only the given fields are compared: a changed source is PATCHed (switching an image service to git prints the webhook too), an unchanged one is redeployed, which builds the branch head; `atoll deploy IMAGE` on a git service switches it back (`git: null`). The token is only read from the environment variable named by `--git-token-env`, never taken as a flag value. `atoll services webhook SERVICE [-p PROJECT]` shows URL, branch and events (the secret only for a panel session). `atoll logs SERVICE --build [DEPLOYMENT] [-f]` prints a build log (default: the newest deployment; `-f` polls with `?after=` until the deployment is `running`/`failed`).
- MCP: `service_deploy` takes `repo`, `branch`, `context`, `dockerfile`, `build_args` instead of `image` (no token argument: set a private repo's token in the panel or with the CLI); `deployment_build_logs` (`read`) returns `{deployment_id, status, last_seq, log}`, poll with `after`.

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

**CLI / MCP (verification).** `atoll backup verify [SERVICE] [--history N]` (newest check and drill, schedules), `atoll backup check [--server SERVER] [--subset 5%] [--wait]`, `atoll backup drill SERVICE [--wait]` (`--wait` exits 1 when it failed). MCP tools: `backup_verify` (read-only), `backup_check`, `backup_drill` (`deploy` permission).

**Limitations.**
- The control server's own state (database, `master.key`, `server.env`, Caddyfile) is backed up separately as encrypted control bundles, uploaded to this same bucket under `<prefix>/control/` — see [Control](#control-中控) and [docs/control.md](control.md). Keep the restic repository password and the control backup passphrase somewhere off the box.
- Postgres dumps (`pg_dumpall --clean --if-exists`) are restored with `psql` with `ON_ERROR_STOP` off: individual statement errors do not abort the restore. Check the restore step detail and the database afterwards.
- Redis assumes RDB persistence with data in `/data`: the dump is an RDB file (`redis-cli --rdb`) written back as `/data/dump.rdb`, and an existing AOF is removed so it does not win over the restored RDB.
- Cross-server recovery restores one service per job; restore the project's other services from their own backups.
- The agent host needs the docker CLI (restic and the dump tools run in containers).

## Backup verification
Backups are only proven by restores, so two jobs test them. Like backups, both are **scheduled and run by the agents** (`internal/agent/backup/verify.go`) from the persisted config, so they keep running while the control server is offline; results are `backup_verify` events (queued while disconnected), recorded in `backup_verifications` (migration 0015) by `internal/server/backups_verify.go`.

**Repository check** (weekly): `restic check --read-data-subset=<subset> --retry-lock 30m` against the shared repository (it never initialises a missing one). The repository is shared by every node, so the control server chooses **one** node to schedule it (`run_check` in the node's config; the previous choice while it stays online, else the oldest online node). restic's exclusive lock guards the rare overlap (e.g. an old node reconnecting with a stale config): a check that cannot get the lock reports `skipped`, which is not a failure. Backups and drills use `--retry-lock 30m`, so a running check delays them instead of failing them. `subset` is a percentage (`5%`, default), a fraction (`1/20`) or a size (`500M`).

**Restore drill** (monthly, every enabled policy, on the service's own node): the newest snapshot of each part the policy backs up (`restic snapshots --tag svc=<id>`; parts with no snapshot fall back to what the repository holds) is restored into throwaway resources:
- `volumes`: `restic restore <snap>:<path> --target /drill --verify` into `<agent data>/backup/drill/<id>/files`; it passes when at least one non-empty regular file comes back (`detail` `files=N bytes=N`; a file count different from the snapshot summary is a warning).
- `dump`: a temporary container `atoll-drill-<id>` of the **service's own image** (same version; it must be on the node, nothing is pulled), entrypoint, command, env and limits, with labels `atoll.role=backup-drill` and `atoll.drill=<id>` (no `atoll.service`, so reconcile ignores it), network mode `none`, no published ports, no restart policy, fresh data dirs under `<agent data>/backup/drill/<id>/db` mounted where the service mounts its volumes (config files stay mounted read-only; a mount of the live volume dir is refused). It waits for the database, loads the dump (`restic dump` → `psql` / `mysql` / `mongorestore`; redis gets the RDB as `/data/dump.rdb` before start) and runs a query: `databases=N tables=N` (postgres, mysql), `databases=N collections=N` (mongo), `keys=N` (redis). An empty result (0 tables/keys) is a warning.
- Before restoring, the drill checks free disk (volumes: snapshot size +10% + 256 MB; dumps: 3× dump size + 512 MB) and fails early when it would not fit. The running container, its volumes and its data are never touched. The temporary container and files are removed when the drill ends; leftovers of a drill interrupted by an agent stop are removed when the agent starts, and the interrupted record is reported failed. A snapshot older than 8 days is reported as a warning.

**Scheduling.** Scheduled checks and drills are not retried after a failure (the failure is a problem; run one again from the panel / CLI). Defaults are automatic slots in the control server's zone that keep clear of the backups: the check on Sunday at the first of 04:30, 05:30, 06:30, 03:30, … that is ≥ 3 h from every enabled backup schedule (else the farthest one), the drill on the 1st of the month the same way (also avoiding the check). Override with `PUT /settings`: `backup_check_schedule`, `backup_drill_schedule` (5-field cron, `off`, or `auto`), `backup_check_subset` (`auto` = 5%). Empty keeps the stored value; `GET /settings` returns the stored value (`""` = auto).

**Types.**
- `BackupVerification` `{id: "bvf_...", kind: check|drill, service_id? (drill), server_id, trigger: schedule|manual, status: running|done|failed|skipped, subset?, steps: [BackupVerifyStep], error?, warnings?, duration_ms, started_at, finished_at?}`.
- `BackupVerifyStep` `{part: check|volumes|dump, snapshot_id?, status: done|failed|skipped, detail?, files?, bytes?, error?}`.
- `BackupVerifyStatus` `{check?, drill?, check_schedule, check_subset, drill_schedule, timezone, check_server_id?, next_check_at?, next_drill_at?, repo_configured}` (schedules effective; `""` = off).
- Errors of the run-now endpoints: 409 `backup_not_configured`, 409 `server_offline`, 409 `verify_in_progress` (one is already running, here or on the agent), 400 for a bad subset or a service with nothing to back up, 502 `agent_error`.
- Records still `running` after 24 h are marked failed; records older than a year are pruned (the newest per kind and service is kept).

**Agent messages.** server → agent: `backup.verify` (BackupVerifyPayload `{id, kind, trigger, repo, subset?, target?}`; result Output `queued` | `busy`), and `backup.config` carries `verify` (BackupVerifyConfig `{run_check, check_schedule, check_subset, drill_schedule, timezone}`). agent → server: `backup_verify` (BackupVerifyEvent: `running`, then `done` | `failed` | `skipped`).

## CLI & MCP permissions
The CLI and `atoll mcp` act with the API token from `atoll login` (or `$ATOLL_URL` / `$ATOLL_TOKEN`). What they may do is the token's permission set (`read`, `deploy`, `exec`, `delete`, `migrate`, `domains`, `restore`, `servers`, `settings`, `tokens`), optionally limited to projects. Without a permission the server answers `403 missing_permission` and the CLI names the permission to grant (`atoll token edit TOKEN --add-perm PERM`); with it the operation runs directly. Only a token created with `require_approval` gets `202 Confirmation` for destructive operations (CLI exit code 3, `--wait` waits for the panel approval). `403 escalation` means a token tried to hand out more than it holds; `403 project_scope` that the token is limited to other projects.

**Waiting for deploys.** `--wait-ready` (global) makes `deploy`, `redeploy`, `rollback`, `start`, `restart` and `env set|unset` wait until the deployment the command started is running (`--ready-timeout`, default 15m). When it fails or is superseded without a newer one, the CLI prints the reason and the last 20 build log lines and exits **4**; progress goes to stderr. It notes the service's newest deployment before acting and follows the next one (and any newer one that supersedes it), so CLI and server clocks never need to agree. MCP: `deployment_wait` (read) does the same for an agent, `timeout_sec` default 300, max 900; when the time runs out it returns `done: false` and can be called again.

**MCP tool names.** Tools are named `<resource>_<action>`: `server_list`, `problem_list`, `project_list`, `project_migrate`, `project_delete`, `service_list`, `service_deploy`, `service_logs`, `service_restart`, `service_stop`, `service_start`, `service_redeploy`, `service_rollback`, `service_exec`, `service_migrate`, `service_delete`, `deployment_get`, `deployment_wait`, `deployment_build_logs`, `env_get`, `env_set`, `domain_list`, `domain_add`, `domain_remove`, `template_list`, `template_deploy`, `update_list`, `backup_list|run|restore|verify|check|drill`, `zeabur_scan`, `zeabur_import_status`. The names used before v0.1.8 (`list_servers`, `deploy`, `set_env`, `restart`, `add_domain`, ...) are still served as deprecated aliases; `atoll mcp --legacy-names=false` drops them. `service_exec` (`exec` permission) returns `output` and `exit_code` (0 on success; absent when the command could not run); `POST /services/{id}/exec` returns `exit_code` the same way.

**Several panels.** `atoll login --name NAME --url URL --token TOKEN` saves a named panel and makes it current (without `--name` it replaces the current one, `default` on first login; an older config becomes `default`). `atoll context ls|current|use NAME|rm NAME` lists and switches them; the global `--panel NAME` (or `$ATOLL_PANEL`) picks one for a single command. (The flag is `--panel`, not `--context`, which `atoll deploy` uses for the git build context.) The config keeps the current panel in the top-level `url`/`token`, so older CLIs still work.

**Environment in bulk.** `atoll env import -s SERVICE FILE|-` applies a `.env` file in one change (blank lines, `#` comments and `export ` are skipped; quoted values unwrapped), `--replace` drops variables not in the file. `--no-redeploy` (on `env set|unset|import`) only saves (`PUT /services/{id}/env?redeploy=0`); deploy later with `atoll redeploy`. MCP `env_set` takes `redeploy: false` for the same. `atoll env export -s SERVICE [-o FILE] [--include-secrets]` writes the variables as a `.env` file that `env import` reads back unchanged: values with whitespace, `#`, quotes or backslashes are double-quoted with `\\`, `\"` and `\n` escapes; secret variables become a `# KEY: secret, value not exported` comment unless `--include-secrets` (uses `?reveal=1`, so the token needs `exec`); `-o` writes the file with mode 0600. The panel's **导出 .env** on the Variables tab writes the same format (copy or download; 包含私有值 reveals secrets).

**Completion.** `atoll completion bash|zsh|fish|powershell` prints the shell script; service names (`atoll restart <TAB>`, `-s <TAB>`), projects (`-p <TAB>`) and `--panel` complete from the panel.

**exec.** `POST /services/{id}/exec` (`atoll exec`) needs the `exec` permission, separate from `deploy` since a shell in a container reads its secrets and data. For the same reason `exec` also allows reading secret variable values (`GET /services/{id}/env?reveal=1`, `atoll env export --include-secrets`). Tokens that held `deploy` before the upgrade were given `exec` by migration 0010; new tokens get it only when asked (`--perm deploy,exec`).

**Tokens.** `atoll token ls`, `atoll token create NAME [--perm read,deploy,...] [--project PROJECT]... [--require-approval]` (default `read`; `--perm all` for every permission; `servers`/`settings`/`tokens` cannot be combined with `--project`; the secret is printed once), `atoll token edit TOKEN [--name N] [--perm ...|--add-perm ...|--rm-perm ...] [--project P...|--all-projects] [--require-approval[=false]]`, `atoll token rm TOKEN` (TOKEN = id, name or prefix). All need the `tokens` permission.

**Placement & network.** `atoll deploy IMAGE -p PROJECT --server SERVER` places a new service on another server (`POST /projects/{id}/services` with `server_id`); `atoll services migrate SERVICE --to SERVER [-p PROJECT]` moves one service (`POST /services/{id}/migrate`, `migrate` permission); `atoll migrate PROJECT --to SERVER` moves a whole project. `atoll services rm SERVICE [-p PROJECT]` and `atoll projects rm PROJECT` delete (`delete` permission). `atoll servers network join|switch|public SERVER` runs `POST /servers/{id}/network` (`servers` permission). `atoll servers upgrade-agent SERVER...|--all` upgrades agents (`servers` permission); `atoll servers ls` shows `v0.1.5 → v0.1.6` when one is behind.

**Agent upgrades.** `Server` carries `agent_latest` (the version hosted for its arch), `agent_self_upgrade` (the agent said in its hello that it handles `upgrade`) and `agent_upgrade` (last panel-started upgrade: `upgrading`/`done`/`failed`, `rolled_back`, `error`; stored in the `agent_upgrades` table, so it survives a control restart). The agent downloads `/downloads/atoll-agent-linux-<arch>` from the control URL it is connected to, checks the SHA-256 and `version` output, then a detached `atoll-agent upgrade-helper` (systemd-run) swaps the binary (old one kept as `atoll-agent.prev`), restarts the unit and waits up to 120s for the new agent to reconnect; otherwise it restores the old binary. The agent sends the helper's result as an `upgrade.result` event after reconnecting. Containers keep running and are not recreated.

**Database templates.** `atoll templates ls` (`GET /templates`, `read`) lists postgres, mysql, redis and mongo with their exposed variables. `atoll deploy --template postgres|mysql|redis|mongodb [NAME] -p PROJECT [-e K=V] [--server SERVER]` (same as `atoll templates deploy TEMPLATE [NAME] -p PROJECT`) creates a NEW service of that kind (`POST /projects/{id}/services` with `kind`, `name` and optional `env` overriding template keys; `deploy` permission). NAME defaults to the kind; an existing service with that name is an error, never overwritten. MCP: `template_list`, `template_deploy`.

**MCP.** At startup `atoll mcp` reads `/auth/me` and only registers the tools the token's permissions allow (servers that predate permissions: mapped from the legacy scope; when `/auth/me` fails every tool is registered and calls report the error):

| Permission | Tools |
|---|---|
| read | `server_list`, `project_list`, `service_list`, `service_logs`, `deployment_get`, `deployment_build_logs`, `zeabur_scan`, `zeabur_import_status`, `update_list`, `backup_list`, `backup_verify`, `template_list` |
| deploy | `deploy` (image or `repo` build from source; `domains` also needs `domains`; `server` places a new service), `template_deploy` (new database service from a template), `env_set`, `restart`, `redeploy`, `rollback`, `backup_run`, `backup_check`, `backup_drill` |
| domains | `domain_add` |
| delete | `service_delete`, `project_delete` (container, volumes and domains are removed) |
| migrate | `migrate` (project), `service_migrate` (one service) |
| restore | `backup_restore` |

Destructive tools say in their description that they run immediately when permitted. A call the server refuses with `missing_permission` returns a `PERMISSION DENIED` tool error naming the permission; `PENDING CONFIRMATION` is returned only for tokens with `require_approval`.

**Audit.** Every audit entry records the client IP, the API token id or the panel session id (first 16 hex chars, as in `/auth/sessions`) — see `GET /audit` under [Control](#control-中控). Every API-token request to a route that needs `exec`, `delete`, `migrate`, `domains`, `restore`, `servers`, `settings` or `tokens` is written to the audit log as `api.<permission>` with the token id and the result (`ok`, the error, or `denied: ...` when the token lacks the permission).

## Control (中控)
The control server managing itself: `internal/server/control*.go`, types in `internal/api/control.go`, operator guide in [docs/control.md](control.md). The control server is never a worker; workloads keep running while it is down.

**Access.** Session only (API tokens: `403 session_required`), except `GET /control` and `GET /control/metrics`, which need `read`. Marked **re-auth** below: the body carries `{password, code?}` (current password; the TOTP code too when 2FA is on — same check and lockout as `POST /auth/password`: `401 invalid_credentials` / `code_required` / `invalid_code`, `429` while locked), or the session re-authenticated with `POST /control/reauth` in the last 5 minutes. Without either: `401 reauth_required`. Failed re-auths are audited as `control.reauth_failed`; every control action is audited (`control.*`).

| Method | Path | Body → answer |
|---|---|---|
| GET | /control | → ControlStatus: `version`, `commit`, `go_version`, `started_at`, `uptime_seconds`, `host {hostname, os, kernel, arch, cpu_cores, uptime_seconds}`, `cpu_pct`, `load1/5/15`, `mem_used_mb/mem_total_mb`, `disk_used_gb/disk_total_gb` (filesystem of the data dir), `data_dir`, `data_dir_bytes` (cached 5 min), `database_bytes` (db + WAL), `public_url`, `tailnet {ip, control_url, listen, listen_error}?`, `agents_connected`, `agents_total`, `last_backup?` (ControlBackup), `latest_release? {tag, name, url, published_at, prerelease, checked_at}` (GitHub, cached 6 h; failures retried after 30 min), `update_available`, `release_error?`, `self_update_supported`, `update_in_progress`, `update_phase?` (`downloading` → `verifying` → `backing_up` → `restarting`), `last_update_result?` (UpdateResult), `rollback_version?` (of `atoll-server.prev`) |
| GET | /control/metrics?range=1h\|24h\|7d | → []NodeMetrics of the control host (sampled every minute, same store and buckets as `/servers/{id}/metrics`) |
| GET | /control/logs?tail=N&since=S | → ControlLogs `{available, source: "journald", lines, message?}` from `journalctl -u atoll-server -o short-iso`; `tail` default 200, max 5000; `since` RFC 3339 or a duration (`15m`). Without journald: `available: false` and a message |
| POST | /control/reauth | `{password, code?}` → `{expires_at}` (5 minutes, this session only) |
| POST | /control/update/check | → `{latest_release, current, update_available, release_error?}` (forces the GitHub check) |
| POST | /control/update | **re-auth** `{version?}` (tag; default latest) → `202 {mode: "update", version, phase}`. Downloads `atoll-server-linux-<arch>` and `SHA256SUMS` of the release, rejects a checksum mismatch or a binary that does not report the tag, takes a `pre-update` control backup, refreshes the hosted `atoll-agent-linux-*` binaries (verified the same way), then starts a detached helper (`systemd-run … atoll-server control-helper`) that keeps the old binary as `atoll-server.prev`, installs the new one, restarts the unit and waits 60 s for `/api/v1/auth/status` and the new version on `/healthz`; otherwise it restores `.prev` and restarts again. Errors: `409 self_update_unsupported` (not under systemd), `409 update_in_progress`, `409 already_current`, `404 release_not_found` |
| POST | /control/rollback | **re-auth** → `202 {mode: "rollback", version}`: swaps `atoll-server` and `atoll-server.prev` with the same health check (rolling back twice goes forward again). `409 no_previous_binary` |
| GET | /control/backups | → ControlBackupList `{backups: [ControlBackup], remote_enabled, remote_error?, keep_local: 3, keep_remote, next_scheduled?, passphrase_from}`; remote-only bundles in the bucket are listed too |
| POST | /control/backups | → `201 ControlBackup` (run now) |
| GET | /control/backups/{id}/download | **re-auth** (window only: a browser download carries no body) → the encrypted bundle (local copy, else streamed from the bucket) |
| POST | /control/backups/passphrase | **re-auth** `{passphrase?}` → `{passphrase, from}`. Without `passphrase` it reveals the passphrase scheduled/local bundles are encrypted with; with one (min 12) it replaces it for future bundles |
| POST | /control/export | **re-auth** `{passphrase}` (min 12 characters, never stored) → a fresh bundle `atoll-control-<host>-<time>.atollbak` |
| GET | /audit?limit&before&actor&action | → AuditPage `{entries: [AuditEntry], next_before?}`, newest first. `limit` default 100, max 500; `before` = entry id (cursor, pass `next_before`); `actor` exact (`user:admin`, `token:tok_…`, `cli`, `system`) or a prefix ending in `:` (`token:`); `action` exact or a prefix ending in `.` or `*` (`control.`). AuditEntry `{id, actor, actor_kind: session\|token\|cli\|system, action, target, ip?, token_id?, session_id?, detail, created_at}` |

**ControlBackup** `{id: "cb_<UTC time>_<4>", created_at, trigger: scheduled|manual|pre-update|unknown, version, size_bytes, sha256?, local, remote, remote_key?, error?}`. Bundles: scrypt + AES-256-GCM (chunked, authenticated, truncation detected) over a tar.gz of `manifest.json`, `atoll.db` (a `VACUUM INTO` snapshot: consistent while the server writes), `master.key`, and `server.env` / `Caddyfile` when readable. Schedule: daily (checked hourly), local copies in `<data>/control-backups` (keep 3), and when the backup target is configured, `<bucket>/<prefix>/control/<id>.atollbak` (keep `control_backup_keep`, default 14). Scheduled/local bundles use the control backup passphrase: its own setting, else the backup repository password, else a generated one (`passphrase_from`).

**Offline CLI** (on the control host): `atoll-server export --out FILE --passphrase-file F [--data DIR]`, `atoll-server restore --bundle FILE --passphrase-file F [--data DIR] [--env-file /etc/atoll/server.env] [--force] [--adopt-public-ip] [--keep-tailnet-url]`; `install-server.sh --restore FILE --restore-passphrase-file F [--adopt-public-ip]`. `atoll-server serve --adopt-public-ip` (or `ATOLL_ADOPT_PUBLIC_IP=1`; `ATOLL_PUBLIC_IP` overrides detection) points the public URL host's A record at this host when it is under the Cloudflare `base_domain` (proxied flag kept). `atoll control` (CLI, `read`) prints the status.

**Agents and the public URL.** On every agent connect, when the panel's public URL is domain-based (not an IP) and the agent has it neither as its control URL nor as a fallback, the server sends `network` `{control_url: <the URL the agent uses>, fallback_url: <public URL>}` (an agent connected through a fallback gets `control_url: ""`, which only adds the fallback). Agents that lose their primary URL (e.g. the old control's tailnet IP) fail over to it, and the tailnet reconcile then moves them to the new control's tailnet URL.
