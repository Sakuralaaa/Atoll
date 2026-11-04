import type { Project, Server } from './types'

export interface ServerGroup { id: string; server?: Server; projects: Project[] }

/** Groups projects by the machine they run on, in the servers' own order; unknown machines last. */
export function groupByServer(projects: Project[], servers: Server[] | undefined): ServerGroup[] {
  const by = new Map<string, Project[]>()
  for (const p of projects) by.set(p.server_id ?? '', [...(by.get(p.server_id ?? '') ?? []), p])
  const out: ServerGroup[] = []
  for (const s of servers ?? []) if (by.has(s.id)) { out.push({ id: s.id, server: s, projects: by.get(s.id)! }); by.delete(s.id) }
  for (const [id, ps] of by) out.push({ id, projects: ps })
  return out
}
