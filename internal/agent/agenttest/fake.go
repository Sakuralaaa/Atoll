// Package agenttest provides an in-memory agent.Runtime for tests.
package agenttest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

type Container struct {
	Spec         agent.ContainerSpec
	Info         agent.ContainerInfo
	ExtraNetwork []string
}

// Fake is a goroutine-safe fake Docker engine.
type Fake struct {
	mu         sync.Mutex
	Containers map[string]*Container
	Networks   map[string]bool
	Images     map[string]agent.ImageInfo
	Created    map[string]time.Time // image ref -> creation time (ListImages)
	Calls      []string
	seq        int

	// Hooks / scripted behaviour.
	PullErr     map[string]error  // image -> error
	CreateErr   error             // next CreateContainer fails
	ExitOnStart map[string]int    // image -> exit code: container "exits" right after start
	LogLines    []agent.LogRecord // returned by Logs
	ExecFunc    func(name string, cmd []string, stdin io.Reader, stdout io.Writer) int
	Loaded      [][]byte // archives given to LoadImage
	LoadRefs    []string // refs LoadImage reports
	StatsFunc   func(name string) agent.ContainerStats
}

func New() *Fake {
	return &Fake{
		Containers: map[string]*Container{}, Networks: map[string]bool{}, Images: map[string]agent.ImageInfo{}, Created: map[string]time.Time{},
		PullErr: map[string]error{}, ExitOnStart: map[string]int{},
	}
}

func (f *Fake) call(format string, a ...any) {
	f.Calls = append(f.Calls, fmt.Sprintf(format, a...))
}

// CallLog returns a copy of recorded calls.
func (f *Fake) CallLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.Calls...)
}

func (f *Fake) Get(name string) (*Container, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.Containers[name]
	if !ok {
		return nil, false
	}
	cp := *c
	return &cp, true
}

// SetState forces a container state (simulate crash / reboot).
func (f *Fake) SetState(name, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.Containers[name]; ok {
		c.Info.State = state
	}
}

func (f *Fake) Version(context.Context) (string, error) { return "28.5.1-fake", nil }

func (f *Fake) EnsureNetwork(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.Networks[name] {
		f.call("network.create %s", name)
	}
	f.Networks[name] = true
	return nil
}

func (f *Fake) RemoveNetwork(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.Networks[name] {
		return agent.ErrNotFound
	}
	for _, c := range f.Containers {
		for _, n := range c.Spec.Networks {
			if n.Name == name {
				return agent.ErrNetworkInUse
			}
		}
	}
	f.call("network.rm %s", name)
	delete(f.Networks, name)
	return nil
}

func (f *Fake) PullImage(_ context.Context, ref string, auth *api.RegistryAuth) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	user := ""
	if auth != nil {
		user = auth.Username
	}
	f.call("pull %s auth=%s", ref, user)
	if err := f.PullErr[ref]; err != nil {
		return err
	}
	f.seq++
	f.Images[ref] = agent.ImageInfo{ID: fmt.Sprintf("sha256:img%04d", f.seq), Digest: fmt.Sprintf("sha256:digest-%s", strings.ReplaceAll(ref, ":", "-"))}
	return nil
}

func (f *Fake) ImageInfo(_ context.Context, ref string) (agent.ImageInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i, ok := f.Images[ref]; ok {
		return i, nil
	}
	for _, i := range f.Images {
		if i.ID == ref {
			return i, nil
		}
	}
	return agent.ImageInfo{}, agent.ErrNotFound
}

