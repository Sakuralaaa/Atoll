package backup

// Backup verification: the weekly repository check and the monthly restore drill.
//
// The check runs `restic check --read-data-subset` (the control server tells exactly one node
// to schedule it; restic's exclusive lock guards an overlap, which then reports "skipped").
//
// A drill restores the newest snapshot of each part of a service into throwaway resources:
//   - volumes: `restic restore --verify` into <agent data>/backup/drill/<id>/files; at least one
//     non-empty file must come back.
//   - dump: a temporary container of the service's own image (same version), with fresh data
//     dirs under <agent data>/backup/drill/<id>/db, no network and no restart policy, loads the
//     dump and must answer a query.
// The running service, its container and its volume dirs are never touched. Everything the
// drill created is removed when it ends, and leftovers of an interrupted drill are removed when
// the agent starts.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// LabelDrill marks a drill's temporary container (value: the verification id).
const LabelDrill = "atoll.drill"

// staleSnapshotAge: a drill warns when the newest snapshot it tested is older than this.
const staleSnapshotAge = 8 * 24 * time.Hour

const checkKey = "check"

func drillKey(serviceID string) string { return "drill:" + serviceID }

func verifyKey(kind, serviceID string) string {
	if kind == api.BackupVerifyCheck {
		return checkKey
	}
	return drillKey(serviceID)
}

type verifyInFlight struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	ServiceID string    `json:"service_id,omitempty"`
	Trigger   string    `json:"trigger"`
	StartedAt time.Time `json:"started_at"`
}

func newVerifyID() string { return "bvf_" + strings.TrimPrefix(newBackupID(), "bak_") }

func (m *Manager) drillRoot() string         { return filepath.Join(m.dir(), "drill") }
func (m *Manager) drillDir(id string) string { return filepath.Join(m.drillRoot(), id) }

// DrillContainer is the name of a drill's temporary database container.
func DrillContainer(id string) string { return "atoll-drill-" + id }

// QueueVerify starts a check or drill in the background. It answers "busy" when one of the
// same kind (and service) is already queued or running on this node.
func (m *Manager) QueueVerify(p api.BackupVerifyPayload) (string, error) {
	if err := validVerify(&p); err != nil {
		return "", err
	}
	key := verifyKey(p.Kind, p.ServiceID())
	if !m.claim(key) {
		return "busy", nil
	}
	m.mu.Lock()
	ctx := m.base
	m.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		defer m.release(key)
		m.runVerify(ctx, p)
	}()
	return "queued", nil
}

func validVerify(p *api.BackupVerifyPayload) error {
	switch p.Kind {
	case api.BackupVerifyCheck:
		if p.Subset == "" {
			p.Subset = api.DefaultBackupCheckSubset
		}
		if !api.ValidCheckSubset(p.Subset) {
			return fmt.Errorf("invalid check subset %q", p.Subset)
		}
		p.Target = nil
	case api.BackupVerifyDrill:
		if p.Target == nil {
			return errors.New("drill without a target")
		}
		if err := validTarget(*p.Target); err != nil {
			return err
		}
		p.Subset = ""
	default:
		return fmt.Errorf("unknown verification kind %q", p.Kind)
	}
	if p.ID == "" {
		p.ID = newVerifyID()
	}
	if p.Trigger == "" {
		p.Trigger = "manual"
	}
	return nil
}

func (m *Manager) claim(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.verifying[key] {
		return false
	}
	m.verifying[key] = true
	return true
}

func (m *Manager) release(key string) {
	m.mu.Lock()
	delete(m.verifying, key)
	m.mu.Unlock()
}

// RunVerify runs one check or drill now and returns its final event (status "busy" when one
// of the same kind is already running).
func (m *Manager) RunVerify(ctx context.Context, p api.BackupVerifyPayload) api.BackupVerifyEvent {
	if err := validVerify(&p); err != nil {
		now := m.Now().UTC()
		return api.BackupVerifyEvent{ID: p.ID, Kind: p.Kind, Status: "failed", Error: err.Error(), StartedAt: now, FinishedAt: &now}
	}
	key := verifyKey(p.Kind, p.ServiceID())
	if !m.claim(key) {
		return api.BackupVerifyEvent{ID: p.ID, Kind: p.Kind, ServiceID: p.ServiceID(), Status: "busy"}
	}
	defer m.release(key)
	return m.runVerify(ctx, p)
}

