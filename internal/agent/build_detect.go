package agent

// Builds without a Dockerfile: the repository's stack is detected (Node with npm/pnpm/yarn,
// Bun, Python, Go, a static site) and a Dockerfile is generated into a temp file outside the
// checkout. The build then runs exactly like a Dockerfile build (same limits, disk check and
// cache pruning). git.stack and the install/build/start commands override detection.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Base images of generated Dockerfiles.
const (
	genNodeImage   = "node:22-alpine"
	genBunImage    = "oven/bun:1-alpine"
	genPythonImage = "python:3.12-slim"
	genGoImage     = "golang:1-alpine"
	genRunImage    = "alpine:3.20"
	genStaticImage = "caddy:2-alpine"
)

// dockerfileFor returns the build context and the Dockerfile to build with: the repository's
// own (per p.Builder) or a generated one (a temp file the caller removes; detected.Stack != "").
// detected is nil for payloads of older control servers (no Builder).
func (b *Builder) dockerfileFor(p api.BuildPayload, root, sha string, lg *buildLog) (ctxDir, dockerfile string, detected *api.GitDetected, err error) {
	ctxDir, dockerfile, hasDF, err := repoPaths(p, root, sha)
	if err != nil {
		return "", "", nil, err
	}
	dfRel := filepath.Join(p.Context, p.Dockerfile)
	switch {
	case p.Builder == "" && !hasDF:
		return "", "", nil, fmt.Errorf("no Dockerfile at %s in %s@%s. Builds without a Dockerfile (automatic detection such as Railpack/Nixpacks) are not supported yet: add a Dockerfile, or set git.dockerfile / git.context to where it is", dfRel, p.Repo, sha[:12])
	case p.Builder == "":
		return ctxDir, dockerfile, nil, nil
	case p.Builder == "dockerfile" && !hasDF:
		return "", "", nil, fmt.Errorf("no Dockerfile at %s in %s@%s: add one, fix git.dockerfile / git.context, or set git.builder to auto to generate one", dfRel, p.Repo, sha[:12])
	case hasDF && p.Builder != "generated":
		return ctxDir, dockerfile, &api.GitDetected{Reason: "Dockerfile at " + dfRel, Dockerfile: dfRel, Commit: sha}, nil
	}
	det, content, err := detectStack(ctxDir, p)
	if err != nil {
		return "", "", nil, fmt.Errorf("%w (in %s@%s)", err, p.Repo, sha[:12])
	}
	det.Commit = sha
	f, err := os.CreateTemp(b.Paths.BuildsDir(), "Dockerfile-"+p.ServiceID+"-")
	if err != nil {
		return "", "", nil, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", "", nil, err
	}
	f.Close()
	lg.line(fmt.Sprintf("atoll-agent: building without a Dockerfile: %s (%s); generated Dockerfile:", det.Stack, det.Reason))
	for _, l := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		lg.line("  " + l)
	}
	return ctxDir, f.Name(), det, nil
}

// repoPaths resolves the build context and the repo's Dockerfile path inside the checkout.
func repoPaths(p api.BuildPayload, root, sha string) (ctxDir, dockerfile string, hasDF bool, err error) {
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
	if ctxDir, err = inside(p.Context); err != nil {
		return "", "", false, fmt.Errorf("build context %q not found in %s@%s", p.Context, p.Repo, sha[:12])
	}
	dockerfile, derr := inside(filepath.Join(p.Context, p.Dockerfile))
	st, serr := os.Stat(dockerfile)
	return ctxDir, dockerfile, derr == nil && serr == nil && !st.IsDir(), nil
}

type pkgJSON struct {
	Main    string            `json:"main"`
	Scripts map[string]string `json:"scripts"`
}

