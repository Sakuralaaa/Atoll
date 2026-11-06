package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Per-project mesh tokens (forwarder side: internal/agent/mesh/auth.go).
//
// The token of a project is HMAC-SHA256(meshKey, "project:"+id) truncated to 16 bytes, with
// meshKey = HMAC-SHA256(master.key, "atoll mesh token v1"), so nothing new is stored and a
// restored control server (same master.key) derives the same tokens.
//
// Rollout without breaking cross-server calls, whatever is upgraded first:
//  1. An export gets the token once its node announced MeshToken in its hello. Its forwarder
//     then strips and checks the handshake but still passes plain traffic (accept mode).
//  2. An import gets the token (and starts sending the handshake) only after the exporting
//     node acknowledged a payload carrying that export's token, i.e. its forwarder runs code
//     that understands the handshake.
//  3. An export gets RequireToken only when its node and every importing node announced
//     MeshToken and every importing node acknowledged its import with the token.
//
// Acknowledgements are fingerprints of the last payload a node accepted (pushMeshAll), one
// per forwarder ("" = ran without a token). A node whose last acknowledgement has no entry
// for a service runs no forwarder for it, so a new forwarder there counts as ready at once:
// adding a server or a service to a project never walks an enforcing exporter back. The
// acknowledgements are dropped (node unknown again) when a node's capability changes
// (upgrade or downgrade): a downgraded node turns enforcement off on the next push, and an
// upgraded one only counts after it acknowledged as the new agent. Capabilities and acknowledgements are kept in the
// settings table (key meshTokenStateKey, fingerprints only, never a token) so a control
// server restart does not walk every exporter back to accept mode.

const meshTokenStateKey = "mesh_token_state"

type meshTokenState struct {
	Caps map[string]bool              `json:"caps"`
	Acks map[string]map[string]string `json:"acks"` // node -> "x:<svc>" | "i:<svc>" -> token fingerprint
}

func deriveMeshKey(master []byte) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte("atoll mesh token v1"))
	return m.Sum(nil)
}

// meshToken is the project's token as 32 hex chars.
func (s *Server) meshToken(projectID string) string {
	m := hmac.New(sha256.New, s.meshKey)
	m.Write([]byte("project:" + projectID))
	return hex.EncodeToString(m.Sum(nil)[:16])
}

func tokenFP(tok string) string {
	sum := sha256.Sum256([]byte("fp:" + tok))
	return hex.EncodeToString(sum[:8])
}

// meshTok returns the loaded token state (callers hold n.tokMu).
func (s *Server) meshTokLocked(ctx context.Context) *meshTokenState {
	n := s.netst()
	if n.tok != nil {
		return n.tok
	}
	st := &meshTokenState{}
	if v, err := s.st.GetSetting(ctx, meshTokenStateKey); err == nil && v != "" {
		if err := json.Unmarshal([]byte(v), st); err != nil {
			s.log.Warn("mesh token state unreadable; starting over", "err", err)
			st = &meshTokenState{}
		}
	}
	if st.Caps == nil {
		st.Caps = map[string]bool{}
	}
	if st.Acks == nil {
		st.Acks = map[string]map[string]string{}
	}
	n.tok = st
	return st
}

func (s *Server) saveMeshTokLocked(ctx context.Context) {
	b, _ := json.Marshal(s.netst().tok)
	if err := s.st.SetSetting(ctx, meshTokenStateKey, string(b)); err != nil {
		s.log.Warn("save mesh token state", "err", err)
	}
}

// setMeshCap records whether nodeID's agent handles mesh tokens (from its hello). A change
// drops the node's acknowledgements: what it runs now has not acknowledged anything.
func (s *Server) setMeshCap(ctx context.Context, nodeID string, ok bool) {
	n := s.netst()
	n.tokMu.Lock()
	defer n.tokMu.Unlock()
	st := s.meshTokLocked(ctx)
	if prev, known := st.Caps[nodeID]; known && prev == ok {
		return
	}
	st.Caps[nodeID] = ok
	delete(st.Acks, nodeID) // unknown until the node acknowledges again
	s.saveMeshTokLocked(ctx)
}

