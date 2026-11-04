package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HeaderSha256 carries the hex sha256 of the tar.gz body of a volume transfer.
const HeaderSha256 = "X-Atoll-Sha256"

// VolumeTransfer streams volume dirs to/from the server's blob relay.
type VolumeTransfer struct {
	rt    Runtime
	paths Paths
	HTTP  *http.Client
	// Header adds auth headers to blob requests (the node bearer token).
	Header http.Header
}

func NewVolumeTransfer(rt Runtime, p Paths, token string) *VolumeTransfer {
	h := http.Header{}
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	return &VolumeTransfer{rt: rt, paths: p, HTTP: &http.Client{Timeout: 6 * time.Hour}, Header: h}
}

func (v *VolumeTransfer) ensureStopped(ctx context.Context, serviceID string) error {
	info, err := v.rt.InspectContainer(ctx, ContainerName(serviceID))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.State == "running" || info.State == "restarting" {
		return fmt.Errorf("service %s must be stopped before a volume transfer (state %s)", serviceID, info.State)
	}
	return nil
}

func (v *VolumeTransfer) volumeDir(serviceID, volume string) (string, error) {
	if err := validID(serviceID); err != nil {
		return "", err
	}
	if err := validVolumeName(volume); err != nil {
		return "", err
	}
	if spec, err := NewStateStore(v.paths).Load(serviceID); err == nil {
		for _, vol := range spec.Volumes {
			if vol.Name == volume && vol.HostPath != "" {
				return "", ErrHostPathVolumes(serviceID)
			}
		}
	}
	return v.paths.VolumeDir(serviceID, volume), nil
}

// Export tars+gzips the volume to a temp file (to know size and sha256 up front), then PUTs it.
func (v *VolumeTransfer) Export(ctx context.Context, serviceID, volume, url string) (string, error) {
	dir, err := v.volumeDir(serviceID, volume)
	if err != nil {
		return "", err
	}
	if err := v.ensureStopped(ctx, serviceID); err != nil {
		return "", err
	}
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("volume %s/%s: %w", serviceID, volume, err)
	}
	if err := os.MkdirAll(v.paths.TmpDir(), 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(v.paths.TmpDir(), "export-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	if err := TarGz(dir, io.MultiWriter(f, h)); err != nil {
		return "", err
	}
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, f)
	if err != nil {
		return "", err
	}
	for k, vs := range v.Header {
		req.Header[k] = vs
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/gzip")
	req.Header.Set(HeaderSha256, sum)
	resp, err := v.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("upload: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return fmt.Sprintf(`{"bytes":%d,"sha256":%q}`, size, sum), nil
}

// Import downloads a tar.gz, verifies sha256 (if the response carries X-Atoll-Sha256) and
// Content-Length, extracts it to a staging dir and swaps it in place of the volume dir.
func (v *VolumeTransfer) Import(ctx context.Context, serviceID, volume, url string) (string, error) {
	dir, err := v.volumeDir(serviceID, volume)
	if err != nil {
		return "", err
	}
	if err := v.ensureStopped(ctx, serviceID); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	for k, vs := range v.Header {
		req.Header[k] = vs
	}
	resp, err := v.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	if err := os.MkdirAll(v.paths.TmpDir(), 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(v.paths.TmpDir(), "import-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), resp.Body)
	if err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if want := resp.Header.Get(HeaderSha256); want != "" && !strings.EqualFold(want, sum) {
		return "", fmt.Errorf("sha256 mismatch: got %s want %s", sum, want)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if want, _ := strconv.ParseInt(cl, 10, 64); want > 0 && want != n {
			return "", fmt.Errorf("size mismatch: got %d want %d", n, want)
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, "."+volume+".importing-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	if err := UntarGz(f, staging); err != nil {
		return "", err
	}
	if err := os.Chmod(staging, 0o755); err != nil {
		return "", err
	}
	backup := dir + ".pre-import"
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(dir); err == nil {
		if err := os.Rename(dir, backup); err != nil {
			return "", err
		}
	}
	if err := os.Rename(staging, dir); err != nil {
		_ = os.Rename(backup, dir)
		return "", err
	}
	_ = os.RemoveAll(backup)
	return fmt.Sprintf(`{"bytes":%d,"sha256":%q}`, n, sum), nil
}

// TarGz writes dir's contents (relative paths, owners/modes/symlinks preserved) as tar.gz.
func TarGz(dir string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == "." {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if fi.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		if !fi.Mode().IsRegular() && !fi.IsDir() && link == "" {
			return nil // skip sockets, fifos, devices
		}
		hdr, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if fi.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uname, hdr.Gname = "", "" // numeric ids only (FileInfoHeader fills Uid/Gid)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(tw, f)
			f.Close()
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// UntarGz extracts into dest, rejecting entries that would escape it.
func UntarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	type dirMeta struct {
		name string
		hdr  *tar.Header
	}
	var dirs []dirMeta
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(hdr.Name))
		if name == "." {
			continue
		}
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path in archive: %q", hdr.Name)
		}
		mode := fs.FileMode(hdr.Mode).Perm() | fs.FileMode(hdr.Mode)&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return err
			}
			dirs = append(dirs, dirMeta{name, hdr})
		case tar.TypeReg:
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
			applyMeta(root, name, hdr, mode)
		case tar.TypeSymlink:
			if err := root.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			// os.Root never follows symlinks out of dest, so storing arbitrary targets is safe.
			if err := root.Symlink(hdr.Linkname, name); err != nil {
				return err
			}
			_ = root.Lchown(name, hdr.Uid, hdr.Gid)
		case tar.TypeLink:
			target := filepath.Clean(filepath.FromSlash(hdr.Linkname))
			if filepath.IsAbs(target) || strings.HasPrefix(target, "..") {
				return fmt.Errorf("unsafe hardlink in archive: %q", hdr.Linkname)
			}
			if err := root.Link(target, name); err != nil {
				return err
			}
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		applyMeta(root, d.name, d.hdr, fs.FileMode(d.hdr.Mode).Perm())
	}
	return nil
}

func applyMeta(root *os.Root, name string, hdr *tar.Header, mode fs.FileMode) {
	_ = root.Lchown(name, hdr.Uid, hdr.Gid) // works when running as root (the agent does)
	_ = root.Chmod(name, mode)
	if !hdr.ModTime.IsZero() {
		_ = root.Chtimes(name, hdr.ModTime, hdr.ModTime)
	}
}
