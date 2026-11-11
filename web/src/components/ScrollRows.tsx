import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { cx } from './ui-utils'

// ScrollRows shows the first `visible` children and scrolls the rest inside the box
// (long tables: audit log, domains, backups, history). Keep a table's header outside it.
// The cap is measured from the rendered rows, so it fits whatever height a
// row has at the current width (one line on desktop, two on a phone).
export function ScrollRows({ children, visible = 5, divide = true, className, testId }: { children: ReactNode; visible?: number; divide?: boolean; className?: string; testId?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const [max, setMax] = useState<number>()
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const measure = () => {
      const rows = el.children
      if (rows.length <= visible) { setMax(undefined); return }
      const last = rows[visible - 1] as HTMLElement
      // never taller than ~60% of the window, whatever the rows' height
      setMax(Math.min(last.offsetTop + last.offsetHeight, Math.max(320, Math.round(window.innerHeight * 0.6))))
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    window.addEventListener('resize', measure)
    return () => { ro.disconnect(); window.removeEventListener('resize', measure) }
  }, [children, visible])
  return (
    <div ref={ref} data-testid={testId} style={max ? { maxHeight: max } : undefined}
      className={cx('relative overflow-y-auto overscroll-contain', divide && 'divide-y divide-line', className)}>
      {children}
    </div>
  )
}
