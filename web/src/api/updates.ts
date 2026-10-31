// Image update detection: REST calls + hooks. Kept out of client.ts so the feature stays self-contained.
import { request } from './client'
import { useQuery } from './hooks'
import type { Deployment, ImageUpdate, ImageUpdatePolicy } from './types'

const e = encodeURIComponent

export const updatesApi = {
  list: () => request<ImageUpdate[]>('GET', '/image-updates'),
  get: (sid: string) => request<ImageUpdate>('GET', `/services/${e(sid)}/image-update`),
  check: (sid: string) => request<ImageUpdate>('POST', `/services/${e(sid)}/image-update/check`, {}),
  setPolicy: (sid: string, p: ImageUpdatePolicy) => request<ImageUpdate>('PUT', `/services/${e(sid)}/image-update/policy`, p),
  apply: (sid: string, tag?: string) => request<Deployment>('POST', `/services/${e(sid)}/image-update/apply`, tag ? { tag } : {}),
}

/** Every service's update status (one request, shared by badges in lists and cards). */
export const useImageUpdates = () => useQuery('image-updates', updatesApi.list)

/** service id -> status, for badges. */
export function useUpdatesByService(): Record<string, ImageUpdate> {
  const q = useImageUpdates()
  const out: Record<string, ImageUpdate> = {}
  for (const u of q.data ?? []) out[u.service_id] = u
  return out
}

export const useImageUpdate = (sid: string) => useQuery(`image-update:${sid}`, () => updatesApi.get(sid))

export const shortDigest = (d?: string) => d ? d.replace(/^sha256:/, '').slice(0, 12) : '—'
