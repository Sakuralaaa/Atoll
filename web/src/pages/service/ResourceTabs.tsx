import { L } from '../../i18n'
import { useState } from 'react'
import { IP } from '../../components/IP'
import { Link, useParams } from 'react-router-dom'
import { HardDrive, Globe, FolderOpen, ExternalLink, Trash2, Plus, Cpu, MemoryStick, ArrowDownUp, Copy, Cloud, Box, ShieldCheck } from 'lucide-react'
import { Button, Card, Chart, Dot } from '../../components/ui'
import { ConfirmDialog, Empty, ErrorState, Label, Modal, Select, TextInput } from '../../components/feedback'
import { api, isDemo, type Range } from '../../api/client'
import { run, useQuery } from '../../api/hooks'
import { bytesRate, primaryPort, timeLabels } from '../../api/view'
import type { Domain, Server, Service } from '../../api/types'
import { RangePicker } from '../ServerDetail'
import { DomainBadge } from '../../components/DomainStatus'
import { BindDomain } from './StatusTab'
import { useCan } from '../../api/queries'

export function UsageTab({ s }: { s: Service }) {
  const [range, setRange] = useState<Range>('24h')
  const q = useQuery(`svc-metrics:${s.id}:${range}`, () => api.serviceMetrics(s.id, range), { interval: 60000 })
  const d = q.data ?? []
  const labels = timeLabels(d.map(x => x.at))
  const tx = d.reduce((a, x, i) => a + (i ? x.net_tx_bps * ((new Date(x.at).getTime() - new Date(d[i - 1].at).getTime()) / 1000) : 0), 0)
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-4"><div className="text-[15px] font-semibold">{L("用量")}</div><RangePicker v={range} set={setRange} /></div>
      {q.error && !q.data && <div className="mb-4"><ErrorState error={q.error} onRetry={q.refetch} /></div>}
      <div className="grid grid-cols-1 @3xl:grid-cols-2 gap-5 [&>*]:min-w-0">
        <Chart title="CPU" icon={<Cpu size={15} />} data={d.map(x => x.cpu_pct)} color="var(--pick)" labels={labels} />
        <Chart title={L("内存")} icon={<MemoryStick size={15} />} data={d.map(x => x.mem_mb)} color="#38bdf8" unit=" MB" labels={labels} />
      </div>
      <div className="mt-5"><Chart title={L("网络用量")} icon={<ArrowDownUp size={15} />} data={d.map(x => (x.net_rx_bps + x.net_tx_bps) / 1048576)} color="#34d399" unit=" MB/s" h={150} labels={labels} /></div>
      <div className="text-[12.5px] text-dim mt-3">{s.name} {L("所选时段出站约")} {(tx / 1073741824).toFixed(1)} GB{d.length ? L(" · 当前 {0} 出站", bytesRate(d[d.length - 1].net_tx_bps)) : ''}</div>
    </>
  )
}

