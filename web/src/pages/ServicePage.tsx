import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Activity, KeyRound, ArchiveRestore, BarChart3, Network, HardDrive, Settings, RotateCw, Rocket, Square, Play, Globe, FolderOpen, ScrollText, SquareTerminal, ExternalLink, Pencil, Trash2, Plus, Eye, EyeOff, Lock, Cpu, MemoryStick, ArrowDownUp, Copy, Cloud, Box, Download, History, ShieldCheck, Check, X, Loader2, Undo2, Share2 } from 'lucide-react'
import { ProjectShell } from '../components/Shell'
import { Badge, Button, Card, Chart, CopyId, Dot, Mini, ServiceIcon, Spark, Tabs, statusText, cx } from '../components/ui'
import { ConfirmDialog, Empty, ErrorState, Label, Modal, RowsSkeleton, Select, Skeleton, TextInput, Toggle, CardSkeleton } from '../components/feedback'
import AddServiceModal from '../components/AddServiceModal'
import { api, isDemo, type Range } from '../api/client'
import { invalidate, run, useBusy, useQuery } from '../api/hooks'
import { ago, bytesRate, day, dateTime, deployStatusText, latestDeploy, primaryPort, svcIcon, timeLabels, triggerText } from '../api/view'
import type { Deployment, Domain, EnvVar, Project, Server, Service } from '../api/types'
import { RangePicker } from './ServerDetail'

const TABS = [
  { k: 'status', l: '服务状态', icon: <Activity size={15} /> },
  { k: 'env', l: '环境变量', icon: <KeyRound size={15} /> },
  { k: 'backup', l: '备份还原', icon: <ArchiveRestore size={15} /> },
  { k: 'usage', l: '用量', icon: <BarChart3 size={15} /> },
  { k: 'network', l: '网络', icon: <Network size={15} /> },
  { k: 'disk', l: '硬盘', icon: <HardDrive size={15} /> },
  { k: 'settings', l: '设置', icon: <Settings size={15} /> },
]

export const useProject = (pid?: string) => useQuery(pid ? `project:${pid}` : null, () => api.project(pid!))
export const placeholder = (id = ''): Project => ({ id, name: '…', server_id: '', created_at: '' })