func (m *Manager) runVerify(ctx context.Context, p api.BackupVerifyPayload) api.BackupVerifyEvent {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	m.load()
	start := m.Now().UTC()
	ev := api.BackupVerifyEvent{ID: p.ID, Kind: p.Kind, ServiceID: p.ServiceID(), Trigger: p.Trigger, Status: "running", Subset: p.Subset, StartedAt: start}
	m.Emit(api.MsgBackupVerifyEvent, ev)
	key := verifyKey(p.Kind, p.ServiceID())
	m.mu.Lock()
	st := m.vstate[key]
	if st == nil {
		st = &svcState{}
		m.vstate[key] = st
	}
	st.LastAttempt = start
	m.mu.Unlock()
	m.writeState(func(sf *stateFile) {
		sf.VerifyInFlight = &verifyInFlight{ID: p.ID, Kind: p.Kind, ServiceID: p.ServiceID(), Trigger: p.Trigger, StartedAt: start}
	})
	m.Log.Info("backup verification started", "kind", p.Kind, "service", p.ServiceID(), "id", p.ID, "trigger", p.Trigger, "repo", p.Repo)

	var err error
	status := "done"
	switch p.Kind {
	case api.BackupVerifyCheck:
		cctx, cancel := context.WithTimeout(ctx, m.CheckTimeout)
		step, skipped, cerr := m.check(cctx, p.Repo, p.Subset)
		cancel()
		ev.Steps, err = []api.BackupVerifyStep{step}, cerr
		if skipped {
			status = "skipped"
			ev.Warnings = append(ev.Warnings, "another check holds the repository lock: skipped this one")
		}
	case api.BackupVerifyDrill:
		dctx, cancel := context.WithTimeout(ctx, m.DrillTimeout)
		ev.Steps, ev.Warnings, err = m.drill(dctx, p.ID, *p.Target, p.Repo)
		cancel()
	}
	fin := m.Now().UTC()
	ev.FinishedAt, ev.DurationMs = &fin, fin.Sub(start).Milliseconds()
	m.mu.Lock()
	if err != nil {
		status, ev.Error = "failed", err.Error()
		st.Failures++
	} else {
		st.LastSuccess, st.Failures = start, 0
	}
	m.mu.Unlock()
	ev.Status = status
	m.writeState(func(sf *stateFile) { sf.VerifyInFlight = nil })
	if err != nil {
		m.Log.Warn("backup verification failed", "kind", p.Kind, "service", p.ServiceID(), "id", p.ID, "err", err)
	} else {
		m.Log.Info("backup verification "+status, "kind", p.Kind, "service", p.ServiceID(), "id", p.ID, "ms", ev.DurationMs)
	}
	m.Emit(api.MsgBackupVerifyEvent, ev)
	return ev
}

// reportInterruptedVerify fails a check or drill that was running when the agent stopped.
func (m *Manager) reportInterruptedVerify() {
	fl := m.readVerifyInFlight()
	if fl == nil {
		return
	}
	now := m.Now().UTC()
	what := "check"
	if fl.Kind == api.BackupVerifyDrill {
		what = "restore drill"
	}
	m.Emit(api.MsgBackupVerifyEvent, api.BackupVerifyEvent{ID: fl.ID, Kind: fl.Kind, ServiceID: fl.ServiceID, Trigger: fl.Trigger, Status: "failed",
		Error: "interrupted: the agent stopped during the " + what, StartedAt: fl.StartedAt, FinishedAt: &now, DurationMs: now.Sub(fl.StartedAt).Milliseconds()})
	m.writeState(func(sf *stateFile) { sf.VerifyInFlight = nil })
}

// requireRepo opens an existing repository. Unlike ensureRepo it never creates one: a check
// or drill of a repository that does not exist is a failure.
func (m *Manager) requireRepo(ctx context.Context, repo api.BackupRepo) error {
	if repo.Endpoint == "" || repo.Bucket == "" || repo.Password == "" {
		return errors.New("no backup repository configured")
	}
	err := m.restic(ctx, repo, nil, nil, nil, "cat", "config")
	switch code := exitCode(err); {
	case err == nil:
		return nil
	case code == 10:
		return fmt.Errorf("repository %s does not exist (no backup was written yet)", repo.URL())
	case code == 12:
		return fmt.Errorf("wrong repository password for %s", repo.URL())
	default:
		return fmt.Errorf("open repository %s: %w", repo.URL(), err)
	}
}

// ---------- check ----------

