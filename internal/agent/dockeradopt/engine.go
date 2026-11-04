// Package dockeradopt implements the agent side of Docker adoption (docs/adopt-docker.md):
// scanning the containers the user started with `docker run` or docker compose, preparing
// their images for Atoll, and stopping / starting them for cutover and rollback. It never
// removes a container, an image or a volume.
package dockeradopt

import (
	"context"
	"time"
)

// Engine is the slice of the Docker Engine adoption needs. DockerEngine talks to dockerd;
// tests use a fake.
type Engine interface {
	Version(ctx context.Context) (string, error)
	// Containers returns every container (running or not) with its full configuration.
	Containers(ctx context.Context) ([]Container, error)
	// Container inspects one container by ID or name.
	Container(ctx context.Context, id string) (Container, error)
	// Image inspects a local image (by ID or reference).
	Image(ctx context.Context, ref string) (Image, error)
	// VolumeDriver returns the driver of a named volume ("local", …).
	VolumeDriver(ctx context.Context, name string) (string, error)
	// MemUsageMB is a one-shot memory reading of a running container.
	MemUsageMB(ctx context.Context, id string) (float64, error)
	Pull(ctx context.Context, ref string) error
	Tag(ctx context.Context, source, target string) error
	Stop(ctx context.Context, id string, timeout time.Duration) error
	Start(ctx context.Context, id string) error
	// SetRestartPolicy changes a container's restart policy ("no", "always",
	// "unless-stopped", "on-failure" or "on-failure:N") without recreating it.
	SetRestartPolicy(ctx context.Context, id, policy string) error
}

// Container is the engine-neutral configuration of one container.
type Container struct {
	ID         string
	Name       string
	Image      string // configured reference
	ImageID    string // sha256:…
	State      string
	Labels     map[string]string
	Env        []string // KEY=VALUE
	Entrypoint []string
	Cmd        []string
	User       string
	WorkingDir string
	Health     *Health
	// Bindings: published ports ("80/tcp" -> host bindings); Exposed: every exposed port.
	Bindings       map[string][]Binding
	Exposed        []string // "80/tcp"
	Mounts         []Mount
	RestartPolicy  string // "no" | "always" | "unless-stopped" | "on-failure[:N]"
	NetworkMode    string
	Networks       []string
	Privileged     bool
	CapAdd         []string
	Devices        []string
	DeviceRequests int
	SecurityOpt    []string
	PidMode        string
	IpcMode        string
	UTSMode        string
	UsernsMode     string
	Runtime        string
	Sysctls        map[string]string
	ExtraHosts     []string
	DNS            []string
	Tmpfs          []string // container paths
	LogDriver      string
	MemoryBytes    int64
	NanoCPUs       int64
	ReadonlyRootfs bool
	ShmSizeBytes   int64
	Ulimits        int
	Init           bool
	VolumesFrom    []string
	Links          []string
}

type Binding struct {
	HostIP   string
	HostPort string
}

type Mount struct {
	Type        string // bind | volume | tmpfs | npipe | cluster
	Name        string
	Source      string
	Destination string
	RW          bool
	Driver      string
}

type Health struct {
	Test     []string
	Interval time.Duration
}

// Image is the part of an image's configuration the scan compares a container against.
type Image struct {
	ID          string
	RepoTags    []string
	RepoDigests []string // repo@sha256:…
	Env         []string
	Entrypoint  []string
	Cmd         []string
	User        string
	WorkingDir  string
	Health      *Health
}

// compose labels
const (
	labelComposeProject = "com.docker.compose.project"
	labelComposeService = "com.docker.compose.service"
	labelComposeOneoff  = "com.docker.compose.oneoff"
	labelComposeDir     = "com.docker.compose.project.working_dir"
	labelComposeFiles   = "com.docker.compose.project.config_files"
)