export default function ServicePage() {
  const { pid, sid } = useParams()
  const [sp, setSp] = useSearchParams()
  const nav = useNavigate()
  const pq = useProject(pid)
  const p = pq.data
  const s = p?.services?.find(x => x.id === sid)
  const srv = useQuery('servers', api.servers).data?.find(x => x.id === p?.server_id)
  const tab = sp.get('tab') ?? 'status'
  const refresh = () => invalidate(`project:${pid}`, 'projects')
  return (
    <ProjectShell project={p ?? placeholder(pid)} active={sid}>
      <div className="max-w-[1320px] mx-auto px-14 pt-10 pb-16">
        {pq.error && !p && <ErrorState error={pq.error} onRetry={pq.refetch} />}
        {!p && !pq.error && <><div className="flex gap-4"><Skeleton className="h-[52px] w-[52px]" /><div className="space-y-2 flex-1"><Skeleton className="h-7 w-64" /><Skeleton className="h-3.5 w-96" /></div></div><div className="mt-10"><CardSkeleton n={1} cols={1} h={260} /></div></>}
        {p && !s && <Empty title="找不到这个服务" desc="它可能已被删除。" action={<Link to={`/projects/${p.id}`}><Button>返回项目</Button></Link>} />}
        {p && s && <>
          <Header s={s} onChange={refresh} />
          <Tabs className="mt-7" items={TABS} value={tab} onChange={k => setSp({ tab: k })} />
          <div className="mt-6">
            {tab === 'status' && <StatusTab p={p} s={s} srv={srv} />}
            {tab === 'env' && <EnvTab s={s} />}
            {tab === 'backup' && <BackupTab />}
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
  const act = (a: 'restart' | 'redeploy') => wrap(() => run(() => api.action(s.id, a), { ok: a === 'restart' ? '正在重新启动' : '已触发重新部署' }).then(onChange))
  return (
    <div className="flex items-start gap-4">
      <ServiceIcon s={svcIcon(s)} size={52} />
      <div className="flex-1">
        <div className="flex items-center gap-3"><h1 className="text-[26px] font-extrabold italic tracking-[-0.03em] leading-tight">{s.name}</h1><Badge tone={tone(s.status)}>{statusText[s.status] ?? s.status}</Badge></div>
        <div className="mt-1 flex items-center gap-4 text-[13px] text-muted"><CopyId v={s.id} /><span>创建于 {day(s.created_at)}</span></div>
      </div>
      <div className="flex gap-2"><Button disabled={busy} onClick={() => act('restart')}><RotateCw size={15} />重新启动</Button><Button variant="primary" disabled={busy} onClick={() => act('redeploy')}><Rocket size={15} />重新部署</Button></div>
    </div>
  )
}

function StatusTab({ p, s, srv }: { p: Project; s: Service; srv?: Server }) {
  const base = `/projects/${p.id}/services/${s.id}`
  const deps = useQuery(`deployments:${s.id}`, () => api.deployments(s.id))
  const m = useQuery(`svc-metrics:${s.id}:1h`, () => api.serviceMetrics(s.id, '1h'), { interval: 60000 })
  const last = latestDeploy(deps.data)
  const [hist, setHist] = useState(false)
  const [bind, setBind] = useState(false)
  const [busy, wrap] = useBusy()
  const stopped = s.status === 'stopped'
  const cpu = (m.data ?? []).slice(-30).map(x => x.cpu_pct), mem = (m.data ?? []).slice(-30).map(x => x.mem_mb)
  const doms = s.domains ?? []
  return (
    <>
      <Card className="p-6">
        <div className="flex items-start gap-8">
          <div className="flex-1">
            <div className="flex items-center gap-3"><Dot status={s.status} /><span className="text-[17px] font-semibold tracking-tight">{statusText[s.status] ?? s.status}</span><span className="text-muted text-[13px]">· {s.replicas}</span></div>
            <div className="text-[13px] text-muted mt-1">{last ? <>最近一次部署 {ago(last.started_at)} · 由「{triggerText[last.trigger] ?? last.trigger}」触发{last.message ? ` · ${last.message}` : ''}</> : deps.data ? '还没有部署记录' : <Skeleton className="h-3.5 w-64" />}</div>
            <div className="mt-6 grid grid-cols-[100px_1fr] gap-y-3.5 text-[13px]">
              <span className="text-muted">镜像来源</span><span className="flex items-center gap-2 font-mono text-[13px] min-w-0"><Box size={14} className="text-[#60a5fa] shrink-0" /><span className="truncate">{s.image || '—'}</span></span>
              <span className="text-muted">镜像摘要</span><span className="font-mono text-[13px] text-fg2">{last?.image_digest ? `${last.image_digest.slice(0, 15)}…${last.image_digest.slice(-4)}` : '—'}</span>
              <span className="text-muted">服务器</span><span className="flex items-center gap-2">{srv ? <><Mini provider={srv.provider ?? '?'} />{srv.name}</> : '—'}</span>
              <span className="text-muted">资源</span><span>CPU {s.cpu_limit ? `${s.cpu_limit} 核` : '不限'} · 内存 {s.mem_limit_mb ? `${s.mem_limit_mb} MB` : '不限'} · 重启策略 unless-stopped</span>
            </div>
          </div>
          <div className="w-[380px] space-y-3">
            {isDemo() ? <Uptime failed={s.status === 'failed'} /> : <RecentDeploys deps={deps.data} />}
            <div className="grid grid-cols-2 gap-3">
              {([['CPU', cpu.length ? `${cpu[cpu.length - 1].toFixed(1)}%` : '—', cpu, '#8b5cf6'], ['内存', mem.length ? `${Math.round(mem[mem.length - 1])} MB` : '—', mem, '#38bdf8']] as const).map(([l, v, d, c]) => (
                <div key={l} className="rounded-xl field px-4 py-3">
                  <div className="text-[12px] text-muted">{l}</div>
                  <div className="flex items-end justify-between mt-1"><span className="text-[17px] font-semibold tnum">{v}</span><Spark data={[...d]} color={c} /></div>
                </div>
              ))}
            </div>
          </div>
        </div>
        <div className="mt-6 pt-5 border-t border-line flex items-center gap-2">
          <Button size="sm" onClick={() => setHist(true)}><History size={14} />部署记录</Button>
          {stopped ? <Button size="sm" disabled={busy} onClick={() => wrap(() => run(() => api.action(s.id, 'start'), { ok: '正在启动', invalidate: [`project:${p.id}`] }))}><Play size={13} />启动</Button>
            : <Button size="sm" disabled={busy} onClick={() => wrap(() => run(() => api.action(s.id, 'stop'), { ok: '已停止', invalidate: [`project:${p.id}`] }))}><Square size={13} />停止</Button>}
          <span className="ml-auto text-[13px] text-dim">回滚到任意历史镜像摘要</span>
        </div>
      </Card>

      <div className="mt-6 text-[13px] font-semibold mb-3">域名</div>
      {doms.length ? <div className="space-y-3">{doms.map(d => (
        <Card key={d.id} className="px-6 py-4 flex items-center gap-4">
          <Globe size={18} className="text-accent" />
          <a href={`https://${d.hostname}`} target="_blank" rel="noreferrer" className="text-[15px] font-semibold flex items-center gap-1.5 hover:underline">{d.hostname}<ExternalLink size={13} /></a>
          <DomainBadge d={d} />
          <span className="ml-auto flex items-center gap-1.5 text-[13px] text-muted">{d.proxied ? <><Cloud size={14} className="text-[#f6821f]" />Cloudflare 代理</> : '仅 DNS'} → {srv?.public_ipv4 ?? '—'}</span>
        </Card>
      ))}</div> : <Card className="px-6 py-5 text-[13px] text-muted flex items-center justify-between">此服务未公开访问，仅项目内网可达<Button size="sm" disabled={!s.ports?.some(x => x.protocol === 'http')} onClick={() => setBind(true)}><Plus size={14} />绑定域名</Button></Card>}

      <div className="mt-6 grid grid-cols-3 gap-5">
        {([
          [<FolderOpen size={22} />, '文件', '浏览容器与卷中的文件', `${base}/files`],
          [<ScrollText size={22} />, '日志', '实时运行日志', `${base}/logs`],
          [<SquareTerminal size={22} />, '命令', '在容器内执行命令', `${base}/logs?view=exec`],
        ] as const).map(([i, t, d, to]) => (
          <Link key={t} to={to} className="rounded-2xl surface surface-hover px-6 py-5 flex items-center gap-4">
            <div className="h-11 w-11 rounded-lg bg-card2 ring-1 ring-line2 grid place-items-center text-fg">{i}</div>
            <div><div className="text-[13px] font-semibold">{t}</div><div className="text-[13px] text-muted mt-0.5">{d}</div></div>
          </Link>
        ))}
      </div>
      {hist && <DeployHistory s={s} deps={deps.data} onClose={() => setHist(false)} />}
      {bind && <BindDomain s={s} onClose={() => setBind(false)} />}
    </>
  )
}

function Uptime({ failed }: { failed: boolean }) {
  return (
    <div className="rounded-xl field px-4 py-3.5">
      <div className="flex items-center text-[12px] text-muted">最近 30 天可用性<span className="ml-auto text-fg font-semibold tnum">{failed ? '97.82%' : '99.98%'}</span></div>
      <div className="mt-2.5 flex gap-[3px]">{Array.from({ length: 30 }, (_, i) => {
        const bad = failed ? i > 26 : i === 11
        return <span key={i} className={cx('flex-1 h-6 rounded-[3px]', bad ? (failed && i > 27 ? 'bg-rose-500/80' : 'bg-amber-400/80') : 'bg-emerald-500/70')} />
      })}</div>
      <div className="mt-1.5 flex justify-between text-[10.5px] text-dim"><span>30 天前</span><span>今天</span></div>
    </div>
  )
}
function RecentDeploys({ deps }: { deps?: Deployment[] }) {
  const list = (deps ?? []).slice(0, 10).reverse()
  return (
    <div className="rounded-xl field px-4 py-3.5">
      <div className="flex items-center text-[12px] text-muted">最近部署<span className="ml-auto text-fg font-semibold tnum">{deps?.length ?? 0} 次</span></div>
      <div className="mt-2.5 flex gap-[3px]">{Array.from({ length: 10 }, (_, i) => {
        const d = list[i - (10 - list.length)]
        return <span key={i} title={d ? `${dateTime(d.started_at)} · ${deployStatusText[d.status] ?? d.status}` : ''} className={cx('flex-1 h-6 rounded-[3px]', !d ? 'bg-ov/[0.06]' : d.status === 'failed' ? 'bg-rose-500/80' : ['running', 'superseded'].includes(d.status) ? 'bg-emerald-500/70' : 'bg-amber-400/80')} />
      })}</div>
      <div className="mt-1.5 flex justify-between text-[10.5px] text-dim"><span>更早</span><span>最新</span></div>
    </div>
  )
}
function DomainBadge({ d }: { d: Domain }) {
  return d.status === 'active' ? <Badge tone="green"><ShieldCheck size={11} />证书有效</Badge> : d.status === 'error' ? <span title={d.error}><Badge tone="red">{d.error ? '异常' : '错误'}</Badge></span> : <Badge tone="amber">配置中</Badge>
}

function DeployHistory({ s, deps, onClose }: { s: Service; deps?: Deployment[]; onClose: () => void }) {
  const [target, setTarget] = useState<Deployment>()
  return (
    <Modal title={<><History size={17} />部署记录 · {s.name}</>} width={760} onClose={onClose}>
      {!deps ? <RowsSkeleton n={3} /> : !deps.length ? <div className="text-[13px] text-muted">还没有部署记录</div> : (
        <div className="divide-y divide-line -my-2">
          {deps.map((d, i) => (
            <div key={d.id} className="py-3 flex items-center gap-4 text-[13px]">
              <Dot status={d.status === 'running' ? 'running' : d.status === 'failed' ? 'failed' : d.status === 'superseded' ? 'stopped' : 'deploying'} />
              <div className="flex-1 min-w-0"><div className="font-semibold">{deployStatusText[d.status] ?? d.status} <span className="text-muted font-normal">· {triggerText[d.trigger] ?? d.trigger}</span></div>
                <div className="font-mono text-[12px] text-muted truncate mt-0.5">{d.image_digest ?? d.id}{d.message ? ` · ${d.message}` : ''}</div></div>
              <span className="text-muted tnum">{dateTime(d.started_at)}</span>
              {i > 0 && d.image_digest ? <Button size="sm" onClick={() => setTarget(d)}><Undo2 size={13} />回滚</Button> : <span className="w-[74px]" />}
            </div>
          ))}
        </div>
      )}
      {target && <ConfirmDialog title="回滚部署" action="确认回滚" danger={false} name={s.name} onClose={() => setTarget(undefined)}
        description={<>将用 <span className="font-mono">{target.image_digest?.slice(0, 19)}</span>（{dateTime(target.started_at)}）重新部署 {s.name}。</>}
        onConfirm={() => run(() => api.rollback(s.id, target.id), { ok: '正在回滚', invalidate: [`deployments:${s.id}`, `project:${s.project_id}`] })} />}
    </Modal>
  )
}

function BindDomain({ s, onClose }: { s: Service; onClose: () => void }) {
  const settings = useQuery('settings', api.settings).data
  const httpPorts = (s.ports ?? []).filter(p => p.protocol === 'http')
  const [host, setHost] = useState(''); const [port, setPort] = useState(httpPorts[0]?.name ?? ''); const [proxied, setProxied] = useState(true)
  const [busy, wrap] = useBusy()
  const hostname = host.includes('.') ? host : host && settings?.base_domain ? `${host}.${settings.base_domain}` : host
  const submit = () => wrap(async () => { const r = await run(() => api.addDomain(s.id, { hostname, port_name: port, proxied }), { ok: '域名已添加，正在配置 DNS 与证书', invalidate: [`project:${s.project_id}`, 'domains'] }); if (r) onClose() })
  return (
    <Modal title="绑定域名" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!hostname || !port || busy} onClick={submit}>绑定</Button></>}>
      <Label t="域名" d={settings?.base_domain ? `只填前缀则使用 *.${settings.base_domain}；完整域名需由同一 Cloudflare 账号托管` : '尚未设置主域名，请填写完整域名'}>
        <div className="flex items-center gap-2"><TextInput autoFocus mono className="flex-1" value={host} onChange={e => setHost(e.target.value.trim())} placeholder={settings?.base_domain ? 'app' : 'app.example.com'} />{settings?.base_domain && !host.includes('.') && <span className="font-mono text-[13px] text-muted">.{settings.base_domain}</span>}</div>
      </Label>
      <Label t="端口">{httpPorts.length ? <Select className="w-full" value={port} onChange={e => setPort(e.target.value)}>{httpPorts.map(p => <option key={p.name} value={p.name}>{p.name} · {p.container_port}</option>)}</Select> : <div className="text-[13px] text-muted">没有 HTTP 端口，请先在「网络」里添加。</div>}</Label>
      <div className="flex items-center justify-between"><div><div className="text-[13px] font-semibold">Cloudflare 代理</div><div className="text-[12px] text-dim mt-0.5">橙色云朵：隐藏源站 IP，80/443 只放行 Cloudflare</div></div><Toggle on={proxied} onChange={setProxied} /></div>
    </Modal>
  )
}

