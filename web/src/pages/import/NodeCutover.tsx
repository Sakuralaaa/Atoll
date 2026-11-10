import { useState } from 'react'
import { useMask } from '../../api/privacy'
import { Layers, Check, AlertTriangle } from 'lucide-react'
import { Badge, Button, Card } from '../../components/ui'
import { ConfirmDialog } from '../../components/feedback'
import { api } from '../../api/client'
import { run, useQuery } from '../../api/hooks'
import type { ImportJob, Server } from '../../api/types'
import { cx } from '../../components/ui-utils'
import { useT } from '../../i18n'

/** Zeabur's ingress-controller is shared by every Zeabur project on a server: cutover and
 *  rollback act on the whole node. These helpers find the other imports of the same server. */
export function useSiblings(job: ImportJob) {
  const all = useQuery('imports', api.imports, { interval: 5000 }).data ?? []
  return all.filter(j => j.id !== job.id && j.server_id && j.server_id === job.server_id && (j.mode === 'zeabur-local' || !!j.namespace))
}

const READY = ['rehearsed', 'rolled_back']

/** Note shown wherever a cutover is offered. */
export function NodeWideNote() {
  const { t } = useT()
  return (
    <Card className="px-5 py-4 flex items-start gap-3 text-[13px] !bg-[#fde68a] text-ink">
      <AlertTriangle size={16} className="mt-0.5 shrink-0" />
      <div className="leading-6">{t(
        'Zeabur 的入口控制器由这台服务器上所有 Zeabur 项目共用：切换会让它们全部停止由 Zeabur 对外服务。服务器上还有其他在运行、有域名的项目时，单独切换会被拒绝——先逐个导入并演练，再一起批量切换。',
        "Zeabur's ingress-controller is shared by every Zeabur project on this server: a cutover stops Zeabur serving all of them. While other running projects with hostnames remain, a single cutover is refused — import and rehearse each one, then cut them over together.")}</div>
    </Card>
  )
}

/** Batch cutover: offered when other imports of the same server are rehearsed too. */
export function NodeCutoverCard({ job, srv }: { job: ImportJob; srv?: Server }) {
  const { t } = useT()
  const ready = useSiblings(job).filter(j => READY.includes(j.status))
  const [picked, setPicked] = useState<Record<string, boolean>>({})
  const [open, setOpen] = useState(false)
  const mask = useMask()
  if (!ready.length || !job.server_id) return null
  const ids = [job.id, ...ready.filter(j => picked[j.id] ?? true).map(j => j.id)]
  return (
    <Card className="p-6">
      <div className="text-[15px] font-semibold flex items-center gap-2"><Layers size={16} className="text-accent" />{t('批量切换', 'Batch cutover')}<Badge tone="violet">{t(`${ready.length + 1} 个项目已就绪`, `${ready.length + 1} projects ready`)}</Badge></div>
      <div className="text-[13px] text-muted mt-1.5 leading-6">{t(
        '同一台服务器上的这些导入一起切换：每个项目各自缩容并做最终同步，入口控制器只停放一次，atoll-caddy 带上所有主机名接管 80/443。任一项目失败，全部自动回滚。',
        'Cut these imports of the same server over together: each scales down and does its final sync, the ingress is parked once, and atoll-caddy serves 80/443 with all their hostnames. If any project fails, all of them roll back automatically.')}</div>
      <div className="mt-4 space-y-1.5">
        {[job, ...ready].map(j => {
          const self = j.id === job.id, on = self || (picked[j.id] ?? true)
          return (
            <button key={j.id} type="button" disabled={self} onClick={() => setPicked(p => ({ ...p, [j.id]: !on }))} className="flex items-center gap-3 w-full text-left min-h-9 text-[13px] disabled:cursor-default">
              <span className={cx('h-5 w-5 rounded-[4px] border-2 border-[var(--edge)] grid place-items-center shrink-0', on ? 'bg-pick text-pick-ink' : 'bg-sunk')}>{on && <Check size={13} strokeWidth={3} />}</span>
              <span className="font-semibold truncate">{j.plan.project_name}</span>
              <span className="font-mono text-[12px] text-muted truncate">{j.namespace}</span>
              {self && <span className="ml-auto text-[12px] text-dim shrink-0">{t('当前', 'current')}</span>}
            </button>
          )
        })}
      </div>
      <Button variant="danger" className="mt-5 w-full" disabled={ids.length < 2} onClick={() => setOpen(true)}><Layers size={15} />{t(`一起切换 ${ids.length} 个项目`, `Cut over ${ids.length} projects together`)}</Button>
      {open && <ConfirmDialog title={t('批量切换到 Atoll', 'Batch cutover to Atoll')} action={t('确认切换', 'Cut over')} name={srv?.name} onClose={() => setOpen(false)}
        description={<>{t(`${ids.length} 个项目的 Zeabur 应用会缩容到 0，入口控制器停用一次，由 Atoll 接管 ${mask(srv?.public_ipv4 ?? '')} 的 80/443。zeabur.app 地址将失效。之后可整体回滚。`,
          `Zeabur apps of ${ids.length} projects scale to 0, the ingress is parked once and Atoll takes 80/443 on ${mask(srv?.public_ipv4 ?? '')}. zeabur.app URLs stop working. You can roll back the whole server later.`)}</>}
        onConfirm={() => run(() => api.zeaburNodeCutover(job.server_id!, { import_ids: ids }), { ok: t('正在批量切换', 'Batch cutover started'), invalidate: ['imports'] })} />}
    </Card>
  )
}

/** Other imports of the same server that are cut over: a rollback must cover them too. */
export function useNodeLive(job: ImportJob) {
  return useSiblings(job).filter(j => j.status === 'live')
}
