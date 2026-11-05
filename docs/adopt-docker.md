# 接管自己用 Docker 启动的容器

很多服务器上已经有你自己用 `docker run` 或 `docker compose up -d` 起的容器。Atoll 可以**原地接管**它们：同一台机器、同样的宿主机端口、同样的绑定目录和数据卷（原地挂载，不复制数据）。你的旧容器、镜像和卷**永远保留**，随时可以回滚。

> 状态：v0.1.25 新功能，单元测试和集成测试（模拟 agent）已覆盖，**还没有在真实的 Docker 主机上完整验证过**。第一次使用请挑一个不重要的项目，先演练，再切换。

流程：**扫描 → 导入 → 演练 → 切换 →（需要时）回滚**。

---

## 准备

1. 服务器上已安装 atoll-agent（面板 → 服务器 → 添加服务器）。agent 版本需 ≥ v0.1.25，旧 agent 会返回 `agent_outdated`，先在面板里升级 agent。
2. CLI 已登录（`atoll login`），或者直接用面板：「迁移」页顶部的「接管自己用 docker run / compose 启动的容器」，或服务器详情 → 设置 →「接管 Docker 容器」。

---

## 1. 扫描

```sh
atoll docker scan my-server
```

agent 只读地列出这台机器上**不归 Atoll 管**的容器（带 `atoll.*` 标签或 `atoll-` 前缀的会跳过，compose 的一次性 `run` 容器也跳过），按 compose 项目分组（标签 `com.docker.compose.project`），单独启动的容器各成一组。分组键形如 `compose:blog`、`container:redis`。

每个容器会提取：镜像（尽量固定到仓库 digest）、相对镜像新增或改动的环境变量、entrypoint/cmd、用户、端口（含绑定的 IP，比如 `127.0.0.1:9000`）、绑定目录和命名卷、重启策略、内存/CPU 限制、当前内存占用。环境变量的值在 API 里会打码。

### 风险等级

| 等级 | 含义 |
|---|---|
| `auto` | 可以自动接管 |
| `manual` | **不会自动接管**，面板和 CLI 会写明原因，需要你手动处理 |

以下情况一律标为 `manual`：

- `network_mode: host`、`none`，或共享别的容器的网络（`container:…`）
- `privileged: true`、`cap_add`、`devices`、`--gpus`
- 挂载 Docker / containerd 的 socket（`/var/run/docker.sock`），比如 watchtower、traefik、portainer
- 关闭安全配置（`seccomp=unconfined`、`apparmor=unconfined`、`label=disable`）
- 共享宿主机的 PID / IPC / UTS / user 命名空间，或使用非默认 runtime
- 发布了 80/443（这两个端口归 Atoll 的 Caddy，请改用 Atoll 域名）或保留端口（22、7301、41641、41000–41999）
- 绑定 `/etc`、`/proc`、`/sys`、`/dev`、`/root`、`/run`、`/usr` 等系统目录，或 Atoll 自己的数据目录（只读的 `/etc/localtime`、`/etc/timezone` 允许）
- 使用非 `local` 驱动的卷（NFS 等）、`--volumes-from`
- 设置了自定义工作目录（`working_dir` / `-w`，Atoll 暂不支持）
- 清空了镜像的 ENTRYPOINT，或镜像已经不在本机

以下情况可以接管，但会给出警告（不会照搬）：`tmpfs`、`sysctls`、`extra_hosts`、自定义 DNS、`shm_size`、`ulimits`、`--init`、只读根文件系统、非 json-file 日志驱动、traefik/caddy/watchtower 标签、随机端口、没有仓库 digest 的本地镜像（只能留在本机，以后不能迁到别的服务器）。

分组层面的警告：环境变量里写了**容器名**（如 `blog-db-1`）而不是 compose 服务名（`db`）——接管后在 Atoll 项目网络里只能用服务名访问；和组外容器共用了网络——接管后组外的容器无法再按名字访问这一组。

## 2. 导入

```sh
atoll docker import my-server compose:blog          # 或简写 blog / 单独容器名
atoll docker import my-server compose:blog --containers blog-web-1,blog-db-1 --name blog
```

为这一组创建一个 Atoll 项目，每个容器一个服务（服务名 = compose 服务名，或容器名），**什么都不启动，你的容器照常运行**。默认接管组里所有 `auto` 容器；`manual` 的容器不能选，会继续按原样运行。

