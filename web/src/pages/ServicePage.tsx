import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Activity, KeyRound, ArchiveRestore, BarChart3, Network, HardDrive, Settings, RotateCw, Rocket, Hammer } from 'lucide-react'
import { ProjectShell } from '../components/Shell'
import { Badge, Button, CopyId, ServiceIcon, Tabs } from '../components/ui'
import { ConfirmDialog, Empty, ErrorState, Skeleton, CardSkeleton } from '../components/feedback'
import AddServiceModal from '../components/AddServiceModal'
import BackupPanel from '../components/BackupPanel'
import { isGit } from '../api/git'
import { api } from '../api/client'
import { invalidate, run, useBusy, useQuery } from '../api/hooks'
import { day, svcIcon } from '../api/view'
import type { Service } from '../api/types'
import { useT } from '../i18n'
import { statusText } from '../components/ui-utils'
import { placeholder, useProject } from '../api/queries'
import { StatusTab } from './service/StatusTab'
import { EnvTab } from './service/EnvTab'
import { DiskTab, NetworkTab, UsageTab } from './service/ResourceTabs'
import { SettingsTab } from './service/SettingsTab'
import { DeployProgress } from '../components/DeployProgress'

const TABS = [
  { k: 'status', l: '服务状态', icon: <Activity size={15} /> },
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
          <Header s={s} onChange={refresh} />
          <DeployProgress s={s} />
          <Tabs className="mt-7" items={TABS} value={tab} onChange={k => setSp({ tab: k })} />
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

function Header({ s, onChange }: { s: Service; onChange: () => void }) {
  const [busy, wrap] = useBusy()
  const { t } = useT()
  const git = isGit(s)
  const [restart, setRestart] = useState(false)
  const act = (a: 'restart' | 'redeploy') => wrap(() => run(() => api.action(s.id, a), { ok: a === 'restart' ? '正在重新启动' : git ? t('已开始构建最新提交', 'Building the latest commit') : '已触发重新部署', invalidate: [`deployments:${s.id}`] }).then(onChange))
  return (
    <div className="flex flex-wrap items-start gap-4">
      <ServiceIcon s={svcIcon(s)} size={52} />
      <div className="flex-1 min-w-[min(100%,220px)]">
        <div className="flex flex-wrap items-center gap-3"><h1 className="text-[22px] @2xl:text-[26px] font-extrabold italic tracking-[-0.03em] leading-tight [overflow-wrap:anywhere]">{s.name}</h1><Badge tone={tone(s.status)}>{statusText[s.status] ?? s.status}</Badge></div>
        <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-muted"><CopyId v={s.id} /><span>创建于 {day(s.created_at)}</span></div>
      </div>
      <div className="flex flex-wrap gap-2"><Button disabled={busy} onClick={() => setRestart(true)}><RotateCw size={15} />重新启动</Button><Button variant="primary" disabled={busy} onClick={() => act('redeploy')}>{git ? <><Hammer size={15} />{t('重新构建最新提交', 'Rebuild latest')}</> : <><Rocket size={15} />重新部署</>}</Button></div>
      {restart && <ConfirmDialog title="重新启动服务" action="重新启动" danger={false} onClose={() => setRestart(false)}
        description={<>重启 {s.name} 的容器，期间会短暂不可访问。</>} onConfirm={() => act('restart')} />}
    </div>
  )
}
