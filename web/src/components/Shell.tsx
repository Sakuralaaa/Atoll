import { useState } from 'react'
import { Link, NavLink, useNavigate } from 'react-router-dom'
import { Search, Package, Server, Globe, ChevronsUpDown, ArrowLeft, Settings, Plus, Copy, Bell, Terminal, Sun, Moon } from 'lucide-react'
import { applyTheme, currentTheme, type Theme } from '../theme'
import { Avatar, Logo, ServiceIcon, Mini, Dot, cx } from './ui'
import { serverById, type Project } from '../mock'

function Team() {
  return (
    <button className="w-full flex items-center gap-2.5 h-11 px-2 rounded-lg hover:bg-ov/5">
      <Avatar size={28} /><span className="text-[13.5px] font-semibold">demo</span><ChevronsUpDown size={14} className="ml-auto text-muted" />
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
  return (
    <div className="flex items-center gap-2.5 px-1">
      <div className="tile h-8 w-8 rounded-lg bg-[#fde047] grid place-items-center text-[13px] font-extrabold font-mono text-[#141414]">S</div>
      <div className="min-w-0 leading-tight"><div className="text-[13px] font-medium truncate">demo</div><div className="text-[11.5px] text-dim">管理员</div></div>
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
  const item = (to: string, icon: React.ReactNode, l: string) => (
    <NavLink to={to} end={to === '/'} className={({ isActive }) => cx('flex items-center gap-3 h-9 px-3 rounded-lg text-[13.5px] font-medium',
      isActive ? 'nav-active text-fg' : 'text-fg2 hover:bg-ov/5')}>{icon}{l}</NavLink>
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
        </nav>
        <div className="flex-1" />
        <div className="rounded-xl surface px-3 py-2.5 mb-3 text-[12px] text-muted flex items-center gap-2"><Dot status="online" />中控在线 · 4/5 服务器已连接</div>
        <div className="h-px bg-line mb-3" />
        <UserRow />
      </aside>
      <main className="flex-1 overflow-auto canvas">{children}</main>
    </div>
  )
}

export function ProjectShell({ project, active, children }: { project: Project; active?: string; children: React.ReactNode }) {
  const nav = useNavigate()
  const srv = serverById(project.serverId)
  return (
    <div className="h-full flex topline">
      <aside className="w-[248px] shrink-0 bg-side border-r border-line flex flex-col px-4 pt-6 pb-4">
        <Link to="/" className="flex items-center gap-2 text-[13px] text-fg2 px-2 h-8 hover:text-fg"><ArrowLeft size={16} />项目</Link>
        <div className="px-2 mt-3">
          <div className="flex items-center gap-2"><h1 className="text-[17px] font-semibold tracking-tight truncate">{project.name}</h1><ChevronsUpDown size={14} className="text-muted" /></div>
          <Link to={`/servers/${srv.id}`} className="mt-1.5 flex items-center gap-2 text-[13px] text-muted hover:text-fg"><Mini provider={srv.provider} />{srv.name}<Dot status={srv.status} /></Link>
        </div>
        <div className="mt-4 flex flex-col gap-1">
          <NavLink to={`/projects/${project.id}/settings`} className={({ isActive }) => cx('flex items-center gap-3 h-9 px-3 rounded-lg text-[13px] font-medium', isActive ? 'nav-active' : 'text-fg2 hover:bg-ov/5')}><Settings size={16} />项目设置</NavLink>
          <button className="flex items-center gap-3 h-9 px-3 rounded-lg text-[13px] font-medium text-fg2 hover:bg-ov/5"><Bell size={16} />通知</button>
          <button onClick={() => nav(`/projects/${project.id}/services/${active ?? project.services[0].id}?add=1`)} className="flex items-center gap-3 h-9 px-3 rounded-lg text-[13px] font-medium text-accent hover:bg-ov/5"><Plus size={16} />新建服务</button>
        </div>
        <div className="h-px bg-line my-4" />
        <div className="px-2 text-[12px] font-semibold text-dim tracking-wider mb-2">服务</div>
        <div className="flex flex-col gap-1 overflow-auto">
          {project.services.map(s => (
            <Link key={s.id} to={`/projects/${project.id}/services/${s.id}`} className={cx('flex items-center gap-3 h-10 px-2 rounded-lg', active === s.id ? 'nav-active' : 'hover:bg-ov/5')}>
              <ServiceIcon s={s} size={26} /><span className="text-[13px] font-medium truncate flex-1">{s.name}</span><Dot status={s.status} />
            </Link>
          ))}
        </div>
        <div className="flex-1" />
        <div className="h-px bg-line mb-3" />
        <div className="px-2 flex items-center"><div><div className="text-[11.5px] text-dim">项目 ID</div>
        <div className="mt-1 flex items-center gap-1.5 font-mono text-[11.5px] text-muted">project-6a3f02c91e7d<Copy size={11} /></div></div><div className="ml-auto"><ThemeToggle /></div></div>
      </aside>
      <main className="flex-1 overflow-auto canvas">{children}</main>
    </div>
  )
}
