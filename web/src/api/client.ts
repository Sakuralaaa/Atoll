import type {
  APIToken, AuthStatus, Confirmation, ContainerMetrics, ContainerState, Deployment, Domain, EnvReference, EnvVar,
  ImportJob, ImportPlan, JoinToken, MigrationJob, ZeaburInventory, ZeaburLocalImportRequest, ZeaburCleanupRequest, Me, NodeMetrics, Port, Project, ResultPayload, Server, Service, Settings, Template, Volume,
} from './types'

export const API = '/api/v1'

export class ApiError extends Error {
  status: number; code?: string
  constructor(status: number, message: string, code?: string) { super(message); this.status = status; this.code = code }
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

function toLogin() {
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
    throw new ApiError(res.status, msg, code)
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
  login: (username: string, password: string) => request<Me>('POST', '/auth/login', { username, password }, { noRedirect: true }),
  setup: (username: string, password: string) => request<Me>('POST', '/auth/setup', { username, password }, { noRedirect: true }),
  logout: () => request<void>('POST', '/auth/logout', {}, { noRedirect: true }),
  // servers
  servers: () => get<Server[]>('/servers'),
  server: (id: string) => get<Server>(`/servers/${e(id)}`),
  updateServer: (id: string, b: Partial<Pick<Server, 'name' | 'provider' | 'region'>>) => patch<Server>(`/servers/${e(id)}`, b),
  deleteServer: (id: string) => del<Confirmation | void>(`/servers/${e(id)}`),
  joinToken: () => post<JoinToken>('/servers/join-token'),
  serverMetrics: (id: string, range: Range = '24h') => get<NodeMetrics[]>(`/servers/${e(id)}/metrics?range=${range}`),
  serverContainers: (id: string) => get<ContainerState[]>(`/servers/${e(id)}/containers`),
  // projects
  projects: () => get<Project[]>('/projects'),
  project: (id: string) => get<Project>(`/projects/${e(id)}`),
  createProject: (b: { name: string; server_id: string; description?: string }) => post<Project>('/projects', b),
  updateProject: (id: string, b: Partial<Pick<Project, 'name' | 'description'>>) => patch<Project>(`/projects/${e(id)}`, b),
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
  putEnv: (id: string, vars: EnvVar[]) => put<EnvVar[]>(`/services/${e(id)}/env`, vars),
  envRefs: (id: string) => get<EnvReference[]>(`/services/${e(id)}/env/references`),
  serviceDomains: (id: string) => get<Domain[]>(`/services/${e(id)}/domains`),
  addDomain: (id: string, b: { hostname: string; port_name: string; proxied: boolean }) => post<Domain | Confirmation>(`/services/${e(id)}/domains`, b),
  deleteDomain: (id: string) => del<Confirmation | void>(`/domains/${e(id)}`),
  domains: () => get<Domain[]>('/domains'),
  deployments: (id: string) => get<Deployment[]>(`/services/${e(id)}/deployments`),
  serviceMetrics: (id: string, range: Range = '24h') => get<ContainerMetrics[]>(`/services/${e(id)}/metrics?range=${range}`),
  exec: (id: string, cmd: string[]) => post<ResultPayload>(`/services/${e(id)}/exec`, { cmd }),
  // tokens, confirmations, settings
  tokens: () => get<APIToken[]>('/tokens'),
  createToken: (b: { name: string; scope: string }) => post<APIToken>('/tokens', b),
  deleteToken: (id: string) => del(`/tokens/${e(id)}`),
  confirmations: (status?: string) => get<Confirmation[]>(`/confirmations${status ? `?status=${status}` : ''}`),
  approve: (id: string) => post<void>(`/confirmations/${e(id)}/approve`),
  reject: (id: string) => post<void>(`/confirmations/${e(id)}/reject`),
  settings: () => get<Settings>('/settings'),
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
  rehearse: (id: string) => post<ImportJob>(`/imports/${e(id)}/rehearse`),
  cutover: (id: string) => post<ImportJob>(`/imports/${e(id)}/cutover`),
  rollbackImport: (id: string) => post<ImportJob>(`/imports/${e(id)}/rollback`),
  cleanupImport: (id: string, b: ZeaburCleanupRequest) => post<ImportJob>(`/imports/${e(id)}/cleanup`, b),
}

/** A PAT-style 202 Confirmation can come back from destructive calls; the panel session normally executes directly. */
export function isConfirmation(x: unknown): x is Confirmation {
  return !!x && typeof x === 'object' && 'action' in x && 'status' in x && (x as Confirmation).status === 'pending'
}
