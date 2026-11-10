package agent

// Builds from source. The control server sends MsgBuild to the service's own server; the
// agent fetches one commit of the repository (shallow) into a temp dir, runs `docker build`
// (BuildKit) and tags the result atoll-build/<service_id>:<commit12>. The server then deploys
// that local image through the normal apply (ResolvedSpec.LocalImage: never pulled).
// One build runs at a time per server; output lines stream to the server as MsgBuildLog.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// BuildImageRepo prefixes every image built from source.
const BuildImageRepo = "atoll-build/"

// DefaultGitImage runs git in a throwaway container when the server has no git binary
// (e.g. a distro mirror that cannot install it). Pinned; ATOLL_GIT_IMAGE overrides it.
const DefaultGitImage = "alpine/git:2.52.0"

// BuildLabel marks images the builder made (value: service id); PruneImages leaves them alone.
const BuildLabel = "atoll.build.service"

// BuildImageTag is the local tag of a service's build of commit.
func BuildImageTag(serviceID, commit string) string {
	c := commit
	if len(c) > 12 {
		c = c[:12]
	}
	return BuildImageRepo + strings.ToLower(serviceID) + ":" + c
}

// CmdRunner runs a host command (git, docker) in dir with extra environment, passing every
// output line (stdout and stderr) to out. Tests replace it.
type CmdRunner interface {
	Run(ctx context.Context, dir string, env []string, out func(string), name string, args ...string) error
}

// ExecRunner runs real commands.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir string, env []string, out func(string), name string, args ...string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s is not installed on this server (install it, e.g. apt-get install -y %s)", name, map[string]string{"git": "git", "docker": "docker-ce-cli docker-buildx-plugin"}[name])
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 10 * time.Second
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			out(sc.Text())
		}
		io.Copy(io.Discard, pr)
	}()
	err := cmd.Run()
	pw.Close()
	<-done
	return err
}

// Builder runs builds from source on this server.
type Builder struct {
	RT    Runtime
	Paths Paths
	Cmd   CmdRunner
	Emit  EmitFunc
	Log   *slog.Logger
	// CurrentImage returns the image a service's container should run now (kept by pruning).
	CurrentImage func(serviceID string) string
	// FlushEvery batches log lines into MsgBuildLog frames (500ms).
	FlushEvery time.Duration

	// Limits caps builds (build_limits.go); the zero value runs plain `docker build`.
	Limits BuildLimits

	// GitImage is the image used for git when the host has none (default DefaultGitImage).
	GitImage string
	// HasCommand reports whether a host command exists (default exec.LookPath). Tests replace it.
	HasCommand func(name string) bool
	// HasSourceServices reports whether any service on this server is built from source;
	// when it says no, the daily cleanup removes the builder (nil = assume yes).
	HasSourceServices func() bool
	// Now is the clock for the idle stop (default time.Now). Tests replace it.
	Now func() time.Time
	// LookupHost resolves a repo host before fetching (default net.DefaultResolver). Tests replace it.
	LookupHost func(ctx context.Context, host string) ([]netip.Addr, error)

	once   sync.Once
	slot   chan struct{}
	bstate builderState
}

// NewBuilder returns a builder using real git/docker commands.
func NewBuilder(rt Runtime, p Paths, emit EmitFunc, log *slog.Logger) *Builder {
	if log == nil {
		log = slog.Default()
	}
	return &Builder{RT: rt, Paths: p, Cmd: ExecRunner{}, Emit: emit, Log: log, FlushEvery: 500 * time.Millisecond,
		Limits: BuildLimitsFromEnv(os.Getenv), GitImage: strings.TrimSpace(os.Getenv("ATOLL_GIT_IMAGE"))}
}

func (b *Builder) init() {
	b.once.Do(func() {
		b.slot = make(chan struct{}, 1)
		if b.FlushEvery <= 0 {
			b.FlushEvery = 500 * time.Millisecond
		}
		if b.Emit == nil {
			b.Emit = func(string, any) {}
		}
		if b.Cmd == nil {
			b.Cmd = ExecRunner{}
		}
		if b.Log == nil {
			b.Log = slog.Default()
		}
		if b.GitImage == "" {
			b.GitImage = DefaultGitImage
		}
		if b.HasCommand == nil {
			b.HasCommand = func(name string) bool { _, err := exec.LookPath(name); return err == nil }
		}
	})
}

