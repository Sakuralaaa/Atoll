package server

// Self-update of atoll-server from GitHub releases:
//
//  1. download atoll-server-linux-<arch> and SHA256SUMS of the release, verify the checksum,
//     check that the new binary runs and reports the expected version
//  2. take a control backup ("pre-update"); refresh the agent binaries this server hosts
//  3. start a detached helper with systemd-run (`atoll-server control-helper`, a copy of the
//     running binary) so the restart survives this process exiting. The helper keeps the old
//     binary as atoll-server.prev, installs the new one, restarts the unit and waits up to 60s
//     for /api/v1/auth/status (and the new version on /healthz); otherwise it restores .prev and
//     restarts again (internal/server/selfupdate). Its result lands in
//     <data>/control-update/result.json and is shown by GET /control.
//
// The systemd unit keeps / read-only for atoll-server itself (ProtectSystem=strict); only the
// helper, which runs as its own transient unit, writes next to the binary.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/selfupdate"
)

const maxBinaryBytes = 300 << 20

var releaseTagRe = regexp.MustCompile(`^v[0-9A-Za-z.+-]{1,40}$`)

// pendingStale: a helper that has not reported after this long is considered lost.
var pendingStale = 15 * time.Minute

type pendingUpdate struct {
	Mode      string    `json:"mode"`
	Version   string    `json:"version,omitempty"`
	From      string    `json:"from"`
	StartedAt time.Time `json:"started_at"`
}

func (s *Server) updateDir() string { return filepath.Join(s.cfg.DataDir, "control-update") }

// binaryPath is the installed atoll-server binary (symlinks resolved).
func (s *Server) binaryPath() (string, error) {
	if s.cfg.BinaryPath != "" {
		return s.cfg.BinaryPath, nil
	}
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// selfUpdateSupported: running as a systemd service with systemd-run available (or a test hook).
func (s *Server) selfUpdateSupported() (bool, string) {
	if s.cfg.SpawnHelper != nil {
		return true, ""
	}
	if runtime.GOOS != "linux" {
		return false, "self-update needs Linux with systemd"
	}
	if os.Getenv("INVOCATION_ID") == "" {
		return false, "atoll-server is not running as a systemd service; update it by re-running install-server.sh --version TAG"
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return false, "systemd-run not found; update by re-running install-server.sh --version TAG"
	}
	return true, ""
}

func (s *Server) healthBase() string {
	addr := s.cfg.ListenAddr
	if addr == "" {
		addr = ":7300"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:7300"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func (c *controlState) setPhase(mode, version, phase string) {
	c.updMu.Lock()
	c.updMode, c.updVersion, c.updPhase = mode, version, phase
	c.updMu.Unlock()
}

func (c *controlState) phase() (string, string, string) {
	c.updMu.Lock()
	defer c.updMu.Unlock()
	return c.updMode, c.updVersion, c.updPhase
}

// fillUpdateStatus adds self-update state to GET /control.
func (s *Server) fillUpdateStatus(st *api.ControlStatus) {
	st.SelfUpdate, _ = s.selfUpdateSupported()
	res, _ := selfupdate.ReadResult(filepath.Join(s.updateDir(), "result.json"))
	st.LastUpdate = res
	p := s.readPending()
	reported := p != nil && res != nil && !res.StartedAt.Before(p.StartedAt.Add(-time.Minute))
	if _, _, ph := s.ctl.phase(); ph == "restarting" && reported {
		s.ctl.setPhase("", "", "") // the helper finished without restarting us (it failed early)
	}
	if _, v, ph := s.ctl.phase(); ph != "" {
		st.UpdateInProgress, st.UpdatePhase, st.UpdateTarget = true, ph, v
	} else if p != nil && !reported {
		if time.Since(p.StartedAt) < pendingStale {
			st.UpdateInProgress, st.UpdatePhase, st.UpdateTarget = true, "restarting", p.Version
		} else {
			st.LastUpdate = &api.UpdateResult{Mode: p.Mode, FromVersion: p.From, ToVersion: p.Version, Error: "the update helper did not report back; check journalctl -u 'atoll-selfupdate-*'", StartedAt: p.StartedAt}
		}
	}
	if exe, err := s.binaryPath(); err == nil {
		st.RollbackVersion = s.prevVersion(selfupdate.PrevPath(exe))
	}
}

// prevVersion is the version of the .prev binary, cached by modification time.
func (s *Server) prevVersion(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return ""
	}
	c := s.ctl
	c.updMu.Lock()
	if c.prevMod.Equal(fi.ModTime()) && c.prevPath == p {
		v := c.prevVer
		c.updMu.Unlock()
		return v
	}
	c.updMu.Unlock()
	v := binaryVersion(context.Background(), p)
	c.updMu.Lock()
	c.prevMod, c.prevPath, c.prevVer = fi.ModTime(), p, v
	c.updMu.Unlock()
	return v
}

func (s *Server) readPending() *pendingUpdate {
	b, err := os.ReadFile(filepath.Join(s.updateDir(), "pending.json"))
	if err != nil {
		return nil
	}
	var p pendingUpdate
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}

func binaryVersion(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return ""
	}
	return selfupdate.ParseVersion(string(out))
}

