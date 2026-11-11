import { Check } from 'lucide-react'
import { cx } from './ui-utils'

/** A thin progress line spanning the full content width (the last step sits at the right edge) for the migration wizards: numbered dots joined by a rule, the current step in the theme's highlight colour. */
export function ImportStepper({ steps, step, done }: { steps: string[]; step: number; done?: number }) {
  const finished = done ?? step
  return (
    <ol className="mt-7 flex items-start" aria-label="steps">
      {steps.map((l, i) => {
        const n = i + 1, last = n === steps.length, ok = n < finished || finished > steps.length, on = n === step && !ok
        return (
          <li key={l} aria-current={on ? 'step' : undefined} className={cx('min-w-0 flex flex-col', last ? 'flex-none items-end' : 'flex-1 items-start')}>
            <div className={cx('flex items-center', !last && 'w-full')}>
              <span className={cx('h-7 w-7 rounded-full grid place-items-center text-[12px] font-bold font-mono shrink-0 border-2 transition-colors',
                ok ? 'bg-[var(--fg)] border-[var(--fg)] text-[var(--bg)]' : on ? 'bg-[var(--nav-bg)] border-[var(--edge)] text-[var(--nav-ink)]' : 'bg-sunk border-line2 text-dim')}>{ok ? <Check size={14} strokeWidth={3} /> : n}</span>
              {i < steps.length - 1 && <span className={cx('h-[2px] flex-1 mx-2 rounded-full', ok ? 'bg-[var(--fg)]' : 'bg-line')} />}
            </div>
            <span className={cx(last ? 'mt-2 text-right' : 'mt-2 pr-3', 'text-[12.5px] leading-4 truncate max-w-full', !on && '@max-2xl:invisible', on ? 'font-semibold text-fg' : ok ? 'text-fg2' : 'text-muted')}>{l}</span>
          </li>
        )
      })}
    </ol>
  )
}
