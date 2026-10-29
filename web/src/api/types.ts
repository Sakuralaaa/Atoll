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

export interface EnvVar { key: string; value: string; secret: boolean; exposed: boolean }

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
  command?: string[]; ports: Port[]; volumes: Volume[]; configs?: ConfigFile[]; env: EnvVar[]; domains: string[]; data_bytes: number
}
export interface ImportPlan {
  source: string; source_project: string; project_name: string; target_server_id: string
  services: ImportService[]; warnings?: string[]
}
export interface ImportStep { name: string; status: 'pending' | 'running' | 'done' | 'failed' | 'skipped' | string; detail?: string }
export interface ImportJob {
  id: string; plan: ImportPlan; project_id?: string
  status: 'planned' | 'uploading' | 'deploying' | 'verifying' | 'done' | 'failed' | string
  steps: ImportStep[]; created_at: string
}

// ---- shapes not spelled out in types.go (assumed; see docs/api.md) ----
export interface AuthStatus { setup_required: boolean }
export interface Me { id?: string; username: string }
/** GET /templates item. Create with POST /projects/{id}/services {name, kind}. */
export interface Template { kind: string; name: string; image: string; description?: string; ports?: Port[]; volumes?: Volume[] }
/** GET /services/{id}/env/references item; `service` (name of the exposing service) is optional. */
export interface EnvReference extends EnvVar { service?: string }
/** /events frame */
export interface LiveEvent { type: 'service' | 'server' | 'deployment' | 'confirmation' | string; payload?: unknown }
