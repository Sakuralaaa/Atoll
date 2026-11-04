package dockeradopt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

type fakeEngine struct {
	mu       sync.Mutex
	ctrs     map[string]*Container
	images   map[string]Image
	pullErr  error
	tags     map[string]string
	calls    []string
	stopErr  map[string]error
	drivers  map[string]string
	memUsage float64
}

func newFake() *fakeEngine {
	return &fakeEngine{ctrs: map[string]*Container{}, images: map[string]Image{}, tags: map[string]string{}, stopErr: map[string]error{}, drivers: map[string]string{}}
}

func (f *fakeEngine) add(c Container) { f.ctrs[c.ID] = &c }

func (f *fakeEngine) Version(context.Context) (string, error) { return "28.0.0", nil }
func (f *fakeEngine) Containers(context.Context) ([]Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Container
	for _, c := range f.ctrs {
		out = append(out, *c)
	}
	return out, nil
}
func (f *fakeEngine) find(id string) *Container {
	if c, ok := f.ctrs[id]; ok {
		return c
	}
	for _, c := range f.ctrs {
		if c.Name == id {
			return c
		}
	}
	return nil
}
func (f *fakeEngine) Container(_ context.Context, id string) (Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c := f.find(id); c != nil {
		return *c, nil
	}
	return Container{}, errors.New("no such container")
}
func (f *fakeEngine) Image(_ context.Context, ref string) (Image, error) {
	if im, ok := f.images[ref]; ok {
		return im, nil
	}
	for _, im := range f.images {
		for _, rd := range im.RepoDigests {
			if rd == ref {
				return im, nil
			}
		}
	}
	return Image{}, errors.New("no such image")
}
func (f *fakeEngine) VolumeDriver(_ context.Context, n string) (string, error) {
	if d, ok := f.drivers[n]; ok {
		return d, nil
	}
	return "local", nil
}
func (f *fakeEngine) MemUsageMB(context.Context, string) (float64, error) { return f.memUsage, nil }
func (f *fakeEngine) Pull(_ context.Context, ref string) error {
	f.calls = append(f.calls, "pull "+ref)
	return f.pullErr
}
func (f *fakeEngine) Tag(_ context.Context, src, dst string) error {
	f.calls = append(f.calls, "tag "+src+" "+dst)
	f.tags[dst] = src
	return nil
}
func (f *fakeEngine) Stop(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "stop "+id)
	if err := f.stopErr[id]; err != nil {
		return err
	}
	f.find(id).State = "exited"
	return nil
}
func (f *fakeEngine) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "start "+id)
	f.find(id).State = "running"
	return nil
}
func (f *fakeEngine) SetRestartPolicy(_ context.Context, id, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := ParsePolicy(p); err != nil {
		return err
	}
	f.calls = append(f.calls, "policy "+id+" "+p)
	f.find(id).RestartPolicy = p
	return nil
}

var testPaths = agent.Paths{Root: "/var/lib/atoll"}

func allExist(string) (bool, bool) { return true, false }

func newAdopter(f *fakeEngine) *Adopter {
	a := New(f, testPaths, nil)
	a.Stat = allExist
	a.DiskFree = func(string) (uint64, error) { return 50e9, nil }
	a.MemFree = func() (uint64, error) { return 2048 << 20, nil }
	return a
}

var nginxImg = Image{ID: "sha256:aaaa1111bbbb2222", RepoDigests: []string{"nginx@sha256:feed"}, Env: []string{"PATH=/usr/bin", "NGINX_VERSION=1.27"},
	Entrypoint: []string{"/docker-entrypoint.sh"}, Cmd: []string{"nginx", "-g", "daemon off;"}}

