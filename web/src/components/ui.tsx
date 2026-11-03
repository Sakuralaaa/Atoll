import { Copy, Server as ServerGlyph } from 'lucide-react'
import type { IconSpec } from '../api/view'
import { cx, si } from './ui-utils'

export function LogoMark({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none">
      <rect x="3.5" y="3.5" width="26" height="26" rx="7" fill="var(--edge)" />
      <rect x="1.5" y="1.5" width="26" height="26" rx="7" fill="#8b5cf6" stroke="var(--edge)" strokeWidth="2" />
      <path d="M14.5 6.8a7.7 7.7 0 1 1-7.2 5" stroke="#fff" strokeWidth="3.4" strokeLinecap="round" />
      <circle cx="14.5" cy="14.5" r="2.4" fill="#fde047" stroke="var(--edge)" strokeWidth="1.5" />
      <circle cx="8.6" cy="8.4" r="1.9" fill="#fde047" stroke="var(--edge)" strokeWidth="1.5" />
    </svg>
  )
}

export function Logo() {
  return (
    <div className="flex items-center gap-2.5 select-none">
      <LogoMark />
      <span className="text-[20px] font-extrabold italic tracking-[-0.03em] leading-none font-mono">Atoll</span>
    </div>
  )
}

export function Avatar({ size = 24 }: { size?: number }) {
  return (
    <div style={{ width: size, height: size }} className="rounded-md bg-[#e9f5d0] grid grid-cols-3 grid-rows-3 p-[3px] gap-[1px] overflow-hidden shrink-0">
      {[0, 1, 0, 1, 1, 1, 0, 1, 0].map((v, i) => <span key={i} className={v ? 'bg-[#9ccc65]' : ''} />)}
    </div>
  )
}

export function Button({ children, variant = 'secondary', size = 'md', className, ...p }: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'secondary' | 'ghost' | 'danger' | 'danger-outline'; size?: 'sm' | 'md' | 'lg' }) {
  return (
    <button {...p} className={cx(
      'inline-flex items-center justify-center gap-1.5 rounded-lg font-medium whitespace-nowrap disabled:opacity-50 disabled:pointer-events-none',
      size === 'sm' && 'h-8 px-3 text-[12.5px]', size === 'md' && 'h-9 px-3.5 text-[13px]', size === 'lg' && 'h-10 px-5 text-[13.5px]',
      variant === 'primary' && 'btn-primary text-white',
      variant === 'secondary' && 'btn-secondary text-fg',
      variant === 'ghost' && 'text-fg2 hover:bg-ov/5',
      variant === 'danger' && 'btn-danger',
      variant === 'danger-outline' && 'btn-secondary !text-[#ef4444]',
      className)}>{children}</button>
  )
}

export function Card({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cx('rounded-2xl surface', className)}>{children}</div>
}

export function Tabs({ items, value, onChange, className }: { items: { k: string; l: string; icon?: React.ReactNode }[]; value: string; onChange: (k: string) => void; className?: string }) {
  return (
    <div className={cx('flex flex-wrap gap-2 pb-4 border-b-2 border-dashed border-line', className)}>
      {items.map(t => (
        <button key={t.k} onClick={() => onChange(t.k)} className={cx('h-9 px-3 inline-flex items-center gap-2 text-[13px] font-semibold rounded-lg whitespace-nowrap transition-all',
          value === t.k ? 'nav-active' : 'text-muted border-2 border-transparent hover:text-fg hover:border-line2')}>
          {t.icon}{t.l}
        </button>
      ))}
    </div>
  )
}

export function Segment({ items, value }: { items: { k: string; l: string; icon?: React.ReactNode }[]; value: string }) {
  return (
    <div className="inline-flex rounded-lg field p-0.5 gap-0.5">
      {items.map(t => <span key={t.k} className={cx('h-8 px-3 rounded-md inline-flex items-center gap-2 text-[13px] font-medium', value === t.k ? 'nav-active text-fg' : 'text-muted')}>{t.icon}{t.l}</span>)}
    </div>
  )
}

const DOT: Record<string, string> = { running: 'bg-emerald-400 text-emerald-400', online: 'bg-emerald-400 text-emerald-400', active: 'bg-emerald-400 text-emerald-400', done: 'bg-emerald-400 text-emerald-400', deploying: 'bg-amber-400 text-amber-400', pending: 'bg-amber-400 text-amber-400', provisioning: 'bg-amber-400 text-amber-400', draining: 'bg-amber-400 text-amber-400', stopped: 'bg-zinc-500 text-zinc-500', unknown: 'bg-zinc-500 text-zinc-500', offline: 'bg-zinc-600 text-zinc-600', failed: 'bg-rose-500 text-rose-500', error: 'bg-rose-500 text-rose-500' }
export function Dot({ status }: { status: string }) {
  const c = DOT[status] ?? DOT.unknown
  const glow = !['stopped', 'offline', 'unknown'].includes(status) && !!DOT[status]
  return <span className={cx('inline-block h-[7px] w-[7px] rounded-full shrink-0', c, glow && 'dot-glow')} />
}

