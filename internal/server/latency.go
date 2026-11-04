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
	// after this many consecutive failed WebSocket pings the loop measures with the
	// fallback message only, retrying a ping every latencyPingRetry ticks
	latencyPingFailMax = 2
	latencyPingRetry   = 8
)

// latencyTimeout bounds one ping and one fallback round trip (a var for tests).
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

// latencyProbe is the per-connection state of the ping loop.
type latencyProbe struct {
	pingFails int  // consecutive failed WebSocket pings
	ticks     int  // measurements since pings were given up on
	warned    bool // the first failure of this connection was logged at warn
}

// pingLoop measures the connection's RTT until it closes.
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

// measureLatency takes one sample: a WebSocket ping, or the fallback message when pings
// failed (now or repeatedly before). Both failing records a failure on the ring.
func (s *Server) measureLatency(c *agentConn, p *latencyProbe, ping func(context.Context) error) {
	if c.ctx.Err() != nil {
		return
	}
	first := c.latency.view() == nil
	var pingErr error
	if p.pingFails >= latencyPingFailMax {
		p.ticks++
	}
	tryPing := p.pingFails < latencyPingFailMax || p.ticks%latencyPingRetry == 0
	if tryPing {
		ctx, cancel := context.WithTimeout(c.ctx, latencyTimeout)
		start := time.Now()
		pingErr = ping(ctx)
		d := time.Since(start)
		cancel()
		if pingErr == nil {
			if p.pingFails >= latencyPingFailMax {
				s.log.Info("agent answers WebSocket pings again", "node", c.nodeID)
			}
			p.pingFails, p.ticks = 0, 0
			c.latency.addBy(d, time.Now(), latencyByPing)
			s.latencyChanged(c, first)
			return
		}
		if c.ctx.Err() != nil {
			return // connection closed while pinging
		}
		p.pingFails++
		if errors.Is(pingErr, context.DeadlineExceeded) {
			pingErr = fmt.Errorf("WebSocket ping: no pong within %s", latencyTimeout)
		} else {
			pingErr = fmt.Errorf("WebSocket ping: %w", pingErr)
		}
	} else {
		pingErr = errors.New("WebSocket ping skipped after repeated failures")
	}
	d, err := s.probeRTT(c)
	if err == nil {
		if tryPing {
			s.logLatency(c, p, slog.LevelWarn, "agent did not answer a WebSocket ping; measuring latency with a message instead", pingErr)
		}
		c.latency.addBy(d, time.Now(), latencyByMessage)
		s.latencyChanged(c, first)
		return
	}
	if c.ctx.Err() != nil {
		return
	}
	err = fmt.Errorf("%v; fallback: %v", pingErr, err)
	c.latency.fail(err, time.Now())
	s.logLatency(c, p, slog.LevelWarn, "agent latency could not be measured", err)
	s.latencyChanged(c, first || c.latency.view().Failures == 1)
}

// logLatency logs at warn the first time per connection, at debug afterwards.
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

// probeRTT times an application-level round trip: logs.close for a stream id that does
// not exist, which every agent version handles as a no-op and answers at once. The answer
// is timestamped by the connection's read loop, so a busy frame worker does not inflate it.
func (s *Server) probeRTT(c *agentConn) (time.Duration, error) {
	id := "rtt-" + randString(16, idAlphabet)
	ch := make(chan time.Time, 1)
	c.pmu.Lock()
	c.probes[id] = ch
	c.pmu.Unlock()
	defer func() {
		c.pmu.Lock()
		delete(c.probes, id)
		c.pmu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(c.ctx, latencyTimeout)
	defer cancel()
	start := time.Now()
	if err := c.send(ctx, api.Envelope{Type: api.MsgLogsClose, ID: id, Payload: api.LogsOpenPayload{StreamID: id}}); err != nil {
		return 0, fmt.Errorf("send: %w", err)
	}
	select {
	case at := <-ch:
		return at.Sub(start), nil
	case <-ctx.Done():
		if c.ctx.Err() != nil {
			return 0, errors.New("agent disconnected")
		}
		return 0, fmt.Errorf("no answer within %s", latencyTimeout)
	}
}

// takeProbe hands a result frame to a waiting probeRTT; false when it is not a probe.
func (c *agentConn) takeProbe(env inEnvelope, at time.Time) bool {
	if env.Type != api.MsgResult || env.ID == "" {
		return false
	}
	c.pmu.Lock()
	ch, ok := c.probes[env.ID]
	c.pmu.Unlock()
	if ok {
		select {
		case ch <- at:
		default:
		}
	}
	return ok
}
