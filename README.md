# Atoll

自托管的多 VPS 容器管理面板：一台轻量中控 + 每台机器一个小 agent + 原生 Docker。手感参考 Zeabur，专注「从镜像部署 + 图形化管理」。

> Status: pre-alpha. 代码可编译、测试通过，但尚未在真实生产环境验证。

## 特点

- **中控离线不影响业务**：流量直达各台 VPS（Caddy 入口），中控只做管理与监控。
- **零手动 DNS**：绑定子域名时自动在 Cloudflare 创建 proxied 记录指向对应机器。
- **镜像部署**：Docker Hub / GHCR / 私有仓库，环境变量跨服务引用，卷、端口、域名、回滚。
- **一键数据库模板**：PostgreSQL / MySQL / Redis / MongoDB。
- **项目迁移**：在 VPS 之间迁移服务（卷 + 数据库导出恢复）。
- **从 Zeabur 原地接管**：在原先交给 Zeabur 管理的同一台 VPS 上扫描 k3s → 导入 → 演练 → 切换 → 可回滚 → 最后卸载 k3s。详见 [docs/migrate-from-zeabur.md](docs/migrate-from-zeabur.md)。
- **定时备份**：restic 直传任意 S3 兼容存储（R2 / B2 / S3 / MinIO），数据库逻辑导出 + 卷快照，保留策略可配；由各节点 agent 本地调度，中控离线也照常备份；恢复前自动保留一份当前数据，原机器宕机时可恢复到另一台 VPS。详见 [docs/api.md](docs/api.md#backups)。
- **镜像更新检测**：定期比对镜像仓库的 digest 与新的 semver tag，可仅提醒或在时间窗内自动更新（同 tag 新 digest），一键应用、可回滚。
- **CLI / API / MCP**：`atoll` 命令行、按权限细分（read / deploy / delete / migrate / domains / restore / servers / settings / tokens）并可限定项目的 `atl_pat_` token（`atoll token create ci --perm deploy --project shop`）、`atoll mcp` 供 Claude Code / Codex 等外部 agent 使用，只暴露 token 有权限的工具；有权限的操作直接执行，缺权限返回 403 并指明所需权限，仅对开启 `require_approval` 的 token 需在面板确认。

## 组成

| 组件 | 路径 |
| --- | --- |
| 中控（API + 内嵌面板） | `cmd/atoll-server`, `internal/server` |
| 节点 agent | `cmd/atoll-agent`, `internal/agent` |
| CLI / MCP | `cmd/atoll`, `internal/cli` |
| Web 面板 | `web/`（React + Vite + Tailwind） |
| 安装脚本 / systemd | `install/`, `deploy/` |

## 构建

```bash
# 面板
(cd web && npm ci && npm run build) && internal/server/webui/sync.sh
# 二进制
go build ./cmd/atoll-server ./cmd/atoll-agent ./cmd/atoll
go test ./...
```

## 安装（发布 Release 之后）

```bash
# 中控（--domain 时自动安装 Caddy 并签发 Let's Encrypt 证书，面板只监听 127.0.0.1；域名需先解析到本机）
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-server.sh | sudo sh -s -- --domain panel.example.com
# 节点（在面板「服务器 → 添加」里复制带 token 的命令）
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-agent.sh | sudo sh -s -- --server https://panel.example.com --token atl_join_xxx
```

节点 agent 主动连出到中控：WebSocket over HTTPS（`wss://<面板域名>/agent/v1/connect`），用加入时换取的节点 token（Bearer）认证，节点无需开放任何入站端口。没有使用 mTLS，所以中控务必放在 HTTPS 后面（`--domain` 或你自己的 TLS 反代）。

### 私有网络（Tailscale，可选）

面板仍走公网 HTTPS；中控 ↔ 节点、跨服务器的服务互访可以改走 Tailscale。**只有中控和节点需要装 Tailscale，你自己的电脑不需要**（面板、CLI 都用公网地址）。电脑上同时开着 Tailscale 和 Clash TUN 的，在规则里加 `IP-CIDR,100.64.0.0/10,DIRECT`。

1. Tailscale 后台：ACL 里 `tagOwners` 加 `"tag:atoll": ["autogroup:admin"]`，并放行 `tag:atoll` → `tag:atoll:7301,41000-41999`；生成 auth key：**可复用、非临时（ephemeral 关）、预授权、标签 `tag:atoll`**。
2. 中控：`install-server.sh --domain panel.example.com --tailscale-authkey tskey-auth-xxx [--agent-tailnet-only]`（以 `atoll-control` 加入 tailnet，`ATOLL_AGENT_LISTEN=tailscale:7301`），再到面板「设置 → 私有网络」选 Tailscale 并填同一个 key。中控的 tailnet 地址要用 **100.x IP**（节点用 `--accept-dns=false`，解析不了 MagicDNS 名）。
3. 节点：照常运行上面的安装命令。脚本会向中控 bootstrap、安装 Tailscale、`tailscale up --ssh=false --accept-dns=false --accept-routes=false`（已在线则跳过，key 不会打印），经 tailnet 地址加入，公网地址留作备用；tailnet 不通时警告并走公网，之后可在「服务器 → 加入 tailnet」或 `atoll-agent tailnet up` 迁移。

`--agent-tailnet-only`（`ATOLL_AGENT_TAILNET_ONLY=1`）之后公网上的 `/agent/*` 一律 404（`/agent/v1/bootstrap` 除外），自己配 Caddy 的也要同样处理。详见 [docs/api.md](docs/api.md#private-network-tailscale)。

## 安全

面板可以直接放在公网域名上，靠以下几层保护：

- **HTTPS**：用 `install-server.sh --domain`（Caddy + Let's Encrypt）或你自己的 TLS 反代；面板只监听 127.0.0.1。会话 cookie 为 `HttpOnly`、`Secure`、`SameSite=Strict`，写操作还会校验 Origin。
- **两步验证（TOTP）**：在面板「设置 → 安全」里启用——输入密码、用验证器 App（Google Authenticator / 1Password / Bitwarden 等）扫码、输入 6 位验证码确认，然后保存 10 个一次性恢复码。启用、停用都会让其他会话下线。「设置 → 安全」里也能查看和退出登录会话。
- **防爆破**：密码和验证码失败按来源 IP 与账号分别计数，超过次数后指数退避锁定（IP 最长 1 小时，账号最长 15 分钟），失败会写入日志。只信任本机反代（Caddy）的 `X-Forwarded-For`，其他情况用 `--trusted-proxies` 配置。
- **找回**：忘记密码 `sudo atoll-server reset-password`；手机和恢复码都丢了 `sudo atoll-server admin reset-2fa <用户名>`（都需要在中控服务器上执行，会让该用户所有会话下线）。
- **CLI / API 令牌**（`atl_pat_`）不走两步验证，只在面板里创建，按需选择最小 scope。

细节见 [docs/api.md](docs/api.md#two-factor-login-totp)。

## 发布 Release

推送 `v*` tag 即触发 [.github/workflows/release.yml](.github/workflows/release.yml)（构建面板、跑测试、打包二进制并发布 Release）：

```bash
git tag v0.1.0 && git push --tags
```

## License

[AGPL-3.0](LICENSE). 部分 Kubernetes 投影规则移植自 [zeabur-oss/zeabur](https://github.com/zeabur-oss/zeabur)（Apache-2.0），见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
