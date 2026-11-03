# Changelog

每个版本的说明和升级步骤在 [docs/releases/](docs/releases/)，发布时自动附在 GitHub Release 上。

- [v0.1.11](docs/releases/v0.1.11.md)：源码构建的清理：删除服务时删构建镜像、构建容器空闲 10 分钟自动停止、每日修剪构建缓存到 5 GB、删除遗留的 `atoll-builder-*` 构建器。
- [v0.1.9](docs/releases/v0.1.9.md)：无卷服务零停机部署、默认资源上限、每周备份校验和每月恢复演练、列表筛选标签、导出 .env、Playwright 冒烟测试。
- [v0.1.8](docs/releases/v0.1.8.md)：异常与通知、环境变量批量编辑、服务日志标签、多面板 context、MCP 工具补齐、历史数据清理。
- [v0.1.7](docs/releases/v0.1.7.md)：部署失败自动回滚、`--wait-ready` 等部署完成、安装命令固定版本并校验、Ctrl K 命令面板、确认弹窗统一。
- [v0.1.6](docs/releases/v0.1.6.md)：在面板里一键升级 agent（批量、校验、自动回滚）。
- [v0.1.5](docs/releases/v0.1.5.md)：安全加固、构建资源限制、中控管理、源码构建、Go 1.26.10。从 v0.1.4 升级请先看这里。
