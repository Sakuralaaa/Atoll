package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/coder/websocket"
)

// ErrNodeOffline is returned when the target agent is not connected.
var ErrNodeOffline = errors.New("server is offline (agent not connected)")

type inEnvelope struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Hub tracks connected agents and correlates requests with results.
type Hub struct {
	s     *Server
	mu    sync.Mutex
	conns map[string]*agentConn
}

type agentConn struct {
	nodeID   string
	ws       *websocket.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	wmu      sync.Mutex
	pmu      sync.Mutex
	pending  map[string]chan api.ResultPayload
	streams  map[string]chan api.LogLine
	lastSeen atomic.Int64
}

func newHub(s *Server) *Hub { return &Hub{s: s, conns: map[string]*agentConn{}} }

// Online reports whether the node's agent is connected.
func (h *Hub) Online(nodeID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conns[nodeID] != nil
}

func (h *Hub) get(nodeID string) *agentConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conns[nodeID]
}

func (c *agentConn) send(ctx context.Context, env api.Envelope) error {
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, raw)
}

// Request sends a request frame and waits for its result.
func (h *Hub) Request(ctx context.Context, nodeID, typ string, payload any, timeout time.Duration) (api.ResultPayload, error) {
	c := h.get(nodeID)
	if c == nil {
		return api.ResultPayload{}, ErrNodeOffline
	}
	id := randString(16, idAlphabet)
	ch := make(chan api.ResultPayload, 1)
	c.pmu.Lock()
	c.pending[id] = ch
	c.pmu.Unlock()
	defer func() {
		c.pmu.Lock()
		delete(c.pending, id)
		c.pmu.Unlock()
	}()
	if err := c.send(ctx, api.Envelope{Type: typ, ID: id, Payload: payload}); err != nil {
		return api.ResultPayload{}, fmt.Errorf("send %s: %w", typ, err)
	}
	if timeout <= 0 {
		timeout = h.s.cfg.RequestTimeout
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case res, ok := <-ch:
		if !ok {
			return api.ResultPayload{}, fmt.Errorf("%s: agent disconnected before answering", typ)
		}
		return res, nil
	case <-t.C:
		return api.ResultPayload{}, fmt.Errorf("%s: agent did not answer within %s", typ, timeout)
	case <-ctx.Done():
		return api.ResultPayload{}, ctx.Err()
	case <-c.ctx.Done():
		return api.ResultPayload{}, fmt.Errorf("%s: agent disconnected", typ)
	}
}

// Call is Request that turns a failed result into an error and returns Output.
func (h *Hub) Call(ctx context.Context, nodeID, typ string, payload any, timeout time.Duration) (string, error) {
	res, err := h.Request(ctx, nodeID, typ, payload, timeout)
	if err != nil {
		return "", err
	}
	if !res.OK {
		if res.Error == "" {
			res.Error = "agent reported failure"
		}
		return res.Output, fmt.Errorf("%s: %s", typ, res.Error)
	}
	return res.Output, nil
}

// Fire sends a request in the background and logs a failed result.
func (h *Hub) Fire(nodeID, typ string, payload any) {
	if !h.Online(nodeID) {
		return
	}
	h.s.goBG(func(ctx context.Context) {
		if _, err := h.Call(ctx, nodeID, typ, payload, 0); err != nil {
			h.s.log.Warn("agent request failed", "node", nodeID, "type", typ, "err", err)
		}
	})
}

func (h *Hub) openStream(nodeID, streamID string) (chan api.LogLine, func()) {
	c := h.get(nodeID)
	if c == nil {
		return nil, func() {}
	}
	ch := make(chan api.LogLine, 256)
	c.pmu.Lock()
	c.streams[streamID] = ch
	c.pmu.Unlock()
	return ch, func() {
		c.pmu.Lock()
		delete(c.streams, streamID)
		c.pmu.Unlock()
	}
}

func (h *Hub) reapStale(timeout time.Duration) {
	h.mu.Lock()
	var stale []*agentConn
	for _, c := range h.conns {
		if time.Since(time.Unix(0, c.lastSeen.Load())) > timeout {
			stale = append(stale, c)
		}
	}
	h.mu.Unlock()
	for _, c := range stale {
		h.s.log.Warn("agent missed heartbeats; disconnecting", "node", c.nodeID)
		c.ws.Close(websocket.StatusPolicyViolation, "heartbeat timeout")
		c.cancel()
	}
}

func (h *Hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.conns {
		c.ws.Close(websocket.StatusGoingAway, "server shutting down")
		c.cancel()
	}
}

// Disconnect drops a node's connection (e.g. node deleted).
func (h *Hub) Disconnect(nodeID string) {
	if c := h.get(nodeID); c != nil {
		c.ws.Close(websocket.StatusNormalClosure, "node removed")
		c.cancel()
	}
}

// ---------- WebSocket endpoint ----------

