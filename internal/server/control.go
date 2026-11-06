package server

// 中控 / Control: managing atoll-server itself (docs/control.md). The control server is not a
// worker and never runs workloads; everything here is about this process and its host:
// status + metrics, journald logs, the audit log, self-update (control_update.go) and
// encrypted control backups / export / restore (control_backup.go).
//
// Access: every route is panel-session only (API tokens get 403) except the status and
// metrics reads, which need the read permission. Export, backup download, passphrase,
// update and rollback additionally need a recent re-authentication (current password, plus
// the TOTP code when 2FA is on) that reuses the change-password check and login lockout.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent/tailnet"
	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/dns"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"golang.org/x/crypto/bcrypt"
)

// controlMetricsID is the node_metrics key the control host's own samples are stored under.
const controlMetricsID = "_control"

// Tunables (tests shorten them).
var (
	controlReauthTTL     = 5 * time.Minute
	controlSampleEvery   = time.Minute
	controlReleaseTTL    = 6 * time.Hour
	controlReleaseErrTTL = 30 * time.Minute
	controlDirSizeTTL    = 5 * time.Minute
)

type controlState struct {
	started time.Time

	mu     sync.Mutex
	reauth map[string]time.Time // session hash -> re-auth valid until

	sampleMu sync.Mutex
	sample   controlSample

	relMu      sync.Mutex
	release    *api.ReleaseInfo
	relAssets  []ghAsset
	relChecked time.Time
	relErr     string

	sizeMu   sync.Mutex
	dirSize  int64
	sizeTime time.Time

	updMu      sync.Mutex
	updPhase   string // "" when idle
	updMode    string
	updVersion string
	prevPath   string // cached version of atoll-server.prev
	prevMod    time.Time
	prevVer    string

	resMu       sync.Mutex  // consuming the update helper's result
	resWatching atomic.Bool // a watchUpdateResult poller is running

	backupMu sync.Mutex // one control backup at a time
}

type controlSample struct {
	at                   time.Time
	cpu                  float64
	load1, load5, load15 float64
	memUsed, memTotal    int
	diskUsed, diskTotal  float64
}

func newControlState() *controlState {
	return &controlState{started: time.Now().UTC(), reauth: map[string]time.Time{}}
}

func (s *Server) registerControlRoutes(a func(string, access, handlerFunc)) {
	const v = "/api/v1"
	a("GET "+v+"/control", needReadGlobal, s.handleControlStatus)
	a("PATCH "+v+"/control", lvlSession, s.handlePatchControl)
	a("GET "+v+"/control/metrics", needReadGlobal, s.handleControlMetrics)
	a("GET "+v+"/control/logs", lvlSession, s.handleControlLogs)
	a("POST "+v+"/control/reauth", lvlSession, s.handleControlReauth)
	a("POST "+v+"/control/update/check", lvlSession, s.handleControlUpdateCheck)
	a("POST "+v+"/control/update", lvlSession, s.handleControlUpdate)
	a("POST "+v+"/control/rollback", lvlSession, s.handleControlRollback)
	a("GET "+v+"/control/backups", lvlSession, s.handleListControlBackups)
	a("POST "+v+"/control/backups", lvlSession, s.handleRunControlBackup)
	a("GET "+v+"/control/backups/{id}/download", lvlSession, s.handleDownloadControlBackup)
	a("POST "+v+"/control/backups/passphrase", lvlSession, s.handleControlPassphrase)
	a("POST "+v+"/control/export", lvlSession, s.handleControlExport)
	a("GET "+v+"/audit", lvlSession, s.handleListAudit)
}

// handlePatchControl saves the control server's display name.
func (s *Server) handlePatchControl(w http.ResponseWriter, r *http.Request) error {
	var req api.ControlPatch
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if len([]rune(name)) > 100 {
			return badRequest("name must be at most 100 characters")
		}
		if err := s.st.SetSetting(r.Context(), "control_name", name); err != nil {
			return err
		}
		s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "control.rename", "control", map[string]string{"name": name})
	}
	return s.handleControlStatus(w, r)
}

// ---------- re-authentication ----------

func (c *controlState) markReauth(sessionHash string) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, t := range c.reauth {
		if now.After(t) {
			delete(c.reauth, k)
		}
	}
	exp := now.Add(controlReauthTTL)
	c.reauth[sessionHash] = exp
	return exp
}

