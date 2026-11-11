import { Check } from 'lucide-react'
import { cx } from './ui-utils'

/**
 * A thin progress line spanning the full content width for the migration
 * wizards: numbered dots joined by a rule, the current step in the theme's
 * highlight colour. Each step is a flex-1 cell (dot + rule) and the last step
 * is just its dot at the right edge, so the rules always meet the next dot;
 * the last label hangs leftwards from that dot instead of widening its cell.
 */
export function ImportStepper({ steps, step, done }: { steps: string[]; step: number; done?: number }) {
  const finished = done ?? step
  return (
    <ol className="mt-7 flex items-start" aria-label="steps">
      {steps.map((l, i) => {
        const n = i + 1, last = n === steps.length, ok = n < finished || finished > steps.length, on = n === step && !ok
        const label = cx('mt-2 text-[12.5px] leading-4 truncate', !on && '@max-2xl:invisible', on ? 'font-semibold text-fg' : ok ? 'text-fg2' : 'text-muted')
        return (
          <li key={l} aria-current={on ? 'step' : undefined} className={cx('min-w-0 flex flex-col', last ? 'flex-none w-7 relative' : 'flex-1')}>
            <div className="flex items-center">
              <span className={cx('h-7 w-7 rounded-full grid place-items-center text-[12px] font-bold font-mono shrink-0 border-2 transition-colors',
                ok ? 'bg-[var(--fg)] border-[var(--fg)] text-[var(--bg)]' : on ? 'bg-[var(--nav-bg)] border-[var(--edge)] text-[var(--nav-ink)]' : 'bg-sunk border-line2 text-dim')}>{ok ? <Check size={14} strokeWidth={3} /> : n}</span>
              {!last && <span className={cx('h-[2px] flex-1 mx-2 rounded-full', ok ? 'bg-[var(--fg)]' : 'bg-line')} />}
            </div>
            {last
              ? <span className={cx(label, 'absolute right-0 top-7 text-right whitespace-nowrap max-w-[10rem]')}>{l}</span>
              : <span className={cx(label, 'pr-3 max-w-full')}>{l}</span>}
          </li>
        )
      })}
    </ol>
  )
}
