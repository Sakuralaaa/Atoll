package server

import (
	"net/http"
	"time"
)

// deploymentDone reports whether a deployment reached a final state.
func deploymentDone(status string) bool {
	return status == "running" || status == "failed" || status == "superseded"
}

// maxDeploymentWait caps ?wait= so a long poll stays under common proxy timeouts.
const maxDeploymentWait = 90 * time.Second

// handleGetDeployment returns one deployment. With ?wait=DURATION (at most 90s) it long-polls:
// it answers as soon as the deployment is running, failed or superseded, or with its current
// state when the wait runs out. Clients loop on it to wait for a deploy to finish.
func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	d, err := s.st.GetDeployment(ctx, r.PathValue("dep"))
	if err != nil || d.ServiceID != r.PathValue("id") {
		return errf(404, "not_found", "deployment %q not found for this service", r.PathValue("dep"))
	}
	var wait time.Duration
	if v := r.URL.Query().Get("wait"); v != "" {
		if wait, err = time.ParseDuration(v); err != nil || wait < 0 {
			return badRequest("wait must be a duration such as 30s")
		}
		wait = min(wait, maxDeploymentWait)
	}
	deadline := time.Now().Add(wait)
	for !deploymentDone(d.Status) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(250 * time.Millisecond):
		}
		if d, err = s.st.GetDeployment(ctx, d.ID); err != nil {
			return err
		}
	}
	writeJSON(w, 200, d)
	return nil
}
