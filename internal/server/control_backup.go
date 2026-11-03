package server

// Control backups: an encrypted bundle (internal/server/ctlbundle) of everything needed to bring
// the control server back on another machine — a consistent SQLite snapshot (VACUUM INTO),
// master.key, server.env and the Caddyfile when readable.
//
//   - scheduled daily, plus "run now", plus an automatic one before every self-update
//   - local copies in <data>/control-backups (keep 3), and — when the service backup target is
//     configured in Settings — uploaded to the same bucket under <prefix>/control/ (keep 14)
//   - scheduled/local bundles are encrypted with the control backup passphrase (its own setting,
//     else the backup repository password, else a generated one that the panel can reveal)
//   - export streams a fresh bundle encrypted with a passphrase the user types (never stored)
//
// Restore is offline: `atoll-server restore --bundle F --passphrase-file P` (RestoreControl).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/ctlbundle"
	"github.com/Sakuralaaa/Atoll/internal/server/s3mini"
	"github.com/Sakuralaaa/Atoll/internal/server/secrets"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

const (
	controlBundleExt      = ".atollbak"
	controlKeepLocal      = 3
	defaultControlKeepRem = 14
)

// Tunables (tests shorten them).
var (
	controlBackupEvery = 24 * time.Hour
	controlBackupTick  = time.Hour
	controlBackupFirst = 10 * time.Minute
)

// controlIDTime is the timestamp in a control backup id (ids sort by creation time).
const controlIDTime = "20060102T150405.000Z"

var controlBackupIDRe = regexp.MustCompile(`^cb_[0-9]{8}T[0-9]{6}\.[0-9]{3}Z_[a-z0-9]{4}$`)

// DefaultServerEnvPath / DefaultCaddyfilePath are what install-server.sh writes.
const (
	DefaultServerEnvPath = "/etc/atoll/server.env"
	DefaultCaddyfilePath = "/etc/caddy/Caddyfile"
)

func (s *Server) controlBackupDir() string { return filepath.Join(s.cfg.DataDir, "control-backups") }

func (s *Server) serverEnvPath() string {
	if s.cfg.ServerEnvPath != "" {
		return s.cfg.ServerEnvPath
	}
	return DefaultServerEnvPath
}

func (s *Server) caddyfilePath() string {
	if s.cfg.CaddyfilePath != "" {
		return s.cfg.CaddyfilePath
	}
	return DefaultCaddyfilePath
}

// ---------- building a bundle ----------

// snapshotDB writes a consistent copy of the live database to dst (VACUUM INTO runs inside one
// read transaction, so concurrent writers never produce a torn copy).
func snapshotDB(ctx context.Context, st *store.Store, dst string) error {
	os.Remove(dst)
	if _, err := st.DB.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("database snapshot: %w", err)
	}
	return os.Chmod(dst, 0o600)
}

type bundleSources struct {
	DataDir   string
	EnvFile   string // optional
	Caddyfile string // optional
	PublicURL string
}

// writeBundle snapshots the database and writes an encrypted bundle to w.
func writeBundle(ctx context.Context, st *store.Store, src bundleSources, passphrase string, w io.Writer) (api.ControlBundleManifest, error) {
	tmp, err := os.MkdirTemp(src.DataDir, ".control-bundle-")
	if err != nil {
		return api.ControlBundleManifest{}, err
	}
	defer os.RemoveAll(tmp)
	snap := filepath.Join(tmp, "atoll.db")
	if err := snapshotDB(ctx, st, snap); err != nil {
		return api.ControlBundleManifest{}, err
	}
	entries := []ctlbundle.Entry{
		{Name: ctlbundle.FileDB, Path: snap},
		{Name: ctlbundle.FileMasterKey, Path: filepath.Join(src.DataDir, "master.key")},
	}
	for name, p := range map[string]string{ctlbundle.FileServerEnv: src.EnvFile, ctlbundle.FileCaddyfile: src.Caddyfile} {
		if p == "" {
			continue
		}
		if b, err := os.ReadFile(p); err == nil {
			entries = append(entries, ctlbundle.Entry{Name: name, Data: b})
		}
	}
	slices.SortFunc(entries, func(a, b ctlbundle.Entry) int { return strings.Compare(a.Name, b.Name) })
	hn, _ := os.Hostname()
	m := api.ControlBundleManifest{Version: Version, CreatedAt: time.Now().UTC(), Hostname: hn, PublicURL: src.PublicURL}
	if err := ctlbundle.Write(w, passphrase, m, entries); err != nil {
		return m, err
	}
	m.Format = 1
	for _, e := range entries {
		m.Files = append(m.Files, e.Name)
	}
	return m, nil
}

