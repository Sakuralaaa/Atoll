package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// verifyEvents captures MsgBackupVerifyEvent frames (and keeps the env's backup capture).
type verifyEvents struct {
	mu  sync.Mutex
	evs []api.BackupVerifyEvent
}

func (v *verifyEvents) all() []api.BackupVerifyEvent {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]api.BackupVerifyEvent(nil), v.evs...)
}

func captureVerify(e *env) *verifyEvents {
	v := &verifyEvents{}
	prev := e.m.Emit
	e.m.Emit = func(typ string, p any) {
		if ev, ok := p.(api.BackupVerifyEvent); ok && typ == api.MsgBackupVerifyEvent {
			v.mu.Lock()
			v.evs = append(v.evs, ev)
			v.mu.Unlock()
			return
		}
		prev(typ, p)
	}
	return v
}

// resticRepoExists: the repository exists; everything else as resticOK.
func resticRepoExists(c call, w io.Writer) error {
	if c.subcmd() == "cat" {
		return nil
	}
	return resticOK(c, w)
}

// mountOf returns the host side of the -v mount whose container side is inside.
func mountOf(c call, inside string) string {
	for i, a := range c.Args {
		if a == "-v" && i+1 < len(c.Args) && strings.HasSuffix(c.Args[i+1], ":"+inside) {
			return strings.TrimSuffix(c.Args[i+1], ":"+inside)
		}
	}
	return ""
}

func checkPayload() api.BackupVerifyPayload {
	return api.BackupVerifyPayload{ID: "bvf_check1", Kind: api.BackupVerifyCheck, Trigger: "manual", Repo: repo}
}

func TestCheckReadsSubsetAndReports(t *testing.T) {
	e := newEnv(t)
	v := captureVerify(e)
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "check" {
			io.WriteString(w, "using temporary cache\nread 5% of data packs\nno errors were found\n")
			return nil
		}
		return resticRepoExists(c, w)
	}
	ev := e.m.RunVerify(context.Background(), checkPayload())
	if ev.Status != "done" || ev.Subset != "5%" || len(ev.Steps) != 1 || !strings.Contains(ev.Steps[0].Detail, "no errors were found") {
		t.Fatalf("event %+v", ev)
	}
	c := e.run.byCmd("check")
	if len(c) != 1 || !c[0].has("--read-data-subset=5%") || !c[0].has("--retry-lock") {
		t.Fatalf("check args %+v", c)
	}
	if len(e.run.byCmd("init")) != 0 {
		t.Fatal("a check must never create the repository")
	}
	evs := v.all()
	if len(evs) != 2 || evs[0].Status != "running" || evs[1].Status != "done" || evs[1].FinishedAt == nil {
		t.Fatalf("events %+v", evs)
	}
	assertNoSecretsInArgv(t, e.run.calls)
	if e.m.readVerifyInFlight() != nil {
		t.Fatal("in-flight marker left behind")
	}
}

func TestCheckOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		cat    error
		check  error
		status string
		errHas string
	}{
		{"errors", nil, &ExitError{Code: 1, Stderr: "error: pack 1234: not referenced\nFatal: repository contains errors"}, "failed", "repository contains errors"},
		{"lock held", nil, &ExitError{Code: 11, Stderr: "Fatal: unable to create lock in backend: repository is already locked exclusively"}, "skipped", ""},
		{"no repository", &ExitError{Code: 10, Stderr: "Fatal: repository does not exist"}, nil, "failed", "does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.run.fn = func(c call, w io.Writer) error {
				switch c.subcmd() {
				case "cat":
					return tc.cat
				case "check":
					return tc.check
				}
				return nil
			}
			p := checkPayload()
			p.Subset = "1/20"
			ev := e.m.RunVerify(context.Background(), p)
			if ev.Status != tc.status || !strings.Contains(ev.Error, tc.errHas) {
				t.Fatalf("event %+v", ev)
			}
			if tc.status == "skipped" && (ev.Error != "" || len(ev.Warnings) == 0) {
				t.Fatalf("skipped must not be an error: %+v", ev)
			}
			if len(e.run.byCmd("init")) != 0 {
				t.Fatal("init")
			}
		})
	}
}

