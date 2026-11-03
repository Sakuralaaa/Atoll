import { useProblems } from './queries'
import type { Problem } from './types'

const LABEL: Record<string, string> = {
  node_offline: '服务器离线', disk_high: '磁盘空间不足', service_down: '服务异常',
  deploy_failed: '部署失败', backup_failed: '备份失败', domain_error: '域名异常',
}
export const problemLabel = (k: string) => LABEL[k] ?? k

/** The page that shows a problem's subject. */
export function problemHref(p: Problem) {
  if (p.kind === 'node_offline' || p.kind === 'disk_high') return `/servers/${p.subject_id}`
  if (p.kind === 'domain_error') return '/domains'
  return p.project_id ? `/projects/${p.project_id}/services/${p.subject_id}` : '/'
}

/** Firing problems, optionally only some kinds. */
export function useFiringProblems(only?: string[]) {
  return (useProblems().data ?? []).filter(p => !p.resolved_at && (!only || only.includes(p.kind)))
}