func (s *Server) bundleSources(ctx context.Context) bundleSources {
	return bundleSources{DataDir: s.cfg.DataDir, EnvFile: s.serverEnvPath(), Caddyfile: s.caddyfilePath(), PublicURL: s.publicURL(ctx, nil)}
}

// ---------- passphrase ----------

// controlPassphrase is the passphrase scheduled and local bundles are encrypted with.
func (s *Server) controlPassphrase(ctx context.Context) (string, string, error) {
	if v, _ := s.st.GetSetting(ctx, "control_backup_passphrase"); v != "" {
		from, _ := s.st.GetSetting(ctx, "control_backup_passphrase_source")
		if from == "" {
			from = "control_backup_passphrase"
		}
		return v, from, nil
	}
	if v, _ := s.st.GetSetting(ctx, "backup_repo_password"); v != "" {
		return v, "backup_repo_password", nil
	}
	v := randString(32, tokenAlphabet)
	if err := s.st.SetSetting(ctx, "control_backup_passphrase", v); err != nil {
		return "", "", err
	}
	s.st.SetSetting(ctx, "control_backup_passphrase_source", "generated")
	return v, "generated", nil
}

func (s *Server) handleControlPassphrase(w http.ResponseWriter, r *http.Request) error {
	var req api.ControlPassphraseRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := s.checkReauth(w, r, req.ReauthFields, "backup_passphrase"); err != nil {
		return err
	}
	ctx := r.Context()
	p := principalFrom(ctx)
	if req.Passphrase != "" {
		if len(req.Passphrase) < ctlbundle.MinPassphrase {
			return badRequest("the passphrase must be at least %d characters", ctlbundle.MinPassphrase)
		}
		if err := s.st.SetSetting(ctx, "control_backup_passphrase", req.Passphrase); err != nil {
			return err
		}
		s.st.SetSetting(ctx, "control_backup_passphrase_source", "control_backup_passphrase")
		s.st.Audit(ctx, p.actor(), "control.backup_passphrase_set", "", nil)
		writeJSON(w, 200, api.ControlPassphrase{Passphrase: req.Passphrase, From: "control_backup_passphrase"})
		return nil
	}
	v, from, err := s.controlPassphrase(ctx)
	if err != nil {
		return err
	}
	s.st.Audit(ctx, p.actor(), "control.backup_passphrase_reveal", "", map[string]string{"from": from})
	writeJSON(w, 200, api.ControlPassphrase{Passphrase: v, From: from})
	return nil
}

// ---------- local store ----------

func (s *Server) sidecarPath(id string) string {
	return filepath.Join(s.controlBackupDir(), id+".json")
}

func (s *Server) bundlePath(id string) string {
	return filepath.Join(s.controlBackupDir(), id+controlBundleExt)
}

func (s *Server) saveSidecar(b api.ControlBackup) {
	raw, _ := json.MarshalIndent(b, "", "  ")
	os.WriteFile(s.sidecarPath(b.ID), raw, 0o600)
}

func (s *Server) localControlBackups() []api.ControlBackup {
	ents, _ := os.ReadDir(s.controlBackupDir())
	out := []api.ControlBackup{}
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !controlBackupIDRe.MatchString(id) {
			continue
		}
		raw, err := os.ReadFile(s.sidecarPath(id))
		if err != nil {
			continue
		}
		var b api.ControlBackup
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		b.Local = fileSize(s.bundlePath(id)) > 0
		out = append(out, b)
	}
	slices.SortFunc(out, func(a, b api.ControlBackup) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out
}

