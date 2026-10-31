// Demo backend for image update detection (GET /image-updates, /services/{id}/image-update/...).
import { ApiError } from './client'
import type { Deployment, ImageUpdate, Service } from './types'

type Seed = { newer?: string; major?: string; moved?: boolean; policy?: string; window?: string; status?: string }
// Generic demo services only (see mock.ts).
const SEED: Record<string, Seed> = {
  'mail-relay': { newer: 'v2.4.0' },
  'acme-api': { moved: true, policy: 'auto', window: '18:00-21:00' },
  chat: { major: '2.0.0' },
  redis: { major: '8-alpine' },
  cron: { status: 'unknown' },
  flare: { policy: 'off' },
}

const H = 3600e3
const fail = (status: number, msg: string): never => { throw new ApiError(status, msg) }

export function makeDemoUpdates(services: Service[], deployments: Record<string, Deployment[]>) {
  const state: Record<string, ImageUpdate> = {}
  const tagOf = (image: string) => { const i = image.lastIndexOf(':'); return i > image.lastIndexOf('/') ? image.slice(i + 1) : 'latest' }
  const nameOf = (image: string) => { const i = image.lastIndexOf(':'); return i > image.lastIndexOf('/') ? image.slice(0, i) : image }
  const changelog = (image: string) => {
    const n = nameOf(image)
    if (n.startsWith('ghcr.io/')) { const [o, r] = n.slice(8).split('/'); return `https://github.com/${o}/${r}/releases` }
    if (!n.includes('.') && !n.includes('/')) return `https://hub.docker.com/_/${n}`
    if (!n.split('/')[0].includes('.')) return `https://hub.docker.com/r/${n}/tags`
    return undefined
  }
  const view = (s: Service): ImageUpdate => {
    if (!state[s.id]) {
      const seed = SEED[s.id] ?? {}
      const cur = deployments[s.id]?.find(d => d.image_digest)?.image_digest
      const latest = seed.moved ? 'sha256:9e41b7c2d05a3f68e1b4c7d29a0f5e3b81c6d4a7' : cur
      state[s.id] = {
        service_id: s.id, project_id: s.project_id, service: s.name, image: s.image, policy: seed.policy ?? 'notify', window: seed.window,
        status: 'up_to_date', update_available: false, digest_changed: false, current_digest: cur, latest_digest: seed.status === 'unknown' ? undefined : latest,
        current_tag: tagOf(s.image), newer_tag: seed.newer, major_tag: seed.major, changelog_url: changelog(s.image),
        checked_at: seed.status === 'unknown' ? undefined : new Date(Date.now() - 2.4 * H).toISOString(),
        next_check_at: new Date(Date.now() + 3.6 * H).toISOString(),
      }
      if (seed.status === 'unknown') { state[s.id].status = 'unknown'; state[s.id].note = 'not checked yet' }
    }
    const u = state[s.id]
    u.image = s.image; u.service = s.name
    if (u.policy === 'off') return { ...u, status: 'disabled', update_available: false, digest_changed: false }
    if (u.status === 'unknown') return u
    u.digest_changed = !!u.current_digest && !!u.latest_digest && u.current_digest !== u.latest_digest
    u.update_available = u.digest_changed || !!u.newer_tag
    u.status = u.update_available ? 'update_available' : 'up_to_date'
    u.note = u.policy === 'auto' && u.digest_changed && u.window ? `auto-update waits for the window ${u.window} UTC` : undefined
    return u
  }
  const svc = (id: string) => services.find(s => s.id === id) ?? fail(404, '服务不存在')

  /** Returns undefined when the path is not an image-update route. */
  return function route(method: string, seg: string[], body: any): unknown {
    if (method === 'GET' && seg.length === 1 && seg[0] === 'image-updates') return services.map(view)
    if (seg[0] !== 'services' || seg[2] !== 'image-update') return undefined
    const s = svc(decodeURIComponent(seg[1]))
    const u = view(s)
    const sub = seg[3]
    if (!sub && method === 'GET') return u
    if (sub === 'check') {
      if (u.status === 'unknown') { u.status = 'up_to_date'; u.latest_digest = u.current_digest; u.note = undefined }
      u.checked_at = new Date().toISOString(); u.next_check_at = new Date(Date.now() + 6 * H).toISOString()
      return view(s)
    }
    if (sub === 'policy') {
      if (!['notify', 'auto', 'off'].includes(body?.policy)) fail(400, 'policy must be notify, auto or off')
      if (body.window && !/^([01]\d|2[0-3]):[0-5]\d-([01]\d|2[0-3]):[0-5]\d$/.test(body.window)) fail(400, 'window must look like 02:00-05:00')
      u.policy = body.policy; u.window = body.policy === 'auto' ? body.window || undefined : undefined
      return view(s)
    }
    if (sub === 'apply') {
      const tag: string | undefined = body?.tag
      const digest = tag ? `sha256:${Math.random().toString(16).slice(2, 10)}4c1e0b7d93a2f586e1c0` : u.latest_digest!
      if (tag) { s.image = `${nameOf(s.image)}:${tag}`; u.current_tag = tag; u.newer_tag = undefined; if (u.major_tag === tag) u.major_tag = undefined }
      const list = deployments[s.id] ?? (deployments[s.id] = [])
      list.forEach(d => { if (d.status === 'running') d.status = 'superseded' })
      const d: Deployment = { id: `dep_demo${Date.now().toString(36)}`, service_id: s.id, server_id: '', image_digest: digest, status: 'running', trigger: 'image-update',
        message: `image update: ${nameOf(s.image)}:${u.current_tag} → ${digest.slice(7, 19)}`, started_at: new Date().toISOString(), finished_at: new Date().toISOString() }
      list.unshift(d)
      u.current_digest = digest; u.latest_digest = digest; u.checked_at = new Date().toISOString()
      view(s)
      return d
    }
    return fail(404, `demo: 未实现 ${method} /${seg.join('/')}`)
  }
}
