import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { createPortal } from 'react-dom'
import { ArrowLeft, Clock, Download, ChevronDown, ChevronRight, ArrowDown, Play, Loader2, Undo2, ScrollText } from 'lucide-react'
import { Button, CopyId, Dot } from '../components/ui'
import { ConfirmDialog, RowsSkeleton } from '../components/feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import { useLogStream } from '../api/ws'
import { dateTime, deployStatusText, shortDigests, triggerText } from '../api/view'
import type { Deployment, LogLine, Service } from '../api/types'
import { BuildLogModal, CommitLink } from '../components/GitSource'
import { hasBuildLog } from '../api/git'
import { cx } from '../components/ui-utils'
import { useProject } from '../api/queries'
import { useNow } from '../api/useNow'

const level = (l: LogLine) => /\b(ERROR|FATAL|CRITICAL|panic)\b/i.test(l.line) ? 'ERROR' : /\bWARN(ING)?\b/i.test(l.line) ? 'WARN' : 'INFO'
const fmt = (iso: string) => { const d = new Date(iso); const p = (n: number) => String(n).padStart(2, '0'); return `${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}` }
const WINDOWS = [{ k: 0, l: '全部时间' }, { k: 15, l: '最近 15 分钟' }, { k: 60, l: '最近 1 小时' }, { k: 360, l: '最近 6 小时' }]

export default function LogsPage() {
  const { pid, sid } = useParams()
  const [sp, setSp] = useSearchParams()
  const p = useProject(pid).data
  const s = p?.services?.find(x => x.id === sid)
  const view = sp.get('view') ?? 'logs'
  const [actions, setActions] = useState<HTMLElement | null>(null)
  return (
    <div className="h-full flex flex-col topline bg-bg">
      <div className="px-4 md:px-10 pt-6 md:pt-8 pb-5 flex flex-wrap items-end justify-between gap-3 border-b border-line">
        <div>
          <Link to={`/projects/${pid}/services/${sid}`} className="inline-flex items-center gap-1.5 text-[13.5px] text-fg2"><ArrowLeft size={15} />返回</Link>
          <div className="text-[13px] text-muted mt-3">{s?.name ?? '…'} · 服务 ID</div>
          <div className="mt-1"><CopyId v={sid ?? ''} /></div>
          <div className="mt-4 inline-flex max-w-full overflow-x-auto rounded-lg field p-0.5 gap-1">
            {[['logs', '运行日志'], ['deploys', '部署记录'], ['exec', '命令']].map(([k, l]) => (
              <button key={k} onClick={() => setSp(k === 'logs' ? {} : { view: k })} className={cx('h-9 px-3 md:px-4 rounded-md inline-flex items-center whitespace-nowrap text-[13.5px]', view === k ? 'nav-active font-semibold' : 'text-muted hover:text-fg')}>{l}</button>
            ))}
          </div>
        </div>
        {view === 'logs' && <div ref={setActions} className="flex gap-2" />}
      </div>
      {view === 'logs' && <Logs sid={sid} name={s?.name} actions={actions} />}
      {view === 'deploys' && s && <Deploys s={s} />}
      {view === 'exec' && sid && <Exec sid={sid} />}
    </div>
  )
}

