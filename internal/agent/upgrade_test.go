package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func testUpgrader(t *testing.T, version string) (*Upgrader, *[]string, string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "atoll-agent")
	os.WriteFile(exe, []byte("old"), 0o755)
	var spawned []string
	u := &Upgrader{
		Dir:       filepath.Join(dir, "agent-upgrade"),
		Exe:       func() (string, error) { return exe, nil },
		Spawn:     func(_ context.Context, args []string) error { spawned = args; return nil },
		Version:   func(context.Context, string) (string, error) { return version, nil },
		Supported: func() error { return nil },
	}
	return u, &spawned, exe
}

func hostedBinary(t *testing.T, body string) (*httptest.Server, string) {
	t.Helper()
	path := "/downloads/atoll-agent-linux-" + runtime.GOARCH
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	h := sha256.Sum256([]byte(body))
	return ts, hex.EncodeToString(h[:])
}

func TestUpgradeStartsHelper(t *testing.T) {
	u, spawned, exe := testUpgrader(t, "v9.9.9")
	ts, sum := hostedBinary(t, "new binary")
	p := api.UpgradePayload{Version: "v9.9.9", Path: "/downloads/atoll-agent-linux-" + runtime.GOARCH, SHA256: sum}
	res, err := u.Start(context.Background(), ts.URL, "tok", p)
	if err != nil {
		t.Fatal(err)
	}
	if res.To != "v9.9.9" || res.From != Version {
		t.Fatalf("result %+v", res)
	}
	b, _ := os.ReadFile(u.newPath())
	if string(b) != "new binary" {
		t.Fatalf("new binary %q", b)
	}
	args := strings.Join(*spawned, " ")
	for _, want := range []string{"upgrade-helper", "--target " + exe, "--new " + u.newPath(), "--unit atoll-agent", "--marker " + u.markerPath()} {
		if !strings.Contains(args, want) {
			t.Fatalf("helper args %q missing %q", args, want)
		}
	}
	if _, err := u.Start(context.Background(), ts.URL, "tok", p); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("second start: %v", err)
	}
}

func TestUpgradeRejectsBadBinary(t *testing.T) {
	ts, sum := hostedBinary(t, "new binary")
	path := "/downloads/atoll-agent-linux-" + runtime.GOARCH
	cases := []struct {
		name    string
		version string // what the downloaded binary reports
		p       api.UpgradePayload
		want    string
	}{
		{"checksum", "v9.9.9", api.UpgradePayload{Version: "v9.9.9", Path: path, SHA256: strings.Repeat("0", 64)}, "checksum mismatch"},
		{"arch", "v9.9.9", api.UpgradePayload{Version: "v9.9.9", Path: "/downloads/atoll-agent-linux-mips", SHA256: sum}, "needs"},
		{"version", "v9.9.8", api.UpgradePayload{Version: "v9.9.9", Path: path, SHA256: sum}, "expected v9.9.9"},
		{"same", "v9.9.9", api.UpgradePayload{Version: Version, Path: path, SHA256: sum}, "already running"},
		{"no sum", "v9.9.9", api.UpgradePayload{Version: "v9.9.9", Path: path}, "SHA-256"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u, spawned, _ := testUpgrader(t, c.version)
			_, err := u.Start(context.Background(), ts.URL, "", c.p)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
			if *spawned != nil {
				t.Fatal("helper started")
			}
			if _, err := os.Stat(u.newPath()); err == nil {
				t.Fatal("rejected binary left in place")
			}
		})
	}
}

func TestUpgradeMarker(t *testing.T) {
	u, _, _ := testUpgrader(t, "")
	before := time.Now().UTC().Add(-time.Second)
	if err := markerHealthy(u.markerPath(), Version, before); err == nil {
		t.Fatal("healthy without a marker")
	}
	u.MarkConnected()
	if err := markerHealthy(u.markerPath(), Version, before); err != nil {
		t.Fatal(err)
	}
	if err := markerHealthy(u.markerPath(), "v9.9.9", before); err == nil {
		t.Fatal("healthy on the wrong version")
	}
	if err := markerHealthy(u.markerPath(), Version, time.Now().UTC().Add(time.Minute)); err == nil {
		t.Fatal("healthy with a marker from before the restart")
	}
}

func TestUpgradeResultReportedOnce(t *testing.T) {
	u, _, _ := testUpgrader(t, "")
	os.MkdirAll(u.Dir, 0o700)
	if u.PendingResult() != nil {
		t.Fatal("result without a file")
	}
	os.WriteFile(u.resultPath(), []byte(`{"mode":"update","ok":true,"to_version":"v9.9.9"}`), 0o600)
	r := u.PendingResult()
	if r == nil || !r.OK || r.ToVersion != "v9.9.9" {
		t.Fatalf("result %+v", r)
	}
	u.Reported()
	if u.PendingResult() != nil {
		t.Fatal("reported twice")
	}
}
