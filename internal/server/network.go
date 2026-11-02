package server

// Private network provider (Tailscale). The panel stays public; machine-to-machine traffic —
// agent <-> control and service <-> service across servers — goes over the tailnet:
//
//   - Settings hold the provider, a reusable tagged auth key (encrypted with master.key, never
//     logged or returned) and optionally the tailnet name / control URL.
//   - install-agent.sh asks POST /agent/v1/bootstrap (join token, not consumed) for the key,
//     joins the tailnet and registers through the control server's tailnet URL.
//   - Already-joined agents are moved over by MsgNetwork: the agent verifies the tailnet URL
//     with its own node token before switching and keeps the old URL as a fallback.
//   - The control server can serve the agent endpoints on its tailnet IP (ATOLL_AGENT_LISTEN)
//     and stop answering them publicly (ATOLL_AGENT_TAILNET_ONLY=1), except bootstrap.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent/tailnet"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// NetOptions configures the agent listener and the mesh port range (from env in main).
type NetOptions struct {
	// AgentListen is an extra listener for the agent endpoints only: "100.x.y.z:7301",
	// "tailscale:7301" (this host's tailnet IPv4, waited for at boot) or ":7301". "" = none.
	AgentListen string
	// AgentTailnetOnly stops answering /agent/* on the main (public) listener, except
	// POST /agent/v1/bootstrap which new nodes need before they are on the tailnet.
	AgentTailnetOnly bool
	// Mesh host ports published on tailnet IPs (default 41000-41999).
	MeshPortLo, MeshPortHi int
}

type netState struct {
	mu         sync.Mutex
	opts       NetOptions
	listenAddr string
	listenErr  string
	meshMu     sync.Mutex
	lastMesh   map[string]string // node -> hash of the last mesh sent
}

var netInitMu sync.Mutex

func (s *Server) netst() *netState {
	netInitMu.Lock()
	defer netInitMu.Unlock()
	if s.net == nil {
		s.net = &netState{opts: NetOptions{MeshPortLo: 41000, MeshPortHi: 41999}, lastMesh: map[string]string{}}
	}
	return s.net
}

// SetNetworkOptions applies NetOptions (call before Handler / ServeAgentListener).
func (s *Server) SetNetworkOptions(o NetOptions) {
	if o.MeshPortLo <= 0 || o.MeshPortHi < o.MeshPortLo || o.MeshPortHi > 65535 {
		o.MeshPortLo, o.MeshPortHi = 41000, 41999
	}
	n := s.netst()
	n.mu.Lock()
	n.opts = o
	n.mu.Unlock()
}

func (s *Server) netOpts() NetOptions {
	n := s.netst()
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.opts
}

func (s *Server) agentListenState() (addr, errMsg string) {
	n := s.netst()
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.listenAddr, n.listenErr
}

// ParseMeshPorts parses "41000-41999".
func ParseMeshPorts(v string) (int, int, error) {
	if v == "" {
		return 41000, 41999, nil
	}
	a, b, ok := strings.Cut(v, "-")
	lo, err1 := strconv.Atoi(strings.TrimSpace(a))
	hi, err2 := strconv.Atoi(strings.TrimSpace(b))
	if !ok || err1 != nil || err2 != nil || lo < 1024 || hi > 65535 || hi < lo {
		return 0, 0, fmt.Errorf("mesh port range %q: want LO-HI within 1024-65535", v)
	}
	return lo, hi, nil
}

// resolveListen turns "tailscale:7301" into "<tailnet ip>:7301".
func resolveListen(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "tailscale" || host == "tailnet" {
		ip := tailnet.LocalIP()
		if ip == "" {
			return "", errors.New("no tailnet IP on this host yet (is tailscaled up?)")
		}
		host = ip
	}
	return net.JoinHostPort(host, port), nil
}

