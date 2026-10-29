// Demo backend: serves the original mock data through the same REST paths the real client uses.
// Enabled with ?demo=1 or VITE_DEMO=1. Mutations live in memory for the tab.
import { projects as mockProjects, servers as mockServers } from '../mock'
import { series } from '../components/ui'
import { ApiError } from './client'
import type {
  APIToken, Confirmation, ContainerMetrics, ContainerState, Deployment, Domain, EnvReference, EnvVar, ImportJob, ImportPlan,
  LogLine, NodeMetrics, Project, Server, Service, Settings, Template,
} from './types'

const now = Date.now()
const iso = (msAgo: number) => new Date(now - msAgo).toISOString()
const H = 3600e3, D = 24 * H
let seq = 1000
const nid = (p: string) => `${p}_demo${(seq++).toString(36)}`

const servers: Server[] = mockServers.map(s => ({
  id: s.id, name: s.name, role: s.role ?? 'worker', status: s.status, public_ipv4: s.ip, provider: s.provider, region: s.region,
  tailnet_host: `${s.id}.tail3c1a.ts.net`, os: s.os, arch: s.arch, cpu_cores: s.cpu, mem_mb: s.memGB * 1024, disk_gb: s.diskGB,
  agent_version: 'v0.1.0', last_seen_at: iso(s.status === 'online' ? 8e3 : 12 * 60e3), created_at: '2026-10-12T08:00:00Z',
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
      id: s.id, project_id: p.id, name: s.name, kind: s.kind, image: `${s.image}:${s.tag}`, status: s.status, replicas: s.replicas,
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
// image services in projects with a DB reference it
for (const p of projects) {
  const ss = services.filter(s => s.project_id === p.id), db = ss.find(s => s.kind !== 'image')
  if (db) ss.filter(s => s.kind === 'image').forEach(s => env[s.id].splice(2, 0, { key: 'DATABASE_URL', value: `\${${db.kind.toUpperCase()}_CONNECTION_STRING}`, secret: false, exposed: false }))
}

const tokens: APIToken[] = [
  { id: 'tok_demo1', name: 'claude-code-macbook', scope: 'admin', prefix: 'atl_pat_3fA9', last_used_at: iso(2 * 60e3), created_at: iso(9 * D) },
  { id: 'tok_demo2', name: 'codex-ci', scope: 'deploy', prefix: 'atl_pat_k21Q', last_used_at: iso(D), created_at: iso(6 * D) },
  { id: 'tok_demo3', name: '只读监控', scope: 'read', prefix: 'atl_pat_Zx07', last_used_at: iso(5 * D), created_at: iso(12 * D) },
]
const confirmations: Confirmation[] = [
  { id: 'cfm_demo1', action: 'service.delete', target: 'cron-worker', summary: '删除服务 LinkProxy / cron-worker（容器、2 个卷）', status: 'pending', created_by: 'codex-ci', created_at: iso(4 * 60e3) },
  { id: 'cfm_demo2', action: 'domain.add', target: 'api.example.com', summary: '为 acme-api / acme-api 绑定 api.example.com（Cloudflare 代理记录 → 203.0.113.10）', status: 'pending', created_by: 'claude-code-macbook', created_at: iso(18 * 60e3) },
  { id: 'cfm_demo3', action: 'service.migrate', target: 'file-share', summary: '把项目 file-share 从 Hetzner CAX21 Falkenstein 迁移到 Oracle ARM West Phoenix', status: 'approved', created_by: 'claude-code-macbook', created_at: iso(2 * D) },
  { id: 'cfm_demo4', action: 'project.delete', target: 'old-blog', summary: '删除项目 old-blog（3 个服务）', status: 'rejected', created_by: 'codex-ci', created_at: iso(4 * D) },
]
const settings: Settings = { base_domain: 'example.com', cloudflare_zone_id: '9f2c4b7a1e0d43c58a6b2f1e7d3c9a05', has_cloudflare_token: true, tls_mode: 'cloudflare-origin', public_url: 'https://panel.example.com' }
const templates: Template[] = [
  { kind: 'postgres', name: 'PostgreSQL', image: 'postgres:17-alpine', description: '自动生成密码，暴露 POSTGRES_CONNECTION_STRING' },
  { kind: 'mysql', name: 'MySQL', image: 'mysql:8.4', description: '自动生成 root 密码与连接串' },
  { kind: 'redis', name: 'Redis', image: 'redis:7-alpine', description: '带密码与持久化' },
  { kind: 'mongo', name: 'MongoDB', image: 'mongo:8.0', description: '自动生成 root 账号' },
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
  if (script.startsWith('printf')) { FS[cmd[4]] = cmd[3]; return { ok: true, output: '' } }
  return { ok: true, output: `$ ${cmd.join(' ')}\n(demo) ok\n` }
}

// ---------- router ----------
const svc = (id: string) => services.find(s => s.id === id) ?? fail(404, '服务不存在')
const withSvc = (s: Service): Service => ({ ...s, domains: domains.filter(d => d.service_id === s.id) })
const fullProject = (p: Project): Project => ({ ...p, services: services.filter(s => s.project_id === p.id).map(withSvc) })
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

  if (m('auth/status')) return { setup_required: false }
  if (m('auth/me') || m('auth/login') || m('auth/setup')) return { id: 'usr_demo', username: body?.username || 'demo' }
  if (m('auth/logout')) return {}

  if (G && m('servers')) return servers
  if (P && m('servers/join-token')) return { token: 'atl_join_7Hq2xV9mK4pR', command: `curl -fsSL ${settings.public_url}/install.sh | sudo sh -s -- --server ${settings.public_url} --join atl_join_7Hq2xV9mK4pR`, expires_at: new Date(now + H).toISOString() }
  if ((a = m('servers/*/metrics'))) {
    const s = servers.find(x => x.id === a![0])!; const u = s.usage!
    const c = metrics(1, u.cpu_pct, u.cpu_pct * 0.4, q.get('range') ?? '24h'), mm = metrics(2, u.mem_used_mb, u.mem_total_mb * 0.04, q.get('range') ?? '24h'), dd = metrics(3, u.disk_used_gb, u.disk_total_gb * 0.006, q.get('range') ?? '24h')
    return c.map((x, i): NodeMetrics => ({ ...u, at: x.at, cpu_pct: Math.min(100, x.v), mem_used_mb: Math.min(u.mem_total_mb, mm[i].v), disk_used_gb: dd[i].v }))
  }
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
    const s: Service = { id: nid('svc'), project_id: a[0], name: body.name, kind: body.kind, image: body.image || t?.image || '', ports: body.ports ?? [], volumes: body.volumes ?? (VOLS[body.kind] ?? []).map(([name, mount_path]) => ({ name, mount_path })), status: 'deploying', replicas: '0/1', created_at: new Date().toISOString() }
    services.push(s); env[s.id] = []; deployments[s.id] = [{ id: nid('dep'), service_id: s.id, server_id: '', status: 'pulling', trigger: 'create', started_at: new Date().toISOString() }]
    return withSvc(s)
  }
  if ((a = m('projects/*/migrate'))) { const pr = projects.find(x => x.id === a![0])!; pr.server_id = body.server_id; return {} }
  if ((a = m('projects/*'))) {
    const pr = projects.find(x => x.id === a![0]) ?? fail(404, '项目不存在')
    if (X) Object.assign(pr, body)
    if (DEL) { projects.splice(projects.indexOf(pr), 1); return {} }
    return fullProject(pr)
  }

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
    const d: Domain = { id: nid('dom'), service_id: a[0], hostname: body.hostname, port_name: body.port_name, proxied: body.proxied, status: 'pending', created_at: new Date().toISOString() }
    domains.push(d); return d
  }
  if ((a = m('services/*/deployments'))) return deployments[a[0]] ?? []
  if ((a = m('services/*/metrics'))) {
    const r = q.get('range') ?? '24h', c = metrics(1, 3, 1.6, r), mem = metrics(2, 180, 14, r), net = metrics(3, 1.1e6, 0.8e6, r)
    return c.map((x, i): ContainerMetrics => ({ service_id: a![0], at: x.at, cpu_pct: x.v, mem_mb: mem[i].v, net_rx_bps: net[i].v * 0.6, net_tx_bps: net[i].v * 0.4 }))
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
  if (P && m('tokens')) { const t: APIToken = { id: nid('tok'), name: body.name, scope: body.scope, prefix: 'atl_pat_' + Math.random().toString(36).slice(2, 6), created_at: new Date().toISOString() }; tokens.unshift(t); return { ...t, token: t.prefix + 'Xk2Vb8NqR4mZt7Lw1PcY9sHd' } }
  if ((a = m('tokens/*'))) { const i = tokens.findIndex(t => t.id === a![0]); tokens.splice(i, 1); return {} }

  if (G && m('confirmations')) return confirmations.filter(c => !q.get('status') || c.status === q.get('status'))
  if ((a = m('confirmations/*/approve')) || (a = m('confirmations/*/reject'))) { const c = confirmations.find(x => x.id === a![0])!; c.status = seg[2] === 'approve' ? 'approved' : 'rejected'; return {} }

  if (m('settings')) { if (U) { const { cloudflare_token, ...rest } = body as Settings; Object.assign(settings, rest); if (cloudflare_token) settings.has_cloudflare_token = true } return settings }
  if (G && m('templates')) return templates
  if (G && m('imports')) return imports
  if (P && m('imports')) { const j: ImportJob = { id: nid('imp'), plan: body, status: 'planned', created_at: new Date().toISOString(), steps: STEPS.map(name => ({ name, status: 'pending' })) }; imports.unshift(j); return j }
  if ((a = m('imports/*/start'))) { const j = imports.find(x => x.id === a![0])!; j.status = 'deploying'; j.steps[0].status = 'done'; j.steps[1].status = 'running'; return j }
  if ((a = m('imports/*'))) return imports.find(x => x.id === a![0]) ?? fail(404, '导入任务不存在')
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
