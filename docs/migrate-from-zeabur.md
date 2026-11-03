# 从 Zeabur 迁移到 Atoll

> Zeabur 将于 **2026-12-31** 停止服务。请留出至少一到两周：先演练（rehearse），稳定运行一段时间后再清理。

本文说明如何把 Zeabur 上的服务迁到 Atoll。主路径是**原地接管**：你的服务本来就跑在你自己的 VPS 上（在 Zeabur 里登记为“自有服务器”，单节点 k3s），Atoll 直接在**同一台机器**上接管，不需要跨服务器搬数据，自定义域名的 A 记录也不用改（IP 不变）。

如果你的服务跑在 Zeabur 的共享集群、或是通过 Zeabur 租用的服务器上，或者你想换一台服务器，请看文末的“次要路径：`atoll import zeabur`”。

---

## 准备

1. 安装 CLI 并登录（在面板 设置 → API tokens 创建令牌）：

   ```sh
   atoll login --url https://atoll.example.com --token atl_pat_...
   ```

   配置保存在 `~/.config/atoll/config.json`，权限 0600。

2. 在面板 设置 里填好**基础域名**（如 `example.com`），用于替换 `*.zeabur.app` 域名；也可以不填，导入时用 `--domain old=new` 逐个指定。Cloudflare 令牌是可选的：没有令牌时由你自己添加 A 记录（切换照常进行，域名在记录生效前显示为 pending）。

3. 在**那台 Zeabur 管理的 VPS 上**安装 atoll-agent（面板 → Servers → Add server，复制 join 命令到 VPS 执行）。agent 会自动发现本机的 k3s。

---

## 原地接管流程

### 1. 扫描

```sh
atoll zeabur scan my-vps
```

列出本机 Zeabur 的项目、环境（namespace，形如 `environment-xxxx`）、每个服务的角色（app / database / cache）、镜像（是否由 Zeabur 构建）、数据量、域名和警告，以及剩余磁盘和内存。环境变量值已打码。

演练期间两套系统会同时运行：**剩余磁盘需不少于数据量的 1.2 倍**，否则先清理磁盘。

### 2. 导入（只创建，不启动）

```sh
atoll zeabur import my-vps --namespace environment-xxxx \
  --domain shop.zeabur.app=shop.example.com \
  [--services web,db] [--name shop] [--base-domain example.com]
```

- 在 Atoll 中创建一个项目，服务 1:1 映射：环境变量（展开后的值）、端口、卷、配置文件、自定义域名。
- `xxx.zeabur.internal` 改写为 Atoll 内部服务名；Zeabur 内置变量被翻译，`ZEABUR_*` ID 被丢弃。
- `*.zeabur.app` 域名默认映射为 `<同前缀>.<基础域名>`（基础域名取自面板设置，可用 `--base-domain` 覆盖）；`--domain old=new` 逐个指定，`--domain old=` 表示丢弃。
- 此时**什么都不启动**，Zeabur 照常服务。

### 3. 演练（rehearse）

```sh
atoll zeabur rehearse imp_xxxx [--ignore-memory]
```

- Zeabur 构建的镜像从 containerd 导出并 `docker load`（标记为 `atoll-import/<服务>:<digest12>`）；来自 Docker Hub / GHCR 等仓库的镜像按原始引用（保留 tag）拉取，拉取失败时退回到 containerd 中的副本并打上同一个引用。
- 应用的卷目录在本机复制；数据库用**同主版本**的工具 dump/restore，并核对每张表的行数。
- Atoll 容器启动但**不接公网流量**。Zeabur 继续服务。
- 在面板里检查服务、日志和数据。可以反复演练。
- 演练期间 Zeabur 和 Atoll 两份副本同时占内存。开始前会估算所需内存（有 metrics-server 时用 Zeabur pod 的实际用量，否则按类型给默认值：postgres 256 MB、mysql 400、mongo 300、redis 64、其他 128），可用内存不够就拒绝（409 `insufficient_memory`）。**一次只演练一个项目**；确实要跳过检查用 `--ignore-memory`。

### 4. 切换（cutover）

> **注意：停放 ingress 是整台服务器级别的。** Zeabur 的 ingress-controller 是一个 DaemonSet，这台服务器上**所有** Zeabur 项目都通过它对外服务。切换时把它停放，所有 `*.zeabur.app` 和自定义域名会同时不再由 Zeabur 提供服务——不只是你正在切换的那个项目。

所以服务器上有多个 Zeabur 项目时，按这个顺序来：

1. 每个项目分别 `import` 和 `rehearse`（逐个演练，见上面的内存检查）。
2. 全部演练通过后，**一起切换**：

```sh
atoll zeabur cutover imp_aaaa imp_bbbb imp_cccc   # 同一台服务器的多个导入
```

（API：`POST /api/v1/servers/{id}/zeabur/cutover`，MCP：`zeabur_node_cutover`，面板：导入页出现「批量切换」。）只有一个项目时照旧 `atoll zeabur cutover imp_xxxx`。

需要你确认（`--yes` 跳过提问）。步骤：每个项目依次把 Zeabur 应用缩容到 0（数据库保持运行直到最后一次 dump）→ 最终数据同步 → 然后**只停放一次** ingress-controller → atoll-caddy 接管 80/443，带上所有项目的主机名 → 逐个项目配置 DNS。每个导入任务各自显示状态和步骤；任一项目失败，这次切换的所有项目都会自动回滚。会有短暂停机。

