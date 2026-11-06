package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/Sakuralaaa/Atoll/internal/agent/tailnet"
	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Cross-server service mesh (see internal/agent/mesh). For every project whose services sit
// on more than one server, each service with ports is exported on its node's tailnet IP and
// imported (as a forwarder carrying its DNS name) on every other node of the project.

// meshProtocol maps a service port protocol to the forwarded transport.
func meshProtocol(p string) string {
	if p == "udp" {
		return "udp"
	}
	return "tcp" // http and tcp
}

// meshPlan computes the MeshPayload of every node. Warnings name services that cannot be
// reached across servers (node without tailnet IP, service without ports).
func (s *Server) meshPlan(ctx context.Context) (map[string]*api.MeshPayload, []string, error) {
	svcs, err := s.st.MeshServices(ctx)
	if err != nil {
		return nil, nil, err
	}
	nodes, err := s.st.ListNodes(ctx)
	if err != nil {
		return nil, nil, err
	}
	nodeByID := map[string]api.Server{}
	plan := map[string]*api.MeshPayload{}
	for _, n := range nodes {
		nodeByID[n.ID] = n
		plan[n.ID] = &api.MeshPayload{TailnetIP: n.TailnetIP, Exports: []api.MeshExport{}, Imports: []api.MeshImport{}}
	}
	byProject := map[string][]store.MeshService{}
	var order []string
	for _, v := range svcs {
		if _, ok := byProject[v.ProjectID]; !ok {
			order = append(order, v.ProjectID)
		}
		byProject[v.ProjectID] = append(byProject[v.ProjectID], v)
	}
	o := s.netOpts()
	keep := map[string]bool{}
	var warnings []string
	for _, pid := range order {
		list := byProject[pid]
		spread := map[string]bool{}
		for _, v := range list {
			spread[v.NodeID] = true
		}
		if len(spread) < 2 {
			continue
		}
		network := "atoll-" + pid
		for _, v := range list {
			n, ok := nodeByID[v.NodeID]
			if !ok {
				continue
			}
			if len(v.Ports) == 0 {
				warnings = append(warnings, fmt.Sprintf("%s has no ports: services on other servers cannot reach it", v.Name))
				continue
			}
			if !tailnet.IsTailnetIP(n.TailnetIP) {
				warnings = append(warnings, fmt.Sprintf("%s: server %s is not on the tailnet", v.Name, n.Name))
				continue
			}
			exp := api.MeshExport{ServiceID: v.ServiceID, ProjectID: pid, Network: network, Name: v.Name}
			seen := map[string]bool{}
			for _, p := range v.Ports {
				proto := meshProtocol(p.Protocol)
				k := store.MeshKey(v.ServiceID, p.ContainerPort, proto)
				if p.ContainerPort <= 0 || seen[k] {
					continue
				}
				seen[k] = true
				hp, err := s.st.MeshPort(ctx, v.NodeID, v.ServiceID, p.ContainerPort, proto, o.MeshPortLo, o.MeshPortHi)
				if err != nil {
					return nil, warnings, err
				}
				keep[k] = true
				exp.Ports = append(exp.Ports, api.MeshPort{Port: p.ContainerPort, Protocol: proto, HostPort: hp})
			}
			if len(exp.Ports) == 0 {
				continue
			}
			plan[v.NodeID].Exports = append(plan[v.NodeID].Exports, exp)
			for other := range spread {
				if other == v.NodeID || plan[other] == nil {
					continue
				}
				imp := api.MeshImport{ServiceID: v.ServiceID, ProjectID: pid, Network: network, Name: v.Name, ServerID: v.NodeID}
				for _, p := range exp.Ports {
					imp.Ports = append(imp.Ports, api.MeshPort{Port: p.Port, Protocol: p.Protocol, Target: fmt.Sprintf("%s:%d", n.TailnetIP, p.HostPort)})
				}
				plan[other].Imports = append(plan[other].Imports, imp)
			}
		}
	}
	for _, p := range plan {
		sort.Slice(p.Imports, func(i, j int) bool { return p.Imports[i].ServiceID < p.Imports[j].ServiceID })
	}
	if err := s.st.PruneMeshPorts(ctx, keep); err != nil {
		return nil, warnings, err
	}
	s.applyMeshTokens(ctx, plan) // mesh_token.go
	return plan, warnings, nil
}

