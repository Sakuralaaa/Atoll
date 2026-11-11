# 安全自审（2026-11）

范围：中控（`internal/server`）、agent（`internal/agent`）、安装脚本（`install/`）、Web 面板依赖。基线提交 `fbc1834`，修复在分支 `security-compat`。本文记录检查了什么、结论、已修复的问题和仍然存在的风险；不是第三方审计。

严重度：**高** = 低权限者可越权读写他人数据或拿到主机/云凭据；**中** = 需要一定前提（泄露的节点 token、同项目成员）才能利用；**低** = 加固或纵深防御。

## 1. 凭据与静态加密（已确认，无需改动）

- 凭据以 AES-256-GCM 静态加密（`internal/server/secrets/secrets.go`，主密钥 `<data>/master.key`，0600，密文带 `v1:` 前缀）。覆盖：settings 敏感键、git token / webhook secret、secret 环境变量、部署 spec、导入任务、根域证书、TOTP、`registry_cred`、通知渠道。
- session、API token、join token、node token、blob token、恢复码只存哈希；mesh token 由 HMAC 派生。
- 每个敏感键都与其功能在同一次提交里加入加密列表，库里没有明文残留，不需要数据迁移。
- API 只返回 `has_*`；通知渠道经 `maskChannel`；`redactURLError` 去掉错误里的 URL。
- 中控备份 bundle 含 `master.key`，整体用 scrypt + AES-GCM 加密。
- **加固（已修，低）**：启动时若已存在的 `master.key` 权限宽于 0600（例如从备份拷回、umask 宽松），收紧为 0600 并打 warning；收紧失败只告警不阻止启动。`0ba04f6`，测试 `TestLoadTightensLooseKeyPerm`。

## 2. 权限模型（已确认）

- `routes.go` 的 access 声明 + `perms.go checkToken` + `members.go checkMember`，默认拒绝；未列入 `memberAllow` 的全局路由对成员一律 `admin_only`（包括 confirmations 的批准/拒绝）。
- 部署详情 / 日志校验 ServiceID；备份恢复的 `target_server_id` 走 `memberMayUse`；服务器级路由拒绝项目 token 和成员。
- Cookie `HttpOnly` + `SameSite=Strict`，非 GET 校验 Origin；CSP / X-Frame-Options / HSTS。

### 2.1 放置与迁移（检查通过）

创建项目、从模板创建、创建服务（`server_id` 与项目主服务器不同时）、项目迁移、服务迁移都先过 `memberMayUse` 再查节点；`PATCH` 不允许直接改 `server_id`（必须走 migrate）。需要确认的迁移在批准时由管理员执行，成员无法批准。Docker / Zeabur 接管路由只对管理员开放。结论：无问题。

### 2.2 环境变量引用（已修，中）

- `envresolve` 的输入只包含同一项目的服务，**不能跨项目引用**。
- 问题 A：`GET /services/{id}/env/references` 会把“非 secret 但展开后包含 secret”的导出变量（例如 `DATABASE_URL=postgres://u:${PASSWORD}@…`，`PASSWORD` 是 secret）的**明文值**返回给任何有 `read` 权限的调用者，而 `GET /env` 对同一 secret 是打码的。修复：resolver 跟踪展开过程中是否读到 secret 变量，被“污染”的变量在 references 里标 `secret: true` 且不返回值。部署时注入的实际环境不变。
- 问题 B：成员只能看到自己被分配服务器上的服务（`memberSeesService`），但 references 会列出同项目其他服务器上服务导出的变量名和值。修复：对成员过滤掉不可见服务的条目。
- `d5ec392`，测试 `TestExpandAndReferences`（扩展）、`TestMemberEnvReferencesHideUnassignedServices`。

### 2.3 exec、文件、卷路径（检查通过）

