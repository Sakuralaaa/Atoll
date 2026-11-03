package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

// DockerRuntime implements Runtime with the Docker Engine API.
type DockerRuntime struct {
	cli *client.Client
}

// NewDockerRuntime connects using DOCKER_HOST etc. (default unix:///var/run/docker.sock).
func NewDockerRuntime() (*DockerRuntime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &DockerRuntime{cli: cli}, nil
}

func (d *DockerRuntime) Close() error { return d.cli.Close() }

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if cerrdefs.IsNotFound(err) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	return err
}

func (d *DockerRuntime) Version(ctx context.Context) (string, error) {
	v, err := d.cli.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

func (d *DockerRuntime) EnsureNetwork(ctx context.Context, name string) error {
	_, err := d.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return err
	}
	_, err = d.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{LabelManaged: "true"},
	})
	if err != nil && cerrdefs.IsConflict(err) {
		return nil // created concurrently
	}
	return err
}

func (d *DockerRuntime) RemoveNetwork(ctx context.Context, name string) error {
	ins, err := d.cli.NetworkInspect(ctx, name, network.InspectOptions{})
	if err != nil {
		return mapErr(err)
	}
	if ins.Labels[LabelManaged] != "true" {
		return fmt.Errorf("network %s is not managed by atoll", name)
	}
	if len(ins.Containers) > 0 {
		return fmt.Errorf("%w: %d container(s) attached", ErrNetworkInUse, len(ins.Containers))
	}
	err = d.cli.NetworkRemove(ctx, ins.ID)
	switch {
	case err == nil:
		return nil
	case cerrdefs.IsPermissionDenied(err) || cerrdefs.IsConflict(err):
		// "has active endpoints": a container attached between inspect and remove.
		return fmt.Errorf("%w: %v", ErrNetworkInUse, err)
	}
	return mapErr(err)
}

func (d *DockerRuntime) PullImage(ctx context.Context, ref string, auth *api.RegistryAuth) error {
	opts := image.PullOptions{}
	if auth != nil && auth.Username != "" {
		enc, err := registry.EncodeAuthConfig(registry.AuthConfig{
			Username:      auth.Username,
			Password:      auth.Password,
			ServerAddress: auth.Server,
		})
		if err != nil {
			return err
		}
		opts.RegistryAuth = enc
	}
	rc, err := d.cli.ImagePull(ctx, ref, opts)
	if err != nil {
		return mapErr(err)
	}
	defer rc.Close()
	// The pull only completes when the progress stream is drained; surface stream errors.
	dec := json.NewDecoder(rc)
	for {
		var msg struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
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

func (d *DockerRuntime) ImageInfo(ctx context.Context, ref string) (ImageInfo, error) {
	ins, err := d.cli.ImageInspect(ctx, ref)
	if err != nil {
		return ImageInfo{}, mapErr(err)
	}
	info := ImageInfo{ID: ins.ID}
	for _, rd := range ins.RepoDigests {
		if i := strings.Index(rd, "@"); i >= 0 {
			info.Digest = rd[i+1:]
			break
		}
	}
	return info, nil
}

func (d *DockerRuntime) ListContainers(ctx context.Context, labelKey string) ([]ContainerInfo, error) {
	list, err := d.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelKey)),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ContainerInfo, 0, len(list))
	for _, c := range list {
		name := c.ID
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		info, err := d.InspectContainer(ctx, name)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

func (d *DockerRuntime) InspectContainer(ctx context.Context, name string) (ContainerInfo, error) {
	ins, err := d.cli.ContainerInspect(ctx, name)
	if err != nil {
		return ContainerInfo{}, mapErr(err)
	}
	info := ContainerInfo{
		ID:           ins.ID,
		Name:         strings.TrimPrefix(ins.Name, "/"),
		ImageID:      ins.Image,
		RestartCount: ins.RestartCount,
	}
	if ins.Config != nil {
		info.Image = ins.Config.Image
		info.Labels = ins.Config.Labels
	}
	if ins.State != nil {
		info.State = ins.State.Status
		info.ExitCode = ins.State.ExitCode
		if ins.State.Health != nil {
			info.Health = ins.State.Health.Status
		}
		if t, err := time.Parse(time.RFC3339Nano, ins.State.StartedAt); err == nil && t.Year() > 1 {
			info.StartedAt = t.UTC()
		}
	}
	return info, nil
}

func (d *DockerRuntime) CreateContainer(ctx context.Context, s ContainerSpec) error {
	cfg := &container.Config{
		Image:        s.Image,
		Env:          s.Env,
		Labels:       s.Labels,
		ExposedPorts: nat.PortSet{},
	}
	if len(s.Entrypoint) > 0 {
		cfg.Entrypoint = s.Entrypoint
	}
	if len(s.Cmd) > 0 {
		cfg.Cmd = s.Cmd
	}
	if s.Health != nil {
		cfg.Healthcheck = &container.HealthConfig{
			Test:        s.Health.Test,
			Interval:    s.Health.Interval,
			Timeout:     s.Health.Timeout,
			Retries:     s.Health.Retries,
			StartPeriod: s.Health.Start,
		}
	}
	host := &container.HostConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyMode(s.RestartPolicy)},
		PortBindings:  nat.PortMap{},
		LogConfig: container.LogConfig{
			Type:   "json-file",
			Config: map[string]string{"max-size": "20m", "max-file": "5"},
		},
	}
	host.NanoCPUs = s.NanoCPUs
	host.Memory = s.MemoryBytes
	for _, p := range s.ExposedPorts {
		cfg.ExposedPorts[nat.Port(fmt.Sprintf("%d/%s", p.ContainerPort, protoOr(p.Protocol)))] = struct{}{}
	}
	for _, p := range s.Ports {
		port := nat.Port(fmt.Sprintf("%d/%s", p.ContainerPort, protoOr(p.Protocol)))
		cfg.ExposedPorts[port] = struct{}{}
		host.PortBindings[port] = append(host.PortBindings[port], nat.PortBinding{HostIP: p.HostIP, HostPort: strconv.Itoa(p.HostPort)})
	}
	for _, m := range s.Mounts {
		host.Mounts = append(host.Mounts, mount.Mount{
			Type: mount.TypeBind, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly,
		})
	}
	var netCfg *network.NetworkingConfig
	if len(s.Networks) > 0 {
		primary := s.Networks[0]
		host.NetworkMode = container.NetworkMode(primary.Name)
		netCfg = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			primary.Name: {Aliases: primary.Aliases},
		}}
	}
	if _, err := d.cli.ContainerCreate(ctx, cfg, host, netCfg, nil, s.Name); err != nil {
		return mapErr(err)
	}
	// Extra networks are connected one by one: older daemons accept a single endpoint at create.
	for _, n := range s.Networks[min(1, len(s.Networks)):] {
		if err := d.cli.NetworkConnect(ctx, n.Name, s.Name, &network.EndpointSettings{Aliases: n.Aliases}); err != nil {
			_ = d.cli.ContainerRemove(context.Background(), s.Name, container.RemoveOptions{Force: true})
			return fmt.Errorf("connect %s to %s: %w", s.Name, n.Name, err)
		}
	}
	return nil
}

