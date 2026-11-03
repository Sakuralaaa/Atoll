import { useState } from 'react'
import { KeyRound, Pencil, Trash2, Plus, Eye, EyeOff, Lock, Copy, Share2 } from 'lucide-react'
import { Button, Card } from '../../components/ui'
import { ConfirmDialog, Empty, ErrorState, Label, Modal, RowsSkeleton, TextInput, Toggle } from '../../components/feedback'
import { api } from '../../api/client'
import { run, useBusy, useQuery } from '../../api/hooks'
import type { EnvVar, Service } from '../../api/types'
import { cx } from '../../components/ui-utils'

// ---------------- env ----------------
export function EnvTab({ s }: { s: Service }) {
  const [reveal, setReveal] = useState(false)
  const q = useQuery(`env:${s.id}:${reveal ? 1 : 0}`, () => api.env(s.id, reveal))
  const refs = useQuery(`env-refs:${s.id}`, () => api.envRefs(s.id))
  const [edit, setEdit] = useState<{ i: number; v: EnvVar } | null>(null)
  const [raw, setRaw] = useState(false)
  const [del, setDel] = useState<EnvVar>()
  const save = async (vars: EnvVar[]) => {
    const r = await run(() => api.putEnv(s.id, vars), { ok: s.status === 'stopped' ? '已保存，下次启动时生效' : '已保存，正在重新部署', invalidate: [`env:${s.id}`, `deployments:${s.id}`] })
    return !!r
  }
  const rows = q.data ?? []
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
                <button title="编辑" onClick={() => setEdit({ i, v })} className="hover:text-fg"><Pencil size={15} /></button><button title="删除" onClick={() => setDel(v)} className="hover:text-[#ef4444]"><Trash2 size={15} /></button>
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
      {del && <ConfirmDialog title="删除环境变量" name={del.key} onClose={() => setDel(undefined)} description={<>删除 <span className="font-mono">{del.key}</span>，重新部署后生效。</>} onConfirm={() => save(rows.filter(x => x.key !== del.key))} />}
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
function RawEnv({ rows, onClose, onSave }: { rows: EnvVar[]; onClose: () => void; onSave: (v: EnvVar[]) => Promise<boolean> }) {
  const [text, setText] = useState(rows.map(r => `${r.key}=${r.value}`).join('\n'))
  const [busy, wrap] = useBusy()
  const parse = (): EnvVar[] => text.split('\n').map(l => l.trim()).filter(l => l && !l.startsWith('#') && l.includes('=')).map(l => {
    const i = l.indexOf('='), key = l.slice(0, i).trim(), value = l.slice(i + 1).replace(/^"(.*)"$/, '$1')
    const old = rows.find(r => r.key === key)
    return { key, value, secret: old?.secret ?? false, exposed: old?.exposed ?? false }
  })
  return (
    <Modal title="编辑原始环境变量" width={720} onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={busy} onClick={() => wrap(async () => { if (await onSave(parse())) onClose() })}>保存</Button></>}>
      <div className="text-[12.5px] text-muted mb-3">每行一个 KEY=VALUE。私有变量显示为空值，保持为空即不修改。</div>
      <textarea value={text} onChange={e => setText(e.target.value)} spellCheck={false} wrap="off" className="w-full h-[320px] max-h-[50vh] rounded-lg field p-3.5 font-mono text-[13px] leading-6 outline-none text-fg resize-none" />
    </Modal>
  )
}
