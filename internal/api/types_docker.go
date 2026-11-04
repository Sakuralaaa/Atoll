package api

import "time"

// ---------- Docker adoption (docs/adopt-docker.md) ----------
// Containers the user started on a worker with `docker run` or docker compose are taken over
// in place: the agent scans them (docker.scan), the server creates one Atoll project per group
// (compose project, or a single standalone container), rehearses (Atoll copies started next to
// the old containers without published ports and with scratch data dirs), cuts over (old
// containers stopped with restart policy "no", Atoll copies published on the same host ports
// with the original bind mounts and named volumes mounted in place via Volume.HostPath) and
// rolls back on request or on failure (old containers started again). Old containers and their
// volumes are never removed.

const (
	MsgDockerScan       = "docker.scan"       // no payload; result Output = JSON DockerInventory (contains env values: server stores encrypted, API masks)
	MsgDockerImage      = "docker.image"      // DockerImagePayload; result Output = JSON DockerImageResult
	MsgDockerContainers = "docker.containers" // DockerContainersPayload: stop/start the user's own containers; result Output = JSON DockerContainersResult
)

// Container risk tiers.
const (
	DockerRiskAuto   = "auto"   // can be adopted automatically
	DockerRiskManual = "manual" // needs a human: RiskReasons say why; never adopted automatically
)

type DockerInventory struct {
	DockerVersion string        `json:"docker_version,omitempty"`
	Groups        []DockerGroup `json:"groups"`
	DiskFreeGB    float64       `json:"disk_free_gb"`
	MemFreeMB     int           `json:"mem_free_mb"`
	ScannedAt     time.Time     `json:"scanned_at"`
}

// DockerGroup is one compose project (label com.docker.compose.project) or one standalone
// container. A group is adopted into one Atoll project.
type DockerGroup struct {
	Key         string            `json:"key"`  // "compose:<project>" | "container:<name>"
	Kind        string            `json:"kind"` // "compose" | "standalone"
	Name        string            `json:"name"` // compose project name or container name
	WorkingDir  string            `json:"working_dir,omitempty"`
	ConfigFiles string            `json:"config_files,omitempty"`
	Containers  []DockerContainer `json:"containers"`
	// Adoptable: at least one container has risk "auto".
	Adoptable bool     `json:"adoptable"`
	Warnings  []string `json:"warnings,omitempty"`
}

type DockerContainer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service,omitempty"` // compose service name
	Image   string `json:"image"`             // as configured (e.g. nginx:1.27)
	ImageID string `json:"image_id"`          // sha256:… of the image the container runs
	// PinnedImage is repo@sha256:digest of the running image when the registry digest is
	// known; "" for images built or loaded locally (adopted under a local tag instead).
	PinnedImage   string        `json:"pinned_image,omitempty"`
	State         string        `json:"state"` // running|exited|created|paused|restarting|dead
	Env           []EnvVar      `json:"env"`   // container env minus what the image sets itself
	Command       []string      `json:"command,omitempty"`
	Args          []string      `json:"args,omitempty"`
	User          string        `json:"user,omitempty"`
	Ports         []DockerPort  `json:"ports"`
	Mounts        []DockerMount `json:"mounts"`
	RestartPolicy string        `json:"restart_policy"`
	Networks      []string      `json:"networks,omitempty"`
	NetworkMode   string        `json:"network_mode,omitempty"`
	Health        *HealthCheck  `json:"health,omitempty"` // container-level healthcheck (compose healthcheck:)
	MemLimitMB    int           `json:"mem_limit_mb,omitempty"`
	CPULimit      float64       `json:"cpu_limit,omitempty"`
	MemUsageMB    int           `json:"mem_usage_mb,omitempty"`
	Risk          string        `json:"risk"` // auto | manual
	RiskReasons   []string      `json:"risk_reasons,omitempty"`
	Warnings      []string      `json:"warnings,omitempty"`
}

type DockerPort struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port,omitempty"` // 0 = exposed only
	Protocol      string `json:"protocol"`            // tcp | udp
	HostIP        string `json:"host_ip,omitempty"`   // "" = all interfaces
}

type DockerMount struct {
	Type        string `json:"type"`           // bind | volume | tmpfs
	Name        string `json:"name,omitempty"` // named volume
	Source      string `json:"source"`         // host path (bind source or the volume's mountpoint)
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"read_only,omitempty"`
	IsFile      bool   `json:"is_file,omitempty"` // the host source is a regular file
	Driver      string `json:"driver,omitempty"`  // volume driver ("local")
}

type DockerImagePayload struct {
	ServiceID string `json:"service_id"` // atoll service id (local tag atoll-adopt/<id>:<image id 12>)
	ImageID   string `json:"image_id"`   // sha256:… the old container runs
	Pinned    string `json:"pinned,omitempty"`
}

// DockerImageResult: Ref is the image the Atoll copy runs. Local is true when it is a local
// tag (atoll-adopt/…) that must never be pulled.
type DockerImageResult struct {
	Ref   string `json:"ref"`
	Local bool   `json:"local"`
}

type DockerContainersPayload struct {
	Action     string               `json:"action"` // "stop" | "start"
	Containers []DockerContainerCtl `json:"containers"`
}

// DockerContainerCtl names one of the user's containers. Stop sets the restart policy to "no"
// (so a reboot cannot start it next to Atoll's copy) and stops it; start restores
// RestartPolicy and starts it when Start is true.
type DockerContainerCtl struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	RestartPolicy string `json:"restart_policy,omitempty"` // start: policy to restore
	Start         bool   `json:"start,omitempty"`          // start: start it (it was running before)
}

type DockerContainersResult struct {
	Containers []DockerContainerState `json:"containers"`
}

type DockerContainerState struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	State         string `json:"state"`
	RestartPolicy string `json:"restart_policy"` // stop: the policy it had before
	WasRunning    bool   `json:"was_running"`
}

// DockerAdoptRequest is the body of POST /api/v1/imports/docker.
type DockerAdoptRequest struct {
	ServerID string `json:"server_id"`
	Group    string `json:"group"` // DockerGroup.Key
	// Containers limits the adoption to these container names (default: every "auto" one).
	Containers  []string `json:"containers,omitempty"`
	ProjectName string   `json:"project_name,omitempty"`
}

// REST additions (docs/api.md "Docker adoption"):
//
//	GET  /api/v1/servers/{id}/docker        -> DockerInventory (env values masked)
//	POST /api/v1/imports/docker              DockerAdoptRequest -> ImportJob (mode "docker")
//	POST /api/v1/imports/{id}/rehearse       start Atoll copies next to the old containers (no ports, scratch data)
//	POST /api/v1/imports/{id}/cutover        stop old containers, Atoll copies take their ports and data
//	POST /api/v1/imports/{id}/rollback       stop Atoll copies, start the old containers again
