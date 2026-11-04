# Panel guide (what each page means)

The web panel keeps its own text short: a heading, the controls, and an ⓘ hint where a
concept needs a sentence. This file is the long form, for people and for AI agents (Claude
Code, Codex) driving Atoll through the CLI, MCP or REST API. API details: [api.md](api.md).
Control server operations: [control.md](control.md).

## Projects, services, servers

- **Server** (服务器): a VPS running `atoll-agent`. It connects out to the control server, so
  no inbound port is needed. Services run on servers.
- **Control** (中控): the machine running `atoll-server` (panel, API, scheduler). Services keep
  running while it restarts or is down. The same machine may also run an agent and host
  projects; the 中控 page then shows that server as 同机节点 (same-host node), matched by
  tailnet IP or public IP.
- The control server has a display name (中控 → pencil icon, or `PATCH /api/v1/control`
  `{"name": "..."}`). Empty means the hostname is shown.

## Adopting Docker containers (接管 Docker 容器)

- Entry: 迁移 page (card at the top) or server → 设置 → 接管 Docker 容器; page `/import/docker`.
- The scan lists containers Atoll does not manage, grouped by compose project. **可自动接管**
  (auto) containers can be ticked; **需手动处理** (manual) ones show in red why not (host
  network, privileged, Docker socket, devices, cap_add…) and keep running untouched.
- 演练 (rehearse) starts Atoll copies next to yours with no published ports and empty scratch
  data dirs, checks health and stops them again. 切换 (cut over) stops your containers (kept,
  restart policy set to no) and starts Atoll's on the same ports with your data mounted in
  place; a failure rolls back by itself. 回滚 (roll back) starts your containers again.
- Mounts shown as `host path → container path` are used in place: Atoll never deletes, backs
  up or moves them. Guide: [adopt-docker.md](adopt-docker.md).

## Image updates (镜像更新)

- The control server periodically asks the registry for the digest of each service's tag
  (HEAD requests; they do not count against Docker Hub pull limits) and shows 有更新 when
  the tag moved or a newer tag with the same major version exists. Interval: 设置 → 镜像更新
  (1–168 h, default 6).
- Per service policy (服务状态 tab): 仅通知 (notify), 自动更新 (auto: redeploys the new digest of
  the same tag inside an optional UTC window, never switches tags), 关闭检查 (off).
- **Version**: when an image carries the OCI label `org.opencontainers.image.version`, the
  card shows it (e.g. `1.2.5`) next to the digest. The label is read once per digest.
- **Pinned** (已固定版本): an image written as `name@sha256:…` (by hand, or left by a rollback)
  is pinned. A pinned service is never reported as having an update and is never auto-updated.
  「改回跟随 <tag>」 (or `POST /services/{id}/image-update/apply`) unpins it: the service goes back
  to `name:<tag>` and deploys that tag's current digest. Setting the image back to `name:latest`
  in the service settings does the same on the next deploy.
- Every update is a new deployment; the previous one stays in 部署记录 for rollback.

## Settings (设置)

- Secrets (Cloudflare token, origin key, backup keys) are write-only: once saved they are never
  shown again; leaving the field empty keeps the stored value.
- **面板地址**: the URL agents and the CLI use to reach the control server; install commands use it.
- **根域名**: the list of root domains services' domains are created under. The default one
  (★) is used by projects that do not pick their own (项目设置 → 默认根域名) and by Zeabur
  imports for `*.zeabur.app` names. Each row shows its Cloudflare zone (matched by suffix,
  longest first) and its certificate: 跟随全局证书 (the HTTPS 证书 section below), Origin CA
  (issued automatically with the API token; needs SSL and Certificates edit permission), or
  ACME (also the fallback when Origin CA issuance fails; the reason is shown under the row).
  ↻ matches the zone and retries the certificate. A root can only be removed when no domain
  uses it and it is not the default (unless it is the last one). Set a new root's Cloudflare
  SSL/TLS mode to Full (strict); Flexible causes redirect loops.
- **绑定域名**: type a prefix (`blog`, `api.v2`, empty = the root itself) and pick the root
  domain; 完整域名… takes any other hostname.
- **Cloudflare**: when a service binds a domain under one of the root domains, Atoll creates and
  deletes the DNS records with this token (needs Zone.DNS edit).
- **HTTPS 证书** (global): used by roots set to 跟随全局 and by hostnames under no root. With Cloudflare proxying, a Cloudflare Origin CA certificate (15 years) is pushed
  to every server's atoll-caddy; DNS-only (grey cloud) hosts get ACME certificates automatically.
- **通知**: sent when a server is offline > 2 min, a disk reaches 90%, a service is unhealthy for
  3 min, a deploy, backup or backup verification fails, or a domain fails to configure; one more
  when it recovers. One notification per problem. Quiet hours only let "server offline" through;
  the rest is sent when quiet hours end.
- **默认资源上限**: applied to services created without their own limits (database templates,
  images, git builds, Zeabur imports). Empty or 0 = no limit. Existing services are unchanged;
  apply the defaults from a service's 服务状态 tab.
- **备份存储**: any S3-compatible storage (Cloudflare R2, Backblaze B2, AWS S3, MinIO). Each
  server's agent encrypts with restic and uploads directly; data never passes through the
  control server.
- **私有网络**: the panel stays public; agent ↔ control and cross-server service traffic use the
  tailnet. Needs a reusable, tagged, ideally pre-approved Tailscale auth key.

## Account (账号与安全)

- Username, password, two-factor authentication and sessions of the panel account. API tokens
  (`atl_pat_…`) are separate and managed under CLI 与 API.
- Changing the username (`POST /api/v1/auth/username`) or the password needs the current
  password, plus a TOTP code when 2FA is on. A username change keeps sessions and tokens; a
  password change signs out every other session.
- Lost both the phone and the recovery codes: on the control server run
  `sudo atoll-server admin reset-2fa <username>`.

## CLI & API, confirmations

- Atoll has no built-in AI. Give the CLI or MCP server to an external agent with an API token.
- A token can only do what its permissions allow (anything else answers 403). With
  "require panel approval" on, deletes, moves and DNS changes started with that token wait in
  待确认 until approved in the panel. Actions taken in the panel itself are never queued.