func (c *controlState) reauthValid(sessionHash string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.reauth[sessionHash]
	return ok && time.Now().Before(t)
}

// checkReauth verifies the current password (+ TOTP code when 2FA is on) given in f, exactly
// like changing the password does (failures count toward the login lockout). With neither
// given, a successful re-auth of this session in the last few minutes is accepted.
func (s *Server) checkReauth(w http.ResponseWriter, r *http.Request, f api.ReauthFields, action string) error {
	p := principalFrom(r.Context())
	if !p.Session {
		return errf(403, "session_required", "this action is only allowed from the panel")
	}
	if f.Password == "" && f.Code == "" {
		if s.ctl.reauthValid(p.SessionHash) {
			return nil
		}
		return errf(401, "reauth_required", "confirm with your current password (and 2FA code) first")
	}
	tf, err := s.st.TOTPState(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	ip := s.clientIP(r)
	if tf.Enabled && strings.TrimSpace(f.Code) == "" {
		// check the password first so a guess is still counted
		if wait := s.lock.wait(ipKey(ip), accountKey(p.User.Username)); wait > 0 {
			return tooMany(w, wait)
		}
		if bcrypt.CompareHashAndPassword([]byte(p.User.PasswordHash), []byte(f.Password)) != nil {
			s.loginFailed(ip, p.User.Username, "control re-auth password")
			s.st.Audit(r.Context(), p.actor(), "control.reauth_failed", action, map[string]string{"reason": "password"})
			return errf(401, "invalid_credentials", "current password is wrong")
		}
		return errf(401, "code_required", "two-factor authentication is on; enter the current code from your authenticator")
	}
	if err := s.reauth(w, r, api.TwoFactorRequest(f), true, tf.Enabled, false); err != nil {
		var reason string
		if ae, ok := err.(*apiError); ok {
			reason = ae.code
		}
		s.st.Audit(r.Context(), p.actor(), "control.reauth_failed", action, map[string]string{"reason": reason})
		return err
	}
	s.ctl.markReauth(p.SessionHash)
	return nil
}

func (s *Server) handleControlReauth(w http.ResponseWriter, r *http.Request) error {
	var f api.ReauthFields
	if err := decode(r, &f); err != nil {
		return err
	}
	if f.Password == "" {
		return badRequest("password required")
	}
	if err := s.checkReauth(w, r, f, "reauth"); err != nil {
		return err
	}
	p := principalFrom(r.Context())
	s.st.Audit(r.Context(), p.actor(), "control.reauth", "", nil)
	s.ctl.mu.Lock()
	exp := s.ctl.reauth[p.SessionHash]
	s.ctl.mu.Unlock()
	writeJSON(w, 200, api.ControlReauthResponse{ExpiresAt: exp.UTC()})
	return nil
}

// ---------- status ----------

func buildCommit() string {
	if Commit != "" {
		return Commit
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	rev, dirty := "", false
	for _, st := range bi.Settings {
		switch st.Key {
		case "vcs.revision":
			rev = st.Value
		case "vcs.modified":
			dirty = st.Value == "true"
		}
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if rev != "" && dirty {
		rev += "-dirty"
	}
	return rev
}

func (s *Server) controlSampleLoop() {
	s.goBG(func(bg context.Context) {
		cpu.PercentWithContext(bg, 0, false) // prime: the next call measures since now
		t := time.NewTimer(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-bg.Done():
				return
			case <-t.C:
			}
			sm := s.takeControlSample(bg)
			s.st.AddNodeMetrics(bg, controlMetricsID, api.NodeMetrics{At: sm.at, CPUPct: sm.cpu, MemUsedMB: sm.memUsed, MemTotalMB: sm.memTotal,
				DiskUsedGB: sm.diskUsed, DiskTotalGB: sm.diskTotal, Load1: sm.load1})
			t.Reset(controlSampleEvery)
		}
	})
}

func (s *Server) takeControlSample(ctx context.Context) controlSample {
	sm := controlSample{at: time.Now().UTC()}
	if p, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(p) > 0 {
		sm.cpu = round1(p[0])
	}
	if l, err := load.AvgWithContext(ctx); err == nil {
		sm.load1, sm.load5, sm.load15 = l.Load1, l.Load5, l.Load15
	}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		sm.memUsed, sm.memTotal = int(m.Used>>20), int(m.Total>>20)
	}
	if d, err := disk.UsageWithContext(ctx, s.cfg.DataDir); err == nil {
		sm.diskUsed, sm.diskTotal = round1(float64(d.Used)/(1<<30)), round1(float64(d.Total)/(1<<30))
	}
	s.ctl.sampleMu.Lock()
	s.ctl.sample = sm
	s.ctl.sampleMu.Unlock()
	return sm
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

func fileSize(p string) int64 {
	if st, err := os.Stat(p); err == nil {
		return st.Size()
	}
	return 0
}

func (s *Server) dataDirSize() int64 {
	c := s.ctl
	c.sizeMu.Lock()
	defer c.sizeMu.Unlock()
	if time.Since(c.sizeTime) < controlDirSizeTTL && !c.sizeTime.IsZero() {
		return c.dirSize
	}
	var n int64
	filepath.WalkDir(s.cfg.DataDir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	c.dirSize, c.sizeTime = n, time.Now()
	return n
}

func (h *Hub) connectedCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.conns)
}

func (s *Server) handleControlStatus(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c := s.ctl
	st := api.ControlStatus{
		Version: Version, Commit: buildCommit(), GoVersion: runtime.Version(),
		StartedAt: c.started, UptimeSeconds: int64(time.Since(c.started).Seconds()),
		DataDir: s.cfg.DataDir, PublicURL: s.publicURL(ctx, r),
	}
	st.Name, _ = s.st.GetSetting(ctx, "control_name")
	st.Host.Arch = runtime.GOARCH
	if hi, err := host.InfoWithContext(ctx); err == nil {
		st.Host.Hostname = hi.Hostname
		st.Host.OS = strings.TrimSpace(hi.Platform + " " + hi.PlatformVersion)
		if st.Host.OS == "" {
			st.Host.OS = hi.OS
		}
		st.Host.Kernel = hi.KernelVersion
		st.Host.UptimeSeconds = int64(hi.Uptime)
	} else {
		st.Host.Hostname, _ = os.Hostname()
	}
	st.Host.CPUCores = runtime.NumCPU()

	c.sampleMu.Lock()
	sm := c.sample
	c.sampleMu.Unlock()
	if time.Since(sm.at) > 2*controlSampleEvery {
		sm = s.takeControlSample(ctx)
	}
	st.CPUPct, st.Load1, st.Load5, st.Load15 = sm.cpu, sm.load1, sm.load5, sm.load15
	st.MemUsedMB, st.MemTotalMB, st.DiskUsedGB, st.DiskTotalGB = sm.memUsed, sm.memTotal, sm.diskUsed, sm.diskTotal

	db := filepath.Join(s.cfg.DataDir, "atoll.db")
	st.DatabaseBytes = fileSize(db) + fileSize(db+"-wal")
	st.DataDirBytes = s.dataDirSize()

	ip := tailnet.LocalIP()
	cu := s.tailnetControlURL(ctx)
	la, le := s.agentListenState()
	if ip != "" || cu != "" || la != "" || le != "" {
		st.Tailnet = &api.ControlTailnet{IP: ip, ControlURL: cu, Listen: la, ListenErr: le}
	}
	st.AgentsConnected = s.hub.connectedCount()
	if nodes, err := s.st.ListNodes(ctx); err == nil {
		st.AgentsTotal = len(nodes)
	}
	st.LastBackup = s.lastControlBackup()

	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second) // never hold the status page for long
	rel, relErr := s.latestRelease(rctx, false)
	rcancel()
	st.LatestRelease, st.ReleaseError = rel, relErr
	if rel != nil {
		st.UpdateAvailable = versionLess(Version, rel.Tag)
	}
	s.fillUpdateStatus(&st)
	writeJSON(w, 200, st)
	return nil
}

