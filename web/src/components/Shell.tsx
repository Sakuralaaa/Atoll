import { useState } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import { Menu, X, Search, Package, Server, Globe, ChevronsUpDown, ArrowLeft, Settings, Plus, Copy, Bell, Terminal, Sun, Moon, ShieldAlert, ArrowRightLeft, LogOut, SlidersHorizontal } from 'lucide-react'
import { setLang, useT, type Lang } from '../i18n'
import { applyTheme, currentTheme, type Theme } from '../theme'
import { Avatar, Logo, ServiceIcon, Mini, Dot, cx } from './ui'
import { api, isDemo } from '../api/client'
import { useQuery } from '../api/hooks'
import { svcIcon } from '../api/view'
import { useUpdatesByService } from '../api/updates'
import { UpdateBadge } from './ImageUpdateCard'
import type { Project } from '../api/types'

const useMe = () => useQuery('me', api.me)
const useServers = () => useQuery('servers', api.servers, { interval: 30000 })
export const usePending = () => useQuery('confirmations:pending', () => api.confirmations('pending'), { interval: 30000 })

function Team() {
  const me = useMe().data
  return (
    <button className="w-full flex items-center gap-2.5 h-11 px-2 rounded-lg hover:bg-ov/5">
      <Avatar size={28} /><span className="text-[13.5px] font-semibold truncate">{me?.username ?? '…'}</span><ChevronsUpDown size={14} className="ml-auto text-muted" />
    </button>
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
  const logout = async () => { await api.logout().catch(() => {}); location.hash = '#/login' }
  return (
    <div className="flex items-center gap-2.5 px-1 group">
      <button onClick={logout} title="退出登录" className="tile h-8 w-8 rounded-lg bg-[#fde047] grid place-items-center text-[13px] font-extrabold font-mono text-[#141414] relative">
        <span className="group-hover:opacity-0">{(me?.username ?? 'A')[0].toUpperCase()}</span><LogOut size={14} className="absolute opacity-0 group-hover:opacity-100" />
      </button>
      <div className="min-w-0 leading-tight"><div className="text-[13px] font-medium truncate">{me?.username ?? '…'}</div><div className="text-[11.5px] text-dim">{isDemo() ? '演示模式' : '管理员'}</div></div>
      <div className="ml-auto"><ThemeToggle /></div>
    </div>
  )
}
function SearchBox() {
  return (
    <div className="flex items-center gap-2 h-10 rounded-lg border border-line field px-3 text-[13px] text-muted">
      <Search size={15} />搜索<span className="ml-auto text-[11px] rounded border border-line2 px-1.5 py-0.5 text-dim">Ctrl K</span>
    </div>
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

export function MainShell({ children }: { children: React.ReactNode }) {
  const servers = useServers().data
  const pending = usePending().data?.length ?? 0
  const workers = servers ?? []
  const online = workers.filter(s => s.status === 'online').length
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
          {item('/domains', <Globe size={16} />, '域名')}
          {item('/developer', <Terminal size={16} />, 'CLI 与 API')}
          {item('/confirmations', <ShieldAlert size={16} />, '待确认', pending)}
          {item('/import', <ArrowRightLeft size={16} />, '从 Zeabur 迁移')}
          {item('/settings', <SlidersHorizontal size={16} />, '设置')}
        </nav>
        <div className="flex-1" />
        <div className="rounded-xl surface px-3 py-2.5 mb-3 text-[12px] text-muted flex items-center gap-2"><Dot status={servers ? 'online' : 'unknown'} />{servers ? `中控在线 · ${online}/${workers.length} 服务器已连接` : '正在连接中控…'}</div>
        <div className="h-px bg-line mb-3" />
        <UserRow />
    </>}>{children}</Frame>
  )
}

export function ProjectShell({ project, active, children }: { project: Project; active?: string; children: React.ReactNode }) {
  const nav = useNavigate()
  const loc = useLocation()
  const srv = useServers().data?.find(s => s.id === project.server_id)
  const services = project.services ?? []
  const updates = useUpdatesByService()
  const add = () => { const q = new URLSearchParams(active ? loc.search : ''); q.set('add', '1'); nav(`${active ? `/projects/${project.id}/services/${active}` : `/projects/${project.id}`}?${q}`) }
  return (
    <Frame pad="pt-6" top={<><Link to="/" title="项目" className="shrink-0 text-fg2 hover:text-fg"><ArrowLeft size={18} /></Link><span className="text-[15px] font-semibold tracking-tight truncate">{project.name}</span></>} side={<>
        <Link to="/" className="flex items-center gap-2 text-[13px] text-fg2 px-2 h-8 hover:text-fg"><ArrowLeft size={16} />项目</Link>
        <div className="px-2 mt-3">
          <div className="flex items-center gap-2"><h1 className="text-[17px] font-semibold tracking-tight truncate">{project.name}</h1><ChevronsUpDown size={14} className="text-muted" /></div>
          {srv ? <Link to={`/servers/${srv.id}`} className="mt-1.5 flex items-center gap-2 text-[13px] text-muted hover:text-fg"><Mini provider={srv.provider ?? '?'} /><span className="truncate">{srv.name}</span><Dot status={srv.status} /></Link>
            : <div className="mt-1.5 h-5 w-40 rounded bg-ov/[0.07] animate-pulse" />}
        </div>
        <div className="mt-4 flex flex-col gap-1">
          <NavLink to={`/projects/${project.id}/settings`} className={({ isActive }) => cx('flex items-center gap-3 h-9 px-3 rounded-lg text-[13px] font-medium', isActive ? 'nav-active' : 'text-fg2 hover:bg-ov/5')}><Settings size={16} />项目设置</NavLink>
          <button className="flex items-center gap-3 h-9 px-3 rounded-lg text-[13px] font-medium text-fg2 hover:bg-ov/5"><Bell size={16} />通知</button>
          <button onClick={add} className="flex items-center gap-3 h-9 px-3 rounded-lg text-[13px] font-medium text-accent hover:bg-ov/5"><Plus size={16} />新建服务</button>
        </div>
        <div className="h-px bg-line my-4" />
        <div className="px-2 text-[12px] font-semibold text-dim tracking-wider mb-2">服务</div>
        <div className="flex flex-col gap-1 overflow-auto">
          {!services.length && <div className="px-2 text-[12.5px] text-dim">还没有服务</div>}
          {services.map(s => (
            <Link key={s.id} to={`/projects/${project.id}/services/${s.id}`} className={cx('flex items-center gap-3 h-10 px-2 rounded-lg', active === s.id ? 'nav-active' : 'hover:bg-ov/5')}>
              <ServiceIcon s={svcIcon(s)} size={26} /><span className="text-[13px] font-medium truncate flex-1">{s.name}</span><UpdateBadge u={updates[s.id]} compact /><Dot status={s.status} />
            </Link>
          ))}
        </div>
        <div className="flex-1" />
        <div className="h-px bg-line mb-3" />
        <div className="px-2 flex items-center gap-2"><div className="min-w-0"><div className="text-[11.5px] text-dim">项目 ID</div>
        <button onClick={() => navigator.clipboard?.writeText(project.id)} className="mt-1 flex items-center gap-1.5 font-mono text-[11.5px] text-muted hover:text-fg max-w-full"><span className="truncate">{project.id}</span><Copy size={11} className="shrink-0" /></button></div><div className="ml-auto"><ThemeToggle /></div></div>
    </>}>{children}</Frame>
  )
}
