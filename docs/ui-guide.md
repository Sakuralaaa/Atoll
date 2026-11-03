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
- **域名与 Cloudflare**: when a service binds a subdomain of the base domain, Atoll creates and
  deletes the DNS records with this token (needs Zone.DNS edit).
- **HTTPS 证书**: with Cloudflare proxying, a Cloudflare Origin CA certificate (15 years) is pushed
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
