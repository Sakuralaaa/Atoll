package agent

// Self-upgrade from the panel (MsgUpgrade).
//
// The running agent downloads the hosted binary from the control URL it is connected to,
// checks its SHA-256 and the version it reports, and hands the swap to a detached helper
// (`atoll-agent upgrade-helper`, a copy of the running binary started with systemd-run so it
// outlives the agent's restart). The helper reuses the control server's selfupdate logic:
// install the new binary, keep the old one as atoll-agent.prev, restart the unit, and wait
// for the new agent to reconnect (it writes a marker on every successful connection). When
// it does not reconnect in time the previous binary goes back. Containers keep running
// throughout: they are dockerd's children, and the spec hash does not change.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/selfupdate"
)

// Upgrader holds the self-upgrade side effects (real ones by default, fakes in tests).
type Upgrader struct {
	Dir  string // <data>/agent-upgrade
	Unit string // systemd unit, default atoll-agent
	// Exe returns the path of the running binary (default os.Executable, symlinks resolved).
	Exe func() (string, error)
	// Spawn starts the detached helper (default systemd-run).
	Spawn func(ctx context.Context, args []string) error
	// Version runs `<bin> version` (default exec).
	Version func(ctx context.Context, bin string) (string, error)
	// Supported reports why self-upgrade cannot work here (default: requires systemd).
	Supported func() error
	HTTP      *http.Client

	mu      sync.Mutex
	started time.Time // last Start that got past the checks (one at a time, 5 min window)
}

// NewUpgrader returns an Upgrader with the real side effects.
func NewUpgrader(p Paths) *Upgrader {
	return &Upgrader{Dir: filepath.Join(p.Root, "agent-upgrade"), Unit: "atoll-agent"}
}

func (u *Upgrader) markerPath() string { return filepath.Join(u.Dir, "connected.json") }
func (u *Upgrader) resultPath() string { return filepath.Join(u.Dir, "result.json") }
func (u *Upgrader) newPath() string    { return filepath.Join(u.Dir, "atoll-agent.new") }

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Start validates p, downloads and checks the new binary from base (the control URL in use)
// and starts the helper. The swap happens after this returns, so the reply reaches the server.
func (u *Upgrader) Start(ctx context.Context, base, token string, p api.UpgradePayload) (api.UpgradeStarted, error) {
	res := api.UpgradeStarted{From: Version, To: p.Version}
	want := "/downloads/atoll-agent-linux-" + runtime.GOARCH
	if p.Path != want {
		return res, fmt.Errorf("this agent runs on %s and needs %s, not %q", runtime.GOARCH, want, p.Path)
	}
	p.SHA256 = strings.ToLower(strings.TrimSpace(p.SHA256))
	if !sha256Re.MatchString(p.SHA256) {
		return res, errors.New("upgrade needs the SHA-256 of the binary")
	}
	if p.Version == "" {
		return res, errors.New("upgrade needs the target version")
	}
	if p.Version == Version {
		return res, fmt.Errorf("already running %s", Version)
	}
	supported := u.Supported
	if supported == nil {
		supported = systemdAvailable
	}
	if err := supported(); err != nil {
		return res, err
	}
	u.mu.Lock()
	if !u.started.IsZero() && time.Since(u.started) < 5*time.Minute {
		u.mu.Unlock()
		return res, errors.New("an upgrade is already in progress")
	}
	u.started = time.Now()
	u.mu.Unlock()
	ok := false
	defer func() {
		if !ok {
			u.mu.Lock()
			u.started = time.Time{}
			u.mu.Unlock()
		}
	}()

	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return res, err
	}
	if err := u.download(ctx, strings.TrimRight(base, "/")+p.Path, token, p.SHA256); err != nil {
		return res, err
	}
	version := u.Version
	if version == nil {
		version = binaryVersion
	}
	got, err := version(ctx, u.newPath())
	if err != nil {
		os.Remove(u.newPath())
		return res, fmt.Errorf("the downloaded binary does not run: %w", err)
	}
	if got != p.Version {
		os.Remove(u.newPath())
		return res, fmt.Errorf("the downloaded binary is %s, expected %s (the control server's hosted agent is out of date)", got, p.Version)
	}
	exeFn := u.Exe
	if exeFn == nil {
		exeFn = executable
	}
	exe, err := exeFn()
	if err != nil {
		return res, fmt.Errorf("locate the running binary: %w", err)
	}
	// the helper runs from a copy: the target is replaced under it
	helper := filepath.Join(u.Dir, "atoll-agent-helper")
	if err := copyExe(exe, helper); err != nil {
		return res, fmt.Errorf("prepare helper: %w", err)
	}
	os.Remove(u.resultPath()) // the next result is this upgrade's
	unit := u.Unit
	if unit == "" {
		unit = "atoll-agent"
	}
	args := []string{helper, "upgrade-helper", "--target", exe, "--new", u.newPath(), "--unit", unit,
		"--marker", u.markerPath(), "--result", u.resultPath(), "--timeout", "120s"}
	spawn := u.Spawn
	if spawn == nil {
		spawn = systemdRun
	}
	if err := spawn(ctx, args); err != nil {
		return res, fmt.Errorf("start upgrade helper: %w", err)
	}
	ok = true
	return res, nil
}

