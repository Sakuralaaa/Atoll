import { useState } from 'react'
import { ScrollRows } from '../components/ScrollRows'
import { Link, useSearchParams } from 'react-router-dom'
import { Server as ServerIcon, FlaskConical, Power, Check, X, ArrowLeft, ArrowRight, RotateCcw, Loader2, Circle, Minus, AlertTriangle, ShieldAlert, ShieldCheck, HardDrive, Undo2, Boxes, Box } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { ImportStepper } from '../components/ImportStepper'
import { Badge, Button, Card, Dot } from '../components/ui'
import { ConfirmDialog, Empty, ErrorState, Load, RowsSkeleton } from '../components/feedback'
import { IP } from '../components/IP'
import { api, ApiError } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import { gb } from '../api/view'
import type { DockerContainer, DockerGroup, DockerInventory, DockerPort, ImportJob } from '../api/types'
import { cx } from '../components/ui-utils'
import { useT } from '../i18n'
import { useBackendText } from '../api/backendText'
import { BACK, backLabel, backTo } from '../api/backTo'

// Docker adoption (docs/adopt-docker.md): take over containers the user started with
// `docker run` or docker compose, in place. /import/docker?server=ID → scan; ?job=ID → steps.

const BUSY = ['rehearsing', 'cutting_over', 'rolling_back']

function portText(p: DockerPort) {
  if (!p.host_port) return `${p.container_port}/${p.protocol}`
  return `${p.host_ip ? p.host_ip + ':' : ''}${p.host_port}→${p.container_port}/${p.protocol}`
}

export default function ImportDocker() {
  const { t } = useT()
  const [sp, setSp] = useSearchParams()
  const jobId = sp.get('job'), server = sp.get('server')
  // ?project=ID: opened from a project's 新建服务; adoption still creates its own project
  const fromProject = sp.get('project')
  const keep = (o: Record<string, string>) => setSp(fromProject ? { ...o, project: fromProject } : o)
  const job = useQuery(jobId ? `imports:${jobId}` : null, () => api.importJob(jobId!), { interval: 2000 })
  const st = job.data?.status
  const step = jobId ? (st === 'live' || st === 'cutting_over' || (st === 'rolled_back' && job.data?.steps.some(s => s.name.startsWith('Stop old') && s.status !== 'pending')) ? 4 : 3) : server ? 2 : 1
  const steps = [t('选择服务器', 'Server'), t('扫描容器', 'Scan'), t('演练', 'Rehearse'), t('切换', 'Cut over')]
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        {fromProject ? <Link to={`/projects/${fromProject}`} className="inline-flex items-center gap-1.5 text-[13.5px] text-fg2 hover:text-fg"><ArrowLeft size={15} />{t('返回项目', 'Back to project')}</Link>
          : <Link to={backTo('/import/docker', '/import')} state={BACK} className="inline-flex items-center gap-1.5 text-[13.5px] text-fg2 hover:text-fg"><ArrowLeft size={15} />{backLabel(backTo('/import/docker', '/import'))}</Link>}
        <div className="flex flex-wrap items-end justify-between gap-3 mt-2 mb-2">
          <div className="min-w-0">
            <h1 className="text-[24px] @2xl:text-[26px] font-extrabold italic tracking-[-0.03em] flex flex-wrap items-center gap-x-3 gap-y-1">{t('接管 Docker 容器', 'Adopt Docker containers')}</h1>
            <p className="text-[13.5px] text-muted mt-2 max-w-[780px] leading-6">{t(
              '把你自己用 docker run 或 docker compose 启动的容器交给 Atoll 管理：同样的端口，同样的绑定目录和数据卷原地挂载，不复制数据。旧容器、镜像和卷永远保留，随时可以回滚。',
              'Hand the containers you started with docker run or docker compose to Atoll: same host ports, the same bind mounts and volumes mounted in place, no data copy. Your old containers, images and volumes are always kept, so you can roll back any time.')}</p>
          </div>
          {(jobId || server) && <Button onClick={() => keep({})}><X size={14} />{t('重新开始', 'Start over')}</Button>}
        </div>
        <ImportStepper steps={steps} step={step} />
        <div className="mt-7">
          {fromProject && !jobId && <div className="mb-5 rounded-lg border-2 border-dashed border-line2 px-4 py-3 text-[12.5px] text-muted leading-6">{t('接管会为每组容器新建一个 Atoll 项目（compose 项目名），不会并入当前项目；接管完成后可以在项目页继续管理。', 'Adoption creates a new Atoll project per container group (the compose project name); it is not merged into the current project.')}</div>}
          {!jobId && !server && <PickServer onPick={id => keep({ server: id })} />}
          {!jobId && server && <Scan serverId={server} onCreated={id => keep({ job: id })} />}
          {jobId && <Load q={job} skeleton={<RowsSkeleton n={6} />}>{j => <Job job={j} />}</Load>}
        </div>
      </div>
    </MainShell>
  )
}

