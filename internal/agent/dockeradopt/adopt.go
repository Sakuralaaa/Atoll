package dockeradopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

// LocalImageRepo is the repository of the local tags given to adopted images that have no
// registry digest (atoll-adopt/<service_id>:<image id 12>). The server never pulls them.
const LocalImageRepo = "atoll-adopt/"

// Adopter implements agent.Extension for the docker.* messages.
type Adopter struct {
	E     Engine
	Paths agent.Paths
	Log   *slog.Logger

	// Host readings, replaceable in tests.
	DiskFree func(path string) (uint64, error)
	MemFree  func() (uint64, error)
	Stat     FileInfo
	// StopTimeout is how long an old container gets to stop (default 30s).
	StopTimeout time.Duration
	// PullTimeout bounds the registry check of a pinned image (default 2m).
	PullTimeout time.Duration
}

func New(e Engine, p agent.Paths, log *slog.Logger) *Adopter {
	if log == nil {
		log = slog.Default()
	}
	return &Adopter{
		E: e, Paths: p, Log: log,
		DiskFree: func(path string) (uint64, error) {
			u, err := disk.Usage(path)
			if err != nil {
				return 0, err
			}
			return u.Free, nil
		},
		MemFree: func() (uint64, error) {
			v, err := mem.VirtualMemory()
			if err != nil {
				return 0, err
			}
			return v.Available, nil
		},
		Stat: func(p string) (bool, bool) {
			fi, err := os.Stat(p)
			if err != nil {
				return false, false
			}
			return true, fi.Mode().IsRegular()
		},
		StopTimeout: 30 * time.Second,
		PullTimeout: 2 * time.Minute,
	}
}

func (a *Adopter) Handles(msgType string) bool { return strings.HasPrefix(msgType, "docker.") }

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

func asJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func (a *Adopter) Handle(ctx context.Context, msgType string, raw json.RawMessage) (string, error) {
	switch msgType {
	case api.MsgDockerScan:
		inv, err := a.Scan(ctx)
		if err != nil {
			return "", err
		}
		return asJSON(inv)
	case api.MsgDockerImage:
		p, err := decode[api.DockerImagePayload](raw)
		if err != nil {
			return "", err
		}
		res, err := a.PrepareImage(ctx, p)
		if err != nil {
			return "", err
		}
		return asJSON(res)
	case api.MsgDockerContainers:
		p, err := decode[api.DockerContainersPayload](raw)
		if err != nil {
			return "", err
		}
		res, err := a.Control(ctx, p)
		out, _ := asJSON(res)
		return out, err
	}
	return "", fmt.Errorf("unsupported message type %q", msgType)
}

// Scan lists the host's containers that are not Atoll's, classified and grouped.
func (a *Adopter) Scan(ctx context.Context) (*api.DockerInventory, error) {
	inv := &api.DockerInventory{Groups: []api.DockerGroup{}, ScannedAt: time.Now().UTC()}
	if v, err := a.E.Version(ctx); err == nil {
		inv.DockerVersion = v
	}
	all, err := a.E.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	raw := map[string]Container{}
	images := map[string]*Image{}
	var cs []api.DockerContainer
	for _, c := range all {
		if Skip(c) {
			continue
		}
		raw[c.ID] = c
		img, seen := images[c.ImageID]
		if !seen {
			if im, err := a.E.Image(ctx, c.ImageID); err == nil {
				img = &im
			}
			images[c.ImageID] = img
		}
		for i, m := range c.Mounts {
			if m.Type == "volume" && m.Driver == "" && m.Name != "" {
				if drv, err := a.E.VolumeDriver(ctx, m.Name); err == nil {
					c.Mounts[i].Driver = drv
				}
			}
		}
		cs = append(cs, Classify(c, img, a.Paths, a.Stat))
	}
	a.memUsage(ctx, cs)
	inv.Groups = Group(cs, raw)
	if free, err := a.DiskFree(a.diskPath()); err == nil {
		inv.DiskFreeGB = float64(free) / 1e9
	}
	if free, err := a.MemFree(); err == nil {
		inv.MemFreeMB = int(free >> 20)
	}
	return inv, nil
}

func (a *Adopter) diskPath() string {
	if a.Paths.Root != "" {
		if _, err := os.Stat(a.Paths.Root); err == nil {
			return a.Paths.Root
		}
	}
	return "/"
}

