# atoll-agent: status and server follow-ups (2026-10-30)

Agent work is done: builds, vets, `go test ./internal/agent/...` passes, and linux/amd64 + linux/arm64 cross-builds succeed.

## Things the server side has to do
1. **`/downloads/atoll-agent-linux-<arch>`**: `install/install-agent.sh` tries this URL first. The server does not serve it yet, so installs fall back to `github.com/atoll-sh/atoll/releases/latest/download/atoll-agent-linux-<arch>`. The script only accepts a download it can actually run (`atoll-agent version`), so an HTML page from the web UI also triggers the GitHub fallback.
2. **Ingress hold**: on the agent, `Ingress` refuses to create or start `atoll-caddy` while any guard fails. The guards are:
   - `HostPortsFree(80, 443)`
   - `zeabur.Takeover.IngressGuard`, which is registered in `cmd/atoll-agent/main.go`. It blocks while any k3s/Zeabur ingress DaemonSet (hostPort 80/443, or hostNetwork with an ingress-like name, svclb included) still has pods.

   While the guard blocks, `MsgIngress` returns `ok:false` with an error starting "not starting atoll-caddy: …Zeabur/k3s ingress is still running". The payload is still saved and is retried at boot (and blocked again). The server should:
   - not send routes for a project on ingress hold (rehearsal), or
   - treat that error as "held", not "failed", until `zeabur.ingress {park:true}` succeeds at cutover.

   `RestoreIngress` (rollback) stops atoll-caddy before un-parking. A stopped caddy will not be restarted while the guard blocks.
3. The join command in `handlers_servers.go` already uses `install-agent.sh --server … --token …`. That matches the script; `--tailscale-authkey` is optional.