// ServeAgentListener serves the agent endpoints on NetOptions.AgentListen until ctx ends. It
// keeps retrying while the address cannot be bound (tailscaled may come up after us at boot).
func (s *Server) ServeAgentListener(ctx context.Context) {
	o := s.netOpts()
	if o.AgentListen == "" {
		return
	}
	n := s.netst()
	setState := func(addr, msg string) {
		n.mu.Lock()
		n.listenAddr, n.listenErr = addr, msg
		n.mu.Unlock()
	}
	for ctx.Err() == nil {
		addr, err := resolveListen(o.AgentListen)
		var ln net.Listener
		if err == nil {
			ln, err = net.Listen("tcp", addr)
		}
		if err != nil {
			setState("", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		setState(ln.Addr().String(), "")
		s.log.Info("agent listener (tailnet)", "addr", ln.Addr().String(), "public_agent_endpoints", !o.AgentTailnetOnly)
		hs := &http.Server{Handler: s.AgentHandler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 2 * time.Minute}
		go func() {
			<-ctx.Done()
			sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
			hs.Shutdown(sctx)
			c()
		}()
		err = hs.Serve(ln)
		if ctx.Err() != nil || errors.Is(err, http.ErrServerClosed) {
			return
		}
		setState("", err.Error())
		time.Sleep(5 * time.Second)
	}
}

// AgentHandler serves only what agents need: join, connect, blobs, bootstrap, ping, network,
// agent binary downloads and /healthz.
func (s *Server) AgentHandler() http.Handler {
	mux := http.NewServeMux()
	open := func(pattern string, fn handlerFunc) { mux.Handle(pattern, s.wrap(fn)) }
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /downloads/{file}", s.handleDownload)
	open("POST /agent/v1/join", s.handleAgentJoin)
	mux.HandleFunc("GET /agent/v1/connect", s.handleAgentConnect)
	open("PUT /agent/v1/blobs/{token}", s.handleBlobPut)
	open("GET /agent/v1/blobs/{token}", s.handleBlobGet)
	s.agentNetworkRoutes(open)
	return securityHeaders(mux)
}

func (s *Server) agentNetworkRoutes(open func(string, handlerFunc)) {
	open("POST /agent/v1/bootstrap", s.handleBootstrap)
	open("GET /agent/v1/network", s.handleAgentNetwork)
	open("GET /agent/v1/ping", s.handleAgentPing)
}

// registerNetworkRoutes adds the network endpoints to the main mux.
func (s *Server) registerNetworkRoutes(mux *http.ServeMux, a func(string, access, handlerFunc), open func(string, handlerFunc)) {
	a("POST /api/v1/servers/{id}/network", needServers, s.handleServerNetwork)
	a("POST /api/v1/services/{id}/migrate", needMigrate, s.handleServiceMigrate)
	s.agentNetworkRoutes(open)
}

// agentGate hides /agent/* on the public listener in tailnet-only mode (bootstrap stays).
func (s *Server) agentGate(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/agent/") && r.URL.Path != "/agent/v1/bootstrap" && s.netOpts().AgentTailnetOnly {
			writeJSON(w, 404, api.ErrorBody{Error: "agent endpoints are only served on the tailnet", Code: "tailnet_only"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

// ---------- settings ----------

func (s *Server) networkProvider(ctx context.Context) string {
	v, _ := s.st.GetSetting(ctx, "network_provider")
	return v
}

// tailnetControlURL is the agent endpoint over the tailnet: the setting, else derived from the
// tailnet listener. MagicDNS names are avoided on purpose: workers run --accept-dns=false.
func (s *Server) tailnetControlURL(ctx context.Context) string {
	if v, _ := s.st.GetSetting(ctx, "tailnet_control_url"); v != "" {
		return strings.TrimRight(v, "/")
	}
	addr, _ := s.agentListenState()
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if !tailnet.IsTailnetIP(host) {
		host = tailnet.LocalIP() // listening on all interfaces: use this host's tailnet IP
		if host == "" {
			return ""
		}
	}
	return "http://" + net.JoinHostPort(host, port)
}

func (s *Server) fillNetworkSettings(ctx context.Context, st *api.Settings) {
	get := func(k string) string { v, _ := s.st.GetSetting(ctx, k); return v }
	st.NetworkProvider = get("network_provider")
	st.HasTailscaleAuthKey = get("tailscale_auth_key") != ""
	st.TailnetName = get("tailnet_name")
	st.TailnetControlURL = s.tailnetControlURL(ctx)
	st.TailnetListen, st.TailnetListenErr = s.agentListenState()
	st.AgentPublic = !s.netOpts().AgentTailnetOnly
}

// applyNetworkSettings validates and stores the network part of PUT /settings. It returns
// whether anything changed (agents are then reconciled).
func (s *Server) applyNetworkSettings(ctx context.Context, req api.Settings) (bool, error) {
	set := map[string]string{}
	switch strings.TrimSpace(req.NetworkProvider) {
	case "":
		// omitted: keep
	case "none":
		set["network_provider"] = ""
	case api.NetworkProviderTailscale:
		set["network_provider"] = api.NetworkProviderTailscale
	default:
		return false, badRequest("network_provider must be tailscale or none")
	}
	if k := strings.TrimSpace(req.TailscaleAuthKey); k != "" {
		if !strings.HasPrefix(k, "tskey-") {
			return false, badRequest("tailscale_auth_key must be a Tailscale auth key (tskey-auth-...)")
		}
		set["tailscale_auth_key"] = k
	} else if req.ClearTailscaleAuthKey {
		set["tailscale_auth_key"] = ""
	}
	if req.TailnetName != "" || req.NetworkProvider != "" {
		set["tailnet_name"] = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(req.TailnetName)), ".")
	}
	if u := strings.TrimRight(strings.TrimSpace(req.TailnetControlURL), "/"); u != "" || req.NetworkProvider != "" {
		if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return false, badRequest("tailnet_control_url must start with http:// or https://")
		}
		set["tailnet_control_url"] = u
	}
	if set["network_provider"] == api.NetworkProviderTailscale && set["tailscale_auth_key"] == "" {
		if cur, _ := s.st.GetSetting(ctx, "tailscale_auth_key"); cur == "" && req.TailscaleAuthKey == "" {
			return false, badRequest("tailscale needs an auth key (reusable, tagged, pre-approved)")
		}
	}
	changed := false
	for k, v := range set {
		if cur, _ := s.st.GetSetting(ctx, k); cur != v {
			changed = true
		}
		if err := s.st.SetSetting(ctx, k, v); err != nil {
			return false, err
		}
	}
	return changed, nil
}

// ---------- agent endpoints ----------

func (s *Server) bootstrapInfo(ctx context.Context, r *http.Request) api.BootstrapResponse {
	br := api.BootstrapResponse{NetworkProvider: s.networkProvider(ctx), PublicURL: s.publicURL(ctx, r)}
	if br.NetworkProvider == api.NetworkProviderTailscale {
		br.TailscaleAuthKey, _ = s.st.GetSetting(ctx, "tailscale_auth_key")
		br.TailnetName, _ = s.st.GetSetting(ctx, "tailnet_name")
		br.ControlURL = s.tailnetControlURL(ctx)
	}
	return br
}

func writeBootstrap(w http.ResponseWriter, r *http.Request, br api.BootstrapResponse) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") != "env" {
		writeJSON(w, 200, br)
		return
	}
	// KEY=value lines for install-agent.sh (values never contain newlines).
	clean := func(v string) string { return strings.NewReplacer("\n", "", "\r", "", "'", "").Replace(v) }
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "ATOLL_NETWORK=%s\nATOLL_TS_AUTHKEY=%s\nATOLL_TAILNET=%s\nATOLL_CONTROL_URL=%s\nATOLL_PUBLIC_URL=%s\n",
		clean(br.NetworkProvider), clean(br.TailscaleAuthKey), clean(br.TailnetName), clean(br.ControlURL), clean(br.PublicURL))
}

