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
  /** 100.x address when the node is on the tailnet */ tailnet_ip?: string
  /** private-network state the agent last reported */ network?: NodeNetwork
  provider?: string; region?: string; os: string; arch: string
  cpu_cores: number; mem_mb: number; disk_gb: number; agent_version: string
  last_seen_at: string; created_at: string
  usage?: NodeMetrics
  /** agent version this control server hosts for the server's arch ("" / missing = none) */ agent_latest?: string
  /** the agent can upgrade itself from the panel (v0.1.6+) */ agent_self_upgrade?: boolean
  /** last panel-started agent upgrade (kept until the control server restarts) */ agent_upgrade?: AgentUpgrade
  /** GeoIP record of the public address (flag, provider guess) */ geo?: NodeGeo
  /** control↔agent round-trip time (WebSocket ping); missing while offline or not measured yet */ latency?: NodeLatency
}

/** Round-trip time between the control server and an agent, in milliseconds. */
export interface NodeLatency {
  last_ms: number; avg_ms: number; samples: number; measured_at: string
  /** latest sample's method: WebSocket ping, or an application message (fallback when pings fail) */ method?: 'ping' | 'message' | string
  /** consecutive failed measurements (ping and fallback both failed); 0 after a success */ failures?: number
  failures_total?: number; last_error?: string; last_attempt?: string
}

/** One account's favorite and pinned projects (GET/PUT /me/project-prefs). */
export interface ProjectPrefs { favorites: string[]; pinned: string[] }

export interface NodeGeo { ip: string; country?: string; city?: string; asn?: string; org?: string; source?: string; at: string }

export interface AgentUpgrade {
  state: 'upgrading' | 'done' | 'failed' | string
  from: string; to: string; error?: string
  /** the new binary did not reconnect and the previous one runs again */ rolled_back?: boolean
  started_at: string; finished_at?: string
}

/** One row of POST /servers/agent-upgrade. */
export interface AgentUpgradeRow { id: string; upgrade?: AgentUpgrade; code?: string; error?: string }

/** Private-network state an agent reports (internal/api NodeNetwork). */
export interface NodeNetwork {
  provider: 'tailscale' | '' | string
  state: 'running' | 'not_installed' | 'stopped' | 'needs_login' | 'starting' | 'unknown' | string
  control_url?: string; fallback_urls?: string[]
  /** control_url is a tailnet address */ via_tailnet: boolean
  mesh_error?: string
}

export interface JoinToken { token: string; command: string; expires_at: string }
export interface InstallCommand { command: string; version: string }

export interface Port { name: string; container_port: number; protocol: 'http' | 'tcp' | 'udp' | string; public_port?: number; /** published on this host address only ("" = all) */ host_ip?: string }
export interface Volume { name: string; mount_path: string; /** adopted from Docker: existing host dir/file mounted in place (read-only in the API) */ host_path?: string; read_only?: boolean; /** "uid[:gid]" the agent gives the empty volume dir */ owner?: string }
export interface ConfigFile { path: string; content: string }
export interface HealthCheck { cmd?: string[]; http_path?: string; interval_sec?: number }

export interface Domain {
  id: string; service_id: string; hostname: string; port_name: string; proxied: boolean
  status: 'provisioning' | 'pending' | 'active' | 'error' | string; error?: string; created_at: string
  /** certificate the servers present: origin = Cloudflare Origin CA, acme = Let's Encrypt, http = none (Cloudflare-only HTTP) */
  cert?: 'origin' | 'acme' | 'http' | string
}

/** GET /cloudflare/token-check: what the stored Cloudflare token may do (probed, cached 10 min). */
export interface CloudflareTokenCheck {
  configured: boolean
  /** Zone.DNS: Edit */ dns: 'ok' | 'missing' | 'unknown' | string; dns_error?: string
  /** Zone.SSL and Certificates: Edit, needed by the roots in ssl_roots (cert mode auto); absent when none needs it */
  ssl?: 'ok' | 'missing' | 'unknown' | string; ssl_error?: string; ssl_roots?: string[]; checked_at: string
}

