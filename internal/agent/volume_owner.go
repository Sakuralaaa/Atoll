package agent

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Volume ownership.
//
// A service's own volume is a host directory the agent creates as root. Images that run as a
// non-root user (USER in the Dockerfile, or the service's user override) then cannot write
// it, unlike a Docker named volume, which Docker seeds with the image's own ownership. So,
// before each start, the agent hands the volume dir to the user the container runs as:
//
//   - the volume's explicit Owner wins ("uid" or "uid:gid"); otherwise
//   - the service's user override, else the image's USER, resolved against the image's own
//     /etc/passwd and /etc/group when it is a name. Root (or no USER) means nothing to do:
//     images that start as root and drop privileges (postgres, redis, ...) fix their own dirs.
//
// The dir is changed only while nothing in it belongs to anyone but root: an empty dir, or
// one where every entry is still root's (a volume created by an earlier version, where the
// app never managed to write, or files root copied in). Anything owned by another account
// means the app (or its entrypoint) already manages ownership, so it is left alone. That makes
// the fix safe to run on every deploy, and it repairs volumes created before this existed on
// their next deploy or restart.

type uidGid struct{ UID, GID int }

// maxOwnerWalk bounds how many entries fixVolumeOwner inspects; a bigger root-owned tree is
// left alone (it is almost certainly not a volume the app never wrote).
const maxOwnerWalk = 20000

// Hooks for tests (chown to another account needs root).
var (
	rootUID  = 0
	lchownFn = os.Lchown
)

// volumeOwner resolves who the volume dir should belong to. ok=false means leave it.
func (m *Manager) volumeOwner(ctx context.Context, spec api.ResolvedSpec, v api.Volume) (uidGid, bool, error) {
	if v.Owner != "" {
		o, err := parseNumericOwner(v.Owner)
		return o, err == nil, err
	}
	user := strings.TrimSpace(spec.User)
	var imageID string
	if user == "" || needsLookup(user) {
		img, err := m.rt.ImageInfo(ctx, spec.Image)
		if err != nil {
			return uidGid{}, false, fmt.Errorf("inspect image: %w", err)
		}
		imageID = img.ID
		if user == "" {
			user = strings.TrimSpace(img.User)
		}
	}
	if user == "" {
		return uidGid{}, false, nil
	}
	if !needsLookup(user) {
		o, err := parseNumericOwner(user)
		if err != nil {
			return uidGid{}, false, err
		}
		return o, o.UID != rootUID, nil
	}
	key := imageID + " " + user
	m.ownerMu.Lock()
	o, cached := m.owners[key]
	m.ownerMu.Unlock()
	if !cached {
		var err error
		o, err = m.lookupImageUser(ctx, spec.Image, user)
		if err != nil {
			return uidGid{}, false, err
		}
		m.ownerMu.Lock()
		m.owners[key] = o
		m.ownerMu.Unlock()
	}
	return o, o.UID != rootUID, nil
}

// needsLookup reports whether user ("name", "uid", "name:group", "uid:gid", ...) names an
// account or group that has to be resolved, or a uid without a gid (the gid is the
// account's primary group then).
func needsLookup(user string) bool {
	u, g, hasG := strings.Cut(user, ":")
	if !isNum(u) {
		return true
	}
	return hasG && !isNum(g) || !hasG
}

func isNum(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil && s != ""
}

func parseNumericOwner(s string) (uidGid, error) {
	u, g, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		g = u
	}
	uid, err := strconv.Atoi(u)
	if err != nil || uid < 0 {
		return uidGid{}, fmt.Errorf("owner %q: want uid or uid:gid", s)
	}
	gid, err := strconv.Atoi(g)
	if err != nil || gid < 0 {
		return uidGid{}, fmt.Errorf("owner %q: want uid or uid:gid", s)
	}
	return uidGid{uid, gid}, nil
}

// lookupImageUser resolves user against the image's /etc/passwd and /etc/group.
func (m *Manager) lookupImageUser(ctx context.Context, image, user string) (uidGid, error) {
	u, g, hasG := strings.Cut(user, ":")
	var o uidGid
	var passwd []byte
	readPasswd := func() error {
		if passwd != nil {
			return nil
		}
		b, err := m.rt.ImageFile(ctx, image, "/etc/passwd")
		if err != nil {
			return fmt.Errorf("read /etc/passwd from the image: %w", err)
		}
		passwd = b
		return nil
	}
	primary := -1
	if isNum(u) {
		o.UID, _ = strconv.Atoi(u)
		if !hasG {
			// numeric uid without gid: docker uses the account's primary group when the
			// uid has an account, else gid 0
			if err := readPasswd(); err == nil {
				if _, gid, found := findPasswd(passwd, func(name string, uid int) bool { return uid == o.UID }); found {
					primary = gid
				}
			}
			if primary < 0 {
				primary = 0
			}
		}
	} else {
		if err := readPasswd(); err != nil {
			return o, err
		}
		uid, gid, found := findPasswd(passwd, func(name string, _ int) bool { return name == u })
		if !found {
			return o, fmt.Errorf("user %q is not in the image's /etc/passwd", u)
		}
		o.UID, primary = uid, gid
	}
	switch {
	case !hasG:
		o.GID = primary
	case isNum(g):
		o.GID, _ = strconv.Atoi(g)
	default:
		b, err := m.rt.ImageFile(ctx, image, "/etc/group")
		if err != nil {
			return o, fmt.Errorf("read /etc/group from the image: %w", err)
		}
		gid, found := findGroup(b, g)
		if !found {
			return o, fmt.Errorf("group %q is not in the image's /etc/group", g)
		}
		o.GID = gid
	}
	return o, nil
}

// findPasswd returns uid and gid of the first passwd line match accepts.
func findPasswd(b []byte, match func(name string, uid int) bool) (int, int, bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 4 || strings.HasPrefix(f[0], "#") {
			continue
		}
		uid, err1 := strconv.Atoi(f[2])
		gid, err2 := strconv.Atoi(f[3])
		if err1 != nil || err2 != nil {
			continue
		}
		if match(f[0], uid) {
			return uid, gid, true
		}
	}
	return 0, 0, false
}

func findGroup(b []byte, name string) (int, bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 3 || f[0] != name {
			continue
		}
		if gid, err := strconv.Atoi(f[2]); err == nil {
			return gid, true
		}
	}
	return 0, false
}

var errForeignOwner = errors.New("owned by another account")

// fixVolumeOwner hands dir (and, when it only holds root's files, everything in it) to o.
// It reports whether anything changed.
func fixVolumeOwner(dir string, o uidGid) (bool, error) {
	top, err := os.Lstat(dir)
	if err != nil {
		return false, err
	}
	tu, tg, ok := statOwner(top)
	if !ok {
		return false, nil
	}
	if tu == o.UID && tg == o.GID {
		return false, nil // already handed over (or created by the app as itself)
	}
	if tu != rootUID {
		return false, nil // someone already manages it
	}
	var paths []string
	n := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		n++
		if n > maxOwnerWalk {
			return errForeignOwner
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if u, _, ok := statOwner(info); !ok || u != rootUID {
			return errForeignOwner
		}
		paths = append(paths, p)
		return nil
	})
	if errors.Is(err, errForeignOwner) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, p := range paths {
		if err := lchownFn(p, o.UID, o.GID); err != nil {
			return false, err
		}
	}
	if err := lchownFn(dir, o.UID, o.GID); err != nil {
		return false, err
	}
	return true, nil
}

func statOwner(fi fs.FileInfo) (int, int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}
