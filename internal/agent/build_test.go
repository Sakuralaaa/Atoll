package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

type cmdCall struct {
	dir  string
	env  []string
	name string
	args []string
}

// fakeCmd stands in for git and docker: checkout writes the repository files, rev-parse
// prints the commit, docker build tags an image in the fake runtime.
type fakeCmd struct {
	rt          *agenttest.Fake
	sha         string
	files       map[string]string // written into the checkout
	fetchOut    []string
	fetchErr    error
	fetchHook   func(env []string) // called during git fetch (build_git_test.go)
	lsRemoteOut []string           // git ls-remote output
	buildBlock  chan struct{}      // docker build waits for it (or ctx) when set
	started     chan string        // receives the tag of every docker build that starts
	inspectErr  error              // docker buildx inspect (dedicated builder exists?)
	createErr   error              // docker buildx create
	pullErr     error              // docker pull
	lsOut       []string           // docker buildx ls output

	mu         sync.Mutex
	calls      []cmdCall
	running    int
	maxRunning int
}

func newFakeCmd(rt *agenttest.Fake) *fakeCmd {
	return &fakeCmd{rt: rt, sha: testSHA, files: map[string]string{"Dockerfile": "FROM scratch\n"}, started: make(chan string, 8)}
}

func (c *fakeCmd) Run(ctx context.Context, dir string, env []string, out func(string), name string, args ...string) error {
	c.mu.Lock()
	c.calls = append(c.calls, cmdCall{dir: dir, env: append([]string(nil), env...), name: name, args: append([]string(nil), args...)})
	c.mu.Unlock()
	// `docker run ... --entrypoint git IMAGE <git args>` behaves like git on the bind-mounted checkout
	if name == "docker" && len(args) > 0 && args[0] == "run" {
		for i, a := range args {
			if a == "--entrypoint" && i+2 < len(args) && args[i+1] == "git" {
				name, args = "git", args[i+3:]
				break
			}
		}
	}
	joined := strings.Join(args, " ")
	switch {
	case name == "docker" && args[0] == "pull":
		out("2.52.0: Pulling from alpine/git")
		out("Status: Downloaded newer image for " + args[1])
		c.rt.AddImage(args[1], time.Now())
		return c.pullErr
	case name == "git" && args[0] == "ls-remote":
		for _, l := range c.lsRemoteOut {
			out(l)
		}
	case name == "git" && args[0] == "fetch":
		if c.fetchHook != nil {
			c.fetchHook(env)
		}
		for _, l := range c.fetchOut {
			out(l)
		}
		return c.fetchErr
	case name == "git" && strings.Contains(joined, "checkout"):
		for rel, content := range c.files {
			p := filepath.Join(dir, rel)
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				return err
			}
		}
	case name == "git" && args[0] == "rev-parse":
		out(c.sha)
	case name == "docker" && args[0] == "buildx" && args[1] == "inspect":
		return c.inspectErr
	case name == "docker" && args[0] == "buildx" && args[1] == "ls":
		for _, l := range c.lsOut {
			out(l)
		}
	case name == "docker" && args[0] == "buildx" && args[1] == "create":
		return c.createErr
	case name == "docker" && (args[0] == "build" || args[0] == "buildx" && args[1] == "build"):
		tag := ""
		for i, a := range args {
			if a == "-t" && i+1 < len(args) {
				tag = args[i+1]
			}
		}
		c.mu.Lock()
		c.running++
		if c.running > c.maxRunning {
			c.maxRunning = c.running
		}
		c.mu.Unlock()
		defer func() {
			c.mu.Lock()
			c.running--
			c.mu.Unlock()
		}()
		c.started <- tag
		out("#1 [internal] load build definition from Dockerfile")
		if c.buildBlock != nil {
			select {
			case <-c.buildBlock:
			case <-ctx.Done():
				return errors.New("signal: killed")
			}
		}
		c.rt.AddImage(tag, time.Now())
		out("#9 naming to " + tag + " done")
	}
	return nil
}

func (c *fakeCmd) callsOf(name, sub string) []cmdCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []cmdCall
	for _, k := range c.calls {
		if k.name == name && strings.Contains(strings.Join(k.args, " "), sub) {
			out = append(out, k)
		}
	}
	return out
}

type buildEvents struct {
	mu    sync.Mutex
	lines []string
}

