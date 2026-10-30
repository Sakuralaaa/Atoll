import { useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import {
  Server as ServerIcon, ScanSearch, ListChecks, FlaskConical, Power, Trash2, Check, Loader2, Circle, X, Minus,
  HardDrive, MemoryStick, Cpu, Globe, AlertTriangle, Link2, Database, ChevronRight, ChevronDown, Upload, RotateCcw, ShieldCheck, Box, Undo2, ArrowRight, Settings2, PartyPopper,
} from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, Dot, ProviderIcon, ServiceIcon, cx } from '../components/ui'
import { CodeBlock, ConfirmDialog, CopyButton, Empty, ErrorState, Label, Load, RowsSkeleton, Select, Skeleton, TextInput } from '../components/feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import { ago, bytes, colorFor, flagFor, gb, letterFor, usagePct } from '../api/view'
import type { ImportJob, ImportPlan, JoinToken, Server, ZeaburInventory, ZeaburProject, ZeaburReference, ZeaburService } from '../api/types'

const STEPS = [
  { n: 1, l: '选择服务器', i: <ServerIcon size={15} /> },
  { n: 2, l: '扫描', i: <ScanSearch size={15} /> },
  { n: 3, l: '选择服务', i: <ListChecks size={15} /> },
  { n: 4, l: '演练', i: <FlaskConical size={15} /> },
  { n: 5, l: '切换', i: <Power size={15} /> },
  { n: 6, l: '移除 Zeabur k3s', i: <Trash2 size={15} /> },
]
const ROLE: Record<string, [string, 'violet' | 'amber' | 'green' | 'zinc']> = { app: ['应用', 'violet'], database: ['数据库', 'amber'], cache: ['缓存', 'green'], other: ['其他', 'zinc'] }
const isZeaburApp = (h: string) => h.endsWith('.zeabur.app')
const ZSTATUS: Record<string, [string, 'green' | 'amber' | 'red' | 'zinc']> = {
  running: ['运行中', 'green'], progressing: ['更新中', 'amber'], degraded: ['降级', 'amber'], failed: ['失败', 'red'], suspended: ['已暂停', 'zinc'], unknown: ['状态未知', 'zinc'],
}
const zStatus = (s: ZeaburService) => s.status || (!s.workload ? 'suspended' : s.replicas ? 'running' : 'unknown')
const STORE: Record<string, string> = { postgres: 'PostgreSQL', mysql: 'MySQL', redis: 'Redis', mongo: 'MongoDB', queue: '消息队列', search: '搜索引擎', other: '其他数据存储' }
const VIA: Record<string, string> = { 'env-value': '变量值', 'zeabur-internal': 'zeabur.internal', 'shared-secret': '共享 Secret', 'shared-configmap': '共享 ConfigMap' }
function StatusBadge({ s }: { s: ZeaburService }) {
  const [l, t] = ZSTATUS[zStatus(s)] ?? [zStatus(s), 'zinc']
  return <Badge tone={t}>{l}</Badge>
}
function Refs({ refs, names }: { refs: ZeaburReference[]; names: Record<string, string> }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="text-[12px] text-muted flex items-center gap-1 mr-0.5"><Link2 size={13} />引用</span>
      {refs.map((r, i) => (
        <span key={i} title={[VIA[r.via] ?? r.via, r.object].filter(Boolean).join(' · ')} className={cx('chip px-2 h-6 inline-flex items-center gap-1 text-[11.5px] font-mono bg-card', r.inferred && 'border-dashed')}>
          {r.key ? <span className="text-fg2">{r.key}</span> : <span className="text-muted">{r.object ?? '整体共享'}</span>}
          <ArrowRight size={11} className="text-accent shrink-0" />
          <span className="font-semibold text-fg">{names[r.target] ?? r.target}</span>{r.target_key && <span className="text-muted">.{r.target_key}</span>}
          {r.inferred && <span className="font-sans not-italic text-[10.5px] text-muted">推断</span>}
        </span>
      ))}
    </div>
  )
}
const LIVE = ['rehearsing', 'cutting_over', 'rolling_back', 'cleaning', 'deploying', 'uploading', 'verifying']

function jobStep(j: ImportJob, want: string | null) {
  if (['cleaning', 'cleaned'].includes(j.status)) return 6
  if (['live', 'rolling_back', 'rolled_back'].includes(j.status)) return want === 'cleanup' && j.status === 'live' ? 6 : 5
  if (j.status === 'cutting_over') return 5
  if (j.status === 'rehearsed' && want === 'cutover') return 5
  return 4
}

export default function Import() {
  const [sp, setSp] = useSearchParams()
  const jobId = sp.get('job'), server = sp.get('server'), stepQ = sp.get('step'), ns = sp.get('ns')
  const job = useQuery(jobId ? `imports:${jobId}` : null, () => api.importJob(jobId!), { interval: 2000 })
  const go = (q: Record<string, string>) => setSp(q)
  const step = jobId ? (job.data ? jobStep(job.data, stepQ) : 4) : server ? (stepQ === 'select' && ns ? 3 : 2) : 1
  const completed = jobId && job.data?.status === 'cleaned' ? 7 : jobId && job.data && ['live'].includes(job.data.status) && step === 5 ? 6 : step
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-14 pt-12 pb-16">
        <div className="flex items-end justify-between mb-2">
          <div>
            <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em] flex items-center gap-3">从 Zeabur 迁移<Badge tone="violet">原地接管</Badge></h1>
            <p className="text-[13.5px] text-muted mt-2 max-w-[760px] leading-6">在 Zeabur 托管的那台 VPS 上装好 Atoll Agent，服务原样搬进本机 Docker：镜像从本机导出，数据卷本机复制，数据库同版本导出恢复。同一台机器、同一个 IP；Zeabur 上的东西一样不删，随时可以回滚。</p>
          </div>
          {(jobId || server) && <Button onClick={() => setSp({})}><X size={14} />重新开始</Button>}
        </div>
        <Stepper step={step} completed={completed} />
        <div className="mt-7">
          {step === 1 && <StepServer onPick={id => go({ server: id })} />}
          {step === 2 && server && <StepScan serverId={server} onPick={n => go({ server, step: 'select', ns: n })} />}
          {step === 3 && server && ns && <StepSelect serverId={server} ns={ns} onBack={() => go({ server })} onCreated={id => go({ job: id })} />}
          {jobId && <Load q={job} skeleton={<RowsSkeleton n={6} />}>{j => <JobView job={j} step={step} set={q => go({ job: j.id, ...q })} openConfirm={sp.get('confirm') === '1'} />}</Load>}
        </div>
        <History current={jobId} />
        <AdvancedCli />
      </div>
    </MainShell>
  )
}

