import { useEffect, useRef, useState } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import { Menu, X, Search, Star, Package, Server, Globe, ArrowLeft, Settings, Plus, Copy, Terminal, Sun, Moon, ShieldAlert, ArrowRightLeft, LogOut, SlidersHorizontal, KeyRound, AlertTriangle, ServerCog, GripVertical } from 'lucide-react'
import { setLang, useT, type Lang } from '../i18n'
import { applyTheme, currentTheme, type Theme } from '../theme'
import { Avatar, Logo, ServiceIcon, Mini, Dot } from './ui'
import { api, isDemo } from '../api/client'
import { run, useQuery } from '../api/hooks'
import { projIcon, svcIcon, svcName, projName } from '../api/view'
import { useUpdatesByService } from '../api/updates'
import { UpdateBadge } from './ImageUpdateCard'
import type { Project } from '../api/types'
import { cx } from './ui-utils'
import { usePending, useProblems, useProjects } from '../api/queries'
import { useProjectPrefs } from '../api/prefs'
import { StarButton } from './ProjectPrefs'
import { FilterChips } from './StatusFilter'
import { FILTER_PARAM, serviceFilters, useStatusFilter } from '../api/filters'
import { useFiringProblems } from '../api/problems'
import { providerOf } from '../api/providers'
import { openPalette } from './palette'

const useMe = () => useQuery('me', api.me)
const useServers = () => useQuery('servers', api.servers, { interval: 30000 })

function Team() {
  const me = useMe().data
  return (
    <div className="w-full flex items-center gap-2.5 h-11 px-2">
      <Avatar size={28} name={me?.username} /><span className="text-[13.5px] font-semibold truncate">{me?.username ?? '…'}</span>
    </div>
  )
}
/** zh / en switch (only some screens are translated so far). */
export function LangToggle() {
  const { lang } = useT()
  return (
    <div className="inline-flex rounded-lg field p-[3px] gap-0.5">
      {(['zh', 'en'] as Lang[]).map(v => (
        <button key={v} onClick={() => setLang(v)} title={v === 'zh' ? '中文' : 'English'} className={cx('h-7 px-2 grid place-items-center rounded-md text-[11.5px] font-bold font-mono transition-colors', lang === v ? 'bg-[#fde047] text-[#141414] border-2 border-[var(--edge)]' : 'text-dim hover:text-fg')}>
          {v === 'zh' ? '中' : 'EN'}
        </button>
      ))}
    </div>
  )
}
export function ThemeToggle() {
  const [t, setT] = useState<Theme>(currentTheme())
  const set = (v: Theme) => { applyTheme(v); setT(v) }
  return (
    <div className="inline-flex rounded-lg field p-[3px] gap-0.5">
      {(['light', 'dark'] as Theme[]).map(v => (
        <button key={v} onClick={() => set(v)} title={v === 'light' ? '明亮模式' : '黑夜模式'} className={cx('h-7 w-7 grid place-items-center rounded-md transition-colors', t === v ? 'bg-[#fde047] text-[#141414] border-2 border-[var(--edge)]' : 'text-dim hover:text-fg')}>
          {v === 'light' ? <Sun size={14} /> : <Moon size={14} />}
        </button>
      ))}
    </div>
  )
}
function UserRow() {
  const me = useMe().data
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const down = (e: MouseEvent) => { if (!ref.current?.contains(e.target as Node)) setOpen(false) }
    const key = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false) }
    addEventListener('mousedown', down); addEventListener('keydown', key)
    return () => { removeEventListener('mousedown', down); removeEventListener('keydown', key) }
  }, [open])
  const logout = async () => { await api.logout().catch(() => {}); location.hash = '#/login' }
  const row = 'w-full flex items-center gap-2.5 h-9 px-3 rounded-lg text-[13px] font-medium text-fg2 hover:bg-ov/5 text-left'
  return (
    <div ref={ref} className="relative flex items-center gap-2.5 px-1">
      <button onClick={() => setOpen(v => !v)} aria-haspopup="menu" aria-expanded={open} title="账户" className="flex items-center gap-2.5 min-w-0 rounded-lg hover:bg-ov/5 -mx-1 px-1 py-0.5">
        <span className="tile h-8 w-8 shrink-0 rounded-lg bg-[#fde047] grid place-items-center text-[13px] font-extrabold font-mono text-[#141414]">{(me?.username ?? 'A')[0].toUpperCase()}</span>
        <span className="min-w-0 leading-tight text-left"><span className="block text-[13px] font-medium truncate">{me?.username ?? '…'}</span><span className="block text-[11.5px] text-dim">{isDemo() ? '演示模式' : '管理员'}</span></span>
      </button>
      <div className="ml-auto"><ThemeToggle /></div>
      {open && (
        <div role="menu" className="rise surface bg-pop absolute left-0 bottom-full mb-2 w-[220px] p-1.5 z-50">
          <Link role="menuitem" to="/settings/security" onClick={() => setOpen(false)} className={row}><KeyRound size={15} />账户与安全</Link>
          <Link role="menuitem" to="/settings" onClick={() => setOpen(false)} className={row}><SlidersHorizontal size={15} />设置</Link>
          <div className="h-px bg-line my-1" />
          <button role="menuitem" onClick={logout} className={cx(row, 'text-[#ef4444]')}><LogOut size={15} />退出登录</button>
        </div>
      )}
    </div>
  )
}
const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
function SearchBox() {
  return (
    <button onClick={openPalette} className="w-full flex items-center gap-2 h-10 rounded-lg border border-line field px-3 text-[13px] text-muted hover:text-fg">
      <Search size={15} />搜索<span className="ml-auto text-[11px] rounded border border-line2 px-1.5 py-0.5 text-dim">{mac ? '⌘ K' : 'Ctrl K'}</span>
    </button>
  )
}


