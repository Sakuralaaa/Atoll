import { ChevronDown, ChevronUp, Pin, Star } from 'lucide-react'
import { useT } from '../i18n'
import { cx } from './ui-utils'
import type { useProjectPrefs } from '../api/prefs'

type Prefs = ReturnType<typeof useProjectPrefs>

/** Buttons inside a card that is itself a link: never follow the link. */
const stop = (fn: () => void) => (e: React.MouseEvent) => { e.preventDefault(); e.stopPropagation(); fn() }

/** 收藏 star toggle (cards, list rows, project header). */
export function StarButton({ id, prefs, size = 16, className }: { id: string; prefs: Prefs; size?: number; className?: string }) {
  const { t } = useT()
  const on = prefs.isFav(id)
  return (
    <button type="button" onClick={stop(() => prefs.toggleFav(id))} aria-pressed={on} title={on ? t('取消收藏', 'Unfavorite') : t('收藏', 'Favorite')}
      className={cx('h-8 w-8 grid place-items-center rounded-md shrink-0 transition-colors hover:bg-ov/5', on ? 'text-[#eab308]' : 'text-dim hover:text-fg2', className)}>
      <Star size={size} fill={on ? 'currentColor' : 'none'} strokeWidth={on ? 2.2 : 2} />
    </button>
  )
}

/** 置顶 toggle; when pinned, also ↑ / ↓ to reorder the pinned projects. */
export function PinControls({ id, prefs, className }: { id: string; prefs: Prefs; className?: string }) {
  const { t } = useT()
  const on = prefs.isPinned(id)
  const i = prefs.prefs.pinned.indexOf(id), n = prefs.prefs.pinned.length
  const btn = 'h-8 w-8 grid place-items-center rounded-md shrink-0 transition-colors hover:bg-ov/5'
  return (
    <span className={cx('flex items-center', className)}>
      {on && n > 1 && <>
        <button type="button" disabled={i <= 0} onClick={stop(() => prefs.movePin(id, -1))} title={t('上移', 'Move up')} className={cx(btn, 'w-6 text-dim hover:text-fg2 disabled:opacity-30 disabled:pointer-events-none')}><ChevronUp size={15} /></button>
        <button type="button" disabled={i >= n - 1} onClick={stop(() => prefs.movePin(id, 1))} title={t('下移', 'Move down')} className={cx(btn, 'w-6 text-dim hover:text-fg2 disabled:opacity-30 disabled:pointer-events-none')}><ChevronDown size={15} /></button>
      </>}
      <button type="button" onClick={stop(() => prefs.togglePin(id))} aria-pressed={on} title={on ? t('取消置顶', 'Unpin') : t('置顶', 'Pin to top')}
        className={cx(btn, on ? 'text-accent' : 'text-dim hover:text-fg2')}>
        <Pin size={15} fill={on ? 'currentColor' : 'none'} className={on ? '' : 'rotate-45'} />
      </button>
    </span>
  )
}
