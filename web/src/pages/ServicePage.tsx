import { Link, useParams, useSearchParams } from 'react-router-dom'
import { Activity, KeyRound, ArchiveRestore, BarChart3, Network, HardDrive, Settings, RotateCw, Rocket, Square, Globe, FolderOpen, ScrollText, SquareTerminal, ExternalLink, Pencil, Trash2, Plus, Eye, Lock, Cpu, MemoryStick, ArrowDownUp, Copy, Cloud, Box, Download, History, ShieldCheck, ChevronDown } from 'lucide-react'
import { ProjectShell } from '../components/Shell'
import { Badge, Button, Card, Chart, CopyId, Dot, Mini, ServiceIcon, Spark, Tabs, series, statusText, cx } from '../components/ui'
import AddServiceModal from '../components/AddServiceModal'
import { projectById, serverById, type Project, type Service } from '../mock'

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
  const p = projectById(pid)
  const s = p.services.find(x => x.id === sid) ?? p.services[0]
  const tab = sp.get('tab') ?? 'status'
  return (
    <ProjectShell project={p} active={s.id}>
      <div className="max-w-[1320px] mx-auto px-14 pt-10 pb-16">
        <div className="flex items-start gap-4">
          <ServiceIcon s={s} size={52} />
          <div className="flex-1">
            <div className="flex items-center gap-3"><h1 className="text-[26px] font-extrabold italic tracking-[-0.03em] leading-tight">{s.name}</h1><Badge tone={s.status === 'running' ? 'green' : s.status === 'failed' ? 'red' : s.status === 'deploying' ? 'amber' : 'zinc'}>{statusText[s.status]}</Badge></div>
            <div className="mt-1 flex items-center gap-4 text-[13px] text-muted"><CopyId v={`service-6ae0${s.id.length}31310ca070d4379490b1`} /><span>创建于 {s.created}</span></div>
          </div>
          <div className="flex gap-2"><Button><RotateCw size={15} />重新启动</Button><Button variant="primary"><Rocket size={15} />重新部署</Button></div>
        </div>
        <Tabs className="mt-7" items={TABS} value={tab} onChange={k => setSp({ tab: k })} />
        <div className="mt-6">
          {tab === 'status' && <StatusTab p={p} s={s} />}
          {tab === 'env' && <EnvTab p={p} s={s} />}
          {tab === 'backup' && <BackupTab />}
          {tab === 'usage' && <UsageTab s={s} />}
          {tab === 'network' && <NetworkTab p={p} s={s} />}
          {tab === 'disk' && <DiskTab s={s} />}
          {tab === 'settings' && <SettingsTab s={s} />}
        </div>
      </div>
      {sp.get('add') && <AddServiceModal onClose={() => { const n = new URLSearchParams(sp); n.delete('add'); setSp(n) }} />}
    </ProjectShell>
  )
}

