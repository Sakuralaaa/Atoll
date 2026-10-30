// Mirrors internal/api/types.go (REST part). Change the Go file first, then this one.
// Times are RFC3339 strings (UTC).

export interface NodeMetrics {
  at: string; cpu_pct: number; mem_used_mb: number; mem_total_mb: number
  disk_used_gb: number; disk_total_gb: number; net_rx_bps: number; net_tx_bps: number; load1: number
}

export interface Server {
  id: string; name: string
  role: 'worker' | 'control' | string
  status: 'pending' | 'online' | 'offline' | 'draining' | string
  public_ipv4: string; public_ipv6?: string; tailnet_host?: string
  provider?: string; region?: string; os: string; arch: string
  cpu_cores: number; mem_mb: number; disk_gb: number; agent_version: string
  last_seen_at: string; created_at: string
  usage?: NodeMetrics
}

export interface JoinToken { token: string; command: string; expires_at: string }

export interface Port { name: string; container_port: number; protocol: 'http' | 'tcp' | 'udp' | string; public_port?: number }
export interface Volume { name: string; mount_path: string }
export interface ConfigFile { path: string; content: string }
export interface HealthCheck { cmd?: string[]; http_path?: string; interval_sec?: number }

export interface Domain {
  id: string; service_id: string; hostname: string; port_name: string; proxied: boolean
  status: 'pending' | 'active' | 'error' | string; error?: string; created_at: string
}

export interface Service {
  id: string; project_id: string; name: string
  kind: 'image' | 'postgres' | 'mysql' | 'redis' | 'mongo' | string
  image: string; command?: string[]; args?: string[]
  ports: Port[]; volumes: Volume[]; configs?: ConfigFile[]; health?: HealthCheck
  cpu_limit?: number; mem_limit_mb?: number; registry_cred_id?: string
  status: 'running' | 'deploying' | 'stopped' | 'failed' | 'unknown' | string
  replicas: string; created_at: string; domains?: Domain[]
}

export interface Project {
  id: string; name: string; description?: string; server_id: string; created_at: string; services?: Service[]
}

export interface EnvVar { key: string; value: string; secret: boolean; exposed: boolean; /** only in /env/references: exposing service name */ source?: string }

export interface Deployment {
  id: string; service_id: string; server_id: string; image_digest?: string
  status: 'queued' | 'pulling' | 'starting' | 'running' | 'failed' | 'superseded' | string
  trigger: 'create' | 'redeploy' | 'env' | 'rollback' | 'import' | 'migrate' | string
  message?: string; started_at: string; finished_at?: string
}

export interface APIToken {
  id: string; name: string; scope: 'read' | 'deploy' | 'admin' | string; prefix: string
  token?: string; last_used_at?: string; created_at: string
}

export interface Confirmation {
  id: string; action: string; target: string; summary: string
  status: 'pending' | 'approved' | 'rejected' | 'expired' | string
  created_by: string; created_at: string
}

export interface Settings {
  base_domain: string; cloudflare_zone_id: string; cloudflare_token?: string
  has_cloudflare_token: boolean; tls_mode: 'cloudflare-origin' | 'acme' | string; public_url: string
  /** write-only like cloudflare_token */ origin_cert_pem?: string; origin_key_pem?: string; has_origin_cert: boolean; acme_email?: string
}

export interface ErrorBody { error: string; code?: string }

export interface ContainerMetrics { service_id: string; at: string; cpu_pct: number; mem_mb: number; net_rx_bps: number; net_tx_bps: number }
export interface LogLine { service_id: string; at: string; stream: 'stdout' | 'stderr' | string; line: string }
export interface ContainerState {
  service_id: string; deployment_id: string; state: string; health?: string
  image_digest?: string; restart_count: number; started_at: string
}
export interface ResultPayload { ok: boolean; error?: string; output?: string }

