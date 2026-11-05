// Package api holds the wire types shared by atoll-server, atoll-agent and the atoll CLI.
// It is the single contract between components: change it here first, then in every user.
// See docs/api.md for the endpoint list.
package api

import "time"

// ---------- REST resources (JSON, /api/v1) ----------

// NodeGeo is the GeoIP record of a server's public address.
type NodeGeo struct {
	IP      string    `json:"ip"`
	Country string    `json:"country,omitempty"` // ISO 3166-1 alpha-2, e.g. "US"
	City    string    `json:"city,omitempty"`
	ASN     string    `json:"asn,omitempty"` // e.g. "AS31898"
	Org     string    `json:"org,omitempty"` // network owner, e.g. "Oracle Corporation"
	Source  string    `json:"source,omitempty"`
	At      time.Time `json:"at"`
}

type Server struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`   // "worker" | "control"
	Status      string `json:"status"` // "pending" | "online" | "offline" | "draining"
	PublicIPv4  string `json:"public_ipv4"`
	PublicIPv6  string `json:"public_ipv6,omitempty"`
	TailnetHost string `json:"tailnet_host,omitempty"`
	TailnetIP   string `json:"tailnet_ip,omitempty"` // 100.x address when the node is on the tailnet
	// Network is the private-network state the agent last reported (provider, control URL in use).
	Network  *NodeNetwork `json:"network,omitempty"`
	Provider string       `json:"provider,omitempty"` // free text, e.g. "Hetzner"
	Region   string       `json:"region,omitempty"`
	// Geo is what a GeoIP lookup of the public address said (country, city, network owner).
	// The panel shows the country's flag and guesses the provider icon from Org when
	// Provider is empty. Nil until looked up or when lookups are off (settings.geoip).
	Geo          *NodeGeo  `json:"geo,omitempty"`
	OS           string    `json:"os"`
	Arch         string    `json:"arch"`
	CPUCores     int       `json:"cpu_cores"`
	MemMB        int       `json:"mem_mb"`
	DiskGB       int       `json:"disk_gb"`
	AgentVersion string    `json:"agent_version"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	CreatedAt    time.Time `json:"created_at"`
	// latest sample, filled by server from metrics
	Usage *NodeMetrics `json:"usage,omitempty"`
	// AgentLatest is the agent version this control server hosts for the server's arch ("" =
	// none hosted). An agent on another version can be upgraded from the panel when
	// AgentSelfUpgrade is true; older agents (before v0.1.6) need install-agent.sh once.
	AgentLatest      string        `json:"agent_latest,omitempty"`
	AgentSelfUpgrade bool          `json:"agent_self_upgrade,omitempty"`
	AgentUpgrade     *AgentUpgrade `json:"agent_upgrade,omitempty"`
	// Latency is the round-trip time between the control server and the agent, measured
	// with WebSocket pings on the agent connection. Nil while offline or not measured yet.
	Latency *NodeLatency `json:"latency,omitempty"`
}

// NodeLatency is the control↔agent round-trip time in milliseconds (0.1 ms resolution).
type NodeLatency struct {
	LastMS     float64   `json:"last_ms"`
	AvgMS      float64   `json:"avg_ms"` // rolling average of the last Samples pings (~15 s apart)
	Samples    int       `json:"samples"`
	MeasuredAt time.Time `json:"measured_at"`
	// Method of the latest sample: "ping" (WebSocket ping frame) or "message" (an
	// application-level round trip, the fallback when pings keep failing).
	Method string `json:"method,omitempty"`
	// Failures counts consecutive failed measurements (WebSocket ping and fallback both
	// failed); it resets on the next success. LastError is the latest failure's error.
	Failures      int        `json:"failures,omitempty"`
	FailuresTotal int        `json:"failures_total,omitempty"` // failed measurements since the agent connected
	LastError     string     `json:"last_error,omitempty"`
	LastAttempt   *time.Time `json:"last_attempt,omitempty"`
}

type JoinToken struct {
	Token     string    `json:"token"`
	Command   string    `json:"command"` // one-line install command for the new VPS
	ExpiresAt time.Time `json:"expires_at"`
}

