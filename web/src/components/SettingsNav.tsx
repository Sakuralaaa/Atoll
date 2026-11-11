import { useEffect, useRef } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { cx } from './ui-utils'

import type { SettingsGroup } from './settings-group'

export function SettingsLayout({ groups, active, children }: { groups: SettingsGroup[]; active?: string; children: React.ReactNode }) {
  const nav = useNavigate()
  const loc = useLocation()
  const ref = useRef<HTMLElement>(null)
  // on phones the rail scrolls sideways: keep the current group in view
  useEffect(() => { ref.current?.querySelector<HTMLElement>('[aria-current="page"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' }) }, [active])
  const go = (k: string) => nav({ pathname: loc.pathname, search: loc.search, hash: k === groups[0]?.k ? '' : k }, { replace: true })
  return (
    <div className="grid grid-cols-1 @4xl:grid-cols-[200px_minmax(0,1fr)] gap-6 @4xl:gap-10 items-start">
      <nav ref={ref} data-testid="settings-nav" className="flex @4xl:flex-col gap-1 overflow-x-auto @4xl:overflow-visible @4xl:sticky @4xl:top-6 -mx-1 px-1 pb-1 @4xl:pb-0">
        {groups.map(g => (
          <button key={g.k} type="button" onClick={() => go(g.k)} aria-current={g.k === active ? 'page' : undefined}
            className={cx('shrink-0 h-9 px-3 rounded-[4px] flex items-center gap-2.5 text-[13px] whitespace-nowrap text-left border-2 transition-colors',
              g.k === active ? 'border-[var(--edge)] bg-hi text-hi-ink font-bold' : 'border-transparent text-fg2 hover:text-fg hover:bg-sunk')}>
            <span className="shrink-0 [&>svg]:size-[15px]">{g.icon}</span>{g.l}
          </button>
        ))}
      </nav>
      <div data-testid="settings-group" className="min-w-0 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">{children}</div>
    </div>
  )
}
