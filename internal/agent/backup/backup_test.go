package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ---------- fake runner ----------

type call struct {
	Args  []string
	Env   []string
	Stdin string
}

// subcmd is the restic subcommand of a docker run argv (first arg after the image).
func (c call) subcmd() string {
	for i, a := range c.Args {
		if a == DefaultImage && i+1 < len(c.Args) {
			return c.Args[i+1]
		}
	}
	return ""
}

func (c call) has(s string) bool {
	for _, a := range c.Args {
		if a == s {
			return true
		}
	}
	return false
}

type fakeRunner struct {
	mu    sync.Mutex
	calls []call
	// fn answers a call: write stdout, return error (use &ExitError{}).
	fn func(c call, stdout io.Writer) error
}

func (f *fakeRunner) Run(ctx context.Context, env []string, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	c := call{Args: append([]string{name}, args...), Env: env}
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		c.Stdin = string(b)
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	fn := f.fn
	f.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(c, stdout)
}

func (f *fakeRunner) byCmd(sub string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.subcmd() == sub {
			out = append(out, c)
		}
	}
	return out
}

func summary(id string, total, added int64) string {
	return fmt.Sprintf(`{"message_type":"status","percent_done":0.5}`+"\n"+`{"message_type":"summary","snapshot_id":"%s","total_bytes_processed":%d,"data_added":%d}`+"\n", id, total, added)
}

// standard restic behaviour: missing repo first, backups succeed, forget removes one.
func resticOK(c call, stdout io.Writer) error {
	switch c.subcmd() {
	case "cat":
		return &ExitError{Code: 10, Stderr: "Fatal: repository does not exist"}
	case "init":
		return nil
	case "backup":
		if c.has("--stdin") {
			io.WriteString(stdout, summary("dumpsnap0001", int64(len(c.Stdin)), 100))
		} else {
			io.WriteString(stdout, summary("volsnap0001", 5000, 1200))
		}
	case "forget":
		io.WriteString(stdout, `[{"tags":["atoll"],"keep":[{"id":"k1"}],"remove":[{"id":"old0001"}]}]`+"\n"+"loaded indexes\n")
	}
	return nil
}

// ---------- harness ----------

const (
	secretKey = "SECRET-s3-key-DO-NOT-LEAK"
	repoPass  = "repo-password-DO-NOT-LEAK"
)

var repo = api.BackupRepo{Endpoint: "https://acct.r2.cloudflarestorage.com", Bucket: "atoll-backups", Prefix: "atoll",
	AccessKey: "AKIDEXAMPLE", SecretKey: secretKey, Password: repoPass}

type env struct {
	m      *Manager
	rt     *agenttest.Fake
	run    *fakeRunner
	paths  agent.Paths
	logs   *bytes.Buffer
	mu     sync.Mutex
	events []api.BackupEvent
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{rt: agenttest.New(), run: &fakeRunner{fn: resticOK}, paths: agent.Paths{Root: t.TempDir()}, logs: &bytes.Buffer{}}
	log := slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	e.m = New(e.rt, e.paths, "srv_node1", func(typ string, p any) {
		if ev, ok := p.(api.BackupEvent); ok && typ == api.MsgBackupEvent {
			e.mu.Lock()
			e.events = append(e.events, ev)
			e.mu.Unlock()
		}
	}, log)
	e.m.Run = e.run
	e.m.ReadyEvery, e.m.Settle, e.m.ReadyTimeout = time.Millisecond, time.Millisecond, 2*time.Second
	return e
}

// deploy creates the service's state + a running container with volume dirs.
func (e *env) deploy(t *testing.T, id, kind string, vols map[string]string) api.ResolvedSpec {
	t.Helper()
	spec := api.ResolvedSpec{ServiceID: id, ProjectID: "prj_demo", Name: kind, Network: "atoll-prj_demo", Image: kind + ":latest"}
	for name, mount := range vols {
		spec.Volumes = append(spec.Volumes, api.Volume{Name: name, MountPath: mount})
		d := e.paths.VolumeDir(id, name)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "data.bin"), []byte("live-data-"+id), 0o644)
	}
	if err := agent.NewStateStore(e.paths).Save(spec); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e.rt.EnsureNetwork(ctx, spec.Network)
	if err := e.rt.CreateContainer(ctx, agent.ContainerSpec{Name: agent.ContainerName(id), Image: spec.Image, Labels: map[string]string{agent.LabelService: id},
		Networks: []agent.NetworkAttach{{Name: spec.Network}}}); err != nil {
		t.Fatal(err)
	}
	e.rt.StartContainer(ctx, agent.ContainerName(id))
	return spec
}

