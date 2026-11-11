import { L } from '../i18n'
import { ScrollRows } from './ScrollRows'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, ArchiveRestore, CalendarClock, CheckCircle2, Database, HardDrive, Loader2, Play, RefreshCw, RotateCcw, Save, Settings as Cog, XCircle } from 'lucide-react'
import { Badge, Button, Card, CopyId } from './ui'
import { ConfirmDialog, Empty, ErrorState, Modal, RowsSkeleton, Select, Skeleton, TextInput, Toggle } from './feedback'
import { api, isConfirmation } from '../api/client'
import { invalidate, run, useBusy, useQuery } from '../api/hooks'
import { ago, bytes, dateTime, projName } from '../api/view'
import { backupsApi, duration, shortSnap, useBackupPolicy, useBackups, useRestores, type BackupPolicyInput } from '../api/backups'
import type { Backup, BackupPolicy, Project, RestoreJob, Server, Service } from '../api/types'
import { cx } from './ui-utils'
import { useNow } from '../api/useNow'
import BackupVerifyCard from './BackupVerifyCard'
import { useCan } from '../api/queries'

const PRESETS = [
  { l: L("每天 03:00"), v: '0 3 * * *' },
  { l: L("每 6 小时"), v: '0 */6 * * *' },
  { l: L("每周（周日 03:00）"), v: '0 3 * * 0' },
]
const ZONES = ['Asia/Shanghai', 'Asia/Tokyo', 'Asia/Singapore', 'Asia/Hong_Kong', 'Europe/Berlin', 'Europe/London', 'America/Los_Angeles', 'America/New_York', 'UTC']
const CRON_RE = /^\S+(\s+\S+){4}$/
const STATUS: Record<string, [string, 'green' | 'amber' | 'red' | 'zinc']> = { done: [L("成功"), 'green'], running: [L("进行中"), 'amber'], failed: [L("失败"), 'red'], pruned: [L("已清理"), 'zinc'] }
const TRIGGER: Record<string, string> = { schedule: L("定时"), manual: L("手动"), catchup: L("补跑"), imported: L("仓库导入") }
const PART: Record<string, string> = { dump: 'dump', volumes: 'volume' }
const SNAP_KEY = (sid: string) => [`backups:${sid}`, `backup-policy:${sid}`]

/** Service → 备份还原 tab. */
export default function BackupPanel({ p, s }: { p: Project; s: Service }) {
  const pol = useBackupPolicy(s.id)
  const canSettings = useCan('settings') // never granted to members: they cannot open panel settings
  if (pol.error && !pol.data) return <ErrorState error={pol.error} onRetry={pol.refetch} />
  if (!pol.data) return <Card className="p-6 space-y-4"><Skeleton className="h-6 w-48" /><Skeleton className="h-11 w-full" /><Skeleton className="h-11 w-full" /></Card>
  if (!pol.data.repo_configured) return (
    <Empty icon={<ArchiveRestore size={22} />} title={L("还没有配置备份存储")}
      desc={L("备份由每台服务器上的 Agent 用 restic 直接加密上传到你的 S3 兼容存储（Cloudflare R2、Backblaze B2、AWS S3、MinIO），数据不经过中控。先在设置里填好存储桶，再回来开启这个服务的备份。")}
      action={canSettings && <Link to="/settings#backup"><Button variant="primary"><Cog size={15} />{L("前往设置 · 备份存储")}</Button></Link>} />
  )
  return <Configured p={p} s={s} policy={pol.data} onPolicy={pol.set} />
}

