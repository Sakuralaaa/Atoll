import { useState } from 'react'
import { CheckCircle2, Circle, DatabaseZap, Loader2, MinusCircle, RotateCcw, Trash2, XCircle } from 'lucide-react'
import { Button } from './ui'
import { ConfirmDialog, Modal } from './feedback'
import { invalidate, run, useBusy } from '../api/hooks'
import { ago } from '../api/view'
import { dbUpgradeApi, DB_UPGRADE_KINDS, useDBUpgrade, type DBUpgrade } from '../api/dbupgrade'
import { splitImage } from '../api/updates'
import type { Service } from '../api/types'
import { L } from '../i18n'
import { useCan } from '../api/queries'
import { cx } from './ui-utils'

const tagOf = (image: string) => splitImage(image).tag ?? 'latest'
const keys = (s: Service) => [`db-upgrade:${s.id}`, `image-update:${s.id}`, 'image-updates', `deployments:${s.id}`, `project:${s.project_id}`, 'projects']

/** "一键升级" for a major tag of a database service (replaces the plain tag switch). */
export function DBUpgradeButton({ s, tag, current }: { s: Service; tag: string; current?: string }) {
  const q = useDBUpgrade(s.id)
  const [open, setOpen] = useState(false)
  const [busy, wrap] = useBusy()
  const info = q.data
  const running = info?.last?.status === 'running'
  const kept = !!info?.last?.old_kept
  const why = !info ? '' : !info.supported ? info.reason : running ? L("正在升级") : kept ? L("请先删除上次升级保留的旧数据，或回到旧版本") : ''
  return <>
    <Button size="sm" variant="secondary" disabled={!info || !!why || busy} title={why || undefined} onClick={() => setOpen(true)}><DatabaseZap size={14} />{L("一键升级")}</Button>
    {open && <Modal title={<><DatabaseZap size={17} />{L("升级 PostgreSQL 到 {0}", tag)}</>} onClose={() => setOpen(false)}
      footer={<><Button size="sm" onClick={() => setOpen(false)}>{L("取消")}</Button><Button size="sm" variant="primary" disabled={busy} onClick={() => wrap(async () => {
        const r = await run(() => dbUpgradeApi.start(s.id, tag), { ok: L("已开始升级"), invalidate: keys(s) })
        if (r) setOpen(false)
      })}>{busy && <Loader2 size={14} className="animate-spin" />}{L("开始升级")}</Button></>}>
      <div className="text-[13.5px] text-fg2 leading-6 [overflow-wrap:anywhere]">
        <p>{L("PostgreSQL 大版本之间数据格式不同，不能直接换标签。一键升级会：")}</p>
        <ol className="mt-2 ml-5 list-decimal space-y-1">
          <li>{L("停止 {0}，用当前版本 {1} 导出全部数据库（含用户和权限）", s.name, current ?? '')}</li>
          <li>{L("用 {0} 新建数据目录并导入，核对数据库和表的数量一致", tag)}</li>
          <li>{L("部署 {0}；新版本起不来时自动回到 {1} 和原来的数据", tag, current ?? '')}</li>
        </ol>
        <div className="mt-3 text-muted text-[13px]">{L("升级期间服务不可用，耗时取决于数据量；需要约两倍于当前数据的空闲磁盘。原来的数据保留在服务器上，删除前随时可以回到旧版本（之后写入的数据会丢失）。")}</div>
      </div>
    </Modal>}
  </>
}

const STEP: Record<string, string> = { convert: L("导出并用新版本导入、核对"), deploy: L("部署新版本") }

function StepIcon({ st }: { st: string }) {
  if (st === 'running') return <Loader2 size={15} className="animate-spin text-accent shrink-0" />
  if (st === 'done') return <CheckCircle2 size={15} className="text-ok shrink-0" />
  if (st === 'failed') return <XCircle size={15} className="text-danger shrink-0" />
  if (st === 'skipped') return <MinusCircle size={15} className="text-dim shrink-0" />
  return <Circle size={15} className="text-dim shrink-0" />
}

