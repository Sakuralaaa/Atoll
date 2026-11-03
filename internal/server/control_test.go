package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/ctlbundle"
	"github.com/Sakuralaaa/Atoll/internal/server/selfupdate"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

const testPass = "correct-horse"

// rawDo returns status and body.
func (e *testEnv) rawDo(method, path string, body any) (int, []byte) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.hc.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func errCode(b []byte) string {
	var eb api.ErrorBody
	json.Unmarshal(b, &eb)
	return eb.Code
}

func TestControlAccessRules(t *testing.T) {
	e := newTestEnvWith(t, func(c *Config) {
		c.Journalctl = func(context.Context, ...string) ([]byte, error) { return []byte("x\n"), nil }
	})
	e.setup()
	var read, admin api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]string{"name": "ro", "scope": "read"}, &read, 201)
	e.mustDo("POST", "/api/v1/tokens", map[string]string{"name": "all", "scope": "admin"}, &admin, 201)

	var st api.ControlStatus
	if code := e.doWith(http.DefaultClient, read.Token, "GET", "/api/v1/control", nil, &st); code != 200 {
		t.Fatalf("read token status: %d", code)
	}
	if st.Version != Version || st.Host.Arch == "" || st.DataDir == "" || st.DatabaseBytes == 0 || st.StartedAt.IsZero() {
		t.Fatalf("status %+v", st)
	}
	if code := e.doWith(http.DefaultClient, read.Token, "GET", "/api/v1/control/metrics?range=24h", nil, nil); code != 200 {
		t.Fatalf("read token metrics: %d", code)
	}
	// everything else is panel-session only, even for an all-permissions token
	for _, rt := range []struct{ m, p string }{
		{"GET", "/api/v1/control/logs"}, {"GET", "/api/v1/audit"}, {"GET", "/api/v1/control/backups"},
		{"POST", "/api/v1/control/backups"}, {"POST", "/api/v1/control/export"}, {"POST", "/api/v1/control/update"},
		{"POST", "/api/v1/control/rollback"}, {"POST", "/api/v1/control/reauth"}, {"POST", "/api/v1/control/update/check"},
		{"GET", "/api/v1/control/backups/cb_20260101T000000.000Z_abcd/download"}, {"POST", "/api/v1/control/backups/passphrase"},
	} {
		var eb api.ErrorBody
		if code := e.doWith(http.DefaultClient, admin.Token, rt.m, rt.p, map[string]string{"passphrase": "aaaaaaaaaaaaaaaa", "password": testPass}, &eb); code != 403 || eb.Code != "session_required" {
			t.Fatalf("%s %s with token: %d %+v", rt.m, rt.p, code, eb)
		}
	}
	if code := e.doWith(http.DefaultClient, "", "GET", "/api/v1/control", nil, nil); code != 401 {
		t.Fatalf("anonymous status: %d", code)
	}
	// session
	e.mustDo("GET", "/api/v1/control", nil, &st, 200)
	e.mustDo("GET", "/api/v1/control/logs", nil, nil, 200)
}

