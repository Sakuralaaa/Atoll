# Atoll 路线图

这是项目方向的公开记录，不是排期、支持矩阵或发版承诺。Atoll 仍处于 **pre-alpha**；优先级会根据实际使用反馈、安全性和维护成本变化。

## 当前重点

- 提高迁移、异机恢复与数据库升级测试的稳定性，并在真实机器上跑安装、部署、备份、恢复、迁移和接管的全流程测试。
- 将前端演示模式的 Playwright 测试纳入 CI，覆盖常用关键流程。
- 持续审查凭据处理、API token 权限、agent 执行面和子账号隔离。下一项：备份按节点分仓库、分凭据（现在所有节点共用一份 S3 密钥和 restic 密码，见 docs/security-review-2026-11.md）。
- 改善快速开始、故障排查和社区贡献入口。

## 值得根据需求探索的方向

以下是候选事项，不代表已承诺开发或有发布时间：

- 定时任务（cron）。
- GitLab、Gitea 与通用 git 源码构建。
- 缺少 Dockerfile 时的构建方式检测或辅助配置。
- 可配置健康检查、更多通知渠道和 Prometheus 指标。
- Docker 方式安装中控与首次启动向导。
- PR 预览环境、OIDC 登录和多副本运行。

欢迎通过 [功能请求模板](https://github.com/Sakuralaaa/Atoll/issues/new?template=feature_request.yml)描述具体问题、工作流、规模和安全约束；可复用的真实需求会帮助安排优先级。

## 尚未定义的承诺

版本兼容与升级规则已写入 [docs/compatibility.md](docs/compatibility.md)：stable/beta 通道、0.x 破坏性变化写进发布说明、迁移只前进、任意 ≥ v0.1.40 的中控可直升最新、不支持降级中控、agent 可回滚、中控与 agent 的版本差规则、弃用至少保留 2 个小版本。

仍未定义的：安全修复的回溯（backport）范围、响应时间 SLA、固定发版节奏，以及 1.0 之后的长期支持策略。