function PickServer({ onPick }: { onPick: (id: string) => void }) {
  const { t } = useT()
  const q = useQuery('servers', api.servers)
  const list = (q.data ?? []).filter(s => s.role !== 'control')
  return (
    <>
      <div className="text-[15px] font-semibold mb-1">{t('容器在哪台服务器上？', 'Which server runs the containers?')}</div>
      <div className="text-[13px] text-muted mb-4">{t('选择已装好 Atoll Agent 的服务器。扫描只读，不改动任何东西。', 'Pick a server with the Atoll agent installed. The scan is read-only.')}</div>
      {q.error && <ErrorState error={q.error} onRetry={q.refetch} />}
      {!q.data && !q.error && <RowsSkeleton n={3} />}
      {q.data && !list.length && <Empty icon={<ServerIcon size={22} />} title={t('还没有连上的服务器', 'No servers yet')} desc={t('先在「服务器」页添加一台。', 'Add one on the Servers page first.')} />}
      {!!list.length && <Card className="overflow-hidden"><ScrollRows testId="import-server-list">
        {list.map(s => {
          const on = s.status === 'online'
          return (
            <button key={s.id} disabled={!on} onClick={() => onPick(s.id)} className={cx('w-full text-left px-4 @2xl:px-5 py-4 flex flex-wrap items-center gap-x-4 gap-y-2', on ? 'hover:bg-ov/[0.03]' : 'opacity-55 cursor-not-allowed')}>
              <Dot status={s.status} />
              <div className="flex-1 min-w-[min(100%,180px)]"><div className="text-[15px] font-semibold truncate">{s.name}</div><div className="text-[13px] text-muted mt-0.5"><IP className="font-mono" v={s.public_ipv4} /></div></div>
              <span className="btn-secondary h-9 px-3.5 inline-flex items-center gap-1.5 text-[13px] shrink-0 ml-auto">{t('扫描', 'Scan')}<ArrowRight size={14} /></span>
            </button>
          )
        })}
      </ScrollRows></Card>}
    </>
  )
}

function Scan({ serverId, onCreated }: { serverId: string; onCreated: (id: string) => void }) {
  const { t } = useT()
  const q = useQuery(`docker:${serverId}`, () => api.dockerScan(serverId))
  const srv = useQuery('servers', api.servers).data?.find(s => s.id === serverId)
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-4">
        <div><div className="text-[15px] font-semibold">{t(`${srv?.name ?? serverId} 上 Atoll 之外的容器`, `Containers outside Atoll on ${srv?.name ?? serverId}`)}</div><div className="text-[13px] text-muted mt-0.5">{t('按 compose 项目分组，单独启动的容器各成一组。环境变量中的密钥已隐藏。', 'Grouped by compose project; standalone containers form their own group. Secret env values are hidden.')}</div></div>
        <Button onClick={q.refetch}><RotateCcw size={14} />{t('重新扫描', 'Rescan')}</Button>
      </div>
      {q.error && <ErrorState error={q.error} onRetry={q.refetch} />}
      {!q.data && !q.error && <RowsSkeleton n={4} />}
      {q.data && <Inventory inv={q.data} serverId={serverId} onCreated={onCreated} />}
    </>
  )
}

