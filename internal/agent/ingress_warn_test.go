package agent_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) count(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Count(s.b.String(), sub)
}

// Without Cloudflare no origin certificate ever arrives; the agent used to log the same WARN
// on every ingress sync. It now warns once per missing spell, and not at all without routes.
func TestIngressWarnsOnceWithoutOriginCert(t *testing.T) {
	buf := &syncBuf{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	p := agent.Paths{Root: t.TempDir()}
	in := agent.NewIngress(agenttest.New(), p, &fwRecorder{}, log)
	in.SetGuards()
	in.LoadConfig = (&loads{}).load
	ctx := context.Background()
	const msg = "no origin certificate yet"

	empty := payload()
	empty.TLSMode = "cloudflare-origin"
	if err := in.Apply(ctx, empty); err != nil {
		t.Fatal(err)
	}
	if n := buf.count(msg); n != 0 {
		t.Fatalf("warned without routes: %d", n)
	}
	pl := payload("a.example.com")
	pl.TLSMode = "cloudflare-origin"
	for range 3 {
		if err := in.Apply(ctx, pl); err != nil {
			t.Fatal(err)
		}
	}
	if n := buf.count(msg); n != 1 {
		t.Fatalf("want 1 warning over 3 syncs, got %d", n)
	}
	// a certificate shows up, then goes away again: that is a new spell, warned once more
	dir := filepath.Join(p.TLSDir())
	os.MkdirAll(dir, 0o700)
	cert, key := filepath.Join(dir, "origin.crt"), filepath.Join(dir, "origin.key")
	os.WriteFile(cert, []byte("x"), 0o600)
	os.WriteFile(key, []byte("x"), 0o600)
	if err := in.Apply(ctx, pl); err != nil {
		t.Fatal(err)
	}
	os.Remove(cert)
	os.Remove(key)
	if err := in.Apply(ctx, pl); err != nil {
		t.Fatal(err)
	}
	if n := buf.count(msg); n != 2 {
		t.Fatalf("want 2 warnings after the certificate went away, got %d", n)
	}
}