func (e *buildEvents) emit(typ string, payload any) {
	if typ != api.MsgBuildLog {
		return
	}
	ev := payload.(api.BuildLogEvent)
	e.mu.Lock()
	e.lines = append(e.lines, ev.Lines...)
	e.mu.Unlock()
}

func (e *buildEvents) all() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.Join(e.lines, "\n")
}

func newBuilder(t *testing.T) (*agent.Builder, *agenttest.Fake, *fakeCmd, *buildEvents, agent.Paths) {
	t.Helper()
	f := agenttest.New()
	c := newFakeCmd(f)
	ev := &buildEvents{}
	p := agent.Paths{Root: t.TempDir()}
	b := &agent.Builder{RT: f, Paths: p, Cmd: c, Emit: ev.emit, FlushEvery: 10 * time.Millisecond,
		HasCommand: func(string) bool { return true }}
	return b, f, c, ev, p
}

func buildPayload() api.BuildPayload {
	return api.BuildPayload{DeploymentID: "dep_1", ServiceID: "svc_app", Repo: "https://github.com/example/app", Branch: "main",
		BuildArgs: map[string]string{"B_ARG": "2", "A_ARG": "1"}}
}

func TestBuildClonesBuildsAndTags(t *testing.T) {
	b, f, c, ev, p := newBuilder(t)
	res, err := b.Build(context.Background(), buildPayload())
	if err != nil {
		t.Fatal(err)
	}
	want := "atoll-build/svc_app:" + testSHA[:12]
	if res.Image != want || res.Commit != testSHA {
		t.Fatalf("result %+v, want image %s commit %s", res, want, testSHA)
	}
	if _, err := f.ImageInfo(context.Background(), want); err != nil {
		t.Fatalf("built image missing: %v", err)
	}
	fetch := c.callsOf("git", "fetch")
	if len(fetch) != 1 || !strings.Contains(strings.Join(fetch[0].args, " "), "--depth 1 --no-tags https://github.com/example/app.git refs/heads/main") {
		t.Fatalf("fetch = %+v", fetch)
	}
	builds := c.callsOf("docker", "build")
	if len(builds) != 1 {
		t.Fatalf("docker builds = %+v", builds)
	}
	args := strings.Join(builds[0].args, " ")
	for _, s := range []string{"-t " + want, "--build-arg A_ARG=1 --build-arg B_ARG=2", "atoll.build.commit=" + testSHA, "--progress=plain"} {
		if !strings.Contains(args, s) {
			t.Fatalf("docker build args %q lack %q", args, s)
		}
	}
	if !strings.Contains(ev.all(), "naming to "+want) {
		t.Fatalf("build output not streamed: %q", ev.all())
	}
	if ents, _ := os.ReadDir(p.BuildsDir()); len(ents) != 0 {
		t.Fatalf("checkout not removed: %v", ents)
	}

	// an exact commit (webhook push) is fetched by sha
	pl := buildPayload()
	pl.Commit = testSHA
	if _, err := b.Build(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	fetch = c.callsOf("git", "fetch")
	if got := fetch[len(fetch)-1].args; got[len(got)-1] != testSHA {
		t.Fatalf("commit fetch args %v", got)
	}
	// ...and a different commit than asked for is refused
	c.sha = strings.Repeat("f", 40)
	if _, err := b.Build(context.Background(), pl); err == nil || !strings.Contains(err.Error(), "wanted "+testSHA) {
		t.Fatalf("commit mismatch: %v", err)
	}
}

func TestBuildMissingDockerfile(t *testing.T) {
	b, _, c, _, _ := newBuilder(t)
	c.files = map[string]string{"README.md": "hi"}
	_, err := b.Build(context.Background(), buildPayload())
	if err == nil || !strings.Contains(err.Error(), "no Dockerfile at Dockerfile") || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err = %v", err)
	}
	if n := len(c.callsOf("docker", "build")); n != 0 {
		t.Fatalf("docker build ran without a Dockerfile (%d)", n)
	}

	// a Dockerfile in a subdirectory is found through context/dockerfile
	c.files = map[string]string{"svc/web/Dockerfile.prod": "FROM scratch\n"}
	pl := buildPayload()
	pl.Context, pl.Dockerfile = "svc/web", "Dockerfile.prod"
	if _, err := b.Build(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	// paths leaving the repository are refused before anything runs
	pl.Context = "../etc"
	if _, err := b.Build(context.Background(), pl); err == nil || !strings.Contains(err.Error(), "inside the repository") {
		t.Fatalf("escape: %v", err)
	}
}

func TestBuildTimeout(t *testing.T) {
	b, _, c, ev, _ := newBuilder(t)
	c.buildBlock = make(chan struct{}) // never released
	pl := buildPayload()
	pl.TimeoutSec = 1
	start := time.Now()
	_, err := b.Build(context.Background(), pl)
	if err == nil || !strings.Contains(err.Error(), "timed out after 1s") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("timeout took %s", d)
	}
	if !strings.Contains(ev.all(), "timed out") {
		t.Fatalf("timeout not in the build log: %q", ev.all())
	}
}