function Stepper({ step, completed }: { step: number; completed: number }) {
  return (
    <div className="mt-7 flex items-center gap-2 flex-wrap">
      {STEPS.map((s, i) => {
        const done = s.n < completed && s.n !== step || completed > 6
        const active = s.n === step && completed <= 6
        return (
          <div key={s.n} className="flex items-center gap-2">
            <div className={cx('h-10 pl-1.5 pr-3.5 rounded-lg inline-flex items-center gap-2 text-[13px] font-semibold border-2', active ? 'nav-active' : done ? 'border-[var(--edge)] bg-card text-fg' : 'border-dashed border-line2 text-muted')}>
              <span className={cx('h-6 w-6 rounded-[4px] grid place-items-center text-[12px] font-extrabold font-mono border-[1.5px] border-[var(--edge)] not-italic', done ? 'bg-[#86efac] text-[#141414]' : active ? 'bg-[#8b5cf6] text-white' : 'bg-sunk text-muted')}>{done ? <Check size={13} strokeWidth={3} /> : s.n}</span>
              {s.l}
            </div>
            {i < STEPS.length - 1 && <ChevronRight size={15} className="text-dim" />}
          </div>
        )
      })}
    </div>
  )
}

// ---------------- step 1 ----------------
function StepServer({ onPick }: { onPick: (id: string) => void }) {
  const q = useQuery('servers', api.servers)
  const list = (q.data ?? []).filter(s => s.role !== 'control')
  const [tok, setTok] = useState<JoinToken>(); const [busy, wrap] = useBusy()
  return (
    <div className="grid grid-cols-[1fr_420px] gap-6 items-start">
      <div>
        <div className="text-[15px] font-semibold mb-1">Zeabur 托管的是哪台服务器？</div>
        <div className="text-[13px] text-muted mb-4">选择已经装好 Atoll Agent 的那台 VPS，下一步会读取本机 k3s 里的 Zeabur 项目。只读，不会改动任何东西。</div>
        {q.error && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <RowsSkeleton n={3} />}
        {q.data && !list.length && <Empty icon={<ServerIcon size={22} />} title="还没有连上的服务器" desc="先在右侧生成安装命令，在 Zeabur 托管的 VPS 上运行。" />}
        <div className="space-y-3">
          {list.map(s => <ServerPick key={s.id} s={s} onPick={() => onPick(s.id)} />)}
        </div>
      </div>
      <Card className="p-6">
        <div className="text-[13px] font-semibold flex items-center gap-2"><Box size={16} />还没装 Agent？</div>
        <div className="text-[13px] text-muted mt-1.5 leading-6">在 Zeabur 托管的 VPS 上以 root 运行安装命令。Agent 与 Zeabur 的 k3s 并存，不占 80/443，直到你确认切换。</div>
        <div className="mt-4">
          {tok ? <>
            <div className="[&>div]:whitespace-pre-wrap [&>div]:break-all"><CodeBlock>{tok.command}</CodeBlock></div>
            <div className="mt-3 flex items-center text-[12px] text-dim">一次性令牌 · {new Date(tok.expires_at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })} 前有效<span className="ml-auto"><CopyButton text={tok.command} label="复制命令" /></span></div>
          </> : <Button variant="primary" disabled={busy} onClick={() => wrap(async () => { const t = await run(api.joinToken); if (t) setTok(t) })}>{busy && <Loader2 size={14} className="animate-spin" />}生成安装命令</Button>}
        </div>
      </Card>
    </div>
  )
}
function ServerPick({ s, onPick }: { s: Server; onPick: () => void }) {
  const u = usagePct(s), on = s.status === 'online'
  return (
    <button disabled={!on} onClick={onPick} className={cx('w-full text-left rounded-2xl surface px-5 py-4 flex items-center gap-4', on ? 'surface-hover' : 'opacity-60')}>
      <ProviderIcon provider={s.provider || '?'} online={on} />
      <div className="flex-1 min-w-0">
        <div className="text-[15px] font-semibold truncate">{s.name}</div>
        <div className="text-[13px] text-muted mt-0.5 flex items-center gap-3"><span className="font-mono">{s.public_ipv4}</span><span>{flagFor(s.region)} {s.region}</span><span>{s.cpu_cores} vCPU · {gb(s.mem_mb)} · {s.arch}</span></div>
      </div>
      <div className="text-right text-[12.5px] text-muted tnum w-28">{on ? <>内存 {u.mem}% · 硬盘 {u.disk}%</> : '离线'}</div>
      <span className="btn-secondary h-9 px-3.5 inline-flex items-center gap-1.5 text-[13px]">扫描<ArrowRight size={14} /></span>
    </button>
  )
}

