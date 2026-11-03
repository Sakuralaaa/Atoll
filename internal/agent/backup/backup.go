// Package backup is the agent side of Atoll's scheduled backups: it runs restic in a
// container (`docker run --rm restic/restic`) straight against the user's S3 bucket, so
// backup data never passes through the control server. Databases (postgres, mysql, mongo,
// redis) are dumped with their own tools inside the running container and streamed into
// `restic backup --stdin`; volume dirs are bind-mounted read-only and snapshotted.
//
// The agent persists its policies (backup/config.json, mode 0600: it holds the repository
// credentials) and runs the schedule itself, so backups keep happening while the control
// server is unreachable. Results go to the server as MsgBackupEvent frames, which the agent
// queues while disconnected.
package backup

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// DefaultImage is the pinned restic image.
const DefaultImage = "restic/restic:0.17.3"

// DBKinds get a logical dump.
var DBKinds = map[string]bool{"postgres": true, "mysql": true, "mongo": true, "redis": true}

// Manager implements agent.Extension for the backup.* messages and runs the local schedule.
type Manager struct {
	RT     agent.Runtime
	Paths  agent.Paths
	Run    Runner
	Docker string // docker CLI (default "docker")
	Image  string // restic image (default DefaultImage)
	Host   string // restic --host (the node id)
	Emit   func(msgType string, payload any)
	Log    *slog.Logger
	// Locked serializes a restore with deploys of the same service (agent.Manager.Locked).
	Locked func(serviceID string, fn func() error) error
	Now    func() time.Time

	Tick         time.Duration // schedule check interval
	ReadyTimeout time.Duration // database readiness wait during restore
	ReadyEvery   time.Duration
	Settle       time.Duration // re-check delay after the first successful readiness probe
	StopTimeout  time.Duration
	RetryAfter   time.Duration // delay before retrying a failed scheduled run (x attempts)
	MaxRetries   int
	CheckTimeout time.Duration // a repository check
	DrillTimeout time.Duration // one restore drill
	LockWait     time.Duration // restic --retry-lock for backups, checks and drills
	// FreeSpace reports the bytes available at a path (default statfs; tests override).
	FreeSpace func(path string) (uint64, error)

	mu        sync.Mutex
	cfg       api.BackupConfigPayload
	state     map[string]*svcState
	vstate    map[string]*svcState // verification schedule: "check", "drill:<service>"
	verifying map[string]bool      // verification queued or running, by verifyKey
	repoOK    map[string]bool
	runMu     sync.Mutex // one backup/restore/check/drill at a time on this node
	stateMu   sync.Mutex // read-modify-write of state.json
	loaded    bool
	base      context.Context // set by Loop; cancels queued runs on shutdown
}

type svcState struct {
	LastAttempt time.Time `json:"last_attempt"`
	LastSuccess time.Time `json:"last_success"`
	Failures    int       `json:"failures"` // consecutive
}

type stateFile struct {
	Services       map[string]*svcState `json:"services"`
	InFlight       *inFlight            `json:"in_flight,omitempty"`
	Verify         map[string]*svcState `json:"verify,omitempty"`
	VerifyInFlight *verifyInFlight      `json:"verify_in_flight,omitempty"`
}

type inFlight struct {
	BackupID  string    `json:"backup_id"`
	ServiceID string    `json:"service_id"`
	Trigger   string    `json:"trigger"`
	StartedAt time.Time `json:"started_at"`
}

