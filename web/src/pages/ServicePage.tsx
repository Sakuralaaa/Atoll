import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Activity, KeyRound, ArchiveRestore, BarChart3, Network, HardDrive, Settings, RotateCw, Rocket, Hammer, ScrollText, MoreHorizontal, History, Terminal, Square, Play } from 'lucide-react'
import { ProjectShell } from '../components/Shell'
import { Badge, Button, CopyId, ServiceIcon, Tabs } from '../components/ui'
import { ConfirmDialog, Empty, ErrorState, Skeleton, CardSkeleton } from '../components/feedback'
import AddServiceModal from '../components/AddServiceModal'
import BackupPanel from '../components/BackupPanel'
import { isGit } from '../api/git'
import { api } from '../api/client'
import { invalidate, run, useBusy, useQuery } from '../api/hooks'
import { day, svcIcon, svcName } from '../api/view'
import type { Service } from '../api/types'
import { useT } from '../i18n'
import { cx, statusText } from '../components/ui-utils'
import { placeholder, useProject } from '../api/queries'
import { StatusTab } from './service/StatusTab'
import { EnvTab } from './service/EnvTab'
import { DiskTab, NetworkTab, UsageTab } from './service/ResourceTabs'
import { SettingsTab } from './service/SettingsTab'
import { DeployProgress } from '../components/DeployProgress'

const TABS = [
  { k: 'status', l: '服务状态', icon: <Activity size={15} /> },
  { k: 'logs', l: '日志', icon: <ScrollText size={15} /> },
  { k: 'env', l: '环境变量', icon: <KeyRound size={15} /> },
  { k: 'backup', l: '备份还原', icon: <ArchiveRestore size={15} /> },
  { k: 'usage', l: '用量', icon: <BarChart3 size={15} /> },
  { k: 'network', l: '网络', icon: <Network size={15} /> },
  { k: 'disk', l: '硬盘', icon: <HardDrive size={15} /> },
  { k: 'settings', l: '设置', icon: <Settings size={15} /> },
]


export default function ServicePage() {
  const { pid, sid } = useParams()
  const [sp, setSp] = useSearchParams()
  const nav = useNavigate()
  const pq = useProject(pid)
  const p = pq.data
  const s = p?.services?.find(x => x.id === sid)
  const srv = useQuery('servers', api.servers).data?.find(x => x.id === (p?.services?.find(v => v.id === sid)?.server_id || p?.server_id))
  const tab = sp.get('tab') ?? 'status'
  const refresh = () => invalidate(`project:${pid}`, 'projects')
  return (
    <ProjectShell project={p ?? placeholder(pid)} active={sid}>
      <div className="max-w-[1320px] mx-auto px-4 @2xl:px-8 @5xl:px-10 @6xl:px-14 pt-6 @2xl:pt-10 pb-16">
        {pq.error && !p && <ErrorState error={pq.error} onRetry={pq.refetch} />}
        {!p && !pq.error && <><div className="flex gap-4"><Skeleton className="h-[52px] w-[52px]" /><div className="space-y-2 flex-1 min-w-0"><Skeleton className="h-7 w-64 max-w-full" /><Skeleton className="h-3.5 w-96 max-w-full" /></div></div><div className="mt-10"><CardSkeleton n={1} cols={1} h={260} /></div></>}
        {p && !s && <Empty title="找不到这个服务" desc="它可能已被删除。" action={<Link to={`/projects/${p.id}`}><Button>返回项目</Button></Link>} />}
        {p && s && <>
          <Header s={s} onChange={refresh} onTab={k => setSp({ tab: k })} />
          <DeployProgress s={s} />
          <Tabs className="mt-7" items={TABS} value={tab} onChange={k => k === 'logs' ? nav(`/projects/${p.id}/services/${s.id}/logs`) : setSp({ tab: k })} />
          <div className="mt-6">
            {tab === 'status' && <StatusTab p={p} s={s} srv={srv} />}
            {tab === 'env' && <EnvTab s={s} />}
            {tab === 'backup' && <BackupPanel p={p} s={s} />}
            {tab === 'usage' && <UsageTab s={s} />}
            {tab === 'network' && <NetworkTab s={s} srv={srv} />}
            {tab === 'disk' && <DiskTab s={s} />}
            {tab === 'settings' && <SettingsTab p={p} s={s} onDeleted={() => nav(`/projects/${p.id}`)} />}
          </div>
        </>}
      </div>
      {sp.get('add') && pid && <AddServiceModal projectId={pid} onCreated={x => nav(`/projects/${pid}/services/${x.id}`)} onClose={() => { const n = new URLSearchParams(sp); n.delete('add'); setSp(n) }} />}
    </ProjectShell>
  )
}

