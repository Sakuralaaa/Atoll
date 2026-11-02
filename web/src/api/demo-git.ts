// Demo backend for builds from source: one demo service (acme-api) is built from GitHub; builds
// progress over a few seconds with a generated log. Paths mirror docs/api.md "Builds from source".
import type { Deployment, DeploymentLogLine, GitSource, GitWebhook, Service } from './types'

const SHA = ['3f2c9d1e8a7b4c60d5e1f2a3b4c5d6e7f8091a2b', 'b7e41c09d2f35a6e8c1b0d9f7a6e5d4c3b2a1908', '9a0d6e3b1c8f7e5d4c2b1a0f9e8d7c6b5a493827']
const LOG = (repo: string, branch: string, sha: string, df: string) => [
  `$ git fetch --depth=1 ${repo} ${branch}`,
  `From ${repo}`, ` * branch            ${branch}       -> FETCH_HEAD`, `HEAD is now at ${sha.slice(0, 7)} chore: bump dependencies`,
  `$ docker build -f ${df} -t atoll-build/svc:${sha.slice(0, 12)} .`,
  '#0 building with "default" instance using docker driver', '#1 [internal] load build definition from Dockerfile', '#1 transferring dockerfile: 512B done', '#1 DONE 0.0s',
  '#2 [internal] load metadata for docker.io/library/node:22-alpine', '#2 DONE 1.2s', '#3 [internal] load .dockerignore', '#3 DONE 0.0s',
  '#4 [1/5] FROM docker.io/library/node:22-alpine', '#4 CACHED', '#5 [2/5] WORKDIR /app', '#5 CACHED', '#6 [3/5] COPY package*.json ./', '#6 DONE 0.1s',
  '#7 [4/5] RUN npm ci --omit=dev', '#7 4.120 added 214 packages, and audited 215 packages in 4s', '#7 4.121 found 0 vulnerabilities', '#7 DONE 4.9s',
  '#8 [5/5] COPY . .', '#8 DONE 0.2s', '#9 exporting to image', '#9 exporting layers 0.6s done', `#9 naming to docker.io/atoll-build/svc:${sha.slice(0, 12)} done`, '#9 DONE 0.7s',
  'build finished, starting container',
]
const FAILED = ['$ docker build -f Dockerfile -t atoll-build/svc:b7e41c09d2f3 .', '#7 [4/5] RUN npm ci --omit=dev', '#7 2.311 npm ERR! code ERESOLVE', '#7 2.312 npm ERR! ERESOLVE unable to resolve dependency tree', '#7 ERROR: process "/bin/sh -c npm ci --omit=dev" did not complete successfully: exit code: 1', 'ERROR: failed to solve: exit code: 1']
const STEP = 350 // ms per demo log line

