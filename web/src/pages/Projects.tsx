import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ArrowLeft, ArrowUpDown, LayoutGrid, List, Package, Plus, SearchX, Server as ServerIcon } from 'lucide-react'
import { ProblemsBanner } from '../components/Problems'
import { MainShell } from '../components/Shell'
import { Button, Dot, Mini, ServiceIcon } from '../components/ui'
import { CardSkeleton, Empty, ErrorState, Label, Modal, Select, TextInput } from '../components/feedback'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import { primaryDomain, projIcon, svcIcon, svcName } from '../api/view'
import { useUpdatesByService } from '../api/updates'
import { UpdateBadge } from '../components/ImageUpdateCard'
import { cx } from '../components/ui-utils'
import { useProblems, useProjects } from '../api/queries'
import { FilterChips } from '../components/StatusFilter'
import { projectFilters, useStatusFilter } from '../api/filters'
import { useT } from '../i18n'
import { ServerGroupCards } from '../components/ServerGroups'
import { groupByServer } from '../api/groups'
import { providerOf } from '../api/providers'
import type { Project } from '../api/types'

const GROUP_KEY = 'atoll.projects.byServer'

export default function Projects() {
  const q = useProjects()
  const servers = useQuery('servers', api.servers).data
  const updates = useUpdatesByService()
  const [sort, setSort] = useState(false)
  const [listView, setListView] = useState(false)
  const [create, setCreate] = useState(false)
  const { t } = useT()
  const all = (q.data ?? []).slice().sort((a, b) => sort ? a.name.localeCompare(b.name) : 0)
  const problems = (useProblems().data ?? []).filter(p => !p.resolved_at)
  const defs = projectFilters<Project>(problems)
  const [sp, setSp] = useSearchParams()
  const sel = sp.get('server') // '_' = projects whose machine is unknown
  const [grouped, setGroupedState] = useState(() => localStorage.getItem(GROUP_KEY) !== '0')
  const setGrouped = (v: boolean) => { localStorage.setItem(GROUP_KEY, v ? '1' : '0'); setGroupedState(v) }
  const machines = new Set(all.map(p => p.server_id ?? '')).size
  const inServer = (p: Project) => (p.server_id || '_') === sel
  const f = useStatusFilter(sel ? all.filter(inServer) : all, defs)
  const list = f.list
  const showGroups = grouped && !sel && machines > 1
  const groups = groupByServer(list, servers)
  const totals = new Map(groupByServer(all, servers).map(g => [g.id, g.projects.length]))
  const selServer = sel ? servers?.find(s => s.id === sel) : undefined
  const back = () => { const n = new URLSearchParams(sp); n.delete('server'); setSp(n) }
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-4 mb-8">
          {sel ? <div className="flex items-center gap-2 min-w-0">
            <button onClick={back} title={t('全部机器', 'All machines')} className="h-11 w-11 -ml-2 grid place-items-center text-fg2 shrink-0"><ArrowLeft size={18} /></button>
            <div className="min-w-0"><div className="text-[12px] text-muted">{t('我的项目', 'My projects')}</div>
              <h1 className="text-[22px] font-extrabold italic tracking-[-0.03em] truncate">{selServer?.name ?? t('未知机器', 'Unknown machine')}</h1></div>
          </div> : <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">我的项目</h1>}
          <div className="flex items-center gap-3">
            {!sel && machines > 1 && <button onClick={() => setGrouped(!grouped)} title={t('按机器分组', 'Group by machine')} className={cx('h-11 w-11 grid place-items-center', grouped ? 'text-accent' : 'text-fg2')}><ServerIcon size={18} /></button>}
            <button onClick={() => setSort(v => !v)} title="按名称排序" className={cx('h-11 w-11 grid place-items-center', sort ? 'text-accent' : 'text-fg2')}><ArrowUpDown size={18} /></button>
            <div className="flex rounded-lg field p-0.5">
              <button onClick={() => setListView(false)} className={cx('h-8 w-10 grid place-items-center rounded-md', !listView ? 'nav-active' : 'text-muted')}><LayoutGrid size={16} /></button>
              <button onClick={() => setListView(true)} className={cx('h-8 w-10 grid place-items-center rounded-md', listView ? 'nav-active' : 'text-muted')}><List size={16} /></button>
            </div>
            <Button variant="primary" size="lg" className="@3xl:w-[200px]" onClick={() => setCreate(true)}>创建项目</Button>
          </div>
        </div>
        <ProblemsBanner />
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <CardSkeleton n={6} h={120} />}
        {q.data && all.length > 0 && <FilterChips f={f} defs={defs} className="mb-5" />}
        {q.data && all.length > 0 && !list.length && f.def && <Empty icon={<SearchX size={22} />} title={t(`没有「${f.def.zh}」的项目`, `No projects match “${f.def.en}”`)}
          desc={t('项目里任一服务符合条件即会列出。', 'A project is listed when any of its services matches.')} action={<Button onClick={() => f.set('')}>{t('显示全部', 'Show all')}</Button>} />}
        {q.data && !all.length && <Empty icon={<Package size={22} />} title="还没有项目" desc="项目是一组运行在同一台服务器上的服务，它们通过 Docker 内网互通。也可以从 Zeabur 直接迁移过来。"
          action={<div className="flex gap-2"><Button variant="primary" onClick={() => setCreate(true)}><Plus size={15} />创建项目</Button><Link to="/import"><Button>从 Zeabur 迁移</Button></Link></div>} />}
        {showGroups && <ServerGroupCards groups={groups} total={totals} listView={listView} />}
        {!showGroups && <div className={cx('grid gap-4', listView ? 'grid-cols-1' : 'grid-cols-1 @2xl:grid-cols-2 @6xl:grid-cols-3')}>
          {list.map(p => {
            const srv = servers?.find(s => s.id === p.server_id)
            const ss = p.services ?? []
            const run = ss.filter(s => s.status === 'running').length
            const bad = ss.some(s => s.status === 'failed')
            const dom = ss.map(primaryDomain).find(Boolean)
            return (
              <Link key={p.id} to={`/projects/${p.id}`} className="group rounded-2xl surface surface-hover overflow-hidden">
                <div className="px-5 pt-5">
                  <div className="flex items-center gap-3">
                    <ServiceIcon s={projIcon(p)} size={38} />
                    <div className="min-w-0 flex-1">
                      <div className="text-[15px] font-semibold truncate">{p.name}</div>
                      <div className="text-[12px] text-muted truncate mt-0.5">{dom ?? (ss.length ? '仅内网访问' : '空项目')}</div>
                    </div>
                  </div>
                  <div className="mt-5 flex items-center gap-1.5 h-[22px] overflow-hidden">
                    {ss.map(s => <span key={s.id} title={svcName(s)}><ServiceIcon s={svcIcon(s)} size={22} /></span>)}
                  </div>
                </div>
                <div className="mt-5 px-5 h-11 border-t border-line bg-sunk/60 flex items-center gap-2 text-[12.5px]">
                  {srv ? <><Mini provider={providerOf(srv)} size={13} /><span className="text-fg2 truncate min-w-0">{srv.name}</span></> : <span className="text-dim">—</span>}
                  <span className="ml-auto flex items-center gap-2 text-muted tnum shrink-0"><UpdateBadge u={ss.map(s => updates[s.id]).find(u => u?.update_available)} /><Dot status={!ss.length ? 'unknown' : bad ? 'failed' : run === ss.length ? 'running' : 'deploying'} />{run}/{ss.length}</span>
                </div>
              </Link>
            )
          })}
        </div>}
        {sel && q.data && !all.some(inServer) && <Empty icon={<Package size={22} />} title={t('这台机器上还没有项目', 'No projects on this machine')} desc="" action={<Button onClick={back}>{t('返回全部机器', 'All machines')}</Button>} />}
      </div>
      {create && <CreateProject defaultServer={selServer?.id} onClose={() => setCreate(false)} />}
    </MainShell>
  )
}

