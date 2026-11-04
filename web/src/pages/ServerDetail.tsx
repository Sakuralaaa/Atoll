import { useState } from 'react'
import { useMask } from '../api/privacy'
import { IP } from '../components/IP'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ArrowLeft, Info, Code2, SquareTerminal, BarChart3, Cpu, Settings, Bell, MemoryStick, HardDrive, Minus, Plus, Server as ServerIcon, ChevronDown } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, Chart, CopyId, Dot, Mini, ServiceIcon, Tabs, Ticks } from '../components/ui'
import { ConfirmDialog, Empty, ErrorState, Modal, RowsSkeleton, CardSkeleton, Select, Skeleton, TextInput, CodeBlock } from '../components/feedback'
import { api, isDemo, type Range } from '../api/client'
import { run, useQuery } from '../api/hooks'
import { ago, day, gb, projIcon, timeLabels, usagePct, projName } from '../api/view'
import type { Project, Server } from '../api/types'
import { NetBadge, ServerNetworkAction } from '../components/ServerNetwork'
import { Latency } from '../components/Latency'
import { useT } from '../i18n'
import { AgentUpgradeAction, AgentUpgradeBadge } from '../components/AgentUpgrade'
import { statusText } from '../components/ui-utils'
import { useProjects } from '../api/queries'
import { providerOf, countryOf, regionOf } from '../api/providers'
import { Flag } from '../components/Flag'
import { useNetState } from '../components/net'

const useServer = (id: string) => useQuery(`server:${id}`, () => api.server(id), { interval: 30000 })
const projectsOn = (ps: Project[] | undefined, sid: string) => (ps ?? []).filter(p => p.server_id === sid)

const TABS = [
  { k: 'info', l: '服务器信息', icon: <Info size={16} /> },
  { k: 'projects', l: '项目', icon: <Code2 size={16} /> },
  { k: 'ssh', l: 'SSH', icon: <SquareTerminal size={16} /> },
  { k: 'metrics', l: '监控指标', icon: <BarChart3 size={16} /> },
  { k: 'status', l: '状态', icon: <Cpu size={16} /> },
  { k: 'settings', l: '设置', icon: <Settings size={16} /> },
]

export default function ServerDetail() {
  const { id = '' } = useParams()
  const [sp, setSp] = useSearchParams()
  const q = useServer(id)
  const s = q.data
  const tab = sp.get('tab') ?? 'info'
  return (
    <MainShell>
      <div className="max-w-[1480px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @6xl:px-20 @5xl:pb-16">
        <Link to="/servers" className="inline-flex items-center gap-1.5 text-[13.5px] text-fg2 hover:text-fg"><ArrowLeft size={15} />返回</Link>
        {s ? <h1 className="text-[22px] @2xl:text-[26px] font-extrabold italic tracking-[-0.03em] mt-2 [overflow-wrap:anywhere]">{s.name}</h1> : <Skeleton className="h-8 w-80 mt-3" />}
        <div className="mt-1"><CopyId v={id} /></div>
        <Tabs className="mt-6" items={TABS} value={tab} onChange={k => setSp({ tab: k })} />
        <div className="mt-6">
          {q.error && !s && <ErrorState error={q.error} onRetry={q.refetch} />}
          {!s && !q.error && <CardSkeleton n={3} h={240} />}
          {s && tab === 'info' && <InfoTab s={s} />}
          {s && tab === 'projects' && <ProjectsTab s={s} />}
          {s && tab === 'metrics' && <MetricsTab s={s} />}
          {s && tab === 'settings' && <SettingsTab s={s} />}
          {s && tab === 'ssh' && <SshTab s={s} />}
          {s && tab === 'status' && <StatusTab s={s} />}
        </div>
      </div>
    </MainShell>
  )
}

