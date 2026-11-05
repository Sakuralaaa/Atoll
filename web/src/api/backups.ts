import { L } from '../i18n'
// Backups (restic to any S3 bucket): REST calls + hooks. Kept out of client.ts like updates.ts.
import { request } from './client'
import { useQuery } from './hooks'
import type { Backup, BackupPolicy, BackupRestoreRequest, BackupTestResult, BackupVerification, BackupVerifyStatus, Confirmation, RestoreJob, Settings } from './types'

const e = encodeURIComponent

export type BackupPolicyInput = Pick<BackupPolicy, 'enabled' | 'schedule' | 'timezone' | 'keep' | 'include_volumes'>

export const backupsApi = {
  policy: (sid: string) => request<BackupPolicy>('GET', `/services/${e(sid)}/backup-policy`),
  setPolicy: (sid: string, p: BackupPolicyInput) => request<BackupPolicy>('PUT', `/services/${e(sid)}/backup-policy`, p),
  list: (sid: string, refresh = false) => request<Backup[]>('GET', `/services/${e(sid)}/backups${refresh ? '?refresh=1' : ''}`),
  run: (sid: string) => request<Backup>('POST', `/services/${e(sid)}/backups`, {}),
  get: (id: string) => request<Backup>('GET', `/backups/${e(id)}`),
  /** Panel session runs it directly (RestoreJob); a token gets a pending Confirmation. */
  restore: (id: string, b: BackupRestoreRequest = {}) => request<RestoreJob | Confirmation>('POST', `/backups/${e(id)}/restore`, b),
  restores: (sid: string) => request<RestoreJob[]>('GET', `/services/${e(sid)}/restores`),
  restoreJob: (id: string) => request<RestoreJob>('GET', `/restores/${e(id)}`),
  /** Body fields override stored values so the form can be tested before saving. */
  test: (s: Partial<Settings> & { server_id?: string } = {}) => request<BackupTestResult>('POST', '/settings/backup/test', s),
  revealPassword: () => request<{ password: string; repository: string }>('GET', '/settings/backup/password'),
  clear: () => request<Settings>('DELETE', '/settings/backup'),
  /** Newest repository check (+ the service's newest restore drill) and their schedules. */
  verifyStatus: (sid?: string) => request<BackupVerifyStatus>('GET', sid ? `/services/${e(sid)}/backup-verify` : '/backup-verify'),
  /** Repository check now (restic check --read-data-subset) → the running record. */
  runCheck: (b: { server_id?: string; subset?: string } = {}) => request<BackupVerification>('POST', '/backup-verify/check', b),
  /** Restore drill of a service now (temporary resources only) → the running record. */
  runDrill: (sid: string) => request<BackupVerification>('POST', `/services/${e(sid)}/backup-drill`, {}),
  verifications: (q: { kind?: 'check' | 'drill'; service_id?: string; limit?: number } = {}) => {
    const p = new URLSearchParams(Object.entries(q).filter(([, v]) => v !== undefined && v !== '').map(([k, v]) => [k, String(v)])).toString()
    return request<BackupVerification[]>('GET', `/backup-verifications${p ? `?${p}` : ''}`)
  },
}

export const useBackupPolicy = (sid: string) => useQuery(`backup-policy:${sid}`, () => backupsApi.policy(sid))
/** Polls faster while a run is in progress (caller passes `running`). */
export const useBackups = (sid: string, running = false) => useQuery(`backups:${sid}`, () => backupsApi.list(sid), { interval: running ? 4000 : 60000 })
/** Polls faster while a check or drill is running (caller passes `running`). */
export const useVerifyStatus = (sid: string, running = false) => useQuery(`backup-verify:${sid}`, () => backupsApi.verifyStatus(sid), { interval: running ? 4000 : 60000 })
export const useRestores = (sid: string, running = false) => useQuery(`restores:${sid}`, () => backupsApi.restores(sid), { interval: running ? 4000 : 60000 })

export const shortSnap = (id?: string) => id ? id.slice(0, 8) : '—'

export function duration(ms: number) {
  if (!ms) return '—'
  if (ms < 1000) return `${ms} ms`
  const s = Math.round(ms / 1000)
  if (s < 60) return L("{0} 秒", s)
  const m = Math.floor(s / 60)
  return m < 60 ? L("{0} 分 {1} 秒", m, s % 60) : L("{0} 小时 {1} 分", Math.floor(m / 60), m % 60)
}
