import { Link } from 'react-router-dom'
import { ArrowUpDown, LayoutGrid, List, MoreVertical, AlertCircle, Server } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, ProviderIcon, Segment, Ticks } from '../components/ui'
import { servers } from '../mock'

export default function Servers() {
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-14 pt-12 pb-16">
        <div className="flex items-center justify-between mb-5">
          <h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">我的服务器</h1>
          <div className="flex items-center gap-3">
            <button className="h-11 w-11 grid place-items-center text-fg2"><ArrowUpDown size={18} /></button>
            <div className="flex rounded-lg field p-0.5"><span className="h-8 w-10 grid place-items-center rounded-md nav-active"><LayoutGrid size={16} /></span><span className="h-8 w-10 grid place-items-center text-muted"><List size={16} /></span></div>
            <Button variant="primary" size="lg" className="w-[200px]">添加服务器</Button>
          </div>
        </div>
        <Segment value="s" items={[{ k: 's', l: '服务器', icon: <Server size={15} /> }]} />
        <div className="grid grid-cols-3 gap-4 mt-6">
          {servers.map(s => (
            <Link key={s.id} to={`/servers/${s.id}`} className="rounded-2xl surface surface-hover overflow-hidden relative">
              <div className="absolute -right-6 top-6 w-48 h-36 dotgrid pointer-events-none" style={{ maskImage: 'radial-gradient(ellipse at 60% 50%, black 20%, transparent 70%)' }} />
              <div className="p-6 pb-5">
                <div className="flex items-start gap-3">
                  <ProviderIcon provider={s.provider} online={s.status === 'online'} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2"><span className="text-[15px] font-semibold truncate">{s.name}</span>
                      {s.attention && <Badge><AlertCircle size={12} />需要处理</Badge>}
                      {s.role === 'control' && <Badge tone="violet">中控</Badge>}
                    </div>
                    <div className="mt-1 flex items-center gap-3 text-[13.5px] text-fg2">
                      <span>{s.os}</span>
                      <span className="flex items-center gap-1.5"><span className={s.status === 'online' ? 'h-2 w-2 rounded-full bg-emerald-500' : 'h-2 w-2 rounded-full bg-zinc-500'} />{s.status === 'online' ? 'Docker' : '离线'}</span>
                    </div>
                  </div>
                  <MoreVertical size={16} className="text-dim" />
                </div>
                <div className="mt-6 grid grid-cols-2 text-[13px]">
                  <div><div className="text-muted">IP 地址</div><div className="font-mono text-[13px] mt-1.5">{s.ip}</div></div>
                  <div><div className="text-muted">区域</div><div className="mt-1.5 flex items-center gap-2">{s.flag} {s.region}</div></div>
                </div>
                <div className="mt-5 space-y-2.5 text-[13px]">
                  <div className="flex items-center gap-4"><span className="text-muted w-20 shrink-0 whitespace-nowrap">CPU 使用率</span><Ticks value={s.cpuUse} n={34} /></div>
                  <div className="flex items-center gap-4"><span className="text-muted w-20 shrink-0 whitespace-nowrap">内存使用率</span><Ticks value={s.memUse} n={34} /></div>
                </div>
              </div>
              <div className="border-t border-line bg-sunk/60 px-6 h-11 flex items-center text-[13.5px] text-muted">
                {s.role === 'control' ? '仅运行面板与调度，不承载项目' : `${s.cpu} vCPU · ${s.memGB} GB · ${s.diskGB} GB · ${s.arch}`}
              </div>
            </Link>
          ))}
        </div>
      </div>
    </MainShell>
  )
}