// New returns a Manager with production defaults.
func New(rt agent.Runtime, p agent.Paths, host string, emit func(string, any), log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	if emit == nil {
		emit = func(string, any) {}
	}
	return &Manager{
		RT: rt, Paths: p, Run: HostRunner{}, Docker: "docker", Image: DefaultImage, Host: host, Emit: emit, Log: log,
		Locked: func(_ string, fn func() error) error { return fn() },
		Now:    time.Now, Tick: 30 * time.Second, ReadyTimeout: 3 * time.Minute, ReadyEvery: 2 * time.Second,
		Settle: 5 * time.Second, StopTimeout: 30 * time.Second, RetryAfter: 15 * time.Minute, MaxRetries: 3,
		CheckTimeout: 12 * time.Hour, DrillTimeout: 6 * time.Hour, LockWait: 30 * time.Minute,
		state: map[string]*svcState{}, vstate: map[string]*svcState{}, verifying: map[string]bool{}, repoOK: map[string]bool{},
	}
}

func (m *Manager) dir() string                { return filepath.Join(m.Paths.Root, "backup") }
func (m *Manager) configPath() string         { return filepath.Join(m.dir(), "config.json") }
func (m *Manager) statePath() string          { return filepath.Join(m.dir(), "state.json") }
func (m *Manager) cacheDir() string           { return filepath.Join(m.dir(), "cache") }
func (m *Manager) asideDir(svc string) string { return filepath.Join(m.dir(), "pre-restore", svc) }

// ---------- agent.Extension ----------

func (m *Manager) Handles(t string) bool {
	switch t {
	case api.MsgBackupConfig, api.MsgBackupRun, api.MsgBackupRestore, api.MsgBackupSnapshots, api.MsgBackupTest, api.MsgBackupVerify:
		return true
	}
	return false
}

func (m *Manager) Handle(ctx context.Context, t string, raw json.RawMessage) (string, error) {
	switch t {
	case api.MsgBackupConfig:
		var p api.BackupConfigPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("bad payload: %w", err)
		}
		return "", m.SetConfig(p)
	case api.MsgBackupRun:
		var p api.BackupRunPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("bad payload: %w", err)
		}
		return m.Queue(p)
	case api.MsgBackupRestore:
		var p api.BackupRestorePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("bad payload: %w", err)
		}
		res, err := m.Restore(ctx, p)
		b, _ := json.Marshal(res)
		return string(b), err
	case api.MsgBackupSnapshots:
		var p api.BackupSnapshotsPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("bad payload: %w", err)
		}
		snaps, err := m.Snapshots(ctx, p.Repo, p.ServiceID)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(snaps)
		return string(b), nil
	case api.MsgBackupTest:
		var p api.BackupRepo
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("bad payload: %w", err)
		}
		return m.Test(ctx, p)
	case api.MsgBackupVerify:
		var p api.BackupVerifyPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("bad payload: %w", err)
		}
		return m.QueueVerify(p)
	}
	return "", fmt.Errorf("unsupported message type %q", t)
}

// ---------- config & state ----------

