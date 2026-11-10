import { L, getLang } from '../i18n'
// 中控 (Control): the atoll-server host itself. Workloads keep running while it restarts or is down;
// the same machine may also run an agent (a worker), shown as 同机节点. Concepts: docs/control.md, docs/ui-guide.md.
import { useEffect, useState } from 'react'
import { IP } from '../components/IP'
import { Link, useSearchParams } from 'react-router-dom'
import {
  Gauge, CircleArrowUp, Archive, ScrollText, ShieldCheck, Truck, Cpu, MemoryStick, HardDrive, ExternalLink, RefreshCw, Loader2,
  CheckCircle2, XCircle, Undo2, Rocket, Info, AlertTriangle, Circle, ServerCog, Pencil, Server as ServerIcon,
} from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, Chart, Dot, Tabs, Ticks } from '../components/ui'
import { CardSkeleton, CodeBlock, ErrorState, Modal, Skeleton, TextInput } from '../components/feedback'
import { ReauthChip } from '../components/Reauth'
import { KV, Where } from '../components/control-ui'
import { api, type Range } from '../api/client'
import { controlApi, PHASES, phaseLabel, triggerLabel, uptime, useControl } from '../api/control'
import { invalidate, run, useBusy, useQuery } from '../api/hooks'
import { ago, bytes, dateTime, gb, timeLabels } from '../api/view'
import type { ControlStatus, UpdateResult } from '../api/types'
import { RangePicker } from './ServerDetail'
import { BackupsTab } from '../components/ControlBackups'
import { AuditTab, LogsTab, MigrateTab } from '../components/ControlTabs'
import { cx, installServerURL } from '../components/ui-utils'
import { toast } from '../components/toast'
import { useReauth } from '../components/useReauth'

const TABS = [
  { k: 'overview', l: L("概览"), icon: <Gauge size={16} /> },
  { k: 'update', l: L("更新"), icon: <CircleArrowUp size={16} /> },
  { k: 'backups', l: L("备份"), icon: <Archive size={16} /> },
  { k: 'logs', l: L("日志"), icon: <ScrollText size={16} /> },
  { k: 'audit', l: L("审计日志"), icon: <ShieldCheck size={16} /> },
  { k: 'migrate', l: L("迁移"), icon: <Truck size={16} /> },
]

/** An update/rollback this tab started: poll fast until a new last_update_result shows up. */
type Watch = { mode: string; version?: string; prevStart?: string; since: number }

