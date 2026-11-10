# 域名与 HTTPS

用户流量由每台节点自己的 Caddy（容器 `atoll-caddy`，占用 80/443）直接转发到服务容器，不经过中控。

## 绑定域名

在服务页选择「绑定域名」，填写 HTTP 端口和主机名。配置了根域名时只需要填前缀（如 `api` → `api.example.com`）。

- **有 Cloudflare 令牌**：Atoll 自动在对应 Zone 创建 / 删除 DNS 记录，等记录在公网解析生效后再下发路由，HTTPS 可用后标记为 active。可以一键切换橙云（代理）/ 灰云。
- **没有 Cloudflare**：自己把 A 记录指向节点的公网 IP，域名在解析到这台机器之前保持 pending。

## 根域名

「设置 → 根域名」可以添加多个根域名（如 `example.com`、`example.net`、`apps.example.org`），其中一个为默认。项目可以单独选默认根域名，绑定时也可以逐个选择。

- **Zone 匹配**：用已保存的 Cloudflare 令牌按后缀匹配，最长的优先。
- **证书模式**：
  - 跟随全局：使用全局的证书设置（Origin CA 证书或 ACME）。
  - 自动：用 API 令牌为 `name` 和 `*.name` 签发 Cloudflare Origin CA 证书（需要 **SSL and Certificates: Edit** 权限），签不了时退回 ACME。
  - ACME：Let's Encrypt 自动签发。
- **Cloudflare SSL/TLS 模式**：代理的根域名请设为 **Full (strict)**。Flexible 会让 Cloudflare 用 HTTP 回源，Caddy 再跳转 HTTPS，浏览器陷入重定向循环。
- **灰云**：Origin CA 根域名下不走代理的域名会改用 ACME 证书，因为浏览器直连时不信任 Origin CA 证书。

## Cloudflare 令牌权限

- **Zone.DNS: Edit**：自动管理 DNS 记录（必需）。
- **SSL and Certificates: Edit**：根域名自动签 Origin CA 证书（可选）。

节点的 80/443 默认只放行 Cloudflare IP；有路由在 ACME 根域名下时会放开，以便证书机构访问。

## 子账号

管理员可以给子账号分配允许使用的根域名，子账号只能在这些根域名下绑定子域名（不能用根域名本身和通配符）；不在任何根域名下的主机名视为子账号自己的域名。详见[子账号](subaccounts.md)。

更多细节见 [API 文档 → Root domains](api.md#root-domains) 与[故障排查 → 域名没有指向服务](troubleshooting.md#域名没有指向服务)。