function title(u: DBUpgrade): string {
  switch (u.status) {
    case 'running': return L("正在升级到 {0}", tagOf(u.to_image))
    case 'done': return L("已升级到 {0}", tagOf(u.to_image))
    case 'rolled_back': return L("已回到 {0}", tagOf(u.from_image))
    default: return u.old_kept ? L("升级未完成") : L("升级失败，数据没有改动")
  }
}

/** Progress and outcome of the latest upgrade, with rollback / delete-old-data while the old data is kept. */
export function DBUpgradeStatus({ s }: { s: Service }) {
  const db = DB_UPGRADE_KINDS.has(s.kind)
  const q = useDBUpgrade(db ? s.id : null, s.project_id)
  const canDeploy = useCan('deploy')
  const [ask, setAsk] = useState<'rollback' | 'cleanup'>()
  const [now] = useState(() => Date.now())
  const u = q.data?.last
  if (!u) return null
  const recent = now - new Date(u.finished_at ?? u.started_at).getTime() < 3 * 864e5
  if (u.status !== 'running' && !u.old_kept && !recent) return null
  const tone = u.status === 'running' ? 'border-accent/40' : u.status === 'done' ? 'border-ok/40' : 'border-warn/50'
  return (
    <div className={cx('mt-5 rounded-lg border bg-bg/50 px-4 py-3.5', tone)}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <DatabaseZap size={16} className="text-muted shrink-0" />
        <span className="text-[14px] font-semibold">{title(u)}</span>
        <span className="text-[12.5px] text-dim font-mono">{tagOf(u.from_image)} → {tagOf(u.to_image)}</span>
        <span className="text-[12px] text-dim ml-auto">{u.finished_at ? ago(u.finished_at) : L("开始于 {0}", ago(u.started_at))}</span>
      </div>
      <div className="mt-3 space-y-1.5">
        {u.steps.map(st => (
          <div key={st.name} className="flex items-start gap-2 text-[13px] min-w-0">
            <span className="mt-[3px]"><StepIcon st={st.status} /></span>
            <span className={cx(st.status === 'pending' || st.status === 'skipped' ? 'text-dim' : 'text-fg2')}>{STEP[st.name] ?? st.name}</span>
            {st.detail && <span className="text-dim font-mono text-[12px] mt-px [overflow-wrap:anywhere] min-w-0">{st.detail}</span>}
          </div>
        ))}
      </div>
      {u.error && <div className={cx('mt-3 text-[12.5px] leading-5 [overflow-wrap:anywhere]', u.status === 'rolled_back' ? 'text-muted' : 'text-danger')}>{u.error}</div>}
      {u.old_kept && u.status !== 'running' && <div className="mt-3 pt-3 border-t border-line flex flex-wrap items-center gap-2">
        <span className="text-[12.5px] text-muted mr-auto">{L("升级前的数据还保留在服务器上。")}</span>
        {canDeploy && <>
          <Button size="sm" onClick={() => setAsk('rollback')}><RotateCcw size={14} />{L("回到 {0}", tagOf(u.from_image))}</Button>
          <Button size="sm" variant="ghost" onClick={() => setAsk('cleanup')}><Trash2 size={14} />{L("删除旧数据")}</Button>
        </>}
      </div>}
      {ask === 'rollback' && <ConfirmDialog title={L("回到 {0}", tagOf(u.from_image))} name={s.name} action={L("回到旧版本")} onClose={() => setAsk(undefined)}
        description={<>{L("将停止")} <b>{s.name}</b>{L("，换回升级前的数据，并部署")} <span className="font-mono">{tagOf(u.from_image)}</span>{L("。升级之后写入的数据会丢失。")}</>}
        onConfirm={() => run(() => dbUpgradeApi.rollback(s.id), { ok: L("已回到旧版本"), invalidate: keys(s) })} />}
      {ask === 'cleanup' && <ConfirmDialog title={L("删除升级前的数据")} action={L("删除")} onClose={() => setAsk(undefined)}
        description={<>{L("删除后无法再回到")} <span className="font-mono">{tagOf(u.from_image)}</span>{L("，备份不受影响。")}</>}
        onConfirm={() => run(() => dbUpgradeApi.cleanup(s.id), { ok: L("已删除旧数据"), invalidate: [`db-upgrade:${s.id}`] }).then(() => invalidate(`db-upgrade:${s.id}`))} />}
    </div>
  )
}