// ---------------- env ----------------
function EnvTab({ s }: { s: Service }) {
  const [reveal, setReveal] = useState(false)
  const q = useQuery(`env:${s.id}:${reveal ? 1 : 0}`, () => api.env(s.id, reveal))
  const refs = useQuery(`env-refs:${s.id}`, () => api.envRefs(s.id))
  const [edit, setEdit] = useState<{ i: number; v: EnvVar } | null>(null)
  const [raw, setRaw] = useState(false)
  const [del, setDel] = useState<EnvVar>()
  const save = async (vars: EnvVar[]) => {
    const r = await run(() => api.putEnv(s.id, vars), { ok: '已保存，重新部署后生效', invalidate: [`env:${s.id}`, `deployments:${s.id}`] })
    return !!r
  }
  const rows = q.data ?? []
  const byService = (refs.data ?? []).reduce<Record<string, EnvVar[]>>((a, v) => { (a[v.service ?? '同项目服务'] ??= []).push(v); return a }, {})
  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <div><div className="text-[15px] font-semibold">用户变量</div><div className="text-[13px] text-muted mt-0.5">修改后需重新部署生效。可用 {'${变量名}'} 引用同项目其他服务暴露的变量。</div></div>
        <div className="flex gap-2"><Button onClick={() => setReveal(v => !v)}>{reveal ? <EyeOff size={14} /> : <Eye size={14} />}{reveal ? '隐藏私有值' : '显示私有值'}</Button><Button onClick={() => setRaw(true)}><Pencil size={14} />编辑原始环境变量</Button><Button variant="primary" onClick={() => setEdit({ i: -1, v: { key: '', value: '', secret: false, exposed: false } })}><Plus size={15} />添加</Button></div>
      </div>
      {q.error && !q.data ? <ErrorState error={q.error} onRetry={q.refetch} /> : !q.data ? <RowsSkeleton n={5} /> : !rows.length ? <Empty icon={<KeyRound size={22} />} title="还没有环境变量" action={<Button variant="primary" onClick={() => setEdit({ i: -1, v: { key: '', value: '', secret: false, exposed: false } })}><Plus size={15} />添加</Button>} /> : (
        <Card className="divide-y divide-line">
          {rows.map((v, i) => (
            <div key={v.key} className="px-6 h-[52px] grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_170px] items-center gap-6 group hover:bg-ov/[0.02] transition-colors">
              <span className="font-mono text-[13px] font-medium truncate">{v.key}</span>
              <span className={cx('font-mono text-[13px] truncate', v.value.includes('${') ? 'text-accent-soft' : 'text-fg2')}>{v.secret && !v.value ? '••••••••••••••••' : v.value}</span>
              <span className="flex items-center justify-end gap-3 text-dim group-hover:text-muted transition-colors">
                {v.exposed && <span title="同项目其他服务可引用" className="flex items-center gap-1 text-[11.5px] rounded-full ring-1 ring-inset ring-line2 px-2 h-[22px]"><Share2 size={11} />暴露</span>}
                {v.secret && <span className="flex items-center gap-1 text-[11.5px] rounded-full ring-1 ring-inset ring-line2 px-2 h-[22px]"><Lock size={11} />私有</span>}
                <button title="编辑" onClick={() => setEdit({ i, v })} className="hover:text-fg"><Pencil size={15} /></button><button title="删除" onClick={() => setDel(v)} className="hover:text-[#ef4444]"><Trash2 size={15} /></button>
              </span>
            </div>
          ))}
        </Card>
      )}
      {Object.entries(byService).map(([svc, vars]) => (
        <div key={svc}>
          <div className="mt-8 text-[15px] font-semibold mb-1">可引用的变量</div>
          <div className="text-[13px] text-muted mb-4">来自同项目服务 {svc}</div>
          <Card className="divide-y divide-line">
            {vars.map(v => (
              <div key={v.key} className="px-6 h-12 grid grid-cols-[1fr_1.4fr_auto] items-center gap-6 text-[13px]">
                <span className="font-mono text-accent-soft">{v.key}</span>
                <span className="font-mono text-dim truncate">{v.secret || !v.value ? '••••••••' : v.value}</span>
                <button title="复制引用" onClick={() => navigator.clipboard?.writeText(`\${${v.key}}`)}><Copy size={14} className="text-muted hover:text-fg" /></button>
              </div>
            ))}
          </Card>
        </div>
      ))}
      {edit && <EnvEditor init={edit.v} isNew={edit.i < 0} onClose={() => setEdit(null)} onSave={async v => {
        const next = rows.slice(); if (edit.i < 0) { if (next.some(x => x.key === v.key)) { return false } next.push(v) } else next[edit.i] = v
        return save(next)
      }} />}
      {raw && <RawEnv rows={rows} onClose={() => setRaw(false)} onSave={save} />}
      {del && <ConfirmDialog title="删除环境变量" name={del.key} onClose={() => setDel(undefined)} description={<>删除 <span className="font-mono">{del.key}</span>，重新部署后生效。</>} onConfirm={() => save(rows.filter(x => x.key !== del.key))} />}
    </>
  )
}
function EnvEditor({ init, isNew, onClose, onSave }: { init: EnvVar; isNew: boolean; onClose: () => void; onSave: (v: EnvVar) => Promise<boolean> }) {
  const [v, setV] = useState(init); const [busy, wrap] = useBusy()
  const ok = /^[A-Za-z_][A-Za-z0-9_]*$/.test(v.key)
  return (
    <Modal title={isNew ? '添加环境变量' : '编辑环境变量'} onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!ok || busy} onClick={() => wrap(async () => { if (await onSave(v)) onClose() })}>保存</Button></>}>
      <Label t="变量名"><TextInput autoFocus={isNew} mono className="w-full" value={v.key} onChange={e => setV({ ...v, key: e.target.value.trim() })} placeholder="DATABASE_URL" /></Label>
      <Label t="值" d={v.secret && !isNew ? '私有变量：留空保持原值不变' : '可用 ${OTHER_KEY} 引用其他变量'}><TextInput autoFocus={!isNew} mono className="w-full" value={v.value} onChange={e => setV({ ...v, value: e.target.value })} /></Label>
      <div className="flex items-center justify-between mb-3"><div className="text-[13px] font-semibold">私有<span className="text-dim font-normal ml-2 text-[12px]">面板与 API 默认隐藏值</span></div><Toggle on={v.secret} onChange={x => setV({ ...v, secret: x })} /></div>
      <div className="flex items-center justify-between"><div className="text-[13px] font-semibold">暴露给同项目服务<span className="text-dim font-normal ml-2 text-[12px]">其他服务可用 {'${' + (v.key || 'KEY') + '}'} 引用</span></div><Toggle on={v.exposed} onChange={x => setV({ ...v, exposed: x })} /></div>
    </Modal>
  )
}
function RawEnv({ rows, onClose, onSave }: { rows: EnvVar[]; onClose: () => void; onSave: (v: EnvVar[]) => Promise<boolean> }) {
  const [text, setText] = useState(rows.map(r => `${r.key}=${r.value}`).join('\n'))
  const [busy, wrap] = useBusy()
  const parse = (): EnvVar[] => text.split('\n').map(l => l.trim()).filter(l => l && !l.startsWith('#') && l.includes('=')).map(l => {
    const i = l.indexOf('='), key = l.slice(0, i).trim(), value = l.slice(i + 1).replace(/^"(.*)"$/, '$1')
    const old = rows.find(r => r.key === key)
    return { key, value, secret: old?.secret ?? false, exposed: old?.exposed ?? false }
  })
  return (
    <Modal title="编辑原始环境变量" width={720} onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={busy} onClick={() => wrap(async () => { if (await onSave(parse())) onClose() })}>保存</Button></>}>
      <div className="text-[12.5px] text-muted mb-3">每行一个 KEY=VALUE。私有变量显示为空值，保持为空即不修改。</div>
      <textarea value={text} onChange={e => setText(e.target.value)} spellCheck={false} className="w-full h-[320px] rounded-lg field p-3.5 font-mono text-[13px] leading-6 outline-none text-fg resize-none" />
    </Modal>
  )
}