export interface ImportService {
  source_service_id: string; name: string; kind: string; image: string; image_needs_push: boolean
  command?: string[]; args?: string[]; ports: Port[]; volumes: Volume[]; configs?: ConfigFile[]; env: EnvVar[]; domains: string[]; data_bytes: number
}
export interface ImportPlan {
  source: string; source_project: string; project_name: string; target_server_id: string
  services: ImportService[]; warnings?: string[]
}
export interface ImportStep { name: string; status: 'pending' | 'running' | 'done' | 'failed' | 'skipped' | string; detail?: string }
export interface ImportJob {
  id: string; plan: ImportPlan; project_id?: string
  /** CLI plan: planned|uploading|deploying|verifying|done|failed. zeabur-local adds (assumed): rehearsing|rehearsed|cutting_over|live|rolling_back|rolled_back|cleaning|cleaned */
  status: string
  steps: ImportStep[]; created_at: string
  // ---- assumed additions for in-place takeover (not yet in types.go) ----
  mode?: 'plan' | 'zeabur-local'
  server_id?: string
  namespace?: string
  /** row counts from zeabur.db results, one row per table/keyspace */
  verify?: ImportVerify[]
}
export interface ImportVerify { service: string; table: string; source_rows: number; target_rows: number }

export interface MigrationJob {
  id: string; project_id: string; from_server_id: string; to_server_id: string
  status: 'running' | 'done' | 'failed' | string; steps: ImportStep[]; error?: string; created_at: string; finished_at?: string
}

// ---- in-place Zeabur takeover ----
export interface ZeaburVolume { pvc: string; mount_path: string; host_dir: string; bytes: number }
export interface ZeaburService {
  service_id: string; name: string; workload: string; replicas: number
  role: 'app' | 'database' | 'cache' | 'other' | string; kind: string
  image: string; image_id: string; zeabur_registry: boolean; image_cached: boolean
  command?: string[]; args?: string[]; ports: Port[]; node_ports?: number[]; env: EnvVar[]
  volumes: ZeaburVolume[]; configs?: ConfigFile[]; domains: string[]; warnings?: string[]
  /** scan-time health */
  status?: 'running' | 'progressing' | 'degraded' | 'failed' | 'suspended' | 'unknown' | string
  /** data-store family from image / StatefulSet+PV; "" for apps */
  data_store?: 'postgres' | 'mysql' | 'redis' | 'mongo' | 'queue' | 'search' | 'other' | '' | string
  k8s_services?: string[]
  references?: ZeaburReference[]
}
/** One env-level connection from a service to another Zeabur service. */
export interface ZeaburReference {
  key?: string; target: string; target_key?: string
  via: 'env-value' | 'zeabur-internal' | 'shared-secret' | 'shared-configmap' | string
  object?: string; inferred: boolean
}
export interface ZeaburProject {
  namespace: string; project_id: string; project_name: string; environment_name: string; services: ZeaburService[]
  /** namespace-level findings (orphan PVCs, non-Zeabur workloads); never secret values */
  warnings?: string[]
}
export interface ZeaburInventory {
  k3s: boolean; k3s_version?: string; projects: ZeaburProject[]; ingress_running: boolean
  disk_free_gb: number; mem_free_mb: number; scanned_at: string
  /** assumed addition: RSS of k3s + Zeabur system pods, shown as "可释放约 X MB 内存" (UI estimates when absent) */
  k3s_mem_mb?: number
}
export interface ZeaburLocalImportRequest {
  server_id: string; namespace: string; service_ids: string[]; project_name: string
  /** *.zeabur.app host -> new host ("" drops it) */
  domain_map?: Record<string, string>
}
export interface ZeaburCleanupRequest { confirm: string; keep_backup: boolean }

// ---- shapes not spelled out in types.go (assumed; see docs/api.md) ----
export interface AuthStatus { setup_required: boolean }
export interface Me { id?: string; username: string }
/** GET /templates item. Create with POST /projects/{id}/services {name, kind} (assumed: server fills image/ports/volumes/env from the template). */
export interface Template { kind: string; name: string; description: string; image: string; command?: string[]; ports: Port[]; volumes: Volume[]; env: EnvVar[] }
/** GET /services/{id}/env/references item; `service` (name of the exposing service) is optional. */
export interface EnvReference extends EnvVar { service?: string }
/** /events frame */
export interface LiveEvent { type: 'service' | 'server' | 'deployment' | 'confirmation' | string; payload?: unknown }
