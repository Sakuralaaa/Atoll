import { useState } from 'react'
import { IP } from '../../components/IP'
import { Link } from 'react-router-dom'
import { FlaskConical, Power, Trash2, Check, Loader2, Circle, X, Minus, Globe, AlertTriangle, ShieldCheck, Undo2, ArrowRight, PartyPopper } from 'lucide-react'
import { Badge, Button, Card, Dot } from '../../components/ui'
import { ConfirmDialog } from '../../components/feedback'
import { api, ApiError } from '../../api/client'
import { run, useBusy, useQuery } from '../../api/hooks'
import { bytes } from '../../api/view'
import type { ImportJob, Server } from '../../api/types'
import { cx } from '../../components/ui-utils'
import { LIVE, projBytes } from './model'
import { NodeCutoverCard, NodeWideNote, useNodeLive } from './NodeCutover'
import { useT } from '../../i18n'

// ---------------- steps 4-6 ----------------
function StepList({ job, from = 0, to }: { job: ImportJob; from?: number; to?: number }) {
  return (
    <div className="space-y-1">
      {job.steps.slice(from, to).map((s, i) => (
        <div key={i} className="flex items-start gap-3 py-2">
          <span className={cx('h-6 w-6 rounded-[4px] border-[1.5px] border-[var(--edge)] grid place-items-center shrink-0',
            s.status === 'done' && 'bg-[#86efac] text-[#141414]', s.status === 'running' && 'bg-[#fcd34d] text-[#141414]', s.status === 'failed' && 'bg-[#fca5a5] text-[#141414]', (s.status === 'pending' || s.status === 'skipped') && 'bg-sunk text-dim')}>
            {s.status === 'done' ? <Check size={13} strokeWidth={3} /> : s.status === 'running' ? <Loader2 size={13} className="animate-spin" /> : s.status === 'failed' ? <X size={13} strokeWidth={3} /> : s.status === 'skipped' ? <Minus size={13} /> : <Circle size={7} fill="currentColor" />}
          </span>
          <div className="min-w-0"><div className={cx('text-[13.5px] font-semibold', s.status === 'pending' && 'text-muted font-medium')}>{s.name}</div>{s.detail && <div className="text-[12.5px] text-muted mt-0.5 font-mono break-words">{s.detail}</div>}</div>
        </div>
      ))}
    </div>
  )
}
function VerifyTable({ job }: { job: ImportJob }) {
  const v = job.verify ?? []
  return (
    <Card className="overflow-hidden">
      <div className="px-5 min-h-12 py-2 flex flex-wrap gap-x-3 items-center border-b border-line text-[13px] font-semibold">数据核对<span className="ml-auto text-[12px] text-muted font-normal">Zeabur 源 vs Atoll 副本</span></div>
      {!v.length ? <div className="px-5 py-8 text-center text-[13px] text-dim">数据库恢复完成后显示行数对比</div> : <>
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_minmax(0,90px)_minmax(0,90px)_64px] gap-x-2 px-5 h-9 items-center text-[12px] text-muted border-b border-line"><span>服务</span><span>表 / 键空间</span><span className="text-right">Zeabur</span><span className="text-right">Atoll</span><span /></div>
        {v.map((r, i) => {
          const ok = r.source_rows === r.target_rows
          return (
            <div key={i} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_minmax(0,90px)_minmax(0,90px)_64px] gap-x-2 px-5 h-11 items-center text-[13px] border-b border-line last:border-0">
              <span className="text-fg2 truncate">{r.service}</span><span className="font-mono text-[12.5px] truncate">{r.table}</span>
              <span className="text-right tnum">{r.source_rows.toLocaleString()}</span><span className={cx('text-right tnum font-semibold', !ok && 'text-[#f59e0b]')}>{r.target_rows.toLocaleString()}</span>
              <span className="flex justify-end">{ok ? <Badge tone="green">一致</Badge> : LIVE.includes(job.status) ? <Badge tone="amber">同步中</Badge> : <Badge tone="red">不一致</Badge>}</span>
            </div>
          )
        })}
      </>}
    </Card>
  )
}