function Configured({ p, s, policy, onPolicy }: { p: Project; s: Service; policy: BackupPolicy; onPolicy: (v: BackupPolicy) => void }) {
  const [anyRunning, setAnyRunning] = useState(false)
  const bq = useBackups(s.id, anyRunning)
  const list = bq.data ?? []
  const running = list.some(b => b.status === 'running')
  if (running !== anyRunning) setAnyRunning(running)
  const [busy, wrap] = useBusy()
  const [restore, setRestore] = useState<Backup | undefined>()
  // members (notes/subaccounts.md): run/policy need deploy, restore needs restore
  const canDeploy = useCan('deploy'), canRestore = useCan('restore')
  const nothing = !policy.parts?.length
  const runNow = () => wrap(() => run(() => backupsApi.run(s.id), { ok: L("备份已开始，Agent 正在上传"), invalidate: SNAP_KEY(s.id) }))
  const refresh = () => wrap(async () => { const r = await run(() => backupsApi.list(s.id, true), { ok: L("已与存储桶同步") }); if (r) bq.set(r) })
  return (
    <>
      <PolicyForm key={policy.updated_at ?? 'default'} s={s} policy={policy} onSaved={onPolicy} />
      <BackupVerifyCard s={s} policy={policy} />
      <RestoreStatus s={s} />
      <div className="mt-8 flex flex-wrap items-end justify-between gap-4 mb-3">
        <div className="flex-1 min-w-[min(100%,260px)]">
          <div className="text-[15px] font-semibold flex items-center gap-2"><ArchiveRestore size={17} />{L("快照")}</div>
          <div className="text-[13px] text-muted mt-1">{L("每次备份产生一个或多个 restic 快照：数据库是逻辑 dump，其余是卷目录。重复数据只上传一次。")}</div>
        </div>
        <div className="flex gap-2 shrink-0">
          <Button size="sm" disabled={busy} onClick={refresh} title={L("从存储桶重新读取快照列表")}><RefreshCw size={14} />{L("同步")}</Button>
          {canDeploy && <Button variant="primary" disabled={busy || running || nothing} onClick={runNow} title={nothing ? L("这个服务没有卷，也不是数据库模板，没有可备份的内容") : undefined}>
            {running || busy ? <Loader2 size={15} className="animate-spin" /> : <Play size={15} />}{running ? L("备份中…") : L("立即备份")}
          </Button>}
        </div>
      </div>
      {bq.error && !bq.data ? <ErrorState error={bq.error} onRetry={bq.refetch} />
        : !bq.data ? <RowsSkeleton n={4} />
          : !list.length ? <Empty icon={<ArchiveRestore size={22} />} title={L("还没有备份")} desc={nothing ? L("这个服务没有挂载卷，也不是数据库模板，没有需要备份的数据。") : policy.enabled ? L("第一次定时备份会在 {0} 运行，也可以现在手动备份。", policy.next_run_at ? dateTime(policy.next_run_at) : '下个计划时间') : L("开启上面的定时备份，或点「立即备份」手动备份一次。")} />
            : <SnapshotTable list={list} onRestore={canRestore ? setRestore : undefined} />}
      {restore && <RestoreFlow p={p} s={s} list={list} initial={restore} onClose={() => setRestore(undefined)} />}
    </>
  )
}

