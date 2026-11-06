package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent/tailnet"
	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/coder/websocket"
)

// Extension handles additional server→agent message types (e.g. the zeabur.* takeover messages).
type Extension interface {
	Handles(msgType string) bool
	Handle(ctx context.Context, msgType string, payload json.RawMessage) (string, error)
}

// Agent is the worker daemon: local reconciler + optional connection to the control server.
type Agent struct {
	Cfg      *Config
	RT       Runtime
	Paths    Paths
	Mgr      *Manager
	Ingress  *Ingress
	Volumes  *VolumeTransfer
	Builds   *Builder  // builds from source (MsgBuild)
	Upgrades *Upgrader // self-upgrade from the panel (MsgUpgrade); nil = unsupported
	// MeshToken: the mesh extension handles per-project tokens (announced in the hello).
	MeshToken bool
	Firewall  interface {
		Firewall
		Loop(ctx context.Context)
	}
	Ext []Extension
	Log *slog.Logger

	// Intervals (overridable in tests).
	StatusEvery  time.Duration // forced status frame
	StatusCheck  time.Duration // change detection poll
	MetricsEvery time.Duration
	// ImagePruneEvery is how often unused images older than ImagePruneAge are removed
	// (default 24h, first run 10 minutes after start; negative disables).
	ImagePruneEvery time.Duration
	ImagePruneAge   time.Duration // default 7 days
	MaxBackoff      time.Duration
	HelloFunc       func(ctx context.Context) api.HelloPayload
	// FailoverAfter is how many failed connection attempts on one control URL make the agent
	// try the next one (fallback URLs, e.g. public after a tailnet switch). Default 3.
	FailoverAfter int
	// ProbeTimeout bounds the reachability check before switching the control URL (60s).
	ProbeTimeout time.Duration
	HTTPClient   *http.Client // probes; nil = default with 10s timeout
	// CfgPath is where Cfg is persisted when the control URL changes ("" = memory only).
	CfgPath string
	Tailnet *tailnet.Client // nil = real tailscale CLI
	// MeshErr reports the cross-server forwarder status for the hello frame (optional).
	MeshErr      func() string
	cfgMu        sync.Mutex
	connURL      string
	switched     atomic.Bool
	sampler      NodeSampler
	connMu       sync.Mutex
	conn         *websocket.Conn
	queue        []api.Envelope // deploy events waiting for a connection
	streamsMu    sync.Mutex
	streams      map[string]context.CancelFunc
	statusKick   chan struct{}
	lastStatus   []byte
	lastStatusAt time.Time
	statusMu     sync.Mutex
}

// New wires an agent with the default components. fw may be nil (firewall disabled).
func New(cfg *Config, rt Runtime, fw interface {
	Firewall
	Loop(ctx context.Context)
}, log *slog.Logger) *Agent {
	if log == nil {
		log = slog.Default()
	}
	p := Paths{Root: cfg.DataDir}
	a := &Agent{
		Cfg: cfg, RT: rt, Paths: p, Firewall: fw, Log: log,
		StatusEvery: 30 * time.Second, StatusCheck: 5 * time.Second, MetricsEvery: 15 * time.Second,
		ImagePruneEvery: 24 * time.Hour, ImagePruneAge: 7 * 24 * time.Hour,
		MaxBackoff: 30 * time.Second,
		streams:    map[string]context.CancelFunc{}, statusKick: make(chan struct{}, 1),
	}
	a.Mgr = NewManager(rt, p, a.Emit, log)
	a.Mgr.OnChange = a.KickStatus
	var f Firewall
	if fw != nil {
		f = fw
	}
	a.Ingress = NewIngress(rt, p, f, log)
	if cfg.CaddyImage != "" {
		a.Ingress.Image = cfg.CaddyImage
	}
	a.Volumes = NewVolumeTransfer(rt, p, cfg.NodeToken)
	a.Builds = NewBuilder(rt, p, a.Emit, log)
	a.Builds.CurrentImage = func(serviceID string) string {
		if spec, err := a.Mgr.Store().Load(serviceID); err == nil {
			return spec.Image
		}
		return ""
	}
	a.Builds.HasSourceServices = func() bool {
		specs, err := a.Mgr.Store().List()
		if err != nil {
			return true // unknown: keep the builder
		}
		for _, s := range specs {
			if s.LocalImage || strings.HasPrefix(s.Image, BuildImageRepo) {
				return true
			}
		}
		return false
	}
	a.Mgr.OnRemoved = a.Builds.RemoveService
	a.HelloFunc = func(ctx context.Context) api.HelloPayload { return CollectHello(ctx, rt, cfg.NodeID) }
	return a
}

