// Display helpers: derive the visual bits the design needs (letters, colors, flags, percentages) from API data.
import type { Deployment, Project, Server, Service } from './types'

const PALETTE = ['#a855f7', '#22c55e', '#3b82f6', '#f59e0b', '#14b8a6', '#38bdf8', '#f97316', '#c084fc', '#60a5fa', '#e5e7eb', '#f43f5e', '#84cc16']
// keep the demo/screenshot look stable for the original mock names
const FIXED: Record<string, string> = {
  'mail-relay': '#a855f7', 'acme-api': '#e5e7eb', LinkProxy: '#3b82f6', 'admin-panel': '#f59e0b', 'cron-worker': '#14b8a6',
  'file-share': '#38bdf8', 'chat-app': '#f97316', flaresolverr: '#60a5fa', 'qa-bot': '#e5e7eb', 'blog-studio': '#c084fc',
}
export function colorFor(name: string) {
  if (FIXED[name]) return FIXED[name]
  let h = 0; for (const c of name) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return PALETTE[h % PALETTE.length]
}
export const letterFor = (name: string) => (name.match(/[A-Za-z0-9]/)?.[0] ?? name[0] ?? '?').toUpperCase()

export interface IconSpec { kind: string; letter?: string; color?: string }
/** The name the panel shows for a service: its display name, else its hostname. */
export const svcName = (s: Pick<Service, 'name' | 'display_name'>) => s.display_name || s.name
export const projName = (p: Pick<Project, 'name' | 'display_name'>) => p.display_name || p.name
export const svcIcon = (s: Pick<Service, 'kind' | 'name'>): IconSpec => ({ kind: s.kind, letter: letterFor(s.name), color: colorFor(s.name) })
export const projIcon = (p: Project): IconSpec => p.services?.[0] ? svcIcon(p.services[0]) : { kind: 'image', letter: letterFor(p.name), color: colorFor(p.name) }

export const primaryDomain = (s: Service) => s.domains?.find(d => d.status !== 'error')?.hostname ?? s.domains?.[0]?.hostname
export const primaryPort = (s: Service) => s.ports?.[0]

/** "Phoenix, US" -> 🇺🇸 (last 2-letter token). */
export function flagFor(region?: string) {
  const m = region?.match(/\b([A-Z]{2})\s*$/)
  if (!m) return ''
  return String.fromCodePoint(...[...m[1]].map(c => 0x1f1e6 + c.charCodeAt(0) - 65))
}

export function usagePct(s: Server) {
  const u = s.usage
  if (!u) return { cpu: 0, mem: 0, disk: 0 }
  const pct = (a: number, b: number) => (b > 0 ? Math.round((a / b) * 100) : 0)
  return { cpu: Math.round(u.cpu_pct), mem: pct(u.mem_used_mb, u.mem_total_mb || s.mem_mb), disk: pct(u.disk_used_gb, u.disk_total_gb || s.disk_gb) }
}
export const needsAttention = (s: Server) => { const u = usagePct(s); return s.status === 'online' && (u.cpu >= 80 || u.mem >= 80 || u.disk >= 85) }
export const isOnline = (s?: Server) => s?.status === 'online'
export const gb = (mb: number) => (mb >= 1024 ? `${+(mb / 1024).toFixed(mb % 1024 ? 1 : 0)} GB` : `${mb} MB`)

export function ago(iso?: string) {
  if (!iso) return '—'
  const t = new Date(iso).getTime()
  if (!t || t < 0) return '—'
  const s = Math.max(0, (Date.now() - t) / 1000)
  if (s < 60) return '刚刚'
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`
  if (s < 86400 * 30) return `${Math.floor(s / 86400)} 天前`
  return day(iso)
}
export const day = (iso?: string) => (iso ? new Date(iso).toLocaleDateString('sv-SE') : '—')
export const dateTime = (iso?: string) => (iso ? new Date(iso).toLocaleString('sv-SE', { hour12: false }).slice(0, 16) : '—')
export const hhmm = (iso: string) => new Date(iso).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })

export const triggerText: Record<string, string> = { create: '创建服务', redeploy: '重新部署', env: '修改环境变量', rollback: '回滚', import: '从 Zeabur 导入', migrate: '迁移服务器', 'image-update': '镜像更新', webhook: 'GitHub 推送' }
export const deployStatusText: Record<string, string> = { queued: '排队中', building: '构建中', pulling: '拉取镜像', starting: '启动中', running: '运行中', failed: '失败', superseded: '已替换' }
export const latestDeploy = (ds?: Deployment[]) => ds?.slice().sort((a, b) => b.started_at.localeCompare(a.started_at))[0]

/** Pick ~6 evenly spaced time labels for a chart. */
export function timeLabels(ats: string[], n = 6) {
  if (ats.length < 2) return ['', '']
  return Array.from({ length: n }, (_, i) => hhmm(ats[Math.round((i / (n - 1)) * (ats.length - 1))]))
}
export function bytesRate(bps: number) {
  if (bps >= 1 << 20) return `${(bps / (1 << 20)).toFixed(1)} MB/s`
  if (bps >= 1024) return `${(bps / 1024).toFixed(1)} KB/s`
  return `${Math.round(bps)} B/s`
}
export function bytes(n: number) {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(2)} GB`
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${n} B`
}

/** Long sha256 digests inside free text (deploy messages, errors) -> "sha256:<12 hex>…"; keep the full text for a title tooltip. */
export const shortDigests = (text?: string) => (text ?? '').replace(/sha256:([0-9a-f]{12})[0-9a-f]{20,}/gi, 'sha256:$1…')

/** The agent runs another version than the one this control server hosts. */
export const agentOutdated = (s: Server) => !!s.agent_latest && !!s.agent_version && s.agent_version !== s.agent_latest
