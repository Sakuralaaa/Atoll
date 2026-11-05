import { L } from '../i18n'
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
  // The whole address is the toggle (not just the eye): cards are links, so a click anywhere on it must not open the card.
  return (
    <span role="button" tabIndex={0} onClick={toggle} onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') toggle(e as unknown as MouseEvent) }}
      title={open ? L("点击隐藏") : L("点击显示完整地址")} aria-label={open ? L("隐藏 IP") : L("显示 IP")} aria-pressed={open}
      className={cx('inline-flex items-center gap-1.5 min-w-0 max-w-full cursor-pointer rounded hover:text-fg', className)}>
      <span className="truncate">{open ? v : masked}</span>
      <span className="shrink-0 text-dim -m-1 p-1">{open ? <EyeOff size={13} /> : <Eye size={13} />}</span>
    </span>
  )
}

/** The page-wide switch: hide or show every IP at once. */
export function HideIPsButton({ className }: { className?: string }) {
  const [hide, set] = useHideIPs()
  return (
    <button type="button" onClick={() => set(!hide)} title={hide ? L("显示全部 IP") : L("隐藏全部 IP")} className={cx('h-11 w-11 grid place-items-center', hide ? 'text-fg2' : 'text-accent', className)}>
      {hide ? <EyeOff size={18} /> : <Eye size={18} />}
    </button>
  )
}