// KickStatus requests an immediate status frame.
func (a *Agent) KickStatus() {
	select {
	case a.statusKick <- struct{}{}:
	default:
	}
}

// Recover re-applies everything from local state: containers, ingress (and firewall).
func (a *Agent) Recover(ctx context.Context) error {
	err := a.Mgr.Reconcile(ctx)
	if ierr := a.Ingress.Restore(ctx); ierr != nil {
		err = errors.Join(err, fmt.Errorf("ingress: %w", ierr))
	}
	return err
}

// Run reconciles from local state, then keeps a connection to the server until ctx ends.
// Nothing here requires the server: containers run and are restored without it.
func (a *Agent) Run(ctx context.Context) error {
	go func() {
		if err := a.Recover(ctx); err != nil {
			a.Log.Error("boot reconcile", "err", err)
		}
	}()
	if a.Firewall != nil {
		go a.Firewall.Loop(ctx)
	}
	go a.statusLoop(ctx)
	go a.metricsLoop(ctx)
	go a.imagePruneLoop(ctx, 10*time.Minute)
	if a.Builds != nil {
		go a.Builds.IdleLoop(ctx)
	}
	a.connectLoop(ctx)
	return nil
}

// ---------- outbound ----------

// Emit sends an event; deploy and backup events are queued while disconnected, others are dropped.
func (a *Agent) Emit(msgType string, payload any) {
	env := api.Envelope{Type: msgType, Payload: payload}
	a.connMu.Lock()
	c := a.conn
	if c == nil {
		if queuedWhileOffline(msgType) {
			a.queue = append(a.queue, env)
			if len(a.queue) > 500 {
				a.queue = a.queue[len(a.queue)-500:]
			}
		}
		a.connMu.Unlock()
		return
	}
	a.connMu.Unlock()
	if err := a.write(c, env); err != nil && queuedWhileOffline(msgType) {
		a.connMu.Lock()
		a.queue = append(a.queue, env)
		a.connMu.Unlock()
	}
}

func (a *Agent) write(c *websocket.Conn, env api.Envelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageText, b)
}

func (a *Agent) statusLoop(ctx context.Context) {
	t := time.NewTicker(a.StatusCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-a.statusKick:
		}
		a.sendStatus(ctx, false)
	}
}

func (a *Agent) sendStatus(ctx context.Context, force bool) {
	st, err := a.Mgr.Status(ctx)
	if err != nil {
		a.Log.Warn("status", "err", err)
		return
	}
	if st == nil {
		st = []api.ContainerState{}
	}
	payload := api.StatusPayload{Containers: st}
	b, _ := json.Marshal(payload)
	a.statusMu.Lock()
	changed := !bytes.Equal(b, a.lastStatus)
	due := time.Since(a.lastStatusAt) >= a.StatusEvery
	if !(force || changed || due) {
		a.statusMu.Unlock()
		return
	}
	a.connMu.Lock()
	connected := a.conn != nil
	a.connMu.Unlock()
	if connected {
		a.lastStatus, a.lastStatusAt = b, time.Now()
	}
	a.statusMu.Unlock()
	if connected {
		a.Emit(api.MsgStatus, payload)
	}
}

