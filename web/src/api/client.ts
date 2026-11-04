import type {
  AgentUpgrade, AgentUpgradeRow, APIToken, AuthStatus, Confirmation, ContainerMetrics, ContainerState, Deployment, Domain, CloudflareTokenCheck, RootDomain, EnvReference, EnvVar,
  ImportJob, ImportPlan, InstallCommand, JoinToken, NetworkResult, ServerNetworkRequest, TokenRequest, TokenUpdate, LoginChallenge, RecoveryCodes, ChangePasswordRequest, ChangePasswordResponse, SessionInfo, TwoFactorEnrollment, TwoFactorStatus, MigrationJob, ZeaburInventory, ZeaburLocalImportRequest, DockerInventory, DockerAdoptRequest, ZeaburCleanupRequest, ZeaburNodeCutoverRequest, ZeaburNodeOp, Me, NodeMetrics, Port, Project, ResultPayload, Server, Service, Settings, Template, Volume, Problem, NotificationSettings, NotificationChannel,
} from './types'

export const API = '/api/v1'

export class ApiError extends Error {
  status: number; code?: string
  /** seconds, from Retry-After (429 lockouts) */
  retryAfter?: number
  constructor(status: number, message: string, code?: string, retryAfter?: number) { super(message); this.status = status; this.code = code; this.retryAfter = retryAfter }
}

let demo: boolean | undefined
/** Demo mode: `?demo=1` (in search or hash query), VITE_DEMO=1, or remembered for this tab. */
export function isDemo(): boolean {
  if (demo !== undefined) return demo
  const hashQ = location.hash.includes('?') ? location.hash.slice(location.hash.indexOf('?')) : ''
  const q = new URLSearchParams(location.search).get('demo') ?? new URLSearchParams(hashQ).get('demo')
  if (q === '0') { sessionStorage.removeItem('atoll-demo'); return (demo = false) }
  demo = q === '1' || import.meta.env.VITE_DEMO === '1' || sessionStorage.getItem('atoll-demo') === '1'
  if (demo) sessionStorage.setItem('atoll-demo', '1')
  return demo
}

type Opts = { noRedirect?: boolean; raw?: BodyInit; signal?: AbortSignal }

export function toLogin() {
  if (!location.hash.startsWith('#/login')) location.hash = '#/login'
}

/** The single transport: every page goes through this, demo mode swaps the backend underneath. */
export async function request<T>(method: string, path: string, body?: unknown, o: Opts = {}): Promise<T> {
  if (isDemo()) {
    const { demoRequest } = await import('./demo')
    return demoRequest<T>(method, path, body)
  }
  const headers: Record<string, string> = { Accept: 'application/json' }
  let payload: BodyInit | undefined = o.raw
  if (body !== undefined && !o.raw) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(body) }
  if (o.raw) headers['Content-Type'] = 'application/octet-stream'
  let res: Response
  try {
    res = await fetch(API + path, { method, headers, body: payload, credentials: 'include', signal: o.signal })
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
    throw new ApiError(0, '无法连接到 Atoll 服务器')
  }
  const text = await res.text()
  let data: unknown = undefined
  if (text) { try { data = JSON.parse(text) } catch { data = text } }
  if (!res.ok) {
    const msg = (data && typeof data === 'object' && 'error' in data) ? String((data as { error: string }).error) : (typeof data === 'string' && data) || `${res.status} ${res.statusText}`
    const code = data && typeof data === 'object' && 'code' in data ? String((data as { code: string }).code) : undefined
    if (res.status === 401 && !o.noRedirect) toLogin()
    const ra = Number(res.headers.get('Retry-After'))
    throw new ApiError(res.status, msg, code, ra > 0 ? ra : undefined)
  }
  return (data === undefined ? {} : data) as T
}

