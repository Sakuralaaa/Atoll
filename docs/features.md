# 功能概览

Atoll 由中控 `atoll-server`、运行在每台 VPS 上的 `atoll-agent`，以及可选的 CLI / MCP 组成。Agent 主动连回中控；服务容器、构建和备份作业都在节点上执行。正常情况下，用户流量由节点上的 Caddy 直接转发到容器。

## 能做什么

| 范围 | 能力 |
| --- | --- |
| 部署 | Docker 镜像、私有镜像凭据、GitHub 源码构建、环境变量、配置文件、卷与端口；构建需要仓库包含 Dockerfile。 |
| 数据服务 | PostgreSQL、MySQL、Redis、MongoDB 模板，自动生成初始密码和服务连接变量。 |
| 域名与 HTTPS | 绑定 HTTP 服务端口、多根域名、Cloudflare DNS、代理状态切换、ACME 或 Origin CA 证书。也可自行管理 DNS。 |
| 迁移 | Atoll 项目/服务在节点之间迁移；支持从 Zeabur 导入，以及接管已有 Docker 容器。 |
| 备份 | restic 备份到 S3 兼容存储；支持服务数据和卷备份、在另一台在线节点上恢复。 |
| 更新与运维 | 镜像 digest / tag 更新检测、agent 与中控升级、日志、资源指标、审计和问题提示。 |
| 权限 | 管理员与子账号；API token 可按权限最小化并限制项目范围；容器内 exec 是独立权限。 |
| 自动化接口 | CLI、REST API 和 MCP；危险操作可以配置为需要面板审批。 |

## 私有网络（可选）

可配置 Tailscale 供中控与节点之间的机器通信、跨节点服务互通。面板仍通过公网 HTTPS 访问。具体 ACL、端口范围和部署步骤见 [API 文档中的 Tailscale 部分](api.md#private-network-tailscale)。

## 安全要点

- 使用 HTTPS；管理员账号建议启用 TOTP 两步验证。
- API token 使用最小权限，并仅在确实需要时授予 `exec`、`delete`、`migrate` 等高风险权限。
- 代理环境下只配置可信反向代理；不要盲目信任客户端提供的转发头。
- 备份存储凭据和其他敏感配置在中控侧加密保存；仍应保护中控主机、备份及其密钥，静态加密不能抵御已控制主机的攻击者。
- 对漏洞请按 [SECURITY.md](../SECURITY.md) 私下报告，不要在公开 issue 粘贴 token、密码或可利用细节。

## 当前边界

项目目前处于 **pre-alpha**，尚未作生产环境规模验证，也没有发布节奏或跨版本直升承诺。源码构建目前只支持 GitHub，仓库需要提供 Dockerfile；缺少 Dockerfile 时不会自动推断构建方式。更多细节见 [REST API 文档](api.md)、[中控运维手册](control.md)和[路线图](../ROADMAP.md)。