export function makeDemoGit(services: Service[], deployments: Record<string, Deployment[]>, nid: (p: string) => string) {
  const logs: Record<string, { lines: string[]; started: number; fail?: boolean }> = {}
  const secrets: Record<string, string> = {}
  const newSecret = () => Array.from({ length: 40 }, () => '0123456789abcdef'[Math.floor(Math.random() * 16)]).join('')
  const hook = (s: Service, secret = true): GitWebhook => ({ url: `https://panel.example.com/api/v1/hooks/github/${s.id}`, secret: secret ? secrets[s.id] : undefined, has_secret: true, content_type: 'application/json', events: 'push', branch: s.git?.branch || 'main' })

  // seed: acme-api is built from GitHub; its previous deploy was a failed webhook build
  const acme = services.find(s => s.id === 'acme-api')
  if (acme) {
    acme.git = { repo: 'https://github.com/example/acme-api', branch: 'main', context: '', dockerfile: 'Dockerfile', build_args: { NODE_ENV: 'production' }, has_token: true, last_commit: SHA[0] }
    acme.source = 'git'; acme.image = `atoll-build/acme-api:${SHA[0].slice(0, 12)}`
    secrets[acme.id] = newSecret()
    const ds = deployments[acme.id] ?? []
    ds.forEach((d, i) => { d.commit = SHA[i]; logs[d.id] = { lines: LOG(acme.git!.repo, 'main', SHA[i], 'Dockerfile'), started: 0 } })
    if (ds[1]) { Object.assign(ds[1], { status: 'failed', trigger: 'webhook', commit: undefined, image_digest: undefined, message: 'build failed: exit code: 1' }); logs[ds[1].id] = { lines: FAILED, started: 0, fail: true } }
    if (ds[0]) ds[0].trigger = 'webhook'
  }

  const startBuild = (s: Service, trigger: string) => {
    const sha = SHA[Math.floor(Math.random() * SHA.length)]
    const d: Deployment = { id: nid('dep'), service_id: s.id, server_id: s.server_id ?? '', status: 'building', trigger, started_at: new Date().toISOString() }
    ;(deployments[s.id] ??= []).forEach(x => { if (x.status === 'running') x.status = 'superseded' })
    deployments[s.id].unshift(d)
    logs[d.id] = { lines: LOG(s.git!.repo, s.git!.branch || 'main', sha, s.git!.dockerfile || 'Dockerfile'), started: Date.now() }
    ;(d as Deployment & { _sha?: string })._sha = sha
    s.status = 'deploying'
    return d
  }
  /** advance demo builds: building -> running once the whole log has been "printed" */
  const tick = (sid: string) => {
    const s = services.find(x => x.id === sid)
    for (const d of deployments[sid] ?? []) {
      const l = logs[d.id]
      if (d.status !== 'building' || !l) continue
      if (Date.now() - l.started >= l.lines.length * STEP) {
        const sha = (d as Deployment & { _sha?: string })._sha ?? SHA[0]
        Object.assign(d, { status: 'running', commit: sha, finished_at: new Date().toISOString(), image_digest: `sha256:${sha}` })
        if (s?.git) { s.git.last_commit = sha; s.image = `atoll-build/${s.id}:${sha.slice(0, 12)}`; s.status = 'running'; s.replicas = '1/1' }
      }
    }
  }
  const clean = (s: Service): Service => s.git ? { ...s, git: { ...s.git, token: undefined } } : s

  /** POST /projects/{id}/services with git: called by the demo create handler. */
  const create = (s: Service, git: Partial<GitSource>): Service => {
    s.git = { repo: git.repo ?? '', branch: git.branch || 'main', context: git.context ?? '', dockerfile: git.dockerfile || 'Dockerfile', build_args: git.build_args ?? {}, has_token: !!git.token }
    s.source = 'git'; s.image = ''
    secrets[s.id] = newSecret()
    deployments[s.id] = []
    startBuild(s, 'create')
    return { ...clean(s), webhook: hook(s) }
  }

  const route = (method: string, seg: string[], body: any, q: URLSearchParams): unknown => {
    const [r0, sid, r2, dep, r4] = seg
    if (r0 !== 'services' || !sid) return undefined
    const s = services.find(x => x.id === sid)
    if (!s) return undefined
    if (seg.length === 3 && r2 === 'webhook' && method === 'GET') return s.git ? hook(s) : undefined
    if (seg.length === 4 && r2 === 'webhook' && dep === 'rotate' && method === 'POST') { secrets[s.id] = newSecret(); return hook(s) }
    if (seg.length === 3 && r2 === 'deployments' && method === 'GET') { tick(sid); return undefined }
    if (seg.length === 5 && r2 === 'deployments' && r4 === 'logs') {
      tick(sid)
      const d = deployments[sid]?.find(x => x.id === dep)
      const l = logs[dep]
      const n = !l ? 0 : d?.status === 'building' ? Math.min(l.lines.length, Math.floor((Date.now() - l.started) / STEP)) : l.lines.length
      const base = new Date(d?.started_at ?? Date.now()).getTime()
      const lines: DeploymentLogLine[] = (l?.lines.slice(0, n) ?? []).map((line, i) => ({ seq: i + 1, at: new Date(base + i * STEP).toISOString(), line }))
      return { deployment_id: dep, status: d?.status ?? 'failed', lines: lines.filter(x => x.seq > Number(q.get('after') ?? 0)) }
    }
    if (seg.length === 3 && r2 === 'actions' && method === 'POST' && body?.action === 'redeploy' && s.git) { startBuild(s, 'redeploy'); return {} }
    if (seg.length === 2 && method === 'PATCH' && body && 'git' in body) {
      if (body.git === null) {
        delete s.git; s.source = 'image'; s.image = body.image; delete secrets[s.id]; s.status = 'deploying'
        return clean(s)
      }
      const first = !s.git
      const g = body.git as Partial<GitSource>
      const cur: GitSource = s.git ?? { repo: '', has_token: false }
      s.git = { ...cur, ...g, has_token: g.token ? true : g.clear_token ? false : cur.has_token, token: undefined, clear_token: undefined }
      s.source = 'git'
      if (first) secrets[s.id] = newSecret()
      startBuild(s, 'redeploy')
      return first ? { ...clean(s), webhook: hook(s) } : clean(s)
    }
    return undefined
  }
  return { route, create, tick }
}
