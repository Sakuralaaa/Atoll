package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestLatencyRing(t *testing.T) {
	var l latencyRing
	if l.view() != nil {
		t.Fatal("empty ring should have no view")
	}
	for i := 1; i <= latencyWindow+2; i++ {
		l.add(time.Duration(i)*10*time.Millisecond, time.Now())
	}
	v := l.view()
	// last 8 samples: 30..100 ms -> avg 65
	if v.LastMS != 100 || v.AvgMS != 65 || v.Samples != latencyWindow || v.Method != latencyByPing {
		t.Fatalf("view: %+v", v)
	}
}

func TestLatencyRingFailures(t *testing.T) {
	var l latencyRing
	now := time.Now()
	l.fail(errors.New("no pong"), now)
	l.fail(errors.New("still no pong"), now.Add(time.Second))
	v := l.view()
	if v == nil || v.Samples != 0 || v.Failures != 2 || v.FailuresTotal != 2 || v.LastError != "still no pong" || v.LastAttempt == nil {
		t.Fatalf("failed-only view: %+v", v)
	}
	l.addBy(42*time.Millisecond, now.Add(2*time.Second), latencyByMessage)
	v = l.view()
	if v.Failures != 0 || v.FailuresTotal != 2 || v.LastMS != 42 || v.Method != latencyByMessage {
		t.Fatalf("after success: %+v", v)
	}
	l.fail(errors.New("timeout"), now.Add(3*time.Second))
	if v = l.view(); v.Failures != 1 || v.Samples != 1 || v.LastError != "timeout" {
		t.Fatalf("failure after success keeps the last sample: %+v", v)
	}
}

// A ping that gets no pong is recorded as a failure (the panel shows 超时) and the API exposes
// it; the connection stays up and the next good ping clears the failure.
func TestLatencyFailuresRecorded(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	c := e.srv.hub.get(nodeID)
	if c == nil {
		t.Fatal("no connection")
	}
	old := latencyTimeout
	latencyTimeout = 200 * time.Millisecond
	t.Cleanup(func() { latencyTimeout = old })
	for deadline := time.Now().Add(5 * time.Second); c.latency.view() == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no sample at connect")
		}
	}
	badPing := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	var p latencyProbe
	e.srv.measureLatency(c, &p, badPing)
	v := c.latency.view()
	if v == nil || v.Failures != 1 || !strings.Contains(v.LastError, "no pong") || v.Samples != 1 {
		t.Fatalf("failure view: %+v", v)
	}
	if e.srv.hub.get(nodeID) != c || c.ctx.Err() != nil {
		t.Fatal("a failed ping must not drop the connection")
	}
	var srv api.Server
	e.mustDo("GET", "/api/v1/servers/"+nodeID, nil, &srv, 200)
	if srv.Latency == nil || srv.Latency.Failures != 1 || srv.Latency.LastError == "" {
		t.Fatalf("server view latency: %+v", srv.Latency)
	}
	e.srv.measureLatency(c, &p, func(context.Context) error { return nil })
	if v = c.latency.view(); v.Failures != 0 || v.Samples != 2 || v.Method != latencyByPing {
		t.Fatalf("after recovery: %+v", v)
	}
}
