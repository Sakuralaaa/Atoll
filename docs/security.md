# 安全

面板可以直接放在公网上，靠下面几层保护。发现漏洞请按 [SECURITY.md](../SECURITY.md) 私下报告，不要在公开 issue 里粘贴 token、密码或可利用细节。

## 访问面板

- **HTTPS**：用安装脚本的 `--domain`（Caddy + Let's Encrypt）、Docker 部署的 `https` profile，或你自己的 TLS 反向代理；面板只监听本机回环。
- **Cookie 与页面**：会话 cookie 是 `HttpOnly` + `Secure` + `SameSite=Strict`，写操作校验 Origin；面板页面带 CSP，HTTPS 下带 HSTS。
- **会话**：最长 30 天，7 天不用自动失效。「设置 → 安全」里可以查看和踢掉会话。
- **两步验证（TOTP）**：「设置 → 安全」里启用，保存好 10 个一次性恢复码。启用和停用都会让其他会话下线。
- **防爆破**：密码和验证码错误按 IP 和账号分别计数，指数退避锁定（IP 最长 1 小时，账号最长 15 分钟）。
- **可信代理**：默认只信任本机回环发来的 `X-Forwarded-For`；其他反向代理用 `--trusted-proxies`（`ATOLL_TRUSTED_PROXIES`）声明。Docker compose 已经只信任 Caddy 容器的固定 IP。

## API 令牌

令牌（`atl_pat_`）只能在面板创建，不走两步验证，请按需授予最小权限并限定项目。`exec` 能读取容器里的数据和私有变量，单独授予；开启 `require_approval` 后危险操作需要在面板审批。使用高风险权限的每次请求都会写入审计日志。详见 [CLI 与 MCP](cli-mcp.md)。

## 节点

- agent 主动连出，节点不开入站端口；节点 token 只通过 `Authorization` 头传递，不出现在 URL 里。
- 加入命令里的 `--sha256-*` 让安装脚本在运行 agent 二进制之前先校验。
- 服务的 `public_port` 不能占用 22、7301、41641 和 41000–41999。

## 数据与密钥

- Cloudflare 令牌、备份凭据、Tailscale key、加密环境变量等在中控侧加密保存，密钥是数据目录里的 `master.key`。静态加密挡不住已经控制了中控主机的攻击者，请保护好中控主机和它的备份。
- 中控备份用口令加密；restic 仓库密码和中控备份口令都请存放在中控以外的地方。

## 找回访问

在中控主机上执行（Docker 部署在前面加 `docker compose exec server`）：

```sh
sudo atoll-server reset-password
sudo atoll-server admin reset-2fa <用户名>
```

## 校验下载的文件

Release 附带 `SHA256SUMS`、它的 Sigstore 签名 `SHA256SUMS.sigstore.json`（v0.1.45 起）和 GitHub 构建来源证明。先验签名，再验文件：

```sh
cosign verify-blob --bundle SHA256SUMS.sigstore.json \
  --certificate-identity-regexp '^https://github.com/Sakuralaaa/Atoll/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify atoll-server-linux-amd64 --repo Sakuralaaa/Atoll
```

签名是 GitHub Actions 发版工作流的无密钥签名（证书身份就是上面的 release.yml），不需要导入公钥。中控镜像 `ghcr.io/sakuralaaa/atoll-server` 也按 digest 签名：

```sh
cosign verify ghcr.io/sakuralaaa/atoll-server:v0.1.45 \
  --certificate-identity-regexp '^https://github.com/Sakuralaaa/Atoll/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## 审计日志

`exec` 的审计记录只保存程序名、参数个数和整条命令的 SHA-256，不保存命令原文（命令里常带口令）。v0.1.45 之前记录的原文由迁移 0031 清掉。
