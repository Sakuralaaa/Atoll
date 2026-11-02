# Contributing to Atoll

## Before you open a PR

```bash
go vet ./... && gofmt -l . && go test ./...        # gofmt -l must print nothing
(cd web && npm ci && npm run lint && npm run build)
sh scripts/test-installers.sh                       # when install/ changed
```

CI also runs `go test -race` (needs cgo locally: `CGO_ENABLED=1 go test -race ./...`) and govulncheck.

## Keeping changes easy to review

- One concern per PR. A refactor (moving code, renaming) goes in its own PR with no behaviour change, so the diff of the real change stays small.
- Keep files focused. When a Go file passes ~600 lines, split it by section into `<topic>_<part>.go` in the same package (see `internal/server/zeabur_*.go`, `backups_*.go`, `internal/cli/commands_*.go`).
- Every behaviour change comes with a test next to it: `internal/server/*_test.go` drive the HTTP API against a fake agent (`startFakeAgent`), `internal/agent` tests use the fake runtime in `internal/agent/agenttest`.
- Schema changes are new numbered files in `internal/server/store/migrations/` (`0012_….sql`). Never edit a migration that has shipped.
- The agent and the server can run different versions during an upgrade. New fields in `internal/api` should be optional (`omitempty`), and before adding a message type check what an older agent does with it.

## Where things live

| Path | What |
| --- | --- |
| `internal/server` | HTTP API, auth and permissions (`perms.go`, `routes.go`), deploy orchestration, imports, backups, control |
| `internal/server/store` | SQLite access. Writes go through `Store.DB` (one connection); heavy panel reads use the read pool |
| `internal/agent` | Node daemon: Docker runtime, Caddy ingress, builds, backups, tailnet |
| `internal/api` | Types shared by server, agent and CLI. `protocol.go` is the agent WebSocket protocol |
| `internal/cli` | `atoll` CLI and the MCP server |
| `web/src` | Panel. `api/client.ts` is the only transport; `?demo=1` runs it against built-in demo data |
| `install/`, `deploy/` | Install scripts and systemd units |
| `docs/` | API contract and operator guides; update them with the code |

## Security issues

Do not open a public issue. Contact the maintainer privately first.