// InstallCommand is the agent install/upgrade one-liner for an already-joined server.
type InstallCommand struct {
	Command string `json:"command"`
	Version string `json:"version"` // the control server's version the command installs
}

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`                   // unique handle (CLI, tokens)
	DisplayName string `json:"display_name,omitempty"` // panel label only; "" = show Name
	Description string `json:"description,omitempty"`
	ServerID    string `json:"server_id"` // all services of a project run on one server
	// RootDomain is the root domain new domains of this project default to ("" = the
	// default root domain in Settings). Must be one of GET /root-domains.
	RootDomain string    `json:"root_domain,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Services   []Service `json:"services,omitempty"`
}

type Port struct {
	Name          string `json:"name"` // e.g. "web"; used for ${WEB_PORT}-style vars
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`              // "http" | "tcp" | "udp"
	PublicPort    int    `json:"public_port,omitempty"` // tcp/udp only, 0 = internal only
	// HostIP restricts a published port to one host address ("" = all interfaces), e.g.
	// "127.0.0.1" for a port an adopted container only published on loopback.
	HostIP string `json:"host_ip,omitempty"`
}

type Volume struct {
	Name      string `json:"name"`       // unique within service
	MountPath string `json:"mount_path"` // in container
	// HostPath mounts this existing host directory or file in place instead of the
	// service's own volume dir (/var/lib/atoll/volumes/<service_id>/<name>). Only Docker
	// adoption sets it (docs/adopt-docker.md); the API refuses new or changed host paths.
	// Atoll never deletes, backs up or migrates a host path.
	HostPath string `json:"host_path,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"` // mount read-only (e.g. an adopted ":ro" bind)
}

type ConfigFile struct {
	Path    string `json:"path"` // absolute path in container
	Content string `json:"content"`
}

type HealthCheck struct {
	Cmd         []string `json:"cmd,omitempty"` // docker HEALTHCHECK CMD
	HTTPPath    string   `json:"http_path,omitempty"`
	IntervalSec int      `json:"interval_sec,omitempty"`
}

type Service struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"` // DNS name inside the project network
	// DisplayName is the label the panel shows instead of Name ("" = Name). Only a label:
	// changing it redeploys nothing and the hostname stays Name.
	DisplayName    string       `json:"display_name,omitempty"`
	Kind           string       `json:"kind"`  // "image" | "postgres" | "mysql" | "redis" | "mongo" ...
	Image          string       `json:"image"` // full ref incl. tag, e.g. ghcr.io/me/app:v1
	Command        []string     `json:"command,omitempty"`
	Args           []string     `json:"args,omitempty"`
	Ports          []Port       `json:"ports"`
	Volumes        []Volume     `json:"volumes"`
	Configs        []ConfigFile `json:"configs,omitempty"`
	Health         *HealthCheck `json:"health,omitempty"`
	CPULimit       float64      `json:"cpu_limit,omitempty"` // cores, 0 = unlimited
	MemLimitMB     int          `json:"mem_limit_mb,omitempty"`
	RegistryCredID string       `json:"registry_cred_id,omitempty"`
	// User overrides the image's USER (docker run --user): "uid", "uid:gid" or a user name.
	// Empty runs the image's own user. Imports from Zeabur set it from the pod's runAsUser.
	User      string    `json:"user,omitempty"`
	Status    string    `json:"status"`   // "running" | "deploying" | "stopped" | "failed" | "unknown"
	Replicas  string    `json:"replicas"` // e.g. "1/1"
	CreatedAt time.Time `json:"created_at"`
	Domains   []Domain  `json:"domains,omitempty"`
	// ServerID is the server this service runs on (read-only in responses). Usually the
	// project's server; a service placed on another server (POST .../services with server_id,
	// or POST /services/{id}/migrate) reaches its siblings over the tailnet mesh.
	ServerID string `json:"server_id,omitempty"`
	// Source is where the image comes from (read-only): "image" (pulled from a registry) or
	// "git" (built from Git on the service's server; Image is then the last built local image).
	Source string `json:"source,omitempty"`
	// Git, when set, builds the service from a GitHub repository instead of pulling Image.
	// PATCH {"git": {...}} replaces it (the token is kept unless given or cleared);
	// PATCH {"git": null, "image": "..."} switches back to a registry image.
	Git *GitSource `json:"git,omitempty"`
	// Webhook is returned once, when a git service is created (or first switched to git): the
	// GitHub push webhook URL and its secret. Later: GET /services/{id}/webhook (secret: panel session only).
	Webhook *GitWebhook `json:"webhook,omitempty"`
}