func (f *Fake) ListContainers(_ context.Context, labelKey string) ([]agent.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agent.ContainerInfo
	for _, c := range f.Containers {
		if _, ok := c.Info.Labels[labelKey]; ok {
			out = append(out, c.Info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *Fake) InspectContainer(_ context.Context, name string) (agent.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.Containers[name]
	if !ok {
		return agent.ContainerInfo{}, agent.ErrNotFound
	}
	return c.Info, nil
}

func (f *Fake) CreateContainer(_ context.Context, s agent.ContainerSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("create %s image=%s", s.Name, s.Image)
	if f.CreateErr != nil {
		err := f.CreateErr
		f.CreateErr = nil
		return err
	}
	if _, ok := f.Containers[s.Name]; ok {
		return fmt.Errorf("conflict: container %s exists", s.Name)
	}
	for _, n := range s.Networks {
		if !f.Networks[n.Name] {
			return fmt.Errorf("network %s not found", n.Name)
		}
	}
	f.seq++
	img := f.Images[s.Image]
	f.Containers[s.Name] = &Container{Spec: s, Info: agent.ContainerInfo{
		ID: fmt.Sprintf("c%04d", f.seq), Name: s.Name, Image: s.Image, ImageID: img.ID,
		Labels: s.Labels, State: "created",
	}}
	return nil
}

func (f *Fake) StartContainer(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.call("start %s", name)
	c, ok := f.Containers[name]
	if !ok {
		return agent.ErrNotFound
	}
	if code, crash := f.ExitOnStart[c.Spec.Image]; crash {
		c.Info.State, c.Info.ExitCode = "exited", code
		return nil
	}
	c.Info.State = "running"
	c.Info.StartedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return nil
}

func (f *Fake) StopContainer(_ context.Context, name string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.Containers[name]
	if !ok {
		return agent.ErrNotFound
	}
	f.call("stop %s", name)
	c.Info.State = "exited"
	return nil
}

func (f *Fake) RestartContainer(_ context.Context, name string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.Containers[name]
	if !ok {
		return agent.ErrNotFound
	}
	f.call("restart %s", name)
	c.Info.State = "running"
	c.Info.RestartCount++
	return nil
}

func (f *Fake) RemoveContainer(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Containers[name]; !ok {
		return agent.ErrNotFound
	}
	f.call("remove %s", name)
	delete(f.Containers, name)
	return nil
}

func (f *Fake) RenameContainer(_ context.Context, oldName, newName string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.Containers[oldName]
	if !ok {
		return agent.ErrNotFound
	}
	if _, exists := f.Containers[newName]; exists {
		return fmt.Errorf("conflict: %s exists", newName)
	}
	f.call("rename %s %s", oldName, newName)
	delete(f.Containers, oldName)
	c.Info.Name = newName
	f.Containers[newName] = c
	return nil
}

func (f *Fake) Stats(_ context.Context, name string) (agent.ContainerStats, error) {
	if f.StatsFunc != nil {
		return f.StatsFunc(name), nil
	}
	return agent.ContainerStats{CPUPct: 1.5, MemMB: 64, RxBytes: 1000, TxBytes: 500}, nil
}

func (f *Fake) Logs(ctx context.Context, name string, tail int, follow bool, fn func(agent.LogRecord)) error {
	if _, err := f.InspectContainer(ctx, name); err != nil {
		return err
	}
	f.mu.Lock()
	lines := append([]agent.LogRecord(nil), f.LogLines...)
	f.mu.Unlock()
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	for _, l := range lines {
		fn(l)
	}
	if follow {
		<-ctx.Done()
	}
	return nil
}

func (f *Fake) Exec(ctx context.Context, name string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	f.mu.Lock()
	_, ok := f.Containers[name]
	f.call("exec %s %s", name, strings.Join(cmd, " "))
	fn := f.ExecFunc
	f.mu.Unlock()
	if !ok {
		return -1, agent.ErrNotFound
	}
	if fn == nil {
		if stdin != nil {
			_, _ = io.Copy(io.Discard, stdin)
		}
		fmt.Fprintf(stdout, "ran %s\n", strings.Join(cmd, " "))
		return 0, nil
	}
	return fn(name, cmd, stdin, stdout), nil
}

func (f *Fake) LoadImage(_ context.Context, archive io.Reader) ([]string, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, archive); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Loaded = append(f.Loaded, buf.Bytes())
	f.call("load %d bytes", buf.Len())
	for _, r := range f.LoadRefs {
		f.seq++
		f.Images[r] = agent.ImageInfo{ID: fmt.Sprintf("sha256:loaded%04d", f.seq)}
	}
	return append([]string(nil), f.LoadRefs...), nil
}

func (f *Fake) TagImage(_ context.Context, source, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.Images[source]
	if !ok {
		return agent.ErrNotFound
	}
	f.call("tag %s %s", source, target)
	f.Images[target] = i
	return nil
}

func (f *Fake) ListImages(_ context.Context, repo string) ([]agent.ImageSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agent.ImageSummary
	for ref, i := range f.Images {
		if strings.HasPrefix(ref, repo+":") {
			out = append(out, agent.ImageSummary{Ref: ref, ID: i.ID, Created: f.Created[ref]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

func (f *Fake) RemoveImage(_ context.Context, ref string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.Images[ref]; !ok {
		return agent.ErrNotFound
	}
	f.call("rmi %s", ref)
	delete(f.Images, ref)
	delete(f.Created, ref)
	return nil
}

// AddImage registers a local image (as a build would), created at t.
func (f *Fake) AddImage(ref string, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	if f.Images == nil {
		f.Images = map[string]agent.ImageInfo{}
	}
	if f.Created == nil { // a Fake built as a struct literal instead of New()
		f.Created = map[string]time.Time{}
	}
	f.Images[ref] = agent.ImageInfo{ID: fmt.Sprintf("sha256:built%04d", f.seq)}
	f.Created[ref] = t
}

var _ agent.Runtime = (*Fake)(nil)
