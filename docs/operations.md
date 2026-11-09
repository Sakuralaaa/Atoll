# 运维参考

## 管理多台机器

机器多了以后，下面几项最常用：

- **加机器**：每台都用面板生成的新命令（token 一次性）。命令里的 `--sha256-*` 让脚本在运行二进制之前先校验，中控用 http 地址时也不会被中间人替换。
- **机器离线**：agent 断线后自动重连，容器由 Docker 管，agent 重启不影响运行中的服务。面板「服务器」页能看到在线状态和资源曲线。
- **中控的负载**：十几台机器的指标和事件都写进中控的 SQLite。写入走单连接，面板的列表、指标和审计查询走独立的只读连接池，不会被写入堵住。
- **构建隔离**：每台机器同一时间只跑一个构建，构建有内存上限和低 CPU 权重，不会拖垮同机的服务（见下一节）。
- **备份**：在「设置 → 备份」配一个 S3 兼容存储桶，所有机器共用。原机器宕机时，可以把备份恢复到另一台。
- **跨机器访问**：开启 Tailscale 后，服务可以用名字访问别的机器上的服务，端口段 `41000–41999`（`ATOLL_MESH_PORTS`）。

### 私有网络（Tailscale，可选）

面板仍走公网 HTTPS，只有中控和节点需要装 Tailscale，你自己的电脑不需要。电脑上同时开着 Tailscale 和 Clash TUN 的，在规则里加 `IP-CIDR,100.64.0.0/10,DIRECT`。

1. Tailscale 后台：ACL 的 `tagOwners` 加 `"tag:atoll": ["autogroup:admin"]`，放行 `tag:atoll` → `tag:atoll:7301,41000-41999`；生成 auth key，选**可复用、非临时、预授权、标签 `tag:atoll`**。
2. 中控：`install-server.sh --domain panel.example.com --tailscale-authkey tskey-auth-xxx [--agent-tailnet-only]`，再到面板「设置 → 私有网络」选 Tailscale，填同一个 key。中控的 tailnet 地址要用 **100.x IP**（节点用 `--accept-dns=false`，解析不了 MagicDNS 名）。
3. 节点：照常运行添加命令。脚本会安装 Tailscale 并经 tailnet 加入，公网地址留作备用。tailnet 不通时会警告并走公网，之后可在「服务器 → 加入 tailnet」或用 `atoll-agent tailnet up` 迁移。