func (a *Agent) metricsLoop(ctx context.Context) {
	t := time.NewTicker(a.MetricsEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.connMu.Lock()
		connected := a.conn != nil
		a.connMu.Unlock()
		node := a.sampler.Node(ctx) // sample even when offline to keep rate deltas meaningful
		if !connected {
			continue
		}
		cs := a.sampler.Containers(ctx, a.RT)
		if cs == nil {
			cs = []api.ContainerMetrics{}
		}
		a.Emit(api.MsgMetrics, api.MetricsPayload{Node: node, Containers: cs})
	}
}

// imagePruneLoop removes old unused images so pulls and builds do not fill the disk. Images a
// container still uses (running or stopped) and Atoll's build images are kept. It also runs
// the daily builder cleanup (Builder.Maintain).
func (a *Agent) imagePruneLoop(ctx context.Context, first time.Duration) {
	if a.ImagePruneEvery < 0 || a.RT == nil {
		return
	}
	every, age := a.ImagePruneEvery, a.ImagePruneAge
	if every == 0 {
		every = 24 * time.Hour
	}
	if age <= 0 {
		age = 7 * 24 * time.Hour
	}
	wait := first
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = every
		if n, err := a.RT.PruneImages(ctx, age); err != nil {
			a.Log.Warn("prune images", "err", err)
		} else if n > 0 {
			a.Log.Info("pruned unused images", "reclaimed_mb", n>>20)
		}
		if a.Builds != nil {
			a.Builds.Maintain(ctx)
		}
	}
}

// ---------- connection ----------

func wsURL(server string) (string, error) {
	u, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("server url %q must be http(s)", server)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/agent/v1/connect"
	return u.String(), nil
}

func (a *Agent) connectLoop(ctx context.Context) {
	maxBackoff := a.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}
	failover := a.FailoverAfter
	if failover <= 0 {
		failover = 3
	}
	initial := min(time.Second, maxBackoff)
	backoff := initial
	idx, fails := 0, 0
	for ctx.Err() == nil {
		urls := a.controlURLs()
		if idx >= len(urls) {
			idx = 0
		}
		u := urls[idx]
		start := time.Now()
		connected, err := a.session(ctx, u)
		if ctx.Err() != nil {
			return
		}
		if a.switched.Swap(false) {
			// The primary URL changed: reconnect right away, starting with the new primary.
			idx, fails, backoff = 0, 0, initial
			continue
		}
		if connected {
			idx, fails = 0, 0 // after a working session always prefer the primary again
		} else if fails++; fails >= failover && len(urls) > 1 {
			idx, fails = (idx+1)%len(urls), 0
			a.Log.Warn("control URL keeps failing; trying the next one", "failed", u, "next", urls[idx])
		}
		if time.Since(start) > time.Minute {
			backoff = initial
		}
		// full jitter in [backoff/2, backoff]
		sleep := backoff/2 + time.Duration(rand.Int64N(int64(backoff/2)+1))
		a.Log.Warn("server connection lost; containers keep running", "err", err, "retry_in", sleep.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

type inbound struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// session runs one WebSocket connection to server until it fails. connected reports whether
// the connection was established (hello sent).
func (a *Agent) session(ctx context.Context, server string) (connected bool, err error) {
	u, err := wsURL(server)
	if err != nil {
		return false, err
	}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+a.Cfg.NodeToken)
	c, resp, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPHeader: hdr})
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return false, fmt.Errorf("server rejected node token (401); re-run `atoll-agent join`: %w", err)
		}
		return false, err
	}
	defer c.CloseNow()
	c.SetReadLimit(64 << 20)

	a.connMu.Lock()
	a.connURL = strings.TrimRight(server, "/")
	a.connMu.Unlock()
	hctx, hcancel := context.WithTimeout(ctx, 15*time.Second)
	hello := a.HelloFunc(hctx)
	if hello.TailnetIP == "" || hello.Network == nil {
		ip, nn := a.networkInfo(hctx)
		if hello.TailnetIP == "" {
			hello.TailnetIP = ip
		}
		if hello.Network == nil {
			hello.Network = nn
		}
	}
	hcancel()
	hello.SelfUpgrade = a.Upgrades != nil
	hello.MeshToken = a.MeshToken
	if err := a.write(c, api.Envelope{Type: api.MsgHello, Payload: hello}); err != nil {
		return false, err
	}
	a.connMu.Lock()
	a.conn = c
	queued := a.queue
	a.queue = nil
	a.connMu.Unlock()
	a.Log.Info("connected to server", "url", u)
	defer func() {
		a.connMu.Lock()
		if a.conn == c {
			a.conn = nil
		}
		a.connMu.Unlock()
		a.closeAllStreams()
	}()
	for _, env := range queued {
		if err := a.write(c, env); err != nil {
			return true, err
		}
	}
	go a.sendStatus(ctx, true)

	sctx, scancel := context.WithCancel(ctx)
	defer scancel()
	if a.Upgrades != nil {
		a.Upgrades.MarkConnected()
		go a.reportUpgrade(sctx)
	}
	go func() { // keepalive: detect half-open connections
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-t.C:
				pctx, pc := context.WithTimeout(sctx, 15*time.Second)
				err := c.Ping(pctx)
				pc()
				if err != nil && sctx.Err() == nil {
					c.Close(websocket.StatusGoingAway, "ping timeout")
					return
				}
			}
		}
	}()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return true, err
		}
		var msg inbound
		if err := json.Unmarshal(data, &msg); err != nil {
			a.Log.Warn("bad frame from server", "err", err)
			continue
		}
		go a.dispatch(ctx, c, msg)
	}
}

