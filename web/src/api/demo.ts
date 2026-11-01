// Demo backend: serves the original mock data through the same REST paths the real client uses.
// Enabled with ?demo=1 or VITE_DEMO=1. Mutations live in memory for the tab.
import { projects as mockProjects, servers as mockServers } from '../mock'
import { series } from '../components/ui'
import { ApiError } from './client'
import { makeDemoUpdates } from './demo-updates'
import { makeDemoBackups } from './demo-backups'
import type {
  APIToken, Confirmation, ContainerMetrics, ContainerState, Deployment, Domain, EnvReference, EnvVar, ImportJob, ImportPlan,
  LogLine, NodeMetrics, Project, Server, Service, SessionInfo, Settings, Template, ZeaburInventory, ImportStep, ImportVerify,
} from './types'

const now = Date.now()
const iso = (msAgo: number) => new Date(now - msAgo).toISOString()
const H = 3600e3, D = 24 * H
let seq = 1000
const nid = (p: string) => `${p}_demo${(seq++).toString(36)}`

const TAILNET: Record<string, [string, boolean]> = { phx: ['100.64.0.10', true], fra: ['100.64.0.20', false], tyo: ['100.64.0.4', true] }
const servers: Server[] = mockServers.map(s => ({
  id: s.id, name: s.name, role: s.role ?? 'worker', status: s.status, public_ipv4: s.ip, provider: s.provider, region: s.region,
  tailnet_host: `${s.id}.tail3c1a.ts.net`, os: s.os, arch: s.arch, cpu_cores: s.cpu, mem_mb: s.memGB * 1024, disk_gb: s.diskGB,
  agent_version: 'v0.1.0', last_seen_at: iso(s.status === 'online' ? 8e3 : 12 * 60e3), created_at: '2026-10-12T08:00:00Z',
  // demo private network: phx + fra joined the tailnet (fra still on the public channel), sin not yet
  ...(TAILNET[s.id] ? { tailnet_ip: TAILNET[s.id][0], network: { provider: 'tailscale', state: 'running', via_tailnet: TAILNET[s.id][1], control_url: TAILNET[s.id][1] ? 'http://100.64.0.4:7301' : 'https://panel.example.com', fallback_urls: TAILNET[s.id][1] ? ['https://panel.example.com'] : [] } } : { network: { provider: '', state: 'not_installed', via_tailnet: false, control_url: 'https://panel.example.com' } }),
  usage: { at: iso(8e3), cpu_pct: s.cpuUse, mem_used_mb: Math.round(s.memGB * 1024 * s.memUse / 100), mem_total_mb: s.memGB * 1024, disk_used_gb: +(s.diskGB * s.diskUse / 100).toFixed(1), disk_total_gb: s.diskGB, net_rx_bps: 120e3, net_tx_bps: 80e3, load1: s.cpuUse / 25 },
}))

const VOLS: Record<string, [string, string][]> = {
  postgres: [['data', '/var/lib/postgresql/data']], mysql: [['data', '/var/lib/mysql']], redis: [['data', '/data']], mongo: [['data', '/data/db']],
}
const services: Service[] = []
const projects: Project[] = []
const domains: Domain[] = []
const env: Record<string, EnvVar[]> = {}
const deployments: Record<string, Deployment[]> = {}

for (const p of mockProjects) {
  projects.push({ id: p.id, name: p.name, server_id: p.serverId, created_at: '2026-10-11T03:00:00Z' })
  for (const s of p.services) {
    const db = s.kind !== 'image'
    const svc: Service = {
      id: s.id, project_id: p.id, server_id: p.serverId, name: s.name, kind: s.kind, image: `${s.image}:${s.tag}`, status: s.status, replicas: s.replicas,
      created_at: `${s.created}T06:00:00Z`,
      ports: [{ name: db ? s.kind : 'web', container_port: s.port, protocol: db ? 'tcp' : 'http' }],
      volumes: (VOLS[s.kind] ?? [['data', '/app/data'], ['logs', '/app/logs']]).map(([name, mount_path]) => ({ name, mount_path })),
      cpu_limit: db ? 0 : 1, mem_limit_mb: db ? 0 : 512, health: db ? undefined : { http_path: '/health', interval_sec: 30 },
    }
    services.push(svc)
    if (s.domain) domains.push({ id: `dom_${s.id}`, service_id: s.id, hostname: s.domain, port_name: 'web', proxied: true, status: s.status === 'failed' ? 'error' : 'active', error: s.status === 'failed' ? '后端不可达 (502)' : undefined, created_at: svc.created_at })
    const U = db ? s.kind.toUpperCase() : ''
    env[s.id] = db ? [
      { key: `${U}_HOST`, value: s.name, secret: false, exposed: true },
      { key: `${U}_PORT`, value: String(s.port), secret: false, exposed: true },
      { key: `${U}_USERNAME`, value: s.kind === 'redis' ? 'default' : 'root', secret: false, exposed: true },
      { key: `${U}_PASSWORD`, value: 'Zq8vN2kPx4LmT7rWc9YbA3sD6fGh1jKe', secret: true, exposed: true },
      { key: `${U}_CONNECTION_STRING`, value: `${s.kind}://root:Zq8vN2kPx4LmT7rWc9YbA3sD6fGh1jKe@${s.name}:${s.port}`, secret: true, exposed: true },
    ] : [
      { key: 'PORT', value: String(s.port), secret: false, exposed: false },
      { key: 'TZ', value: 'Asia/Shanghai', secret: false, exposed: false },
      { key: 'SECRET_KEY', value: 'f3c9a1e07b5d48e2a6c1b9d0e4f7a2c8', secret: true, exposed: false },
      { key: 'ADMIN_PASSWORD', value: 'atoll-demo-pass', secret: true, exposed: false },
      { key: 'PUBLIC_URL', value: 'https://${ATOLL_WEB_DOMAIN}', secret: false, exposed: false },
    ]
    deployments[s.id] = [0, 1, 2].map(i => ({
      id: `dep_${s.id}${i}`, service_id: s.id, server_id: p.serverId, image_digest: `sha256:${['7f3a9c0e', '2b81d4f6', 'c09e5a13'][i]}5d1e8b0c4a9f27e21b`,
      status: i === 0 ? (s.status === 'failed' ? 'failed' : s.status === 'deploying' ? 'pulling' : 'running') : 'superseded',
      trigger: ['env', 'redeploy', 'create'][i], message: i === 0 && s.status === 'failed' ? 'container exited with code 1 (restarted 14 times)' : undefined,
      started_at: iso((i * 3 + 2) * D), finished_at: iso((i * 3 + 2) * D - 40e3),
    }))
  }
}
projects.push({ id: 'sandbox', name: 'sandbox', description: '空项目示例', server_id: 'fra', created_at: iso(D) })
// image services in projects with a DB reference it
for (const p of projects) {
  const ss = services.filter(s => s.project_id === p.id), db = ss.find(s => s.kind !== 'image')
  if (db) ss.filter(s => s.kind === 'image').forEach(s => env[s.id].splice(2, 0, { key: 'DATABASE_URL', value: `\${${db.kind.toUpperCase()}_CONNECTION_STRING}`, secret: false, exposed: false }))
}