// ---------- handlers ----------

func (s *Server) handleControlUpdate(w http.ResponseWriter, r *http.Request) error {
	var req api.ControlUpdateRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if ok, why := s.selfUpdateSupported(); !ok {
		return errf(409, "self_update_unsupported", "%s", why)
	}
	req.Version = strings.TrimSpace(req.Version)
	if req.Version != "" && !releaseTagRe.MatchString(req.Version) {
		return badRequest("version must be a release tag like v0.2.0")
	}
	if err := s.checkReauth(w, r, req.ReauthFields, "update"); err != nil {
		return err
	}
	ctx := r.Context()
	actor := principalFrom(ctx).actor()
	if _, _, ph := s.ctl.phase(); ph != "" {
		return errf(409, "update_in_progress", "an update is already in progress (%s)", ph)
	}
	rel, err := s.fetchRelease(ctx, req.Version)
	if err != nil {
		return err
	}
	if rel.TagName == Version {
		return errf(409, "already_current", "atoll-server %s is already running", Version)
	}
	s.ctl.setPhase("update", rel.TagName, "downloading")
	s.st.Audit(ctx, actor, "control.update", rel.TagName, map[string]string{"from": Version})
	s.goBG(func(bg context.Context) {
		if err := s.performUpdate(bg, rel, actor); err != nil {
			s.log.Error("self-update failed", "version", rel.TagName, "err", err)
			selfupdate.WriteResult(filepath.Join(s.updateDir(), "result.json"), api.UpdateResult{Mode: "update", FromVersion: Version, ToVersion: rel.TagName, Error: err.Error(), StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()})
			s.st.Audit(bg, actor, "control.update_failed", rel.TagName, map[string]string{"error": err.Error()})
			s.ctl.setPhase("", "", "")
		}
	})
	writeJSON(w, 202, api.ControlUpdateResponse{Mode: "update", Version: rel.TagName, Phase: "downloading", Message: "follow progress in GET /api/v1/control"})
	return nil
}

func (s *Server) handleControlRollback(w http.ResponseWriter, r *http.Request) error {
	var req api.ReauthFields
	if r.ContentLength != 0 {
		if err := decode(r, &req); err != nil && r.ContentLength > 0 {
			return err
		}
	}
	if ok, why := s.selfUpdateSupported(); !ok {
		return errf(409, "self_update_unsupported", "%s", why)
	}
	exe, err := s.binaryPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(selfupdate.PrevPath(exe)); err != nil {
		return errf(409, "no_previous_binary", "there is no previous binary (%s) to roll back to", selfupdate.PrevPath(exe))
	}
	if err := s.checkReauth(w, r, req, "rollback"); err != nil {
		return err
	}
	if _, _, ph := s.ctl.phase(); ph != "" {
		return errf(409, "update_in_progress", "an update is already in progress (%s)", ph)
	}
	ctx := r.Context()
	prevVer := binaryVersion(ctx, selfupdate.PrevPath(exe))
	s.st.Audit(ctx, principalFrom(ctx).actor(), "control.rollback", prevVer, map[string]string{"from": Version})
	s.ctl.setPhase("rollback", prevVer, "restarting")
	if err := s.spawnHelper(ctx, selfupdate.ModeRollback, exe, "", prevVer); err != nil {
		s.ctl.setPhase("", "", "")
		return err
	}
	writeJSON(w, 202, api.ControlUpdateResponse{Mode: "rollback", Version: prevVer, Phase: "restarting", Message: "atoll-server restarts in a few seconds"})
	return nil
}