/** App frame: the sidebar is a fixed column from lg up; below that it becomes an off-canvas drawer
 *  opened from a sticky top bar, so phones and portrait tablets keep the full width for content. */
function Frame({ side, top, children, pad = 'pt-7' }: { side: React.ReactNode; top: React.ReactNode; children: React.ReactNode; pad?: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="h-full flex topline">
      {open && <div className="fixed inset-0 z-40 bg-black/50 lg:hidden" onClick={() => setOpen(false)} />}
      <aside onClick={e => { if ((e.target as HTMLElement).closest('a')) setOpen(false) }}
        className={cx('w-[248px] shrink-0 bg-side border-r border-line flex flex-col px-4 pb-4 overflow-y-auto', pad,
          'max-lg:fixed max-lg:inset-y-0 max-lg:left-0 max-lg:z-50 max-lg:w-[min(280px,86vw)] max-lg:border-r-2 max-lg:border-[var(--edge)] max-lg:transition-transform max-lg:duration-200',
          open ? 'max-lg:translate-x-0 max-lg:shadow-[6px_0_0_0_var(--edge)]' : 'max-lg:-translate-x-full max-lg:invisible')}>
        <button onClick={() => setOpen(false)} title="关闭菜单" className="lg:hidden absolute right-3 top-5 h-8 w-8 grid place-items-center rounded-md text-muted hover:text-fg"><X size={18} /></button>
        {side}
      </aside>
      <div className="flex-1 min-w-0 flex flex-col">
        <header className="lg:hidden shrink-0 h-[60px] pt-1 flex items-center gap-3 px-3 bg-side border-b-2 border-[var(--edge)]">
          <button onClick={() => setOpen(true)} title="打开菜单" aria-label="打开菜单" className="chip h-9 w-9 grid place-items-center bg-[var(--card)] text-fg shrink-0"><Menu size={18} /></button>
          <div className="min-w-0 flex-1 flex items-center gap-2">{top}</div>
        </header>
        <main className="flex-1 min-h-0 overflow-auto canvas @container">{children}</main>
      </div>
    </div>
  )
}

