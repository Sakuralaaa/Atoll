// Package api holds the wire types shared by atoll-server, atoll-agent and the atoll CLI.
// It is the single contract between components: change it here first, then in every user.
// See docs/api.md for the endpoint list.
package api

import "time"

// ---------- REST resources (JSON, /api/v1) ----------

type Server struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Role         string    `json:"role"`   // "worker" | "control"
	Status       string    `json:"status"` // "pending" | "online" | "offline" | "draining"
	PublicIPv4   string    `json:"public_ipv4"`
	PublicIPv6   string    `json:"public_ipv6,omitempty"`
	TailnetHost  string    `json:"tailnet_host,omitempty"`
	Provider     string    `json:"provider,omitempty"` // free text, e.g. "Hetzner"
	Region       string    `json:"region,omitempty"`
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
}

type JoinToken struct {
	Token     string    `json:"token"`
	Command   string    `json:"command"` // one-line install command for the new VPS
	ExpiresAt time.Time `json:"expires_at"`
}

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	ServerID    string    `json:"server_id"` // all services of a project run on one server
	CreatedAt   time.Time `json:"created_at"`
	Services    []Service `json:"services,omitempty"`
}

type Port struct {
	Name          string `json:"name"` // e.g. "web"; used for ${WEB_PORT}-style vars
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`              // "http" | "tcp" | "udp"
	PublicPort    int    `json:"public_port,omitempty"` // tcp/udp only, 0 = internal only
}

type Volume struct {
	Name      string `json:"name"`       // unique within service
	MountPath string `json:"mount_path"` // in container
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
	ID             string       `json:"id"`
	ProjectID      string       `json:"project_id"`
	Name           string       `json:"name"`  // DNS name inside the project network
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
	Status         string       `json:"status"`   // "running" | "deploying" | "stopped" | "failed" | "unknown"
	Replicas       string       `json:"replicas"` // e.g. "1/1"
	CreatedAt      time.Time    `json:"created_at"`
	Domains        []Domain     `json:"domains,omitempty"`
}

type EnvVar struct {
	Key     string `json:"key"`
	Value   string `json:"value"` // masked as "" when Secret and not revealed
	Secret  bool   `json:"secret"`
	Exposed bool   `json:"exposed"` // shared with other services in the project as ${KEY}
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
}

type Deployment struct {
	ID          string     `json:"id"`
	ServiceID   string     `json:"service_id"`
	ServerID    string     `json:"server_id"`
	ImageDigest string     `json:"image_digest,omitempty"`
	Status      string     `json:"status"`  // "queued" | "pulling" | "starting" | "running" | "failed" | "superseded"
	Trigger     string     `json:"trigger"` // "create" | "redeploy" | "env" | "rollback" | "import" | "migrate"
	Message     string     `json:"message,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`           // "read" | "deploy" | "admin"
	Prefix     string     `json:"prefix"`          // first chars, e.g. "atl_pat_3fA9"
	Token      string     `json:"token,omitempty"` // only returned once at creation
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Confirmation: destructive ops (delete, migrate, DNS change) requested with an API token
// return 202 + Confirmation; a human approves it in the panel.
type Confirmation struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"` // "service.delete" | "project.delete" | "service.migrate" | "domain.add" | "domain.delete" | "server.delete"
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

// ---------- agent protocol (WebSocket /agent/v1/connect) ----------
// Every frame is an Envelope. Requests from server carry an ID; the agent answers with
// type "result" and the same ID. Events from the agent carry no ID.

type Envelope struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Payload any    `json:"payload,omitempty"`
}

