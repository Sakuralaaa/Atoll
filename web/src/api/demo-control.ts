// Demo backend for 中控 / Control (/control*, /audit). In memory, example.com values.
// Re-auth: any password passes except "wrong"; 3 wrong in a row lock for 30 s (429 + Retry-After).
// `#…&2fa=1` (or 2FA enabled in Settings → Security) also asks for a 6-digit code.
// `?stress=1` swaps in long hostnames/paths/errors and a long backup list; `#…&nojournal=1` makes logs unavailable.
import { ApiError } from './client'
import type { AuditEntry, ControlBackup, ControlStatus, NodeMetrics, Server, Settings, UpdateResult } from './types'
import { series } from '../components/ui-utils'

const H = 3600e3, D = 24 * H
const fail = (status: number, msg: string, code?: string, retryAfter?: number): never => { throw new ApiError(status, msg, code, retryAfter) }
const pad = (n: number) => String(n).padStart(2, '0')
const idAt = (t: number, k: number) => { const d = new Date(t); return `cb_${d.getUTCFullYear()}${pad(d.getUTCMonth() + 1)}${pad(d.getUTCDate())}T${pad(d.getUTCHours())}${pad(d.getUTCMinutes())}${pad(d.getUTCSeconds())}Z_${(0xa1b2 + k * 4099).toString(16).slice(-4)}` }
const hex = (k: number) => Array.from({ length: 64 }, (_, i) => '0123456789abcdef'[(i * 7 + k * 13 + (i >> 2)) % 16]).join('')

