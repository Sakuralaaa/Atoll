<p align="center">
  <img alt="Atoll" src="./web/public/favicon.svg" width="88" />
</p>

<h1 align="center">Atoll</h1>

<p align="center"><strong>自托管的多 VPS 容器面板：一台轻量中控 + 每台机器一个 agent + 原生 Docker</strong></p>

<p align="center">简体中文 | <a href="./README.en.md">English</a></p>

<p align="center">
  <a href="https://github.com/Sakuralaaa/Atoll/releases"><img alt="Release" src="https://img.shields.io/github/v/release/Sakuralaaa/Atoll?include_prereleases&label=release&color=8b5cf6" /></a>
  <a href="./go.mod"><img alt="Go" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" /></a>
  <a href="./web/package.json"><img alt="React" src="https://img.shields.io/badge/React-19-61DAFB?logo=react&logoColor=111827" /></a>
  <img alt="Platform" src="https://img.shields.io/badge/Linux-amd64%20%7C%20arm64-FCC624?logo=linux&logoColor=111827" />
  <a href="./LICENSE"><img alt="License" src="https://img.shields.io/badge/license-AGPL--3.0-blue" /></a>
</p>

> **状态：pre-alpha。** CI 测试通过不等于已完成生产环境验证。请先在非关键服务上试用，并演练备份恢复。

## 文档

- [文档索引](docs/README.md)
- [快速开始](docs/quick-start.md)：安装中控、添加节点、部署第一个服务。
- [功能概览](docs/features.md)：部署、域名、迁移、备份与权限边界。
- [故障排查](docs/troubleshooting.md) · [常见问题](docs/faq.md) · [运维参考](docs/operations.md)

## Atoll 是什么

Atoll 由中控 `atoll-server`、每台 VPS 上的 `atoll-agent`，以及可选的 CLI / REST API / MCP 组成。Agent 主动通过 WebSocket 连回中控；服务、源码构建和备份在节点上运行。已部署容器由 Docker 管理，用户流量由节点上的 Caddy 直接转发。

面板管理 Docker 镜像和 GitHub 源码构建、环境变量、卷、端口、域名、迁移和备份。项目内服务可通过名字互通；Tailscale 私有网络、Cloudflare DNS 和 S3 兼容备份存储均为可选集成。源码构建目前要求 GitHub 仓库提供 Dockerfile。

## 安装

需要 Debian / Ubuntu（amd64 或 arm64）、root 权限，以及一个解析到中控主机的域名：

```bash
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-server.sh \
  | sudo sh -s -- --domain panel.example.com
```

打开 `https://panel.example.com` 创建管理员账号，再在「服务器 → 添加」生成并运行节点加入命令。首次部署、域名和备份配置请按[快速开始](docs/quick-start.md)操作。

## 内置演示

前端内置了不连接真实服务器的演示后端。要在本地试用，在 `web/` 目录运行：

```bash
npm ci
VITE_DEMO=1 npm run dev
```

Playwright CI 也使用这个演示模式覆盖登录、创建服务、绑定域名、子账号权限和中英文切换。

## 文档与社区

| 内容 | 链接 |
| --- | --- |
| REST API、agent 协议、权限与备份 | [docs/api.md](docs/api.md) |
| 中控备份、升级与灾难恢复 | [docs/control.md](docs/control.md) |
| Zeabur 迁移 / Docker 容器接管 | [迁移说明](docs/migrate-from-zeabur.md) · [接管说明](docs/adopt-docker.md) |
| 漏洞报告 | [SECURITY.md](SECURITY.md)（使用 GitHub 私密漏洞报告，不要公开漏洞细节） |
| 贡献与讨论方向 | [CONTRIBUTING.md](CONTRIBUTING.md) · [行为准则](CODE_OF_CONDUCT.md) · [路线图](ROADMAP.md) |
| Bug / 功能请求 | 使用仓库的 [Bug 模板](https://github.com/Sakuralaaa/Atoll/issues/new?template=bug_report.yml) 或 [功能请求模板](https://github.com/Sakuralaaa/Atoll/issues/new?template=feature_request.yml) |

## 开发与测试

```bash
# 前端静态检查、构建和演示模式 E2E
cd web && npm ci && npm run lint && npm run build && npx playwright install chromium && npm run test:e2e

# 回到仓库根目录运行后端检查与完整测试
cd ..
go vet ./... && go test -race ./...
sh scripts/test-installers.sh
```

所有 GitHub Actions 用于构建与测试；演示模式 UI 流程在 CI 中运行。贡献约定见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## License

[AGPL-3.0](LICENSE)。第三方 notices 见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。