const (
	// server -> agent
	MsgApply        = "apply"     // ApplyPayload: create/update one service (full resolved spec)
	MsgRemove       = "remove"    // RemovePayload
	MsgAction       = "action"    // ActionPayload: start/stop/restart
	MsgSync         = "sync"      // SyncPayload: full desired state for this node; agent removes unknown atoll containers
	MsgIngress      = "ingress"   // IngressPayload: full route table for this node's Caddy
	MsgLogsOpen     = "logs.open" // LogsOpenPayload, agent then streams MsgLogLine events tagged with Stream ID
	MsgLogsClose    = "logs.close"
	MsgExec         = "exec"          // ExecPayload, one-shot command, result carries output
	MsgVolumeExport = "volume.export" // VolumeTransferPayload: agent PUTs tar.gz of volume to URL
	MsgVolumeImport = "volume.import" // VolumeTransferPayload: agent GETs tar.gz from URL into volume
	MsgTLS          = "tls"           // TLSPayload: origin cert+key for Caddy

	// agent -> server
	MsgHello   = "hello"   // HelloPayload, first frame after connect
	MsgStatus  = "status"  // StatusPayload, on change and every 30s
	MsgMetrics = "metrics" // MetricsPayload, every 15s
	MsgDeploy  = "deploy"  // DeployEvent
	MsgLogLine = "log"     // LogEvent
	MsgResult  = "result"  // ResultPayload
)

// ResolvedSpec is what the agent runs: env fully expanded, nothing to look up.
type ResolvedSpec struct {
	ServiceID    string            `json:"service_id"`
	ProjectID    string            `json:"project_id"`
	DeploymentID string            `json:"deployment_id"`
	Name         string            `json:"name"`    // container DNS alias inside network
	Network      string            `json:"network"` // docker network, "atoll-<project_id>"
	Image        string            `json:"image"`
	Command      []string          `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	Env          map[string]string `json:"env"`
	Ports        []Port            `json:"ports"`
	Volumes      []Volume          `json:"volumes"` // host path: /var/lib/atoll/volumes/<service_id>/<name>
	Configs      []ConfigFile      `json:"configs,omitempty"`
	Health       *HealthCheck      `json:"health,omitempty"`
	CPULimit     float64           `json:"cpu_limit,omitempty"`
	MemLimitMB   int               `json:"mem_limit_mb,omitempty"`
	RegistryAuth *RegistryAuth     `json:"registry_auth,omitempty"`
	Stopped      bool              `json:"stopped"` // desired state is stopped
}

type RegistryAuth struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type ApplyPayload struct {
	Spec ResolvedSpec `json:"spec"`
}
type RemovePayload struct {
	ServiceID   string `json:"service_id"`
	KeepVolumes bool   `json:"keep_volumes"`
}
type ActionPayload struct {
	ServiceID string `json:"service_id"`
	Action    string `json:"action"` // "start" | "stop" | "restart"
}
type SyncPayload struct {
	Specs []ResolvedSpec `json:"specs"`
}

type IngressRoute struct {
	Hostname  string `json:"hostname"`
	Upstream  string `json:"upstream"` // "<container_name>:<port>" on docker network atoll-ingress
	ServiceID string `json:"service_id"`
}
type IngressPayload struct {
	Routes         []IngressRoute `json:"routes"`
	TLSMode        string         `json:"tls_mode"` // "cloudflare-origin" | "acme"
	ACMEEmail      string         `json:"acme_email,omitempty"`
	CloudflareOnly bool           `json:"cloudflare_only"` // firewall: allow 80/443 only from CF ranges
}
type TLSPayload struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

type LogsOpenPayload struct {
	StreamID  string `json:"stream_id"`
	ServiceID string `json:"service_id"`
	Tail      int    `json:"tail"`
	Follow    bool   `json:"follow"`
}
type LogEvent struct {
	StreamID string  `json:"stream_id"`
	Line     LogLine `json:"line"`
}
type ExecPayload struct {
	ServiceID  string   `json:"service_id"`
	Cmd        []string `json:"cmd"`
	TimeoutSec int      `json:"timeout_sec"`
}
type VolumeTransferPayload struct {
	ServiceID string `json:"service_id"`
	Volume    string `json:"volume"`
	URL       string `json:"url"` // presigned server URL (one-time token in query)
}

type HelloPayload struct {
	NodeID        string `json:"node_id"`
	AgentVersion  string `json:"agent_version"`
	Hostname      string `json:"hostname"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	CPUCores      int    `json:"cpu_cores"`
	MemMB         int    `json:"mem_mb"`
	DiskGB        int    `json:"disk_gb"`
	PublicIPv4    string `json:"public_ipv4"`
	PublicIPv6    string `json:"public_ipv6,omitempty"`
	TailnetHost   string `json:"tailnet_host,omitempty"`
	DockerVersion string `json:"docker_version"`
}