function Inventory({ inv, serverId, onCreated }: { inv: DockerInventory; serverId: string; onCreated: (id: string) => void }) {
  const { t } = useT()
  if (!inv.groups.length) return <Empty icon={<Boxes size={22} />} title={t('没有可接管的容器', 'Nothing to adopt')} desc={t('这台服务器上的容器都已由 Atoll 管理。', 'Every container on this server is already managed by Atoll.')} />
  return (
    <>
      <div className="text-[13px] text-muted mb-4 flex flex-wrap gap-x-5 gap-y-1"><span>Docker {inv.docker_version ?? '—'}</span><span>{t('可用内存', 'Free memory')} {gb(inv.mem_free_mb)}</span><span>{t('可用硬盘', 'Free disk')} {inv.disk_free_gb.toFixed(1)} GB</span></div>
      <div className="space-y-6">{inv.groups.map(g => <GroupCard key={g.key} g={g} serverId={serverId} onCreated={onCreated} />)}</div>
    </>
  )
}

function GroupCard({ g, serverId, onCreated }: { g: DockerGroup; serverId: string; onCreated: (id: string) => void }) {
  const { t } = useT()
  const { bt } = useBackendText()
  const auto = g.containers.filter(c => c.risk === 'auto').map(c => c.name)
  const [picked, setPicked] = useState<string[]>(auto)
  const [busy, wrap] = useBusy()
  const adopt = () => wrap(async () => {
    const j = await run(() => api.dockerAdopt({ server_id: serverId, group: g.key, containers: picked.length === auto.length ? undefined : picked }), { ok: t('已创建 Atoll 项目', 'Atoll project created'), invalidate: ['imports'] })
    if (j && 'id' in j) onCreated(j.id)
  })
  return (
    <Card className="overflow-hidden">
      <div className="px-4 @2xl:px-6 py-4 flex flex-wrap items-center gap-x-4 gap-y-3 border-b-2 border-[var(--edge)] bg-card2">
        {g.kind === 'compose' ? <Boxes size={22} className="text-accent shrink-0" /> : <Box size={22} className="text-accent shrink-0" />}
        <div className="flex-1 min-w-[min(100%,180px)]">
          <div className="text-[15px] font-semibold [overflow-wrap:anywhere]">{g.name} <span className="text-[12.5px] text-muted font-normal ml-1">· {g.kind === 'compose' ? 'docker compose' : t('单独容器', 'standalone')}</span></div>
          {g.working_dir && <div className="font-mono text-[11.5px] text-dim mt-0.5 truncate">{g.working_dir}</div>}
        </div>
        <Button variant="primary" disabled={!g.adoptable || !picked.length || busy} onClick={adopt}>{busy && <Loader2 size={14} className="animate-spin" />}{t('接管这一组', 'Adopt this group')}<ArrowRight size={14} /></Button>
      </div>
      {!!g.warnings?.length && <div className="px-4 @2xl:px-6 py-3 border-b border-line bg-t-amber/20 space-y-1">
        {g.warnings.map(w => <div key={w} className="flex items-start gap-1.5 text-[12.5px]"><AlertTriangle size={13} className="text-amber mt-0.5 shrink-0" /><span className="min-w-0 break-words">{bt(w)}</span></div>)}
      </div>}
      <div className="divide-y divide-line">
        {g.containers.map(c => <ContainerRow key={c.id} c={c} checked={picked.includes(c.name)} onToggle={() => setPicked(p => p.includes(c.name) ? p.filter(x => x !== c.name) : [...p, c.name])} />)}
      </div>
    </Card>
  )
}