// recordMeshAck stores what nodeID acknowledged with payload p: for every export and import
// the fingerprint of its token, "" when it ran without one. A node with an entry (even an
// empty one) is "known": a service missing from it has no forwarder there.
func (s *Server) recordMeshAck(ctx context.Context, nodeID string, p *api.MeshPayload) bool {
	n := s.netst()
	n.tokMu.Lock()
	defer n.tokMu.Unlock()
	st := s.meshTokLocked(ctx)
	acks := map[string]string{}
	fp := func(tok string) string {
		if tok == "" {
			return ""
		}
		return tokenFP(tok)
	}
	for _, e := range p.Exports {
		acks["x:"+e.ServiceID] = fp(e.Token)
	}
	for _, i := range p.Imports {
		acks["i:"+i.ServiceID] = fp(i.Token)
	}
	old, had := st.Acks[nodeID]
	if had && len(old) == len(acks) {
		same := true
		for k, v := range acks {
			if ov, ok := old[k]; !ok || ov != v {
				same = false
				break
			}
		}
		if same {
			return false
		}
	}
	st.Acks[nodeID] = acks
	s.saveMeshTokLocked(ctx)
	return true
}

// applyMeshTokens fills Token / RequireToken into a plan (see the rollout above). Nodes not
// in the plan any more are pruned from the state.
func (s *Server) applyMeshTokens(ctx context.Context, plan map[string]*api.MeshPayload) {
	n := s.netst()
	n.tokMu.Lock()
	defer n.tokMu.Unlock()
	st := s.meshTokLocked(ctx)
	pruned := false
	for id := range st.Caps {
		if plan[id] == nil {
			delete(st.Caps, id)
			pruned = true
		}
	}
	for id := range st.Acks {
		if plan[id] == nil {
			delete(st.Acks, id)
			pruned = true
		}
	}
	if pruned {
		s.saveMeshTokLocked(ctx)
	}
	// runsWith: node's forwarder for key runs with tok, or the node is known to run none for
	// it (a forwarder created from this plan starts with the token right away).
	runsWith := func(node, key, tok string) bool {
		acks, known := st.Acks[node]
		if !known || tok == "" {
			return false
		}
		v, has := acks[key]
		return !has || v == tokenFP(tok)
	}
	importers := map[string][]string{} // service -> importing nodes
	ids := make([]string, 0, len(plan))
	for id := range plan {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, i := range plan[id].Imports {
			importers[i.ServiceID] = append(importers[i.ServiceID], id)
		}
	}
	// exports: token when the node supports it
	ready := map[string]bool{} // service -> exporter forwarder runs with its token
	for _, id := range ids {
		p := plan[id]
		for k := range p.Exports {
			e := &p.Exports[k]
			if !st.Caps[id] {
				continue
			}
			e.Token = s.meshToken(e.ProjectID)
			ready[e.ServiceID] = runsWith(id, "x:"+e.ServiceID, e.Token)
		}
	}
	// imports: token once the exporter understands the handshake
	for _, id := range ids {
		p := plan[id]
		for k := range p.Imports {
			i := &p.Imports[k]
			if ready[i.ServiceID] && st.Caps[i.ServerID] {
				i.Token = s.meshToken(i.ProjectID)
			}
		}
	}
	// enforcement: every importer supports it and runs with the token
	for _, id := range ids {
		p := plan[id]
		for k := range p.Exports {
			e := &p.Exports[k]
			if e.Token == "" || !ready[e.ServiceID] {
				continue
			}
			req := true
			for _, m := range importers[e.ServiceID] {
				if !st.Caps[m] || !runsWith(m, "i:"+e.ServiceID, e.Token) {
					req = false
					break
				}
			}
			e.RequireToken = req
		}
	}
}
