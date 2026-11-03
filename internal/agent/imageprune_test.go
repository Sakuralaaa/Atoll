package agent

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type pruneRT struct {
	Runtime
	mu    sync.Mutex
	calls []time.Duration
}

func (p *pruneRT) PruneImages(_ context.Context, age time.Duration) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, age)
	return 5 << 20, nil
}

func (p *pruneRT) n() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.calls) }

func TestImagePruneLoop(t *testing.T) {
	rt := &pruneRT{}
	a := &Agent{RT: rt, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), ImagePruneEvery: 20 * time.Millisecond, ImagePruneAge: 48 * time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.imagePruneLoop(ctx, 0); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for rt.n() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if rt.n() < 2 || rt.calls[0] != 48*time.Hour {
		t.Fatalf("prune calls %v", rt.calls)
	}

	off := &pruneRT{}
	a = &Agent{RT: off, Log: a.Log, ImagePruneEvery: -1}
	a.imagePruneLoop(context.Background(), 0) // returns at once when disabled
	if off.n() != 0 {
		t.Fatal("disabled loop pruned")
	}
}
