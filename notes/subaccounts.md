# 子账号（member）与服务器分配：设计与交接

状态：进行中（2026-11-06 开始）。本文件是唯一的进度来源，接手的 agent 先读这里，做完一步就更新「进度」并推送。

## 目标
- 管理员（admin）可以创建子账号（member），并把若干台工作机分配给它。
- member 只能看到、操作分配给它的服务器，以及这些服务器上的项目和服务。中控页、其他服务器、面板设置、令牌管理、加入令牌、Agent 升级、Zeabur/Docker 接管它都看不到。
- 管理员可以随时调整 member 的服务器和权限，也可以停用 member，改动对下一个请求立即生效（权限每次请求都从数据库重新读取，不做缓存）。
- 升级无感：迁移只新增内容，已有账号成为 admin，没有 member 时行为与之前完全一致。

## 已定的默认（用户未指定时采用）
- 授权单位是服务器：member 能看到的项目 = `projects.server_id` 属于其已分配服务器的所有项目。若一个服务（`services.server_id`）在未分配的服务器上，即使它所属项目可见，访问也要被拒绝。管理员不应把自己的项目放在已分配出去的服务器上（UI 中给出提示）。
- 一台服务器可以分配给多个 member（表结构天然支持），它们共享这台服务器上的项目。
- member 的权限从 `api.Perm*` 中挑选，但 `servers` / `settings` / `tokens` 永远不能给 member。默认权限为 read + deploy。exec / delete / domains / restore / migrate 需要管理员手动勾选。
- 不强制 2FA，也暂不做配额（第二阶段再考虑）。
- 无法判断 member 能否访问时一律拒绝（fail closed）：不在白名单内的全局路由，对 member 返回 404/403。

## 数据模型（迁移 0024_members.sql）
- `users.role TEXT NOT NULL DEFAULT 'admin'`，取值 `admin` 或 `member`。
- `users.disabled INTEGER NOT NULL DEFAULT 0`：停用后登录、会话和令牌全部失效。
- `users.permissions TEXT NOT NULL DEFAULT ''`：member 的权限，逗号分隔。admin 忽略此字段。
- `users.must_change_password INTEGER NOT NULL DEFAULT 0`。
- `server_assignments(server_id TEXT, user_id INTEGER, created_at TEXT, PRIMARY KEY(server_id,user_id))`，两个外键都设 ON DELETE CASCADE。

## 授权（internal/server/auth.go、perms.go）
- `principal` 新增字段：`Member bool`、`Servers map[string]bool`。`Projects` 字段复用为 member 可见的项目集合（与按项目授权的令牌用同一套机制）。
- member 的会话：`Session=true, Member=true`，`Perms` 取自 users.permissions，`Projects` 和 `Servers` 每次请求都重新计算。
- member 的 API 令牌：令牌自身的权限与 member 的权限取交集，项目范围也取交集。
- `inProject()` 不能再因为 `Session` 为真就直接放行，要改成 `(p.Session && !p.Member) || ...`。凡是用 `p.Session` 表示「拥有全部权限」的地方都要逐个检查（`grep -n "\.Session" internal/server/*.go`，共约 26 处）。
- 在 `authed()` 中对 member 增加 `checkMember(r, p, need)`：
  - 路由的 `perm` 必须在 member 权限内（lvlSession 路由不检查 perm）。
  - 项目类资源（projects/services/domains/backups/restores/migrations 的 `{id}`）：资源所属项目必须在 `Projects` 中；服务和域名还要求对应服务所在的服务器在 `Servers` 中。
  - 其他路由按 `memberAllowed` 白名单处理（键为路由 pattern），未列出的全部拒绝。
- 白名单（初版）：
  - `/auth/me`、`/auth/password`、`/auth/username`、`/auth/2fa*`、`/auth/sessions*`、`/me/*`
  - `GET /servers`、`GET /servers/{id}`（handler 中按 `Servers` 过滤，未分配的服务器返回 404）
  - `GET /projects`、`GET /image-updates`、`GET /domains`、`GET /backup-verifications`（resList 类路由，handler 已经按 `p.Projects` 过滤，需要逐个核实）
  - `POST /projects`、`POST /app-templates/{id}`：handler 中校验 server_id 属于 `Servers`
  - `GET /templates`、`GET /app-templates`、`GET /root-domains`（只返回名称，不含令牌）
  - `GET /events`：必须先按项目和服务器过滤后才能放开（第一步先拒绝，UI 实时刷新会失效，见进度）
- 明确拒绝：`/settings*`、`/tokens*`（第二阶段再允许 member 管理自己的令牌）、`/servers/join-token`、`agent-upgrade`、`PATCH/DELETE /servers/{id}`、`/imports*`、`/backup-verify*`、`/confirmations*`、`/cloudflare/*`、`/servers/agent-install-command`、中控相关路由。

