# Atoll API contract (v1)

Types: `internal/api/types.go` (Go) — the web app mirrors them in `web/src/api/types.ts`.
All JSON. Errors: non-2xx with `ErrorBody`.

## Components
- `atoll-server` (control node): REST API + embedded web UI (`web/dist` via `go:embed`), SQLite (modernc.org/sqlite, WAL), agent WebSocket hub, Cloudflare DNS, MCP-over-HTTP not required (MCP is in the CLI, stdio).
  Listens on `ATOLL_LISTEN` (default `:7300`). Data dir `ATOLL_DATA` (default `/var/lib/atoll-server`), secrets encrypted with AES-GCM key in `<data>/master.key`.
- `atoll-agent` (each worker VPS): connects out to the server over WebSocket, runs containers with the Docker Engine API, keeps desired state in `/var/lib/atoll/state/*.json` so it keeps working when the server is offline, configures a Caddy container (`atoll-caddy`) via its admin API for ingress, collects metrics.
- `atoll` CLI: talks to REST with a PAT; `atoll mcp` serves MCP over stdio; `atoll import zeabur` migrates from Zeabur.

## Auth
- Panel: `POST /api/v1/auth/login {username,password}` → httpOnly cookie `atoll_session`. `POST /api/v1/auth/logout`, `GET /api/v1/auth/me`.
  First run: if no user exists, `POST /api/v1/auth/setup {username,password}` creates the owner (only allowed while users table is empty). `GET /api/v1/auth/status` → `{setup_required: bool}`.
- API/CLI: `Authorization: Bearer atl_pat_...`. Scopes: `read` (GET only), `deploy` (everything except destructive), `admin`.
- Destructive actions with a PAT (delete service/project/server, migrate, add/delete domain) → `202 Confirmation`; approved in panel via `POST /api/v1/confirmations/{id}/approve|reject`. Session (panel) requests execute directly.

## REST endpoints (/api/v1)
| Method | Path | Body / notes |
|---|---|---|
| GET | /servers | []Server (with latest Usage) |
| POST | /servers/join-token | → JoinToken (valid 1h, single use) |
| GET/PATCH/DELETE | /servers/{id} | PATCH {name, provider, region} |
| GET | /servers/{id}/metrics?range=1h\|24h\|7d | []NodeMetrics |
| GET | /servers/{id}/containers | []ContainerState |
| GET/POST | /projects | POST {name, server_id, description} |
| GET/PATCH/DELETE | /projects/{id} | GET includes services |
| GET/POST | /projects/{id}/services | POST Service (kind+image or template kind) |
| GET/PATCH/DELETE | /services/{id} | PATCH partial Service → triggers redeploy |
| POST | /services/{id}/actions | {action: start\|stop\|restart\|redeploy} |
| POST | /services/{id}/rollback | {deployment_id} |
| POST | /projects/{id}/migrate | {server_id}: stop → volume export/import via server relay → start on target → DNS switch |
| GET/PUT | /services/{id}/env | []EnvVar (PUT replaces all; secret values "" keep old) ; `?reveal=1` shows secrets (session only) |
| GET | /services/{id}/env/references | []EnvVar exposed by other services in project |
| GET/POST | /services/{id}/domains | POST {hostname, port_name, proxied} |
| DELETE | /domains/{id} | |
| GET | /domains | all domains |
| GET | /services/{id}/deployments | []Deployment |
| GET | /services/{id}/metrics?range= | []ContainerMetrics |
| GET (WS) | /services/{id}/logs?tail=200&follow=1 | stream of LogLine JSON frames |
| POST | /services/{id}/exec | {cmd: []string} → ResultPayload |
| GET/POST/DELETE | /tokens, /tokens/{id} | APIToken |
| GET | /confirmations?status=pending | |
| POST | /confirmations/{id}/approve, /reject | session only |
| GET/PUT | /settings | Settings |
| GET/POST | /imports | POST ImportPlan → ImportJob (creates project+services, not started) |
| GET | /imports/{id} | ImportJob |
| PUT | /imports/{id}/volumes/{service_name}/{volume} | raw tar.gz body, streamed to target agent via volume.import |
| POST | /imports/{id}/start | deploy all services |
| GET | /templates | db templates (postgres, mysql, redis, mongo) |
| GET | /events (WS) | live updates {type: "service"|"server"|"deployment"|"confirmation", payload} |

## Agent endpoints
- `POST /agent/v1/join` JoinRequest → JoinResponse
- `GET  /agent/v1/connect` (WebSocket, `Authorization: Bearer <node_token>`) — Envelope frames, see types.go.
- `PUT  /agent/v1/blobs/{token}` / `GET /agent/v1/blobs/{token}` — volume tar.gz relay for migration/import (one-time tokens, stored in `<data>/blobs`, deleted after import).

## Conventions
- IDs: 12-char lowercase base32 with prefix: `srv_`, `prj_`, `svc_`, `dep_`, `dom_`, `tok_`, `cfm_`, `imp_`.
- Docker: container name `atoll-<service_id>`, labels `atoll.service=<id>`, `atoll.project=<id>`, `atoll.deployment=<id>`; network per project `atoll-<project_id>` with alias = service name; every container with an http port also joins `atoll-ingress`, where `atoll-caddy` runs. Restart policy `unless-stopped`.
- Volumes: bind mounts under `/var/lib/atoll/volumes/<service_id>/<volume_name>`.
- Env references: `${KEY}` resolves own vars then exposed vars of other services in project; `${SERVICE.KEY}` for explicit; built-ins: `${<SERVICE>_HOST}` (service name), `${<PORTNAME>_PORT}`, `${ATOLL_<PORTNAME>_DOMAIN}`, `${ATOLL_<PORTNAME>_URL}`, `PORT` = first port, `PASSWORD` random 32 chars for db templates.
- Times UTC RFC3339.

## In-place Zeabur takeover (primary import path)
Target user: their OWN VPS registered to Zeabur (unmanaged, single-node k3s), services built by Zeabur, data on that VPS. Atoll takes the same machine over — no server-to-server move.
1. Install atoll-agent on that VPS (join command). Agent detects k3s (`k3s kubectl`), `GET /servers/{id}/zeabur` shows Zeabur projects/services found locally (types `ZeaburInventory`).
2. `POST /imports/zeabur-local` creates an Atoll project on that server with services mapped 1:1: env from pod spec (expanded), `<x>.zeabur.internal` → service name, Zeabur built-ins translated, ZEABUR_* ids dropped, ports, volumes, configmaps, custom domains (zeabur.app → warning).
3. Rehearse: agent exports Zeabur-built images from containerd (`k3s ctr -n k8s.io images export` | `docker load`, tag `atoll-import/<svc>:<digest12>`), pulls public images pinned to running digest, copies PVC dirs locally (apps) and dump/restore DBs (same major version), starts containers WITHOUT public ingress, verifies row counts. Zeabur keeps serving.
4. Cutover (user confirms): scale Zeabur app workloads to 0 (DBs keep running until final dump), final DB sync + volume rsync, park Zeabur ingress-controller DaemonSet, start atoll-caddy on 80/443 with the same hostnames. DNS unchanged (same IP) — custom domain A records already point here; only *.zeabur.app URLs die.
5. Rollback: stop atoll services, restore ingress-controller, scale Zeabur workloads back to recorded replicas.
6. Later, after user approval: remove Zeabur components / k3s (`/usr/local/bin/k3s-uninstall.sh`) to free RAM — explicit confirmation, never automatic.
Risk: both stacks run side by side during rehearsal (RAM/disk) — scan reports free mem/disk; refuse rehearsal if disk free < 1.2× data size.