- 端口：原来发布的宿主机端口原样记录（`8080→80/tcp`、`127.0.0.1:9000→9000/tcp`），只暴露不发布的端口只在项目内网可见。
- 数据：绑定目录和命名卷记为服务卷的 `host_path`（命名卷取它在宿主机上的挂载点，如 `/var/lib/docker/volumes/blog_data/_data`），切换后原地挂载，`:ro` 保持只读。
- 环境变量里出现的 `${…}` 会转义为 `$${…}`，不会被 Atoll 当成变量引用展开。
- 没有内存限制的容器会套用「设置」里的默认内存上限（导入时会提示），需要更多内存的服务请在切换前调高。

## 3. 演练

```sh
atoll docker rehearse IMPORT_ID [--ignore-memory]
```

Atoll 在你的容器**旁边**启动一份副本，用来确认镜像、命令、环境变量和用户都能跑起来：

- **不发布任何宿主机端口**（不会和旧容器抢端口）；
- **可写的目录和卷换成临时的空目录**（`/var/lib/atoll/volumes/<服务>/<卷>`），绝不和正在运行的旧容器同时写同一份数据；只读挂载和单个文件（配置文件）原地挂载；
- 有仓库 digest 的镜像固定到那个 digest（仓库拉不到时退回本地镜像，打标签 `atoll-adopt/<服务>:<镜像ID>`，之后永不从仓库拉取）；
- 等全部副本健康后，再把它们停掉释放内存。

演练前会检查内存：旧容器当前占用的内存（拿不到时每个按 128 MB 估算）超过服务器可用内存时返回 409 `insufficient_memory`，`--ignore-memory` 跳过。

## 4. 切换

```sh
atoll docker cutover IMPORT_ID
```

1. 重新扫描一次，记录每个旧容器的重启策略和是否在运行；
2. 把旧容器的重启策略改为 `no`（防止重启服务器后旧容器和 Atoll 的同时起来抢端口），然后 `docker stop`——**不删除**；
3. Atoll 服务改成正式形态：发布同样的宿主机端口（含绑定 IP），绑定目录和卷原地挂载；部署并等待健康。

任何一步失败都会**自动回滚**：Atoll 服务停掉并改回不发布端口，旧容器按原来的重启策略重新启动。切换会短暂中断服务（旧容器停止到新容器启动之间）。

需要 `migrate` 权限；`require_approval` 的令牌会进入审批（面板「审批」里批准后执行）。

## 5. 回滚

```sh
atoll docker rollback IMPORT_ID
```

停止 Atoll 服务（并改回不发布端口、临时数据目录的形态），恢复旧容器的重启策略并启动原来在运行的那些。切换后写进数据目录的数据本来就在原地，回滚后旧容器直接看到。

之后可以再次 `rehearse` / `cutover`。

---

## 切换之后

- **不要再对这个 compose 项目运行 `docker compose up`**：它会把旧容器启动起来，和 Atoll 的服务抢端口。确认一切稳定后，旧容器、镜像、卷由你自己用 `docker rm` / `docker volume rm` 删除，Atoll 从不删除它们（也没有 cleanup 步骤）。删除命名卷前请确认 Atoll 服务不再使用它的挂载点。
- 原地挂载的数据（`host_path`）**不在 Atoll 的备份、恢复和迁移范围内**：备份这类服务会失败并说明原因，迁移项目/服务到别的服务器会被拒绝（409 `host_path`）。请自己备份这些目录。
- API 不允许新增或修改 `host_path`（只有接管能设置）；编辑服务时省略它会保留原值。
- **已经接管了容器的服务器不要把 agent 降级到 v0.1.25 以前的版本**：旧 agent 不认识 `host_path` 和 `host_ip`，会把服务改挂到空的 Atoll 卷目录上（数据还在原地，但服务看不到），并把只绑定在 127.0.0.1 的端口发布到所有网卡。

## API

见 [api.md](api.md)「Docker adoption」。MCP 工具：`docker_scan`、`docker_adopt`、`docker_rehearse`、`docker_cutover`、`docker_rollback`，进度用 `zeabur_import_status` 查看（同一个导入任务接口）。
