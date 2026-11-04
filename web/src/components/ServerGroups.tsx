import { Link } from 'react-router-dom'
import { IP } from './IP'
import { projName } from '../api/view'
import { ChevronRight, Server as ServerIcon } from 'lucide-react'
import { Dot, Mini } from './ui'
import { useT } from '../i18n'
import { providerOf, countryOf } from '../api/providers'
import { Flag } from './Flag'
import type { ServerGroup } from '../api/groups'
import { ClampedTags } from './ClampedTags'
import { Latency } from './Latency'
import { pinnedFirst, type useProjectPrefs } from '../api/prefs'
import { Pin, Star } from 'lucide-react'

/** One card per machine; clicking opens that machine's projects (?server=<id>). */
export function ServerGroupCards({ groups, total, listView, prefs }: { groups: ServerGroup[]; total: Map<string, number>; listView: boolean; prefs: ReturnType<typeof useProjectPrefs> }) {
  const { t } = useT()
  return (
    <div className={listView ? 'grid gap-4 grid-cols-1' : 'grid gap-4 grid-cols-1 @2xl:auto-rows-fr @2xl:grid-cols-2 @6xl:grid-cols-3'}>
      {groups.map(g => {
        const s = g.server
        const ss = g.projects.flatMap(p => p.services ?? [])
        const run = ss.filter(x => x.status === 'running').length
        const bad = ss.filter(x => x.status === 'failed').length
        const all = total.get(g.id) ?? g.projects.length
        const status = s?.status === 'offline' ? 'failed' : !ss.length ? 'unknown' : bad ? 'failed' : run === ss.length ? 'running' : 'deploying'
        return (
          <Link key={g.id || '_'} to={`/?server=${encodeURIComponent(g.id || '_')}`} className="group rounded-2xl surface surface-hover overflow-hidden flex flex-col" data-testid="server-group">
            <div className="px-5 pt-5 pb-4 flex items-center gap-3">
              <div className="h-[38px] w-[38px] rounded-xl field grid place-items-center shrink-0">{s && providerOf(s) !== '?' ? <Mini provider={providerOf(s)} size={20} /> : <ServerIcon size={18} className="text-muted" />}</div>
              {/* name wraps to two lines (full name in the tooltip) like the server cards; the block
                  reserves two lines so every card keeps the same height */}
              <div className="min-w-0 flex-1 min-h-[62px] flex flex-col justify-center">
                <div className="text-[15px] font-semibold leading-snug line-clamp-2 [overflow-wrap:anywhere]" title={s?.name}>{s?.name ?? t('未知机器', 'Unknown machine')}{s && <Flag code={countryOf(s)} size={12} className="inline-block ml-2 align-[-1px]" />}</div>
                <div className="text-[12px] text-muted truncate mt-0.5 font-mono">{s ? <IP v={s.public_ipv4 || s.tailnet_ip} /> : g.id || '—'}{s?.status === 'offline' && <span className="ml-2 font-sans text-[#ef4444] font-semibold">{t('离线', 'Offline')}</span>}</div>
              </div>
              <ChevronRight size={18} className="text-dim group-hover:text-fg2 shrink-0" />
            </div>
            {/* project names clamped to two lines with one "+N" chip, so every card is the same height */}
            <div className="px-5 pb-4 flex-1">
              <ClampedTags rows={listView ? 1 : 2} className={listView ? 'h-[30px]' : 'max-h-[64px] @2xl:h-[64px]'} items={pinnedFirst(g.projects, prefs.prefs.pinned).map(p => ({
                key: p.id, title: projName(p),
                node: <span className="inline-flex items-center gap-1 max-w-full">{prefs.isPinned(p.id) && <Pin size={10} className="text-accent shrink-0" fill="currentColor" />}{prefs.isFav(p.id) && <Star size={10} className="text-[#eab308] shrink-0" fill="currentColor" />}<span className="truncate">{projName(p)}</span></span>,
              }))} />
            </div>
            <div className="mt-auto px-5 h-11 shrink-0 border-t border-line bg-sunk/60 flex items-center gap-2 text-[12.5px] text-muted tnum">
              <span>{g.projects.length === all ? t(`${all} 个项目`, `${all} projects`) : t(`${g.projects.length} / ${all} 个项目`, `${g.projects.length} of ${all} projects`)}</span>
              {bad > 0 && <span className="text-[#ef4444] font-semibold">{t(`${bad} 个失败`, `${bad} failed`)}</span>}
              {s && s.status === 'online' && <Latency s={s} label={false} className="text-[12.5px]" />}
              <span className="ml-auto flex items-center gap-2"><Dot status={status} />{run}/{ss.length}</span>
            </div>
          </Link>
        )
      })}
    </div>
  )
}