## 管理接口（仅 admin，会话专用）
- `GET /users`、`POST /users {username, password, permissions, servers}`
- `PATCH /users/{id} {permissions?, servers?, disabled?, password?}`：停用账号或重置密码时，同时删除该用户的会话和令牌。
- `DELETE /users/{id}`：不能删除自己，也不能删除最后一个 admin。
- 以上操作都写审计日志，actor 记为 `user:<名字>`。

## 部署边界（第一阶段一并完成）
- member 创建或修改服务时，禁止设置 privileged、宿主机路径挂载、host 网络和 cap_add（在 api.Service 中找到对应字段，在 handler 中拒绝）。
- 迁移（migrate）只能在 member 自己的服务器之间进行。

## 第二阶段
- mesh 隔离：member 的服务只能访问同一 member 范围内的服务（涉及 Agent，所有节点都需要升级）。
- 备份按用户隔离（restic 路径前缀），member 管理自己的令牌，配额，以及 events 的过滤。
- Web UI：admin 的用户管理页；member 侧栏隐藏中控、设置、加入服务器、接管等入口（`/auth/me` 返回 role 和 permissions，供前端判断）。

## 测试要求
每条 member 可达的路由都要有越权测试：member A 访问未分配服务器上的项目、服务、域名、备份时必须返回 403 或 404；白名单之外的全局路由必须拒绝；停用后会话立即失效。

