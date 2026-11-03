package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/client"
)

// readyFailedError is a deploy that finished but did not come up (--wait-ready exits 4).
type readyFailedError struct{ msg string }

func (e *readyFailedError) Error() string { return e.msg }

// pollEvery is how often waitReady looks for the new deployment (tests shorten it).
var pollEvery = time.Second

// readyWaiter remembers a service's newest deployment before an action, so that afterwards
// it can wait for the deployment the action started (no clock comparison between the CLI
// and the server needed).
type readyWaiter struct {
	app    *App
	c      *client.Client
	svc    *api.Service
	prevID string
}

// beforeDeploy is called before an action that starts a deployment. With --wait-ready off it
// does nothing and its wait returns at once.
func (app *App) beforeDeploy(ctx context.Context, c *client.Client, svc *api.Service) (*readyWaiter, error) {
	w := &readyWaiter{app: app, c: c, svc: svc}
	if !app.WaitReady || svc == nil {
		return w, nil
	}
	deps, err := c.ListDeployments(ctx, svc.ID)
	if err != nil {
		return nil, err
	}
	if len(deps) > 0 {
		w.prevID = deps[0].ID
	}
	return w, nil
}

// onlyIfDeployed turns the wait off for a service that has never been deployed (an env
// change starts no deployment for it).
func (w *readyWaiter) onlyIfDeployed() {
	if w.prevID == "" {
		w.svc = nil
	}
}

// wait blocks until the deployment started after beforeDeploy is running (nil), or failed or
// was superseded (a readyFailedError carrying the reason and the last build log lines).
func (w *readyWaiter) wait(ctx context.Context) (*api.Deployment, error) {
	app := w.app
	if !app.WaitReady || w.svc == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, app.ReadyTimeout)
	defer cancel()
	timeout := func(err error) error {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%s: not ready after %s (--ready-timeout)", w.svc.Name, app.ReadyTimeout)
		}
		return err
	}

	var dep api.Deployment
	for {
		deps, err := w.c.ListDeployments(ctx, w.svc.ID)
		if err != nil {
			return nil, timeout(err)
		}
		if len(deps) > 0 && deps[0].ID != w.prevID {
			dep = deps[0]
			break
		}
		select {
		case <-ctx.Done():
			return nil, timeout(ctx.Err())
		case <-time.After(pollEvery):
		}
	}

	last := ""
	for {
		if dep.Status != last && !app.JSON {
			fmt.Fprintf(app.Err, "%s: %s %s\n", w.svc.Name, dep.ID, dep.Status)
			last = dep.Status
		}
		if dep.Status == "superseded" {
			// one command can start several deployments (env, then image); follow the newest
			deps, err := w.c.ListDeployments(ctx, w.svc.ID)
			if err != nil {
				return nil, timeout(err)
			}
			if len(deps) > 0 && deps[0].ID != dep.ID {
				dep = deps[0]
				continue
			}
		}
		if client.DeploymentDone(dep.Status) {
			break
		}
		d, err := w.c.GetDeployment(ctx, w.svc.ID, dep.ID, 30*time.Second)
		if err != nil {
			return nil, timeout(err)
		}
		dep = *d
	}
	if dep.Status == "running" {
		return &dep, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: deployment %s %s", w.svc.Name, dep.ID, dep.Status)
	if dep.Message != "" {
		b.WriteString(": " + dep.Message)
	}
	// a failed build: its own log has the reason; the message only says the build failed
	if lg, err := w.c.DeploymentLogs(ctx, w.svc.ID, dep.ID, 0); err == nil && len(lg.Lines) > 0 && dep.Status == "failed" {
		lines := lg.Lines
		if len(lines) > 20 {
			lines = lines[len(lines)-20:]
		}
		b.WriteString("\nlast build log lines:")
		for _, l := range lines {
			b.WriteString("\n  " + l.Line)
		}
	}
	return &dep, &readyFailedError{msg: b.String()}
}

// waitRunning waits for a start or restart (no new deployment) to leave the service running.
func (app *App) waitRunning(ctx context.Context, c *client.Client, svc *api.Service) error {
	if !app.WaitReady {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, app.ReadyTimeout)
	defer cancel()
	// give the agent's next status report a moment so a stale "running" is not taken as the answer
	select {
	case <-ctx.Done():
	case <-time.After(pollEvery):
	}
	for {
		s, err := c.GetService(ctx, svc.ID)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("%s: not running after %s (--ready-timeout)", svc.Name, app.ReadyTimeout)
			}
			return err
		}
		switch s.Status {
		case "running":
			return nil
		case "failed":
			return &readyFailedError{msg: fmt.Sprintf("%s: failed to start", svc.Name)}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: not running after %s (--ready-timeout), status %s", svc.Name, app.ReadyTimeout, s.Status)
		case <-time.After(pollEvery):
		}
	}
}

// waitDeploymentTool backs the MCP wait_deployment tool: it waits for depID (default the
// newest deployment, following newer ones when it is superseded) to finish, at most timeoutSec.
func waitDeploymentTool(ctx context.Context, c *client.Client, svc *api.Service, depID string, timeoutSec int) (any, error) {
	if timeoutSec <= 0 || timeoutSec > 900 {
		timeoutSec = 300
	}
	follow := depID == ""
	newest := func(ctx context.Context) (*api.Deployment, error) {
		deps, err := c.ListDeployments(ctx, svc.ID)
		if err != nil {
			return nil, err
		}
		if len(deps) == 0 {
			return nil, fmt.Errorf("%s has no deployments", svc.Name)
		}
		return &deps[0], nil
	}
	var dep *api.Deployment
	var err error
	if follow {
		if dep, err = newest(ctx); err != nil {
			return nil, err
		}
	} else if dep, err = c.GetDeployment(ctx, svc.ID, depID, 0); err != nil {
		return nil, err
	}
	wctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	for {
		if dep.Status == "superseded" && follow {
			if n, err := newest(wctx); err == nil && n.ID != dep.ID {
				dep = n
				continue
			}
		}
		if client.DeploymentDone(dep.Status) {
			break
		}
		d, err := c.GetDeployment(wctx, svc.ID, dep.ID, 30*time.Second)
		if err != nil {
			if wctx.Err() != nil {
				return map[string]any{"deployment": dep, "done": false, "note": fmt.Sprintf("still %s after %ds; call again to keep waiting", dep.Status, timeoutSec)}, nil
			}
			return nil, err
		}
		dep = d
	}
	out := map[string]any{"deployment": dep, "done": true}
	if dep.Status == "failed" {
		if lg, err := c.DeploymentLogs(ctx, svc.ID, dep.ID, 0); err == nil && len(lg.Lines) > 0 {
			lines := lg.Lines
			if len(lines) > 30 {
				lines = lines[len(lines)-30:]
			}
			tail := make([]string, len(lines))
			for i, l := range lines {
				tail[i] = l.Line
			}
			out["build_log_tail"] = tail
		}
	}
	return out, nil
}