function BackupTab() {
  if (!isDemo()) return <Empty icon={<ArchiveRestore size={22} />} title="备份功能即将推出" desc="计划：restic 增量备份到 Cloudflare R2，每天自动执行。现在可以在「硬盘」里查看卷，或用 atoll CLI 导出数据。" />
  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <div><div className="text-[15px] font-semibold">备份</div><div className="text-[13px] text-muted mt-0.5">restic 增量备份到 Cloudflare R2，每天 04:00 自动执行，保留 14 份。</div></div>
        <div className="flex gap-2"><Button>备份策略</Button><Button variant="primary"><Plus size={15} />立即备份</Button></div>
      </div>
      <Card className="divide-y divide-line">
        {['2026-10-29 04:00', '2026-10-28 04:00', '2026-10-27 04:00', '2026-10-26 04:00', '2026-10-25 04:00'].map((t, i) => (
          <div key={t} className="px-6 h-14 flex items-center gap-4 text-[13px]">
            <ArchiveRestore size={16} className="text-muted" /><span className="font-semibold">{t}</span><span className="text-muted">{['1.42 GB', '1.41 GB', '1.40 GB', '1.38 GB', '1.37 GB'][i]} · 增量 {[12, 9, 31, 7, 18][i]} MB</span>
            <span className="ml-auto flex gap-2"><Button size="sm"><Download size={14} />下载</Button><Button size="sm">还原</Button></span>
          </div>
        ))}
      </Card>
    </>
  )
}

