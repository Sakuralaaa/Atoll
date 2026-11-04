# Changelog

每个版本的说明和升级步骤在 [docs/releases/](docs/releases/)，发布时自动附在 GitHub Release 上。

- [v0.1.25](docs/releases/v0.1.25.md)：接管自己用 docker run / compose 启动的容器（同端口、数据原地挂载、旧容器保留可回滚；尚未在真实 Docker 主机上验证，接管后勿把 agent 降级）；默认关闭 MPTCP 监听以缓解 CVE-2026-98432。
- [v0.1.24](docs/releases/v0.1.24.md)：多个根域名（设置里管理列表、自动匹配 Cloudflare Zone、自动签 Origin 证书失败则用 ACME），项目可选默认根域名，绑定域名时选后缀。中控和节点都要升级；新根域名的 Cloudflare SSL 请用 Full (strict)。
- [v0.1.23](docs/releases/v0.1.23.md)：修复卡片上点隐藏的 IP 会进入详情页，现在点地址即可显示。
- [v0.1.22](docs/releases/v0.1.22.md)：IP 地址默认隐藏（可单个或一键显示），服务器卡片标签移到标题行、底栏对齐。
- [v0.1.21](docs/releases/v0.1.21.md)：项目「显示名称」（可重复，项目名称仍全局唯一），机器卡片底栏对齐。
- [v0.1.20](docs/releases/v0.1.20.md)：服务「显示名称」，只改面板显示，不重新部署、不影响内网主机名。
- [v0.1.19](docs/releases/v0.1.19.md)：项目页按机器分组；按 IP 自动显示国旗和推测服务商（可在设置里关闭）；国旗改用 SVG；更多服务商图标、名称不区分大小写。
- [v0.1.18](docs/releases/v0.1.18.md)：Zeabur 接管迁移 PostgreSQL 时保留所有者、库级授权和设置（仅 agent）。
- [v0.1.17](docs/releases/v0.1.17.md)：Zeabur 接管重跑 PostgreSQL 迁移时先删副本再恢复，修复分区表最终同步失败（仅 agent）。
- [v0.1.16](docs/releases/v0.1.16.md)：Zeabur 接管修复：照搬容器用户（新 `user` 字段）、复制 PostgreSQL 角色和密码、支持 TimescaleDB、演练尊重固定的镜像 digest。
- [v0.1.15](docs/releases/v0.1.15.md)：设置页保存按钮统一、删掉重复小字、手机上 Webhook 地址完整显示。
- [v0.1.14](docs/releases/v0.1.14.md)：新标志与图标、固定版本的镜像不再提示更新并显示版本号、可改用户名和中控名称、说明文字收进 ⓘ（完整说明见 docs/ui-guide.md）、手机布局修正。
- [v0.1.11](docs/releases/v0.1.11.md)：源码构建的清理：删除服务时删构建镜像、构建容器空闲 10 分钟自动停止、每日修剪构建缓存到 5 GB、删除遗留的 `atoll-builder-*` 构建器。
- [v0.1.9](docs/releases/v0.1.9.md)：无卷服务零停机部署、默认资源上限、每周备份校验和每月恢复演练、列表筛选标签、导出 .env、Playwright 冒烟测试。
- [v0.1.8](docs/releases/v0.1.8.md)：异常与通知、环境变量批量编辑、服务日志标签、多面板 context、MCP 工具补齐、历史数据清理。
- [v0.1.7](docs/releases/v0.1.7.md)：部署失败自动回滚、`--wait-ready` 等部署完成、安装命令固定版本并校验、Ctrl K 命令面板、确认弹窗统一。
- [v0.1.6](docs/releases/v0.1.6.md)：在面板里一键升级 agent（批量、校验、自动回滚）。
- [v0.1.5](docs/releases/v0.1.5.md)：安全加固、构建资源限制、中控管理、源码构建、Go 1.26.10。从 v0.1.4 升级请先看这里。
