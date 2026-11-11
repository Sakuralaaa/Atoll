import { L } from '../../i18n'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { FlaskConical, Check, Loader2, Globe, AlertTriangle, ShieldCheck, ArrowRight, Settings2 } from 'lucide-react'
import { Badge, Button, Card, ServiceIcon } from '../../components/ui'
import { Empty, ErrorState, Label, TextInput } from '../../components/feedback'
import { api } from '../../api/client'
import { run, useBusy, useQuery } from '../../api/hooks'
import { bytes, colorFor, letterFor } from '../../api/view'
import type { ZeaburProject } from '../../api/types'
import { cx } from '../../components/ui-utils'
import { ImageBadge, StatusBadge } from './zeabur'
import { ROLE, isZeaburApp, useScan, zStatus } from './model'
import { ScanSkeleton } from './StepScan'

// ---------------- step 3 ----------------
export function StepSelect({ serverId, ns, onBack, onCreated }: { serverId: string; ns: string; onBack: () => void; onCreated: (id: string) => void }) {
  const q = useScan(serverId)
  const settings = useQuery('settings', api.settings)
  const p = q.data?.projects.find(x => x.namespace === ns)
  if (q.error) return <ErrorState error={q.error} onRetry={q.refetch} />
  if (!q.data || !settings.data) return <ScanSkeleton />
  if (!p) return <Empty title={L("扫描结果里没有这个项目")} action={<Button onClick={onBack}>{L("返回扫描")}</Button>} />
  return <SelectForm key={ns} p={p} serverId={serverId} base={settings.data.base_domain} cfReady={!!settings.data.base_domain && settings.data.has_cloudflare_token} onBack={onBack} onCreated={onCreated} />
}
function SelectForm({ p, serverId, base, cfReady, onBack, onCreated }: { p: ZeaburProject; serverId: string; base: string; cfReady: boolean; onBack: () => void; onCreated: (id: string) => void }) {
  const [sel, setSel] = useState<Set<string>>(() => new Set(p.services.filter(s => s.workload || s.volumes.length).map(s => s.service_id)))
  const [name, setName] = useState(p.project_name)
  const chosen = p.services.filter(s => sel.has(s.service_id))
  const zHosts = [...new Set(chosen.flatMap(s => s.domains.filter(isZeaburApp)))]
  const [map, setMap] = useState<Record<string, string>>({})
  const target = (h: string) => map[h] ?? (base ? `${h.replace(/\.zeabur\.app$/, '')}.${base}` : '')
  const rewrite = chosen.flatMap(s => s.env.filter(e => /zeabur\.app/.test(e.value)).map(e => ({ svc: s.name, key: e.key })))
  const data = chosen.reduce((a, s) => a + s.volumes.reduce((b, v) => b + v.bytes, 0), 0)
  const exportN = chosen.filter(s => s.zeabur_registry).length
  const [busy, wrap] = useBusy()
  const toggle = (id: string) => setSel(v => { const n = new Set(v); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const needCf = zHosts.length > 0 && !cfReady
  const names = Object.fromEntries(p.services.map(x => [x.service_id, x.name]))
  const dangling = chosen.flatMap(s => (s.references ?? []).filter(r => !sel.has(r.target) && names[r.target]).map(r => ({ from: s.name, to: names[r.target], key: r.key })))
  const submit = () => wrap(async () => {
    const domain_map = Object.fromEntries(zHosts.map(h => [h, target(h).trim()]))
    const j = await run(() => api.createZeaburLocal({ server_id: serverId, namespace: p.namespace, service_ids: chosen.map(s => s.service_id), project_name: name.trim(), domain_map }))
    if (!j) return
    const r = await run(() => api.rehearse(j.id), { ok: L("已开始演练，Zeabur 继续对外服务"), invalidate: ['imports'] })
    onCreated((r ?? j).id)
  })
  return (
    <div className="grid grid-cols-1 @5xl:grid-cols-[minmax(0,1fr)_380px] gap-6 items-start [&>*]:min-w-0">
      <div className="space-y-6">
        <div>
          <div className="text-[15px] font-semibold mb-1">{L("要迁移的服务")}</div>
          <div className="text-[13px] text-muted mb-4">{L("来自 Zeabur 项目")} {p.project_name}{L("。环境变量原样带过来，")}<span className="font-mono">*.zeabur.internal</span> {L("会改写成 Atoll 内网主机名。")}</div>
          <Card className="divide-y divide-line">
            {p.services.map(s => {
              const on = sel.has(s.service_id), [rl, rt] = ROLE[s.role] ?? ROLE.other
              return (
                <button key={s.service_id} onClick={() => toggle(s.service_id)} className="w-full text-left px-5 h-16 flex items-center gap-4 hover:bg-ov/[0.02]">
                  <span className={cx('h-5 w-5 rounded-[4px] border-2 border-[var(--edge)] grid place-items-center shrink-0', on ? 'bg-pick text-pick-ink' : 'bg-sunk')}>{on && <Check size={13} strokeWidth={3} />}</span>
                  <ServiceIcon s={{ kind: s.kind, letter: letterFor(s.name), color: colorFor(s.name) }} size={30} />
                  <div className="flex-1 min-w-0"><div className="text-[13.5px] font-semibold flex items-center gap-2">{s.name}<Badge tone={rt}>{rl}</Badge>{zStatus(s) !== 'running' && <StatusBadge s={s} />}</div><div className="font-mono text-[12px] text-muted truncate mt-0.5">{s.image}</div></div>
                  <ImageBadge s={s} />
                  <span className="w-20 text-right text-[12.5px] text-muted tnum">{bytes(s.volumes.reduce((a, v) => a + v.bytes, 0))}</span>
                </button>
              )
            })}
          </Card>
          {dangling.length > 0 && (
            <div className="mt-3 rounded-[4px] border-2 border-[var(--edge)] bg-t-amber text-ink px-4 py-3 text-[12.5px]">
              <div className="font-bold flex items-center gap-1.5"><AlertTriangle size={14} />{L("有服务引用了没选中的服务")}</div>
              {dangling.map((d, i) => <div key={i} className="mt-1 font-mono">{d.from}{d.key ? `.${d.key}` : ''} → {d.to}</div>)}
              <div className="mt-1">{L("迁移后这些连接会断开，除非你之后手动改成新地址。")}</div>
            </div>
          )}
        </div>

        <div>
          <div className="text-[15px] font-semibold mb-1 flex items-center gap-2"><Globe size={16} />{L("域名")}</div>
          <div className="text-[13px] text-muted mb-4">{L("切换时自动在 Cloudflare 创建记录；zeabur.app 地址届时失效。")}</div>
          {!zHosts.length ? <Card className="px-6 py-5 text-[13px] text-muted">{L("选中的服务没有 zeabur.app 域名。")}</Card> : needCf ? (
            <Card className="px-4 @2xl:px-6 py-5 flex flex-wrap items-center gap-4 !bg-t-amber text-ink">
              <Settings2 size={20} className="shrink-0" />
              <div className="flex-1 min-w-[min(100%,200px)]"><div className="text-[13.5px] font-bold">{L("先设置主域名和 Cloudflare 令牌")}</div><div className="text-[13px] mt-0.5">{L("这些服务目前只有 zeabur.app 地址，切换后就会失效。需要一个由 Cloudflare 托管的主域名来生成新地址。")}</div></div>
              <Link to="/settings#domains" className="btn-secondary h-9 px-3.5 inline-flex items-center gap-1.5 text-[13px]">{L("去设置")}<ArrowRight size={14} /></Link>
            </Card>
          ) : (
            <Card className="overflow-hidden">
              <div className="hidden @2xl:grid grid-cols-[minmax(0,1fr)_32px_minmax(0,1.2fr)] gap-4 px-6 h-10 items-center text-[12.5px] text-muted border-b border-line"><span>{L("Zeabur 地址")}</span><span /><span>{L("新地址（Cloudflare 代理）")}</span></div>
              {zHosts.map(h => (
                <div key={h} className="grid grid-cols-[minmax(0,1fr)] @2xl:grid-cols-[minmax(0,1fr)_32px_minmax(0,1.2fr)] gap-x-4 gap-y-2 px-4 @2xl:px-6 py-3 @2xl:py-0 @2xl:h-16 items-center border-b border-line last:border-0">
                  <span className="flex items-center gap-2 min-w-0"><span className="font-mono text-[13px] text-danger line-through decoration-2 truncate">{h}</span><Badge tone="red">{L("迁移后失效")}</Badge></span>
                  <ArrowRight size={16} className="text-accent @max-2xl:rotate-90" />
                  <TextInput mono className="w-full" value={target(h)} onChange={e => setMap(m => ({ ...m, [h]: e.target.value }))} placeholder={L("留空则不绑定")} />
                </div>
              ))}
            </Card>
          )}
          {rewrite.length > 0 && (
            <div className="mt-4 rounded-xl border-2 border-dashed border-line2 px-5 py-4">
              <div className="text-[13px] font-semibold flex items-center gap-2">{L("环境变量")}<Badge tone="green">{L("将自动改写")}</Badge></div>
              <div className="text-[12.5px] text-muted mt-1 mb-3">{L("这些变量的值引用了 zeabur.app，会改写成上面的新地址。")}</div>
              <div className="flex flex-wrap gap-2">{rewrite.map(r => <span key={r.svc + r.key} className="chip px-2.5 h-7 inline-flex items-center gap-1.5 bg-card font-mono text-[12px]"><span className="text-muted">{r.svc}</span>{r.key}</span>)}</div>
            </div>
          )}
        </div>
      </div>

      <Card className="p-6 sticky top-6">
        <Label t={L("Atoll 项目名称")}><TextInput className="w-full" value={name} onChange={e => setName(e.target.value)} /></Label>
        <div className="space-y-2.5 text-[13px] mt-2">
          <div className="flex justify-between"><span className="text-muted">{L("服务")}</span><span className="font-semibold">{chosen.length} {L("个")}</span></div>
          <div className="flex justify-between"><span className="text-muted">{L("从本机导出镜像")}</span><span className="font-semibold">{exportN} {L("个")}</span></div>
          <div className="flex justify-between"><span className="text-muted">{L("数据")}</span><span className="font-semibold tnum">{bytes(data)}</span></div>
          <div className="flex justify-between"><span className="text-muted">{L("新域名")}</span><span className="font-semibold">{zHosts.filter(h => target(h)).length} {L("个")}</span></div>
        </div>
        <div className="mt-5 rounded-lg field px-4 py-3 text-[12.5px] text-fg2 leading-5 flex gap-2"><ShieldCheck size={15} className="text-ok shrink-0 mt-0.5" />{L("下一步只是演练：Atoll 在本机起一套副本、复制数据并核对行数，不接公网流量，Zeabur 继续对外服务。")}</div>
        <div className="mt-5 flex gap-2">
          <Button onClick={onBack}>{L("返回")}</Button>
          {needCf ? <Link to="/settings" className="flex-1"><Button className="w-full">{L("先完成域名设置")}</Button></Link>
            : <Button variant="primary" className="flex-1" disabled={!chosen.length || !name.trim() || busy} onClick={submit}>{busy ? <Loader2 size={14} className="animate-spin" /> : <FlaskConical size={15} />}{L("创建并开始演练")}</Button>}
        </div>
      </Card>
    </div>
  )
}