function UsageTab({ s }: { s: Service }) {
  const [range, setRange] = useState<Range>('24h')
  const q = useQuery(`svc-metrics:${s.id}:${range}`, () => api.serviceMetrics(s.id, range), { interval: 60000 })
  const d = q.data ?? []
  const labels = timeLabels(d.map(x => x.at))
  const tx = d.reduce((a, x, i) => a + (i ? x.net_tx_bps * ((new Date(x.at).getTime() - new Date(d[i - 1].at).getTime()) / 1000) : 0), 0)
  return (
    <>
      <div className="flex items-center justify-between mb-4"><div className="text-[15px] font-semibold">用量</div><RangePicker v={range} set={setRange} /></div>
      {q.error && !q.data && <div className="mb-4"><ErrorState error={q.error} onRetry={q.refetch} /></div>}
      <div className="grid grid-cols-2 gap-5">
        <Chart title="CPU" icon={<Cpu size={15} />} data={d.map(x => x.cpu_pct)} color="#8b5cf6" labels={labels} />
        <Chart title="内存" icon={<MemoryStick size={15} />} data={d.map(x => x.mem_mb)} color="#38bdf8" unit=" MB" labels={labels} />
      </div>
      <div className="mt-5"><Chart title="网络用量" icon={<ArrowDownUp size={15} />} data={d.map(x => (x.net_rx_bps + x.net_tx_bps) / 1048576)} color="#34d399" unit=" MB/s" h={150} labels={labels} /></div>
      <div className="text-[12.5px] text-dim mt-3">{s.name} 所选时段出站约 {(tx / 1073741824).toFixed(1)} GB{d.length ? ` · 当前 ${bytesRate(d[d.length - 1].net_tx_bps)} 出站` : ''}</div>
    </>
  )
}

