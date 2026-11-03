# 中控（Control）：管理 atoll-server 本身

中控是装着 `atoll-server` 的那台机器（面板、API、agent hub、SQLite）。它**不是 worker**，永远不跑业务容器；它宕机或重启时，各 VPS 上的服务照常运行（agent 本地保存期望状态，入口 Caddy 和定时备份都在 agent 上）。

面板的「中控」页（后续版本）和 API `/api/v1/control*` 提供：

- **状态**：版本 / commit、运行时长、主机（主机名、系统、内核、架构）、CPU、负载、内存、数据目录所在磁盘、SQLite 大小、数据目录大小、公网地址、tailnet 状态、已连接 agent 数、最近一次中控备份、GitHub 最新版本（6 小时检查一次，可手动强制）。另有 1h / 24h / 7d 资源曲线。
- **日志**：`journalctl -u atoll-server` 最近 N 行。
- **审计日志**：谁（会话 / API token / CLI / 系统）、从哪个 IP、做了什么。
- **自更新 / 回滚**：从 GitHub Release 下载、校验 SHA256、先自动做一次中控备份，再切换并重启；新版本 60 秒内不健康就自动换回旧版本。
- **中控备份 / 导出 / 迁移**：加密的中控备份包，每天自动备份，可下载、可导出，可在新机器上一条命令恢复。