// handleBootstrap: POST /agent/v1/bootstrap {join_token}. The token is checked, not consumed.
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) error {
	var req api.BootstrapRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if !strings.HasPrefix(req.JoinToken, "atl_join_") || !s.st.JoinTokenValid(r.Context(), hashToken(req.JoinToken)) {
		return errf(401, "invalid_join_token", "join token invalid, expired or already used")
	}
	writeBootstrap(w, r, s.bootstrapInfo(r.Context(), r))
	return nil
}

func (s *Server) nodeFromRequest(r *http.Request) (string, bool) {
	tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if tok == "" {
		return "", false
	}
	id, err := s.st.NodeByToken(r.Context(), hashToken(tok))
	return id, err == nil
}

// handleAgentNetwork: GET /agent/v1/network (node token) — `atoll-agent tailnet up`.
func (s *Server) handleAgentNetwork(w http.ResponseWriter, r *http.Request) error {
	if _, ok := s.nodeFromRequest(r); !ok {
		return errf(401, "unauthorized", "invalid node token")
	}
	writeBootstrap(w, r, s.bootstrapInfo(r.Context(), r))
	return nil
}

// handleAgentPing: GET /agent/v1/ping. With a node token it confirms the token (agents verify a
// new control URL with it before switching); without one it only says "ok".
func (s *Server) handleAgentPing(w http.ResponseWriter, r *http.Request) error {
	res := tailnet.PingResult{OK: true}
	if r.Header.Get("Authorization") != "" {
		id, ok := s.nodeFromRequest(r)
		if !ok {
			return errf(401, "unauthorized", "invalid node token")
		}
		res.NodeID = id
	}
	writeJSON(w, 200, res)
	return nil
}

