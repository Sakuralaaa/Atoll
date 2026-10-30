package agent

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ErrNotFound is returned by a Runtime when a container, image or network does not exist.
var ErrNotFound = errors.New("not found")

// Runtime is the slice of the Docker Engine the agent needs. The real implementation
// (DockerRuntime) talks to dockerd; tests use an in-memory fake.
type Runtime interface {
	Version(ctx context.Context) (string, error)

	EnsureNetwork(ctx context.Context, name string) error

	// PullImage pulls ref (with optional registry credentials).
	PullImage(ctx context.Context, ref string, auth *api.RegistryAuth) error
	// ImageInfo returns the local image ID and repo digest of ref, ErrNotFound if absent.
	ImageInfo(ctx context.Context, ref string) (ImageInfo, error)

	// ListContainers returns all containers (running or not) carrying the given label key.
	ListContainers(ctx context.Context, labelKey string) ([]ContainerInfo, error)
	// InspectContainer returns ErrNotFound when no container has that name.
	InspectContainer(ctx context.Context, name string) (ContainerInfo, error)
	CreateContainer(ctx context.Context, spec ContainerSpec) error
	StartContainer(ctx context.Context, name string) error
	StopContainer(ctx context.Context, name string, timeout time.Duration) error
	RestartContainer(ctx context.Context, name string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, name string) error
	RenameContainer(ctx context.Context, oldName, newName string) error

	// Stats returns a one-shot resource sample.
	Stats(ctx context.Context, name string) (ContainerStats, error)
	// Logs writes demultiplexed log lines to fn until ctx ends (follow) or the log is exhausted.
	Logs(ctx context.Context, name string, tail int, follow bool, fn func(LogRecord)) error
	// Exec runs cmd in the container. stdin may be nil. stdout/stderr may be the same writer.
	Exec(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (exitCode int, err error)

	// LoadImage loads a docker/OCI image archive (as from `docker save` or `ctr images export`)
	// and returns the image references it contained.
	LoadImage(ctx context.Context, archive io.Reader) ([]string, error)
	TagImage(ctx context.Context, source, target string) error
}

type ImageInfo struct {
	ID     string // sha256:... local image id
	Digest string // repo digest "sha256:..." when known
}

type NetworkAttach struct {
	Name    string
	Aliases []string
}

type Mount struct {
	Source   string // host path
	Target   string // container path
	ReadOnly bool
}

type PortBinding struct {
	ContainerPort int
	HostPort      int
	Protocol      string // "tcp" | "udp"
}

type HealthSpec struct {
	Test     []string // e.g. ["CMD-SHELL", "..."]
	Interval time.Duration
	Timeout  time.Duration
	Retries  int
	Start    time.Duration
}

// ContainerSpec is the runtime-neutral container definition derived from a ResolvedSpec.
type ContainerSpec struct {
	Name          string
	Image         string
	Entrypoint    []string
	Cmd           []string
	Env           []string // KEY=VALUE, sorted
	Labels        map[string]string
	Networks      []NetworkAttach // first is the primary network
	Mounts        []Mount
	Ports         []PortBinding // host-published ports
	ExposedPorts  []PortBinding // container ports exposed without publishing
	RestartPolicy string        // "unless-stopped"
	NanoCPUs      int64
	MemoryBytes   int64
	Health        *HealthSpec
}

type ContainerInfo struct {
	ID           string
	Name         string
	Image        string // image reference used
	ImageID      string
	Labels       map[string]string
	State        string // running/exited/restarting/created/paused/dead
	Health       string // healthy/unhealthy/starting or ""
	ExitCode     int
	RestartCount int
	StartedAt    time.Time
}

type ContainerStats struct {
	CPUPct  float64
	MemMB   float64
	RxBytes uint64
	TxBytes uint64
}

type LogRecord struct {
	At     time.Time
	Stream string // stdout | stderr
	Line   string
}
