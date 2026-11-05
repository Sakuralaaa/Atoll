import { L } from '../i18n'
import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, CheckCircle2, Info, RefreshCw, X, Loader2, Copy, Check } from 'lucide-react'
import { Button, Card } from './ui'
import { cx } from './ui-utils'
import { dismissToast, toast, useToasts } from './toast'

// ---------- toasts ----------
export function Toaster() {
  const list = useToasts()
  return (
    <div className="fixed left-4 right-4 bottom-4 sm:left-auto sm:right-6 sm:bottom-6 z-[70] flex flex-col gap-3 sm:w-[380px]">
      {list.map(t => (
        <div key={t.id} role="status" className={cx('rise surface px-4 py-3 flex items-start gap-3 text-[13px] font-semibold',
          t.tone === 'error' && '!bg-[#fca5a5] text-[#141414]', t.tone === 'ok' && '!bg-[#86efac] text-[#141414]')}>
          {t.tone === 'error' ? <AlertTriangle size={16} className="mt-0.5 shrink-0" /> : t.tone === 'ok' ? <CheckCircle2 size={16} className="mt-0.5 shrink-0" /> : <Info size={16} className="mt-0.5 shrink-0 text-accent" />}
          <span className="flex-1 min-w-0 [overflow-wrap:anywhere]">{t.msg}</span>
          {t.action && <button onClick={() => { dismissToast(t.id); t.action?.run() }} className="shrink-0 underline underline-offset-2 font-bold hover:opacity-80">{t.action.label}</button>}
          <button onClick={() => dismissToast(t.id)} className="opacity-60 hover:opacity-100"><X size={14} /></button>
        </div>
      ))}
    </div>
  )
}

// ---------- modal & confirm ----------
export function Modal({ title, onClose, children, width = 560, footer }: { title: React.ReactNode; onClose: () => void; children: React.ReactNode; width?: number; footer?: React.ReactNode }) {
  useEffect(() => { const k = (e: KeyboardEvent) => e.key === 'Escape' && onClose(); addEventListener('keydown', k); return () => removeEventListener('keydown', k) }, [onClose])
  return (
    <div className="fixed inset-0 z-50 bg-black/65 flex items-start justify-center pt-[12vh]" onClick={onClose}>
      <div style={{ width }} className="rise surface bg-pop overflow-hidden max-h-[80vh] max-w-[calc(100vw-32px)] flex flex-col" onClick={e => e.stopPropagation()}>
        <div className="flex items-center gap-3 px-5 h-14 border-b-2 border-[var(--edge)] shrink-0">
          <div className="text-[15px] font-extrabold italic tracking-[-0.02em] flex-1 min-w-0 flex items-center gap-2 [&>svg]:shrink-0"><span className="contents">{title}</span></div>
          <button onClick={onClose} className="text-muted hover:text-fg"><X size={18} /></button>
        </div>
        <div className="p-5 overflow-auto">{children}</div>
        {footer && <div className="px-5 py-4 border-t border-line flex justify-end gap-2 shrink-0">{footer}</div>}
      </div>
    </div>
  )
}

/** Confirm dialog. With `name`, the user must type it exactly (reserved for deleting a service,
 *  project, server or other hard-to-undo actions); without it, it is a plain confirm. */
export function ConfirmDialog({ title, description, name, action = L("确认删除"), danger = true, onConfirm, onClose, children }: {
  title: string; description: React.ReactNode; name?: string; action?: string; danger?: boolean
  onConfirm: () => Promise<unknown> | void; onClose: () => void; children?: React.ReactNode
}) {
  const [v, setV] = useState(''); const [busy, setBusy] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  // double-click the name (or the box) to fill it in; the delete still needs its own click
  const fill = () => { if (name === undefined || v === name) return; setV(name); window.getSelection()?.removeAllRanges(); input.current?.focus() }
  const ok = (name === undefined || v === name) && !busy
  const go = async () => { if (!ok) return; setBusy(true); try { await onConfirm(); onClose() } finally { setBusy(false) } }
  return (
    <Modal title={<><AlertTriangle size={17} className={danger ? 'text-[#ef4444]' : 'text-accent'} />{title}</>} onClose={onClose}
      footer={<><Button size="sm" onClick={onClose}>{L("取消")}</Button><Button size="sm" variant={danger ? 'danger' : 'primary'} autoFocus={name === undefined} disabled={!ok} className={!ok ? 'opacity-50 pointer-events-none' : ''} onClick={go}>{busy && <Loader2 size={14} className="animate-spin" />}{action}</Button></>}>
      <div className="text-[13.5px] text-fg2 leading-6 [overflow-wrap:anywhere]">{description}</div>
      {children}
      {name !== undefined && <>
        <div className="mt-5 text-[13px] text-muted [overflow-wrap:anywhere]">{L("输入")} <span onDoubleClick={fill} title={L("双击填入")} className="font-mono font-bold text-fg cursor-pointer select-none">{name}</span> {L("以确认")}<span className="text-dim">{L("（双击名称或输入框可自动填入）")}</span></div>
        <input ref={input} autoFocus value={v} onDoubleClick={fill} onChange={e => setV(e.target.value)} onKeyDown={e => e.key === 'Enter' && go()} className="mt-2 w-full h-11 rounded-lg field px-3.5 font-mono text-[13px] outline-none" />
      </>}
    </Modal>
  )
}

