import { useState } from 'react'
import { ArrowUpCircle, ExternalLink, Loader2, RefreshCw, Rocket, TriangleAlert } from 'lucide-react'
import { Badge, Button, Card } from './ui'
import { Modal, Select, Skeleton, TextInput } from './feedback'
import { invalidate, run, useBusy } from '../api/hooks'
import { ago } from '../api/view'
import { shortDigest, updatesApi, useImageUpdate } from '../api/updates'
import type { ImageUpdate, ImageUpdatePolicyKind, Service } from '../api/types'
import { isGit } from '../api/git'
import { L, useT } from '../i18n'
import { useCan } from '../api/queries'
import { cx } from './ui-utils'

const STATUS: Record<string, [string, 'amber' | 'green' | 'red' | 'zinc']> = {
  update_available: [L("有更新"), 'amber'],
  up_to_date: [L("已是最新"), 'green'],
  error: [L("检查失败"), 'red'],
  unknown: [L("未检查"), 'zinc'],
  unsupported: [L("不支持"), 'zinc'],
  disabled: [L("已关闭"), 'zinc'],
  pinned: [L("已固定版本"), 'green'],
}

/** Small "有更新" badge for service lists and project cards. */
export function UpdateBadge({ u, compact }: { u?: ImageUpdate; compact?: boolean }) {
  if (!u?.update_available) return null
  const what = u.digest_changed ? L("{0} 有新镜像", u.current_tag ?? '') : u.newer_tag ? L("新版本 {0}", u.newer_tag) : L("有更新")
  return <span title={what}><Badge tone="amber"><ArrowUpCircle size={11} strokeWidth={2.5} />{compact ? L("更新") : L("有更新")}</Badge></span>
}

const WINDOW_RE = /^([01]\d|2[0-3]):[0-5]\d-([01]\d|2[0-3]):[0-5]\d$/

/** Update card on the service 服务状态 tab: running vs registry digest/tag, policy, 更新 button. Not for git builds. */
export default function ImageUpdateCard({ s }: { s: Service }) {
  if (isGit(s)) return <SourceBuildNote />
  return <RegistryUpdateCard s={s} />
}

/** Services built from source have no registry image to compare: pushes / "Rebuild latest" update them. */
function SourceBuildNote() {
  const { t } = useT()
  return (
    <Card className="mt-6 px-6 py-4 flex flex-wrap items-center gap-3">
      <ArrowUpCircle size={18} className="text-muted" />
      <span className="text-[15px] font-semibold tracking-tight">{t('镜像更新', 'Image updates')}</span>
      <Badge tone="zinc">{t('不适用', 'N/A')}</Badge>
      <span className="text-[12.5px] text-dim">{t('源码构建的服务不检查镜像仓库更新；推送代码或点「重新构建最新提交」即可更新。', 'Not applicable for source builds: push to the branch or use "Rebuild latest" to update.')}</span>
    </Card>
  )
}

