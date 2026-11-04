package dockeradopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

// DockerEngine implements Engine with the Docker Engine API.
type DockerEngine struct{ cli *client.Client }

// NewDockerEngine connects like the agent's runtime (DOCKER_HOST, default unix socket).
func NewDockerEngine() (*DockerEngine, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &DockerEngine{cli: cli}, nil
}

func (d *DockerEngine) Close() error { return d.cli.Close() }

func (d *DockerEngine) Version(ctx context.Context) (string, error) {
	v, err := d.cli.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

func (d *DockerEngine) Containers(ctx context.Context) ([]Container, error) {
	list, err := d.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(list))
	for _, c := range list {
		ci, err := d.Container(ctx, c.ID)
		if err != nil {
			continue // removed meanwhile
		}
		out = append(out, ci)
	}
	return out, nil
}

func health(h *container.HealthConfig) *Health {
	if h == nil || len(h.Test) == 0 {
		return nil
	}
	return &Health{Test: append([]string{}, h.Test...), Interval: h.Interval}
}

func policyString(p container.RestartPolicy) string {
	name := string(p.Name)
	if name == "" {
		name = "no"
	}
	if name == "on-failure" && p.MaximumRetryCount > 0 {
		return fmt.Sprintf("on-failure:%d", p.MaximumRetryCount)
	}
	return name
}

func (d *DockerEngine) Container(ctx context.Context, id string) (Container, error) {
	ins, err := d.cli.ContainerInspect(ctx, id)
	if err != nil {
		return Container{}, err
	}
	c := Container{ID: ins.ID, Name: strings.TrimPrefix(ins.Name, "/"), ImageID: ins.Image, Bindings: map[string][]Binding{}}
	if ins.State != nil {
		c.State = ins.State.Status
	}
	if cfg := ins.Config; cfg != nil {
		c.Image, c.Labels, c.Env, c.User, c.WorkingDir = cfg.Image, cfg.Labels, cfg.Env, cfg.User, cfg.WorkingDir
		c.Entrypoint, c.Cmd = cfg.Entrypoint, cfg.Cmd
		c.Health = health(cfg.Healthcheck)
		for p := range cfg.ExposedPorts {
			c.Exposed = append(c.Exposed, string(p))
		}
	}
	if c.Labels == nil {
		c.Labels = map[string]string{}
	}
	if h := ins.HostConfig; h != nil {
		for p, bs := range h.PortBindings {
			for _, b := range bs {
				c.Bindings[string(p)] = append(c.Bindings[string(p)], Binding{HostIP: b.HostIP, HostPort: b.HostPort})
			}
		}
		c.RestartPolicy = policyString(h.RestartPolicy)
		c.NetworkMode = string(h.NetworkMode)
		c.Privileged = h.Privileged
		c.CapAdd = h.CapAdd
		for _, dv := range h.Devices {
			c.Devices = append(c.Devices, dv.PathOnHost)
		}
		c.DeviceRequests = len(h.DeviceRequests)
		c.SecurityOpt = h.SecurityOpt
		c.PidMode, c.IpcMode, c.UTSMode, c.UsernsMode = string(h.PidMode), string(h.IpcMode), string(h.UTSMode), string(h.UsernsMode)
		c.Runtime = h.Runtime
		c.Sysctls, c.ExtraHosts, c.DNS = h.Sysctls, h.ExtraHosts, h.DNS
		for p := range h.Tmpfs {
			c.Tmpfs = append(c.Tmpfs, p)
		}
		c.LogDriver = h.LogConfig.Type
		c.MemoryBytes, c.NanoCPUs = h.Memory, h.NanoCPUs
		c.ReadonlyRootfs, c.ShmSizeBytes, c.Ulimits = h.ReadonlyRootfs, h.ShmSize, len(h.Ulimits)
		c.Init = h.Init != nil && *h.Init
		c.VolumesFrom, c.Links = h.VolumesFrom, h.Links
	}
	if ns := ins.NetworkSettings; ns != nil {
		for n := range ns.Networks {
			c.Networks = append(c.Networks, n)
		}
	}
	for _, m := range ins.Mounts {
		c.Mounts = append(c.Mounts, Mount{Type: string(m.Type), Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW, Driver: m.Driver})
	}
	return c, nil
}

func (d *DockerEngine) Image(ctx context.Context, ref string) (Image, error) {
	ins, err := d.cli.ImageInspect(ctx, ref)
	if err != nil {
		return Image{}, err
	}
	im := Image{ID: ins.ID, RepoTags: ins.RepoTags, RepoDigests: ins.RepoDigests}
	if cfg := ins.Config; cfg != nil {
		im.Env, im.Entrypoint, im.Cmd, im.User, im.WorkingDir = cfg.Env, cfg.Entrypoint, cfg.Cmd, cfg.User, cfg.WorkingDir
		if cfg.Healthcheck != nil && len(cfg.Healthcheck.Test) > 0 {
			im.Health = &Health{Test: cfg.Healthcheck.Test, Interval: cfg.Healthcheck.Interval}
		}
	}
	return im, nil
}

func (d *DockerEngine) VolumeDriver(ctx context.Context, name string) (string, error) {
	v, err := d.cli.VolumeInspect(ctx, name)
	if err != nil {
		return "", err
	}
	return v.Driver, nil
}

func (d *DockerEngine) MemUsageMB(ctx context.Context, id string) (float64, error) {
	r, err := d.cli.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return 0, err
	}
	defer r.Body.Close()
	var s container.StatsResponse
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		return 0, err
	}
	m := float64(s.MemoryStats.Usage)
	if v, ok := s.MemoryStats.Stats["inactive_file"]; ok && float64(v) < m {
		m -= float64(v)
	} else if v, ok := s.MemoryStats.Stats["total_inactive_file"]; ok && float64(v) < m {
		m -= float64(v)
	}
	return m / (1 << 20), nil
}

func (d *DockerEngine) Pull(ctx context.Context, ref string) error {
	rc, err := d.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()
	dec := json.NewDecoder(rc)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
	}
}

func (d *DockerEngine) Tag(ctx context.Context, source, target string) error {
	return d.cli.ImageTag(ctx, source, target)
}

func (d *DockerEngine) Stop(ctx context.Context, id string, timeout time.Duration) error {
	s := int(timeout / time.Second)
	return d.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &s})
}

func (d *DockerEngine) Start(ctx context.Context, id string) error {
	return d.cli.ContainerStart(ctx, id, container.StartOptions{})
}

// ParsePolicy parses "no", "always", "unless-stopped", "on-failure" or "on-failure:N".
func ParsePolicy(p string) (container.RestartPolicy, error) {
	name, n, _ := strings.Cut(p, ":")
	rp := container.RestartPolicy{Name: container.RestartPolicyMode(name)}
	switch name {
	case "no", "always", "unless-stopped":
		if n != "" {
			return rp, fmt.Errorf("restart policy %q takes no retry count", p)
		}
	case "on-failure":
		if n != "" {
			c, err := strconv.Atoi(n)
			if err != nil || c < 0 {
				return rp, fmt.Errorf("invalid restart policy %q", p)
			}
			rp.MaximumRetryCount = c
		}
	default:
		return rp, fmt.Errorf("invalid restart policy %q", p)
	}
	return rp, nil
}

func (d *DockerEngine) SetRestartPolicy(ctx context.Context, id, policy string) error {
	rp, err := ParsePolicy(policy)
	if err != nil {
		return err
	}
	_, err = d.cli.ContainerUpdate(ctx, id, container.UpdateConfig{RestartPolicy: rp})
	return err
}

var _ Engine = (*DockerEngine)(nil)
