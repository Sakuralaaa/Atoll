// Builds from source (git repo -> docker build on the service's own server): REST calls, build-log hook, helpers.
// Kept out of client.ts so the feature stays self-contained (see docs/api.md "Builds from source").
import { useEffect, useRef, useState } from 'react'
import { isDemo, request } from './client'
import { onBuildLog } from './ws'
import type { Deployment, DeploymentLogLine, DeploymentLogs, GitBuilder, GitDetected, GitProvider, GitSource, GitWebhook, Service } from './types'

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
  /** preview stack detection on the service's server; the body overrides builder / stack / commands */
  detect: (sid: string, over?: Partial<Pick<GitSource, 'builder' | 'stack' | 'install_command' | 'build_command' | 'start_command'>>) =>
    request<GitDetected>('POST', `/services/${e(sid)}/git/detect`, over ?? {}),
}

export const isGit = (s?: Pick<Service, 'source' | 'git'>) => !!s && (s.source === 'git' || !!s.git)
/** Host and path of an https, ssh:// or scp-like (git@host:path) repo URL. */
export function parseRepo(repo?: string): { host: string; path: string; ssh: boolean } | undefined {
  const r = (repo ?? '').trim()
  let m = /^https:\/\/([^/@\s]+)\/(.+)$/i.exec(r)
  if (m) return { host: m[1].toLowerCase(), path: m[2], ssh: false }
  m = /^ssh:\/\/[^@/\s]+@([^/\s]+)\/(.+)$/i.exec(r) ?? /^[A-Za-z0-9._-]+@([A-Za-z0-9.-]+):([^/\s].*)$/.exec(r)
  if (m) return { host: m[1].toLowerCase(), path: m[2], ssh: true }
}
const cleanPath = (p: string) => p.replace(/\.git$/i, '').replace(/\/+$/, '')
/** forge guessed from the host (the server does the same when provider is omitted) */
export function inferProvider(repo?: string): GitProvider {
  const h = (parseRepo(repo)?.host ?? '').replace(/:\d+$/, '')
  if (h === 'github.com') return 'github'
  if (h === 'gitlab.com' || h.startsWith('gitlab.')) return 'gitlab'
  if (h === 'codeberg.org' || h === 'gitea.com' || h.startsWith('gitea.') || h.startsWith('forgejo.')) return 'gitea'
  return 'git'
}
/** "https://github.com/owner/repo(.git)" -> "owner/repo"; other hosts keep the host: "gitlab.com/group/app" */
export const repoPath = (repo?: string) => {
  const p = parseRepo(repo)
  if (!p) return (repo ?? '').replace(/\.git$/i, '')
  return p.host === 'github.com' ? cleanPath(p.path) : `${p.host.replace(/:\d+$/, '')}/${cleanPath(p.path)}`
}
/** web page of the repo (https forges; an SSH URL maps to the same host over https) */
export const repoUrl = (repo?: string) => {
  const p = parseRepo(repo)
  return p ? `https://${p.ssh ? p.host.replace(/:\d+$/, '') : p.host}/${cleanPath(p.path)}` : '#'
}
export const commitUrl = (repo: string | undefined, sha: string, provider?: GitProvider) =>
  `${repoUrl(repo)}/${(provider ?? inferProvider(repo)) === 'gitlab' ? '-/commit' : 'commit'}/${sha}`
export const shortSha = (sha?: string) => (sha ? sha.slice(0, 7) : '')
export const REPO_RE = /^(https:\/\/[A-Za-z0-9.-]+(:\d+)?\/[A-Za-z0-9_.~+/-]+|ssh:\/\/[A-Za-z0-9._-]+@[A-Za-z0-9.:[\]-]+\/[A-Za-z0-9_.~+/-]+|[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9_.~+][A-Za-z0-9_.~+/-]*)$/
export const isSshRepo = (repo: string) => !!parseRepo(repo)?.ssh
export const providerName: Record<GitProvider, string> = { github: 'GitHub', gitlab: 'GitLab', gitea: 'Gitea / Forgejo', git: 'Git' }

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
export interface GitForm {
  repo: string; branch: string; context: string; dockerfile: string; args: { k: string; v: string }[]; token: string; clearToken: boolean
  /** '' = from the host */ provider: GitProvider | ''; username: string
  sshKey: string; clearSshKey: boolean; knownHosts: string
  /** seconds, 0 = off */ poll: number
  builder: GitBuilder; stack: string; install: string; build: string; start: string
}
export const emptyGitForm = (): GitForm => ({ repo: '', branch: 'main', context: '', dockerfile: 'Dockerfile', args: [], token: '', clearToken: false,
  provider: '', username: '', sshKey: '', clearSshKey: false, knownHosts: '', poll: 0, builder: 'auto', stack: '', install: '', build: '', start: '' })
