import { useState } from 'react'
import { Globe2, Loader2, Plus, RefreshCw, Star, Trash2 } from 'lucide-react'
import { Badge, Button, Card, Hint } from './ui'
import { ConfirmDialog, Select, Skeleton, TextInput } from './feedback'
import { api } from '../api/client'
import { run, useBusy, useQuery } from '../api/hooks'
import type { RootDomain } from '../api/types'
import { useT } from '../i18n'

const INVALIDATE = ['root-domains', 'settings', 'domains', 'projects']

/** Settings → 根域名: the list of root domains (multi-domain), each with its Cloudflare zone and certificate. */
export default function RootDomainsSettings() {
  const { t } = useT()
  const q = useQuery('root-domains', api.rootDomains)
  const [name, setName] = useState('')
  const [busy, wrap] = useBusy()
  const [del, setDel] = useState<RootDomain>()
  const clean = name.trim().toLowerCase().replace(/\.$/, '')
  const valid = /^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z][a-z0-9-]*$/.test(clean)
  const add = () => wrap(async () => {
    if (!valid) return
    const r = await run(() => api.addRootDomain({ name: clean }), { ok: t('根域名已添加', 'Root domain added'), invalidate: INVALIDATE })
    if (r) setName('')
  })
  const list = q.data ?? []
  return (
    <div className="mb-8">
      <div className="text-[15px] font-semibold flex items-center gap-2 mb-3"><Globe2 size={17} />{t('根域名', 'Root domains')}
        <Hint>{t('服务的域名建在这些根域名下面。默认根域名用于没有指定根域名的项目；项目设置里可以单独选。Cloudflare Zone 按后缀自动匹配（最长的优先）。证书「自动」会用 API 令牌签发 Cloudflare Origin CA 证书（需要 SSL and Certificates 编辑权限），签不了就用 ACME。新根域名在 Cloudflare 的 SSL/TLS 模式请设为 Full (strict)，Flexible 会导致重定向循环。',
          'Service domains are created under these roots. The default root is used by projects that do not pick one (Project settings). The Cloudflare zone is matched by suffix (longest wins). "Auto" certificates are Cloudflare Origin CA certificates issued with the API token (needs SSL and Certificates: Edit), ACME when that fails. Set a new root\'s Cloudflare SSL/TLS mode to Full (strict): Flexible causes redirect loops.')}</Hint>
      </div>
      <Card className="divide-y divide-line">
        {!q.data && <div className="p-5 space-y-3"><Skeleton className="h-10 w-full" /><Skeleton className="h-10 w-full" /></div>}
        {q.data && !list.length && <div className="px-4 @2xl:px-6 py-5 text-[13px] text-muted">{t('还没有根域名。添加一个后，绑定域名时只填前缀即可。', 'No root domain yet. Add one and a bare prefix is enough when binding a domain.')}</div>}
        {list.map(r => <Row key={r.name} r={r} only={list.length === 1} onDelete={() => setDel(r)} />)}
        <div className="px-4 @2xl:px-6 py-4 flex flex-wrap items-center gap-3">
          <TextInput mono className="flex-1 min-w-[min(100%,240px)]" value={name} onChange={e => setName(e.target.value)} onKeyDown={e => e.key === 'Enter' && add()} placeholder="example.net" />
          <Button size="sm" variant="primary" disabled={!valid || busy} onClick={add}>{busy ? <Loader2 size={13} className="animate-spin" /> : <Plus size={14} />}{t('添加根域名', 'Add root domain')}</Button>
        </div>
      </Card>
      {del && <ConfirmDialog title={t('移除根域名', 'Remove root domain')} action={t('确认移除', 'Remove')} name={del.name} onClose={() => setDel(undefined)}
        description={<>{t('从列表移除', 'Removes')} <b>{del.name}</b>{t('，并吊销 Atoll 为它签发的 Origin 证书。还有域名在用它时无法移除。', ' from the list and revokes the Origin certificate Atoll issued for it. Not possible while domains still use it.')}</>}
        onConfirm={() => run(() => api.deleteRootDomain(del.name), { ok: t('已移除', 'Removed'), invalidate: INVALIDATE })} />}
    </div>
  )
}