func (s *Server) lastControlBackup() *api.ControlBackup {
	l := s.localControlBackups()
	if len(l) == 0 {
		return nil
	}
	return &l[0]
}

func (s *Server) pruneLocalControlBackups(keep int) {
	l := s.localControlBackups()
	for i, b := range l {
		if i < keep {
			continue
		}
		os.Remove(s.bundlePath(b.ID))
		if !b.Remote {
			os.Remove(s.sidecarPath(b.ID))
		} else {
			b.Local = false
			s.saveSidecar(b)
		}
	}
}

// ---------- remote (the service backup bucket) ----------

func (s *Server) controlS3(ctx context.Context) (*s3mini.Client, string, bool) {
	repo, ok := s.backupRepo(ctx)
	if !ok {
		return nil, "", false
	}
	prefix := strings.Trim(repo.Prefix, "/")
	if prefix == "" {
		prefix = defaultBackupPrefix
	}
	c := &s3mini.Client{Endpoint: repo.Endpoint, Bucket: repo.Bucket, Region: repo.Region, AccessKey: repo.AccessKey, SecretKey: repo.SecretKey, HTTP: s.cfg.S3HTTP}
	return c, prefix + "/control/", true
}

func (s *Server) controlKeepRemote(ctx context.Context) int {
	if v, _ := s.st.GetSetting(ctx, "control_backup_keep"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultControlKeepRem
}

func (s *Server) pruneRemoteControlBackups(ctx context.Context, c *s3mini.Client, prefix string, keep int) error {
	objs, err := c.List(ctx, prefix)
	if err != nil {
		return err
	}
	ids := []string{}
	for _, o := range objs {
		id, ok := strings.CutSuffix(strings.TrimPrefix(o.Key, prefix), controlBundleExt)
		if ok && controlBackupIDRe.MatchString(id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids) // ids sort by creation time
	for len(ids) > keep {
		if err := c.Delete(ctx, prefix+ids[0]+controlBundleExt); err != nil {
			return err
		}
		if raw, err := os.ReadFile(s.sidecarPath(ids[0])); err == nil {
			var b api.ControlBackup
			if json.Unmarshal(raw, &b) == nil {
				if fileSize(s.bundlePath(b.ID)) > 0 {
					b.Remote, b.RemoteKey = false, ""
					s.saveSidecar(b)
				} else {
					os.Remove(s.sidecarPath(b.ID))
				}
			}
		}
		ids = ids[1:]
	}
	return nil
}

// ---------- running a backup ----------

// runControlBackup writes a new local bundle, uploads it when a backup target is configured and
// prunes old copies. trigger: "scheduled" | "manual" | "pre-update".
func (s *Server) runControlBackup(ctx context.Context, trigger, actor string) (api.ControlBackup, error) {
	s.ctl.backupMu.Lock()
	defer s.ctl.backupMu.Unlock()
	pass, _, err := s.controlPassphrase(ctx)
	if err != nil {
		return api.ControlBackup{}, err
	}
	if err := os.MkdirAll(s.controlBackupDir(), 0o700); err != nil {
		return api.ControlBackup{}, err
	}
	now := time.Now().UTC()
	b := api.ControlBackup{ID: "cb_" + now.Format(controlIDTime) + "_" + strings.ToLower(randString(4, "abcdefghijklmnopqrstuvwxyz0123456789")), CreatedAt: now, Trigger: trigger, Version: Version}
	final := s.bundlePath(b.ID)
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return b, err
	}
	h := sha256.New()
	_, werr := writeBundle(ctx, s.st, s.bundleSources(ctx), pass, io.MultiWriter(f, h))
	if err := f.Close(); werr == nil {
		werr = err
	}
	if werr != nil {
		os.Remove(tmp)
		s.st.Audit(ctx, actor, "control.backup", b.ID, map[string]string{"trigger": trigger, "result": werr.Error()})
		return b, werr
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return b, err
	}
	b.SHA256, b.SizeBytes, b.Local = hex.EncodeToString(h.Sum(nil)), fileSize(final), true
	if c, prefix, ok := s.controlS3(ctx); ok {
		uctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		key := prefix + b.ID + controlBundleExt
		if err := c.PutFile(uctx, key, final); err != nil {
			b.Error = "upload failed: " + err.Error()
			s.log.Warn("control backup upload failed", "id", b.ID, "err", err)
		} else {
			b.Remote, b.RemoteKey = true, key
			if err := s.pruneRemoteControlBackups(uctx, c, prefix, s.controlKeepRemote(ctx)); err != nil {
				s.log.Warn("control backup remote prune", "err", err)
			}
		}
		cancel()
	}
	s.saveSidecar(b)
	s.pruneLocalControlBackups(controlKeepLocal)
	detail := map[string]any{"trigger": trigger, "size": b.SizeBytes, "remote": b.Remote}
	if b.Error != "" {
		detail["error"] = b.Error
	}
	s.st.Audit(ctx, actor, "control.backup", b.ID, detail)
	if trigger == "scheduled" {
		s.st.SetSetting(ctx, "control_backup_last_scheduled", store.TS(now))
	}
	s.log.Info("control backup written", "id", b.ID, "trigger", trigger, "bytes", b.SizeBytes, "remote", b.Remote)
	return b, nil
}

func (s *Server) lastScheduledControlBackup(ctx context.Context) time.Time {
	v, _ := s.st.GetSetting(ctx, "control_backup_last_scheduled")
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

func (s *Server) startControlBackupScheduler() {
	s.goBG(func(bg context.Context) {
		t := time.NewTimer(controlBackupFirst)
		defer t.Stop()
		for {
			select {
			case <-bg.Done():
				return
			case <-t.C:
			}
			if time.Since(s.lastScheduledControlBackup(bg)) >= controlBackupEvery {
				if _, err := s.runControlBackup(bg, "scheduled", "system"); err != nil {
					s.log.Warn("scheduled control backup failed", "err", err)
				}
			}
			t.Reset(controlBackupTick)
		}
	})
}

// ---------- handlers ----------

func (s *Server) handleListControlBackups(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	out := api.ControlBackupList{Backups: s.localControlBackups(), KeepLocal: controlKeepLocal, KeepRemote: s.controlKeepRemote(ctx)}
	_, from, _ := s.controlPassphrase(ctx)
	out.PassphraseFrom = from
	if last := s.lastScheduledControlBackup(ctx); !last.IsZero() {
		n := last.Add(controlBackupEvery)
		out.NextScheduled = &n
	}
	if c, prefix, ok := s.controlS3(ctx); ok {
		out.RemoteEnabled = true
		lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		objs, err := c.List(lctx, prefix)
		cancel()
		if err != nil {
			out.RemoteError = err.Error()
		} else {
			known := map[string]int{}
			for i, b := range out.Backups {
				known[b.ID] = i
			}
			for _, o := range objs {
				id, ok := strings.CutSuffix(strings.TrimPrefix(o.Key, prefix), controlBundleExt)
				if !ok || !controlBackupIDRe.MatchString(id) {
					continue
				}
				if i, ok := known[id]; ok {
					out.Backups[i].Remote, out.Backups[i].RemoteKey = true, o.Key
					continue
				}
				ts, _ := time.Parse(controlIDTime, id[3:3+len(controlIDTime)])
				out.Backups = append(out.Backups, api.ControlBackup{ID: id, CreatedAt: ts, Trigger: "unknown", SizeBytes: o.Size, Remote: true, RemoteKey: o.Key})
			}
			slices.SortFunc(out.Backups, func(a, b api.ControlBackup) int { return b.CreatedAt.Compare(a.CreatedAt) })
		}
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleRunControlBackup(w http.ResponseWriter, r *http.Request) error {
	b, err := s.runControlBackup(r.Context(), "manual", principalFrom(r.Context()).actor())
	if err != nil {
		return err
	}
	writeJSON(w, 201, b)
	return nil
}

func (s *Server) handleDownloadControlBackup(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if !controlBackupIDRe.MatchString(id) {
		return errf(404, "not_found", "no such control backup")
	}
	if err := s.checkReauth(w, r, api.ReauthFields{}, "backup_download"); err != nil {
		return err
	}
	ctx := r.Context()
	actor := principalFrom(ctx).actor()
	name := "atoll-control-" + id + controlBundleExt
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if f, err := os.Open(s.bundlePath(id)); err == nil {
		defer f.Close()
		st, _ := f.Stat()
		s.st.Audit(ctx, actor, "control.backup_download", id, map[string]string{"from": "local"})
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, name, st.ModTime(), f)
		return nil
	}
	c, prefix, ok := s.controlS3(ctx)
	if !ok {
		return errf(404, "not_found", "no such control backup")
	}
	body, n, err := c.Get(ctx, prefix+id+controlBundleExt)
	if err != nil {
		return errf(404, "not_found", "control backup %s: %v", id, err)
	}
	defer body.Close()
	s.st.Audit(ctx, actor, "control.backup_download", id, map[string]string{"from": "remote"})
	w.Header().Set("Content-Type", "application/octet-stream")
	if n > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
	}
	io.Copy(w, body)
	return nil
}

func (s *Server) handleControlExport(w http.ResponseWriter, r *http.Request) error {
	var req api.ControlExportRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if len(req.Passphrase) < ctlbundle.MinPassphrase {
		return badRequest("the passphrase must be at least %d characters", ctlbundle.MinPassphrase)
	}
	if err := s.checkReauth(w, r, req.ReauthFields, "export"); err != nil {
		return err
	}
	ctx := r.Context()
	if err := os.MkdirAll(s.controlBackupDir(), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.controlBackupDir(), ".export-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	m, err := writeBundle(ctx, s.st, s.bundleSources(ctx), req.Passphrase, f)
	if err != nil {
		s.st.Audit(ctx, principalFrom(ctx).actor(), "control.export", "", map[string]string{"result": err.Error()})
		return err
	}
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	s.st.Audit(ctx, principalFrom(ctx).actor(), "control.export", "", map[string]any{"files": m.Files, "size": st.Size()})
	host := m.Hostname
	if host == "" {
		host = "control"
	}
	name := "atoll-control-" + fileSafe(host) + "-" + m.CreatedAt.Format("20060102T150405Z") + controlBundleExt
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, m.CreatedAt, f)
	return nil
}

func fileSafe(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.') {
			b[i] = '-'
		}
	}
	return string(b)
}