## 进度
- [x] 1. 迁移 0024 + store（store/members.go，测试 store.TestMembersAndScope）
- [x] 2. principal/member 认证（auth.go applyMember）+ checkMember（server/members.go，fail closed）；登录拒绝已停用账号；/auth/me 返回 role/servers。server 层测试 server.TestMemberScope。（GET /servers、/servers/{id}、POST /projects、/events 已在第 4 步放开）
- [x] 3. 用户管理 API（handlers_users.go：GET/POST /users，PATCH/DELETE /users/{id}，只限 admin 会话；不能把中控分给 member；新建或重置密码后 must_change_password=1，本人改密后清零）+ 测试 server.TestUserManagementAPI。must_change_password 已强制（见第 6 步）
- [x] 4. 已完成：GET /servers 过滤、GET /servers/{id}、POST /projects、创建服务时的放置位置、项目和服务迁移的目标服务器都按 memberMayUse 校验（members.go），并有测试覆盖。/events 已放开：按 memberSeesEvent 过滤（无法归属的帧一律丢弃，confirmation 帧不发），每 30 秒重新鉴权一次，停用或改权限在 30 秒内生效。白名单里的 resList handler 都已核实走 inProject，`.Session` 的其余用法只出现在 member 本来就拿不到的路由里，或者限定在它自己的项目内（env 明文）。POST /app-templates/{id} 和 GET /root-domains（只返回名字）已在接力 A3、A4 放开。
- [x] 5. 部署边界：已核实 2026-11-06，api.Service 本身没有 privileged、host 网络和 cap_add 字段，新的宿主路径挂载 API 一律拒绝（checkHostPaths）。Docker 接管与导入（/imports*）只对 admin 开放，因此不需要额外代码。以后若为 Service 新增这类字段，必须对 member 拒绝。
- [~] 6. Web UI 进度：管理员子账号页 web/src/pages/Users.tsx（/settings/users，可新建、编辑服务器和权限、重置密码、停用、删除）已完成；member 侧栏只显示项目、服务器、域名；设置页中 member 只看到「账号与安全」。must_change_password：前端有顶部提示，后端也已强制（未改密前 member 会话只能访问 /auth/* 和 /me/*）；项目设置与服务设置里的「迁移」「删除」已按权限隐藏（useCan，web/src/api/queries.ts）；域名页的「绑定域名」也已按 domains 权限隐藏；日志页的「命令」（exec）也已按权限隐藏；服务状态页的「文件」「命令」入口和「绑定域名」按钮也已按权限隐藏；**还缺**：服务页其他位置的域名操作、备份恢复、部署类按钮也要按 useCan 隐藏（目前点击后服务端返回 403）；以上都尚未在真实浏览器中目测。
- [~] 7. docs/api.md 已写；docs/releases/v0.1.41.md 草稿和 CHANGELOG 行已写（标注草稿）。发版前需真实浏览器目测，并取得用户确认；发版时去掉「草稿」字样


## 接力：后续待办（2026-11-06 17:10 更新，v0.1.41 已发，A 段完成）

v0.1.41 包含第一阶段（第 1 至 7 步）。下一位 agent 从这里接手，每完成一小步就推送并更新本节。

### A. 第一阶段收尾（小）
1. [x] 目测（2026-11-06）：demo 加了子账号 alice（web/src/api/demo-users.ts；`?demo=1&member=1` 有 read+deploy，`member=ro` 只读，`member=0` 回到管理员），auth/me 带 role/permissions/servers/must_change_password，列表按她的服务器过滤。Playwright 截图看过：子账号页、member 侧栏、改密横幅、服务页各 tab、服务器页、域名页、项目设置。修了：横幅文案「账户」→「账号」；member 侧栏底部不再写「中控在线」；member 打开 /settings 直接跳到账号与安全（以前会请求 /settings）；域名页对 member 不再请求 /settings（以前骨架屏一直转）；服务器页对 member 隐藏中控卡片、添加服务器、Agent 升级；服务器详情对 member 隐藏 SSH 和设置 tab。demo.ts 拆出 demo-auth.ts、demo-exec.ts（<600 行）。
2. [x] 按 useCan 隐藏（2026-11-06）：deploy（服务页头部重新部署/重启/启停、状态页启停与套用默认值、部署记录回滚（状态页和日志页）、环境变量增删改、新增服务（项目侧栏、空项目、?add=1）、创建项目、镜像更新卡片的策略/检查/更新、源码卡片的编辑/重建/改回镜像/轮换 webhook、网络页加端口、硬盘页挂载/卸载卷、服务设置和项目设置只读、备份策略只读与立即备份、恢复演练）；restore（快照「还原」）；domains（网络页绑定/解绑、域名页解绑）；exec（更多菜单「执行命令」、硬盘页文件入口）；仓库校验只给 admin。注意：`POST /services/{id}/webhook/rotate` 是 lvlSession，服务端对 member 未按 deploy 拦截（UI 已隐藏），可在第二阶段收紧。
3. [x] `POST /app-templates/{id}` 已对 member 开放（2026-11-06）：先 memberMayUse 校验 server_id（不属于自己的返回 400，与 POST /projects 一致）；`!p.Session` 分支改为 `!p.Session || p.Member`，以前 member 会话会绕过 domains 权限直接绑定域名，现在带 hostname/domains 必须有 domains 权限。测试 server.TestMemberAppTemplates。
4. [x] `GET /root-domains` 对 member 只读开放（2026-11-06），只返回 `[{name, default}]`，不含 zone、证书状态和错误；写操作仍只限 admin。测试 server.TestMemberRootDomainsNamesOnly。
5. [x] 管理员页提示（2026-11-06）：子账号编辑框里每台服务器旁显示上面有几个项目；新勾选一台已有项目的服务器时，黄色提示会列出项目名，并说明分配后该子账号能按权限看到和操作这些项目，建议先迁走不想共享的项目。项目没有记录创建者，因此这里统计服务器上的全部项目，前端实现（web/src/pages/Users.tsx）。
6. [x] 偶发测试（2026-11-06）：
   - `internal/agent TestWebSocketSession`：stop 之后的状态帧是变化驱动的，可能先于 action 的 result 到达；`fs.result` 会丢弃它，后面再等 exited 就一直等到 10 秒超时（CI 日志 session_test.go:289）。现在在同一个循环里同时等 result 和 exited 状态。
   - `internal/server TestTwoFactorFlow`：CI 日志是 `enable wrong code: 200`，固定的错误码 "000000" 恰好命中了当前 ±1 步之一（每次约 1/333000）。改为 `wrongTOTP(secret, now)` 现算一个确定不被接受的码；auth_test、control_test 里同类的固定码一并替换。

A 段已全部完成（2026-11-06），说明写在 docs/releases/v0.1.42.md，标注为草稿。按用户要求暂不打 tag、不发版、不改 web 版本号，等第二阶段做完后一起发布。

### B. 第二阶段（大，需要先和用户确认方案）
1. mesh 隔离：member 的服务不应通过 tailnet 访问其他服务器上的服务。需要所有节点升级 Agent。
2. member 自管 API 令牌（权限不超过自己，作用域与本人相同），以及 CLI/MCP 支持。
3. 资源配额：CPU、内存、服务数。
4. 备份按用户隔离（存储前缀或独立仓库）。
5. `/problems` 对 member 过滤：服务器级告警只发给拥有该服务器的人。
6. 审计日志记录操作者（admin 或 member）。

### C. 与子账号无关、仍待用户决定的事项
- 线上中控升级到 v0.1.41：包含 0019 至 0024 迁移，升级前先备份；按二进制替换流程操作，见 project 记录或 docs/control.md。
- InstaBox 远程构建（v0.1.42 候选）：API key 只能由用户在面板里填写。
- Oracle 迁移切换：由另一个 agent 执行，不要假设已经切换。
