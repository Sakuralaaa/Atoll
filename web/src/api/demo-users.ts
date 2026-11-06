import { ApiError } from './client'
import type { Me, PanelUser, Project, Server } from './types'

/**
 * Demo sub-accounts (GET/POST /users, PATCH/DELETE /users/{id}) and the member view.
 * `?demo=1&member=1` (search or hash query) signs the tab in as the member "alice":
 * /auth/me carries role/permissions/servers, lists are filtered to her servers and the
 * yellow "change your password" banner shows until she changes it. `member=ro` is the same
 * account with only the read permission (every write button hidden). `member=0` goes back.
 */
export function makeDemoUsers(servers: Server[], now: number) {
  const iso = (msAgo: number) => new Date(now - msAgo).toISOString()
  const fail = (status: number, msg: string): never => { throw new ApiError(status, msg) }
  const workers = servers.filter(s => s.role !== 'control').map(s => s.id)
  const users: PanelUser[] = [
    { id: 1, username: 'demo', role: 'admin', created_at: iso(40 * 864e5) },
    { id: 2, username: 'alice', role: 'member', permissions: ['read', 'deploy'], servers: workers.slice(0, 1), must_change_password: true, created_at: iso(2 * 864e5), root_domains: ['example.com'],
      quota: { cpu: 4, mem_mb: 8192, services: 12 }, usage: { cpu: 2.5, mem_mb: 5632, services: 8, unlimited: 1 } },
    { id: 3, username: 'bob', role: 'member', permissions: ['read', 'deploy', 'exec', 'domains'], servers: workers.slice(1, 2), disabled: true, created_at: iso(9 * 864e5) },
  ]
  let seq = 4
  const flag = () => {
    const hq = location.hash.includes('?') ? location.hash.slice(location.hash.indexOf('?') + 1) : ''
    const v = new URLSearchParams(location.search).get('member') ?? new URLSearchParams(hq).get('member')
    if (v === '0') sessionStorage.removeItem('atoll-demo-member')
    if (v === '1' || v === 'ro') sessionStorage.setItem('atoll-demo-member', v)
    return sessionStorage.getItem('atoll-demo-member') ?? ''
  }
  const member = () => {
    const f = flag(), u = f ? users.find(x => x.id === 2) : undefined
    return u && f === 'ro' ? { ...u, permissions: ['read'] } : u
  }
  const quotaOf = (q?: { cpu?: number; mem_mb?: number; services?: number }) => {
    if (!q) return undefined
    if ((q.cpu ?? 0) < 0 || (q.mem_mb ?? 0) < 0 || (q.services ?? 0) < 0) fail(400, 'quota values must be 0 (no limit) or positive')
    const out = { cpu: q.cpu || undefined, mem_mb: q.mem_mb || undefined, services: q.services || undefined }
    return out.cpu || out.mem_mb || out.services ? out : undefined
  }
  const clean = (s: string[] | undefined) => [...new Set((s ?? []).map(String))]
  return {
    /** The signed-in member, or undefined for the admin demo. */
    member,
    me(base: Me): Me {
      const u = member()
      if (!u) return { ...base, role: 'admin' }
      return { ...base, id: String(u.id), username: u.username, role: 'member', permissions: u.permissions, servers: u.servers, must_change_password: u.must_change_password || undefined, quota: u.quota, usage: u.quota ? u.usage : undefined }
    },
    /** B7: a member may bind only strict subdomains of its allowed roots; other configured roots,
     *  the root itself and wildcards are refused; outside domains stay allowed. */
    checkHost(host: string, roots: string[]) {
      const u = member()
      if (!u) return
      if (host.includes('*')) fail(403, 'sub-accounts cannot bind wildcard hostnames')
      const root = roots.filter(r => host === r || host.endsWith('.' + r)).sort((a, b) => b.length - a.length)[0]
      if (!root) return
      if (!(u.root_domains ?? []).includes(root)) fail(403, `this account may not use the root domain ${root}; ask an admin to allow it`)
      if (host === root) fail(403, `sub-accounts can only bind subdomains of ${root}, not ${root} itself`)
    },
    passwordChanged() { const u = users.find(x => x.id === 2); if (flag() && u) u.must_change_password = false },
    servers: (all: Server[]) => { const u = member(); return u ? all.filter(s => u.servers?.includes(s.id)) : all },
    projects: <T extends Project>(all: T[]) => { const u = member(); return u ? all.filter(p => u.servers?.includes(p.server_id)) : all },
    route(method: string, m: (p: string) => string[] | null, body: any): unknown {
      let a: string[] | null
      if (!m('users') && !m('users/*')) return undefined
      if (member()) fail(403, 'admin only')
      if (method === 'GET' && m('users')) return users
      if (method === 'POST' && m('users')) {
        const name = String(body?.username ?? '').trim()
        if (!/^[A-Za-z0-9_.@-]{1,64}$/.test(name)) fail(400, 'username must be 1-64 characters: letters, digits, _ . @ -')
        if (users.some(u => u.username === name)) fail(409, 'username taken')
        if (String(body?.password ?? '').length < 8) fail(400, 'password must be at least 8 characters')
        const u: PanelUser = { id: seq++, username: name, role: 'member', permissions: clean(['read', ...(body?.permissions ?? [])]), servers: clean(body?.servers), must_change_password: true, created_at: new Date().toISOString(), root_domains: clean(body?.root_domains), quota: quotaOf(body?.quota), usage: { cpu: 0, mem_mb: 0, services: 0 } }
        users.push(u); return u
      }
      if ((a = m('users/*'))) {
        const u = users.find(x => x.id === Number(a![0])) ?? fail(404, 'user not found')
        if (method === 'DELETE') { if (u.role === 'admin') fail(400, 'cannot delete an admin'); users.splice(users.indexOf(u), 1); return {} }
        if (method === 'PATCH') {
          if (body?.permissions) u.permissions = clean(['read', ...body.permissions])
          if (body?.servers) u.servers = clean(body.servers)
          if (typeof body?.disabled === 'boolean') u.disabled = body.disabled || undefined
          if (body?.password) u.must_change_password = true
          if (body?.quota) u.quota = quotaOf(body.quota)
          if (body?.root_domains) u.root_domains = clean(body.root_domains)
          return u
        }
      }
      return undefined
    },
  }
}
