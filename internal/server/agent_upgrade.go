package server

// Agent upgrades from the panel: the control server tells an agent to replace its binary
// with the one this server hosts in <data>/downloads (same version as atoll-server: the
// installer and the control self-update keep them in step). The agent verifies and swaps it
// itself (internal/agent/upgrade.go); this file starts upgrades and tracks their outcome.

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// agentUpgradeTimeout is how long an upgrade may stay "upgrading" without a report.
const agentUpgradeTimeout = 5 * time.Minute

// agentUpgrades caches the last upgrade per node; every change is written through save
// (the agent_upgrades table, migration 0013), so the state survives a control restart.
type agentUpgrades struct {
	mu   sync.Mutex
	m    map[string]*api.AgentUpgrade
	save func(nodeID string, u api.AgentUpgrade) // nil in tests that do not care
}

func (u *agentUpgrades) load(m map[string]api.AgentUpgrade) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.m = map[string]*api.AgentUpgrade{}
	for id, v := range m {
		u.m[id] = &v
	}
}

func (u *agentUpgrades) persist(nodeID string, v api.AgentUpgrade) {
	if u.save != nil {
		u.save(nodeID, v)
	}
}

func (u *agentUpgrades) get(nodeID string) *api.AgentUpgrade {
	u.mu.Lock()
	cur := u.m[nodeID]
	if cur == nil {
		u.mu.Unlock()
		return nil
	}
	timedOut := false
	if cur.State == api.AgentUpgradeRunning && time.Since(cur.StartedAt) > agentUpgradeTimeout {
		cur.State = api.AgentUpgradeFailed
		cur.Error = "no report from the agent within 5 minutes; check `journalctl -u atoll-agent` on the server"
		cur.FinishedAt = time.Now().UTC()
		timedOut = true
	}
	c := *cur
	u.mu.Unlock()
	if timedOut {
		u.persist(nodeID, c)
	}
	return &c
}

func (u *agentUpgrades) set(nodeID string, v *api.AgentUpgrade) {
	u.mu.Lock()
	if u.m == nil {
		u.m = map[string]*api.AgentUpgrade{}
	}
	u.m[nodeID] = v
	c := *v
	u.mu.Unlock()
	u.persist(nodeID, c)
}

// finish applies fn to the node's running upgrade (no-op when none is running).
func (u *agentUpgrades) finish(nodeID string, fn func(*api.AgentUpgrade)) bool {
	u.mu.Lock()
	cur := u.m[nodeID]
	if cur == nil || cur.State != api.AgentUpgradeRunning {
		u.mu.Unlock()
		return false
	}
	fn(cur)
	cur.FinishedAt = time.Now().UTC()
	c := *cur
	u.mu.Unlock()
	u.persist(nodeID, c)
	return true
}

// withAgentInfo fills the agent upgrade fields of a server view.
func (s *Server) withAgentInfo(n *api.Server) {
	if s.hostedAgentSum(n.Arch) != "" {
		n.AgentLatest = Version
	}
	if c := s.hub.get(n.ID); c != nil {
		n.AgentSelfUpgrade = c.selfUpgrade
		n.Latency = c.latency.view()
	}
	n.AgentUpgrade = s.agentUpg.get(n.ID)
}

// onAgentHello completes a running upgrade when the agent comes back on the target version
// (the agent's own report may follow; it carries rollback details).
func (s *Server) onAgentHello(nodeID string, h api.HelloPayload) {
	s.agentUpg.finish(nodeID, func(u *api.AgentUpgrade) {
		if h.AgentVersion == u.To {
			u.State = api.AgentUpgradeDone
		}
	})
}

// onAgentUpgradeResult records the helper's result sent by the agent after reconnecting.
func (s *Server) onAgentUpgradeResult(ctx context.Context, nodeID string, r api.UpdateResult) {
	u := s.agentUpg.get(nodeID)
	if u == nil || r.StartedAt.Before(u.StartedAt.Add(-time.Minute)) {
		return // a result from an upgrade this server did not start (or before a restart)
	}
	s.agentUpg.mu.Lock()
	cur := s.agentUpg.m[nodeID]
	if r.OK {
		cur.State = api.AgentUpgradeDone
		cur.Error = ""
	} else {
		cur.State = api.AgentUpgradeFailed
		cur.Error = r.Error
		cur.RolledBack = r.RolledBack
	}
	cur.FinishedAt = time.Now().UTC()
	saved := *cur
	s.agentUpg.mu.Unlock()
	s.agentUpg.persist(nodeID, saved)
	s.log.Info("agent upgrade finished", "node", nodeID, "ok", r.OK, "from", r.FromVersion, "to", r.ToVersion, "err", r.Error)
	s.st.Audit(ctx, "agent:"+nodeID, "server.agent_upgrade_result", nodeID, map[string]any{
		"ok": r.OK, "from": r.FromVersion, "to": r.ToVersion, "rolled_back": r.RolledBack, "error": r.Error})
	s.publishServer(ctx, nodeID)
}