function StatusTab({ p, s }: { p: Project; s: Service }) {
  const srv = serverById(p.serverId)
  const base = `/projects/${p.id}/services/${s.id}`
  return (
    <>
      <Card className="p-6">
        <div className="flex items-start gap-8">
          <div className="flex-1">
            <div className="flex items-center gap-3"><Dot status={s.status} /><span className="text-[17px] font-semibold tracking-tight">{statusText[s.status]}</span><span className="text-muted text-[13px]">· {s.replicas}</span></div>
            <div className="text-[13px] text-muted mt-1">最近一次部署 2 天前 · 由「修改环境变量」触发</div>
            <div className="mt-6 grid grid-cols-[100px_1fr] gap-y-3.5 text-[13px]">
              <span className="text-muted">镜像来源</span><span className="flex items-center gap-2 font-mono text-[13px]"><Box size={14} className="text-[#60a5fa]" />{s.image}:{s.tag}</span>
              <span className="text-muted">镜像摘要</span><span className="font-mono text-[13px] text-fg2">sha256:7f3a9c0e…e21b</span>
              <span className="text-muted">服务器</span><span className="flex items-center gap-2"><Mini provider={srv.provider} />{srv.name}</span>
              <span className="text-muted">资源</span><span>CPU 1 核 · 内存 512 MB · 重启策略 unless-stopped</span>
            </div>
          </div>
          <div className="w-[380px] space-y-3">
            <div className="rounded-xl field px-4 py-3.5">
              <div className="flex items-center text-[12px] text-muted">最近 30 天可用性<span className="ml-auto text-fg font-semibold tnum">{s.status === 'failed' ? '97.82%' : '99.98%'}</span></div>
              <div className="mt-2.5 flex gap-[3px]">{Array.from({ length: 30 }, (_, i) => {
                const bad = s.status === 'failed' ? i > 26 : i === 11
                return <span key={i} className={cx('flex-1 h-6 rounded-[3px]', bad ? (s.status === 'failed' && i > 27 ? 'bg-rose-500/80' : 'bg-amber-400/80') : 'bg-emerald-500/70')} />
              })}</div>
              <div className="mt-1.5 flex justify-between text-[10.5px] text-dim"><span>30 天前</span><span>今天</span></div>
            </div>
            <div className="grid grid-cols-2 gap-3">
              {[['CPU', '6.4%', series(30, 6, 3, 2), '#8b5cf6'], ['内存', '142 MB', series(30, 40, 5, 4), '#38bdf8']].map(([l, v, d, c]) => (
                <div key={l as string} className="rounded-xl field px-4 py-3">
                  <div className="text-[12px] text-muted">{l as string}</div>
                  <div className="flex items-end justify-between mt-1"><span className="text-[17px] font-semibold tnum">{v as string}</span><Spark data={d as number[]} color={c as string} /></div>
                </div>
              ))}
            </div>
          </div>
        </div>
        <div className="mt-6 pt-5 border-t border-line flex items-center gap-2">
          <Button size="sm"><History size={14} />部署记录</Button><Button size="sm"><Square size={13} />停止</Button>
          <span className="ml-auto text-[13px] text-dim">回滚到任意历史镜像摘要</span>
        </div>
      </Card>

      <div className="mt-6 text-[13px] font-semibold mb-3">域名</div>
      {s.domain ? (
        <Card className="px-6 py-4 flex items-center gap-4">
          <Globe size={18} className="text-accent" />
          <a className="text-[15px] font-semibold flex items-center gap-1.5 hover:underline">{s.domain}<ExternalLink size={13} /></a>
          <Badge tone="green"><ShieldCheck size={11} />证书有效</Badge>
          <span className="ml-auto flex items-center gap-1.5 text-[13px] text-muted"><Cloud size={14} className="text-[#f6821f]" />Cloudflare 代理 → {srv.ip}</span>
        </Card>
      ) : <Card className="px-6 py-5 text-[13px] text-muted flex items-center justify-between">此服务未公开访问，仅项目内网可达<Button size="sm"><Plus size={14} />绑定域名</Button></Card>}

      <div className="mt-6 grid grid-cols-3 gap-5">
        {[
          [<FolderOpen size={22} />, '文件', '浏览容器与卷中的文件', `${base}/files`],
          [<ScrollText size={22} />, '日志', '实时运行日志', `${base}/logs`],
          [<SquareTerminal size={22} />, '命令', '在容器内执行命令', `${base}/logs`],
        ].map(([i, t, d, to]) => (
          <Link key={t as string} to={to as string} className="rounded-2xl surface surface-hover px-6 py-5 flex items-center gap-4">
            <div className="h-11 w-11 rounded-lg bg-card2 ring-1 ring-line2 grid place-items-center text-fg">{i}</div>
            <div><div className="text-[13px] font-semibold">{t}</div><div className="text-[13px] text-muted mt-0.5">{d}</div></div>
          </Link>
        ))}
      </div>
    </>
  )
}

function EnvTab({ p, s }: { p: Project; s: Service }) {
  const db = p.services.find(x => x.kind !== 'image')
  const rows: [string, string, boolean, boolean][] = [
    ['PORT', String(s.port), false, false],
    ['TZ', 'Asia/Shanghai', false, false],
    ...(db ? [['DATABASE_URL', `\${${db.name.toUpperCase()}_CONNECTION_STRING}`, false, true] as [string, string, boolean, boolean]] : []),
    ['SECRET_KEY', '••••••••••••••••••••', true, false],
    ['ADMIN_PASSWORD', '••••••••••••', true, false],
    ['PUBLIC_URL', `https://\${ATOLL_WEB_DOMAIN}`, false, true],
  ]
  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <div><div className="text-[15px] font-semibold">用户变量</div><div className="text-[13px] text-muted mt-0.5">修改后需重新部署生效。可用 {'${变量名}'} 引用同项目其他服务暴露的变量。</div></div>
        <div className="flex gap-2"><Button><Pencil size={14} />编辑原始环境变量</Button><Button variant="primary"><Plus size={15} />添加</Button></div>
      </div>
      <Card className="divide-y divide-line">
        {rows.map(([k, v, secret, ref]) => (
          <div key={k} className="px-6 h-[52px] grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_150px] items-center gap-6 group hover:bg-ov/[0.02] transition-colors">
            <span className="font-mono text-[13px] font-medium">{k}</span>
            <span className={cx('font-mono text-[13px] truncate', ref ? 'text-accent-soft' : 'text-fg2')}>{v}</span>
            <span className="flex items-center justify-end gap-3 text-dim group-hover:text-muted transition-colors">
              {secret && <span className="flex items-center gap-1 text-[11.5px] rounded-full ring-1 ring-inset ring-line2 px-2 h-[22px]"><Lock size={11} />私有</span>}
              {secret && <Eye size={15} />}<Pencil size={15} /><Trash2 size={15} />
            </span>
          </div>
        ))}
      </Card>
      {db && (
        <>
          <div className="mt-8 text-[15px] font-semibold mb-1">可引用的变量</div>
          <div className="text-[13px] text-muted mb-4">来自同项目服务 {db.name}</div>
          <Card className="divide-y divide-line">
            {['HOST', 'PORT', 'USERNAME', 'PASSWORD', 'CONNECTION_STRING'].map(k => (
              <div key={k} className="px-6 h-12 grid grid-cols-[1fr_1.4fr_auto] items-center gap-6 text-[13px]">
                <span className="font-mono text-accent-soft">{db.name.toUpperCase()}_{k}</span>
                <span className="font-mono text-dim">{k === 'HOST' ? db.name : k === 'PORT' ? db.port : '••••••••'}</span><Copy size={14} className="text-muted" />
              </div>
            ))}
          </Card>
        </>
      )}
    </>
  )
}