const get = <T,>(p: string, o?: Opts) => request<T>('GET', p, undefined, o)
const post = <T,>(p: string, b?: unknown) => request<T>('POST', p, b ?? {})
const put = <T,>(p: string, b: unknown) => request<T>('PUT', p, b)
const patch = <T,>(p: string, b: unknown) => request<T>('PATCH', p, b)
const del = <T = void,>(p: string) => request<T>('DELETE', p)
const e = encodeURIComponent

export type Range = '1h' | '24h' | '7d'
export type NewService = Partial<Service> & { name: string; kind: string; image?: string; ports?: Port[]; volumes?: Volume[] }

export const api = {
  // auth
  authStatus: () => get<AuthStatus>('/auth/status', { noRedirect: true }),
  me: () => get<Me>('/auth/me'),
  /** Answers Me (session started) or a LoginChallenge when the account has 2FA on. */
  login: (username: string, password: string) => request<Me | LoginChallenge>('POST', '/auth/login', { username, password }, { noRedirect: true }),
  login2fa: (pending_token: string, code: string) => request<Me>('POST', '/auth/login/2fa', { pending_token, code }, { noRedirect: true }),
  twoFactor: () => get<TwoFactorStatus>('/auth/2fa'),
  enroll2fa: (password: string) => request<TwoFactorEnrollment>('POST', '/auth/2fa/enroll', { password }, { noRedirect: true }),
  enable2fa: (code: string) => request<RecoveryCodes>('POST', '/auth/2fa/enable', { code }, { noRedirect: true }),
  disable2fa: (password: string, code: string) => request<void>('POST', '/auth/2fa/disable', { password, code }, { noRedirect: true }),
  regenRecovery: (password: string, code: string) => request<RecoveryCodes>('POST', '/auth/2fa/recovery-codes', { password, code }, { noRedirect: true }),
  /** Panel session only. A wrong current password answers 401 without leaving the page. */
  changePassword: (b: ChangePasswordRequest) => request<ChangePasswordResponse>('POST', '/auth/password', b, { noRedirect: true }),
  /** Panel session only: rename the login, confirmed with the current password (+ TOTP code). */
  changeUsername: (b: { username: string; current_password: string; code?: string }) => request<Me>('POST', '/auth/username', b, { noRedirect: true }),
  sessions: () => get<SessionInfo[]>('/auth/sessions'),
  revokeSession: (id: string) => del(`/auth/sessions/${e(id)}`),
  revokeOtherSessions: () => post<{ revoked: number }>('/auth/sessions/revoke-others'),
  setup: (username: string, password: string) => request<Me>('POST', '/auth/setup', { username, password }, { noRedirect: true }),
  logout: () => request<void>('POST', '/auth/logout', {}, { noRedirect: true }),
  // servers
  servers: () => get<Server[]>('/servers'),
  server: (id: string) => get<Server>(`/servers/${e(id)}`),
  updateServer: (id: string, b: Partial<Pick<Server, 'name' | 'provider' | 'region'>>) => patch<Server>(`/servers/${e(id)}`, b),
  deleteServer: (id: string) => del<Confirmation | void>(`/servers/${e(id)}`),
  joinToken: () => post<JoinToken>('/servers/join-token'),
  agentInstallCommand: () => get<InstallCommand>('/servers/agent-install-command'),
  /** start an agent upgrade to the control server's version (202; the outcome shows on the server) */
  upgradeAgent: (id: string) => request<AgentUpgrade>('POST', `/servers/${e(id)}/agent-upgrade`, {}),
  /** several servers at once; no ids = every online server whose agent is on another version */
  upgradeAgents: (ids?: string[]) => post<AgentUpgradeRow[]>('/servers/agent-upgrade', { ids: ids ?? [] }),
  serverMetrics: (id: string, range: Range = '24h') => get<NodeMetrics[]>(`/servers/${e(id)}/metrics?range=${range}`),
  serverContainers: (id: string) => get<ContainerState[]>(`/servers/${e(id)}/containers`),
  /** join: install Tailscale + tailscale up + switch agent to the tailnet; switch: already on it; public: back to the public URL */
  serverNetwork: (id: string, action: ServerNetworkRequest['action']) => post<NetworkResult>(`/servers/${e(id)}/network`, { action }),
  // projects
  projects: () => get<Project[]>('/projects'),
  project: (id: string) => get<Project>(`/projects/${e(id)}`),
  createProject: (b: { name: string; server_id: string; description?: string }) => post<Project>('/projects', b),
  updateProject: (id: string, b: Partial<Pick<Project, 'name' | 'display_name' | 'description' | 'root_domain'>>) => patch<Project>(`/projects/${e(id)}`, b),
  deleteProject: (id: string) => del<Confirmation | void>(`/projects/${e(id)}`),
  migrateProject: (id: string, server_id: string) => post<MigrationJob | Confirmation>(`/projects/${e(id)}/migrate`, { server_id }),
  /** /projects may omit services; fill them from /projects/{id} when missing. */
  projectsFull: async () => {
    const ps = await get<Project[]>('/projects')
    return Promise.all(ps.map(p => p.services ? p : get<Project>(`/projects/${e(p.id)}`)))
  },
  // services
  createService: (pid: string, b: NewService) => post<Service>(`/projects/${e(pid)}/services`, b),
  service: (id: string) => get<Service>(`/services/${e(id)}`),
  updateService: (id: string, b: Partial<Service>) => patch<Service>(`/services/${e(id)}`, b),
  deleteService: (id: string) => del<Confirmation | void>(`/services/${e(id)}`),
  action: (id: string, action: 'start' | 'stop' | 'restart' | 'redeploy') => post<void>(`/services/${e(id)}/actions`, { action }),
  rollback: (id: string, deployment_id: string) => post<void>(`/services/${e(id)}/rollback`, { deployment_id }),
  env: (id: string, reveal = false) => get<EnvVar[]>(`/services/${e(id)}/env${reveal ? '?reveal=1' : ''}`),
  putEnv: (id: string, vars: EnvVar[], redeploy = true) => put<EnvVar[]>(`/services/${e(id)}/env${redeploy ? '' : '?redeploy=0'}`, vars),
  envRefs: (id: string) => get<EnvReference[]>(`/services/${e(id)}/env/references`),
  serviceDomains: (id: string) => get<Domain[]>(`/services/${e(id)}/domains`),
  addDomain: (id: string, b: { hostname: string; port_name: string; proxied: boolean; root_domain?: string }) => post<Domain | Confirmation>(`/services/${e(id)}/domains`, b),
  deleteDomain: (id: string) => del<Confirmation | void>(`/domains/${e(id)}`),
  /** orange (true) / grey (false) cloud: rewrites the Cloudflare record */
  setDomainProxied: (id: string, proxied: boolean) => patch<Domain | Confirmation>(`/domains/${e(id)}`, { proxied }),
  cloudflareTokenCheck: (refresh = false) => get<CloudflareTokenCheck>(`/cloudflare/token-check${refresh ? '?refresh=1' : ''}`),
  domains: () => get<Domain[]>('/domains'),
  // root domains (multi-domain)
  rootDomains: () => get<RootDomain[]>('/root-domains'),
  addRootDomain: (b: { name: string; cert_mode?: string; default?: boolean }) => post<RootDomain>('/root-domains', b),
  updateRootDomain: (name: string, b: { cert_mode?: string; default?: boolean }) => patch<RootDomain>(`/root-domains/${e(name)}`, b),
  refreshRootDomain: (name: string) => post<RootDomain>(`/root-domains/${e(name)}/refresh`),
  deleteRootDomain: (name: string) => del<void>(`/root-domains/${e(name)}`),
  deployments: (id: string) => get<Deployment[]>(`/services/${e(id)}/deployments`),
  serviceMetrics: (id: string, range: Range = '24h') => get<ContainerMetrics[]>(`/services/${e(id)}/metrics?range=${range}`),
  /** move one service (container + volumes) to another server; siblings reach it over the tailnet mesh */
  migrateService: (id: string, server_id: string) => post<MigrationJob | Confirmation>(`/services/${e(id)}/migrate`, { server_id }),
  exec: (id: string, cmd: string[]) => post<ResultPayload>(`/services/${e(id)}/exec`, { cmd }),
  // tokens, confirmations, settings
  tokens: () => get<APIToken[]>('/tokens'),
  createToken: (b: TokenRequest) => post<APIToken>('/tokens', b),
  updateToken: (id: string, b: TokenUpdate) => patch<APIToken>(`/tokens/${e(id)}`, b),
  deleteToken: (id: string) => del(`/tokens/${e(id)}`),
  confirmations: (status?: string) => get<Confirmation[]>(`/confirmations${status ? `?status=${status}` : ''}`),
  approve: (id: string) => post<void>(`/confirmations/${e(id)}/approve`),
  reject: (id: string) => post<void>(`/confirmations/${e(id)}/reject`),
  settings: () => get<Settings>('/settings'),
  problems: (resolved = false) => get<Problem[]>(`/problems${resolved ? '?resolved=1' : ''}`),
  notifications: () => get<NotificationSettings>('/settings/notifications'),
  putNotifications: (n: NotificationSettings) => put<NotificationSettings>('/settings/notifications', n),
  testNotification: (channel: NotificationChannel) => post<{ ok: boolean }>('/settings/notifications/test', { channel }),
  putSettings: (s: Partial<Settings>) => put<Settings>('/settings', s),
  // import / templates
  imports: () => get<ImportJob[]>('/imports'),
  importJob: (id: string) => get<ImportJob>(`/imports/${e(id)}`),
  createImport: (plan: ImportPlan) => post<ImportJob>('/imports', plan),
  startImport: (id: string) => post<ImportJob>(`/imports/${e(id)}/start`),
  templates: () => get<Template[]>('/templates'),
  migration: (id: string) => get<MigrationJob>(`/migrations/${e(id)}`),
  // in-place Zeabur takeover
  zeaburScan: (serverId: string) => get<ZeaburInventory>(`/servers/${e(serverId)}/zeabur`),
  createZeaburLocal: (b: ZeaburLocalImportRequest) => post<ImportJob>('/imports/zeabur-local', b),
  rehearse: (id: string, b?: { ignore_memory?: boolean }) => post<ImportJob>(`/imports/${e(id)}/rehearse`, b),
  cutover: (id: string, b?: { force?: boolean }) => post<ImportJob | Confirmation>(`/imports/${e(id)}/cutover`, b),
  rollbackImport: (id: string) => post<ImportJob>(`/imports/${e(id)}/rollback`),
  /** cut over several imports of one server together (Zeabur's ingress is shared by the whole node) */
  zeaburNodeCutover: (serverId: string, b: ZeaburNodeCutoverRequest) => post<ZeaburNodeOp | Confirmation>(`/servers/${e(serverId)}/zeabur/cutover`, b),
  /** roll back every cut-over import of the server, restoring Zeabur's ingress once */
  zeaburNodeRollback: (serverId: string) => post<ZeaburNodeOp>(`/servers/${e(serverId)}/zeabur/rollback`),
  cleanupImport: (id: string, b: ZeaburCleanupRequest) => post<ImportJob>(`/imports/${e(id)}/cleanup`, b),
  // Docker adoption (containers the user started with docker run / compose); rehearse, cutover
  // and rollbackImport above work for these jobs too
  dockerScan: (serverId: string) => get<DockerInventory>(`/servers/${e(serverId)}/docker`),
  dockerAdopt: (b: DockerAdoptRequest) => post<ImportJob>('/imports/docker', b),
}

/** A PAT-style 202 Confirmation can come back from destructive calls; the panel session normally executes directly. */
export function isConfirmation(x: unknown): x is Confirmation {
  return !!x && typeof x === 'object' && 'action' in x && 'status' in x && (x as Confirmation).status === 'pending'
}

export function isChallenge(x: unknown): x is LoginChallenge {
  return !!x && typeof x === 'object' && (x as LoginChallenge).two_factor_required === true
}