export function NetworkTab({ s, srv }: { s: Service; srv?: Server }) {
  const port = primaryPort(s)
  const [bind, setBind] = useState(false)
  const [del, setDel] = useState<Domain>()
  const [addPort, setAddPort] = useState(false)
  const canDeploy = useCan('deploy'), canBind = useCan('domains')
  return (
    <>
      <div className="text-[15px] font-semibold mb-1">{L("内网访问")}</div>
      <div className="text-[13px] text-muted mb-4">{L("同项目的服务通过 Docker 内网直接互通，不经过公网。")}</div>
      <Card className="px-4 @2xl:px-6 py-5 grid grid-cols-1 @2xl:grid-cols-2 gap-6 [&>div]:min-w-0">
        <div><div className="text-[13px] text-muted mb-2">{L("主机名")}</div><div className="h-11 rounded-lg field px-3.5 flex items-center justify-between gap-3 font-mono text-[13.5px]"><span className="truncate" title={s.name}>{s.name}</span><button className="shrink-0" onClick={() => navigator.clipboard?.writeText(s.name)}><Copy size={14} className="text-muted hover:text-fg" /></button></div></div>
        <div><div className="text-[13px] text-muted mb-2 flex items-center">{L("端口")}{canDeploy && <button onClick={() => setAddPort(true)} className="ml-auto text-accent text-[12.5px] flex items-center gap-1"><Plus size={13} />{L("添加")}</button>}</div>
          {(s.ports ?? []).length ? <div className="space-y-2">{s.ports.map(p => <div key={p.name} className="h-11 rounded-lg field px-3.5 flex items-center justify-between font-mono text-[13.5px]">{p.container_port}<span className="text-[12px] text-dim">{p.name} · {p.protocol.toUpperCase()}{p.public_port ? L(" · 公网 {0}", p.public_port) : ''}</span></div>)}</div>
            : <div className="h-11 rounded-lg field px-3.5 flex items-center text-[13px] text-dim">{L("未暴露端口")}</div>}
        </div>
      </Card>

      <div className="mt-8 flex flex-wrap items-end justify-between gap-3 mb-4">
        <div className="min-w-[min(100%,260px)] flex-1"><div className="text-[15px] font-semibold">{L("公网访问")}</div><div className="text-[13px] text-muted mt-0.5">{L("绑定后自动在 Cloudflare 建代理记录，直接指向服务器，不经过中控。")}</div></div>
        {canBind && <div className="flex gap-2"><Button disabled={!s.ports?.some(p => p.protocol === 'http')} onClick={() => setBind(true)}><Plus size={15} />{L("绑定域名")}</Button></div>}
      </div>
      <Card className="p-4 @2xl:p-6">
        <div className="flex flex-col @4xl:flex-row items-center justify-center gap-0 py-4">
          {([[L("用户"), <Globe key="g" size={18} />], ['Cloudflare', <Cloud key="c" size={18} className="text-[#f6821f]" />], [`Caddy · ${srv?.name.split(' ')[0] ?? '…'}`, <ShieldCheck key="s" size={18} className="text-emerald-400" />], [`${s.name}:${port?.container_port ?? '—'}`, <Box key="b" size={18} className="text-accent" />]] as const).map(([l, i], idx, a) => (
            <div key={idx} className="flex flex-col @4xl:flex-row items-center min-w-0 max-w-full">
              <div className="rounded-xl border border-line2 bg-card2 px-4 h-12 flex items-center gap-2.5 text-[13px] font-semibold min-w-0 max-w-full [&>svg]:shrink-0">{i}<span className="truncate" title={l}>{l}</span></div>
              {idx < a.length - 1 && <><div className="@4xl:hidden h-6 border-l-2 border-dashed border-brand/60 relative"><span className="absolute -left-[5px] -bottom-[6px] text-brand text-[10px] leading-none">▼</span></div><div className="@max-4xl:hidden w-16 shrink-0 border-t-2 border-dashed border-brand/60 relative"><span className="absolute -right-1 -top-[5px] text-brand text-[10px]">▶</span></div></>}
            </div>
          ))}
        </div>
        {(s.domains ?? []).map(d => (
          <div key={d.id} className="mt-4 rounded-lg border border-line bg-bg/50 px-4 @2xl:px-5 py-3 min-h-14 flex flex-wrap items-center gap-x-3 gap-y-1.5">
            <Dot status={d.status} /><span className="font-semibold text-[15px] min-w-0 [overflow-wrap:anywhere]">{d.hostname}</span><DomainBadge d={d} />
            <span className="text-[13px] text-muted @4xl:ml-3 [overflow-wrap:anywhere]">A <IP v={srv?.public_ipv4} /> · {d.proxied ? L("已代理 · Full (strict)") : L("仅 DNS")} {L("· 端口")} {d.port_name}</span>
            <span className="ml-auto flex gap-3 text-muted"><a href={`https://${d.hostname}`} target="_blank" rel="noreferrer" className="hover:text-fg"><ExternalLink size={15} /></a>{canBind && <button onClick={() => setDel(d)} className="hover:text-danger"><Trash2 size={15} /></button>}</span>
          </div>
        ))}
      </Card>
      {bind && <BindDomain s={s} onClose={() => setBind(false)} />}
      {addPort && <AddPort s={s} onClose={() => setAddPort(false)} />}
      {del && <ConfirmDialog title={L("解绑域名")} name={del.hostname} onClose={() => setDel(undefined)} description={<>{L("将删除 Cloudflare 上")} <b>{del.hostname}</b> {L("的记录，并从 Caddy 移除路由。")}</>}
        onConfirm={() => run(() => api.deleteDomain(del.id), { ok: L("域名已解绑"), invalidate: [`project:${s.project_id}`, 'domains'] })} />}
    </>
  )
}
function AddPort({ s, onClose }: { s: Service; onClose: () => void }) {
  const [name, setName] = useState(s.ports?.length ? '' : 'web'); const [port, setPort] = useState(''); const [proto, setProto] = useState('http'); const [pub, setPub] = useState('')
  const submit = () => run(() => api.updateService(s.id, { ports: [...(s.ports ?? []), { name, container_port: +port, protocol: proto, public_port: pub ? +pub : undefined }] }), { ok: L("已添加端口，正在重新部署"), invalidate: [`project:${s.project_id}`] }).then(r => r && onClose())
  return (
    <Modal title={L("添加端口")} onClose={onClose} footer={<><Button size="sm" onClick={onClose}>{L("取消")}</Button><Button size="sm" variant="primary" disabled={!name || !port} onClick={submit}>{L("添加")}</Button></>}>
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-4">
        <Label t={L("名称")} d={L("用于 ${NAME_PORT} 变量")}><TextInput mono className="w-full" value={name} onChange={e => setName(e.target.value.replace(/[^a-z0-9-]/gi, '').toLowerCase())} /></Label>
        <Label t={L("容器端口")}><TextInput mono className="w-full" value={port} onChange={e => setPort(e.target.value.replace(/\D/g, ''))} /></Label>
        <Label t={L("协议")}><Select className="w-full" value={proto} onChange={e => setProto(e.target.value)}><option value="http">HTTP</option><option value="tcp">TCP</option><option value="udp">UDP</option></Select></Label>
        {proto !== 'http' && <Label t={L("公网端口")} d={L("留空则仅内网")}><TextInput mono className="w-full" value={pub} onChange={e => setPub(e.target.value.replace(/\D/g, ''))} /></Label>}
      </div>
    </Modal>
  )
}

