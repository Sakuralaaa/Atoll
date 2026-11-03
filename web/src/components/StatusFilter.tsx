import { useT } from '../i18n'
import type { FilterDef, useStatusFilter } from '../api/filters'
import { cx } from './ui-utils'

/** 全部 / … chips with counts. `compact` is the sidebar size. */
export function FilterChips<T>({ f, defs, compact, className }: {
  f: ReturnType<typeof useStatusFilter<T>>; defs: FilterDef<T>[]; compact?: boolean; className?: string
}) {
  const { t } = useT()
  const chip = (id: string, label: string, n: number, alert?: boolean) => {
    const on = f.active === id
    return (
      <button key={id || 'all'} type="button" aria-pressed={on} onClick={() => f.set(on ? '' : id)}
        className={cx('inline-flex items-center gap-1.5 rounded-md font-semibold whitespace-nowrap transition-colors',
          compact ? 'h-7 px-2 text-[11.5px]' : 'h-8 px-3 text-[12.5px]',
          on ? 'nav-active text-fg' : 'border-2 border-transparent text-fg2 hover:bg-ov/5',
          !on && !n && id && 'opacity-50')}>
        {label}
        <span className={cx('tnum font-mono text-[11px] min-w-[18px] h-[18px] px-1 rounded-[3px] grid place-items-center not-italic',
          alert && n ? 'bg-[#fca5a5] text-[#141414] border-[1.5px] border-[var(--edge)]' : 'bg-ov/[0.07] text-muted')}>{n}</span>
      </button>
    )
  }
  return (
    <div role="group" aria-label={t('筛选', 'Filter')} className={cx('flex flex-wrap items-center gap-1.5', className)}>
      {chip('', t('全部', 'All'), f.total)}
      {defs.map(d => chip(d.id, t(d.zh, d.en), f.counts[d.id] ?? 0, d.alert))}
    </div>
  )
}