// ---------- policy ----------
function PolicyForm({ s, policy, onSaved }: { s: Service; policy: BackupPolicy; onSaved: (p: BackupPolicy) => void }) {
  const init: BackupPolicyInput = { enabled: policy.enabled, schedule: policy.schedule || '0 3 * * *', timezone: policy.timezone || 'Asia/Shanghai', keep: { ...policy.keep }, include_volumes: policy.include_volumes }
  const [v, setV] = useState(init)
  const [busy, wrap] = useBusy()
  const canDeploy = useCan('deploy')
  const isDb = ['postgres', 'mysql', 'redis', 'mongo'].includes(s.kind)
  const cronOk = CRON_RE.test(v.schedule.trim())
  const keepOk = [v.keep.daily, v.keep.weekly, v.keep.monthly].every(n => Number.isInteger(n) && n >= 0 && n <= 1000) && v.keep.daily + v.keep.weekly + v.keep.monthly > 0
  const dirty = JSON.stringify(v) !== JSON.stringify(init)
  const preset = PRESETS.find(x => x.v === v.schedule.trim())
  const setKeep = (k: keyof BackupPolicyInput['keep']) => (e: React.ChangeEvent<HTMLInputElement>) => setV(x => ({ ...x, keep: { ...x.keep, [k]: e.target.value === '' ? 0 : Math.floor(Number(e.target.value)) } }))
  const save = () => wrap(async () => {
    const r = await run(() => backupsApi.setPolicy(s.id, { ...v, schedule: v.schedule.trim(), timezone: v.timezone.trim() }), { ok: L("备份策略已保存") })
    if (r) onSaved(r)
  })
  return (
    <Card className="p-4 @2xl:p-6">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <CalendarClock size={18} className={policy.enabled ? 'text-accent' : 'text-muted'} />
        <span className="text-[15px] font-semibold tracking-tight">{L("定时备份")}</span>
        <Badge tone={policy.enabled ? 'green' : 'zinc'}>{policy.enabled ? L("已开启") : L("未开启")}</Badge>
        {policy.enabled && policy.next_run_at && <span className="text-[12.5px] text-dim">{L("下次")} {dateTime(policy.next_run_at)}</span>}
        <span className="ml-auto flex items-center gap-1.5">{(policy.parts ?? []).map(x => <PartBadge key={x} part={x} />)}</span>
      </div>
      <div className="text-[13px] text-muted mt-1.5 leading-5">{L("计划由服务所在服务器的 Agent 自己执行，中控离线时也照常备份。")}</div>
      <fieldset disabled={!canDeploy} className="contents">
      <div className="mt-5 grid grid-cols-1 @2xl:grid-cols-[150px_minmax(0,1fr)] gap-x-6 gap-y-2 @2xl:gap-y-5 items-center text-[13px] [&>span]:@max-2xl:mt-3 [&>*]:min-w-0">
        <span className="font-semibold">{L("启用")}</span>
        <div className="flex items-center gap-3 min-w-0"><Toggle on={v.enabled} onChange={on => setV(x => ({ ...x, enabled: on }))} /><span className="text-muted">{v.enabled ? L("按计划自动备份") : L("只在手动点击时备份")}</span></div>

        <span className="font-semibold self-start @2xl:mt-2.5">{L("计划（cron）")}</span>
        <div>
          <div className="flex flex-wrap items-center gap-2">
            {PRESETS.map(x => (
              <button key={x.v} type="button" onClick={() => setV(y => ({ ...y, schedule: x.v }))}
                className={cx('h-9 px-3 rounded-lg text-[12.5px] font-semibold border-2 transition-all', preset?.v === x.v ? 'nav-active' : 'border-[var(--edge)] bg-card text-fg2 hover:bg-ov/5')}>{x.l}</button>
            ))}
            <TextInput mono aria-label={L("cron 表达式")} className="w-full max-w-[180px]" value={v.schedule} onChange={e => setV(x => ({ ...x, schedule: e.target.value }))} placeholder="0 3 * * *" />
          </div>
          <div className={cx('text-[12px] mt-1.5', cronOk ? 'text-dim' : 'text-danger')}>{cronOk ? L("5 段：分 时 日 月 周。例如 30 2 * * * 表示每天 02:30") : L("需要 5 段 cron 表达式，例如 0 3 * * *")}</div>
        </div>

        <span className="font-semibold">{L("时区")}</span>
        <div className="flex flex-wrap items-center gap-3">
          <TextInput mono list="backup-tz" className="w-full max-w-[240px]" value={v.timezone} onChange={e => setV(x => ({ ...x, timezone: e.target.value }))} placeholder="Asia/Shanghai" />
          <datalist id="backup-tz">{ZONES.map(z => <option key={z} value={z} />)}</datalist>
          <span className="text-[12px] text-dim">{L("计划按这个 IANA 时区解读")}</span>
        </div>

        <span className="font-semibold self-start @2xl:mt-2.5">{L("保留")}</span>
        <div>
          <div className="flex flex-wrap items-center gap-4">
            {([['daily', L("每日")], ['weekly', L("每周")], ['monthly', L("每月")]] as const).map(([k, l]) => (
              <label key={k} className="flex items-center gap-2"><span className="text-fg2">{l}</span>
                <TextInput mono type="number" min={0} max={1000} className="w-[84px]" value={String(v.keep[k])} onChange={setKeep(k)} /></label>
            ))}
          </div>
          <div className={cx('text-[12px] mt-1.5', keepOk ? 'text-dim' : 'text-danger')}>{keepOk ? L("restic forget：每天、每周、每月各保留最近一份，超出的自动清理") : L("至少保留一份，每项 0–1000")}</div>
        </div>

        {isDb && <>
          <span className="font-semibold">{L("同时备份卷")}</span>
          <div className="flex items-center gap-3 min-w-0"><Toggle on={v.include_volumes} onChange={on => setV(x => ({ ...x, include_volumes: on }))} /><span className="text-muted">{L("数据库总是先做逻辑 dump；开启后再额外快照数据目录")}</span></div>
        </>}
      </div>
      </fieldset>
      {canDeploy && <div className="mt-6 pt-5 border-t-2 border-dashed border-line flex flex-wrap items-center justify-end gap-3">
        {dirty && <span className="text-[12.5px] text-muted">{L("有未保存的修改")}</span>}
        {dirty && <Button size="sm" onClick={() => setV(init)}>{L("还原修改")}</Button>}
        <Button size="sm" variant="primary" disabled={!dirty || !cronOk || !keepOk || !v.timezone.trim() || busy} onClick={save}>{busy ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}{L("保存策略")}</Button>
      </div>}
    </Card>
  )
}

