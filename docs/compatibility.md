# 版本兼容与升级承诺

Atoll 仍是 pre-alpha（0.x）。下面是维护者对升级路径做出的、**可以用代码和测试检验**的承诺；每条后面注明它依据的代码或测试。超出这里的组合不在支持范围内。

## 1. 版本号与发布通道

- 正式版（stable）：`vX.Y.Z`。测试版（beta）：`vX.Y.Z-beta.N`。
- release 工作流把任何带 `-` 后缀的 tag 发布为 GitHub **pre-release**（`.github/workflows/release.yml`）。
- **默认只走 stable**：
  - `install-server.sh` / `install-agent.sh` 不带 `--version` 时下载 `releases/latest`，GitHub 定义上不含 pre-release 与 draft。
  - 面板「中控 → 更新」不指定版本时，若取到的最新 Release 是 pre-release（GitHub 标记或 tag 带后缀，任一即可），返回 `409 prerelease`（`checkUpdateTarget`，测试 `TestCheckUpdateTarget`）。
- **beta 需要显式选择**：`POST /api/v1/control/update {"version": "v0.2.0-beta.1"}`，或 `install-server.sh --version v0.2.0-beta.1`。运行 beta 的中控之后可以正常升到同版本或更高的正式版。

## 2. 0.x 的破坏性变化

0.x 期间小版本之间仍可能有破坏性变化（API 字段、行为、配置项）。每一个破坏性变化都会写进该版本的发布说明 `docs/releases/<tag>.md` 的「破坏性变化」一节，并附升级步骤；没有写进发布说明的破坏性变化视为 bug。

## 3. 弃用

被弃用的 API 字段、路由、CLI 参数、环境变量至少再保留 **2 个小版本**：在 `v0.Y` 宣布弃用的，最早在 `v0.(Y+2).0` 删除。弃用在发布说明和 `docs/api.md` 中标注。

## 4. 中控升级

- **迁移只前进**：数据库迁移是 `internal/server/store/migrations/NNNN_*.sql`，启动时按序应用缺失的迁移并记录在 `schema_migrations`；没有 down 迁移。
- **升级前备份**：面板一键更新会先自动做一次 `pre-update` 中控备份（`TestControlUpdateFlow`）；用安装脚本升级前请先在「中控 → 导出」或 `atoll-server export` 做一次备份。
- **跨版本直升**：任意 **≥ v0.1.40** 的中控都可以直接升级到最新正式版，不需要逐个版本升级——缺失的迁移会一次按序应用。
- **不支持降级中控**：
  - 面板更新到比当前旧的版本返回 `409 downgrade_unsupported`（`TestCheckUpdateTarget`）。
  - `install-server.sh --version` 指定的版本比已安装的旧时拒绝执行，除非显式 `--allow-downgrade`（`scripts/test-installers.sh`）；`--allow-downgrade` 不受支持，后果自负。
  - 唯一支持的“回退”是一键更新后立即使用「回滚」：换回更新时保留的 `atoll-server.prev`。新版本加的表/列旧版本一般会忽略；如有问题，用 `pre-update` 备份恢复（见 [control.md](control.md)）。
  - 开发版本（版本号不是 semver，例如 `dev`）不做版本比较。

## 5. 中控与 agent 的版本规则

以下规则按代码实际行为写成：

1. **目标版本 = 中控版本**。中控只托管与自己同版本的 agent 二进制（中控更新时一并刷新 `<data>/downloads/atoll-agent-linux-*`），一键升级把 agent 换成**恰好**中控的版本；agent 版本 ≠ 中控版本时面板显示可升级（`startAgentUpgrade`、`withAgentInfo`）。
2. **旧 agent 可以继续工作**：支持 agent 版本在 **v0.1.40 ≤ agent ≤ 中控** 范围内。协议只做增量：JSON 未知字段双方都忽略；需要新 agent 的能力通过 hello 里的能力标志判断（如 `self_upgrade`、`mesh_token`）；旧 agent 收到不认识的消息类型时回 `unsupported message type` 错误（`internal/agent/session_test.go`），中控把它作为该操作的失败返回，不会破坏已有容器。建议中控升级后尽快把 agent 升到同版本。
3. **agent 比中控新不受支持**：只在升级过程中短暂出现。此时对该节点执行一键升级会把 agent 换回中控的版本。
4. **agent 可以回滚**：一键升级失败（新 agent 没在时限内重连）时 helper 自动换回 `atoll-agent.prev`；也可以用中控托管的二进制或 `install-agent.sh --version <tag>` 把 agent 换到中控支持范围内的任一版本。容器在 agent 升级/回滚期间不重启。
5. 不支持自升级（hello 不带 `self_upgrade`，v0.1.6 之前）的 agent 需要在节点上重跑面板给出的安装命令。

## 6. 推荐升级顺序

1. 导出中控备份。
2. 升级中控（面板一键更新，或 `install-server.sh`）。
3. 在「服务器」页对所有节点执行 agent 一键升级。

---

## English summary

- **Channels**: stable `vX.Y.Z`, beta `vX.Y.Z-beta.N` (published as GitHub pre-releases). Installers and the panel's one-click update use stable only by default (`409 prerelease` otherwise); a beta is installed only when its tag is named explicitly.
- **Breaking changes** during 0.x are listed in the release notes (`docs/releases/<tag>.md`). **Deprecations** are kept for at least 2 minor versions.
- **Control server**: migrations only go forward; take a backup before upgrading (one-click update does it automatically). Any control server **≥ v0.1.40** upgrades directly to the latest stable. **Downgrading the control server is not supported** (`409 downgrade_unsupported`; `install-server.sh` refuses unless `--allow-downgrade`); only the immediate rollback to the binary kept by the last update is supported.
- **Control ↔ agent**: the control server upgrades agents to exactly its own version. Agents from v0.1.40 up to the control's version are supported (the protocol is additive, features are capability-gated, unknown messages fail cleanly). An agent newer than the control is unsupported; upgrading it from the panel brings it back. **Agents can be rolled back** (automatic on a failed upgrade, or by installing an older supported tag).