func (a *Agent) dispatch(ctx context.Context, c *websocket.Conn, msg inbound) {
	out, err := a.Handle(ctx, msg.Type, msg.Payload)
	if msg.ID == "" {
		if err != nil {
			a.Log.Error("handle "+msg.Type, "err", err)
		}
		return
	}
	res := api.ResultPayload{OK: err == nil, Output: out}
	if err != nil {
		res.Error = err.Error()
	}
	// Reply on the current connection (the original may have been replaced by a reconnect).
	a.connMu.Lock()
	cur := a.conn
	a.connMu.Unlock()
	if cur == nil {
		cur = c
	}
	if werr := a.write(cur, api.Envelope{Type: api.MsgResult, ID: msg.ID, Payload: res}); werr != nil {
		a.Log.Warn("could not deliver result", "id", msg.ID, "type", msg.Type, "err", werr)
	}
}

// ---------- request handling ----------

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, errors.New("missing payload")
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("bad payload: %w", err)
	}
	return v, nil
}

// Handle executes one server→agent message and returns the result output.
func (a *Agent) Handle(ctx context.Context, msgType string, raw json.RawMessage) (string, error) {
	switch msgType {
	case api.MsgApply:
		p, err := decode[api.ApplyPayload](raw)
		if err != nil {
			return "", err
		}
		return "", a.Mgr.Apply(ctx, p.Spec)
	case api.MsgRemove:
		p, err := decode[api.RemovePayload](raw)
		if err != nil {
			return "", err
		}
		return "", a.Mgr.Remove(ctx, p.ServiceID, p.KeepVolumes)
	case api.MsgAction:
		p, err := decode[api.ActionPayload](raw)
		if err != nil {
			return "", err
		}
		return "", a.Mgr.Action(ctx, p.ServiceID, p.Action)
	case api.MsgSync:
		p, err := decode[api.SyncPayload](raw)
		if err != nil {
			return "", err
		}
		return "", a.Mgr.Sync(ctx, p.Specs)
	case api.MsgIngress:
		p, err := decode[api.IngressPayload](raw)
		if err != nil {
			return "", err
		}
		return "", a.Ingress.Apply(ctx, p)
	case api.MsgTLS:
		p, err := decode[api.TLSPayload](raw)
		if err != nil {
			return "", err
		}
		return "", a.Ingress.SaveTLS(ctx, p)
	case api.MsgLogsOpen:
		p, err := decode[api.LogsOpenPayload](raw)
		if err != nil {
			return "", err
		}
		return "", a.openLogs(ctx, p)
	case api.MsgLogsClose:
		p, err := decode[api.LogsOpenPayload](raw)
		if err != nil {
			return "", err
		}
		a.closeStream(p.StreamID)
		return "", nil
	case api.MsgExec:
		p, err := decode[api.ExecPayload](raw)
		if err != nil {
			return "", err
		}
		return a.exec(ctx, p)
	case api.MsgNetwork:
		p, err := decode[api.NetworkPayload](raw)
		if err != nil {
			return "", err
		}
		return a.handleNetwork(ctx, p)
	case api.MsgBuild:
		p, err := decode[api.BuildPayload](raw)
		if err != nil {
			return "", err
		}
		res, err := a.Builds.Build(ctx, p)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(res)
		return string(b), nil
	case api.MsgUpgrade:
		p, err := decode[api.UpgradePayload](raw)
		if err != nil {
			return "", err
		}
		return a.handleUpgrade(ctx, p)
	case api.MsgVolumeExport, api.MsgVolumeImport:
		p, err := decode[api.VolumeTransferPayload](raw)
		if err != nil {
			return "", err
		}
		if msgType == api.MsgVolumeExport {
			return a.Volumes.Export(ctx, p.ServiceID, p.Volume, p.URL)
		}
		return a.Volumes.Import(ctx, p.ServiceID, p.Volume, p.URL)
	}
	for _, e := range a.Ext {
		if e.Handles(msgType) {
			return e.Handle(ctx, msgType, raw)
		}
	}
	return "", fmt.Errorf("unsupported message type %q", msgType)
}