// performUpdate downloads + verifies the release and hands over to the helper.
func (s *Server) performUpdate(ctx context.Context, rel *ghRelease, actor string) error {
	dir := s.updateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	assetName := "atoll-server-linux-" + runtime.GOARCH
	sums, err := s.releaseChecksums(ctx, rel)
	if err != nil {
		return err
	}
	asset := findAsset(rel, assetName)
	if asset == nil {
		return fmt.Errorf("release %s has no %s asset", rel.TagName, assetName)
	}
	want := sums[assetName]
	if want == "" {
		return fmt.Errorf("SHA256SUMS of %s has no entry for %s", rel.TagName, assetName)
	}
	newPath := filepath.Join(dir, "atoll-server.new")
	if err := downloadVerified(ctx, asset.URL, newPath, want); err != nil {
		os.Remove(newPath)
		return err
	}
	s.ctl.setPhase("update", rel.TagName, "verifying")
	if err := os.Chmod(newPath, 0o755); err != nil {
		return err
	}
	if v := binaryVersion(ctx, newPath); v != rel.TagName {
		os.Remove(newPath)
		return fmt.Errorf("downloaded binary reports version %q, expected %s", v, rel.TagName)
	}
	s.ctl.setPhase("update", rel.TagName, "backing_up")
	if _, err := s.runControlBackup(ctx, "pre-update", actor); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("pre-update control backup failed (update not started): %w", err)
	}
	s.refreshAgentBinaries(ctx, rel, sums)
	exe, err := s.binaryPath()
	if err != nil {
		return err
	}
	s.ctl.setPhase("update", rel.TagName, "restarting")
	return s.spawnHelper(ctx, selfupdate.ModeUpdate, exe, newPath, rel.TagName)
}

func findAsset(rel *ghRelease, name string) *ghAsset {
	for i := range rel.Assets {
		if rel.Assets[i].Name == name {
			return &rel.Assets[i]
		}
	}
	return nil
}

// releaseChecksums downloads and parses the release's SHA256SUMS (sha256sum format).
func (s *Server) releaseChecksums(ctx context.Context, rel *ghRelease) (map[string]string, error) {
	a := findAsset(rel, "SHA256SUMS")
	if a == nil {
		return nil, fmt.Errorf("release %s has no SHA256SUMS; refusing to install an unverified binary", rel.TagName)
	}
	body, err := httpGet(ctx, a.URL, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("SHA256SUMS: %w", err)
	}
	return parseChecksums(string(body)), nil
}

func parseChecksums(text string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || len(f[0]) != 64 {
			continue
		}
		if _, err := hex.DecodeString(f[0]); err != nil {
			continue
		}
		out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	return out
}

func httpGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "atoll-server/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// errChecksum is returned when a download does not match SHA256SUMS.
var errChecksum = errors.New("checksum mismatch")

// downloadVerified downloads url to dst and checks its SHA-256 against want (hex).
func downloadVerified(ctx context.Context, url, dst, want string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "atoll-server/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s: HTTP %d", filepath.Base(dst), resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxBinaryBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if n > maxBinaryBytes {
		return fmt.Errorf("download: file too large")
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(want) {
		return fmt.Errorf("%w for %s: got %s, SHA256SUMS says %s", errChecksum, filepath.Base(url), got, want)
	}
	return nil
}

// refreshAgentBinaries replaces the atoll-agent binaries this server hosts for new nodes
// (<data>/downloads), verified against the same SHA256SUMS. Best effort.
func (s *Server) refreshAgentBinaries(ctx context.Context, rel *ghRelease, sums map[string]string) {
	dir := filepath.Join(s.cfg.DataDir, "downloads")
	if _, err := os.Stat(dir); err != nil {
		return
	}
	for _, arch := range []string{"amd64", "arm64"} {
		name := "atoll-agent-linux-" + arch
		a := findAsset(rel, name)
		if a == nil || sums[name] == "" {
			continue
		}
		tmp := filepath.Join(dir, "."+name+".new")
		if err := downloadVerified(ctx, a.URL, tmp, sums[name]); err != nil {
			s.log.Warn("agent binary refresh", "name", name, "err", err)
			os.Remove(tmp)
			continue
		}
		os.Chmod(tmp, 0o755)
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			os.Remove(tmp)
		}
	}
}

// spawnHelper starts the detached swap helper (systemd-run) and records the pending update.
func (s *Server) spawnHelper(ctx context.Context, mode, exe, newPath, version string) error {
	dir := s.updateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// run the helper from a copy of the running binary: the target is replaced under it
	helper := filepath.Join(dir, "atoll-server-helper")
	if err := copyExecutable(exe, helper); err != nil {
		return fmt.Errorf("prepare helper: %w", err)
	}
	p := pendingUpdate{Mode: mode, Version: version, From: Version, StartedAt: time.Now().UTC()}
	pb, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(dir, "pending.json"), pb, 0o600); err != nil {
		return err
	}
	args := []string{helper, "control-helper",
		"--mode", mode, "--target", exe, "--unit", s.unitName(),
		"--health", s.healthBase(), "--result", filepath.Join(dir, "result.json"), "--timeout", "60s"}
	if newPath != "" {
		args = append(args, "--new", newPath)
	}
	spawn := s.cfg.SpawnHelper
	if spawn == nil {
		spawn = systemdRunHelper
	}
	if err := spawn(ctx, args); err != nil {
		os.Remove(filepath.Join(dir, "pending.json"))
		return fmt.Errorf("start update helper: %w", err)
	}
	s.log.Info("self-update helper started", "mode", mode, "version", version)
	s.watchUpdateResult() // a helper that fails before restarting us reports while we run
	return nil
}

