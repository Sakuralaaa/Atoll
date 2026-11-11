package server

// POST /services/{id}/git/detect: preview how a git service builds without a Dockerfile.
// The service's server fetches the branch head and detects the stack (MsgBuildDetect); the
// body may override builder, stack and the commands to preview them before saving.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

type gitDetectRequest struct {
	Builder        *string `json:"builder"`
	Stack          *string `json:"stack"`
	InstallCommand *string `json:"install_command"`
	BuildCommand   *string `json:"build_command"`
	StartCommand   *string `json:"start_command"`
}

func (s *Server) handleGitDetect(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	svc, _, err := s.st.GetService(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	g, err := s.st.GetServiceGit(ctx, svc.ID)
	if err == store.ErrNotFound {
		return errf(409, "not_git", "service %s is not built from git", svc.Name)
	}
	if err != nil {
		return err
	}
	var req gitDetectRequest
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			return err
		}
	}
	over := false
	cur := g.GitSource
	for _, f := range []struct {
		v   *string
		dst *string
	}{{req.Builder, &cur.Builder}, {req.Stack, &cur.Stack}, {req.InstallCommand, &cur.InstallCommand},
		{req.BuildCommand, &cur.BuildCommand}, {req.StartCommand, &cur.StartCommand}} {
		if f.v != nil {
			*f.dst, over = *f.v, true
		}
	}
	if over {
		u, err := parseGitURL(cur.Repo)
		if err != nil {
			return err
		}
		if err := validateGitExtras(&cur, u); err != nil {
			return err
		}
	}
	node, err := s.serviceNode(ctx, svc)
	if err != nil {
		return err
	}
	if !s.hub.Online(node) {
		return errf(409, "server_offline", "%v; detection runs on the service's server", ErrNodeOffline)
	}
	if !s.agentBuildSources(node) {
		return errf(409, "agent_too_old", "%s", agentTooOldBuildMsg)
	}
	row := *g
	row.GitSource = cur
	b := api.BuildPayload{ServiceID: svc.ID, Repo: g.Repo, Branch: g.Branch, Context: g.Context, Dockerfile: g.Dockerfile,
		BuildArgs: g.BuildArgs, Token: g.Token, TimeoutSec: 180, QueueSec: 60, Keep: s.cfg.BuildKeep}
	port := 0
	if len(svc.Ports) > 0 {
		port = svc.Ports[0].ContainerPort
	}
	fillBuildSources(&b, &row, port)
	res, err := s.hub.Request(ctx, node, api.MsgBuildDetect, b, 5*time.Minute)
	if err != nil {
		return errf(502, "agent_error", "%v", err)
	}
	if !res.OK {
		if strings.Contains(res.Error, "unsupported message type") {
			return errf(409, "agent_too_old", "%s", agentTooOldBuildMsg)
		}
		return errf(422, "detect_failed", "%s", res.Error)
	}
	var det api.GitDetected
	if err := json.Unmarshal([]byte(res.Output), &det); err != nil {
		return errf(502, "agent_error", "agent returned no detection")
	}
	if !over {
		s.st.SetGitDetected(ctx, svc.ID, &det)
	}
	writeJSON(w, 200, det)
	return nil
}