- `exec` 在容器内执行，路由要求 `exec` 权限并校验服务归属；审计记录完整命令（见遗留风险）。
- 卷名在中控校验为小写 DNS label，agent 侧 `validVolumeName` 拒绝 `.`、`..`、`/`、`\`、NUL；ID 经 `validID`；`prepareHost` 先 `BuildContainerSpec`（含校验）再建目录和 chown。
- `host_path` 只能由 Docker 接管设置，API 不能新增或修改（`checkHostPaths`）；agent 侧 `CheckHostPath` 要求干净绝对路径，拒绝 `/`、`/etc`、`/proc`、`/sys`、`/dev`、`/root`、`/usr` 等、容器运行时 socket、Atoll 数据目录和 Docker 数据目录（时区文件可只读挂载）。
- 卷导入 `UntarGz` 基于 `os.Root`，拒绝绝对路径、`..` 和越界硬链接，符号链接不会被跟随出目标目录；中控 bundle 解包只接受白名单文件名。
- 配置文件路径 `configHostPath` 先 `path.Clean("/"+p)` 再拼到服务的 configs 目录下。
- 结论：未发现 `../` 穿越。

## 3. SSRF（已修，中）

新增共享包 `internal/netguard`（`7b16a5c`）：

| 级别 | API | 拒绝 | 用于 |
| --- | --- | --- | --- |
| 仅公网 | `PublicOnlyDialer()`、`CheckPublicIP` | 下一级全部 + 私网（RFC 1918、`fc00::/7`）、`100.64.0.0/10`、NAT64 | 子账号的 webhook（原 `publicOnlyHTTP` 改为复用它，不跟随重定向） |
| 标准 | `CheckHost(host)`、`CheckIP`、`Dialer()` | 回环、链路本地、unspecified、组播、`0.0.0.0/8`、广播、IPv4-mapped IPv6 字面量、`169.254.169.254`、`100.100.100.200`、`fd00:ec2::254`、`localhost` / `*.localhost` / `metadata.google.internal` | 管理员通知 webhook / Telegram（新的 `NotifyHTTP` 默认值）；后续通用 git 源也应使用 |
| 仅元数据 | `CheckMetadataHost`、`NoMetadataDialer()` | 只拒绝云元数据地址和名字 | 管理员配置的 S3 端点（备份、中控备份）和镜像仓库查询（镜像更新检查） |

- 拨号器在 `Control` 回调里检查**实际要连的 IP**，因此 DNS 解析结果、DNS rebinding 和每一次重定向都会被检查；客户端不使用环境代理（否则检查的是代理）。
- 为什么 S3 / 镜像仓库只禁元数据：管理员合法地会用内网或本机的 MinIO（`10.x`、`127.0.0.1:9000`）和私有 registry，禁私网会直接破坏这些部署；而云元数据服务永远不是合法的 S3 或 registry 端点，却能交出主机的云凭据，所以无条件拒绝。镜像仓库的 token realm 来自远端 `WWW-Authenticate`，同样经过该拨号器。
- `backup_endpoint` 在保存时就拒绝元数据地址（400），内网端点照常允许。
- git 仓库：本分支仍只接受 `https://github.com/OWNER/REPO`（中控 `normalizeRepo` 和 agent `validateBuild` 双重校验），无 SSRF 面；`git-sources` 分支扩展到任意 git 主机时应复用 `netguard.CheckHost` 与 `netguard.Dialer()`。
- **行为变化**：管理员 webhook 不再能发往回环或链路本地地址（例如同机 `127.0.0.1` 上的 ntfy）；私网地址（Docker 网桥、局域网）不受影响。发版说明需写明。
- 测试：`internal/netguard` 全部用例、`TestOutboundClientsGuarded`、`TestBackupEndpointRefusesMetadata`。

## 4. 节点 token 泄露的影响面（已修一部分，中）

节点 token 只用于 agent 的 WebSocket（`/agent/v1/connect`），拿到它等于冒充这台 agent：

- 能拿到的：发往该节点的服务 spec（含解密后的环境变量和 secret）、该节点服务的镜像仓库凭据、TLS 证书、备份仓库配置（S3 密钥和 restic 密码，**所有节点共用一份**，见遗留风险）。
- 修复前还能影响**其他服务器**：上报任意部署的状态（`handleDeployEvent` 不校验部署所在节点）、为任意服务写入/覆盖备份记录并把它的快照标为已清理（`handleBackupEvent`）、覆盖其他节点的备份校验记录、上报不属于本节点的容器状态改写服务状态。
- 修复（`cf7a8c4`）：部署事件要求 `deployment.server_id == 连接节点`；容器状态只作用于当前放置在该节点的服务（顺带修正了迁移后旧节点残留容器把服务标成失败的问题）；备份事件只能新建本节点服务的记录、只能更新本节点自己的记录；校验事件不能覆盖其他节点的记录。测试 `TestAgentEventsScopedToTheirServer`。
- 补救：在面板删除该服务器即吊销 token，重新 join；如果怀疑泄露，同时轮换备份仓库凭据和该节点服务的 secret。

