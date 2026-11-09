# 常见问题

## 中控离线会让已部署服务停止吗？

不会。已运行的容器由节点上的 Docker 管理，正常用户流量由该节点上的 Caddy 转发。中控离线期间无法正常进行面板管理、发布新部署或调度操作；请仍然监控节点自身状态。

## 节点需要开放 agent 入站端口吗？

通常不需要。Agent 主动通过 WebSocket 连回中控。用户访问服务的端口、以及可选的私有网络配置，按你的部署和网络策略单独决定。

## 一定要用 Cloudflare 吗？

不必。Cloudflare 配置好后，Atoll 可以代管域名的 A 记录；不使用时，仍可绑定域名并自己将 A 记录指向服务节点。证书模式、DNS 传播和代理状态会影响访问方式。

## 一定要用 S3 备份吗？

要使用面板内置的 restic 备份功能，需要配置 S3 兼容存储。中控配置、备份目标和服务备份分别管理；灾难恢复步骤见[中控手册](control.md)。

## Atoll 可以从任意 Git 仓库构建吗？

目前源码构建路径面向 GitHub。仓库需要提供 Dockerfile，且构建在服务所在节点上执行；尚不支持 GitLab、Gitea 或通用 git 源码，也不会在没有 Dockerfile 时自动推断语言或构建命令。

## 当前支持哪些系统？

安装文档针对 Debian / Ubuntu 的 Linux amd64 与 arm64 主机。桌面浏览器访问面板；另有 Android 客户端。其他 Linux 发行版和架构不在这里作兼容承诺。

## 它适合直接承载生产业务吗？

项目目前标记为 **pre-alpha**，尚未完成大规模生产验证。应先在非关键服务上部署，演练备份恢复，并自行评估风险。当前没有已批准的支持版本矩阵、直升承诺或固定发版节奏。

## 在哪里提 bug 或需求？

使用仓库的 [Bug 报告](https://github.com/Sakuralaaa/Atoll/issues/new?template=bug_report.yml) 或 [功能请求](https://github.com/Sakuralaaa/Atoll/issues/new?template=feature_request.yml)模板。漏洞请按 [SECURITY.md](../SECURITY.md) 私密报告，不要公开提交漏洞细节。