function KV({ k, v }: { k: string; v: React.ReactNode }) {
  return <div className="flex items-start justify-between gap-4 min-h-10 py-2.5 text-[13px] leading-5"><span className="text-muted shrink-0 whitespace-nowrap">{k}</span><span className="text-fg min-w-0 text-right [overflow-wrap:anywhere]">{v}</span></div>
}

function InfoTab({ s }: { s: Server }) {
  const { t } = useT()
  const u = usagePct(s), on = s.status === 'online'
  return (
    <>
      <div className="grid grid-cols-1 @3xl:grid-cols-2 @6xl:grid-cols-3 gap-5 [&>*]:min-w-0">
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-3">运行信息</div>
          <KV k="状态" v={<span className="flex items-center gap-2"><Dot status={s.status} />{on ? 'Agent 已连接' : s.status === 'pending' ? '等待 Agent 连接' : `失联 · ${ago(s.last_seen_at)}`}</span>} />
          <KV k={t('延迟', 'Latency')} v={<Latency s={s} label={false} avg />} />
          <KV k="Agent" v={<span className="flex flex-wrap items-center justify-end gap-2">{s.agent_version || '—'}<AgentUpgradeBadge s={s} /></span>} />
          <KV k={t('管理通道', 'Agent channel')} v={<span className="flex flex-col items-end gap-1.5"><span className="whitespace-nowrap"><NetBadge s={s} /></span>{s.tailnet_host && <span className="font-mono text-[12.5px] max-w-full [overflow-wrap:anywhere]" title={s.tailnet_host}>{s.tailnet_host}</span>}</span>} />
          <KV k={t('内网 IP', 'Tailnet IP')} v={<IP className="font-mono text-[13px]" v={s.tailnet_ip} />} />
          <KV k="最近心跳" v={ago(s.last_seen_at)} />
          <KV k="区域" v={<span className="flex items-center gap-2"><Flag code={countryOf(s)} />{regionOf(s) || '—'}</span>} />
          <KV k="IPv4" v={<IP className="font-mono text-[13px]" v={s.public_ipv4} />} />
        </Card>
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-3">规格信息</div>
          <KV k="服务商" v={<span className="flex items-center gap-2">{providerOf(s) !== '?' && <Mini provider={providerOf(s)} />}{s.provider || (providerOf(s) !== '?' ? `${providerOf(s)}（按 IP 推测）` : '—')}</span>} />
          <KV k="CPU" v={`${s.cpu_cores} vCPU · ${s.arch}`} />
          <KV k="内存" v={gb(s.mem_mb)} />
          <KV k="硬盘" v={`${s.disk_gb} GB`} />
          <KV k="系统" v={s.os || '—'} />
          <KV k="加入时间" v={day(s.created_at)} />
        </Card>
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-5">资源使用</div>
          {[['CPU 使用率', u.cpu, '#60a5fa'], ['内存使用率', u.mem, '#a78bfa'], ['硬盘使用率', u.disk, '#34d399']].map(([l, v, c]) => (
            <div key={l as string} className="mb-5">
              <div className="flex justify-between text-[13px] mb-2"><span className="text-muted">{l}</span><span className="font-semibold">{v}%</span></div>
              <Ticks value={v as number} color={c as string} n={64} />
            </div>
          ))}
          <div className="text-[12.5px] text-dim">{s.usage ? `负载 ${s.usage.load1.toFixed(2)} · 采样于 ${ago(s.usage.at)}` : '暂无监控数据'}</div>
        </Card>
      </div>
      <div className="mt-8 text-[13px] font-semibold mb-3">项目</div>
      <ProjectsTab s={s} />
    </>
  )
}

