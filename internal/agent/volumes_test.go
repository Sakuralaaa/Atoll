package agent_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
)

// blobRelay is a fake /agent/v1/blobs/{token} endpoint.
type blobRelay struct {
	mu      sync.Mutex
	body    []byte
	sum     string
	auth    string
	corrupt bool // serve a wrong sha256 header
}

func (b *blobRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.auth = r.Header.Get("Authorization")
	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		h := sha256.Sum256(body)
		if got := hex.EncodeToString(h[:]); got != r.Header.Get(agent.HeaderSha256) {
			http.Error(w, "sha mismatch", http.StatusBadRequest)
			return
		}
		if r.ContentLength != int64(len(body)) {
			http.Error(w, "length", http.StatusBadRequest)
			return
		}
		b.body, b.sum = body, r.Header.Get(agent.HeaderSha256)
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		sum := b.sum
		if b.corrupt {
			sum = strings.Repeat("0", 64)
		}
		w.Header().Set(agent.HeaderSha256, sum)
		w.Write(b.body)
	}
}

func writeTree(t *testing.T, dir string) {
	t.Helper()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o750))
	must(os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o640))
	must(os.WriteFile(filepath.Join(dir, "sub", "deep", "b.bin"), bytes.Repeat([]byte{1, 2, 3}, 100000), 0o600))
	must(os.Symlink("a.txt", filepath.Join(dir, "link")))
}

func TestVolumeExportImportRoundTrip(t *testing.T) {
	relay := &blobRelay{}
	srv := httptest.NewServer(relay)
	defer srv.Close()
	f := agenttest.New()
	p := agent.Paths{Root: t.TempDir()}
	vt := agent.NewVolumeTransfer(f, p, "node-token")
	ctx := context.Background()

	writeTree(t, p.VolumeDir("svc_a", "data"))
	out, err := vt.Export(ctx, "svc_a", "data", srv.URL+"/agent/v1/blobs/t1")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Bytes  int64  `json:"bytes"`
		Sha256 string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Sha256 != relay.sum || res.Bytes != int64(len(relay.body)) {
		t.Fatalf("export result %s (relay %d %s)", out, len(relay.body), relay.sum)
	}
	if relay.auth != "Bearer node-token" {
		t.Fatalf("auth %q", relay.auth)
	}

	// Import into another service whose volume already has stale content.
	dst := p.VolumeDir("svc_b", "data")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(dst, "stale"), []byte("x"), 0o644)
	out, err = vt.Import(ctx, "svc_b", "data", srv.URL+"/agent/v1/blobs/t1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, relay.sum) {
		t.Fatalf("import result %s", out)
	}
	if _, err := os.Stat(filepath.Join(dst, "stale")); !os.IsNotExist(err) {
		t.Fatal("import must replace the volume, not merge")
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "a.txt")); string(b) != "alpha" {
		t.Fatalf("a.txt %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "sub", "deep", "b.bin")); len(b) != 300000 {
		t.Fatalf("b.bin %d", len(b))
	}
	if l, err := os.Readlink(filepath.Join(dst, "link")); err != nil || l != "a.txt" {
		t.Fatalf("symlink %q %v", l, err)
	}
	if fi, _ := os.Stat(filepath.Join(dst, "a.txt")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(dst, "sub")); fi.Mode().Perm() != 0o750 {
		t.Fatalf("dir mode %v", fi.Mode())
	}
	if m, _ := filepath.Glob(filepath.Join(p.VolumesDir("svc_b"), ".data.importing-*")); len(m) != 0 {
		t.Fatalf("staging left behind: %v", m)
	}
	if _, err := os.Stat(dst + ".pre-import"); !os.IsNotExist(err) {
		t.Fatal("backup left behind")
	}

	// Corrupted download: rejected, existing volume untouched.
	relay.corrupt = true
	if _, err := vt.Import(ctx, "svc_b", "data", srv.URL+"/agent/v1/blobs/t1"); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("corrupt: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "a.txt")); string(b) != "alpha" {
		t.Fatal("volume damaged by a failed import")
	}
}