export interface Service {
  id: string; project_id: string; name: string
  /** panel label ("" = name); not a hostname, changing it redeploys nothing */ display_name?: string
  kind: 'image' | 'postgres' | 'mysql' | 'redis' | 'mongo' | string
  image: string; command?: string[]; args?: string[]
  ports: Port[]; volumes: Volume[]; configs?: ConfigFile[]; health?: HealthCheck
  cpu_limit?: number; mem_limit_mb?: number; registry_cred_id?: string
  status: 'running' | 'deploying' | 'stopped' | 'failed' | 'unknown' | string
  replicas: string; created_at: string; domains?: Domain[]
  /** server the service runs on (read-only); usually the project's server */ server_id?: string
  /** read-only: "image" (pulled from a registry) or "git" (built from GitHub on its own server) */ source?: 'image' | 'git' | string
  /** set = built from a GitHub repository (see docs/api.md "Builds from source") */ git?: GitSource
  /** returned once, when a git service is created or first switched to git */ webhook?: GitWebhook
}

/** Builds a service from source on its own server. PATCH is partial (omitted fields and the token are kept). */
export interface GitSource {
  /** https://github.com/OWNER/REPO */ repo: string
  /** default "main" */ branch?: string
  /** build context: subdirectory of the repo ("" = root) */ context?: string
  /** relative to the context, default "Dockerfile" */ dockerfile?: string
  /** docker build --build-arg (not secret: visible in the image history) */ build_args?: Record<string, string>
  /** write-only GitHub token for a private repo */ token?: string
  has_token?: boolean
  /** PATCH only: remove the stored token */ clear_token?: boolean
  /** read-only: commit of the last successful build */ last_commit?: string
}
/** GET /services/{id}/webhook. Secret only at creation / rotation and to a panel session. */
export interface GitWebhook { url: string; secret?: string; has_secret: boolean; content_type: string; events: string; branch: string }
export interface DeploymentLogLine { seq: number; at: string; line: string }
/** GET /services/{id}/deployments/{dep}/logs?after=SEQ */
export interface DeploymentLogs { deployment_id: string; status: string; lines: DeploymentLogLine[]; truncated?: boolean }
/** /events {type: "build_log"} payload */
export interface BuildLogEvent { deployment_id: string; service_id: string; lines: string[] }

export interface Project {
  id: string; name: string; /** panel label only; '' = show name */ display_name?: string; description?: string; server_id: string; created_at: string; services?: Service[]
  /** root domain new domains default to; absent = the default root domain */ root_domain?: string
}

/** One root domain of Settings → 根域名 (GET /root-domains). */
export interface RootDomain {
  name: string; default: boolean; zone_id?: string; zone_name?: string
  /** global = follow the global HTTPS settings; auto = Cloudflare Origin CA, ACME if that fails; acme = always ACME */
  cert_mode: 'global' | 'auto' | 'acme' | string
  /** what the servers actually serve */ cert_status: 'global' | 'origin' | 'acme' | '' | string
  cert_error?: string; cert_expires_at?: string; created_at: string
}

export interface EnvVar { key: string; value: string; secret: boolean; exposed: boolean; /** only in /env/references: exposing service name */ source?: string }

export interface Deployment {
  id: string; service_id: string; server_id: string; image_digest?: string
  status: 'queued' | 'building' | 'pulling' | 'starting' | 'running' | 'failed' | 'superseded' | string
  trigger: 'create' | 'redeploy' | 'env' | 'rollback' | 'import' | 'migrate' | 'image-update' | 'webhook' | string
  /** git services: the commit built (set once the build finished) */ commit?: string
  message?: string; started_at: string; finished_at?: string
}

// ---- API token permissions ----
export type Permission = 'read' | 'deploy' | 'exec' | 'delete' | 'migrate' | 'domains' | 'restore' | 'servers' | 'settings' | 'tokens'
/** display order (api.AllPermissions) */
export const ALL_PERMISSIONS: Permission[] = ['read', 'deploy', 'exec', 'delete', 'migrate', 'domains', 'restore', 'servers', 'settings', 'tokens']
/** cannot be combined with a project-scoped token (api.GlobalPermissions) */
export const GLOBAL_PERMISSIONS: Permission[] = ['servers', 'settings', 'tokens']