func protoOr(p string) string {
	if p == "udp" {
		return "udp"
	}
	return "tcp"
}

func secs(t time.Duration) *int {
	s := int(t / time.Second)
	return &s
}

func (d *DockerRuntime) StartContainer(ctx context.Context, name string) error {
	return mapErr(d.cli.ContainerStart(ctx, name, container.StartOptions{}))
}

func (d *DockerRuntime) StopContainer(ctx context.Context, name string, timeout time.Duration) error {
	return mapErr(d.cli.ContainerStop(ctx, name, container.StopOptions{Timeout: secs(timeout)}))
}

func (d *DockerRuntime) RestartContainer(ctx context.Context, name string, timeout time.Duration) error {
	return mapErr(d.cli.ContainerRestart(ctx, name, container.StopOptions{Timeout: secs(timeout)}))
}

func (d *DockerRuntime) RemoveContainer(ctx context.Context, name string) error {
	return mapErr(d.cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true}))
}

func (d *DockerRuntime) RenameContainer(ctx context.Context, oldName, newName string) error {
	return mapErr(d.cli.ContainerRename(ctx, oldName, newName))
}

func (d *DockerRuntime) Stats(ctx context.Context, name string) (ContainerStats, error) {
	// stream=false returns a single sample that includes precpu_stats, so CPU% is computable.
	r, err := d.cli.ContainerStats(ctx, name, false)
	if err != nil {
		return ContainerStats{}, mapErr(err)
	}
	defer r.Body.Close()
	var s container.StatsResponse
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		return ContainerStats{}, err
	}
	out := ContainerStats{}
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	ncpu := float64(s.CPUStats.OnlineCPUs)
	if ncpu == 0 {
		ncpu = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpuDelta > 0 && sysDelta > 0 && ncpu > 0 {
		out.CPUPct = cpuDelta / sysDelta * ncpu * 100
	}
	mem := float64(s.MemoryStats.Usage)
	// match `docker stats`: subtract page cache
	if v, ok := s.MemoryStats.Stats["inactive_file"]; ok && float64(v) < mem {
		mem -= float64(v)
	} else if v, ok := s.MemoryStats.Stats["total_inactive_file"]; ok && float64(v) < mem {
		mem -= float64(v)
	}
	out.MemMB = mem / (1024 * 1024)
	for _, n := range s.Networks {
		out.RxBytes += n.RxBytes
		out.TxBytes += n.TxBytes
	}
	return out, nil
}