/** Sidebar quick access to the account's favorite projects (收藏). */
function Favorites() {
  const { t } = useT()
  const prefs = useProjectPrefs()
  const projects = useProjects().data ?? []
  const loc = useLocation()
  const favs = projects.filter(p => prefs.isFav(p.id))
  if (!favs.length) return null
  const max = 8
  return (
    <div className="mt-4">
      <div className="h-px bg-line mb-4" />
      <Link to="/?filter=fav" className="px-3 mb-1.5 flex items-center gap-1.5 text-[12px] font-semibold text-dim tracking-wider hover:text-fg2"><Star size={12} />{t('收藏', 'Favorites')}<span className="ml-auto font-mono tnum">{favs.length}</span></Link>
      <div className="flex flex-col gap-0.5">
        {favs.slice(0, max).map(p => {
          const on = loc.pathname.startsWith(`/projects/${p.id}`)
          return <Link key={p.id} to={`/projects/${p.id}`} className={cx('flex items-center gap-2.5 h-9 px-2 rounded-lg text-[13px]', on ? 'nav-active' : 'text-fg2 hover:bg-ov/5')}>
            <ServiceIcon s={projIcon(p)} size={22} /><span className="truncate flex-1 font-medium">{projName(p)}</span>
          </Link>
        })}
        {favs.length > max && <Link to="/?filter=fav" className="h-8 px-3 flex items-center text-[12.5px] text-accent hover:underline">{t(`查看全部 ${favs.length} 个`, `All ${favs.length}`)}</Link>}
      </div>
    </div>
  )
}

export function MainShell({ children }: { children: React.ReactNode }) {
  const servers = useServers().data
  const pending = usePending().data?.length ?? 0
  // the control host is not a worker: it has its own 中控 entry, never counted as a server
  const workers = (servers ?? []).filter(s => s.role !== 'control')
  const online = workers.filter(s => s.status === 'online').length
  const problems = useFiringProblems().length
  const item = (to: string, icon: React.ReactNode, l: string, badge?: number) => (
    <NavLink to={to} end={to === '/'} className={({ isActive }) => cx('flex items-center gap-3 h-9 px-3 rounded-lg text-[13.5px] font-medium',
      isActive ? 'nav-active text-fg' : 'text-fg2 hover:bg-ov/5')}>{icon}{l}
      {!!badge && <span className="ml-auto min-w-[22px] h-[20px] px-1.5 rounded-[3px] bg-[#fca5a5] text-[#141414] border-[1.5px] border-[var(--edge)] text-[11px] font-bold font-mono not-italic grid place-items-center tnum">{badge}</span>}</NavLink>
  )
  return (
    <Frame top={<><Link to="/" className="shrink-0"><Logo /></Link>{!!pending && <Link to="/confirmations" className="ml-auto mr-1 min-w-[22px] h-[20px] px-1.5 rounded-[3px] bg-[#fca5a5] text-[#141414] border-[1.5px] border-[var(--edge)] text-[11px] font-bold font-mono grid place-items-center tnum" title="待确认">{pending}</Link>}</>} side={<>
        <Link to="/" className="px-2 mb-6"><Logo /></Link>
        <Team />
        <div className="mt-3"><SearchBox /></div>
        <div className="h-px bg-line my-4" />
        <nav className="flex flex-col gap-1">
          {item('/', <Package size={16} />, '项目')}
          {item('/servers', <Server size={16} />, '服务器')}
          {item('/control', <ServerCog size={16} />, '中控')}
          {item('/domains', <Globe size={16} />, '域名')}
          {item('/developer', <Terminal size={16} />, 'CLI 与 API')}
          {item('/confirmations', <ShieldAlert size={16} />, '待确认', pending)}
          {item('/import', <ArrowRightLeft size={16} />, '从 Zeabur 迁移')}
          {item('/settings', <SlidersHorizontal size={16} />, '设置')}
        </nav>
        <Favorites />
        <div className="flex-1" />
        {!!problems && <Link to="/" className="rounded-xl surface !bg-[#fca5a5] text-[#141414] px-3 py-2.5 mb-2 text-[12.5px] font-bold flex items-center gap-2"><AlertTriangle size={14} />{problems} 个异常需要处理</Link>}
        <Link to="/control" className="rounded-xl surface surface-hover px-3 py-2.5 mb-3 text-[12px] text-muted flex items-center gap-2"><Dot status={servers ? 'online' : 'unknown'} />{servers ? `中控在线 · ${online}/${workers.length} 服务器已连接` : '正在连接中控…'}</Link>
        <div className="h-px bg-line mb-3" />
        <UserRow />
    </>}>{children}</Frame>
  )
}