func TestBuildOneAtATime(t *testing.T) {
	b, _, c, ev, _ := newBuilder(t)
	c.buildBlock = make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		_, err := b.Build(context.Background(), buildPayload())
		errs <- err
	}()
	select {
	case <-c.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first build never started")
	}
	second := buildPayload()
	second.ServiceID, second.DeploymentID = "svc_other", "dep_2"
	go func() {
		_, err := b.Build(context.Background(), second)
		errs <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(ev.all(), "waiting for another build") {
		if time.Now().After(deadline) {
			t.Fatal("second build did not wait for the slot")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case tag := <-c.started:
		t.Fatalf("second docker build %s started while the first runs", tag)
	case <-time.After(150 * time.Millisecond):
	}
	close(c.buildBlock)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if c.maxRunning != 1 {
		t.Fatalf("max concurrent docker builds = %d", c.maxRunning)
	}

	<-c.started // the second build's start

	// a slot held longer than queue_sec fails the waiting build
	c.buildBlock = make(chan struct{})
	go func() {
		_, err := b.Build(context.Background(), buildPayload())
		errs <- err
	}()
	<-c.started
	q := second
	q.QueueSec = 1
	if _, err := b.Build(context.Background(), q); err == nil || !strings.Contains(err.Error(), "build slot busy") {
		t.Fatalf("queue timeout: %v", err)
	}
	close(c.buildBlock)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
}

func TestBuildRetention(t *testing.T) {
	b, f, _, _, _ := newBuilder(t)
	base := time.Now().Add(-10 * time.Hour)
	old := func(i int) string { return "atoll-build/svc_app:old" + string(rune('0'+i)) }
	for i := 1; i <= 5; i++ { // old1 oldest ... old5 newest
		f.AddImage(old(i), base.Add(time.Duration(i)*time.Hour))
	}
	f.AddImage("atoll-build/svc_other:keepme", base)
	b.CurrentImage = func(id string) string {
		if id == "svc_app" {
			return old(1) // the container still runs the oldest one
		}
		return ""
	}
	pl := buildPayload()
	pl.Keep = 3
	res, err := b.Build(context.Background(), pl)
	if err != nil {
		t.Fatal(err)
	}
	imgs, _ := f.ListImages(context.Background(), "atoll-build/svc_app")
	got := map[string]bool{}
	for _, im := range imgs {
		got[im.Ref] = true
	}
	want := []string{res.Image, old(5), old(4), old(1)}
	if len(got) != len(want) {
		t.Fatalf("kept %v, want %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("kept %v, want %v", got, want)
		}
	}
	if _, err := f.ImageInfo(context.Background(), "atoll-build/svc_other:keepme"); err != nil {
		t.Fatal("pruned another service's image")
	}
}

func TestBuildTokenRedacted(t *testing.T) {
	const tok = "ghp_TESTTOKEN0123456789"
	b, _, c, ev, _ := newBuilder(t)
	c.fetchOut = []string{"remote: Repository not found for https://x-access-token:" + tok + "@github.com/example/app.git"}
	c.fetchErr = errors.New("exit status 128 (" + tok + ")")
	pl := buildPayload()
	pl.Token = tok
	_, err := b.Build(context.Background(), pl)
	if err == nil {
		t.Fatal("fetch failure not reported")
	}
	if strings.Contains(err.Error(), tok) || !strings.Contains(err.Error(), "***") {
		t.Fatalf("token in error: %v", err)
	}
	if logs := ev.all(); strings.Contains(logs, tok) || !strings.Contains(logs, "x-access-token:***@") {
		t.Fatalf("token in build log: %q", logs)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	authed := false
	for _, k := range c.calls {
		if strings.Contains(strings.Join(k.args, " "), tok) {
			t.Fatalf("token in argv: %v", k.args)
		}
		for _, e := range k.env {
			if strings.Contains(e, tok) {
				t.Fatalf("plaintext token in env: %s", e)
			}
			if strings.HasPrefix(e, "GIT_CONFIG_VALUE_0=AUTHORIZATION: basic ") {
				authed = true
			}
		}
	}
	if !authed {
		t.Fatal("git fetch got no auth header config")
	}
}

// Without git on the host the same git steps run in a throwaway container: the image is
// pulled once (progress in the build log), the checkout is bind-mounted, and the token
// reaches the container only through the environment (`-e NAME`, never a value in argv).
func TestBuildGitInContainer(t *testing.T) {
	const tok = "ghp_TESTTOKEN0123456789"
	b, _, c, ev, _ := newBuilder(t)
	b.HasCommand = func(name string) bool { return name != "git" }
	pl := buildPayload()
	pl.Token = tok
	res, err := b.Build(context.Background(), pl)
	if err != nil {
		t.Fatal(err)
	}
	if res.Commit != testSHA {
		t.Fatalf("result %+v", res)
	}
	if n := len(c.callsOf("git", "")); n != 0 {
		t.Fatalf("host git ran %d times without git on the host", n)
	}
	if pulls := c.callsOf("docker", "pull "+agent.DefaultGitImage); len(pulls) != 1 {
		t.Fatalf("git image pulls = %+v", pulls)
	}
	logs := ev.all()
	for _, s := range []string{"running git in a container (" + agent.DefaultGitImage + ")", "Downloaded newer image", "naming to " + res.Image} {
		if !strings.Contains(logs, s) {
			t.Fatalf("build log lacks %q: %q", s, logs)
		}
	}
	runs := c.callsOf("docker", "--entrypoint git")
	if len(runs) != 4 { // init, fetch, checkout, rev-parse
		t.Fatalf("git container runs = %d: %+v", len(runs), runs)
	}
	for _, r := range runs {
		args := strings.Join(r.args, " ")
		for _, s := range []string{"run --rm", "-v " + r.dir + ":/src", "-w /src", "-u ", "-e GIT_CONFIG_VALUE_0 ", "--entrypoint git " + agent.DefaultGitImage} {
			if !strings.Contains(args, s) {
				t.Fatalf("docker run args %q lack %q", args, s)
			}
		}
		if strings.Contains(args, tok) || strings.Contains(args, "AUTHORIZATION") || strings.Contains(args, "basic ") {
			t.Fatalf("credentials in argv: %s", args)
		}
		authed := false
		for _, e := range r.env {
			if strings.Contains(e, tok) {
				t.Fatalf("plaintext token in env: %s", e)
			}
			if strings.HasPrefix(e, "GIT_CONFIG_VALUE_0=AUTHORIZATION: basic ") {
				authed = true
			}
		}
		if !authed {
			t.Fatalf("git container got no auth header env: %v", r.env)
		}
	}
	if strings.Contains(logs, tok) || strings.Contains(logs, "AUTHORIZATION") {
		t.Fatalf("credentials in build log: %q", logs)
	}
	if fetch := c.callsOf("docker", "fetch --depth 1 --no-tags https://github.com/example/app.git refs/heads/main"); len(fetch) != 1 {
		t.Fatalf("fetch in container = %+v", fetch)
	}

	// the image is pulled only once
	if _, err := b.Build(context.Background(), pl); err != nil {
		t.Fatal(err)
	}
	if pulls := c.callsOf("docker", "pull "+agent.DefaultGitImage); len(pulls) != 1 {
		t.Fatalf("git image pulled again: %+v", pulls)
	}

	// a pull failure names the container path
	b2, _, c2, _, _ := newBuilder(t)
	b2.HasCommand = func(name string) bool { return name != "git" }
	b2.GitImage = "example/git:1"
	c2.pullErr = errors.New("exit status 1")
	if _, err := b2.Build(context.Background(), buildPayload()); err == nil || !strings.Contains(err.Error(), "git is not installed on this server") || !strings.Contains(err.Error(), "example/git:1") {
		t.Fatalf("pull failure: %v", err)
	}
}