// ---------- form bits ----------
export function TextInput({ className, mono, ...p }: React.InputHTMLAttributes<HTMLInputElement> & { mono?: boolean }) {
  return <input {...p} className={cx('h-11 rounded-lg field px-3.5 text-[13px] outline-none placeholder:text-dim text-fg', mono && 'font-mono', className)} />
}
export function Select({ className, children, ...p }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...p} className={cx('h-11 rounded-lg field px-3 text-[13px] outline-none text-fg', className)}>{children}</select>
}
export function Label({ t, d, children }: { t: string; d?: string; children: React.ReactNode }) {
  return <label className="block mb-4 min-w-0"><div className="text-[13px] font-semibold mb-1.5">{t}</div>{children}{d && <div className="text-[12px] text-dim mt-1.5 [overflow-wrap:anywhere]">{d}</div>}</label>
}
export function Toggle({ on, onChange }: { on: boolean; onChange: (v: boolean) => void }) {
  return (
    <button type="button" onClick={() => onChange(!on)} className={cx('h-6 w-11 shrink-0 rounded-full relative border-2 border-[var(--edge)] transition-colors', on ? 'bg-[#7c3aed]' : 'bg-sunk')}>
      <span className={cx('absolute top-[1px] h-[18px] w-[18px] rounded-full border-2 border-[var(--edge)] bg-[#fde047] transition-all', on ? 'right-[1px]' : 'left-[1px]')} />
    </button>
  )
}
export function CopyButton({ text, label = L("复制") }: { text: string; label?: string }) {
  const [done, setDone] = useState(false)
  return <Button size="sm" onClick={() => { navigator.clipboard?.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1500) }, () => toast(L("复制失败，请手动选择"), 'error')) }}>{done ? <Check size={14} /> : <Copy size={14} />}{done ? L("已复制") : label}</Button>
}
/** Copyable command. Wraps instead of scrolling sideways, so the copy button never covers text; `grow` fills a stretched card. */
export function CodeBlock({ children, grow }: { children: string; grow?: boolean }) {
  const [done, setDone] = useState(false)
  return (
    <div className={cx('relative rounded-lg field min-w-0', grow && 'flex-1')}>
      <pre className="px-4 py-3.5 pr-11 font-mono text-[13px] leading-6 text-fg2 whitespace-pre-wrap [overflow-wrap:anywhere] m-0">{children}</pre>
      <button aria-label={L("复制")} title={L("复制")} onClick={() => navigator.clipboard?.writeText(children).then(() => { setDone(true); setTimeout(() => setDone(false), 1500) })} className="absolute right-2.5 top-3 h-7 w-7 grid place-items-center rounded-md text-muted hover:text-fg hover:bg-ov/5">{done ? <Check size={14} /> : <Copy size={14} />}</button>
    </div>
  )
}

// ---------- states ----------
export function Skeleton({ className, style }: { className?: string; style?: React.CSSProperties }) {
  return <div style={style} className={cx('rounded-md bg-ov/[0.07] animate-pulse', className)} />
}
export function CardSkeleton({ h = 180, n = 3, cols = 3 }: { h?: number; n?: number; cols?: number }) {
  return (
    <div className={cx('grid gap-4 grid-cols-1', cols >= 2 && '@2xl:grid-cols-2', cols >= 3 && '@6xl:grid-cols-3')}>
      {Array.from({ length: n }, (_, i) => (
        <Card key={i} className="p-5" ><div style={{ height: h }} className="flex flex-col gap-3"><div className="flex items-center gap-3"><Skeleton className="h-10 w-10" /><div className="flex-1 space-y-2"><Skeleton className="h-3.5 w-2/3" /><Skeleton className="h-3 w-1/3" /></div></div><div className="flex-1" /><Skeleton className="h-3 w-full" /><Skeleton className="h-3 w-4/5" /></div></Card>
      ))}
    </div>
  )
}
export function RowsSkeleton({ n = 4 }: { n?: number }) {
  return <Card className="divide-y divide-line">{Array.from({ length: n }, (_, i) => <div key={i} className="px-6 h-14 flex items-center gap-6"><Skeleton className="h-3.5 w-40" /><Skeleton className="h-3 w-64" /><Skeleton className="h-3 w-24 ml-auto" /></div>)}</Card>
}
export function Empty({ icon, title, desc, action }: { icon?: React.ReactNode; title: string; desc?: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="rounded-2xl border-2 border-dashed border-line2 px-8 py-12 flex flex-col items-center text-center">
      {icon && <div className="tile h-12 w-12 rounded-lg bg-[#fde047] text-[#141414] grid place-items-center mb-4">{icon}</div>}
      <div className="text-[15px] font-extrabold italic tracking-[-0.02em]">{title}</div>
      {desc && <div className="text-[13px] text-muted mt-1.5 max-w-[460px] leading-6">{desc}</div>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  )
}
export function ErrorState({ error, onRetry }: { error: Error; onRetry?: () => void }) {
  return (
    <Card className="px-6 py-5 flex items-center gap-4 !bg-[#fca5a5] text-[#141414]">
      <AlertTriangle size={18} /><div className="flex-1"><div className="text-[13.5px] font-bold">{L("加载失败")}</div><div className="text-[13px] mt-0.5">{error.message}</div></div>
      {onRetry && <button onClick={onRetry} className="btn-secondary h-8 px-3 text-[12.5px] inline-flex items-center gap-1.5"><RefreshCw size={13} />{L("重试")}</button>}
    </Card>
  )
}
/** Render loading / error / data for a query in one line. */
export function Load<T>({ q, skeleton, children }: { q: { data: T | undefined; loading: boolean; error?: Error; refetch: () => void }; skeleton?: React.ReactNode; children: (d: T) => React.ReactNode }) {
  if (q.data !== undefined) return <>{children(q.data)}</>
  if (q.error) return <ErrorState error={q.error} onRetry={q.refetch} />
  return <>{skeleton ?? <RowsSkeleton />}</>
}