function ProjectsTab({ s }: { s: Server }) {
  const q = useProjects()
  if (!q.data) return q.error ? <ErrorState error={q.error} onRetry={q.refetch} /> : <CardSkeleton n={3} h={140} />
  const ps = projectsOn(q.data, s.id)
  if (!ps.length) return <Empty icon={<Code2 size={22} />} title="这台服务器上还没有项目" desc={s.role === 'control' ? '中控节点只运行面板与调度。' : '创建项目时选择这台服务器，或把其他服务器上的项目迁移过来。'} />
  return (
    <div className="grid grid-cols-1 @2xl:grid-cols-2 @6xl:grid-cols-3 gap-5 [&>*]:min-w-0">
      {ps.map(p => {
        const ss = p.services ?? []
        const run = ss.filter(x => x.status === 'running').length
        return (
          <Link key={p.id} to={`/projects/${p.id}`} className="rounded-2xl surface surface-hover p-5">
            <div className="flex items-center gap-3 min-w-0"><ServiceIcon s={projIcon(p)} size={40} /><span className="text-[15px] font-semibold truncate min-w-0" title={p.name}>{projName(p)}</span></div>
            <div className="mt-8 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-3.5 text-[13px]">
              <span className="text-muted">服务器</span><span className="flex items-center justify-end gap-2 min-w-0">{providerOf(s) !== '?' && <Mini provider={providerOf(s)} />}<span className="truncate" title={s.name}>{s.name}</span></span>
              <span className="text-muted">服务</span><span className="flex items-center justify-end gap-2"><Dot status={!ss.length ? 'unknown' : run === ss.length ? 'running' : 'deploying'} />{run}/{ss.length} 运行中</span>
            </div>
          </Link>
        )
      })}
    </div>
  )
}

const RANGES: { k: Range; l: string }[] = [{ k: '1h', l: '最近 1 小时' }, { k: '24h', l: '最近 24 小时' }, { k: '7d', l: '最近 7 天' }]
export function RangePicker({ v, set }: { v: Range; set: (r: Range) => void }) {
  return (
    <div className="relative">
      <select value={v} onChange={e => set(e.target.value as Range)} className="appearance-none h-9 pl-3 pr-8 rounded-lg border border-line bg-card text-[13.5px] text-fg outline-none">{RANGES.map(r => <option key={r.k} value={r.k}>{r.l}</option>)}</select>
      <ChevronDown size={14} className="absolute right-2.5 top-2.5 pointer-events-none" />
    </div>
  )
}
function MetricsTab({ s }: { s: Server }) {
  const [range, setRange] = useState<Range>('24h')
  const m = useQuery(`server-metrics:${s.id}:${range}`, () => api.serverMetrics(s.id, range), { interval: 60000 })
  const ps = projectsOn(useProjects().data, s.id)
  const colors = ['#a855f7', '#eab308', '#22c55e', '#ef4444', '#3b82f6', '#f97316']
  const svcIds = ps.flatMap(p => (p.services ?? []).map(x => x.id))
  const per = useQuery(svcIds.length ? `svc-metrics-agg:${s.id}:${range}:${svcIds.join(',')}` : null, () => Promise.all(svcIds.map(id => api.serviceMetrics(id, range).catch(() => []))))
  const d = m.data ?? []
  const labels = timeLabels(d.map(x => x.at))
  const pct = (a: number, b: number) => (b ? (a / b) * 100 : 0)
  // sum container series by index (agents sample on the same 15s tick)
  const agg = (pick: (x: { cpu_pct: number; mem_mb: number }) => number) => {
    const arrs = per.data ?? []; const n = Math.max(0, ...arrs.map(a => a.length))
    return Array.from({ length: n }, (_, i) => arrs.reduce((t, a) => t + (a[i] ? pick(a[i]) : 0), 0))
  }
  const aggAts = (per.data ?? []).find(a => a.length)?.map(x => x.at) ?? []
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-5"><h2 className="text-[20px] font-semibold tracking-tight">监控指标</h2><div className="flex flex-wrap gap-2"><RangePicker v={range} set={setRange} />{isDemo() && <Button variant="primary"><Bell size={15} />设置告警</Button>}</div></div>
      {m.error && !m.data && <ErrorState error={m.error} onRetry={m.refetch} />}
      <div className="grid grid-cols-1 @3xl:grid-cols-2 @6xl:grid-cols-3 gap-5 [&>*]:min-w-0">
        <Chart title="CPU 使用率" icon={<Cpu size={15} />} data={d.map(x => x.cpu_pct)} color="#8b5cf6" max={100} labels={labels} />
        <Chart title="内存使用率" icon={<MemoryStick size={15} />} data={d.map(x => pct(x.mem_used_mb, x.mem_total_mb))} color="#38bdf8" max={100} labels={labels} />
        <Chart title="硬盘空间使用率" icon={<HardDrive size={15} />} data={d.map(x => pct(x.disk_used_gb, x.disk_total_gb))} color="#34d399" max={100} labels={labels} />
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 mt-9 mb-3"><h3 className="text-[15px] font-semibold">项目分布</h3><span className="text-[13px] text-muted">{RANGES.find(r => r.k === range)?.l}</span></div>
      <div className="flex flex-wrap gap-x-5 gap-y-2 mb-4 text-[13px]">{ps.map((p, i) => <span key={p.id} className="flex items-center gap-2 min-w-0 max-w-full"><span className="h-3 w-3 rounded-full shrink-0" style={{ background: colors[i % 6] }} />{projName(p)}</span>)}</div>
      <div className="grid grid-cols-1 @4xl:grid-cols-2 gap-5 [&>*]:min-w-0">
        <Chart title="CPU 使用率（容器合计）" icon={<Cpu size={15} />} data={agg(x => x.cpu_pct)} color="#a855f7" labels={timeLabels(aggAts)} />
        <Chart title="内存（容器合计）" icon={<MemoryStick size={15} />} data={agg(x => x.mem_mb)} color="#eab308" unit=" MB" labels={timeLabels(aggAts)} />
      </div>
    </>
  )
}

