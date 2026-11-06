package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// startProblemWatcher evaluates watched conditions every ProblemsTick and sends notifications.
func (s *Server) startProblemWatcher() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(s.cfg.ProblemsTick)
		defer t.Stop()
		for {
			select {
			case <-s.bg.Done():
				return
			case now := <-t.C:
				s.evaluateProblems(s.bg, now)
			}
		}
	}()
}

// problemsMu serializes evaluations (the ticker and tests) so notifications go out once.
var problemsMu sync.Mutex

// evaluateProblems reconciles the problems table with what holds now, publishes a change to
// the panel, then delivers pending notifications.
func (s *Server) evaluateProblems(ctx context.Context, now time.Time) {
	problemsMu.Lock()
	defer problemsMu.Unlock()
	observed, err := s.st.ProblemSignals(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("problem signals", "err", err)
		}
		return
	}
	changes, err := s.st.SyncProblems(ctx, now, observed)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("sync problems", "err", err)
		}
		return
	}
	for _, c := range changes {
		if c.Resolved {
			s.log.Info("problem resolved", "kind", c.Problem.Kind, "subject", c.Problem.SubjectName)
		} else {
			s.log.Warn("problem", "kind", c.Problem.Kind, "subject", c.Problem.SubjectName, "message", c.Problem.Message)
		}
	}
	if len(changes) > 0 {
		s.publish("problems", map[string]int{"changed": len(changes)})
	}
	s.deliverNotifications(ctx, now)
}

// GET /problems[?resolved=1]
func (s *Server) handleListProblems(w http.ResponseWriter, r *http.Request) error {
	list, err := s.st.ListProblems(r.Context(), r.URL.Query().Get("resolved") == "1")
	if err != nil {
		return err
	}
	pr := principalFrom(r.Context())
	out := list[:0]
	for _, p := range list {
		switch {
		case pr.Member:
			if !memberSeesProblem(pr.Servers, pr.Projects, p) {
				continue
			}
		case p.ProjectID != "" && !pr.inProject(p.ProjectID):
			continue // a project-scoped token sees the problems of its projects only
		}
		out = append(out, p)
	}
	writeJSON(w, 200, out)
	return nil
}

func problemKindText(kind string) string {
	switch kind {
	case api.ProblemNodeOffline:
		return "服务器离线"
	case api.ProblemDiskHigh:
		return "磁盘空间不足"
	case api.ProblemServiceDown:
		return "服务异常"
	case api.ProblemDeployFailed:
		return "部署失败"
	case api.ProblemBackupFailed:
		return "备份失败"
	case api.ProblemDomainError:
		return "域名异常"
	case api.ProblemBackupVerifyFailed:
		return "备份校验失败"
	}
	return kind
}

// memberSeesProblem: a member (servers and projects in its scope) sees problems of its own
// projects whose service runs on one of its servers, and server-level problems (offline,
// disk) of its servers. Panel-wide ones (the backup repository check) stay with admins.
func memberSeesProblem(servers, projects map[string]bool, p api.Problem) bool {
	if p.ProjectID != "" {
		return projects[p.ProjectID] && (p.ServerID == "" || servers[p.ServerID])
	}
	if p.SubjectID == api.BackupRepoSubjectID {
		return false
	}
	return p.ServerID != "" && servers[p.ServerID]
}
