# 快速开始

本指南从一台空白 Linux 主机开始，带你启动 Atoll、加入一台节点机器并创建第一个服务。

## 准备

- 一台 Debian 或 Ubuntu 的中控主机，以及至少一台 Debian / Ubuntu 节点（amd64 或 arm64）。
- 主机上的 root 权限；节点需要安装并运行 Docker。
- 一个解析到中控主机的域名。若暂时没有域名，也可以自备 HTTPS 反向代理。
- 面板的备份目标建议准备一个 S3 兼容存储桶。

Atoll 目前处于 **pre-alpha**。请先在不重要的业务上试用，确认备份和恢复后再承载重要服务。

## 1. 安装中控

将 `panel.example.com` 换成已经解析到中控主机的域名：

```bash
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-server.sh \
  | sudo sh -s -- --domain panel.example.com
```

安装脚本会配置中控服务、Caddy 和 HTTPS；面板默认只监听本机，由反向代理对外提供访问。安装后打开 `https://panel.example.com`，创建第一个管理员账号，并在「设置 → 安全」启用两步验证。

如果使用自己的 HTTPS 反向代理或需要额外配置，请看[中控运维手册](control.md)。

## 2. 添加节点

在面板的「服务器 → 添加」生成安装命令，并在目标节点上执行。命令包含一次性加入 token 和校验 agent 二进制的 SHA-256：

```bash
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-agent.sh \
  | sudo sh -s -- --server https://panel.example.com --token atl_join_xxx \
      --sha256-amd64 … --sha256-arm64 …
```

请直接使用面板生成的完整命令，不要重复使用已经过期的一次性 token。Agent 主动通过 WebSocket 连回中控；节点不需要开放 agent 入站端口。

## 3. 部署第一个服务

1. 在「项目」里创建项目并选择节点。
2. 打开项目，选择「新建服务」，再选择 Docker 镜像或数据库模板。
3. 填写镜像名（例如 `nginx:1`）、服务名和 HTTP 端口，点击「部署」。
4. 在服务页面选择「绑定域名」。配置好 Cloudflare 根域名和令牌时，Atoll 会尝试自动创建 DNS 记录；否则请手动创建指向节点公网 IP 的 A 记录。

项目内的服务可通过服务名互相访问。跨节点通信需要先在「设置 → 私有网络」配置 Tailscale，详见[功能概览](features.md)和 [API/运维文档](api.md)。

## 4. 先完成备份配置

在「设置 → 备份」填写 S3 兼容存储的 endpoint、bucket 和凭据，为需要保护的服务启用备份策略。恢复到另一台机器时，目标节点必须在线；跨机恢复用于原节点已经丢失的情况。如果原节点仍在线，优先使用迁移功能。

## 下一步

- [功能概览](features.md)
- [故障排查](troubleshooting.md)
- [常见问题](faq.md)
- [从 Zeabur 迁移](migrate-from-zeabur.md) · [接管 Docker 容器](adopt-docker.md)