// ---------------- step 2 ----------------
const useScan = (serverId: string) => useQuery(`zeabur:${serverId}`, () => api.zeaburScan(serverId))
const projBytes = (p: ZeaburProject) => p.services.reduce((a, s) => a + s.volumes.reduce((b, v) => b + v.bytes, 0), 0)

function StepScan({ serverId, onPick }: { serverId: string; onPick: (ns: string) => void }) {
  const q = useScan(serverId)
  const srv = useQuery('servers', api.servers).data?.find(s => s.id === serverId)
  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <div><div className="text-[15px] font-semibold">在 {srv?.name ?? serverId} 上找到的 Zeabur 项目</div><div className="text-[13px] text-muted mt-0.5">读取本机 k3s 的 Deployment / StatefulSet / PVC / Ingress，环境变量中的密钥已隐藏。</div></div>
        <Button onClick={q.refetch}><RotateCcw size={14} />重新扫描</Button>
      </div>
      {q.error && <ErrorState error={q.error} onRetry={q.refetch} />}
      {!q.data && !q.error && <ScanSkeleton />}
      {q.data && <ScanResult inv={q.data} onPick={onPick} />}
    </>
  )
}
function ScanSkeleton() {
  return <div className="space-y-5"><div className="grid grid-cols-4 gap-4">{[0, 1, 2, 3].map(i => <Card key={i} className="p-5"><Skeleton className="h-3 w-20" /><Skeleton className="h-6 w-28 mt-3" /></Card>)}</div><RowsSkeleton n={3} /></div>
}
function Stat({ i, l, v, sub, warn }: { i: React.ReactNode; l: string; v: React.ReactNode; sub?: string; warn?: boolean }) {
  return <Card className={cx('px-5 py-4', warn && '!bg-[#fcd34d] text-[#141414]')}><div className={cx('flex items-center gap-1.5 text-[12.5px]', warn ? '' : 'text-muted')}>{i}{l}</div><div className="text-[20px] font-extrabold italic tracking-[-0.02em] mt-1 tnum">{v}</div>{sub && <div className={cx('text-[12px] mt-0.5', warn ? '' : 'text-dim')}>{sub}</div>}</Card>
}
function ScanResult({ inv, onPick }: { inv: ZeaburInventory; onPick: (ns: string) => void }) {
  const total = inv.projects.reduce((a, p) => a + projBytes(p), 0)
  if (!inv.k3s) return <Empty icon={<ScanSearch size={22} />} title="这台服务器上没有 Zeabur" desc="没有检测到 k3s。确认选择的是 Zeabur 托管的那台 VPS，或者用下方「高级：用 CLI 导入」。" />
  return (
    <>
      <div className="grid grid-cols-4 gap-4">
        <Stat i={<Cpu size={14} />} l="Zeabur k3s" v={inv.k3s_version ?? '已安装'} sub={inv.ingress_running ? '入口控制器运行中 · 占用 80/443' : '入口控制器未运行'} />
        <Stat i={<HardDrive size={14} />} l="可用硬盘" v={`${inv.disk_free_gb.toFixed(1)} GB`} sub={`演练需要约 ${bytes(total * 1.2)}（数据 ×1.2）`} warn={inv.disk_free_gb * 2 ** 30 < total * 1.2} />
        <Stat i={<MemoryStick size={14} />} l="可用内存" v={gb(inv.mem_free_mb)} sub="演练期间两套服务同时运行" warn={inv.mem_free_mb < 1024} />
        <Stat i={<ListChecks size={14} />} l="发现" v={`${inv.projects.length} 个项目`} sub={`${inv.projects.reduce((a, p) => a + p.services.length, 0)} 个服务 · 数据 ${bytes(total)}`} />
      </div>
      <div className="mt-6 space-y-6">
        {inv.projects.map(p => (
          <Card key={p.namespace} className="overflow-hidden">
            <div className="px-6 py-4 flex items-center gap-4 border-b-2 border-[var(--edge)] bg-card2">
              <ServiceIcon s={{ kind: 'image', letter: letterFor(p.project_name), color: colorFor(p.project_name) }} size={36} />
              <div className="flex-1"><div className="text-[15px] font-semibold">{p.project_name} <span className="text-[12.5px] text-muted font-normal ml-1">· {p.environment_name}</span></div><div className="font-mono text-[11.5px] text-dim mt-0.5">{p.namespace}</div></div>
              <span className="text-[13px] text-muted">{p.services.length} 个服务 · {bytes(projBytes(p))}</span>
              <Button variant="primary" onClick={() => onPick(p.namespace)}>迁移这个项目<ArrowRight size={14} /></Button>
            </div>
            {!!p.warnings?.length && (
              <div className="px-6 py-3 border-b border-line bg-[#fcd34d]/20 space-y-1">
                {p.warnings.map(w => <div key={w} className="flex items-start gap-1.5 text-[12.5px] text-fg"><AlertTriangle size={13} className="text-[#d97706] mt-0.5 shrink-0" /><span className="min-w-0 break-words">{w}</span></div>)}
              </div>
            )}
            <div className="divide-y divide-line">{p.services.map(s => <ZRow key={s.service_id} s={s} names={Object.fromEntries(p.services.map(x => [x.service_id, x.name]))} />)}</div>
          </Card>
        ))}
      </div>
    </>
  )
}
function ImageBadge({ s }: { s: ZeaburService }) {
  if (s.zeabur_registry) return s.image_cached ? <Badge tone="violet">Zeabur 构建 · 将从本机导出</Badge> : <Badge tone="red">Zeabur 构建 · 本机无缓存</Badge>
  return <Badge tone="zinc">公共镜像 · 固定摘要</Badge>
}
function ZRow({ s, names }: { s: ZeaburService; names: Record<string, string> }) {
  const [rl, rt] = ROLE[s.role] ?? ROLE.other
  return (
    <div className="px-6 py-4 grid grid-cols-[230px_minmax(0,1fr)_240px] gap-6 text-[13px]">
      <div className="flex items-start gap-3 min-w-0">
        <ServiceIcon s={{ kind: s.kind, letter: letterFor(s.name), color: colorFor(s.name) }} size={30} />
        <div className="min-w-0">
          <div className="font-semibold truncate">{s.name}</div>
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5"><Badge tone={rt}>{rl}</Badge><StatusBadge s={s} /></div>
          <div className="mt-1.5 text-[12px] text-muted flex items-center gap-1.5">{s.workload ? <><Dot status={s.replicas ? 'running' : 'stopped'} />{s.replicas} 副本 · {s.workload.split('/')[0]}</> : '无工作负载（只有 PVC）'}</div>
        </div>
      </div>
      <div className="min-w-0 space-y-2">
        <div className="flex flex-wrap items-center gap-2 min-w-0"><span className="font-mono text-[12.5px] text-fg2 truncate max-w-full">{s.image}</span><ImageBadge s={s} /></div>
        {s.data_store && <div className="flex items-center gap-1.5 text-[12.5px] text-fg2"><Database size={13} className="text-accent shrink-0" />数据存储 · <span className="font-semibold">{STORE[s.data_store] ?? s.data_store}</span>{['postgres', 'mysql', 'mongo', 'redis'].includes(s.data_store) ? <span className="text-muted">· 同主版本导出恢复</span> : <span className="text-muted">· 按目录复制</span>}</div>}
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-muted">
          {s.ports.map(p => <span key={p.name} className="font-mono text-[12px]">{p.container_port}/{p.protocol}</span>)}
          {s.node_ports?.map(n => <span key={n} className="font-mono text-[12px] text-dim">NodePort {n}</span>)}
          {s.domains.map(d => <span key={d} className="flex items-center gap-1.5 min-w-0"><Globe size={13} className="shrink-0" /><span className={cx('font-medium truncate', isZeaburApp(d) ? 'text-[#ef4444] line-through decoration-2' : 'text-fg')}>{d}</span>{isZeaburApp(d) && <Badge tone="red">迁移后失效</Badge>}</span>)}
        </div>
        {!!s.references?.length && <Refs refs={s.references} names={names} />}
        {s.warnings?.map(w => <div key={w} className="flex items-start gap-1.5 text-[12.5px] text-fg2"><AlertTriangle size={13} className="text-[#f59e0b] mt-0.5 shrink-0" /><span className="min-w-0 break-words">{w}</span></div>)}
      </div>
      <div className="space-y-1.5 min-w-0">
        {!s.volumes.length && <span className="text-dim">无数据卷</span>}
        {s.volumes.map(v => <div key={v.pvc} className="flex items-center gap-2 min-w-0"><HardDrive size={13} className="text-accent shrink-0" /><span className="font-mono text-[12px] truncate flex-1" title={v.host_dir}>{v.mount_path}</span><span className="tnum text-muted shrink-0">{bytes(v.bytes)}</span></div>)}
      </div>
    </div>
  )
}