// SetConfig persists the node's backup config. New targets start counting from now, so
// enabling a policy does not trigger an immediate run.
func (m *Manager) SetConfig(p api.BackupConfigPayload) error {
	m.load()
	if p.Targets == nil {
		p.Targets = []api.BackupTarget{}
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFile(m.configPath(), b, 0o600); err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg = p
	now := m.Now()
	for _, t := range p.Targets {
		if m.state[t.ServiceID] == nil {
			m.state[t.ServiceID] = &svcState{LastAttempt: now}
		}
		if m.vstate[drillKey(t.ServiceID)] == nil {
			m.vstate[drillKey(t.ServiceID)] = &svcState{LastAttempt: now}
		}
	}
	if m.vstate[checkKey] == nil {
		m.vstate[checkKey] = &svcState{LastAttempt: now}
	}
	m.mu.Unlock()
	return m.saveState(nil, false)
}

// Config returns the current config (tests).
func (m *Manager) Config() api.BackupConfigPayload {
	m.load()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

func (m *Manager) load() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded {
		return
	}
	m.loaded = true
	if b, err := os.ReadFile(m.configPath()); err == nil {
		if err := json.Unmarshal(b, &m.cfg); err != nil {
			m.Log.Warn("backup config unreadable", "err", err)
		}
	}
	if b, err := os.ReadFile(m.statePath()); err == nil {
		var sf stateFile
		if json.Unmarshal(b, &sf) == nil {
			if sf.Services != nil {
				m.state = sf.Services
			}
			if sf.Verify != nil {
				m.vstate = sf.Verify
			}
		}
	}
}

func (m *Manager) readStateFile() stateFile {
	var sf stateFile
	if b, err := os.ReadFile(m.statePath()); err == nil {
		json.Unmarshal(b, &sf)
	}
	return sf
}

func (m *Manager) readInFlight() *inFlight { return m.readStateFile().InFlight }

func (m *Manager) readVerifyInFlight() *verifyInFlight { return m.readStateFile().VerifyInFlight }

// writeState writes the schedule state; edit may replace the in-flight markers (the ones it
// leaves alone keep their value on disk).
func (m *Manager) writeState(edit func(sf *stateFile)) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	cur := m.readStateFile()
	sf := stateFile{InFlight: cur.InFlight, VerifyInFlight: cur.VerifyInFlight}
	if edit != nil {
		edit(&sf)
	}
	m.mu.Lock()
	sf.Services, sf.Verify = m.state, m.vstate
	b, err := json.MarshalIndent(sf, "", "  ")
	m.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFile(m.statePath(), b, 0o600)
}