func TestVolumeTransferRefusals(t *testing.T) {
	f := agenttest.New()
	p := agent.Paths{Root: t.TempDir()}
	vt := agent.NewVolumeTransfer(f, p, "")
	ctx := context.Background()
	for _, c := range [][2]string{{"../x", "data"}, {"svc_a", ".."}, {"svc_a", "a/b"}, {"", "data"}} {
		if _, err := vt.Export(ctx, c[0], c[1], "http://127.0.0.1:1/x"); err == nil {
			t.Fatalf("export accepted %q/%q", c[0], c[1])
		}
		if _, err := vt.Import(ctx, c[0], c[1], "http://127.0.0.1:1/x"); err == nil {
			t.Fatalf("import accepted %q/%q", c[0], c[1])
		}
	}
	// Running service: refuse (data would be inconsistent).
	f.EnsureNetwork(ctx, "n")
	f.CreateContainer(ctx, agent.ContainerSpec{Name: agent.ContainerName("svc_run"), Image: "x", Networks: []agent.NetworkAttach{{Name: "n"}}})
	f.StartContainer(ctx, agent.ContainerName("svc_run"))
	os.MkdirAll(p.VolumeDir("svc_run", "data"), 0o755)
	if _, err := vt.Export(ctx, "svc_run", "data", "http://127.0.0.1:1/x"); err == nil || !strings.Contains(err.Error(), "must be stopped") {
		t.Fatalf("running export: %v", err)
	}
}

func tgz(t *testing.T, entries []tar.Header, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(content))
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(content))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestUntarRejectsPathTraversal(t *testing.T) {
	cases := map[string][]tar.Header{
		"dotdot":        {{Name: "../evil", Typeflag: tar.TypeReg}},
		"nested dotdot": {{Name: "a/../../evil", Typeflag: tar.TypeReg}},
		"absolute":      {{Name: "/etc/evil", Typeflag: tar.TypeReg}},
		"hardlink out":  {{Name: "h", Typeflag: tar.TypeLink, Linkname: "../../etc/passwd"}},
		"symlink then write through": {
			{Name: "s", Typeflag: tar.TypeSymlink, Linkname: ".."},
			{Name: "s/evil", Typeflag: tar.TypeReg},
		},
	}
	for name, hdrs := range cases {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			dest := filepath.Join(parent, "vol")
			os.MkdirAll(dest, 0o755)
			err := agent.UntarGz(bytes.NewReader(tgz(t, hdrs, "pwned")), dest)
			if err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, err := os.Stat(filepath.Join(parent, "evil")); err == nil {
				t.Fatal("file written outside dest")
			}
		})
	}
	// A symlink pointing outside is stored as a link (harmless inside the volume) but never followed.
	dest := t.TempDir()
	if err := agent.UntarGz(bytes.NewReader(tgz(t, []tar.Header{{Name: "out", Typeflag: tar.TypeSymlink, Linkname: "/etc"}}, "")), dest); err != nil {
		t.Fatal(err)
	}
	if l, _ := os.Readlink(filepath.Join(dest, "out")); l != "/etc" {
		t.Fatalf("link %q", l)
	}
}

func TestImportRejectsTraversalArchive(t *testing.T) {
	bad := tgz(t, []tar.Header{{Name: "ok.txt", Typeflag: tar.TypeReg}, {Name: "../../escape", Typeflag: tar.TypeReg}}, "x")
	h := sha256.Sum256(bad)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(agent.HeaderSha256, hex.EncodeToString(h[:]))
		w.Write(bad)
	}))
	defer srv.Close()
	p := agent.Paths{Root: t.TempDir()}
	vt := agent.NewVolumeTransfer(agenttest.New(), p, "")
	dst := p.VolumeDir("svc_a", "data")
	os.MkdirAll(dst, 0o755)
	os.WriteFile(filepath.Join(dst, "keep"), []byte("1"), 0o644)
	if _, err := vt.Import(context.Background(), "svc_a", "data", srv.URL); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "keep")); err != nil {
		t.Fatal("existing volume replaced by a rejected archive")
	}
	if _, err := os.Stat(filepath.Join(p.Root, "volumes", "escape")); err == nil {
		t.Fatal("escaped")
	}
}
