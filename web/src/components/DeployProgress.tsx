import { useState } from 'react'
import { Loader2, ScrollText, XCircle } from 'lucide-react'
import { api } from '../api/client'
import { useQuery } from '../api/hooks'
import { hasBuildLog } from '../api/git'
import { ago, deployStatusText, latestDeploy, shortDigests } from '../api/view'
import type { Service } from '../api/types'
import { BuildLogModal } from './GitSource'
import { Button } from './ui'
import { cx } from './ui-utils'
import { useT } from '../i18n'

const STEPS: readonly string[] = ['queued', 'building', 'pulling', 'starting', 'running']
const active = (st?: string) => !!st && st !== 'running' && st !== 'failed' && st !== 'superseded'
/** a failure stays visible this long after it happened */
const FAILED_SHOWN_MS = 15 * 60 * 1000

/**
 * Strip under the service header while a deployment runs: its stage, with the build log one
 * click away; and, for a while after, why the last one failed. Polls only while one runs.
 */
export function DeployProgress({ s }: { s: Service }) {
  const { t } = useT()
  const [now] = useState(() => Date.now())
  const [log, setLog] = useState(false)
  // poll while the newest deployment is in flight (the service row can lag behind it)
  const [poll, setPoll] = useState(false)
  const q = useQuery(`deployments:${s.id}`, () => api.deployments(s.id), { interval: poll || s.status === 'deploying' ? 2000 : undefined })
  const d = latestDeploy(q.data)
  const running = active(d?.status)
  if (running !== poll) setPoll(running)
  if (!d) return null
  const failedRecently = d.status === 'failed' && now - new Date(d.finished_at ?? d.started_at).getTime() < FAILED_SHOWN_MS
  if (!running && !failedRecently) return null
  const git = hasBuildLog(s, d)
  const steps = git ? STEPS : STEPS.filter(x => x !== 'building')
  const at = steps.indexOf(d.status)
  return (
    <div className={cx('mt-5 rounded-xl border-2 border-[var(--edge)] px-4 py-3 text-[13px] flex flex-wrap items-center gap-3', running ? 'bg-card2/60' : 'bg-[#fca5a5] text-[#141414]')}>
      {running ? <Loader2 size={16} className="animate-spin text-accent shrink-0" /> : <XCircle size={16} className="shrink-0" />}
      {running ? <div className="flex-1 min-w-[min(100%,260px)]">
        <div className="font-semibold">{t('正在部署', 'Deploying')} · {deployStatusText[d.status] ?? d.status}<span className="font-normal text-muted"> · {ago(d.started_at)}</span></div>
        <div className="mt-2 flex gap-1.5" aria-hidden>{steps.map((x, i) => <span key={x} title={deployStatusText[x]} className={cx('h-1.5 flex-1 rounded-full', i < at ? 'bg-accent' : i === at ? 'bg-accent animate-pulse' : 'bg-line2')} />)}</div>
      </div> : <div className="flex-1 min-w-[min(100%,260px)] [overflow-wrap:anywhere]">
        <span className="font-semibold">{t('最近一次部署失败', 'The last deployment failed')}</span>{d.message ? <span> · {shortDigests(d.message.split('\n')[0])}</span> : null}
        <div className="text-[12px] opacity-80 mt-0.5">{t('已自动保留上一个可用版本。', 'The previous working version was kept.')}</div>
      </div>}
      {git && <Button size="sm" onClick={() => setLog(true)}><ScrollText size={14} />{t('构建日志', 'Build log')}</Button>}
      {log && <BuildLogModal s={s} d={d} onClose={() => setLog(false)} />}
    </div>
  )
}