func (s *Server) handleControlMetrics(w http.ResponseWriter, r *http.Request) error {
	since, step, err := rangeStep(r.URL.Query().Get("range"), 7*24*time.Hour)
	if err != nil {
		return err
	}
	ms, err := s.st.NodeMetricsRange(r.Context(), controlMetricsID, since, step)
	if err != nil {
		return err
	}
	writeJSON(w, 200, ms)
	return nil
}

// ---------- releases (GitHub) ----------

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Assets      []ghAsset `json:"assets"`
}

func (s *Server) githubAPI() string {
	if s.cfg.GitHubAPI != "" {
		return strings.TrimRight(s.cfg.GitHubAPI, "/")
	}
	return "https://api.github.com"
}

func (s *Server) githubRepo() string {
	if s.cfg.GitHubRepo != "" {
		return s.cfg.GitHubRepo
	}
	return "Sakuralaaa/Atoll"
}

// fetchRelease reads one release ("" = latest) from the GitHub API.
func (s *Server) fetchRelease(ctx context.Context, tag string) (*ghRelease, error) {
	u := s.githubAPI() + "/repos/" + s.githubRepo() + "/releases/latest"
	if tag != "" {
		u = s.githubAPI() + "/repos/" + s.githubRepo() + "/releases/tags/" + url.PathEscape(tag)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "atoll-server/"+Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		if tag != "" {
			return nil, errf(404, "release_not_found", "release %s not found", tag)
		}
		return nil, fmt.Errorf("github: no release published yet")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github: HTTP %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	if rel.TagName == "" || rel.Draft {
		return nil, fmt.Errorf("github: release has no tag")
	}
	return &rel, nil
}

// latestRelease returns the cached latest release, checking GitHub at most every 6 hours
// (failures are retried after 30 minutes) unless force is set.
func (s *Server) latestRelease(ctx context.Context, force bool) (*api.ReleaseInfo, string) {
	c := s.ctl
	c.relMu.Lock()
	defer c.relMu.Unlock()
	ttl := controlReleaseTTL
	if c.relErr != "" {
		ttl = controlReleaseErrTTL
	}
	if !force && !c.relChecked.IsZero() && time.Since(c.relChecked) < ttl {
		return c.release, c.relErr
	}
	rel, err := s.fetchRelease(ctx, "")
	c.relChecked = time.Now().UTC()
	if err != nil {
		c.relErr = err.Error()
		s.log.Warn("release check failed", "err", err)
		return c.release, c.relErr
	}
	c.relErr = ""
	c.release = &api.ReleaseInfo{Tag: rel.TagName, Name: rel.Name, URL: rel.HTMLURL, PublishedAt: rel.PublishedAt, Prerelease: rel.Prerelease, CheckedAt: c.relChecked}
	c.relAssets = rel.Assets
	return c.release, ""
}

func (s *Server) handleControlUpdateCheck(w http.ResponseWriter, r *http.Request) error {
	rel, errMsg := s.latestRelease(r.Context(), true)
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "control.update_check", "", map[string]string{"latest": tagOf(rel), "error": errMsg})
	out := map[string]any{"latest_release": rel, "current": Version, "update_available": rel != nil && versionLess(Version, rel.Tag)}
	if errMsg != "" {
		out["release_error"] = errMsg
	}
	writeJSON(w, 200, out)
	return nil
}

