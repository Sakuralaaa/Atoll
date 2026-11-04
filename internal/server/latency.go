package server

import (
	"context"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Latency: the control server pings each connected agent over its WebSocket (a protocol
// ping frame, answered by the agent's WebSocket library without any agent code), so every
// agent version reports a round-trip time. The latest value and a short rolling average are
// kept in memory only; they reset when the agent reconnects.

const (
	latencyEvery   = 15 * time.Second
	latencyTimeout = 10 * time.Second
	latencyWindow  = 8 // samples in the rolling average (~2 minutes)
)

type latencyRing struct {
	mu      sync.Mutex
	samples []time.Duration
	at      time.Time
}

func (l *latencyRing) add(d time.Duration, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.samples = append(l.samples, d)
	if len(l.samples) > latencyWindow {
		l.samples = l.samples[len(l.samples)-latencyWindow:]
	}
	l.at = now
}

// view returns the latest and average RTT in milliseconds (nil when nothing measured yet).
func (l *latencyRing) view() *api.NodeLatency {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.samples) == 0 {
		return nil
	}
	var sum time.Duration
	for _, d := range l.samples {
		sum += d
	}
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()/100) / 10 } // 0.1 ms
	return &api.NodeLatency{
		LastMS:     ms(l.samples[len(l.samples)-1]),
		AvgMS:      ms(sum / time.Duration(len(l.samples))),
		Samples:    len(l.samples),
		MeasuredAt: l.at.UTC(),
	}
}

// pingLoop measures the connection's RTT until it closes. A failed ping is not recorded
// (the heartbeat reaper decides about dead connections).
func (s *Server) pingLoop(c *agentConn) {
	measure := func() {
		ctx, cancel := context.WithTimeout(c.ctx, latencyTimeout)
		defer cancel()
		start := time.Now()
		if err := c.ws.Ping(ctx); err == nil {
			first := c.latency.view() == nil
			c.latency.add(time.Since(start), time.Now())
			if first {
				s.publishServer(c.ctx, c.nodeID)
			}
		}
	}
	measure()
	t := time.NewTicker(latencyEvery)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			measure()
		}
	}
}