type ContainerState struct {
	ServiceID    string    `json:"service_id"`
	DeploymentID string    `json:"deployment_id"`
	State        string    `json:"state"` // docker state: running/exited/restarting/created/missing
	Health       string    `json:"health,omitempty"`
	ImageDigest  string    `json:"image_digest,omitempty"`
	RestartCount int       `json:"restart_count"`
	StartedAt    time.Time `json:"started_at"`
}
type StatusPayload struct {
	Containers []ContainerState `json:"containers"`
}
type MetricsPayload struct {
	Node       NodeMetrics        `json:"node"`
	Containers []ContainerMetrics `json:"containers"`
}
type DeployEvent struct {
	DeploymentID string `json:"deployment_id"`
	ServiceID    string `json:"service_id"`
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
	ImageDigest  string `json:"image_digest,omitempty"`
}
type ResultPayload struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Output string `json:"output,omitempty"`
}

// Join (plain HTTPS, before WebSocket): POST /agent/v1/join
type JoinRequest struct {
	JoinToken string       `json:"join_token"`
	Hello     HelloPayload `json:"hello"`
}
type JoinResponse struct {
	NodeID    string `json:"node_id"`
	NodeToken string `json:"node_token"` // long-lived bearer for /agent/v1/connect
}

// ---------- Zeabur import ----------
// The CLI (`atoll import zeabur`) runs on the user's machine where `npx zeabur` is logged in,
// builds an ImportPlan, then POSTs it to /api/v1/imports. Data (volumes, db dumps) is uploaded
// separately per volume.

type ImportPlan struct {
	Source        string          `json:"source"`         // "zeabur"
	SourceProject string          `json:"source_project"` // zeabur project id
	ProjectName   string          `json:"project_name"`
	TargetServer  string          `json:"target_server_id"`
	Services      []ImportService `json:"services"`
	Warnings      []string        `json:"warnings,omitempty"`
}

type ImportService struct {
	SourceServiceID string       `json:"source_service_id"`
	Name            string       `json:"name"`
	Kind            string       `json:"kind"`
	Image           string       `json:"image"`            // pinned; for zeabur-registry images: the ref after re-push
	ImageNeedsPush  bool         `json:"image_needs_push"` // built on Zeabur, exported tar must be pushed to user's registry
	Command         []string     `json:"command,omitempty"`
	Ports           []Port       `json:"ports"`
	Volumes         []Volume     `json:"volumes"`
	Configs         []ConfigFile `json:"configs,omitempty"`
	Env             []EnvVar     `json:"env"`     // expanded values; .zeabur.internal rewritten
	Domains         []string     `json:"domains"` // custom domains only; *.zeabur.app listed in Warnings
	DataBytes       int64        `json:"data_bytes"`
}

type ImportJob struct {
	ID        string       `json:"id"`
	Plan      ImportPlan   `json:"plan"`
	ProjectID string       `json:"project_id,omitempty"`
	Status    string       `json:"status"` // "planned" | "uploading" | "deploying" | "verifying" | "done" | "failed"
	Steps     []ImportStep `json:"steps"`
	CreatedAt time.Time    `json:"created_at"`
}

type ImportStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "pending" | "running" | "done" | "failed" | "skipped"
	Detail string `json:"detail,omitempty"`
}