export default function Control() {
  const [sp, setSp] = useSearchParams()
  const tab = TABS.some(t => t.k === sp.get('tab')) ? sp.get('tab')! : 'overview'
  const [watch, setWatch] = useState<Watch>()
  const [renaming, setRenaming] = useState(false)
  const [remoteBusy, setRemoteBusy] = useState(false) // an update started elsewhere (CLI, another tab)
  const q = useControl(watch || remoteBusy ? 2000 : 30000)
  const s = q.data
  const inProgress = !!s?.update_in_progress
  if (inProgress !== remoteBusy) setRemoteBusy(inProgress) // adjust during render, not in an effect
  // finished: the helper reported a result newer than the one we saw when starting
  useEffect(() => {
    if (!watch || !s || q.error) return
    const r = s.last_update_result
    const target = watch.version || s.update_target
    if (target && s.version === target && !(r && r.started_at !== watch.prevStart && !r.ok)) {
      // the new binary is serving: done from the user's point of view (the helper's report follows)
      toast(watch.mode === 'rollback' ? L("已回滚到 {0}", target) : L("已升级到 {0}", target), 'ok')
      // oxlint-disable-next-line react/set-state-in-effect
      setWatch(undefined); invalidate('control')
      return
    }
    if (s.update_in_progress) return
    if (r && r.started_at !== watch.prevStart) {
      if (r.ok) toast(r.mode === 'rollback' ? L("已回滚到 {0}", r.to_version) : L("已升级到 {0}", r.to_version), 'ok')
      else toast(r.rolled_back ? L("新版本不健康，已自动换回 {0}", r.from_version) : L("{0}失败：{1}", r.mode === 'rollback' ? L('回滚') : (getLang() === 'en' ? 'Upgrade' : '升级'), r.error ?? ''), 'error')
      // reacting to a polled result (toast + stop watching) is the effect's job; nothing to derive in render
      // oxlint-disable-next-line react/set-state-in-effect
      setWatch(undefined); invalidate('control')
    } else if (Date.now() - watch.since > 5 * 60e3) setWatch(undefined)
  }, [s, watch, q.error])
  const tabs = TABS.map(t => t.k === 'update' && s?.update_available ? { ...t, l: L("更新 · 有新版本") } : t)
  return (
    <MainShell>
      <div className="max-w-[1480px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @6xl:px-20 @5xl:pb-16">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <h1 className="text-[22px] @2xl:text-[26px] font-extrabold italic tracking-[-0.03em]">{L("中控")}</h1>
          {s && <span className="flex items-center gap-2 text-[13px] text-fg2 min-w-0"><Dot status={q.error ? 'offline' : 'online'} /><span className="font-semibold text-fg truncate" title={s.host.hostname}>{s.name || s.host.hostname}</span>
            <button aria-label={L("重命名中控")} title={L("重命名")} onClick={() => setRenaming(true)} className="text-muted hover:text-fg"><Pencil size={13} /></button></span>}
          <span className="ml-auto"><ReauthChip /></span>
        </div>
        {renaming && s && <RenameControl s={s} onClose={() => setRenaming(false)} />}
        <Tabs className="mt-6" items={tabs} value={tab} onChange={k => setSp(k === 'overview' ? {} : { tab: k })} />
        <div className="mt-6">
          {q.error && !s && <ErrorState error={q.error} onRetry={q.refetch} />}
          {!s && !q.error && <CardSkeleton n={3} h={220} />}
          {s && tab === 'overview' && <Overview s={s} go={k => setSp({ tab: k })} />}
          {s && tab === 'update' && <UpdateTab s={s} offline={!!q.error} watch={watch} setWatch={setWatch} />}
          {s && tab === 'backups' && <BackupsTab s={s} />}
          {tab === 'logs' && <LogsTab />}
          {tab === 'audit' && <AuditTab />}
          {s && tab === 'migrate' && <MigrateTab s={s} />}
        </div>
      </div>
    </MainShell>
  )
}

function RenameControl({ s, onClose }: { s: ControlStatus; onClose: () => void }) {
  const [name, setName] = useState(s.name ?? '')
  const [busy, wrap] = useBusy()
  const save = () => wrap(async () => { if (await run(() => controlApi.rename(name.trim()), { ok: name.trim() ? L("已重命名") : L("已恢复为主机名"), invalidate: ['control'] })) onClose() })
  return (
    <Modal title={<><Pencil size={16} />{L("重命名中控")}</>} onClose={onClose} width={440}
      footer={<><Button size="sm" onClick={onClose}>{L("取消")}</Button><Button size="sm" variant="primary" disabled={busy || [...name.trim()].length > 100} onClick={save}>{busy && <Loader2 size={14} className="animate-spin" />}{L("保存")}</Button></>}>
      <TextInput autoFocus className="w-full" placeholder={s.host.hostname} value={name} maxLength={100} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === 'Enter' && save()} />
      <div className="mt-2 text-[12.5px] text-muted">{L("留空则显示主机名")} {s.host.hostname}</div>
    </Modal>
  )
}

const pct = (a: number, b: number) => (b > 0 ? Math.round((a / b) * 100) : 0)