export function DiskTab({ s }: { s: Service }) {
  const [add, setAdd] = useState(false); const [del, setDel] = useState<string>()
  const vols = s.volumes ?? []
  const p = useParams()
  const canDeploy = useCan('deploy'), canExec = useCan('exec')
  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-3 mb-4"><div className="min-w-[min(100%,260px)] flex-1"><div className="text-[15px] font-semibold">{L("硬盘")}</div><div className="text-[13px] text-muted mt-0.5">{L("卷保存在服务器本地 /var/lib/atoll/volumes，迁移时随服务一起搬走。")}</div></div>{canDeploy && <Button variant="primary" onClick={() => setAdd(true)}><Plus size={15} />{L("挂载卷")}</Button>}</div>
      {!vols.length ? <Empty icon={<HardDrive size={22} />} title={L("没有挂载卷")} desc={L("不挂载卷时，容器重新部署后文件会丢失。")} /> : (
        <Card className="divide-y divide-line">
          {vols.map((v, i) => (
            <div key={v.name} className="px-4 @2xl:px-6 py-3.5 min-h-16 flex flex-wrap @3xl:flex-nowrap items-center gap-x-4 gap-y-2.5">
              <HardDrive size={18} className="text-accent shrink-0" />
              <div className="flex-1 min-w-0"><div className="font-mono text-[13px] [overflow-wrap:anywhere]">{v.mount_path}</div><div className="text-[12.5px] text-muted mt-0.5 font-mono [overflow-wrap:anywhere]">/var/lib/atoll/volumes/{s.id}/{v.name}</div></div>
              {isDemo() && <div className="w-56 @max-3xl:order-last @max-3xl:w-full @max-3xl:pl-[34px]"><div className="flex justify-between text-[12.5px] mb-1.5"><span className="text-muted">{L("已用")}</span><span>{i ? '86 MB' : '1.4 GB'}</span></div><div className="h-2.5 rounded-[4px] field"><div className="h-full bg-pick" style={{ width: i ? '6%' : '28%' }} /></div></div>}
              <span className="flex gap-3 text-muted @3xl:ml-4 shrink-0">{canExec && <Link to={`/projects/${p.pid}/services/${s.id}/files?path=${encodeURIComponent(v.mount_path)}`} className="hover:text-fg"><FolderOpen size={15} /></Link>}{canDeploy && <button onClick={() => setDel(v.name)} className="hover:text-danger"><Trash2 size={15} /></button>}</span>
            </div>
          ))}
        </Card>
      )}
      {add && <AddVolume s={s} onClose={() => setAdd(false)} />}
      {del && <ConfirmDialog title={L("卸载卷")} name={del} onClose={() => setDel(undefined)} description={<>{L("卸载卷")} <span className="font-mono">{del}</span> {L("并重新部署。服务器上的数据目录不会立即删除。")}</>}
        onConfirm={() => run(() => api.updateService(s.id, { volumes: vols.filter(v => v.name !== del) }), { ok: L("已卸载，正在重新部署"), invalidate: [`project:${s.project_id}`] })} />}
    </>
  )
}
function AddVolume({ s, onClose }: { s: Service; onClose: () => void }) {
  const [name, setName] = useState('data'); const [path, setPath] = useState('/data')
  return (
    <Modal title={L("挂载卷")} onClose={onClose} footer={<><Button size="sm" onClick={onClose}>{L("取消")}</Button><Button size="sm" variant="primary" disabled={!name || !path.startsWith('/') || s.volumes?.some(v => v.name === name)}
      onClick={() => run(() => api.updateService(s.id, { volumes: [...(s.volumes ?? []), { name, mount_path: path }] }), { ok: L("已挂载，正在重新部署"), invalidate: [`project:${s.project_id}`] }).then(r => r && onClose())}>{L("挂载")}</Button></>}>
      <Label t={L("卷名")} d={L("服务内唯一")}><TextInput mono className="w-full" value={name} onChange={e => setName(e.target.value.replace(/[^a-z0-9-_]/gi, ''))} /></Label>
      <Label t={L("容器内路径")}><TextInput mono className="w-full" value={path} onChange={e => setPath(e.target.value)} /></Label>
    </Modal>
  )
}