export function JobView({ job, step, set, openConfirm }: { job: ImportJob; step: number; set: (q: Record<string, string>) => void; openConfirm: boolean }) {
  const srv = useQuery('servers', api.servers).data?.find(s => s.id === job.server_id)
  if (job.mode !== 'zeabur-local' && !job.namespace) return <PlanJob job={job} />
  const name = job.plan.project_name
  const hosts = job.plan.services.flatMap(s => s.domains)
  const banner = (tone: string, icon: React.ReactNode, title: string, desc: React.ReactNode, right?: React.ReactNode) => (
    <Card className={cx('px-4 @2xl:px-6 py-5 flex flex-wrap items-center gap-4 mb-6 [&>svg]:shrink-0', tone)}>{icon}<div className="flex-1 min-w-[min(100%,220px)]"><div className="text-[15px] font-extrabold italic tracking-[-0.02em]">{title}</div><div className="text-[13px] mt-0.5 leading-6">{desc}</div></div>{right}</Card>
  )
  if (step === 4) {
    const done = job.status === 'rehearsed'
    return (
      <>
        {done ? banner('!bg-[#86efac] text-[#141414]', <ShieldCheck size={22} />, '演练通过', <>Atoll 副本已在本机运行，数据一致。Zeabur 仍在对外服务，用户无感知。</>, <Button variant="primary" size="lg" onClick={() => set({ step: 'cutover' })}>下一步：切换<ArrowRight size={15} /></Button>)
          : job.status === 'planned' ? banner('', <FlaskConical size={22} className="text-accent" />, '准备演练', '已创建 Atoll 项目和服务，尚未开始复制。', <RehearseBtn job={job} />)
          : job.status === 'failed' ? banner('!bg-[#fca5a5] text-[#141414]', <AlertTriangle size={22} />, '演练失败', '查看下方失败的步骤。Zeabur 未受影响。', <RehearseBtn job={job} label="重新演练" />)
          : banner('', <Loader2 size={22} className="animate-spin text-accent" />, '正在演练…', <><span className="font-semibold">Zeabur 继续对外服务</span>，Atoll 在本机起一套不接公网的副本并复制数据。可以离开此页，进度会保留。</>)}
        <div className="grid grid-cols-1 @5xl:grid-cols-[400px_minmax(0,1fr)] gap-6 items-start [&>*]:min-w-0">
          <Card className="p-5"><div className="text-[13px] font-semibold mb-2 flex items-center gap-2">演练步骤<span className="ml-auto text-[12px] text-muted font-normal">{name} · {srv?.name}</span></div><StepList job={job} to={6} /></Card>
          <div className="space-y-5">
            <VerifyTable job={job} />
            <Card className="px-5 py-4 flex items-center gap-3 text-[13px]"><Dot status="running" /><span className="font-semibold">Zeabur 继续对外服务</span><span className="text-muted">入口控制器仍占用 80/443，Atoll 容器只在项目内网可达</span></Card>
          </div>
        </div>
      </>
    )
  }
  if (step === 5 && (job.status === 'rehearsed' || job.status === 'cutting_over')) return <Cutover job={job} srv={srv} hosts={hosts} open={openConfirm} onBack={() => set({})} />
  if (step === 5) {
    const rolled = job.status === 'rolled_back', rolling = job.status === 'rolling_back'
    return (
      <>
        {rolled ? banner('', <Undo2 size={22} className="text-accent" />, '已回滚到 Zeabur', 'Zeabur 的工作负载和入口控制器已恢复，Atoll 副本已停止。可以随时重新演练。', <RehearseBtn job={job} label="重新演练" />)
          : banner('!bg-[#86efac] text-[#141414]', <PartyPopper size={22} />, rolling ? '正在回滚…' : '完成：已由 Atoll 接管', <>{name} 的 {job.plan.services.length} 个服务正在本机 Docker 中运行，atoll-caddy 接管了 80/443。Zeabur 的组件都还在，只是停着。</>,
            job.project_id && <Link to={`/projects/${job.project_id}`} className="btn-secondary h-10 px-5 inline-flex items-center gap-1.5 text-[13.5px]">打开项目<ArrowRight size={14} /></Link>)}
        <div className="grid grid-cols-1 @5xl:grid-cols-[minmax(0,1fr)_400px] gap-6 items-start [&>*]:min-w-0">
          <div className="space-y-5">
            <Card className="overflow-hidden">
              <div className="px-5 h-12 flex items-center border-b border-line text-[13px] font-semibold">访问地址</div>
              {hosts.length ? hosts.map(h => <div key={h} className="px-5 h-12 flex items-center gap-3 border-b border-line last:border-0 text-[13px]"><Globe size={15} className="text-accent" /><span className="font-semibold">{h}</span><Badge tone="green"><ShieldCheck size={11} />Cloudflare 代理</Badge><span className="ml-auto font-mono text-[12.5px] text-muted">A {srv?.public_ipv4}</span></div>)
                : <div className="px-5 py-6 text-[13px] text-dim">没有绑定公网域名</div>}
            </Card>
            <VerifyTable job={job} />
          </div>
          <div className="space-y-5">
            <Card className="p-6">
              <div className="text-[13px] font-semibold flex items-center gap-2"><Undo2 size={16} />有问题？一键回滚</div>
              <div className="text-[13px] text-muted mt-1.5 leading-6">停止 Atoll 服务，恢复 Zeabur 入口控制器，把 Zeabur 工作负载扩回原来的副本数。之后 Atoll 上产生的新数据不会写回 Zeabur。</div>
              <RollbackBtn job={job} disabled={rolled || rolling} />
            </Card>
            {!rolled && !rolling && (
              <Card className="p-6">
                <div className="text-[13px] font-semibold flex items-center gap-2"><Trash2 size={16} />最后一步：移除 Zeabur k3s</div>
                <div className="text-[13px] text-muted mt-1.5 leading-6">运行稳定几天后，再卸载 k3s 释放内存。卸载后不能回滚。</div>
                <Button className="mt-4 w-full" onClick={() => set({ step: 'cleanup' })}>前往移除<ArrowRight size={14} /></Button>
              </Card>
            )}
          </div>
        </div>
      </>
    )
  }
  return <Cleanup job={job} srv={srv} onBack={() => set({})} />
}
function RehearseBtn({ job, label = '开始演练' }: { job: ImportJob; label?: string }) {
  const { t } = useT()
  const [busy, wrap] = useBusy()
  const [mem, setMem] = useState<string | null>(null)
  const start = () => wrap(async () => {
    try {
      await api.rehearse(job.id)
      run(async () => undefined, { ok: '已开始演练', invalidate: ['imports'] })
    } catch (e) {
      if (e instanceof ApiError && e.code === 'insufficient_memory') setMem(e.message)
      else run(() => Promise.reject(e))
    }
  })
  return <>
    <Button variant="primary" size="lg" disabled={busy} onClick={start}>{busy ? <Loader2 size={14} className="animate-spin" /> : <FlaskConical size={15} />}{label}</Button>
    {mem && <ConfirmDialog title={t('内存可能不足', 'Memory may be insufficient')} action={t('仍然演练', 'Rehearse anyway')} onClose={() => setMem(null)}
      description={<>{t('演练期间 Zeabur 和 Atoll 两份副本同时运行。建议一次只演练一个项目，或先释放内存。', 'Zeabur and the Atoll copies run side by side during the rehearsal. Rehearse one project at a time or free memory first.')}<div className="mt-3 font-mono text-[12.5px] text-muted">{mem}</div></>}
      onConfirm={() => run(() => api.rehearse(job.id, { ignore_memory: true }), { ok: '已开始演练', invalidate: ['imports'] })} />}
  </>
}
function RollbackBtn({ job, disabled }: { job: ImportJob; disabled?: boolean }) {
  const { t } = useT()
  const [open, setOpen] = useState(false)
  const others = useNodeLive(job)
  const node = others.length > 0 && !!job.server_id
  return <>
    {node && <div className="text-[12.5px] text-muted mt-3 leading-5">{t(
      `同一台服务器上还有 ${others.length} 个已切换的项目（${others.map(j => j.plan.project_name).join('、')}）共用入口控制器，只能一起回滚。`,
      `${others.length} other cut-over project(s) on this server (${others.map(j => j.plan.project_name).join(', ')}) share the ingress; they roll back together.`)}</div>}
    <button disabled={disabled} onClick={() => setOpen(true)} className="btn-danger mt-4 w-full h-12 inline-flex items-center justify-center gap-2 text-[15px] disabled:opacity-50 disabled:pointer-events-none"><Undo2 size={18} />{node ? t(`整体回滚 ${others.length + 1} 个项目`, `Roll back all ${others.length + 1} projects`) : '回滚到 Zeabur'}</button>
    {open && <ConfirmDialog title="回滚到 Zeabur" action="确认回滚" name={job.plan.project_name} onClose={() => setOpen(false)}
      description={node
        ? <>{t(`将停止这台服务器上 ${others.length + 1} 个已切换项目的 Atoll 服务，恢复 Zeabur 入口控制器并扩回它们的工作负载。切换后写进 Atoll 的数据不会同步回 Zeabur。`, `Stops the Atoll services of all ${others.length + 1} cut-over projects on this server, restores Zeabur's ingress and scales their workloads back. Data written to Atoll after the cutover is not synced back to Zeabur.`)}</>
        : <>将停止 Atoll 上的 <b>{job.plan.services.length}</b> 个服务，恢复 Zeabur 入口控制器并扩回工作负载。切换后写进 Atoll 的数据不会同步回 Zeabur。</>}
      onConfirm={() => run<unknown>(() => node ? api.zeaburNodeRollback(job.server_id!) : api.rollbackImport(job.id), { ok: '正在回滚', invalidate: ['imports'] })} />}
  </>
}