// agentURL rewrites a URL built on the public base so it uses the base the node's agent is
// connected through (its tailnet control URL when it reached us over the tailnet).
func (s *Server) agentURL(ctx context.Context, nodeID, base, u string) string {
	n, err := s.st.GetNode(ctx, nodeID)
	if err != nil || n.Network == nil || !n.Network.ViaTailnet || n.Network.ControlURL == "" {
		return u
	}
	if strings.HasPrefix(u, base) {
		return strings.TrimRight(n.Network.ControlURL, "/") + strings.TrimPrefix(u, base)
	}
	return u
}

// ---------- node network actions ----------

// reconcileNetwork moves an agent that is on the tailnet but still connected publicly to the
// tailnet control URL (the agent verifies it first and keeps the public URL as fallback), and
// makes sure every agent keeps the panel's public domain URL as a fallback.
func (s *Server) reconcileNetwork(ctx context.Context, nodeID string, h api.HelloPayload) {
	if !s.reconcileTailnet(ctx, nodeID, h) {
		s.ensurePublicFallback(ctx, nodeID, h)
	}
}

// reconcileTailnet reports whether it sent the agent a MsgNetwork (which carries the public URL
// as fallback).
func (s *Server) reconcileTailnet(ctx context.Context, nodeID string, h api.HelloPayload) bool {
	if s.networkProvider(ctx) != api.NetworkProviderTailscale || h.Network == nil {
		return false
	}
	cu := s.tailnetControlURL(ctx)
	pub := s.publicURL(ctx, nil)
	if cu == "" {
		return false
	}
	switch {
	case !h.Network.ViaTailnet && tailnet.IsTailnetIP(h.TailnetIP):
		s.log.Info("moving agent to the tailnet control URL", "node", nodeID, "url", cu)
		s.hub.Fire(nodeID, api.MsgNetwork, api.NetworkPayload{Provider: api.NetworkProviderTailscale, ControlURL: cu, FallbackURL: pub})
		return true
	case h.Network.ViaTailnet && pub != "" && !contains(h.Network.FallbackURLs, pub) && strings.TrimRight(h.Network.ControlURL, "/") == cu:
		// make sure the public URL is a fallback (e.g. node joined directly over the tailnet)
		s.hub.Fire(nodeID, api.MsgNetwork, api.NetworkPayload{Provider: api.NetworkProviderTailscale, ControlURL: cu, FallbackURL: pub})
		return true
	}
	return false
}

// domainURL reports whether u is an http(s) URL whose host is a DNS name (not an IP address or
// localhost): the only kind of control URL that survives moving the control server.
func domainURL(u string) bool {
	pu, err := url.Parse(u)
	if err != nil || (pu.Scheme != "http" && pu.Scheme != "https") {
		return false
	}
	h := strings.ToLower(pu.Hostname())
	return h != "" && h != "localhost" && net.ParseIP(h) == nil && strings.Contains(h, ".")
}

