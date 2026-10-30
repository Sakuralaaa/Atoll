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
- **CLI / API / MCP**：`atoll` 命令行、带 scope 的 `atl_pat_` token、`atoll mcp` 供 Claude Code / Codex 等外部 agent 使用；破坏性操作需在面板确认。

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
# 中控
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-server.sh | sudo sh
# 节点（在面板「服务器 → 添加」里复制带 token 的命令）
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-agent.sh | sudo sh -s -- --server https://panel.example.com --token atl_join_xxx
```

## License

[AGPL-3.0](LICENSE). 部分 Kubernetes 投影规则移植自 [zeabur-oss/zeabur](https://github.com/zeabur-oss/zeabur)（Apache-2.0），见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