func target(id, kind string, vols ...string) api.BackupTarget {
	return api.BackupTarget{ServiceID: id, ProjectID: "prj_demo", Name: kind, Kind: kind, Volumes: vols, IncludeVolumes: true, Enabled: true,
		Schedule: "0 3 * * *", Timezone: "UTC", Keep: api.BackupKeep{Daily: 7, Weekly: 4, Monthly: 6}}
}

func assertNoSecretsInArgv(t *testing.T, calls []call) {
	t.Helper()
	for _, c := range calls {
		joined := strings.Join(c.Args, " ")
		for _, s := range []string{secretKey, repoPass, "AKIDEXAMPLE"} {
			if strings.Contains(joined, s) {
				t.Fatalf("secret %q in argv: %s", s, joined)
			}
		}
	}
}

// ---------- tests ----------

func TestBackupPostgresDumpAndVolumes(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_pg", "postgres", map[string]string{"data": "/var/lib/postgresql/data"})
	e.rt.ExecFunc = func(name string, cmd []string, stdin io.Reader, stdout io.Writer) int {
		if strings.Contains(cmd[2], "pg_dumpall") {
			io.WriteString(stdout, "-- PostgreSQL database cluster dump\nCREATE TABLE users();\n")
		}
		return 0
	}
	ev := e.m.RunBackup(context.Background(), "bak_test1", "manual", target("svc_pg", "postgres", "data"), repo)
	if ev.Status != "done" {
		t.Fatalf("status %s: %s", ev.Status, ev.Error)
	}
	if len(ev.Snapshots) != 2 || ev.Snapshots[0].Part != "dump" || ev.Snapshots[1].Part != "volumes" {
		t.Fatalf("snapshots %+v", ev.Snapshots)
	}
	if ev.Snapshots[0].ID != "dumpsnap0001" || ev.Snapshots[1].Bytes != 5000 || ev.Added != 1300 {
		t.Fatalf("sizes %+v added=%d", ev.Snapshots, ev.Added)
	}
	if len(ev.Pruned) != 2 || ev.Pruned[0] != "old0001" {
		t.Fatalf("pruned %v", ev.Pruned)
	}
	// repo initialised on first use
	if len(e.run.byCmd("cat")) != 1 || len(e.run.byCmd("init")) != 1 {
		t.Fatalf("init sequence: %d cat, %d init", len(e.run.byCmd("cat")), len(e.run.byCmd("init")))
	}
	backups := e.run.byCmd("backup")
	if len(backups) != 2 {
		t.Fatalf("backup calls %d", len(backups))
	}
	d := backups[0]
	if !strings.Contains(d.Stdin, "CREATE TABLE users") {
		t.Fatalf("dump not streamed into restic: %q", d.Stdin)
	}
	want := []string{"docker", "run", "--rm", "--label", "atoll.role=backup", "-i", "-e", "RESTIC_REPOSITORY", "-e", "RESTIC_PASSWORD",
		"-e", "AWS_ACCESS_KEY_ID", "-e", "AWS_SECRET_ACCESS_KEY", "-e", "AWS_DEFAULT_REGION"}
	if got := d.Args[:len(want)]; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("docker args\n got %v\nwant %v", got, want)
	}
	for _, s := range []string{"--stdin-filename", "postgres.sql", "--host", "srv_node1", "svc=svc_pg", "part=dump", "project=prj_demo", "--json"} {
		if !d.has(s) {
			t.Fatalf("dump backup args miss %q: %v", s, d.Args)
		}
	}
	v := backups[1]
	mount := e.paths.VolumesDir("svc_pg") + ":/backup/svc_pg:ro"
	if !v.has(mount) || !v.has("/backup/svc_pg") || !v.has("part=volumes") || v.has("-i") {
		t.Fatalf("volume backup args %v", v.Args)
	}
	// env carries the secrets, argv does not
	envs := strings.Join(d.Env, "\n")
	for _, s := range []string{"RESTIC_REPOSITORY=s3:https://acct.r2.cloudflarestorage.com/atoll-backups/atoll", "RESTIC_PASSWORD=" + repoPass,
		"AWS_SECRET_ACCESS_KEY=" + secretKey, "AWS_DEFAULT_REGION=auto"} {
		if !strings.Contains(envs, s) {
			t.Fatalf("env misses %q", s)
		}
	}
	assertNoSecretsInArgv(t, e.run.calls)
	for _, s := range []string{secretKey, repoPass} {
		if strings.Contains(e.logs.String(), s) {
			t.Fatalf("secret logged:\n%s", e.logs.String())
		}
	}
	if !strings.Contains(e.logs.String(), "credentials=redacted") {
		t.Fatalf("expected redacted repo in logs:\n%s", e.logs.String())
	}
	// events: running then done
	if len(e.events) != 2 || e.events[0].Status != "running" || e.events[1].Status != "done" {
		t.Fatalf("events %+v", e.events)
	}
}