// ensurePublicFallback pushes the panel's public URL to an agent that does not have it among its
// control URLs, when that URL is domain-based: after the control server moves to another
// machine, agents find it again through DNS (docs/control.md). The URL the agent is connected
// through stays its primary (an agent connected through a fallback only gets the URL added).
func (s *Server) ensurePublicFallback(ctx context.Context, nodeID string, h api.HelloPayload) {
	if h.Network == nil {
		return
	}
	pub := s.publicURL(ctx, nil)
	if !domainURL(pub) || strings.TrimRight(h.Network.ControlURL, "/") == pub || contains(h.Network.FallbackURLs, pub) {
		return
	}
	cur := strings.TrimRight(h.Network.ControlURL, "/")
	if contains(h.Network.FallbackURLs, cur) {
		cur = "" // connected through a fallback: do not make it the primary
	}
	s.log.Info("adding the public control URL as agent fallback", "node", nodeID, "url", pub)
	s.hub.Fire(nodeID, api.MsgNetwork, api.NetworkPayload{Provider: h.Network.Provider, ControlURL: cur, FallbackURL: pub})
}

func contains(list []string, v string) bool {
	v = strings.TrimRight(v, "/")
	for _, x := range list {
		if strings.TrimRight(x, "/") == v {
			return true
		}
	}
	return false
}

// handleServerNetwork: POST /api/v1/servers/{id}/network {action: join|switch|public}.
func (s *Server) handleServerNetwork(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	n, err := s.st.GetNode(ctx, r.PathValue("id"))
	if err != nil {
		return err
	}
	var req api.ServerNetworkRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if !s.hub.Online(n.ID) {
		return errf(409, "server_offline", "%v", ErrNodeOffline)
	}
	pub := s.publicURL(ctx, r)
	p := api.NetworkPayload{Provider: api.NetworkProviderTailscale, FallbackURL: pub}
	timeout := 3 * time.Minute
	switch req.Action {
	case "join", "switch":
		if s.networkProvider(ctx) != api.NetworkProviderTailscale {
			return errf(409, "network_disabled", "enable the Tailscale network provider in Settings first")
		}
		p.ControlURL = s.tailnetControlURL(ctx)
		if p.ControlURL == "" {
			return errf(409, "no_tailnet_url", "the control server has no tailnet URL: set ATOLL_AGENT_LISTEN (e.g. tailscale:7301) or Settings → tailnet control URL")
		}
		if req.Action == "join" {
			p.AuthKey, _ = s.st.GetSetting(ctx, "tailscale_auth_key")
			if p.AuthKey == "" {
				return errf(409, "no_auth_key", "no Tailscale auth key stored")
			}
			timeout = 15 * time.Minute // may install Tailscale
		}
	case "public":
		p.ControlURL, p.FallbackURL = pub, ""
	default:
		return badRequest("action must be join, switch or public")
	}
	out, err := s.hub.Call(ctx, n.ID, api.MsgNetwork, p, timeout)
	if err != nil {
		return errf(502, "agent_error", "%s", tailnet.Redact(err.Error(), p.AuthKey))
	}
	var res api.NetworkResult
	json.Unmarshal([]byte(out), &res)
	s.st.Audit(ctx, principalFrom(ctx).actor(), "server.network", n.ID, map[string]string{"action": req.Action, "control_url": res.ControlURL})
	writeJSON(w, 200, res)
	return nil
}

// checkPlacement rejects putting a service on a server other than its siblings' unless both
// ends are on the tailnet (cross-server traffic never leaves the tailnet).
func (s *Server) checkPlacement(ctx context.Context, projectID, serviceID, nodeID string) error {
	svcs, err := s.st.ListServices(ctx, projectID)
	if err != nil {
		return err
	}
	nodes := map[string]bool{nodeID: true}
	for _, v := range svcs {
		if v.ID != serviceID {
			nodes[v.ServerID] = true
		}
	}
	if len(nodes) <= 1 {
		return nil
	}
	if s.networkProvider(ctx) != api.NetworkProviderTailscale {
		return errf(409, "tailnet_required", "services of one project on different servers talk over the tailnet: enable the Tailscale network provider in Settings first")
	}
	for id := range nodes {
		n, err := s.st.GetNode(ctx, id)
		if err != nil {
			return badRequest("unknown server %q", id)
		}
		if !tailnet.IsTailnetIP(n.TailnetIP) {
			return errf(409, "tailnet_required", "server %s is not on the tailnet yet (Servers → %s → Join tailnet)", n.Name, n.Name)
		}
	}
	return nil
}
