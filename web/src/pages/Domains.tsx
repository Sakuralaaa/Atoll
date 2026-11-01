import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Cloud, Plus, ExternalLink, ShieldCheck, Globe, Trash2, Settings2, ArrowRight, Loader2 } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, Mini } from '../components/ui'
import { ConfirmDialog, Empty, ErrorState, Label, Modal, RowsSkeleton, Select, Skeleton, TextInput, Toggle } from '../components/feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import type { Domain, Project, Service } from '../api/types'
import { useProjects } from './Projects'
import { DomainBadge, DomainNote } from '../components/DomainStatus'


export default function Domains() {
  const q = useQuery('domains', api.domains, { interval: 15000 })
  const projects = useProjects().data
  const servers = useQuery('servers', api.servers).data
  const settings = useQuery('settings', api.settings).data
  const [add, setAdd] = useState(false)
  const [del, setDel] = useState<Domain>()
  const svcOf = (d: Domain) => {
    for (const p of projects ?? []) { const s = p.services?.find(x => x.id === d.service_id); if (s) return { p, s } }
    return undefined
  }
  const cfOk = !!settings?.base_domain && settings.has_cloudflare_token
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-14 pt-12 pb-16">
        <div className="flex items-center justify-between mb-8"><h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">域名</h1><Button variant="primary" size="lg" onClick={() => setAdd(true)}><Plus size={16} />绑定域名</Button></div>
        {!settings ? <Card className="p-6 mb-8"><Skeleton className="h-12 w-full" /></Card> : (
          <Card className="p-6 flex items-center gap-5 mb-8">
            <div className="tile h-12 w-12 rounded-lg bg-[#fdba74] text-[#141414] grid place-items-center shrink-0"><Cloud size={24} /></div>
            <div className="flex-1 min-w-0">
              <div className="flex items-center gap-2 text-[15px] font-semibold">{settings.base_domain || '未设置主域名'}{cfOk ? <Badge tone="green">已连接</Badge> : <Badge tone="amber">未连接</Badge>}</div>
              <div className="text-[13px] text-muted mt-1">{cfOk ? `Cloudflare · ${settings.tls_mode === 'acme' ? 'ACME 证书' : 'Origin CA 证书'} · 绑定 *.${settings.base_domain} 子域名时自动创建 DNS 记录` : '设置主域名和 Cloudflare API 令牌后，绑定域名时会自动建 DNS 记录。'}</div>
            </div>
            {cfOk ? <div className="text-right text-[13px] text-muted shrink-0"><div className="tnum">{q.data?.length ?? '—'} 条记录由 Atoll 管理</div><div className="mt-1 flex items-center gap-1 justify-end text-fg2"><ShieldCheck size={13} className="text-emerald-500" />令牌权限：Zone.DNS 编辑</div></div>
              : <Link to="/settings"><Button><Settings2 size={14} />去设置<ArrowRight size={14} /></Button></Link>}
          </Card>
        )}
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <RowsSkeleton n={4} />}
        {q.data && !q.data.length && <Empty icon={<Globe size={22} />} title="还没有绑定域名" desc="给有 HTTP 端口的服务绑定域名后，atoll-caddy 会自动签发证书并反代到容器。" action={<Button variant="primary" onClick={() => setAdd(true)}><Plus size={15} />绑定域名</Button>} />}
        {q.data && q.data.length > 0 && (
          <Card className="overflow-hidden">
            <div className="grid grid-cols-[1.5fr_1.2fr_1.5fr_1fr_110px_72px] gap-4 px-6 h-11 items-center text-[12.5px] text-muted border-b-2 border-[var(--edge)]"><span>域名</span><span>服务</span><span>指向服务器</span><span>记录</span><span>状态</span><span /></div>
            {q.data.map(d => {
              const o = svcOf(d), srv = servers?.find(x => x.id === o?.p.server_id)
              return (
                <div key={d.id} className="grid grid-cols-[1.5fr_1.2fr_1.5fr_1fr_110px_72px] gap-4 px-6 min-h-14 py-2 items-center text-[13px] border-b border-line last:border-0">
                  <div className="min-w-0"><div className="font-semibold truncate">{d.hostname}</div><DomainNote d={d} /></div>
                  {o ? <Link to={`/projects/${o.p.id}/services/${o.s.id}?tab=network`} className="text-fg2 hover:text-fg truncate">{o.p.name} / {o.s.name}</Link> : <span className="text-dim">—</span>}
                  <span className="flex items-center gap-2 text-fg2 min-w-0">{srv ? <><Mini provider={srv.provider ?? '?'} /><span className="truncate">{srv.name}</span></> : <span className="text-dim">—</span>}</span>
                  <span className="font-mono text-[12.5px] text-muted truncate">{srv ? `A ${srv.public_ipv4}` : '—'}{d.proxied ? '' : ' · 直连'}</span>
                  <span><DomainBadge d={d} /></span>
                  <span className="flex items-center gap-1 justify-end">
                    <a href={`https://${d.hostname}`} target="_blank" rel="noreferrer" title="打开" className="h-8 w-8 grid place-items-center text-muted hover:text-fg"><ExternalLink size={15} /></a>
                    <button title="解绑" onClick={() => setDel(d)} className="h-8 w-8 grid place-items-center text-muted hover:text-[#ef4444]"><Trash2 size={15} /></button>
                  </span>
                </div>
              )
            })}
          </Card>
        )}
      </div>
      {add && <AddDomain projects={projects ?? []} base={settings?.base_domain} onClose={() => setAdd(false)} />}
      {del && <ConfirmDialog title="解绑域名" action="确认解绑" name={del.hostname} onClose={() => setDel(undefined)}
        description={<>将从 atoll-caddy 移除 <b>{del.hostname}</b> 的路由，并删除 Atoll 创建的 Cloudflare DNS 记录。</>}
        onConfirm={() => run(() => api.deleteDomain(del.id), { ok: '已解绑', invalidate: ['domains', 'project', 'projects'] })} />}
    </MainShell>
  )
}