const tone = (st: string) => st === 'running' ? 'green' : st === 'failed' ? 'red' : st === 'deploying' ? 'amber' : 'zinc'

function Header({ s, onChange, onTab }: { s: Service; onChange: () => void; onTab: (k: string) => void }) {
  const [busy, wrap] = useBusy()
  const { t } = useT()
  const nav = useNavigate()
  const git = isGit(s)
  const [confirm, setConfirm] = useState<'restart' | 'stop'>()
  const act = (a: 'restart' | 'redeploy' | 'stop' | 'start') => wrap(() => run(() => api.action(s.id, a), { ok: { restart: '正在重新启动', stop: '正在停止', start: '正在启动', redeploy: git ? t('已开始构建最新提交', 'Building the latest commit') : '已触发重新部署' }[a], invalidate: [`deployments:${s.id}`] }).then(onChange))
  const logs = `/projects/${s.project_id}/services/${s.id}/logs`
  const stopped = s.status === 'stopped'
  return (
    <div className="flex flex-wrap items-start gap-4">
      <ServiceIcon s={svcIcon(s)} size={52} />
      <div className="flex-1 min-w-[min(100%,220px)]">
        <div className="flex flex-wrap items-center gap-3"><h1 className="text-[22px] @2xl:text-[26px] font-extrabold italic tracking-[-0.03em] leading-tight [overflow-wrap:anywhere]">{svcName(s)}</h1>{s.display_name && <span className="font-mono text-[13px] text-muted" title="内网主机名">{s.name}</span>}<Badge tone={tone(s.status)}>{statusText[s.status] ?? s.status}</Badge></div>
        <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-muted"><CopyId v={s.id} /><span>创建于 {day(s.created_at)}</span></div>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button disabled={busy} onClick={() => setConfirm('restart')}><RotateCw size={15} />重新启动</Button>
        <Button variant="primary" disabled={busy} onClick={() => act('redeploy')}>{git ? <><Hammer size={15} />{t('重新构建最新提交', 'Rebuild latest')}</> : <><Rocket size={15} />重新部署</>}</Button>
        <MoreMenu items={[
          { l: '查看日志', icon: <ScrollText size={14} />, run: () => nav(logs) },
          { l: '部署记录', icon: <History size={14} />, run: () => nav(`${logs}?view=deploys`) },
          { l: '执行命令', icon: <Terminal size={14} />, run: () => nav(`${logs}?view=exec`) },
          { l: '环境变量', icon: <KeyRound size={14} />, run: () => onTab('env') },
          stopped ? { l: '启动服务', icon: <Play size={14} />, run: () => act('start') } : { l: '停止服务', icon: <Square size={14} />, run: () => setConfirm('stop'), danger: true },
          { l: '服务设置', icon: <Settings size={14} />, run: () => onTab('settings') },
        ]} disabled={busy} />
      </div>
      {confirm === 'restart' && <ConfirmDialog title="重新启动服务" action="重新启动" danger={false} onClose={() => setConfirm(undefined)}
        description={<>重启 {svcName(s)} 的容器，期间会短暂不可访问。</>} onConfirm={() => act('restart')} />}
      {confirm === 'stop' && <ConfirmDialog title="停止服务" action="停止" onClose={() => setConfirm(undefined)}
        description={<>停止 {svcName(s)} 的容器，服务将不可访问，直到再次启动。数据卷不受影响。</>} onConfirm={() => act('stop')} />}
    </div>
  )
}

type MenuItem = { l: string; icon: React.ReactNode; run: () => void; danger?: boolean }
function MoreMenu({ items, disabled }: { items: MenuItem[]; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent | KeyboardEvent) => { if (e instanceof KeyboardEvent ? e.key === 'Escape' : !ref.current?.contains(e.target as Node)) setOpen(false) }
    document.addEventListener('mousedown', close); document.addEventListener('keydown', close)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', close) }
  }, [open])
  return (
    <div ref={ref} className="relative">
      <Button aria-label="更多操作" aria-haspopup="menu" aria-expanded={open} disabled={disabled} onClick={() => setOpen(v => !v)}><MoreHorizontal size={16} /></Button>
      {open && <div role="menu" className="absolute right-0 top-11 z-30 surface bg-pop p-1 w-44">
        {items.map(it => <button key={it.l} role="menuitem" onClick={() => { setOpen(false); it.run() }} className={cx('w-full flex items-center gap-2.5 text-left px-3 h-9 rounded-md text-[13px] hover:bg-ov/5', it.danger && 'text-[#ef4444]')}>{it.icon}{it.l}</button>)}
      </div>}
    </div>
  )
}