func systemdRunHelper(ctx context.Context, args []string) error {
	unit := "atoll-selfupdate-" + strconv.FormatInt(time.Now().Unix(), 10)
	full := append([]string{"--unit", unit, "--collect", "--quiet", "--on-active=2", "--timer-property=AccuracySec=100ms", "--"}, args...)
	out, err := exec.CommandContext(ctx, "systemd-run", full...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemd-run: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// The helper writes result.json after it has restarted the unit, i.e. after the new server
// has already started, so the result is looked for at start and then polled for a while.
var (
	updateResultPoll   = 3 * time.Second
	updateResultWindow = 10 * time.Minute
)

// watchUpdateResult consumes the helper's result now and, while an update is pending, keeps
// checking for it every updateResultPoll for up to updateResultWindow. One watcher at a time.
func (s *Server) watchUpdateResult() {
	if s.consumeUpdateResult() || s.readPending() == nil {
		return
	}
	if !s.ctl.resWatching.CompareAndSwap(false, true) {
		return
	}
	poll, window := updateResultPoll, updateResultWindow
	s.goBG(func(bg context.Context) {
		defer s.ctl.resWatching.Store(false)
		t := time.NewTicker(poll)
		defer t.Stop()
		deadline := time.NewTimer(window)
		defer deadline.Stop()
		for {
			select {
			case <-bg.Done():
				return
			case <-deadline.C:
				return
			case <-t.C:
				if s.consumeUpdateResult() || s.readPending() == nil {
					return
				}
			}
		}
	})
}

// consumeUpdateResult audits the helper's result once and removes pending.json. It reports
// whether the pending update is settled (its result was consumed now or earlier).
// Idempotent: result.audited records the result already logged, so a crash between the audit
// and the removal of pending.json, or two callers racing, never log it twice.
func (s *Server) consumeUpdateResult() bool {
	s.ctl.resMu.Lock()
	defer s.ctl.resMu.Unlock()
	p := s.readPending()
	if p == nil {
		return false
	}
	dir := s.updateDir()
	res, _ := selfupdate.ReadResult(filepath.Join(dir, "result.json"))
	if res == nil || res.FinishedAt.IsZero() || res.StartedAt.Before(p.StartedAt.Add(-time.Minute)) {
		return false
	}
	key := res.StartedAt.UTC().Format(time.RFC3339Nano) + " " + res.FinishedAt.UTC().Format(time.RFC3339Nano)
	marker := filepath.Join(dir, "result.audited")
	if b, err := os.ReadFile(marker); err != nil || strings.TrimSpace(string(b)) != key {
		s.st.Audit(s.bg, "system", "control.update_result", res.ToVersion, res)
		if err := os.WriteFile(marker, []byte(key+"\n"), 0o600); err != nil {
			s.log.Warn("record consumed update result", "err", err)
		}
	}
	os.Remove(filepath.Join(dir, "pending.json"))
	return true
}

// ---------- the helper process (atoll-server control-helper) ----------

// HelperOptions are the control-helper flags.
type HelperOptions struct {
	Mode, Target, New, Unit, Health, Result string
	Timeout                                 time.Duration
}

// RunHelper runs the swap with the real side effects: systemctl restart, HTTP health checks
// against the local listener, `<binary> version`.
func RunHelper(ctx context.Context, o HelperOptions) api.UpdateResult {
	hc := &http.Client{Timeout: 3 * time.Second}
	get := func(ctx context.Context, path string) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(o.Health, "/")+path, nil)
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
		}
		return b, nil
	}
	ops := selfupdate.Ops{
		Restart: func(ctx context.Context) error {
			out, err := exec.CommandContext(ctx, "systemctl", "restart", o.Unit).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		},
		Healthy: func(ctx context.Context, want string) error {
			if _, err := get(ctx, "/api/v1/auth/status"); err != nil {
				return err
			}
			if want == "" {
				return nil
			}
			b, err := get(ctx, "/healthz")
			if err != nil {
				return err
			}
			var h struct {
				Version string `json:"version"`
			}
			json.Unmarshal(b, &h)
			if h.Version != want {
				return fmt.Errorf("running version %q, want %s", h.Version, want)
			}
			return nil
		},
		Version: func(ctx context.Context, bin string) (string, error) {
			v := binaryVersion(ctx, bin)
			if v == "" {
				return "", fmt.Errorf("%s version: no output", bin)
			}
			return v, nil
		},
	}
	return selfupdate.Run(ctx, selfupdate.Config{Mode: o.Mode, Target: o.Target, New: o.New, Timeout: o.Timeout, Result: o.Result}, ops)
}