function ContainerRow({ c, checked, onToggle }: { c: DockerContainer; checked: boolean; onToggle: () => void }) {
  const { t } = useT()
  const { bt, state } = useBackendText()
  const manual = c.risk === 'manual'
  return (
    <div className="px-4 @2xl:px-6 py-4 grid grid-cols-1 @3xl:grid-cols-[230px_minmax(0,1fr)] gap-x-6 gap-y-3 text-[13px] [&>*]:min-w-0">
      <div className="flex items-start gap-3 min-w-0">
        <button type="button" disabled={manual} onClick={onToggle} aria-label={c.name} className={cx('h-5 w-5 mt-0.5 rounded-[4px] border-2 border-[var(--edge)] grid place-items-center shrink-0', manual ? 'bg-sunk opacity-50' : checked ? 'bg-pick text-pick-ink' : 'bg-sunk')}>{checked && !manual && <Check size={13} strokeWidth={3} />}</button>
        <div className="min-w-0">
          <div className="font-semibold truncate">{c.name}</div>
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
            {manual ? <Badge tone="red"><ShieldAlert size={11} />{t('需手动处理', 'Manual')}</Badge> : <Badge tone="green"><ShieldCheck size={11} />{t('可自动接管', 'Auto')}</Badge>}
            <span className="text-[12px] text-muted flex items-center gap-1.5"><Dot status={c.state === 'running' ? 'running' : 'stopped'} />{state(c.state)}</span>
          </div>
          {c.service && <div className="mt-1 text-[12px] text-muted">compose: {c.service}</div>}
        </div>
      </div>
      <div className="min-w-0 space-y-2">
        <div className="flex flex-wrap items-center gap-2"><span className="font-mono text-[12.5px] text-fg2 truncate max-w-full">{c.image}</span><Badge tone="zinc">{c.pinned_image ? t('固定摘要', 'pinned digest') : t('本地镜像', 'local image')}</Badge>{!!c.mem_usage_mb && <span className="text-[12px] text-muted">{c.mem_usage_mb} MB</span>}</div>
        <div className="flex flex-wrap gap-x-4 gap-y-1">{c.ports.map((p, i) => <span key={i} className={cx('font-mono text-[12px]', p.host_port ? 'text-fg' : 'text-dim')}>{portText(p)}</span>)}</div>
        {c.mounts.map((m, i) => <div key={i} className="flex items-center gap-2 min-w-0 text-[12px]"><HardDrive size={13} className="text-accent shrink-0" /><span className="font-mono truncate" title={m.source}>{m.name || m.source}</span><ArrowRight size={11} className="text-dim shrink-0" /><span className="font-mono truncate">{m.destination}</span>{m.read_only && <Badge tone="zinc">ro</Badge>}</div>)}
        {c.risk_reasons?.map(r => <div key={r} className="flex items-start gap-1.5 text-[12.5px] text-danger"><ShieldAlert size={13} className="mt-0.5 shrink-0" /><span className="min-w-0 break-words">{bt(r)}</span></div>)}
        {c.warnings?.map(w => <div key={w} className="flex items-start gap-1.5 text-[12.5px] text-fg2"><AlertTriangle size={13} className="text-warn mt-0.5 shrink-0" /><span className="min-w-0 break-words">{bt(w)}</span></div>)}
      </div>
    </div>
  )
}

function Steps({ job }: { job: ImportJob }) {
  const { bt } = useBackendText()
  return (
    <div className="space-y-1">
      {job.steps.map((s, i) => (
        <div key={i} className="flex items-start gap-3 py-2">
          <span className={cx('h-6 w-6 rounded-[4px] border-[1.5px] border-[var(--edge)] grid place-items-center shrink-0',
            s.status === 'done' && 'bg-t-green text-ink', s.status === 'running' && 'bg-t-amber text-ink', s.status === 'failed' && 'bg-t-red text-ink', (s.status === 'pending' || s.status === 'skipped') && 'bg-sunk text-dim')}>
            {s.status === 'done' ? <Check size={13} strokeWidth={3} /> : s.status === 'running' ? <Loader2 size={13} className="animate-spin" /> : s.status === 'failed' ? <X size={13} strokeWidth={3} /> : s.status === 'skipped' ? <Minus size={13} /> : <Circle size={7} fill="currentColor" />}
          </span>
          <div className="min-w-0"><div className={cx('text-[13.5px] font-semibold', s.status === 'pending' && 'text-muted font-medium')}>{bt(s.name)}</div>{s.detail && <div className="text-[12.5px] text-muted mt-0.5 break-words">{bt(s.detail)}</div>}</div>
        </div>
      ))}
    </div>
  )
}

