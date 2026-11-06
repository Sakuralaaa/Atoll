package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
	"github.com/shirou/gopsutil/v4/disk"
)

func (s *Server) withUsage(ctx context.Context, n *api.Server) {
	n.Usage, _ = s.st.LatestNodeMetrics(ctx, n.ID)
	s.withAgentInfo(n)
}

func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) error {
	ns, err := s.st.ListNodes(r.Context())
	if err != nil {
		return err
	}
	if p := principalFrom(r.Context()); p != nil && p.Member { // only its assigned servers
		kept := ns[:0]
		for _, n := range ns {
			if p.Servers[n.ID] {
				kept = append(kept, n)
			}
		}
		ns = kept
	}
	for i := range ns {
		s.withUsage(r.Context(), &ns[i])
	}
	writeJSON(w, 200, ns)
	return nil
}

func (s *Server) handleGetServer(w http.ResponseWriter, r *http.Request) error {
	if !memberMayUse(r, r.PathValue("id")) {
		return errMemberHidden()
	}
	n, err := s.st.GetNode(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	s.withUsage(r.Context(), n)
	writeJSON(w, 200, n)
	return nil
}

func (s *Server) handlePatchServer(w http.ResponseWriter, r *http.Request) error {
	n, err := s.st.GetNode(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	var req struct {
		Name     *string `json:"name"`
		Provider *string `json:"provider"`
		Region   *string `json:"region"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Name != nil {
		n.Name = strings.TrimSpace(*req.Name)
		if n.Name == "" || len(n.Name) > 100 {
			return badRequest("name must be 1-100 chars")
		}
	}
	if req.Provider != nil {
		n.Provider = strings.TrimSpace(*req.Provider)
	}
	if req.Region != nil {
		n.Region = strings.TrimSpace(*req.Region)
	}
	if err := s.st.UpdateNodeMeta(r.Context(), n.ID, n.Name, n.Provider, n.Region); err != nil {
		return err
	}
	s.withUsage(r.Context(), n)
	s.publish("server", n)
	writeJSON(w, 200, n)
	return nil
}

func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) error {
	n, err := s.st.GetNode(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := s.checkServerEmpty(r.Context(), n.ID); err != nil {
		return err
	}
	return s.requireConfirm(w, r, "server.delete", n.ID, fmt.Sprintf("Remove server %s (%s)", n.Name, n.PublicIPv4), nil,
		func(ctx context.Context) (int, any, error) {
			return 204, nil, s.execDeleteServer(ctx, n.ID, principalFrom(r.Context()).actor())
		})
}

func (s *Server) checkServerEmpty(ctx context.Context, id string) error {
	ps, err := s.st.ListProjects(ctx, id)
	if err != nil {
		return err
	}
	if len(ps) > 0 {
		return errf(409, "server_in_use", "server still hosts %d project(s); migrate or delete them first", len(ps))
	}
	if n, err := s.st.CountServicesOnNode(ctx, id); err == nil && n > 0 {
		return errf(409, "server_in_use", "server still runs %d service(s) of other servers' projects; migrate or delete them first", n)
	}
	return nil
}

func (s *Server) execDeleteServer(ctx context.Context, id, actor string) error {
	if err := s.checkServerEmpty(ctx, id); err != nil {
		return err
	}
	s.hub.Disconnect(id)
	if err := s.st.DeleteNode(ctx, id); err != nil {
		return err
	}
	s.st.Audit(ctx, actor, "server.delete", id, nil)
	s.publish("server", map[string]any{"id": id, "deleted": true})
	return nil
}

func (s *Server) handleJoinToken(w http.ResponseWriter, r *http.Request) error {
	tok := "atl_join_" + randString(32, tokenAlphabet)
	exp := time.Now().Add(time.Hour).UTC()
	if err := s.st.CreateJoinToken(r.Context(), hashToken(tok), exp); err != nil {
		return err
	}
	base := s.publicURL(r.Context(), r)
	// pins the agent binaries this server hosts: the script refuses a /downloads/ file that
	// does not match, even when --server is plain http
	cmd := s.agentInstallCommand(base, "--token "+tok)
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "server.join_token", "", nil)
	writeJSON(w, 201, api.JoinToken{Token: tok, Command: cmd, ExpiresAt: exp})
	return nil
}

func rangeStep(rng string, maxRange time.Duration) (time.Time, int64, error) {
	var d time.Duration
	var step int64
	switch rng {
	case "", "1h":
		d, step = time.Hour, 60
	case "24h":
		d, step = 24*time.Hour, 300
	case "7d":
		d, step = 7*24*time.Hour, 900
	default:
		return time.Time{}, 0, badRequest("range must be 1h, 24h or 7d")
	}
	if d > maxRange {
		d = maxRange
	}
	return time.Now().Add(-d), step, nil
}

func (s *Server) handleServerMetrics(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.st.GetNode(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	since, step, err := rangeStep(r.URL.Query().Get("range"), 7*24*time.Hour)
	if err != nil {
		return err
	}
	ms, err := s.st.NodeMetricsRange(r.Context(), r.PathValue("id"), since, step)
	if err != nil {
		return err
	}
	writeJSON(w, 200, ms)
	return nil
}

func (s *Server) handleServerContainers(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.st.GetNode(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	cs, err := s.st.ContainerStates(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, cs)
	return nil
}

// ---------- agent join & blob relay ----------

func (s *Server) handleAgentJoin(w http.ResponseWriter, r *http.Request) error {
	var req api.JoinRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if !strings.HasPrefix(req.JoinToken, "atl_join_") {
		return errf(401, "invalid_join_token", "invalid join token")
	}
	nodeID := newID("srv")
	if err := s.st.ConsumeJoinToken(r.Context(), hashToken(req.JoinToken), nodeID); err != nil {
		if err == store.ErrNotFound {
			return errf(401, "invalid_join_token", "join token invalid, expired or already used")
		}
		return err
	}
	h := req.Hello
	name := strings.TrimSpace(h.Hostname)
	if name == "" {
		name = nodeID
	}
	n := api.Server{ID: nodeID, Name: name, Role: "worker", Status: "pending", PublicIPv4: h.PublicIPv4, PublicIPv6: h.PublicIPv6,
		TailnetHost: h.TailnetHost, OS: h.OS, Arch: h.Arch, CPUCores: h.CPUCores, MemMB: h.MemMB, DiskGB: h.DiskGB,
		AgentVersion: h.AgentVersion, CreatedAt: time.Now().UTC(), LastSeenAt: time.Now().UTC()}
	if err := s.st.CreateNode(r.Context(), n); err != nil {
		return err
	}
	nodeTok := "atl_node_" + randString(40, tokenAlphabet)
	if err := s.st.CreateNodeToken(r.Context(), hashToken(nodeTok), nodeID); err != nil {
		return err
	}
	s.st.Audit(r.Context(), "agent:"+nodeID, "server.join", nodeID, map[string]string{"hostname": h.Hostname, "ip": h.PublicIPv4})
	s.publish("server", n)
	writeJSON(w, 201, api.JoinResponse{NodeID: nodeID, NodeToken: nodeTok})
	return nil
}

func (s *Server) blobPath(id string) string {
	return filepath.Join(s.cfg.DataDir, "blobs", id+".tar.gz")
}

// newBlob returns a blob id plus one-time put and get URLs.
func (s *Server) newBlob(ctx context.Context, base string) (id, putURL, getURL string, err error) {
	id = randString(20, idAlphabet)
	put := randString(40, tokenAlphabet)
	get := randString(40, tokenAlphabet)
	ttl := s.cfg.TransferTimeout + time.Hour
	if err = s.st.CreateBlobToken(ctx, hashToken(put), id, "put", ttl); err != nil {
		return
	}
	if err = s.st.CreateBlobToken(ctx, hashToken(get), id, "get", ttl); err != nil {
		return
	}
	return id, base + "/agent/v1/blobs/" + put, base + "/agent/v1/blobs/" + get, nil
}

func (s *Server) handleBlobPut(w http.ResponseWriter, r *http.Request) error {
	id, err := s.st.ConsumeBlobToken(r.Context(), hashToken(r.PathValue("token")), "put")
	if err != nil {
		return errf(403, "invalid_blob_token", "blob token invalid or already used")
	}
	if err := s.writeBlob(id, r.Body); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

func (s *Server) writeBlob(id string, body io.Reader) error {
	tmp := s.blobPath(id) + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, &diskGuard{r: body, dir: filepath.Dir(tmp), free: blobMinFree}); err != nil {
		f.Close()
		os.Remove(tmp)
		if errors.Is(err, errDiskLow) {
			return errf(507, "insufficient_storage", "%v", err)
		}
		return errf(400, "upload_failed", "upload interrupted: %v", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.blobPath(id))
}

func (s *Server) handleBlobGet(w http.ResponseWriter, r *http.Request) error {
	id, err := s.st.ConsumeBlobToken(r.Context(), hashToken(r.PathValue("token")), "get")
	if err != nil {
		return errf(403, "invalid_blob_token", "blob token invalid or already used")
	}
	f, err := os.Open(s.blobPath(id))
	if err != nil {
		return errf(404, "blob_missing", "blob not uploaded yet")
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/gzip")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
	}
	io.Copy(w, f)
	return nil
}

// blobMinFree is the free space a blob upload must leave on the control server's data disk:
// an upload that would eat into it is aborted (507) instead of filling the disk the
// database and backups live on.
var blobMinFree uint64 = 2 << 30

var errDiskLow = errors.New("not enough free disk space on the control server for this transfer")

// diskGuard re-checks free space every diskGuardEvery bytes read through it.
type diskGuard struct {
	r     io.Reader
	dir   string
	free  uint64
	since int64
}

const diskGuardEvery = 64 << 20

func (g *diskGuard) Read(p []byte) (int, error) {
	if g.since == 0 || g.since >= diskGuardEvery {
		g.since = 1
		if u, err := disk.Usage(g.dir); err == nil && u.Free < g.free {
			return 0, errDiskLow
		}
	}
	n, err := g.r.Read(p)
	g.since += int64(n)
	return n, err
}