func (s *Server) handleAgentConnect(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	nodeID, err := s.st.NodeByToken(r.Context(), hashToken(tok))
	if tok == "" || err != nil {
		writeJSON(w, 401, api.ErrorBody{Error: "invalid node token", Code: "unauthorized"})
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ws.SetReadLimit(64 << 20)
	ctx, cancel := context.WithCancel(s.bg)
	c := &agentConn{nodeID: nodeID, ws: ws, ctx: ctx, cancel: cancel,
		pending: map[string]chan api.ResultPayload{}, streams: map[string]chan api.LogLine{}}
	c.lastSeen.Store(time.Now().UnixNano())
	defer func() {
		cancel()
		ws.CloseNow()
		s.hub.mu.Lock()
		current := s.hub.conns[nodeID] == c
		if current {
			delete(s.hub.conns, nodeID)
		}
		s.hub.mu.Unlock()
		c.pmu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		for id, ch := range c.streams {
			close(ch)
			delete(c.streams, id)
		}
		c.pmu.Unlock()
		if current {
			s.st.SetNodeStatus(context.Background(), nodeID, "offline", false)
			s.publishServer(context.Background(), nodeID)
			s.log.Info("agent disconnected", "node", nodeID)
		}
	}()

	// First frame must be hello.
	hctx, hcancel := context.WithTimeout(ctx, 30*time.Second)
	env, err := readEnvelope(hctx, ws)
	hcancel()
	if err != nil || env.Type != api.MsgHello {
		ws.Close(websocket.StatusPolicyViolation, "expected hello")
		return
	}
	var hello api.HelloPayload
	json.Unmarshal(env.Payload, &hello)
	if err := s.st.UpdateNodeHello(ctx, nodeID, hello); err != nil {
		s.log.Error("hello", "err", err)
		return
	}
	s.hub.mu.Lock()
	if old := s.hub.conns[nodeID]; old != nil {
		old.ws.Close(websocket.StatusPolicyViolation, "replaced by new connection")
		old.cancel()
	}
	s.hub.conns[nodeID] = c
	s.hub.mu.Unlock()
	s.log.Info("agent connected", "node", nodeID, "version", hello.AgentVersion, "ip", hello.PublicIPv4)
	s.publishServer(ctx, nodeID)
	s.forgetMesh(nodeID)
	s.goBG(func(bg context.Context) {
		s.pushNodeState(bg, nodeID)
		s.reconcileNetwork(bg, nodeID, hello) // network.go: move to the tailnet control URL
	})

	for {
		env, err := readEnvelope(ctx, ws)
		if err != nil {
			return
		}
		c.lastSeen.Store(time.Now().UnixNano())
		s.handleAgentFrame(ctx, c, env)
	}
}

func readEnvelope(ctx context.Context, ws *websocket.Conn) (inEnvelope, error) {
	var env inEnvelope
	_, data, err := ws.Read(ctx)
	if err != nil {
		return env, err
	}
	err = json.Unmarshal(data, &env)
	return env, err
}

func (s *Server) handleAgentFrame(ctx context.Context, c *agentConn, env inEnvelope) {
	switch env.Type {
	case api.MsgResult:
		var res api.ResultPayload
		json.Unmarshal(env.Payload, &res)
		c.pmu.Lock()
		ch := c.pending[env.ID]
		c.pmu.Unlock()
		if ch != nil {
			select {
			case ch <- res:
			default:
			}
		}
	case api.MsgStatus:
		var st api.StatusPayload
		if json.Unmarshal(env.Payload, &st) == nil {
			s.handleStatus(ctx, c.nodeID, st)
		}
		s.st.TouchNode(ctx, c.nodeID)
	case api.MsgMetrics:
		var m api.MetricsPayload
		if json.Unmarshal(env.Payload, &m) == nil {
			s.handleMetrics(ctx, c.nodeID, m)
		}
	case api.MsgBackupEvent:
		var ev api.BackupEvent
		if json.Unmarshal(env.Payload, &ev) == nil {
			s.handleBackupEvent(ctx, c.nodeID, ev)
		}
	case api.MsgDeploy:
		var ev api.DeployEvent
		if json.Unmarshal(env.Payload, &ev) == nil {
			s.handleDeployEvent(ctx, c.nodeID, ev)
		}
	case api.MsgBuildLog:
		var ev api.BuildLogEvent
		if json.Unmarshal(env.Payload, &ev) == nil {
			s.handleBuildLog(ctx, c.nodeID, ev)
		}
	case api.MsgLogLine:
		var ev api.LogEvent
		if json.Unmarshal(env.Payload, &ev) == nil {
			c.pmu.Lock()
			ch := c.streams[ev.StreamID]
			if ch != nil {
				select {
				case ch <- ev.Line:
				default: // client too slow; drop line
				}
			}
			c.pmu.Unlock()
		}
	case api.MsgHello:
		var hello api.HelloPayload
		if json.Unmarshal(env.Payload, &hello) == nil {
			s.st.UpdateNodeHello(ctx, c.nodeID, hello)
			s.goBG(func(bg context.Context) { s.pushMeshAll(bg) })
		}
	default:
		s.log.Debug("unknown agent frame", "type", env.Type)
	}
}

func (s *Server) handleMetrics(ctx context.Context, nodeID string, m api.MetricsPayload) {
	if !m.Node.At.IsZero() || m.Node.MemTotalMB > 0 {
		s.st.AddNodeMetrics(ctx, nodeID, m.Node)
	}
	for _, cm := range m.Containers {
		if cm.ServiceID != "" {
			s.st.AddContainerMetrics(ctx, cm)
		}
	}
	s.st.TouchNode(ctx, nodeID)
}

func (s *Server) publishServer(ctx context.Context, nodeID string) {
	if n, err := s.st.GetNode(ctx, nodeID); err == nil {
		n.Usage, _ = s.st.LatestNodeMetrics(ctx, nodeID)
		s.publish("server", n)
	}
}
