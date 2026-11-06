package api

import "time"

// ---------- Image update detection (see docs/api.md "Image updates") ----------
// The server periodically resolves each service's image tag to its registry manifest digest
// (HEAD, OCI distribution API) and compares it with the digest the agent reports in
// ContainerState.ImageDigest. Semver-like tags also get the newest higher tag.

const (
	ImageUpdatePolicyNotify = "notify" // default: show the update, do nothing
	ImageUpdatePolicyAuto   = "auto"   // redeploy pinned to the new digest (same tag), inside the window
	ImageUpdatePolicyOff    = "off"    // never check
)

// ImageUpdate is GET /api/v1/services/{id}/image-update (and one item of GET /api/v1/image-updates).
type ImageUpdate struct {
	ServiceID string `json:"service_id"`
	ProjectID string `json:"project_id"`
	Service   string `json:"service"` // service name
	Image     string `json:"image"`   // the service's image ref
	Policy    string `json:"policy"`  // notify | auto | off
	// Window is the optional daily auto-update window "HH:MM-HH:MM" (UTC, may wrap midnight); "" = any time.
	Window string `json:"window,omitempty"`
	// Status: up_to_date | update_available | unknown (not checked yet / running digest unknown) |
	// error | unsupported (local or digest-only image) | disabled (policy off) | pinned (image fixed to a digest).
	Status          string `json:"status"`
	UpdateAvailable bool   `json:"update_available"` // digest changed or a newer same-major tag exists
	// DigestChanged: the tag now points to a different manifest than the running container.
	DigestChanged bool   `json:"digest_changed"`
	CurrentDigest string `json:"current_digest,omitempty"` // running (agent-reported)
	LatestDigest  string `json:"latest_digest,omitempty"`  // registry digest of the current tag
	CurrentTag    string `json:"current_tag,omitempty"`
	// CurrentVersion / LatestVersion are the images' org.opencontainers.image.version labels
	// ("" when the image has none or it was not looked up yet).
	CurrentVersion string `json:"current_version,omitempty"`
	LatestVersion  string `json:"latest_version,omitempty"`
	// Pinned: the service's image names a digest (name@sha256:…), set by hand or by a rollback.
	// A pinned service is never reported as update_available and never auto-updated; an
	// explicit update returns it to following CurrentTag.
	Pinned bool `json:"pinned,omitempty"`
	// NewerTag is the newest higher semver tag with the same major (and same shape/suffix);
	// MajorTag the newest with a higher major, shown separately (not counted in UpdateAvailable).
	NewerTag     string     `json:"newer_tag,omitempty"`
	MajorTag     string     `json:"major_tag,omitempty"`
	ChangelogURL string     `json:"changelog_url,omitempty"`
	Error        string     `json:"error,omitempty"`
	Note         string     `json:"note,omitempty"` // e.g. why auto-update is waiting or paused
	CheckedAt    *time.Time `json:"checked_at,omitempty"`
	NextCheckAt  *time.Time `json:"next_check_at,omitempty"`
	// LastAutoDigest is the last digest auto-update deployed (never retried automatically).
	LastAutoDigest string `json:"last_auto_digest,omitempty"`
}

// ImageUpdatePolicy is the body of PUT /api/v1/services/{id}/image-update/policy.
type ImageUpdatePolicy struct {
	Policy string `json:"policy"`
	Window string `json:"window,omitempty"`
}

// ImageUpdateApplyRequest is the body of POST /api/v1/services/{id}/image-update/apply.
// Empty Tag redeploys the current tag pinned to its latest digest; a Tag switches the
// service to that tag (validated against the registry) and deploys it.
type ImageUpdateApplyRequest struct {
	Tag string `json:"tag,omitempty"`
}

// ---------- Private network (Tailscale) and cross-server service mesh ----------
// See docs/api.md "Private network (Tailscale)". The panel stays public; machine-to-machine
// traffic (agent<->control, service<->service across servers) goes over the tailnet.

const (
	NetworkProviderNone      = ""
	NetworkProviderTailscale = "tailscale"
)

// NodeNetwork is the private-network state an agent reports in its hello.
type NodeNetwork struct {
	Provider string `json:"provider"` // "tailscale" | ""
	// State: "running" | "not_installed" | "stopped" | "needs_login" | "starting" | "unknown".
	State        string   `json:"state"`
	ControlURL   string   `json:"control_url,omitempty"`   // URL the agent is connected through
	FallbackURLs []string `json:"fallback_urls,omitempty"` // tried when ControlURL fails (e.g. the public URL)
	ViaTailnet   bool     `json:"via_tailnet"`             // ControlURL is a tailnet address
	MeshError    string   `json:"mesh_error,omitempty"`    // last cross-server forwarder problem
}

