// Demo backend for database major upgrades (GET/POST /services/{id}/db-upgrade, .../rollback, DELETE .../old):
// an upgrade converts for 3 s, deploys for 2 s, then the service runs the new tag.
import type { Service } from './types'
import type { DBUpgrade, DBUpgradeInfo } from './dbupgrade'

export function makeDemoDBUpgrade(svc: (id: string) => Service, fail: (status: number, msg: string) => never) {
  const state = new Map<string, DBUpgrade>()
  const t0 = new Map<string, number>()
  const tagged = (image: string, tag: string) => { const i = image.lastIndexOf(':'); return (i > image.lastIndexOf('/') ? image.slice(0, i) : image) + ':' + tag }
  const advance = (s: Service, u: DBUpgrade) => {
    if (u.status !== 'running') return
    const t = Date.now() - (t0.get(s.id) ?? 0)
    if (t > 3000) u.steps[0] = { name: 'convert', status: 'done', detail: 'databases=3 tables=41 · dump 186.4 MB' }
    u.steps[1] = { name: 'deploy', status: t > 5000 ? 'done' : t > 3000 ? 'running' : 'pending', detail: t > 5000 ? u.to_image : undefined }
    if (t > 3000) u.old_kept = true
    if (t > 5000) { u.status = 'done'; u.finished_at = new Date().toISOString(); s.image = u.to_image }
  }
  const view = (s: Service): DBUpgradeInfo => {
    const u = state.get(s.id)
    if (u) advance(s, u)
    const supported = s.kind === 'postgres' && !s.git
    const last = u ? { ...u, steps: u.steps.map(x => ({ ...x })) } : undefined
    return { supported, reason: supported ? undefined : 'only PostgreSQL services have a one-click major upgrade', last }
  }
  return (method: string, m: (p: string) => string[] | null, body: any, canDeploy: () => boolean): unknown => {
    let a: string[] | null
    if (method === 'GET' && (a = m('services/*/db-upgrade'))) return view(svc(a[0]))
    if (method === 'POST' && (a = m('services/*/db-upgrade'))) {
      if (!canDeploy()) fail(403, 'missing permission: deploy')
      const s = svc(a[0]), info = view(s)
      if (!info.supported) fail(409, info.reason!)
      if (info.last?.status === 'running') fail(409, 'an upgrade of this service is running')
      if (info.last?.old_kept) fail(409, 'the data kept from the previous upgrade is still on the server; delete it or roll back first')
      const u = { service_id: s.id, status: 'running', from_image: s.image, to_image: tagged(s.image, String(body?.tag ?? '')), started_at: new Date().toISOString(), by: 'demo',
        steps: [{ name: 'convert', status: 'running' }, { name: 'deploy', status: 'pending' }] }
      state.set(s.id, u); t0.set(s.id, Date.now())
      return view(s).last
    }
    if (method === 'POST' && (a = m('services/*/db-upgrade/rollback'))) {
      const s = svc(a[0]), u = state.get(s.id)
      if (!u?.old_kept) fail(409, 'no data from before an upgrade is kept for this service')
      s.image = u!.from_image
      Object.assign(u!, { status: 'rolled_back', old_kept: false, error: 'rolled back by demo', finished_at: new Date().toISOString() })
      return view(s).last
    }
    if (method === 'DELETE' && (a = m('services/*/db-upgrade/old'))) {
      const s = svc(a[0]), u = state.get(s.id)
      if (!u?.old_kept) fail(409, 'no data from before an upgrade is kept for this service')
      u!.old_kept = false
      return view(s).last
    }
    return undefined
  }
}