function BackupTab() {
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
  return (
    <>
      <div className="flex items-center justify-between mb-4"><div className="text-[15px] font-semibold">用量</div><button className="h-9 px-3 rounded-lg border border-line bg-card text-[13.5px] flex items-center gap-2">最近 12 小时<ChevronDown size={14} /></button></div>
      <div className="grid grid-cols-2 gap-5">
        <Chart title="CPU" icon={<Cpu size={15} />} data={series(60, 3, 1.6, 1)} color="#8b5cf6" />
        <Chart title="内存" icon={<MemoryStick size={15} />} data={series(60, 180, 14, 2)} color="#38bdf8" unit=" MB" />
      </div>
      <div className="mt-5"><Chart title="网络用量" icon={<ArrowDownUp size={15} />} data={series(60, 1.1, 0.8, 3)} color="#34d399" unit=" MB/s" h={150} /></div>
      <div className="text-[12.5px] text-dim mt-3">{s.name} 本月出站流量 4.8 GB</div>
    </>
  )
}

function NetworkTab({ p, s }: { p: Project; s: Service }) {
  const srv = serverById(p.serverId)
  return (
    <>
      <div className="text-[15px] font-semibold mb-1">内网访问</div>
      <div className="text-[13px] text-muted mb-4">同项目的服务通过 Docker 内网直接互通，不经过公网。</div>
      <Card className="px-6 py-5 grid grid-cols-2 gap-6">
        <div><div className="text-[13px] text-muted mb-2">主机名</div><div className="h-11 rounded-lg field px-3.5 flex items-center justify-between font-mono text-[13.5px]">{s.id}<Copy size={14} className="text-muted" /></div></div>
        <div><div className="text-[13px] text-muted mb-2">端口</div><div className="h-11 rounded-lg field px-3.5 flex items-center justify-between font-mono text-[13.5px]">{s.port}<span className="text-[12px] text-dim">HTTP</span></div></div>
      </Card>

      <div className="mt-8 flex items-end justify-between mb-4">
        <div><div className="text-[15px] font-semibold">公网访问</div><div className="text-[13px] text-muted mt-0.5">绑定后自动在 Cloudflare 建代理记录，直接指向服务器，不经过中控。</div></div>
        <div className="flex gap-2"><Button><Plus size={15} />绑定子域名</Button><Button>绑定外部域名</Button></div>
      </div>
      <Card className="p-6">
        <div className="flex items-center justify-center gap-0 py-4">
          {[['用户', <Globe key="g" size={18} />], ['Cloudflare', <Cloud key="c" size={18} className="text-[#f6821f]" />], [`Caddy · ${srv.name.split(' ')[0]}`, <ShieldCheck key="s" size={18} className="text-emerald-400" />], [`${s.name}:${s.port}`, <Box key="b" size={18} className="text-accent" />]].map(([l, i], idx, a) => (
            <div key={idx} className="flex items-center">
              <div className="rounded-xl border border-line2 bg-card2 px-4 h-12 flex items-center gap-2.5 text-[13px] font-semibold">{i}{l}</div>
              {idx < a.length - 1 && <div className="w-16 border-t-2 border-dashed border-[#7c3aed]/60 relative"><span className="absolute -right-1 -top-[5px] text-[#7c3aed] text-[10px]">▶</span></div>}
            </div>
          ))}
        </div>
        {s.domain && (
          <div className="mt-4 rounded-lg border border-line bg-bg/50 px-5 h-14 flex items-center gap-3">
            <span className="h-2 w-2 rounded-full bg-emerald-500" /><span className="font-semibold text-[15px]">{s.domain}</span><Badge tone="green"><ShieldCheck size={11} />证书有效</Badge>
            <span className="text-[13px] text-muted ml-3">A {srv.ip} · 已代理 · Full (strict)</span>
            <span className="ml-auto flex gap-3 text-muted"><ExternalLink size={15} /><Pencil size={15} /><Trash2 size={15} /></span>
          </div>
        )}
      </Card>
    </>
  )
}