// pushMeshAll recomputes the mesh and sends every connected node its payload when it changed
// since the last successful push to that node (agents persist it, so this is not needed for
// them to keep working while the control server is down).
//
// meshMu only covers planning. The agent calls happen after it is released and in parallel:
// holding it across a call (up to 2x the request timeout) used to block reconnecting agents
// (forgetMesh runs before their read loop), so their pongs went unread and they were dropped,
// which reconnected them and started another push.
//
// Token rollout (mesh_token.go) advances one stage per acknowledged push, so a push round
// whose acknowledgements changed the plan is followed by another (at most meshTokenRounds).
func (s *Server) pushMeshAll(ctx context.Context) {
	n := s.netst()
	n.pushMu.Lock()
	defer n.pushMu.Unlock()
	for round := 0; round < meshTokenRounds; round++ {
		if !s.pushMeshRound(ctx) {
			return
		}
	}
}

const meshTokenRounds = 4

// pushMeshRound pushes changed payloads once; it reports whether an acknowledgement changed
// the token state (the plan may have moved on).
func (s *Server) pushMeshRound(ctx context.Context) bool {
	n := s.netst()
	n.meshMu.Lock()
	plan, warnings, err := s.meshPlan(ctx)
	n.meshMu.Unlock()
	if err != nil {
		s.log.Error("mesh plan", "err", err)
		return false
	}
	for _, w := range warnings {
		s.log.Debug("mesh", "warning", w)
	}
	ids := make([]string, 0, len(plan))
	for id := range plan {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var wg sync.WaitGroup
	var advMu sync.Mutex
	advanced := false
	for _, id := range ids {
		if !s.hub.Online(id) {
			continue
		}
		p := plan[id]
		b, _ := json.Marshal(p)
		sum := sha256.Sum256(b)
		h := hex.EncodeToString(sum[:])
		n.lastMu.Lock()
		same := n.lastMesh[id] == h
		n.lastMu.Unlock()
		if same {
			continue
		}
		wg.Add(1)
		go func(id string, p *api.MeshPayload, h string) {
			defer wg.Done()
			if _, err := s.hub.Call(ctx, id, api.MsgMesh, p, 2*s.cfg.RequestTimeout); err != nil {
				s.log.Warn("mesh push", "node", id, "err", err) // agent errors name containers, never tokens
				if len(p.Exports)+len(p.Imports) == 0 {
					n.lastMu.Lock()
					n.lastMesh[id] = h // old agents without mesh support: do not retry empty payloads
					n.lastMu.Unlock()
				}
				return
			}
			n.lastMu.Lock()
			n.lastMesh[id] = h
			n.lastMu.Unlock()
			if s.recordMeshAck(ctx, id, p) {
				advMu.Lock()
				advanced = true
				advMu.Unlock()
			}
		}(id, p, h)
	}
	wg.Wait()
	return advanced
}

// forgetMesh makes the next pushMeshAll resend to nodeID (after it reconnected).
// It runs on the agent connect path, so it must never wait on a mesh push.
func (s *Server) forgetMesh(nodeID string) {
	n := s.netst()
	n.lastMu.Lock()
	delete(n.lastMesh, nodeID)
	n.lastMu.Unlock()
}

// MeshPlan exposes the plan to tests.
func (s *Server) MeshPlan(ctx context.Context) (map[string]*api.MeshPayload, []string, error) {
	n := s.netst()
	n.meshMu.Lock()
	defer n.meshMu.Unlock()
	return s.meshPlan(ctx)
}
