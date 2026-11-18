package backup

import (
	"context"
	"encoding/json"
	"fmt"
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

// pgFake answers the scripts an upgrade runs in its temporary containers.
type pgFake struct {
	mu        sync.Mutex
	before    string // counts printed by the old container
	after     string // counts printed by the new container
	dataDir   string
	loaded    string
	dumpedBy  string
	loadedBy  string
	failStart bool
}

func (p *pgFake) exec(name string, cmd []string, stdin io.Reader, stdout io.Writer) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := cmd[len(cmd)-1]
	switch {
	case strings.Contains(s, "pg_isready"):
		if p.failStart && strings.Contains(name, "-new-") {
			return 2
		}
		return 0
	case strings.Contains(s, "information_schema.tables"):
		if strings.Contains(name, "-old-") {
			fmt.Fprintln(stdout, p.before)
		} else {
			fmt.Fprintln(stdout, p.after)
		}
	case strings.Contains(s, "pg_dumpall"):
		p.dumpedBy = name
		io.WriteString(stdout, "CREATE TABLE users();\n")
	case strings.Contains(s, "-q -o /dev/null -f -"):
		b, _ := io.ReadAll(stdin)
		p.loaded, p.loadedBy = string(b), name
	case strings.Contains(s, "show data_directory"):
		fmt.Fprintln(stdout, p.dataDir)
	}
	return 0
}

func upgradeEnv(t *testing.T) (*env, *pgFake) {
	e := newEnv(t)
	e.m.ReadyTimeout = 50 * time.Millisecond
	e.deploy(t, "svc_pg", "postgres", map[string]string{"data": "/var/lib/postgresql/data"})
	pg := &pgFake{before: "databases=2 tables=5", after: "databases=2 tables=5", dataDir: "/var/lib/postgresql/data/pgdata"}
	e.rt.ExecFunc = pg.exec
	return e, pg
}

func liveFile(e *env) (string, error) {
	b, err := os.ReadFile(filepath.Join(e.paths.VolumeDir("svc_pg", "data"), "data.bin"))
	return string(b), err
}

