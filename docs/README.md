# Atoll 文档

Atoll 是自托管的多 VPS 容器面板：由一台中控、每台机器上的 agent 和原生 Docker 组成。

在线阅读：<https://sakuralaaa.github.io/Atoll/> · 在线演示：<https://sakuralaaa.github.io/Atoll/demo/?demo=1>

> **项目状态：pre-alpha。** 生产使用前请先在非关键服务上验证，并演练备份恢复。

## 上手与常见问题

- [快速开始](quick-start.md)：安装中控、添加节点并部署第一个服务。
- [用 Docker 安装中控](install-docker.md)：官方镜像 + Docker Compose，可选 Caddy HTTPS。
- [功能概览](features.md)：了解部署、域名、迁移、备份与权限模型。
- [故障排查](troubleshooting.md)：处理节点离线、DNS、构建、登录和恢复问题。
- [常见问题](faq.md)：架构、依赖、平台与当前限制。
- [运维参考](operations.md)：多节点、Tailscale、源码构建和日常维护。

## 参考手册

- [备份](backups.md) · [域名与 HTTPS](domains.md) · [子账号](subaccounts.md) · [CLI 与 MCP](cli-mcp.md) · [安全](security.md)
- [REST API、agent 协议与权限](api.md)
- [中控备份、自更新与迁移](control.md)
- [版本兼容与升级承诺](compatibility.md)
- [2026-11 安全自审](security-review-2026-11.md)
- [从 Zeabur 迁移](migrate-from-zeabur.md)
- [接管已有 Docker 容器](adopt-docker.md)
- [贡献说明](../CONTRIBUTING.md) · [安全政策](../SECURITY.md) · [路线图](../ROADMAP.md)
