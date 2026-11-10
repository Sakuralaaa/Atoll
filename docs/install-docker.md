# 用 Docker 安装中控

除了 `install-server.sh`（systemd + 主机上的 Caddy），中控 `atoll-server` 也可以用官方镜像
`ghcr.io/sakuralaaa/atoll-server` 跑在 Docker 里。镜像同时支持 linux/amd64 和 linux/arm64。

> 只有**中控**可以容器化。节点上的 `atoll-agent` 仍然用 `install-agent.sh` 装成 systemd 服务，因为它要管理宿主机的 Docker、80/443 入口和备份。

## 什么时候选 Docker

| | `install-server.sh` | Docker Compose |
|---|---|---|
| 运行方式 | systemd 服务，二进制在 `/usr/local/bin` | 容器，二进制在镜像里 |
| HTTPS | 安装脚本装 Caddy（`--domain`） | 可选 `https` profile 起一个 Caddy 容器 |
| 升级 | 面板里一键自更新 / 回滚 | `docker compose pull && docker compose up -d`（面板里的自更新按钮不可用） |
| 面板「日志」 | `journalctl -u atoll-server` | 不可用，用 `docker compose logs server` |
| Tailscale 私网监听 | 支持（`--tailscale-authkey`） | compose 不配置；需要时请用安装脚本 |
| 数据目录 | `/var/lib/atoll` | 卷 `atoll_data`（容器内 `/var/lib/atoll`） |

## 准备