function Section({ title, children }: { title: React.ReactNode; children: React.ReactNode }) {
  return <div className="mb-8"><div className="text-[13px] font-semibold mb-3 flex items-center gap-2">{title}</div><Card className="divide-y divide-line">{children}</Card></div>
}
function Row({ t, d, children }: { t: React.ReactNode; d?: string; children?: React.ReactNode }) {
  return <div className="px-4 @2xl:px-6 py-5 flex flex-wrap items-center gap-x-6 gap-y-3"><div className="flex-1 min-w-[min(100%,220px)]"><div className="text-[13px] font-semibold flex flex-wrap items-center gap-2">{t}</div>{d && <div className="text-[13.5px] text-muted mt-1 [overflow-wrap:anywhere]">{d}</div>}</div>{children}</div>
}

function Stepper({ l, v, max }: { l: string; v: number; max: string }) {
  return (
    <div className="flex-1">
      <div className="flex justify-between text-[13px] mb-2"><span className="font-semibold">{l}</span><span className="text-muted text-[12.5px]">{v} / {max}</span></div>
      <div className="h-11 rounded-lg field flex items-center px-3"><Minus size={15} /><span className="flex-1 text-center font-semibold">{v}</span><Plus size={15} /></div>
    </div>
  )
}

function SettingsTab({ s }: { s: Server }) {
  const { t } = useT()
  const nav = useNavigate()
  const [name, setName] = useState(s.name); const [provider, setProvider] = useState(s.provider ?? ''); const [region, setRegion] = useState(s.region ?? '')
  const [del, setDel] = useState(false); const [evac, setEvac] = useState(false)
  const dirty = name !== s.name || provider !== (s.provider ?? '') || region !== (s.region ?? '')
  const save = () => run(() => api.updateServer(s.id, { name, provider, region }), { ok: '已保存', invalidate: ['servers', `server:${s.id}`] })
  const ps = projectsOn(useProjects().data, s.id)
  return (
    <>
      <Section title="基本">
        <Row t="服务器名称"><TextInput className="w-full @4xl:w-[480px]" value={name} onChange={e => setName(e.target.value)} /></Row>
        <Row t="服务商" d="仅用于显示图标，例如 Hetzner、Oracle、RackNerd、VMISS；留空则按 IP 归属自动推测"><TextInput className="w-full @4xl:w-[480px]" value={provider} onChange={e => setProvider(e.target.value)} /></Row>
        <Row t="区域" d="以国家代码结尾会显示旗帜，例如 Phoenix, US；留空则按 IP 自动显示"><TextInput className="w-full @4xl:w-[480px]" value={region} onChange={e => setRegion(e.target.value)} /></Row>
        <Row t="IP 地址" d="由 Agent 自动上报；更换公网 IP 后绑定的域名会自动改指向。"><div className="w-full @4xl:w-[480px] min-h-11 py-2 rounded-lg field px-3.5 flex items-center text-[13px] font-mono [overflow-wrap:anywhere]"><IP v={s.public_ipv4} /></div></Row>
        <div className="px-6 py-4 flex justify-end gap-2">{dirty && <Button size="sm" onClick={() => { setName(s.name); setProvider(s.provider ?? ''); setRegion(s.region ?? '') }}>取消</Button>}<Button variant="primary" size="sm" disabled={!dirty || !name.trim()} onClick={save}>保存配置</Button></div>
      </Section>
      <NetworkSection s={s} />
      <Section title="Agent">
        <Row t="Atoll Agent" d={`${s.agent_version || '未知版本'} · 本地保存期望状态，中控离线时容器照常运行`}><span className="flex items-center gap-2 text-[13px]"><Dot status={s.status} />{s.status === 'online' ? '已连接' : statusText[s.status] ?? s.status}</span><AgentUpgradeAction s={s} /></Row>
        {s.agent_upgrade?.state === 'failed' && s.agent_upgrade.error && <div className="px-6 pb-4 -mt-2 text-[12.5px] text-[#ef4444] [overflow-wrap:anywhere]">{s.agent_upgrade.error}</div>}
        <Row t="防火墙" d="80/443 仅放行 Cloudflare IP；SSH 与 Agent 只走 Tailscale"><Badge tone="green">已启用</Badge></Row>
      </Section>
      {isDemo() && <Section title="资源保留">
        <div className="p-6">
          <div className="text-[13.5px] text-muted mb-4">为 Docker、Caddy 和系统进程保留 CPU 与内存，项目容器不会占满整台机器。</div>
          <div className="rounded-xl border border-line bg-card2/60 p-5 flex flex-col @2xl:flex-row gap-5"><Stepper l="CPU" v={250} max={`${s.cpu_cores * 1000} m`} /><Stepper l="内存" v={512} max={`${s.mem_mb} Mi`} /></div>
          <div className="flex justify-end gap-2 mt-5"><Button variant="primary" size="sm">保存</Button><Button size="sm">取消</Button></div>
        </div>
      </Section>}
      <Section title={t('接管容器', 'Adopt containers')}>
        <Row t={t('接管 Docker 容器', 'Adopt Docker containers')} d={t('把这台服务器上你自己用 docker run / compose 启动的容器交给 Atoll：同样端口，数据原地挂载，旧容器保留可回滚。', 'Hand the containers you started here with docker run / compose to Atoll: same ports, data mounted in place, old containers kept for rollback.')}><Link to={`/import/docker?server=${s.id}`} className="btn-secondary h-8 px-3 inline-flex items-center gap-1.5 text-[12.5px]">{t('扫描容器', 'Scan containers')}</Link></Row>
      </Section>
      <Section title="危险区域">
        <Row t="迁出全部项目" d="把这台服务器上的项目迁到其他服务器，完成后自动切换 DNS。"><Button variant="danger-outline" size="sm" disabled={!ps.length} onClick={() => setEvac(true)}>迁出项目</Button></Row>
        <Row t="删除服务器" d="移除 Agent 并删除与它关联的所有数据。"><Button variant="danger" size="sm" onClick={() => setDel(true)}>删除服务器</Button></Row>
      </Section>
      {del && <ConfirmDialog title="删除服务器" name={s.name} onClose={() => setDel(false)}
        description={<>将从 Atoll 中移除 <b>{s.name}</b>，Agent 会停止并删除本机的期望状态。{ps.length ? <>这台服务器上还有 <b>{ps.length}</b> 个项目，它们会一并失去管理。</> : null}</>}
        onConfirm={async () => { const r = await run(() => api.deleteServer(s.id), { ok: '服务器已删除', invalidate: ['servers'] }); if (r !== undefined) nav('/servers') }} />}
      {evac && <Evacuate s={s} ps={ps} onClose={() => setEvac(false)} />}
    </>
  )
}