// saveState writes the per-service state; fl replaces the backup in-flight marker when setFl.
func (m *Manager) saveState(fl *inFlight, setFl bool) error {
	return m.writeState(func(sf *stateFile) {
		if setFl {
			sf.InFlight = fl
		}
	})
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newBackupID() string {
	const alpha = "abcdefghijklmnopqrstuvwxyz234567"
	b := make([]byte, 12)
	rand.Read(b)
	for i := range b {
		b[i] = alpha[int(b[i])%len(alpha)]
	}
	return "bak_" + string(b)
}

// ---------- running ----------

// Queue starts a run in the background (runs on a node are serialized).
func (m *Manager) Queue(p api.BackupRunPayload) (string, error) {
	if err := validTarget(p.Target); err != nil {
		return "", err
	}
	if p.Since != nil {
		m.load()
		m.mu.Lock()
		st := m.state[p.Target.ServiceID]
		skip := st != nil && !st.LastSuccess.Before(*p.Since)
		m.mu.Unlock()
		if skip {
			return "skipped", nil
		}
	}
	if p.BackupID == "" {
		p.BackupID = newBackupID()
	}
	if p.Trigger == "" {
		p.Trigger = "manual"
	}
	m.mu.Lock()
	ctx := m.base
	m.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go m.RunBackup(ctx, p.BackupID, p.Trigger, p.Target, p.Repo)
	return "queued", nil
}

func validTarget(t api.BackupTarget) error {
	if t.ServiceID == "" || strings.ContainsAny(t.ServiceID, "/\\. ") {
		return fmt.Errorf("invalid service id %q", t.ServiceID)
	}
	return nil
}

// RunBackup performs one backup run and reports it. It returns the final event.
func (m *Manager) RunBackup(ctx context.Context, id, trigger string, t api.BackupTarget, repo api.BackupRepo) api.BackupEvent {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	m.load()
	start := m.Now().UTC()
	ev := api.BackupEvent{BackupID: id, ServiceID: t.ServiceID, Trigger: trigger, Status: "running", StartedAt: start}
	m.Emit(api.MsgBackupEvent, ev)
	m.mu.Lock()
	st := m.state[t.ServiceID]
	if st == nil {
		st = &svcState{}
		m.state[t.ServiceID] = st
	}
	st.LastAttempt = start
	m.mu.Unlock()
	m.saveState(&inFlight{BackupID: id, ServiceID: t.ServiceID, Trigger: trigger, StartedAt: start}, true)
	m.Log.Info("backup started", "service", t.ServiceID, "backup", id, "trigger", trigger, "repo", repo)

	snaps, pruned, warns, err := m.backup(ctx, id, t, repo)
	fin := m.Now().UTC()
	ev.Snapshots, ev.Warnings, ev.Pruned = snaps, warns, pruned
	for _, s := range snaps {
		ev.Bytes += s.Bytes
		ev.Added += s.Added
	}
	ev.FinishedAt = &fin
	ev.DurationMs = fin.Sub(start).Milliseconds()
	ev.Status = "done"
	m.mu.Lock()
	if err != nil {
		ev.Status, ev.Error = "failed", err.Error()
		st.Failures++
	} else {
		st.LastSuccess, st.Failures = start, 0
	}
	m.mu.Unlock()
	m.saveState(nil, true)
	if err != nil {
		m.Log.Warn("backup failed", "service", t.ServiceID, "backup", id, "err", err)
	} else {
		m.Log.Info("backup done", "service", t.ServiceID, "backup", id, "bytes", ev.Bytes, "added", ev.Added, "ms", ev.DurationMs)
	}
	m.Emit(api.MsgBackupEvent, ev)
	return ev
}

func (m *Manager) backup(ctx context.Context, id string, t api.BackupTarget, repo api.BackupRepo) (snaps []api.BackupSnapshot, pruned, warns []string, err error) {
	if repo.Endpoint == "" || repo.Bucket == "" || repo.Password == "" {
		return nil, nil, nil, errors.New("no backup repository configured")
	}
	if _, err := agent.NewStateStore(m.Paths).Load(t.ServiceID); err != nil {
		return nil, nil, nil, fmt.Errorf("service %s is not deployed on this server", t.ServiceID)
	}
	if err := m.ensureRepo(ctx, repo); err != nil {
		return nil, nil, nil, err
	}
	doVolumes := t.IncludeVolumes || !DBKinds[t.Kind]
	if DBKinds[t.Kind] {
		info, ierr := m.RT.InspectContainer(ctx, agent.ContainerName(t.ServiceID))
		if ierr != nil || info.State != "running" {
			warns = append(warns, "database is not running: no dump taken, backed up its (consistent, stopped) volumes instead")
			doVolumes = true
		} else {
			sn, derr := m.backupDump(ctx, t, repo)
			if derr != nil {
				return snaps, nil, warns, fmt.Errorf("dump: %w", derr)
			}
			snaps = append(snaps, sn)
		}
	}
	if doVolumes {
		dir := m.Paths.VolumesDir(t.ServiceID)
		if ents, derr := os.ReadDir(dir); derr != nil || len(ents) == 0 {
			if len(snaps) == 0 {
				return nil, nil, warns, fmt.Errorf("nothing to back up: %s is missing or empty", dir)
			}
			if t.IncludeVolumes && len(t.Volumes) > 0 {
				warns = append(warns, "volume dir is empty; skipped")
			}
		} else {
			if DBKinds[t.Kind] && len(snaps) > 0 {
				warns = append(warns, "volume snapshot of a running database is not crash-consistent; restores use the dump")
			}
			sn, verr := m.backupVolumes(ctx, t, repo)
			if verr != nil {
				return snaps, nil, warns, fmt.Errorf("volumes: %w", verr)
			}
			snaps = append(snaps, sn)
		}
	}
	// retention
	parts := map[string]bool{}
	for _, s := range snaps {
		parts[s.Part] = true
	}
	for _, part := range []string{"dump", "volumes"} {
		if !parts[part] {
			continue
		}
		ids, ferr := m.forget(ctx, repo, t, part)
		if ferr != nil {
			warns = append(warns, "retention ("+part+"): "+ferr.Error())
			continue
		}
		pruned = append(pruned, ids...)
	}
	return snaps, pruned, warns, nil
}

// ---------- restic plumbing ----------

func (m *Manager) env(repo api.BackupRepo) []string {
	region := repo.Region
	if region == "" && strings.Contains(repo.Endpoint, "r2.cloudflarestorage.com") {
		region = "auto"
	}
	env := []string{
		"RESTIC_REPOSITORY=" + repo.URL(),
		"RESTIC_PASSWORD=" + repo.Password,
		"AWS_ACCESS_KEY_ID=" + repo.AccessKey,
		"AWS_SECRET_ACCESS_KEY=" + repo.SecretKey,
	}
	if region != "" {
		env = append(env, "AWS_DEFAULT_REGION="+region)
	}
	return env
}

// dockerArgs builds `docker run` for restic. Only variable NAMES appear in argv; the values
// come from the docker CLI's own environment (Runner env).
func (m *Manager) dockerArgs(env []string, stdin bool, mounts []string, resticArgs ...string) []string {
	args := []string{"run", "--rm", "--label", "atoll.role=backup"}
	if stdin {
		args = append(args, "-i")
	}
	for _, kv := range env {
		args = append(args, "-e", kv[:strings.IndexByte(kv, '=')])
	}
	args = append(args, "-v", m.cacheDir()+":/root/.cache/restic")
	for _, mt := range mounts {
		args = append(args, "-v", mt)
	}
	args = append(args, m.Image)
	return append(args, resticArgs...)
}

func (m *Manager) restic(ctx context.Context, repo api.BackupRepo, stdin io.Reader, stdout io.Writer, mounts []string, args ...string) error {
	if err := os.MkdirAll(m.cacheDir(), 0o700); err != nil {
		return err
	}
	env := m.env(repo)
	var errb bytes.Buffer
	if stdout == nil {
		stdout = io.Discard
	}
	return m.Run.Run(ctx, env, stdin, stdout, &errb, m.Docker, m.dockerArgs(env, stdin != nil, mounts, args...)...)
}

// ensureRepo opens the repository and initialises it on first use.
func (m *Manager) ensureRepo(ctx context.Context, repo api.BackupRepo) error {
	key := repo.URL() + "\x00" + repo.AccessKey + "\x00" + repo.Password
	m.mu.Lock()
	ok := m.repoOK[key]
	m.mu.Unlock()
	if ok {
		return nil
	}
	if _, err := m.openRepo(ctx, repo); err != nil {
		return err
	}
	m.mu.Lock()
	m.repoOK[key] = true
	m.mu.Unlock()
	return nil
}

// openRepo returns whether the repository had to be created.
func (m *Manager) openRepo(ctx context.Context, repo api.BackupRepo) (created bool, err error) {
	err = m.restic(ctx, repo, nil, nil, nil, "cat", "config")
	switch code := exitCode(err); {
	case err == nil:
		return false, nil
	case code == 10: // repository does not exist (restic >= 0.17)
		if ierr := m.restic(ctx, repo, nil, nil, nil, "init"); ierr != nil {
			return false, fmt.Errorf("init repository %s: %w", repo.URL(), ierr)
		}
		return true, nil
	case code == 12:
		return false, fmt.Errorf("wrong repository password for %s", repo.URL())
	default:
		return false, fmt.Errorf("open repository %s: %w", repo.URL(), err)
	}
}

// Test checks the repository (creating it when missing) and describes the result.
func (m *Manager) Test(ctx context.Context, repo api.BackupRepo) (string, error) {
	created, err := m.openRepo(ctx, repo)
	if err != nil {
		return "", err
	}
	if created {
		return "repository created at " + repo.URL(), nil
	}
	return "repository OK at " + repo.URL(), nil
}

// retryLock makes restic wait for a lock held elsewhere (a repository check holds an
// exclusive one; backups from other nodes sharing the repository hold shared ones).
func (m *Manager) retryLock() []string {
	if m.LockWait <= 0 {
		return nil
	}
	return []string{"--retry-lock", m.LockWait.String()}
}

func tags(t api.BackupTarget, part string) []string {
	out := []string{"--tag", "atoll", "--tag", "svc=" + t.ServiceID, "--tag", "part=" + part}
	if t.ProjectID != "" {
		out = append(out, "--tag", "project="+t.ProjectID)
	}
	return out
}

func (m *Manager) host() string {
	if m.Host != "" {
		return m.Host
	}
	h, _ := os.Hostname()
	return h
}

// summaryWriter parses restic --json output line by line, keeping only the summary.
type summaryWriter struct {
	buf     []byte
	summary *resticSummary
	errMsg  string
}

type resticSummary struct {
	MessageType string `json:"message_type"`
	SnapshotID  string `json:"snapshot_id"`
	DataAdded   int64  `json:"data_added"`
	TotalBytes  int64  `json:"total_bytes_processed"`
	Message     string `json:"message"`
	Error       *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (w *summaryWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			if len(w.buf) > 1<<20 { // runaway line
				w.buf = w.buf[:0]
			}
			return len(p), nil
		}
		w.line(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
}

func (w *summaryWriter) line(l []byte) {
	l = bytes.TrimSpace(l)
	if len(l) == 0 || l[0] != '{' {
		return
	}
	var s resticSummary
	if json.Unmarshal(l, &s) != nil {
		return
	}
	switch s.MessageType {
	case "summary":
		w.summary = &s
	case "error", "exit_error":
		if s.Error != nil {
			w.errMsg = s.Error.Message
		} else if s.Message != "" {
			w.errMsg = s.Message
		}
	}
}

func (w *summaryWriter) flush() {
	if len(w.buf) > 0 {
		w.line(w.buf)
		w.buf = nil
	}
}

func (w *summaryWriter) snapshot(part string, err error) (api.BackupSnapshot, error) {
	w.flush()
	// exit code 3: snapshot created but some files could not be read
	if err != nil && exitCode(err) != 3 {
		return api.BackupSnapshot{}, err
	}
	if w.summary == nil || w.summary.SnapshotID == "" {
		msg := "restic reported no snapshot"
		if w.errMsg != "" {
			msg += ": " + w.errMsg
		}
		return api.BackupSnapshot{}, errors.New(msg)
	}
	return api.BackupSnapshot{ID: w.summary.SnapshotID, Part: part, Bytes: w.summary.TotalBytes, Added: w.summary.DataAdded}, nil
}

// DumpFile is the file name a database dump is stored under in its snapshot.
func DumpFile(kind string) string {
	switch kind {
	case "postgres":
		return "/postgres.sql"
	case "mysql":
		return "/mysql.sql"
	case "mongo":
		return "/mongo.archive.gz"
	case "redis":
		return "/redis.rdb"
	}
	return "/dump"
}

const (
	pgEnv    = `U="${POSTGRES_USER:-${POSTGRES_USERNAME:-${PGUSER:-postgres}}}"; export PGPASSWORD="${POSTGRES_PASSWORD:-$PGPASSWORD}"; export PGHOST=127.0.0.1; `
	myEnv    = `if [ -n "$MYSQL_ROOT_PASSWORD" ]; then U=root; export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; else U="${MYSQL_USER:-${MYSQL_USERNAME:-root}}"; export MYSQL_PWD="${MYSQL_PASSWORD:-$PASSWORD}"; fi; M=$(command -v mysql || command -v mariadb); D=$(command -v mysqldump || command -v mariadb-dump); `
	mongoEnv = `set --; if [ -n "$MONGO_INITDB_ROOT_USERNAME" ]; then set -- --username "$MONGO_INITDB_ROOT_USERNAME" --password "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin; fi; `
	redisEnv = `P="${REDIS_PASSWORD:-$PASSWORD}"; set --; if [ -n "$P" ]; then set -- -a "$P" --no-auth-warning; fi; `
)

// DumpScript is the shell script run inside the database container (same-major tools,
// credentials from the container's own env) that writes the dump to stdout.
func DumpScript(kind string) (string, error) {
	switch kind {
	case "postgres":
		return pgEnv + `pg_dumpall -U "$U" --clean --if-exists`, nil
	case "mysql":
		return myEnv + `DBS=$("$M" -h127.0.0.1 -u"$U" -N -B -e "show databases" | grep -Ev '^(mysql|information_schema|performance_schema|sys)$'); ` +
			`if [ -z "$DBS" ]; then echo "-- atoll: no user databases"; exit 0; fi; ` +
			`"$D" -h127.0.0.1 -u"$U" --single-transaction --routines --triggers --events --add-drop-database --databases $DBS`, nil
	case "mongo":
		return mongoEnv + `mongodump --host 127.0.0.1 "$@" --archive --gzip`, nil
	case "redis":
		return redisEnv + `redis-cli -h 127.0.0.1 "$@" --rdb /tmp/atoll-backup.rdb >/dev/null && cat /tmp/atoll-backup.rdb; rc=$?; rm -f /tmp/atoll-backup.rdb; exit $rc`, nil
	}
	return "", fmt.Errorf("no dump support for kind %q", kind)
}

// RestoreScript reads a dump on stdin inside the (freshly initialised) database container.
func RestoreScript(kind string) (string, error) {
	switch kind {
	case "postgres":
		return pgEnv + `psql -U "$U" -d postgres -q -o /dev/null -f -`, nil
	case "mysql":
		return myEnv + `"$M" -h127.0.0.1 -u"$U"`, nil
	case "mongo":
		return mongoEnv + `mongorestore --host 127.0.0.1 "$@" --archive --gzip --drop`, nil
	}
	return "", fmt.Errorf("no stream restore for kind %q", kind)
}

// ReadyScript succeeds once the database accepts authenticated TCP connections.
func ReadyScript(kind string) string {
	switch kind {
	case "postgres":
		return pgEnv + `pg_isready -q -h 127.0.0.1 -U "$U" && psql -U "$U" -d postgres -Atc "select 1" >/dev/null`
	case "mysql":
		return myEnv + `"$M" -h127.0.0.1 -u"$U" -N -B -e "select 1" >/dev/null`
	case "mongo":
		return mongoEnv + `if command -v mongosh >/dev/null; then S=mongosh; else S=mongo; fi; "$S" --quiet --host 127.0.0.1 "$@" --eval 'db.adminCommand({ping:1})' >/dev/null`
	case "redis": // PONG once the rdb is loaded (LOADING before)
		return redisEnv + `redis-cli -h 127.0.0.1 "$@" ping | grep -q PONG`
	}
	return "true"
}

type countWriter struct {
	w io.Writer
	n atomic.Int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(int64(n))
	return n, err
}

// backupDump streams the dump from `docker exec` into `restic backup --stdin`.
func (m *Manager) backupDump(ctx context.Context, t api.BackupTarget, repo api.BackupRepo) (api.BackupSnapshot, error) {
	script, err := DumpScript(t.Kind)
	if err != nil {
		return api.BackupSnapshot{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	cw := &countWriter{w: pw}
	dumpErr := make(chan error, 1)
	go func() {
		var errb bytes.Buffer
		code, err := m.RT.Exec(ctx, agent.ContainerName(t.ServiceID), []string{"sh", "-c", script}, nil, cw, &errb)
		if err == nil && code != 0 {
			err = fmt.Errorf("exit %d: %s", code, lastLines(errb.String(), 4, 1000))
		}
		pw.CloseWithError(err)
		dumpErr <- err
	}()
	sw := &summaryWriter{}
	args := append([]string{"backup", "--json", "--stdin", "--stdin-filename", strings.TrimPrefix(DumpFile(t.Kind), "/"), "--host", m.host()}, tags(t, "dump")...)
	args = append(args, m.retryLock()...)
	rerr := m.restic(ctx, repo, pr, sw, nil, args...)
	pr.CloseWithError(errors.New("restic finished"))
	derr := <-dumpErr
	sn, serr := sw.snapshot("dump", rerr)
	if derr == nil && cw.n.Load() == 0 {
		derr = errors.New("dump produced no data")
	}
	if derr != nil {
		if serr == nil { // restic stored a partial dump: drop it
			m.restic(context.Background(), repo, nil, nil, nil, "forget", sn.ID)
		}
		return api.BackupSnapshot{}, derr
	}
	return sn, serr
}

func (m *Manager) backupVolumes(ctx context.Context, t api.BackupTarget, repo api.BackupRepo) (api.BackupSnapshot, error) {
	inside := "/backup/" + t.ServiceID
	sw := &summaryWriter{}
	args := append([]string{"backup", "--json", inside, "--host", m.host()}, tags(t, "volumes")...)
	args = append(args, m.retryLock()...)
	err := m.restic(ctx, repo, nil, sw, []string{m.Paths.VolumesDir(t.ServiceID) + ":" + inside + ":ro"}, args...)
	return sw.snapshot("volumes", err)
}

// ForgetArgs is the retention command for one service part.
func ForgetArgs(t api.BackupTarget, part string) []string {
	args := []string{"forget", "--json", "--retry-lock", "5m", "--tag", "svc=" + t.ServiceID + ",part=" + part, "--group-by", "tags"}
	if t.Keep.Daily > 0 {
		args = append(args, "--keep-daily", fmt.Sprint(t.Keep.Daily))
	}
	if t.Keep.Weekly > 0 {
		args = append(args, "--keep-weekly", fmt.Sprint(t.Keep.Weekly))
	}
	if t.Keep.Monthly > 0 {
		args = append(args, "--keep-monthly", fmt.Sprint(t.Keep.Monthly))
	}
	if t.Keep.Daily+t.Keep.Weekly+t.Keep.Monthly == 0 {
		args = append(args, "--keep-last", "1") // never forget everything
	}
	return append(args, "--prune")
}

func (m *Manager) forget(ctx context.Context, repo api.BackupRepo, t api.BackupTarget, part string) ([]string, error) {
	var out bytes.Buffer
	if err := m.restic(ctx, repo, nil, &out, nil, ForgetArgs(t, part)...); err != nil {
		return nil, err
	}
	return ParseForget(out.Bytes()), nil
}

// ParseForget extracts removed snapshot ids from `restic forget --json` output (the JSON
// array is followed by plain prune output).
func ParseForget(out []byte) []string {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		l := bytes.TrimSpace(sc.Bytes())
		if len(l) == 0 || l[0] != '[' {
			continue
		}
		var groups []struct {
			Remove []struct {
				ID string `json:"id"`
			} `json:"remove"`
		}
		if json.Unmarshal(l, &groups) != nil {
			continue
		}
		var ids []string
		for _, g := range groups {
			for _, r := range g.Remove {
				ids = append(ids, r.ID)
			}
		}
		return ids
	}
	return nil
}

// Snapshots lists the repository's snapshots of a service.
func (m *Manager) Snapshots(ctx context.Context, repo api.BackupRepo, serviceID string) ([]api.ResticSnapshot, error) {
	return m.snapshots(ctx, repo, serviceID)
}

func (m *Manager) snapshots(ctx context.Context, repo api.BackupRepo, serviceID string, extra ...string) ([]api.ResticSnapshot, error) {
	var out bytes.Buffer
	err := m.restic(ctx, repo, nil, &out, nil, append([]string{"snapshots", "--json", "--tag", "svc=" + serviceID}, extra...)...)
	if exitCode(err) == 10 {
		return []api.ResticSnapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	snaps := []api.ResticSnapshot{}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &snaps); err != nil {
		return nil, fmt.Errorf("parse snapshots: %w", err)
	}
	return snaps, nil
}