// NetworkPayload (server -> agent, MsgNetwork). ControlURL is only switched to after the agent
// reached <ControlURL>/agent/v1/ping with its own node token; on failure the old URL stays.
type NetworkPayload struct {
	Provider    string `json:"provider"`
	ControlURL  string `json:"control_url,omitempty"`  // switch to this URL ("" = keep)
	FallbackURL string `json:"fallback_url,omitempty"` // keep as fallback (normally the public URL)
	// AuthKey is only sent for an explicit "join tailnet" action on a node that is not on the
	// tailnet yet: the agent installs Tailscale when missing and runs tailscale up. Never logged.
	AuthKey  string `json:"auth_key,omitempty"`
	Hostname string `json:"hostname,omitempty"` // tailscale --hostname, default atoll-<hostname>
}

// NetworkResult is the Output of MsgNetwork and the body of POST /servers/{id}/network.
type NetworkResult struct {
	Switched   bool   `json:"switched"`
	ControlURL string `json:"control_url"`
	TailnetIP  string `json:"tailnet_ip,omitempty"`
	State      string `json:"state"`
	Message    string `json:"message,omitempty"`
}

// ServerNetworkRequest is the body of POST /api/v1/servers/{id}/network.
type ServerNetworkRequest struct {
	// Action: "join" (install Tailscale if needed, tailscale up with the stored auth key, then
	// switch the agent to the tailnet control URL), "switch" (node already on the tailnet:
	// switch the control URL), "public" (switch the agent back to the public URL).
	Action string `json:"action"`
}

// BootstrapRequest / BootstrapResponse: POST /agent/v1/bootstrap with a (not consumed) join
// token, or GET /agent/v1/network with a node token. `?format=env` answers KEY=value lines
// for install-agent.sh. The auth key is only ever returned here.
type BootstrapRequest struct {
	JoinToken string `json:"join_token"`
}
type BootstrapResponse struct {
	NetworkProvider  string `json:"network_provider"`
	TailscaleAuthKey string `json:"tailscale_auth_key,omitempty"`
	TailnetName      string `json:"tailnet_name,omitempty"`
	ControlURL       string `json:"control_url,omitempty"` // agent endpoint over the tailnet
	PublicURL        string `json:"public_url"`
}

// MeshPort is one forwarded port. On an export HostPort is the port published on the node's
// tailnet IP; on an import Target is "<tailnet_ip>:<host_port>" of the exporting node.
type MeshPort struct {
	Port     int    `json:"port"`     // container port of the service
	Protocol string `json:"protocol"` // "tcp" | "udp"
	HostPort int    `json:"host_port,omitempty"`
	Target   string `json:"target,omitempty"`
}

// MeshExport: a local service other servers of the same project consume. The agent runs a
// forwarder in the project network that listens on <tailnet_ip>:<host_port> only.
type MeshExport struct {
	ServiceID string     `json:"service_id"`
	ProjectID string     `json:"project_id"`
	Network   string     `json:"network"`
	Name      string     `json:"name"` // service DNS name in the project network
	Ports     []MeshPort `json:"ports"`
	// Token is the project's mesh token (32 hex chars). Set: the forwarder strips and checks
	// the importers' handshake; RequireToken: traffic without a valid one is dropped, otherwise
	// plain traffic from importers that do not send it yet still passes. Older agents ignore
	// both. Never logged or returned by the HTTP API.
	Token        string `json:"token,omitempty"`
	RequireToken bool   `json:"require_token,omitempty"`
}

// MeshImport: a service of a local project that runs on another server. The agent runs a
// forwarder in the project network with the service's DNS name (and Aliases) that forwards
// each port to the exporting node over the tailnet.
type MeshImport struct {
	ServiceID string     `json:"service_id"`
	ProjectID string     `json:"project_id"`
	Network   string     `json:"network"`
	Name      string     `json:"name"`
	Aliases   []string   `json:"aliases,omitempty"`
	ServerID  string     `json:"server_id"` // node the service runs on
	Ports     []MeshPort `json:"ports"`
	// Token: send the project's mesh handshake on every connection/datagram to the exporter.
	// Only set once the exporting node runs a forwarder that understands it.
	Token string `json:"token,omitempty"`
}

// MeshPayload is the full mesh of one node (MsgMesh). Empty lists remove every forwarder.
type MeshPayload struct {
	TailnetIP string       `json:"tailnet_ip"` // this node's tailnet IP (exports bind here only)
	Exports   []MeshExport `json:"exports"`
	Imports   []MeshImport `json:"imports"`
}

// ServiceMigrateRequest is the body of POST /api/v1/services/{id}/migrate: move one service
// (container + volumes) to another server; the rest of the project stays and reaches it over
// the tailnet mesh under the same service name.
type ServiceMigrateRequest struct {
	ServerID string `json:"server_id"`
}
