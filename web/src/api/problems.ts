import { L } from '../i18n'
import { useProblems } from './queries'
import type { Problem } from './types'

const LABEL: Record<string, string> = {
  node_offline: L("服务器离线"), disk_high: L("磁盘空间不足"), service_down: L("服务异常"),
  deploy_failed: L("部署失败"), backup_failed: L("备份失败"), backup_verify_failed: L("备份校验失败"), domain_error: L("域名异常"),
}
export const problemLabel = (k: string) => LABEL[k] ?? k

/** The page that shows a problem's subject. */
export function problemHref(p: Problem) {
  if (p.kind === 'node_offline' || p.kind === 'disk_high') return `/servers/${p.subject_id}`
  if (p.kind === 'domain_error') return '/domains'
  if (p.kind === 'backup_verify_failed' && p.subject_id === 'backup_repo') return '/settings#backup'
  return p.project_id ? `/projects/${p.project_id}/services/${p.subject_id}` : '/'
}

/** Which page a problem belongs to: servers (offline, disk), projects (anything about a
 *  service, a deploy or a domain), or neither (the backup repository check: Settings). */
export function problemScope(p: Problem): 'servers' | 'projects' | '' {
  if (p.kind === 'node_offline' || p.kind === 'disk_high') return 'servers'
  if (p.kind === 'domain_error' || p.project_id) return 'projects'
  return ''
}

/** Firing problems, optionally only some kinds. */
export function useFiringProblems(only?: string[]) {
  return (useProblems().data ?? []).filter(p => !p.resolved_at && (!only || only.includes(p.kind)))
}
