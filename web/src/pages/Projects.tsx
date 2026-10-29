import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ArrowUpDown, LayoutGrid, List, MoreVertical, Package, Plus } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Button, Dot, Mini, ServiceIcon, cx } from '../components/ui'
import { CardSkeleton, Empty, ErrorState, Label, Modal, Select, TextInput } from '../components/feedback'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import { primaryDomain, projIcon, svcIcon } from '../api/view'

export const useProjects = () => useQuery('projects', api.projectsFull)

export default function Projects() {
  const q = useProjects()
  const servers = useQuery('servers', api.servers).data
  const [sort, setSort] = useState(false)
  const [listView, setListView] = useState(false)
  const [create, setCreate] = useState(false)
  const list = (q.data ?? []).slice().sort((a, b) => sort ? a.name.localeCompare(b.name) : 0)
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-14 pt-12 pb-16">
        <div className="flex items-center justify-between mb-8">
          <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">我的项目</h1>
          <div className="flex items-center gap-3">
            <button onClick={() => setSort(v => !v)} title="按名称排序" className={cx('h-11 w-11 grid place-items-center', sort ? 'text-accent' : 'text-fg2')}><ArrowUpDown size={18} /></button>
            <div className="flex rounded-lg field p-0.5">
              <button onClick={() => setListView(false)} className={cx('h-8 w-10 grid place-items-center rounded-md', !listView ? 'nav-active' : 'text-muted')}><LayoutGrid size={16} /></button>
              <button onClick={() => setListView(true)} className={cx('h-8 w-10 grid place-items-center rounded-md', listView ? 'nav-active' : 'text-muted')}><List size={16} /></button>
            </div>
            <Button variant="primary" size="lg" className="w-[200px]" onClick={() => setCreate(true)}>创建项目</Button>
          </div>
        </div>
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <CardSkeleton n={6} h={120} />}
        {q.data && !list.length && <Empty icon={<Package size={22} />} title="还没有项目" desc="项目是一组运行在同一台服务器上的服务，它们通过 Docker 内网互通。也可以从 Zeabur 直接迁移过来。"
          action={<div className="flex gap-2"><Button variant="primary" onClick={() => setCreate(true)}><Plus size={15} />创建项目</Button><Link to="/import"><Button>从 Zeabur 迁移</Button></Link></div>} />}
        <div className={cx('grid gap-4', listView ? 'grid-cols-1' : 'grid-cols-3')}>
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
                    <MoreVertical size={15} className="text-dim opacity-0 group-hover:opacity-100 transition-opacity" />
                  </div>
                  <div className="mt-5 flex items-center gap-1.5 h-[22px]">
                    {ss.map(s => <span key={s.id} title={s.name}><ServiceIcon s={svcIcon(s)} size={22} /></span>)}
                  </div>
                </div>
                <div className="mt-5 px-5 h-11 border-t border-line bg-sunk/60 flex items-center gap-2 text-[12.5px]">
                  {srv ? <><Mini provider={srv.provider ?? '?'} size={13} /><span className="text-fg2 truncate">{srv.name}</span></> : <span className="text-dim">—</span>}
                  <span className="ml-auto flex items-center gap-2 text-muted tnum"><Dot status={!ss.length ? 'unknown' : bad ? 'failed' : run === ss.length ? 'running' : 'deploying'} />{run}/{ss.length}</span>
                </div>
              </Link>
            )
          })}
        </div>
      </div>
      {create && <CreateProject onClose={() => setCreate(false)} />}
    </MainShell>
  )
}

function CreateProject({ onClose }: { onClose: () => void }) {
  const nav = useNavigate()
  const servers = (useQuery('servers', api.servers).data ?? []).filter(s => s.role !== 'control')
  const [name, setName] = useState(''); const [desc, setDesc] = useState(''); const [sid, setSid] = useState('')
  const [busy, setBusy] = useState(false)
  const server = sid || servers.find(s => s.status === 'online')?.id || servers[0]?.id || ''
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