function Logs({ sid, name, actions }: { sid?: string; name?: string; actions: HTMLElement | null }) {
  const { lines, live } = useLogStream(sid)
  const [win, setWin] = useState(0); const [winOpen, setWinOpen] = useState(false)
  const [open, setOpen] = useState<Set<number>>(new Set())
  const [follow, setFollow] = useState(true)
  const box = useRef<HTMLDivElement>(null)
  const now = useNow(15000)
  const shown = useMemo(() => win ? lines.filter(l => new Date(l.at).getTime() >= now - win * 60e3) : lines, [lines, win, now])
  const { bars, ticks, max } = useMemo(() => {
    const n = 48
    if (shown.length < 2) return { bars: Array(n).fill(0) as number[], ticks: [] as string[], max: 1 }
    const t0 = new Date(shown[0].at).getTime(), t1 = new Date(shown[shown.length - 1].at).getTime(), span = Math.max(1, t1 - t0)
    const b = Array(n).fill(0) as number[]
    shown.forEach(l => { b[Math.min(n - 1, Math.floor(((new Date(l.at).getTime() - t0) / span) * n))]++ })
    const mx = Math.max(...b)
    return { bars: b, max: mx, ticks: Array.from({ length: 7 }, (_, i) => fmt(new Date(t0 + (span * i) / 6).toISOString()).slice(0, 11)) }
  }, [shown])
  useEffect(() => { if (follow && box.current) box.current.scrollTop = box.current.scrollHeight }, [shown, follow])
  const exportLogs = () => {
    const blob = new Blob([shown.map(l => `${l.at} ${l.stream} ${l.line}`).join('\n')], { type: 'text/plain' })
    const a = document.createElement('a'); a.href = URL.createObjectURL(blob); a.download = `${name ?? sid}-logs.txt`; a.click(); URL.revokeObjectURL(a.href)
  }
  const top = Math.max(4, Math.ceil(max / 2) * 2)
  return (
    <>
      <Portal el={actions}>
        <div className="relative">
          <button onClick={() => setWinOpen(v => !v)} className="h-11 px-3 md:px-4 whitespace-nowrap rounded-lg border border-line2 bg-card flex items-center gap-2 text-[13.5px] font-semibold"><Clock size={16} />{WINDOWS.find(w => w.k === win)?.l}<ChevronDown size={15} /></button>
          {winOpen && <div className="absolute right-0 top-12 z-20 surface bg-pop p-1 w-44">{WINDOWS.map(w => <button key={w.k} onClick={() => { setWin(w.k); setWinOpen(false) }} className={cx('w-full text-left px-3 h-9 rounded-md text-[13px]', w.k === win ? 'nav-active' : 'hover:bg-ov/5')}>{w.l}</button>)}</div>}
        </div>
        <button onClick={exportLogs} className="h-11 px-3 md:px-4 whitespace-nowrap rounded-lg border border-line2 bg-card flex items-center gap-2 text-[13.5px] font-semibold"><Download size={16} />导出</button>
      </Portal>
      <div className="px-4 md:px-10 pt-5 pb-3 border-b border-line">
        <div className="flex gap-3 h-[110px] md:h-[150px]">
          <div className="flex flex-col justify-between text-[11.5px] text-dim pb-5 tnum w-6 text-right"><span>{top}</span><span>{Math.round(top * 0.6)}</span><span>{Math.round(top * 0.3)}</span><span>0</span></div>
          <div className="flex-1 flex flex-col">
            <div className="flex-1 flex items-end gap-[3px]">{bars.map((b, i) => <div key={i} className="flex-1 bg-[#8b5cf6] rounded-t-[2px]" style={{ height: `${(b / top) * 100}%` }} />)}</div>
            <div className="flex justify-between text-[11.5px] text-dim mt-1.5 tnum whitespace-nowrap overflow-hidden">{ticks.map((t, i) => <span key={i} className={cx(i % 2 === 1 && 'max-md:hidden')}>{t}</span>)}</div>
          </div>
        </div>
      </div>
      <div className="grid grid-cols-[84px_minmax(0,1fr)] md:grid-cols-[130px_150px_minmax(0,1fr)] px-4 h-10 items-center text-[11.5px] font-semibold tracking-wider text-muted border-b border-line bg-sunk"><span className="pl-6">SEVERITY</span><span className="max-md:hidden">TIME</span><span>SUMMARY</span></div>
      <div ref={box} onScroll={e => { const el = e.currentTarget; setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40) }} className="flex-1 overflow-auto font-mono text-[13px] relative">
        {!lines.length && !live && <div className="p-4"><RowsSkeleton n={6} /></div>}
        {!shown.length && live && <div className="h-24 grid place-items-center text-[13px] text-muted font-sans">{lines.length ? '这个时间段没有日志' : '还没有日志输出'}</div>}
        {shown.map((l, i) => {
          const lv = level(l), isOpen = open.has(i)
          return (
            <div key={i} onClick={() => setOpen(o => { const n = new Set(o); if (n.has(i)) n.delete(i); else n.add(i); return n })} className={cx('grid grid-cols-[84px_minmax(0,1fr)] md:grid-cols-[130px_150px_minmax(0,1fr)] px-4 items-center border-b border-line/60 hover:bg-ov/[0.02] cursor-pointer', isOpen ? 'py-2 items-start' : 'h-[33px]')}>
              <span className="flex items-center gap-2">{isOpen ? <ChevronDown size={14} className="text-dim" /> : <ChevronRight size={14} className="text-dim" />}<span className={cx('h-2 w-2 rounded-full', lv === 'ERROR' ? 'bg-rose-500' : lv === 'WARN' ? 'bg-amber-400' : 'bg-sky-500')} /><span className="text-[11.5px] font-semibold">{lv}</span></span>
              <span className="text-muted max-md:hidden">{fmt(l.at)}</span><span className={cx('text-fg2 min-w-0', isOpen ? 'whitespace-pre-wrap break-all' : 'truncate')}>{l.line}{isOpen && <span className="block text-dim text-[12px] mt-1">{l.stream} · {l.at}</span>}</span>
            </div>
          )
        })}
        <div className="h-10 flex items-center justify-center text-[13px] text-muted gap-2 font-sans"><span className={cx('h-2 w-2 rounded-full', live ? 'bg-emerald-500 animate-pulse' : 'bg-zinc-500')} />{live ? 'Streaming logs' : '连接中…'}</div>
        {!follow && <button onClick={() => { setFollow(true); box.current?.scrollTo({ top: box.current.scrollHeight }) }} className="sticky float-right right-6 bottom-4 mr-6 h-10 w-10 rounded-full bg-card2 ring-1 ring-line2 grid place-items-center"><ArrowDown size={17} /></button>}
      </div>
    </>
  )
}