func TestBackupStoppedDatabaseUsesVolumes(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_my", "mysql", map[string]string{"data": "/var/lib/mysql"})
	e.rt.SetState(agent.ContainerName("svc_my"), "exited")
	tg := target("svc_my", "mysql", "data")
	tg.IncludeVolumes = false
	ev := e.m.RunBackup(context.Background(), "bak_x", "schedule", tg, repo)
	if ev.Status != "done" || len(ev.Snapshots) != 1 || ev.Snapshots[0].Part != "volumes" || len(ev.Warnings) == 0 {
		t.Fatalf("event %+v", ev)
	}
}

func TestBackupFailedDumpDropsPartialSnapshot(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_pg", "postgres", map[string]string{"data": "/var/lib/postgresql/data"})
	e.rt.ExecFunc = func(name string, cmd []string, stdin io.Reader, stdout io.Writer) int {
		io.WriteString(stdout, "-- partial")
		return 1
	}
	ev := e.m.RunBackup(context.Background(), "bak_bad", "manual", target("svc_pg", "postgres", "data"), repo)
	if ev.Status != "failed" || !strings.Contains(ev.Error, "dump") {
		t.Fatalf("event %+v", ev)
	}
	f := e.run.byCmd("forget")
	if len(f) != 1 || !f[0].has("dumpsnap0001") {
		t.Fatalf("partial snapshot not forgotten: %+v", f)
	}
}

func TestBackupAppWithoutVolumesFails(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_app", "image", nil)
	ev := e.m.RunBackup(context.Background(), "bak_x", "manual", target("svc_app", "image"), repo)
	if ev.Status != "failed" || !strings.Contains(ev.Error, "nothing to back up") {
		t.Fatalf("event %+v", ev)
	}
}

func TestWrongPassword(t *testing.T) {
	e := newEnv(t)
	e.run.fn = func(c call, _ io.Writer) error {
		return &ExitError{Code: 12, Stderr: "Fatal: wrong password or no key found"}
	}
	_, err := e.m.Test(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "wrong repository password") || strings.Contains(err.Error(), repoPass) {
		t.Fatalf("err %v", err)
	}
	e.run.fn = resticOK
	msg, err := e.m.Test(context.Background(), repo)
	if err != nil || !strings.Contains(msg, "created") {
		t.Fatalf("%q %v", msg, err)
	}
}

func TestForgetArgsRetention(t *testing.T) {
	tg := target("svc_a", "postgres")
	got := strings.Join(ForgetArgs(tg, "dump"), " ")
	want := "forget --json --retry-lock 5m --tag svc=svc_a,part=dump --group-by tags --keep-daily 7 --keep-weekly 4 --keep-monthly 6 --prune"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	tg.Keep = api.BackupKeep{Weekly: 2}
	if got := strings.Join(ForgetArgs(tg, "volumes"), " "); strings.Contains(got, "keep-daily") || !strings.Contains(got, "--keep-weekly 2") {
		t.Fatalf("zero keeps must be omitted: %s", got)
	}
	tg.Keep = api.BackupKeep{}
	if got := strings.Join(ForgetArgs(tg, "volumes"), " "); !strings.Contains(got, "--keep-last 1") {
		t.Fatalf("empty retention must keep one: %s", got)
	}
	if ids := ParseForget([]byte("noise\n[{\"remove\":[{\"id\":\"a\"},{\"id\":\"b\"}]},{\"remove\":null}]\nprune output\n")); len(ids) != 2 {
		t.Fatalf("parse forget %v", ids)
	}
}