func TestQueueVerifyValidatesAndRefusesDuplicates(t *testing.T) {
	e := newEnv(t)
	block := make(chan struct{})
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "check" {
			<-block
		}
		return resticRepoExists(c, w)
	}
	if _, err := e.m.QueueVerify(api.BackupVerifyPayload{Kind: api.BackupVerifyCheck, Subset: "150%", Repo: repo}); err == nil {
		t.Fatal("invalid subset accepted")
	}
	if _, err := e.m.QueueVerify(api.BackupVerifyPayload{Kind: api.BackupVerifyDrill, Repo: repo}); err == nil {
		t.Fatal("drill without target accepted")
	}
	if out, err := e.m.QueueVerify(checkPayload()); err != nil || out != "queued" {
		t.Fatalf("%s %v", out, err)
	}
	if out, _ := e.m.QueueVerify(checkPayload()); out != "busy" {
		t.Fatalf("second check: %s", out)
	}
	close(block)
	waitFor(t, func() bool { return e.m.claim(checkKey) })
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func snapshotsJSON(svc string, parts ...string) string {
	var items []string
	for _, p := range parts {
		path := "/backup/" + svc
		if p == "dump" {
			path = "/postgres.sql"
		}
		items = append(items, `{"id":"`+p+`snap01","short_id":"`+p+`snap","time":"`+time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)+`","hostname":"srv_node1",`+
			`"tags":["atoll","svc=`+svc+`","part=`+p+`"],"paths":["`+path+`"],"summary":{"total_files_processed":2,"total_bytes_processed":1024}}`)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func drillPayload(tg api.BackupTarget) api.BackupVerifyPayload {
	return api.BackupVerifyPayload{ID: "bvf_drill1", Kind: api.BackupVerifyDrill, Trigger: "manual", Repo: repo, Target: &tg}
}

func TestDrillVolumesRestoresIntoTempDirOnly(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_app", "image", map[string]string{"uploads": "/app/uploads"})
	before := len(e.rt.CallLog())
	var restoredInto string
	e.run.fn = func(c call, w io.Writer) error {
		switch c.subcmd() {
		case "snapshots":
			io.WriteString(w, snapshotsJSON("svc_app", "volumes"))
			return nil
		case "restore":
			restoredInto = mountOf(c, "/drill")
			d := filepath.Join(restoredInto, "uploads")
			os.MkdirAll(d, 0o755)
			os.WriteFile(filepath.Join(d, "a.txt"), []byte("hello"), 0o644)
			os.WriteFile(filepath.Join(d, "b.txt"), []byte("world!"), 0o644)
			return nil
		}
		return resticRepoExists(c, w)
	}
	ev := e.m.RunVerify(context.Background(), drillPayload(target("svc_app", "image", "uploads")))
	if ev.Status != "done" || len(ev.Steps) != 1 {
		t.Fatalf("event %+v", ev)
	}
	st := ev.Steps[0]
	if st.Part != "volumes" || st.Files != 2 || st.Bytes != 11 || st.SnapshotID != "volumessnap01" {
		t.Fatalf("step %+v", st)
	}
	r := e.run.byCmd("restore")
	if len(r) != 1 || !r[0].has("volumessnap01:/backup/svc_app") || !r[0].has("--verify") || !r[0].has("/drill") {
		t.Fatalf("restore args %+v", r)
	}
	if !strings.HasPrefix(restoredInto, e.m.drillRoot()) || strings.HasPrefix(restoredInto, e.paths.VolumesDir("svc_app")) {
		t.Fatalf("restored into %s", restoredInto)
	}
	if _, err := os.Stat(restoredInto); !os.IsNotExist(err) {
		t.Fatalf("drill files not cleaned up: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.paths.VolumeDir("svc_app", "uploads"), "data.bin")); string(b) != "live-data-svc_app" {
		t.Fatalf("live data touched: %q", b)
	}
	for _, c := range e.rt.CallLog()[before:] {
		if strings.Contains(c, agent.ContainerName("svc_app")) {
			t.Fatalf("drill touched the running service: %s", c)
		}
	}
	if c, _ := e.rt.Get(agent.ContainerName("svc_app")); c.Info.State != "running" {
		t.Fatalf("service state %s", c.Info.State)
	}
	assertNoSecretsInArgv(t, e.run.calls)
}

func TestDrillVolumesEmptySnapshotFails(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_app", "image", map[string]string{"uploads": "/app/uploads"})
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "snapshots" {
			io.WriteString(w, snapshotsJSON("svc_app", "volumes"))
			return nil
		}
		return resticRepoExists(c, w)
	}
	ev := e.m.RunVerify(context.Background(), drillPayload(target("svc_app", "image", "uploads")))
	if ev.Status != "failed" || !strings.Contains(ev.Error, "holds no data") {
		t.Fatalf("event %+v", ev)
	}
}

func TestDrillWithoutSnapshotsOrSpaceFails(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_app", "image", map[string]string{"uploads": "/app/uploads"})
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "snapshots" {
			io.WriteString(w, "[]")
			return nil
		}
		return resticRepoExists(c, w)
	}
	if ev := e.m.RunVerify(context.Background(), drillPayload(target("svc_app", "image", "uploads"))); ev.Status != "failed" || !strings.Contains(ev.Error, "no snapshot") {
		t.Fatalf("event %+v", ev)
	}
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "snapshots" {
			io.WriteString(w, snapshotsJSON("svc_app", "volumes"))
			return nil
		}
		return resticRepoExists(c, w)
	}
	e.m.FreeSpace = func(string) (uint64, error) { return 1 << 20, nil }
	if ev := e.m.RunVerify(context.Background(), drillPayload(target("svc_app", "image", "uploads"))); ev.Status != "failed" || !strings.Contains(ev.Error, "not enough free disk") {
		t.Fatalf("event %+v", ev)
	}
	if len(e.run.byCmd("restore")) != 0 {
		t.Fatal("restore attempted without space")
	}
}

