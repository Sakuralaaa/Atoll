import { useSearchParams } from 'react-router-dom'
import type { Problem, Server, Service } from './types'
import { needsAttention } from './view'

// List filters (项目 / 服务 / 服务器 chips). The chosen chip lives in the URL so a view can be shared.

/** One filter chip: a URL value, its zh/en label and the test an item must pass. */
export interface FilterDef<T> { id: string; zh: string; en: string; test: (x: T) => boolean; alert?: boolean }

/** The search param every list filter is kept in (`#/servers?filter=offline`), so a view can be shared. */
export const FILTER_PARAM = 'filter'

/** Filter `items` by the chip named in the URL; unknown values fall back to all. */
export function useStatusFilter<T>(items: T[], defs: FilterDef<T>[]) {
  const [sp, setSp] = useSearchParams()
  const raw = sp.get(FILTER_PARAM) ?? ''
  const def = defs.find(d => d.id === raw)
  const set = (id: string) => {
    const n = new URLSearchParams(sp)
    if (id && defs.some(d => d.id === id)) n.set(FILTER_PARAM, id); else n.delete(FILTER_PARAM)
    setSp(n, { replace: true })
  }
  const counts = Object.fromEntries(defs.map(d => [d.id, items.filter(d.test).length]))
  return { active: def?.id ?? '', def, set, counts, total: items.length, list: def ? items.filter(def.test) : items }
}

/** Kinds of firing problems that are about one service (subject_id = service id). */
const SERVICE_PROBLEMS = ['service_down', 'deploy_failed', 'backup_failed']
export const serviceHasProblem = (s: Service, problems: Problem[]) =>
  s.status === 'failed' || problems.some(p => SERVICE_PROBLEMS.includes(p.kind) && p.subject_id === s.id)

/** Service chips: 异常 / 运行中 / 已停止 / 部署中. */
export function serviceFilters(problems: Problem[]): FilterDef<Service>[] {
  return [
    { id: 'problem', zh: '异常', en: 'Problems', alert: true, test: s => serviceHasProblem(s, problems) },
    { id: 'running', zh: '运行中', en: 'Running', test: s => s.status === 'running' },
    { id: 'stopped', zh: '已停止', en: 'Stopped', test: s => s.status === 'stopped' },
    { id: 'deploying', zh: '部署中', en: 'Deploying', test: s => s.status === 'deploying' },
  ]
}

/** Project chips: a project matches when one of its services does (异常 also counts its firing problems). */
export function projectFilters<P extends { id: string; services?: Service[] }>(problems: Problem[]): FilterDef<P>[] {
  return serviceFilters(problems).map(d => ({
    ...d,
    test: (p: P) => (p.services ?? []).some(d.test) || (d.id === 'problem' && problems.some(x => x.project_id === p.id)),
  }))
}

/** Server chips: 在线 / 离线 / 异常 (a firing node problem, or CPU / memory / disk running high). */
export function serverFilters(problems: Problem[]): FilterDef<Server>[] {
  const firing = (s: Server) => problems.some(p => (p.kind === 'node_offline' || p.kind === 'disk_high') && (p.subject_id === s.id || p.server_id === s.id))
  return [
    { id: 'online', zh: '在线', en: 'Online', test: s => s.status === 'online' },
    { id: 'offline', zh: '离线', en: 'Offline', test: s => s.status === 'offline' },
    { id: 'problem', zh: '异常', en: 'Problems', alert: true, test: s => firing(s) || needsAttention(s) },
  ]
}