var (
	buildCommitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
	buildBranchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	buildArgKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func validateBuild(p *api.BuildPayload) error {
	if err := validID(p.ServiceID); err != nil {
		return err
	}
	if p.Provider == "" && p.Builder == "" && (!legacyGitHub(p.Repo) || strings.ContainsAny(p.Repo, "@ \t\n?#")) {
		return fmt.Errorf("repo must be a GitHub https URL") // an older control server only sends those
	}
	if _, err := parseRemote(p.Repo); err != nil { // build_git.go
		return err
	}
	switch p.Builder {
	case "", "auto", "dockerfile", "generated":
	default:
		return fmt.Errorf("unknown builder %q", p.Builder)
	}
	if !buildBranchRe.MatchString(p.Branch) || strings.HasPrefix(p.Branch, "-") || strings.Contains(p.Branch, "..") {
		return fmt.Errorf("invalid branch %q", p.Branch)
	}
	if p.Commit != "" && !buildCommitRe.MatchString(p.Commit) {
		return fmt.Errorf("invalid commit %q", p.Commit)
	}
	for _, rel := range []string{p.Context, p.Dockerfile} {
		if rel != "" && (filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(filepath.Clean(rel), "../")) {
			return fmt.Errorf("path %q must stay inside the repository", rel)
		}
	}
	for k := range p.BuildArgs {
		if !buildArgKeyRe.MatchString(k) {
			return fmt.Errorf("invalid build arg %q", k)
		}
	}
	if p.Dockerfile == "" {
		p.Dockerfile = "Dockerfile"
	}
	if p.TimeoutSec <= 0 {
		p.TimeoutSec = 1200
	}
	if p.QueueSec <= 0 {
		p.QueueSec = p.TimeoutSec
	}
	if p.Keep <= 0 {
		p.Keep = 3
	}
	return nil
}

// buildLog batches output lines to the server and keeps the tail for error messages.
type buildLog struct {
	b      *Builder
	p      api.BuildPayload
	redact func(string) string
	mu     sync.Mutex
	buf    []string
	tail   []string
	stop   chan struct{}
	done   chan struct{}
}

func (b *Builder) newLog(p api.BuildPayload) *buildLog {
	l := &buildLog{b: b, p: p, stop: make(chan struct{}), done: make(chan struct{})}
	l.redact = redactor(authSecrets(buildAuth(p))) // build_git.go
	go func() {
		defer close(l.done)
		t := time.NewTicker(b.FlushEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				l.flush()
			case <-l.stop:
				l.flush()
				return
			}
		}
	}()
	return l
}

func (l *buildLog) line(s string) {
	s = l.redact(strings.TrimRight(s, "\r"))
	l.mu.Lock()
	l.buf = append(l.buf, s)
	l.tail = append(l.tail, s)
	if len(l.tail) > 15 {
		l.tail = l.tail[len(l.tail)-15:]
	}
	full := len(l.buf) >= 200
	l.mu.Unlock()
	if full {
		l.flush()
	}
}

func (l *buildLog) flush() {
	l.mu.Lock()
	lines := l.buf
	l.buf = nil
	l.mu.Unlock()
	if len(lines) > 0 {
		l.b.Emit(api.MsgBuildLog, api.BuildLogEvent{DeploymentID: l.p.DeploymentID, ServiceID: l.p.ServiceID, Lines: lines})
	}
}

func (l *buildLog) close() {
	close(l.stop)
	<-l.done
}

func (l *buildLog) lastLines() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.tail, "\n")
}

// Build runs one build (waiting for this server's build slot first) and returns the image.
func (b *Builder) Build(ctx context.Context, p api.BuildPayload) (api.BuildResult, error) {
	b.init()
	if err := validateBuild(&p); err != nil {
		return api.BuildResult{}, err
	}
	lg := b.newLog(p)
	defer lg.close()

	select {
	case b.slot <- struct{}{}:
	default:
		lg.line("atoll-agent: waiting for another build on this server to finish")
		qt := time.NewTimer(time.Duration(p.QueueSec) * time.Second)
		select {
		case b.slot <- struct{}{}:
			qt.Stop()
		case <-qt.C:
			return api.BuildResult{}, fmt.Errorf("another build kept this server's build slot busy for %s", time.Duration(p.QueueSec)*time.Second)
		case <-ctx.Done():
			qt.Stop()
			return api.BuildResult{}, ctx.Err()
		}
	}
	b.bstate.actMu.Lock()
	b.bstate.active = p.ServiceID
	b.bstate.actMu.Unlock()
	defer func() {
		b.bstate.actMu.Lock()
		b.bstate.active = ""
		b.bstate.actMu.Unlock()
		<-b.slot
	}()

	timeout := time.Duration(p.TimeoutSec) * time.Second
	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := b.build(bctx, p, lg)
	if err != nil {
		if errors.Is(bctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("build timed out after %s", timeout)
		}
		lg.line("atoll-agent: " + lg.redact(err.Error()))
		b.Log.Warn("build failed", "service", p.ServiceID, "deployment", p.DeploymentID, "err", lg.redact(err.Error()))
		return api.BuildResult{}, errors.New(lg.redact(err.Error()))
	}
	b.prune(context.WithoutCancel(ctx), p.ServiceID, p.Keep, res.Image, lg)
	b.Log.Info("built", "service", p.ServiceID, "image", res.Image, "commit", res.Commit)
	return res, nil
}