func composeWeb() Container {
	return Container{
		ID: "c1", Name: "blog-web-1", Image: "nginx:1.27", ImageID: nginxImg.ID, State: "running",
		Labels:     map[string]string{labelComposeProject: "blog", labelComposeService: "web", labelComposeDir: "/srv/blog"},
		Env:        []string{"PATH=/usr/bin", "NGINX_VERSION=1.27", "SITE=example.com", "UPSTREAM=blog-db-1:5432"},
		Entrypoint: nginxImg.Entrypoint, Cmd: nginxImg.Cmd,
		Bindings:      map[string][]Binding{"80/tcp": {{HostIP: "0.0.0.0", HostPort: "8080"}, {HostIP: "::", HostPort: "8080"}}, "9000/tcp": {{HostIP: "127.0.0.1", HostPort: "9000"}}},
		Exposed:       []string{"80/tcp", "9000/tcp", "8443/tcp"},
		Mounts:        []Mount{{Type: "bind", Source: "/srv/blog/html", Destination: "/usr/share/nginx/html", RW: false}, {Type: "volume", Name: "blog_data", Source: "/var/lib/docker/volumes/blog_data/_data", Destination: "/data", RW: true}},
		RestartPolicy: "unless-stopped", NetworkMode: "blog_default", Networks: []string{"blog_default"},
	}
}

func composeDB() Container {
	return Container{
		ID: "c2", Name: "blog-db-1", Image: "postgres:16", ImageID: "sha256:pg", State: "running",
		Labels:        map[string]string{labelComposeProject: "blog", labelComposeService: "db"},
		Env:           []string{"POSTGRES_PASSWORD=s3cret"},
		RestartPolicy: "always", NetworkMode: "blog_default", Networks: []string{"blog_default"},
		Mounts: []Mount{{Type: "volume", Name: "blog_pg", Source: "/var/lib/docker/volumes/blog_pg/_data", Destination: "/var/lib/postgresql/data", RW: true}},
	}
}

func TestClassifyComposeContainer(t *testing.T) {
	d := Classify(composeWeb(), &nginxImg, testPaths, allExist)
	if d.Risk != api.DockerRiskAuto {
		t.Fatalf("risk %s %v", d.Risk, d.RiskReasons)
	}
	if d.PinnedImage != "nginx@sha256:feed" {
		t.Fatalf("pinned %q", d.PinnedImage)
	}
	if len(d.Env) != 2 || d.Env[0].Key != "SITE" || d.Env[1].Value != "blog-db-1:5432" {
		t.Fatalf("env %+v", d.Env)
	}
	if d.Command != nil || d.Args != nil {
		t.Fatalf("command copied from the image: %v %v", d.Command, d.Args)
	}
	want := []api.DockerPort{{ContainerPort: 80, HostPort: 8080, Protocol: "tcp"}, {ContainerPort: 9000, HostPort: 9000, Protocol: "tcp", HostIP: "127.0.0.1"}, {ContainerPort: 8443, Protocol: "tcp"}}
	if fmt.Sprint(d.Ports) != fmt.Sprint(want) {
		t.Fatalf("ports %+v", d.Ports)
	}
	if len(d.Mounts) != 2 || !d.Mounts[0].ReadOnly || d.Mounts[1].Name != "blog_data" {
		t.Fatalf("mounts %+v", d.Mounts)
	}
}

func TestClassifyCommandOverride(t *testing.T) {
	c := composeWeb()
	c.Cmd = []string{"nginx", "-T"}
	d := Classify(c, &nginxImg, testPaths, allExist)
	if d.Command != nil || fmt.Sprint(d.Args) != "[nginx -T]" {
		t.Fatalf("cmd override: %v %v", d.Command, d.Args)
	}
	c.Entrypoint, c.Cmd = []string{"/bin/sh", "-c"}, []string{"echo hi"}
	d = Classify(c, &nginxImg, testPaths, allExist)
	if fmt.Sprint(d.Command) != "[/bin/sh -c]" || fmt.Sprint(d.Args) != "[echo hi]" {
		t.Fatalf("entrypoint override: %v %v", d.Command, d.Args)
	}
}