func (u *Upgrader) download(ctx context.Context, url, token, want string) error {
	hc := u.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	tmp, err := os.CreateTemp(u.Dir, ".download*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, 512<<20)); err != nil {
		tmp.Close()
		return fmt.Errorf("download %s: %w", url, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", url, got, want)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), u.newPath())
}

// connectedMarker is written after every successful hello; the helper waits for one with the
// new version that is newer than its restart.
type connectedMarker struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
}

// MarkConnected records that this binary reached the control server.
func (u *Upgrader) MarkConnected() {
	if os.MkdirAll(u.Dir, 0o700) != nil {
		return
	}
	b, _ := json.Marshal(connectedMarker{Version: Version, At: time.Now().UTC()})
	tmp := u.markerPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		os.Rename(tmp, u.markerPath())
	}
}

// PendingResult returns the helper's result once it is written and not reported yet.
func (u *Upgrader) PendingResult() *api.UpdateResult {
	r, err := selfupdate.ReadResult(u.resultPath())
	if err != nil {
		return nil
	}
	return r
}

// Reported marks the pending result as delivered.
func (u *Upgrader) Reported() {
	os.Rename(u.resultPath(), filepath.Join(u.Dir, "result.reported.json"))
}

// reportUpgrade sends the helper's result once it appears (the helper writes it only after
// this process has connected, so it polls for a while after each connection).
func (a *Agent) reportUpgrade(ctx context.Context) {
	u := a.Upgrades
	if u == nil {
		return
	}
	deadline := time.Now().Add(4 * time.Minute)
	for {
		if r := u.PendingResult(); r != nil {
			a.Emit(api.MsgUpgradeResult, r)
			u.Reported()
			return
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (a *Agent) handleUpgrade(ctx context.Context, p api.UpgradePayload) (string, error) {
	if a.Upgrades == nil {
		return "", errors.New("self-upgrade is not available in this agent")
	}
	a.connMu.Lock()
	base := a.connURL
	a.connMu.Unlock()
	if base == "" {
		base = a.currentURL()
	}
	res, err := a.Upgrades.Start(ctx, base, a.Cfg.NodeToken, p)
	if err != nil {
		return "", err
	}
	a.Log.Info("agent upgrade started", "from", res.From, "to", res.To)
	b, _ := json.Marshal(res)
	return string(b), nil
}

// HelperOptions configure `atoll-agent upgrade-helper`.
type HelperOptions struct {
	Target, New, Unit, Marker, Result string
	Timeout                           time.Duration
}

// RunUpgradeHelper swaps the binary and restarts the agent, rolling back when the new agent
// does not reconnect within the timeout.
func RunUpgradeHelper(ctx context.Context, o HelperOptions) api.UpdateResult {
	var since time.Time
	ops := selfupdate.Ops{
		Restart: func(ctx context.Context) error {
			since = time.Now().UTC()
			out, err := exec.CommandContext(ctx, "systemctl", "restart", o.Unit).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		Healthy: func(_ context.Context, want string) error {
			return markerHealthy(o.Marker, want, since)
		},
		Version: binaryVersion,
	}
	return selfupdate.Run(ctx, selfupdate.Config{Mode: selfupdate.ModeUpdate, Target: o.Target, New: o.New,
		Timeout: o.Timeout, Poll: 2 * time.Second, Result: o.Result}, ops)
}

func markerHealthy(path, want string, since time.Time) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return errors.New("agent has not reconnected yet")
	}
	var m connectedMarker
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	if m.At.Before(since) {
		return errors.New("agent has not reconnected yet")
	}
	if want != "" && m.Version != want {
		return fmt.Errorf("connected agent runs %s, want %s", m.Version, want)
	}
	return nil
}

func binaryVersion(ctx context.Context, bin string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, "version").Output()
	if err != nil {
		return "", err
	}
	v := selfupdate.ParseVersion(string(out))
	if v == "" {
		return "", errors.New("no version output")
	}
	return v, nil
}

func executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func systemdAvailable() error {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return errors.New("self-upgrade needs systemd (re-run install-agent.sh to upgrade this agent)")
	}
	if os.Getenv("INVOCATION_ID") == "" {
		return errors.New("this agent is not running as a systemd service (re-run install-agent.sh to upgrade it)")
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return errors.New("systemd-run not found")
	}
	return nil
}

func systemdRun(ctx context.Context, args []string) error {
	unit := "atoll-agent-upgrade-" + strconv.FormatInt(time.Now().Unix(), 10)
	full := append([]string{"--unit", unit, "--collect", "--quiet", "--on-active=2", "--timer-property=AccuracySec=100ms", "--"}, args...)
	out, err := exec.CommandContext(ctx, "systemd-run", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemd-run: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyExe(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