// GitSource builds a service from source (see docs/api.md "Builds from source"). The build
// runs on the service's own server, never on the control server.
type GitSource struct {
	Repo       string            `json:"repo"`                 // https://github.com/OWNER/REPO
	Branch     string            `json:"branch,omitempty"`     // default "main"
	Context    string            `json:"context,omitempty"`    // build context: subdirectory of the repo ("" = root)
	Dockerfile string            `json:"dockerfile,omitempty"` // path inside the context, default "Dockerfile"
	BuildArgs  map[string]string `json:"build_args,omitempty"` // docker build --build-arg (not secret: visible in image history)
	// Token is a GitHub token for a private repository. Write-only: stored encrypted, never
	// returned (HasToken says whether one is stored). ClearToken (PATCH) removes it.
	Token      string `json:"token,omitempty"`
	HasToken   bool   `json:"has_token"`
	ClearToken bool   `json:"clear_token,omitempty"`
	LastCommit string `json:"last_commit,omitempty"` // read-only: commit of the last successful build
}

// GitWebhook is GET /api/v1/services/{id}/webhook: configure it on GitHub as a push webhook
// (content type application/json). Secret is only returned once at creation / rotation, and
// to a panel session; API tokens get HasSecret only.
type GitWebhook struct {
	URL         string `json:"url"`
	Secret      string `json:"secret,omitempty"`
	HasSecret   bool   `json:"has_secret"`
	ContentType string `json:"content_type"` // "application/json"
	Events      string `json:"events"`       // "push"
	Branch      string `json:"branch"`       // only pushes to this branch deploy
}

// DeploymentLogLine is one line of a deployment's build log.
type DeploymentLogLine struct {
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
	Line string    `json:"line"`
}

// DeploymentLogs is GET /api/v1/services/{id}/deployments/{dep}/logs (?after=SEQ for the
// lines after a seen one). Only the newest lines of a long build are kept (Truncated).
type DeploymentLogs struct {
	DeploymentID string              `json:"deployment_id"`
	Status       string              `json:"status"`
	Lines        []DeploymentLogLine `json:"lines"`
	Truncated    bool                `json:"truncated,omitempty"`
}

type EnvVar struct {
	Key     string `json:"key"`
	Value   string `json:"value"` // masked as "" when Secret and not revealed
	Secret  bool   `json:"secret"`
	Exposed bool   `json:"exposed"` // shared with other services in the project as ${KEY}
	// Source is set only in GET /services/{id}/env/references: the service name
	// the var comes from (reference it explicitly as ${SOURCE.KEY}).
	Source string `json:"source,omitempty"`
}

type Domain struct {
	ID        string    `json:"id"`
	ServiceID string    `json:"service_id"`
	Hostname  string    `json:"hostname"`
	PortName  string    `json:"port_name"`
	Proxied   bool      `json:"proxied"` // Cloudflare orange cloud
	Status    string    `json:"status"`  // "pending" | "active" | "error"
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Cert is the certificate the servers present for the hostname, filled in by the
	// domain lists: "origin" (Cloudflare Origin CA), "acme" (Let's Encrypt via Caddy) or
	// "http" (no certificate on the server: Cloudflare-only HTTP). Empty when unknown.
	Cert string `json:"cert,omitempty"`
}

// DomainPatchRequest is the body of PATCH /domains/{id}.
type DomainPatchRequest struct {
	Proxied *bool `json:"proxied"` // Cloudflare orange cloud on/off; updates the DNS record
}