export function makeDemoControl(o: { now: number; servers: Server[]; settings: Settings; stress: boolean; twoFactor: () => boolean }) {
  const { now, servers, settings, stress } = o
  const iso = (ago: number) => new Date(now - ago).toISOString()
  const prefix = stress ? 'enterprise-customer-portal/production/eu-central-1/atoll' : 'atoll'
  const publicURL = () => stress ? 'https://atoll-control-panel.enterprise-customer-portal.eu-central.example.com' : settings.public_url || 'https://panel.example.com'

  // ---- backups: 3 local (newest), up to 14 in the bucket ----
  const backups: ControlBackup[] = []
  const nRemote = stress ? 14 : 6
  for (let k = 0; k < nRemote; k++) {
    const t = now - (k === 0 ? 3.2 * H : k === 1 ? 3 * D + 2 * H : (k - 1) * D + 3 * H) // 0: today, 1: pre-update 3d ago
    const trigger = k === 0 ? 'scheduled' : k === 1 ? 'pre-update' : k === 3 ? 'manual' : 'scheduled'
    const id = idAt(t, k)
    backups.push({ id, created_at: new Date(t).toISOString(), trigger, version: k <= 1 ? 'v0.2.0' : 'v0.1.9', size_bytes: Math.round(9.4e6 + k * 61e3 - (k % 3) * 17e3),
      sha256: hex(k), local: k < 3, remote: !(stress && k === 2), remote_key: !(stress && k === 2) ? `${prefix}/control/${id}.atollbak` : undefined,
      error: stress && k === 2 ? `upload to s3://atoll-backups-enterprise-customer-portal-production/${prefix}/control/${id}.atollbak failed: PUT https://r2.example.com: 503 SlowDown: Please reduce your request rate (the local copy is still fine)` : undefined })
  }
  backups.sort((a, b) => b.created_at.localeCompare(a.created_at))
  let passphrase = 'correct-horse-battery-staple-7Qp2', passFrom = 'backup_repo_password'

  // ---- status / update simulation ----
  let version = 'v0.2.0', rollbackVersion: string | undefined = 'v0.1.9', startedAt = now - 3 * D - 5 * H
  let lastUpdate: UpdateResult | undefined = stress
    ? { mode: 'update', from_version: 'v0.1.9', to_version: 'v0.2.0-rc.3', ok: false, rolled_back: true, error: 'the new binary did not become healthy within 60s: GET http://127.0.0.1:7300/healthz reported version v0.1.9 (want v0.2.0-rc.3); restored /usr/local/bin/atoll-server.prev and restarted atoll-server.service', started_at: iso(4 * D), finished_at: iso(4 * D - 75e3) }
    : { mode: 'update', from_version: 'v0.1.9', to_version: 'v0.2.0', ok: true, started_at: iso(3 * D + 5 * H + 70e3), finished_at: iso(3 * D + 5 * H) }
  let job: { mode: 'update' | 'rollback'; from: string; to: string; t0: number } | undefined
  let checkedAt = now - 2.2 * H
  const latest = 'v0.2.1'
  const PH: [number, string][] = [[2500, 'downloading'], [5000, 'verifying'], [8000, 'backing_up'], [12000, 'restarting']]
  function tick() {
    if (!job) return undefined
    const el = Date.now() - job.t0
    const ph = job.mode === 'rollback' ? (el < 4000 ? 'restarting' : undefined) : PH.find(([t]) => el < t)?.[1]
    if (!ph) {
      lastUpdate = { mode: job.mode, from_version: job.from, to_version: job.to, ok: true, started_at: new Date(job.t0).toISOString(), finished_at: new Date().toISOString() }
      if (job.mode === 'update') backups.unshift({ id: idAt(job.t0 + 6000, 99), created_at: new Date(job.t0 + 6000).toISOString(), trigger: 'pre-update', version: job.from, size_bytes: 9.52e6, sha256: hex(99), local: true, remote: true, remote_key: `${prefix}/control/${idAt(job.t0 + 6000, 99)}.atollbak` })
      version = job.to; rollbackVersion = job.from; startedAt = Date.now() - 1500; job = undefined
      return undefined
    }
    // the unit restarts in the middle of the last phase: the panel briefly cannot reach the server
    const restartAt = job.mode === 'rollback' ? 1500 : 9000
    if (ph === 'restarting' && el > restartAt && el < restartAt + 1800) fail(0, '无法连接到 Atoll 服务器')
    return ph
  }
  const status = (): ControlStatus => {
    const phase = tick()
    const on = servers.filter(s => s.status === 'online').length
    const up = Math.floor((Date.now() - startedAt) / 1000)
    return {
      version, commit: version === 'v0.2.0' ? 'bf9864e1c0a2' : '7c41d0a9e3b5', go_version: 'go1.25.3', started_at: new Date(startedAt).toISOString(), uptime_seconds: up,
      host: stress
        ? { hostname: 'atoll-control-hetzner-cx22-nuremberg-primary-panel-node-01', os: 'ubuntu 24.04.3 LTS (Noble Numbat)', kernel: '6.8.0-87-generic-64k-hwe-24.04-enterprise', arch: 'arm64', cpu_cores: 2, uptime_seconds: 41 * 86400 + 7200 }
        : { hostname: 'atoll-control', os: 'debian 13', kernel: '6.12.48+deb13-amd64', arch: 'amd64', cpu_cores: 2, uptime_seconds: 41 * 86400 + 7200 },
      cpu_pct: 6.4, load1: 0.18, load5: 0.22, load15: 0.19, mem_used_mb: 812, mem_total_mb: 1967, disk_used_gb: 9.8, disk_total_gb: 29.4,
      data_dir: stress ? '/srv/atoll/production/control-plane/var/lib/atoll-server-data' : '/var/lib/atoll', data_dir_bytes: 431_882_240, database_bytes: 39_845_888,
      public_url: publicURL(),
      tailnet: settings.network_provider === 'tailscale'
        ? { ip: '100.64.0.4', control_url: settings.tailnet_control_url || 'http://100.64.0.4:7301', listen: '100.64.0.4:7301', listen_error: stress ? 'listen tcp 100.64.0.4:7301: bind: cannot assign requested address (tailscaled is not up yet; retrying every 30s)' : undefined }
        : undefined,
      agents_connected: on, agents_total: servers.length,
      last_backup: backups[0],
      latest_release: { tag: latest, name: `Atoll ${latest}`, url: `https://github.com/example/atoll/releases/tag/${latest}`, published_at: iso(26 * H), checked_at: new Date(checkedAt).toISOString() },
      update_available: version !== latest, release_error: stress ? 'GET https://api.github.com/repos/example/atoll/releases?per_page=10: 403 Forbidden: API rate limit exceeded for 203.0.113.40 (showing the cached result from the last successful check)' : undefined,
      self_update_supported: true, update_in_progress: !!job, update_phase: phase,
      last_update_result: lastUpdate, rollback_version: rollbackVersion,
    }
  }

  // ---- re-auth ----
  let until = 0, fails = 0, lockedUntil = 0
  const needs2fa = () => o.twoFactor() || location.hash.includes('2fa=1')
  function reauth(body: { password?: string; code?: string } | undefined, inline: boolean) {
    const pw = body?.password ?? '', code = String(body?.code ?? '').trim()
    if (!pw && !code) { if (inline && Date.now() < until) return; fail(401, 'confirm with your current password (and 2FA code) first', 'reauth_required') }
    if (Date.now() < lockedUntil) { const s = Math.ceil((lockedUntil - Date.now()) / 1000); fail(429, `too many failed attempts; try again in ${s} seconds`, 'rate_limited', s) }
    if (pw === 'wrong') {
      if (++fails >= 3) { lockedUntil = Date.now() + 30e3; fails = 0 }
      fail(401, 'current password is wrong', 'invalid_credentials')
    }
    if (needs2fa() && !code) fail(401, 'two-factor authentication is on; enter the current code from your authenticator', 'code_required')
    if (needs2fa() && !/^\d{6}$/.test(code)) fail(401, 'invalid verification code', 'invalid_code')
    fails = 0; until = Date.now() + 5 * 60e3
  }

  // ---- metrics / logs / audit ----
  function metrics(range: string): NodeMetrics[] {
    const n = range === '1h' ? 60 : 48, step = (range === '1h' ? H : range === '7d' ? 7 * D : D) / n
    const c = series(n, 6, 3, 4), m = series(n, 812, 60, 5), d = series(n, 9.8, 0.05, 6)
    return c.map((v, i) => ({ at: new Date(Date.now() - (n - 1 - i) * step).toISOString(), cpu_pct: Math.min(100, v), mem_used_mb: Math.round(m[i]), mem_total_mb: 1967, disk_used_gb: +d[i].toFixed(2), disk_total_gb: 29.4, net_rx_bps: 0, net_tx_bps: 0, load1: v / 30 }))
  }
  const host = () => status().host.hostname
  const MSG: [string, string][] = [
    ['INFO', 'msg="agent connected" node=phx via=tailnet addr=100.64.0.10:51234'],
    ['INFO', 'msg="metrics sample" cpu=6.1 mem_mb=809'],
    ['INFO', 'msg="control backup done" id=cb_20261102T030000Z_a1b2 size=9.4MB remote=true'],
    ['WARN', 'msg="agent heartbeat late" node=lax last_seen=12m'],
    ['INFO', 'msg="GET /api/v1/control" status=200 dur=3.1ms ip=203.0.113.24'],
    ['INFO', 'msg="release check" latest=v0.2.1 current=v0.2.0'],
    ['ERROR', 'msg="agent disconnected" node=lax err="websocket: close 1006 (abnormal closure): unexpected EOF"'],
    ['INFO', 'msg="image update check" services=11 updates=2'],
  ]
  const LONG = 'msg="backup upload failed, will retry" target=s3://atoll-backups-enterprise-customer-portal-production/enterprise-customer-portal/production/eu-central-1/atoll/control/cb_20261102T030000Z_a1b2.atollbak err="PUT https://r2.example.com/atoll-backups-enterprise-customer-portal-production: 503 SlowDown: Please reduce your request rate. request_id=7f3a9c0e5d1e8b0c4a9f27e21b"'
  function logs(q: URLSearchParams) {
    if (location.hash.includes('nojournal=1')) return { available: false, source: 'journald', lines: [], message: 'journalctl not found: atoll-server is not running under systemd (logs go to stderr of the process that started it)' }
    const tail = Math.min(5000, Math.max(1, Number(q.get('tail')) || 200))
    const since = q.get('since') ?? ''
    const dm = /^(\d+)(m|h)$/.exec(since)
    const from = dm ? Date.now() - Number(dm[1]) * (dm[2] === 'h' ? H : 60e3) : since ? Date.parse(since) || 0 : 0
    const out: string[] = []
    const total = 600
    for (let i = 0; i < total; i++) {
      const t = Date.now() - (total - i) * 47e3
      if (t < from) continue
      const [lv, m] = stress && i % 11 === 0 ? ['WARN', LONG] : MSG[i % MSG.length]
      const d = new Date(t)
      const ts = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}+0800`
      out.push(`${ts} ${host()} atoll-server[812]: time=${d.toISOString()} level=${lv} ${m}`)
    }
    return { available: true, source: 'journald', lines: out.slice(-tail) }
  }

  const audit: AuditEntry[] = []
  {
    const A: [string, string, string, string?][] = [
      ['user:admin', 'session', 'control.reauth'], ['user:admin', 'session', 'control.backup_download', 'cb_20261101T030000Z_b3c4'],
      ['token:tok_demo1', 'token', 'api.deploy', 'acme-api'], ['token:tok_demo2', 'token', 'api.domains', 'api.example.com'],
      ['system', 'system', 'control.backup', 'cb_20261102T030000Z_a1b2'], ['user:admin', 'session', 'service.redeploy', 'mail-relay'],
      ['cli', 'cli', 'control.export', ''], ['user:admin', 'session', 'auth.login', ''], ['user:admin', 'session', 'control.reauth_failed', 'update'],
      ['token:tok_demo3', 'token', 'api.servers', 'denied: token lacks the servers permission'], ['system', 'system', 'control.dns_adopt', 'panel.example.com'],
      ['user:admin', 'session', 'control.update', 'v0.2.0'], ['user:admin', 'session', 'settings.update', 'backup'],
    ]
    for (let i = 0; i < (stress ? 260 : 140); i++) {
      const [actor, kind, action, target] = A[i % A.length]
      const longT = stress && i % 7 === 0
      audit.push({ id: 5000 - i, actor: longT && kind === 'token' ? 'token:tok_github_actions_enterprise_customer_portal_production_deployer' : actor, actor_kind: kind, action,
        target: longT ? 'enterprise-customer-portal-production-environment-eu-central / customer-notification-delivery-pipeline-worker' : target ?? '',
        ip: kind === 'system' || kind === 'cli' ? undefined : longT ? '2001:db8:85a3:0000:0000:8a2e:0370:7334' : i % 3 ? '203.0.113.24' : '198.51.100.7',
        token_id: kind === 'token' ? actor.slice(6) : undefined, session_id: kind === 'session' ? (i % 3 ? '3f9a1c0d7e2b4a65' : 'b81e4f2a90c37d16') : undefined,
        detail: action === 'control.reauth_failed' ? { reason: 'invalid_credentials' } : action === 'control.backup' ? { trigger: 'scheduled', size: 9400000, remote: true } : action.startsWith('api.') ? { result: target?.startsWith('denied') ? target : 'ok' } : null,
        created_at: iso(i * 37 * 60e3 + 4 * 60e3) })
    }
  }
  const log = (action: string, target = '', detail: unknown = null) => audit.unshift({ id: (audit[0]?.id ?? 5000) + 1, actor: 'user:demo', actor_kind: 'session', action, target, ip: '203.0.113.24', session_id: '3f9a1c0d7e2b4a65', detail, created_at: new Date().toISOString() })
  function auditPage(q: URLSearchParams) {
    const limit = Math.min(500, Number(q.get('limit')) || 100), before = Number(q.get('before')) || 0
    const actor = q.get('actor') ?? '', action = q.get('action') ?? ''
    const match = (v: string, f: string, sep: string) => !f || (f.endsWith(sep) || f.endsWith('*') ? v.startsWith(f.replace(/\*$/, '')) : v === f)
    const rows = audit.filter(x => (!before || x.id < before) && match(x.actor, actor, ':') && match(x.action, action, '.'))
    const page = rows.slice(0, limit)
    return { entries: page, next_before: rows.length > limit ? page[page.length - 1].id : undefined }
  }

  /** Returns undefined when the path is not a control route. */
  return function route(method: string, seg: string[], body: any, q: URLSearchParams): unknown {
    const P = method === 'POST', G = method === 'GET'
    if (G && seg.length === 1 && seg[0] === 'audit') return auditPage(q)
    if (seg[0] !== 'control') return undefined
    const p = seg.slice(1).join('/')
    if (G && p === '') return status()
    if (G && p === 'metrics') return metrics(q.get('range') ?? '24h')
    if (G && p === 'logs') return logs(q)
    if (P && p === 'reauth') {
      if (!body?.password) fail(400, 'password required', 'invalid')
      try { reauth(body, false) } catch (x) { log('control.reauth_failed', 'reauth', { reason: (x as ApiError).code }); throw x }
      log('control.reauth'); return { expires_at: new Date(until).toISOString() }
    }
    if (P && p === 'update/check') { checkedAt = Date.now(); const s = status(); return { latest_release: s.latest_release, current: s.version, update_available: s.update_available, release_error: s.release_error } }
    if (P && p === 'update') {
      reauth(body, true)
      if (job) fail(409, 'an update is already running', 'update_in_progress')
      const to = body?.version || latest
      if (to === version) fail(409, `already running ${version}`, 'already_current')
      job = { mode: 'update', from: version, to, t0: Date.now() }; log('control.update', to)
      return { mode: 'update', version: to, phase: 'downloading' }
    }
    if (P && p === 'rollback') {
      reauth(body, true)
      if (job) fail(409, 'an update is already running', 'update_in_progress')
      const prev = rollbackVersion ?? fail(409, 'there is no previous binary to roll back to', 'no_previous_binary')
      job = { mode: 'rollback', from: version, to: prev, t0: Date.now() }; log('control.rollback', prev)
      return { mode: 'rollback', version: prev, phase: 'restarting' }
    }
    if (G && p === 'backups') return { backups, remote_enabled: true, remote_error: stress ? 'list s3://atoll-backups-enterprise-customer-portal-production/enterprise-customer-portal/production/eu-central-1/atoll/control/: 403 AccessDenied (showing local bundles and the last known remote list)' : undefined, keep_local: 3, keep_remote: 14, next_scheduled: new Date(now + 5.8 * H).toISOString(), passphrase_from: passFrom }
    if (P && p === 'backups') {
      const t = Date.now(), id = idAt(t, backups.length + 7)
      const b: ControlBackup = { id, created_at: new Date(t).toISOString(), trigger: 'manual', version, size_bytes: 9.47e6, sha256: hex(backups.length), local: true, remote: true, remote_key: `${prefix}/control/${id}.atollbak` }
      backups.unshift(b)
      backups.filter(x => x.local).slice(3).forEach(x => { x.local = false })
      log('control.backup', id, { trigger: 'manual' }); return b
    }
    if (G && seg[1] === 'backups' && seg[3] === 'download') {
      reauth(undefined, true)
      const id = decodeURIComponent(seg[2])
      if (!backups.some(b => b.id === id)) fail(404, 'no such control backup', 'not_found')
      log('control.backup_download', id); return { ok: true }
    }
    if (P && p === 'backups/passphrase') {
      reauth(body, true)
      if (body?.passphrase) {
        if (String(body.passphrase).length < 12) fail(400, 'the passphrase must be at least 12 characters', 'invalid')
        passphrase = body.passphrase; passFrom = 'control_backup_passphrase'; log('control.passphrase_change')
      } else log('control.passphrase_reveal')
      return { passphrase, from: passFrom }
    }
    if (P && p === 'export') {
      if (String(body?.passphrase ?? '').length < 12) fail(400, 'the passphrase must be at least 12 characters', 'invalid')
      reauth(body, true)
      const d = new Date(), name = `atoll-control-${host()}-${d.getUTCFullYear()}${pad(d.getUTCMonth() + 1)}${pad(d.getUTCDate())}T${pad(d.getUTCHours())}${pad(d.getUTCMinutes())}${pad(d.getUTCSeconds())}Z.atollbak`
      log('control.export', '', { size: 9.6e6 }); return { name }
    }
    return fail(404, `demo: 未实现 ${method} /${seg.join('/')}`)
  }
}