function Overview({ s, go }: { s: ControlStatus; go: (tab: string) => void }) {
  const t = s.tailnet, b = s.last_backup
  // the control host can also run an agent: same tailnet address (or same public IP)
  const servers = useQuery('servers', api.servers).data ?? []
  const pubHost = (() => { try { return s.public_url ? new URL(s.public_url).hostname : '' } catch { return '' } })()
  const peer = servers.find(x => x.role !== 'control' && ((t?.ip && x.tailnet_ip === t.ip) || (pubHost && x.public_ipv4 === pubHost)))
  return (
    <>
      <div className="grid grid-cols-1 @3xl:grid-cols-2 @6xl:grid-cols-3 gap-5 [&>*]:min-w-0">
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-3">{L("运行信息")}</div>
          <KV k={L("状态")} v={<span className="inline-flex items-center gap-2"><Dot status="online" />{L("运行中 · 已运行")} {uptime(s.uptime_seconds)}</span>} />
          <KV k={L("版本")} v={<span className="inline-flex flex-wrap items-center justify-end gap-2"><span className="font-mono font-semibold">{s.version}</span>{s.update_available && s.latest_release && <button onClick={() => go('update')}><Badge tone="violet"><CircleArrowUp size={12} />{s.latest_release.tag} {L("可用")}</Badge></button>}</span>} />
          <KV k="Commit" v={<span className="font-mono text-[12.5px]">{s.commit || '—'}</span>} />
          <KV k="Go" v={<span className="font-mono text-[12.5px]">{s.go_version}</span>} />
          <KV k={L("启动时间")} v={dateTime(s.started_at)} />
        </Card>
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-3">{L("主机")}</div>
          <KV k={L("主机名")} v={<span className="font-mono text-[12.5px]">{s.host.hostname}</span>} />
          {peer && <KV k={L("同机节点")} v={<Link to={`/servers/${peer.id}`} className="inline-flex items-center gap-1.5 hover:underline"><ServerIcon size={13} />{peer.name}</Link>} />}
          <KV k={L("系统")} v={s.host.os || '—'} />
          <KV k={L("内核")} v={<span className="font-mono text-[12.5px]">{s.host.kernel || '—'}</span>} />
          <KV k={L("架构")} v={`${s.host.arch} · ${s.host.cpu_cores} vCPU`} />
          <KV k={L("开机时长")} v={uptime(s.host.uptime_seconds)} />
        </Card>
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-5">{L("资源使用")}</div>
          {([['CPU', Math.round(s.cpu_pct), `${s.cpu_pct.toFixed(1)}%`, '#60a5fa'], [L("内存"), pct(s.mem_used_mb, s.mem_total_mb), `${gb(s.mem_used_mb)} / ${gb(s.mem_total_mb)}`, '#a78bfa'], [L("硬盘（数据目录所在）"), pct(s.disk_used_gb, s.disk_total_gb), `${s.disk_used_gb} / ${s.disk_total_gb} GB`, '#34d399']] as const).map(([l, v, txt, c]) => (
            <div key={l} className="mb-5">
              <div className="flex justify-between gap-3 text-[13px] mb-2"><span className="text-muted truncate">{l}</span><span className="font-semibold tnum whitespace-nowrap">{txt}</span></div>
              <Ticks value={v} color={c} n={64} />
            </div>
          ))}
          <div className="text-[12.5px] text-dim tnum">{L("负载")} {s.load1.toFixed(2)} / {s.load5.toFixed(2)} / {s.load15.toFixed(2)}</div>
        </Card>
        <Card className="p-6 flex flex-col">
          <div className="text-[13px] font-semibold mb-3">{L("数据")}</div>
          <KV k={L("数据库")} v={<span className="tnum">{bytes(s.database_bytes)}<span className="text-dim"> · SQLite + WAL</span></span>} />
          <KV k={L("数据目录")} v={<span className="tnum">{bytes(s.data_dir_bytes)}</span>} />
          <KV k={L("路径")} v={<span className="font-mono text-[12.5px]">{s.data_dir}</span>} />
          <KV k={L("所在硬盘可用")} v={<span className="tnum">{Math.max(0, s.disk_total_gb - s.disk_used_gb).toFixed(1)} GB<span className="text-dim"> / {s.disk_total_gb} GB</span></span>} />
          {/* fills the card to the row's height: what the data directory is made of */}
          <div className="mt-auto pt-4">
            <div className="flex justify-between gap-3 text-[12.5px] mb-2"><span className="text-muted">{L("数据库占数据目录")}</span><span className="font-semibold tnum">{pct(s.database_bytes, s.data_dir_bytes)}%</span></div>
            <Ticks value={pct(s.database_bytes, s.data_dir_bytes)} color="#fbbf24" n={64} />
            <div className="mt-2 text-[12px] text-dim">{L("其余主要是中控备份、迁移数据包和 agent 下载文件")}</div>
          </div>
        </Card>
        <Card className="p-6">
          <div className="text-[13px] font-semibold mb-3">{L("网络")}</div>
          <KV k={L("公网地址")} v={s.public_url ? <a href={s.public_url} target="_blank" rel="noreferrer" className="font-mono text-[12.5px] hover:underline">{s.public_url}</a> : '—'} />
          <KV k="Tailnet" v={t ? <span className="inline-flex flex-wrap items-center justify-end gap-2"><IP className="font-mono text-[12.5px]" v={t.ip} />{t.listen_error ? <Badge tone="red">{L("监听失败")}</Badge> : t.listen ? <Badge tone="green">{L("监听中")}</Badge> : null}</span> : <span className="text-dim">{L("未启用")}</span>} />
          {t?.control_url && <KV k={L("Agent 私网地址")} v={<span className="font-mono text-[12px] tracking-tight">{t.control_url}</span>} />}
          {t?.listen_error && <div className="text-[12.5px] text-danger font-semibold py-1 [overflow-wrap:anywhere]">{t.listen_error}</div>}
          <KV k={L("Agent 连接")} v={<Link to="/servers" className="inline-flex items-center gap-2 hover:underline"><Dot status={s.agents_connected === s.agents_total ? 'online' : 'deploying'} />{s.agents_connected} / {s.agents_total} {L("台服务器在线")}</Link>} />
        </Card>
        <Card className="p-6 flex flex-col">
          <div className="text-[13px] font-semibold mb-3 flex items-center gap-2">{L("最近一次中控备份")}<button onClick={() => go('backups')} className="ml-auto text-[12.5px] font-medium text-accent hover:underline">{L("全部备份")}</button></div>
          {b ? <>
            <KV k={L("时间")} v={<span>{dateTime(b.created_at)}<span className="text-dim"> · {ago(b.created_at)}</span></span>} />
            <KV k={L("触发")} v={triggerLabel[b.trigger] ?? b.trigger} />
            <KV k={L("大小")} v={<span className="tnum">{bytes(b.size_bytes)}</span>} />
            <KV k={L("位置")} v={<Where local={b.local} remote={b.remote} />} />
            {b.error && <div className="text-[12.5px] text-warn font-semibold py-1 [overflow-wrap:anywhere]">{b.error}</div>}
          </> : <div className="flex-1 grid place-items-center text-[13px] text-dim py-6">{L("还没有中控备份（每天自动备份一次）")}</div>}
        </Card>
      </div>
      <Metrics />
    </>
  )
}

