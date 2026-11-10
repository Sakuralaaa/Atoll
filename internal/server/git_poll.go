package server

// Polling git sources (git.poll_interval_sec): for forges whose webhooks cannot reach the
// control server. The control server never connects to user repositories; it asks the
// service's own server for the branch head (MsgGitHead, git ls-remote with the same address
// pinning as builds) and deploys a new commit with trigger "poll".

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// gitPoller keeps the polling state that does not belong in the database.
type gitPoller struct {
	tick     time.Duration // how often due polls are looked for (30s; tests shorten it)
	mu       sync.Mutex
	inflight map[string]bool
	tried    map[string]string // service -> head commit a poll already deployed (failed builds are not retried every interval)
}

func newGitPoller() *gitPoller {
	return &gitPoller{tick: 30 * time.Second, inflight: map[string]bool{}, tried: map[string]string{}}
}

func (s *Server) gitPollLoop() {
	defer s.wg.Done()
	t := time.NewTicker(s.gitPoll.tick)
	defer t.Stop()
	for {
		select {
		case <-s.bg.Done():
			return
		case <-t.C:
			s.runDueGitPolls(s.bg, time.Now())
		}
	}
}

// runDueGitPolls starts the polls whose interval has passed (each in the background).
func (s *Server) runDueGitPolls(ctx context.Context, now time.Time) {
	ids, err := s.st.PolledGitServices(ctx)
	if err != nil {
		s.log.Warn("list polled git services", "err", err)
		return
	}
	for _, id := range ids {
		g, err := s.st.GetServiceGit(ctx, id)
		if err != nil || g.PollIntervalSec <= 0 {
			continue
		}
		if !g.LastPollAt.IsZero() && now.Sub(g.LastPollAt) < time.Duration(g.PollIntervalSec)*time.Second {
			continue
		}
		s.gitPoll.mu.Lock()
		busy := s.gitPoll.inflight[id]
		if !busy {
			s.gitPoll.inflight[id] = true
		}
		s.gitPoll.mu.Unlock()
		if busy {
			continue
		}
		s.goBG(func(bg context.Context) {
			defer func() {
				s.gitPoll.mu.Lock()
				delete(s.gitPoll.inflight, id)
				s.gitPoll.mu.Unlock()
			}()
			s.pollGitService(bg, id, now)
		})
	}
}

// pollGitService checks one service's branch head and deploys it when it moved.
func (s *Server) pollGitService(ctx context.Context, svcID string, now time.Time) {
	svc, stopped, err := s.st.GetService(ctx, svcID)
	if err != nil || svc.Git == nil {
		return
	}
	node, err := s.serviceNode(ctx, svc)
	if err != nil || !s.hub.Online(node) || !s.agentBuildSources(node) {
		return // checked again next tick; an older agent cannot ls-remote
	}
	s.st.SetGitPolled(ctx, svcID, now)
	if stopped {
		return // a stopped service is not started by a push
	}
	g, err := s.st.GetServiceGit(ctx, svcID)
	if err != nil {
		return
	}
	res, err := s.hub.Request(ctx, node, api.MsgGitHead, api.GitHeadPayload{ServiceID: svcID, Repo: g.Repo, Branch: g.Branch,
		Provider: g.Provider, Username: g.Username, Token: g.Token, SSHKey: g.SSHKey, KnownHosts: g.KnownHosts}, 3*time.Minute)
	if err == nil && !res.OK {
		err = errString(agentUpgradeHint(res.Error))
	}
	var head api.GitHeadResult
	if err == nil {
		if json.Unmarshal([]byte(res.Output), &head) != nil || !commitShaRe.MatchString(head.Commit) {
			err = errString("agent returned no commit")
		}
	}
	if err != nil {
		s.log.Warn("git poll", "service", svcID, "err", err)
		return
	}
	s.gitPoll.mu.Lock()
	tried := s.gitPoll.tried[svcID]
	s.gitPoll.mu.Unlock()
	if head.Commit == g.LastCommit || head.Commit == tried {
		return
	}
	if d, err := s.st.LatestDeployment(ctx, svcID); err == nil && (d.Status == "queued" || d.Status == "building") {
		return // a deploy is under way; look again next interval
	}
	d, err := s.deployCommit(ctx, svcID, "poll", head.Commit)
	if err != nil {
		s.log.Warn("git poll deploy", "service", svcID, "err", err)
		return
	}
	s.gitPoll.mu.Lock()
	s.gitPoll.tried[svcID] = head.Commit
	s.gitPoll.mu.Unlock()
	s.st.Audit(ctx, "git-poll", "service.redeploy", svcID, map[string]string{"commit": head.Commit, "deployment": d.ID})
}

type errString string

func (e errString) Error() string { return string(e) }
