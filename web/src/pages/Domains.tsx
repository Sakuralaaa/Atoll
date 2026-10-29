import { Cloud, Plus, ExternalLink, ShieldCheck } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, Mini } from '../components/ui'
import { projects, serverById } from '../mock'

export default function Domains() {
  const rows = projects.flatMap(p => p.services.filter(s => s.domain).map(s => ({ p, s, srv: serverById(p.serverId) })))
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-14 pt-12 pb-16">
        <div className="flex items-center justify-between mb-8"><h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">域名</h1><Button variant="primary" size="lg"><Plus size={16} />添加主域名</Button></div>
        <Card className="p-6 flex items-center gap-5 mb-8">
          <div className="h-12 w-12 rounded-xl bg-[#f6821f]/15 grid place-items-center"><Cloud size={24} className="text-[#f6821f]" /></div>
          <div className="flex-1"><div className="flex items-center gap-2 text-[15px] font-semibold">example.com<Badge tone="green">已连接</Badge></div><div className="text-[13.5px] text-muted mt-1">Cloudflare · 代理模式 · Origin CA 证书 · 每个服务绑定 *.example.com 子域名时自动建记录</div></div>
          <div className="text-right text-[13px] text-muted"><div>{rows.length} 条记录由 Atoll 管理</div><div className="mt-1 flex items-center gap-1 justify-end text-emerald-300"><ShieldCheck size={13} />API 令牌权限：Zone.DNS 编辑</div></div>
        </Card>
        <Card className="overflow-hidden">
          <div className="grid grid-cols-[1.4fr_1fr_1.4fr_1fr_1fr_40px] px-6 h-11 items-center text-[12.5px] text-muted border-b border-line"><span>域名</span><span>服务</span><span>指向服务器</span><span>记录</span><span>状态</span><span /></div>
          {rows.map(({ p, s, srv }) => (
            <div key={s.domain} className="grid grid-cols-[1.4fr_1fr_1.4fr_1fr_1fr_40px] px-6 h-14 items-center text-[13px] border-b border-line last:border-0">
              <span className="font-semibold">{s.domain}</span><span className="text-fg2">{p.name} / {s.name}</span>
              <span className="flex items-center gap-2 text-fg2"><Mini provider={srv.provider} />{srv.name}</span>
              <span className="font-mono text-[12.5px] text-muted">A {srv.ip}</span>
              <span>{s.status === 'failed' ? <Badge tone="red">后端异常</Badge> : <Badge tone="green">证书有效</Badge>}</span>
              <ExternalLink size={15} className="text-muted" />
            </div>
          ))}
        </Card>
      </div>
    </MainShell>
  )
}