// memUsage reads the memory use of the running containers (a few at a time: a one-shot
// docker stats sample takes about a second).
func (a *Adopter) memUsage(ctx context.Context, cs []api.DockerContainer) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for i := range cs {
		if cs[i].State != "running" {
			continue
		}
		wg.Add(1)
		go func(c *api.DockerContainer) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if mb, err := a.E.MemUsageMB(sctx, c.ID); err == nil {
				c.MemUsageMB = int(mb + 0.5)
			}
		}(&cs[i])
	}
	wg.Wait()
}

// LocalTag is the local tag of an adopted image without a registry digest.
func LocalTag(serviceID, imageID string) string {
	id := strings.TrimPrefix(imageID, "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return LocalImageRepo + strings.ToLower(serviceID) + ":" + id
}

// PrepareImage decides which image the Atoll copy runs: the pinned registry digest when the
// registry still serves it (so the service can later move to another server), otherwise the
// exact local image under a local tag. The image the old container runs is never removed.
func (a *Adopter) PrepareImage(ctx context.Context, p api.DockerImagePayload) (api.DockerImageResult, error) {
	if p.ServiceID == "" || p.ImageID == "" {
		return api.DockerImageResult{}, errors.New("service_id and image_id are required")
	}
	if _, err := a.E.Image(ctx, p.ImageID); err != nil {
		return api.DockerImageResult{}, fmt.Errorf("image %s is no longer on this server: %w", p.ImageID, err)
	}
	if p.Pinned != "" {
		pctx, cancel := context.WithTimeout(ctx, a.PullTimeout)
		err := a.E.Pull(pctx, p.Pinned)
		cancel()
		if err == nil {
			if im, ierr := a.E.Image(ctx, p.Pinned); ierr == nil && im.ID == p.ImageID {
				return api.DockerImageResult{Ref: p.Pinned}, nil
			}
		} else {
			a.Log.Info("adopt: pinned image not pullable, using a local tag", "image", p.Pinned, "err", err)
		}
	}
	ref := LocalTag(p.ServiceID, p.ImageID)
	if err := a.E.Tag(ctx, p.ImageID, ref); err != nil {
		return api.DockerImageResult{}, fmt.Errorf("tag %s: %w", ref, err)
	}
	return api.DockerImageResult{Ref: ref, Local: true}, nil
}

// Control stops or starts the user's own containers. Stop first sets the restart policy to
// "no" (a reboot must not start the old container next to Atoll's copy), then stops it; the
// result carries the policy each one had. Start restores the given policy and, when asked,
// starts the container. Containers are never removed. Every container is attempted; the
// error lists the ones that failed.
func (a *Adopter) Control(ctx context.Context, p api.DockerContainersPayload) (api.DockerContainersResult, error) {
	res := api.DockerContainersResult{Containers: []api.DockerContainerState{}}
	if p.Action != "stop" && p.Action != "start" {
		return res, fmt.Errorf("action must be stop or start, not %q", p.Action)
	}
	var errs []string
	for _, ref := range p.Containers {
		id := ref.ID
		if id == "" {
			id = ref.Name
		}
		c, err := a.E.Container(ctx, id)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ref.Name, err))
			continue
		}
		if Skip(c) {
			errs = append(errs, fmt.Sprintf("%s is managed by Atoll, not an adopted container", c.Name))
			continue
		}
		st := api.DockerContainerState{ID: c.ID, Name: c.Name, RestartPolicy: c.RestartPolicy, WasRunning: c.State == "running" || c.State == "restarting"}
		switch p.Action {
		case "stop":
			if err := a.E.SetRestartPolicy(ctx, c.ID, "no"); err != nil {
				errs = append(errs, fmt.Sprintf("%s: set restart policy: %v", c.Name, err))
				continue
			}
			if st.WasRunning {
				if err := a.E.Stop(ctx, c.ID, a.StopTimeout); err != nil {
					errs = append(errs, fmt.Sprintf("%s: stop: %v", c.Name, err))
				}
			}
		case "start":
			if ref.RestartPolicy != "" {
				if err := a.E.SetRestartPolicy(ctx, c.ID, ref.RestartPolicy); err != nil {
					errs = append(errs, fmt.Sprintf("%s: restore restart policy: %v", c.Name, err))
				}
			}
			if ref.Start && !st.WasRunning {
				if err := a.E.Start(ctx, c.ID); err != nil {
					errs = append(errs, fmt.Sprintf("%s: start: %v", c.Name, err))
				}
			}
		}
		if now, err := a.E.Container(ctx, c.ID); err == nil {
			st.State = now.State
		}
		res.Containers = append(res.Containers, st)
	}
	if len(errs) > 0 {
		return res, errors.New(strings.Join(errs, "; "))
	}
	return res, nil
}
