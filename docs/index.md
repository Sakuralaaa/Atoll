---
layout: home
hero:
  name: Atoll
  text: 自托管的多 VPS 容器面板
  tagline: 一台中控 + 每台机器上的 agent + 原生 Docker。部署、域名、备份、迁移都在你自己的服务器上完成；中控离线不影响业务。
  actions:
    - theme: brand
      text: 快速开始
      link: /quick-start
    - theme: alt
      text: 用 Docker 安装中控
      link: /install-docker
    - theme: alt
      text: 在线演示
      link: https://sakuralaaa.github.io/Atoll/demo/?demo=1
      target: _blank
features:
  - title: 部署
    details: Docker 镜像、私有仓库、GitHub 仓库源码构建（需要 Dockerfile）、环境变量、卷、端口，一键回滚；PostgreSQL / MySQL / Redis / MongoDB 模板和一键应用。
  - title: 域名与 HTTPS
    details: 每台机器自己的 Caddy 入口；多根域名；配置 Cloudflare 后自动建 DNS 记录，Origin CA 或 ACME 证书。
  - title: 备份
    details: restic 直传任意 S3 兼容存储，由节点本地调度，中控离线照常备份；定期仓库检查与恢复演练。
  - title: 迁移与接管
    details: 服务在机器之间迁移；原地接管 Zeabur 项目和自己 docker run / compose 起的容器；一条命令迁移中控。
  - title: 权限
    details: 管理员与子账号、按服务器分配、配额；最小权限、可限定项目的 API 令牌；危险操作可要求面板审批。
  - title: 自动化
    details: atoll CLI、REST API 和 atoll mcp，让 Claude Code、Codex 等 AI agent 在令牌权限内操作。
---

> **项目状态：pre-alpha。** 生产使用前请先在非关键服务上验证，并演练备份恢复。

## 组成

- **中控 `atoll-server`**：REST API、内嵌面板、SQLite、agent hub。用[安装脚本](quick-start.md)装成 systemd 服务，或[用 Docker 运行](install-docker.md)。
- **`atoll-agent`**：每台节点一个，主动用 WebSocket 连回中控，节点不开入站端口；管理本机 Docker、Caddy 入口和备份。
- **`atoll` CLI / MCP**：给人和 AI agent 用，见 [CLI 与 MCP](cli-mcp.md)。

在线演示是面板的内置演示模式：数据都在浏览器内存里，可以随意点，刷新即重置。