function RegistryUpdateCard({ s }: { s: Service }) {
  const q = useImageUpdate(s.id)
  const u = q.data
  const [busy, wrap] = useBusy()
  const canDeploy = useCan('deploy') // check, policy and apply are deploy routes
  const [confirm, setConfirm] = useState<{ tag?: string } | undefined>()
  const [win, setWin] = useState<string | undefined>()
  const keys = [`image-update:${s.id}`, 'image-updates']
  const check = () => wrap(async () => { const r = await run(() => updatesApi.check(s.id), { invalidate: keys }); if (r) q.set(r) })
  const setPolicy = (policy: ImageUpdatePolicyKind, window = u?.window ?? '') =>
    wrap(async () => { const r = await run(() => updatesApi.setPolicy(s.id, { policy, window: policy === 'auto' ? window : '' }), { ok: L("更新策略已保存"), invalidate: keys }); if (r) { q.set(r); setWin(undefined) } })
  const winValue = win ?? u?.window ?? ''
  const winBad = winValue !== '' && !WINDOW_RE.test(winValue)

  if (!u) return <Card className="mt-6 p-6">{q.error ? <div className="text-[13px] text-muted">{L("无法读取镜像更新状态：")}{q.error.message}</div> : <Skeleton className="h-16 w-full" />}</Card>
  const [label, tone] = STATUS[u.status] ?? [u.status, 'zinc']
  const checkable = u.status !== 'unsupported' && u.status !== 'disabled'
  return (
    <Card className="mt-6 p-4 @2xl:p-6">
      <div className="flex flex-wrap items-center gap-3">
        <ArrowUpCircle size={18} className={u.update_available ? 'text-accent' : 'text-muted'} />
        <span className="text-[15px] font-semibold tracking-tight">{L("镜像更新")}</span>
        <Badge tone={tone}>{label}</Badge>
        <span className="text-[12.5px] text-dim">{u.checked_at ? L("检查于 {0}", ago(u.checked_at)) : L("尚未检查")}{u.next_check_at && u.status !== 'disabled' ? L(" · 下次 {0}", new Date(u.next_check_at).toLocaleString('zh-CN', { hour12: false, month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })) : ''}</span>
        {canDeploy && <div className="ml-auto flex flex-wrap items-center gap-2">
          <Select aria-label={L("更新策略")} className="h-9 w-[132px]" value={u.policy} disabled={busy} onChange={e => setPolicy(e.target.value as ImageUpdatePolicyKind)}>
            <option value="notify">{L("仅通知")}</option>
            <option value="auto">{L("自动更新")}</option>
            <option value="off">{L("关闭检查")}</option>
          </Select>
          <Button size="sm" disabled={busy || u.status === 'disabled' || u.status === 'unsupported'} onClick={check}>{busy ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />}{L("立即检查")}</Button>
        </div>}
      </div>

      {checkable && <div className="mt-5 grid grid-cols-[48px_minmax(0,1fr)_minmax(0,1fr)] @2xl:grid-cols-[100px_minmax(0,1fr)_minmax(0,1fr)] gap-y-3 gap-x-3 @2xl:gap-x-6 text-[13px]">
        <span />
        <span className="text-[12px] text-muted">{L("当前运行")}</span>
        <span className="text-[12px] text-muted">{L("仓库最新")}</span>
        {(u.current_version || u.latest_version) && <>
          <span className="text-muted">{L("版本")}</span>
          <span className="font-mono font-semibold truncate">{u.current_version || '—'}</span>
          <span className={cx('font-mono truncate', u.latest_version && u.latest_version !== u.current_version ? 'text-accent font-semibold' : 'text-fg2')}>{u.latest_version || '—'}</span>
        </>}
        <span className="text-muted">{L("标签")}</span>
        <span className="font-mono truncate" title={u.pinned ? s.image : u.current_tag}>{u.pinned ? L("已固定") : u.current_tag ?? '—'}</span>
        <span className="font-mono flex flex-wrap items-center gap-2 min-w-0">
          {u.newer_tag ? <><span className="text-accent font-semibold truncate max-w-full" title={u.newer_tag}>{u.newer_tag}</span>{canDeploy && <Button size="sm" disabled={busy} onClick={() => setConfirm({ tag: u.newer_tag })}>{L("更新到")} {u.newer_tag}</Button>}</> : <span className="text-fg2 truncate" title={u.current_tag}>{u.current_tag ?? '—'}</span>}
        </span>
        <span className="text-muted">{L("摘要")}</span>
        <span className="font-mono text-fg2 truncate" title={u.current_digest}>{shortDigest(u.current_digest)}</span>
        <span className={cx('font-mono truncate', u.digest_changed ? 'text-accent font-semibold' : 'text-fg2')} title={u.latest_digest}>{shortDigest(u.latest_digest)}</span>
        {u.major_tag && <>
          <span className="text-muted">{L("大版本")}</span>
          <span className="col-span-2 flex flex-wrap items-center gap-2 text-fg2 min-w-0"><TriangleAlert size={14} className="text-warn shrink-0" /><span className="font-mono font-semibold">{u.major_tag}</span><span className="text-muted">{L("主版本号变化，可能不兼容，请先看更新日志")}</span>
            {canDeploy && <Button size="sm" variant="ghost" disabled={busy} onClick={() => setConfirm({ tag: u.major_tag })}>{L("切换")}</Button>}</span>
        </>}
      </div>}

      {(u.error || u.note) && <div className={cx('mt-4 text-[12.5px] leading-5 [overflow-wrap:anywhere]', u.error ? 'text-danger' : 'text-muted')}>{u.error || u.note}</div>}

      {u.policy === 'auto' && checkable && canDeploy && <div className="mt-4 flex flex-wrap items-center gap-3 text-[13px]">
        <span className="text-muted">{L("自动更新时段")}</span>
        <TextInput mono className={cx('h-9 w-[150px]', winBad && 'ring-2 ring-danger')} placeholder={L("任意时间")} value={winValue}
          onChange={e => setWin(e.target.value.trim())} onKeyDown={e => e.key === 'Enter' && !winBad && setPolicy('auto', winValue)} />
        <span className="text-dim text-[12px]">{L("UTC，例如 18:00-21:00（北京时间 02:00-05:00）；留空表示随时。只自动更新同一标签的新摘要，不会自动换标签。")}</span>
        {win !== undefined && win !== (u.window ?? '') && <Button size="sm" disabled={busy || winBad} onClick={() => setPolicy('auto', winValue)}>{L("保存")}</Button>}
      </div>}

      <div className="mt-5 pt-4 border-t border-line flex flex-wrap items-center gap-3">
        {u.changelog_url && <a href={u.changelog_url} target="_blank" rel="noreferrer" className="text-[13px] text-fg2 hover:text-fg flex items-center gap-1.5 hover:underline">{L("更新日志")}<ExternalLink size={13} /></a>}
        {u.pinned
          ? <span className="text-[12.5px] text-dim">{L("已固定，不提示也不自动更新。")}</span>
          : <span className="text-[12.5px] text-dim">{L("旧部署保留在部署记录里，可回滚。")}</span>}
        {canDeploy && <Button className="ml-auto" variant={u.pinned ? 'secondary' : 'primary'} size="sm" disabled={busy || (u.pinned ? !u.latest_digest : !u.digest_changed)} onClick={() => setConfirm({})}><Rocket size={14} />{u.pinned ? L("改回跟随 {0}", u.current_tag ?? 'latest') : L("更新")}</Button>}
      </div>

      {confirm && <Modal title={<><Rocket size={17} />{L("更新")} {s.name}</>} onClose={() => setConfirm(undefined)}
        footer={<><Button size="sm" onClick={() => setConfirm(undefined)}>{L("取消")}</Button><Button size="sm" variant="primary" disabled={busy} onClick={() => wrap(async () => {
          const d = await run(() => updatesApi.apply(s.id, confirm.tag), { ok: L("正在更新镜像"), invalidate: [...keys, `deployments:${s.id}`, `project:${s.project_id}`, 'projects'] })
          if (d) { setConfirm(undefined); invalidate(...keys) }
        })}>{busy && <Loader2 size={14} className="animate-spin" />}{L("确认更新")}</Button></>}>
        <div className="text-[13.5px] text-fg2 leading-6 [overflow-wrap:anywhere]">
          {confirm.tag
            ? <>{L("将")} <span className="font-mono font-semibold">{s.name}</span> {L("的镜像标签从")} <span className="font-mono">{u.current_tag}</span> {L("切换到")} <span className="font-mono font-semibold">{confirm.tag}</span>{L("，并按该标签当前的摘要重新部署。")}</>
            : u.pinned
            ? <>{L("取消固定，")}<span className="font-mono font-semibold">{s.name}</span> {L("改回跟随")} <span className="font-mono">{u.current_tag}</span>{L("，并部署它现在的最新镜像")}{u.latest_version ? <>{L("（")}<span className="font-mono font-semibold">{u.latest_version}</span>{L("）")}</> : null}{L("。以后会照常提示更新。")}</>
            : <>{L("按")} <span className="font-mono">{u.current_tag}</span> {L("在仓库中的最新摘要")} <span className="font-mono font-semibold">{shortDigest(u.latest_digest)}</span> {L("重新部署")} <span className="font-mono font-semibold">{s.name}</span>{L("（当前")} {shortDigest(u.current_digest)}{L("）。")}</>}
          <div className="mt-3 text-muted text-[13px]">{L("容器会被重建，期间服务短暂不可用；出现问题可在「部署记录」中回滚。")}</div>
        </div>
      </Modal>}
    </Card>
  )
}