function CertBadge({ r }: { r: RootDomain }) {
  const { t } = useT()
  if (r.cert_status === 'origin') return <Badge tone="green">Origin CA{r.cert_expires_at ? ` · ${t('至', 'until')} ${r.cert_expires_at.slice(0, 7)}` : ''}</Badge>
  if (r.cert_status === 'acme') return <Badge tone="violet">ACME</Badge>
  if (r.cert_status === 'global') return <Badge tone="zinc">{t('跟随全局证书', 'Global certificate')}</Badge>
  return <Badge tone="amber">{t('待签发', 'Pending')}</Badge>
}

function Row({ r, only, onDelete }: { r: RootDomain; only: boolean; onDelete: () => void }) {
  const { t } = useT()
  const [busy, wrap] = useBusy()
  const patch = (b: { cert_mode?: string; default?: boolean }, ok: string) => wrap(() => run(() => api.updateRootDomain(r.name, b), { ok, invalidate: INVALIDATE }))
  const refresh = () => wrap(() => run(() => api.refreshRootDomain(r.name), { ok: t('已重新检查', 'Checked again'), invalidate: INVALIDATE }))
  return (
    <div className="px-4 @2xl:px-6 py-4 flex flex-wrap items-center gap-x-5 gap-y-2.5">
      <div className="min-w-[min(100%,220px)] flex-1">
        <div className="flex flex-wrap items-center gap-2 font-mono text-[13.5px] font-semibold [overflow-wrap:anywhere]">{r.name}{r.default && <Badge tone="green"><Star size={10} strokeWidth={3} />{t('默认', 'Default')}</Badge>}<CertBadge r={r} /></div>
        <div className="text-[12.5px] text-muted mt-1">{r.zone_name ? <>Cloudflare Zone <span className="font-mono">{r.zone_name}</span></> : t('未找到 Cloudflare Zone：DNS 记录需要手动创建', 'No Cloudflare zone found: create DNS records by hand')}</div>
        {r.cert_error && <div className="text-[12px] text-warn mt-1 [overflow-wrap:anywhere]">{r.cert_error}</div>}
      </div>
      <div className={`flex flex-wrap items-center gap-2 ${busy ? 'opacity-60 pointer-events-none' : ''}`}>
        <Select className="w-[210px]" value={r.cert_mode} onChange={e => patch({ cert_mode: e.target.value }, t('证书模式已更新', 'Certificate mode updated'))} title={t('证书模式', 'Certificate mode')}>
          <option value="global">{t('跟随全局 HTTPS 设置', 'Follow global HTTPS settings')}</option>
          <option value="auto">{t('自动（Origin CA，失败用 ACME）', 'Auto (Origin CA, else ACME)')}</option>
          <option value="acme">{t('ACME（Let\'s Encrypt）', 'ACME (Let\'s Encrypt)')}</option>
        </Select>
        {!r.default && <Button size="sm" onClick={() => patch({ default: true }, t('已设为默认根域名', 'Default root domain set'))}><Star size={13} />{t('设为默认', 'Make default')}</Button>}
        <button title={t('重新匹配 Zone 并重试证书', 'Match the zone and retry the certificate again')} onClick={refresh} className="h-8 w-8 grid place-items-center text-muted hover:text-fg">{busy ? <Loader2 size={15} className="animate-spin" /> : <RefreshCw size={15} />}</button>
        <button title={t('移除', 'Remove')} disabled={r.default && !only} onClick={onDelete} className="h-8 w-8 grid place-items-center text-muted hover:text-danger disabled:opacity-30 disabled:pointer-events-none"><Trash2 size={15} /></button>
      </div>
    </div>
  )
}

/** Root domain names for pickers (default first), [] while loading or when none is configured. */
export function useRootDomains() {
  return useQuery('root-domains', api.rootDomains)
}
