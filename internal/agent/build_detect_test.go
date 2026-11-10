package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGeneratedDockerfiles(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		stack string
		want  []string
	}{
		"pnpm": {map[string]string{"package.json": `{"scripts":{"build":"vite build","start":"node server.js"}}`, "pnpm-lock.yaml": ""}, "node",
			[]string{"FROM node:22-alpine", "pnpm install --frozen-lockfile", "RUN pnpm run build", `CMD ["sh","-c","pnpm run start"]`, "ENV PORT=3000"}},
		"npm-main": {map[string]string{"package.json": `{"main":"app.js"}`, "package-lock.json": "{}", "app.js": ""}, "node",
			[]string{"RUN npm ci", `"exec node app.js"`}},
		"yarn-spa": {map[string]string{"package.json": `{"scripts":{"build":"react-scripts build"}}`, "yarn.lock": ""}, "node",
			[]string{"FROM node:22-alpine AS build", "yarn install", "FROM caddy:2-alpine", "COPY --from=build /out /srv", "file-server"}},
		"bun": {map[string]string{"package.json": `{"scripts":{"start":"bun run src/index.ts"}}`, "bun.lock": ""}, "bun",
			[]string{"FROM oven/bun:1-alpine", "RUN bun install", "bun run start"}},
		"python": {map[string]string{"requirements.txt": "flask\n", "Procfile": "web: gunicorn app:app -b 0.0.0.0:$PORT\n"}, "python",
			[]string{"FROM python:3.12-slim", "pip install -r requirements.txt", "gunicorn app:app", "ENV PORT=8000"}},
		"go":     {map[string]string{"go.mod": "module x\n", "main.go": "package main"}, "go", []string{"FROM golang:1-alpine AS build", "go build", "COPY --from=build /out /app", "exec /app/app"}},
		"static": {map[string]string{"index.html": "<h1>hi</h1>"}, "static", []string{"FROM caddy:2-alpine", "COPY . /srv"}},
	} {
		t.Run(name, func(t *testing.T) {
			b, _, c, ev, _ := newBuilder(t)
			c.files = tc.files
			pl := buildPayload()
			pl.Builder = "auto"
			res, err := b.Build(context.Background(), pl)
			if err != nil {
				t.Fatal(err)
			}
			if res.Detected == nil || res.Detected.Stack != tc.stack || res.Detected.Commit != testSHA {
				t.Fatalf("detected %+v", res.Detected)
			}
			if len(c.dockerfiles) != 1 {
				t.Fatalf("docker builds %d", len(c.dockerfiles))
			}
			df := c.dockerfiles[0]
			for _, w := range tc.want {
				if !strings.Contains(df, w) {
					t.Errorf("generated Dockerfile lacks %q:\n%s", w, df)
				}
			}
			if df != res.Detected.Dockerfile || !strings.Contains(ev.all(), "generated Dockerfile") {
				t.Fatal("detected dockerfile / log mismatch")
			}
			build := c.callsOf("docker", "build")
			for i, a := range build[0].args {
				if a == "-f" {
					if _, err := os.Stat(build[0].args[i+1]); !os.IsNotExist(err) {
						t.Fatalf("generated Dockerfile left behind: %v", err)
					}
					if strings.HasPrefix(build[0].args[i+1], build[0].args[len(build[0].args)-1]) {
						t.Fatal("generated Dockerfile written into the checkout")
					}
				}
			}
		})
	}
}

func TestBuildGeneratedOverridesAndPort(t *testing.T) {
	b, _, c, _, _ := newBuilder(t)
	c.files = map[string]string{"Dockerfile": "FROM scratch\n", "package.json": `{}`}
	pl := buildPayload()
	pl.Builder, pl.Stack, pl.Port = "generated", "node", 4000
	pl.InstallCommand, pl.BuildCommand, pl.StartCommand = "npm i --omit=dev", "npm run compile", "node dist/main.js"
	res, err := b.Build(context.Background(), pl)
	if err != nil || res.Detected == nil || res.Detected.Reason != "git.stack" {
		t.Fatalf("%+v %v", res.Detected, err)
	}
	df := c.dockerfiles[0]
	for _, w := range []string{"RUN npm i --omit=dev", "RUN npm run compile", `"node dist/main.js"`, "ENV PORT=4000", "EXPOSE 4000"} {
		if !strings.Contains(df, w) {
			t.Errorf("lacks %q:\n%s", w, df)
		}
	}
}

func TestBuildDockerfileModes(t *testing.T) {
	b, _, c, _, _ := newBuilder(t)
	c.files = map[string]string{"Dockerfile": "FROM scratch\n", "package.json": `{"scripts":{"start":"x"}}`}
	pl := buildPayload()
	pl.Builder = "auto"
	res, err := b.Build(context.Background(), pl)
	if err != nil || res.Detected == nil || res.Detected.Stack != "" || res.Detected.Dockerfile != "Dockerfile" || c.dockerfiles[0] != "FROM scratch\n" {
		t.Fatalf("auto with Dockerfile: %+v %v", res.Detected, err)
	}

	b, _, c, _, _ = newBuilder(t)
	c.files = map[string]string{"package.json": `{"scripts":{"start":"x"}}`}
	pl.Builder = "dockerfile"
	if _, err := b.Build(context.Background(), pl); err == nil || !strings.Contains(err.Error(), "git.builder to auto") {
		t.Fatalf("dockerfile mode: %v", err)
	}
	pl.Builder = "" // payload of an older control server: old behaviour and message
	if _, err := b.Build(context.Background(), pl); err == nil || !strings.Contains(err.Error(), "not supported yet") {
		t.Fatalf("legacy: %v", err)
	}
	c.files = map[string]string{"README.md": "hi"}
	pl.Builder = "auto"
	if _, err := b.Build(context.Background(), pl); err == nil || !strings.Contains(err.Error(), "no known stack") {
		t.Fatalf("unknown stack: %v", err)
	}
	pl.StartCommand = "a\nRUN evil"
	if _, err := b.Build(context.Background(), pl); err == nil || !strings.Contains(err.Error(), "one line") {
		t.Fatalf("multi-line command: %v", err)
	}
}

func TestDetectOnly(t *testing.T) {
	b, _, c, ev, p := newBuilder(t)
	c.files = map[string]string{"go.mod": "module x\n"}
	pl := buildPayload()
	det, err := b.Detect(context.Background(), pl)
	if err != nil || det.Stack != "go" || det.Reason != "go.mod" || !strings.Contains(det.Dockerfile, "golang") || det.Commit != testSHA {
		t.Fatalf("%+v %v", det, err)
	}
	if n := len(c.callsOf("docker", "build")); n != 0 {
		t.Fatalf("detect built: %d", n)
	}
	if ev.all() != "" {
		t.Fatalf("detect emitted logs: %q", ev.all())
	}
	left, _ := filepath.Glob(filepath.Join(p.BuildsDir(), "*"))
	if len(left) != 0 {
		t.Fatalf("detect left %v", left)
	}
}
