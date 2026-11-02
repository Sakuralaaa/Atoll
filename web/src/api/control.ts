// 中控 / Control: REST calls for managing atoll-server itself (docs/api.md → Control).
// Every session-only call uses noRedirect: a 401 here is usually "re-auth needed" or a wrong
// password in the re-auth dialog, which must stay on the page. Any other 401 (session gone)
// still goes to the login page.
import { API, ApiError, isDemo, request, toLogin, type Range } from './client'
import { useQuery } from './hooks'
import type {
  AuditPage, ControlBackup, ControlBackupList, ControlLogs, ControlPassphrase, ControlReauth, ControlStatus, ControlUpdateCheck,
  ControlUpdateResponse, NodeMetrics, ReauthFields,
} from './types'

const e = encodeURIComponent
/** 401 codes that belong to the re-auth flow (never bounce to /login for these). */
export const REAUTH_CODES = ['reauth_required', 'invalid_credentials', 'code_required', 'invalid_code']

async function ctl<T>(method: string, path: string, body?: unknown): Promise<T> {
  try {
    return await request<T>(method, path, body, { noRedirect: true })
  } catch (x) {
    const err = x as ApiError
    if (err.status === 401 && !REAUTH_CODES.includes(err.code ?? '')) toLogin()
    throw err
  }
}

export const controlApi = {
  status: () => request<ControlStatus>('GET', '/control'),
  metrics: (range: Range) => request<NodeMetrics[]>('GET', `/control/metrics?range=${range}`),
  logs: (tail: number, since?: string) => ctl<ControlLogs>('GET', `/control/logs?tail=${tail}${since ? `&since=${e(since)}` : ''}`),
  reauth: (f: ReauthFields) => ctl<ControlReauth>('POST', '/control/reauth', f),
  checkUpdate: () => ctl<ControlUpdateCheck>('POST', '/control/update/check', {}),
  update: (version?: string) => ctl<ControlUpdateResponse>('POST', '/control/update', version ? { version } : {}),
  rollback: () => ctl<ControlUpdateResponse>('POST', '/control/rollback', {}),
  backups: () => ctl<ControlBackupList>('GET', '/control/backups'),
  runBackup: () => ctl<ControlBackup>('POST', '/control/backups', {}),
  /** Without a passphrase: reveal the current one. With one (min 12): replace it. */
  passphrase: (passphrase?: string) => ctl<ControlPassphrase>('POST', '/control/backups/passphrase', passphrase ? { passphrase } : {}),
  audit: (o: { limit?: number; before?: number; actor?: string; action?: string }) => {
    const q = new URLSearchParams()
    if (o.limit) q.set('limit', String(o.limit))
    if (o.before) q.set('before', String(o.before))
    if (o.actor) q.set('actor', o.actor)
    if (o.action) q.set('action', o.action)
    return ctl<AuditPage>('GET', `/audit${q.size ? `?${q}` : ''}`)
  },
}

export const useControl = (interval = 30000) => useQuery('control', controlApi.status, { interval })

/** Minimum passphrase length for exports and the backup passphrase (ctlbundle.MinPassphrase). */
export const MIN_PASSPHRASE = 12

function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url; a.download = name; document.body.appendChild(a); a.click(); a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 30000)
}
const demoBundle = (name: string) => new Blob([`ATOLLBAK\n(demo) ${name}\n`], { type: 'application/octet-stream' })

/** GET /control/backups/{id}/download needs the re-auth window (a browser download carries no body).
 *  The bundle is streamed by the browser's own download manager. */
export async function downloadBackup(id: string) {
  const path = `/control/backups/${e(id)}/download`
  const name = `atoll-control-${id}.atollbak`
  if (isDemo()) { await ctl('GET', path); saveBlob(demoBundle(name), name); return }
  const a = document.createElement('a')
  a.href = API + path; a.download = name; document.body.appendChild(a); a.click(); a.remove()
}