// ---------- offline export / restore (atoll-server export|restore) ----------

// ExportControl writes an encrypted control bundle of dataDir to out (offline: the server may be
// stopped; when it runs, the snapshot is still consistent).
func ExportControl(ctx context.Context, dataDir, out, passphrase, envFile, caddyfile string) (*api.ControlBundleManifest, error) {
	if len(passphrase) < ctlbundle.MinPassphrase {
		return nil, fmt.Errorf("the passphrase must be at least %d characters", ctlbundle.MinPassphrase)
	}
	st, err := openDataStore(dataDir)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	pub, _ := st.GetSetting(ctx, "public_url")
	if pub == "" {
		pub = envValue(envFile, "ATOLL_PUBLIC_URL")
	}
	f, err := os.OpenFile(out+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	m, werr := writeBundle(ctx, st, bundleSources{DataDir: dataDir, EnvFile: envFile, Caddyfile: caddyfile, PublicURL: pub}, passphrase, f)
	if err := f.Close(); werr == nil {
		werr = err
	}
	if werr != nil {
		os.Remove(out + ".tmp")
		return nil, werr
	}
	if err := os.Rename(out+".tmp", out); err != nil {
		return nil, err
	}
	st.Audit(ctx, "cli", "control.export", out, map[string]any{"files": m.Files})
	return &m, nil
}

// RestoreOptions configures RestoreControl.
type RestoreOptions struct {
	Bundle     string
	Passphrase string
	DataDir    string
	// EnvFile, when set, receives the bundle's server.env: keys already in the file win, the
	// rest are added (a new host keeps what its installer just wrote).
	EnvFile string
	// Force replaces an existing database (moved aside to <data>/pre-restore-<time>/).
	Force bool
	// AdoptPublicIP makes the next start point the panel's DNS record at this host.
	AdoptPublicIP bool
	// KeepTailnetURL keeps a stored tailnet control URL that is an IP address (by default it is
	// cleared: the new host has a different tailnet IP and re-announces its own).
	KeepTailnetURL bool
}

// RestoreResult reports what RestoreControl did.
type RestoreResult struct {
	Manifest       api.ControlBundleManifest
	MovedAside     string
	EnvWritten     bool
	CaddyfileSaved string
	TailnetCleared string
	Nodes          int
}

// RestoreControl installs a control bundle into opts.DataDir. Run it with atoll-server stopped.
// When it fails before the bundle is installed (a wrong passphrase, a damaged bundle), the
// directories it created for opts.DataDir are removed again; a directory that existed before
// is never removed.
func RestoreControl(ctx context.Context, opts RestoreOptions) (*RestoreResult, error) {
	if opts.DataDir == "" {
		return nil, errors.New("data dir required")
	}
	in, err := os.Open(opts.Bundle)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	created := firstMissingDir(opts.DataDir)
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, err
	}
	installed := false
	res, err := restoreControl(ctx, opts, in, &installed)
	if err != nil && !installed && created != "" {
		os.RemoveAll(created)
	}
	return res, err
}