function DiskTab({ s }: { s: Service }) {
  const vols = s.kind === 'postgres' ? ['/var/lib/postgresql/data'] : s.kind === 'mysql' ? ['/var/lib/mysql'] : ['/app/data', '/app/logs']
  return (
    <>
      <div className="flex items-center justify-between mb-4"><div><div className="text-[15px] font-semibold">硬盘</div><div className="text-[13px] text-muted mt-0.5">卷保存在服务器本地 /var/lib/atoll/volumes，迁移时随服务一起搬走。</div></div><Button variant="primary"><Plus size={15} />挂载卷</Button></div>
      <Card className="divide-y divide-line">
        {vols.map((v, i) => (
          <div key={v} className="px-6 h-16 flex items-center gap-4">
            <HardDrive size={18} className="text-accent" />
            <div className="flex-1"><div className="font-mono text-[13px]">{v}</div><div className="text-[12.5px] text-muted mt-0.5">卷 ID vol-{s.id}-{i + 1}</div></div>
            <div className="w-56"><div className="flex justify-between text-[12.5px] mb-1.5"><span className="text-muted">已用</span><span>{i ? '86 MB' : '1.4 GB'}</span></div><div className="h-2.5 rounded-[4px] field"><div className="h-full bg-[#8b5cf6]" style={{ width: i ? '6%' : '28%' }} /></div></div>
            <span className="flex gap-3 text-muted ml-4"><FolderOpen size={15} /><Trash2 size={15} /></span>
          </div>
        ))}
      </Card>
    </>
  )
}

function SettingsTab({ s }: { s: Service }) {
  const Row = ({ t, d, children }: { t: string; d?: string; children: React.ReactNode }) => <div className="px-6 py-5 flex items-center gap-6"><div className="flex-1"><div className="text-[13px] font-semibold">{t}</div>{d && <div className="text-[13px] text-muted mt-1">{d}</div>}</div>{children}</div>
  const Input = ({ v, mono }: { v: string; mono?: boolean }) => <div className={cx('w-[420px] h-11 rounded-lg field px-3.5 flex items-center text-[13px]', mono && 'font-mono text-[13px]', !v && 'text-dim')}>{v || '使用镜像默认值'}</div>
  const Sec = ({ t, children }: { t: string; children: React.ReactNode }) => <div className="mb-8"><div className="text-[13px] font-semibold mb-3">{t}</div><Card className="divide-y divide-line">{children}</Card></div>
  return (
    <>
      <Sec t="基本">
        <Row t="服务名称" d="也是项目内网的主机名"><Input v={s.name} /></Row>
        <Row t="镜像" d="Docker Hub、GHCR 或私有仓库"><Input v={`${s.image}:${s.tag}`} mono /></Row>
        <Row t="镜像更新" d="检测到同标签新摘要时提示，可选自动重新部署"><span className="h-6 w-11 rounded-full bg-[#7c3aed] relative"><span className="absolute right-0.5 top-0.5 h-5 w-5 rounded-full bg-white" /></span></Row>
      </Sec>
      <Sec t="启动">
        <Row t="启动命令" d="覆盖镜像的 CMD"><Input v="" mono /></Row>
        <Row t="健康检查" d="失败 3 次后自动重启"><Input v={`GET /health :${s.port}`} mono /></Row>
      </Sec>
      <Sec t="资源限制">
        <Row t="CPU" d="核"><Input v="1.0" /></Row>
        <Row t="内存" d="超出后容器会被 OOM 重启"><Input v="512 MB" /></Row>
      </Sec>
      <Sec t="危险区域">
        <Row t="迁移到其他服务器" d="两轮 rsync 同步卷数据，切换后自动更新 DNS"><Button variant="danger-outline" size="sm">迁移服务</Button></Row>
        <Row t="删除服务" d="容器、卷与域名记录都会被删除"><Button variant="danger" size="sm">删除服务</Button></Row>
      </Sec>
    </>
  )
}