function CreateProject({ onClose, defaultServer }: { onClose: () => void; defaultServer?: string }) {
  const nav = useNavigate()
  const servers = (useQuery('servers', api.servers).data ?? []).filter(s => s.role !== 'control')
  const [name, setName] = useState(''); const [desc, setDesc] = useState(''); const [sid, setSid] = useState('')
  const [busy, setBusy] = useState(false)
  const server = sid || (defaultServer && servers.some(s => s.id === defaultServer) ? defaultServer : '') || servers.find(s => s.status === 'online')?.id || servers[0]?.id || ''
  const submit = async () => {
    if (!name.trim() || !server) return
    setBusy(true)
    const p = await run(() => api.createProject({ name: name.trim(), server_id: server, description: desc || undefined }), { ok: '项目已创建', invalidate: ['projects'] })
    setBusy(false)
    if (p) { onClose(); nav(`/projects/${p.id}?add=1`) }
  }
  return (
    <Modal title="创建项目" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!name.trim() || !server || busy} onClick={submit}>创建</Button></>}>
      <Label t="项目名称"><TextInput autoFocus className="w-full" value={name} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === 'Enter' && submit()} placeholder="my-app" /></Label>
      <Label t="运行服务器" d="同一项目的服务都部署在这台服务器上，之后可以整体迁移">
        {servers.length ? <Select className="w-full" value={server} onChange={e => setSid(e.target.value)}>{servers.map(s => <option key={s.id} value={s.id}>{s.name}{s.status !== 'online' ? '（离线）' : ''}</option>)}</Select>
          : <div className="text-[13px] text-muted">还没有可用的服务器，请先到「服务器」页添加。</div>}
      </Label>
      <Label t="描述（可选）"><TextInput className="w-full" value={desc} onChange={e => setDesc(e.target.value)} /></Label>
    </Modal>
  )
}