function NetworkTab({ s, srv }: { s: Service; srv?: Server }) {
  const port = primaryPort(s)
  const [bind, setBind] = useState(false)
  const [del, setDel] = useState<Domain>()
  const [addPort, setAddPort] = useState(false)
  return (
    <>
      <div className="text-[15px] font-semibold mb-1">内网访问</div>
      <div className="text-[13px] text-muted mb-4">同项目的服务通过 Docker 内网直接互通，不经过公网。</div>
      <Card className="px-6 py-5 grid grid-cols-2 gap-6">
        <div><div className="text-[13px] text-muted mb-2">主机名</div><div className="h-11 rounded-lg field px-3.5 flex items-center justify-between font-mono text-[13.5px]">{s.name}<button onClick={() => navigator.clipboard?.writeText(s.name)}><Copy size={14} className="text-muted hover:text-fg" /></button></div></div>
        <div><div className="text-[13px] text-muted mb-2 flex items-center">端口<button onClick={() => setAddPort(true)} className="ml-auto text-accent text-[12.5px] flex items-center gap-1"><Plus size={13} />添加</button></div>
          {(s.ports ?? []).length ? <div className="space-y-2">{s.ports.map(p => <div key={p.name} className="h-11 rounded-lg field px-3.5 flex items-center justify-between font-mono text-[13.5px]">{p.container_port}<span className="text-[12px] text-dim">{p.name} · {p.protocol.toUpperCase()}{p.public_port ? ` · 公网 ${p.public_port}` : ''}</span></div>)}</div>
            : <div className="h-11 rounded-lg field px-3.5 flex items-center text-[13px] text-dim">未暴露端口</div>}
        </div>
      </Card>

      <div className="mt-8 flex items-end justify-between mb-4">
        <div><div className="text-[15px] font-semibold">公网访问</div><div className="text-[13px] text-muted mt-0.5">绑定后自动在 Cloudflare 建代理记录，直接指向服务器，不经过中控。</div></div>
        <div className="flex gap-2"><Button disabled={!s.ports?.some(p => p.protocol === 'http')} onClick={() => setBind(true)}><Plus size={15} />绑定域名</Button></div>
      </div>
      <Card className="p-6">
        <div className="flex items-center justify-center gap-0 py-4">
          {([['用户', <Globe key="g" size={18} />], ['Cloudflare', <Cloud key="c" size={18} className="text-[#f6821f]" />], [`Caddy · ${srv?.name.split(' ')[0] ?? '…'}`, <ShieldCheck key="s" size={18} className="text-emerald-400" />], [`${s.name}:${port?.container_port ?? '—'}`, <Box key="b" size={18} className="text-accent" />]] as const).map(([l, i], idx, a) => (
            <div key={idx} className="flex items-center">
              <div className="rounded-xl border border-line2 bg-card2 px-4 h-12 flex items-center gap-2.5 text-[13px] font-semibold">{i}{l}</div>
              {idx < a.length - 1 && <div className="w-16 border-t-2 border-dashed border-[#7c3aed]/60 relative"><span className="absolute -right-1 -top-[5px] text-[#7c3aed] text-[10px]">▶</span></div>}
            </div>
          ))}
        </div>
        {(s.domains ?? []).map(d => (
          <div key={d.id} className="mt-4 rounded-lg border border-line bg-bg/50 px-5 h-14 flex items-center gap-3">
            <Dot status={d.status} /><span className="font-semibold text-[15px]">{d.hostname}</span><DomainBadge d={d} />
            <span className="text-[13px] text-muted ml-3">A {srv?.public_ipv4 ?? '—'} · {d.proxied ? '已代理 · Full (strict)' : '仅 DNS'} · 端口 {d.port_name}</span>
            <span className="ml-auto flex gap-3 text-muted"><a href={`https://${d.hostname}`} target="_blank" rel="noreferrer" className="hover:text-fg"><ExternalLink size={15} /></a><button onClick={() => setDel(d)} className="hover:text-[#ef4444]"><Trash2 size={15} /></button></span>
          </div>
        ))}
      </Card>
      {bind && <BindDomain s={s} onClose={() => setBind(false)} />}
      {addPort && <AddPort s={s} onClose={() => setAddPort(false)} />}
      {del && <ConfirmDialog title="解绑域名" name={del.hostname} onClose={() => setDel(undefined)} description={<>将删除 Cloudflare 上 <b>{del.hostname}</b> 的记录，并从 Caddy 移除路由。</>}
        onConfirm={() => run(() => api.deleteDomain(del.id), { ok: '域名已解绑', invalidate: [`project:${s.project_id}`, 'domains'] })} />}
    </>
  )
}
function AddPort({ s, onClose }: { s: Service; onClose: () => void }) {
  const [name, setName] = useState(s.ports?.length ? '' : 'web'); const [port, setPort] = useState(''); const [proto, setProto] = useState('http'); const [pub, setPub] = useState('')
  const submit = () => run(() => api.updateService(s.id, { ports: [...(s.ports ?? []), { name, container_port: +port, protocol: proto, public_port: pub ? +pub : undefined }] }), { ok: '已添加端口，正在重新部署', invalidate: [`project:${s.project_id}`] }).then(r => r && onClose())
  return (
    <Modal title="添加端口" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!name || !port} onClick={submit}>添加</Button></>}>
      <div className="grid grid-cols-2 gap-4">
        <Label t="名称" d="用于 ${NAME_PORT} 变量"><TextInput mono className="w-full" value={name} onChange={e => setName(e.target.value.replace(/[^a-z0-9-]/gi, '').toLowerCase())} /></Label>
        <Label t="容器端口"><TextInput mono className="w-full" value={port} onChange={e => setPort(e.target.value.replace(/\D/g, ''))} /></Label>
        <Label t="协议"><Select className="w-full" value={proto} onChange={e => setProto(e.target.value)}><option value="http">HTTP</option><option value="tcp">TCP</option><option value="udp">UDP</option></Select></Label>
        {proto !== 'http' && <Label t="公网端口" d="留空则仅内网"><TextInput mono className="w-full" value={pub} onChange={e => setPub(e.target.value.replace(/\D/g, ''))} /></Label>}
      </div>
    </Modal>
  )
}

