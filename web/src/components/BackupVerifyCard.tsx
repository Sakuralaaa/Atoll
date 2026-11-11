import { useState } from 'react'
import { CheckCircle2, ChevronDown, ChevronRight, FlaskConical, Loader2, MinusCircle, Play, ShieldCheck, XCircle } from 'lucide-react'
import { Badge, Button, Card } from './ui'
import { ErrorState, Skeleton } from './feedback'
import { run, useBusy } from '../api/hooks'
import { ago, bytes, dateTime } from '../api/view'
import { backupsApi, duration, shortSnap, useVerifyStatus } from '../api/backups'
import type { BackupPolicy, BackupVerification, Service } from '../api/types'
import { useT } from '../i18n'
import { useAdmin, useCan } from '../api/queries'

type T = (zh: string, en: string) => string

const tone = (st?: string): 'green' | 'amber' | 'red' | 'zinc' =>
  st === 'done' ? 'green' : st === 'running' ? 'amber' : st === 'failed' ? 'red' : 'zinc'

function statusLabel(t: T, v?: BackupVerification) {
  switch (v?.status) {
    case undefined: return t('从未运行', 'Never run')
    case 'done': return t('通过', 'Passed')
    case 'failed': return t('失败', 'Failed')
    case 'running': return t('进行中', 'Running')
    case 'skipped': return t('已跳过', 'Skipped')
  }
  return v?.status ?? ''
}

function partLabel(t: T, part: string) {
  if (part === 'check') return t('仓库', 'Repository')
  if (part === 'dump') return t('数据库 dump', 'Database dump')
  if (part === 'volumes') return t('卷快照', 'Volume snapshot')
  return part
}

/** "files=12 bytes=5242880" / "databases=2 tables=5" in words. */
function stepDetail(t: T, part: string, detail?: string, b?: number) {
  if (!detail) return ''
  const kv = Object.fromEntries(detail.split(/\s+/).map(x => x.split('=')).filter(x => x.length === 2))
  if (part === 'volumes' && kv.files !== undefined) return t(`恢复出 ${kv.files} 个文件，共 ${bytes(Number(kv.bytes) || 0)}`, `${kv.files} files restored, ${bytes(Number(kv.bytes) || 0)}`)
  if (part === 'dump') {
    const parts: string[] = []
    if (kv.databases !== undefined) parts.push(t(`${kv.databases} 个库`, `${kv.databases} databases`))
    if (kv.tables !== undefined) parts.push(t(`${kv.tables} 张表`, `${kv.tables} tables`))
    if (kv.collections !== undefined) parts.push(t(`${kv.collections} 个集合`, `${kv.collections} collections`))
    if (kv.keys !== undefined) parts.push(t(`${kv.keys} 个键`, `${kv.keys} keys`))
    if (parts.length) return t(`导入临时数据库并查询成功：${parts.join('，')}`, `Loaded into a temporary database and queried: ${parts.join(', ')}`) + (b ? ` · dump ${bytes(b)}` : '')
  }
  return detail
}

function Row({ t, title, desc, v, next, off, action }: {
  t: T; title: string; desc: string; v?: BackupVerification; next?: string; off: boolean; action: React.ReactNode
}) {
  const [open, setOpen] = useState(false)
  const hasMore = !!(v && (v.error || v.warnings?.length || v.steps.length))
  const Icon = v?.status === 'done' ? CheckCircle2 : v?.status === 'failed' ? XCircle : v?.status === 'running' ? Loader2 : MinusCircle
  return (
    <div className="py-4 first:pt-0 last:pb-0">
      <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
        <div className="flex-1 min-w-[min(100%,260px)]">
          <div className="flex flex-wrap items-center gap-2 text-[13px]">
            <Icon size={16} className={v?.status === 'done' ? 'text-ok' : v?.status === 'failed' ? 'text-danger' : v?.status === 'running' ? 'animate-spin text-accent' : 'text-dim'} />
            <span className="font-semibold">{title}</span>
            <Badge tone={tone(v?.status)}>{statusLabel(t, v)}</Badge>
            {v && <span className="text-[12.5px] text-muted">{dateTime(v.started_at)} · {ago(v.started_at)}{v.trigger === 'manual' ? t(' · 手动', ' · manual') : t(' · 定时', ' · scheduled')}{v.duration_ms ? ` · ${duration(v.duration_ms)}` : ''}</span>}
          </div>
          <div className="text-[12.5px] text-dim mt-1 leading-5">
            {desc}{' '}
            {off ? t('定时已关闭。', 'Schedule off.') : next ? t(`下次 ${dateTime(next)}。`, `Next ${dateTime(next)}.`) : ''}
          </div>
          {v?.error && !open && <div className="mt-1.5 font-mono text-[12px] text-danger break-all line-clamp-2">{v.error}</div>}
          {hasMore && (
            <button type="button" onClick={() => setOpen(!open)} className="mt-1.5 text-[12px] text-fg2 hover:text-fg inline-flex items-center gap-1">
              {open ? <ChevronDown size={13} /> : <ChevronRight size={13} />}{t('详情', 'Details')}
            </button>
          )}
        </div>
        <div className="shrink-0">{action}</div>
      </div>
      {open && v && (
        <div className="mt-2.5 space-y-1.5">
          {v.steps.map((s, i) => (
            <div key={i} className="rounded-lg field px-3.5 py-2.5 text-[12.5px] flex flex-wrap gap-x-3 gap-y-1 items-baseline">
              <Badge tone={tone(s.status)}>{partLabel(t, s.part)}</Badge>
              {s.snapshot_id && <span className="font-mono text-[12px] text-dim" title={s.snapshot_id}>{shortSnap(s.snapshot_id)}</span>}
              <span className={s.error ? 'font-mono text-[12px] text-danger break-all' : 'text-fg2'}>{s.error || stepDetail(t, s.part, s.detail, s.bytes)}</span>
            </div>
          ))}
          {v.error && <div className="rounded-lg field px-3.5 py-2.5 font-mono text-[12px] text-danger break-all">{v.error}</div>}
          {v.warnings?.map((w, i) => <div key={i} className="rounded-lg field px-3.5 py-2.5 font-mono text-[12px] text-fg2 break-all">⚠ {w}</div>)}
        </div>
      )}
    </div>
  )
}

