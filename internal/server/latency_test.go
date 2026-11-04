package server

import (
	"context"
	"errors"
	"strings"
	"sync"
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

// A blocked frame handler must not stop the read loop from queueing (and so from
// processing pongs); frames still run in order.
func TestFrameWorkerDoesNotBlockReader(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var got []string
	w := startFrameWorker(8, func(env inEnvelope) {
		<-release
		mu.Lock()
		got = append(got, env.ID)
		mu.Unlock()
	})
	done := make(chan struct{})
	go func() {
		for _, id := range []string{"a", "b", "c", "d"} {
			w.push(context.Background(), inEnvelope{ID: id})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("push blocked behind a slow handler")
	}
	close(release)
	w.stop()
	if strings.Join(got, "") != "abcd" {
		t.Fatalf("order: %v", got)
	}
	// a full queue gives up once the connection context ends
	block := make(chan struct{})
	w2 := startFrameWorker(1, func(inEnvelope) { <-block })
	w2.push(context.Background(), inEnvelope{}) // taken by the worker
	w2.push(context.Background(), inEnvelope{}) // fills the queue
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if w2.push(ctx, inEnvelope{}) {
		t.Fatal("push into a full queue should fail when ctx ends")
	}
	close(block)
	w2.stop()
}

// When WebSocket pings fail, the latency comes from the logs.close round trip that every
// agent version answers; when that fails too, the failure is recorded with its error.
func TestLatencyFallbackAndFailures(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	a, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	c := e.srv.hub.get(nodeID)
	if c == nil {
		t.Fatal("no connection")
	}
	old := latencyTimeout
	latencyTimeout = 300 * time.Millisecond
	t.Cleanup(func() { latencyTimeout = old })
	for deadline := time.Now().Add(5 * time.Second); c.latency.view() == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no sample at connect")
		}
	}
	c.latency.reset() // drop the sample taken at connect
	badPing := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

	var p latencyProbe
	e.srv.measureLatency(c, &p, badPing)
	v := c.latency.view()
	if v == nil || v.Samples != 1 || v.Method != latencyByMessage || v.Failures != 0 {
		t.Fatalf("fallback sample: %+v", v)
	}
	if len(a.received(api.MsgLogsClose)) == 0 {
		t.Fatal("fallback did not send logs.close")
	}

	// the agent stops answering the fallback too
	a.mu.Lock()
	a.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type == api.MsgLogsClose {
			time.Sleep(time.Second)
		}
		return api.ResultPayload{}, false
	}
	a.mu.Unlock()
	e.srv.measureLatency(c, &p, badPing)
	v = c.latency.view()
	if v.Failures != 1 || !strings.Contains(v.LastError, "no pong") || !strings.Contains(v.LastError, "fallback") || v.Samples != 1 {
		t.Fatalf("failure view: %+v", v)
	}
	// pings are given up on after repeated failures: the next tick skips the ping
	if p.pingFails < latencyPingFailMax {
		t.Fatalf("ping failures not counted: %+v", p)
	}
	e.srv.measureLatency(c, &p, func(context.Context) error { t.Fatal("ping should be skipped"); return nil })
	if v = c.latency.view(); v.Failures != 2 || !strings.Contains(v.LastError, "skipped") {
		t.Fatalf("skip view: %+v", v)
	}

	// the API exposes the failures
	var srv api.Server
	e.mustDo("GET", "/api/v1/servers/"+nodeID, nil, &srv, 200)
	if srv.Latency == nil || srv.Latency.Failures != 2 || srv.Latency.LastError == "" {
		t.Fatalf("server view latency: %+v", srv.Latency)
	}
}