function DiskTab({ s }: { s: Service }) {
  const [add, setAdd] = useState(false); const [del, setDel] = useState<string>()
  const vols = s.volumes ?? []
  const p = useParams()
  return (
    <>
      <div className="flex items-center justify-between mb-4"><div><div className="text-[15px] font-semibold">硬盘</div><div className="text-[13px] text-muted mt-0.5">卷保存在服务器本地 /var/lib/atoll/volumes，迁移时随服务一起搬走。</div></div><Button variant="primary" onClick={() => setAdd(true)}><Plus size={15} />挂载卷</Button></div>
      {!vols.length ? <Empty icon={<HardDrive size={22} />} title="没有挂载卷" desc="不挂载卷时，容器重新部署后文件会丢失。" /> : (
        <Card className="divide-y divide-line">
          {vols.map((v, i) => (
            <div key={v.name} className="px-6 h-16 flex items-center gap-4">
              <HardDrive size={18} className="text-accent" />
              <div className="flex-1"><div className="font-mono text-[13px]">{v.mount_path}</div><div className="text-[12.5px] text-muted mt-0.5 font-mono">/var/lib/atoll/volumes/{s.id}/{v.name}</div></div>
              {isDemo() && <div className="w-56"><div className="flex justify-between text-[12.5px] mb-1.5"><span className="text-muted">已用</span><span>{i ? '86 MB' : '1.4 GB'}</span></div><div className="h-2.5 rounded-[4px] field"><div className="h-full bg-[#8b5cf6]" style={{ width: i ? '6%' : '28%' }} /></div></div>}
              <span className="flex gap-3 text-muted ml-4"><Link to={`/projects/${p.pid}/services/${s.id}/files?path=${encodeURIComponent(v.mount_path)}`} className="hover:text-fg"><FolderOpen size={15} /></Link><button onClick={() => setDel(v.name)} className="hover:text-[#ef4444]"><Trash2 size={15} /></button></span>
            </div>
          ))}
        </Card>
      )}
      {add && <AddVolume s={s} onClose={() => setAdd(false)} />}
      {del && <ConfirmDialog title="卸载卷" name={del} onClose={() => setDel(undefined)} description={<>卸载卷 <span className="font-mono">{del}</span> 并重新部署。服务器上的数据目录不会立即删除。</>}
        onConfirm={() => run(() => api.updateService(s.id, { volumes: vols.filter(v => v.name !== del) }), { ok: '已卸载，正在重新部署', invalidate: [`project:${s.project_id}`] })} />}
    </>
  )
}
function AddVolume({ s, onClose }: { s: Service; onClose: () => void }) {
  const [name, setName] = useState('data'); const [path, setPath] = useState('/data')
  return (
    <Modal title="挂载卷" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!name || !path.startsWith('/') || s.volumes?.some(v => v.name === name)}
      onClick={() => run(() => api.updateService(s.id, { volumes: [...(s.volumes ?? []), { name, mount_path: path }] }), { ok: '已挂载，正在重新部署', invalidate: [`project:${s.project_id}`] }).then(r => r && onClose())}>挂载</Button></>}>
      <Label t="卷名" d="服务内唯一"><TextInput mono className="w-full" value={name} onChange={e => setName(e.target.value.replace(/[^a-z0-9-_]/gi, ''))} /></Label>
      <Label t="容器内路径"><TextInput mono className="w-full" value={path} onChange={e => setPath(e.target.value)} /></Label>
    </Modal>
  )
}

