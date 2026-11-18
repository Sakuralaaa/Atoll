import { L } from '../../i18n'
import { useState } from 'react'
import { HardDrive } from 'lucide-react'
import { Button, Card, Chart } from '../../components/ui'
import { Label, Modal, Select, TextInput } from '../../components/feedback'
import { cx } from '../../components/ui-utils'
import { api } from '../../api/client'
import { run } from '../../api/hooks'
import { ago, bytes, timeLabels } from '../../api/view'
import type { Service, ServiceDisk, VolumeDisk } from '../../api/types'

const MB = 1 << 20

/** Disk usage on the Usage tab: total over time, then what it is made of. */
export function DiskUsageChart({ d }: { d?: ServiceDisk }) {
  if (!d) return null
  if (!d.supported && !d.measured_at) {
    return <Card className="px-5 py-4 flex items-center gap-2 text-[13px] text-muted"><HardDrive size={15} />{L("磁盘：该服务器的 Agent 版本较旧，升级 Agent 后显示磁盘占用。")}</Card>
  }
  const h = d.history ?? []
  const gb = Math.max(d.total_bytes, ...h.map(x => x.bytes)) >= 2 * 1024 * MB
  const div = gb ? 1024 * MB : MB
  const parts = [
    ...d.volumes.filter(v => v.measured && !v.orphan).map(v => L("卷 {0} {1}", v.name, bytes(v.bytes))),
    ...(d.rootfs_bytes >= 0 ? [L("容器可写层 {0}", bytes(d.rootfs_bytes))] : []),
  ]
  return (
    <>
      <Chart title={L("磁盘")} icon={<HardDrive size={15} />} data={h.map(x => x.bytes / div)} color="var(--c-5)" unit={gb ? ' GB' : ' MB'} h={150}
        labels={timeLabels(h.map(x => x.at))} empty={L("暂无数据，Agent 每 10 分钟统计一次")} />
      {d.measured_at && <div className="text-[12.5px] text-dim mt-3">{parts.join(' · ')}{parts.length ? ' · ' : ''}{L("统计于 {0}", ago(d.measured_at))}</div>}
    </>
  )
}

/** Used space of one volume on the Disk tab, against its limit when it has one. */
export function VolumeUsageBar({ v }: { v?: VolumeDisk }) {
  if (!v) return null
  if (v.host_path) return <div className="text-[12.5px] text-dim">{L("主机目录，不统计")}</div>
  if (!v.measured) return <div className="text-[12.5px] text-dim">{L("尚未统计")}</div>
  const cap = (v.limit_mb ?? 0) * MB
  const pct = cap ? (v.bytes / cap) * 100 : 0
  return (
    <>
      <div className="flex justify-between gap-3 text-[12.5px] mb-1.5"><span className="text-muted">{L("已用")}</span>
        <span className={cx('tnum', pct > 100 && 'text-danger', pct >= 80 && pct <= 100 && 'text-warn')}>{bytes(v.bytes)}{cap ? ` / ${bytes(cap)}` : ''}</span></div>
      {cap ? <div className="h-2.5 rounded-[4px] field overflow-hidden"><div className={cx('h-full', pct > 100 ? 'bg-danger' : pct >= 80 ? 'bg-warn' : 'bg-pick')} style={{ width: `${Math.min(100, pct)}%` }} /></div>
        : <div className="text-[11.5px] text-dim">{L("未设上限")}</div>}
    </>
  )
}

/** Set or remove a volume's soft limit. */
export function VolumeLimitModal({ s, v, onClose, onSaved }: { s: Service; v: VolumeDisk; onClose: () => void; onSaved: (d: ServiceDisk) => void }) {
  const cur = v.limit_mb ?? 0
  const [unit, setUnit] = useState<'MB' | 'GB'>(cur && cur % 1024 ? 'MB' : 'GB')
  const [val, setVal] = useState(cur ? String(cur % 1024 ? cur : cur / 1024) : '')
  const [act, setAct] = useState<'stop' | 'alert'>(v.over_limit ?? 'stop')
  const mb = Math.round(Number(val) * (unit === 'GB' ? 1024 : 1))
  const ok = val !== '' && mb > 0
  const save = (limit: number) => run(() => api.setVolumeLimit(s.id, v.name, { limit_mb: limit, over_limit: act }), { ok: limit ? L("已设置上限") : L("已移除上限"), invalidate: [`svc-disk:${s.id}`] })
    .then(r => { if (r) { onSaved(r); onClose() } })
  return (
    <Modal title={L("卷 {0} 的容量上限", v.name)} onClose={onClose} footer={<>
      {cur > 0 && <Button size="sm" className="mr-auto" onClick={() => save(0)}>{L("移除上限")}</Button>}
      <Button size="sm" onClick={onClose}>{L("取消")}</Button>
      <Button size="sm" variant="primary" disabled={!ok} onClick={() => save(mb)}>{L("保存")}</Button></>}>
      <Label t={L("上限")} d={v.measured ? L("当前已用 {0}", bytes(v.bytes)) : undefined}>
        <div className="flex gap-2"><TextInput mono className="flex-1 min-w-0" inputMode="decimal" value={val} onChange={e => setVal(e.target.value.replace(/[^\d.]/g, ''))} />
          <Select className="w-24" value={unit} onChange={e => setUnit(e.target.value as 'MB' | 'GB')}><option>MB</option><option>GB</option></Select></div>
      </Label>
      <Label t={L("超出上限后")}>
        <Select className="w-full" value={act} onChange={e => setAct(e.target.value as 'stop' | 'alert')}>
          <option value="stop">{L("停止服务并告警")}</option><option value="alert">{L("仅告警")}</option>
        </Select>
      </Label>
      <div className="text-[12.5px] text-muted leading-relaxed">{L("每 10 分钟统计一次，用到 80% 时告警并改为每分钟统计。这是软限制，两次统计之间仍可能多写一些。被停止后手动启动，会有 1 小时用来清理数据。")}</div>
      {ok && act === 'stop' && v.measured && v.bytes > mb * MB && <div className="mt-3 text-[12.5px] text-danger">{L("当前已用 {0}，超过新上限，保存后服务会在下一次统计时被停止。", bytes(v.bytes))}</div>}
    </Modal>
  )
}