type Sink = { write: (c: Uint8Array) => Promise<void>; close: () => Promise<void>; abort: () => Promise<void> }
type SavePicker = (o: { suggestedName: string; types?: { description: string; accept: Record<string, string[]> }[] }) => Promise<{ createWritable: () => Promise<Sink> }>

/** Ask for the destination file right away (must run inside the click), when the browser supports it,
 *  so the export streams to disk instead of being buffered in memory. Undefined: fall back to a blob. */
export async function pickExportFile(suggestedName: string): Promise<Sink | undefined | null> {
  const pick = (window as unknown as { showSaveFilePicker?: SavePicker }).showSaveFilePicker
  if (!pick || isDemo()) return undefined
  try {
    const h = await pick({ suggestedName, types: [{ description: 'Atoll control bundle', accept: { 'application/octet-stream': ['.atollbak'] } }] })
    return await h.createWritable()
  } catch (x) {
    if ((x as Error).name === 'AbortError') return null // user cancelled the picker
    return undefined
  }
}

/** POST /control/export {passphrase}: a fresh bundle, streamed to `sink` (or downloaded as a blob). */
export async function exportBundle(passphrase: string, sink: Sink | undefined, onProgress: (bytes: number) => void, fallbackName: string) {
  if (isDemo()) {
    const r = await ctl<{ name: string }>('POST', '/control/export', { passphrase })
    for (let i = 1; i <= 8; i++) { await new Promise(res => setTimeout(res, 120)); onProgress(i * 1.2e6) }
    saveBlob(demoBundle(r.name), r.name); return r.name
  }
  let res: Response
  try {
    res = await fetch(API + '/control/export', { method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json', Accept: 'application/octet-stream' }, body: JSON.stringify({ passphrase }) })
  } catch { throw new ApiError(0, '无法连接到 Atoll 服务器') }
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`, code: string | undefined
    try { const d = await res.json(); msg = d.error ?? msg; code = d.code } catch { /* not json */ }
    if (res.status === 401 && !REAUTH_CODES.includes(code ?? '')) toLogin()
    throw new ApiError(res.status, msg, code, Number(res.headers.get('Retry-After')) || undefined)
  }
  const name = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '')?.[1] ?? fallbackName
  const reader = res.body?.getReader()
  if (!reader) { saveBlob(await res.blob(), name); return name }
  const chunks: Uint8Array[] = []
  let n = 0
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      n += value.byteLength; onProgress(n)
      if (sink) await sink.write(value); else chunks.push(value)
    }
  } catch (x) { if (sink) await sink.abort().catch(() => {}); throw x }
  if (sink) await sink.close(); else saveBlob(new Blob(chunks as BlobPart[], { type: 'application/octet-stream' }), name)
  return name
}

// ---------- display helpers ----------
export function uptime(sec: number) {
  if (!sec || sec < 0) return '—'
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60)
  if (d) return `${d} 天 ${h} 小时`
  if (h) return `${h} 小时 ${m} 分钟`
  return `${Math.max(1, m)} 分钟`
}
export const triggerLabel: Record<string, string> = { scheduled: '定时', manual: '手动', 'pre-update': '更新前', unknown: '未知' }
export const phaseLabel: Record<string, string> = { downloading: '下载新版本', verifying: '校验 SHA256 与版本号', backing_up: '中控备份（pre-update）', restarting: '切换并重启，等待健康检查' }
export const PHASES = ['downloading', 'verifying', 'backing_up', 'restarting']
export const passphraseFromText: Record<string, string> = {
  control_backup_passphrase: '中控备份口令（在本页设置）',
  backup_repo_password: '备份仓库密码（设置 → 备份）',
  generated: '自动生成的口令',
}
/** host of a URL when it is a domain name (not an IP literal). */
export function domainOf(url?: string) {
  try {
    const h = new URL(url ?? '').hostname
    if (!h || /^\d+\.\d+\.\d+\.\d+$/.test(h) || h.startsWith('[')) return undefined
    return h
  } catch { return undefined }
}
