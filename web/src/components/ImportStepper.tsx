import { Check } from 'lucide-react'
import { cx } from './ui-utils'

/** A thin progress line for the migration wizards: numbered dots joined by a rule, the current step in the theme's highlight colour. */
export function ImportStepper({ steps, step, done }: { steps: string[]; step: number; done?: number }) {
  const finished = done ?? step
  return (
    <ol className="mt-7 flex items-start" aria-label="steps">
      {steps.map((l, i) => {
        const n = i + 1, ok = n < finished || finished > steps.length, on = n === step && !ok
        return (
          <li key={l} aria-current={on ? 'step' : undefined} className="flex-1 min-w-0 flex flex-col items-start">
            <div className="w-full flex items-center">
              <span className={cx('h-7 w-7 rounded-full grid place-items-center text-[12px] font-bold font-mono shrink-0 border-2 transition-colors',
                ok ? 'bg-[var(--fg)] border-[var(--fg)] text-[var(--bg)]' : on ? 'bg-[var(--nav-bg)] border-[var(--edge)] text-[var(--nav-ink)]' : 'bg-sunk border-line2 text-dim')}>{ok ? <Check size={14} strokeWidth={3} /> : n}</span>
              {i < steps.length - 1 && <span className={cx('h-[2px] flex-1 mx-2 rounded-full', ok ? 'bg-[var(--fg)]' : 'bg-line')} />}
            </div>
            <span className={cx('mt-2 pr-3 text-[12.5px] leading-4 truncate max-w-full', on ? 'font-semibold text-fg' : ok ? 'text-fg2' : 'text-muted')}>{l}</span>
          </li>
        )
      })}
    </ol>
  )
}
