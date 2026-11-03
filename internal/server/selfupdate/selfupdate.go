// Package selfupdate swaps the atoll-server binary and restarts it, restoring the previous
// binary when the new one does not come up healthy. It runs in a detached helper process
// (`atoll-server control-helper`, started with systemd-run so it survives the restart of
// atoll-server), and is plain Go so the swap/rollback decisions are unit-tested; the only
// shell-like parts are the injected Restart and Version functions.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Modes.
const (
	ModeUpdate   = "update"   // install New as Target, keep the old one as Target.prev
	ModeRollback = "rollback" // swap Target and Target.prev
)

// Config describes one swap.
type Config struct {
	Mode    string
	Target  string // /usr/local/bin/atoll-server
	New     string // verified new binary (update mode)
	Timeout time.Duration
	Poll    time.Duration
	Result  string // where the api.UpdateResult JSON is written ("" = not written)
}

// Ops are the side effects (real ones in cmd/atoll-server, fakes in tests).
type Ops struct {
	// Restart restarts the service (systemctl restart atoll-server).
	Restart func(ctx context.Context) error
	// Healthy reports nil when the running server answers its health check and runs version
	// want ("" = any version).
	Healthy func(ctx context.Context, want string) error
	// Version runs `<binary> version`.
	Version func(ctx context.Context, binary string) (string, error)
	Now     func() time.Time
	Sleep   func(time.Duration)
}

// PrevPath is where the previous binary is kept.
func PrevPath(target string) string { return target + ".prev" }

// Run performs the swap described by cfg and returns the result (also written to cfg.Result).
func Run(ctx context.Context, cfg Config, ops Ops) api.UpdateResult {
	if ops.Now == nil {
		ops.Now = time.Now
	}
	if ops.Sleep == nil {
		ops.Sleep = time.Sleep
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 2 * time.Second
	}
	res := api.UpdateResult{Mode: cfg.Mode, StartedAt: ops.Now().UTC()}
	res.FromVersion, _ = ops.Version(ctx, cfg.Target)
	err := run(ctx, cfg, ops, &res)
	if err != nil {
		res.Error = err.Error()
	}
	res.OK = err == nil
	res.FinishedAt = ops.Now().UTC()
	if cfg.Result != "" {
		WriteResult(cfg.Result, res)
	}
	return res
}

func run(ctx context.Context, cfg Config, ops Ops, res *api.UpdateResult) error {
	prev := PrevPath(cfg.Target)
	// the binary that will run after the swap, and what goes back on failure
	var incoming string
	switch cfg.Mode {
	case ModeUpdate:
		incoming = cfg.New
	case ModeRollback:
		incoming = prev
	default:
		return fmt.Errorf("unknown mode %q", cfg.Mode)
	}
	if _, err := os.Stat(incoming); err != nil {
		return fmt.Errorf("nothing to install: %w", err)
	}
	want, err := ops.Version(ctx, incoming)
	if err != nil {
		return fmt.Errorf("%s does not run: %w", incoming, err)
	}
	res.ToVersion = want

	// keep the running binary aside (it becomes .prev after a successful swap)
	aside := cfg.Target + ".swap"
	if err := copyFile(cfg.Target, aside); err != nil {
		return fmt.Errorf("keep current binary: %w", err)
	}
	defer os.Remove(aside)
	if err := installFile(incoming, cfg.Target); err != nil {
		return fmt.Errorf("install: %w", err)
	}
	if err := installFile(aside, prev); err != nil {
		// target already holds the new binary; put the old one back and stop
		installFile(aside, cfg.Target)
		return fmt.Errorf("keep previous binary: %w", err)
	}
	if cfg.Mode == ModeUpdate {
		os.Remove(cfg.New)
	}
	herr := restartAndWait(ctx, cfg, ops, want)
	if herr == nil {
		return nil
	}
	// failed: restore what ran before (now in .prev) and keep the failed binary as .prev
	failed := cfg.Target + ".failed"
	copyFile(cfg.Target, failed)
	defer os.Remove(failed)
	if err := installFile(prev, cfg.Target); err != nil {
		return fmt.Errorf("%v; restoring the previous binary failed too: %w", herr, err)
	}
	installFile(failed, prev)
	res.RolledBack = true
	if err := restartAndWait(ctx, cfg, ops, res.FromVersion); err != nil {
		return fmt.Errorf("%v; restored %s but it is not healthy either: %w", herr, res.FromVersion, err)
	}
	return fmt.Errorf("new binary %s did not become healthy within %s (%v); restored %s", want, cfg.Timeout, herr, res.FromVersion)
}

func restartAndWait(ctx context.Context, cfg Config, ops Ops, want string) error {
	if err := ops.Restart(ctx); err != nil {
		return fmt.Errorf("restart: %w", err)
	}
	deadline := ops.Now().Add(cfg.Timeout)
	for {
		err := ops.Healthy(ctx, want)
		if err == nil {
			return nil
		}
		if !ops.Now().Before(deadline) || ctx.Err() != nil {
			return err
		}
		ops.Sleep(cfg.Poll)
	}
}

// copyFile copies src to dst atomically (temp file + rename), mode 0755.
func copyFile(src, dst string) error { return installFile(src, dst) }

func installFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// WriteResult writes r as JSON (atomically).
func WriteResult(path string, r api.UpdateResult) error {
	b, _ := json.MarshalIndent(r, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadResult reads a result file.
func ReadResult(path string) (*api.UpdateResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r api.UpdateResult
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ParseVersion extracts the version from `atoll-server version` output.
func ParseVersion(out string) string {
	f := strings.Fields(out)
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}