const tokens: APIToken[] = [
  { id: 'tok_demo1', name: 'claude-code-macbook', scope: 'admin', permissions: ['read', 'deploy', 'delete', 'migrate', 'domains', 'restore', 'servers', 'settings', 'tokens'], require_approval: true, prefix: 'atl_pat_3fA9', last_used_at: iso(2 * 60e3), created_at: iso(9 * D) },
  { id: 'tok_demo2', name: 'codex-ci', scope: 'deploy', permissions: ['read', 'deploy', 'domains'], projects: ['acme', 'linkproxy'], prefix: 'atl_pat_k21Q', last_used_at: iso(D), created_at: iso(6 * D) },
  { id: 'tok_demo3', name: '只读监控', scope: 'read', permissions: ['read'], prefix: 'atl_pat_Zx07', last_used_at: iso(5 * D), created_at: iso(12 * D) },
]
const confirmations: Confirmation[] = [
  { id: 'cfm_demo1', action: 'service.delete', target: 'cron-worker', summary: '删除服务 LinkProxy / cron-worker（容器、2 个卷）', status: 'pending', created_by: 'codex-ci', created_at: iso(4 * 60e3) },
  { id: 'cfm_demo2', action: 'domain.add', target: 'api.example.com', summary: '为 acme-api / acme-api 绑定 api.example.com（Cloudflare 代理记录 → 203.0.113.10）', status: 'pending', created_by: 'claude-code-macbook', created_at: iso(18 * 60e3) },
  { id: 'cfm_demo3', action: 'service.migrate', target: 'file-share', summary: '把项目 file-share 从 Hetzner CAX21 Falkenstein 迁移到 Oracle ARM West Phoenix', status: 'approved', created_by: 'claude-code-macbook', created_at: iso(2 * D) },
  { id: 'cfm_demo4', action: 'project.delete', target: 'old-blog', summary: '删除项目 old-blog（3 个服务）', status: 'rejected', created_by: 'codex-ci', created_at: iso(4 * D) },
]
const settings: Settings = { base_domain: 'example.com', cloudflare_zone_id: '9f2c4b7a1e0d43c58a6b2f1e7d3c9a05', has_cloudflare_token: true, tls_mode: 'cloudflare-origin', public_url: 'https://panel.example.com', has_origin_cert: true,
  network_provider: 'tailscale', has_tailscale_auth_key: true, tailnet_name: 'tail3c1a.ts.net', tailnet_control_url: 'http://100.64.0.4:7301', tailnet_listen: '100.64.0.4:7301', agent_public: true }
const tpl = (kind: string, name: string, image: string, description: string, port: number, mount: string): Template =>
  ({ kind, name, image, description, ports: [{ name: kind, container_port: port, protocol: 'tcp' }], volumes: [{ name: 'data', mount_path: mount }], env: [] })
const templates: Template[] = [
  tpl('postgres', 'PostgreSQL', 'postgres:17-alpine', '自动生成密码，暴露 POSTGRES_CONNECTION_STRING', 5432, '/var/lib/postgresql/data'),
  tpl('mysql', 'MySQL', 'mysql:8.4', '自动生成 root 密码与连接串', 3306, '/var/lib/mysql'),
  tpl('redis', 'Redis', 'redis:7-alpine', '带密码与持久化', 6379, '/data'),
  tpl('mongo', 'MongoDB', 'mongo:8.0', '自动生成 root 账号', 27017, '/data/db'),
]
const mkPlan = (name: string, svcs: [string, string, string, number][], warnings: string[] = []): ImportPlan => ({
  source: 'zeabur', source_project: '7f00e1f2a9c3', project_name: name, target_server_id: 'phx', warnings,
  services: svcs.map(([n, kind, image, bytes]) => ({ source_service_id: `zb-${n}`, name: n, kind, image, image_needs_push: image.includes('zeabur'), ports: [], volumes: [], env: [], domains: [], data_bytes: bytes })),
})
const STEPS = ['创建项目与服务', '推送镜像', '上传数据卷', '部署服务', '健康检查', '切换 DNS']
const imports: ImportJob[] = [
  { id: 'imp_demo1', status: 'deploying', created_at: iso(25 * 60e3), project_id: 'acme',
    plan: mkPlan('acme-api', [['acme-api', 'image', 'ghcr.io/example/acme-api:latest', 0], ['postgresql', 'postgres', 'postgres:17-alpine', 182e6], ['redis', 'redis', 'redis:7-alpine', 3e6]], ['acme-api.zeabur.app 不会迁移，请改用自定义域名']),
    steps: STEPS.map((name, i) => ({ name, status: i < 3 ? 'done' : i === 3 ? 'running' : 'pending', detail: i === 2 ? '2 个卷 · 185 MB' : i === 3 ? '2/3 已运行' : undefined })) },
  { id: 'imp_demo2', status: 'done', created_at: iso(3 * D), project_id: 'mailrelay',
    plan: mkPlan('mail-relay', [['mail-relay', 'image', 'registry.zeabur.cloud/p-7f00/mailrelay:7c1', 41e6]]),
    steps: STEPS.map(name => ({ name, status: 'done' })) },
  { id: 'imp_demo3', status: 'planned', created_at: iso(5 * 60e3),
    plan: mkPlan('blog-studio', [['blog-studio', 'image', 'ghcr.io/example/blog-studio:v1.0.3', 12e6], ['mysql', 'mysql', 'mysql:8.4', 640e6]]),
    steps: STEPS.map(name => ({ name, status: 'pending' })) },
]