// firstMissingDir returns the outermost directory of path that does not exist yet ("" when
// path already exists), i.e. what os.MkdirAll(path) would create.
func firstMissingDir(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	missing := ""
	for p := abs; ; p = filepath.Dir(p) {
		if _, err := os.Lstat(p); err == nil {
			return missing
		} else if !os.IsNotExist(err) {
			return "" // unknown state: never remove anything
		}
		missing = p
		if filepath.Dir(p) == p {
			return ""
		}
	}
}

func restoreControl(ctx context.Context, opts RestoreOptions, in io.Reader, installed *bool) (*RestoreResult, error) {
	dbPath := filepath.Join(opts.DataDir, "atoll.db")
	if _, err := os.Stat(dbPath); err == nil && !opts.Force {
		return nil, fmt.Errorf("%s already has a database; stop atoll-server and pass --force to replace it (the current one is kept aside)", opts.DataDir)
	}
	tmp, err := os.MkdirTemp(opts.DataDir, ".restore-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	m, err := ctlbundle.Extract(in, opts.Passphrase, tmp)
	if err != nil {
		return nil, err
	}
	res := &RestoreResult{Manifest: *m}
	// the bundle must open with its own key before anything is replaced
	key, err := secrets.LoadOrCreateKey(filepath.Join(tmp, ctlbundle.FileMasterKey))
	if err != nil {
		return nil, fmt.Errorf("bundle master.key: %w", err)
	}
	box, err := secrets.New(key)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(tmp, ctlbundle.FileDB), box)
	if err != nil {
		return nil, fmt.Errorf("bundle database: %w", err)
	}
	var ok string
	if err := st.DB.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		st.Close()
		return nil, fmt.Errorf("bundle database failed the integrity check: %v %s", err, ok)
	}
	if v, _ := st.GetSetting(ctx, "tailnet_control_url"); v != "" && !opts.KeepTailnetURL {
		if u, err := url.Parse(v); err == nil && net.ParseIP(u.Hostname()) != nil {
			st.DB.ExecContext(ctx, `DELETE FROM settings WHERE key='tailnet_control_url'`)
			res.TailnetCleared = v
		}
	}
	st.DB.ExecContext(ctx, `UPDATE nodes SET status='offline' WHERE status='online'`)
	if nodes, err := st.ListNodes(ctx); err == nil {
		res.Nodes = len(nodes)
	}
	st.Audit(ctx, "cli", "control.restore", m.Hostname, map[string]any{"bundle_version": m.Version, "created_at": m.CreatedAt, "restored_by": Version})
	if _, err := st.DB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		st.Close()
		return nil, err
	}
	st.Close()

	// move the current state aside, then install
	if _, err := os.Stat(dbPath); err == nil {
		aside := filepath.Join(opts.DataDir, "pre-restore-"+time.Now().UTC().Format("20060102T150405Z"))
		if err := os.MkdirAll(aside, 0o700); err != nil {
			return nil, err
		}
		for _, n := range []string{"atoll.db", "atoll.db-wal", "atoll.db-shm", "master.key"} {
			if _, err := os.Stat(filepath.Join(opts.DataDir, n)); err == nil {
				if err := os.Rename(filepath.Join(opts.DataDir, n), filepath.Join(aside, n)); err != nil {
					return nil, err
				}
			}
		}
		res.MovedAside = aside
	}
	for _, n := range []string{"atoll.db-wal", "atoll.db-shm"} {
		os.Remove(filepath.Join(opts.DataDir, n))
	}
	if err := os.Rename(filepath.Join(tmp, ctlbundle.FileMasterKey), filepath.Join(opts.DataDir, "master.key")); err != nil {
		return nil, err
	}
	if err := os.Rename(filepath.Join(tmp, ctlbundle.FileDB), dbPath); err != nil {
		return nil, err
	}
	*installed = true
	if b, err := os.ReadFile(filepath.Join(tmp, ctlbundle.FileCaddyfile)); err == nil {
		dir := filepath.Join(opts.DataDir, "control-restore")
		os.MkdirAll(dir, 0o700)
		p := filepath.Join(dir, "Caddyfile")
		if os.WriteFile(p, b, 0o600) == nil {
			res.CaddyfileSaved = p
		}
	}
	if opts.EnvFile != "" {
		if b, err := os.ReadFile(filepath.Join(tmp, ctlbundle.FileServerEnv)); err == nil {
			if err := mergeEnvFile(opts.EnvFile, string(b)); err != nil {
				return res, fmt.Errorf("server.env: %w", err)
			}
			res.EnvWritten = true
		}
	}
	if opts.AdoptPublicIP {
		os.WriteFile(filepath.Join(opts.DataDir, "adopt-public-ip"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	}
	return res, nil
}

func envKey(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	k, _, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(k)
}

// restoreSkipEnv are server.env keys a restore does not carry over: with the agent endpoints
// answered only on the old host's tailnet IP, agents could not find the new host through the
// public domain. Turn it back on once every agent is on the new tailnet URL.
var restoreSkipEnv = map[string]bool{"ATOLL_AGENT_TAILNET_ONLY": true}

// mergeEnvFile adds the KEY=VALUE lines of restored whose keys path does not set yet.
func mergeEnvFile(path, restored string) error {
	cur, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(cur), "\n") {
		if k := envKey(l); k != "" {
			have[k] = true
		}
	}
	out := strings.TrimRight(string(cur), "\n")
	added := []string{}
	for _, l := range strings.Split(restored, "\n") {
		if k := envKey(l); k != "" && !have[k] && !restoreSkipEnv[k] {
			added = append(added, strings.TrimSpace(l))
			have[k] = true
		}
	}
	if len(added) == 0 && len(cur) > 0 {
		return nil
	}
	if out == "" {
		out = "# atoll-server environment (restored from a control bundle)"
	}
	if len(added) > 0 {
		out += "\n# restored from a control bundle\n" + strings.Join(added, "\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out+"\n"), 0o600)
}

// envValue reads KEY from an env file ("" when missing).
func envValue(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	v := ""
	for _, l := range strings.Split(string(b), "\n") {
		if envKey(l) == key {
			_, v, _ = strings.Cut(strings.TrimSpace(l), "=")
		}
	}
	return strings.Trim(strings.TrimSpace(v), `"'`)
}