export const Row = ({ t, d, children }: { t: string; d?: string; children: React.ReactNode }) => <div className="px-6 py-5 flex items-center gap-6"><div className="flex-1"><div className="text-[13px] font-semibold">{t}</div>{d && <div className="text-[13px] text-muted mt-1">{d}</div>}</div>{children}</div>
export const Sec = ({ t, children }: { t: string; children: React.ReactNode }) => <div className="mb-8"><div className="text-[13px] font-semibold mb-3">{t}</div><Card className="divide-y divide-line">{children}</Card></div>

function SettingsTab({ p, s, onDeleted }: { p: Project; s: Service; onDeleted: () => void }) {
  const [f, setF] = useState({ name: s.name, image: s.image, command: (s.command ?? []).join(' '), health: s.health?.http_path ?? '', cpu: s.cpu_limit ? String(s.cpu_limit) : '', mem: s.mem_limit_mb ? String(s.mem_limit_mb) : '' })
  const [del, setDel] = useState(false)
  const [busy, wrap] = useBusy()
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value })
  const dirty = f.name !== s.name || f.image !== s.image || f.command !== (s.command ?? []).join(' ') || f.health !== (s.health?.http_path ?? '') || f.cpu !== (s.cpu_limit ? String(s.cpu_limit) : '') || f.mem !== (s.mem_limit_mb ? String(s.mem_limit_mb) : '')
  const save = () => wrap(() => run(() => api.updateService(s.id, {
    name: f.name, image: f.image, command: f.command.trim() ? f.command.trim().split(/\s+/) : [],
    health: f.health ? { ...s.health, http_path: f.health } : undefined, cpu_limit: +f.cpu || 0, mem_limit_mb: +f.mem || 0,
  }), { ok: '已保存，正在重新部署', invalidate: [`project:${p.id}`, 'projects'] }))
  return (
    <>
      <Sec t="基本">
        <Row t="服务名称" d="也是项目内网的主机名"><TextInput className="w-[420px]" value={f.name} onChange={set('name')} /></Row>
        <Row t="镜像" d="Docker Hub、GHCR 或私有仓库"><TextInput mono className="w-[420px]" value={f.image} onChange={set('image')} /></Row>
      </Sec>
      <Sec t="启动">
        <Row t="启动命令" d="覆盖镜像的 CMD"><TextInput mono className="w-[420px]" value={f.command} onChange={set('command')} placeholder="使用镜像默认值" /></Row>
        <Row t="健康检查" d="HTTP 路径，失败 3 次后自动重启"><TextInput mono className="w-[420px]" value={f.health} onChange={set('health')} placeholder="例如 /health" /></Row>
      </Sec>
      <Sec t="资源限制">
        <Row t="CPU" d="核，留空不限"><TextInput className="w-[420px]" value={f.cpu} onChange={set('cpu')} placeholder="不限" /></Row>
        <Row t="内存" d="MB，超出后容器会被 OOM 重启"><TextInput className="w-[420px]" value={f.mem} onChange={set('mem')} placeholder="不限" /></Row>
      </Sec>
      {dirty && <div className="sticky bottom-4 z-10 mb-8 surface px-5 py-3 flex items-center gap-3 text-[13px]"><span className="flex-1 font-semibold">有未保存的修改，保存后会重新部署</span><Button size="sm" onClick={() => setF({ name: s.name, image: s.image, command: (s.command ?? []).join(' '), health: s.health?.http_path ?? '', cpu: s.cpu_limit ? String(s.cpu_limit) : '', mem: s.mem_limit_mb ? String(s.mem_limit_mb) : '' })}><X size={13} />放弃</Button><Button size="sm" variant="primary" disabled={busy} onClick={save}>{busy ? <Loader2 size={13} className="animate-spin" /> : <Check size={13} />}保存并部署</Button></div>}
      <Sec t="危险区域">
        <Row t="迁移到其他服务器" d="同一项目的服务在同一台服务器上，迁移以项目为单位"><Link to={`/projects/${p.id}/settings`}><Button variant="danger-outline" size="sm">迁移项目</Button></Link></Row>
        <Row t="删除服务" d="容器、卷与域名记录都会被删除"><Button variant="danger" size="sm" onClick={() => setDel(true)}>删除服务</Button></Row>
      </Sec>
      {del && <ConfirmDialog title="删除服务" name={s.name} onClose={() => setDel(false)}
        description={<>将删除 <b>{p.name} / {s.name}</b> 的容器、{s.volumes?.length ?? 0} 个卷{s.domains?.length ? ` 和 ${s.domains.length} 条域名记录` : ''}。此操作不可撤销。</>}
        onConfirm={async () => { const r = await run(() => api.deleteService(s.id), { ok: '服务已删除', invalidate: [`project:${p.id}`, 'projects', 'domains'] }); if (r !== undefined) onDeleted() }} />}
    </>
  )
}