export function Spark({ data, color, w = 72, h = 26 }: { data: number[]; color: string; w?: number; h?: number }) {
  if (data.length < 2) return <svg width={w} height={h}><path d={`M0,${h - 1} L${w},${h - 1}`} stroke={color} strokeWidth="1.5" strokeDasharray="3 3" /></svg>
  const mxRaw = Math.max(...data); if (!mxRaw) data = data.map(() => 0.0001)
  const mx = Math.max(...data) * 1.15, st = w / (data.length - 1)
  const d = smooth(data.map((v, i) => [i * st, h - (v / mx) * h]))
  return <svg width={w} height={h} className="overflow-visible"><path d={d} fill="none" stroke={color} strokeWidth="1.5" /></svg>
}

export function Ticks({ value, color, n = 44 }: { value: number; color?: string; n?: number }) {
  color = color ?? (value >= 80 ? '#f43f5e' : value >= 60 ? '#f59e0b' : '#60a5fa')
  const on = Math.round((value / 100) * n)
  return (
    <div className="flex gap-[2px] items-center h-3 min-w-0 max-w-full">
      {Array.from({ length: n }, (_, i) => <span key={i} className="flex-1 min-w-px max-w-[4px] w-[4px] h-[12px] rounded-[1px]" style={{ background: i < Math.max(on, 1) ? color : 'var(--tick-off)',  }} />)}
    </div>
  )
}

function Glyph({ path, size, color }: { path: string; size: number; color: string }) {
  return <svg viewBox="0 0 24 24" width={size} height={size} fill={color}><path d={path} /></svg>
}
const DB: Record<string, { path: string; hex: string }> = { postgres: si.siPostgresql, mysql: si.siMysql, redis: si.siRedis, mongo: si.siMongodb }

export function ServiceIcon({ s, size = 28 }: { s: IconSpec; size?: number }) {
  if (DB[s.kind]) {
    const ic = DB[s.kind]
    return (
      <div style={{ width: size, height: size, background: `color-mix(in srgb, #${ic.hex} 22%, var(--card))` }} className="tile rounded-[8px] grid place-items-center shrink-0">
        <Glyph path={ic.path} size={size * 0.52} color={s.kind === 'mysql' ? '#5fb3d4' : `#${ic.hex}`} />
      </div>
    )
  }
  return (
    <div style={{ width: size, height: size, background: `color-mix(in srgb, ${s.color ?? '#8b5cf6'} 38%, var(--card))` }} className="tile rounded-[8px] grid place-items-center shrink-0">
      <span style={{ fontSize: size * 0.44 }} className="font-extrabold font-mono text-fg">{s.letter}</span>
    </div>
  )
}

const PROVIDER: Record<string, { path?: string; hex: string; ring?: boolean; letter?: string }> = {
  Oracle: { hex: 'F80000', ring: true },
  Hetzner: { hex: 'D50C2D', path: si.siHetzner.path },
  DigitalOcean: { hex: '0080FF', path: si.siDigitalocean.path },
  Vultr: { hex: '007BFC', path: si.siVultr.path },
  RackNerd: { hex: '22C55E', letter: 'R' },
}
function ProviderGlyph({ provider, size }: { provider: string; size: number }) {
  // provider is optional on servers (pages pass '?' when unset): show a generic server glyph, not a stray "?"
  if (!PROVIDER[provider] && !/^[A-Za-z0-9]/.test(provider)) return <ServerGlyph size={size} strokeWidth={2.2} className="text-muted" />
  const p = PROVIDER[provider] ?? { hex: '888888', letter: provider[0].toUpperCase() }
  if (p.ring) return <span className="rounded-full" style={{ width: size, height: size * 0.6, border: `${Math.max(2, size * 0.15)}px solid #${p.hex}` }} />
  if (p.path) return <Glyph path={p.path} size={size} color={`#${p.hex}`} />
  return <span className="font-bold" style={{ color: `#${p.hex}`, fontSize: size * 0.9, lineHeight: 1 }}>{p.letter}</span>
}

export function ProviderIcon({ provider, size = 40, online }: { provider: string; color?: string; size?: number; online?: boolean }) {
  return (
    <div className="relative shrink-0" style={{ width: size, height: size }}>
      <div className="tile w-full h-full rounded-[10px] grid place-items-center bg-card2">
        <ProviderGlyph provider={provider} size={size * 0.48} />
      </div>
      {online !== undefined && <span className={cx('absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full ring-2 ring-[var(--edge)]', online ? 'bg-emerald-400' : 'bg-zinc-600')} />}
    </div>
  )
}

export function Mini({ provider, size = 14 }: { provider: string; color?: string; size?: number }) {
  return <span className="inline-grid place-items-center shrink-0" style={{ width: size, height: size }}><ProviderGlyph provider={provider} size={size} /></span>
}