func TestClassifyManualTiers(t *testing.T) {
	cases := []struct {
		name string
		mod  func(c *Container)
		want string
	}{
		{"host network", func(c *Container) { c.NetworkMode = "host" }, "host network"},
		{"privileged", func(c *Container) { c.Privileged = true }, "privileged"},
		{"docker socket", func(c *Container) {
			c.Mounts = append(c.Mounts, Mount{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock", RW: true})
		}, "runtime socket"},
		{"devices", func(c *Container) { c.Devices = []string{"/dev/ttyUSB0"} }, "host devices"},
		{"gpus", func(c *Container) { c.DeviceRequests = 1 }, "GPUs"},
		{"cap_add", func(c *Container) { c.CapAdd = []string{"NET_ADMIN"} }, "capabilities"},
		{"seccomp", func(c *Container) { c.SecurityOpt = []string{"seccomp=unconfined"} }, "security profile"},
		{"pid host", func(c *Container) { c.PidMode = "host" }, "PID namespace"},
		{"port 443", func(c *Container) { c.Bindings["443/tcp"] = []Binding{{HostPort: "443"}} }, "80/443"},
		{"port 22", func(c *Container) { c.Bindings["22/tcp"] = []Binding{{HostPort: "22"}} }, "SSH"},
		{"etc bind", func(c *Container) {
			c.Mounts = append(c.Mounts, Mount{Type: "bind", Source: "/etc/ssl", Destination: "/etc/ssl", RW: true})
		}, "/etc"},
		{"nfs volume", func(c *Container) {
			c.Mounts = append(c.Mounts, Mount{Type: "volume", Name: "x", Source: "/var/lib/docker/volumes/x/_data", Destination: "/x", RW: true, Driver: "nfs"})
		}, "driver"},
		{"workdir", func(c *Container) { c.WorkingDir = "/app" }, "working directory"},
		{"no image", nil, "no longer on this server"},
	}
	for _, tc := range cases {
		c := composeWeb()
		img := &nginxImg
		if tc.mod == nil {
			img = nil
		} else {
			tc.mod(&c)
		}
		d := Classify(c, img, testPaths, allExist)
		if d.Risk != api.DockerRiskManual || !strings.Contains(strings.Join(d.RiskReasons, "|"), tc.want) {
			t.Errorf("%s: risk %s reasons %v", tc.name, d.Risk, d.RiskReasons)
		}
	}
	// localtime read-only is fine
	c := composeWeb()
	c.Mounts = append(c.Mounts, Mount{Type: "bind", Source: "/etc/localtime", Destination: "/etc/localtime", RW: false})
	if d := Classify(c, &nginxImg, testPaths, allExist); d.Risk != api.DockerRiskAuto {
		t.Fatalf("localtime:ro flagged: %v", d.RiskReasons)
	}
}

func TestScanGroupsAndSkips(t *testing.T) {
	f := newFake()
	f.images[nginxImg.ID] = nginxImg
	f.images["sha256:pg"] = Image{ID: "sha256:pg"}       // locally built: no digest
	f.images["sha256:redis"] = Image{ID: "sha256:redis"} // standalone
	f.add(composeWeb())
	f.add(composeDB())
	f.add(Container{ID: "c3", Name: "cache", Image: "redis:7", ImageID: "sha256:redis", State: "exited", Labels: map[string]string{}, RestartPolicy: "no", Networks: []string{"blog_default"}})
	f.add(Container{ID: "c4", Name: "atoll-svc_x", Image: "x", ImageID: "x", Labels: map[string]string{"atoll.managed": "true"}})
	f.add(Container{ID: "c5", Name: "blog-web-run-1", Image: "nginx", ImageID: nginxImg.ID, Labels: map[string]string{labelComposeProject: "blog", labelComposeOneoff: "True"}})
	f.memUsage = 42
	a := newAdopter(f)
	out, err := a.Handle(context.Background(), api.MsgDockerScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	var inv api.DockerInventory
	if err := json.Unmarshal([]byte(out), &inv); err != nil {
		t.Fatal(err)
	}
	if len(inv.Groups) != 2 || inv.Groups[0].Key != "compose:blog" || inv.Groups[1].Key != "container:cache" {
		t.Fatalf("groups %+v", inv.Groups)
	}
	g := inv.Groups[0]
	if g.Kind != "compose" || g.WorkingDir != "/srv/blog" || len(g.Containers) != 2 || !g.Adoptable {
		t.Fatalf("compose group %+v", g)
	}
	if g.Containers[1].MemUsageMB != 42 || g.Containers[0].MemUsageMB != 42 {
		t.Fatalf("mem usage %+v", g.Containers)
	}
	ws := strings.Join(g.Warnings, "|")
	if !strings.Contains(ws, "UPSTREAM names container blog-db-1") || !strings.Contains(ws, "network blog_default is shared with cache") {
		t.Fatalf("group warnings %v", g.Warnings)
	}
	if inv.MemFreeMB != 2048 || inv.DiskFreeGB != 50 || inv.DockerVersion == "" {
		t.Fatalf("host readings %+v", inv)
	}
	db := g.Containers[0]
	if db.Name != "blog-db-1" || db.PinnedImage != "" || !strings.Contains(strings.Join(db.Warnings, "|"), "no registry digest") {
		t.Fatalf("db %+v", db)
	}
}

func TestPrepareImage(t *testing.T) {
	f := newFake()
	f.images[nginxImg.ID] = nginxImg
	a := newAdopter(f)
	ctx := context.Background()
	res, err := a.PrepareImage(ctx, api.DockerImagePayload{ServiceID: "svc_A", ImageID: nginxImg.ID, Pinned: "nginx@sha256:feed"})
	if err != nil || res.Ref != "nginx@sha256:feed" || res.Local {
		t.Fatalf("pinned: %+v %v", res, err)
	}
	f.pullErr = errors.New("unauthorized")
	res, err = a.PrepareImage(ctx, api.DockerImagePayload{ServiceID: "svc_A", ImageID: nginxImg.ID, Pinned: "nginx@sha256:feed"})
	if err != nil || res.Ref != "atoll-adopt/svc_a:aaaa1111bbbb" || !res.Local || f.tags[res.Ref] != nginxImg.ID {
		t.Fatalf("fallback: %+v %v", res, err)
	}
	if _, err := a.PrepareImage(ctx, api.DockerImagePayload{ServiceID: "svc_A", ImageID: "sha256:gone"}); err == nil {
		t.Fatal("missing image accepted")
	}
}

func TestControlStopStart(t *testing.T) {
	f := newFake()
	f.add(composeWeb())
	f.add(composeDB())
	a := newAdopter(f)
	ctx := context.Background()
	res, err := a.Control(ctx, api.DockerContainersPayload{Action: "stop", Containers: []api.DockerContainerCtl{{ID: "c1", Name: "blog-web-1"}, {Name: "blog-db-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Containers[0].RestartPolicy != "unless-stopped" || !res.Containers[0].WasRunning || res.Containers[0].State != "exited" || res.Containers[1].RestartPolicy != "always" {
		t.Fatalf("stop result %+v", res)
	}
	if f.ctrs["c1"].RestartPolicy != "no" || f.ctrs["c2"].RestartPolicy != "no" {
		t.Fatal("restart policy not disabled before stop")
	}
	// policy first, then stop
	if f.calls[0] != "policy c1 no" || f.calls[1] != "stop c1" {
		t.Fatalf("calls %v", f.calls)
	}
	_, err = a.Control(ctx, api.DockerContainersPayload{Action: "start", Containers: []api.DockerContainerCtl{{ID: "c1", RestartPolicy: "unless-stopped", Start: true}, {ID: "c2", RestartPolicy: "always"}}})
	if err != nil {
		t.Fatal(err)
	}
	if f.ctrs["c1"].State != "running" || f.ctrs["c1"].RestartPolicy != "unless-stopped" || f.ctrs["c2"].State != "exited" || f.ctrs["c2"].RestartPolicy != "always" {
		t.Fatalf("after start: %+v %+v", f.ctrs["c1"], f.ctrs["c2"])
	}
	// errors are collected, other containers still handled; Atoll's containers refused
	f.add(Container{ID: "c9", Name: "atoll-svc_y", Labels: map[string]string{"atoll.managed": "true"}})
	f.stopErr["c1"] = errors.New("boom")
	res, err = a.Control(ctx, api.DockerContainersPayload{Action: "stop", Containers: []api.DockerContainerCtl{{ID: "c1"}, {ID: "c9"}, {ID: "c2"}}})
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "managed by Atoll") || len(res.Containers) != 2 {
		t.Fatalf("partial stop: %+v %v", res, err)
	}
	if _, err := a.Control(ctx, api.DockerContainersPayload{Action: "remove"}); err == nil {
		t.Fatal("remove accepted")
	}
}

func TestParsePolicy(t *testing.T) {
	for _, p := range []string{"no", "always", "unless-stopped", "on-failure", "on-failure:3"} {
		if _, err := ParsePolicy(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{"", "sometimes", "always:2", "on-failure:x"} {
		if _, err := ParsePolicy(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}