// ---------- in-place Zeabur takeover (wizard demo) ----------
const zv = (pvc: string, mount_path: string, bytes: number, ns: string) => ({ pvc, mount_path, host_dir: `/var/lib/rancher/k3s/storage/pvc-${pvc}_${ns}_${pvc}`, bytes })
const NS1 = 'environment-a1b2c3d4e5f6', NS2 = 'environment-e5f6a7b8c9d0'
const zeabur: ZeaburInventory = {
  k3s: true, k3s_version: 'v1.30.4+k3s1', ingress_running: true, disk_free_gb: 112.4, mem_free_mb: 9830, k3s_mem_mb: 1460, scanned_at: iso(20e3),
  projects: [
    { namespace: NS1, project_id: 'a1b2c3d4e5f6a', project_name: 'acme-api', environment_name: 'production', services: [
      { service_id: 'a1b2d0e2f3', name: 'acme-api', workload: 'deployment/service-a1b2d0e2f3', replicas: 1, role: 'app', kind: 'image', image: 'registry.zeabur.cloud/a1b2c3d4/acme-api:9c1e7b2', image_id: 'sha256:4be1c09a7d2f', zeabur_registry: true, image_cached: true,
        ports: [{ name: 'web', container_port: 8000, protocol: 'http' }], volumes: [zv('acme-api-data', '/app/data', 41e6, NS1)], domains: ['acme-api.zeabur.app'],
        env: [{ key: 'PUBLIC_URL', value: 'https://acme-api.zeabur.app', secret: false, exposed: false }, { key: 'CALLBACK_URL', value: 'https://acme-api.zeabur.app/api/callback', secret: false, exposed: false }, { key: 'DATABASE_URL', value: '', secret: true, exposed: false }, { key: 'REDIS_URL', value: 'redis://redis.zeabur.internal:6379', secret: false, exposed: false }],
        status: 'running', data_store: '', k8s_services: ['service-a1b2d0e2f3'],
        references: [
          { key: 'DATABASE_URL', target: 'a1b2c1d0f7', target_key: 'POSTGRES_CONNECTION_STRING', via: 'shared-secret', object: 'Secret/postgresql-a1b2c1d0f7', inferred: false },
          { key: 'REDIS_URL', target: 'a1b2c1d10a', via: 'zeabur-internal', object: 'redis.zeabur.internal', inferred: true },
        ] },
      { service_id: 'a1b2c1d0f7', name: 'postgresql', workload: 'statefulset/service-a1b2c1d0f7', replicas: 1, role: 'database', kind: 'postgres', image: 'postgres:16-alpine', image_id: 'sha256:9a07e3c1b4d8', zeabur_registry: false, image_cached: true,
        ports: [{ name: 'database', container_port: 5432, protocol: 'tcp' }], env: [], volumes: [zv('postgresql-data', '/var/lib/postgresql/data', 182e6, NS1)], domains: [],
        status: 'running', data_store: 'postgres', k8s_services: ['service-a1b2c1d0f7', 'postgresql'],
        warnings: ['PostgreSQL 16：用 postgres:16 同主版本 pg_dump/pg_restore'] },
      { service_id: 'a1b2c1d10a', name: 'redis', workload: 'statefulset/service-a1b2c1d10a', replicas: 1, role: 'cache', kind: 'redis', image: 'redis:7.2-alpine', image_id: 'sha256:1fd3a8b0c6e2', zeabur_registry: false, image_cached: true,
        ports: [{ name: 'redis', container_port: 6379, protocol: 'tcp' }], env: [], volumes: [zv('redis-data', '/data', 3.1e6, NS1)], domains: [],
        status: 'degraded', data_store: 'redis', k8s_services: ['service-a1b2c1d10a'],
        warnings: ['未开启 AOF：只有最后一次 RDB 快照会被迁移，快照之后写入的键可能丢失', '最近 1 小时重启 3 次（OOMKilled）'] },
    ], warnings: ['孤立 PVC acme-api-uploads-old（112 MB）不属于任何服务，不会迁移；需要的话请先手动备份', 'postgresql 的 PVC 使用 local-path（hostPath），删除 k3s 会一并删除，切换后请勿提前清理'] },
    { namespace: NS2, project_id: 'e5f6a7b8c9d0b', project_name: 'LinkProxy', environment_name: 'production', services: [
      { service_id: 'e5f6aa0b31', name: 'linkproxy', workload: 'deployment/service-e5f6aa0b31', replicas: 1, role: 'app', kind: 'image', image: 'example/link-proxy:latest', image_id: 'sha256:c3e90f12aa47', zeabur_registry: false, image_cached: true,
        ports: [{ name: 'web', container_port: 8317, protocol: 'http' }], node_ports: [30817], env: [{ key: 'MANAGEMENT_BASE', value: 'https://linkproxy-demo.zeabur.app/v0', secret: false, exposed: false }], volumes: [zv('auths', '/root/.link-proxy', 2.4e6, NS2)], domains: ['linkproxy-demo.zeabur.app'],
        status: 'running', data_store: '',
        warnings: ['latest 标签：将固定到正在运行的摘要 sha256:c3e90f12aa47', 'NodePort 30817 迁移后改为 Caddy 反代'] },
      { service_id: 'e5f6aa0c88', name: 'config-editor', workload: '', replicas: 0, role: 'other', kind: 'image', image: 'registry.zeabur.cloud/e5f6a7b8/config-editor:1a2b3c4', image_id: '', zeabur_registry: true, image_cached: false,
        ports: [], env: [], volumes: [zv('config', '/data', 0.2e6, NS2)], domains: [], status: 'suspended',
        references: [{ target: 'e5f6aa0b31', via: 'shared-configmap', object: 'ConfigMap/linkproxy-config', inferred: false }],
        warnings: ['服务已暂停，本机没有镜像缓存，只能迁移数据卷'] },
    ], warnings: ['deployment/debug-shell 不是 Zeabur 服务，已忽略'] },
  ],
}
const LOCAL_STEPS = ['导出 Zeabur 构建的镜像', '拉取公共镜像（固定到运行中的摘要）', '复制数据卷', '导出并恢复数据库', '启动 Atoll 容器（不接公网）', '校验数据行数', '缩容 Zeabur 应用', '最终数据同步', '停用 Zeabur 入口', '启用 atoll-caddy 接管 80/443']
const VERIFY: ImportVerify[] = [
  { service: 'postgresql', table: 'public.tokens', source_rows: 1284, target_rows: 1284 },
  { service: 'postgresql', table: 'public.request_logs', source_rows: 98213, target_rows: 98213 },
  { service: 'postgresql', table: 'public.users', source_rows: 3, target_rows: 3 },
  { service: 'postgresql', table: 'public.settings', source_rows: 27, target_rows: 27 },
  { service: 'redis', table: 'db0 (keys)', source_rows: 412, target_rows: 412 },
]
const DETAIL: Record<number, string> = { 0: '1 个镜像 · registry.zeabur.cloud/…/acme-api → atoll-import/acme-api:4be1c09a7d2f', 1: 'postgres:16-alpine@sha256:9a07e3…, redis:7.2-alpine@sha256:1fd3a8…', 2: '3 个卷 · 226 MB', 3: 'pg_dump 16.4 → pg_restore · 4 张表', 4: '3/3 运行中 · 仅项目内网' }
const localSteps = (done: number, running = -1): ImportStep[] => LOCAL_STEPS.map((name, i) => ({ name, status: i < done ? 'done' : i === running ? 'running' : 'pending', detail: i < done || i === running ? DETAIL[i] ?? (i === 5 && i < done ? '5/5 一致' : undefined) : undefined }))
const acmePlan = (): ImportPlan => ({ source: 'zeabur', source_project: 'a1b2c3d4e5f6a', project_name: 'acme-api', target_server_id: 'phx', warnings: ['acme-api.zeabur.app 切换后失效，已映射到 acme-api.example.com'],
  services: zeabur.projects[0].services.map(z => ({ source_service_id: z.service_id, name: z.name, kind: z.kind, image: z.image, image_needs_push: false, ports: z.ports, volumes: z.volumes.map(v => ({ name: v.pvc, mount_path: v.mount_path })), env: [], domains: z.domains.map(d => d.replace('.zeabur.app', '.example.com')), data_bytes: z.volumes.reduce((a, v) => a + v.bytes, 0) })) })