func TestControlReauthGating(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	exp := api.ControlExportRequest{Passphrase: "export passphrase 1"}
	// no re-auth yet
	if code, b := e.rawDo("POST", "/api/v1/control/export", exp); code != 401 || errCode(b) != "reauth_required" {
		t.Fatalf("export without re-auth: %d %s", code, b)
	}
	// too short passphrase is rejected before anything else
	if code, _ := e.rawDo("POST", "/api/v1/control/export", api.ControlExportRequest{Passphrase: "short", ReauthFields: api.ReauthFields{Password: testPass}}); code != 400 {
		t.Fatalf("short passphrase: %d", code)
	}
	// wrong password: 401, audited, counted toward the lockout
	exp.Password = "wrong-password"
	if code, b := e.rawDo("POST", "/api/v1/control/export", exp); code != 401 || errCode(b) != "invalid_credentials" {
		t.Fatalf("wrong password: %d %s", code, b)
	}
	var n int
	e.srv.st.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='control.reauth_failed'`).Scan(&n)
	if n != 1 {
		t.Fatalf("failed re-auth audited %d times", n)
	}
	// inline password works and opens a short window
	exp.Password = testPass
	code, body := e.rawDo("POST", "/api/v1/control/export", exp)
	if code != 200 || !bytes.HasPrefix(body, []byte("ATOLLCB1")) {
		t.Fatalf("export: %d %.80q", code, body)
	}
	exp.Password = ""
	if code, _ := e.rawDo("POST", "/api/v1/control/export", exp); code != 200 {
		t.Fatalf("export inside the re-auth window: %d", code)
	}
	// window expires
	e.srv.ctl.mu.Lock()
	for k := range e.srv.ctl.reauth {
		e.srv.ctl.reauth[k] = time.Now().Add(-time.Second)
	}
	e.srv.ctl.mu.Unlock()
	if code, _ := e.rawDo("POST", "/api/v1/control/export", exp); code != 401 {
		t.Fatalf("export after the window: %d", code)
	}
	// explicit re-auth endpoint
	var rr api.ControlReauthResponse
	e.mustDo("POST", "/api/v1/control/reauth", api.ReauthFields{Password: testPass}, &rr, 200)
	if time.Until(rr.ExpiresAt) < time.Minute {
		t.Fatalf("window %v", rr.ExpiresAt)
	}

	// with 2FA on, the password alone is not enough
	clock := pinClock(t, time.Now().Truncate(30*time.Second).Add(10*time.Second))
	var en api.TwoFactorEnrollment
	e.mustDo("POST", "/api/v1/auth/2fa/enroll", api.TwoFactorRequest{Password: testPass}, &en, 200)
	c, _ := totpCode(en.Secret, *clock)
	e.mustDo("POST", "/api/v1/auth/2fa/enable", api.TwoFactorRequest{Code: c}, nil, 200)
	*clock = clock.Add(30 * time.Second)
	if code, b := e.rawDo("POST", "/api/v1/control/reauth", api.ReauthFields{Password: testPass}); code != 401 || errCode(b) != "code_required" {
		t.Fatalf("2fa without code: %d %s", code, b)
	}
	if code, b := e.rawDo("POST", "/api/v1/control/reauth", api.ReauthFields{Password: testPass, Code: "000000"}); code != 401 || errCode(b) != "invalid_code" {
		t.Fatalf("2fa wrong code: %d %s", code, b)
	}
	c, _ = totpCode(en.Secret, *clock)
	e.mustDo("POST", "/api/v1/control/reauth", api.ReauthFields{Password: testPass, Code: c}, nil, 200)

	// repeated failures lock the account like logins do
	e2 := newTestEnv(t)
	e2.setup()
	locked := false
	for range 12 {
		code, _ := e2.rawDo("POST", "/api/v1/control/reauth", api.ReauthFields{Password: "nope-nope"})
		if code == 429 {
			locked = true
			break
		}
	}
	if !locked {
		t.Fatal("re-auth failures never hit the lockout")
	}
}

func TestControlExportRoundTrip(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "server.env")
	os.WriteFile(envFile, []byte("ATOLL_PUBLIC_URL=https://panel.example.com\nATOLL_LISTEN=127.0.0.1:7300\n"), 0o600)
	e := newTestEnvWith(t, func(c *Config) { c.ServerEnvPath = envFile })
	e.setup()
	e.srv.st.SetSetting(context.Background(), "base_domain", "example.com")
	code, body := e.rawDo("POST", "/api/v1/control/export", api.ControlExportRequest{Passphrase: "export passphrase 1", ReauthFields: api.ReauthFields{Password: testPass}})
	if code != 200 {
		t.Fatalf("export %d %s", code, body)
	}
	dir := t.TempDir()
	m, err := ctlbundle.Extract(bytes.NewReader(body), "export passphrase 1", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(m.Files, "atoll.db") || !slices.Contains(m.Files, "master.key") || !slices.Contains(m.Files, "server.env") {
		t.Fatalf("files %v", m.Files)
	}
	if k1, _ := os.ReadFile(filepath.Join(e.srv.cfg.DataDir, "master.key")); !bytes.Equal(k1, mustRead(t, filepath.Join(dir, "master.key"))) {
		t.Fatal("master.key differs")
	}
	// the snapshot opens with the bundled key and holds the data
	st, err := openDataStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, _ := st.GetSetting(context.Background(), "base_domain"); v != "example.com" {
		t.Fatalf("snapshot setting %q", v)
	}
	if _, err := ctlbundle.Extract(bytes.NewReader(body), "a wrong passphrase", t.TempDir()); err != ctlbundle.ErrDecrypt {
		t.Fatalf("wrong passphrase: %v", err)
	}
	var n int
	e.srv.st.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='control.export'`).Scan(&n)
	if n != 1 {
		t.Fatalf("export audited %d times", n)
	}
	// the passphrase is never stored
	e.srv.st.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%export passphrase%'`).Scan(&n)
	var m2 int
	e.srv.st.DB.QueryRow(`SELECT COUNT(*) FROM settings WHERE value LIKE '%export passphrase%'`).Scan(&m2)
	if n+m2 != 0 {
		t.Fatal("passphrase stored")
	}
}