function Cutover({ job, srv, hosts, open, onBack }: { job: ImportJob; srv?: Server; hosts: string[]; open: boolean; onBack: () => void }) {
  const [confirm, setConfirm] = useState(open)
  const running = job.status === 'cutting_over'
  return (
    <>
      <div className="grid grid-cols-1 @5xl:grid-cols-[minmax(0,1fr)_400px] gap-6 items-start [&>*]:min-w-0">
        <div className="space-y-5">
          <Card className="p-6">
            <div className="text-[15px] font-semibold">切换会做什么</div>
            <div className="mt-4 space-y-3 text-[13.5px]">
              {[['缩容 Zeabur 应用到 0', '数据库保持运行，直到最后一次导出'], ['最终数据同步', '增量 rsync 数据卷，重新 dump/restore 数据库并再次核对行数'], ['停用 Zeabur 入口控制器', '整台服务器共用：所有 Zeabur 项目同时停止由 Zeabur 服务；记录原配置，回滚时原样恢复'], ['atoll-caddy 接管 80/443', '用同样的主机名提供 HTTPS，新域名在 Cloudflare 自动建记录']].map(([t, d], i) => (
                <div key={t} className="flex gap-3"><span className="h-6 w-6 rounded-[4px] border-[1.5px] border-[var(--edge)] bg-[#c4b5fd] text-[#141414] grid place-items-center text-[12px] font-extrabold font-mono shrink-0">{i + 1}</span><div><div className="font-semibold">{t}</div><div className="text-[12.5px] text-muted mt-0.5">{d}</div></div></div>
              ))}
            </div>
          </Card>
          <Card className="p-6 flex gap-5 items-center">
            <div className="tile h-12 w-12 rounded-lg bg-[#86efac] text-[#141414] grid place-items-center shrink-0"><Globe size={22} /></div>
            <div className="flex-1"><div className="text-[14px] font-semibold">同一台机器，同一个 IP，DNS 不用动</div><div className="text-[13px] text-muted mt-1 leading-6">服务仍在 <span className="font-mono text-fg"><IP v={srv?.public_ipv4} /></span> 上，只是从 Zeabur 的 k3s 换到了 Atoll 的 Docker。已有的 A 记录继续有效；只有 *.zeabur.app 地址会失效{hosts.length ? '，新地址会在切换时创建' : ''}。预计停机不到一分钟。</div></div>
          </Card>
          {running && <Card className="p-5"><div className="text-[13px] font-semibold mb-2 flex items-center gap-2"><Loader2 size={14} className="animate-spin text-accent" />正在切换…</div><StepList job={job} from={6} /></Card>}
          {!running && <NodeWideNote />}
        </div>
        <div className="space-y-5">
        {!running && <NodeCutoverCard job={job} srv={srv} />}
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-3">新的访问地址</div>
          {hosts.length ? hosts.map(h => <div key={h} className="flex items-center gap-2 h-9 text-[13px]"><Globe size={14} className="text-accent" /><span className="font-semibold">{h}</span><span className="ml-auto text-[12px] text-dim">A {srv?.public_ipv4}</span></div>) : <div className="text-[13px] text-dim">无</div>}
          <div className="h-px bg-line my-5" />
          <div className="text-[12.5px] text-muted leading-5 mb-4">切换后如果有问题，可以在完成页一键回滚到 Zeabur。</div>
          <div className="flex gap-2">
            <Button onClick={onBack} disabled={running}>返回演练</Button>
            <Button variant="danger" className="flex-1" disabled={running} onClick={() => setConfirm(true)}>{running ? <Loader2 size={14} className="animate-spin" /> : <Power size={15} />}开始切换</Button>
          </div>
        </Card>
        </div>
      </div>
      {confirm && <ConfirmDialog title="切换到 Atoll" action="确认切换" name={job.plan.project_name} onClose={() => setConfirm(false)}
        description={<>Zeabur 上 <b>{job.plan.project_name}</b> 的应用会缩容到 0，入口控制器停用，由 Atoll 接管 <IP v={srv?.public_ipv4} empty="" /> 的 80/443。DNS 无需改动；zeabur.app 地址将失效。之后可一键回滚。</>}
        onConfirm={() => run(() => api.cutover(job.id), { ok: '正在切换', invalidate: ['imports'] })} />}
    </>
  )
}