function Metrics() {
  const [range, setRange] = useState<Range>('24h')
  const m = useQuery(`control-metrics:${range}`, () => controlApi.metrics(range), { interval: 60000 })
  const d = m.data ?? []
  const labels = timeLabels(d.map(x => x.at))
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3 mt-9 mb-5"><h2 className="text-[18px] font-semibold tracking-tight">{L("资源曲线")}</h2><RangePicker v={range} set={setRange} /></div>
      {m.error && !m.data && <ErrorState error={m.error} onRetry={m.refetch} />}
      <div className="grid grid-cols-1 @3xl:grid-cols-2 @6xl:grid-cols-3 gap-5 [&>*]:min-w-0">
        <Chart title={L("CPU 使用率")} icon={<Cpu size={15} />} data={d.map(x => x.cpu_pct)} color="var(--pick)" max={100} labels={labels} />
        <Chart title={L("内存使用率")} icon={<MemoryStick size={15} />} data={d.map(x => (x.mem_total_mb ? (x.mem_used_mb / x.mem_total_mb) * 100 : 0))} color="#38bdf8" max={100} labels={labels} />
        <Chart title={L("硬盘空间使用率")} icon={<HardDrive size={15} />} data={d.map(x => (x.disk_total_gb ? (x.disk_used_gb / x.disk_total_gb) * 100 : 0))} color="#34d399" max={100} labels={labels} />
      </div>
    </>
  )
}

