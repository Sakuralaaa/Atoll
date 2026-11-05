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
- **我的项目**: one card per project (services count, status, first domain). The server icon
  next to the sort button groups the cards by machine instead (remembered per browser);
  `?server=ID` shows one machine's projects.
- **收藏 / 置顶**: the star on a project card, list row or the project's header (sidebar, and the
  top bar on phones) favorites it; the pin keeps it at the top of 我的项目, above everything
  else. With several pins, ↑ / ↓ on a pinned card (or dragging one pinned card onto another)
  sets their order. In the grouped view the pinned projects get their own 置顶 row above the
  machine cards, and pinned / favorite names in a machine card carry a small pin / star.
  The 收藏 chip (`?filter=fav`) lists favorites, and the sidebar shows up to 8 of them under
  the navigation. Both are stored per account on the control server (`/api/v1/me/project-prefs`),
  so they are the same on every device.
- Cards in a grid are the same height. Machine cards show the project names on at most two
  lines; what does not fit becomes one `+N` chip (hover lists the rest).
- **延迟**: server cards, machine cards and the server detail show the control↔agent round trip
  of the latest measurement (green under 100 ms, amber under 300 ms, red from 300 ms). While
  measurements fail it shows a red `超时` whose tooltip carries the error; `—` only while offline
  or before the first attempt. The detail page adds the average of the last 8 samples.
- Machine cards (按服务器分组) wrap long server names to two lines (full name in the tooltip)
  like the server cards; the name block always reserves two lines so cards stay the same height.
- Texts that come from the agent or server in English (Docker adoption and Zeabur scan warnings,
  risk reasons, step names and details, container states) are translated in the UI by pattern
  (`web/src/api/backendText.ts`), so older agents translate too; unknown texts show as they came.
- **服务器** cards show the full name (wrapping), the online state and the OS on their own
  lines; 需要处理 and agent upgrade badges sit in the footer next to the hardware.
- **异常**: the sidebar's 异常 entry counts every firing problem. A page shows a red banner
  only for its own kind: 服务器 for offline servers and full disks, 我的项目 for services,
  deploys, backups and domains. 收起 / 展开 folds the banner; each page remembers it in the
  browser.
- **中控**: 最近一次中控备份 → 位置 shows 本机磁盘 (the copy in the control server's data
  directory) and/or 异地存储桶 (uploaded to the backup bucket); hover for the meaning.
- **新建服务** → 从已有 Docker 容器接管 opens `/import/docker` on the project's server (or the
  server picked in the dialog). Adoption creates its own Atoll project; it is not merged into
  the project the dialog was opened from.

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
  domain; 完整域名… takes any other hostname. 接入方式 is two cards: **Cloudflare 代理**
  (orange cloud, recommended: hides the server IP, CDN and protection; costs a 100-second
  request timeout and only HTTP(S) is proxied) and **直连** (grey cloud: no timeout, any
  protocol, for long requests; exposes the server IP).
- **域名 page**: each row carries 橙云 (100 秒超时) or 灰云 (会暴露源站 IP) and the certificate
  the servers present: Origin CA, ACME or HTTP (none). With the Cloudflare token set, clicking
  the cloud tag switches the proxy after a confirmation (`PATCH /domains/{id}`); switching to
  grey under an Origin CA root moves that host to an ACME certificate (a minute or two). A
  yellow Origin CA tag on a grey host means browsers will reject it (the host follows the
  global Origin CA certificate): set its root to 自动 or ACME. The project and service column
  shows one name when both are the same.
- **Cloudflare**: when a service binds a domain under one of the root domains, Atoll creates and
  deletes the DNS records with this token (needs Zone.DNS edit). The 域名 page header shows
  whether the token holds Zone.DNS 编辑 and, when a root uses Origin CA (自动), SSL 证书编辑
  (Zone.SSL and Certificates: Edit); a missing one turns yellow, since without it issuing the
  Origin CA certificate fails with 1016 and the root falls back to ACME. The server probes this
  without changing anything (`GET /cloudflare/token-check`, cached 10 min).
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
  (`atl_pat_…`) are separate and managed under 连接 (Connect).
- Changing the username (`POST /api/v1/auth/username`) or the password needs the current
  password, plus a TOTP code when 2FA is on. A username change keeps sessions and tokens; a
  password change signs out every other session.
- Lost both the phone and the recovery codes: on the control server run
  `sudo atoll-server admin reset-2fa <username>`.

## CLI & API, confirmations

- Atoll has no built-in AI. Give the CLI or MCP server to an external agent with an API token.
- A token can only do what its permissions allow (anything else answers 403). With
  "require panel approval" on, deletes, moves and DNS changes started with that token wait in
  审批 (Approvals) until approved in the panel. Actions taken in the panel itself are never queued.
