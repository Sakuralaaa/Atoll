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
    <div className="max-w-[960px] mx-auto">
      <nav ref={ref} data-testid="settings-nav" className="flex justify-start @3xl:justify-center gap-1 overflow-x-auto border-b border-line mb-8 -mx-4 px-4 @2xl:mx-0 @2xl:px-0">
        {groups.map(g => (
          <button key={g.k} type="button" onClick={() => go(g.k)} aria-current={g.k === active ? 'page' : undefined}
            className={cx('shrink-0 h-10 px-3.5 -mb-px flex items-center gap-2 text-[13px] whitespace-nowrap border-b-2 transition-colors',
              g.k === active ? 'border-[var(--fg)] text-fg font-semibold' : 'border-transparent text-muted hover:text-fg')}>
            <span className="shrink-0 [&>svg]:size-[15px]">{g.icon}</span>{g.l}
          </button>
        ))}
      </nav>
      <div data-testid="settings-group" className="min-w-0 [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">{children}</div>
    </div>
  )
}
