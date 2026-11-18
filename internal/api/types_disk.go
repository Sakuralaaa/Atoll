package api

import "time"

// Disk usage of services (v0.1.48). Agents with HelloPayload.DiskUsage measure each service's
// volume dirs and its container's writable layer every DiskUsageEvery and send MsgDiskUsage;
// the control server also asks for a fresh measurement (MsgDiskMeasure) of services that are
// near a volume limit. Limits are soft: the control server compares the measurements with
// them and alerts or stops the service, the filesystem does not enforce anything.

const (
	MsgDiskUsage   = "disk.usage"   // agent→server DiskUsagePayload, every DiskUsageEvery
	MsgDiskMeasure = "disk.measure" // server→agent DiskMeasurePayload; result Output = JSON DiskUsagePayload
)

// DiskUsageEvery is how often agents measure all services.
const DiskUsageEvery = 10 * time.Minute

// Volume over-limit actions.
const (
	OverLimitStop  = "stop"  // stop the service (default)
	OverLimitAlert = "alert" // only raise the volume_full problem
)

// VolumeUsage is the size of one volume dir on the agent (allocated blocks, hard links once).
type VolumeUsage struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// ServiceDiskSample is one service's measurement. Volumes lists every dir under the service's
// volumes dir, including ones no longer mounted; host-path volumes are not measured.
type ServiceDiskSample struct {
	ServiceID string        `json:"service_id"`
	Volumes   []VolumeUsage `json:"volumes"`
	// RootfsBytes is the container's writable layer (files written outside volumes); -1 when
	// there is no container or Docker did not say.
	RootfsBytes int64 `json:"rootfs_bytes"`
}

type DiskUsagePayload struct {
	At       time.Time           `json:"at"`
	Services []ServiceDiskSample `json:"services"`
}

// DiskMeasurePayload asks for a measurement now; empty ServiceIDs = every service.
type DiskMeasurePayload struct {
	ServiceIDs []string `json:"service_ids,omitempty"`
}

// ServiceDisk is GET /services/{id}/disk.
type ServiceDisk struct {
	// Supported: the service's server runs an agent that reports disk usage.
	Supported  bool       `json:"supported"`
	MeasuredAt *time.Time `json:"measured_at,omitzero"`
	// TotalBytes = mounted volumes + writable layer (unmounted volume data not included).
	TotalBytes  int64        `json:"total_bytes"`
	RootfsBytes int64        `json:"rootfs_bytes"` // -1 unknown
	Volumes     []VolumeDisk `json:"volumes"`
	History     []DiskPoint  `json:"history,omitempty"`
}

// VolumeDisk is one volume of ServiceDisk.
type VolumeDisk struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path,omitempty"`
	Bytes     int64  `json:"bytes"`
	Measured  bool   `json:"measured"`
	LimitMB   int    `json:"limit_mb,omitempty"`
	OverLimit string `json:"over_limit,omitempty"` // stop | alert (set with LimitMB)
	// HostPath: an adopted host directory; Atoll does not measure or limit it.
	HostPath bool `json:"host_path,omitempty"`
	// Orphan: data of a volume no longer mounted, still on the server.
	Orphan bool `json:"orphan,omitempty"`
}

type DiskPoint struct {
	At    time.Time `json:"at"`
	Bytes int64     `json:"bytes"`
}

// VolumeLimit is PUT /services/{id}/volumes/{name}/limit. LimitMB 0 removes the limit.
type VolumeLimit struct {
	LimitMB   int    `json:"limit_mb"`
	OverLimit string `json:"over_limit,omitempty"` // default stop
}

// VolumeWarnRatio is the share of a limit at which volume_full fires.
const VolumeWarnRatio = 0.8