// CheckArgs is the repository check command.
func CheckArgs(subset string, lock []string) []string {
	return append([]string{"check", "--read-data-subset=" + subset}, lock...)
}

// lockHeld reports a restic failure to get its lock (exit 11 since restic 0.17).
func lockHeld(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return exitCode(err) == 11 || strings.Contains(msg, "already locked") || strings.Contains(msg, "unable to create lock")
}

func (m *Manager) check(ctx context.Context, repo api.BackupRepo, subset string) (api.BackupVerifyStep, bool, error) {
	step := api.BackupVerifyStep{Part: "check", Status: "failed"}
	if err := m.requireRepo(ctx, repo); err != nil {
		step.Error = err.Error()
		return step, false, err
	}
	var out bytes.Buffer
	err := m.restic(ctx, repo, nil, &out, nil, CheckArgs(subset, m.retryLock())...)
	step.Detail = lastLines(out.String(), 2, 300)
	if lockHeld(err) {
		step.Status, step.Error = "skipped", err.Error()
		return step, true, nil
	}
	if err != nil {
		err = fmt.Errorf("restic check (%s of the data): %w", subset, err)
		step.Error = err.Error()
		return step, false, err
	}
	step.Status = "done"
	return step, false, nil
}

// ---------- drill ----------

// LatestByPart returns the newest snapshot of each part ("part=" tag; untagged = volumes).
func LatestByPart(snaps []api.ResticSnapshot) map[string]api.ResticSnapshot {
	out := map[string]api.ResticSnapshot{}
	for _, sn := range snaps {
		part := "volumes"
		for _, t := range sn.Tags {
			if p, ok := strings.CutPrefix(t, "part="); ok {
				part = p
			}
		}
		if cur, ok := out[part]; !ok || sn.Time.After(cur.Time) {
			out[part] = sn
		}
	}
	return out
}

// DrillParts says which parts a drill tests: those the policy backs up that have a snapshot,
// else whatever the repository holds for the service.
func DrillParts(t api.BackupTarget, latest map[string]api.ResticSnapshot) []string {
	var want []string
	if DBKinds[t.Kind] {
		want = append(want, "dump")
	}
	if !DBKinds[t.Kind] || t.IncludeVolumes {
		want = append(want, "volumes")
	}
	var out []string
	for _, p := range want {
		if _, ok := latest[p]; ok {
			out = append(out, p)
		}
	}
	if len(out) > 0 {
		return out
	}
	for p := range latest {
		if p == "dump" && !DBKinds[t.Kind] {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out) // dump before volumes
	return out
}

func (m *Manager) drill(ctx context.Context, id string, t api.BackupTarget, repo api.BackupRepo) ([]api.BackupVerifyStep, []string, error) {
	if err := m.requireRepo(ctx, repo); err != nil {
		return nil, nil, err
	}
	snaps, err := m.snapshots(ctx, repo, t.ServiceID, m.retryLock()...)
	if err != nil {
		return nil, nil, fmt.Errorf("list snapshots: %w", err)
	}
	latest := LatestByPart(snaps)
	parts := DrillParts(t, latest)
	if len(parts) == 0 {
		return nil, nil, fmt.Errorf("no snapshot of %s in the repository yet", t.ServiceID)
	}
	defer m.cleanupDrill(id)
	var steps []api.BackupVerifyStep
	var warns, failed []string
	now := m.Now()
	for _, part := range parts {
		sn := latest[part]
		if age := now.Sub(sn.Time); age > staleSnapshotAge {
			warns = append(warns, fmt.Sprintf("newest %s snapshot is %d days old", part, int(age.Hours()/24)))
		}
		var st api.BackupVerifyStep
		var w []string
		switch part {
		case "dump":
			st, w = m.drillDump(ctx, id, t, sn, repo)
		default:
			st, w = m.drillVolumes(ctx, id, t, sn, repo)
		}
		warns = append(warns, w...)
		steps = append(steps, st)
		if st.Status != "done" {
			failed = append(failed, st.Part+": "+st.Error)
		}
		if ctx.Err() != nil {
			break
		}
	}
	if len(failed) > 0 {
		return steps, warns, errors.New(strings.Join(failed, "; "))
	}
	return steps, warns, nil
}

// FreeBytes returns the space available to the agent at path (statfs).
func FreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// ensureSpace fails when the drill would not fit on the agent's disk.
func (m *Manager) ensureSpace(need int64) error {
	if need <= 0 {
		return nil
	}
	free := m.FreeSpace
	if free == nil {
		free = FreeBytes
	}
	os.MkdirAll(m.drillRoot(), 0o700)
	have, err := free(m.drillRoot())
	if err != nil {
		return nil // unknown: let restic fail if it does not fit
	}
	if have < uint64(need) {
		return fmt.Errorf("not enough free disk for the drill: needs about %s, %s free", humanBytes(uint64(need)), humanBytes(have))
	}
	return nil
}

func humanBytes(n uint64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	d, e := float64(n), 0
	for d >= u && e < 4 {
		d /= u
		e++
	}
	return fmt.Sprintf("%.1f %ciB", d, "KMGT"[e-1])
}

func snapshotBytes(sn api.ResticSnapshot) int64 {
	if sn.Summary != nil {
		return sn.Summary.TotalBytesProcessed
	}
	return 0
}

// countFiles returns the regular files and bytes under dir.
func countFiles(dir string) (files, size int64, err error) {
	err = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			files++
			size += fi.Size()
		}
		return nil
	})
	return files, size, err
}

