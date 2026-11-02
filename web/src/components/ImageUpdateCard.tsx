import { useState } from 'react'
import { ArrowUpCircle, ExternalLink, Loader2, RefreshCw, Rocket, TriangleAlert } from 'lucide-react'
import { Badge, Button, Card, cx } from './ui'
import { Modal, Select, Skeleton, TextInput } from './feedback'
import { invalidate, run, useBusy } from '../api/hooks'
import { ago } from '../api/view'
import { shortDigest, updatesApi, useImageUpdate } from '../api/updates'
import type { ImageUpdate, ImageUpdatePolicyKind, Service } from '../api/types'
import { isGit } from '../api/git'
import { useT } from '../i18n'

const STATUS: Record<string, [string, 'amber' | 'green' | 'red' | 'zinc']> = {
  update_available: ['有更新', 'amber'],
  up_to_date: ['已是最新', 'green'],
  error: ['检查失败', 'red'],
  unknown: ['未检查', 'zinc'],
  unsupported: ['不支持', 'zinc'],
  disabled: ['已关闭', 'zinc'],
}

/** Small "有更新" badge for service lists and project cards. */
export function UpdateBadge({ u, compact }: { u?: ImageUpdate; compact?: boolean }) {
  if (!u?.update_available) return null
  const what = u.digest_changed ? `${u.current_tag ?? ''} 有新镜像` : u.newer_tag ? `新版本 ${u.newer_tag}` : '有更新'
  return <span title={what}><Badge tone="amber"><ArrowUpCircle size={11} strokeWidth={2.5} />{compact ? '更新' : '有更新'}</Badge></span>
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
  const [confirm, setConfirm] = useState<{ tag?: string } | undefined>()
  const [win, setWin] = useState<string | undefined>()
  const keys = [`image-update:${s.id}`, 'image-updates']
  const check = () => wrap(async () => { const r = await run(() => updatesApi.check(s.id), { invalidate: keys }); if (r) q.set(r) })
  const setPolicy = (policy: ImageUpdatePolicyKind, window = u?.window ?? '') =>
    wrap(async () => { const r = await run(() => updatesApi.setPolicy(s.id, { policy, window: policy === 'auto' ? window : '' }), { ok: '更新策略已保存', invalidate: keys }); if (r) { q.set(r); setWin(undefined) } })
  const winValue = win ?? u?.window ?? ''
  const winBad = winValue !== '' && !WINDOW_RE.test(winValue)

  if (!u) return <Card className="mt-6 p-6">{q.error ? <div className="text-[13px] text-muted">无法读取镜像更新状态：{q.error.message}</div> : <Skeleton className="h-16 w-full" />}</Card>
  const [label, tone] = STATUS[u.status] ?? [u.status, 'zinc']
  const checkable = u.status !== 'unsupported' && u.status !== 'disabled'
  return (
    <Card className="mt-6 p-6">
      <div className="flex flex-wrap items-center gap-3">
        <ArrowUpCircle size={18} className={u.update_available ? 'text-accent' : 'text-muted'} />
        <span className="text-[15px] font-semibold tracking-tight">镜像更新</span>
        <Badge tone={tone}>{label}</Badge>
        <span className="text-[12.5px] text-dim">{u.checked_at ? `检查于 ${ago(u.checked_at)}` : '尚未检查'}{u.next_check_at && u.status !== 'disabled' ? ` · 下次 ${new Date(u.next_check_at).toLocaleString('zh-CN', { hour12: false, month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })}` : ''}</span>
        <div className="ml-auto flex items-center gap-2">
          <Select aria-label="更新策略" className="h-9 w-[132px]" value={u.policy} disabled={busy} onChange={e => setPolicy(e.target.value as ImageUpdatePolicyKind)}>
            <option value="notify">仅通知</option>
            <option value="auto">自动更新</option>
            <option value="off">关闭检查</option>
          </Select>
          <Button size="sm" disabled={busy || u.status === 'disabled' || u.status === 'unsupported'} onClick={check}>{busy ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />}立即检查</Button>
        </div>
      </div>

      {checkable && <div className="mt-5 grid grid-cols-[100px_minmax(0,1fr)_minmax(0,1fr)] gap-y-3 gap-x-6 text-[13px]">
        <span />
        <span className="text-[12px] text-muted">当前运行</span>
        <span className="text-[12px] text-muted">仓库最新</span>
        <span className="text-muted">标签</span>
        <span className="font-mono truncate">{u.current_tag ?? '—'}</span>
        <span className="font-mono truncate flex items-center gap-2">
          {u.newer_tag ? <><span className="text-accent font-semibold">{u.newer_tag}</span><Button size="sm" disabled={busy} onClick={() => setConfirm({ tag: u.newer_tag })}>更新到 {u.newer_tag}</Button></> : <span className="text-fg2">{u.current_tag ?? '—'}</span>}
        </span>
        <span className="text-muted">摘要</span>
        <span className="font-mono text-fg2 truncate" title={u.current_digest}>{shortDigest(u.current_digest)}</span>
        <span className={cx('font-mono truncate', u.digest_changed ? 'text-accent font-semibold' : 'text-fg2')} title={u.latest_digest}>{shortDigest(u.latest_digest)}</span>
        {u.major_tag && <>
          <span className="text-muted">大版本</span>
          <span className="col-span-2 flex items-center gap-2 text-fg2"><TriangleAlert size={14} className="text-[#f59e0b] shrink-0" /><span className="font-mono font-semibold">{u.major_tag}</span><span className="text-muted">主版本号变化，可能不兼容，请先看更新日志</span>
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => setConfirm({ tag: u.major_tag })}>切换</Button></span>
        </>}
      </div>}

      {(u.error || u.note) && <div className={cx('mt-4 text-[12.5px] leading-5 [overflow-wrap:anywhere]', u.error ? 'text-[#ef4444]' : 'text-muted')}>{u.error || u.note}</div>}

      {u.policy === 'auto' && checkable && <div className="mt-4 flex flex-wrap items-center gap-3 text-[13px]">
        <span className="text-muted">自动更新时段</span>
        <TextInput mono className={cx('h-9 w-[150px]', winBad && 'ring-2 ring-[#ef4444]')} placeholder="任意时间" value={winValue}
          onChange={e => setWin(e.target.value.trim())} onKeyDown={e => e.key === 'Enter' && !winBad && setPolicy('auto', winValue)} />
        <span className="text-dim text-[12px]">UTC，例如 18:00-21:00（北京时间 02:00-05:00）；留空表示随时。只自动更新同一标签的新摘要，不会自动换标签。</span>
        {win !== undefined && win !== (u.window ?? '') && <Button size="sm" disabled={busy || winBad} onClick={() => setPolicy('auto', winValue)}>保存</Button>}
      </div>}

      <div className="mt-5 pt-4 border-t border-line flex flex-wrap items-center gap-3">
        {u.changelog_url && <a href={u.changelog_url} target="_blank" rel="noreferrer" className="text-[13px] text-fg2 hover:text-fg flex items-center gap-1.5 hover:underline">更新日志<ExternalLink size={13} /></a>}
        <span className="text-[12.5px] text-dim">更新会创建一次新部署，旧部署保留在部署记录中，可随时回滚。</span>
        <Button className="ml-auto" variant="primary" size="sm" disabled={busy || !u.digest_changed} onClick={() => setConfirm({})}><Rocket size={14} />更新</Button>
      </div>

      {confirm && <Modal title={<><Rocket size={17} />更新 {s.name}</>} onClose={() => setConfirm(undefined)}
        footer={<><Button size="sm" onClick={() => setConfirm(undefined)}>取消</Button><Button size="sm" variant="primary" disabled={busy} onClick={() => wrap(async () => {
          const d = await run(() => updatesApi.apply(s.id, confirm.tag), { ok: '正在更新镜像', invalidate: [...keys, `deployments:${s.id}`, `project:${s.project_id}`, 'projects'] })
          if (d) { setConfirm(undefined); invalidate(...keys) }
        })}>{busy && <Loader2 size={14} className="animate-spin" />}确认更新</Button></>}>
        <div className="text-[13.5px] text-fg2 leading-6">
          {confirm.tag
            ? <>将 <span className="font-mono font-semibold">{s.name}</span> 的镜像标签从 <span className="font-mono">{u.current_tag}</span> 切换到 <span className="font-mono font-semibold">{confirm.tag}</span>，并按该标签当前的摘要重新部署。</>
            : <>按 <span className="font-mono">{u.current_tag}</span> 在仓库中的最新摘要 <span className="font-mono font-semibold">{shortDigest(u.latest_digest)}</span> 重新部署 <span className="font-mono font-semibold">{s.name}</span>（当前 {shortDigest(u.current_digest)}）。</>}
          <div className="mt-3 text-muted text-[13px]">容器会被重建，期间服务短暂不可用；出现问题可在「部署记录」中回滚。</div>
        </div>
      </Modal>}
    </Card>
  )
}
