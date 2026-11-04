import { HardDrive, Globe, AlertTriangle, Link2, Database, ArrowRight } from 'lucide-react'
import { Badge, Dot, ServiceIcon } from '../../components/ui'
import { bytes, colorFor, letterFor } from '../../api/view'
import type { ZeaburReference, ZeaburService } from '../../api/types'
import { cx } from '../../components/ui-utils'
import { ROLE, STORE, VIA, ZSTATUS, isZeaburApp, zStatus } from './model'
import { useBackendText } from '../../api/backendText'

export function StatusBadge({ s }: { s: ZeaburService }) {
  const [l, t] = ZSTATUS[zStatus(s)] ?? [zStatus(s), 'zinc']
  return <Badge tone={t}>{l}</Badge>
}
function Refs({ refs, names }: { refs: ZeaburReference[]; names: Record<string, string> }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="text-[12px] text-muted flex items-center gap-1 mr-0.5"><Link2 size={13} />引用</span>
      {refs.map((r, i) => (
        <span key={i} title={[VIA[r.via] ?? r.via, r.object].filter(Boolean).join(' · ')} className={cx('chip px-2 min-h-6 py-0.5 inline-flex flex-wrap items-center gap-x-1 text-[11.5px] font-mono bg-card max-w-full [overflow-wrap:anywhere]', r.inferred && 'border-dashed')}>
          {r.key ? <span className="text-fg2">{r.key}</span> : <span className="text-muted">{r.object ?? '整体共享'}</span>}
          <ArrowRight size={11} className="text-accent shrink-0" />
          <span className="font-semibold text-fg">{names[r.target] ?? r.target}</span>{r.target_key && <span className="text-muted">.{r.target_key}</span>}
          {r.inferred && <span className="font-sans not-italic text-[10.5px] text-muted">推断</span>}
        </span>
      ))}
    </div>
  )
}
export function ImageBadge({ s }: { s: ZeaburService }) {
  if (s.zeabur_registry) return s.image_cached ? <Badge tone="violet">Zeabur 构建 · 将从本机导出</Badge> : <Badge tone="red">Zeabur 构建 · 本机无缓存</Badge>
  return <Badge tone="zinc">公共镜像 · 固定摘要</Badge>
}
export function ZRow({ s, names }: { s: ZeaburService; names: Record<string, string> }) {
  const [rl, rt] = ROLE[s.role] ?? ROLE.other
  const { bt } = useBackendText()
  return (
    <div className="px-4 @2xl:px-6 py-4 grid grid-cols-1 @3xl:grid-cols-[200px_minmax(0,1fr)] @5xl:grid-cols-[230px_minmax(0,1fr)_240px] gap-x-6 gap-y-3 text-[13px] [&>*]:min-w-0">
      <div className="flex items-start gap-3 min-w-0">
        <ServiceIcon s={{ kind: s.kind, letter: letterFor(s.name), color: colorFor(s.name) }} size={30} />
        <div className="min-w-0">
          <div className="font-semibold truncate">{s.name}</div>
          <div className="mt-1.5 flex flex-wrap items-center gap-1.5"><Badge tone={rt}>{rl}</Badge><StatusBadge s={s} /></div>
          <div className="mt-1.5 text-[12px] text-muted flex items-center gap-1.5">{s.workload ? <><Dot status={s.replicas ? 'running' : 'stopped'} />{s.replicas} 副本 · {s.workload.split('/')[0]}</> : '无工作负载（只有 PVC）'}</div>
        </div>
      </div>
      <div className="min-w-0 space-y-2">
        <div className="flex flex-wrap items-center gap-2 min-w-0"><span className="font-mono text-[12.5px] text-fg2 truncate max-w-full">{s.image}</span><ImageBadge s={s} /></div>
        {s.data_store && <div className="flex items-center gap-1.5 text-[12.5px] text-fg2"><Database size={13} className="text-accent shrink-0" />数据存储 · <span className="font-semibold">{STORE[s.data_store] ?? s.data_store}</span>{['postgres', 'mysql', 'mongo', 'redis'].includes(s.data_store) ? <span className="text-muted">· 同主版本导出恢复</span> : <span className="text-muted">· 按目录复制</span>}</div>}
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-muted">
          {s.ports.map(p => <span key={p.name} className="font-mono text-[12px]">{p.container_port}/{p.protocol}</span>)}
          {s.node_ports?.map(n => <span key={n} className="font-mono text-[12px] text-dim">NodePort {n}</span>)}
          {s.domains.map(d => <span key={d} className="flex items-center gap-1.5 min-w-0"><Globe size={13} className="shrink-0" /><span className={cx('font-medium truncate', isZeaburApp(d) ? 'text-[#ef4444] line-through decoration-2' : 'text-fg')}>{d}</span>{isZeaburApp(d) && <Badge tone="red">迁移后失效</Badge>}</span>)}
        </div>
        {!!s.references?.length && <Refs refs={s.references} names={names} />}
        {s.warnings?.map(w => <div key={w} className="flex items-start gap-1.5 text-[12.5px] text-fg2"><AlertTriangle size={13} className="text-[#f59e0b] mt-0.5 shrink-0" /><span className="min-w-0 break-words">{bt(w)}</span></div>)}
      </div>
      <div className="space-y-1.5 min-w-0">
        {!s.volumes.length && <span className="text-dim">无数据卷</span>}
        {s.volumes.map(v => <div key={v.pvc} className="flex items-center gap-2 min-w-0"><HardDrive size={13} className="text-accent shrink-0" /><span className="font-mono text-[12px] truncate flex-1" title={v.host_dir}>{v.mount_path}</span><span className="tnum text-muted shrink-0">{bytes(v.bytes)}</span></div>)}
      </div>
    </div>
  )
}
