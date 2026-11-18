// Database major-version upgrades: REST calls + hook (GET/POST /services/{id}/db-upgrade, .../rollback, DELETE .../old).
import { useEffect, useRef, useState } from 'react'
import { request } from './client'
import { invalidate, useQuery } from './hooks'

export interface DBUpgradeStep { name: 'convert' | 'deploy' | string; status: 'pending' | 'running' | 'done' | 'failed' | 'skipped' | string; detail?: string }
export interface DBUpgrade {
  service_id: string
  status: 'running' | 'done' | 'failed' | 'rolled_back' | string
  from_image: string; to_image: string
  steps: DBUpgradeStep[]
  error?: string
  /** the data from before the upgrade is still on the server (rollback possible) */
  old_kept?: boolean
  started_at: string; finished_at?: string; by?: string
}
export interface DBUpgradeInfo { supported: boolean; reason?: string; last?: DBUpgrade }

const e = encodeURIComponent
export const dbUpgradeApi = {
  get: (sid: string) => request<DBUpgradeInfo>('GET', `/services/${e(sid)}/db-upgrade`),
  start: (sid: string, tag: string) => request<DBUpgrade>('POST', `/services/${e(sid)}/db-upgrade`, { tag }),
  rollback: (sid: string) => request<DBUpgrade>('POST', `/services/${e(sid)}/db-upgrade/rollback`, {}),
  cleanup: (sid: string) => request<DBUpgrade>('DELETE', `/services/${e(sid)}/db-upgrade/old`),
}

export const DB_UPGRADE_KINDS = new Set(['postgres'])

/** The service's upgrade state; polls every 2 s while one runs and refreshes the service and
 * its image card when it ends. null key = not a database. */
export function useDBUpgrade(sid: string | null, projectId?: string) {
  const [poll, setPoll] = useState(false)
  const q = useQuery(sid ? `db-upgrade:${sid}` : null, () => dbUpgradeApi.get(sid!), { interval: poll ? 2000 : 0 })
  const running = q.data?.last?.status === 'running'
  if (running !== poll) setPoll(running)
  const was = useRef(running)
  useEffect(() => {
    if (was.current && !running && sid) invalidate(`image-update:${sid}`, 'image-updates', `deployments:${sid}`, ...(projectId ? [`project:${projectId}`] : []), 'projects')
    was.current = running
  }, [running, sid, projectId])
  return q
}
