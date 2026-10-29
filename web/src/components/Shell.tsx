import { useState } from 'react'
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom'
import { Search, Package, Server, Globe, ChevronsUpDown, ArrowLeft, Settings, Plus, Copy, Bell, Terminal, Sun, Moon, ShieldAlert, ArrowRightLeft, LogOut, SlidersHorizontal } from 'lucide-react'
import { applyTheme, currentTheme, type Theme } from '../theme'
import { Avatar, Logo, ServiceIcon, Mini, Dot, cx } from './ui'
import { api, isDemo } from '../api/client'
import { useQuery } from '../api/hooks'
import { svcIcon } from '../api/view'
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
    <div className="h-full flex topline">
      <aside className="w-[248px] shrink-0 bg-side border-r border-line flex flex-col px-4 pt-7 pb-4">
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
      </aside>
      <main className="flex-1 overflow-auto canvas">{children}</main>
    </div>
  )
}

export function ProjectShell({ project, active, children }: { project: Project; active?: string; children: React.ReactNode }) {
  const nav = useNavigate()
  const loc = useLocation()
  const srv = useServers().data?.find(s => s.id === project.server_id)
  const services = project.services ?? []
  const add = () => { const q = new URLSearchParams(loc.search); q.set('add', '1'); nav(`${active ? `/projects/${project.id}/services/${active}` : loc.pathname}?${q}`) }
  return (
    <div className="h-full flex topline">
      <aside className="w-[248px] shrink-0 bg-side border-r border-line flex flex-col px-4 pt-6 pb-4">
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
              <ServiceIcon s={svcIcon(s)} size={26} /><span className="text-[13px] font-medium truncate flex-1">{s.name}</span><Dot status={s.status} />
            </Link>
          ))}
        </div>
        <div className="flex-1" />
        <div className="h-px bg-line mb-3" />
        <div className="px-2 flex items-center"><div><div className="text-[11.5px] text-dim">项目 ID</div>
        <button onClick={() => navigator.clipboard?.writeText(project.id)} className="mt-1 flex items-center gap-1.5 font-mono text-[11.5px] text-muted hover:text-fg">{project.id}<Copy size={11} /></button></div><div className="ml-auto"><ThemeToggle /></div></div>
      </aside>
      <main className="flex-1 overflow-auto canvas">{children}</main>
    </div>
  )
}