func (a *Agent) openLogs(ctx context.Context, p api.LogsOpenPayload) error {
	if p.StreamID == "" {
		return errors.New("stream_id required")
	}
	name := ContainerName(p.ServiceID)
	if _, err := a.RT.InspectContainer(ctx, name); err != nil {
		return fmt.Errorf("service %s: %w", p.ServiceID, err)
	}
	sctx, cancel := context.WithCancel(ctx)
	a.streamsMu.Lock()
	if old, ok := a.streams[p.StreamID]; ok {
		old()
	}
	a.streams[p.StreamID] = cancel
	a.streamsMu.Unlock()
	tail := p.Tail
	if tail <= 0 {
		tail = 200
	}
	go func() {
		defer a.closeStream(p.StreamID)
		err := a.RT.Logs(sctx, name, tail, p.Follow, func(r LogRecord) {
			a.Emit(api.MsgLogLine, api.LogEvent{StreamID: p.StreamID, Line: api.LogLine{
				ServiceID: p.ServiceID, At: r.At, Stream: r.Stream, Line: r.Line,
			}})
		})
		if err != nil && sctx.Err() == nil {
			a.Log.Warn("log stream ended", "stream", p.StreamID, "err", err)
		}
	}()
	return nil
}

func (a *Agent) closeStream(id string) {
	a.streamsMu.Lock()
	if c, ok := a.streams[id]; ok {
		c()
		delete(a.streams, id)
	}
	a.streamsMu.Unlock()
}

func (a *Agent) closeAllStreams() {
	a.streamsMu.Lock()
	for id, c := range a.streams {
		c()
		delete(a.streams, id)
	}
	a.streamsMu.Unlock()
}

// limitedBuffer keeps at most max bytes (then marks truncation).
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
			l.truncated = true
		} else {
			l.buf.Write(p)
		}
	} else {
		l.truncated = true
	}
	return len(p), nil
}

func (a *Agent) exec(ctx context.Context, p api.ExecPayload) (string, error) {
	if len(p.Cmd) == 0 {
		return "", errors.New("cmd required")
	}
	timeout := time.Duration(p.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timeout = min(timeout, time.Hour)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out := &limitedBuffer{max: 1 << 20}
	code, err := a.RT.Exec(ctx, ContainerName(p.ServiceID), p.Cmd, nil, out, out)
	s := out.buf.String()
	if out.truncated {
		s += "\n[output truncated]"
	}
	if err != nil {
		return s, err
	}
	if code != 0 {
		return s, fmt.Errorf("exit code %d", code)
	}
	return s, nil
}