func (b *Builder) run(ctx context.Context, lg *buildLog, dir string, env []string, out func(string), name string, args ...string) error {
	lg.line("$ " + name + " " + strings.Join(args, " "))
	if out == nil {
		out = lg.line
	}
	if err := b.Cmd.Run(ctx, dir, env, out, name, args...); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := fmt.Sprintf("%s %s: %v", name, args[0], err)
		if t := lg.lastLines(); t != "" {
			msg += "\n" + t
		}
		return errors.New(msg)
	}
	return nil
}

func (b *Builder) build(ctx context.Context, p api.BuildPayload, lg *buildLog) (api.BuildResult, error) {
	if err := b.checkBuildDisk(); err != nil {
		return api.BuildResult{}, err
	}
	if err := os.MkdirAll(b.Paths.BuildsDir(), 0o700); err != nil {
		return api.BuildResult{}, err
	}
	dir, err := os.MkdirTemp(b.Paths.BuildsDir(), p.ServiceID+"-")
	if err != nil {
		return api.BuildResult{}, err
	}
	defer os.RemoveAll(dir)

	// The token never appears in argv: git reads it from GIT_CONFIG_* (as actions/checkout does);
	// a deploy key sits in a 0600 file of a private dir removed after the fetch (build_git.go).
	remote, err := parseRemote(p.Repo)
	if err != nil {
		return api.BuildResult{}, err
	}
	secDir, err := os.MkdirTemp(b.Paths.BuildsDir(), "sec-")
	if err != nil {
		return api.BuildResult{}, err
	}
	defer os.RemoveAll(secDir)
	env, _, err := b.gitAccess(ctx, buildAuth(p), remote, secDir)
	if err != nil {
		return api.BuildResult{}, err
	}
	ref := "refs/heads/" + p.Branch
	if p.Commit != "" {
		ref = p.Commit
	}
	git, err := b.gitCommand(ctx, lg, dir, env)
	if err != nil {
		return api.BuildResult{}, err
	}
	if err := git(nil, "init", "-q"); err != nil {
		return api.BuildResult{}, err
	}
	err = git(nil, "fetch", "--depth", "1", "--no-tags", remote.fetchURL, ref)
	os.RemoveAll(secDir) // the key is not needed after the fetch
	if err != nil {
		return api.BuildResult{}, fmt.Errorf("fetching %s from %s failed (private repository without a valid token or deploy key, or unknown branch/commit): %w", ref, p.Repo, err)
	}
	if err := git(nil, "-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"); err != nil {
		return api.BuildResult{}, err
	}
	var sha string
	if err := git(func(s string) {
		if t := strings.TrimSpace(s); buildCommitRe.MatchString(t) {
			sha = t
		}
		lg.line(s)
	}, "rev-parse", "HEAD"); err != nil {
		return api.BuildResult{}, err
	}
	if sha == "" {
		return api.BuildResult{}, errors.New("git rev-parse HEAD returned no commit")
	}
	if p.Commit != "" && sha != p.Commit {
		return api.BuildResult{}, fmt.Errorf("fetched commit %s, wanted %s", sha, p.Commit)
	}

	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return api.BuildResult{}, err
	}
	inside := func(rel string) (string, error) {
		pth, err := filepath.EvalSymlinks(filepath.Join(root, rel))
		if err != nil {
			return "", err
		}
		if pth != root && !strings.HasPrefix(pth, root+string(filepath.Separator)) {
			return "", fmt.Errorf("%s points outside the repository", rel)
		}
		return pth, nil
	}
	ctxDir, err := inside(p.Context)
	if err != nil {
		return api.BuildResult{}, fmt.Errorf("build context %q not found in %s@%s", p.Context, p.Repo, sha[:12])
	}
	dfRel := filepath.Join(p.Context, p.Dockerfile)
	dockerfile, err := inside(dfRel)
	if st, serr := os.Stat(dockerfile); err != nil || serr != nil || st.IsDir() {
		return api.BuildResult{}, fmt.Errorf("no Dockerfile at %s in %s@%s. Builds without a Dockerfile (automatic detection such as Railpack/Nixpacks) are not supported yet: add a Dockerfile, or set git.dockerfile / git.context to where it is", dfRel, p.Repo, sha[:12])
	}

	tag := BuildImageTag(p.ServiceID, sha)
	args := []string{"build", "--progress=plain", "-f", dockerfile, "-t", tag,
		"--label", BuildLabel + "=" + p.ServiceID, "--label", "atoll.build.commit=" + sha}
	keys := make([]string, 0, len(p.BuildArgs))
	for k := range p.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+p.BuildArgs[k])
	}
	args = append(args, ctxDir)
	builder := b.dedicatedBuilder(ctx, lg)
	if builder != "" {
		// same flags through buildx, in the capped builder, loaded back into Docker
		args = append([]string{"buildx", "build", "--builder", builder, "--load"}, args[1:]...)
	}
	if builder != "" {
		b.bstate.mu.Lock()
		b.bstate.stopped = false // buildx starts a stopped builder for the build
		b.bstate.mu.Unlock()
	}
	err = b.run(ctx, lg, dir, []string{"DOCKER_BUILDKIT=1"}, nil, "docker", args...)
	b.pruneBuildCache(context.WithoutCancel(ctx), builder)
	if builder != "" {
		b.bstate.mu.Lock()
		b.bstate.lastUsed = b.now()
		b.bstate.mu.Unlock()
	}
	if err != nil {
		return api.BuildResult{}, err
	}
	if _, err := b.RT.ImageInfo(ctx, tag); err != nil {
		return api.BuildResult{}, fmt.Errorf("docker build finished but %s is missing: %w", tag, err)
	}
	return api.BuildResult{Image: tag, Commit: sha}, nil
}