const localJob = (id: string, status: string, steps: ImportStep[], verify: ImportVerify[], ago: number): ImportJob =>
  ({ id, mode: 'zeabur-local', server_id: 'phx', namespace: NS1, project_id: 'acme', status, steps, verify, created_at: iso(ago), plan: acmePlan() })
const started: Record<string, number> = {}
function tickLocal(j: ImportJob) {
  const t0 = started[j.id]; if (t0 === undefined) return
  const k = Math.floor((Date.now() - t0) / 1800)
  if (j.status === 'rehearsing') {
    const done = Math.min(6, k); j.steps = localSteps(done, done < 6 ? done : -1)
    j.verify = done >= 5 ? VERIFY : done >= 3 ? VERIFY.map((v, i) => ({ ...v, target_rows: i === 1 ? Math.round(v.source_rows * 0.6) : v.target_rows })) : []
    if (done >= 6) j.status = 'rehearsed'
  } else if (j.status === 'cutting_over') {
    const done = Math.min(10, 6 + k); j.steps = localSteps(done, done < 10 ? done : -1)
    if (done >= 10) j.status = 'live'
  } else if (j.status === 'rolling_back' && k >= 2) { j.status = 'rolled_back'; j.steps = localSteps(6) }
  else if (j.status === 'cleaning' && k >= 3) { j.status = 'cleaned'; zeabur.k3s = false }
}

imports.unshift(
  localJob('imp_zl_rehearse', 'rehearsing', localSteps(3, 3), VERIFY.map((v, i) => ({ ...v, target_rows: i === 1 ? 61022 : i === 4 ? 0 : v.target_rows })), 3 * 60e3),
  localJob('imp_zl_ready', 'rehearsed', localSteps(6), VERIFY, 12 * 60e3),
  localJob('imp_zl_live', 'live', localSteps(10), VERIFY, 2 * H),
)

// ---------- fake container filesystem for exec ----------
const CONFIG = `# mail-relay 配置
server:
  host: 0.0.0.0
  port: 5000
  public_url: https://mail.example.com

database:
  path: /app/data/mail.db

scheduler:
  forward_interval: 300   # 秒
  timezone: Asia/Shanghai

forward:
  channels: []            # telegram / webhook

log:
  level: INFO
  dir: /app/logs`
