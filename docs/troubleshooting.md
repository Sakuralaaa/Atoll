# 故障排查

先记录 Atoll 版本、操作系统/架构、涉及的服务或节点 ID，以及出现问题前做过的操作。分享日志前，请删掉密码、API token、Cloudflare/S3 凭据、会话 cookie 和业务数据。

## 面板打不开或登录失败

- 检查中控服务：`sudo systemctl status atoll-server`。
- 查看日志：`sudo journalctl -u atoll-server -f`。
- 确认域名解析到了中控主机，且 HTTPS 反向代理仍在运行。
- 忘记管理员密码时，在中控主机执行 `sudo atoll-server reset-password`。
- 两步验证设备和恢复码都丢失时，在中控主机执行 `sudo atoll-server admin reset-2fa <用户名>`，再重新登录并配置 TOTP。

## 节点显示离线

Agent 会主动连接中控；先确认节点能访问中控 URL 和 HTTPS/WebSocket 端点，再检查节点服务：

```bash
sudo systemctl status atoll-agent
sudo journalctl -u atoll-agent -f
```

同时检查中控日志 `sudo journalctl -u atoll-server -f`。Agent 断线后会自动重连；不要为了让面板显示在线而开放不必要的入站端口。若近期升级过 agent，先看升级状态和节点日志。

## 域名没有指向服务

- 核对服务确实有 HTTP 端口，域名绑定到了正确的服务和端口。
- 检查 A 记录是否指向该服务所在节点的公网 IP；如果 Atoll 未托管 DNS，需要手动维护记录。
- 如果希望自动创建 Cloudflare 记录，检查根域名配置、令牌是否有效，并确保它有目标 zone 的 DNS 编辑权限。
- Cloudflare 橙云与灰云的代理、证书要求不同；检查面板显示的域名状态与证书提示。
- 需要查看面板端细节时，检查中控日志；节点上的入口由 agent 管理，可同时检查 agent 日志。

## 源码构建失败

- 目前源码构建针对 GitHub 仓库，仓库需要有 Dockerfile；Atoll 不会自动识别 Node/Python 等项目并生成构建配置。
- 在服务页查看构建日志，并确认分支、构建上下文和 Dockerfile 路径正确。
- 确认节点 Docker 可用、磁盘空间充足。构建资源限制和 BuildKit 行为见[旧版详细说明](https://github.com/Sakuralaaa/Atoll/blob/main/README.md#从源码构建服务)。

## 备份或异机恢复失败

- 检查「设置 → 备份」中的 S3 endpoint、bucket、region 和凭据；确认节点能访问存储端点。
- 确认目标节点在线、可部署服务，备份快照仍然可用。
- 跨机恢复是原机器丢失时的灾难恢复流程：源节点仍在线时，面板会拒绝把同一项目恢复到另一台机器；请使用迁移功能。
- 在恢复作业详情检查失败步骤与错误信息，并查看中控/节点日志。恢复后不要仅凭任务已启动判断成功，应检查目标服务、域名和数据。
- 备份配置与中控灾难恢复细节见[中控运维手册](control.md)及 [API 文档](api.md)。

## 升级后 agent 没有连回

面板会显示升级结果；agent 更新失败时会在限定时间内回滚到旧二进制。检查服务是否运行、日志是否有启动错误，并确认节点能重新访问中控。旧版本 agent 可能需要重新运行面板生成的加入命令；具体提示以当前面板为准。

## 仍未解决

先查看 [常见问题](faq.md)、[已有 issues](https://github.com/Sakuralaaa/Atoll/issues)和[详细手册](https://github.com/Sakuralaaa/Atoll/tree/main/docs)。提交 bug 时附上脱敏后的复现步骤、版本、环境和相关日志。
