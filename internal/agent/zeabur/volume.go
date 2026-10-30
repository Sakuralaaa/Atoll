package zeabur

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

type VolumeResult struct {
	Method string `json:"method"` // "rsync" | "copy"
	Bytes  int64  `json:"bytes"`
	Files  int    `json:"files"`
	Dest   string `json:"dest"`
}

func (t *Takeover) checkSource(dir string) (string, error) {
	clean := filepath.Clean(dir)
	root := filepath.Clean(t.StorageRoot)
	if !strings.HasPrefix(clean, root+string(filepath.Separator)) || strings.Contains(strings.TrimPrefix(clean, root+"/"), "/") {
		return "", fmt.Errorf("host_dir %q must be a PVC directory directly under %s", dir, root)
	}
	fi, err := os.Stat(clean)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory", clean)
	}
	return clean, nil
}

// CopyVolume mirrors a PVC dir into the atoll volume dir. Repeat runs only transfer changes
// and delete files removed at the source, so the last run before cutover is the final sync.
func (t *Takeover) CopyVolume(ctx context.Context, p api.ZeaburVolumePayload) (VolumeResult, error) {
	src, err := t.checkSource(p.HostDir)
	if err != nil {
		return VolumeResult{}, err
	}
	if p.ServiceID == "" || p.Volume == "" || strings.ContainsAny(p.ServiceID+p.Volume, "/\\") || p.Volume == ".." || p.ServiceID == ".." {
		return VolumeResult{}, errors.New("valid service_id and volume are required")
	}
	dst := t.Paths.VolumeDir(p.ServiceID, p.Volume)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return VolumeResult{}, err
	}
	res := VolumeResult{Dest: dst}
	if rsync, err := t.X.LookPath("rsync"); err == nil {
		res.Method = "rsync"
		if err := t.X.Stream(ctx, nil, io.Discard, rsync, "-aH", "--numeric-ids", "--delete", src+"/", dst+"/"); err != nil {
			return res, err
		}
	} else {
		res.Method = "copy"
		if err := MirrorDir(src, dst); err != nil {
			return res, err
		}
	}
	_ = filepath.WalkDir(dst, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				res.Bytes += fi.Size()
				res.Files++
			}
		}
		return nil
	})
	return res, nil
}

// MirrorDir makes dst an exact copy of src (like rsync -a --delete): modes, owners, mtimes and
// symlinks preserved; unchanged files (same size+mtime) are skipped.
func MirrorDir(src, dst string) error {
	seen := map[string]bool{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		seen[rel] = true
		target := filepath.Join(dst, rel)
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case fi.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			// metadata for dirs applied after the walk (children change mtime)
			return nil
		case fi.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if cur, err := os.Readlink(target); err == nil && cur == link {
				return nil
			}
			_ = os.RemoveAll(target)
			if err := os.Symlink(link, target); err != nil {
				return err
			}
			chownL(target, fi)
			return nil
		case fi.Mode().IsRegular():
			if tfi, err := os.Lstat(target); err == nil && tfi.Mode().IsRegular() && tfi.Size() == fi.Size() && tfi.ModTime().Equal(fi.ModTime()) {
				return nil
			}
			if err := copyFile(p, target, fi); err != nil {
				return err
			}
			return nil
		default:
			return nil // sockets/fifos/devices are not copied
		}
	})
	if err != nil {
		return err
	}
	// delete extraneous entries (deepest first)
	var extra []string
	_ = filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dst, p)
		if !seen[rel] {
			extra = append(extra, p)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	for _, p := range extra {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	// directory metadata, deepest first
	var dirs []string
	_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		fi, err := os.Lstat(dirs[i])
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(src, dirs[i])
		target := filepath.Join(dst, rel)
		chownL(target, fi)
		_ = os.Chmod(target, fi.Mode().Perm()|fi.Mode()&(os.ModeSetgid|os.ModeSticky|os.ModeSetuid))
		_ = os.Chtimes(target, fi.ModTime(), fi.ModTime())
	}
	return nil
}

func copyFile(src, dst string, fi os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := filepath.Join(filepath.Dir(dst), ".atoll-copy-"+filepath.Base(dst))
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	chownL(tmp, fi)
	if err := os.Chmod(tmp, fi.Mode().Perm()|fi.Mode()&(os.ModeSetgid|os.ModeSticky|os.ModeSetuid)); err != nil {
		return err
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return os.Chtimes(dst, fi.ModTime(), fi.ModTime())
}

func chownL(p string, fi os.FileInfo) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		_ = os.Lchown(p, int(st.Uid), int(st.Gid))
	}
}
