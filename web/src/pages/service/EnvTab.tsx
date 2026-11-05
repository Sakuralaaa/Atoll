import { useEffect, useRef, useState } from 'react'
import { KeyRound, Pencil, Trash2, Plus, Eye, EyeOff, Lock, Copy, Share2, Download } from 'lucide-react'
import { Button, Card } from '../../components/ui'
import { Empty, ErrorState, Label, Modal, RowsSkeleton, TextInput, Toggle } from '../../components/feedback'
import { api } from '../../api/client'
import { run, useBusy, useQuery } from '../../api/hooks'
import type { EnvVar, Service } from '../../api/types'
import { cx } from '../../components/ui-utils'
import { toast } from '../../components/toast'
import { formatEnvText, parseEnvText, quoteEnvValue } from '../../api/dotenv'
import { L, useT } from '../../i18n'

// ---------------- env ----------------
export function EnvTab({ s }: { s: Service }) {
  const { t } = useT()
  const [reveal, setReveal] = useState(false)
  const q = useQuery(`env:${s.id}:${reveal ? 1 : 0}`, () => api.env(s.id, reveal))
  const refs = useQuery(`env-refs:${s.id}`, () => api.envRefs(s.id))
  const [edit, setEdit] = useState<{ i: number; v: EnvVar } | null>(null)
  const [raw, setRaw] = useState(false)
  const [exp, setExp] = useState(false)
  // Deleting a variable is undoable: the row hides at once and the save (which redeploys)
  // waits a few seconds, so a misclick costs nothing. Any other save carries the delete along.
  const [hidden, setHidden] = useState<string>()
  const timer = useRef<{ id: number; flush: () => void }>(undefined)
  const cancelPending = () => { if (timer.current) { clearTimeout(timer.current.id); timer.current = undefined } }
  useEffect(() => () => { const t = timer.current; if (t) { clearTimeout(t.id); t.flush() } }, [])
  const save = async (vars: EnvVar[], redeploy = true) => {
    cancelPending()
    const ok = !redeploy ? L("已保存，未重新部署；稍后手动重新部署生效") : s.status === 'stopped' ? L("已保存，下次启动时生效") : L("已保存，正在重新部署")
    const r = await run(() => api.putEnv(s.id, vars, redeploy), { ok, invalidate: [`env:${s.id}`, `deployments:${s.id}`] })
    return !!r
  }
  const rows = (q.data ?? []).filter(x => x.key !== hidden)
  const remove = (v: EnvVar) => {
    cancelPending()
    const next = rows.filter(x => x.key !== v.key)
    setHidden(v.key)
    const flush = () => { timer.current = undefined; void save(next).finally(() => setHidden(undefined)) }
    timer.current = { id: window.setTimeout(flush, 6000), flush }
    toast(L("已删除 {0}", v.key), 'info', { label: L("撤销"), run: () => { cancelPending(); setHidden(undefined) } }, 6000)
  }
  const byService = (refs.data ?? []).reduce<Record<string, EnvVar[]>>((a, v) => { (a[v.service ?? L("同项目服务")] ??= []).push(v); return a }, {})
  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-3 mb-4">
        <div className="min-w-[min(100%,260px)] flex-1"><div className="text-[15px] font-semibold">{L("用户变量")}</div><div className="text-[13px] text-muted mt-0.5">{L("修改后需重新部署生效。可用")} {L("${变量名}")} {L("引用同项目其他服务暴露的变量。")}</div></div>
        <div className="flex flex-wrap gap-2"><Button onClick={() => setReveal(v => !v)}>{reveal ? <EyeOff size={14} /> : <Eye size={14} />}{reveal ? L("隐藏私有值") : L("显示私有值")}</Button><Button onClick={() => setRaw(true)}><Pencil size={14} />{L("编辑原始环境变量")}</Button><Button disabled={!rows.length} onClick={() => setExp(true)}><Download size={14} />{t('导出 .env', 'Export .env')}</Button><Button variant="primary" onClick={() => setEdit({ i: -1, v: { key: '', value: '', secret: false, exposed: false } })}><Plus size={15} />{L("添加")}</Button></div>
      </div>
      {q.error && !q.data ? <ErrorState error={q.error} onRetry={q.refetch} /> : !q.data ? <RowsSkeleton n={5} /> : !rows.length ? <Empty icon={<KeyRound size={22} />} title={L("还没有环境变量")} action={<Button variant="primary" onClick={() => setEdit({ i: -1, v: { key: '', value: '', secret: false, exposed: false } })}><Plus size={15} />{L("添加")}</Button>} /> : (
        <Card className="divide-y divide-line">
          {rows.map((v, i) => (
            <div key={v.key} className="px-4 @2xl:px-6 py-3 @3xl:py-0 @3xl:h-[52px] grid grid-cols-[minmax(0,1fr)_auto] @3xl:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_170px] items-center gap-x-6 gap-y-1 group hover:bg-ov/[0.02] transition-colors">
              <span className="font-mono text-[13px] font-medium truncate" title={v.key}>{v.key}</span>
              <span title={v.secret && !v.value ? undefined : v.value} className={cx('font-mono text-[13px] truncate @max-3xl:col-span-2 @max-3xl:row-start-2', v.value.includes('${') ? 'text-accent-soft' : 'text-fg2')}>{v.secret && !v.value ? '••••••••••••••••' : v.value}</span>
              <span className="flex items-center justify-end gap-3 text-dim group-hover:text-muted transition-colors @max-3xl:row-start-1 @max-3xl:col-start-2">
                {v.exposed && <span title={L("同项目其他服务可引用")} className="flex items-center gap-1 text-[11.5px] rounded-full ring-1 ring-inset ring-line2 px-2 h-[22px]"><Share2 size={11} />{L("暴露")}</span>}
                {v.secret && <span className="flex items-center gap-1 text-[11.5px] rounded-full ring-1 ring-inset ring-line2 px-2 h-[22px]"><Lock size={11} />{L("私有")}</span>}
                <button title={L("编辑")} onClick={() => setEdit({ i, v })} className="hover:text-fg"><Pencil size={15} /></button><button title={L("删除")} onClick={() => remove(v)} className="hover:text-[#ef4444]"><Trash2 size={15} /></button>
              </span>
            </div>
          ))}
        </Card>
      )}
      {Object.entries(byService).map(([svc, vars]) => (
        <div key={svc}>
          <div className="mt-8 text-[15px] font-semibold mb-1">{L("可引用的变量")}</div>
          <div className="text-[13px] text-muted mb-4">{L("来自同项目服务")} {svc}</div>
          <Card className="divide-y divide-line">
            {vars.map(v => (
              <div key={v.key} className="px-4 @2xl:px-6 py-3 @3xl:py-0 @3xl:h-12 grid grid-cols-[minmax(0,1fr)_auto] @3xl:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto] items-center gap-x-6 gap-y-1 text-[13px]">
                <span className="font-mono text-accent-soft truncate" title={v.key}>{v.key}</span>
                <span className="font-mono text-dim truncate @max-3xl:col-span-2 @max-3xl:row-start-2">{v.secret || !v.value ? '••••••••' : v.value}</span>
                <button className="@max-3xl:row-start-1 @max-3xl:col-start-2" title={L("复制引用")} onClick={() => navigator.clipboard?.writeText(`\${${v.key}}`)}><Copy size={14} className="text-muted hover:text-fg" /></button>
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
      {exp && <ExportEnv s={s} onClose={() => setExp(false)} />}
    </>
  )
}
function EnvEditor({ init, isNew, onClose, onSave }: { init: EnvVar; isNew: boolean; onClose: () => void; onSave: (v: EnvVar) => Promise<boolean> }) {
  const [v, setV] = useState(init); const [busy, wrap] = useBusy()
  const ok = /^[A-Za-z_][A-Za-z0-9_]*$/.test(v.key)
  return (
    <Modal title={isNew ? L("添加环境变量") : L("编辑环境变量")} onClose={onClose} footer={<><Button size="sm" onClick={onClose}>{L("取消")}</Button><Button size="sm" variant="primary" disabled={!ok || busy} onClick={() => wrap(async () => { if (await onSave(v)) onClose() })}>{L("保存")}</Button></>}>
      <Label t={L("变量名")}><TextInput autoFocus={isNew} mono className="w-full" value={v.key} onChange={e => setV({ ...v, key: e.target.value.trim() })} placeholder="DATABASE_URL" /></Label>
      <Label t={L("值")} d={v.secret && !isNew ? L("私有变量：留空保持原值不变") : L("可用 ${OTHER_KEY} 引用其他变量")}><TextInput autoFocus={!isNew} mono className="w-full" value={v.value} onChange={e => setV({ ...v, value: e.target.value })} /></Label>
      <div className="flex items-center justify-between gap-4 mb-3"><div className="text-[13px] font-semibold">{L("私有")}<span className="text-dim font-normal ml-2 text-[12px]">{L("面板与 API 默认隐藏值")}</span></div><Toggle on={v.secret} onChange={x => setV({ ...v, secret: x })} /></div>
      <div className="flex items-center justify-between gap-4"><div className="text-[13px] font-semibold">{L("暴露给同项目服务")}<span className="text-dim font-normal ml-2 text-[12px]">{L("其他服务可用")} {'${' + (v.key || 'KEY') + '}'} {L("引用")}</span></div><Toggle on={v.exposed} onChange={x => setV({ ...v, exposed: x })} /></div>
    </Modal>
  )
}
function RawEnv({ rows, onClose, onSave }: { rows: EnvVar[]; onClose: () => void; onSave: (v: EnvVar[], redeploy: boolean) => Promise<boolean> }) {
  const [text, setText] = useState(rows.map(r => `${r.key}=${quoteEnvValue(r.value)}`).join('\n'))
  const [redeploy, setRedeploy] = useState(true)
  const [busy, wrap] = useBusy()
  const parsed = parseEnvText(text)
  const vars = (): EnvVar[] => parsed.vars.map(({ key, value }) => {
    const old = rows.find(r => r.key === key)
    return { key, value, secret: old?.secret ?? false, exposed: old?.exposed ?? false }
  })
  const keys = new Set(parsed.vars.map(v => v.key))
  const added = parsed.vars.filter(v => !rows.some(r => r.key === v.key)).length
  const removed = rows.filter(r => !keys.has(r.key)).length
  return (
    <Modal title={L("编辑原始环境变量")} width={720} onClose={onClose} footer={<>
      <label className="mr-auto flex items-center gap-2 text-[12.5px] text-muted"><Toggle on={redeploy} onChange={setRedeploy} />{L("保存后重新部署")}</label>
      <Button size="sm" onClick={onClose}>{L("取消")}</Button><Button size="sm" variant="primary" disabled={busy || parsed.errors.length > 0} onClick={() => wrap(async () => { if (await onSave(vars(), redeploy)) onClose() })}>{L("保存")}</Button></>}>
      <div className="text-[12.5px] text-muted mb-3">{L("每行一个 KEY=VALUE，可直接粘贴 .env 文件。私有变量显示为空值，保持为空即不修改；删掉的行会被移除。")}</div>
      <textarea value={text} onChange={e => setText(e.target.value)} spellCheck={false} wrap="off" className="w-full h-[320px] max-h-[50vh] rounded-lg field p-3.5 font-mono text-[13px] leading-6 outline-none text-fg resize-none" />
      <div className="mt-2 text-[12px] text-dim">{L("共")} {parsed.vars.length} {L("个变量")}{added ? L("，新增 {0}", added) : ''}{removed ? L("，移除 {0}", removed) : ''}</div>
      {parsed.errors.length > 0 && <div className="mt-1 text-[12px] text-[#ef4444]">{L("无法解析：")}{parsed.errors.slice(0, 3).map(e => L("第 {0} 行：{1}", e.line, e.text)).join(L("；"))}{parsed.errors.length > 3 ? L(" 等 {0} 行", parsed.errors.length) : ''}</div>}
    </Modal>
  )
}

/** 导出 .env: secrets are left out (a comment line) unless 包含私有值 is on, which reveals them
 *  (GET ?reveal=1: panel session only, audited as env.reveal). Copy or download as <service>.env. */
function ExportEnv({ s, onClose }: { s: Service; onClose: () => void }) {
  const { t } = useT()
  const [secrets, setSecrets] = useState(false)
  const q = useQuery(`env:${s.id}:${secrets ? 1 : 0}`, () => api.env(s.id, secrets))
  const out = q.data ? formatEnvText(q.data, secrets) : undefined
  const file = `${s.name.replace(/[^\w.-]+/g, '_') || 'service'}.env`
  const copy = async () => {
    if (!out) return
    try { await navigator.clipboard.writeText(out.text); toast(t('已复制到剪贴板', 'Copied to clipboard'), 'ok') }
    catch { toast(t('无法访问剪贴板，请改用下载', 'Clipboard unavailable, use Download'), 'error') }
  }
  const download = () => {
    if (!out) return
    const url = URL.createObjectURL(new Blob([out.text], { type: 'text/plain;charset=utf-8' }))
    const a = document.createElement('a'); a.href = url; a.download = file
    document.body.appendChild(a); a.click(); a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return (
    <Modal title={t('导出 .env', 'Export .env')} width={720} onClose={onClose} footer={<>
      <label className="mr-auto flex items-center gap-2 text-[12.5px] text-muted"><Toggle on={secrets} onChange={setSecrets} />{t('包含私有值', 'Include secret values')}</label>
      <Button size="sm" disabled={!out} onClick={copy}><Copy size={14} />{t('复制', 'Copy')}</Button>
      <Button size="sm" variant="primary" disabled={!out} onClick={download}><Download size={14} />{t('下载', 'Download')} {file}</Button></>}>
      <div className="text-[12.5px] text-muted mb-3">{secrets
        ? t('文件里包含私有变量的真实值，请妥善保管。显示私有值会记入审计日志。', 'The file contains the real secret values; keep it safe. Revealing them is recorded in the audit log.')
        : t('私有变量只保留变量名（注释行），不含值；打开「包含私有值」导出真实值。', 'Secret variables are kept as comment lines without their value; turn on “Include secret values” to export them.')}
        {' '}{t('可直接用「编辑原始环境变量」或 atoll env import 导入。', 'Import it again with “Edit raw variables” or atoll env import.')}</div>
      {q.error && !q.data ? <ErrorState error={q.error} onRetry={q.refetch} /> : !out ? <RowsSkeleton n={4} /> : <>
        <textarea readOnly value={out.text} spellCheck={false} wrap="off" className="w-full h-[300px] max-h-[50vh] rounded-lg field p-3.5 font-mono text-[13px] leading-6 outline-none text-fg resize-none" />
        <div className="mt-2 text-[12px] text-dim">{t(`共 ${q.data!.length - out.omitted} 个变量`, `${q.data!.length - out.omitted} variables`)}{out.omitted ? t(`，${out.omitted} 个私有变量未导出值`, `, ${out.omitted} secret values left out`) : ''}</div>
      </>}
    </Modal>
  )
}