function Job({ job }: { job: ImportJob }) {
  const { t } = useT()
  const { bt } = useBackendText()
  const [busy, wrap] = useBusy()
  const [mem, setMem] = useState<string | null>(null)
  const [dlg, setDlg] = useState<'cutover' | 'rollback' | null>(null)
  if (job.mode !== 'docker') return <Card className="p-6 text-[13px]">{t('这不是 Docker 接管任务。', 'This is not a Docker adoption.')} <Link className="underline" to={`/import?job=${job.id}`}>{t('打开', 'Open')}</Link></Card>
  const s = job.status
  const rehearse = (ignore = false) => wrap(async () => {
    try {
      await api.rehearse(job.id, ignore ? { ignore_memory: true } : undefined)
      run(async () => undefined, { ok: t('已开始演练', 'Rehearsal started'), invalidate: ['imports'] })
    } catch (e) {
      if (e instanceof ApiError && e.code === 'insufficient_memory') setMem(e.message)
      else run(() => Promise.reject(e))
    }
  })
  const banner = (tone: string, icon: React.ReactNode, title: string, desc: React.ReactNode, right?: React.ReactNode) => (
    <Card className={cx('px-4 @2xl:px-6 py-5 flex flex-wrap items-center gap-4 mb-6 [&>svg]:shrink-0', tone)}>{icon}<div className="flex-1 min-w-[min(100%,220px)]"><div className="text-[15px] font-extrabold italic tracking-[-0.02em]">{title}</div><div className="text-[13px] mt-0.5 leading-6">{desc}</div></div>{right}</Card>
  )
  const rehearseBtn = (label: string) => <Button variant="primary" size="lg" disabled={busy} onClick={() => rehearse()}>{busy ? <Loader2 size={14} className="animate-spin" /> : <FlaskConical size={15} />}{label}</Button>
  const cutBtn = <Button variant="danger" size="lg" onClick={() => setDlg('cutover')}><Power size={15} />{t('开始切换', 'Cut over')}</Button>
  const rbBtn = <Button variant="danger-outline" size="lg" onClick={() => setDlg('rollback')}><Undo2 size={15} />{t('回滚', 'Roll back')}</Button>
  return (
    <>
      {BUSY.includes(s) ? banner('', <Loader2 size={22} className="animate-spin text-accent" />, { rehearsing: t('正在演练…', 'Rehearsing…'), cutting_over: t('正在切换…', 'Cutting over…'), rolling_back: t('正在回滚…', 'Rolling back…') }[s]!, t('可以离开此页，进度会保留。', 'You can leave this page; progress is kept.'))
        : s === 'planned' ? banner('', <FlaskConical size={22} className="text-accent" />, t('准备演练', 'Ready to rehearse'), t('Atoll 会在你的容器旁边启动副本：不发布端口、可写目录用临时空目录，检查健康后再停掉。你的容器照常服务。', 'Atoll starts copies next to your containers: no published ports, scratch dirs for writable data; checked for health, then stopped again. Your containers keep serving.'), rehearseBtn(t('开始演练', 'Rehearse')))
        : s === 'rehearsed' ? banner('!bg-t-green text-ink', <ShieldCheck size={22} />, t('演练通过', 'Rehearsal passed'), t('切换时停止你的旧容器（保留，重启策略改为 no），Atoll 服务在同样的端口和数据上启动。失败会自动回滚。', 'The cutover stops your old containers (kept, restart policy set to no) and starts the Atoll services on the same ports and data. A failure rolls back by itself.'), cutBtn)
        : s === 'live' ? banner('!bg-t-green text-ink', <ShieldCheck size={22} />, t('已由 Atoll 接管', 'Adopted by Atoll'), <>{t('不要再对这个项目运行 docker compose up，否则旧容器会和 Atoll 的同时启动。', 'Do not run docker compose up for this project any more: it would start the old containers next to Atoll\'s.')}</>,
          <div className="flex flex-wrap gap-2">{job.project_id && <Link to={`/projects/${job.project_id}`} className="btn-secondary h-10 px-5 inline-flex items-center gap-1.5 text-[13.5px]">{t('打开项目', 'Open project')}<ArrowRight size={14} /></Link>}{rbBtn}</div>)
        : s === 'rolled_back' ? banner('', <Undo2 size={22} className="text-accent" />, t('已回滚', 'Rolled back'), t('你的旧容器又在运行，Atoll 服务已停止。', 'Your old containers run again; the Atoll services are stopped.'), <div className="flex flex-wrap gap-2">{rehearseBtn(t('重新演练', 'Rehearse again'))}{cutBtn}</div>)
        : banner('!bg-t-red text-ink', <AlertTriangle size={22} />, t('失败', 'Failed'), t('查看下方失败的步骤。旧容器不会被删除。', 'See the failed step below. Old containers are never removed.'), <div className="flex flex-wrap gap-2">{rehearseBtn(t('重新演练', 'Rehearse again'))}{rbBtn}</div>)}
      <div className="grid grid-cols-1 @5xl:grid-cols-[420px_minmax(0,1fr)] gap-6 items-start [&>*]:min-w-0">
        <Card className="p-5"><div className="text-[13px] font-semibold mb-2 flex items-center gap-2">{t('步骤', 'Steps')}<span className="ml-auto text-[12px] text-muted font-normal font-mono">{job.group}</span></div><Steps job={job} /></Card>
        <div className="space-y-5">
          <Card className="overflow-hidden">
            <div className="px-5 h-12 flex items-center border-b border-line text-[13px] font-semibold">{t('服务', 'Services')}<span className="ml-auto text-[12px] text-muted font-normal">{job.plan.project_name}</span></div>
            {job.plan.services.map(sv => (
              <div key={sv.name} className="px-5 py-3 border-b border-line last:border-0 text-[13px] space-y-1.5">
                <div className="flex flex-wrap items-center gap-2"><span className="font-semibold">{sv.name}</span><span className="font-mono text-[12px] text-muted truncate">{sv.image}</span></div>
                <div className="flex flex-wrap gap-x-4 gap-y-1">{sv.ports.filter(p => p.public_port).map(p => <span key={p.name} className="font-mono text-[12px]">{p.host_ip ? p.host_ip + ':' : ''}{p.public_port}→{p.container_port}/{p.protocol}</span>)}</div>
                {sv.volumes.map(v => <div key={v.name} className="flex items-center gap-2 min-w-0 text-[12px]"><HardDrive size={13} className="text-accent shrink-0" /><span className="font-mono truncate">{v.host_path}</span><ArrowRight size={11} className="text-dim shrink-0" /><span className="font-mono truncate">{v.mount_path}</span>{v.read_only && <Badge tone="zinc">ro</Badge>}</div>)}
              </div>
            ))}
          </Card>
          {!!job.plan.warnings?.length && <Card className="p-5 space-y-1.5">{job.plan.warnings.map(w => <div key={w} className="flex items-start gap-1.5 text-[12.5px] text-fg2"><AlertTriangle size={13} className="text-warn mt-0.5 shrink-0" /><span className="min-w-0 break-words">{bt(w)}</span></div>)}</Card>}
        </div>
      </div>
      {mem && <ConfirmDialog title={t('内存可能不足', 'Memory may be insufficient')} action={t('仍然演练', 'Rehearse anyway')} danger={false} onClose={() => setMem(null)}
        description={<>{t('演练期间旧容器和 Atoll 副本同时运行。', 'Your containers and the Atoll copies run side by side during the rehearsal.')}<div className="mt-3 font-mono text-[12.5px] text-muted">{mem}</div></>}
        onConfirm={() => rehearse(true)} />}
      {dlg === 'cutover' && <ConfirmDialog title={t('切换到 Atoll', 'Cut over to Atoll')} action={t('确认切换', 'Cut over')} name={job.plan.project_name} onClose={() => setDlg(null)}
        description={t(`将停止 ${job.plan.services.length} 个旧容器（保留不删，重启策略改为 no），由 Atoll 在同样的端口和数据上启动服务。会有短暂停机；失败自动回滚。`, `Stops ${job.plan.services.length} old container(s) (kept, restart policy set to no) and starts the Atoll services on the same ports and data. Expect a short downtime; a failure rolls back by itself.`)}
        onConfirm={() => run(() => api.cutover(job.id), { ok: t('正在切换', 'Cutting over'), invalidate: ['imports'] })} />}
      {dlg === 'rollback' && <ConfirmDialog title={t('回滚', 'Roll back')} action={t('确认回滚', 'Roll back')} name={job.plan.project_name} onClose={() => setDlg(null)}
        description={t('停止 Atoll 服务，用原来的重启策略重新启动你的旧容器。', 'Stops the Atoll services and starts your old containers again with their original restart policy.')}
        onConfirm={() => run(() => api.rollbackImport(job.id), { ok: t('正在回滚', 'Rolling back'), invalidate: ['imports'] })} />}
    </>
  )
}
