import type { MemberQuota } from './types'

/** Text fields of a quota editor: "" = no limit. */
export type QuotaDraft = { cpu: string; mem: string; services: string }
export const quotaDraft = (q?: MemberQuota): QuotaDraft => ({ cpu: q?.cpu ? String(q.cpu) : '', mem: q?.mem_mb ? String(q.mem_mb) : '', services: q?.services ? String(q.services) : '' })
export const quotaFromDraft = (d: QuotaDraft): MemberQuota => ({ cpu: Number(d.cpu) || 0, mem_mb: Math.round(Number(d.mem) || 0), services: Math.round(Number(d.services) || 0) })
export const quotaDraftValid = (d: QuotaDraft) => [d.cpu, d.mem, d.services].every(v => v.trim() === '' || (Number.isFinite(Number(v)) && Number(v) >= 0))