function NetworkSection({ s }: { s: Server }) {
  const { t } = useT()
  const state = useNetState()
  const mask = useMask()
  const n = s.network
  return (
    <Section title={<>{t('私有网络', 'Private network')}<NetBadge s={s} /></>}>
      <Row t={t('Tailscale', 'Tailscale')} d={s.tailnet_ip ? `${mask(s.tailnet_ip)} · ${state(n?.state)}` : n ? state(n.state) : t('这台服务器还没有加入 tailnet', 'This server is not on the tailnet yet')}>
        <ServerNetworkAction s={s} />
      </Row>
      <Row t={t('管理通道', 'Agent channel')} d={n?.fallback_urls?.length ? `${t('备用', 'Fallback')}: ${n.fallback_urls.join(', ')}` : undefined}>
        <span className="font-mono text-[12.5px] text-fg2 truncate max-w-full @4xl:max-w-[420px]">{n?.control_url || '—'}</span>
      </Row>
      {n?.mesh_error && <Row t={t('跨服务器转发', 'Cross-server mesh')} d={n.mesh_error}><Badge tone="red">{t('异常', 'Error')}</Badge></Row>}
    </Section>
  )
}

function Evacuate({ s, ps, onClose }: { s: Server; ps: Project[]; onClose: () => void }) {
  const targets = (useQuery('servers', api.servers).data ?? []).filter(x => x.id !== s.id && x.role !== 'control' && x.status === 'online')
  const [t, setT] = useState(''); const [confirm, setConfirm] = useState(false)
  const target = t || targets[0]?.id || ''
  const tName = targets.find(x => x.id === target)?.name
  if (confirm) return <ConfirmDialog title="迁出全部项目" action="开始迁移" name={s.name} onClose={onClose}
    description={<>将把 <b>{ps.length}</b> 个项目（{ps.map(projName).join('、')}）迁移到 <b>{tName}</b>。迁移期间服务会短暂停机，完成后自动切换 DNS。</>}
    onConfirm={async () => { for (const p of ps) await run(() => api.migrateProject(p.id, target)); run(async () => {}, { ok: '迁移任务已提交', invalidate: ['projects'] }) }} />
  return (
    <Modal title="迁出全部项目" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!target} onClick={() => setConfirm(true)}>下一步</Button></>}>
      <div className="text-[13.5px] text-fg2 mb-4">选择目标服务器，{ps.length} 个项目会依次迁移。</div>
      {targets.length ? <Select className="w-full" value={target} onChange={e => setT(e.target.value)}>{targets.map(x => <option key={x.id} value={x.id}>{x.name}</option>)}</Select> : <div className="text-[13px] text-muted">没有其他在线的服务器。</div>}
    </Modal>
  )
}

