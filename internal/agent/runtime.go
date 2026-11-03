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

// ErrNetworkInUse is returned by RemoveNetwork when containers are still attached.
var ErrNetworkInUse = errors.New("network in use")

// Runtime is the slice of the Docker Engine the agent needs. The real implementation
// (DockerRuntime) talks to dockerd; tests use an in-memory fake.
type Runtime interface {
	Version(ctx context.Context) (string, error)

	EnsureNetwork(ctx context.Context, name string) error
	// RemoveNetwork deletes an atoll-managed network. It returns ErrNetworkInUse (and leaves
	// the network) while any container is attached, ErrNotFound when it does not exist, and
	// refuses networks atoll did not create.
	RemoveNetwork(ctx context.Context, name string) error

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
	// ConnectNetwork attaches an existing (possibly running) container to one more network
	// with the given DNS aliases.
	ConnectNetwork(ctx context.Context, container, network string, aliases []string) error

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
	// ListImages returns the local images of one repository (e.g. "atoll-build/svc_x"), one
	// entry per tag.
	ListImages(ctx context.Context, repo string) ([]ImageSummary, error)
	// RemoveImage removes a local image reference (untag; the image goes with its last tag
	// unless a container uses it). ErrNotFound when absent.
	RemoveImage(ctx context.Context, ref string) error
	// PruneImages removes images no container uses that are older than olderThan, except
	// Atoll's own build images (label atoll.build.service), which the builder keeps for
	// rollback. It returns the bytes reclaimed.
	PruneImages(ctx context.Context, olderThan time.Duration) (uint64, error)
}

// ImageSummary is one tagged local image.
type ImageSummary struct {
	Ref     string // repo:tag
	ID      string
	Created time.Time
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
	// HostIP restricts the published port to one host address ("" = all). Mesh exports bind
	// to the node's tailnet IP only, so they are never reachable from the public interface.
	HostIP string
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
	PidsLimit     int64 // 0 = no limit
	OomScoreAdj   int   // <0 makes the kernel OOM killer pick this container last
	Health        *HealthSpec
}

// DefaultPidsLimit caps the processes of every service container, so a fork bomb or a leaking
// worker pool cannot exhaust the host's PIDs (and take the agent and Caddy with it).
const DefaultPidsLimit = 4096

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