API 细节见 [api.md → Control](api.md#control-中控)。

## 权限与二次验证

- 除了「状态」和「资源曲线」（API token 有 `read` 即可），中控的所有操作**只允许面板登录会话**，API token 一律 `403`。
- **导出、下载备份、查看/修改备份口令、更新、回滚**还需要**重新验证**：输入当前密码（开了两步验证还要输入当前验证码），校验和失败锁定规则与「修改密码」完全相同。验证通过后 5 分钟内本会话无需重复输入。
- 每个中控操作都写入审计日志（`control.*`），失败的重新验证也会记录。

## 中控备份

备份包内容：

| 文件 | 说明 |
|---|---|
| `atoll.db` | 数据库的一致性快照（SQLite `VACUUM INTO`，服务运行中照样一致） |
| `master.key` | 解密数据库里各种密钥（Cloudflare token、备份凭据、Tailscale key、加密环境变量）所必需 |
| `server.env` | `/etc/atoll/server.env`（可读时） |
| `Caddyfile` | `/etc/caddy/Caddyfile`（可读时，仅供参考） |
| `manifest.json` | 版本、时间、主机名、公网地址 |

整个包用口令加密（scrypt 派生密钥 + AES-256-GCM 分块认证加密；口令错、文件被改或被截断都会被发现）。**没有口令就打不开，丢了口令就等于丢了备份。**

- **定时**：每天一次。本地保留最近 3 份（`/var/lib/atoll/control-backups/`）；如果在 设置 → 备份 配置了 S3 / R2 存储，同时上传到同一个 bucket 的 `<前缀>/control/` 下，保留最近 14 份（设置项 `control_backup_keep`）。
- **立即备份**：`POST /api/v1/control/backups`。每次自更新前也会自动做一份（`pre-update`）。
- **定时备份用哪个口令**：优先用你在中控页设置的「中控备份口令」；没设置就用备份仓库密码（设置 → 备份里生成的那个）；两者都没有时自动生成一个。在中控页（重新验证后）可以查看当前口令或换一个新的。**请把它存进密码管理器**，中控机器整台坏掉时只能靠它解开存储桶里的备份。
- **导出**：`POST /api/v1/control/export`，现场生成一个新包，用你这次输入的口令（至少 12 个字符）加密，口令不会被保存。
- **离线导出**（在中控主机上，服务停着也行）：

  ```sh
  printf '%s\n' '你的口令至少12个字符' > /root/pass && chmod 600 /root/pass
  atoll-server export --data /var/lib/atoll --out /root/atoll-control.atollbak --passphrase-file /root/pass
  ```

## 历史数据保留

中控每 6 小时清理一次历史，数据库不会无限增长：

- 审计日志：保留 180 天。
- 已处理或已过期的确认请求：保留 90 天。待确认的请求永远不删。
- 导入任务：只删除 180 天前已结束的任务（计划导入为 done/failed，Zeabur 接管为 cleaned/rolled_back）。进行中或 live 的接管不会被删。
- 部署记录：每个服务保留最近 50 条。只删除 failed 和 superseded 状态的记录；运行中和进行中的部署永远保留。
- 已过期的登录会话。

每次清理的条数会写入中控日志（`pruned history`）。

## 自更新与回滚

1. 中控页显示有新版本 → 点更新（重新验证）。
2. 服务端下载 `atoll-server-linux-<架构>` 和 `SHA256SUMS`，校验和不符直接拒绝；确认新二进制能运行且版本号正确。
3. 自动做一份 `pre-update` 中控备份，并顺便更新本机托管的 `atoll-agent` 二进制（同样校验）。
4. 通过 `systemd-run` 启动一个独立的辅助进程（`atoll-server control-helper`），它不随 atoll-server 退出：把旧二进制保存为 `/usr/local/bin/atoll-server.prev`，换上新的，`systemctl restart atoll-server`，60 秒内检查 `/api/v1/auth/status` 和 `/healthz` 的版本号。不健康就换回 `.prev` 再重启，结果显示在中控页（`last_update_result`）。
5. **回滚**：`atoll-server` 与 `atoll-server.prev` 互换，同样有健康检查（再回滚一次就回到新版本）。注意：新版本可能已经给数据库加了新表/列，旧版本一般能忽略它们正常运行；如有问题，用 `pre-update` 备份恢复。

自更新要求 atoll-server 以 systemd 服务运行（`install-server.sh` 安装的就是）。其他方式运行时，请用 `install-server.sh --version vX.Y.Z` 升级。

更新期间面板和 API 会中断几秒；agent 会自动重连，**业务不受影响**。

## 把中控迁移到另一台机器

适用于：中控 VPS 到期不续费、换配置、换机房。全程业务不停，只有面板/API 和 agent 的控制连接中断几分钟。

前提（强烈建议）：面板用**域名**访问（`ATOLL_PUBLIC_URL=https://panel.example.com`），且这个域名在 设置 → DNS 配置的 Cloudflare 区域里。agent 正是靠这个域名找到新中控的。从 v0.1.5 起，中控会自动把这个域名推送给每个 agent 作为备用地址（agent 连上时检查，缺了就补）——迁移前确认所有服务器都已在线连过一次 v0.1.5 及以上的中控（v0.1.4 的中控没有导出功能，要先升级）。

### 1. 在旧中控导出

- 面板：中控 → 导出（输入密码 + 验证码，再输入导出口令），下载得到 `atoll-control-<主机>-<时间>.atollbak`；
- 或在旧主机上用上面的 `atoll-server export`。

导出之后**不要再在旧面板上做改动**（之后的改动不会进入新中控）。想做到零丢失，可以先 `systemctl stop atoll-server`，再用离线 `atoll-server export` 导出——停掉中控不影响业务。

把备份包和口令文件拷到新机器，例如 `/root/atoll-control.atollbak` 和 `/root/pass`。

### 2. 在新 VPS 上安装并恢复

```sh
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-server.sh \
  | sudo sh -s -- --restore /root/atoll-control.atollbak --restore-passphrase-file /root/pass \
      --adopt-public-ip [--tailscale-authkey tskey-auth-...]
```

安装脚本会：安装同版本或最新的 atoll-server → 在**第一次启动前**把备份恢复到 `/var/lib/atoll`（数据库、`master.key`）→ 把备份里的 `server.env` 合并进 `/etc/atoll/server.env`（本次命令行给的参数优先）→ 公网地址是 `https://域名` 时自动装 Caddy 为这个域名签证书 → 启动。

- `--adopt-public-ip`：新中控第一次启动时，把面板域名的 A 记录改成本机公网 IPv4（保留原来的「代理」开关）。不想自动改 DNS 就去掉它，第 3 步手动改。
- `--tailscale-authkey`：旧中控在 tailnet 上时，新中控也要加入 tailnet，agent 才能回到私网连接。新机器在 tailnet 里的名字可能变成 `atoll-control-1`（旧机器还在时），没关系——新中控会把自己的 tailnet 地址重新告诉 agent。
- 恢复时会清掉数据库里写死的旧 tailnet IP 地址（新主机的 IP 不同），也**不会**带过来 `ATOLL_AGENT_TAILNET_ONLY=1`：迁移期间 agent 需要从公网域名找到新中控。所有 agent 都切回 tailnet 后，再按需重新开启。
- 旧的 Caddyfile 放在 `/var/lib/atoll/control-restore/Caddyfile`，仅供对照，不会被安装。
- 不用安装脚本也可以：先装好 atoll-server，`systemctl stop atoll-server`，再
  `atoll-server restore --bundle F --passphrase-file P --data /var/lib/atoll --env-file /etc/atoll/server.env --adopt-public-ip`，然后 `systemctl start atoll-server`。目标目录已有数据库时需要加 `--force`（旧数据会挪到 `/var/lib/atoll/pre-restore-<时间>/`）。

### 3. DNS 切换

- 用了 `--adopt-public-ip` 且面板域名在 Cloudflare 区域内：新中控启动后自动完成，日志里会有 `panel DNS ... now points at <IP>`，审计日志记为 `control.dns_adopt`。
- 否则：手动把面板域名的 A 记录改为新机器 IP。开着 Cloudflare 代理时几乎立即生效；不开代理时受 TTL 影响。
- 业务域名（`blog.example.com` 等）指向的是各 worker，**不需要改**。

### 4. agent 自动重连

每个 agent 依次尝试：主地址（通常是旧中控的 tailnet 地址，连不上）→ 备用地址里的面板域名（现在指向新中控）→ 连上新中控。如果启用了 Tailscale，新中控随即把 agent 切回自己的 tailnet 地址（agent 会先验证新地址可用再切换）。在面板「服务器」页确认所有服务器恢复在线。

没收到过面板域名作为备用地址的 agent（例如一直离线、或面板一直用 IP 访问）不会自动找到新中控，在那台服务器上执行：

```sh
sudo atoll-agent tailnet switch --url https://panel.example.com
```

（先用本机 node token 验证新地址，旧地址保留为备用，然后重启 agent；容器不受影响。）

### 5. 关闭旧中控

所有 agent 都连上新中控后，在旧机器上：

```sh
sudo systemctl disable --now atoll-server
```

**务必关闭**：旧中控还开着的话，通过旧 tailnet 地址连接的 agent 会一直连在旧中控上（两个中控同时管理同一批服务器）。确认无误后再退订旧 VPS。

### 停机说明

| 什么 | 影响 |
|---|---|
| 业务容器、对外域名、HTTPS | **不受影响**（入口和证书都在各 worker 上） |
| 定时服务备份 | 不受影响（由 agent 本地调度，结果在重连后补报） |
| 面板 / API / CLI / MCP | 从旧中控停止到新中控启动、DNS 生效，通常几分钟 |
| 部署、迁移、域名变更 | 中控不可用期间无法操作 |
| agent 控制连接 | 主地址失败 3 次后切到备用地址，通常一两分钟内重连 |

## 恢复到原机器（灾难恢复）

中控数据损坏、误操作：

```sh
sudo systemctl stop atoll-server
sudo atoll-server restore --bundle /var/lib/atoll/control-backups/<id>.atollbak \
     --passphrase-file /root/pass --data /var/lib/atoll --force
sudo systemctl start atoll-server
```

本地已没有备份时，从存储桶 `<前缀>/control/` 下载一份（用任意 S3 客户端）。