function SshTab({ s }: { s: Server }) {
  const host = s.tailnet_host || s.public_ipv4
  if (!isDemo()) return (
    <Card className="p-6">
      <div className="text-[13px] font-semibold mb-1 flex items-center gap-2"><SquareTerminal size={16} />SSH</div>
      <div className="text-[13px] text-muted mb-4">面板内终端尚未开放。通过 Tailscale 连接：</div>
      <CodeBlock>{`ssh root@${host}`}</CodeBlock>
      <div className="text-[12.5px] text-dim mt-3">也可以在服务页的「命令」里对单个容器执行命令。</div>
    </Card>
  )
  return (
    <Card className="bg-sunk overflow-hidden">
      <div className="h-10 px-4 flex items-center text-[13px] text-muted border-b border-line gap-2 min-w-0 whitespace-nowrap overflow-hidden"><SquareTerminal size={14} className="shrink-0" />root@{s.name.split(' ')[0].toLowerCase()} · 经 Tailscale</div>
      <pre className="p-5 font-mono text-[13px] leading-7 text-fg2 h-[460px] overflow-auto">{`Welcome to ${s.os} (GNU/Linux ${s.arch})
Last login: Thu Oct 29 20:58:11 2026 from 100.88.12.4
root@${s.id}:~# docker ps --format 'table {{.Names}}\\t{{.Status}}'
NAMES                 STATUS
atoll-mail-relay   Up 2 days
atoll-acme-api        Up 9 days
atoll-pg              Up 9 days (healthy)
atoll-redis           Up 9 days
atoll-linkproxy        Up 18 days
atoll-caddy           Up 18 days
atoll-agent           Up 18 days
root@${s.id}:~# `}<span className="inline-block w-2 h-4 bg-fg2 align-middle animate-pulse" /></pre>
    </Card>
  )
}