const PartBadge = ({ part }: { part: string }) => <Badge tone={part === 'dump' ? 'violet' : 'zinc'}>{part === 'dump' ? <Database size={11} /> : <HardDrive size={11} />}{PART[part] ?? part}</Badge>

// ---------- snapshots ----------
function SnapshotTable({ list, onRestore }: { list: Backup[]; onRestore?: (b: Backup) => void }) {
  const [open, setOpen] = useState<string | undefined>()
  const live = (b: Backup) => b.snapshots.filter(x => !x.pruned)
  return (
    <Card className="overflow-x-auto"><div className="min-w-[960px]">
      <div className="grid grid-cols-[170px_150px_150px_100px_110px_minmax(0,1fr)_96px] gap-4 px-6 h-11 items-center text-[12px] font-semibold text-muted border-b-2 border-[var(--edge)] bg-ov/5">
        <span>{L("时间")}</span><span>{L("类型")}</span><span>{L("大小")}</span><span>{L("耗时")}</span><span>{L("状态")}</span><span>{L("快照 ID")}</span><span />
      </div>
      <ScrollRows visible={8} testId="backup-list">
        {list.map(b => {
          const [label, tone] = STATUS[b.status] ?? [b.status, 'zinc']
          const restorable = b.status === 'done' && live(b).length > 0
          const parts = [...new Set(b.snapshots.map(x => x.part))]
          return (
            <div key={b.id}>
              <div className="grid grid-cols-[170px_150px_150px_100px_110px_minmax(0,1fr)_96px] gap-4 px-6 min-h-14 py-2.5 items-center text-[13px]">
                <div><div className="font-semibold font-mono text-[12.5px]">{dateTime(b.started_at)}</div><div className="text-[11.5px] text-dim">{TRIGGER[b.trigger] ?? b.trigger} · {ago(b.started_at)}</div></div>
                <div className="flex flex-wrap gap-1.5">{parts.length ? parts.map(x => <PartBadge key={x} part={x} />) : <span className="text-dim">—</span>}</div>
                <div>{b.bytes ? <><div className="font-mono tnum">{bytes(b.bytes)}</div><div className="text-[11.5px] text-dim">{L("新增")} {bytes(b.added)}</div></> : <span className="text-dim">—</span>}</div>
                <div className="font-mono tnum text-fg2">{b.status === 'running' ? '…' : duration(b.duration_ms)}</div>
                <div>
                  <button type="button" disabled={!b.error && !b.warnings?.length} onClick={() => setOpen(open === b.id ? undefined : b.id)} className="disabled:cursor-default">
                    <Badge tone={tone}>{b.status === 'running' && <Loader2 size={11} className="animate-spin" />}{label}{b.warnings?.length ? L(" · 警告") : ''}</Badge>
                  </button>
                </div>
                <div className="flex flex-wrap gap-x-3 gap-y-1 min-w-0">
                  {b.snapshots.length ? b.snapshots.map(x => (
                    <button key={x.id} type="button" onClick={() => navigator.clipboard?.writeText(x.id)} title={L("{0} · {1}{2} · 点击复制完整 ID", PART[x.part] ?? x.part, x.id, x.pruned ? L('（已被保留策略清理）') : '')}
                      className={cx('font-mono text-[12px] text-fg2 hover:text-fg', x.pruned && 'line-through opacity-50')}>{shortSnap(x.id)}</button>
                  )) : <span className="text-dim">—</span>}
                </div>
                <div className="flex justify-end">
                  {onRestore && <Button size="sm" disabled={!restorable} onClick={() => onRestore(b)} title={restorable ? undefined : L("只有成功且快照仍在仓库中的备份可以还原")}><RotateCcw size={13} />{L("还原")}</Button>}
                </div>
              </div>
              {open === b.id && (b.error || b.warnings?.length) && (
                <div className="px-6 pb-4 -mt-1 space-y-1.5">
                  {b.error && <div className="rounded-lg field px-3.5 py-2.5 font-mono text-[12px] text-danger break-all">{b.error}</div>}
                  {b.warnings?.map((w, i) => <div key={i} className="rounded-lg field px-3.5 py-2.5 font-mono text-[12px] text-fg2 break-all">⚠ {w}</div>)}
                </div>
              )}
            </div>
          )
        })}
      </ScrollRows>
    </div></Card>
  )
}

