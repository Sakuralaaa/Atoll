import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { cx } from './ui-utils'

// ScrollRows shows the first `visible` children and scrolls the rest.
// The cap is measured from the rendered rows, so it fits whatever height a
// row has at the current width (one line on desktop, two on a phone).
export function ScrollRows({ children, visible = 5, className, testId }: { children: ReactNode; visible?: number; className?: string; testId?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const [max, setMax] = useState<number>()
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const measure = () => {
      const rows = el.children
      if (rows.length <= visible) { setMax(undefined); return }
      const last = rows[visible - 1] as HTMLElement
      setMax(last.offsetTop + last.offsetHeight)
    }
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [children, visible])
  return (
    <div ref={ref} data-testid={testId} style={max ? { maxHeight: max } : undefined}
      className={cx('relative divide-y divide-line overflow-y-auto overscroll-contain', className)}>
      {children}
    </div>
  )
}
