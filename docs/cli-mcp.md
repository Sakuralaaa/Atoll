# CLI 与 MCP

`atoll` 是同一套 REST API 的命令行客户端；`atoll mcp` 用 stdio 提供 MCP 服务，让 Claude Code、Codex 等 AI agent 操作 Atoll。它们都用面板里创建的 **API 令牌**（`atl_pat_…`）认证。

## 安装与登录

从 [GitHub Releases](https://github.com/Sakuralaaa/Atoll/releases) 下载对应平台的 `atoll`（Linux、macOS、Windows，amd64 / arm64），放进 `PATH`，然后在面板「连接」页创建令牌：

```sh
atoll login --url https://panel.example.com --token atl_pat_...
```

管理多个面板：`atoll login --name NAME ...` 保存命名面板，`atoll context ls|use NAME` 切换，单条命令用 `--panel NAME`。也可以用环境变量 `ATOLL_URL` / `ATOLL_TOKEN`。

## 常用命令

```sh
atoll deploy ghcr.io/me/api:v1.9 -p saas-api
atoll env set -s api DATABASE_URL=...
atoll env import -s api .env
atoll logs api -f
atoll domain add api.example.com -s api
atoll templates deploy postgres -p saas-api
atoll apps create memos --domain memos.example.com
atoll migrate saas-api --to fra
atoll servers upgrade-agent --all
atoll completion zsh
```

`--wait-ready` 让 `deploy`、`redeploy`、`rollback`、`restart` 等命令等到部署真正运行；失败时打印原因和最后 20 行构建日志并以退出码 4 结束。

## 令牌与权限

令牌的权限可选：`read`、`deploy`、`exec`、`delete`、`migrate`、`domains`、`restore`、`servers`、`settings`、`tokens`，还可以限定到部分项目。

- 缺少权限时服务器返回 `403 missing_permission`，CLI 会提示要加的权限。
- `exec`（在容器里执行命令、读取私有变量的值）是单独的权限，`deploy` 不包含它。
- 创建时开启 `require_approval` 的令牌，删除、迁移、恢复等危险操作会进入面板「审批」，CLI 返回退出码 3（`--wait` 等待批准）。

```sh
atoll token create ci --perm read,deploy --project saas-api
atoll token edit ci --add-perm domains
```

## 接入 AI agent（MCP）

Claude Code：

```sh
claude mcp add atoll -- atoll mcp --url https://panel.example.com --token atl_pat_...
```

Codex（`~/.codex/config.toml`）：

```toml
[mcp_servers.atoll]
command = "atoll"
args = ["mcp", "--url", "https://panel.example.com", "--token", "atl_pat_..."]
```

`atoll mcp` 启动时读取令牌的权限，只注册允许的工具（如 `service_deploy`、`env_set`、`domain_add`、`backup_run`、`deployment_wait`）。被拒绝的调用返回 `PERMISSION DENIED` 并写明缺少的权限。完整工具列表见 [API 文档 → CLI & MCP permissions](api.md#cli-mcp-permissions)。

## REST API

```sh
curl https://panel.example.com/api/v1/projects -H "Authorization: Bearer atl_pat_..."
```

接口约定见 [API 文档](api.md)。