func TestDrillPostgresDumpInTemporaryContainer(t *testing.T) {
	e := newEnv(t)
	spec := e.deploy(t, "svc_pg", "postgres", map[string]string{"data": "/var/lib/postgresql/data"})
	e.rt.Images[spec.Image] = agent.ImageInfo{ID: "sha256:pg"}
	before := len(e.rt.CallLog())
	drill := DrillContainer("bvf_drill1")
	var loaded string
	var created agent.ContainerSpec
	e.rt.ExecFunc = func(name string, cmd []string, stdin io.Reader, stdout io.Writer) int {
		if name != drill {
			t.Errorf("exec in %s", name)
			return 1
		}
		if c, ok := e.rt.Get(drill); ok {
			created = c.Spec
		}
		switch {
		case strings.Contains(cmd[2], "pg_isready"):
			return 0
		case strings.Contains(cmd[2], "-q -o /dev/null -f -"):
			b, _ := io.ReadAll(stdin)
			loaded = string(b)
			return 0
		case strings.Contains(cmd[2], "information_schema.tables"):
			io.WriteString(stdout, "databases=2 tables=5\n")
			return 0
		}
		return 1
	}
	e.run.fn = func(c call, w io.Writer) error {
		switch c.subcmd() {
		case "snapshots":
			io.WriteString(w, snapshotsJSON("svc_pg", "dump"))
			return nil
		case "dump":
			if !c.has("dumpsnap01") || !c.has("/postgres.sql") {
				t.Errorf("dump args %v", c.Args)
			}
			io.WriteString(w, "CREATE TABLE users();\n")
			return nil
		}
		return resticRepoExists(c, w)
	}
	tg := target("svc_pg", "postgres", "data")
	tg.IncludeVolumes = false
	ev := e.m.RunVerify(context.Background(), drillPayload(tg))
	if ev.Status != "done" || len(ev.Steps) != 1 || ev.Steps[0].Detail != "databases=2 tables=5" || ev.Steps[0].Bytes != int64(len(loaded)) {
		t.Fatalf("event %+v", ev)
	}
	if loaded != "CREATE TABLE users();\n" {
		t.Fatalf("loaded %q", loaded)
	}
	// the temporary container: same image, no network, fresh data dir, gone afterwards
	if created.Image != spec.Image || created.NetworkMode != "none" || len(created.Networks) != 0 || len(created.Ports) != 0 || created.RestartPolicy != "" {
		t.Fatalf("drill container %+v", created)
	}
	if created.Labels[LabelDrill] != "bvf_drill1" || created.Labels[agent.LabelService] != "" {
		t.Fatalf("labels %v", created.Labels)
	}
	if len(created.Mounts) != 1 || created.Mounts[0].Target != "/var/lib/postgresql/data" || !strings.HasPrefix(created.Mounts[0].Source, e.m.drillRoot()) {
		t.Fatalf("mounts %+v", created.Mounts)
	}
	if _, ok := e.rt.Get(drill); ok {
		t.Fatal("temporary container not removed")
	}
	if _, err := os.Stat(e.m.drillDir("bvf_drill1")); !os.IsNotExist(err) {
		t.Fatal("drill dir not removed")
	}
	for _, c := range e.rt.CallLog()[before:] {
		if strings.Contains(c, agent.ContainerName("svc_pg")) {
			t.Fatalf("drill touched the running database: %s", c)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(e.paths.VolumeDir("svc_pg", "data"), "data.bin")); string(b) != "live-data-svc_pg" {
		t.Fatalf("live data touched: %q", b)
	}
}

func TestDrillDumpQueryFailureStillCleansUp(t *testing.T) {
	e := newEnv(t)
	spec := e.deploy(t, "svc_my", "mysql", map[string]string{"data": "/var/lib/mysql"})
	e.rt.Images[spec.Image] = agent.ImageInfo{ID: "sha256:my"}
	e.rt.ExecFunc = func(name string, cmd []string, stdin io.Reader, stdout io.Writer) int {
		if stdin != nil {
			io.Copy(io.Discard, stdin)
		}
		if strings.Contains(cmd[2], "information_schema.tables") {
			return 1
		}
		return 0
	}
	e.run.fn = func(c call, w io.Writer) error {
		switch c.subcmd() {
		case "snapshots":
			io.WriteString(w, snapshotsJSON("svc_my", "dump"))
			return nil
		case "dump":
			io.WriteString(w, "CREATE DATABASE app;\n")
			return nil
		}
		return resticRepoExists(c, w)
	}
	ev := e.m.RunVerify(context.Background(), drillPayload(target("svc_my", "mysql", "data")))
	if ev.Status != "failed" || !strings.Contains(ev.Error, "dump: query exited 1") {
		t.Fatalf("event %+v", ev)
	}
	if _, ok := e.rt.Get(DrillContainer("bvf_drill1")); ok {
		t.Fatal("temporary container left behind")
	}
}

func TestDrillDumpNeedsLocalImage(t *testing.T) {
	e := newEnv(t)
	e.deploy(t, "svc_pg", "postgres", map[string]string{"data": "/var/lib/postgresql/data"})
	e.run.fn = func(c call, w io.Writer) error {
		if c.subcmd() == "snapshots" {
			io.WriteString(w, snapshotsJSON("svc_pg", "dump"))
			return nil
		}
		return resticRepoExists(c, w)
	}
	ev := e.m.RunVerify(context.Background(), drillPayload(target("svc_pg", "postgres", "data")))
	if ev.Status != "failed" || !strings.Contains(ev.Error, "is not on this server") {
		t.Fatalf("event %+v", ev)
	}
}

func TestDrillContainerSpecNeverMountsLiveData(t *testing.T) {
	p := agent.Paths{Root: "/var/lib/atoll"}
	spec := api.ResolvedSpec{ServiceID: "svc_pg", ProjectID: "prj_1", Name: "pg", Network: "atoll-prj_1", Image: "postgres:16",
		Command: []string{"docker-entrypoint.sh"}, Args: []string{"postgres", "-c", "config_file=/etc/pg.conf"},
		Env:     map[string]string{"POSTGRES_PASSWORD": "pw"},
		Ports:   []api.Port{{Name: "db", ContainerPort: 5432, Protocol: "tcp", PublicPort: 15432}},
		Volumes: []api.Volume{{Name: "data", MountPath: "/var/lib/postgresql/data"}},
		Configs: []api.ConfigFile{{Path: "/etc/pg.conf", Content: "x"}}, MemLimitMB: 512}
	cs, err := DrillContainerSpec(p, spec, "bvf_x", "/var/lib/atoll/backup/drill/bvf_x/db")
	if err != nil {
		t.Fatal(err)
	}
	if cs.Name != "atoll-drill-bvf_x" || cs.Image != "postgres:16" || cs.NetworkMode != "none" || len(cs.Networks) != 0 || len(cs.Ports) != 0 || len(cs.ExposedPorts) != 0 {
		t.Fatalf("spec %+v", cs)
	}
	if strings.Join(cs.Cmd, " ") != "postgres -c config_file=/etc/pg.conf" || cs.MemoryBytes != 512<<20 || strings.Join(cs.Env, ",") != "POSTGRES_PASSWORD=pw" {
		t.Fatalf("not the service's own command/env/limits: %+v", cs)
	}
	var data, conf bool
	for _, m := range cs.Mounts {
		if strings.HasPrefix(m.Source, p.VolumesDir("svc_pg")) {
			t.Fatalf("live volume mounted: %+v", m)
		}
		data = data || (m.Target == "/var/lib/postgresql/data" && m.Source == "/var/lib/atoll/backup/drill/bvf_x/db/data" && !m.ReadOnly)
		conf = conf || (m.Target == "/etc/pg.conf" && m.ReadOnly)
	}
	if !data || !conf {
		t.Fatalf("mounts %+v", cs.Mounts)
	}
	if _, ok := cs.Labels[agent.LabelService]; ok {
		t.Fatal("drill container must not look like a service container")
	}
}

func TestScheduledCheckAndDrills(t *testing.T) {
	e := newEnv(t)
	v := captureVerify(e)
	e.deploy(t, "svc_app", "image", map[string]string{"uploads": "/app/uploads"})
	e.run.fn = func(c call, w io.Writer) error {
		switch c.subcmd() {
		case "snapshots":
			io.WriteString(w, snapshotsJSON("svc_app", "volumes"))
			return nil
		case "restore":
			d := mountOf(c, "/drill")
			os.MkdirAll(d, 0o755)
			os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o644)
			return nil
		case "backup":
			return resticOK(c, w)
		}
		return resticRepoExists(c, w)
	}
	now := time.Date(2030, 6, 1, 1, 0, 0, 0, time.UTC) // a Saturday
	e.m.Now = func() time.Time { return now }
	r := repo
	tg := target("svc_app", "image", "uploads")
	tg.Schedule = "0 3 * * *"
	cfg := api.BackupConfigPayload{Repo: &r, Targets: []api.BackupTarget{tg},
		Verify: &api.BackupVerifyConfig{RunCheck: true, CheckSchedule: "30 4 * * 0", CheckSubset: "2%", DrillSchedule: "30 5 2 * *", Timezone: "UTC"}}
	if err := e.m.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e.m.CheckDue(context.Background())
	if len(v.all()) != 0 {
		t.Fatal("nothing is due right after the config arrived")
	}
	now = time.Date(2030, 6, 2, 4, 31, 0, 0, time.UTC) // Sunday 04:31 (the 2nd: drill at 05:30)
	e.m.CheckDue(context.Background())
	evs := v.all()
	if len(evs) != 2 || evs[1].Kind != api.BackupVerifyCheck || evs[1].Trigger != "schedule" || evs[1].Subset != "2%" || evs[1].Status != "done" {
		t.Fatalf("events %+v", evs)
	}
	now = time.Date(2030, 6, 2, 5, 31, 0, 0, time.UTC)
	e.m.CheckDue(context.Background())
	evs = v.all()
	if len(evs) != 4 || evs[3].Kind != api.BackupVerifyDrill || evs[3].ServiceID != "svc_app" || evs[3].Status != "done" {
		t.Fatalf("events %+v", evs)
	}
	e.m.CheckDue(context.Background()) // same slots: nothing more
	if len(v.all()) != 4 {
		t.Fatalf("ran twice: %+v", v.all())
	}
	// a node that is not the check node never runs the check
	cfg.Verify.RunCheck = false
	e.m.SetConfig(cfg)
	now = time.Date(2030, 6, 9, 4, 31, 0, 0, time.UTC)
	e.m.CheckDue(context.Background())
	for _, ev := range v.all()[4:] {
		if ev.Kind == api.BackupVerifyCheck {
			t.Fatalf("check ran on a node without run_check: %+v", ev)
		}
	}
	// state survives a restart (no immediate re-run from a fresh Manager)
	m2 := New(e.rt, e.paths, "srv_node1", e.m.Emit, e.m.Log)
	m2.Run, m2.Now = e.run, e.m.Now
	m2.CheckDue(context.Background())
	if n := len(e.run.byCmd("restore")); n != 1 {
		t.Fatalf("drills: %d", n)
	}
}

