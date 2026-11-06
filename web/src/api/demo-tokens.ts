import { ApiError } from './client'
import type { APIToken } from './types'
import type { makeDemoUsers } from './demo-users'

const ALL = ['read', 'deploy', 'delete', 'migrate', 'domains', 'restore', 'servers', 'settings', 'tokens']
const GLOBAL = ['servers', 'settings', 'tokens']
const fail = (status: number, msg: string, code?: string): never => { throw new ApiError(status, msg, code) }

/** mirrors the server: read always on, global perms rejected with a project scope, legacy scope summary */
function tokenPerms(b: { scope?: string; permissions?: string[]; projects?: string[]; require_approval?: boolean }) {
  let perms = b.permissions?.length ? b.permissions : b.scope === 'admin' ? ALL : b.scope === 'deploy' ? ['read', 'deploy'] : ['read']
  perms = ALL.filter(p => p === 'read' || perms.includes(p))
  const projects = b.projects ?? []
  if (projects.length && perms.some(p => GLOBAL.includes(p))) fail(400, 'servers/settings/tokens permissions cannot be combined with a project-scoped token')
  const scope = perms.length === ALL.length ? 'admin' : perms.includes('deploy') ? 'deploy' : 'read'
  return { permissions: perms, projects, require_approval: !!b.require_approval, scope }
}

/** Demo /tokens. The admin sees every token (a member's carries owner/owner_role); the member
 *  alice (`?member=1`) sees and manages only hers, never beyond her own permissions. */
export function makeDemoTokens(tokens: APIToken[], demoUsers: ReturnType<typeof makeDemoUsers>, nid: (p: string) => string) {
  for (const t of tokens) Object.assign(t, { owner: t.owner ?? 'demo', owner_role: t.owner_role ?? 'admin' })
  tokens.push({ id: 'tok_alice_ci', name: 'alice-github-actions', scope: 'deploy', permissions: ['read', 'deploy'], prefix: 'atl_pat_aL1c', owner: 'alice', owner_role: 'member',
    last_used_at: new Date(Date.now() - 3 * 3600e3).toISOString(), created_at: new Date(Date.now() - 2 * 864e5).toISOString() })
  const mine = () => { const u = demoUsers.member(); return u ? tokens.filter(t => t.owner === u.username) : tokens }
  const own = (id: string) => mine().find(t => t.id === id) ?? fail(404, 'not found')
  const memberGrant = (perms: string[], requireApproval: boolean) => {
    const u = demoUsers.member()
    if (!u) return
    const bad = perms.find(p => GLOBAL.includes(p) || !(u.permissions ?? []).includes(p))
    if (bad) fail(403, `your account does not hold the "${bad}" permission, so its tokens cannot either`, 'escalation')
    if (requireApproval) fail(400, 'sub-account tokens cannot require approval (approvals are the admin\'s)')
  }
  return {
    route(method: string, m: (p: string) => string[] | null, body: any): unknown {
      let a: string[] | null
      if (method === 'GET' && m('tokens')) return mine()
      if (method === 'POST' && m('tokens')) {
        const tp = tokenPerms(body); memberGrant(tp.permissions, tp.require_approval)
        const u = demoUsers.member()
        const t: APIToken = { id: nid('tok'), name: body.name, ...tp, prefix: 'atl_pat_' + Math.random().toString(36).slice(2, 6), created_at: new Date().toISOString(), owner: u?.username ?? 'demo', owner_role: u ? 'member' : 'admin' }
        tokens.unshift(t); return { ...t, token: t.prefix + 'Xk2Vb8NqR4mZt7Lw1PcY9sHd' }
      }
      if (method === 'PATCH' && (a = m('tokens/*'))) { const t = own(a[0]); const tp = tokenPerms({ ...t, ...body }); memberGrant(tp.permissions, tp.require_approval); Object.assign(t, tp); if (body.name) t.name = body.name; return t }
      if (method === 'DELETE' && (a = m('tokens/*'))) { const t = own(a[0]); tokens.splice(tokens.indexOf(t), 1); return {} }
      return undefined
    },
  }
}