// detectStack picks the stack of dir (or p.Stack) and returns the generated Dockerfile.
func detectStack(dir string, p api.BuildPayload) (*api.GitDetected, string, error) {
	has := func(name string) bool {
		st, err := os.Lstat(filepath.Join(dir, name))
		return err == nil && st.Mode().IsRegular()
	}
	stack, reason := p.Stack, "git.stack"
	if stack == "" {
		switch {
		case has("bun.lockb") || has("bun.lock"):
			stack, reason = "bun", "bun lockfile"
		case has("package.json"):
			stack, reason = "node", "package.json"
		case has("go.mod"):
			stack, reason = "go", "go.mod"
		case has("requirements.txt"):
			stack, reason = "python", "requirements.txt"
		case has("pyproject.toml"):
			stack, reason = "python", "pyproject.toml"
		case has("index.html"):
			stack, reason = "static", "index.html"
		default:
			return nil, "", errors.New("no Dockerfile and no known stack (package.json, bun.lock, go.mod, requirements.txt, pyproject.toml, index.html): add a Dockerfile, or set git.stack and git.start_command")
		}
	}
	port := p.Port
	if port <= 0 {
		port = map[string]int{"node": 3000, "bun": 3000, "python": 8000}[stack]
		if port == 0 {
			port = 8080
		}
	}
	g := &dfGen{port: port}
	var err error
	switch stack {
	case "node", "bun":
		reason, err = g.node(dir, p, stack, reason, has)
	case "python":
		reason, err = g.python(dir, p, reason, has)
	case "go":
		g.goApp(p)
	case "static":
		g.static()
	default:
		return nil, "", fmt.Errorf("unknown stack %q", stack)
	}
	if err != nil {
		return nil, "", err
	}
	return &api.GitDetected{Stack: stack, Reason: reason, Dockerfile: g.String()}, g.String(), nil
}

// dfGen writes a Dockerfile.
type dfGen struct {
	b    strings.Builder
	port int
}

func (g *dfGen) String() string { return g.b.String() }
func (g *dfGen) l(format string, a ...any) {
	fmt.Fprintf(&g.b, format+"\n", a...)
}
func (g *dfGen) run(cmd string) {
	if cmd != "" {
		g.l("RUN %s", cmd)
	}
}
func (g *dfGen) cmd(start string) {
	raw, _ := json.Marshal([]string{"sh", "-c", start})
	g.l("ENV PORT=%d", g.port)
	g.l("EXPOSE %d", g.port)
	g.l("CMD %s", raw)
}

// staticStage serves /out of the build stage with Caddy on $PORT.
func (g *dfGen) staticFrom(stage string) {
	g.l("FROM %s", genStaticImage)
	g.l("COPY --from=%s /out /srv", stage)
	g.cmd("exec caddy file-server --root /srv --listen :$PORT")
}

func (g *dfGen) node(dir string, p api.BuildPayload, stack, reason string, has func(string) bool) (string, error) {
	var pkg pkgJSON
	if raw, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		if err := json.Unmarshal(raw, &pkg); err != nil {
			return "", fmt.Errorf("package.json: %v", err)
		}
	}
	pm, install := "npm", "npm install"
	switch {
	case stack == "bun":
		pm, install = "bun", "bun install"
	case has("pnpm-lock.yaml"):
		pm, install, reason = "pnpm", "corepack enable && pnpm install --frozen-lockfile", reason+" with pnpm-lock.yaml"
	case has("yarn.lock"):
		pm, install, reason = "yarn", "corepack enable && yarn install", reason+" with yarn.lock"
	case has("package-lock.json") || has("npm-shrinkwrap.json"):
		install, reason = "npm ci", reason+" with package-lock.json"
	}
	if p.InstallCommand != "" {
		install = p.InstallCommand
	}
	build := p.BuildCommand
	if build == "" && pkg.Scripts["build"] != "" {
		build = pm + " run build"
	}
	start := p.StartCommand
	if start == "" && pkg.Scripts["start"] != "" {
		start = pm + " run start"
	}
	run := "node"
	if stack == "bun" {
		run = "bun"
	}
	if start == "" {
		cands := []string{pkg.Main, "server.js", "index.js", "app.js", "main.js"}
		if stack == "bun" {
			cands = append(cands, "index.ts", "server.ts", "src/index.ts")
		}
		for _, f := range cands {
			if f != "" && !strings.HasPrefix(f, "-") && !strings.ContainsAny(f, " '\"`$;&|\\") && has(f) {
				start = "exec " + run + " " + f
				break
			}
		}
	}
	image := genNodeImage
	if stack == "bun" {
		image = genBunImage
	}
	if start == "" && build != "" {
		// a front-end without a server: serve the build output (dist/, build/, out/ or public/)
		g.l("FROM %s AS build", image)
		g.l("WORKDIR /app")
		g.l("COPY . .")
		g.run(install)
		g.run(build)
		g.run(`mkdir -p /out && for d in dist build out public; do if [ -d "$d" ]; then cp -a "$d"/. /out/; break; fi; done && [ -n "$(ls -A /out)" ] || (echo "no build output in dist/, build/, out/ or public/; set git.start_command" >&2; exit 1)`)
		g.staticFrom("build")
		return reason + ", static build output", nil
	}
	if start == "" {
		return "", errors.New("no start script, main file or build script in package.json: set git.start_command")
	}
	g.l("FROM %s", image)
	g.l("WORKDIR /app")
	g.l("COPY . .")
	g.run(install)
	g.run(build)
	g.l("ENV NODE_ENV=production")
	g.cmd(start)
	return reason, nil
}