export function Brand({ icon, size = 16, color }: { icon: { path: string; hex: string }; size?: number; color?: string }) {
  return <Glyph path={icon.path} size={size} color={color ?? `#${icon.hex}`} />
}

export function CopyId({ v }: { v: string }) {
  return <button type="button" title="复制" onClick={() => navigator.clipboard?.writeText(v)} className="inline-flex items-center gap-1.5 font-mono text-[12px] text-muted hover:text-fg">{v}<Copy size={13} className="text-muted" /></button>
}

export function Badge({ children, tone = 'amber' }: { children: React.ReactNode; tone?: 'amber' | 'green' | 'violet' | 'zinc' | 'red' }) {
  return <span className={cx('inline-flex items-center gap-1 whitespace-nowrap shrink-0 rounded-[3px] px-2 h-[22px] text-[11.5px] font-bold font-mono italic border-[1.5px] border-[var(--edge)] shadow-[2px_2px_0_0_var(--edge)]',
    tone === 'amber' && 'bg-[#fcd34d] text-[#141414]',
    tone === 'green' && 'bg-[#86efac] text-[#141414]',
    tone === 'violet' && 'bg-[#c4b5fd] text-[#141414]',
    tone === 'red' && 'bg-[#fca5a5] text-[#141414]',
    tone === 'zinc' && 'bg-card2 text-fg2')}>{children}</span>
}

function smooth(pts: number[][]) {
  let d = `M${pts[0][0]},${pts[0][1]}`
  for (let i = 0; i < pts.length - 1; i++) {
    const p0 = pts[i - 1] ?? pts[i], p1 = pts[i], p2 = pts[i + 1], p3 = pts[i + 2] ?? p2
    const c1 = [p1[0] + (p2[0] - p0[0]) / 6, p1[1] + (p2[1] - p0[1]) / 6], c2 = [p2[0] - (p3[0] - p1[0]) / 6, p2[1] - (p3[1] - p1[1]) / 6]
    d += ` C${c1[0].toFixed(1)},${c1[1].toFixed(1)} ${c2[0].toFixed(1)},${c2[1].toFixed(1)} ${p2[0].toFixed(1)},${p2[1].toFixed(1)}`
  }
  return d
}

export function Chart({ title, icon, data, color = '#8b5cf6', unit = '%', labels = ['12:24', '14:48', '17:12', '19:36', '22:00', '00:24'], h = 160, max }: { title: string; icon?: React.ReactNode; data: number[]; color?: string; unit?: string; labels?: string[]; h?: number; max?: number }) {
  const w = 600
  if (data.length < 2) return (
    <Card className="overflow-hidden"><div className="px-5 pt-4 flex items-center gap-1.5 text-[12.5px] text-muted">{icon}{title}</div>
      <div className="px-5 pb-5 pt-3"><div style={{ height: h + 50 }} className="rounded-lg border-2 border-dashed border-line2 grid place-items-center text-[13px] text-dim">暂无数据，Agent 每 15 秒上报一次</div></div></Card>
  )
  const top = max ?? (Math.ceil(Math.max(...data) * 1.3) || 1)
  const step = w / (data.length - 1)
  const pts = data.map((v, i) => [i * step, h - (v / top) * h * 0.92])
  const d = smooth(pts)
  const id = 'c' + title.length + color.slice(1)
  const last = data[data.length - 1]
  return (
    <Card className="overflow-hidden">
      <div className="px-5 pt-4 flex items-start">
        <div><div className="flex items-center gap-1.5 text-[12.5px] text-muted">{icon}{title}</div>
          <div className="mt-1 text-[22px] font-semibold tracking-tight tnum">{last.toFixed(1)}<span className="text-[13px] text-muted font-normal ml-0.5">{unit}</span></div></div>
        <span className="ml-auto text-[11.5px] text-dim mt-0.5 tnum">峰值 {Math.max(...data).toFixed(1)}{unit}</span>
      </div>
      <div className="px-5 pt-3 pb-3">
        <div className="relative">
          <div className="absolute inset-0 flex flex-col justify-between pointer-events-none">{[0, 1, 2, 3].map(i => <div key={i} className="border-t border-ov/[0.04]" />)}</div>
          <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className="w-full relative overflow-visible" style={{ height: h }}>
            <defs><linearGradient id={id} x1="0" x2="0" y1="0" y2="1"><stop offset="0" stopColor={color} stopOpacity=".35" /><stop offset="1" stopColor={color} stopOpacity=".05" /></linearGradient></defs>
            <path d={`${d} L${w},${h} L0,${h} Z`} fill={`url(#${id})`} />
            <path d={d} fill="none" stroke={color} strokeWidth="2.2" vectorEffect="non-scaling-stroke" />
          </svg>
        </div>
        <div className="flex justify-between text-[11px] text-dim mt-2 tnum">{labels.map((l, i) => <span key={i}>{l}</span>)}</div>
      </div>
    </Card>
  )
}