func TestDBUpgradeConvertsAndKeepsOldData(t *testing.T) {
	e, pg := upgradeEnv(t)
	res, err := e.m.Upgrade(context.Background(), api.DBUpgradePayload{ServiceID: "svc_pg", Kind: "postgres", Image: "postgres:19-alpine"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Before != "databases=2 tables=5" || res.After != res.Before || res.DumpBytes == 0 || res.DataDir != "/var/lib/postgresql/data/pgdata" {
		t.Fatalf("result %+v", res)
	}
	if pg.dumpedBy != UpgradeContainer("svc_pg", "old") || pg.loadedBy != UpgradeContainer("svc_pg", "new") || pg.loaded != "CREATE TABLE users();\n" {
		t.Fatalf("dumped by %s, loaded %q by %s", pg.dumpedBy, pg.loaded, pg.loadedBy)
	}
	// the live dir is the new version's (fresh) data; the old data is kept aside
	if _, err := liveFile(e); !os.IsNotExist(err) {
		t.Fatalf("live dir still holds the old data (%v)", err)
	}
	if b, err := os.ReadFile(filepath.Join(e.m.upgradeOld("svc_pg"), "data", "data.bin")); err != nil || string(b) != "live-data-svc_pg" {
		t.Fatalf("old data not kept: %q %v", b, err)
	}
	if _, err := os.Stat(e.m.upgradeMarker("svc_pg")); !os.IsNotExist(err) {
		t.Fatal("marker left behind")
	}
	for _, w := range []string{"old", "new"} {
		if _, ok := e.rt.Get(UpgradeContainer("svc_pg", w)); ok {
			t.Fatalf("temporary %s container left behind", w)
		}
	}
	if c, _ := e.rt.Get(agent.ContainerName("svc_pg")); c.Info.State == "running" {
		t.Fatal("service restarted on the old image; the server deploys the new one")
	}
	pulled := false
	for _, c := range e.rt.CallLog() {
		pulled = pulled || strings.HasPrefix(c, "pull postgres:19-alpine")
	}
	if !pulled {
		t.Fatal("new image not pulled")
	}

	// a second upgrade waits for the kept data to be removed
	if _, err := e.m.Upgrade(context.Background(), api.DBUpgradePayload{ServiceID: "svc_pg", Kind: "postgres", Image: "postgres:20"}); err == nil || !strings.Contains(err.Error(), "previous upgrade") {
		t.Fatalf("second upgrade: %v", err)
	}

	// rollback: the old data comes back, what the new version wrote is discarded
	os.WriteFile(filepath.Join(e.paths.VolumeDir("svc_pg", "data"), "new.bin"), []byte("x"), 0o644)
	if err := e.m.UpgradeRollback(context.Background(), "svc_pg"); err != nil {
		t.Fatal(err)
	}
	if b, err := liveFile(e); err != nil || b != "live-data-svc_pg" {
		t.Fatalf("rollback: %q %v", b, err)
	}
	if _, err := os.Stat(e.m.upgradeDir("svc_pg")); !os.IsNotExist(err) {
		t.Fatal("upgrade dir left after rollback")
	}
	if err := e.m.UpgradeRollback(context.Background(), "svc_pg"); err == nil {
		t.Fatal("second rollback must fail: nothing is kept")
	}
}

func TestDBUpgradeFailuresPutOldDataBack(t *testing.T) {
	for name, edit := range map[string]func(*pgFake){
		"counts differ":       func(p *pgFake) { p.after = "databases=2 tables=4" },
		"data off the volume": func(p *pgFake) { p.dataDir = "/var/lib/postgresql/19/docker" },
		"new version broken":  func(p *pgFake) { p.failStart = true },
	} {
		t.Run(name, func(t *testing.T) {
			e, pg := upgradeEnv(t)
			edit(pg)
			_, err := e.m.Upgrade(context.Background(), api.DBUpgradePayload{ServiceID: "svc_pg", Kind: "postgres", Image: "postgres:19"})
			if err == nil || !strings.Contains(err.Error(), "nothing changed") {
				t.Fatalf("err %v", err)
			}
			if b, err := liveFile(e); err != nil || b != "live-data-svc_pg" {
				t.Fatalf("old data not back: %q %v", b, err)
			}
			if _, err := os.Stat(e.m.upgradeDir("svc_pg")); !os.IsNotExist(err) {
				t.Fatal("upgrade dir left behind")
			}
			if c, _ := e.rt.Get(agent.ContainerName("svc_pg")); c.Info.State != "running" {
				t.Fatalf("service not restarted: %s", c.Info.State)
			}
			for _, w := range []string{"old", "new"} {
				if _, ok := e.rt.Get(UpgradeContainer("svc_pg", w)); ok {
					t.Fatalf("temporary %s container left behind", w)
				}
			}
		})
	}
}

func TestDBUpgradeRefusals(t *testing.T) {
	e, _ := upgradeEnv(t)
	if _, err := e.m.Upgrade(context.Background(), api.DBUpgradePayload{ServiceID: "svc_pg", Kind: "mysql", Image: "mysql:9"}); err == nil {
		t.Fatal("mysql has no one-click upgrade")
	}
	e.rt.PullErr = map[string]error{"postgres:19": fmt.Errorf("manifest unknown")}
	if _, err := e.m.Upgrade(context.Background(), api.DBUpgradePayload{ServiceID: "svc_pg", Kind: "postgres", Image: "postgres:19"}); err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("pull failure: %v", err)
	}
	if c, _ := e.rt.Get(agent.ContainerName("svc_pg")); c.Info.State != "running" {
		t.Fatal("a failed pull must not stop the service")
	}
	e.rt.PullErr = nil
	e.m.FreeSpace = func(string) (uint64, error) { return 1 << 20, nil }
	if _, err := e.m.Upgrade(context.Background(), api.DBUpgradePayload{ServiceID: "svc_pg", Kind: "postgres", Image: "postgres:19"}); err == nil || !strings.Contains(err.Error(), "not enough free disk") {
		t.Fatalf("space: %v", err)
	}
}

func TestDBUpgradeInterruptedIsRecovered(t *testing.T) {
	e, _ := upgradeEnv(t)
	// simulate an agent that died after moving the data aside
	os.MkdirAll(e.m.upgradeDir("svc_pg"), 0o700)
	os.WriteFile(e.m.upgradeMarker("svc_pg"), []byte("postgres:19\n"), 0o600)
	if err := os.Rename(e.paths.VolumesDir("svc_pg"), e.m.upgradeOld("svc_pg")); err != nil {
		t.Fatal(err)
	}
	e.rt.StopContainer(context.Background(), agent.ContainerName("svc_pg"), 0)
	e.m.recoverUpgrades(context.Background())
	if b, err := liveFile(e); err != nil || b != "live-data-svc_pg" {
		t.Fatalf("not recovered: %q %v", b, err)
	}
	if c, _ := e.rt.Get(agent.ContainerName("svc_pg")); c.Info.State != "running" {
		t.Fatal("service not restarted")
	}
}

func TestDBUpgradeHandle(t *testing.T) {
	e, _ := upgradeEnv(t)
	if !e.m.Handles(api.MsgDBUpgrade) || !e.m.Handles(api.MsgDBUpgradeCleanup) {
		t.Fatal("not handled")
	}
	raw, _ := json.Marshal(api.DBUpgradePayload{ServiceID: "svc_pg", Kind: "postgres", Image: "postgres:19"})
	out, err := e.m.Handle(context.Background(), api.MsgDBUpgrade, raw)
	if err != nil {
		t.Fatal(err)
	}
	var res api.DBUpgradeResult
	if json.Unmarshal([]byte(out), &res) != nil || res.After == "" {
		t.Fatalf("output %q", out)
	}
	raw, _ = json.Marshal(api.DBUpgradeRef{ServiceID: "svc_pg"})
	if _, err := e.m.Handle(context.Background(), api.MsgDBUpgradeCleanup, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.m.upgradeDir("svc_pg")); !os.IsNotExist(err) {
		t.Fatal("cleanup left the kept data")
	}
}