// CloudflareTokenCheck is GET /cloudflare/token-check: what the stored API token can do,
// probed with requests Cloudflare rejects before they change anything.
type CloudflareTokenCheck struct {
	Configured bool `json:"configured"` // a token is stored
	// DNS is "ok" | "missing" | "unknown" for Zone.DNS: Edit on the default root's zone.
	DNS      string `json:"dns"`
	DNSError string `json:"dns_error,omitempty"`
	// SSL is Zone.SSL and Certificates: Edit, needed to issue Origin CA certificates for
	// the roots in SSLRoots (cert mode auto). "" when no root needs it.
	SSL       string    `json:"ssl,omitempty"`
	SSLError  string    `json:"ssl_error,omitempty"`
	SSLRoots  []string  `json:"ssl_roots,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// RootDomain is one entry of the root domain list (Settings → Domains). A bare hostname
// ("blog") is completed with a root domain: the one named in the request, else the
// project's default, else the default root domain.
type RootDomain struct {
	Name     string `json:"name"`    // e.g. example.com
	Default  bool   `json:"default"` // settings.base_domain mirrors the default's name
	ZoneID   string `json:"zone_id,omitempty"`
	ZoneName string `json:"zone_name,omitempty"` // Cloudflare zone, longest suffix match
	// CertMode: "global" follows the global TLS settings (tls_mode + uploaded origin
	// certificate, the pre-v0.1.24 behaviour); "auto" issues a Cloudflare Origin CA
	// certificate for name and *.name and falls back to ACME when that fails; "acme" always
	// uses Caddy's ACME issuance.
	CertMode string `json:"cert_mode"`
	// CertStatus is what the agents actually serve: "global" | "origin" | "acme".
	CertStatus    string     `json:"cert_status"`
	CertError     string     `json:"cert_error,omitempty"` // why Origin CA issuance failed (auto mode)
	CertExpiresAt *time.Time `json:"cert_expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// RootDomainRequest is the body of POST /root-domains and PATCH /root-domains/{name}.
type RootDomainRequest struct {
	Name     string `json:"name,omitempty"`      // POST only
	CertMode string `json:"cert_mode,omitempty"` // "" = "auto" (POST; "global" for the first domain) / unchanged (PATCH)
	Default  *bool  `json:"default,omitempty"`   // true makes it the default; false is ignored
}

type Deployment struct {
	ID          string     `json:"id"`
	ServiceID   string     `json:"service_id"`
	ServerID    string     `json:"server_id"`
	ImageDigest string     `json:"image_digest,omitempty"`
	Status      string     `json:"status"`           // "queued" | "building" | "pulling" | "starting" | "running" | "failed" | "superseded"
	Trigger     string     `json:"trigger"`          // "create" | "redeploy" | "env" | "rollback" | "import" | "migrate" | "image-update" | "webhook"
	Commit      string     `json:"commit,omitempty"` // git services: the commit built (set once the build finished)
	Message     string     `json:"message,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

type APIToken struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scope string `json:"scope"` // legacy summary derived from Permissions: "read" | "deploy" | "admin"
	// Permissions the token holds (see Perm*); "read" is always included.
	Permissions []string `json:"permissions"`
	// Projects limits the token to these project ids; empty = all projects.
	Projects []string `json:"projects,omitempty"`
	// RequireApproval: destructive operations the token is allowed to do still wait
	// for approval in the panel (202 Confirmation) instead of running directly.
	RequireApproval bool       `json:"require_approval,omitempty"`
	Prefix          string     `json:"prefix"`          // first chars, e.g. "atl_pat_3fA9"
	Token           string     `json:"token,omitempty"` // only returned once at creation
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// ---------- API token permissions ----------

// Token permissions. A token can do an operation only when it holds the permission;
// without it the request fails with 403 missing_permission (no approval request).
// With it the operation runs directly, unless the token has RequireApproval.
const (
	PermRead     = "read"     // every GET: projects, services, env (masked), logs, metrics, settings
	PermDeploy   = "deploy"   // create/edit projects and services, env, start/stop/restart/redeploy, rollback, image updates, backup policy + run, imports
	PermExec     = "exec"     // run commands inside service containers (a shell in the container: reads its secrets and data)
	PermDelete   = "delete"   // delete services and projects (with their volumes)
	PermMigrate  = "migrate"  // move projects between servers, Zeabur cutover / rollback
	PermDomains  = "domains"  // bind and remove domains (changes DNS)
	PermRestore  = "restore"  // restore backups (replaces live data)
	PermServers  = "servers"  // join tokens, rename/remove servers
	PermSettings = "settings" // panel settings (Cloudflare, TLS, backup storage)
	PermTokens   = "tokens"   // list/create/change/delete API tokens (never more than the token itself holds)
)

// AllPermissions in display order.
var AllPermissions = []string{PermRead, PermDeploy, PermExec, PermDelete, PermMigrate, PermDomains, PermRestore, PermServers, PermSettings, PermTokens}

// GlobalPermissions cannot be combined with a project-scoped token.
var GlobalPermissions = []string{PermServers, PermSettings, PermTokens}

// TokenRequest creates a token (POST /tokens). Scope is the legacy shorthand
// (read → read; deploy → read+deploy; admin → all) used when Permissions is empty.
type TokenRequest struct {
	Name            string   `json:"name"`
	Scope           string   `json:"scope,omitempty"`
	Permissions     []string `json:"permissions,omitempty"`
	Projects        []string `json:"projects,omitempty"`
	RequireApproval bool     `json:"require_approval,omitempty"`
}

// TokenUpdate changes a token (PATCH /tokens/{id}); nil fields stay.
type TokenUpdate struct {
	Name            *string   `json:"name,omitempty"`
	Permissions     *[]string `json:"permissions,omitempty"`
	Projects        *[]string `json:"projects,omitempty"`
	RequireApproval *bool     `json:"require_approval,omitempty"`
}

// ---------- panel login & two-factor auth ----------

// LoginChallenge is POST /auth/login's answer when the account has 2FA on: finish
// with POST /auth/login/2fa {pending_token, code} before PendingToken expires.
type LoginChallenge struct {
	TwoFactorRequired bool      `json:"two_factor_required"`
	PendingToken      string    `json:"pending_token"`
	ExpiresAt         time.Time `json:"expires_at"`
}

// LoginTwoFactorRequest completes a login; Code is a 6-digit TOTP or a recovery code.
type LoginTwoFactorRequest struct {
	PendingToken string `json:"pending_token"`
	Code         string `json:"code"`
}

// TwoFactorStatus is GET /auth/2fa.
type TwoFactorStatus struct {
	Enabled           bool       `json:"enabled"`
	EnabledAt         *time.Time `json:"enabled_at,omitempty"`
	RecoveryCodesLeft int        `json:"recovery_codes_left"`
}

// TwoFactorEnrollment is POST /auth/2fa/enroll: show OTPAuthURI as a QR code.
type TwoFactorEnrollment struct {
	Secret     string `json:"secret"`      // base32, for manual entry
	OTPAuthURI string `json:"otpauth_uri"` // otpauth://totp/...
}

// TwoFactorRequest re-authenticates a 2FA change (which fields are needed depends on the endpoint).
type TwoFactorRequest struct {
	Password string `json:"password,omitempty"`
	Code     string `json:"code,omitempty"`
}

// ChangePasswordRequest is POST /auth/password (panel session only). Code is the
// current TOTP code, required when two-factor authentication is on.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	Code            string `json:"code,omitempty"`
}

// ChangeUsernameRequest is POST /auth/username (panel session only): the new login name,
// confirmed with the current password (and the TOTP code when 2FA is on).
type ChangeUsernameRequest struct {
	Username        string `json:"username"`
	CurrentPassword string `json:"current_password"`
	Code            string `json:"code,omitempty"`
}

// ChangePasswordResponse reports how many other sessions were signed out.
type ChangePasswordResponse struct {
	RevokedSessions int64 `json:"revoked_sessions"`
}

// RecoveryCodes are shown once, after enabling 2FA or regenerating.
type RecoveryCodes struct {
	Codes []string `json:"recovery_codes"`
}

// SessionInfo is one live panel session (GET /auth/sessions).
type SessionInfo struct {
	ID        string    `json:"id"`
	IP        string    `json:"ip,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	Current   bool      `json:"current"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Confirmation: destructive ops (delete, migrate, DNS change) requested with an API token
// return 202 + Confirmation; a human approves it in the panel.
type Confirmation struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"` // "service.delete" | "project.delete" | "service.migrate" | "domain.add" | "domain.delete" | "domain.proxy" | "server.delete"
	Target    string    `json:"target"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"` // "pending" | "approved" | "rejected" | "expired"
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type Settings struct {
	BaseDomain         string `json:"base_domain"` // e.g. example.com
	CloudflareZoneID   string `json:"cloudflare_zone_id"`
	CloudflareToken    string `json:"cloudflare_token,omitempty"` // write-only; GET returns "" and HasCloudflareToken
	HasCloudflareToken bool   `json:"has_cloudflare_token"`
	TLSMode            string `json:"tls_mode"`   // "cloudflare-origin" | "acme"
	PublicURL          string `json:"public_url"` // panel URL agents/CLI use
	// Cloudflare Origin CA certificate pushed to every agent (MsgTLS).
	// Write-only like CloudflareToken: GET returns "" and HasOriginCert.
	OriginCertPEM string `json:"origin_cert_pem,omitempty"`
	OriginKeyPEM  string `json:"origin_key_pem,omitempty"`
	HasOriginCert bool   `json:"has_origin_cert"`
	ACMEEmail     string `json:"acme_email,omitempty"` // used when TLSMode == "acme"

	// Backup target (any S3 API: Cloudflare R2, Backblaze B2, AWS S3, MinIO). See BackupRepo.
	// Access/secret key and the restic repository password are write-only like CloudflareToken.
	BackupEndpoint       string `json:"backup_endpoint,omitempty"` // e.g. https://<account>.r2.cloudflarestorage.com
	BackupBucket         string `json:"backup_bucket,omitempty"`
	BackupPrefix         string `json:"backup_prefix,omitempty"` // path inside the bucket, default "atoll"
	BackupRegion         string `json:"backup_region,omitempty"` // "auto" for R2; B2 e.g. us-west-004
	BackupAccessKey      string `json:"backup_access_key,omitempty"`
	BackupSecretKey      string `json:"backup_secret_key,omitempty"`
	HasBackupCredentials bool   `json:"has_backup_credentials"`
	// BackupRepoPassword encrypts the restic repository. Write it to set an existing repo's
	// password (disaster recovery on a fresh control server). When none is stored the first
	// save of a backup target generates one and returns it ONCE in the PUT response.
	BackupRepoPassword string `json:"backup_repo_password,omitempty"`
	HasBackupPassword  bool   `json:"has_backup_password"`
	BackupConfigured   bool   `json:"backup_configured"` // endpoint, bucket, credentials and password all set
	// GeoIP is "on" (default: look up each server's public IP at ipinfo.io, ip-api.com as
	// fallback, for the flag and provider icon) or "off". "" in a PUT keeps the stored value.
	GeoIP string `json:"geoip,omitempty"`
	// Backup verification (see docs/api.md "Backup verification"). Empty keeps the stored value;
	// "auto" resets a schedule (or the subset) to its default; "off" turns a schedule off.
	// GET returns the stored value ("" = auto).
	BackupCheckSchedule string `json:"backup_check_schedule,omitempty"` // weekly restic check, cron
	BackupCheckSubset   string `json:"backup_check_subset,omitempty"`   // --read-data-subset, default "5%"
	BackupDrillSchedule string `json:"backup_drill_schedule,omitempty"` // monthly restore drill, cron
	// ImageCheckIntervalHours is how often image update checks run (default 6; PUT 0 keeps the stored value).
	ImageCheckIntervalHours int `json:"image_check_interval_hours"`
	// Limits a new service gets when it is created without its own (template, image, git
	// build, import). 0 = no limit. Defaults: 1024 MB memory, CPU unlimited. GET always
	// returns both; a PUT leaving one out (null) keeps the stored value. Existing services
	// are not changed.
	DefaultMemLimitMB *int     `json:"default_mem_limit_mb"`
	DefaultCPULimit   *float64 `json:"default_cpu_limit"`

	// Private network provider for machine-to-machine traffic (agent<->control, service<->service
	// across servers). See docs/api.md "Private network (Tailscale)".
	NetworkProvider string `json:"network_provider"` // "" (none) | "tailscale"; PUT omitting it keeps the stored value, "none" clears
	// TailscaleAuthKey is write-only (reusable, tagged, ideally pre-approved); GET returns "" and HasTailscaleAuthKey.
	TailscaleAuthKey      string `json:"tailscale_auth_key,omitempty"`
	HasTailscaleAuthKey   bool   `json:"has_tailscale_auth_key"`
	ClearTailscaleAuthKey bool   `json:"clear_tailscale_auth_key,omitempty"` // PUT only
	TailnetName           string `json:"tailnet_name,omitempty"`             // e.g. tail1234.ts.net (MagicDNS suffix)
	// TailnetControlURL is the agent endpoint over the tailnet, e.g. http://100.101.102.103:7301.
	// Empty = derived from the server's tailnet listener (TailnetListen).
	TailnetControlURL string `json:"tailnet_control_url,omitempty"`
	// read-only status
	TailnetListen    string `json:"tailnet_listen,omitempty"`       // address the tailnet agent listener is bound to ("" = none)
	TailnetListenErr string `json:"tailnet_listen_error,omitempty"` // why the tailnet listener is not up yet
	AgentPublic      bool   `json:"agent_public"`                   // agent endpoints also answer on the public listener
}

// Template is a one-click database service (GET /api/v1/templates). Env values may
// contain ${PASSWORD}, which the server fills with a random 32-char secret on create.
type Template struct {
	Kind        string   `json:"kind"` // "postgres" | "mysql" | "redis" | "mongo"
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Image       string   `json:"image"`
	Command     []string `json:"command,omitempty"`
	Ports       []Port   `json:"ports"`
	Volumes     []Volume `json:"volumes"`
	Env         []EnvVar `json:"env"`
}

// MigrationJob tracks POST /api/v1/projects/{id}/migrate (GET /api/v1/migrations/{id}).
type MigrationJob struct {
	ID         string       `json:"id"` // "mig_..."
	ProjectID  string       `json:"project_id"`
	ServiceID  string       `json:"service_id,omitempty"` // set when one service moves (POST /services/{id}/migrate)
	FromServer string       `json:"from_server_id"`
	ToServer   string       `json:"to_server_id"`
	Status     string       `json:"status"` // "running" | "done" | "failed"
	Steps      []ImportStep `json:"steps"`
	Error      string       `json:"error,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	FinishedAt *time.Time   `json:"finished_at,omitempty"`
}

type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

// ---------- metrics & logs ----------

type NodeMetrics struct {
	At          time.Time `json:"at"`
	CPUPct      float64   `json:"cpu_pct"`
	MemUsedMB   int       `json:"mem_used_mb"`
	MemTotalMB  int       `json:"mem_total_mb"`
	DiskUsedGB  float64   `json:"disk_used_gb"`
	DiskTotalGB float64   `json:"disk_total_gb"`
	NetRxBps    float64   `json:"net_rx_bps"`
	NetTxBps    float64   `json:"net_tx_bps"`
	Load1       float64   `json:"load1"`
}

type ContainerMetrics struct {
	ServiceID string    `json:"service_id"`
	At        time.Time `json:"at"`
	CPUPct    float64   `json:"cpu_pct"`
	MemMB     float64   `json:"mem_mb"`
	NetRxBps  float64   `json:"net_rx_bps"`
	NetTxBps  float64   `json:"net_tx_bps"`
}

type LogLine struct {
	ServiceID string    `json:"service_id"`
	At        time.Time `json:"at"`
	Stream    string    `json:"stream"` // "stdout" | "stderr"
	Line      string    `json:"line"`
}

// ProjectPrefs are one account's favorite and pinned projects (GET/PUT /api/v1/me/project-prefs).
type ProjectPrefs struct {
	Favorites []string `json:"favorites"` // project ids, in the sidebar order (PUT saves the order sent)
	Pinned    []string `json:"pinned"`    // project ids, in the order they show at the top
}
