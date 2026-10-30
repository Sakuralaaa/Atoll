package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// stepper keeps a []ImportStep up to date and persists it after each change.
type stepper struct {
	steps *[]api.ImportStep
	save  func()
}

func (st *stepper) idx(name string) int {
	for i, s := range *st.steps {
		if s.Name == name {
			return i
		}
	}
	*st.steps = append(*st.steps, api.ImportStep{Name: name, Status: "pending"})
	return len(*st.steps) - 1
}

func (st *stepper) set(name, status, detail string) {
	i := st.idx(name)
	(*st.steps)[i].Status = status
	(*st.steps)[i].Detail = detail
	st.save()
}

func (st *stepper) run(name string)             { st.set(name, "running", "") }
func (st *stepper) done(name, detail string)    { st.set(name, "done", detail) }
func (st *stepper) fail(name string, err error) { st.set(name, "failed", err.Error()) }
func (st *stepper) skip(name, why string)       { st.set(name, "skipped", why) }

// do runs fn as a named step.
func (st *stepper) do(name string, fn func() (string, error)) error {
	st.run(name)
	detail, err := fn()
	if err != nil {
		st.fail(name, err)
		return err
	}
	st.done(name, detail)
	return nil
}

// waitDeployments waits until every deployment is running (nil) or one fails.
func (s *Server) waitDeployments(ctx context.Context, ids []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pending := 0
		for _, id := range ids {
			d, err := s.st.GetDeployment(ctx, id)
			if err != nil {
				return err
			}
			switch d.Status {
			case "running":
			case "failed", "superseded":
				return fmt.Errorf("deployment %s %s: %s", d.ID, d.Status, d.Message)
			default:
				pending++
			}
		}
		if pending == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d deployment(s) not running after %s", pending, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// waitHealthy waits for the agent to report each service's container as
// running (and not unhealthy) in a status frame received after `since`.
func (s *Server) waitHealthy(ctx context.Context, serviceIDs []string, since time.Time, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		bad := ""
		for _, id := range serviceIDs {
			c, at, err := s.st.ContainerState(ctx, id)
			if err != nil || at.Before(since) {
				bad = id + ": no status reported yet"
				break
			}
			if c.State != "running" || c.Health == "unhealthy" || c.Health == "starting" {
				bad = fmt.Sprintf("%s: state=%s health=%s", id, c.State, c.Health)
				break
			}
		}
		if bad == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not healthy after %s (%s)", timeout, bad)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *Server) removeBlob(id string) { os.Remove(s.blobPath(id)) }
