import { Link, useParams, useSearchParams } from 'react-router-dom'
import { ArrowLeft, Info, Code2, SquareTerminal, BarChart3, Cpu, Settings, Bell, MemoryStick, HardDrive, Minus, Plus, RefreshCw } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Avatar, Badge, Button, Card, Chart, CopyId, Dot, Mini, Tabs, Ticks, series, statusText } from '../components/ui'
import { projectsOn, serverById } from '../mock'

const TABS = [
  { k: 'info', l: '服务器信息', icon: <Info size={16} /> },
  { k: 'projects', l: '项目', icon: <Code2 size={16} /> },
  { k: 'ssh', l: 'SSH', icon: <SquareTerminal size={16} /> },
  { k: 'metrics', l: '监控指标', icon: <BarChart3 size={16} /> },
  { k: 'status', l: '状态', icon: <Cpu size={16} /> },
  { k: 'settings', l: '设置', icon: <Settings size={16} /> },
]

export default function ServerDetail() {
  const { id } = useParams()
  const [sp, setSp] = useSearchParams()
  const s = serverById(id ?? 'phx') ?? serverById('phx')
  const tab = sp.get('tab') ?? 'info'
  return (
    <MainShell>
      <div className="max-w-[1480px] mx-auto px-20 pt-10 pb-16">
        <Link to="/servers" className="inline-flex items-center gap-1.5 text-[13.5px] text-fg2 hover:text-fg"><ArrowLeft size={15} />返回</Link>
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em] mt-2">{s.name}</h1>
        <div className="mt-1"><CopyId v="server-6a40e0d4378dd7103f8b429d" /></div>
        <Tabs className="mt-6" items={TABS} value={tab} onChange={k => setSp({ tab: k })} />
        <div className="mt-6">
          {tab === 'info' && <InfoTab id={s.id} />}
          {tab === 'projects' && <ProjectsTab id={s.id} />}
          {tab === 'metrics' && <MetricsTab id={s.id} />}
          {tab === 'settings' && <SettingsTab id={s.id} />}
          {tab === 'ssh' && <SshTab id={s.id} />}
          {tab === 'status' && <StatusTab id={s.id} />}
        </div>
      </div>
    </MainShell>
  )
}

function KV({ k, v }: { k: string; v: React.ReactNode }) {
  return <div className="flex items-center justify-between h-10 text-[13px]"><span className="text-muted">{k}</span><span className="text-fg">{v}</span></div>
}

function InfoTab({ id }: { id: string }) {
  const s = serverById(id)
  return (
    <>
      <div className="grid grid-cols-3 gap-5">
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-3">运行信息</div>
          <KV k="状态" v={<span className="flex items-center gap-2"><Dot status={s.status} />{s.status === 'online' ? 'Agent 已连接' : '失联 12 分钟'}</span>} />
          <KV k="运行时" v="Docker 27.3 · Caddy 2.9" />
          <KV k="管理通道" v="Tailscale · 直连" />
          <KV k="延迟" v={s.status === 'online' ? `${s.latency} ms` : '—'} />
          <KV k="区域" v={`${s.flag} ${s.region}`} />
          <KV k="IPv4" v={<span className="font-mono text-[13px]">{s.ip}</span>} />
        </Card>
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-3">规格信息</div>
          <KV k="服务商" v={<span className="flex items-center gap-2"><Mini provider={s.provider} />{s.provider}</span>} />
          <KV k="CPU" v={`${s.cpu} vCPU · ${s.arch}`} />
          <KV k="内存" v={`${s.memGB} GB`} />
          <KV k="硬盘" v={`${s.diskGB} GB`} />
          <KV k="系统" v={s.os} />
          <KV k="加入时间" v="2026-10-12" />
        </Card>
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-5">资源使用</div>
          {[['CPU 使用率', s.cpuUse, '#60a5fa'], ['内存使用率', s.memUse, '#a78bfa'], ['硬盘使用率', s.diskUse, '#34d399']].map(([l, v, c]) => (
            <div key={l as string} className="mb-5">
              <div className="flex justify-between text-[13px] mb-2"><span className="text-muted">{l}</span><span className="font-semibold">{v}%</span></div>
              <Ticks value={v as number} color={c as string} n={64} />
            </div>
          ))}
          <div className="text-[12.5px] text-dim">流量本月 186 GB / 3 TB</div>
        </Card>
      </div>
      <div className="mt-8 text-[13px] font-semibold mb-3">项目</div>
      <ProjectsTab id={id} />
    </>
  )
}