function Cleanup({ job, srv, onBack }: { job: ImportJob; srv?: Server; onBack: () => void }) {
  const inv = useQuery(job.server_id && job.status !== 'cleaned' ? `zeabur:${job.server_id}` : null, () => api.zeaburScan(job.server_id!)).data
  const [keep, setKeep] = useState(true)
  const [open, setOpen] = useState(false)
  const memMb = inv?.k3s_mem_mb ?? 1200
  const pvcBytes = inv?.projects.reduce((a, p) => a + projBytes(p), 0) ?? 0
  if (job.status === 'cleaned') return (
    <Card className="px-4 @2xl:px-6 py-6 flex flex-wrap items-center gap-4 !bg-[#86efac] text-[#141414]"><PartyPopper size={24} className="shrink-0" /><div className="flex-1 min-w-[min(100%,200px)]"><div className="text-[16px] font-extrabold italic tracking-[-0.02em]">迁移全部完成</div><div className="text-[13px] mt-0.5">Zeabur 的 k3s 已从 {srv?.name} 上移除。{keep ? 'PVC 备份保存在 /var/lib/atoll/zeabur-backup。' : ''}</div></div>{job.project_id && <Link to={`/projects/${job.project_id}`} className="btn-secondary h-10 px-5 inline-flex items-center gap-1.5 text-[13.5px]">打开项目<ArrowRight size={14} /></Link>}</Card>
  )
  const busy = job.status === 'cleaning'
  return (
    <div className="grid grid-cols-1 @5xl:grid-cols-[minmax(0,1fr)_400px] gap-6 items-start [&>*]:min-w-0">
      <div className="space-y-5">
        <Card className="px-6 py-5 flex items-start gap-4 !bg-[#fca5a5] text-[#141414]">
          <AlertTriangle size={22} className="shrink-0 mt-0.5" />
          <div><div className="text-[15px] font-extrabold italic tracking-[-0.02em]">不可逆操作</div><div className="text-[13.5px] mt-1 leading-6 font-semibold">移除后无法回滚到 Zeabur，可释放约 {memMb.toLocaleString()} MB 内存。</div>
            <div className="text-[13px] mt-1 leading-6">会运行 <span className="font-mono">/usr/local/bin/k3s-uninstall.sh</span>，删除 k3s、Zeabur 的全部工作负载、入口控制器和 PVC 目录。Atoll 的容器与数据不受影响。</div></div>
        </Card>
        <Card className="p-6">
          <button type="button" onClick={() => setKeep(v => !v)} className="flex items-start gap-3 text-left w-full">
            <span className={cx('h-5 w-5 mt-0.5 rounded-[4px] border-2 border-[var(--edge)] grid place-items-center shrink-0', keep ? 'bg-[#8b5cf6] text-white' : 'bg-sunk')}>{keep && <Check size={13} strokeWidth={3} />}</span>
            <div><div className="text-[13.5px] font-semibold">先备份 Zeabur 数据卷</div><div className="text-[13px] text-muted mt-0.5">把每个 PVC 目录打包到 <span className="font-mono">/var/lib/atoll/zeabur-backup/&lt;pvc&gt;.tgz</span>{pvcBytes ? `，约 ${bytes(pvcBytes)}` : ''}。确认用不上后可手动删除。</div></div>
          </button>
        </Card>
        {busy && <Card className="p-5 flex items-center gap-3 text-[13px]"><Loader2 size={16} className="animate-spin text-accent" />正在移除 Zeabur k3s…</Card>}
      </div>
      <Card className="p-6">
        <div className="space-y-2.5 text-[13px]">
          <div className="flex justify-between"><span className="text-muted">服务器</span><span className="font-semibold">{srv?.name}</span></div>
          <div className="flex justify-between"><span className="text-muted">k3s</span><span className="font-semibold">{inv?.k3s_version ?? '—'}</span></div>
          <div className="flex justify-between"><span className="text-muted">预计释放内存</span><span className="font-semibold tnum">≈ {memMb.toLocaleString()} MB</span></div>
          <div className="flex justify-between"><span className="text-muted">备份数据卷</span><span className="font-semibold">{keep ? '是' : '否'}</span></div>
        </div>
        <div className="flex gap-2 mt-6"><Button onClick={onBack}>返回</Button><Button variant="danger" className="flex-1" disabled={busy || !srv} onClick={() => setOpen(true)}><Trash2 size={15} />移除 Zeabur k3s</Button></div>
      </Card>
      {open && srv && <ConfirmDialog title="移除 Zeabur k3s" action="永久移除" name={srv.name} onClose={() => setOpen(false)}
        description={<>将从 <b>{srv.name}</b> 卸载 k3s 和所有 Zeabur 组件。<b>移除后无法回滚到 Zeabur</b>，可释放约 {memMb.toLocaleString()} MB 内存。{keep ? '数据卷会先打包备份。' : '不保留数据卷备份。'}</>}
        onConfirm={() => run(() => api.cleanupImport(job.id, { confirm: srv.name, keep_backup: keep }), { ok: '正在移除 Zeabur k3s', invalidate: ['imports'] })} />}
    </div>
  )
}

function PlanJob({ job }: { job: ImportJob }) {
  const [busy, wrap] = useBusy()
  return (
    <Card className="p-6">
      <div className="flex items-center gap-3 mb-3"><div className="text-[15px] font-semibold flex-1">{job.plan.project_name} <span className="text-[12.5px] text-muted font-normal">· CLI 导入计划</span></div>
        {job.status === 'planned' && <Button variant="primary" disabled={busy} onClick={() => wrap(() => run(() => api.startImport(job.id), { ok: '开始部署', invalidate: ['imports'] }))}>开始部署</Button>}</div>
      <StepList job={job} />
    </Card>
  )
}
