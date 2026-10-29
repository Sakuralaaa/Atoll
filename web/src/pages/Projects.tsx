import { Link } from 'react-router-dom'
import { ArrowUpDown, LayoutGrid, List, MoreVertical } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Button, Dot, Mini, ServiceIcon } from '../components/ui'
import { projects, serverById } from '../mock'

export default function Projects() {
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-14 pt-12 pb-16">
        <div className="flex items-center justify-between mb-8">
          <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">我的项目</h1>
          <div className="flex items-center gap-3">
            <button className="h-11 w-11 grid place-items-center text-fg2"><ArrowUpDown size={18} /></button>
            <div className="flex rounded-lg field p-0.5"><span className="h-8 w-10 grid place-items-center rounded-md nav-active"><LayoutGrid size={16} /></span><span className="h-8 w-10 grid place-items-center text-muted"><List size={16} /></span></div>
            <Button variant="primary" size="lg" className="w-[200px]">创建项目</Button>
          </div>
        </div>
        <div className="grid grid-cols-3 gap-4">
          {projects.map(p => {
            const srv = serverById(p.serverId)
            const run = p.services.filter(s => s.status === 'running').length
            const bad = p.services.some(s => s.status === 'failed')
            return (
              <Link key={p.id} to={`/projects/${p.id}/services/${p.services[0].id}`} className="group rounded-2xl surface surface-hover overflow-hidden">
                <div className="px-5 pt-5">
                  <div className="flex items-center gap-3">
                    <ServiceIcon s={p.services[0]} size={38} />
                    <div className="min-w-0 flex-1">
                      <div className="text-[15px] font-semibold truncate">{p.name}</div>
                      <div className="text-[12px] text-muted truncate mt-0.5">{p.services.find(s => s.domain)?.domain ?? '仅内网访问'}</div>
                    </div>
                    <MoreVertical size={15} className="text-dim opacity-0 group-hover:opacity-100 transition-opacity" />
                  </div>
                  <div className="mt-5 flex items-center gap-1.5">
                    {p.services.map(s => <span key={s.id} title={s.name}><ServiceIcon s={s} size={22} /></span>)}
                  </div>
                </div>
                <div className="mt-5 px-5 h-11 border-t border-line bg-sunk/60 flex items-center gap-2 text-[12.5px]">
                  <Mini provider={srv.provider} size={13} /><span className="text-fg2 truncate">{srv.name}</span>
                  <span className="ml-auto flex items-center gap-2 text-muted tnum"><Dot status={bad ? 'failed' : run === p.services.length ? 'running' : 'deploying'} />{run}/{p.services.length}</span>
                </div>
              </Link>
            )
          })}
        </div>
      </div>
    </MainShell>
  )
}