export interface APIToken {
  id: string; name: string
  /** legacy summary derived from permissions */ scope: 'read' | 'deploy' | 'admin' | string
  /** "read" is always included */ permissions: (Permission | string)[]
  /** project ids; empty = all projects */ projects?: string[]
  /** allowed destructive ops still wait for approval in the panel */ require_approval?: boolean
  prefix: string; token?: string; last_used_at?: string; created_at: string
}
/** POST /tokens */
export interface TokenRequest { name: string; scope?: string; permissions?: string[]; projects?: string[]; require_approval?: boolean }
/** PATCH /tokens/{id}; omitted fields stay */
export interface TokenUpdate { name?: string; permissions?: string[]; projects?: string[]; require_approval?: boolean }

export interface Confirmation {
  id: string; action: string; target: string; summary: string
  status: 'pending' | 'approved' | 'rejected' | 'expired' | string
  created_by: string; created_at: string
}

export interface Settings {
  /** GeoIP lookups of server addresses for flags and provider icons */ geoip?: 'on' | 'off' | string
  base_domain: string; cloudflare_zone_id: string; cloudflare_token?: string
  has_cloudflare_token: boolean; tls_mode: 'cloudflare-origin' | 'acme' | string; public_url: string
  /** write-only like cloudflare_token */ origin_cert_pem?: string; origin_key_pem?: string; has_origin_cert: boolean; acme_email?: string
  /** image update check interval (hours, default 6); 0 on PUT keeps the stored value */ image_check_interval_hours?: number
  /** limits a new service gets when created without its own (0 = no limit; defaults 1024 MB / CPU unlimited); null/omitted on PUT keeps the stored value */
  default_mem_limit_mb?: number | null; default_cpu_limit?: number | null
  /** backup target (any S3 API). Empty fields on PUT keep the stored value; clear with DELETE /settings/backup */
  backup_endpoint?: string; backup_bucket?: string; backup_prefix?: string; backup_region?: string
  /** write-only */ backup_access_key?: string; backup_secret_key?: string; has_backup_credentials?: boolean
  /** write-only; returned ONCE in the PUT response when the server generated it */ backup_repo_password?: string
  has_backup_password?: boolean; backup_configured?: boolean
  /** backup verification: cron | "off" | "auto" on PUT (empty keeps); GET returns the stored value, "" = auto */
  backup_check_schedule?: string; backup_drill_schedule?: string
  /** restic --read-data-subset: "5%" | "1/20" | "500M"; "auto" on PUT resets to 5% */ backup_check_subset?: string
  /** private network: "" (none) | "tailscale"; PUT omitting it keeps, "none" clears */ network_provider?: '' | 'tailscale' | 'none' | string
  /** write-only (tskey-…) */ tailscale_auth_key?: string; has_tailscale_auth_key?: boolean
  /** PUT only */ clear_tailscale_auth_key?: boolean
  /** MagicDNS suffix, e.g. tail1234.ts.net */ tailnet_name?: string
  /** agent endpoint over the tailnet; "" = derived from the tailnet listener */ tailnet_control_url?: string
  // read-only status
  tailnet_listen?: string; tailnet_listen_error?: string; agent_public?: boolean
}

/** POST /servers/{id}/network */
export interface ServerNetworkRequest { action: 'join' | 'switch' | 'public' }
export interface NetworkResult { switched: boolean; control_url: string; tailnet_ip?: string; state: string; message?: string }
/** POST /services/{id}/migrate */
export interface ServiceMigrateRequest { server_id: string }

export interface ErrorBody { error: string; code?: string }

export interface ContainerMetrics { service_id: string; at: string; cpu_pct: number; mem_mb: number; net_rx_bps: number; net_tx_bps: number }
export interface LogLine { service_id: string; at: string; stream: 'stdout' | 'stderr' | string; line: string }
export interface ContainerState {
  service_id: string; deployment_id: string; state: string; health?: string
  image_digest?: string; restart_count: number; started_at: string
}
export interface ResultPayload { ok: boolean; error?: string; output?: string; exit_code?: number }

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
  mode?: 'plan' | 'zeabur-local' | 'docker'
  server_id?: string
  namespace?: string
  /** Docker adoption: the DockerGroup.key taken over */
  group?: string
  /** row counts from zeabur.db results, one row per table/keyspace */
  verify?: ImportVerify[]
}
export interface ImportVerify { service: string; table: string; source_rows: number; target_rows: number }