func TestInterruptedVerifyReportedAndLeftoversRemoved(t *testing.T) {
	e := newEnv(t)
	v := captureVerify(e)
	e.m.writeState(func(sf *stateFile) {
		sf.VerifyInFlight = &verifyInFlight{ID: "bvf_dead", Kind: api.BackupVerifyDrill, ServiceID: "svc_a", Trigger: "schedule", StartedAt: time.Now().Add(-time.Minute)}
	})
	ctx := context.Background()
	e.rt.CreateContainer(ctx, agent.ContainerSpec{Name: DrillContainer("bvf_dead"), Image: "postgres:16", Labels: map[string]string{LabelDrill: "bvf_dead"}})
	e.rt.StartContainer(ctx, DrillContainer("bvf_dead"))
	os.MkdirAll(filepath.Join(e.m.drillDir("bvf_dead"), "db", "data"), 0o755)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	e.m.Loop(cctx)
	evs := v.all()
	if len(evs) != 1 || evs[0].ID != "bvf_dead" || evs[0].Status != "failed" || !strings.Contains(evs[0].Error, "interrupted") {
		t.Fatalf("events %+v", evs)
	}
	if e.m.readVerifyInFlight() != nil {
		t.Fatal("marker kept")
	}
	if _, ok := e.rt.Get(DrillContainer("bvf_dead")); ok {
		t.Fatal("leftover container")
	}
	if _, err := os.Stat(e.m.drillRoot()); !os.IsNotExist(err) {
		t.Fatal("leftover files")
	}
}