- 一台 Linux 主机，装好 Docker Engine 和 Compose 插件（`docker compose version` 能运行）。
- 用 `https` profile 时：一个 A/AAAA 记录指向这台主机的域名，且主机的 80、443 端口空闲。
- 这台主机**同时要当节点**时，先看下面的[同机部署](#同一台机器既当中控又当节点)。

## 1. 下载 compose 文件

```sh
mkdir -p /opt/atoll && cd /opt/atoll
base=https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/deploy/docker
curl -fsSLO $base/compose.yaml
curl -fsSLO $base/Caddyfile
curl -fsSL  $base/.env.example -o .env
```

编辑 `.env`：

| 变量 | 说明 |
|---|---|
| `ATOLL_VERSION` | 镜像标签。`latest` 跟随正式版；建议固定成某个 `vX.Y.Z`，升级时再改。预发布版本只有版本号标签，没有 `latest`。 |
| `ATOLL_PUBLIC_URL` | 浏览器、CLI 和**其他机器上的 agent** 访问中控的地址，例如 `https://panel.example.com`。也可以之后在「设置」里修改。 |
| `ATOLL_DOMAIN` | `https` profile 下 Caddy 签证书的域名。 |
| `ATOLL_BIND` / `ATOLL_PORT` | 面板端口发布到宿主机的哪个地址。默认 `127.0.0.1:7300`，只有本机（Caddy、反向代理、同机 agent）能访问。 |
| `ATOLL_SUBNET` / `ATOLL_PROXY_IP` | compose 网络的子网和 Caddy 容器的固定 IP（默认 `172.30.73.0/24` / `172.30.73.10`）。与内网冲突时两个一起改。 |
| `ATOLL_LOG_LEVEL`、`TZ` | 日志级别、时区。 |

## 2. 启动

**带 HTTPS（推荐，独立的中控主机）：**

```sh
docker compose --profile https up -d
```

Caddy 容器监听 80/443，为 `ATOLL_DOMAIN` 自动申请证书，并反向代理到 `server:7300`。中控只信任来自 Caddy 固定 IP 和本机回环的 `X-Forwarded-For`（compose 已写进 `ATOLL_TRUSTED_PROXIES`），登录限速和审计日志看到的是真实客户端 IP。

**只起中控（自带反向代理，或同机部署）：**

```sh
docker compose up -d
```

然后打开 `ATOLL_PUBLIC_URL`，创建第一个管理员账号，并在「设置 → 安全」启用两步验证。第一次登录后会出现一个可跳过的首次启动向导，带你填写中控地址、添加节点、配置域名和备份。

检查状态：

```sh
docker compose ps          # server 应为 healthy（镜像自带 /healthz 健康检查）
docker compose logs -f server
```

## 3. 添加节点

和二进制安装完全一样：在面板「服务器 → 添加」复制加入命令，到节点上执行。节点从中控的 `/downloads/` 下载与中控**同版本**的 `atoll-agent`——镜像里已经带了 amd64 和 arm64 两个 agent 二进制，每次容器启动时复制到数据卷的 `downloads/` 目录，升级镜像后 agent 升级也会拿到新版本。

节点必须能访问 `ATOLL_PUBLIC_URL`。如果 `ATOLL_BIND` 仍是 `127.0.0.1` 且没有起 Caddy 或其他反向代理，其他机器上的节点是连不上的。

## 同一台机器既当中控又当节点

节点上的 agent 会启动自己的入口容器 `atoll-caddy`，它要**独占宿主机的 80 和 443**；端口被占用时 agent 会拒绝启动入口（报错 `host port 80 is busy`）。所以同机部署时：

1. **不要**用 `--profile https`：只运行 `docker compose up -d`，中控只发布 `7300`。
2. 中控地址二选一：
   - 仅本机节点：保持 `ATOLL_BIND=127.0.0.1`，加入命令里的 `--server` 用 `http://127.0.0.1:7300`（agent 跑在宿主机上，可以直接访问）。浏览器通过 SSH 隧道打开面板：`ssh -L 7300:127.0.0.1:7300 root@主机`，再访问 `http://127.0.0.1:7300`。
   - 还要接入其他机器：设 `ATOLL_BIND=0.0.0.0`，`ATOLL_PUBLIC_URL=http://<主机IP>:7300`，用防火墙把 7300 限制在你的节点 IP 和自己的 IP；这条连接是明文 HTTP，只在受信网络或私网（如 Tailscale）里这样用。需要面板走 HTTPS 时，请把中控放到单独的主机上。
3. 然后在本机执行面板生成的 `install-agent.sh` 加入命令。

compose 里的容器名都以 `atoll-` 开头（`atoll-control`、`atoll-control-proxy`），agent 的「接管 Docker 容器」扫描会自动跳过它们，不会把中控当成可接管的业务；compose 网络名是 `atoll_control`，不会和 agent 管理的 `atoll-*` 项目网络冲突。

## 升级

镜像里的二进制是只读的一部分，所以面板「中控」页的自更新和回滚按钮在容器部署下不可用（页面会显示下面的命令，API 返回 `409 self_update_unsupported`）。在 compose 目录执行：

```sh
# 固定版本时先把 .env 里的 ATOLL_VERSION 改成新版本
docker compose pull
docker compose up -d
```

数据在卷里，重建容器不会丢失。升级前建议在中控页「立即备份」一次。回滚：把 `ATOLL_VERSION` 改回旧版本再 `up -d`；如果新版本已经迁移过数据库，用升级前的中控备份恢复。

## 备份与恢复

中控备份（每天一次，见[中控运维](control.md#中控备份)）照常工作，本地副本在卷里的 `control-backups/`。容器内没有 `/etc/atoll/server.env` 和 `/etc/caddy/Caddyfile`，备份包里会跳过这两项——请自己保存好 `compose.yaml`、`.env` 和 `Caddyfile`。

离线导出与恢复（命令都在 compose 目录执行；口令从标准输入传入，容器用户 `10001` 不需要读你的口令文件）：

```sh
# 导出：包先写进数据卷，再拷到宿主机
printf '%s\n' '你的口令至少12个字符' > pass && chmod 600 pass
docker compose exec -T server atoll-server export \
  --out /var/lib/atoll/atoll-control.atollbak --passphrase-file - < pass
docker compose cp server:/var/lib/atoll/atoll-control.atollbak .
docker compose exec server rm /var/lib/atoll/atoll-control.atollbak

# 在新主机上恢复（先放好 compose.yaml 和 .env；中控必须停着）
chmod 644 atoll-control.atollbak        # 备份包本身是加密的
docker compose run --rm -T --no-deps -v "$PWD/atoll-control.atollbak:/tmp/bundle.atollbak:ro" server \
  restore --bundle /tmp/bundle.atollbak --passphrase-file - --env-file "" < pass
docker compose up -d
```

`--env-file ""` 表示不合并备份里的 `server.env`（容器的环境变量来自 `.env`）；需要时手动对照备份里的值。目标卷里已有数据库时需要加 `--force`。重置密码、关闭两步验证同理：

```sh
docker compose exec server atoll-server reset-password
docker compose exec server atoll-server admin reset-2fa <用户名>
```

## 数据卷权限

容器以非 root 用户 `uid 10001` 运行。默认的命名卷会自动拥有正确权限。若改成绑定宿主机目录（如 `./data:/var/lib/atoll`），先执行：

```sh
sudo mkdir -p data && sudo chown -R 10001:10001 data && sudo chmod 700 data
```

## 自己构建镜像

```sh
git clone https://github.com/Sakuralaaa/Atoll && cd Atoll
docker build -f deploy/docker/Dockerfile --build-arg VERSION=dev -t atoll-server:dev .
```

然后在 `.env` 里设 `ATOLL_IMAGE=atoll-server`、`ATOLL_VERSION=dev`。注意 agent 要求与中控**版本号相同**，本地构建的镜像带的是同一次构建的 agent，可以直接用来加入节点。