const FS: Record<string, string[] | string> = {
  '/': ['app/', 'bin/', 'etc/', 'tmp/', 'usr/'],
  '/app': ['.env.example', '.github/', 'VERSION', 'data/', 'docker-compose.yml', 'docs/', 'static/', 'main.py', 'config.yaml', 'requirements.txt', 'scripts/', 'start.sh', 'templates/', 'logs/'],
  '/app/data': ['mail.db', 'accounts.json'], '/app/logs': ['app.log'], '/app/.github': ['workflows/'], '/app/docs': ['README.md'],
  '/app/static': ['app.js', 'style.css'], '/app/scripts': ['migrate.sh'], '/app/templates': ['index.html', 'login.html'],
  '/app/config.yaml': CONFIG, '/app/VERSION': 'v2.3.1\n', '/app/.env.example': 'SECRET_KEY=\nADMIN_PASSWORD=\n', '/app/start.sh': '#!/bin/sh\nexec python main.py\n',
  '/app/requirements.txt': 'flask==3.1.0\napscheduler==3.11.0\nrequests==2.32.3\n', '/app/main.py': 'from app import create_app\n\napp = create_app()\n',
}
function exec(cmd: string[]): { ok: boolean; output?: string; error?: string } {
  const script = cmd[0] === 'sh' && cmd[1] === '-c' ? cmd[2] : cmd.join(' ')
  if (script === 'pwd') return { ok: true, output: '/app\n' }
  const target = cmd[cmd.length - 1]
  if (script.startsWith('ls')) {
    const d = FS[target.replace(/\/$/, '') || '/']
    return Array.isArray(d) ? { ok: true, output: d.join('\n') + '\n' } : Array.isArray(FS[target]) ? { ok: true, output: (FS[target] as string[]).join('\n') } : { ok: true, output: '' }
  }
  if (script.startsWith('cat') || script.startsWith('head')) {
    const f = FS[target]
    return typeof f === 'string' ? { ok: true, output: f } : { ok: true, output: `# ${target}\n` }
  }
  if (script.startsWith('printf')) {
    const path = cmd[5], dir = path.replace(/\/[^/]+$/, '') || '/', name = path.slice(dir.length + (dir === '/' ? 0 : 1))
    if (!(path in FS) && Array.isArray(FS[dir])) (FS[dir] as string[]).push(name)
    FS[path] = cmd[4]; return { ok: true, output: '' }
  }
  if (cmd[0] === 'mkdir') {
    const path = target.replace(/\/$/, ''), dir = path.replace(/\/[^/]+$/, '') || '/', name = path.slice(dir.length + (dir === '/' ? 0 : 1))
    if (!(path in FS)) { FS[path] = []; if (Array.isArray(FS[dir])) (FS[dir] as string[]).push(name + '/') }
    return { ok: true, output: '' }
  }
  return { ok: true, output: `$ ${cmd.join(' ')}\n(demo) ok\n` }
}

