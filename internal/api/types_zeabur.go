package api

import "time"

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
	Args            []string     `json:"args,omitempty"` // added by CLI importer: Zeabur templates often set only args (e.g. redis --requirepass)
	User            string       `json:"user,omitempty"` // container user Zeabur ran it as (docker --user)
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
	// In-place Zeabur takeover additions (zeabur-local status values: rehearsing | rehearsed |
	// cutting_over | live | rolling_back | rolled_back | cleaning | cleaned, plus failed).
	Mode      string         `json:"mode,omitempty"`      // "plan" | "zeabur-local" | "docker"
	ServerID  string         `json:"server_id,omitempty"` // host being taken over (zeabur-local)
	Namespace string         `json:"namespace,omitempty"` // zeabur environment-<id> namespace
	Verify    []ImportVerify `json:"verify,omitempty"`    // row counts from zeabur.db results
	// Docker adoption (mode "docker", types_docker.go): the DockerGroup.Key taken over. Its
	// statuses are those of zeabur-local without cleaning/cleaned.
	Group string `json:"group,omitempty"`
}

// ImportVerify is one table/keyspace row count compared after a zeabur.db dump/restore.
type ImportVerify struct {
	Service    string `json:"service"`
	Table      string `json:"table"`
	SourceRows int64  `json:"source_rows"`
	TargetRows int64  `json:"target_rows"`
}

type ImportStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "pending" | "running" | "done" | "failed" | "skipped"
	Detail string `json:"detail,omitempty"`
}

// ---------- In-place takeover of a user's own VPS that Zeabur manages (primary import path) ----------
// The user's VPS was registered to Zeabur (unmanaged server, single-node k3s). Atoll's agent is
// installed on the SAME host, reads Zeabur's k3s objects locally, and moves each service into
// plain Docker on that host: running image exported from containerd and `docker load`ed (no
// registry needed), PVC dirs copied locally, databases dumped/restored with same-major tools.
// Cutover scales Zeabur workloads to 0 and parks Zeabur's ingress-controller so atoll-caddy can
// take 80/443. Rollback reverses both. Nothing on Zeabur is deleted.

const (
	MsgZeaburScan    = "zeabur.scan"    // no payload; result Output = JSON ZeaburInventory (contains secrets: server stores encrypted, API masks)
	MsgZeaburImage   = "zeabur.image"   // ZeaburImagePayload; result Output = local docker image ref
	MsgZeaburVolume  = "zeabur.volume"  // ZeaburVolumePayload: copy PVC dir -> /var/lib/atoll/volumes/<service_id>/<volume>
	MsgZeaburDB      = "zeabur.db"      // ZeaburDBPayload: dump from k3s pod, restore into atoll container; result Output = JSON row counts
	MsgZeaburScale   = "zeabur.scale"   // ZeaburScalePayload
	MsgZeaburIngress = "zeabur.ingress" // ZeaburIngressPayload: park/restore zeabur ingress-controller DaemonSet
)

type ZeaburInventory struct {
	K3s            bool            `json:"k3s"`
	K3sVersion     string          `json:"k3s_version,omitempty"`
	Projects       []ZeaburProject `json:"projects"`
	IngressRunning bool            `json:"ingress_running"`
	DiskFreeGB     float64         `json:"disk_free_gb"`
	MemFreeMB      int             `json:"mem_free_mb"`
	ScannedAt      time.Time       `json:"scanned_at"`
}

type ZeaburProject struct {
	Namespace       string          `json:"namespace"` // environment-<envID>
	ProjectID       string          `json:"project_id"`
	ProjectName     string          `json:"project_name"`
	EnvironmentName string          `json:"environment_name"`
	Services        []ZeaburService `json:"services"`
	// Warnings are namespace-level findings (orphan PVCs not tied to any service, workloads
	// that are not Zeabur services). Never contain secret values.
	Warnings []string `json:"warnings,omitempty"`
}