## 5. 登录限流（检查通过，有遗留）

`lockout.go`：按客户端 IP（IPv6 按 /64）和按账户名分别计数，IP 5 次免费后 30 s 起指数退避、上限 1 h；账户 10 次后上限 15 min；24 h 无失败遗忘；成功登录清零。客户端 IP 只信任配置的反向代理的 `X-Forwarded-For`，Cloudflare 边缘才信 `CF-Connecting-IP`。密码和第二因素都计数。API token 为高熵随机值，不需要限流。

## 6. 安装脚本（检查通过，已加固）

- `install-server.sh` / `install-agent.sh` 从 GitHub Release 下载时都对照同一 Release 的 `SHA256SUMS` 校验；面板生成的 agent 安装命令带 `--sha256-<arch>` 固定校验和，未固定时明确警告“not verified”。
- 默认 `latest` 走 `releases/latest/download`，GitHub 定义上不包含 pre-release 和 draft。
- **加固（已修，低）**：`install-server.sh --version` 指定的版本若比已安装的 `atoll-server` 旧，默认拒绝（迁移只前进），需要显式 `--allow-downgrade`。`052994e`，测试在 `scripts/test-installers.sh`。

## 7. 依赖扫描

- `govulncheck ./...`（2026-11-10）：2 个被标记为可达的漏洞，都在 `github.com/docker/docker@v28.5.1+incompatible`：GO-2026-4887（AuthZ 插件超大请求体绕过）、GO-2026-4883（插件权限校验 off-by-one）。两者是 **dockerd 守护进程侧**的问题，Atoll 只用该模块的客户端库，被标记是因为导入了同一模块的包；该模块无修复版本（`Fixed in: N/A`，上游代码已迁至 `github.com/moby/moby/v2`）。不影响 Atoll，主机上请保持 Docker Engine 为最新补丁版本。另有 5 个漏洞位于依赖模块但代码未调用。
- `cd web && npm audit --omit=dev`：0 个漏洞（含 dev 依赖的完整 `npm audit` 也是 0）。

## 8. 兼容与升级相关的安全修复

一键更新中控时，若没有指定版本且最新 Release 是 pre-release，返回 `409 prerelease`；目标版本比当前旧时返回 `409 downgrade_unsupported`（修复前，运行 beta 的中控点“更新”会被降级到更旧的正式版，而数据库迁移已前进）。`052994e`，测试 `TestCheckUpdateTarget`。规则见 [compatibility.md](compatibility.md)。

## 遗留风险

| 风险 | 严重度 | 说明 / 建议 |
| --- | --- | --- |
| ~~`service.exec` 审计记录完整命令~~ | 已修复 v0.1.45 | 只记录程序名、参数个数、是否 `sh -c` 和 SHA-256；迁移 0031 清掉旧记录的原文。 |
| 备份仓库凭据所有节点共用 | 中 | 任一节点 token 或节点主机被攻破，就能读取所有服务的备份。按节点分仓库 / 分前缀凭据需要较大改动。 |
| 账户维度锁定可被他人触发 | 低 | 攻击者持续用错误密码可让某账户最多 15 分钟无法登录（IP 维度不受影响）。属于防暴力破解的常见取舍。 |
| 管理员 SMTP 主机未做地址限制 | 低 | 同机 SMTP 中继（`localhost:25`）是常见合法用法，故未限制；SMTP 渠道只有管理员可配，子账号不可用 email 渠道。 |
| ~~发布产物只有校验和，无独立签名校验~~ | 已缓解 v0.1.45 | `SHA256SUMS` 附 Sigstore 无密钥签名（`SHA256SUMS.sigstore.json`），中控镜像按 digest 签名，验证方法见 security.md。安装脚本仍只校验 `SHA256SUMS`（不强制依赖 cosign）。 |
| 节点 token 泄露仍可读取该节点服务的 secret | 中 | 这是 agent 运行所必需的；处理方式见第 4 节。 |