// ---------- router ----------
const demoUpdates = makeDemoUpdates(services, deployments)
const demoBackups = makeDemoBackups(services, servers, pid => projects.find(p => p.id === pid)?.server_id ?? '', settings)
const svc = (id: string) => services.find(s => s.id === id) ?? fail(404, '服务不存在')
const withSvc = (s: Service): Service => ({ ...s, domains: domains.filter(d => d.service_id === s.id) })
const fullProject = (p: Project): Project => ({ ...p, services: services.filter(s => s.project_id === p.id).map(withSvc) })
const DEMO_SECRET = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP'
const demo2fa = { enabled: false, at: '', left: 10 }
const demoCodes = () => ['k7m2q-x9tpw', 'h4rcz-m8vne', 'p3dqs-w7kua', 'y9bft-c2hrm', 'n6gxe-t4jqp', 'r2wmk-z8cdv', 'e5ujn-q3fyb', 'a8tpc-v6sgh', 'w4hzr-k9nem', 'f7qdy-b3xut']
const demoSessions: SessionInfo[] = [
  { id: '3f9a1c0d7e2b4a65', ip: '203.0.113.24', user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 Safari/605.1.15', current: true, created_at: iso(2 * H), expires_at: new Date(now + 30 * D).toISOString() },
  { id: 'b81e4f2a90c37d16', ip: '198.51.100.7', user_agent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Mobile/15E148', current: false, created_at: iso(3 * D), expires_at: new Date(now + 27 * D).toISOString() },
]
const ALL = ['read', 'deploy', 'delete', 'migrate', 'domains', 'restore', 'servers', 'settings', 'tokens']
/** mirrors the server: read always on, global perms rejected with a project scope, legacy scope summary */
function tokenPerms(b: { scope?: string; permissions?: string[]; projects?: string[]; require_approval?: boolean }) {
  let perms = b.permissions?.length ? b.permissions : b.scope === 'admin' ? ALL : b.scope === 'deploy' ? ['read', 'deploy'] : ['read']
  perms = ALL.filter(p => p === 'read' || perms.includes(p))
  const projects = b.projects ?? []
  if (projects.length && perms.some(p => ['servers', 'settings', 'tokens'].includes(p))) fail(400, 'servers/settings/tokens permissions cannot be combined with a project-scoped token')
  const scope = perms.length === ALL.length ? 'admin' : perms.includes('deploy') ? 'deploy' : 'read'
  return { permissions: perms, projects, require_approval: !!b.require_approval, scope }
}
function fail(status: number, msg: string): never { throw new ApiError(status, msg) }
const clone = <T,>(x: T): T => JSON.parse(JSON.stringify(x))

function metrics(seed: number, base: number, amp: number, range: string) {
  const n = range === '1h' ? 60 : 48, step = (range === '1h' ? H : range === '7d' ? 7 * D : D) / n
  return series(n, base, amp, seed).map((v, i) => ({ at: new Date(now - (n - 1 - i) * step).toISOString(), v }))
}

function route(method: string, path: string, body: any): unknown {
  const [p, qs] = path.split('?'); const q = new URLSearchParams(qs)
  const seg = p.split('/').filter(Boolean)
  const m = (pattern: string) => {
    const ps = pattern.split('/').filter(Boolean)
    if (ps.length !== seg.length) return null
    const out: string[] = []
    for (let i = 0; i < ps.length; i++) { if (ps[i] === '*') out.push(decodeURIComponent(seg[i])); else if (ps[i] !== seg[i]) return null }
    return out
  }
  const G = method === 'GET', P = method === 'POST', U = method === 'PUT', X = method === 'PATCH', DEL = method === 'DELETE'
  let a: string[] | null

  if (m('auth/status')) return { setup_required: location.hash.includes('setup=1') }
  // demo 2FA: `#/login?demo=1&2fa=1` (or enabling it in Settings → Security) makes login ask for a code; any 6 digits pass
  if (P && m('auth/login') && (demo2fa.enabled || location.hash.includes('2fa=1'))) return { two_factor_required: true, pending_token: 'demo-pending', expires_at: new Date(now + 5 * 60e3).toISOString() }
  if (P && m('auth/login/2fa')) { if (!/^\d{6}$|^[a-z2-9]{5}-?[a-z2-9]{5}$/i.test(String(body?.code ?? '').trim())) fail(401, 'invalid verification code'); return { id: 'usr_demo', username: 'demo' } }
  if (m('auth/me') || m('auth/login') || m('auth/setup')) return { id: 'usr_demo', username: body?.username || 'demo' }
  if (m('auth/logout')) return {}
  if (G && m('auth/2fa')) return { enabled: demo2fa.enabled, enabled_at: demo2fa.enabled ? demo2fa.at : undefined, recovery_codes_left: demo2fa.enabled ? demo2fa.left : 0 }
  if (P && m('auth/2fa/enroll')) { if (!body?.password) fail(401, 'wrong password'); return { secret: DEMO_SECRET, otpauth_uri: `otpauth://totp/Atoll:demo@${location.host}?secret=${DEMO_SECRET}&issuer=Atoll&algorithm=SHA1&digits=6&period=30` } }
  if (P && m('auth/2fa/enable')) { if (!/^\d{6}$/.test(String(body?.code ?? '').trim())) fail(401, 'invalid verification code'); Object.assign(demo2fa, { enabled: true, at: new Date().toISOString(), left: 10 }); return { recovery_codes: demoCodes() } }
  if (P && m('auth/2fa/disable')) { if (!body?.password || !body?.code) fail(401, 'wrong password or code'); demo2fa.enabled = false; return {} }
  if (P && m('auth/2fa/recovery-codes')) { if (!body?.password || !body?.code) fail(401, 'wrong password or code'); demo2fa.left = 10; return { recovery_codes: demoCodes() } }
  if (G && m('auth/sessions')) return demoSessions
  if (P && m('auth/sessions/revoke-others')) { const n = demoSessions.length - 1; demoSessions.splice(0, demoSessions.length, ...demoSessions.filter(x => x.current)); return { revoked: n } }
  if (DEL && (a = m('auth/sessions/*'))) { const i = demoSessions.findIndex(x => x.id === a![0]); if (i >= 0) demoSessions.splice(i, 1); return {} }

  if (G && m('servers')) return servers
  if (P && m('servers/join-token')) return { token: 'atl_join_7Hq2xV9mK4pR', command: `curl -fsSL ${settings.public_url}/install.sh | sudo sh -s -- --server ${settings.public_url} --join atl_join_7Hq2xV9mK4pR`, expires_at: new Date(now + H).toISOString() }
  if ((a = m('servers/*/metrics'))) {
    const s = servers.find(x => x.id === a![0])!; const u = s.usage!
    const c = metrics(1, u.cpu_pct, u.cpu_pct * 0.4, q.get('range') ?? '24h'), mm = metrics(2, u.mem_used_mb, u.mem_total_mb * 0.04, q.get('range') ?? '24h'), dd = metrics(3, u.disk_used_gb, u.disk_total_gb * 0.006, q.get('range') ?? '24h')
    return c.map((x, i): NodeMetrics => ({ ...u, at: x.at, cpu_pct: Math.min(100, x.v), mem_used_mb: Math.min(u.mem_total_mb, mm[i].v), disk_used_gb: dd[i].v }))
  }
  if (P && (a = m('servers/*/network'))) {
    const s = servers.find(x => x.id === a![0]) ?? fail(404, '服务器不存在')
    if (s.status !== 'online') fail(409, 'server is offline')
    if (body.action !== 'public' && settings.network_provider !== 'tailscale') fail(409, 'enable the Tailscale network provider in Settings first')
    const pub = settings.public_url, cu = settings.tailnet_control_url || 'http://100.64.0.4:7301'
    if (body.action === 'public') s.network = { provider: 'tailscale', state: 'running', via_tailnet: false, control_url: pub, fallback_urls: [] }
    else { s.tailnet_ip = s.tailnet_ip || `100.64.0.${30 + servers.indexOf(s)}`; s.network = { provider: 'tailscale', state: 'running', via_tailnet: true, control_url: cu, fallback_urls: [pub] } }
    return { switched: true, control_url: s.network.control_url, tailnet_ip: s.tailnet_ip, state: 'running', message: body.action === 'join' ? '(demo) Tailscale 已安装并加入 tailnet' : undefined }
  }
  if ((a = m('servers/*/zeabur'))) { if (a[0] !== 'phx') fail(404, '这台服务器上没有检测到 Zeabur（k3s 未安装）'); return { ...zeabur, scanned_at: new Date().toISOString() } }
  if ((a = m('servers/*/containers'))) {
    const pids = projects.filter(p => p.server_id === a![0]).map(p => p.id)
    return services.filter(s => pids.includes(s.project_id)).map((s): ContainerState => ({ service_id: s.id, deployment_id: deployments[s.id]?.[0]?.id ?? '', state: s.status === 'running' ? 'running' : s.status === 'failed' ? 'restarting' : 'exited', restart_count: s.status === 'failed' ? 14 : 0, started_at: iso(2 * D) }))
  }
  if ((a = m('servers/*'))) {
    const i = servers.findIndex(x => x.id === a![0]); if (i < 0) fail(404, '服务器不存在')
    if (X) Object.assign(servers[i], body)
    if (DEL) { servers.splice(i, 1); return {} }
    return servers[i]
  }

  if (G && m('projects')) return projects.map(fullProject)
  if (P && m('projects')) { const pr: Project = { id: nid('prj'), name: body.name, server_id: body.server_id, description: body.description, created_at: new Date().toISOString() }; projects.push(pr); return fullProject(pr) }
  if ((a = m('projects/*/services'))) {
    if (G) return services.filter(s => s.project_id === a![0]).map(withSvc)
    const t = templates.find(x => x.kind === body.kind)
    const s: Service = { id: nid('svc'), project_id: a[0], server_id: body.server_id || projects.find(x => x.id === a![0])?.server_id, name: body.name, kind: body.kind, image: body.image || t?.image || '', ports: body.ports ?? [], volumes: body.volumes ?? (VOLS[body.kind] ?? []).map(([name, mount_path]) => ({ name, mount_path })), status: 'deploying', replicas: '0/1', created_at: new Date().toISOString() }
    services.push(s); env[s.id] = []; deployments[s.id] = [{ id: nid('dep'), service_id: s.id, server_id: '', status: 'pulling', trigger: 'create', started_at: new Date().toISOString() }]
    return withSvc(s)
  }
  if ((a = m('projects/*/migrate'))) { const pr = projects.find(x => x.id === a![0])!; pr.server_id = body.server_id; services.filter(x => x.project_id === pr.id).forEach(x => { x.server_id = body.server_id }); return {} }
  if ((a = m('projects/*'))) {
    const pr = projects.find(x => x.id === a![0]) ?? fail(404, '项目不存在')
    if (X) Object.assign(pr, body)
    if (DEL) { projects.splice(projects.indexOf(pr), 1); return {} }
    return fullProject(pr)
  }

  { const r = demoUpdates(method, seg, body); if (r !== undefined) return r }
  { const r = demoBackups.route(method, seg, body); if (r !== undefined) return r }
  if ((a = m('services/*/actions'))) { const s = svc(a[0]); s.status = body.action === 'stop' ? 'stopped' : 'running'; s.replicas = body.action === 'stop' ? '0/1' : '1/1'; return {} }
  if ((a = m('services/*/rollback'))) return {}
  if ((a = m('services/*/env/references'))) {
    const s = svc(a[0]); const out: EnvReference[] = []
    services.filter(x => x.project_id === s.project_id && x.id !== s.id).forEach(x => env[x.id].filter(v => v.exposed).forEach(v => out.push({ ...v, value: v.secret ? '' : v.value, service: x.name })))
    return out
  }
  if ((a = m('services/*/env'))) {
    if (U) { const old = env[a[0]]; env[a[0]] = (body as EnvVar[]).map(v => v.secret && !v.value ? { ...v, value: old.find(o => o.key === v.key)?.value ?? '' } : v); }
    return env[a[0]].map(v => v.secret && q.get('reveal') !== '1' ? { ...v, value: '' } : v)
  }
  if ((a = m('services/*/domains'))) {
    if (G) return domains.filter(d => d.service_id === a![0])
    const d: Domain = { id: nid('dom'), service_id: a[0], hostname: body.hostname, port_name: body.port_name, proxied: body.proxied, status: 'provisioning', created_at: new Date().toISOString() }
    domains.push(d); setTimeout(() => { d.status = 'active' }, 8000); return d
  }
  if ((a = m('services/*/deployments'))) return deployments[a[0]] ?? []
  if ((a = m('services/*/metrics'))) {
    const r = q.get('range') ?? '24h', c = metrics(1, 3, 1.6, r), mem = metrics(2, 180, 14, r), net = metrics(3, 1.1e6, 0.8e6, r)
    return c.map((x, i): ContainerMetrics => ({ service_id: a![0], at: x.at, cpu_pct: x.v, mem_mb: mem[i].v, net_rx_bps: net[i].v * 0.6, net_tx_bps: net[i].v * 0.4 }))
  }
  if (P && (a = m('services/*/migrate'))) {
    const s = svc(a[0]); const from = s.server_id ?? ''
    if (!servers.some(x => x.id === body.server_id)) fail(400, `unknown target server_id "${body.server_id}"`)
    if (from === body.server_id) fail(400, 'service already runs there')
    s.server_id = body.server_id
    return { id: nid('mig'), project_id: s.project_id, service_id: s.id, from_server_id: from, to_server_id: body.server_id, status: 'running', steps: [{ name: '停止服务', status: 'done' }, { name: '复制数据卷', status: 'running' }], created_at: new Date().toISOString() }
  }
  if ((a = m('services/*/exec'))) return exec(body.cmd)
  if ((a = m('services/*'))) {
    const s = svc(a[0])
    if (X) { Object.assign(s, body); s.status = 'deploying' }
    if (DEL) { services.splice(services.indexOf(s), 1); return {} }
    return withSvc(s)
  }
  if (G && m('domains')) return domains
  if ((a = m('domains/*'))) { const i = domains.findIndex(d => d.id === a![0]); if (i >= 0) domains.splice(i, 1); return {} }

  if (G && m('tokens')) return tokens
  if (P && m('tokens')) { const t: APIToken = { id: nid('tok'), name: body.name, ...tokenPerms(body), prefix: 'atl_pat_' + Math.random().toString(36).slice(2, 6), created_at: new Date().toISOString() }; tokens.unshift(t); return { ...t, token: t.prefix + 'Xk2Vb8NqR4mZt7Lw1PcY9sHd' } }
  if (X && (a = m('tokens/*'))) { const t = tokens.find(x => x.id === a![0]) ?? fail(404, 'token not found'); Object.assign(t, tokenPerms({ ...t, ...body })); if (body.name) t.name = body.name; return t }
  if ((a = m('tokens/*'))) { const i = tokens.findIndex(t => t.id === a![0]); tokens.splice(i, 1); return {} }

  if (G && m('confirmations')) return confirmations.filter(c => !q.get('status') || c.status === q.get('status'))
  if ((a = m('confirmations/*/approve')) || (a = m('confirmations/*/reject'))) { const c = confirmations.find(x => x.id === a![0])!; c.status = seg[2] === 'approve' ? 'approved' : 'rejected'; return {} }

  if (G && m('settings') && location.hash.includes('nocf=1')) return { ...settings, base_domain: '', has_cloudflare_token: false }
  if (m('settings')) {
    if (U) {
      const { tailscale_auth_key: tsKey, clear_tailscale_auth_key: tsClear, network_provider: np, tailnet_name: tn, tailnet_control_url: tcu, tailnet_listen: _tl, tailnet_listen_error: _tle, agent_public: _ap, has_tailscale_auth_key: _hts, cloudflare_token, backup_access_key: _ak, backup_secret_key: _sk, backup_repo_password: _pw, backup_endpoint: _e, backup_bucket: _b, backup_prefix: _p, backup_region: _r, has_backup_credentials: _h1, has_backup_password: _h2, backup_configured: _c, ...rest } = body as Settings
      Object.assign(settings, rest); if (cloudflare_token) settings.has_cloudflare_token = true
      if (tsKey) { if (!tsKey.startsWith('tskey-')) fail(400, 'tailscale_auth_key must be a Tailscale auth key (tskey-auth-...)'); settings.has_tailscale_auth_key = true } else if (tsClear) settings.has_tailscale_auth_key = false
      if (np === 'none') settings.network_provider = ''
      else if (np === 'tailscale') { if (!settings.has_tailscale_auth_key) fail(400, 'tailscale needs an auth key'); settings.network_provider = 'tailscale' }
      if (np || tn) settings.tailnet_name = tn ?? ''
      if (np || tcu) settings.tailnet_control_url = tcu || (settings.tailnet_listen ? `http://${settings.tailnet_listen}` : '')
      const generated = demoBackups.putSettings(body)
      return generated ? { ...settings, backup_repo_password: generated } : settings
    }
    return settings
  }
  if (G && m('templates')) return templates
  if (G && m('imports')) { imports.forEach(tickLocal); return imports }
  if (P && m('imports')) { const j: ImportJob = { id: nid('imp'), plan: body, status: 'planned', created_at: new Date().toISOString(), steps: STEPS.map(name => ({ name, status: 'pending' })) }; imports.unshift(j); return j }
  if (P && m('imports/zeabur-local')) {
    const zp = zeabur.projects.find(x => x.namespace === body.namespace) ?? fail(404, '找不到 Zeabur 项目')
    const pick = zp.services.filter(z => !body.service_ids?.length || body.service_ids.includes(z.service_id))
    const j = localJob(nid('imp'), 'planned', localSteps(0), [], 0)
    j.created_at = new Date().toISOString(); j.namespace = zp.namespace; j.server_id = body.server_id; j.project_id = undefined
    j.plan = { ...acmePlan(), project_name: body.project_name, source_project: zp.project_id, services: acmePlan().services.filter(x => pick.some(z => z.service_id === x.source_service_id)) }
    const dm: Record<string, string> = body.domain_map ?? {}
    j.plan.services.forEach(x => { const z = pick.find(p => p.service_id === x.source_service_id)!; x.domains = z.domains.map(d => d in dm ? dm[d] : d).filter(Boolean) })
    imports.unshift(j); return j
  }
  if ((a = m('imports/*/cleanup'))) {
    const j = imports.find(x => x.id === a![0]) ?? fail(404, '导入任务不存在')
    const srv = servers.find(x => x.id === j.server_id)
    if (body?.confirm !== srv?.name) fail(400, '确认名称与服务器名称不一致')
    j.status = 'cleaning'; started[j.id] = Date.now(); return j
  }
  if ((a = m('imports/*/rehearse')) || (a = m('imports/*/cutover')) || (a = m('imports/*/rollback'))) {
    const j = imports.find(x => x.id === a![0]) ?? fail(404, '导入任务不存在')
    j.status = { rehearse: 'rehearsing', cutover: 'cutting_over', rollback: 'rolling_back' }[seg[2]]!; started[j.id] = Date.now(); tickLocal(j); return j
  }
  if ((a = m('imports/*/start'))) { const j = imports.find(x => x.id === a![0])!; j.status = 'deploying'; j.steps[0].status = 'done'; j.steps[1].status = 'running'; return j }
  if ((a = m('imports/*'))) { const j = imports.find(x => x.id === a![0]) ?? fail(404, '导入任务不存在'); tickLocal(j); return j }
  fail(404, `demo: 未实现 ${method} ${path}`)
}

export async function demoRequest<T>(method: string, path: string, body?: unknown): Promise<T> {
  await new Promise(r => setTimeout(r, 60))
  return clone(route(method, path, body)) as T
}

const LOGS: [string, string][] = [
  ['stdout', '[forward] skip job: no active channels configured'],
  ['stdout', '2026-10-29 21:11:07,454 INFO [apscheduler.executors.default] Job "邮件转发轮询 (trigger: interval[0:05:00])" executed successfully'],
  ['stdout', '2026-10-29 21:16:07,452 INFO [apscheduler.executors.default] Running job "邮件转发轮询 (trigger: interval[0:05:00], next run at: 21:21:07 CST)"'],
  ['stdout', '[forward] skip job: no active channels configured'],
  ['stderr', 'WARN token refresh for account #3 took 4.2s (threshold 3s)'],
  ['stdout', '104.28.158.90 - - [29/Oct/2026:21:21:01 +0800] "GET / HTTP/1.1" 302 199 "-" "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36"'],
  ['stdout', '104.28.158.90 - - [29/Oct/2026:21:21:01 +0800] "GET /login HTTP/1.1" 200 8141 "-" "Mozilla/5.0 (X11; Linux x86_64)"'],
  ['stderr', 'ERROR 104.28.158.90 - - [29/Oct/2026:21:21:02 +0800] "GET /favicon.ico HTTP/1.1" 404 240 "https://mail.example.com/login"'],
  ['stdout', '2026-10-29 21:21:07,452 INFO [apscheduler.executors.default] Running job "邮件转发轮询 (trigger: interval[0:05:00], next run at: 21:26:07 CST)"'],
  ['stdout', '[forward] skip job: no active channels configured'],
  ['stdout', '2026-10-29 21:21:07,453 INFO [apscheduler.executors.default] Job "邮件转发轮询 (trigger: interval[0:05:00])" executed successfully'],
  ['stdout', '2026-10-29 21:26:07,451 INFO [apscheduler.executors.default] Running job "邮件转发轮询"'],
]
export function demoLogStream(serviceId: string, push: (l: LogLine) => void, onState: (open: boolean) => void) {
  onState(true)
  // backfill: spread history over the last 4h so the histogram has shape
  for (let i = 0; i < 160; i++) {
    const [stream, line] = LOGS[i % LOGS.length]
    push({ service_id: serviceId, at: new Date(now - (160 - i) * 90e3 - (i % 7) * 20e3).toISOString(), stream, line })
  }
  let i = 0
  const t = setInterval(() => { const [stream, line] = LOGS[i++ % LOGS.length]; push({ service_id: serviceId, at: new Date().toISOString(), stream, line }) }, 4000)
  return () => { clearInterval(t); onState(false) }
}