type ZeaburService struct {
	ServiceID      string   `json:"service_id"`
	Name           string   `json:"name"`     // dnsName (app.kubernetes.io/name)
	Workload       string   `json:"workload"` // "deployment/service-<id>" | "statefulset/..." | "" when suspended (PVC only)
	Replicas       int      `json:"replicas"`
	Role           string   `json:"role"` // "app" | "database" | "cache" | "other" (guessed from image)
	Kind           string   `json:"kind"` // Atoll kind guess: image|postgres|mysql|redis|mongo
	Image          string   `json:"image"`
	ImageID        string   `json:"image_id"`        // running digest
	ZeaburRegistry bool     `json:"zeabur_registry"` // built on Zeabur: must export from containerd
	ImageCached    bool     `json:"image_cached"`
	Command        []string `json:"command,omitempty"`
	Args           []string `json:"args,omitempty"`
	// User is the pod's effective securityContext runAsUser[:runAsGroup] ("" = image default).
	// Zeabur runs most pods as root (runAsUser 0), whatever USER the image declares.
	User      string         `json:"user,omitempty"`
	Ports     []Port         `json:"ports"`
	NodePorts []int          `json:"node_ports,omitempty"`
	Env       []EnvVar       `json:"env"` // effective expanded values from pod spec
	Volumes   []ZeaburVolume `json:"volumes"`
	Configs   []ConfigFile   `json:"configs,omitempty"`
	Domains   []string       `json:"domains"` // from Ingress objects (custom + *.zeabur.app)
	// MemUsageMB is the pods' current memory use (kubectl top; 0 when metrics are unavailable).
	MemUsageMB int      `json:"mem_usage_mb,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	// Status is the scan-time health: running|progressing|degraded|failed|suspended|unknown.
	Status string `json:"status,omitempty"`
	// DataStore is the data-store family detected from the image name (or StatefulSet with a
	// persistent volume): postgres|mysql|redis|mongo|queue|search|other; "" for apps.
	DataStore string `json:"data_store,omitempty"`
	// K8sServices are the Kubernetes Services attributed to this workload (selector match, or
	// service-<id> by name); env values may name them as hosts.
	K8sServices []string `json:"k8s_services,omitempty"`
	// References are connections to other services found in this service's env.
	References []ZeaburReference `json:"references,omitempty"`
}

// ZeaburReference is one env-level connection from a service to another Zeabur service.
type ZeaburReference struct {
	Key       string `json:"key,omitempty"`        // env var of this service ("" = whole object shared)
	Target    string `json:"target"`               // zeabur service id referenced
	TargetKey string `json:"target_key,omitempty"` // env var of Target holding the same Secret/ConfigMap key
	Via       string `json:"via"`                  // env-value | zeabur-internal | shared-secret | shared-configmap
	Object    string `json:"object,omitempty"`     // "Secret/<name>", "ConfigMap/<name>" or the k8s Service / host named
	Inferred  bool   `json:"inferred"`             // true when read from text
}

type ZeaburVolume struct {
	PVC       string `json:"pvc"`
	MountPath string `json:"mount_path"`
	HostDir   string `json:"host_dir"` // /var/lib/rancher/k3s/storage/<pv>_<ns>_<pvc>
	Bytes     int64  `json:"bytes"`
}

type ZeaburImagePayload struct {
	Image     string `json:"image"`
	ServiceID string `json:"service_id"` // atoll service id, used for local tag atoll-import/<id>:<digest12>
	// Pull: the image comes from a public registry (Docker Hub, GHCR...). The agent pulls the
	// original ref (tag kept) and only falls back to exporting it from k3s' containerd, tagged
	// locally with that same ref, when the pull fails. Output is then the original ref.
	Pull bool `json:"pull,omitempty"`
}
type ZeaburVolumePayload struct {
	HostDir   string `json:"host_dir"`
	ServiceID string `json:"service_id"`
	Volume    string `json:"volume"`
}
type ZeaburDBPayload struct {
	Namespace       string `json:"namespace"`
	SourceServiceID string `json:"source_service_id"` // zeabur service id (pod label zeabur_service_id)
	Kind            string `json:"kind"`              // postgres|mysql|mongo|redis
	TargetServiceID string `json:"target_service_id"` // atoll service id
}
type ZeaburScalePayload struct {
	Namespace string `json:"namespace"`
	Workload  string `json:"workload"`
	Replicas  int    `json:"replicas"`
}
type ZeaburIngressPayload struct {
	Park bool `json:"park"` // true: stop zeabur ingress-controller (record original), false: restore
	// The ingress is shared by every Zeabur project on the node. Namespaces lists the ones
	// whose sites move to Atoll with this park (the cutover's jobs plus jobs already live).
	// The agent refuses to park while any other environment-* namespace still runs workloads
	// that have Ingress hosts, unless Force is set. Operators (control-plane=controller-manager,
	// cluster-wide) are scaled to 0 only when no other namespace runs workloads at all.
	Namespaces []string `json:"namespaces,omitempty"`
	Force      bool     `json:"force,omitempty"`
}

// ZeaburNodeCutoverRequest is the body of POST /api/v1/servers/{id}/zeabur/cutover: cut over
// several zeabur-local import jobs of that server together (one ingress park for all of them).
type ZeaburNodeCutoverRequest struct {
	ImportIDs []string `json:"import_ids"`
	// Force parks the shared ingress even though other Zeabur projects on the server still
	// serve hostnames through it (they go offline).
	Force bool `json:"force,omitempty"`
}

// ZeaburCutoverRequest is the optional body of POST /api/v1/imports/{id}/cutover.
type ZeaburCutoverRequest struct {
	Force bool `json:"force,omitempty"`
}

// ZeaburRehearseRequest is the optional body of POST /api/v1/imports/{id}/rehearse.
type ZeaburRehearseRequest struct {
	// IgnoreMemory skips the free-memory check (409 insufficient_memory).
	IgnoreMemory bool `json:"ignore_memory,omitempty"`
}

// ZeaburNodeOp is the answer of the node-level cutover / rollback: one entry per job, each
// with its own status and steps (follow them with GET /imports/{id}).
type ZeaburNodeOp struct {
	ServerID string      `json:"server_id"`
	Jobs     []ImportJob `json:"jobs"`
}

// REST additions (see docs/api.md "In-place Zeabur takeover"):
//
//	GET  /api/v1/servers/{id}/zeabur          -> ZeaburInventory (env values masked)
//	POST /api/v1/imports/zeabur-local          ZeaburLocalImportRequest -> ImportJob
//	POST /api/v1/imports/{id}/rehearse         start atoll copies without public ingress, copy data, verify
//	POST /api/v1/imports/{id}/cutover          scale zeabur apps to 0, final data sync, park zeabur ingress, enable atoll ingress
//	POST /api/v1/imports/{id}/rollback         reverse cutover (409 node_rollback_required while other jobs on the node are cut over)
//	POST /api/v1/servers/{id}/zeabur/cutover   ZeaburNodeCutoverRequest -> ZeaburNodeOp (several jobs, one ingress park)
//	POST /api/v1/servers/{id}/zeabur/rollback  roll back every cut-over job of the server -> ZeaburNodeOp
type ZeaburLocalImportRequest struct {
	ServerID    string   `json:"server_id"`
	Namespace   string   `json:"namespace"`
	ServiceIDs  []string `json:"service_ids"` // zeabur service ids; empty = all in namespace
	ProjectName string   `json:"project_name"`
	// DomainMap overrides the proposed replacement for each *.zeabur.app hostname
	// (default "<same-prefix>.<settings.base_domain>"). Map a host to "" to drop it.
	DomainMap map[string]string `json:"domain_map,omitempty"`
}

// ZeaburCleanupRequest is the body of POST /api/v1/imports/{id}/cleanup (session only).
// Confirm must equal the server's name exactly.
type ZeaburCleanupRequest struct {
	Confirm    string `json:"confirm"`
	KeepBackup bool   `json:"keep_backup"`
}

// MsgZeaburUninstall removes Zeabur's k3s from the host after a completed cutover
// (runs /usr/local/bin/k3s-uninstall.sh). Result Output = uninstall log + verification summary.
const MsgZeaburUninstall = "zeabur.uninstall"

type ZeaburUninstallPayload struct {
	KeepBackup bool `json:"keep_backup"` // tar.gz every PVC dir to /var/lib/atoll/zeabur-backup/<pvc>.tgz first
}