加了 `--agent-tailnet-only`（`ATOLL_AGENT_TAILNET_ONLY=1`）后，公网上的 `/agent/*` 一律返回 404（`/agent/v1/bootstrap` 除外）。自己配 Caddy 的话也要同样处理。详见 [docs/api.md](api.md#private-network-tailscale)。

## 从源码构建服务

```bash
atoll deploy --git https://github.com/OWNER/REPO [--branch main] [--context DIR] [--dockerfile F] \
  [--build-arg K=V] [--git-token-env GH_TOKEN] -p PROJECT
atoll logs SERVICE --build -f
```

构建在服务所在的机器上进行，不在中控上。新服务只在创建时打印一次 GitHub webhook 的 URL 和 secret（`atoll services webhook SERVICE` 可再查看 URL）。同一个 push 重复投递（GitHub 重试或重放）只会构建一次。

**资源限制**：构建默认在一个独立的 BuildKit 容器里运行（buildx `docker-container` driver），内存上限为本机内存的 75%，CPU 权重是普通容器的 1/4，空闲时能用满，忙时让给业务。磁盘剩余不足 3 GiB 时拒绝开始构建，每次构建后把构建缓存修剪到 10 GB。机器上没有 buildx，或拉不到 `moby/buildkit` 镜像时，会自动退回普通 `docker build`，并在构建日志里说明。

要调整这些值，在节点上创建 `/etc/atoll/agent.env`，然后执行 `systemctl restart atoll-agent`：

| 变量 | 默认 | 含义 |
| --- | --- | --- |
| `ATOLL_BUILD_LIMITS` | 开 | 设为 `off` 时用普通 `docker build`，不做限制 |
| `ATOLL_BUILD_MEMORY` | 内存的 75%（至少 1 GiB） | 构建内存上限，如 `2g`、`1536m` |
| `ATOLL_BUILD_CPUS` | 不设硬上限 | CPU 硬上限，如 `1.5` |
| `ATOLL_BUILD_MIN_FREE_GB` | `3` | 低于这个剩余空间时拒绝构建，`0` 表示不检查 |
| `ATOLL_BUILD_CACHE_GB` | `10` | 每次构建后保留的构建缓存，`0` 表示不修剪 |
| `ATOLL_BUILD_CACHE_DAILY_GB` | `5` | 每日清理时保留的构建缓存，`0` 表示每日不修剪 |
| `ATOLL_BUILDER_IDLE_STOP` | `10` | 构建容器空闲多少分钟后停止（缓存保留，下次构建自动启动），也可写 `90s`、`1h`；`0` 表示不停止 |
| `ATOLL_DOCKER_ROOT` | `/var/lib/docker` | 磁盘检查看的路径 |

**清理**：删除服务（或项目、迁走）时，这台机器上该服务的构建镜像 `atoll-build/<服务ID>:*` 和残留的构建目录一并删除。构建容器空闲 10 分钟后自动停止，不占内存；每天清理一次：限制参数改过后留下的旧 `atoll-builder-*` 构建器会被删掉，机器上已没有源码构建的服务时连当前构建器和它的缓存一起删除（下次构建会重新创建），否则把缓存修剪到 5 GB。

1–2 GB 内存的小机器建议设 `ATOLL_BUILD_MEMORY=768m`，或者把构建放到 CI 里，改用镜像部署。

## 日常运维

| 要做的事 | 怎么做 |
| --- | --- |
| 升级中控 | 面板「中控 → 更新」，或重跑 `install-server.sh --version vX.Y.Z` |
| 升级 agent | 面板「服务器」页顶部「全部升级」，或单台服务器「设置 → Agent」；CLI `atoll servers upgrade-agent --all`。校验 SHA-256 后替换，新 agent 两分钟内没连回就自动回滚，容器不重建。v0.1.6 之前的 agent 需要在节点上重跑一次添加命令（已加入的节点会跳过加入步骤） |
| 中控备份和迁移 | 见 [docs/control.md](control.md) |
| 看日志 | `journalctl -u atoll-server -f`、`journalctl -u atoll-agent -f`，或在面板「中控」页查看 |
| 清理磁盘 | 构建镜像每个服务保留最近 3 个；全局清理用 `docker system prune` |

## 校验下载的文件

每个 Release 带 `SHA256SUMS`，还有 GitHub 签发的构建来源证明（provenance），能证明二进制是由本仓库的 release 工作流构建的。

```bash
# 校验哈希
sha256sum -c SHA256SUMS --ignore-missing
# 校验来源（需要 gh CLI）
gh attestation verify atoll-server-linux-amd64 --repo Sakuralaaa/Atoll
```

安装脚本会自动对照 `SHA256SUMS` 校验从 GitHub 下载的二进制；从中控下载 agent 时，对照添加命令里的 `--sha256-*` 校验。中控自更新同样先校验 `SHA256SUMS`，失败自动回滚。

## Android 客户端

[`android/`](https://github.com/Sakuralaaa/Atoll/blob/main/android/) 是一个原生 Android 壳应用（Kotlin + Jetpack Compose，Android 8.0+）：原生登录（支持两步验证和恢复码，密码用 Android Keystore 加密保存）、全屏打开面板、会话过期自动重新登录、多个面板快速切换、文件上传下载。

APK 随每个版本附在 [GitHub Release](https://github.com/Sakuralaaa/Atoll/releases) 上（`atoll-android-<版本>.apk`，附 `.sha256`）；`main` 上的开发版在 Actions 的 [android 工作流](https://github.com/Sakuralaaa/Atoll/actions/workflows/android.yml) 里作为 artifact 下载。说明见 [android/README.md](https://github.com/Sakuralaaa/Atoll/blob/main/android/README.md)。