// ---------------- step 3 ----------------
function StepSelect({ serverId, ns, onBack, onCreated }: { serverId: string; ns: string; onBack: () => void; onCreated: (id: string) => void }) {
  const q = useScan(serverId)
  const settings = useQuery('settings', api.settings)
  const p = q.data?.projects.find(x => x.namespace === ns)
  if (q.error) return <ErrorState error={q.error} onRetry={q.refetch} />
  if (!q.data || !settings.data) return <ScanSkeleton />
  if (!p) return <Empty title="扫描结果里没有这个项目" action={<Button onClick={onBack}>返回扫描</Button>} />
  return <SelectForm key={ns} p={p} serverId={serverId} base={settings.data.base_domain} cfReady={!!settings.data.base_domain && settings.data.has_cloudflare_token} onBack={onBack} onCreated={onCreated} />
}
function SelectForm({ p, serverId, base, cfReady, onBack, onCreated }: { p: ZeaburProject; serverId: string; base: string; cfReady: boolean; onBack: () => void; onCreated: (id: string) => void }) {
  const [sel, setSel] = useState<Set<string>>(() => new Set(p.services.filter(s => s.workload || s.volumes.length).map(s => s.service_id)))
  const [name, setName] = useState(p.project_name)
  const chosen = p.services.filter(s => sel.has(s.service_id))
  const zHosts = [...new Set(chosen.flatMap(s => s.domains.filter(isZeaburApp)))]
  const [map, setMap] = useState<Record<string, string>>({})
  const target = (h: string) => map[h] ?? (base ? `${h.replace(/\.zeabur\.app$/, '')}.${base}` : '')
  const rewrite = chosen.flatMap(s => s.env.filter(e => /zeabur\.app/.test(e.value)).map(e => ({ svc: s.name, key: e.key })))
  const data = chosen.reduce((a, s) => a + s.volumes.reduce((b, v) => b + v.bytes, 0), 0)
  const exportN = chosen.filter(s => s.zeabur_registry).length
  const [busy, wrap] = useBusy()
  const toggle = (id: string) => setSel(v => { const n = new Set(v); n.has(id) ? n.delete(id) : n.add(id); return n })
  const needCf = zHosts.length > 0 && !cfReady
  const names = Object.fromEntries(p.services.map(x => [x.service_id, x.name]))
  const dangling = chosen.flatMap(s => (s.references ?? []).filter(r => !sel.has(r.target) && names[r.target]).map(r => ({ from: s.name, to: names[r.target], key: r.key })))
  const submit = () => wrap(async () => {
    const domain_map = Object.fromEntries(zHosts.map(h => [h, target(h).trim()]))
    const j = await run(() => api.createZeaburLocal({ server_id: serverId, namespace: p.namespace, service_ids: chosen.map(s => s.service_id), project_name: name.trim(), domain_map }))
    if (!j) return
    const r = await run(() => api.rehearse(j.id), { ok: '已开始演练，Zeabur 继续对外服务', invalidate: ['imports'] })
    onCreated((r ?? j).id)
  })
  return (
    <div className="grid grid-cols-[1fr_380px] gap-6 items-start">
      <div className="space-y-6">
        <div>
          <div className="text-[15px] font-semibold mb-1">要迁移的服务</div>
          <div className="text-[13px] text-muted mb-4">来自 Zeabur 项目 {p.project_name}。环境变量原样带过来，<span className="font-mono">*.zeabur.internal</span> 会改写成 Atoll 内网主机名。</div>
          <Card className="divide-y divide-line">
            {p.services.map(s => {
              const on = sel.has(s.service_id), [rl, rt] = ROLE[s.role] ?? ROLE.other
              return (
                <button key={s.service_id} onClick={() => toggle(s.service_id)} className="w-full text-left px-5 h-16 flex items-center gap-4 hover:bg-ov/[0.02]">
                  <span className={cx('h-5 w-5 rounded-[4px] border-2 border-[var(--edge)] grid place-items-center shrink-0', on ? 'bg-[#8b5cf6] text-white' : 'bg-sunk')}>{on && <Check size={13} strokeWidth={3} />}</span>
                  <ServiceIcon s={{ kind: s.kind, letter: letterFor(s.name), color: colorFor(s.name) }} size={30} />
                  <div className="flex-1 min-w-0"><div className="text-[13.5px] font-semibold flex items-center gap-2">{s.name}<Badge tone={rt}>{rl}</Badge>{zStatus(s) !== 'running' && <StatusBadge s={s} />}</div><div className="font-mono text-[12px] text-muted truncate mt-0.5">{s.image}</div></div>
                  <ImageBadge s={s} />
                  <span className="w-20 text-right text-[12.5px] text-muted tnum">{bytes(s.volumes.reduce((a, v) => a + v.bytes, 0))}</span>
                </button>
              )
            })}
          </Card>
          {dangling.length > 0 && (
            <div className="mt-3 rounded-[4px] border-2 border-[var(--edge)] bg-[#fcd34d] text-[#141414] px-4 py-3 text-[12.5px]">
              <div className="font-bold flex items-center gap-1.5"><AlertTriangle size={14} />有服务引用了没选中的服务</div>
              {dangling.map((d, i) => <div key={i} className="mt-1 font-mono">{d.from}{d.key ? `.${d.key}` : ''} → {d.to}</div>)}
              <div className="mt-1">迁移后这些连接会断开，除非你之后手动改成新地址。</div>
            </div>
          )}
        </div>

        <div>
          <div className="text-[15px] font-semibold mb-1 flex items-center gap-2"><Globe size={16} />域名</div>
          <div className="text-[13px] text-muted mb-4">切换时自动在 Cloudflare 创建记录；zeabur.app 地址届时失效。</div>
          {!zHosts.length ? <Card className="px-6 py-5 text-[13px] text-muted">选中的服务没有 zeabur.app 域名。</Card> : needCf ? (
            <Card className="px-6 py-5 flex items-center gap-4 !bg-[#fcd34d] text-[#141414]">
              <Settings2 size={20} />
              <div className="flex-1"><div className="text-[13.5px] font-bold">先设置主域名和 Cloudflare 令牌</div><div className="text-[13px] mt-0.5">这些服务目前只有 zeabur.app 地址，切换后就会失效。需要一个由 Cloudflare 托管的主域名来生成新地址。</div></div>
              <Link to="/settings" className="btn-secondary h-9 px-3.5 inline-flex items-center gap-1.5 text-[13px]">去设置<ArrowRight size={14} /></Link>
            </Card>
          ) : (
            <Card className="overflow-hidden">
              <div className="grid grid-cols-[1fr_32px_1.2fr] gap-4 px-6 h-10 items-center text-[12.5px] text-muted border-b border-line"><span>Zeabur 地址</span><span /><span>新地址（Cloudflare 代理）</span></div>
              {zHosts.map(h => (
                <div key={h} className="grid grid-cols-[1fr_32px_1.2fr] gap-4 px-6 h-16 items-center border-b border-line last:border-0">
                  <span className="flex items-center gap-2 min-w-0"><span className="font-mono text-[13px] text-[#ef4444] line-through decoration-2 truncate">{h}</span><Badge tone="red">迁移后失效</Badge></span>
                  <ArrowRight size={16} className="text-accent" />
                  <TextInput mono className="w-full" value={target(h)} onChange={e => setMap(m => ({ ...m, [h]: e.target.value }))} placeholder="留空则不绑定" />
                </div>
              ))}
            </Card>
          )}
          {rewrite.length > 0 && (
            <div className="mt-4 rounded-xl border-2 border-dashed border-line2 px-5 py-4">
              <div className="text-[13px] font-semibold flex items-center gap-2">环境变量<Badge tone="green">将自动改写</Badge></div>
              <div className="text-[12.5px] text-muted mt-1 mb-3">这些变量的值引用了 zeabur.app，会改写成上面的新地址。</div>
              <div className="flex flex-wrap gap-2">{rewrite.map(r => <span key={r.svc + r.key} className="chip px-2.5 h-7 inline-flex items-center gap-1.5 bg-card font-mono text-[12px]"><span className="text-muted">{r.svc}</span>{r.key}</span>)}</div>
            </div>
          )}
        </div>
      </div>

      <Card className="p-6 sticky top-6">
        <Label t="Atoll 项目名称"><TextInput className="w-full" value={name} onChange={e => setName(e.target.value)} /></Label>
        <div className="space-y-2.5 text-[13px] mt-2">
          <div className="flex justify-between"><span className="text-muted">服务</span><span className="font-semibold">{chosen.length} 个</span></div>
          <div className="flex justify-between"><span className="text-muted">从本机导出镜像</span><span className="font-semibold">{exportN} 个</span></div>
          <div className="flex justify-between"><span className="text-muted">数据</span><span className="font-semibold tnum">{bytes(data)}</span></div>
          <div className="flex justify-between"><span className="text-muted">新域名</span><span className="font-semibold">{zHosts.filter(h => target(h)).length} 个</span></div>
        </div>
        <div className="mt-5 rounded-lg field px-4 py-3 text-[12.5px] text-fg2 leading-5 flex gap-2"><ShieldCheck size={15} className="text-emerald-500 shrink-0 mt-0.5" />下一步只是演练：Atoll 在本机起一套副本、复制数据并核对行数，不接公网流量，Zeabur 继续对外服务。</div>
        <div className="mt-5 flex gap-2">
          <Button onClick={onBack}>返回</Button>
          {needCf ? <Link to="/settings" className="flex-1"><Button className="w-full">先完成域名设置</Button></Link>
            : <Button variant="primary" className="flex-1" disabled={!chosen.length || !name.trim() || busy} onClick={submit}>{busy ? <Loader2 size={14} className="animate-spin" /> : <FlaskConical size={15} />}创建并开始演练</Button>}
        </div>
      </Card>
    </div>
  )
}

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
      <div className="px-5 h-12 flex items-center border-b border-line text-[13px] font-semibold">数据核对<span className="ml-auto text-[12px] text-muted font-normal">Zeabur 源 vs Atoll 副本</span></div>
      {!v.length ? <div className="px-5 py-8 text-center text-[13px] text-dim">数据库恢复完成后显示行数对比</div> : <>
        <div className="grid grid-cols-[1fr_1.4fr_100px_100px_70px] px-5 h-9 items-center text-[12px] text-muted border-b border-line"><span>服务</span><span>表 / 键空间</span><span className="text-right">Zeabur</span><span className="text-right">Atoll</span><span /></div>
        {v.map((r, i) => {
          const ok = r.source_rows === r.target_rows
          return (
            <div key={i} className="grid grid-cols-[1fr_1.4fr_100px_100px_70px] px-5 h-11 items-center text-[13px] border-b border-line last:border-0">
              <span className="text-fg2">{r.service}</span><span className="font-mono text-[12.5px] truncate">{r.table}</span>
              <span className="text-right tnum">{r.source_rows.toLocaleString()}</span><span className={cx('text-right tnum font-semibold', !ok && 'text-[#f59e0b]')}>{r.target_rows.toLocaleString()}</span>
              <span className="flex justify-end">{ok ? <Badge tone="green">一致</Badge> : LIVE.includes(job.status) ? <Badge tone="amber">同步中</Badge> : <Badge tone="red">不一致</Badge>}</span>
            </div>
          )
        })}
      </>}
    </Card>
  )
}