func TestDrillPartsAndParsers(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2030, 1, 1, h, 0, 0, 0, time.UTC) }
	latest := LatestByPart([]api.ResticSnapshot{
		{ID: "d1", Time: at(1), Tags: []string{"part=dump"}}, {ID: "d2", Time: at(3), Tags: []string{"part=dump"}},
		{ID: "v1", Time: at(2), Tags: []string{"part=volumes"}}, {ID: "x", Time: at(0)},
	})
	if latest["dump"].ID != "d2" || latest["volumes"].ID != "v1" {
		t.Fatalf("latest %+v", latest)
	}
	pg := target("svc", "postgres")
	pg.IncludeVolumes = false
	if got := strings.Join(DrillParts(pg, latest), ","); got != "dump" {
		t.Fatalf("pg parts %s", got)
	}
	pg.IncludeVolumes = true
	if got := strings.Join(DrillParts(pg, latest), ","); got != "dump,volumes" {
		t.Fatalf("pg+vol parts %s", got)
	}
	// a stopped database was backed up by its volumes only
	if got := strings.Join(DrillParts(target("svc", "mysql"), map[string]api.ResticSnapshot{"volumes": {ID: "v"}}), ","); got != "volumes" {
		t.Fatalf("fallback parts %s", got)
	}
	if got := DrillParts(target("svc", "image"), map[string]api.ResticSnapshot{"dump": {ID: "d"}}); len(got) != 0 {
		t.Fatalf("app with only a dump %v", got)
	}
	for s, ok := range map[string]bool{"5%": true, "2.5%": true, "100%": true, "0%": false, "101%": false, "1/20": true, "3/2": false,
		"500M": true, "2G": true, "": false, "5": false, "abc": false, "5%;rm": false} {
		if api.ValidCheckSubset(s) != ok {
			t.Errorf("ValidCheckSubset(%q) != %v", s, ok)
		}
	}
	if c, line, err := ParseCounts("noise\ndatabases=2 tables=0\n"); err != nil || c["tables"] != 0 || c["databases"] != 2 || line != "databases=2 tables=0" {
		t.Fatalf("%v %q %v", c, line, err)
	}
	if _, _, err := ParseCounts("ERROR: relation does not exist"); err == nil {
		t.Fatal("garbage parsed")
	}
	for _, k := range []string{"postgres", "mysql", "mongo", "redis"} {
		if _, err := QueryScript(k); err != nil {
			t.Fatal(k, err)
		}
	}
	if !strings.Contains(strings.Join(CheckArgs("5%", []string{"--retry-lock", "30m0s"}), " "), "check --read-data-subset=5% --retry-lock 30m0s") {
		t.Fatal("check args")
	}
}
