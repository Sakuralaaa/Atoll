import { Gauge } from 'lucide-react'
import { Card, Hint } from './ui'
import { TextInput } from './feedback'
import type { MemberQuota, MemberUsage } from '../api/types'
import { useT } from '../i18n'
import { cx } from './ui-utils'
import type { QuotaDraft } from '../api/quota'

/** Admin, sub-account editor: CPU / memory / service caps (empty = no limit). */
export function QuotaEditor({ value, onChange }: { value: QuotaDraft; onChange: (d: QuotaDraft) => void }) {
  const { t } = useT()
  const field = (k: keyof QuotaDraft, label: string, unit: string, ph: string) => (
    <label className="min-w-0"><div className="text-[12px] text-muted mb-1">{label}</div>
      <div className="flex items-center gap-1.5"><TextInput mono inputMode="decimal" className="w-full" value={value[k]} placeholder={ph} onChange={e => onChange({ ...value, [k]: e.target.value.replace(/[^\d.]/g, '') })} /><span className="text-[12px] text-dim shrink-0">{unit}</span></div>
    </label>
  )
  return (
    <div>
      <div className="text-[12px] font-bold text-muted mb-1 flex items-center gap-1.5">{t('资源配额（留空 = 不限）', 'Resource quota (empty = no limit)')}<Hint>{t('按这个子账号能看到的项目里所有服务的 CPU、内存限制之和与服务数计算。设置后它新建的服务必须带限制（不填时 CPU 1 核、内存 512 MB）；超出时创建、调高限制、一键应用和迁移会被拒绝。管理员不受限制。', 'Counts the CPU and memory limits and the number of services in the projects this sub-account sees. Once set, its new services must have limits (1 core / 512 MB when none is given); creating, raising limits, one-click apps and migrations that would go over are refused. Admins are not limited.')}</Hint></div>
      <div className="grid grid-cols-3 gap-3">
        {field('cpu', 'CPU', t('核', 'cores'), t('不限', 'none'))}
        {field('mem', t('内存', 'Memory'), 'MB', t('不限', 'none'))}
        {field('services', t('服务数', 'Services'), t('个', ''), t('不限', 'none'))}
      </div>
    </div>
  )
}

function Bar({ label, used, cap, unit }: { label: string; used: number; cap?: number; unit: string }) {
  const pct = cap ? Math.min(100, (used / cap) * 100) : 0
  const fmt = (v: number) => String(Math.round(v * 100) / 100)
  return (
    <div className="min-w-0">
      <div className="flex items-baseline justify-between gap-2 text-[12px]"><span className="text-muted">{label}</span><span className="font-mono tnum">{fmt(used)}{cap ? ` / ${fmt(cap)}` : ''} {unit}</span></div>
      {cap ? <div className="mt-1 h-1.5 rounded-full bg-sunk overflow-hidden"><div className={cx('h-full', pct >= 100 ? 'bg-danger' : pct >= 80 ? 'bg-warn' : 'bg-ok')} style={{ width: `${pct}%` }} /></div> : <div className="mt-1 text-[11px] text-dim">—</div>}
    </div>
  )
}

/** Usage against a quota, three small bars. */
export function QuotaUsage({ quota, usage, compact }: { quota?: MemberQuota; usage?: MemberUsage; compact?: boolean }) {
  const { t } = useT()
  if (!usage) return null
  return (
    <div className={cx('grid grid-cols-3 gap-4', compact ? 'max-w-[460px]' : '')}>
      <Bar label="CPU" used={usage.cpu} cap={quota?.cpu} unit={t('核', 'cores')} />
      <Bar label={t('内存', 'Memory')} used={usage.mem_mb} cap={quota?.mem_mb} unit="MB" />
      <Bar label={t('服务', 'Services')} used={usage.services} cap={quota?.services} unit="" />
    </div>
  )
}

/** Settings → 账号, for a sub-account with a quota. */
export function MyQuotaCard({ quota, usage }: { quota?: MemberQuota; usage?: MemberUsage }) {
  const { t } = useT()
  if (!quota || !usage) return null
  return (
    <div className="mt-8">
      <div className="text-[15px] font-semibold flex items-center gap-2 mb-3"><Gauge size={17} />{t('资源配额', 'Resource quota')}<Hint>{t('管理员为你的账号设置的上限：你项目里所有服务的 CPU、内存限制之和与服务数。新服务不填限制时按 CPU 1 核、内存 512 MB 计。', 'Caps the admin set for your account: the CPU and memory limits and the number of services across your projects. A new service without limits counts as 1 core / 512 MB.')}</Hint></div>
      <Card className="p-5">
        <QuotaUsage quota={quota} usage={usage} />
        {!!usage.unlimited && <div className="mt-3 text-[12px] text-dim">{t(`${usage.unlimited} 个服务没有设置 CPU 或内存限制，不计入对应用量。`, `${usage.unlimited} service(s) have no CPU or memory limit and add nothing to that total.`)}</div>}
      </Card>
    </div>
  )
}
