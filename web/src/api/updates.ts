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

/** "ghcr.io/x/komari@sha256:5c43…" -> { name, tag?, digest? } */
export function splitImage(image: string): { name: string; tag?: string; digest?: string } {
  const at = image.indexOf('@')
  if (at >= 0) return { name: image.slice(0, at), digest: image.slice(at + 1) }
  const i = image.lastIndexOf(':')
  return i > image.lastIndexOf('/') ? { name: image.slice(0, i), tag: image.slice(i + 1) } : { name: image }
}

/** GitHub repo page of a GHCR image ("ghcr.io/owner/repo[/sub]:tag" -> https://github.com/owner/repo); undefined for other registries. */
export function imageRepoUrl(image?: string): string | undefined {
  const m = /^ghcr\.io\/([A-Za-z0-9-]+)\/([A-Za-z0-9._-]+)/i.exec(splitImage(image ?? '').name)
  return m ? `https://github.com/${m[1]}/${m[2]}` : undefined
}

/** Human label for what an image ref runs: "1.2.5 · 5c43b1b" for a digest pin, the tag otherwise. */
export function imageVersionLabel(image: string, version?: string): string {
  const p = splitImage(image)
  if (p.digest) return [version, p.digest.replace(/^sha256:/, '').slice(0, 7)].filter(Boolean).join(' · ')
  return version && version !== p.tag ? `${p.tag ?? 'latest'} (${version})` : p.tag ?? 'latest'
}