func mustRead(t *testing.T, p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Writers keep two settings equal inside one transaction; every snapshot taken meanwhile must
// open cleanly and see them equal.
func TestControlSnapshotConsistency(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			tx, err := e.srv.st.DB.BeginTx(ctx, nil)
			if err != nil {
				continue
			}
			v := fmt.Sprint(i)
			tx.Exec(`INSERT INTO settings(key, value) VALUES('snap_a', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, v)
			tx.Exec(`INSERT INTO audit_log(actor, action, target, detail, created_at) VALUES('t','snap.fill',?, ?, ?)`, v, strings.Repeat("x", 500), store.TS(time.Now()))
			tx.Exec(`INSERT INTO settings(key, value) VALUES('snap_b', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, v)
			tx.Commit()
		}
	}()
	dir := t.TempDir()
	for i := range 8 {
		p := filepath.Join(dir, fmt.Sprintf("s%d.db", i))
		if err := snapshotDB(ctx, e.srv.st, p); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "master.key"), mustRead(t, filepath.Join(e.srv.cfg.DataDir, "master.key")), 0o600)
		os.Rename(p, filepath.Join(dir, "atoll.db"))
		st, err := openDataStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		var ok, a, b string
		st.DB.QueryRow(`PRAGMA integrity_check`).Scan(&ok)
		st.DB.QueryRow(`SELECT value FROM settings WHERE key='snap_a'`).Scan(&a)
		st.DB.QueryRow(`SELECT value FROM settings WHERE key='snap_b'`).Scan(&b)
		st.Close()
		os.Remove(filepath.Join(dir, "atoll.db"))
		if ok != "ok" || a != b {
			t.Fatalf("snapshot %d: integrity=%s a=%s b=%s", i, ok, a, b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
}

// ---------- fake S3 ----------

type fakeS3 struct {
	mu   sync.Mutex
	objs map[string][]byte
	puts int
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") || r.Header.Get("x-amz-date") == "" {
		w.WriteHeader(403)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}
	switch {
	case r.Method == "PUT":
		b, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(b)
		if r.Header.Get("x-amz-content-sha256") != hex.EncodeToString(sum[:]) {
			w.WriteHeader(400)
			return
		}
		f.objs[key] = b
		f.puts++
	case r.Method == "GET" && key == "":
		prefix := r.URL.Query().Get("prefix")
		keys := []string{}
		for k := range f.objs {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		fmt.Fprint(w, `<ListBucketResult>`)
		for _, k := range keys {
			fmt.Fprintf(w, `<Contents><Key>%s</Key><Size>%d</Size><LastModified>2026-01-01T00:00:00Z</LastModified></Contents>`, k, len(f.objs[k]))
		}
		fmt.Fprint(w, `<IsTruncated>false</IsTruncated></ListBucketResult>`)
	case r.Method == "GET":
		b, ok := f.objs[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Write(b)
	case r.Method == "DELETE":
		delete(f.objs, key)
		w.WriteHeader(204)
	}
}

func (f *fakeS3) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []string{}
	for k := range f.objs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestControlBackupsLocalAndRemote(t *testing.T) {
	s3 := &fakeS3{objs: map[string][]byte{}}
	s3ts := httptest.NewServer(s3)
	defer s3ts.Close()
	e := newTestEnv(t)
	e.setup()
	ctx := context.Background()
	// no target yet: local only, encrypted with a generated passphrase
	var b api.ControlBackup
	e.mustDo("POST", "/api/v1/control/backups", nil, &b, 201)
	if !b.Local || b.Remote || b.SizeBytes == 0 || b.Trigger != "manual" {
		t.Fatalf("backup %+v", b)
	}
	// download needs the re-auth window; the passphrase can be revealed
	if code, _ := e.rawDo("GET", "/api/v1/control/backups/"+b.ID+"/download", nil); code != 401 {
		t.Fatalf("download without re-auth: %d", code)
	}
	var pp api.ControlPassphrase
	e.mustDo("POST", "/api/v1/control/backups/passphrase", api.ControlPassphraseRequest{ReauthFields: api.ReauthFields{Password: testPass}}, &pp, 200)
	if pp.From != "generated" || len(pp.Passphrase) < 12 {
		t.Fatalf("passphrase %+v", pp)
	}
	code, body := e.rawDo("GET", "/api/v1/control/backups/"+b.ID+"/download", nil)
	if code != 200 {
		t.Fatalf("download %d", code)
	}
	if _, err := ctlbundle.Extract(bytes.NewReader(body), pp.Passphrase, t.TempDir()); err != nil {
		t.Fatalf("downloaded bundle: %v", err)
	}
	if code, _ := e.rawDo("GET", "/api/v1/control/backups/../../etc/download", nil); code == 200 {
		t.Fatal("path traversal")
	}

	// configure the service backup target: uploads go to <prefix>/control/
	for k, v := range map[string]string{"backup_endpoint": s3ts.URL, "backup_bucket": "atoll-test", "backup_prefix": "atoll", "backup_access_key": "AK",
		"backup_secret_key": "SK", "backup_repo_password": "repo-password-123", "control_backup_keep": "2"} {
		e.srv.st.SetSetting(ctx, k, v)
	}
	e.srv.cfg.S3HTTP = s3ts.Client()
	ids := []string{}
	for range 4 {
		e.mustDo("POST", "/api/v1/control/backups", nil, &b, 201)
		if !b.Remote || b.Error != "" || !strings.HasPrefix(b.RemoteKey, "atoll/control/") {
			t.Fatalf("remote backup %+v", b)
		}
		ids = append(ids, b.ID)
		time.Sleep(5 * time.Millisecond)
	}
	keys := s3.keys()
	if len(keys) != 2 || keys[1] != "atoll/control/"+ids[3]+".atollbak" || keys[0] != "atoll/control/"+ids[2]+".atollbak" {
		t.Fatalf("remote keeps the newest 2: %v (ids %v)", keys, ids)
	}
	// local keeps 3
	n := 0
	ents, _ := os.ReadDir(e.srv.controlBackupDir())
	for _, x := range ents {
		if strings.HasSuffix(x.Name(), ".atollbak") {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("local bundles: %d", n)
	}
	var list api.ControlBackupList
	e.mustDo("GET", "/api/v1/control/backups", nil, &list, 200)
	if !list.RemoteEnabled || list.RemoteError != "" || list.Backups[0].ID != ids[3] || list.KeepLocal != 3 || list.KeepRemote != 2 {
		t.Fatalf("list %+v", list)
	}
	// the generated passphrase stays in use (bundles stay readable with what was revealed)
	if list.PassphraseFrom != "generated" {
		t.Fatalf("passphrase from %s", list.PassphraseFrom)
	}
	// a remote-only bundle downloads through the server
	s3.mu.Lock()
	remoteOnly := "cb_20200101T000000.000Z_zzzz"
	s3.objs["atoll/control/"+remoteOnly+".atollbak"] = []byte("ATOLLCB1remote")
	s3.mu.Unlock()
	code, body = e.rawDo("GET", "/api/v1/control/backups/"+remoteOnly+"/download", nil)
	if code != 200 || string(body) != "ATOLLCB1remote" {
		t.Fatalf("remote download %d %q", code, body)
	}
	var cnt int
	e.srv.st.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='control.backup'`).Scan(&cnt)
	if cnt != 5 {
		t.Fatalf("backups audited %d", cnt)
	}
}

// ---------- self-update ----------

type fakeGitHub struct {
	mu     sync.Mutex
	files  map[string][]byte // asset name -> content
	sums   string            // SHA256SUMS override
	tag    string
	server *httptest.Server
}

func (g *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if strings.HasPrefix(r.URL.Path, "/dl/") {
		name := strings.TrimPrefix(r.URL.Path, "/dl/")
		if name == "SHA256SUMS" {
			io.WriteString(w, g.sumsText())
			return
		}
		b, ok := g.files[name]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Write(b)
		return
	}
	if r.URL.Path == "/repos/Sakuralaaa/Atoll/releases/latest" || r.URL.Path == "/repos/Sakuralaaa/Atoll/releases/tags/"+g.tag {
		assets := []map[string]any{{"name": "SHA256SUMS", "browser_download_url": g.server.URL + "/dl/SHA256SUMS"}}
		for n := range g.files {
			assets = append(assets, map[string]any{"name": n, "browser_download_url": g.server.URL + "/dl/" + n})
		}
		json.NewEncoder(w).Encode(map[string]any{"tag_name": g.tag, "name": "Atoll " + g.tag, "html_url": "https://github.com/Sakuralaaa/Atoll/releases/" + g.tag,
			"published_at": time.Now().UTC(), "assets": assets})
		return
	}
	w.WriteHeader(404)
}

func (g *fakeGitHub) sumsText() string {
	if g.sums != "" {
		return g.sums
	}
	var sb strings.Builder
	for n, b := range g.files {
		s := sha256.Sum256(b)
		fmt.Fprintf(&sb, "%s  %s\n", hex.EncodeToString(s[:]), n)
	}
	return sb.String()
}

// spawned returns a copy of the helper invocations so far (the update runs in the background).
func newUpdateEnv(t *testing.T) (*testEnv, *fakeGitHub, func() [][]string, string) {
	gh := &fakeGitHub{files: map[string][]byte{}, tag: "v9.9.9"}
	gh.server = httptest.NewServer(gh)
	t.Cleanup(gh.server.Close)
	bin := filepath.Join(t.TempDir(), "atoll-server")
	os.WriteFile(bin, []byte("#!/bin/sh\necho v0.0.1\n"), 0o755)
	var mu sync.Mutex
	spawned := &[][]string{}
	e := newTestEnvWith(t, func(c *Config) {
		c.GitHubAPI = gh.server.URL
		c.BinaryPath = bin
		c.SpawnHelper = func(_ context.Context, args []string) error {
			mu.Lock()
			*spawned = append(*spawned, args)
			mu.Unlock()
			return nil
		}
	})
	e.setup()
	snapshot := func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return append([][]string(nil), *spawned...)
	}
	return e, gh, snapshot, bin
}

func TestControlUpdateFlow(t *testing.T) {
	e, gh, spawned, bin := newUpdateEnv(t)
	arch := "atoll-server-linux-" + goarch()
	gh.files[arch] = []byte("#!/bin/sh\necho v9.9.9\n")
	os.MkdirAll(filepath.Join(e.srv.cfg.DataDir, "downloads"), 0o755)
	gh.files["atoll-agent-linux-amd64"] = []byte("agent-amd64")

	var st api.ControlStatus
	e.mustDo("GET", "/api/v1/control", nil, &st, 200)
	if st.LatestRelease == nil || st.LatestRelease.Tag != "v9.9.9" || !st.SelfUpdate {
		t.Fatalf("status %+v", st)
	}
	// re-auth required
	if code, b := e.rawDo("POST", "/api/v1/control/update", api.ControlUpdateRequest{Version: "v9.9.9"}); code != 401 || errCode(b) != "reauth_required" {
		t.Fatalf("update without re-auth: %d %s", code, b)
	}
	if code, _ := e.rawDo("POST", "/api/v1/control/update", api.ControlUpdateRequest{Version: "not a tag", ReauthFields: api.ReauthFields{Password: testPass}}); code != 400 {
		t.Fatalf("bad tag: %d", code)
	}
	var ur api.ControlUpdateResponse
	e.mustDo("POST", "/api/v1/control/update", api.ControlUpdateRequest{Version: "v9.9.9", ReauthFields: api.ReauthFields{Password: testPass}}, &ur, 202)
	waitFor(t, "helper spawned", func() bool { return len(spawned()) == 1 })
	args := spawned()[0]
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "control-helper --mode update --target "+bin) || !strings.Contains(joined, "--new "+filepath.Join(e.srv.updateDir(), "atoll-server.new")) {
		t.Fatalf("helper args %v", args)
	}
	if got := mustRead(t, filepath.Join(e.srv.updateDir(), "atoll-server.new")); !bytes.Equal(got, gh.files[arch]) {
		t.Fatal("new binary not staged")
	}
	if mustRead(t, args[0]) == nil || !bytes.Equal(mustRead(t, args[0]), mustRead(t, bin)) {
		t.Fatal("helper is not a copy of the running binary")
	}
	// a pre-update control backup was taken, and agent binaries refreshed
	if l := e.srv.localControlBackups(); len(l) != 1 || l[0].Trigger != "pre-update" {
		t.Fatalf("backups %+v", l)
	}
	if string(mustRead(t, filepath.Join(e.srv.cfg.DataDir, "downloads", "atoll-agent-linux-amd64"))) != "agent-amd64" {
		t.Fatal("agent binary not refreshed")
	}
	e.mustDo("GET", "/api/v1/control", nil, &st, 200)
	if !st.UpdateInProgress || st.UpdatePhase != "restarting" || st.UpdateTarget != "v9.9.9" {
		t.Fatalf("in progress: %+v", st)
	}
	// restarted on the new binary, helper not reported yet: still in progress, target tells the UI it is up
	e.srv.ctl.setPhase("", "", "")
	e.mustDo("GET", "/api/v1/control", nil, &st, 200)
	if !st.UpdateInProgress || st.UpdatePhase != "restarting" || st.UpdateTarget != "v9.9.9" {
		t.Fatalf("restarted, unreported: %+v", st)
	}
	// the helper reports back (as after the restart)
	selfupdate.WriteResult(filepath.Join(e.srv.updateDir(), "result.json"), api.UpdateResult{Mode: "update", FromVersion: "v0.0.1", ToVersion: "v9.9.9", OK: true, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()})
	e.srv.ctl.setPhase("", "", "") // a restarted server starts idle
	e.mustDo("GET", "/api/v1/control", nil, &st, 200)
	if st.UpdateInProgress || st.LastUpdate == nil || !st.LastUpdate.OK {
		t.Fatalf("after: %+v", st)
	}
}

func TestControlUpdateChecksumMismatch(t *testing.T) {
	e, gh, spawned, _ := newUpdateEnv(t)
	arch := "atoll-server-linux-" + goarch()
	gh.files[arch] = []byte("#!/bin/sh\necho v9.9.9\n")
	gh.sums = strings.Repeat("ab", 32) + "  " + arch + "\n"
	e.mustDo("POST", "/api/v1/control/update", api.ControlUpdateRequest{ReauthFields: api.ReauthFields{Password: testPass}}, nil, 202)
	var st api.ControlStatus
	waitFor(t, "update failure", func() bool {
		e.mustDo("GET", "/api/v1/control", nil, &st, 200)
		return st.LastUpdate != nil && !st.UpdateInProgress
	})
	if st.LastUpdate.OK || !strings.Contains(st.LastUpdate.Error, "checksum mismatch") {
		t.Fatalf("result %+v", st.LastUpdate)
	}
	if len(spawned()) != 0 {
		t.Fatal("helper started for a binary with a bad checksum")
	}
	if _, err := os.Stat(filepath.Join(e.srv.updateDir(), "atoll-server.new")); err == nil {
		t.Fatal("bad binary left staged")
	}
	if l := e.srv.localControlBackups(); len(l) != 0 {
		t.Fatal("backup taken although the update was rejected")
	}
	// missing SHA256SUMS entry is refused too
	gh.sums = "\n"
	e.mustDo("POST", "/api/v1/control/update", api.ControlUpdateRequest{}, nil, 202)
	waitFor(t, "update failure 2", func() bool {
		e.mustDo("GET", "/api/v1/control", nil, &st, 200)
		return st.LastUpdate != nil && strings.Contains(st.LastUpdate.Error, "no entry") && !st.UpdateInProgress
	})
}

func TestControlRollbackEndpoint(t *testing.T) {
	e, _, spawned, bin := newUpdateEnv(t)
	if code, b := e.rawDo("POST", "/api/v1/control/rollback", api.ReauthFields{Password: testPass}); code != 409 || errCode(b) != "no_previous_binary" {
		t.Fatalf("rollback without prev: %d %s", code, b)
	}
	os.WriteFile(selfupdate.PrevPath(bin), []byte("#!/bin/sh\necho v0.0.0\n"), 0o755)
	var st api.ControlStatus
	e.mustDo("GET", "/api/v1/control", nil, &st, 200)
	if st.RollbackVersion != "v0.0.0" {
		t.Fatalf("rollback version %q", st.RollbackVersion)
	}
	var ur api.ControlUpdateResponse
	e.mustDo("POST", "/api/v1/control/rollback", api.ReauthFields{Password: testPass}, &ur, 202)
	if ur.Version != "v0.0.0" || len(spawned()) != 1 || !slices.Contains(spawned()[0], "rollback") {
		t.Fatalf("rollback %+v %v", ur, spawned())
	}
}

func goarch() string { return runtime.GOARCH }

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.1.4", "v0.2.0", true}, {"v0.2.0", "v0.1.9", false}, {"v1.0.0-rc1", "v1.0.0", true}, {"v1.0.0", "v1.0.0", false}, {"dev", "v9.0.0", false}} {
		if versionLess(c.a, c.b) != c.want {
			t.Fatalf("%s < %s", c.a, c.b)
		}
	}
	if s := parseChecksums("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855 *atoll-server-linux-arm64\nbad line\n"); s["atoll-server-linux-arm64"] == "" {
		t.Fatalf("sums %v", s)
	}
}

// ---------- logs ----------

func TestControlLogs(t *testing.T) {
	var got []string
	fail := false
	e := newTestEnvWith(t, func(c *Config) {
		c.Journalctl = func(_ context.Context, args ...string) ([]byte, error) {
			got = args
			if fail {
				return nil, fmt.Errorf("journalctl not found on this host")
			}
			return []byte("2026-11-02T10:00:00+0000 host atoll-server[1]: hello\n2026-11-02T10:00:01+0000 host atoll-server[1]: world\n"), nil
		}
	})
	e.setup()
	var l api.ControlLogs
	e.mustDo("GET", "/api/v1/control/logs?tail=50&since=15m", nil, &l, 200)
	if !l.Available || len(l.Lines) != 2 {
		t.Fatalf("logs %+v", l)
	}
	j := strings.Join(got, " ")
	if !strings.Contains(j, "-u atoll-server -o short-iso") || !strings.Contains(j, "-n 50") || !strings.Contains(j, "--since @") {
		t.Fatalf("args %v", got)
	}
	if code := e.do("GET", "/api/v1/control/logs?since=yesterday", nil, nil); code != 400 {
		t.Fatalf("bad since: %d", code)
	}
	fail = true
	e.mustDo("GET", "/api/v1/control/logs", nil, &l, 200)
	if l.Available || !strings.Contains(l.Message, "not available") {
		t.Fatalf("fallback %+v", l)
	}
}

// ---------- audit API ----------

func TestAuditAPIFiltering(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "ops", "permissions": []string{"read", "deploy", "delete"}}, &tok, 201)
	// a token action that is audited (denied delete of a missing project still audits as api.delete)
	e.doWith(http.DefaultClient, tok.Token, "DELETE", "/api/v1/projects/prj_missing", nil, nil)
	e.mustDo("POST", "/api/v1/control/reauth", api.ReauthFields{Password: testPass}, nil, 200)
	e.mustDo("POST", "/api/v1/control/backups", nil, nil, 201)

	var page api.AuditPage
	e.mustDo("GET", "/api/v1/audit?action=control.", nil, &page, 200)
	if len(page.Entries) != 2 || page.Entries[0].Action != "control.backup" || page.Entries[1].Action != "control.reauth" {
		t.Fatalf("control entries %+v", page.Entries)
	}
	ent := page.Entries[1]
	if ent.ActorKind != "session" || ent.Actor != "user:admin" || ent.IP == "" || len(ent.SessionID) != 16 {
		t.Fatalf("session entry %+v", ent)
	}
	e.mustDo("GET", "/api/v1/audit?actor=token:", nil, &page, 200)
	if len(page.Entries) == 0 {
		t.Fatal("no token entries")
	}
	for _, x := range page.Entries {
		if x.ActorKind != "token" || x.TokenID != tok.ID || x.IP == "" {
			t.Fatalf("token entry %+v", x)
		}
	}
	// paging
	var all, p1, p2 api.AuditPage
	e.mustDo("GET", "/api/v1/audit?limit=500", nil, &all, 200)
	e.mustDo("GET", "/api/v1/audit?limit=2", nil, &p1, 200)
	if len(p1.Entries) != 2 || p1.NextBefore != p1.Entries[1].ID {
		t.Fatalf("page 1 %+v", p1)
	}
	e.mustDo("GET", fmt.Sprintf("/api/v1/audit?limit=2&before=%d", p1.NextBefore), nil, &p2, 200)
	if len(p2.Entries) == 0 || p2.Entries[0].ID >= p1.NextBefore || p2.Entries[0].ID != all.Entries[2].ID {
		t.Fatalf("page 2 %+v", p2)
	}
	e.mustDo("GET", "/api/v1/audit?action=auth.setup&actor=user:admin", nil, &page, 200)
	for _, x := range page.Entries {
		if x.Action != "auth.setup" || x.Actor != "user:admin" {
			t.Fatalf("exact filter %+v", x)
		}
	}
	if code := e.do("GET", "/api/v1/audit?limit=abc", nil, nil); code != 400 {
		t.Fatalf("bad limit: %d", code)
	}
	// LIKE wildcards in a filter are literal
	e.mustDo("GET", "/api/v1/audit?action=%25.", nil, &page, 200)
	if len(page.Entries) != 0 {
		t.Fatalf("wildcard matched %d", len(page.Entries))
	}
}

// ---------- restore CLI round trip ----------

func TestRestoreControlRoundTrip(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	e.setup()
	e.srv.st.SetSetting(ctx, "base_domain", "example.com")
	e.srv.st.SetSetting(ctx, "tailnet_control_url", "http://100.100.0.1:7301")
	e.srv.st.SetSetting(ctx, "cloudflare_token", "cf-secret-token")
	_, _ = startNetAgent(t, e, "vps-a", "", &api.NodeNetwork{State: "not_installed", ControlURL: "https://panel.example.com"})

	srcEnv := filepath.Join(t.TempDir(), "server.env")
	os.WriteFile(srcEnv, []byte("ATOLL_PUBLIC_URL=https://panel.example.com\nATOLL_AGENT_LISTEN=tailscale:7301\nATOLL_AGENT_TAILNET_ONLY=1\n"), 0o600)
	bundle := filepath.Join(t.TempDir(), "control.atollbak")
	if _, err := ExportControl(ctx, e.srv.cfg.DataDir, bundle, "short", srcEnv, ""); err == nil {
		t.Fatal("short passphrase accepted")
	}
	m, err := ExportControl(ctx, e.srv.cfg.DataDir, bundle, "migration passphrase", srcEnv, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if m.PublicURL != "https://panel.example.com" {
		t.Fatalf("manifest %+v", m)
	}

	newData := filepath.Join(t.TempDir(), "atoll")
	newEnv := filepath.Join(t.TempDir(), "etc", "server.env")
	os.MkdirAll(filepath.Dir(newEnv), 0o755)
	os.WriteFile(newEnv, []byte("ATOLL_LISTEN=127.0.0.1:7300\nATOLL_AGENT_LISTEN=tailscale:7399\n"), 0o600)
	if _, err := RestoreControl(ctx, RestoreOptions{Bundle: bundle, Passphrase: "wrong passphrase!!", DataDir: newData}); err != ctlbundle.ErrDecrypt {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if _, err := os.Stat(newData); !os.IsNotExist(err) {
		t.Fatalf("failed restore left %s behind: %v", newData, err)
	}
	// nested dirs it created are removed too; a dir that already existed is kept as it was
	nested := filepath.Join(t.TempDir(), "a", "b", "atoll")
	if _, err := RestoreControl(ctx, RestoreOptions{Bundle: bundle, Passphrase: "wrong passphrase!!", DataDir: nested}); err != ctlbundle.ErrDecrypt {
		t.Fatalf("wrong passphrase (nested): %v", err)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(nested))); !os.IsNotExist(err) {
		t.Fatalf("created parent dirs left behind: %v", err)
	}
	existing := t.TempDir()
	os.WriteFile(filepath.Join(existing, "keep.txt"), []byte("x"), 0o600)
	if _, err := RestoreControl(ctx, RestoreOptions{Bundle: bundle, Passphrase: "wrong passphrase!!", DataDir: existing}); err != ctlbundle.ErrDecrypt {
		t.Fatalf("wrong passphrase (existing): %v", err)
	}
	if ents, _ := os.ReadDir(existing); len(ents) != 1 || ents[0].Name() != "keep.txt" {
		t.Fatalf("pre-existing data dir changed: %v", ents)
	}
	under := filepath.Join(existing, "sub")
	if _, err := RestoreControl(ctx, RestoreOptions{Bundle: bundle, Passphrase: "wrong passphrase!!", DataDir: under}); err != ctlbundle.ErrDecrypt {
		t.Fatalf("wrong passphrase (under existing): %v", err)
	}
	if _, err := os.Stat(under); !os.IsNotExist(err) {
		t.Fatal("created sub dir left behind")
	}
	if _, err := os.Stat(existing); err != nil {
		t.Fatal("pre-existing parent removed")
	}
	res, err := RestoreControl(ctx, RestoreOptions{Bundle: bundle, Passphrase: "migration passphrase", DataDir: newData, EnvFile: newEnv, AdoptPublicIP: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 1 || res.TailnetCleared != "http://100.100.0.1:7301" || !res.EnvWritten {
		t.Fatalf("result %+v", res)
	}
	env := string(mustRead(t, newEnv))
	if !strings.Contains(env, "ATOLL_PUBLIC_URL=https://panel.example.com") || !strings.Contains(env, "ATOLL_AGENT_LISTEN=tailscale:7399") || strings.Contains(env, "tailscale:7301") || strings.Contains(env, "TAILNET_ONLY") {
		t.Fatalf("merged env:\n%s", env)
	}
	if _, err := os.Stat(filepath.Join(newData, "adopt-public-ip")); err != nil {
		t.Fatal("adopt marker missing")
	}
	// a second restore without --force refuses
	if _, err := RestoreControl(ctx, RestoreOptions{Bundle: bundle, Passphrase: "migration passphrase", DataDir: newData}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("restore over existing: %v", err)
	}

	// the restored server starts, decrypts secrets, accepts the old password, points the panel
	// DNS record at its own IP and forgets the old tailnet URL
	e2 := newTestEnvWith(t, func(c *Config) { c.DataDir = newData; c.PublicIP = "198.51.100.7" })
	e2.mustDo("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPass}, nil, 200)
	if v, _ := e2.srv.st.GetSetting(ctx, "cloudflare_token"); v != "cf-secret-token" {
		t.Fatalf("secret after restore %q", v)
	}
	e2.srv.st.SetSetting(ctx, "public_url", "https://panel.example.com")
	changed, msg, err := e2.srv.adoptPublicIP(ctx)
	if err != nil || !changed {
		t.Fatalf("adopt: %v %s", err, msg)
	}
	e2.cf.mu.Lock()
	got := e2.cf.records["panel.example.com"]
	e2.cf.mu.Unlock()
	if got != "198.51.100.7" {
		t.Fatalf("panel record %q", got)
	}
	if changed, _, _ := e2.srv.adoptPublicIP(ctx); changed {
		t.Fatal("second adopt changed the record again")
	}
	if v, _ := e2.srv.st.GetSetting(ctx, "tailnet_control_url"); v != "" {
		t.Fatalf("tailnet URL kept: %q", v)
	}
	var servers []api.Server
	e2.mustDo("GET", "/api/v1/servers", nil, &servers, 200)
	if len(servers) != 1 || servers[0].Status == "online" {
		t.Fatalf("servers after restore %+v", servers)
	}
	// with --force the current state is moved aside
	res, err = RestoreControl(ctx, RestoreOptions{Bundle: bundle, Passphrase: "migration passphrase", DataDir: t.TempDir(), Force: true})
	if err != nil || res.MovedAside != "" {
		t.Fatalf("force on empty dir: %v %+v", err, res)
	}
}

func TestAdoptPublicIPOutsideZone(t *testing.T) {
	e := newTestEnvWith(t, func(c *Config) { c.PublicIP = "198.51.100.7" })
	e.setup()
	ctx := context.Background()
	e.srv.st.SetSetting(ctx, "cloudflare_token", "tok")
	e.srv.st.SetSetting(ctx, "base_domain", "example.com")
	e.srv.st.SetSetting(ctx, "public_url", "https://panel.other.org")
	if changed, msg, err := e.srv.adoptPublicIP(ctx); err != nil || changed || !strings.Contains(msg, "not under") {
		t.Fatalf("outside zone: %v %v %s", changed, err, msg)
	}
	e.srv.st.SetSetting(ctx, "public_url", "http://203.0.113.5:7300")
	if changed, _, err := e.srv.adoptPublicIP(ctx); err != nil || changed {
		t.Fatal("IP public URL changed DNS")
	}
}

// ---------- fallback URL push ----------

func TestPublicDomainPushedAsFallback(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	e.srv.st.SetSetting(context.Background(), "public_url", "https://panel.example.com")
	// connected over an IP URL without the domain: gets the domain as fallback, primary kept
	a, _ := startNetAgent(t, e, "vps-a", "", &api.NodeNetwork{State: "not_installed", ControlURL: "http://203.0.113.1:7300"})
	waitFor(t, "fallback push", func() bool { return len(a.received(api.MsgNetwork)) > 0 })
	var p api.NetworkPayload
	json.Unmarshal(a.received(api.MsgNetwork)[0], &p)
	if p.FallbackURL != "https://panel.example.com" || p.ControlURL != "http://203.0.113.1:7300" || p.AuthKey != "" {
		t.Fatalf("payload %+v", p)
	}
	// connected through a fallback: only the URL is added
	b, _ := startNetAgent(t, e, "vps-b", "", &api.NodeNetwork{State: "not_installed", ControlURL: "http://203.0.113.1:7300", FallbackURLs: []string{"http://203.0.113.1:7300"}})
	waitFor(t, "fallback push b", func() bool { return len(b.received(api.MsgNetwork)) > 0 })
	p = api.NetworkPayload{}
	json.Unmarshal(b.received(api.MsgNetwork)[0], &p)
	if p.FallbackURL != "https://panel.example.com" || p.ControlURL != "" {
		t.Fatalf("payload b %+v", p)
	}
	// already has it: nothing sent
	c, _ := startNetAgent(t, e, "vps-c", "", &api.NodeNetwork{State: "running", ControlURL: "http://100.100.0.1:7301", FallbackURLs: []string{"https://panel.example.com/"}})
	d, _ := startNetAgent(t, e, "vps-d", "", &api.NodeNetwork{State: "running", ControlURL: "https://panel.example.com"})
	time.Sleep(300 * time.Millisecond)
	if len(c.received(api.MsgNetwork))+len(d.received(api.MsgNetwork)) != 0 {
		t.Fatal("pushed to agents that already have the domain")
	}
	// an IP-based public URL is never pushed
	e.srv.st.SetSetting(context.Background(), "public_url", "http://203.0.113.9:7300")
	f, _ := startNetAgent(t, e, "vps-f", "", &api.NodeNetwork{State: "running", ControlURL: "http://100.100.0.1:7301"})
	time.Sleep(300 * time.Millisecond)
	if len(f.received(api.MsgNetwork)) != 0 {
		t.Fatal("IP public URL pushed")
	}
	if domainURL("https://localhost:7300") || domainURL("http://[::1]:7300") || !domainURL("https://a.example.com:8443") {
		t.Fatal("domainURL")
	}
}

// ---------- update result after restart ----------

func countAudit(t *testing.T, e *testEnv, action string) int {
	t.Helper()
	rows, err := e.srv.st.ListAudit(context.Background(), store.AuditFilter{Action: action, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// The helper writes result.json only after the new server has started: the server keeps
// looking for it and logs the audit entry exactly once.
func TestUpdateResultWrittenAfterStart(t *testing.T) {
	oldPoll, oldWin := updateResultPoll, updateResultWindow
	updateResultPoll, updateResultWindow = 20*time.Millisecond, time.Minute
	t.Cleanup(func() { updateResultPoll, updateResultWindow = oldPoll, oldWin })

	data := t.TempDir()
	dir := filepath.Join(data, "control-update")
	os.MkdirAll(dir, 0o700)
	started := time.Now().UTC()
	pb, _ := json.Marshal(pendingUpdate{Mode: "update", Version: "v9.9.9", From: "v0.0.1", StartedAt: started})
	os.WriteFile(filepath.Join(dir, "pending.json"), pb, 0o600)
	// a result from an earlier update is not this one's
	selfupdate.WriteResult(filepath.Join(dir, "result.json"), api.UpdateResult{Mode: "update", ToVersion: "v0.0.1", OK: true, StartedAt: started.Add(-time.Hour), FinishedAt: started.Add(-time.Hour)})

	e := newTestEnvWith(t, func(c *Config) { c.DataDir = data })
	time.Sleep(100 * time.Millisecond)
	if n := countAudit(t, e, "control.update_result"); n != 0 {
		t.Fatalf("stale result audited: %d", n)
	}
	if !e.srv.ctl.resWatching.Load() {
		t.Fatal("no watcher for the pending update")
	}
	// the helper reports after the server is up
	res := api.UpdateResult{Mode: "update", FromVersion: "v0.0.1", ToVersion: "v9.9.9", OK: true, StartedAt: started, FinishedAt: time.Now().UTC()}
	selfupdate.WriteResult(filepath.Join(dir, "result.json"), res)
	waitFor(t, "result audited", func() bool { return countAudit(t, e, "control.update_result") == 1 })
	if _, err := os.Stat(filepath.Join(dir, "pending.json")); !os.IsNotExist(err) {
		t.Fatal("pending.json not removed")
	}
	waitFor(t, "watcher stops", func() bool { return !e.srv.ctl.resWatching.Load() })

	// idempotent: consuming again, or pending.json reappearing (a crash before its removal),
	// logs nothing new
	if e.srv.consumeUpdateResult() {
		t.Fatal("consumed without a pending update")
	}
	os.WriteFile(filepath.Join(dir, "pending.json"), pb, 0o600)
	if !e.srv.consumeUpdateResult() {
		t.Fatal("pending update with its result not settled")
	}
	e.srv.watchUpdateResult()
	if n := countAudit(t, e, "control.update_result"); n != 1 {
		t.Fatalf("audited %d times", n)
	}
}

// The watcher gives up after its window when the helper never reports.
func TestUpdateResultWatchWindow(t *testing.T) {
	oldPoll, oldWin := updateResultPoll, updateResultWindow
	updateResultPoll, updateResultWindow = 10*time.Millisecond, 80*time.Millisecond
	t.Cleanup(func() { updateResultPoll, updateResultWindow = oldPoll, oldWin })
	data := t.TempDir()
	dir := filepath.Join(data, "control-update")
	os.MkdirAll(dir, 0o700)
	pb, _ := json.Marshal(pendingUpdate{Mode: "update", Version: "v9.9.9", From: "v0.0.1", StartedAt: time.Now().UTC()})
	os.WriteFile(filepath.Join(dir, "pending.json"), pb, 0o600)
	e := newTestEnvWith(t, func(c *Config) { c.DataDir = data })
	waitFor(t, "watcher gives up", func() bool { return !e.srv.ctl.resWatching.Load() })
	if n := countAudit(t, e, "control.update_result"); n != 0 {
		t.Fatalf("audited %d", n)
	}
}
