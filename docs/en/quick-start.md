# Quick start

From a blank Linux host to the control server, a first server and a first service.

## Requirements

- A Debian or Ubuntu host for the control server, and at least one Debian / Ubuntu server (amd64 or arm64).
- Root on the hosts; servers need Docker (the agent install script installs it).
- A domain name pointing at the control host, or your own HTTPS reverse proxy.
- An S3-compatible bucket for backups is recommended.

Atoll is **pre-alpha**. Try it on unimportant workloads first, and confirm backups and restores before hosting anything that matters.

## 1. Install the control server

Replace `panel.example.com` with a domain that already points at the control host:

```bash
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-server.sh \
  | sudo sh -s -- --domain panel.example.com
```

The script sets up the `atoll-server` systemd service, Caddy and HTTPS; the panel listens on localhost only and Caddy serves it. Open `https://panel.example.com`, create the first admin account, and turn on two-factor authentication under Settings → Security. A skippable first-run guide then walks you through the control server's address, the first server, Cloudflare and backups.

Prefer containers? Run the control server with Docker Compose instead: see [Docker install](/install-docker) (Chinese).

## 2. Add a server

In the panel, Servers → Add server shows an install command. Run it as root on the new machine. It carries a one-time join token and the SHA-256 of the agent binaries:

```bash
curl -fsSL https://raw.githubusercontent.com/Sakuralaaa/Atoll/main/install/install-agent.sh \
  | sudo sh -s -- --server https://panel.example.com --token atl_join_xxx \
      --sha256-amd64 … --sha256-arm64 …
```

Always use the full command the panel generates; tokens are single-use and expire. The agent connects back to the control server over a WebSocket, so the server needs no inbound port for it.

## 3. Deploy a first service

1. Under Projects, create a project and pick a server (or deploy an app template right away).
2. Open the project, choose New service, then a Docker image or a database template.
3. Enter the image (for example `nginx:1`), a service name and the HTTP port, and deploy.
4. On the service page, bind a domain. With a Cloudflare root domain and token configured, Atoll creates the DNS record; otherwise add an A record pointing at the server's public IP.

Services in a project reach each other by service name. Talking across servers needs the private network (Tailscale) under Settings.

## 4. Set up backups

Under Settings → Backup storage, enter the endpoint, bucket and credentials of S3-compatible storage, then enable a backup policy for each service that needs one. Keep the generated repository password somewhere safe: without it the snapshots cannot be read.

## Next

- [Panel guide](/ui-guide) — what each page means
- [API reference](/api)
- [Live demo](https://sakuralaaa.github.io/Atoll/demo/?demo=1&lang=en)