// ---------- restore ----------
function RestoreFlow({ p, s, list, initial, onClose }: { p: Project; s: Service; list: Backup[]; initial: Backup; onClose: () => void }) {
  const servers = useQuery('servers', api.servers).data
  const choices = list.filter(b => b.status === 'done' && b.snapshots.some(x => !x.pruned))
  const [bid, setBid] = useState(initial.id)
  const origin = servers?.find(x => x.id === p.server_id)
  const originOnline = origin?.status === 'online'
  const [target, setTarget] = useState(p.server_id)
  const [step, setStep] = useState<'pick' | 'confirm'>('pick')
  const b = choices.find(x => x.id === bid) ?? initial
  const tgt = servers?.find(x => x.id === target)
  const moving = target !== p.server_id
  const targetOk = !!tgt && tgt.status === 'online' && (!moving || !originOnline)
  const opt = (x: Server) => {
    const self = x.id === p.server_id
    const off = x.status !== 'online'
    const blocked = !self && originOnline
    return { disabled: off || blocked, label: `${x.name}${self ? L("（原服务器 · 原地还原）") : ''}${off ? L(" · 离线") : blocked ? L(" · 原服务器在线时不可选") : ''}` }
  }
  const go = async () => {
    const r = await run(() => backupsApi.restore(b.id, moving ? { target_server_id: target } : {}), { invalidate: [`restores:${s.id}`, `project:${p.id}`, 'projects'] })
    if (r && !isConfirmation(r)) invalidate(`restores:${s.id}`)
  }
  if (step === 'confirm') return (
    <ConfirmDialog title={L("还原备份")} name={s.name} action={L("覆盖并还原")} onClose={onClose} onConfirm={go}
      description={<>
        <b className="text-fg">{s.name}</b> {L("现在的数据会被")} <b className="text-fg">{dateTime(b.started_at)}</b> {L("的备份")}<b className="text-fg">{L("完全替换")}</b>{L("，之后写入的数据会丢失。还原期间服务会停止。")}
        {moving && <div className="mt-3 rounded-lg field px-3.5 py-3 text-[13px] flex gap-2.5"><AlertTriangle size={16} className="text-warn shrink-0 mt-0.5" /><span>{L("整个项目「")}{projName(p)}{L("」会迁到")} <b className="text-fg">{tgt?.name}</b>{L("：在那里按已保存的配置重建所有服务，再还原这份数据，并把域名 DNS 切过去。")}</span></div>}
      </>} />
  )
  return (
    <Modal title={<><RotateCcw size={17} />{L("还原")} {s.name}</>} width={620} onClose={onClose}
      footer={<><Button size="sm" onClick={onClose}>{L("取消")}</Button><Button size="sm" variant="primary" disabled={!targetOk} onClick={() => setStep('confirm')}>{L("下一步")}</Button></>}>
      <div className="text-[13px] font-semibold mb-1.5">{L("选择快照")}</div>
      <Select className="w-full font-mono" value={bid} onChange={e => setBid(e.target.value)}>
        {choices.map(x => <option key={x.id} value={x.id}>{dateTime(x.started_at)} · {[...new Set(x.snapshots.filter(y => !y.pruned).map(y => PART[y.part] ?? y.part))].join(' + ')} · {bytes(x.bytes)} · {x.snapshots.filter(y => !y.pruned).map(y => shortSnap(y.id)).join(', ')}</option>)}
      </Select>
      <div className="text-[13px] font-semibold mt-5 mb-1.5">{L("还原到")}</div>
      {!servers ? <Skeleton className="h-11 w-full" /> : (
        <Select className="w-full" value={target} onChange={e => setTarget(e.target.value)}>
          {servers.map(x => { const o = opt(x); return <option key={x.id} value={x.id} disabled={o.disabled}>{o.label}</option> })}
        </Select>
      )}
      <div className="mt-3 rounded-lg field px-3.5 py-3 text-[12.5px] text-fg2 leading-5">
        {originOnline
          ? <>{L("原服务器")} <b className="text-fg">{origin?.name}</b> {L("在线，只能原地还原。还原到其他服务器是给原服务器已经不可用时的灾难恢复；想换服务器请在项目设置里用「迁移」。")}</>
          : origin
            ? <>{L("原服务器")} <b className="text-fg">{origin.name}</b> {L("离线，可以选择另一台在线服务器：整个项目会在那里重建并还原这份数据，DNS 随之切换。")}</>
            : <>{L("正在读取服务器状态…")}</>}
      </div>
      {tgt && tgt.status !== 'online' && <div className="text-[12px] text-danger mt-2">{L("目标服务器离线，无法还原")}</div>}
    </Modal>
  )
}