function ProjectsTab({ id }: { id: string }) {
  const s = serverById(id)
  return (
    <div className="grid grid-cols-3 gap-5">
      {projectsOn(id).map(p => {
        const run = p.services.filter(x => x.status === 'running').length
        return (
          <Link key={p.id} to={`/projects/${p.id}/services/${p.services[0].id}`} className="rounded-2xl surface surface-hover p-5">
            <div className="flex items-center gap-3"><div className="h-10 w-10 rounded-lg ring-1 ring-line2 bg-bg grid place-items-center font-semibold" style={{ color: p.color }}>{p.letter}</div><span className="text-[15px] font-semibold">{p.name}</span></div>
            <div className="mt-8 grid grid-cols-[auto_1fr] gap-y-3.5 text-[13px]">
              <span className="text-muted">服务器</span><span className="flex items-center justify-end gap-2"><Mini provider={s.provider} />{s.name}</span>
              <span className="text-muted">服务</span><span className="flex items-center justify-end gap-2"><Dot status={run === p.services.length ? 'running' : 'deploying'} />{run}/{p.services.length} 运行中</span>
              <span className="text-muted">成员</span><span className="flex justify-end"><Avatar size={22} /></span>
            </div>
          </Link>
        )
      })}
    </div>
  )
}

function MetricsTab({ id }: { id: string }) {
  const s = serverById(id)
  const ps = projectsOn(id)
  const colors = ['#a855f7', '#eab308', '#22c55e', '#ef4444', '#3b82f6', '#f97316']
  return (
    <>
      <div className="flex items-center justify-between mb-5"><h2 className="text-[20px] font-semibold tracking-tight">监控指标</h2><Button variant="primary"><Bell size={15} />设置告警</Button></div>
      <div className="grid grid-cols-3 gap-5">
        <Chart title="CPU 使用率" icon={<Cpu size={15} />} data={series(48, s.cpuUse, s.cpuUse * 0.4, 1)} color="#8b5cf6" max={100} />
        <Chart title="内存使用率" icon={<MemoryStick size={15} />} data={series(48, s.memUse, 4, 2)} color="#38bdf8" max={100} />
        <Chart title="硬盘空间使用率" icon={<HardDrive size={15} />} data={series(48, s.diskUse, 0.6, 3)} color="#34d399" max={100} />
      </div>
      <div className="flex items-center justify-between mt-9 mb-3"><h3 className="text-[15px] font-semibold">项目分布</h3><span className="text-[13px] text-muted">最近 12 小时</span></div>
      <div className="flex flex-wrap gap-x-5 gap-y-2 mb-4 text-[13px]">{ps.map((p, i) => <span key={p.id} className="flex items-center gap-2"><span className="h-3 w-3 rounded-full" style={{ background: colors[i % 6] }} />{p.name}</span>)}</div>
      <div className="grid grid-cols-2 gap-5">
        <Chart title="CPU 使用率（按项目）" icon={<Cpu size={15} />} data={series(48, 6, 3, 4)} color="#a855f7" />
        <Chart title="内存使用率（按项目）" icon={<MemoryStick size={15} />} data={series(48, 22, 3, 5)} color="#eab308" />
      </div>
    </>
  )
}

function Section({ title, children }: { title: React.ReactNode; children: React.ReactNode }) {
  return <div className="mb-8"><div className="text-[13px] font-semibold mb-3 flex items-center gap-2">{title}</div><Card className="divide-y divide-line">{children}</Card></div>
}
function Row({ t, d, children }: { t: React.ReactNode; d?: string; children?: React.ReactNode }) {
  return <div className="px-6 py-5 flex items-center gap-6"><div className="flex-1"><div className="text-[13px] font-semibold flex items-center gap-2">{t}</div>{d && <div className="text-[13.5px] text-muted mt-1">{d}</div>}</div>{children}</div>
}
const Input = ({ v, w = 'w-[480px]', mono }: { v: string; w?: string; mono?: boolean }) => <div className={`${w} h-11 rounded-lg field px-3.5 flex items-center text-[13px] ${mono ? 'font-mono text-[13px]' : ''}`}>{v}</div>

function Stepper({ l, v, max }: { l: string; v: number; max: string }) {
  return (
    <div className="flex-1">
      <div className="flex justify-between text-[13px] mb-2"><span className="font-semibold">{l}</span><span className="text-muted text-[12.5px]">{v} / {max}</span></div>
      <div className="h-11 rounded-lg field flex items-center px-3"><Minus size={15} /><span className="flex-1 text-center font-semibold">{v}</span><Plus size={15} /></div>
    </div>
  )
}

