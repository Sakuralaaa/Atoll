import { useState } from 'react'
import { IP } from '../components/IP'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { Loader2, ArrowRight } from 'lucide-react'
import { ProjectShell } from '../components/Shell'
import { Button, Card, Dot, Mini } from '../components/ui'
import { ConfirmDialog, ErrorState, Modal, Select, Skeleton, TextInput } from '../components/feedback'
import { api, isConfirmation } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import type { Project, Server } from '../api/types'
import { providerOf } from '../api/providers'
import { placeholder, useProject } from '../api/queries'

const Row = ({ t, d, children }: { t: string; d?: string; children: React.ReactNode }) => (
  <div className="px-4 @2xl:px-6 py-5 flex flex-wrap items-center gap-x-6 gap-y-3"><div className="flex-1 min-w-[min(100%,220px)]"><div className="text-[13px] font-semibold">{t}</div>{d && <div className="text-[13px] text-muted mt-1 leading-5">{d}</div>}</div>{children}</div>
)

export default function ProjectSettings() {
  const { pid } = useParams()
  const q = useProject(pid)
  const p = q.data
  const servers = useQuery('servers', api.servers).data
  return (
    <ProjectShell project={p ?? placeholder(pid)}>
      <div className="max-w-[1100px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em] mb-8">项目设置</h1>
        {q.error && !p && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!p && !q.error && <Card className="p-6 space-y-4"><Skeleton className="h-11 w-full" /><Skeleton className="h-11 w-full" /></Card>}
        {p && <Body key={p.id} p={p} servers={servers ?? []} />}
      </div>
    </ProjectShell>
  )
}

function Body({ p, servers }: { p: Project; servers: Server[] }) {
  const nav = useNavigate()
  const srv = servers.find(s => s.id === p.server_id)
  const [name, setName] = useState(p.name)
  const [label, setLabel] = useState(p.display_name ?? '')
  const [desc, setDesc] = useState(p.description ?? '')
  const roots = useQuery('root-domains', api.rootDomains).data ?? []
  const [root, setRoot] = useState(p.root_domain ?? '')
  const [busy, wrap] = useBusy()
  const [migrate, setMigrate] = useState(false)
  const [del, setDel] = useState(false)
  const changed = name.trim() !== p.name || label.trim() !== (p.display_name ?? '') || desc !== (p.description ?? '') || root !== (p.root_domain ?? '')
  const save = () => wrap(() => run(() => api.updateProject(p.id, { name: name.trim(), display_name: label.trim(), description: desc, ...(root !== (p.root_domain ?? '') ? { root_domain: root } : {}) }), { ok: '已保存', invalidate: [`project:${p.id}`, 'projects'] }))
  const n = p.services?.length ?? 0
  return (
    <>
      <div className="text-[13px] font-semibold mb-3">基本</div>
      <Card className="divide-y divide-line mb-8">
        <Row t="显示名称" d="只改面板里显示的名字，可以重复；留空则显示项目名称"><TextInput className="w-[420px]" value={label} maxLength={64} onChange={e => setLabel(e.target.value)} placeholder={name.trim() || p.name} /></Row>
        <Row t="项目名称" d="全局唯一，CLI 和 API 令牌按它指定项目"><TextInput className="w-[420px]" value={name} onChange={e => setName(e.target.value)} /></Row>
        <Row t="描述"><TextInput className="w-[420px]" value={desc} onChange={e => setDesc(e.target.value)} placeholder="可选" /></Row>
        {roots.length > 0 && <Row t="默认根域名" d="这个项目新绑定的域名默认建在它下面；已有域名不变">
          <Select className="w-[420px] font-mono" value={root} onChange={e => setRoot(e.target.value)}>
            <option value="">跟随全局默认（{roots.find(r => r.default)?.name ?? '—'}）</option>
            {roots.map(r => <option key={r.name} value={r.name}>{r.name}</option>)}
            {root && !roots.some(r => r.name === root) && <option value={root}>{root}（已移除）</option>}
          </Select>
        </Row>}
        <div className="px-6 py-4 flex justify-end"><Button variant="primary" size="sm" disabled={!changed || !name.trim() || busy} onClick={save}>{busy && <Loader2 size={13} className="animate-spin" />}保存</Button></div>
      </Card>
      <div className="text-[13px] font-semibold mb-3">服务器与迁移</div>
      <Card className="divide-y divide-line mb-8">
        <Row t="运行服务器" d="同一项目的服务都部署在这台服务器上，通过 Docker 内网互通">
          {srv ? <Link to={`/servers/${srv.id}`} className="flex items-center gap-2 text-[13px] hover:text-accent"><Mini provider={providerOf(srv)} />{srv.name}<Dot status={srv.status} /></Link> : <Skeleton className="h-4 w-48" />}
        </Row>
        <Row t="迁移到其他服务器" d="先同步一次卷数据，停机后再增量同步并导出导入数据库，最后自动切换 DNS。停机通常在一分钟内。"><Button variant="primary" size="sm" onClick={() => setMigrate(true)}>选择目标服务器</Button></Row>
      </Card>
      <div className="text-[13px] font-semibold mb-3">危险区域</div>
      <Card className="divide-y divide-line">
        <Row t="删除项目" d={`删除项目下全部 ${n} 个服务、卷和域名记录，不可恢复`}><Button variant="danger" size="sm" onClick={() => setDel(true)}>删除项目</Button></Row>
      </Card>
      {migrate && <Migrate p={p} servers={servers} onClose={() => setMigrate(false)} />}
      {del && <ConfirmDialog title="删除项目" name={p.name} onClose={() => setDel(false)}
        description={<>将删除 <b>{p.name}</b> 下的 {n} 个服务、它们的数据卷和 Atoll 创建的 DNS 记录。此操作不可恢复。</>}
        onConfirm={async () => { const r = await run(() => api.deleteProject(p.id), { ok: '项目已删除', invalidate: ['projects'] }); if (r !== undefined && !isConfirmation(r)) nav('/') }} />}
    </>
  )
}

