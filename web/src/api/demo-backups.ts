// Demo backend for backups (/services/{id}/backup-policy, /services/{id}/backups, /backups/{id}/restore,
// /restores/{id}, /settings/backup/*). Generic demo data only (see mock.ts).
import { ApiError } from './client'
import type { Backup, BackupPolicy, BackupVerification, BackupVerifyStatus, RestoreJob, Server, Service, Settings } from './types'

const H = 3600e3, D = 24 * H
const DB_KINDS = new Set(['postgres', 'mysql', 'redis', 'mongo'])
const fail = (status: number, msg: string): never => { throw new ApiError(status, msg) }
const hex = (n: number) => Array.from({ length: n }, () => Math.floor(Math.random() * 16).toString(16)).join('')
const CRON_RE = /^(\S+\s+){4}\S+$/
const DEMO_PASSWORD = 'Rk7mQ2vX9pLw4Tz8Nc3Hb6Yd1Fs5Gj0Ae2Ku7Mx4'

export function makeDemoBackups(services: Service[], servers: Server[], projectServer: (pid: string) => string, settings: Settings) {
  // Demo starts with a configured R2 bucket; `#...?nobackup=1` shows the unconfigured state.
  if (!location.hash.includes('nobackup=1')) Object.assign(settings, {
    backup_endpoint: 'https://0123456789abcdef.r2.cloudflarestorage.com', backup_bucket: 'atoll-backups', backup_prefix: 'atoll', backup_region: 'auto',
    has_backup_credentials: true, has_backup_password: true, backup_configured: true,
  } satisfies Partial<Settings>)
  let password = settings.has_backup_password ? DEMO_PASSWORD : ''
  const configured = () => !!settings.backup_configured

  const policies: Record<string, BackupPolicy> = {}
  const backups: Record<string, Backup[]> = {}
  const restores: RestoreJob[] = []
  const verifications: BackupVerification[] = []
  const onlineServer = () => servers.find(x => x.status === 'online')?.id ?? ''
  const nextWeekday = (dow: number, h: number, m: number) => { const d = new Date(); d.setHours(h, m, 0, 0); while (d.getDay() !== dow || d.getTime() < Date.now()) d.setTime(d.getTime() + D); return d.toISOString() }
  const nextMonthDay = (h: number, m: number) => { const d = new Date(); d.setDate(1); d.setHours(h, m, 0, 0); if (d.getTime() < Date.now()) d.setMonth(d.getMonth() + 1); return d.toISOString() }
  const seedVerify = () => {
    if (verifications.length || !configured()) return
    const at = new Date(nextWeekday(0, 4, 30)); at.setTime(at.getTime() - 7 * D)
    verifications.push({ id: 'bvf_demochk1', kind: 'check', server_id: onlineServer(), trigger: 'schedule', status: 'done', subset: '5%', duration_ms: 384e3,
      steps: [{ part: 'check', status: 'done', detail: 'read 5% of data packs | no errors were found' }], started_at: at.toISOString(), finished_at: new Date(at.getTime() + 384e3).toISOString() })
  }
  const latest = (kind: string, sid = '') => verifications.find(v => v.kind === kind && (v.service_id ?? '') === sid)
  const verifyStatus = (s?: Service): BackupVerifyStatus => {
    seedVerify()
    const st: BackupVerifyStatus = { check: latest('check'), check_schedule: '30 4 * * 0', check_subset: '5%', drill_schedule: '30 4 1 * *', timezone: 'Asia/Shanghai',
      check_server_id: configured() ? onlineServer() : undefined, next_check_at: configured() ? nextWeekday(0, 4, 30) : undefined, repo_configured: configured() }
    if (s) { st.drill = latest('drill', s.id); if (configured() && policy(s).enabled) st.next_drill_at = nextMonthDay(4, 30) }
    return st
  }
  const startVerify = (kind: 'check' | 'drill', s?: Service): BackupVerification => {
    if (!configured()) fail(409, 'configure a backup target in Settings → 备份存储 first')
    if (latest(kind, s?.id)?.status === 'running') fail(409, `a ${kind} is still running`)
    const v: BackupVerification = { id: `bvf_demo${hex(8)}`, kind, service_id: s?.id, server_id: s ? projectServer(s.project_id) : onlineServer(), trigger: 'manual',
      status: 'running', subset: kind === 'check' ? '5%' : undefined, steps: [], duration_ms: 0, started_at: new Date().toISOString() }
    verifications.unshift(v); return v
  }
  const svc = (id: string) => services.find(s => s.id === id) ?? fail(404, '服务不存在')
  const parts = (s: Service, p: BackupPolicy) => DB_KINDS.has(s.kind) ? ['dump', ...(p.include_volumes && s.volumes?.length ? ['volumes'] : [])] : s.volumes?.length ? ['volumes'] : []
  const nextRun = (p: BackupPolicy) => {
    if (!p.enabled) return undefined
    const [mi, hr] = p.schedule.split(/\s+/)
    const d = new Date(); d.setSeconds(0, 0)
    if (/^\d+$/.test(mi) && /^\d+$/.test(hr)) { d.setHours(+hr, +mi); if (d.getTime() < Date.now()) d.setTime(d.getTime() + D) }
    else if (/^\d+$/.test(mi) && hr.startsWith('*/')) { const st = +hr.slice(2) || 1; d.setMinutes(+mi); while (d.getTime() < Date.now() || d.getHours() % st) d.setTime(d.getTime() + H) }
    else d.setTime(d.getTime() + H)
    return d.toISOString()
  }
  const policy = (s: Service): BackupPolicy => {
    const p = policies[s.id] ?? (policies[s.id] = {
      service_id: s.id, enabled: ['acme-api', 'pg', 'mail-relay', 'pg2'].includes(s.id), schedule: '0 3 * * *', timezone: 'Asia/Shanghai',
      keep: { daily: 7, weekly: 4, monthly: 6 }, include_volumes: !DB_KINDS.has(s.kind), repo_configured: false,
    })
    return { ...p, repo_configured: configured(), parts: parts(s, p), next_run_at: nextRun(p) }
  }
  const seed = (s: Service): Backup[] => {
    if (backups[s.id]) return backups[s.id]
    const list: Backup[] = backups[s.id] = []
    if (!policy(s).enabled || !configured()) return list
    const base = DB_KINDS.has(s.kind) ? 48e6 : 310e6
    for (let i = 0; i < 8; i++) {
      const start = new Date(); start.setHours(3, 0, 0, 0); start.setTime(start.getTime() - (start.getTime() > Date.now() ? i + 1 : i) * D)
      const ps = parts(s, policy(s))
      const failed = i === 3
      const dur = 18e3 + (i * 7919) % 40e3
      const snaps = failed ? [] : ps.map((part, j) => ({ id: hex(64), part, bytes: Math.round((part === 'dump' ? base : base * 2.4) * (1 - i * 0.004) + j), added: Math.round((2 + ((i * 37) % 11)) * 1e6) }))
      list.push({
        id: `bak_demo${s.id}${i}`, service_id: s.id, server_id: projectServer(s.project_id), trigger: i === 5 ? 'manual' : 'schedule',
        status: failed ? 'failed' : 'done', snapshots: snaps, bytes: snaps.reduce((a, x) => a + x.bytes, 0), added: snaps.reduce((a, x) => a + x.added, 0),
        duration_ms: failed ? 4100 : dur, error: failed ? 'restic: Fatal: unable to open config file: Stat: 503 Service Unavailable' : undefined,
        started_at: start.toISOString(), finished_at: new Date(start.getTime() + dur).toISOString(),
      })
    }
    return list
  }
  const tick = () => {
    for (const list of Object.values(backups)) for (const b of list) {
      if (b.status !== 'running' || Date.now() - new Date(b.started_at).getTime() < 6000) continue
      const s = svc(b.service_id)
      b.snapshots = parts(s, policy(s)).map(part => ({ id: hex(64), part, bytes: part === 'dump' ? 47.6e6 : 742e6, added: 3.2e6 }))
      b.bytes = b.snapshots.reduce((a, x) => a + x.bytes, 0); b.added = b.snapshots.reduce((a, x) => a + x.added, 0)
      b.status = 'done'; b.duration_ms = Date.now() - new Date(b.started_at).getTime(); b.finished_at = new Date().toISOString()
    }
    for (const v of verifications) {
      if (v.status !== 'running' || Date.now() - new Date(v.started_at).getTime() < 7000) continue
      v.status = 'done'; v.duration_ms = Date.now() - new Date(v.started_at).getTime(); v.finished_at = new Date().toISOString()
      if (v.kind === 'check') v.steps = [{ part: 'check', status: 'done', detail: 'read 5% of data packs | no errors were found' }]
      else {
        const s = svc(v.service_id ?? '')
        v.steps = parts(s, policy(s)).map(part => part === 'dump'
          ? { part, snapshot_id: hex(64), status: 'done', detail: 'databases=2 tables=14', bytes: 47.6e6 }
          : { part, snapshot_id: hex(64), status: 'done', detail: 'files=1284 bytes=742000000', files: 1284, bytes: 742e6 })
      }
    }
    for (const j of restores) {
      if (j.status !== 'running') continue
      const n = Math.floor((Date.now() - new Date(j.created_at).getTime()) / 2500)
      j.steps.forEach((st, i) => { st.status = i < n ? 'done' : i === n ? 'running' : 'pending' })
      if (n >= j.steps.length) { j.status = 'done'; j.finished_at = new Date().toISOString() }
    }
  }

  /** PUT /settings: backup fields (empty keeps). Returns the generated password once. */
  function putSettings(body: Settings): string | undefined {
    const keep = (v?: string) => (v ?? '').trim()
    for (const k of ['backup_endpoint', 'backup_bucket', 'backup_prefix', 'backup_region'] as const) if (keep(body[k])) settings[k] = keep(body[k])
    if (body.backup_bucket && !/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/.test(body.backup_bucket.trim())) fail(400, `invalid backup_bucket "${body.backup_bucket}"`)
    if (keep(body.backup_access_key) && keep(body.backup_secret_key)) settings.has_backup_credentials = true
    if (body.backup_repo_password) { password = body.backup_repo_password; settings.has_backup_password = true }
    let generated: string | undefined
    if (!password && settings.backup_bucket) { password = generated = DEMO_PASSWORD; settings.has_backup_password = true }
    if (settings.backup_bucket && !settings.backup_prefix) settings.backup_prefix = 'atoll'
    settings.backup_configured = !!(settings.backup_endpoint && settings.backup_bucket && settings.has_backup_credentials && password)
    return generated
  }

  /** Returns undefined when the path is not a backup route. */
  function route(method: string, seg: string[], body: any): unknown {
    const G = method === 'GET', P = method === 'POST', U = method === 'PUT'
    tick()
    if (seg[0] === 'settings' && seg[1] === 'backup') {
      if (seg.length === 2 && method === 'DELETE') {
        for (const k of ['backup_endpoint', 'backup_bucket', 'backup_prefix', 'backup_region'] as const) delete settings[k]
        settings.has_backup_credentials = false; settings.backup_configured = false
        return settings
      }
      if (P && seg[2] === 'test') {
        const ep = (body?.backup_endpoint || settings.backup_endpoint || '').trim(), bucket = (body?.backup_bucket || settings.backup_bucket || '').trim()
        const creds = settings.has_backup_credentials || (body?.backup_access_key && body?.backup_secret_key)
        if (!ep || !bucket || !creds) fail(400, 'endpoint, bucket, access key and secret key are required')
        if (!password && !body?.backup_repo_password) fail(400, 'no repository password yet: save the backup settings first (one is generated)')
        return { ok: true, server_id: servers.find(x => x.status === 'online')?.id ?? '', message: `repository s3:${ep}/${bucket}/${settings.backup_prefix || 'atoll'} opened (8 snapshots)` }
      }
      if (G && seg[2] === 'password') {
        if (!password) fail(404, 'no backup repository password is set')
        return { password, repository: `s3:${settings.backup_endpoint}/${settings.backup_bucket}/${settings.backup_prefix || 'atoll'}` }
      }
      return fail(404, `demo: 未实现 ${method} /${seg.join('/')}`)
    }
    if (seg[0] === 'backup-verify') {
      if (G && seg.length === 1) return verifyStatus()
      if (P && seg[1] === 'check') return startVerify('check')
      return fail(404, `demo: 未实现 ${method} /${seg.join('/')}`)
    }
    if (seg[0]?.startsWith('backup-verifications') && G) { seedVerify(); return verifications }
    if (seg[0] === 'services' && seg.length === 3) {
      const sub = seg[2]
      if (!['backup-policy', 'backups', 'restores', 'backup-verify', 'backup-drill'].includes(sub)) return undefined
      const s = svc(decodeURIComponent(seg[1]))
      if (sub === 'backup-verify' && G) return verifyStatus(s)
      if (sub === 'backup-drill' && P) {
        if (!parts(s, policy(s)).length) fail(400, `service ${s.name} has no volumes and is not a database template: nothing to restore`)
        return startVerify('drill', s)
      }
      if (sub === 'restores') return restores.filter(j => j.service_id === s.id)
      if (sub === 'backup-policy') {
        if (U) {
          const k = body?.keep ?? {}
          if (body.schedule && !CRON_RE.test(String(body.schedule).trim())) fail(400, `expected 5 fields: ${body.schedule}`)
          if ((k.daily | 0) + (k.weekly | 0) + (k.monthly | 0) === 0) fail(400, 'retention must keep at least one snapshot (keep.daily, keep.weekly or keep.monthly)')
          const cur = policy(s)
          policies[s.id] = { ...cur, enabled: !!body.enabled, schedule: String(body.schedule || '0 3 * * *').trim(), timezone: body.timezone || 'Asia/Shanghai',
            keep: { daily: k.daily | 0, weekly: k.weekly | 0, monthly: k.monthly | 0 }, include_volumes: DB_KINDS.has(s.kind) ? !!body.include_volumes : true, updated_at: new Date().toISOString() }
        }
        return policy(s)
      }
      const list = seed(s)
      if (G) return list
      if (P) {
        if (!configured()) fail(409, 'no backup target configured: set it in Settings → 备份存储')
        if (!parts(s, policy(s)).length) fail(409, `service ${s.name} has no volumes and is not a database template: nothing to back up`)
        if (list.some(b => b.status === 'running')) fail(409, 'a backup of this service is already running')
        const b: Backup = { id: `bak_demo${hex(8)}`, service_id: s.id, server_id: projectServer(s.project_id), trigger: 'manual', status: 'running', snapshots: [], bytes: 0, added: 0, duration_ms: 0, started_at: new Date().toISOString() }
        list.unshift(b); return b
      }
      return undefined
    }
    if (seg[0] === 'backups' && seg.length >= 2) {
      const id = decodeURIComponent(seg[1])
      const b = Object.values(backups).flat().find(x => x.id === id) ?? fail(404, '备份不存在')
      if (G && seg.length === 2) return b
      if (P && seg[2] === 'restore') {
        if (b.status !== 'done') fail(409, `backup ${b.id} is ${b.status}`)
        const s = svc(b.service_id), from = projectServer(s.project_id), to = body?.target_server_id || from
        const tgt = servers.find(x => x.id === to) ?? fail(400, `unknown target_server_id "${to}"`)
        if (tgt.status !== 'online') fail(409, 'target server is offline')
        if (to !== from && servers.find(x => x.id === from)?.status === 'online') fail(409, 'the project\'s server is online: restore in place, or move the project with migrate first (restoring to another server is for when the server is gone)')
        const steps = to === from ? ['停止服务', '下载快照', '恢复数据', '启动服务'] : ['在目标服务器创建项目', '下载快照', '恢复数据', '启动服务', '切换 DNS']
        const j: RestoreJob = { id: `rst_demo${hex(8)}`, backup_id: b.id, service_id: s.id, project_id: s.project_id, from_server_id: from, to_server_id: to, status: 'running',
          steps: steps.map(name => ({ name, status: 'pending' })), created_at: new Date().toISOString() }
        restores.unshift(j); return j
      }
      return fail(404, `demo: 未实现 ${method} /${seg.join('/')}`)
    }
    if (seg[0] === 'restores' && seg.length === 2 && G) return restores.find(j => j.id === decodeURIComponent(seg[1])) ?? fail(404, '还原任务不存在')
    return undefined
  }
  return { route, putSettings }
}
