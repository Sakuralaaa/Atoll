import { useState } from 'react'
import { IP } from '../components/IP'
import { svcName } from '../api/view'
import { Link } from 'react-router-dom'
import { Cloud, CloudOff, Plus, ExternalLink, ShieldCheck, ShieldAlert, Globe, Trash2, Settings2, ArrowRight, Loader2 } from 'lucide-react'
import { MainShell } from '../components/Shell'
import { Badge, Button, Card, Mini } from '../components/ui'
import { ConfirmDialog, Empty, ErrorState, Label, Modal, RowsSkeleton, Select, Skeleton } from '../components/feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import type { CloudflareTokenCheck, Domain, Project, RootDomain, Service } from '../api/types'
import { DomainBadge, DomainNote } from '../components/DomainStatus'
import { providerOf } from '../api/providers'
import { useProjects } from '../api/queries'
import { HostnameInput, useHostnameInput } from '../components/HostnameInput'
import { useT } from '../i18n'
import { ProxyChoice } from '../components/ProxyChoice'


export default function Domains() {
  const q = useQuery('domains', api.domains, { interval: 15000 })
  const projects = useProjects().data
  const servers = useQuery('servers', api.servers).data
  const settings = useQuery('settings', api.settings).data
  const roots = useQuery('root-domains', api.rootDomains).data
  const [add, setAdd] = useState(false)
  const [del, setDel] = useState<Domain>()
  const [flip, setFlip] = useState<Domain>()
  const svcOf = (d: Domain) => {
    for (const p of projects ?? []) { const s = p.services?.find(x => x.id === d.service_id); if (s) return { p, s } }
    return undefined
  }
  const cfOk = !!settings?.base_domain && settings.has_cloudflare_token
  const perms = useQuery(cfOk ? 'cf-token-check' : null, () => api.cloudflareTokenCheck()).data
  return (
    <MainShell>
      <div className="max-w-[1240px] mx-auto px-4 pt-6 pb-12 @2xl:px-8 @2xl:pt-10 @5xl:px-14 @5xl:pt-12 @5xl:pb-16">
        <div className="flex flex-wrap items-center justify-between gap-3 mb-8"><h1 className="text-[26px] font-extrabold italic tracking-[-0.03em]">域名</h1><Button variant="primary" size="lg" onClick={() => setAdd(true)}><Plus size={16} />绑定域名</Button></div>
        {!settings ? <Card className="p-6 mb-8"><Skeleton className="h-12 w-full" /></Card> : (
          <Card className="mb-8 overflow-hidden">
            <div className="p-5 @2xl:p-6 flex flex-wrap @3xl:flex-nowrap items-center gap-5">
              <div className="tile h-12 w-12 rounded-lg bg-[#fdba74] text-[#141414] grid place-items-center shrink-0"><Cloud size={24} /></div>
              <div className="flex-1 min-w-[min(100%,240px)]">
                <div className="flex flex-wrap items-center gap-2 text-[15px] font-semibold [overflow-wrap:anywhere]">{settings.base_domain || '未设置根域名'}{(roots?.length ?? 0) > 1 && <span className="text-[13px] font-normal text-muted">+{roots!.length - 1} 个根域名</span>}{cfOk ? <Badge tone="green">已连接</Badge> : <Badge tone="amber">未连接</Badge>}</div>
                <div className="text-[13px] text-muted mt-1">{cfOk ? `Cloudflare · ${settings.tls_mode === 'acme' ? 'ACME 证书' : 'Origin CA 证书'} · 绑定根域名下的域名时自动创建 DNS 记录` : '在设置里添加根域名和 Cloudflare API 令牌后，绑定域名时会自动建 DNS 记录。'}</div>
              </div>
              {cfOk ? <div className="@3xl:text-right text-[13px] text-muted shrink-0 tnum @max-3xl:w-full @max-3xl:pl-[68px]">{q.data?.length ?? '—'} 条记录由 Atoll 管理</div>
                : <Link to="/settings"><Button><Settings2 size={14} />去设置<ArrowRight size={14} /></Button></Link>}
            </div>
            {cfOk && <TokenPerms c={perms} />}
          </Card>
        )}
        {q.error && !q.data && <ErrorState error={q.error} onRetry={q.refetch} />}
        {!q.data && !q.error && <RowsSkeleton n={4} />}
        {q.data && !q.data.length && <Empty icon={<Globe size={22} />} title="还没有绑定域名" desc="给有 HTTP 端口的服务绑定域名后，atoll-caddy 会自动签发证书并反代到容器。" action={<Button variant="primary" onClick={() => setAdd(true)}><Plus size={15} />绑定域名</Button>} />}
        {q.data && q.data.length > 0 && (
          <Card className="overflow-hidden">
            <div className="hidden @4xl:grid grid-cols-[minmax(0,1.5fr)_minmax(0,1.2fr)_minmax(0,1.5fr)_minmax(0,1fr)_110px_72px] gap-4 px-6 h-11 items-center text-[12.5px] text-muted border-b-2 border-[var(--edge)]"><span>域名</span><span>服务</span><span>指向服务器</span><span>记录</span><span>状态</span><span /></div>
            {q.data.map(d => {
              const o = svcOf(d), srv = servers?.find(x => x.id === o?.p.server_id)
              return (
                <div key={d.id} className="flex flex-wrap @4xl:grid @4xl:grid-cols-[minmax(0,1.5fr)_minmax(0,1.2fr)_minmax(0,1.5fr)_minmax(0,1fr)_110px_72px] gap-x-4 gap-y-1.5 px-4 @2xl:px-6 min-h-14 py-3 @4xl:py-2 items-center text-[13px] border-b border-line last:border-0">
                  <div className="min-w-0 @max-4xl:flex-1"><div className="font-semibold truncate" title={d.hostname}>{d.hostname}</div>
                    <div className="flex flex-wrap items-center gap-1.5 mt-1"><CloudTag d={d} onClick={cfOk ? () => setFlip(d) : undefined} /><CertTag d={d} /></div>
                    <DomainNote d={d} /></div>
                  {o ? <Link to={`/projects/${o.p.id}/services/${o.s.id}?tab=network`} className="text-fg2 hover:text-fg truncate @max-4xl:order-2 @max-4xl:basis-full" title={svcLabel(o.p, o.s)}>{svcLabel(o.p, o.s)}</Link> : <span className="text-dim @max-4xl:order-2 @max-4xl:basis-full">—</span>}
                  <span className="flex items-center gap-2 text-fg2 min-w-0 @max-4xl:order-3 @max-4xl:max-w-full">{srv ? <><Mini provider={providerOf(srv)} /><span className="truncate">{srv.name}</span></> : <span className="text-dim">—</span>}</span>
                  <span className="font-mono text-[12.5px] text-muted truncate @max-4xl:order-3">{srv ? <>A <IP v={srv.public_ipv4} /></> : '—'}</span>
                  <span className="@max-4xl:order-3"><DomainBadge d={d} /></span>
                  <span className="flex items-center gap-1 justify-end @max-4xl:order-1 @max-4xl:-my-1.5">
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
      {flip && <ProxyFlip d={flip} roots={roots ?? []} onClose={() => setFlip(undefined)} />}
    </MainShell>
  )
}

/** "project / service", just one name when both are the same (qa-bot / qa-bot). */
function svcLabel(p: Project, s: Service) {
  const n = svcName(s)
  return p.name === n ? n : `${p.name} / ${n}`
}

const tagCls = 'inline-flex items-center gap-1 h-[20px] px-1.5 rounded-[3px] border-[1.5px] border-[var(--edge)] text-[11px] font-bold whitespace-nowrap'

/** Orange / grey cloud tag with the price of each; clickable (switch the proxy) when Atoll manages the record. */
function CloudTag({ d, onClick }: { d: Domain; onClick?: () => void }) {
  const { t } = useT()
  const tip = d.proxied
    ? t('Cloudflare 代理（橙云）：隐藏源站 IP、有防护；单个请求 100 秒超时，只代理 HTTP(S)', 'Proxied by Cloudflare (orange cloud): hides the origin IP, protected; 100-second request timeout, HTTP(S) only')
    : t('仅 DNS（灰云）：直连服务器，没有超时限制、任何协议都行；会暴露源站 IP', 'DNS only (grey cloud): straight to the server, no timeout, any protocol; exposes the origin IP')
  const body = <>
    <span className={`${tagCls} ${d.proxied ? 'bg-[#fdba74] text-[#141414]' : 'bg-card2 text-fg2'}`}>{d.proxied ? <Cloud size={11} strokeWidth={2.5} /> : <CloudOff size={11} strokeWidth={2.5} />}{d.proxied ? t('橙云', 'Proxied') : t('灰云', 'DNS only')}</span>
    <span className={`text-[11.5px] ${d.proxied ? 'text-dim' : 'text-[#d97706]'}`}>{d.proxied ? t('100 秒超时', '100 s timeout') : t('会暴露源站 IP', 'exposes origin IP')}</span>
  </>
  if (!onClick) return <span className="inline-flex items-center gap-1.5" title={tip}>{body}</span>
  return <button type="button" onClick={onClick} title={`${tip}\n${t('点击切换', 'Click to switch')}`} className="inline-flex items-center gap-1.5 rounded-[3px] hover:opacity-80">{body}</button>
}

/** Certificate the servers present for the host. */
function CertTag({ d }: { d: Domain }) {
  const { t } = useT()
  if (d.cert === 'origin' && !d.proxied) return <span className={`${tagCls} bg-[#fcd34d] text-[#141414]`} title={t('直连时浏览器不信任 Cloudflare Origin CA 证书：把这个根域名的证书模式改为「自动」或 ACME', 'Browsers reject a Cloudflare Origin CA certificate on a direct connection: set this root domain to "Auto" or ACME')}><ShieldAlert size={11} />Origin CA</span>
  if (d.cert === 'origin') return <span className={`${tagCls} bg-[#86efac] text-[#141414]`} title={t('Cloudflare Origin CA 证书（只有 Cloudflare 信任，配合橙云使用）', 'Cloudflare Origin CA certificate (trusted by Cloudflare only, for proxied hosts)')}>Origin CA</span>
  if (d.cert === 'acme') return <span className={`${tagCls} bg-[#c4b5fd] text-[#141414]`} title={t('ACME 证书（Let\'s Encrypt，浏览器直接信任）', 'ACME certificate (Let\'s Encrypt, trusted by browsers)')}>ACME</span>
  if (d.cert === 'http') return <span className={`${tagCls} bg-card2 text-fg2`} title={t('服务器上没有证书：只能经 Cloudflare 以 HTTP 回源', 'No certificate on the server: Cloudflare reaches it over HTTP')}>HTTP</span>
  return null
}

/** Domains page header: the token permissions Atoll needs, as probed by the server — a strip of
 *  chips under the Cloudflare card, with the consequence of a missing permission spelled out below. */
function TokenPerms({ c }: { c?: CloudflareTokenCheck }) {
  const { t } = useT()
  const chip = (state: string | undefined, label: string, warn: string, err?: string) => {
    const ok = state === 'ok', missing = state === 'missing'
    return (
      <span className={`inline-flex items-center gap-1.5 h-7 px-2.5 rounded-md border text-[12.5px] whitespace-nowrap ${missing ? 'border-[#d97706]/50 bg-[#fcd34d]/20 text-[#b45309] dark:text-[#fbbf24]' : 'border-line bg-sunk text-fg2'}`} title={missing ? `${warn}${err ? `\n${err}` : ''}` : err}>
        {!c ? <Loader2 size={13} className="animate-spin text-muted" /> : ok ? <ShieldCheck size={13} className="text-emerald-500" /> : <ShieldAlert size={13} className={missing ? '' : 'text-muted'} />}
        {label}{c && !ok && <span className="font-semibold">{missing ? t(' · 缺失', ' · missing') : t(' · 未知', ' · unknown')}</span>}
      </span>
    )
  }
  return (
    <div className="px-5 @2xl:px-6 py-3.5 border-t border-line bg-card2 flex flex-col gap-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[12.5px] text-muted mr-1">{t('令牌权限', 'Token permissions')}</span>
        {chip(c?.dns, t('Zone.DNS 编辑', 'Zone.DNS Edit'), t('缺少 Zone.DNS 编辑权限：绑定域名时无法自动建 DNS 记录', 'Missing Zone.DNS Edit: DNS records cannot be created when binding a domain'), c?.dns_error)}
        {c?.ssl && chip(c.ssl, t('SSL 证书编辑', 'SSL and Certificates Edit'), t(`缺少 Zone.SSL and Certificates 编辑权限：${c.ssl_roots?.join('、')} 签 Origin CA 证书会报 1016，只能退回 ACME`, `Missing Zone.SSL and Certificates Edit: issuing Origin CA certificates for ${c.ssl_roots?.join(', ')} fails with 1016 and falls back to ACME`), c.ssl_error)}
      </div>
      {(c?.dns === 'missing' || c?.ssl === 'missing') && <div className="flex items-start gap-2 text-[12.5px] leading-5 text-[#b45309] dark:text-[#fbbf24]">
        <ShieldAlert size={14} className="mt-0.5 shrink-0" />
        <div className="min-w-0 space-y-0.5">
          {c.dns === 'missing' && <div>{t('缺少 Zone.DNS 编辑权限：绑定域名时无法自动建 DNS 记录，需要在 Cloudflare 给令牌补上。', 'Missing Zone.DNS Edit: DNS records cannot be created when binding a domain; add it to the token in Cloudflare.')}</div>}
          {c.ssl === 'missing' && <div>{t(`根域名用 Origin CA 时令牌还需要「SSL and Certificates：编辑」，否则${c.ssl_roots?.length ? ` ${c.ssl_roots.join('、')} ` : ''}签证书会报 1016，只能退回 ACME。`, `Origin CA roots${c.ssl_roots?.length ? ` (${c.ssl_roots.join(', ')})` : ''} also need "SSL and Certificates: Edit", otherwise issuing fails with 1016 and falls back to ACME.`)}</div>}
        </div>
      </div>}
    </div>
  )
}

/** Confirm a proxy switch; says what happens to the certificate (grey cloud under an Origin CA root gets ACME). */
function ProxyFlip({ d, roots, onClose }: { d: Domain; roots: RootDomain[]; onClose: () => void }) {
  const { t } = useT()
  const to = !d.proxied
  const root = roots.filter(r => d.hostname === r.name || d.hostname.endsWith('.' + r.name)).sort((a, b) => b.name.length - a.name.length)[0]
  const originRoot = !!root && root.cert_mode !== 'global' && root.cert_status === 'origin'
  const globalOrigin = (!root || root.cert_mode === 'global') && d.cert === 'origin'
  return (
    <ConfirmDialog danger={false} title={to ? t('切换为橙云（Cloudflare 代理）', 'Switch to proxied (orange cloud)') : t('切换为灰云（直连）', 'Switch to DNS only (grey cloud)')}
      action={t('确认切换', 'Switch')} onClose={onClose}
      onConfirm={() => run(() => api.setDomainProxied(d.id, to), { ok: t('已切换，DNS 与证书生效中', 'Switched: DNS and certificate are updating'), invalidate: ['domains', 'project', 'projects'] })}
      description={<>
        <div>{t('将修改 Cloudflare 上', 'Updates the Cloudflare record of')} <b>{d.hostname}</b> {t('的记录。', '.')}{to
          ? t('走 Cloudflare 后会隐藏源站 IP、有防护，但单个请求 100 秒超时，且只代理 HTTP(S)。', 'Through Cloudflare the origin IP is hidden and protected, but requests time out after 100 seconds and only HTTP(S) is proxied.')
          : t('直连后没有 100 秒超时、可用于长请求和非 HTTP 协议，但会暴露源站 IP，也没有 Cloudflare 防护。', 'Direct: no 100-second timeout, fine for long requests and non-HTTP, but the origin IP is exposed and Cloudflare protection is gone.')}</div>
        {!to && originRoot && <div className="mt-3 text-[13px] text-fg2">{t('根域名 ', 'Root ')}<b>{root!.name}</b>{t(' 用的是 Cloudflare Origin CA 证书，浏览器直连时不信任：这个域名会改用 ACME（Let\'s Encrypt）证书，DNS 生效后自动签发，可能需要一两分钟。', ' uses a Cloudflare Origin CA certificate, which browsers reject on a direct connection: this host switches to an ACME (Let\'s Encrypt) certificate, issued once DNS has updated (a minute or two).')}</div>}
        {to && originRoot && <div className="mt-3 text-[13px] text-fg2">{t('这个域名会改回根域名的 Origin CA 证书。', 'This host goes back to the root\'s Origin CA certificate.')}</div>}
        {!to && globalOrigin && <div className="mt-3 text-[13px] text-[#d97706]">{t('这个域名跟随全局证书（Origin CA），直连时浏览器会报证书错误。请先在 设置 → 根域名 把它的证书模式改为「自动」或 ACME。', 'This host follows the global certificate (Origin CA): browsers will show a certificate error on a direct connection. Set its root to "Auto" or ACME in Settings → Root domains first.')}</div>}
      </>} />
  )
}

export function AddDomain({ projects, base, onClose, fixed }: { projects: Project[]; base?: string; onClose: () => void; fixed?: Service }) {
  const opts = fixed ? [{ p: undefined as Project | undefined, s: fixed }] : projects.flatMap(p => (p.services ?? []).filter(s => s.ports?.some(x => x.protocol === 'http')).map(s => ({ p, s })))
  const [sid, setSid] = useState(opts[0]?.s.id ?? '')
  const cur = opts.find(o => o.s.id === sid)?.s
  const httpPorts = cur?.ports?.filter(x => x.protocol === 'http') ?? []
  const [port, setPort] = useState('')
  const projectRoot = (fixed ? projects.find(p => p.id === fixed.project_id) : opts.find(o => o.s.id === sid)?.p)?.root_domain
  const h = useHostnameInput(projectRoot)
  const [proxied, setProxied] = useState(true)
  const [busy, wrap] = useBusy()
  const portName = port || httpPorts[0]?.name || ''
  const ok = !!cur && !!portName && h.v.ok
  const submit = () => wrap(async () => {
    if (!ok || !cur) return
    const r = await run(() => api.addDomain(cur.id, { hostname: h.v.hostname, root_domain: h.v.root_domain, port_name: portName, proxied }), { ok: '域名已绑定，DNS 与证书生效中', invalidate: ['domains', `project:${cur.project_id}`, 'projects'] })
    if (r) onClose()
  })
  return (
    <Modal title="绑定域名" onClose={onClose} footer={<><Button size="sm" onClick={onClose}>取消</Button><Button size="sm" variant="primary" disabled={!ok || busy} onClick={submit}>{busy && <Loader2 size={13} className="animate-spin" />}绑定</Button></>}>
      {!opts.length ? <div className="text-[13px] text-muted">还没有带 HTTP 端口的服务。</div> : <>
        {!fixed && <Label t="服务"><Select className="w-full" value={sid} onChange={e => { setSid(e.target.value); setPort('') }}>{opts.map(o => <option key={o.s.id} value={o.s.id}>{o.p ? svcLabel(o.p, o.s) : svcName(o.s)}</option>)}</Select></Label>}
        <Label t="域名" d={h.names.length ? `根域名下的域名会自动在 Cloudflare 建记录；其他域名请手动把 A 记录指向服务器 IP。${h.v.ok && h.v.full ? ` 将绑定 ${h.v.full}` : ''}` : base ? `${base} 的子域名会自动在 Cloudflare 建记录；其他域名请手动把 A 记录指向服务器 IP。` : '请把域名的 A 记录指向服务器 IP。'}>
          <HostnameInput h={h} autoFocus onEnter={submit} />
        </Label>
        {httpPorts.length > 1 && <Label t="端口"><Select className="w-full" value={portName} onChange={e => setPort(e.target.value)}>{httpPorts.map(x => <option key={x.name} value={x.name}>{x.name} · {x.container_port}</option>)}</Select></Label>}
        <ProxyChoice proxied={proxied} onChange={setProxied} />
      </>}
    </Modal>
  )
}