func (g *dfGen) python(dir string, p api.BuildPayload, reason string, has func(string) bool) (string, error) {
	install := p.InstallCommand
	if install == "" {
		switch {
		case has("requirements.txt"):
			install = "pip install -r requirements.txt"
		case has("pyproject.toml"):
			install = "pip install ."
		}
	}
	start := p.StartCommand
	if start == "" && has("Procfile") {
		raw, _ := os.ReadFile(filepath.Join(dir, "Procfile"))
		for _, l := range strings.Split(string(raw), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "web:"); ok && strings.TrimSpace(v) != "" {
				start, reason = strings.TrimSpace(v), reason+", Procfile web"
				break
			}
		}
	}
	if start == "" {
		for _, f := range []string{"main.py", "app.py", "server.py"} {
			if has(f) {
				start = "exec python " + f
				break
			}
		}
	}
	if start == "" {
		return "", errors.New("python project without a Procfile web line, main.py or app.py: set git.start_command")
	}
	g.l("FROM %s", genPythonImage)
	g.l("ENV PYTHONUNBUFFERED=1 PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1")
	g.l("WORKDIR /app")
	g.l("COPY . .")
	g.run(install)
	g.run(p.BuildCommand)
	g.cmd(start)
	return reason, nil
}

func (g *dfGen) goApp(p api.BuildPayload) {
	install := p.InstallCommand
	if install == "" {
		install = "go mod download"
	}
	build := p.BuildCommand
	if build == "" {
		build = "CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/app ."
	}
	start := p.StartCommand
	if start == "" {
		start = "exec /app/app"
	}
	g.l("FROM %s AS build", genGoImage)
	g.l("ENV GOTOOLCHAIN=auto")
	g.l("WORKDIR /src")
	g.l("COPY . .")
	g.run(install)
	g.run("mkdir -p /out && " + build)
	g.l("FROM %s", genRunImage)
	g.run("apk add --no-cache ca-certificates tzdata")
	g.l("WORKDIR /app")
	g.l("COPY --from=build /out /app")
	g.cmd(start)
}

func (g *dfGen) static() {
	g.l("FROM %s", genStaticImage)
	g.l("COPY . /srv")
	g.cmd("exec caddy file-server --root /srv --listen :$PORT")
}

// Detect fetches the commit a build would use and reports how it would be built, without
// building (MsgBuildDetect: the panel's preview).
func (b *Builder) Detect(ctx context.Context, p api.BuildPayload) (api.GitDetected, error) {
	b.init()
	if p.Builder == "" {
		p.Builder = "auto"
	}
	if err := validateBuild(&p); err != nil {
		return api.GitDetected{}, err
	}
	lg := &buildLog{b: b, quiet: true, redact: redactor(authSecrets(buildAuth(p))), stop: make(chan struct{}), done: make(chan struct{})}
	close(lg.done)
	if p.QueueSec > 120 {
		p.QueueSec = 120
	}
	release, err := b.acquire(ctx, p, lg)
	if err != nil {
		return api.GitDetected{}, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	root, sha, cleanup, err := b.checkout(ctx, p, lg)
	defer cleanup()
	if err != nil {
		return api.GitDetected{}, errors.New(lg.redact(err.Error()))
	}
	ctxDir, _, hasDF, err := repoPaths(p, root, sha)
	if err != nil {
		return api.GitDetected{}, err
	}
	if hasDF && p.Builder != "generated" {
		rel := filepath.Join(p.Context, p.Dockerfile)
		return api.GitDetected{Reason: "Dockerfile at " + rel, Dockerfile: rel, Commit: sha}, nil
	}
	if p.Builder == "dockerfile" {
		return api.GitDetected{}, fmt.Errorf("no Dockerfile at %s", filepath.Join(p.Context, p.Dockerfile))
	}
	det, _, err := detectStack(ctxDir, p)
	if err != nil {
		return api.GitDetected{}, err
	}
	det.Commit = sha
	return *det, nil
}