function StatusTab({ s }: { s: Server }) {
  const c = useQuery(`containers:${s.id}`, () => api.serverContainers(s.id), { interval: 15000 })
  const ps = useProjects().data
  if (!c.data) return c.error ? <ErrorState error={c.error} onRetry={c.refetch} /> : <RowsSkeleton n={5} />
  if (!c.data.length) return <Empty icon={<ServerIcon size={22} />} title="没有 Atoll 容器" desc="Agent 每 30 秒上报一次容器状态。" />
  const find = (sid: string) => { for (const p of ps ?? []) { const x = p.services?.find(v => v.id === sid); if (x) return { p, x } } return undefined }
  const st = (state: string) => state === 'running' ? 'running' : state === 'restarting' ? 'failed' : state === 'created' ? 'deploying' : 'stopped'
  return (
    <Card className="overflow-x-auto"><div className="min-w-[720px]">
      <div className="grid grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1.6fr)_minmax(0,1fr)_minmax(0,0.7fr)] gap-x-4 px-6 h-11 items-center text-[12.5px] text-muted border-b border-line"><span>容器</span><span>项目</span><span>镜像</span><span>状态</span><span>重启次数</span></div>
      {c.data.map(row => {
        const f = find(row.service_id)
        return (
          <div key={row.service_id} className="grid grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1.6fr)_minmax(0,1fr)_minmax(0,0.7fr)] gap-x-4 px-6 h-12 items-center text-[13px] border-b border-line last:border-0">
            <span className="font-mono text-[13px] truncate">{f ? <Link to={`/projects/${f.p.id}/services/${f.x.id}`} className="hover:underline">atoll-{f.x.name}</Link> : `atoll-${row.service_id}`}</span><span className="truncate" title={f?.p.name}>{f ? projName(f.p) : '—'}</span><span className="font-mono text-[12.5px] text-muted truncate">{f?.x.image ?? row.image_digest ?? '—'}</span>
            <span className="flex items-center gap-2"><Dot status={st(row.state)} />{row.state}{row.health ? ` · ${row.health}` : ''}</span><span className={row.restart_count ? 'text-[#f59e0b] font-semibold' : 'text-muted'}>{row.restart_count}</span>
          </div>
        )
      })}
    </div></Card>
  )
}