export const gitFormFrom = (g?: GitSource): GitForm => g ? {
  ...emptyGitForm(),
  repo: g.repo, branch: g.branch || 'main', context: g.context ?? '', dockerfile: g.dockerfile || 'Dockerfile',
  args: Object.entries(g.build_args ?? {}).map(([k, v]) => ({ k, v })),
  provider: g.provider ?? '', username: g.username ?? '', knownHosts: g.known_hosts ?? '', poll: g.poll_interval_sec ?? 0,
  builder: g.builder ?? 'auto', stack: g.stack ?? '', install: g.install_command ?? '', build: g.build_command ?? '', start: g.start_command ?? '',
} : emptyGitForm()
const ARG_RE = /^[A-Za-z_][A-Za-z0-9_]*$/
export function gitFormError(f: GitForm, t: (zh: string, en: string) => string): string | undefined {
  const repo = f.repo.trim()
  if (!repo) return t('请填写仓库地址', 'Repository URL is required')
  if (!REPO_RE.test(repo) || /^https:\/\/[^/]*@/.test(repo)) return t('仓库地址须为 https://主机/路径 或 SSH 地址（git@主机:路径），且不含凭据', 'Repository must be https://host/path or an SSH URL (git@host:path), without credentials')
  if ([f.context, f.dockerfile].some(p => p.split('/').includes('..') || p.startsWith('/'))) return t('路径不能离开仓库', 'Paths may not leave the repository')
  const keys = f.args.filter(a => a.k || a.v).map(a => a.k.trim())
  if (keys.some(k => !ARG_RE.test(k))) return t('构建参数名只能包含字母、数字和下划线', 'Build arg names: letters, digits and underscores')
  if (new Set(keys).size !== keys.length) return t('构建参数名重复', 'Duplicate build arg name')
  if (f.token.trim() && isSshRepo(repo)) return t('SSH 地址用部署密钥认证，不用 Token', 'SSH URLs authenticate with a deploy key, not a token')
  if (f.sshKey.trim() && !isSshRepo(repo)) return t('部署密钥只用于 SSH 地址', 'A deploy key is for SSH URLs only')
  if (f.sshKey.trim() && !/-----BEGIN [A-Z ]*PRIVATE KEY-----/.test(f.sshKey)) return t('部署密钥须为 PEM / OpenSSH 私钥', 'The deploy key must be a PEM / OpenSSH private key')
  if ([f.install, f.build, f.start].some(c => /[\r\n]/.test(c))) return t('命令只能写一行', 'Commands must be a single line')
}
/** Body for POST/PATCH `git`. On PATCH secrets are only sent when typed; clear_* removes them. */
export function gitBody(f: GitForm, patch = false): Partial<GitSource> {
  const build_args: Record<string, string> = {}
  f.args.filter(a => a.k.trim()).forEach(a => { build_args[a.k.trim()] = a.v })
  const b: Partial<GitSource> = { repo: f.repo.trim().replace(/\/+$/, ''), branch: f.branch.trim() || 'main', context: f.context.trim().replace(/^\.(\/|$)/, '').replace(/\/+$/, ''), dockerfile: f.dockerfile.trim() || 'Dockerfile', build_args,
    username: f.username.trim(), known_hosts: f.knownHosts.trim(), poll_interval_sec: f.poll, builder: f.builder,
    stack: f.stack, install_command: f.install.trim(), build_command: f.build.trim(), start_command: f.start.trim() }
  if (f.provider) b.provider = f.provider
  else if (patch) b.provider = inferProvider(f.repo)
  if (f.token.trim()) b.token = f.token.trim()
  else if (patch && f.clearToken) b.clear_token = true
  if (f.sshKey.trim()) b.ssh_key = f.sshKey.trim()
  else if (patch && f.clearSshKey) b.clear_ssh_key = true
  return b
}
export const autoName = (repo: string) => repoPath(repo).split(/[/:]/).pop()?.replace(/[^a-z0-9-]/gi, '-').toLowerCase() ?? ''