// startAgentUpgrade sends MsgUpgrade to one online agent.
func (s *Server) startAgentUpgrade(ctx context.Context, nodeID, actor string) (*api.AgentUpgrade, error) {
	n, err := s.st.GetNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	c := s.hub.get(nodeID)
	if c == nil {
		return nil, errf(409, "offline", "%s is offline", n.Name)
	}
	if n.AgentVersion == Version {
		return nil, errf(409, "up_to_date", "%s already runs %s", n.Name, Version)
	}
	if !c.selfUpgrade {
		return nil, errf(409, "agent_too_old", "the agent on %s (%s) cannot upgrade itself; run once on that server: %s",
			n.Name, n.AgentVersion, s.agentInstallCommand(s.publicURL(ctx, nil)))
	}
	sum := s.hostedAgentSum(n.Arch)
	if sum == "" {
		return nil, errf(409, "not_hosted", "this control server hosts no atoll-agent-linux-%s (installed with --no-agent-binaries?)", n.Arch)
	}
	if u := s.agentUpg.get(nodeID); u != nil && u.State == api.AgentUpgradeRunning {
		return nil, errf(409, "busy", "an upgrade of %s is already running", n.Name)
	}
	p := api.UpgradePayload{Version: Version, Path: "/downloads/atoll-agent-linux-" + n.Arch, SHA256: sum}
	started := time.Now().UTC()
	res, err := s.hub.Request(ctx, nodeID, api.MsgUpgrade, p, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		s.st.Audit(ctx, actor, "server.agent_upgrade", nodeID, map[string]string{"from": n.AgentVersion, "to": Version, "error": res.Error})
		return nil, errf(409, "upgrade_failed", "%s: %s", n.Name, res.Error)
	}
	u := &api.AgentUpgrade{State: api.AgentUpgradeRunning, From: n.AgentVersion, To: Version, StartedAt: started}
	s.agentUpg.set(nodeID, u)
	s.st.Audit(ctx, actor, "server.agent_upgrade", nodeID, map[string]string{"from": n.AgentVersion, "to": Version})
	s.log.Info("agent upgrade started", "node", nodeID, "from", n.AgentVersion, "to", Version)
	s.publishServer(ctx, nodeID)
	c2 := *u
	return &c2, nil
}

// POST /api/v1/servers/{id}/agent-upgrade
func (s *Server) handleAgentUpgrade(w http.ResponseWriter, r *http.Request) error {
	u, err := s.startAgentUpgrade(r.Context(), r.PathValue("id"), principalFrom(r.Context()).actor())
	if err != nil {
		return err
	}
	writeJSON(w, 202, u)
	return nil
}

// agentUpgradeBatchRow is one row of POST /api/v1/servers/agent-upgrade.
type agentUpgradeBatchRow struct {
	ID      string            `json:"id"`
	Upgrade *api.AgentUpgrade `json:"upgrade,omitempty"`
	Code    string            `json:"code,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// POST /api/v1/servers/agent-upgrade {"ids": [...]} (empty = every online server not on this
// version). Three at a time; one row per server.
func (s *Server) handleAgentUpgradeBatch(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		IDs []string `json:"ids"`
	}
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil {
			return err
		}
	}
	ctx := r.Context()
	ids := req.IDs
	if len(ids) == 0 {
		ns, err := s.st.ListNodes(ctx)
		if err != nil {
			return err
		}
		for _, n := range ns {
			if n.Status == "online" && n.AgentVersion != Version {
				ids = append(ids, n.ID)
			}
		}
	}
	if len(ids) > 200 {
		return badRequest("at most 200 servers per request")
	}
	actor := principalFrom(ctx).actor()
	rows := make([]agentUpgradeBatchRow, len(ids))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rows[i].ID = id
			u, err := s.startAgentUpgrade(ctx, id, actor)
			if err != nil {
				rows[i].Error = err.Error()
				var ae *apiError
				if errors.As(err, &ae) {
					rows[i].Code = ae.code
				}
				return
			}
			rows[i].Upgrade = u
		}()
	}
	wg.Wait()
	writeJSON(w, 200, rows)
	return nil
}