切换前会重新扫描服务器：如果还有**不在这次切换里**、仍在运行且有域名的 Zeabur 项目，切换会被拒绝（409 `other_zeabur_projects`，并列出这些项目和域名）。确定接受它们下线时才加 `--force`。Zeabur 的 operator 是整个集群共用的，只有在这台服务器上所有 Zeabur 项目都被覆盖时才会缩容到 0。

### 5. 验证

逐个打开你的域名，检查登录、写入、上传、定时任务、邮件等关键流程。`*.zeabur.app` 地址从此失效，替换域名需要一条指向这台 VPS IP 的 DNS 记录。

### 6. 出问题就回滚

```sh
atoll zeabur rollback imp_xxxx            # 这台服务器上只有它被切换过
atoll zeabur rollback --server my-vps     # 一起切换的多个项目一起回滚
```

停止 Atoll 副本，恢复 Zeabur 的 ingress-controller（以及被停掉的 operator），把 Zeabur 工作负载按记录的副本数恢复。Zeabur 上的对象从未被删除，所以回滚是完整的。

恢复 ingress 同样是整台服务器级别的（会停掉 atoll-caddy）：只要同一台服务器上还有别的导入处于已切换状态，单独回滚一个会被拒绝（409 `node_rollback_required`），请用 `--server` 整体回滚（API：`POST /api/v1/servers/{id}/zeabur/rollback`）。

### 7. 清理（不可逆，稳定之后再做）

```sh
atoll zeabur cleanup imp_xxxx [--no-backup]
```

运行 `k3s-uninstall.sh` 卸载 Zeabur 的 k3s，释放内存和磁盘。默认先把 PVC 目录打包到 `/var/lib/atoll/zeabur-backup/`；`--no-backup` 则不保留。**必须手动输入服务器名称确认**（区分大小写），执行后**无法再回滚**。

用 `atoll zeabur status imp_xxxx` 随时查看导入任务及各步骤状态；所有命令都支持 `--json`。

---

## Zeabur 以前替你做的事，现在由谁负责

| 以前 Zeabur 做的 | 现在 |
|---|---|
| 镜像仓库（Zeabur 构建并托管镜像） | 迁移时镜像直接从本机 containerd 导出；之后由你自己的仓库（推荐 GHCR）负责 |
| `*.zeabur.app` 域名 | 失效；换成你自己的域名（导入时映射），DNS 由你管理 |
| TLS 证书 | atoll-caddy：Cloudflare Origin 证书或 ACME，在面板 设置 中配置 |
| 内部 DNS（`*.zeabur.internal`） | 每个项目一个 Docker 网络，服务名即主机名（导入时已改写） |
| 数据库备份 | **由你负责**。Atoll 不会替你自动做异地备份，请自行安排定期 dump 并存放到另一台机器或对象存储 |

---

## 本指南遵守的规则

- **不自动删除 Zeabur 的任何对象**。切换只是缩容和停放 ingress；只有你亲自确认的 `cleanup` 才会卸载 k3s。
- **DNS 由你修改**，除非你明确批准由面板（Cloudflare）代为修改；通过 API 令牌添加域名需要在面板中批准。
- **数据库使用同主版本的工具**导出导入（例如 Postgres 16 → Postgres 16），不在迁移中顺带升级。
- **在稳定之前保留回滚能力**：不要急着 cleanup，先稳定运行一段时间。
- 机密（环境变量值）只保存在加密存储或权限 0600 的文件中；atoll 从不索要 Zeabur 的令牌或密码。

---

## 之后怎么部署

Zeabur 构建镜像的能力不会迁移过来。推荐流程：

```
GitHub Actions 构建 → 推送到 GHCR（ghcr.io/你/应用:sha） → atoll deploy
```

```sh
atoll deploy ghcr.io/you/app:sha-abc123 -p shop --name web
```

同名服务已存在时只更新镜像并重新部署；在 CI 中使用 `deploy` 范围的 API 令牌（`ATOLL_URL` / `ATOLL_TOKEN` 环境变量）。AI 助手可通过 `atoll mcp`（stdio）调用同样的操作，危险操作仍需在面板中批准。

---

## 次要路径：`atoll import zeabur`

适用于：服务跑在 Zeabur 共享集群或 Zeabur 租用的服务器上，或者想迁到另一台服务器。在你自己的电脑上运行，前提是 Zeabur CLI 已登录（`npx zeabur@latest auth login`，由你自己执行）。

```sh
atoll import zeabur inventory                     # 读取项目、服务、变量（机密写入 0600 文件）
atoll import zeabur plan --server my-new-vps      # 生成 plan.json，不改变任何东西
atoll import zeabur images --push-to ghcr.io/you  # 仅限专用服务器：导出 Zeabur 构建的镜像（共享集群请从源码重建）
atoll import zeabur data                          # 导出数据库 / 卷到本地
atoll import zeabur apply                         # 创建 Atoll 项目、上传数据、核对行数
atoll import zeabur cutover                       # 暂停 Zeabur 应用服务、添加域名，并打印回滚步骤
```

这条路径需要修改 DNS 指向新服务器，cutover 会先记录当前 DNS（`dns-before.json`）供回滚使用。
