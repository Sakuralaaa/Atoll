import { L } from '../i18n'
import { useLayoutEffect, useRef, useState } from 'react'
import { cx } from './ui-utils'

/** A wrapping row of chips clamped to `rows` lines: what does not fit becomes one "+N" chip at
 *  the end of the last line, so every card using it has the same height whatever the names are. */
export function ClampedTags({ items, rows = 2, className }: { items: { key: string; node: React.ReactNode; title?: string }[]; rows?: number; className?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const [shown, setShown] = useState(items.length)
  const [width, setWidth] = useState(0)
  const sig = items.map(i => i.key).join('\n')
  // start over when the items or the width change
  const [seen, setSeen] = useState({ sig, width })
  if (seen.sig !== sig || seen.width !== width) { setSeen({ sig, width }); setShown(items.length) }
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(() => setWidth(el.clientWidth))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const kids = [...el.children] as HTMLElement[]
    if (!kids.length) return
    const top = kids[0].offsetTop, h = kids[0].offsetHeight
    const gap = kids.length > 1 ? Math.max(0, parseFloat(getComputedStyle(el).rowGap) || 0) : 0
    const limit = top + rows * h + (rows - 1) * gap + 1
    const over = kids.some(k => k.offsetTop + k.offsetHeight > limit)
    // drop one chip at a time until the chips plus "+N" fit (a few passes at most)
    // oxlint-disable-next-line react/set-state-in-effect
    if (over && shown > 0) setShown(shown - 1)
  }, [shown, sig, width, rows])
  const rest = items.length - shown
  return (
    <div ref={ref} className={cx('flex flex-wrap content-start items-center gap-1.5 overflow-hidden', className)}>
      {items.slice(0, shown).map(i => <span key={i.key} title={i.title} className="text-[12px] leading-5 px-2 py-0.5 rounded-md field text-fg2 max-w-[160px] truncate">{i.node}</span>)}
      {rest > 0 && <span className="text-[12px] leading-5 px-2 py-0.5 rounded-md bg-ov/[0.07] text-muted font-mono tnum shrink-0" title={items.slice(shown).map(i => i.title ?? i.key).join(L("、"))}>+{rest}</span>}
    </div>
  )
}
