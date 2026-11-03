import { useEffect, useRef, useState } from 'react'
import { KeyRound, Pencil, Trash2, Plus, Eye, EyeOff, Lock, Copy, Share2 } from 'lucide-react'
import { Button, Card } from '../../components/ui'
import { Empty, ErrorState, Label, Modal, RowsSkeleton, TextInput, Toggle } from '../../components/feedback'
import { api } from '../../api/client'
import { run, useBusy, useQuery } from '../../api/hooks'
import type { EnvVar, Service } from '../../api/types'
import { cx } from '../../components/ui-utils'
import { toast } from '../../components/toast'

// ---------------- env ----------------
export function EnvTab({ s }: { s: Service }) {
  const [reveal, setReveal] = useState(false)
  const q = useQuery(`env:${s.id}:${reveal ? 1 : 0}`, () => api.env(s.id, reveal))
  const refs = useQuery(`env-refs:${s.id}`, () => api.envRefs(s.id))
  const [edit, setEdit] = useState<{ i: number; v: EnvVar } | null>(null)
  const [raw, setRaw] = useState(false)
  // Deleting a variable is undoable: the row hides at once and the save (which redeploys)
  // waits a few seconds, so a misclick costs nothing. Any other save carries the delete along.
  const [hidden, setHidden] = useState<string>()
  const timer = useRef<{ id: number; flush: () => void }>(undefined)
  const cancelPending = () => { if (timer.current) { clearTimeout(timer.current.id); timer.current = undefined } }
  useEffect(() => () => { const t = timer.current; if (t) { clearTimeout(t.id); t.flush() } }, [])
  const save = async (vars: EnvVar[], redeploy = true) => {
    cancelPending()
    const ok = !redeploy ? '已保存，未重新部署；稍后手动重新部署生效' : s.status === 'stopped' ? '已保存，下次启动时生效' : '已保存，正在重新部署'
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
    toast(`已删除 ${v.key}`, 'info', { label: '撤销', run: () => { cancelPending(); setHidden(undefined) } }, 6000)
  }
  const byService = (refs.data ?? []).reduce<Record<string, EnvVar[]>>((a, v) => { (a[v.service ?? '同项目服务'] ??= []).push(v); return a }, {})
  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-3 mb-4">
        <div className="min-w-[min(100%,260px)] flex-1"><div className="text-[15px] font-semibold">用户变量</div><div className="text-[13px] text-muted mt-0.5">修改后需重新部署生效。可用 {'${变量名}'} 引用同项目其他服务暴露的变量。</div></div>
        <div className="flex flex-wrap gap-2"><Button onClick={() => setReveal(v => !v)}>{reveal ? <EyeOff size={14} /> : <Eye size={14} />}{reveal ? '隐藏私有值' : '显示私有值'}</Button><Button onClick={() => setRaw(true)}><Pencil size={14} />编辑原始环境变量</Button><Button variant="primary" onClick={() => setEdit({ i: -1, v: { key: '', value: '', secret: false, exposed: false } })}><Plus size={15} />添加</Button></div>
      </div>
      {q.error && !q.data ? <ErrorState error={q.error} onRetry={q.refetch} /> : !q.data ? <RowsSkeleton n={5} /> : !rows.length ? <Empty icon={<KeyRound size={22} />} title="还没有环境变量" action={<Button variant="primary" onClick={() => setEdit({ i: -1, v: { key: '', value: '', secret: false, exposed: false } })}><Plus size={15} />添加</Button>} /> : (
        <Card className="divide-y divide-line">
          {rows.map((v, i) => (
            <div key={v.key} className="px-4 @2xl:px-6 py-3 @3xl:py-0 @3xl:h-[52px] grid grid-cols-[minmax(0,1fr)_auto] @3xl:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_170px] items-center gap-x-6 gap-y-1 group hover:bg-ov/[0.02] transition-colors">
              <span className="font-mono text-[13px] font-medium truncate" title={v.key}>{v.key}</span>
              <span title={v.secret && !v.value ? undefined : v.value} className={cx('font-mono text-[13px] truncate @max-3xl:col-span-2 @max-3xl:row-start-2', v.value.includes('${') ? 'text-accent-soft' : 'text-fg2')}>{v.secret && !v.value ? '••••••••••••••••' : v.value}</span>
              <span className="flex items-center justify-end gap-3 text-dim group-hover:text-muted transition-colors @max-3xl:row-start-1 @max-3xl:col-start-2">
                {v.exposed && <span title="同项目其他服务可引用" className="flex items-center gap-1 text-[11.5px] rounded-full ring-1 ring-inset ring-line2 px-2 h-[22px]"><Share2 size={11} />暴露</span>}
                {v.secret && <span className="flex items-center gap-1 text-[11.5px] rounded-full ring-1 ring-inset ring-line2 px-2 h-[22px]"><Lock size={11} />私有</span>}
                <button title="编辑" onClick={() => setEdit({ i, v })} className="hover:text-fg"><Pencil size={15} /></button><button title="删除" onClick={() => remove(v)} className="hover:text-[#ef4444]"><Trash2 size={15} /></button>
              </span>
            </div>
          ))}
        </Card>
      )}
      {Object.entries(byService).map(([svc, vars]) => (
        <div key={svc}>
          <div className="mt-8 text-[15px] font-semibold mb-1">可引用的变量</div>
          <div className="text-[13px] text-muted mb-4">来自同项目服务 {svc}</div>
          <Card className="divide-y divide-line">
            {vars.map(v => (
              <div key={v.key} className="px-4 @2xl:px-6 py-3 @3xl:py-0 @3xl:h-12 grid grid-cols-[minmax(0,1fr)_auto] @3xl:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto] items-center gap-x-6 gap-y-1 text-[13px]">
                <span className="font-mono text-accent-soft truncate" title={v.key}>{v.key}</span>
                <span className="font-mono text-dim truncate @max-3xl:col-span-2 @max-3xl:row-start-2">{v.secret || !v.value ? '••••••••' : v.value}</span>
                <button className="@max-3xl:row-start-1 @max-3xl:col-start-2" title="复制引用" onClick={() => navigator.clipboard?.writeText(`\${${v.key}}`)}><Copy size={14} className="text-muted hover:text-fg" /></button>
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
    </>
  )
}
function EnvEditor({ init, isNew, onClose, onSave }: { init: EnvVar; isNew: boolean; onClose: () => void; onSave: (v: EnvVar) => Promise<boolean> }) {
  const [v, setV] = useState(init); const [busy, wrap] = useBusy()
  const ok = /^[A-Za-z_][A-Za-z0-9_]*$/.test(v.key)
  return (
    <Modal title={isNew ? '添加环境变量' : '编辑环境变量'} onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!ok || busy} onClick={() => wrap(async () => { if (await onSave(v)) onClose() })}>保存</Button></>}>
      <Label t="变量名"><TextInput autoFocus={isNew} mono className="w-full" value={v.key} onChange={e => setV({ ...v, key: e.target.value.trim() })} placeholder="DATABASE_URL" /></Label>
      <Label t="值" d={v.secret && !isNew ? '私有变量：留空保持原值不变' : '可用 ${OTHER_KEY} 引用其他变量'}><TextInput autoFocus={!isNew} mono className="w-full" value={v.value} onChange={e => setV({ ...v, value: e.target.value })} /></Label>
      <div className="flex items-center justify-between gap-4 mb-3"><div className="text-[13px] font-semibold">私有<span className="text-dim font-normal ml-2 text-[12px]">面板与 API 默认隐藏值</span></div><Toggle on={v.secret} onChange={x => setV({ ...v, secret: x })} /></div>
      <div className="flex items-center justify-between gap-4"><div className="text-[13px] font-semibold">暴露给同项目服务<span className="text-dim font-normal ml-2 text-[12px]">其他服务可用 {'${' + (v.key || 'KEY') + '}'} 引用</span></div><Toggle on={v.exposed} onChange={x => setV({ ...v, exposed: x })} /></div>
    </Modal>
  )
}
// parseEnvText reads KEY=VALUE lines like a .env file: blank lines and # comments are skipped, a
// leading `export` is dropped, quoted values are unwrapped ("..." understands \n), and an
// unquoted value loses a trailing " # comment". Bad lines are reported, later keys win.
function parseEnvText(text: string): { vars: { key: string; value: string }[]; errors: string[] } {
  const out = new Map<string, string>(); const errors: string[] = []
  text.split('\n').forEach((raw, n) => {
    let l = raw.trim()
    if (!l || l.startsWith('#')) return
    if (l.startsWith('export ')) l = l.slice(7).trim()
    const i = l.indexOf('=')
    const key = i > 0 ? l.slice(0, i).trim() : ''
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) { errors.push(`第 ${n + 1} 行：${raw.trim().slice(0, 40)}`); return }
    let value = l.slice(i + 1).trim()
    const q = value[0]
    if (value.length >= 2 && (q === '"' || q === "'") && value.endsWith(q)) {
      value = value.slice(1, -1)
      if (q === '"') value = value.replace(/\\(n|"|\\)/g, (_, c: string) => c === 'n' ? '\n' : c)
    } else {
      const j = value.indexOf(' #'); if (j >= 0) value = value.slice(0, j).trim()
    }
    out.delete(key); out.set(key, value)
  })
  return { vars: [...out].map(([key, value]) => ({ key, value })), errors }
}
function RawEnv({ rows, onClose, onSave }: { rows: EnvVar[]; onClose: () => void; onSave: (v: EnvVar[], redeploy: boolean) => Promise<boolean> }) {
  const [text, setText] = useState(rows.map(r => `${r.key}=${r.value}`).join('\n'))
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
    <Modal title="编辑原始环境变量" width={720} onClose={onClose} footer={<>
      <label className="mr-auto flex items-center gap-2 text-[12.5px] text-muted"><Toggle on={redeploy} onChange={setRedeploy} />保存后重新部署</label>
      <Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={busy || parsed.errors.length > 0} onClick={() => wrap(async () => { if (await onSave(vars(), redeploy)) onClose() })}>保存</Button></>}>
      <div className="text-[12.5px] text-muted mb-3">每行一个 KEY=VALUE，可直接粘贴 .env 文件。私有变量显示为空值，保持为空即不修改；删掉的行会被移除。</div>
      <textarea value={text} onChange={e => setText(e.target.value)} spellCheck={false} wrap="off" className="w-full h-[320px] max-h-[50vh] rounded-lg field p-3.5 font-mono text-[13px] leading-6 outline-none text-fg resize-none" />
      <div className="mt-2 text-[12px] text-dim">共 {parsed.vars.length} 个变量{added ? `，新增 ${added}` : ''}{removed ? `，移除 ${removed}` : ''}</div>
      {parsed.errors.length > 0 && <div className="mt-1 text-[12px] text-[#ef4444]">无法解析：{parsed.errors.slice(0, 3).join('；')}{parsed.errors.length > 3 ? ` 等 ${parsed.errors.length} 行` : ''}</div>}
    </Modal>
  )
}