function JobView({ job, step, set, openConfirm }: { job: ImportJob; step: number; set: (q: Record<string, string>) => void; openConfirm: boolean }) {
  const srv = useQuery('servers', api.servers).data?.find(s => s.id === job.server_id)
  if (job.mode !== 'zeabur-local' && !job.namespace) return <PlanJob job={job} />
  const name = job.plan.project_name
  const hosts = job.plan.services.flatMap(s => s.domains)
  const banner = (tone: string, icon: React.ReactNode, title: string, desc: React.ReactNode, right?: React.ReactNode) => (
    <Card className={cx('px-6 py-5 flex items-center gap-4 mb-6', tone)}>{icon}<div className="flex-1"><div className="text-[15px] font-extrabold italic tracking-[-0.02em]">{title}</div><div className="text-[13px] mt-0.5 leading-6">{desc}</div></div>{right}</Card>
  )
  if (step === 4) {
    const done = job.status === 'rehearsed'
    return (
      <>
        {done ? banner('!bg-[#86efac] text-[#141414]', <ShieldCheck size={22} />, '演练通过', <>Atoll 副本已在本机运行，数据一致。Zeabur 仍在对外服务，用户无感知。</>, <Button variant="primary" size="lg" onClick={() => set({ step: 'cutover' })}>下一步：切换<ArrowRight size={15} /></Button>)
          : job.status === 'planned' ? banner('', <FlaskConical size={22} className="text-accent" />, '准备演练', '已创建 Atoll 项目和服务，尚未开始复制。', <RehearseBtn job={job} />)
          : job.status === 'failed' ? banner('!bg-[#fca5a5] text-[#141414]', <AlertTriangle size={22} />, '演练失败', '查看下方失败的步骤。Zeabur 未受影响。', <RehearseBtn job={job} label="重新演练" />)
          : banner('', <Loader2 size={22} className="animate-spin text-accent" />, '正在演练…', <><span className="font-semibold">Zeabur 继续对外服务</span>，Atoll 在本机起一套不接公网的副本并复制数据。可以离开此页，进度会保留。</>)}
        <div className="grid grid-cols-[400px_1fr] gap-6 items-start">
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
        <div className="grid grid-cols-[1fr_400px] gap-6 items-start">
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
  const [busy, wrap] = useBusy()
  return <Button variant="primary" size="lg" disabled={busy} onClick={() => wrap(() => run(() => api.rehearse(job.id), { ok: '已开始演练', invalidate: [`imports`] }))}>{busy ? <Loader2 size={14} className="animate-spin" /> : <FlaskConical size={15} />}{label}</Button>
}
function RollbackBtn({ job, disabled }: { job: ImportJob; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  return <>
    <button disabled={disabled} onClick={() => setOpen(true)} className="btn-danger mt-4 w-full h-12 inline-flex items-center justify-center gap-2 text-[15px] disabled:opacity-50 disabled:pointer-events-none"><Undo2 size={18} />回滚到 Zeabur</button>
    {open && <ConfirmDialog title="回滚到 Zeabur" action="确认回滚" name={job.plan.project_name} onClose={() => setOpen(false)}
      description={<>将停止 Atoll 上的 <b>{job.plan.services.length}</b> 个服务，恢复 Zeabur 入口控制器并扩回工作负载。切换后写进 Atoll 的数据不会同步回 Zeabur。</>}
      onConfirm={() => run(() => api.rollbackImport(job.id), { ok: '正在回滚', invalidate: ['imports'] })} />}
  </>
}

function Cutover({ job, srv, hosts, open, onBack }: { job: ImportJob; srv?: Server; hosts: string[]; open: boolean; onBack: () => void }) {
  const [confirm, setConfirm] = useState(open)
  const running = job.status === 'cutting_over'
  return (
    <>
      <div className="grid grid-cols-[1fr_400px] gap-6 items-start">
        <div className="space-y-5">
          <Card className="p-6">
            <div className="text-[15px] font-semibold">切换会做什么</div>
            <div className="mt-4 space-y-3 text-[13.5px]">
              {[['缩容 Zeabur 应用到 0', '数据库保持运行，直到最后一次导出'], ['最终数据同步', '增量 rsync 数据卷，重新 dump/restore 数据库并再次核对行数'], ['停用 Zeabur 入口控制器', '记录原配置，回滚时原样恢复'], ['atoll-caddy 接管 80/443', '用同样的主机名提供 HTTPS，新域名在 Cloudflare 自动建记录']].map(([t, d], i) => (
                <div key={t} className="flex gap-3"><span className="h-6 w-6 rounded-[4px] border-[1.5px] border-[var(--edge)] bg-[#c4b5fd] text-[#141414] grid place-items-center text-[12px] font-extrabold font-mono shrink-0">{i + 1}</span><div><div className="font-semibold">{t}</div><div className="text-[12.5px] text-muted mt-0.5">{d}</div></div></div>
              ))}
            </div>
          </Card>
          <Card className="p-6 flex gap-5 items-center">
            <div className="tile h-12 w-12 rounded-lg bg-[#86efac] text-[#141414] grid place-items-center shrink-0"><Globe size={22} /></div>
            <div className="flex-1"><div className="text-[14px] font-semibold">同一台机器，同一个 IP，DNS 不用动</div><div className="text-[13px] text-muted mt-1 leading-6">服务仍在 <span className="font-mono text-fg">{srv?.public_ipv4}</span> 上，只是从 Zeabur 的 k3s 换到了 Atoll 的 Docker。已有的 A 记录继续有效；只有 *.zeabur.app 地址会失效{hosts.length ? '，新地址会在切换时创建' : ''}。预计停机不到一分钟。</div></div>
          </Card>
          {running && <Card className="p-5"><div className="text-[13px] font-semibold mb-2 flex items-center gap-2"><Loader2 size={14} className="animate-spin text-accent" />正在切换…</div><StepList job={job} from={6} /></Card>}
        </div>
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
      {confirm && <ConfirmDialog title="切换到 Atoll" action="确认切换" name={job.plan.project_name} onClose={() => setConfirm(false)}
        description={<>Zeabur 上 <b>{job.plan.project_name}</b> 的应用会缩容到 0，入口控制器停用，由 Atoll 接管 {srv?.public_ipv4} 的 80/443。DNS 无需改动；zeabur.app 地址将失效。之后可一键回滚。</>}
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
    <Card className="px-6 py-6 flex items-center gap-4 !bg-[#86efac] text-[#141414]"><PartyPopper size={24} /><div className="flex-1"><div className="text-[16px] font-extrabold italic tracking-[-0.02em]">迁移全部完成</div><div className="text-[13px] mt-0.5">Zeabur 的 k3s 已从 {srv?.name} 上移除。{keep ? 'PVC 备份保存在 /var/lib/atoll/zeabur-backup。' : ''}</div></div>{job.project_id && <Link to={`/projects/${job.project_id}`} className="btn-secondary h-10 px-5 inline-flex items-center gap-1.5 text-[13.5px]">打开项目<ArrowRight size={14} /></Link>}</Card>
  )
  const busy = job.status === 'cleaning'
  return (
    <div className="grid grid-cols-[1fr_400px] gap-6 items-start">
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

// ---------------- history & advanced ----------------
const JOB_TEXT: Record<string, [string, 'green' | 'amber' | 'red' | 'zinc' | 'violet']> = {
  planned: ['待开始', 'zinc'], rehearsing: ['演练中', 'amber'], rehearsed: ['演练通过', 'violet'], cutting_over: ['切换中', 'amber'], live: ['已接管', 'green'],
  rolling_back: ['回滚中', 'amber'], rolled_back: ['已回滚', 'zinc'], cleaning: ['移除 k3s 中', 'amber'], cleaned: ['已完成', 'green'],
  uploading: ['上传中', 'amber'], deploying: ['部署中', 'amber'], verifying: ['校验中', 'amber'], done: ['已完成', 'green'], failed: ['失败', 'red'],
}
function History({ current }: { current: string | null }) {
  const q = useQuery('imports', api.imports, { interval: 10000 })
  const nav = useNavigate()
  if (!q.data?.length) return null
  return (
    <div className="mt-12">
      <div className="text-[13px] font-semibold mb-3">迁移记录</div>
      <Card className="divide-y divide-line">
        {q.data.map(j => {
          const [t, tone] = JOB_TEXT[j.status] ?? [j.status, 'zinc']
          const done = j.steps.filter(s => s.status === 'done').length
          return (
            <button key={j.id} onClick={() => nav(`/import?job=${j.id}`)} className={cx('w-full text-left px-6 h-14 grid grid-cols-[1.3fr_1fr_1fr_120px_110px_20px] items-center gap-4 text-[13px] hover:bg-ov/[0.02]', current === j.id && 'bg-ov/[0.04]')}>
              <span className="font-semibold truncate">{j.plan.project_name}</span>
              <span className="text-muted">{j.mode === 'zeabur-local' ? '原地接管' : 'CLI 计划'} · {j.plan.services.length} 个服务</span>
              <span className="text-muted tnum">{done}/{j.steps.length} 步</span>
              <span className="text-muted">{ago(j.created_at)}</span>
              <span><Badge tone={tone}>{t}</Badge></span>
              <ChevronRight size={15} className="text-dim" />
            </button>
          )
        })}
      </Card>
    </div>
  )
}

function AdvancedCli() {
  const [open, setOpen] = useState(false)
  const [plan, setPlan] = useState<ImportPlan>(); const [err, setErr] = useState<string>(); const [target, setTarget] = useState('')
  const servers = (useQuery(open ? 'servers' : null, api.servers).data ?? []).filter(s => s.role !== 'control')
  const file = useRef<HTMLInputElement>(null)
  const nav = useNavigate()
  const [busy, wrap] = useBusy()
  const load = async (f?: File) => {
    if (!f) return
    try { const p = JSON.parse(await f.text()) as ImportPlan; if (!p.services) throw new Error('不是有效的 plan.json（缺少 services）'); setPlan(p); setErr(undefined); setTarget(p.target_server_id || '') }
    catch (e) { setErr((e as Error).message); setPlan(undefined) }
  }
  const tgt = target || servers[0]?.id || ''
  const submit = () => wrap(async () => {
    if (!plan) return
    const j = await run(() => api.createImport({ ...plan, target_server_id: tgt }), { ok: '已创建导入任务', invalidate: ['imports'] })
    if (j) { const s = await run(() => api.startImport(j.id), { ok: '开始部署' }); nav(`/import?job=${(s ?? j).id}`); setPlan(undefined) }
  })
  const dataBytes = useMemo(() => plan?.services.reduce((a, s) => a + (s.data_bytes || 0), 0) ?? 0, [plan])
  return (
    <div className="mt-10">
      <button onClick={() => setOpen(v => !v)} className="flex items-center gap-2 text-[13px] font-semibold text-fg2 hover:text-fg">{open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}高级：用 CLI 导入<span className="text-muted font-normal">（Zeabur 托管的不是你自己的服务器时）</span></button>
      {open && (
        <div className="mt-4 grid grid-cols-2 gap-5">
          <Card className="p-5">
            <div className="text-[13px] font-semibold mb-1">在已登录 <span className="font-mono">npx zeabur</span> 的电脑上运行</div>
            <div className="text-[12.5px] text-muted mb-3">镜像需推送到你自己的仓库，数据经中控中转上传。</div>
            <CodeBlock>{`atoll import zeabur inventory          # 列出 Zeabur 项目
atoll import zeabur plan -p <id> > plan.json
atoll import zeabur images plan.json   # 导出并推送镜像
atoll import zeabur data plan.json     # 上传卷与数据库
atoll import zeabur apply plan.json
atoll import zeabur cutover plan.json`}</CodeBlock>
          </Card>
          <Card className="p-5">
            <div className="text-[13px] font-semibold mb-3">或在这里上传 plan.json</div>
            <input ref={file} type="file" accept="application/json,.json" className="hidden" onChange={e => load(e.target.files?.[0])} />
            {!plan ? (
              <button onClick={() => file.current?.click()} onDragOver={e => e.preventDefault()} onDrop={e => { e.preventDefault(); load(e.dataTransfer.files[0]) }} className="w-full h-[150px] rounded-xl border-2 border-dashed border-line2 grid place-items-center text-[13px] text-muted hover:text-fg hover:border-[var(--edge)]">
                <span className="flex flex-col items-center gap-2"><Upload size={20} />点击或拖入 plan.json</span>
              </button>
            ) : (
              <div>
                <div className="flex items-center gap-2 text-[13.5px] font-semibold">{plan.project_name}<span className="text-muted font-normal text-[12.5px]">· {plan.services.length} 个服务 · {bytes(dataBytes)}</span><button onClick={() => setPlan(undefined)} className="ml-auto text-muted hover:text-fg"><X size={15} /></button></div>
                <div className="mt-2 flex flex-wrap gap-1.5">{plan.services.map(s => <span key={s.name} className="chip px-2 h-6 inline-flex items-center bg-card text-[12px] font-mono">{s.name}</span>)}</div>
                {plan.warnings?.map(w => <div key={w} className="mt-2 flex items-start gap-1.5 text-[12.5px] text-fg2"><AlertTriangle size={13} className="text-[#f59e0b] mt-0.5 shrink-0" />{w}</div>)}
                <div className="mt-4 flex gap-2"><Select className="flex-1" value={tgt} onChange={e => setTarget(e.target.value)}>{servers.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}</Select><Button variant="primary" disabled={busy || !tgt} onClick={submit}>创建并开始</Button></div>
              </div>
            )}
            {err && <div className="mt-3 text-[12.5px] text-[#ef4444]">{err}</div>}
          </Card>
        </div>
      )}
    </div>
  )
}