/** Service → 备份还原: latest repository check and restore drill, with "check now" / "drill now". */
export default function BackupVerifyCard({ s, policy }: { s: Service; policy: BackupPolicy }) {
  const { t } = useT()
  const [poll, setPoll] = useState(false)
  const q = useVerifyStatus(s.id, poll)
  const st = q.data
  const running = st?.check?.status === 'running' || st?.drill?.status === 'running'
  if (running !== poll) setPoll(running)
  const [busy, wrap] = useBusy()
  const key = [`backup-verify:${s.id}`]
  const nothing = !policy.parts?.length
  const admin = useAdmin(), canDeploy = useCan('deploy') // the repository check is panel-wide (admin); a drill needs deploy
  const check = () => wrap(() => run(() => backupsApi.runCheck(), { ok: t('仓库校验已开始', 'Repository check started'), invalidate: key }))
  const drill = () => wrap(() => run(() => backupsApi.runDrill(s.id), { ok: t('恢复演练已开始', 'Restore drill started'), invalidate: key }))
  if (q.error && !st) return <div className="mt-6"><ErrorState error={q.error} onRetry={q.refetch} /></div>
  return (
    <Card className="mt-6 p-4 @2xl:p-6">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <ShieldCheck size={18} className="text-accent" />
        <span className="text-[15px] font-semibold tracking-tight">{t('备份校验', 'Backup verification')}</span>
      </div>
      <div className="text-[13px] text-muted mt-1.5 leading-5">
        {t('备份能不能真的恢复，要定期验证：每周用 restic check 抽查仓库数据，每月把最新快照恢复到临时目录和临时数据库里演练一次，结束后全部清理，不会碰正在运行的服务和数据。失败时会产生「备份校验失败」问题并通知。',
          'Backups are only as good as their restores: a weekly restic check reads a sample of the repository, and a monthly drill restores the newest snapshots into a temporary directory and a temporary database, then removes everything. The running service and its data are never touched. Failures raise a "backup verification failed" problem and notify you.')}
      </div>
      {!st ? <div className="mt-5 space-y-3"><Skeleton className="h-11 w-full" /><Skeleton className="h-11 w-full" /></div> : (
        <div className="mt-5 divide-y divide-line">
          <Row t={t} title={t('仓库校验', 'Repository check')} v={st.check} next={st.next_check_at} off={!st.check_schedule}
            desc={t(`每次读取约 ${st.check_subset} 的数据并校验，整个仓库共用、由一台服务器执行${st.check_server_id ? `（${st.check_server_id}）` : ''}。`,
              `Reads about ${st.check_subset} of the data each time; shared by the whole repository and run by one server${st.check_server_id ? ` (${st.check_server_id})` : ''}.`)}
            action={admin && <Button size="sm" disabled={busy || st.check?.status === 'running' || !st.repo_configured} onClick={check}>
              {st.check?.status === 'running' ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />}{t('立即校验', 'Check now')}</Button>} />
          <Row t={t} title={t('恢复演练', 'Restore drill')} v={st.drill} next={policy.enabled ? st.next_drill_at : undefined} off={!st.drill_schedule}
            desc={policy.enabled
              ? t('在服务所在服务器上恢复最新快照：卷需恢复出非空文件，数据库 dump 需导入同版本临时容器并能查询。', 'Restores the newest snapshots on the service\'s server: volumes must come back non-empty, dumps must load into a temporary container of the same version and answer a query.')
              : t('开启定时备份后每月自动演练；现在也可以手动演练。', 'Runs monthly once scheduled backups are on; you can also run one now.')}
            action={canDeploy && <Button size="sm" disabled={busy || st.drill?.status === 'running' || nothing || !st.repo_configured} onClick={drill}
              title={nothing ? t('这个服务没有可备份的内容', 'Nothing to back up for this service') : undefined}>
              {st.drill?.status === 'running' ? <Loader2 size={14} className="animate-spin" /> : <FlaskConical size={14} />}{t('立即演练', 'Drill now')}</Button>} />
        </div>
      )}
    </Card>
  )
}
