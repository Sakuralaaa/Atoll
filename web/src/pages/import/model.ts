import { L } from '../../i18n'
// Zeabur import: labels, status helpers and shared queries (no components, so fast refresh stays happy).
import { api } from '../../api/client'
import { useQuery } from '../../api/hooks'
import type { ZeaburProject, ZeaburService } from '../../api/types'

export const ROLE: Record<string, [string, 'violet' | 'amber' | 'green' | 'zinc']> = { app: [L("应用"), 'violet'], database: [L("数据库"), 'amber'], cache: [L("缓存"), 'green'], other: [L("其他"), 'zinc'] }
export const isZeaburApp = (h: string) => h.endsWith('.zeabur.app')
export const ZSTATUS: Record<string, [string, 'green' | 'amber' | 'red' | 'zinc']> = {
  running: [L("运行中"), 'green'], progressing: [L("更新中"), 'amber'], degraded: [L("降级"), 'amber'], failed: [L("失败"), 'red'], suspended: [L("已暂停"), 'zinc'], unknown: [L("状态未知"), 'zinc'],
}
export const zStatus = (s: ZeaburService) => s.status || (!s.workload ? 'suspended' : s.replicas ? 'running' : 'unknown')
export const STORE: Record<string, string> = { postgres: 'PostgreSQL', mysql: 'MySQL', redis: 'Redis', mongo: 'MongoDB', queue: L("消息队列"), search: L("搜索引擎"), other: L("其他数据存储") }
export const VIA: Record<string, string> = { 'env-value': L("变量值"), 'zeabur-internal': 'zeabur.internal', 'shared-secret': L("共享 Secret"), 'shared-configmap': L("共享 ConfigMap") }
/** Job states that are still moving (poll fast, show a spinner). */
export const LIVE = ['rehearsing', 'cutting_over', 'rolling_back', 'cleaning', 'deploying', 'uploading', 'verifying']
export const useScan = (serverId: string) => useQuery(`zeabur:${serverId}`, () => api.zeaburScan(serverId))
export const projBytes = (p: ZeaburProject) => p.services.reduce((a, s) => a + s.volumes.reduce((b, v) => b + v.bytes, 0), 0)