// DrillRestoreArgs restores a volume snapshot into the drill dir (mounted at /drill).
func DrillRestoreArgs(sn api.ResticSnapshot, serviceID string, lock []string) []string {
	src := "/backup/" + serviceID
	if len(sn.Paths) > 0 && sn.Paths[0] != "" {
		src = sn.Paths[0]
	}
	return append([]string{"restore", sn.ID + ":" + src, "--target", "/drill", "--verify"}, lock...)
}

func (m *Manager) drillVolumes(ctx context.Context, id string, t api.BackupTarget, sn api.ResticSnapshot, repo api.BackupRepo) (api.BackupVerifyStep, []string) {
	st := api.BackupVerifyStep{Part: "volumes", SnapshotID: sn.ID, Status: "failed"}
	fail := func(err error) (api.BackupVerifyStep, []string) { st.Error = err.Error(); return st, nil }
	size := snapshotBytes(sn)
	if err := m.ensureSpace(size + size/10 + 256<<20); err != nil {
		return fail(err)
	}
	dir := filepath.Join(m.drillDir(id), "files")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(err)
	}
	err := m.restic(ctx, repo, nil, nil, []string{dir + ":/drill"}, DrillRestoreArgs(sn, t.ServiceID, m.retryLock())...)
	files, n, cerr := countFiles(dir)
	st.Files, st.Bytes = files, n
	st.Detail = fmt.Sprintf("files=%d bytes=%d", files, n)
	switch {
	case err != nil:
		return fail(fmt.Errorf("restore: %w", err))
	case cerr != nil:
		return fail(fmt.Errorf("read restored files: %w", cerr))
	case files == 0 || n == 0:
		return fail(fmt.Errorf("restored %d files, %d bytes: the snapshot holds no data", files, n))
	}
	st.Status = "done"
	var warns []string
	if sn.Summary != nil && sn.Summary.TotalFilesProcessed > 0 && files != sn.Summary.TotalFilesProcessed {
		warns = append(warns, fmt.Sprintf("volumes: restored %d files, the snapshot recorded %d", files, sn.Summary.TotalFilesProcessed))
	}
	return st, warns
}

// DrillContainerSpec is the temporary database container of a drill: the service's own image,
// entrypoint, command, env and limits, but its own name and labels, no network, no published
// ports, no restart policy, and fresh data dirs under dataRoot in place of the service's
// volumes. Read-only config files stay mounted. It refuses any mount of the live volumes.
func DrillContainerSpec(p agent.Paths, spec api.ResolvedSpec, id, dataRoot string) (agent.ContainerSpec, error) {
	cs, err := agent.BuildContainerSpec(p, spec)
	if err != nil {
		return cs, err
	}
	cs.Name = DrillContainer(id)
	cs.Labels = map[string]string{agent.LabelRole: "backup-drill", LabelDrill: id}
	cs.Networks, cs.NetworkMode = nil, "none"
	cs.Ports, cs.ExposedPorts = nil, nil
	cs.RestartPolicy, cs.Health = "", nil
	live := filepath.Clean(p.VolumesDir(spec.ServiceID))
	under := func(path string) bool {
		path = filepath.Clean(path)
		return path == live || strings.HasPrefix(path, live+string(filepath.Separator))
	}
	var mounts []agent.Mount
	for _, mt := range cs.Mounts {
		switch {
		case under(mt.Source):
			rel, _ := filepath.Rel(live, filepath.Clean(mt.Source))
			mounts = append(mounts, agent.Mount{Source: filepath.Join(dataRoot, rel), Target: mt.Target})
		case mt.ReadOnly:
			mounts = append(mounts, mt) // config files
		}
	}
	cs.Mounts = mounts
	root := filepath.Clean(dataRoot) + string(filepath.Separator)
	for _, mt := range cs.Mounts {
		if under(mt.Source) || (!mt.ReadOnly && !strings.HasPrefix(filepath.Clean(mt.Source)+string(filepath.Separator), root)) {
			return cs, fmt.Errorf("drill container would mount %s", mt.Source)
		}
	}
	return cs, nil
}

