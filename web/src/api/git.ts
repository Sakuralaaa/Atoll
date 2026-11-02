// Builds from source (GitHub repo -> docker build on the service's own server): REST calls, build-log hook, helpers.
// Kept out of client.ts so the feature stays self-contained (see docs/api.md "Builds from source").
import { useEffect, useRef, useState } from 'react'
import { isDemo, request } from './client'
import { onBuildLog } from './ws'
import type { Deployment, DeploymentLogLine, DeploymentLogs, GitSource, GitWebhook, Service } from './types'

const e = encodeURIComponent

/** PATCH /services/{id} body for source changes: `git: null` + `image` switches back to a registry image. */
export type SourcePatch = { git: Partial<GitSource> } | { git: null; image: string }

export const gitApi = {
  /** secret only for a panel session */
  webhook: (sid: string) => request<GitWebhook>('GET', `/services/${e(sid)}/webhook`),
  rotate: (sid: string) => request<GitWebhook>('POST', `/services/${e(sid)}/webhook/rotate`, {}),
  logs: (sid: string, dep: string, after = 0, signal?: AbortSignal) =>
    request<DeploymentLogs>('GET', `/services/${e(sid)}/deployments/${e(dep)}/logs${after ? `?after=${after}` : ''}`, undefined, { signal }),
  patchSource: (sid: string, b: SourcePatch) => request<Service>('PATCH', `/services/${e(sid)}`, b),
}

export const isGit = (s?: Pick<Service, 'source' | 'git'>) => !!s && (s.source === 'git' || !!s.git)
/** "https://github.com/owner/repo(.git)" -> "owner/repo" */
export const repoPath = (repo?: string) => (repo ?? '').replace(/^https?:\/\/github\.com\//i, '').replace(/\.git$/i, '').replace(/\/+$/, '')
export const repoUrl = (repo?: string) => `https://github.com/${repoPath(repo)}`
export const commitUrl = (repo: string | undefined, sha: string) => `${repoUrl(repo)}/commit/${sha}`
export const shortSha = (sha?: string) => (sha ? sha.slice(0, 7) : '')
export const REPO_RE = /^https:\/\/github\.com\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+?(\.git)?\/?$/

/** Statuses after which a deployment's build log no longer grows. */
const FINAL = new Set(['running', 'failed', 'superseded', 'stopped'])
export const buildActive = (status?: string) => !status || !FINAL.has(status)

/** True when a deployment can have a build log: git service, or a building / built-from-commit deployment. */
export const hasBuildLog = (s: Pick<Service, 'source' | 'git'>, d: Deployment) => isGit(s) || d.status === 'building' || !!d.commit

/**
 * Build output of one deployment. Loads everything once, then fetches `?after=<last seq>` when a
 * build_log event arrives for it and every 2.5 s while the build is still active (polling covers a
 * dropped socket). Stops once the deployment reaches a final status. Mount with key={dep}.
 */
export function useBuildLogs(sid: string, dep: string) {
  const [lines, setLines] = useState<DeploymentLogLine[]>([])
  const [status, setStatus] = useState<string>()
  const [truncated, setTruncated] = useState(false)
  const [error, setError] = useState<Error>()
  const [loaded, setLoaded] = useState(false)
  const last = useRef(0)
  useEffect(() => {
    // one hook instance per deployment: callers mount the viewer with key={deployment id}
    const ac = new AbortController()
    let inflight = false, again = false, done = false, timer: ReturnType<typeof setTimeout> | undefined
    const fetchMore = async () => {
      if (done) return
      if (inflight) { again = true; return }
      inflight = true
      try {
        const r = await gitApi.logs(sid, dep, last.current, ac.signal)
        const fresh = (r.lines ?? []).filter(l => l.seq > last.current)
        if (fresh.length) { last.current = fresh[fresh.length - 1].seq; setLines(prev => [...prev, ...fresh].slice(-5000)) }
        setStatus(r.status); setTruncated(!!r.truncated); setError(undefined)
        if (!buildActive(r.status)) done = true
      } catch (err) {
        if ((err as Error).name === 'AbortError') return
        setError(err as Error)
      } finally {
        inflight = false; setLoaded(true)
      }
      if (again && !done) { again = false; void fetchMore() }
    }
    const tick = async () => { await fetchMore(); if (!done && !ac.signal.aborted) timer = setTimeout(tick, isDemo() ? 1200 : 2500) }
    void tick()
    let debounce: ReturnType<typeof setTimeout> | undefined
    const off = onBuildLog(ev => {
      if (ev.deployment_id !== dep || done) return
      if (!debounce) debounce = setTimeout(() => { debounce = undefined; void fetchMore() }, 200)
    })
    return () => { ac.abort(); off(); if (timer) clearTimeout(timer); if (debounce) clearTimeout(debounce) }
  }, [sid, dep])
  return { lines, status, truncated, error, loaded }
}

// ---- form model shared by the create flow and the edit dialog ----
export interface GitForm { repo: string; branch: string; context: string; dockerfile: string; args: { k: string; v: string }[]; token: string; clearToken: boolean }
export const emptyGitForm = (): GitForm => ({ repo: '', branch: 'main', context: '', dockerfile: 'Dockerfile', args: [], token: '', clearToken: false })
export const gitFormFrom = (g?: GitSource): GitForm => g ? {
  repo: g.repo, branch: g.branch || 'main', context: g.context ?? '', dockerfile: g.dockerfile || 'Dockerfile',
  args: Object.entries(g.build_args ?? {}).map(([k, v]) => ({ k, v })), token: '', clearToken: false,
} : emptyGitForm()
const ARG_RE = /^[A-Za-z_][A-Za-z0-9_]*$/
export function gitFormError(f: GitForm, t: (zh: string, en: string) => string): string | undefined {
  if (!f.repo.trim()) return t('请填写仓库地址', 'Repository URL is required')
  if (!REPO_RE.test(f.repo.trim())) return t('仓库地址须为 https://github.com/OWNER/REPO，且不含凭据', 'Repository must be https://github.com/OWNER/REPO, without credentials')
  if ([f.context, f.dockerfile].some(p => p.split('/').includes('..') || p.startsWith('/'))) return t('路径不能离开仓库', 'Paths may not leave the repository')
  const keys = f.args.filter(a => a.k || a.v).map(a => a.k.trim())
  if (keys.some(k => !ARG_RE.test(k))) return t('构建参数名只能包含字母、数字和下划线', 'Build arg names: letters, digits and underscores')
  if (new Set(keys).size !== keys.length) return t('构建参数名重复', 'Duplicate build arg name')
}
/** Body for POST/PATCH `git`. On PATCH the token is only sent when typed; clear_token removes it. */
export function gitBody(f: GitForm, patch = false): Partial<GitSource> {
  const build_args: Record<string, string> = {}
  f.args.filter(a => a.k.trim()).forEach(a => { build_args[a.k.trim()] = a.v })
  const b: Partial<GitSource> = { repo: f.repo.trim().replace(/\/+$/, ''), branch: f.branch.trim() || 'main', context: f.context.trim().replace(/^\.(\/|$)/, '').replace(/\/+$/, ''), dockerfile: f.dockerfile.trim() || 'Dockerfile', build_args }
  if (f.token.trim()) b.token = f.token.trim()
  else if (patch && f.clearToken) b.clear_token = true
  return b
}
export const autoName = (repo: string) => repoPath(repo).split('/').pop()?.replace(/[^a-z0-9-]/gi, '-').toLowerCase() ?? ''