function SettingsTab({ id }: { id: string }) {
  const s = serverById(id)
  return (
    <>
      <Section title="基本">
        <Row t="服务器名称"><Input v={s.name} /></Row>
        <Row t="IP 地址" d="服务商更换公网 IP 后在此更新，绑定的域名会自动改指向。"><Input v={s.ip} w="w-[380px]" mono /><Button>更改 IP</Button></Row>
        <div className="px-6 py-4 flex justify-end"><Button variant="primary" size="sm">保存配置</Button></div>
      </Section>
      <Section title="Agent">
        <Row t="Atoll Agent" d="v0.1.0 · 本地保存期望状态，中控离线时容器照常运行"><span className="flex items-center gap-2 text-[13px]"><Dot status="online" />已连接</span><Button size="sm"><RefreshCw size={14} />更新 Agent</Button></Row>
        <Row t="防火墙" d="80/443 仅放行 Cloudflare IP；SSH 与 Agent 只走 Tailscale"><Badge tone="green">已启用</Badge></Row>
      </Section>
      <Section title="资源保留">
        <div className="p-6">
          <div className="text-[13.5px] text-muted mb-4">为 Docker、Caddy 和系统进程保留 CPU 与内存，项目容器不会占满整台机器。</div>
          <div className="rounded-xl border border-line bg-card2/60 p-5 flex gap-5"><Stepper l="CPU" v={250} max={`${s.cpu * 1000} m`} /><Stepper l="内存" v={512} max={`${s.memGB * 1024} Mi`} /></div>
          <div className="flex justify-end gap-2 mt-5"><Button variant="primary" size="sm">保存</Button><Button size="sm">取消</Button></div>
        </div>
      </Section>
      <Section title="危险区域">
        <Row t="迁出全部项目" d="把这台服务器上的项目迁到其他服务器，完成后自动切换 DNS。"><Button variant="danger-outline" size="sm">迁出项目</Button></Row>
        <Row t="删除服务器" d="移除 Agent 并删除与它关联的所有数据。"><Button variant="danger" size="sm">删除服务器</Button></Row>
      </Section>
    </>
  )
}

function SshTab({ id }: { id: string }) {
  const s = serverById(id)
  return (
    <Card className="bg-sunk overflow-hidden">
      <div className="h-10 px-4 flex items-center text-[13px] text-muted border-b border-line gap-2"><SquareTerminal size={14} />root@{s.name.split(' ')[0].toLowerCase()} · 经 Tailscale</div>
      <pre className="p-5 font-mono text-[13px] leading-7 text-fg2 h-[460px]">{`Welcome to ${s.os} (GNU/Linux ${s.arch})
Last login: Thu Oct 29 20:58:11 2026 from 100.88.12.4
root@phx:~# docker ps --format 'table {{.Names}}\\t{{.Status}}'
NAMES                 STATUS
mail-relay         Up 2 days
acme-api              Up 9 days
acme-api-postgresql   Up 9 days (healthy)
acme-api-redis        Up 9 days
linkproxy              Up 18 days
caddy                 Up 18 days
atoll-agent          Up 18 days
root@phx:~# `}<span className="inline-block w-2 h-4 bg-zinc-300 align-middle animate-pulse" /></pre>
    </Card>
  )
}

function StatusTab({ id }: { id: string }) {
  const rows = projectsOn(id).flatMap(p => p.services.map(x => ({ p, x })))
  return (
    <Card className="overflow-hidden">
      <div className="grid grid-cols-[1.4fr_1fr_1.6fr_1fr_1fr] px-6 h-11 items-center text-[12.5px] text-muted border-b border-line"><span>容器</span><span>项目</span><span>镜像</span><span>状态</span><span>重启次数</span></div>
      {rows.map(({ p, x }) => (
        <div key={p.id + x.id} className="grid grid-cols-[1.4fr_1fr_1.6fr_1fr_1fr] px-6 h-12 items-center text-[13px] border-b border-line last:border-0">
          <span className="font-mono text-[13px]">{p.id}-{x.id}</span><span>{p.name}</span><span className="font-mono text-[12.5px] text-muted truncate">{x.image}:{x.tag}</span>
          <span className="flex items-center gap-2"><Dot status={x.status} />{statusText[x.status]}</span><span className="text-muted">{x.status === 'failed' ? 14 : 0}</span>
        </div>
      ))}
    </Card>
  )
}