// QueryScript runs inside the drill container after the dump was loaded and prints one line
// of key=value counts; it fails when the database cannot be queried.
func QueryScript(kind string) (string, error) {
	switch kind {
	case "postgres":
		return pgEnv + `D=$(psql -U "$U" -d postgres -Atc "select count(*) from pg_database where not datistemplate") || exit 1; T=0; ` +
			`for d in $(psql -U "$U" -d postgres -Atc "select datname from pg_database where not datistemplate"); do ` +
			`n=$(psql -U "$U" -d "$d" -Atc "select count(*) from information_schema.tables where table_schema not in ('pg_catalog','information_schema')") || exit 1; T=$((T+n)); done; ` +
			`echo "databases=$D tables=$T"`, nil
	case "mysql":
		return myEnv + `R=$("$M" -h127.0.0.1 -u"$U" -N -B -e "select count(distinct table_schema), count(*) from information_schema.tables where table_schema not in ('mysql','information_schema','performance_schema','sys')") || exit 1; ` +
			`set -- $R; echo "databases=$1 tables=$2"`, nil
	case "mongo":
		return mongoEnv + `if command -v mongosh >/dev/null; then S=mongosh; else S=mongo; fi; "$S" --quiet --host 127.0.0.1 "$@" --eval '` +
			`var d=db.adminCommand({listDatabases:1}).databases.filter(function(x){return ["admin","local","config"].indexOf(x.name)<0});` +
			`var c=0; d.forEach(function(x){c+=db.getSiblingDB(x.name).getCollectionNames().length});` +
			`print("databases="+d.length+" collections="+c)'`, nil
	case "redis":
		return redisEnv + `K=$(redis-cli -h 127.0.0.1 "$@" dbsize) || exit 1; case "$K" in ''|*[!0-9]*) echo "$K" >&2; exit 1;; esac; echo "keys=$K"`, nil
	}
	return "", fmt.Errorf("no query for kind %q", kind)
}

// ParseCounts reads the last "a=1 b=2" line of a QueryScript output.
func ParseCounts(out string) (map[string]int64, string, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.Contains(l, "=") {
			continue
		}
		res := map[string]int64{}
		for _, f := range strings.Fields(l) {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, l, fmt.Errorf("unexpected query output %q", l)
			}
			res[k] = n
		}
		if len(res) > 0 {
			return res, l, nil
		}
	}
	return nil, "", fmt.Errorf("unexpected query output %q", lastLines(out, 2, 200))
}