// ---------- 更新 ----------
function UpdateTab({ s, offline, watch, setWatch }: { s: ControlStatus; offline: boolean; watch?: Watch; setWatch: (w?: Watch) => void }) {
  const { guard, dialog } = useReauth()
  const [checking, wrapCheck] = useBusy()
  const [confirm, setConfirm] = useState<'update' | 'rollback'>()
  const rel = s.latest_release
  const busy = s.update_in_progress || !!watch
  // the new version is already serving (only the helper's health report is outstanding): show the
  // finished state, not a progress header next to it
  const target = watch?.version || s.update_target
  const arrived = !!target && s.version === target && !offline
  const check = () => wrapCheck(() => run(() => controlApi.checkUpdate(), { invalidate: ['control'] }).then(r => {
    if (r) toast(r.update_available ? L("发现新版本 {0}", r.latest_release?.tag) : r.release_error ? L("检查失败：{0}", r.release_error) : L("已是最新版本"), r.release_error && !r.update_available ? 'error' : r.update_available ? 'info' : 'ok')
  }))
  const start = async (mode: 'update' | 'rollback') => {
    setConfirm(undefined)
    const prevStart = s.last_update_result?.started_at
    const r = await guard(() => mode === 'update' ? controlApi.update(rel?.tag) : controlApi.rollback(), {
      title: mode === 'update' ? L("升级到 {0}", rel?.tag) : L("回滚到 {0}", s.rollback_version),
      desc: mode === 'update' ? L("升级中控需要确认身份。") : L("回滚中控需要确认身份。"),
    })
    if (r) { setWatch({ mode, version: r.version, prevStart, since: Date.now() }); invalidate('control') }
  }
  return (
    <div className="space-y-6">
      {busy && !arrived && <Progress s={s} offline={offline} watch={watch} />}
      <Card className="p-5 @2xl:p-6">
        <div className="grid grid-cols-1 @3xl:grid-cols-2 gap-5 [&>*]:min-w-0">
          <div className="rounded-xl border-2 border-dashed border-line2 p-4 min-w-0">
            <div className="text-[12.5px] text-muted">{L("当前版本")}</div>
            <div className="mt-1 text-[22px] font-extrabold italic font-mono tracking-tight">{s.version}</div>
            <div className="text-[12.5px] text-dim font-mono mt-0.5 truncate">{s.commit ? `commit ${s.commit}` : ''}{s.commit ? ' · ' : ''}{s.go_version}</div>
          </div>
          <div className={cx('rounded-xl p-4 min-w-0', s.update_available ? 'surface !bg-[color-mix(in_srgb,var(--pick)_16%,var(--card))]' : 'border-2 border-dashed border-line2')}>
            <div className="text-[12.5px] text-muted flex items-center gap-2">{L("最新发布")}{rel?.prerelease && <Badge tone="amber">{L("预发布")}</Badge>}</div>
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1">
              <span className="text-[22px] font-extrabold italic font-mono tracking-tight">{rel?.tag ?? '—'}</span>
              {s.update_available ? <Badge tone="violet">{L("有新版本")}</Badge> : rel && <Badge tone="green">{L("已是最新")}</Badge>}
            </div>
            <div className="text-[12.5px] text-dim mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-1">
              {rel && <span>{L("发布于")} {ago(rel.published_at)}</span>}
              {rel?.url && <a href={rel.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-accent hover:underline font-medium">{L("发布说明")}<ExternalLink size={12} /></a>}
            </div>
          </div>
        </div>
        {s.release_error && <div className="mt-4 rounded-lg border-2 border-[var(--edge)] bg-t-note text-ink px-3.5 py-2.5 text-[12.5px] font-semibold flex items-start gap-2 [overflow-wrap:anywhere]"><AlertTriangle size={14} className="mt-0.5 shrink-0" /><span className="min-w-0">{L("检查 GitHub 发布失败：")}{s.release_error}</span></div>}
        <div className="mt-5 flex flex-wrap items-center gap-3">
          <Button variant="primary" disabled={!s.update_available || !s.self_update_supported || busy || !rel} onClick={() => setConfirm('update')}><Rocket size={15} />{s.update_available && rel ? L("升级到 {0}", rel.tag) : L("已是最新版本")}</Button>
          <Button onClick={check} disabled={checking || busy}>{checking ? <Loader2 size={15} className="animate-spin" /> : <RefreshCw size={15} />}{L("检查更新")}</Button>
          {rel && <span className="text-[12.5px] text-dim">{L("上次检查")} {ago(rel.checked_at)} {L("· 每 6 小时自动检查")}</span>}
        </div>
        {!s.self_update_supported && <div className="mt-5">
          <div className="text-[13px] text-fg2 mb-2 leading-6">{L("atoll-server 不是以 systemd 服务运行，无法在面板里自更新。请在中控主机上用安装脚本升级：")}</div>
          <div className="[&>div]:whitespace-pre-wrap [&>div]:[overflow-wrap:anywhere]"><CodeBlock>{`curl -fsSL ${installServerURL(rel?.tag ?? 'main')} | sudo sh -s -- --version ${rel?.tag ?? 'vX.Y.Z'}`}</CodeBlock></div>
        </div>}
      </Card>
      <Card className="p-5 flex gap-3 items-start">
        <Info size={17} className="text-accent mt-0.5 shrink-0" />
        <div className="text-[13px] text-fg2 leading-6 min-w-0">
          <b>{L("升级期间各服务器上的服务照常运行。")}</b>{L("中控重启时面板和 API 会中断几秒，agent 会自动重连；业务容器、对外域名和 HTTPS 都在各 worker 上，不受影响。\n          升级前会自动做一份")} <span className="font-mono">pre-update</span> {L("中控备份；新版本 60 秒内没有通过健康检查会自动换回旧版本。")}
        </div>
      </Card>
      {s.last_update_result && <LastResult r={s.last_update_result} />}
      <Card className="p-5 @2xl:p-6">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
          <div className="flex-1 min-w-[min(100%,260px)]">
            <div className="text-[13px] font-semibold flex items-center gap-2"><Undo2 size={15} />{L("回滚")}</div>
            <div className="text-[13px] text-muted mt-1 leading-6">{s.rollback_version
              ? <>{L("把")} <span className="font-mono">atoll-server</span> {L("换回上一个版本")} <b className="font-mono text-fg">{s.rollback_version}</b>{L("，同样有 60 秒健康检查（再回滚一次就回到当前版本）。新版本可能已给数据库加了新表或列，旧版本一般能忽略；如有问题，用 pre-update 备份恢复。")}</>
              : L("没有可回滚的上一个版本（自更新成功后才会保留 atoll-server.prev）。")}</div>
          </div>
          <Button variant="danger-outline" disabled={!s.rollback_version || busy || !s.self_update_supported} onClick={() => setConfirm('rollback')}><Undo2 size={15} />{L("回滚")}{s.rollback_version ? L("到 {0}", s.rollback_version) : ''}</Button>
        </div>
      </Card>
      {confirm && <Modal width={520} title={<>{confirm === 'update' ? <Rocket size={17} className="text-accent" /> : <Undo2 size={17} className="text-danger" />}{confirm === 'update' ? L("升级到 {0}", rel?.tag) : L("回滚到 {0}", s.rollback_version)}</>} onClose={() => setConfirm(undefined)}
        footer={<><Button size="sm" onClick={() => setConfirm(undefined)}>{L("取消")}</Button><Button size="sm" variant={confirm === 'update' ? 'primary' : 'danger'} onClick={() => start(confirm)}>{confirm === 'update' ? L("开始升级") : L("开始回滚")}</Button></>}>
        <div className="text-[13.5px] text-fg2 leading-6 space-y-2 [overflow-wrap:anywhere]">
          {confirm === 'update'
            ? <><p>{L("中控将从 GitHub 下载")} <b className="font-mono">{rel?.tag}</b>{L("，校验 SHA256 和版本号，先做一份 pre-update 中控备份，再切换并重启。")}</p></>
            : <p>{L("中控将换回")} <b className="font-mono">{s.rollback_version}</b> {L("并重启。")}</p>}
          <p>{L("面板会中断几秒后自动恢复；")}<b>{L("各服务器上的服务不受影响")}</b>{L("。60 秒内不健康会自动换回。")}</p>
        </div>
      </Modal>}
      {dialog}
    </div>
  )
}

function Progress({ s, offline, watch }: { s: ControlStatus; offline: boolean; watch?: Watch }) {
  const mode = watch?.mode ?? 'update'
  const steps = mode === 'rollback' ? ['restarting'] : PHASES
  const phase = s.update_phase ?? (watch ? (mode === 'rollback' ? 'restarting' : 'downloading') : undefined)
  const cur = Math.max(0, steps.indexOf(phase ?? ''))
  const [t0] = useState(() => Date.now())
  const [now, setNow] = useState(t0)
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [])
  const el = Math.floor((now - t0) / 1000)
  return (
    <Card className="p-5 @2xl:p-6 !bg-[color-mix(in_srgb,var(--pick)_16%,var(--card))]">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <Loader2 size={17} className="animate-spin text-accent" />
        <div className="text-[15px] font-extrabold italic tracking-[-0.02em]">{mode === 'rollback' ? L("正在回滚") : L("正在升级")}{watch?.version ? <span className="font-mono"> → {watch.version}</span> : null}</div>
        <span className="ml-auto text-[12.5px] text-muted tnum">{el}s</span>
      </div>
      <div className="mt-4 grid grid-cols-1 @2xl:grid-cols-2 @5xl:grid-cols-4 gap-2.5">
        {steps.map((k, i) => (
          <div key={k} className={cx('rounded-lg px-3 py-2.5 flex items-center gap-2.5 text-[13px] min-w-0 border-2', i === cur ? 'border-[var(--edge)] bg-[var(--card)] font-semibold' : 'border-transparent', i > cur && 'text-dim')}>
            {i < cur ? <CheckCircle2 size={16} className="text-emerald-500 shrink-0" /> : i === cur ? <Loader2 size={16} className="animate-spin text-accent shrink-0" /> : <Circle size={16} className="shrink-0" />}
            <span className="min-w-0">{phaseLabel[k] ?? k}</span>
          </div>
        ))}
      </div>
      <div className="mt-3 text-[12.5px] text-fg2 leading-6">
        {offline ? L("中控正在重启，暂时连不上，等它恢复后这里会自动更新…") : L("可以离开本页，升级在中控后台继续。")}{L("各服务器上的服务照常运行。")}
      </div>
    </Card>
  )
}

function LastResult({ r }: { r: UpdateResult }) {
  const tone = r.ok ? 'green' : r.rolled_back ? 'amber' : 'red'
  return (
    <Card className="p-5 @2xl:p-6">
      <div className="flex flex-wrap items-center gap-2">
        <div className="text-[13px] font-semibold mr-1">{L("上一次")}{r.mode === 'rollback' ? L("回滚") : L("升级")}</div>
        <Badge tone={tone}>{r.ok ? <><CheckCircle2 size={12} />{L("成功")}</> : r.rolled_back ? <><Undo2 size={12} />{L("失败，已自动换回")}</> : <><XCircle size={12} />{L("失败")}</>}</Badge>
        <span className="ml-auto text-[12.5px] text-dim">{dateTime(r.finished_at || r.started_at)} · {ago(r.finished_at || r.started_at)}</span>
      </div>
      <div className="mt-3 font-mono text-[13px] [overflow-wrap:anywhere]">{r.from_version || '?'} → {r.to_version || '?'}</div>
      {r.error && <div className="mt-2 text-[12.5px] text-danger font-semibold leading-5 [overflow-wrap:anywhere]">{r.error}</div>}
    </Card>
  )
}

/** Compact card at the top of the 服务器 list: the control host is not a server, so it gets its own row. */
export function ControlCard() {
  const q = useControl(30000)
  const s = q.data
  const on = !!s && !q.error
  return (
    <Link to="/control" className="rounded-2xl surface surface-hover px-4 @2xl:px-5 py-4 flex flex-wrap items-center gap-x-5 gap-y-3 mb-5">
      <div className="flex items-center gap-3 min-w-0 flex-1 basis-[220px]">
        <div className="relative shrink-0">
          <div className="tile h-10 w-10 rounded-[10px] grid place-items-center bg-t-violet text-ink"><ServerCog size={19} /></div>
          <span className={cx('absolute -bottom-0.5 -right-0.5 h-2.5 w-2.5 rounded-full ring-2 ring-[var(--edge)]', on ? 'bg-emerald-400' : s ? 'bg-zinc-600' : 'bg-zinc-500')} />
        </div>
        <div className="min-w-0">
          <div className="flex items-center gap-2 min-w-0"><span className="text-[15px] font-semibold">{L("中控")}</span>{s && <span className="font-mono text-[12.5px] text-fg2">{s.version}</span>}{s?.update_available && <Badge tone="violet">{L("可更新")}</Badge>}</div>
          <div className="text-[12.5px] text-muted truncate" title={s?.host.hostname}>{s ? `${on ? L("在线") : L("连不上")} · ${s.name || s.host.hostname}` : q.error ? L("连不上中控") : <Skeleton className="h-3 w-32 mt-1" />}</div>
        </div>
      </div>
      {s && <div className="grid grid-cols-3 gap-x-4 text-[12px] w-full @3xl:w-auto @3xl:min-w-[360px] [&>div]:min-w-0">
        {([['CPU', Math.round(s.cpu_pct)], [L("内存"), pct(s.mem_used_mb, s.mem_total_mb)], [L("硬盘"), pct(s.disk_used_gb, s.disk_total_gb)]] as const).map(([l, v]) => (
          <div key={l}><div className="flex justify-between text-muted mb-1"><span>{l}</span><span className="font-semibold text-fg tnum">{v}%</span></div><Ticks value={v} n={18} /></div>
        ))}
      </div>}
    </Link>
  )
}
