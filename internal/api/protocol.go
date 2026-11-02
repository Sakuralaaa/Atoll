package api

import "time"

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
	MsgNetwork      = "network"       // NetworkPayload: join the tailnet and/or switch the control URL (verified first); result Output = JSON NetworkResult
	MsgMesh         = "mesh"          // MeshPayload: full cross-server service mesh of this node; agent persists it and runs forwarders
	MsgBuild        = "build"         // BuildPayload: clone + docker build on this worker (one at a time); result Output = JSON BuildResult; log via MsgBuildLog
	MsgUpgrade      = "upgrade"       // UpgradePayload: replace the agent binary (verified) and restart; result Output = JSON UpgradeStarted; outcome via MsgUpgradeResult

	// agent -> server
	MsgHello         = "hello"          // HelloPayload, first frame after connect
	MsgStatus        = "status"         // StatusPayload, on change and every 30s
	MsgMetrics       = "metrics"        // MetricsPayload, every 15s
	MsgDeploy        = "deploy"         // DeployEvent
	MsgLogLine       = "log"            // LogEvent
	MsgBuildLog      = "build.log"      // BuildLogEvent: build output lines (dropped while disconnected)
	MsgUpgradeResult = "upgrade.result" // UpdateResult of the last agent self-upgrade (sent once after reconnecting)
	MsgResult        = "result"         // ResultPayload
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
	// LocalImage: Image only exists on this server (built from source); never pull it.
	LocalImage bool `json:"local_image,omitempty"`
	Stopped    bool `json:"stopped"` // desired state is stopped
}

// BuildPayload asks the service's server to build its image from Git: shallow fetch of Commit
// (or the head of Branch) into a temp dir, then `docker build` (BuildKit) tagged
// atoll-build/<service_id>:<commit12>. Only the newest Keep build images of the service are
// kept (plus the one in use). Token is a secret: never log the payload.
type BuildPayload struct {
	DeploymentID string            `json:"deployment_id"`
	ServiceID    string            `json:"service_id"`
	Repo         string            `json:"repo"`
	Branch       string            `json:"branch"`
	Commit       string            `json:"commit,omitempty"` // exact commit to build ("" = head of Branch)
	Context      string            `json:"context,omitempty"`
	Dockerfile   string            `json:"dockerfile,omitempty"`
	BuildArgs    map[string]string `json:"build_args,omitempty"`
	Token        string            `json:"token,omitempty"`
	TimeoutSec   int               `json:"timeout_sec"` // the build itself (default 1200)
	QueueSec     int               `json:"queue_sec"`   // max wait for this server's build slot (default = TimeoutSec)
	Keep         int               `json:"keep"`        // build images kept per service (default 3)
}

// BuildResult is the Output of MsgBuild.
type BuildResult struct {
	Image  string `json:"image"`  // local tag, e.g. atoll-build/svc_abc:0123456789ab
	Commit string `json:"commit"` // full commit sha built
}

// BuildLogEvent carries build output lines of one deployment.
type BuildLogEvent struct {
	DeploymentID string   `json:"deployment_id"`
	ServiceID    string   `json:"service_id"`
	Lines        []string `json:"lines"`
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
	NodeID        string       `json:"node_id"`
	AgentVersion  string       `json:"agent_version"`
	Hostname      string       `json:"hostname"`
	OS            string       `json:"os"`
	Arch          string       `json:"arch"`
	CPUCores      int          `json:"cpu_cores"`
	MemMB         int          `json:"mem_mb"`
	DiskGB        int          `json:"disk_gb"`
	PublicIPv4    string       `json:"public_ipv4"`
	PublicIPv6    string       `json:"public_ipv6,omitempty"`
	TailnetHost   string       `json:"tailnet_host,omitempty"`
	DockerVersion string       `json:"docker_version"`
	TailnetIP     string       `json:"tailnet_ip,omitempty"`
	Network       *NodeNetwork `json:"network,omitempty"`
	// SelfUpgrade: this agent handles MsgUpgrade (v0.1.6+).
	SelfUpgrade bool `json:"self_upgrade,omitempty"`
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