function RestoreStatus({ s }: { s: Service }) {
  const [poll, setPoll] = useState(false)
  const q = useRestores(s.id, poll)
  const now = useNow(60000)
  const j: RestoreJob | undefined = q.data?.[0]
  const active = j?.status === 'running'
  if (active !== poll) setPoll(active)
  if (!j) return null
  const recent = active || (j.finished_at && now - new Date(j.finished_at).getTime() < 24 * 3600e3)
  if (!recent) return null
  return (
    <Card className={cx('mt-6 px-4 @2xl:px-6 py-4', j.status === 'failed' && '!bg-t-red text-ink')}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px]">
        {active ? <Loader2 size={16} className="animate-spin text-accent" /> : j.status === 'done' ? <CheckCircle2 size={16} className="text-ok" /> : <XCircle size={16} />}
        <span className="font-semibold">{active ? L("正在还原") : j.status === 'done' ? L("还原完成") : L("还原失败")}</span>
        <span className={j.status === 'failed' ? '' : 'text-muted'}>{ago(j.created_at)} {L("开始")}{j.to_server_id !== j.from_server_id ? L(" · 跨服务器灾难恢复") : ''}</span>
        <span className="ml-auto"><CopyId v={j.id} /></span>
      </div>
      <div className="mt-3 flex flex-wrap gap-2">
        {j.steps.map(st => <Badge key={st.name} tone={st.status === 'done' ? 'green' : st.status === 'running' ? 'amber' : st.status === 'failed' ? 'red' : 'zinc'}>{st.status === 'running' && <Loader2 size={11} className="animate-spin" />}{st.name}</Badge>)}
      </div>
      {j.error && <div className="mt-2 font-mono text-[12px] break-all">{j.error}</div>}
    </Card>
  )
}