function Migrate({ p, servers, onClose }: { p: Project; servers: Server[]; onClose: () => void }) {
  const list = servers.filter(s => s.id !== p.server_id && s.role !== 'control')
  const [to, setTo] = useState(list.find(s => s.status === 'online')?.id ?? '')
  const [busy, wrap] = useBusy()
  const target = list.find(s => s.id === to)
  const go = () => wrap(async () => {
    if (!to) return
    const r = await run(() => api.migrateProject(p.id, to), { ok: '已开始迁移，完成后会自动切换 DNS', invalidate: [`project:${p.id}`, 'projects'] })
    if (r) onClose()
  })
  return (
    <Modal title="迁移项目" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!target || target.status !== 'online' || busy} onClick={go}>{busy && <Loader2 size={13} className="animate-spin" />}开始迁移<ArrowRight size={13} /></Button></>}>
      {!list.length ? <div className="text-[13px] text-muted">没有其他可用的服务器。</div> : <>
        <div className="text-[13px] font-semibold mb-1.5">目标服务器</div>
        <Select className="w-full" value={to} onChange={e => setTo(e.target.value)}>{list.map(s => <option key={s.id} value={s.id} disabled={s.status !== 'online'}>{s.name}{s.status !== 'online' ? '（离线）' : ''}</option>)}</Select>
        <div className="mt-4 rounded-lg field px-4 py-3 text-[12.5px] text-fg2 leading-5">{p.name} 的 {p.services?.length ?? 0} 个服务会在迁移期间短暂停机；绑定的域名会自动改指向 {target?.public_ipv4 ? <IP v={target.public_ipv4} /> : '新服务器'}。</div>
      </>}
    </Modal>
  )
}