func TestRestoreVolumesFromOtherService(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_new", "image", map[string]string{"uploads": "/app/uploads"})
	var restoredInto string
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "restore" {
			restoredInto = c.Args[len(c.Args)-1]
			os.WriteFile(filepath.Join(e.paths.VolumeDir("svc_new", "uploads"), "restored.txt"), []byte("from backup"), 0o644)
		}
		return resticOK(c, w)
	}
	res, err := e.m.Restore(context.Background(), api.BackupRestorePayload{Target: target("svc_new", "image", "uploads"), SourceServiceID: "svc_old",
		Snapshots: []api.BackupSnapshot{{ID: "volsnap0001", Part: "volumes", Bytes: 5000}}, Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	r := e.run.byCmd("restore")
	if len(r) != 1 || !r[0].has("volsnap0001:/backup/svc_old") || restoredInto != "/backup/svc_new" ||
		!r[0].has(e.paths.VolumesDir("svc_new")+":/backup/svc_new") {
		t.Fatalf("restore args %+v", r)
	}
	if res.MovedAside == "" {
		t.Fatal("previous data not moved aside")
	}
	if b, _ := os.ReadFile(filepath.Join(res.MovedAside, "uploads", "data.bin")); string(b) != "live-data-svc_new" {
		t.Fatalf("aside copy: %q", b)
	}
	if _, err := os.Stat(filepath.Join(e.paths.VolumeDir("svc_new", "uploads"), "data.bin")); err == nil {
		t.Fatal("old file still in restored volume")
	}
	c, _ := e.rt.Get(agent.ContainerName("svc_new"))
	if c.Info.State != "running" {
		t.Fatalf("container not restarted: %s", c.Info.State)
	}
	assertNoSecretsInArgv(t, e.run.calls)
}

func TestRestoreFailurePutsDataBack(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_a", "image", map[string]string{"data": "/data"})
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "restore" {
			return &ExitError{Code: 1, Stderr: "Fatal: network down"}
		}
		return resticOK(c, w)
	}
	_, err := e.m.Restore(context.Background(), api.BackupRestorePayload{Target: target("svc_a", "image", "data"),
		Snapshots: []api.BackupSnapshot{{ID: "s1", Part: "volumes"}}, Repo: repo})
	if err == nil || !strings.Contains(err.Error(), "previous data put back") {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.paths.VolumeDir("svc_a", "data"), "data.bin")); string(b) != "live-data-svc_a" {
		t.Fatalf("data not restored after failure: %q", b)
	}
}

func TestRestorePostgresFromDump(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_pg", "postgres", map[string]string{"data": "/var/lib/postgresql/data"})
	probes := 0
	var loaded string
	e.rt.ExecFunc = func(name string, cmd []string, stdin io.Reader, stdout io.Writer) int {
		switch {
		case strings.Contains(cmd[2], "pg_isready"):
			probes++
			if probes < 3 { // initialising
				return 2
			}
			return 0
		case strings.Contains(cmd[2], "psql -U \"$U\" -d postgres -q"):
			b, _ := io.ReadAll(stdin)
			loaded = string(b)
			return 0
		}
		return 0
	}
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "dump" {
			if !c.has("dumpsnap0001") || !c.has("/postgres.sql") {
				return errors.New("bad dump args")
			}
			io.WriteString(w, "CREATE TABLE users();\n")
			return nil
		}
		return resticOK(c, w)
	}
	res, err := e.m.Restore(context.Background(), api.BackupRestorePayload{Target: target("svc_pg", "postgres", "data"),
		Snapshots: []api.BackupSnapshot{{ID: "dumpsnap0001", Part: "dump"}, {ID: "volsnap0001", Part: "volumes"}}, Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	if loaded != "CREATE TABLE users();\n" || res.Restored[0] != "dump" || res.Bytes != int64(len(loaded)) {
		t.Fatalf("loaded %q res %+v", loaded, res)
	}
	if probes < 4 {
		t.Fatalf("readiness not confirmed twice (%d probes)", probes)
	}
	if len(e.run.byCmd("restore")) != 0 {
		t.Fatal("volume snapshot must not be applied for a database with a dump")
	}
}

func TestRestoreRedisWritesRDB(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_rd", "redis", map[string]string{"data": "/data"})
	os.MkdirAll(filepath.Join(e.paths.VolumeDir("svc_rd", "data"), "appendonlydir"), 0o755)
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "dump" {
			io.WriteString(w, "REDIS0011-fake-rdb-payload")
			return nil
		}
		return resticOK(c, w)
	}
	_, err := e.m.Restore(context.Background(), api.BackupRestorePayload{Target: target("svc_rd", "redis", "data"),
		Snapshots: []api.BackupSnapshot{{ID: "d1", Part: "dump"}}, Repo: repo})
	if err != nil {
		t.Fatal(err)
	}
	dir := e.paths.VolumeDir("svc_rd", "data")
	if b, _ := os.ReadFile(filepath.Join(dir, "dump.rdb")); string(b) != "REDIS0011-fake-rdb-payload" {
		t.Fatalf("rdb %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "appendonlydir")); err == nil {
		t.Fatal("old AOF must not survive (it would win over the restored rdb)")
	}
}

func TestScheduleDueAndCatchup(t *testing.T) {
	tg := target("svc_a", "postgres")
	day := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, time.UTC) }
	// last attempt yesterday 03:00, now 03:01 -> due
	if !Due(tg, svcState{LastAttempt: day(29, 3, 0), LastSuccess: day(29, 3, 0)}, day(30, 3, 1), 15*time.Minute, 3) {
		t.Fatal("expected due")
	}
	// already ran this slot
	if Due(tg, svcState{LastAttempt: day(30, 3, 0), LastSuccess: day(30, 3, 0)}, day(30, 9, 0), 15*time.Minute, 3) {
		t.Fatal("not due after run")
	}
	// agent was down for 3 days: one catch-up
	if !Due(tg, svcState{LastAttempt: day(26, 3, 0)}, day(30, 12, 0), 15*time.Minute, 3) {
		t.Fatal("missed run must catch up")
	}
	// failed at 03:00: retry after 15m, give up after 3 attempts
	failed := svcState{LastAttempt: day(30, 3, 0), LastSuccess: day(29, 3, 0), Failures: 1}
	if Due(tg, failed, day(30, 3, 10), 15*time.Minute, 3) || !Due(tg, failed, day(30, 3, 16), 15*time.Minute, 3) {
		t.Fatal("retry timing")
	}
	failed.Failures = 3
	if Due(tg, failed, day(30, 9, 0), 15*time.Minute, 3) {
		t.Fatal("gave up after max retries")
	}
	tg.Enabled = false
	if Due(tg, svcState{}, day(30, 9, 0), 15*time.Minute, 3) {
		t.Fatal("disabled")
	}
}

