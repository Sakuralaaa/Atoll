import { useState } from 'react'
import { ShieldAlert, Check, X, Loader2, Trash2, Globe, ArrowRightLeft, Server, Bot, ShieldCheck } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, cx } from '../components/ui'
import { Empty, ErrorState, RowsSkeleton } from '../components/feedback'
import { api } from '../api/client'
import { run, useQuery } from '../api/hooks'
import { ago, dateTime } from '../api/view'
import type { Confirmation } from '../api/types'

const ACTION: Record<string, [string, React.ReactNode]> = {
  'service.delete': ['删除服务', <Trash2 size={18} />],
  'project.delete': ['删除项目', <Trash2 size={18} />],
  'server.delete': ['移除服务器', <Server size={18} />],
  'service.migrate': ['迁移', <ArrowRightLeft size={18} />],
  'project.migrate': ['迁移', <ArrowRightLeft size={18} />],
  'domain.add': ['绑定域名', <Globe size={18} />],
  'domain.delete': ['解绑域名', <Globe size={18} />],
}
const STATUS: Record<string, [string, 'green' | 'red' | 'zinc' | 'amber']> = { approved: ['已批准', 'green'], rejected: ['已拒绝', 'red'], expired: ['已过期', 'zinc'], pending: ['待确认', 'amber'] }
const danger = (a: string) => a.endsWith('.delete')

export default function Confirmations() {
  const q = useQuery('confirmations:all', () => api.confirmations(), { interval: 15000 })
  const pending = (q.data ?? []).filter(c => c.status === 'pending')
  const done = (q.data ?? []).filter(c => c.status !== 'pending').sort((a, b) => b.created_at.localeCompare(a.created_at))
  return (
    <MainShell>
      <div className="max-w-[1100px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em] flex items-center gap-3">待确认{pending.length > 0 && <Badge tone="red">{pending.length}</Badge>}</h1>
        <p className="text-[13px] text-muted mt-2 mb-8 max-w-[760px] leading-6">CLI 和 AI agent 用 API 令牌发起的删除、迁移、改 DNS 操作不会直接执行，会停在这里等你批准。在面板里直接操作不需要确认。</p>
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <RowsSkeleton n={3} />}
        {q.data && !pending.length && <Empty icon={<ShieldCheck size={22} />} title="没有待确认的操作" desc="agent 发起破坏性操作时会出现在这里，侧栏会显示数量。" />}
        <div className="space-y-4">{pending.map(c => <PendingCard key={c.id} c={c} />)}</div>
        {done.length > 0 && <>
          <div className="text-[13px] font-semibold mt-12 mb-3">历史</div>
          <Card className="divide-y divide-line">
            {done.map(c => {
              const [st, tone] = STATUS[c.status] ?? [c.status, 'zinc']
              return (
                <div key={c.id} className="px-4 @2xl:px-6 min-h-14 py-3 grid grid-cols-[minmax(0,1fr)_auto] @4xl:grid-cols-[100px_minmax(0,1fr)_180px_130px_80px] gap-x-4 gap-y-1 items-center text-[13px]">
                  <span className="font-semibold">{ACTION[c.action]?.[0] ?? c.action}</span>
                  <span className="text-fg2 truncate @max-4xl:col-span-2 @max-4xl:row-start-2" title={c.summary}>{c.summary}</span>
                  <span className="text-muted flex items-center gap-1.5 min-w-0 @max-4xl:row-start-3"><Bot size={13} className="shrink-0" /><span className="truncate" title={c.created_by}>{c.created_by}</span></span>
                  <span className="text-muted tnum whitespace-nowrap @max-4xl:row-start-3 @max-4xl:col-start-2 @max-4xl:text-right">{dateTime(c.created_at)}</span>
                  <span className="flex justify-end @max-4xl:row-start-1 @max-4xl:col-start-2"><Badge tone={tone}>{st}</Badge></span>
                </div>
              )
            })}
          </Card>
        </>}
      </div>
    </MainShell>
  )
}

function PendingCard({ c }: { c: Confirmation }) {
  const [busy, setBusy] = useState<'a' | 'r'>()
  const [label, icon] = ACTION[c.action] ?? [c.action, <ShieldAlert size={18} />]
  const act = async (k: 'a' | 'r') => {
    setBusy(k)
    await run(() => (k === 'a' ? api.approve(c.id) : api.reject(c.id)), { ok: k === 'a' ? '已批准，正在执行' : '已拒绝', invalidate: ['confirmations', 'projects', 'project:', 'domains', 'servers'] })
    setBusy(undefined)
  }
  return (
    <Card className="p-4 @2xl:p-5 flex flex-wrap @3xl:flex-nowrap items-start @3xl:items-center gap-4 @2xl:gap-5">
      <div className={cx('tile h-12 w-12 rounded-lg grid place-items-center shrink-0 text-[#141414]', danger(c.action) ? 'bg-[#fca5a5]' : 'bg-[#fcd34d]')}>{icon}</div>
      <div className="flex-1 min-w-[min(100%,220px)]">
        <div className="flex items-center gap-2 flex-wrap"><span className="text-[15px] font-extrabold italic tracking-[-0.02em]">{label}</span><span className="font-mono text-[12.5px] text-fg2 truncate max-w-full" title={c.target}>{c.target}</span></div>
        <div className="text-[13px] text-fg2 mt-1 leading-5 [overflow-wrap:anywhere]">{c.summary}</div>
        <div className="text-[12px] text-muted mt-1.5 flex items-start gap-1.5 [overflow-wrap:anywhere]"><Bot size={13} className="shrink-0 mt-px" />令牌「{c.created_by}」· {ago(c.created_at)}</div>
      </div>
      <div className="flex gap-2 shrink-0 @max-3xl:w-full @max-3xl:justify-end">
        <Button disabled={!!busy} onClick={() => act('r')}>{busy === 'r' ? <Loader2 size={14} className="animate-spin" /> : <X size={14} />}拒绝</Button>
        <Button variant={danger(c.action) ? 'danger' : 'primary'} disabled={!!busy} onClick={() => act('a')}>{busy === 'a' ? <Loader2 size={14} className="animate-spin" /> : <Check size={14} />}批准</Button>
      </div>
    </Card>
  )
}