export function ProjectShell({ project, active, children }: { project: Project; active?: string; children: React.ReactNode }) {
  const nav = useNavigate()
  const loc = useLocation()
  const srv = useServers().data?.find(s => s.id === project.server_id)
  const { t } = useT()
  const prefs = useProjectPrefs()
  const services = project.services ?? []
  const updates = useUpdatesByService()
  const defs = serviceFilters((useProblems().data ?? []).filter(p => !p.resolved_at))
  const f = useStatusFilter(services, defs)
  // keep the chosen chip when moving between services, so a filtered view can be shared too
  const keep = f.active ? `?${FILTER_PARAM}=${f.active}` : ''
  const add = () => { const q = new URLSearchParams(active ? loc.search : ''); q.set('add', '1'); nav(`${active ? `/projects/${project.id}/services/${active}` : `/projects/${project.id}`}?${q}`) }
  return (
    <Frame pad="pt-6" top={<><Link to="/" title="项目" className="shrink-0 text-fg2 hover:text-fg"><ArrowLeft size={18} /></Link><span className="text-[15px] font-semibold tracking-tight truncate flex-1 min-w-0">{projName(project)}</span>{project.created_at && <StarButton id={project.id} prefs={prefs} />}</>} side={<>
        <Link to="/" className="flex items-center gap-2 text-[13px] text-fg2 px-2 h-8 hover:text-fg"><ArrowLeft size={16} />项目</Link>
        <div className="px-2 mt-3">
          <div className="flex items-center gap-1"><h1 className="text-[17px] font-semibold tracking-tight truncate flex-1 min-w-0">{projName(project)}</h1>{project.created_at && <StarButton id={project.id} prefs={prefs} className="-mr-1.5" />}</div>
          {srv ? <Link to={`/servers/${srv.id}`} className="mt-1.5 flex items-center gap-2 text-[13px] text-muted hover:text-fg"><Mini provider={providerOf(srv)} /><span className="truncate">{srv.name}</span><Dot status={srv.status} /></Link>
            : <div className="mt-1.5 h-5 w-40 rounded bg-ov/[0.07] animate-pulse" />}
        </div>
        <div className="mt-4 flex flex-col gap-1">
          <NavLink to={`/projects/${project.id}/settings`} className={({ isActive }) => cx('flex items-center gap-3 h-9 px-3 rounded-lg text-[13px] font-medium', isActive ? 'nav-active' : 'text-fg2 hover:bg-ov/5')}><Settings size={16} />项目设置</NavLink>
          <button onClick={add} className="flex items-center gap-3 h-9 px-3 rounded-lg text-[13px] font-medium text-accent hover:bg-ov/5"><Plus size={16} />新建服务</button>
        </div>
        <div className="h-px bg-line my-4" />
        <div className="px-2 text-[12px] font-semibold text-dim tracking-wider mb-2">服务</div>
        {services.length > 1 && <FilterChips f={f} defs={defs} compact className="px-1 mb-2" />}
        <div className="flex flex-col gap-1 overflow-auto">
          {!services.length && <div className="px-2 text-[12.5px] text-dim">还没有服务</div>}
          {!!services.length && !f.list.length && f.def && <div className="px-2 py-1 text-[12.5px] text-dim">{t(`没有「${f.def.zh}」的服务`, `No services match “${f.def.en}”`)} · <button onClick={() => f.set('')} className="text-accent hover:underline">{t('显示全部', 'Show all')}</button></div>}
          <SortableServices project={project} list={f.list} sortable={!f.active && services.length > 1} render={(s, grip) => (
            <Link to={`/projects/${project.id}/services/${s.id}${keep}`} className={cx('group flex items-center gap-3 h-10 px-2 rounded-lg', active === s.id ? 'nav-active' : 'hover:bg-ov/5')}>
              {grip}<ServiceIcon s={svcIcon(s)} size={26} /><span className="text-[13px] font-medium truncate flex-1">{svcName(s)}</span><UpdateBadge u={updates[s.id]} compact /><Dot status={s.status} />
            </Link>
          )} />
        </div>
        <div className="flex-1" />
        <div className="h-px bg-line mb-3" />
        <div className="px-2 flex items-center gap-2"><div className="min-w-0"><div className="text-[11.5px] text-dim">项目 ID</div>
        <button onClick={() => navigator.clipboard?.writeText(project.id)} className="mt-1 flex items-center gap-1.5 font-mono text-[11.5px] text-muted hover:text-fg max-w-full"><span className="truncate">{project.id}</span><Copy size={11} className="shrink-0" /></button></div><div className="ml-auto"><ThemeToggle /></div></div>
    </>}>{children}</Frame>
  )
}

