package selfupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fake binaries are text files whose content is their version.
type fakeHost struct {
	t        *testing.T
	target   string
	running  string // version "running" after the last restart
	healthy  map[string]bool
	restarts int
	clock    time.Time
}

func newFakeHost(t *testing.T, current string) *fakeHost {
	dir := t.TempDir()
	h := &fakeHost{t: t, target: filepath.Join(dir, "atoll-server"), running: current, healthy: map[string]bool{current: true}, clock: time.Unix(1_700_000_000, 0)}
	os.WriteFile(h.target, []byte(current), 0o755)
	return h
}

func (h *fakeHost) ops() Ops {
	return Ops{
		Restart: func(context.Context) error {
			b, _ := os.ReadFile(h.target)
			h.running = string(b)
			h.restarts++
			return nil
		},
		Healthy: func(_ context.Context, want string) error {
			if !h.healthy[h.running] {
				return errors.New("connection refused")
			}
			if want != "" && want != h.running {
				return errors.New("wrong version " + h.running)
			}
			return nil
		},
		Version: func(_ context.Context, bin string) (string, error) {
			b, err := os.ReadFile(bin)
			return string(b), err
		},
		Now:   func() time.Time { return h.clock },
		Sleep: func(d time.Duration) { h.clock = h.clock.Add(d) },
	}
}

func (h *fakeHost) read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func (h *fakeHost) newBinary(v string) string {
	p := filepath.Join(filepath.Dir(h.target), "atoll-server.new")
	os.WriteFile(p, []byte(v), 0o755)
	return p
}

func TestUpdateHealthy(t *testing.T) {
	h := newFakeHost(t, "v1")
	h.healthy["v2"] = true
	res := Run(context.Background(), Config{Mode: ModeUpdate, Target: h.target, New: h.newBinary("v2"), Result: filepath.Join(filepath.Dir(h.target), "result.json")}, h.ops())
	if !res.OK || res.RolledBack || res.FromVersion != "v1" || res.ToVersion != "v2" {
		t.Fatalf("result %+v", res)
	}
	if h.read(h.target) != "v2" || h.read(PrevPath(h.target)) != "v1" || h.running != "v2" {
		t.Fatalf("target=%s prev=%s running=%s", h.read(h.target), h.read(PrevPath(h.target)), h.running)
	}
	if r, err := ReadResult(filepath.Join(filepath.Dir(h.target), "result.json")); err != nil || !r.OK {
		t.Fatalf("result file %+v %v", r, err)
	}
	if _, err := os.Stat(h.target + ".swap"); err == nil {
		t.Fatal("temp file left behind")
	}
}

func TestUpdateUnhealthyRollsBack(t *testing.T) {
	h := newFakeHost(t, "v1") // v2 never becomes healthy
	res := Run(context.Background(), Config{Mode: ModeUpdate, Target: h.target, New: h.newBinary("v2"), Timeout: 60 * time.Second, Poll: 2 * time.Second}, h.ops())
	if res.OK || !res.RolledBack || !strings.Contains(res.Error, "did not become healthy") {
		t.Fatalf("result %+v", res)
	}
	if h.read(h.target) != "v1" || h.running != "v1" || h.restarts != 2 {
		t.Fatalf("target=%s running=%s restarts=%d", h.read(h.target), h.running, h.restarts)
	}
	// the failed binary is kept as .prev for inspection, not as the target
	if h.read(PrevPath(h.target)) != "v2" {
		t.Fatalf("prev=%s", h.read(PrevPath(h.target)))
	}
	// the health wait honoured the timeout (about 60s of fake time)
	if el := h.clock.Sub(time.Unix(1_700_000_000, 0)); el < 60*time.Second || el > 70*time.Second {
		t.Fatalf("waited %v", el)
	}
}

func TestRollbackSwapsAndToggles(t *testing.T) {
	h := newFakeHost(t, "v2")
	h.healthy["v1"] = true
	os.WriteFile(PrevPath(h.target), []byte("v1"), 0o755)
	res := Run(context.Background(), Config{Mode: ModeRollback, Target: h.target}, h.ops())
	if !res.OK || res.ToVersion != "v1" || h.read(h.target) != "v1" || h.read(PrevPath(h.target)) != "v2" {
		t.Fatalf("res %+v target=%s prev=%s", res, h.read(h.target), h.read(PrevPath(h.target)))
	}
	// rolling back again goes forward to v2
	res = Run(context.Background(), Config{Mode: ModeRollback, Target: h.target}, h.ops())
	if !res.OK || h.read(h.target) != "v2" || h.read(PrevPath(h.target)) != "v1" {
		t.Fatalf("second rollback %+v", res)
	}
}

func TestRollbackUnhealthyRestores(t *testing.T) {
	h := newFakeHost(t, "v2")
	os.WriteFile(PrevPath(h.target), []byte("v1"), 0o755) // v1 is not healthy
	res := Run(context.Background(), Config{Mode: ModeRollback, Target: h.target, Timeout: 10 * time.Second}, h.ops())
	if res.OK || !res.RolledBack || h.read(h.target) != "v2" || h.running != "v2" {
		t.Fatalf("res %+v target=%s running=%s", res, h.read(h.target), h.running)
	}
}

func TestRollbackWithoutPrev(t *testing.T) {
	h := newFakeHost(t, "v2")
	res := Run(context.Background(), Config{Mode: ModeRollback, Target: h.target}, h.ops())
	if res.OK || h.restarts != 0 || h.read(h.target) != "v2" {
		t.Fatalf("res %+v", res)
	}
}

func TestParseVersion(t *testing.T) {
	if ParseVersion("v0.2.0\n") != "v0.2.0" || ParseVersion("atoll-server v1.0.0") != "v1.0.0" || ParseVersion("") != "" {
		t.Fatal("parse")
	}
}