func (m *Manager) drillDump(ctx context.Context, id string, t api.BackupTarget, sn api.ResticSnapshot, repo api.BackupRepo) (api.BackupVerifyStep, []string) {
	st := api.BackupVerifyStep{Part: "dump", SnapshotID: sn.ID, Status: "failed"}
	fail := func(err error) (api.BackupVerifyStep, []string) { st.Error = err.Error(); return st, nil }
	spec, err := agent.NewStateStore(m.Paths).Load(t.ServiceID)
	if err != nil {
		return fail(fmt.Errorf("service %s is not deployed on this server (the drill uses its image)", t.ServiceID))
	}
	if _, err := m.RT.ImageInfo(ctx, spec.Image); err != nil {
		return fail(fmt.Errorf("image %s is not on this server: %v", spec.Image, err))
	}
	size := snapshotBytes(sn)
	if err := m.ensureSpace(3*size + 512<<20); err != nil {
		return fail(err)
	}
	dataRoot := filepath.Join(m.drillDir(id), "db")
	cs, err := DrillContainerSpec(m.Paths, spec, id, dataRoot)
	if err != nil {
		return fail(err)
	}
	if t.Kind == "redis" {
		hasData := false
		for _, mt := range cs.Mounts {
			hasData = hasData || mt.Target == "/data"
		}
		if !hasData {
			cs.Mounts = append(cs.Mounts, agent.Mount{Source: filepath.Join(dataRoot, "data"), Target: "/data"})
		}
	}
	for _, mt := range cs.Mounts {
		if !mt.ReadOnly {
			if err := os.MkdirAll(mt.Source, 0o755); err != nil {
				return fail(err)
			}
		}
	}
	name := cs.Name
	if t.Kind == "redis" {
		var dataDir string
		for _, mt := range cs.Mounts {
			if mt.Target == "/data" {
				dataDir = mt.Source
			}
		}
		f, err := os.OpenFile(filepath.Join(dataDir, "dump.rdb"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return fail(err)
		}
		cw := &countWriter{w: f}
		err = m.restic(ctx, repo, nil, cw, nil, append([]string{"dump", sn.ID, DumpFile("redis")}, m.retryLock()...)...)
		f.Close()
		st.Bytes = cw.n.Load()
		if err != nil {
			return fail(fmt.Errorf("read dump from repository: %w", err))
		}
		if st.Bytes < 9 {
			return fail(errors.New("rdb in snapshot is empty"))
		}
	}
	if err := m.RT.CreateContainer(ctx, cs); err != nil {
		return fail(fmt.Errorf("create temporary container: %w", err))
	}
	if err := m.RT.StartContainer(ctx, name); err != nil {
		return fail(fmt.Errorf("start temporary container: %w", err))
	}
	if err := m.waitReady(ctx, name, t.Kind); err != nil {
		return fail(err)
	}
	if t.Kind != "redis" {
		script, err := RestoreScript(t.Kind)
		if err != nil {
			return fail(err)
		}
		n, err := m.loadDump(ctx, repo, name, t.Kind, script, sn.ID)
		st.Bytes = n
		if err != nil {
			return fail(err)
		}
		if n == 0 {
			return fail(errors.New("dump in snapshot is empty"))
		}
	}
	q, err := QueryScript(t.Kind)
	if err != nil {
		return fail(err)
	}
	var out, errb bytes.Buffer
	code, err := m.RT.Exec(ctx, name, []string{"sh", "-c", q}, nil, &out, &errb)
	if err != nil {
		return fail(fmt.Errorf("query: %w", err))
	}
	if code != 0 {
		return fail(fmt.Errorf("query exited %d: %s", code, lastLines(errb.String()+"\n"+out.String(), 4, 1000)))
	}
	counts, line, err := ParseCounts(out.String())
	if err != nil {
		return fail(err)
	}
	st.Detail, st.Status = line, "done"
	var warns []string
	for _, k := range []string{"tables", "collections", "keys"} {
		if n, ok := counts[k]; ok && n == 0 {
			warns = append(warns, "dump: the restored database holds no "+k)
		}
	}
	return st, warns
}

// removeDrillContainer removes a drill container together with the anonymous volumes its image
// declared (e.g. mongo's /data/configdb) when the runtime can, so monthly drills leave nothing.
func (m *Manager) removeDrillContainer(ctx context.Context, name string) error {
	if rv, ok := m.RT.(interface {
		RemoveContainerAndVolumes(ctx context.Context, name string) error
	}); ok {
		return rv.RemoveContainerAndVolumes(ctx, name)
	}
	return m.RT.RemoveContainer(ctx, name)
}

// cleanupDrill removes a drill's temporary container and files.
func (m *Manager) cleanupDrill(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := m.removeDrillContainer(ctx, DrillContainer(id)); err != nil && !errors.Is(err, agent.ErrNotFound) {
		m.Log.Warn("drill cleanup: remove container", "id", id, "err", err)
	}
	if err := os.RemoveAll(m.drillDir(id)); err != nil {
		m.Log.Warn("drill cleanup: remove files", "id", id, "err", err)
	}
}

// cleanupStaleDrills removes what drills interrupted by an agent stop left behind.
func (m *Manager) cleanupStaleDrills(ctx context.Context) {
	if list, err := m.RT.ListContainers(ctx, LabelDrill); err == nil {
		for _, c := range list {
			if err := m.removeDrillContainer(ctx, c.Name); err != nil && !errors.Is(err, agent.ErrNotFound) {
				m.Log.Warn("drill cleanup: remove container", "name", c.Name, "err", err)
			}
		}
	}
	if err := os.RemoveAll(m.drillRoot()); err != nil {
		m.Log.Warn("drill cleanup: remove files", "err", err)
	}
}