// gitCommand returns how this build runs git: the host's git when installed, otherwise the
// same git commands in a throwaway container (GitImage, pulled on first use) with the checkout
// bind-mounted at /src and run as the agent's uid:gid. The environment (including the token
// header) reaches the container as `-e NAME` without a value, so docker copies it from its
// own environment and the token never appears in argv or in the logged command line.
func (b *Builder) gitCommand(ctx context.Context, lg *buildLog, dir string, env []string) (func(out func(string), args ...string) error, error) {
	if b.HasCommand("git") {
		return func(out func(string), args ...string) error {
			return b.run(ctx, lg, dir, env, out, "git", args...)
		}, nil
	}
	lg.line("atoll-agent: git is not installed on this server; running git in a container (" + b.GitImage + ")")
	if _, err := b.RT.ImageInfo(ctx, b.GitImage); err != nil {
		if !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("git is not installed on this server and checking the git container image %s failed: %w", b.GitImage, err)
		}
		if err := b.run(ctx, lg, "", nil, nil, "docker", "pull", b.GitImage); err != nil {
			return nil, fmt.Errorf("git is not installed on this server and the git container image %s could not be pulled (install git, or make the image reachable): %w", b.GitImage, err)
		}
	}
	base := []string{"run", "--rm", "--pull", "never", "--security-opt", "no-new-privileges",
		"-v", dir + ":/src", "-w", "/src", "-u", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "-e", "HOME=/tmp"}
	base = append(base, containerMounts(env)...) // deploy key / known hosts (build_git.go)
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && name != "" {
			base = append(base, "-e", name)
		}
	}
	base = append(base, "--entrypoint", "git", b.GitImage)
	return func(out func(string), args ...string) error {
		full := append(append([]string(nil), base...), args...)
		if err := b.run(ctx, lg, dir, env, out, "docker", full...); err != nil {
			return fmt.Errorf("git %s (in container %s): %w", args[0], b.GitImage, err)
		}
		return nil
	}, nil
}

// prune keeps the newest keep build images of a service, plus the one just built and the
// one its container should run; older ones are removed (best effort).
func (b *Builder) prune(ctx context.Context, serviceID string, keep int, built string, lg *buildLog) {
	imgs, err := b.RT.ListImages(ctx, BuildImageRepo+strings.ToLower(serviceID))
	if err != nil {
		b.Log.Warn("list build images", "service", serviceID, "err", err)
		return
	}
	sort.SliceStable(imgs, func(i, j int) bool {
		if !imgs[i].Created.Equal(imgs[j].Created) {
			return imgs[i].Created.After(imgs[j].Created)
		}
		return imgs[i].Ref > imgs[j].Ref
	})
	pinned := map[string]bool{built: true}
	if b.CurrentImage != nil {
		if cur := b.CurrentImage(serviceID); cur != "" {
			pinned[cur] = true
		}
	}
	kept := 0
	for _, im := range imgs {
		if pinned[im.Ref] || kept < keep {
			kept++
			continue
		}
		if err := b.RT.RemoveImage(ctx, im.Ref); err != nil && !errors.Is(err, ErrNotFound) {
			b.Log.Warn("remove old build image", "image", im.Ref, "err", err)
			continue
		}
		if lg != nil {
			lg.line("atoll-agent: removed old build image " + im.Ref)
		}
	}
}