func TestConfigPersistedAndNoImmediateRun(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_a", "image", map[string]string{"data": "/data"})
	now := time.Date(2026, 10, 30, 3, 30, 0, 0, time.UTC)
	e.m.Now = func() time.Time { return now }
	r := repo
	if err := e.m.SetConfig(api.BackupConfigPayload{Repo: &r, Targets: []api.BackupTarget{target("svc_a", "image", "data")}}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(e.m.configPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("config file mode %v %v", fi, err)
	}
	e.m.CheckDue(context.Background())
	if len(e.run.byCmd("backup")) != 0 {
		t.Fatal("a freshly enabled policy must wait for its next slot")
	}
	// a new Manager (agent restart, server offline) reads the config from disk and runs at 03:00 next day
	m2 := New(e.rt, e.paths, "srv_node1", e.m.Emit, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m2.Run = e.run
	m2.Now = func() time.Time { return now.Add(24 * time.Hour) }
	m2.CheckDue(context.Background())
	m2.load()
	m2.CheckDue(context.Background())
	if n := len(e.run.byCmd("backup")); n != 1 {
		t.Fatalf("expected one scheduled backup from persisted config, got %d", n)
	}
	// catch-up request for a slot already covered is skipped
	since := now.Add(24 * time.Hour).Add(-time.Hour)
	if out, _ := m2.Queue(api.BackupRunPayload{Target: target("svc_a", "image", "data"), Repo: repo, Since: &since}); out != "skipped" {
		t.Fatalf("catch-up: %s", out)
	}
}

func TestInterruptedRunReported(t *testing.T) {
	e := newEnv(t)
	e.m.saveState(&inFlight{BackupID: "bak_dead", ServiceID: "svc_a", Trigger: "schedule", StartedAt: time.Now().Add(-time.Minute)}, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.m.Loop(ctx)
	if len(e.events) != 1 || e.events[0].BackupID != "bak_dead" || e.events[0].Status != "failed" {
		t.Fatalf("events %+v", e.events)
	}
	if e.m.readInFlight() != nil {
		t.Fatal("in-flight marker not cleared")
	}
}

func TestHandleRoundTrip(t *testing.T) {
	e := newEnv(t)
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "snapshots" {
			io.WriteString(w, `[{"id":"abc","short_id":"abc","time":"2026-10-30T03:00:00Z","hostname":"srv_node1","tags":["atoll","svc=svc_a","part=dump"],"paths":["/postgres.sql"]}]`)
			return nil
		}
		return resticOK(c, w)
	}
	raw, _ := json.Marshal(api.BackupSnapshotsPayload{Repo: repo, ServiceID: "svc_a"})
	out, err := e.m.Handle(context.Background(), api.MsgBackupSnapshots, raw)
	if err != nil || !strings.Contains(out, `"id":"abc"`) {
		t.Fatalf("%s %v", out, err)
	}
	if !e.m.Handles(api.MsgBackupRun) || e.m.Handles(api.MsgApply) {
		t.Fatal("Handles")
	}
}
