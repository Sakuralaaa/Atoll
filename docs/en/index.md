---
layout: home
hero:
  name: Atoll
  text: Self-hosted multi-VPS container panel
  tagline: One control server, an agent on each machine, plain Docker. Deploys, domains, backups and moves all happen on your own servers; services keep running while the control server is down.
  actions:
    - theme: brand
      text: Quick start
      link: /en/quick-start
    - theme: alt
      text: Live demo
      link: https://sakuralaaa.github.io/Atoll/demo/?demo=1&lang=en
      target: _blank
    - theme: alt
      text: GitHub
      link: https://github.com/Sakuralaaa/Atoll
features:
  - title: Deploy
    details: Docker images, private registries, builds from GitHub repositories (a Dockerfile is required), env vars, volumes, ports, one-click rollback; PostgreSQL / MySQL / Redis / MongoDB templates and one-click apps.
  - title: Domains and HTTPS
    details: A Caddy ingress on every machine; several root domains; with Cloudflare, DNS records are created for you, with Origin CA or ACME certificates.
  - title: Backups
    details: restic straight to any S3-compatible bucket, scheduled by the nodes themselves, so backups keep running while the control server is offline; periodic repository checks and restore drills.
  - title: Moves and takeovers
    details: Move services between machines; take over Zeabur projects and your own docker run / compose containers in place; move the control server with one command.
  - title: Access control
    details: Admins and sub-accounts with assigned servers and quotas; least-privilege API tokens scoped to projects; dangerous actions can require approval in the panel.
  - title: Automation
    details: The atoll CLI, the REST API and atoll mcp, so AI agents such as Claude Code and Codex work within their token's permissions.
---

> **Status: pre-alpha.** Try it on non-critical services first and rehearse a backup restore before relying on it.

Most guides on this site are in Chinese; the [panel guide](/ui-guide) and the [API reference](/api) are in English. The live demo is the panel's built-in demo mode: all data lives in your browser's memory and resets on reload.
