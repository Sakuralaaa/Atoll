import { useState, type MouseEvent } from 'react'
import { Eye, EyeOff } from 'lucide-react'
import { cx } from './ui-utils'
import { maskIPs, useHideIPs } from '../api/privacy'

/** An IP address (or text containing them), masked while IPs are hidden, with an eye to show this one. */
export function IP({ v, className, empty = '—' }: { v?: string | null; className?: string; empty?: string }) {
  const [hide] = useHideIPs()
  const [open, setOpen] = useState(false)
  if (!v) return <span className={className}>{empty}</span>
  const masked = maskIPs(v)
  if (!hide || masked === v) return <span className={className} title={v}>{v}</span>
  const toggle = (e: MouseEvent) => { e.preventDefault(); e.stopPropagation(); setOpen(o => !o) }
  return (
    <span className={cx('inline-flex items-center gap-1.5 min-w-0 max-w-full', className)}>
      <span className="truncate" title={open ? v : undefined}>{open ? v : masked}</span>
      <button type="button" onClick={toggle} title={open ? '隐藏' : '显示完整地址'} aria-label={open ? '隐藏 IP' : '显示 IP'} className="shrink-0 text-dim hover:text-fg2"><span className="sr-only">{open ? '隐藏' : '显示'}</span>{open ? <EyeOff size={13} /> : <Eye size={13} />}</button>
    </span>
  )
}

/** The page-wide switch: hide or show every IP at once. */
export function HideIPsButton({ className }: { className?: string }) {
  const [hide, set] = useHideIPs()
  return (
    <button type="button" onClick={() => set(!hide)} title={hide ? '显示全部 IP' : '隐藏全部 IP'} className={cx('h-11 w-11 grid place-items-center', hide ? 'text-fg2' : 'text-accent', className)}>
      {hide ? <EyeOff size={18} /> : <Eye size={18} />}
    </button>
  )
}
