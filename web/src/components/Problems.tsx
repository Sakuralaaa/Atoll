import { useState } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, ArchiveX, ChevronDown, Globe, HardDrive, Rocket, ServerOff, ShieldAlert, Siren } from 'lucide-react'
import { problemHref, problemLabel, problemScope, useFiringProblems } from '../api/problems'
import { ago } from '../api/view'
import { useT } from '../i18n'

const ICON: Record<string, React.ReactNode> = {
  node_offline: <ServerOff size={15} />, disk_high: <HardDrive size={15} />, service_down: <Siren size={15} />,
  deploy_failed: <Rocket size={15} />, backup_failed: <ArchiveX size={15} />, backup_verify_failed: <ShieldAlert size={15} />, domain_error: <Globe size={15} />,
}

const COLLAPSE_KEY = 'atoll.problems.collapsed.'

/** Firing problems that concern the page, pinned above its content; renders nothing when
 *  all is well. The sidebar always counts every problem; a page shows only its own kind
 *  (servers page: servers, projects page: services, deploys and domains). Collapsible, per
 *  page and per browser (localStorage). */
export function ProblemsBanner({ scope }: { scope: 'servers' | 'projects' }) {
  const { t } = useT()
  const list = useFiringProblems().filter(p => problemScope(p) === scope)
  const [collapsed, setCollapsedState] = useState(() => localStorage.getItem(COLLAPSE_KEY + scope) === '1')
  const setCollapsed = (v: boolean) => { localStorage.setItem(COLLAPSE_KEY + scope, v ? '1' : '0'); setCollapsedState(v) }
  if (!list.length) return null
  return (
    <div className="mb-6 rounded-2xl surface !bg-t-red text-ink overflow-hidden" role="alert">
      <button type="button" onClick={() => setCollapsed(!collapsed)} aria-expanded={!collapsed}
        className={`w-full px-5 h-11 flex items-center gap-2 text-[13.5px] font-extrabold text-left hover:bg-ink/5 ${collapsed ? '' : 'border-b-2 border-[var(--edge)]'}`}>
        <AlertTriangle size={16} />{t(`${list.length} 个异常需要处理`, `${list.length} problem${list.length > 1 ? 's' : ''} to look at`)}
        <span className="ml-auto flex items-center gap-1 text-[12px] font-semibold opacity-70">{collapsed ? t('展开', 'Show') : t('收起', 'Hide')}<ChevronDown size={15} className={collapsed ? '' : 'rotate-180'} /></span>
      </button>
      {!collapsed && <div className="divide-y divide-black/15">
        {list.map(p => (
          <Link key={p.id} to={problemHref(p)} className="px-5 py-2.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[13px] hover:bg-ink/5">
            <span className="shrink-0">{ICON[p.kind] ?? <AlertTriangle size={15} />}</span>
            <span className="font-bold shrink-0">{problemLabel(p.kind)}</span>
            <span className="font-mono font-semibold truncate max-w-[40%]">{p.subject_name}</span>
            <span className="min-w-0 flex-1 truncate opacity-80 @max-2xl:basis-full @max-2xl:order-last @max-2xl:whitespace-normal @max-2xl:pl-7">{p.message}</span>
            <span className="shrink-0 text-[12px] opacity-70 tnum">{ago(p.since)}</span>
          </Link>
        ))}
      </div>}
    </div>
  )
}