// lineWriter splits a byte stream into lines and calls emit for each.
type lineWriter struct {
	mu     sync.Mutex
	stream string
	buf    []byte
	emit   func(stream string, line string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := strings.IndexByte(string(w.buf), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		w.emit(w.stream, line)
	}
	if len(w.buf) > 64*1024 { // very long line without newline: flush it
		w.emit(w.stream, string(w.buf))
		w.buf = nil
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(w.stream, string(w.buf))
		w.buf = nil
	}
}

// parseTimestamped splits docker's "--timestamps" prefix.
func parseTimestamped(line string) (time.Time, string) {
	if i := strings.IndexByte(line, ' '); i > 0 {
		if t, err := time.Parse(time.RFC3339Nano, line[:i]); err == nil {
			return t.UTC(), line[i+1:]
		}
	}
	return time.Now().UTC(), line
}

func (d *DockerRuntime) Logs(ctx context.Context, name string, tail int, follow bool, fn func(LogRecord)) error {
	opts := container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: follow, Timestamps: true}
	if tail >= 0 {
		opts.Tail = strconv.Itoa(tail)
	}
	rc, err := d.cli.ContainerLogs(ctx, name, opts)
	if err != nil {
		return mapErr(err)
	}
	defer rc.Close()
	go func() { <-ctx.Done(); rc.Close() }()
	emit := func(stream, line string) {
		at, text := parseTimestamped(line)
		fn(LogRecord{At: at, Stream: stream, Line: text})
	}
	stdout := &lineWriter{stream: "stdout", emit: emit}
	stderr := &lineWriter{stream: "stderr", emit: emit}
	// Containers created by the agent never use a TTY, so the stream is multiplexed.
	_, err = stdcopy.StdCopy(stdout, stderr, bufio.NewReader(rc))
	stdout.Flush()
	stderr.Flush()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (d *DockerRuntime) Exec(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	ex, err := d.cli.ContainerExecCreate(ctx, name, container.ExecOptions{
		Cmd: cmd, AttachStdout: true, AttachStderr: true, AttachStdin: stdin != nil,
	})
	if err != nil {
		return -1, mapErr(err)
	}
	att, err := d.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{})
	if err != nil {
		return -1, err
	}
	defer att.Close()
	if stdin != nil {
		go func() {
			_, _ = io.Copy(att.Conn, stdin)
			_ = att.CloseWrite()
		}()
	}
	done := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(stdout, stderr, att.Reader)
		done <- err
	}()
	select {
	case <-ctx.Done():
		att.Close()
		return -1, fmt.Errorf("exec: %w", ctx.Err())
	case err := <-done:
		if err != nil {
			return -1, err
		}
	}
	ins, err := d.cli.ContainerExecInspect(ctx, ex.ID)
	if err != nil {
		return -1, err
	}
	return ins.ExitCode, nil
}

func (d *DockerRuntime) LoadImage(ctx context.Context, archive io.Reader) ([]string, error) {
	resp, err := d.cli.ImageLoad(ctx, archive, client.ImageLoadWithQuiet(true))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var refs []string
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Stream string `json:"stream"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return refs, err
		}
		if msg.Error != "" {
			return refs, errors.New(msg.Error)
		}
		for _, line := range strings.Split(msg.Stream, "\n") {
			for _, pfx := range []string{"Loaded image: ", "Loaded image ID: "} {
				if strings.HasPrefix(line, pfx) {
					refs = append(refs, strings.TrimSpace(strings.TrimPrefix(line, pfx)))
				}
			}
		}
	}
	if len(refs) == 0 {
		return nil, errors.New("image load reported no images")
	}
	return refs, nil
}

func (d *DockerRuntime) TagImage(ctx context.Context, source, target string) error {
	return mapErr(d.cli.ImageTag(ctx, source, target))
}

func (d *DockerRuntime) ListImages(ctx context.Context, repo string) ([]ImageSummary, error) {
	list, err := d.cli.ImageList(ctx, image.ListOptions{Filters: filters.NewArgs(filters.Arg("reference", repo+":*"))})
	if err != nil {
		return nil, mapErr(err)
	}
	var out []ImageSummary
	for _, im := range list {
		for _, t := range im.RepoTags {
			if strings.HasPrefix(t, repo+":") {
				out = append(out, ImageSummary{Ref: t, ID: im.ID, Created: time.Unix(im.Created, 0).UTC()})
			}
		}
	}
	return out, nil
}

func (d *DockerRuntime) RemoveImage(ctx context.Context, ref string) error {
	_, err := d.cli.ImageRemove(ctx, ref, image.RemoveOptions{PruneChildren: true})
	return mapErr(err)
}
