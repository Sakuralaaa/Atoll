import { L } from '../../i18n'
import { useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { X, AlertTriangle, ChevronRight, ChevronDown, Upload } from 'lucide-react'
import { Badge, Button, Card } from '../../components/ui'
import { CodeBlock, Select } from '../../components/feedback'
import { api } from '../../api/client'
import { run, useBusy, useQuery } from '../../api/hooks'
import { ago, bytes } from '../../api/view'
import type { ImportPlan } from '../../api/types'
import { cx } from '../../components/ui-utils'
import { useBackendText } from '../../api/backendText'

// ---------------- history & advanced ----------------
const JOB_TEXT: Record<string, [string, 'green' | 'amber' | 'red' | 'zinc' | 'violet']> = {
  planned: [L("待开始"), 'zinc'], rehearsing: [L("演练中"), 'amber'], rehearsed: [L("演练通过"), 'violet'], cutting_over: [L("切换中"), 'amber'], live: [L("已接管"), 'green'],
  rolling_back: [L("回滚中"), 'amber'], rolled_back: [L("已回滚"), 'zinc'], cleaning: [L("移除 k3s 中"), 'amber'], cleaned: [L("已完成"), 'green'],
  uploading: [L("上传中"), 'amber'], deploying: [L("部署中"), 'amber'], verifying: [L("校验中"), 'amber'], done: [L("已完成"), 'green'], failed: [L("失败"), 'red'],
}
export function History({ current }: { current: string | null }) {
  const q = useQuery('imports', api.imports, { interval: 10000 })
  const nav = useNavigate()
  if (!q.data?.length) return null
  return (
    <div className="mt-12">
      <div className="text-[13px] font-semibold mb-3">{L("迁移记录")}</div>
      <Card className="divide-y divide-line">
        {q.data.map(j => {
          const [t, tone] = JOB_TEXT[j.status] ?? [j.status, 'zinc']
          const done = j.steps.filter(s => s.status === 'done').length
          return (
            <button key={j.id} onClick={() => nav(j.mode === 'docker' ? `/import/docker?job=${j.id}` : `/import?job=${j.id}`)} className={cx('w-full text-left px-4 @2xl:px-6 py-3 @3xl:py-0 @3xl:h-14 grid grid-cols-[minmax(0,1fr)_auto] @3xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)_minmax(0,0.7fr)_minmax(0,100px)_110px_20px] items-center gap-x-4 gap-y-1 text-[13px] hover:bg-ov/[0.02]', current === j.id && 'bg-ov/[0.04]')}>
              <span className="font-semibold truncate">{j.plan.project_name}</span>
              <span className="text-muted truncate @max-3xl:col-span-2 @max-3xl:text-[12px]">{j.mode === 'zeabur-local' ? L("原地接管") : j.mode === 'docker' ? L("Docker 接管") : L("CLI 计划")} · {j.plan.services.length} {L("个服务")}</span>
              <span className="text-muted tnum @max-3xl:hidden">{done}/{j.steps.length} {L("步")}</span>
              <span className="text-muted truncate @max-3xl:hidden">{ago(j.created_at)}</span>
              <span className="@max-3xl:row-start-1 @max-3xl:col-start-2"><Badge tone={tone}>{t}</Badge></span>
              <ChevronRight size={15} className="text-dim @max-3xl:hidden" />
            </button>
          )
        })}
      </Card>
    </div>
  )
}

export function AdvancedCli() {
  const { bt } = useBackendText()
  const [open, setOpen] = useState(false)
  const [plan, setPlan] = useState<ImportPlan>(); const [err, setErr] = useState<string>(); const [target, setTarget] = useState('')
  const servers = (useQuery(open ? 'servers' : null, api.servers).data ?? []).filter(s => s.role !== 'control')
  const file = useRef<HTMLInputElement>(null)
  const nav = useNavigate()
  const [busy, wrap] = useBusy()
  const load = async (f?: File) => {
    if (!f) return
    try { const p = JSON.parse(await f.text()) as ImportPlan; if (!p.services) throw new Error(L("不是有效的 plan.json（缺少 services）")); setPlan(p); setErr(undefined); setTarget(p.target_server_id || '') }
    catch (e) { setErr((e as Error).message); setPlan(undefined) }
  }
  const tgt = target || servers[0]?.id || ''
  const submit = () => wrap(async () => {
    if (!plan) return
    const j = await run(() => api.createImport({ ...plan, target_server_id: tgt }), { ok: L("已创建导入任务"), invalidate: ['imports'] })
    if (j) { const s = await run(() => api.startImport(j.id), { ok: L("开始部署") }); nav(`/import?job=${(s ?? j).id}`); setPlan(undefined) }
  })
  const dataBytes = useMemo(() => plan?.services.reduce((a, s) => a + (s.data_bytes || 0), 0) ?? 0, [plan])
  return (
    <div className="mt-10">
      <button onClick={() => setOpen(v => !v)} className="flex items-center gap-2 text-[13px] font-semibold text-fg2 hover:text-fg">{open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}{L("高级：用 CLI 导入")}<span className="text-muted font-normal">{L("（Zeabur 托管的不是你自己的服务器时）")}</span></button>
      {open && (
        <div className="mt-4 grid grid-cols-1 @4xl:grid-cols-2 gap-5 [&>*]:min-w-0">
          <Card className="p-5">
            <div className="text-[13px] font-semibold mb-1">{L("在已登录")} <span className="font-mono">npx zeabur</span> {L("的电脑上运行")}</div>
            <div className="text-[12.5px] text-muted mb-3">{L("镜像需推送到你自己的仓库，数据经中控中转上传。")}</div>
            <CodeBlock>{L("atoll import zeabur inventory          # 列出 Zeabur 项目\natoll import zeabur plan -p <id> > plan.json\natoll import zeabur images plan.json   # 导出并推送镜像\natoll import zeabur data plan.json     # 上传卷与数据库\natoll import zeabur apply plan.json\natoll import zeabur cutover plan.json")}</CodeBlock>
          </Card>
          <Card className="p-5">
            <div className="text-[13px] font-semibold mb-3">{L("或在这里上传 plan.json")}</div>
            <input ref={file} type="file" accept="application/json,.json" className="hidden" onChange={e => load(e.target.files?.[0])} />
            {!plan ? (
              <button onClick={() => file.current?.click()} onDragOver={e => e.preventDefault()} onDrop={e => { e.preventDefault(); load(e.dataTransfer.files[0]) }} className="w-full h-[150px] rounded-xl border-2 border-dashed border-line2 grid place-items-center text-[13px] text-muted hover:text-fg hover:border-[var(--edge)]">
                <span className="flex flex-col items-center gap-2"><Upload size={20} />{L("点击或拖入 plan.json")}</span>
              </button>
            ) : (
              <div>
                <div className="flex items-center gap-2 text-[13.5px] font-semibold">{plan.project_name}<span className="text-muted font-normal text-[12.5px]">· {plan.services.length} {L("个服务 ·")} {bytes(dataBytes)}</span><button onClick={() => setPlan(undefined)} className="ml-auto text-muted hover:text-fg"><X size={15} /></button></div>
                <div className="mt-2 flex flex-wrap gap-1.5">{plan.services.map(s => <span key={s.name} className="chip px-2 h-6 inline-flex items-center bg-card text-[12px] font-mono">{s.name}</span>)}</div>
                {plan.warnings?.map(w => <div key={w} className="mt-2 flex items-start gap-1.5 text-[12.5px] text-fg2"><AlertTriangle size={13} className="text-[#f59e0b] mt-0.5 shrink-0" /><span className="min-w-0 break-words">{bt(w)}</span></div>)}
                <div className="mt-4 flex gap-2"><Select className="flex-1" value={tgt} onChange={e => setTarget(e.target.value)}>{servers.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}</Select><Button variant="primary" disabled={busy || !tgt} onClick={submit}>{L("创建并开始")}</Button></div>
              </div>
            )}
            {err && <div className="mt-3 text-[12.5px] text-[#ef4444]">{err}</div>}
          </Card>
        </div>
      )}
    </div>
  )
}
