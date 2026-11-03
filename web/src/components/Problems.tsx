import { Link } from 'react-router-dom'
import { AlertTriangle, ArchiveX, Globe, HardDrive, Rocket, ServerOff, ShieldAlert, Siren } from 'lucide-react'
import { problemHref, problemLabel, useFiringProblems } from '../api/problems'
import { ago } from '../api/view'

const ICON: Record<string, React.ReactNode> = {
  node_offline: <ServerOff size={15} />, disk_high: <HardDrive size={15} />, service_down: <Siren size={15} />,
  deploy_failed: <Rocket size={15} />, backup_failed: <ArchiveX size={15} />, backup_verify_failed: <ShieldAlert size={15} />, domain_error: <Globe size={15} />,
}

/** Firing problems, pinned above a page's content; renders nothing when all is well.
 *  `only` keeps the kinds a page is about (servers page: node_offline, disk_high). */
export function ProblemsBanner({ only }: { only?: string[] }) {
  const list = useFiringProblems(only)
  if (!list.length) return null
  return (
    <div className="mb-6 rounded-2xl surface !bg-[#fca5a5] text-[#141414] overflow-hidden" role="alert">
      <div className="px-5 h-11 flex items-center gap-2 text-[13.5px] font-extrabold border-b-2 border-[var(--edge)]"><AlertTriangle size={16} />{list.length} 个异常需要处理</div>
      <div className="divide-y divide-black/15">
        {list.map(p => (
          <Link key={p.id} to={problemHref(p)} className="px-5 py-2.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[13px] hover:bg-black/5">
            <span className="shrink-0">{ICON[p.kind] ?? <AlertTriangle size={15} />}</span>
            <span className="font-bold shrink-0">{problemLabel(p.kind)}</span>
            <span className="font-mono font-semibold truncate max-w-[40%]">{p.subject_name}</span>
            <span className="min-w-0 flex-1 truncate opacity-80 @max-2xl:basis-full @max-2xl:order-last @max-2xl:whitespace-normal @max-2xl:pl-7">{p.message}</span>
            <span className="shrink-0 text-[12px] opacity-70 tnum">{ago(p.since)}</span>
          </Link>
        ))}
      </div>
    </div>
  )
}