export interface MigrationJob {
  id: string; project_id: string; /** set when one service moves */ service_id?: string; from_server_id: string; to_server_id: string
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
  /** pods' current memory use in MiB (kubectl top); absent without metrics-server */
  mem_usage_mb?: number
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
/** POST /servers/{id}/zeabur/cutover: several imports of one server, one ingress park */
export interface ZeaburNodeCutoverRequest { import_ids: string[]; force?: boolean }
/** answer of the node cutover / rollback: one job per import, each with its own status and steps */
export interface ZeaburNodeOp { server_id: string; jobs: ImportJob[] }
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
export interface Me { id?: string; username: string; avatar?: string; nav_order?: string[] }
/** GET /templates item. Create with POST /projects/{id}/services {name, kind} (assumed: server fills image/ports/volumes/env from the template). */
/** GET /app-templates item: a one-click app (one or more services). */
export interface AppTemplateService { name: string; template?: string; image?: string; command?: string[]; ports?: Port[]; volumes?: Volume[]; configs?: ConfigFile[]; env?: EnvVar[]; domain_env?: EnvVar[]; mem_limit_mb?: number; main?: boolean; domain?: boolean; domain_port?: string; domain_label?: string; domain_label_en?: string; domain_hint?: string; domain_hint_en?: string }
/** A value the user may type when creating the app (a password); empty = generated. */
export interface AppTemplateInput { key: string; label: string; label_en: string; secret: boolean; placeholder?: string }
export interface AppTemplate {
  id: string; name: string; category: 'blog' | 'tools' | 'ops' | 'dev' | 'ai' | string; description: string; description_en: string
  website: string; color: string; needs_domain: boolean; min_mem_mb: number; note?: string; note_en?: string; inputs?: AppTemplateInput[]; services: AppTemplateService[]
}
export interface AppTemplateResult { project: Project; services: Service[]; domain?: Domain; domain_error?: string; extra_domains?: Domain[]; domain_errors?: Record<string, string> }
export interface Template { kind: string; name: string; description: string; image: string; command?: string[]; ports: Port[]; volumes: Volume[]; env: EnvVar[] }
/** GET /services/{id}/env/references item; `service` (name of the exposing service) is optional. */
export interface EnvReference extends EnvVar { service?: string }
/** /events frame */
export interface LiveEvent { type: 'service' | 'server' | 'deployment' | 'build_log' | 'confirmation' | string; payload?: unknown }

// ---- image update detection (GET /image-updates, /services/{id}/image-update) ----
export type ImageUpdatePolicyKind = 'notify' | 'auto' | 'off'
export interface ImageUpdate {
  service_id: string; project_id: string; service: string; image: string
  policy: ImageUpdatePolicyKind | string
  /** daily auto-update window "HH:MM-HH:MM" in UTC */ window?: string
  status: 'up_to_date' | 'update_available' | 'unknown' | 'error' | 'unsupported' | 'disabled' | 'pinned' | string
  update_available: boolean; digest_changed: boolean
  current_digest?: string; latest_digest?: string; current_tag?: string
  /** org.opencontainers.image.version labels */ current_version?: string; latest_version?: string
  /** image is fixed to a digest (name@sha256:…): never "update available", never auto-updated */ pinned?: boolean
  /** newest higher tag, same major */ newer_tag?: string
  /** newest higher major, shown separately */ major_tag?: string
  changelog_url?: string; error?: string; note?: string
  checked_at?: string; next_check_at?: string; last_auto_digest?: string
}
export interface ImageUpdatePolicy { policy: ImageUpdatePolicyKind; window?: string }


// ---------- Backups (restic to any S3 bucket) ----------
export interface BackupKeep { daily: number; weekly: number; monthly: number }
export interface BackupPolicy {
  service_id: string; enabled: boolean
  /** 5-field cron, default "0 3 * * *" */ schedule: string
  /** IANA zone the schedule is read in */ timezone: string
  keep: BackupKeep
  /** database templates: also snapshot volumes (always on for other services) */ include_volumes: boolean
  updated_at?: string
  // read-only
  next_run_at?: string; repo_configured: boolean; parts?: ('dump' | 'volumes' | string)[]
}
export interface BackupSnapshot { id: string; part: 'dump' | 'volumes' | string; bytes: number; added: number; pruned?: boolean }
export interface Backup {
  id: string; service_id: string; server_id: string
  trigger: 'schedule' | 'manual' | 'catchup' | 'imported' | string
  status: 'running' | 'done' | 'failed' | 'pruned' | string
  snapshots: BackupSnapshot[]; bytes: number; added: number; duration_ms: number
  error?: string; warnings?: string[]; started_at: string; finished_at?: string
}
export interface BackupRestoreRequest { target_server_id?: string }
export interface RestoreJob {
  id: string; backup_id: string; service_id: string; project_id: string; from_server_id: string; to_server_id: string
  status: 'running' | 'done' | 'failed' | string; steps: ImportStep[]; error?: string; created_at: string; finished_at?: string
}
export interface BackupTestResult { ok: boolean; server_id: string; message: string }
/** One part of a repository check or restore drill. */
export interface BackupVerifyStep {
  part: 'check' | 'volumes' | 'dump' | string; snapshot_id?: string
  status: 'done' | 'failed' | 'skipped' | string; detail?: string; files?: number; bytes?: number; error?: string
}
/** A repository check (kind check, restic check --read-data-subset) or a restore drill of one service (kind drill). */
export interface BackupVerification {
  id: string; kind: 'check' | 'drill' | string; service_id?: string; server_id: string
  trigger: 'schedule' | 'manual' | string; status: 'running' | 'done' | 'failed' | 'skipped' | string
  subset?: string; steps: BackupVerifyStep[]; error?: string; warnings?: string[]
  duration_ms: number; started_at: string; finished_at?: string
}
/** GET /backup-verify, GET /services/{id}/backup-verify */
export interface BackupVerifyStatus {
  check?: BackupVerification; drill?: BackupVerification
  /** effective cron, "" = off */ check_schedule: string; check_subset: string; drill_schedule: string; timezone: string
  check_server_id?: string; next_check_at?: string; next_drill_at?: string; repo_configured: boolean
}

// ---- login 2FA (internal/api/types.go) ----
export interface LoginChallenge { two_factor_required: true; pending_token: string; expires_at: string }
export interface TwoFactorStatus { enabled: boolean; enabled_at?: string; recovery_codes_left: number }
export interface TwoFactorEnrollment { secret: string; otpauth_uri: string }
export interface ChangePasswordRequest { current_password: string; new_password: string; code?: string }
export interface ChangePasswordResponse { revoked_sessions: number }
export interface RecoveryCodes { recovery_codes: string[] }
export interface SessionInfo { id: string; ip?: string; user_agent?: string; current: boolean; created_at: string; expires_at: string }

// ---------- 中控 / Control (internal/api/control.go) ----------
export interface ControlHost { hostname: string; os: string; kernel: string; arch: string; cpu_cores: number; uptime_seconds: number }
export interface ControlTailnet { ip?: string; control_url?: string; listen?: string; listen_error?: string }
export interface ReleaseInfo { tag: string; name?: string; url?: string; published_at: string; prerelease?: boolean; checked_at: string }
export interface UpdateResult { mode: 'update' | 'rollback' | string; from_version?: string; to_version?: string; ok: boolean; rolled_back?: boolean; error?: string; started_at: string; finished_at: string }
export interface ControlBackup {
  id: string; created_at: string; trigger: 'scheduled' | 'manual' | 'pre-update' | 'unknown' | string; version: string; size_bytes: number
  sha256?: string; local: boolean; remote: boolean; remote_key?: string; error?: string
}
export interface ControlStatus {
  /** display name set in the panel; "" = show host.hostname */ name?: string
  version: string; commit?: string; go_version: string; started_at: string; uptime_seconds: number
  host: ControlHost
  cpu_pct: number; load1: number; load5: number; load15: number
  mem_used_mb: number; mem_total_mb: number; disk_used_gb: number; disk_total_gb: number
  data_dir: string; data_dir_bytes: number; database_bytes: number
  public_url: string; tailnet?: ControlTailnet; agents_connected: number; agents_total: number
  last_backup?: ControlBackup
  latest_release?: ReleaseInfo; update_available: boolean; release_error?: string
  self_update_supported: boolean; update_in_progress: boolean; update_phase?: string; update_target?: string
  last_update_result?: UpdateResult; rollback_version?: string
}
export interface ReauthFields { password?: string; code?: string }
export interface ControlReauth { expires_at: string }
export interface ControlUpdateResponse { mode: string; version?: string; phase: string; message?: string }
export interface ControlUpdateCheck { latest_release?: ReleaseInfo; current: string; update_available: boolean; release_error?: string }
export interface ControlLogs { available: boolean; source: string; lines: string[]; message?: string }
export interface ControlBackupList {
  backups: ControlBackup[]; remote_enabled: boolean; remote_error?: string; keep_local: number; keep_remote: number
  next_scheduled?: string; passphrase_from: 'control_backup_passphrase' | 'backup_repo_password' | 'generated' | string
}
export interface ControlPassphrase { passphrase: string; from: string }
export interface AuditEntry {
  id: number; actor: string; actor_kind: 'session' | 'token' | 'cli' | 'system' | string; action: string; target: string
  ip?: string; token_id?: string; session_id?: string; detail: unknown; created_at: string
}
export interface AuditPage { entries: AuditEntry[]; next_before?: number }

/** A condition the control server watches (GET /problems). Only firing ones are listed. */
export interface Problem {
  id: string
  kind: 'node_offline' | 'disk_high' | 'service_down' | 'deploy_failed' | 'backup_failed' | 'backup_verify_failed' | 'domain_error' | string
  /** server id for node_offline / disk_high, domain id for domain_error, "backup_repo" for a failed repository check, service id otherwise */ subject_id: string
  subject_name: string; project_id?: string; server_id?: string
  message: string; since: string; firing_at?: string; resolved_at?: string
}
export interface NotificationChannel {
  id: string; type: 'webhook' | 'telegram' | 'email' | string; name?: string; enabled: boolean
  url?: string; has_url?: boolean
  bot_token?: string; has_bot_token?: boolean; chat_id?: string
  smtp_host?: string; smtp_port?: number; username?: string; password?: string; has_password?: boolean; from?: string; to?: string
}
export interface NotificationSettings {
  channels: NotificationChannel[]; notify_resolved: boolean
  quiet_start?: string; quiet_end?: string; timezone?: string
}

// ---------- Docker adoption (types_docker.go) ----------
export interface DockerPort { container_port: number; host_port?: number; protocol: string; host_ip?: string }
export interface DockerMount { type: 'bind' | 'volume' | 'tmpfs' | string; name?: string; source: string; destination: string; read_only?: boolean; is_file?: boolean; driver?: string }
export interface DockerContainer {
  id: string; name: string; service?: string; image: string; image_id: string; pinned_image?: string
  state: string; env: EnvVar[]; command?: string[]; args?: string[]; user?: string
  ports: DockerPort[]; mounts: DockerMount[]; restart_policy: string; networks?: string[]; network_mode?: string
  mem_limit_mb?: number; cpu_limit?: number; mem_usage_mb?: number
  risk: 'auto' | 'manual'; risk_reasons?: string[]; warnings?: string[]
}
export interface DockerGroup { key: string; kind: 'compose' | 'standalone'; name: string; working_dir?: string; config_files?: string; containers: DockerContainer[]; adoptable: boolean; warnings?: string[] }
export interface DockerInventory { docker_version?: string; groups: DockerGroup[]; disk_free_gb: number; mem_free_mb: number; scanned_at: string }
export interface DockerAdoptRequest { server_id: string; group: string; containers?: string[]; project_name?: string }