type Svc = NonNullable<Project['services']>[number]

/** The project sidebar's services, reorderable by dragging the grip (mouse, pen or touch: pointer
 *  events, so it works on tablets where HTML drag-and-drop does not). The order is saved for the
 *  project (PUT /projects/{id}/service-order). Off while a status filter narrows the list. */
function SortableServices({ project, list, sortable, render }: { project: Project; list: Svc[]; sortable: boolean; render: (s: Svc, grip: React.ReactNode) => React.ReactNode }) {
  const { t } = useT()
  const ids = list.map(s => s.id).join(',')
  // a local order only lives until the server's list changes (saved, or another device reordered)
  const [local, setLocal] = useState<{ base: string; ids: string[] } | null>(null)
  const order = local && local.base === ids ? local.ids : null
  const setOrder = (o: string[] | null) => setLocal(o ? { base: ids, ids: o } : null)
  const [dragId, setDragId] = useState<string | null>(null)
  const rows = useRef(new Map<string, HTMLDivElement>())
  const start = useRef<string>('')
  const byId = new Map(list.map(s => [s.id, s]))
  const shown = order ? order.map(id => byId.get(id)).filter((s): s is Svc => !!s) : list
  const move = (y: number) => {
    if (!dragId) return
    const cur = shown.map(s => s.id)
    let to = cur.length - 1
    for (let i = 0; i < cur.length; i++) {
      const r = rows.current.get(cur[i])?.getBoundingClientRect()
      if (r && y < r.top + r.height / 2) { to = i; break }
    }
    const from = cur.indexOf(dragId)
    if (from < 0) return
    if (to > from) to--
    if (to === from) return
    cur.splice(from, 1); cur.splice(to, 0, dragId)
    setOrder(cur)
  }
  const end = () => {
    if (!dragId) return
    setDragId(null)
    const next = shown.map(s => s.id)
    if (next.join(',') === start.current) return
    void run(() => api.setServiceOrder(project.id, next), { invalidate: ['project'] }).then(r => { if (!r) setOrder(null) })
  }
  return <>{shown.map(s => {
    const grip = sortable ? (
      <span role="button" aria-label={t('拖动排序', 'Drag to reorder')} title={t('拖动排序', 'Drag to reorder')}
        className={cx('-ml-1 -mr-2 shrink-0 cursor-grab touch-none select-none text-dim transition-opacity', dragId === s.id ? 'opacity-100 cursor-grabbing' : 'opacity-40 md:opacity-0 md:group-hover:opacity-60')}
        onClick={e => { e.preventDefault(); e.stopPropagation() }}
        onPointerDown={e => { e.preventDefault(); e.stopPropagation(); (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId); start.current = shown.map(x => x.id).join(','); setDragId(s.id) }}
        onPointerMove={e => { if (dragId === s.id) move(e.clientY) }}
        onPointerUp={end} onPointerCancel={end}><GripVertical size={14} /></span>
    ) : null
    return <div key={s.id} ref={el => { if (el) rows.current.set(s.id, el); else rows.current.delete(s.id) }} className={cx(dragId === s.id && 'opacity-70 ring-1 ring-accent/50 rounded-lg')}>{render(s, grip)}</div>
  })}</>
}
