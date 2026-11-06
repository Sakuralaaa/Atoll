package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// ownerRT is a minimal Runtime for volumeOwner: only ImageInfo and ImageFile are used.
type ownerRT struct {
	Runtime
	mu    sync.Mutex
	user  string
	files map[string]string
	reads int
}

func (r *ownerRT) ImageInfo(_ context.Context, ref string) (ImageInfo, error) {
	return ImageInfo{ID: "sha256:" + ref, User: r.user}, nil
}

func (r *ownerRT) ImageFile(_ context.Context, _, path string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	c, ok := r.files[path]
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(c), nil
}

const testPasswd = "root:x:0:0:root:/root:/bin/sh\nnode:x:1000:1000::/home/node:/bin/sh\nzcgw:x:100:101:Linux User,,,:/home/zcgw:/sbin/nologin\n"
const testGroup = "root:x:0:\nstaff:x:50:\nzcgw:x:101:zcgw\n"

func TestVolumeOwnerResolution(t *testing.T) {
	cases := []struct {
		name, imgUser, svcUser, volOwner string
		want                             uidGid
		ok                               bool
	}{
		{"no user is root", "", "", "", uidGid{}, false},
		{"root by name", "root", "", "", uidGid{}, false},
		{"root by uid", "0:0", "", "", uidGid{}, false},
		{"image user name", "zcgw", "", "", uidGid{100, 101}, true},
		{"image uid only takes primary group", "1000", "", "", uidGid{1000, 1000}, true},
		{"image uid without account gets gid 0", "4242", "", "", uidGid{4242, 0}, true},
		{"name with group name", "zcgw:staff", "", "", uidGid{100, 50}, true},
		{"numeric pair", "1001:1002", "", "", uidGid{1001, 1002}, true},
		{"service user overrides image", "zcgw", "node", "", uidGid{1000, 1000}, true},
		{"service runs as root", "zcgw", "0", "", uidGid{}, false},
		{"explicit volume owner wins", "zcgw", "", "33:33", uidGid{33, 33}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rt := &ownerRT{user: c.imgUser, files: map[string]string{"/etc/passwd": testPasswd, "/etc/group": testGroup}}
			m := NewManager(rt, Paths{Root: t.TempDir()}, nil, nil)
			got, ok, err := m.volumeOwner(context.Background(), api.ResolvedSpec{Image: "img:" + c.name, User: c.svcUser}, api.Volume{Name: "data", Owner: c.volOwner})
			if err != nil {
				t.Fatal(err)
			}
			if ok != c.ok || (ok && got != c.want) {
				t.Fatalf("got %v ok=%v, want %v ok=%v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestVolumeOwnerUnknownUserAndCache(t *testing.T) {
	rt := &ownerRT{user: "ghost", files: map[string]string{"/etc/passwd": testPasswd}}
	m := NewManager(rt, Paths{Root: t.TempDir()}, nil, nil)
	if _, _, err := m.volumeOwner(context.Background(), api.ResolvedSpec{Image: "a"}, api.Volume{}); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("want unknown user error, got %v", err)
	}
	rt.user = "zcgw"
	for range 3 {
		if o, ok, err := m.volumeOwner(context.Background(), api.ResolvedSpec{Image: "b"}, api.Volume{}); err != nil || !ok || o != (uidGid{100, 101}) {
			t.Fatalf("got %v %v %v", o, ok, err)
		}
	}
	if rt.reads != 2 { // one failed lookup for "ghost", then one for "zcgw", cached after
		t.Fatalf("passwd read %d times, want 2", rt.reads)
	}
}

// withOwnerHooks treats the test's own uid as "root" and records chowns instead of doing them.
func withOwnerHooks(t *testing.T) *[]string {
	t.Helper()
	var got []string
	oldRoot, oldFn := rootUID, lchownFn
	rootUID = os.Getuid()
	lchownFn = func(p string, uid, gid int) error {
		got = append(got, p)
		return nil
	}
	t.Cleanup(func() { rootUID, lchownFn = oldRoot, oldFn })
	return &got
}

func TestParseNumericOwner(t *testing.T) {
	for _, bad := range []string{"abc", "", "1:x", "-1"} {
		if _, err := parseNumericOwner(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if o, err := parseNumericOwner("7"); err != nil || o != (uidGid{7, 7}) {
		t.Fatalf("got %v %v", o, err)
	}
}

func TestFixVolumeOwner(t *testing.T) {
	target := uidGid{os.Getuid() + 1000, os.Getgid() + 1000}

	t.Run("empty dir", func(t *testing.T) {
		got := withOwnerHooks(t)
		dir := t.TempDir()
		changed, err := fixVolumeOwner(dir, target)
		if err != nil || !changed || len(*got) != 1 || (*got)[0] != dir {
			t.Fatalf("changed=%v err=%v chowns=%v", changed, err, *got)
		}
	})

	t.Run("root-only tree is handed over whole", func(t *testing.T) {
		got := withOwnerHooks(t)
		dir := t.TempDir()
		_ = os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
		_ = os.WriteFile(filepath.Join(dir, "sub", "accounts.json"), []byte("[]"), 0o600)
		_ = os.Symlink("sub/accounts.json", filepath.Join(dir, "link"))
		changed, err := fixVolumeOwner(dir, target)
		if err != nil || !changed || len(*got) != 4 {
			t.Fatalf("changed=%v err=%v chowns=%v", changed, err, *got)
		}
	})

	t.Run("already owned by the target", func(t *testing.T) {
		got := withOwnerHooks(t)
		dir := t.TempDir()
		changed, err := fixVolumeOwner(dir, uidGid{os.Getuid(), os.Getgid()})
		if err != nil || changed || len(*got) != 0 {
			t.Fatalf("changed=%v err=%v chowns=%v", changed, err, *got)
		}
	})

	t.Run("dir someone else manages is left alone", func(t *testing.T) {
		got := withOwnerHooks(t)
		rootUID = os.Getuid() + 1 // the dir's owner is then "another account"
		dir := t.TempDir()
		changed, err := fixVolumeOwner(dir, target)
		if err != nil || changed || len(*got) != 0 {
			t.Fatalf("changed=%v err=%v chowns=%v", changed, err, *got)
		}
	})
}
