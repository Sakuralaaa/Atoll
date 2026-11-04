package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Latency: the control server pings each connected agent over its WebSocket (a protocol
// ping frame, answered by the agent's WebSocket library without any agent code), so every
// agent version reports a round-trip time. When pings keep failing (a proxy on the path
// that swallows control frames, or an agent busy writing big frames), the round trip is
// measured with an application-level request every agent version answers instead
// (logs.close for a stream that does not exist: a no-op that replies at once).
//
// The latest value, a short rolling average and the failures are kept in memory only;
// they reset when the agent reconnects.

const (
	latencyEvery  = 15 * time.Second
	latencyWindow = 8 // samples in the rolling average (~2 minutes)
)

// latencyTimeout bounds one ping (a var for tests).
var latencyTimeout = 20 * time.Second

const (
	latencyByPing    = "ping"
	latencyByMessage = "message"
)

type latencyRing struct {
	mu       sync.Mutex
	samples  []time.Duration
	at       time.Time
	method   string
	fails    int // consecutive failed measurements
	failsAll int
	lastErr  string
	attempt  time.Time
}

func (l *latencyRing) add(d time.Duration, now time.Time) { l.addBy(d, now, latencyByPing) }

func (l *latencyRing) addBy(d time.Duration, now time.Time, method string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.samples = append(l.samples, d)
	if len(l.samples) > latencyWindow {
		l.samples = l.samples[len(l.samples)-latencyWindow:]
	}
	l.at, l.attempt, l.method, l.fails = now, now, method, 0
}

// reset forgets every sample and failure.
func (l *latencyRing) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.samples, l.at, l.method, l.fails, l.failsAll, l.lastErr, l.attempt = nil, time.Time{}, "", 0, 0, "", time.Time{}
}

// fail records a measurement that failed (nothing answered in time).
func (l *latencyRing) fail(err error, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails++
	l.failsAll++
	l.lastErr = err.Error()
	l.attempt = now
}

// view returns the latest and average RTT in milliseconds plus the failures; nil when
// nothing was attempted yet. Samples == 0 with Failures > 0 means no attempt succeeded.
func (l *latencyRing) view() *api.NodeLatency {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.samples) == 0 && l.failsAll == 0 {
		return nil
	}
	v := &api.NodeLatency{Method: l.method, Failures: l.fails, FailuresTotal: l.failsAll, LastError: l.lastErr}
	if !l.attempt.IsZero() {
		at := l.attempt.UTC()
		v.LastAttempt = &at
	}
	if len(l.samples) == 0 {
		return v
	}
	var sum time.Duration
	for _, d := range l.samples {
		sum += d
	}
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()/100) / 10 } // 0.1 ms
	v.LastMS = ms(l.samples[len(l.samples)-1])
	v.AvgMS = ms(sum / time.Duration(len(l.samples)))
	v.Samples = len(l.samples)
	v.MeasuredAt = l.at.UTC()
	return v
}

type latencyProbe struct {
	warned bool // the first failure of this connection was logged at warn
}

// pingLoop measures the control-plane round trip with a WebSocket ping every latencyEvery,
// while the connection lives. A failed ping is recorded (the panel shows 超时) but never
// closes the connection: the agent's own keepalive and the heartbeat reaper decide that.
func (s *Server) pingLoop(c *agentConn) {
	var p latencyProbe
	ping := func(ctx context.Context) error { return c.ws.Ping(ctx) }
	s.measureLatency(c, &p, ping)
	t := time.NewTicker(latencyEvery)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			s.measureLatency(c, &p, ping)
		}
	}
}

func (s *Server) measureLatency(c *agentConn, p *latencyProbe, ping func(context.Context) error) {
	if c.ctx.Err() != nil {
		return
	}
	first := c.latency.view() == nil
	ctx, cancel := context.WithTimeout(c.ctx, latencyTimeout)
	start := time.Now()
	err := ping(ctx)
	d := time.Since(start)
	cancel()
	if err == nil {
		c.latency.addBy(d, time.Now(), latencyByPing)
		s.latencyChanged(c, first)
		return
	}
	if c.ctx.Err() != nil {
		return // connection closed while pinging
	}
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("WebSocket ping: no pong within %s", latencyTimeout)
	} else {
		err = fmt.Errorf("WebSocket ping: %w", err)
	}
	c.latency.fail(err, time.Now())
	s.logLatency(c, p, slog.LevelWarn, "agent latency could not be measured", err)
	s.latencyChanged(c, first || c.latency.view().Failures == 1)
}

func (s *Server) logLatency(c *agentConn, p *latencyProbe, lvl slog.Level, msg string, err error) {
	if p.warned {
		lvl = slog.LevelDebug
	}
	p.warned = true
	s.log.Log(c.ctx, lvl, msg, "node", c.nodeID, "err", err)
}

// latencyChanged publishes the server when the latency view changes state (first value,
// first failure); steady values reach the UI with the regular server refreshes.
func (s *Server) latencyChanged(c *agentConn, changed bool) {
	if changed {
		s.publishServer(c.ctx, c.nodeID)
	}
}