/** Renders children into the header action slot (a callback ref from LogsPage). */
function Portal({ el, children }: { el: HTMLElement | null; children: React.ReactNode }) { return el ? createPortal(children, el) : null }

function Deploys({ s }: { s: Service }) {
  const q = useQuery(`deployments:${s.id}`, () => api.deployments(s.id), { interval: 10000 })
  const [target, setTarget] = useState<Deployment>()
  const [logs, setLogs] = useState<Deployment>()
  return (
    <div className="flex-1 overflow-auto px-4 md:px-10 py-6">
      {!q.data ? <RowsSkeleton n={4} /> : !q.data.length ? <div className="text-[13px] text-muted">还没有部署记录</div> : (
        <div className="surface divide-y divide-line">
          {q.data.map((d, i) => (
            <div key={d.id} className="px-4 md:px-6 py-3 min-h-16 flex flex-wrap sm:flex-nowrap items-center gap-x-4 gap-y-2 text-[13px]">
              <Dot status={d.status === 'running' ? 'running' : d.status === 'failed' ? 'failed' : d.status === 'superseded' ? 'stopped' : 'deploying'} />
              <div className="flex-1 min-w-[calc(100%-32px)] sm:min-w-0"><div className="font-semibold flex flex-wrap items-center gap-x-2 gap-y-0.5">{deployStatusText[d.status] ?? d.status}<span className="text-muted font-normal"> · {triggerText[d.trigger] ?? d.trigger}</span><CommitLink s={s} d={d} /></div><div className="font-mono text-[12px] text-muted truncate mt-0.5" title={[d.image_digest ?? d.id, d.message].filter(Boolean).join(' · ')}>{shortDigests(d.image_digest) || d.id}{d.message ? ` · ${shortDigests(d.message)}` : ''}</div></div>
              {hasBuildLog(s, d) && <Button size="sm" variant="ghost" onClick={() => setLogs(d)}><ScrollText size={13} />构建日志</Button>}
              <span className="text-muted tnum whitespace-nowrap max-sm:ml-[23px]">{dateTime(d.started_at)}</span>
              {i > 0 && d.image_digest ? <Button size="sm" className="max-sm:ml-auto" onClick={() => setTarget(d)}><Undo2 size={13} />回滚</Button> : <span className="w-[74px] max-sm:hidden" />}
            </div>
          ))}
        </div>
      )}
      {target && <ConfirmDialog title="回滚部署" action="确认回滚" danger={false} onClose={() => setTarget(undefined)}
        description={<>将用 {dateTime(target.started_at)} 的镜像摘要重新部署 {s.name}。</>}
        onConfirm={() => run(() => api.rollback(s.id, target.id), { ok: '正在回滚', invalidate: [`deployments:${s.id}`, `project:${s.project_id}`] })} />}
      {logs && <BuildLogModal key={logs.id} s={s} d={logs} onClose={() => setLogs(undefined)} />}
    </div>
  )
}

function Exec({ sid }: { sid: string }) {
  const [cmd, setCmd] = useState('')
  const [hist, setHist] = useState<{ cmd: string; out: string; ok: boolean }[]>([])
  const [busy, wrap] = useBusy()
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => { box.current?.scrollTo({ top: box.current.scrollHeight }) }, [hist])
  const go = () => wrap(async () => {
    const c = cmd.trim(); if (!c) return
    setCmd('')
    try { const r = await api.exec(sid, ['sh', '-c', c]); setHist(h => [...h, { cmd: c, out: (r.output ?? '') + (r.error ? `\n${r.error}` : ''), ok: r.ok }]) }
    catch (e) { setHist(h => [...h, { cmd: c, out: (e as Error).message, ok: false }]) }
  })
  return (
    <div className="flex-1 flex flex-col px-4 md:px-10 py-6 min-h-0">
      <div className="text-[13px] text-muted mb-3">在容器内用 <span className="font-mono">sh -c</span> 执行一次性命令（非交互，超时 30 秒）。</div>
      <div ref={box} className="flex-1 surface bg-sunk overflow-auto p-5 font-mono text-[13px] leading-6 min-h-0">
        {!hist.length && <div className="text-dim">例如：ls -la /app、env | sort、cat /etc/os-release</div>}
        {hist.map((h, i) => <div key={i} className="mb-3"><div className="text-accent-soft">$ {h.cmd}</div><pre className={cx('whitespace-pre-wrap break-all', h.ok ? 'text-fg2' : 'text-[#ef4444]')}>{h.out || '(无输出)'}</pre></div>)}
        {busy && <Loader2 size={14} className="animate-spin text-muted" />}
      </div>
      <div className="mt-4 flex gap-2"><input value={cmd} onChange={e => setCmd(e.target.value)} onKeyDown={e => e.key === 'Enter' && go()} placeholder="输入命令，回车执行" className="flex-1 min-w-0 h-11 rounded-lg field px-3.5 font-mono text-[13px] outline-none text-fg placeholder:text-dim" /><Button variant="primary" size="lg" disabled={busy || !cmd.trim()} onClick={go}><Play size={14} />执行</Button></div>
    </div>
  )
}
