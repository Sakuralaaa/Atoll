package agent

// MsgGitHead: the head commit of a branch (git ls-remote), for polled git services. It runs
// here, on the service's own server, with the same address pinning and credentials as a
// build, so the control server never connects to user repositories itself.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Head returns the commit refs/heads/<branch> points at.
func (b *Builder) Head(ctx context.Context, p api.GitHeadPayload) (api.GitHeadResult, error) {
	b.init()
	if err := validID(p.ServiceID); err != nil {
		return api.GitHeadResult{}, err
	}
	if !buildBranchRe.MatchString(p.Branch) || strings.HasPrefix(p.Branch, "-") || strings.Contains(p.Branch, "..") {
		return api.GitHeadResult{}, fmt.Errorf("invalid branch %q", p.Branch)
	}
	remote, err := parseRemote(p.Repo)
	if err != nil {
		return api.GitHeadResult{}, err
	}
	a := gitAuth{p.ServiceID, p.Repo, p.Provider, p.Username, p.Token, p.SSHKey, p.KnownHosts}
	lg := &buildLog{b: b, quiet: true, redact: redactor(authSecrets(a)), stop: make(chan struct{}), done: make(chan struct{})}
	close(lg.done)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := os.MkdirAll(b.Paths.BuildsDir(), 0o700); err != nil {
		return api.GitHeadResult{}, err
	}
	dir, err := os.MkdirTemp(b.Paths.BuildsDir(), "head-")
	if err != nil {
		return api.GitHeadResult{}, err
	}
	defer os.RemoveAll(dir)
	env, _, err := b.gitAccess(ctx, a, remote, dir)
	if err != nil {
		return api.GitHeadResult{}, errors.New(lg.redact(err.Error()))
	}
	git, err := b.gitCommand(ctx, lg, dir, env)
	if err != nil {
		return api.GitHeadResult{}, errors.New(lg.redact(err.Error()))
	}
	ref := "refs/heads/" + p.Branch
	var sha string
	err = git(func(line string) {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == ref && buildCommitRe.MatchString(f[0]) {
			sha = f[0]
		}
		lg.line(line)
	}, "ls-remote", "--", remote.fetchURL, ref)
	if err != nil {
		return api.GitHeadResult{}, errors.New(lg.redact(err.Error()))
	}
	if sha == "" {
		return api.GitHeadResult{}, fmt.Errorf("branch %s not found in %s", p.Branch, p.Repo)
	}
	return api.GitHeadResult{Commit: sha}, nil
}

// Detect fetches the commit a build would use and reports how it would be built (stack
// detection for repositories without a Dockerfile; build_detect.go).
func (b *Builder) Detect(ctx context.Context, p api.BuildPayload) (api.GitDetected, error) {
	return api.GitDetected{}, errors.New("stack detection is not available yet")
}