func tagOf(r *api.ReleaseInfo) string {
	if r == nil {
		return ""
	}
	return r.Tag
}

// parseSemver parses vMAJOR.MINOR.PATCH[-pre]; ok=false for anything else (e.g. "dev").
func parseSemver(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nums, "", false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nums, "", false
		}
		nums[i] = n
	}
	return nums, pre, true
}

// versionLess reports a < b for release versions (false when either is not semver).
func versionLess(a, b string) bool {
	na, pa, ok1 := parseSemver(a)
	nb, pb, ok2 := parseSemver(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range 3 {
		if na[i] != nb[i] {
			return na[i] < nb[i]
		}
	}
	switch {
	case pa == pb:
		return false
	case pa == "":
		return false // release > prerelease
	case pb == "":
		return true
	}
	return pa < pb
}

// ---------- logs (journald) ----------

func defaultJournalctl(ctx context.Context, args ...string) ([]byte, error) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return nil, fmt.Errorf("journalctl not found on this host")
	}
	return exec.CommandContext(ctx, "journalctl", args...).Output()
}

func (s *Server) unitName() string {
	if s.cfg.UnitName != "" {
		return s.cfg.UnitName
	}
	return "atoll-server"
}

func (s *Server) handleControlLogs(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	tail := 200
	if v := q.Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return badRequest("tail must be a positive number")
		}
		tail = min(n, 5000)
	}
	args := []string{"-u", s.unitName(), "-o", "short-iso", "--no-pager", "-n", strconv.Itoa(tail)}
	if v := q.Get("since"); v != "" {
		var t time.Time
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			t = time.Now().Add(-d)
		} else if tt, err := time.Parse(time.RFC3339, v); err == nil {
			t = tt
		} else {
			return badRequest("since must be an RFC 3339 time or a duration like 15m")
		}
		args = append(args, "--since", "@"+strconv.FormatInt(t.Unix(), 10))
	}
	jc := s.cfg.Journalctl
	if jc == nil {
		jc = defaultJournalctl
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := jc(ctx, args...)
	res := api.ControlLogs{Source: "journald", Lines: []string{}}
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		res.Message = "journald logs are not available here (" + msg + "); atoll-server logs to stderr — run it under systemd (journalctl -u " + s.unitName() + ") or check your process supervisor"
		writeJSON(w, 200, res)
		return nil
	}
	res.Available = true
	for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if l != "" && !strings.HasPrefix(l, "-- No entries --") {
			res.Lines = append(res.Lines, l)
		}
	}
	writeJSON(w, 200, res)
	return nil
}

