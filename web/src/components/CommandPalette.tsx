import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Search, Package, Server, Globe, ScrollText, RotateCw, CornerDownLeft, Box, ArrowRight } from 'lucide-react'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import { useProjects } from '../api/queries'
import { ConfirmDialog } from './feedback'
import { cx } from './ui-utils'
import { OPEN_PALETTE, score } from './palette'

type Item = { id: string; group: string; label: string; hint?: string; icon: React.ReactNode; to?: string; restart?: { id: string; name: string } }

const pages: [string, string][] = [
  ['/', '项目'], ['/servers', '服务器'], ['/control', '中控'], ['/domains', '域名'], ['/developer', 'CLI 与 API'],
  ['/confirmations', '待确认'], ['/import', '从 Zeabur 迁移'], ['/settings', '设置'], ['/settings/security', '账户与安全'],
]

export function CommandPalette() {
  const [open, setOpen] = useState(false)
  const onLogin = useLocation().pathname === '/login'
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setOpen(v => !v) }
    }
    const ev = () => setOpen(true)
    addEventListener('keydown', key); addEventListener(OPEN_PALETTE, ev)
    return () => { removeEventListener('keydown', key); removeEventListener(OPEN_PALETTE, ev) }
  }, [])
  if (!open || onLogin) return null
  return <Palette onClose={() => setOpen(false)} />
}

function Palette({ onClose }: { onClose: () => void }) {
  const nav = useNavigate()
  const projects = useProjects().data
  const servers = useQuery('servers', api.servers).data
  const domains = useQuery('domains', api.domains).data
  const [q, setQ] = useState('')
  const [sel, setSel] = useState(0)
  const [restart, setRestart] = useState<{ id: string; name: string }>()
  const list = useRef<HTMLDivElement>(null)

  const items = useMemo(() => {
    const out: Item[] = pages.map(([to, l]) => ({ id: `page:${to}`, group: '页面', label: l, icon: <ArrowRight size={15} />, to }))
    const svcPath = new Map<string, { path: string; name: string }>()
    for (const p of projects ?? []) {
      out.push({ id: `p:${p.id}`, group: '项目', label: p.name, icon: <Package size={15} />, to: `/projects/${p.id}` })
      for (const s of p.services ?? []) {
        const path = `/projects/${p.id}/services/${s.id}`
        svcPath.set(s.id, { path, name: s.name })
        out.push({ id: `s:${s.id}`, group: '服务', label: s.name, hint: p.name, icon: <Box size={15} />, to: path })
        out.push({ id: `l:${s.id}`, group: '操作', label: `查看日志 ${s.name}`, hint: p.name, icon: <ScrollText size={15} />, to: `${path}/logs` })
        out.push({ id: `r:${s.id}`, group: '操作', label: `重新启动 ${s.name}`, hint: p.name, icon: <RotateCw size={15} />, restart: { id: s.id, name: s.name } })
      }
    }
    for (const s of servers ?? []) if (s.role !== 'control') out.push({ id: `srv:${s.id}`, group: '服务器', label: s.name, hint: s.public_ipv4, icon: <Server size={15} />, to: `/servers/${s.id}` })
    for (const d of domains ?? []) {
      const s = svcPath.get(d.service_id)
      out.push({ id: `d:${d.id}`, group: '域名', label: d.hostname, hint: s?.name, icon: <Globe size={15} />, to: s?.path ?? '/domains' })
    }
    return out
  }, [projects, servers, domains])

  const shown = useMemo(() => {
    const term = q.trim()
    if (!term) return items.filter(x => x.group !== '操作').slice(0, 40)
    const hits = items.map(x => ({ x, s: score(`${x.label} ${x.hint ?? ''}`, term) })).filter(r => r.s >= 0)
    // keep each group together, groups ordered by their best hit
    const best = new Map<string, number>()
    for (const r of hits) best.set(r.x.group, Math.min(best.get(r.x.group) ?? Infinity, r.s))
    return hits.sort((a, b) => best.get(a.x.group)! - best.get(b.x.group)! || a.x.group.localeCompare(b.x.group) || a.s - b.s)
      .slice(0, 40).map(r => r.x)
  }, [items, q])

  useEffect(() => { list.current?.querySelector(`[data-i="${sel}"]`)?.scrollIntoView({ block: 'nearest' }) }, [sel])

  const pick = (x?: Item) => {
    if (!x) return
    if (x.restart) { setRestart(x.restart); return }
    if (x.to) nav(x.to)
    onClose()
  }
  const key = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setSel(i => Math.min(i + 1, shown.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setSel(i => Math.max(i - 1, 0)) }
    else if (e.key === 'Enter') { e.preventDefault(); pick(shown[sel]) }
    else if (e.key === 'Escape') onClose()
  }

  if (restart) return <ConfirmDialog title="重新启动服务" action="重新启动" danger={false} onClose={onClose}
    description={<>重启 {restart.name} 的容器，期间会短暂不可访问。</>}
    onConfirm={() => run(() => api.action(restart.id, 'restart'), { ok: '正在重新启动', invalidate: [`deployments:${restart.id}`] })} />

  return (
    <div className="fixed inset-0 z-[60] bg-black/65 flex items-start justify-center pt-[12vh]" onClick={onClose}>
      <div role="dialog" aria-label="命令面板" className="rise surface bg-pop w-[600px] max-w-[calc(100vw-32px)] overflow-hidden flex flex-col max-h-[70vh]" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-3 px-4 h-14 border-b-2 border-[var(--edge)] shrink-0">
          <Search size={17} className="text-muted" />
          <input autoFocus value={q} onChange={e => { setQ(e.target.value); setSel(0) }} onKeyDown={key} placeholder="搜索项目、服务、服务器、域名或操作…"
            className="flex-1 bg-transparent outline-none text-[14px] placeholder:text-dim" />
          <span className="text-[11px] rounded border border-line2 px-1.5 py-0.5 text-dim">Esc</span>
        </div>
        <div ref={list} className="overflow-auto p-2">
          {!shown.length && <div className="px-3 py-8 text-center text-[13px] text-dim">{projects ? '没有匹配的结果' : '正在加载…'}</div>}
          {shown.map((x, i) => {
            const head = x.group !== shown[i - 1]?.group
            return (
              <div key={x.id}>
                {head && <div className="px-3 pt-2 pb-1 text-[11.5px] font-semibold text-dim tracking-wider">{x.group}</div>}
                <button data-i={i} onMouseMove={() => setSel(i)} onClick={() => pick(x)}
                  className={cx('w-full flex items-center gap-3 h-10 px-3 rounded-lg text-left text-[13.5px]', i === sel ? 'bg-ov/[0.08]' : 'hover:bg-ov/[0.04]')}>
                  <span className="text-muted shrink-0">{x.icon}</span>
                  <span className="truncate font-medium">{x.label}</span>
                  {x.hint && <span className="truncate text-[12.5px] text-dim">{x.hint}</span>}
                  {i === sel && <CornerDownLeft size={13} className="ml-auto shrink-0 text-dim" />}
                </button>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}
