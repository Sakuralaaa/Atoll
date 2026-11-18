import type { Service, ServiceDisk, VolumeDisk } from './types'

const MB = 1 << 20

/** Demo disk usage (GET /services/{id}/disk, PUT .../volumes/{name}/limit): a stable size per
 * volume from the service id, a slowly growing history, and limits kept in memory. */
export function makeDemoDisk(now: number, svc: (id: string) => Service, fail: (status: number, msg: string) => never) {
  const limits = new Map<string, { limit_mb: number; over_limit: 'stop' | 'alert' }>()
  const hash = (s: string) => [...s].reduce((a, c) => (a * 31 + c.charCodeAt(0)) >>> 0, 7)
  const size = (sid: string, vol: string, i: number) => {
    const h = hash(sid + vol)
    return i === 0 ? (180 + (h % 1600)) * MB : (4 + (h % 120)) * MB
  }
  // the first service with volumes gets a volume close to a limit, so the warning state shows
  const seeded = new Set<string>()
  const seed = (s: Service) => {
    if (seeded.has(s.id)) return
    seeded.add(s.id)
    if (s.kind === 'postgres' && !limits.has(`${s.id}/${s.volumes?.[0]?.name}`) && s.volumes?.length)
      limits.set(`${s.id}/${s.volumes[0].name}`, { limit_mb: Math.ceil(size(s.id, s.volumes[0].name, 0) / MB / 0.86 / 100) * 100, over_limit: 'stop' })
  }
  const view = (s: Service, range: string): ServiceDisk => {
    seed(s)
    const rootfs = (2 + (hash(s.id) % 60)) * MB
    const volumes: VolumeDisk[] = (s.volumes ?? []).map((v, i) => {
      const l = limits.get(`${s.id}/${v.name}`)
      return v.host_path ? { name: v.name, mount_path: v.mount_path, bytes: 0, measured: false, host_path: true }
        : { name: v.name, mount_path: v.mount_path, bytes: size(s.id, v.name, i), measured: true, ...(l ?? {}) }
    })
    const total = rootfs + volumes.reduce((a, v) => a + v.bytes, 0)
    const n = range === '1h' ? 6 : 48, step = (range === '1h' ? 3600e3 : range === '7d' ? 7 * 864e5 : 864e5) / n
    const history = Array.from({ length: n }, (_, i) => ({ at: new Date(now - (n - 1 - i) * step).toISOString(), bytes: Math.round(total * (0.9 + 0.1 * i / (n - 1))) }))
    return { supported: true, measured_at: new Date(now - 4 * 60e3).toISOString(), total_bytes: total, rootfs_bytes: rootfs, volumes, history }
  }
  return {
    route(method: string, m: (p: string) => string[] | null, body: any, q: URLSearchParams, canDeploy: () => boolean): unknown {
      let a: string[] | null
      if (method === 'GET' && (a = m('services/*/disk'))) return view(svc(a[0]), q.get('range') ?? '24h')
      if (method === 'PUT' && (a = m('services/*/volumes/*/limit'))) {
        if (!canDeploy()) fail(403, 'missing permission: deploy')
        const s = svc(a[0]), v = s.volumes?.find(x => x.name === a![1])
        if (!v) fail(404, `service has no volume "${a[1]}"`)
        if (v.host_path) fail(400, `volume "${a[1]}" mounts a host directory; Atoll does not measure or limit it`)
        const lim = Number(body?.limit_mb ?? 0)
        if (!(lim >= 0)) fail(400, 'limit_mb must be between 0 (no limit) and 1073741824')
        const ol = body?.over_limit || 'stop'
        if (ol !== 'stop' && ol !== 'alert') fail(400, 'over_limit must be stop or alert')
        if (lim > 0) limits.set(`${s.id}/${v.name}`, { limit_mb: lim, over_limit: ol })
        else limits.delete(`${s.id}/${v.name}`)
        return view(s, '1h')
      }
      return undefined
    },
  }
}