// ---------- panel DNS after a move (--adopt-public-ip) ----------

// adoptMarker is written by `atoll-server restore --adopt-public-ip`: the next start points the
// panel's DNS record at this host once, then removes it.
func (s *Server) adoptMarker() string { return filepath.Join(s.cfg.DataDir, "adopt-public-ip") }

// startAdoptPublicIP updates the panel's DNS record in the background when asked to.
func (s *Server) startAdoptPublicIP() {
	_, err := os.Stat(s.adoptMarker())
	marker := err == nil
	if !s.cfg.AdoptPublicIP && !marker {
		return
	}
	s.goBG(func(bg context.Context) {
		delay := 5 * time.Second
		for attempt := 1; attempt <= 6; attempt++ {
			changed, msg, err := s.adoptPublicIP(bg)
			if err == nil {
				s.log.Info("panel DNS", "result", msg, "changed", changed)
				if marker {
					os.Remove(s.adoptMarker())
				}
				return
			}
			s.log.Warn("panel DNS update failed", "attempt", attempt, "err", err)
			select {
			case <-bg.Done():
				return
			case <-time.After(delay):
			}
			delay *= 2
		}
	})
}

// adoptPublicIP points the A record of the public URL's host at this host's public IPv4 when
// that host is inside the Cloudflare base_domain zone. The record's proxied flag is kept.
func (s *Server) adoptPublicIP(ctx context.Context) (bool, string, error) {
	pub := s.publicURL(ctx, nil)
	u, err := url.Parse(pub)
	if err != nil || u.Hostname() == "" {
		return false, "no public URL configured", nil
	}
	h := strings.ToLower(u.Hostname())
	if net.ParseIP(h) != nil || h == "localhost" {
		return false, "public URL " + pub + " is not a domain; nothing to update", nil
	}
	base, _ := s.st.GetSetting(ctx, "base_domain")
	base = strings.ToLower(strings.Trim(base, "."))
	if base == "" || (h != base && !strings.HasSuffix(h, "."+base)) {
		return false, h + " is not under the Cloudflare zone " + base + "; update its DNS record by hand", nil
	}
	prov := s.dnsProvider(ctx)
	ip := s.cfg.PublicIP
	if ip == "" {
		lookup := s.cfg.LookupPublicIPv4
		if lookup == nil {
			lookup = lookupPublicIPv4
		}
		if ip, err = lookup(ctx); err != nil {
			return false, "", err
		}
	}
	proxied := true
	if f, ok := prov.(dns.Finder); ok {
		recs, err := f.FindA(ctx, h)
		if err != nil {
			return false, "", err
		}
		if len(recs) > 0 {
			if recs[0].Content == ip {
				return false, h + " already points at " + ip, nil
			}
			proxied = recs[0].Proxied
		}
	}
	if _, err := prov.UpsertA(ctx, h, ip, proxied); err != nil {
		return false, "", err
	}
	s.st.Audit(ctx, "system", "control.dns_adopt", h, map[string]any{"ip": ip, "proxied": proxied})
	return true, h + " now points at " + ip, nil
}

func lookupPublicIPv4(ctx context.Context) (string, error) {
	for _, u := range []string{"https://api.ipify.org", "https://ipv4.icanhazip.com"} {
		c, cancel := context.WithTimeout(ctx, 6*time.Second)
		req, _ := http.NewRequestWithContext(c, http.MethodGet, u, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
			resp.Body.Close()
			if ip := net.ParseIP(strings.TrimSpace(string(b))); ip != nil && ip.To4() != nil {
				cancel()
				return ip.String(), nil
			}
		}
		cancel()
	}
	return "", fmt.Errorf("could not detect this host's public IPv4 (set ATOLL_PUBLIC_IP)")
}