export function AddDomain({ projects, base, onClose, fixed }: { projects: Project[]; base?: string; onClose: () => void; fixed?: Service }) {
  const opts = fixed ? [{ p: undefined as Project | undefined, s: fixed }] : projects.flatMap(p => (p.services ?? []).filter(s => s.ports?.some(x => x.protocol === 'http')).map(s => ({ p, s })))
  const [sid, setSid] = useState(opts[0]?.s.id ?? '')
  const cur = opts.find(o => o.s.id === sid)?.s
  const httpPorts = cur?.ports?.filter(x => x.protocol === 'http') ?? []
  const [port, setPort] = useState('')
  const [host, setHost] = useState('')
  const [proxied, setProxied] = useState(true)
  const [busy, wrap] = useBusy()
  const portName = port || httpPorts[0]?.name || ''
  const ok = !!cur && !!portName && /^[a-z0-9.-]+\.[a-z]{2,}$/i.test(host.trim())
  const submit = () => wrap(async () => {
    if (!ok || !cur) return
    const r = await run(() => api.addDomain(cur.id, { hostname: host.trim().toLowerCase(), port_name: portName, proxied }), { ok: '域名已绑定，DNS 与证书生效中', invalidate: ['domains', `project:${cur.project_id}`, 'projects'] })
    if (r) onClose()
  })
  return (
    <Modal title="绑定域名" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!ok || busy} onClick={submit}>{busy && <Loader2 size={13} className="animate-spin" />}绑定</Button></>}>
      {!opts.length ? <div className="text-[13px] text-muted">还没有带 HTTP 端口的服务。</div> : <>
        {!fixed && <Label t="服务"><Select className="w-full" value={sid} onChange={e => { setSid(e.target.value); setPort('') }}>{opts.map(o => <option key={o.s.id} value={o.s.id}>{o.p?.name} / {o.s.name}</option>)}</Select></Label>}
        <Label t="域名" d={base ? `${base} 的子域名会自动在 Cloudflare 建记录；其他域名请手动把 A 记录指向服务器 IP。` : '请把域名的 A 记录指向服务器 IP。'}>
          <TextInput autoFocus mono className="w-full" value={host} onChange={e => setHost(e.target.value)} onKeyDown={e => e.key === 'Enter' && submit()} placeholder={base ? `app.${base}` : 'app.example.com'} />
        </Label>
        {httpPorts.length > 1 && <Label t="端口"><Select className="w-full" value={portName} onChange={e => setPort(e.target.value)}>{httpPorts.map(x => <option key={x.name} value={x.name}>{x.name} · {x.container_port}</option>)}</Select></Label>}
        <div className="flex items-center gap-3 mt-1"><Toggle on={proxied} onChange={setProxied} /><div><div className="text-[13px] font-semibold">Cloudflare 代理（橙色云朵）</div><div className="text-[12px] text-dim">隐藏服务器 IP，走 Cloudflare CDN</div></div></div>
      </>}
    </Modal>
  )
}
