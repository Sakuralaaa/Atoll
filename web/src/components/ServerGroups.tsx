import { Link } from 'react-router-dom'
import { ChevronRight, Server as ServerIcon } from 'lucide-react'
import { Dot, Mini } from './ui'
import { useT } from '../i18n'
import { providerOf, countryOf } from '../api/providers'
import { Flag } from './Flag'
import type { ServerGroup } from '../api/groups'

/** One card per machine; clicking opens that machine's projects (?server=<id>). */
export function ServerGroupCards({ groups, total, listView }: { groups: ServerGroup[]; total: Map<string, number>; listView: boolean }) {
  const { t } = useT()
  return (
    <div className={listView ? 'grid gap-4 grid-cols-1' : 'grid gap-4 grid-cols-1 @2xl:grid-cols-2 @6xl:grid-cols-3'}>
      {groups.map(g => {
        const s = g.server
        const ss = g.projects.flatMap(p => p.services ?? [])
        const run = ss.filter(x => x.status === 'running').length
        const bad = ss.filter(x => x.status === 'failed').length
        const all = total.get(g.id) ?? g.projects.length
        const status = s?.status === 'offline' ? 'failed' : !ss.length ? 'unknown' : bad ? 'failed' : run === ss.length ? 'running' : 'deploying'
        return (
          <Link key={g.id || '_'} to={`/?server=${encodeURIComponent(g.id || '_')}`} className="group rounded-2xl surface surface-hover overflow-hidden" data-testid="server-group">
            <div className="px-5 pt-5 pb-4 flex items-center gap-3">
              <div className="h-[38px] w-[38px] rounded-xl field grid place-items-center shrink-0">{s && providerOf(s) !== '?' ? <Mini provider={providerOf(s)} size={20} /> : <ServerIcon size={18} className="text-muted" />}</div>
              <div className="min-w-0 flex-1">
                <div className="text-[15px] font-semibold truncate flex items-center gap-2"><span className="truncate">{s?.name ?? t('未知机器', 'Unknown machine')}</span>{s && <Flag code={countryOf(s)} size={12} />}</div>
                <div className="text-[12px] text-muted truncate mt-0.5 font-mono">{s ? (s.public_ipv4 || s.tailnet_ip || '—') : g.id || '—'}{s?.status === 'offline' && <span className="ml-2 font-sans text-[#ef4444] font-semibold">{t('离线', 'Offline')}</span>}</div>
              </div>
              <ChevronRight size={18} className="text-dim group-hover:text-fg2 shrink-0" />
            </div>
            <div className="px-5 pb-4 flex flex-wrap gap-1.5 min-h-[26px]">
              {g.projects.slice(0, 6).map(p => <span key={p.id} className="text-[12px] px-2 py-0.5 rounded-md field text-fg2 max-w-[160px] truncate">{p.name}</span>)}
              {g.projects.length > 6 && <span className="text-[12px] px-2 py-0.5 text-muted">+{g.projects.length - 6}</span>}
            </div>
            <div className="px-5 h-11 border-t border-line bg-sunk/60 flex items-center gap-2 text-[12.5px] text-muted tnum">
              <span>{g.projects.length === all ? t(`${all} 个项目`, `${all} projects`) : t(`${g.projects.length} / ${all} 个项目`, `${g.projects.length} of ${all} projects`)}</span>
              {bad > 0 && <span className="text-[#ef4444] font-semibold">{t(`${bad} 个失败`, `${bad} failed`)}</span>}
              <span className="ml-auto flex items-center gap-2"><Dot status={status} />{run}/{ss.length}</span>
            </div>
          </Link>
        )
      })}
    </div>
  )
}
