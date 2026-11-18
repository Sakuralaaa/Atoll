package agent

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// DiskMeter measures how much disk each service uses: every dir under its volumes dir and the
// writable layer of its container. It only reads (stat), never changes data, and runs one
// measurement at a time so a slow disk is never walked twice in parallel.
type DiskMeter struct {
	Paths Paths
	RT    Runtime
	// Specs lists the services this node runs (nil = only dirs under the volumes root).
	Specs func() []api.ResolvedSpec
	mu    sync.Mutex
}

// rootfsSizer is implemented by runtimes that can report a container's writable layer.
type rootfsSizer interface {
	ContainerSizeRw(ctx context.Context, name string) (int64, error)
}

// Measure returns the usage of the given services (all known services when ids is empty).
func (d *DiskMeter) Measure(ctx context.Context, ids []string) api.DiskUsagePayload {
	d.mu.Lock()
	defer d.mu.Unlock()
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	set := map[string]bool{}
	if d.Specs != nil {
		for _, s := range d.Specs() {
			set[s.ServiceID] = true
		}
	}
	if ents, err := os.ReadDir(filepath.Join(d.Paths.Root, "volumes")); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				set[e.Name()] = true
			}
		}
	}
	var sids []string
	for id := range set {
		if len(want) == 0 || want[id] {
			sids = append(sids, id)
		}
	}
	sort.Strings(sids)
	out := api.DiskUsagePayload{At: time.Now().UTC(), Services: []api.ServiceDiskSample{}}
	sizer, _ := d.RT.(rootfsSizer)
	for _, sid := range sids {
		if ctx.Err() != nil {
			break
		}
		smp := api.ServiceDiskSample{ServiceID: sid, Volumes: []api.VolumeUsage{}, RootfsBytes: -1}
		if ents, err := os.ReadDir(d.Paths.VolumesDir(sid)); err == nil {
			for _, e := range ents {
				if !e.IsDir() {
					continue
				}
				smp.Volumes = append(smp.Volumes, api.VolumeUsage{Name: e.Name(), Bytes: DirSize(ctx, filepath.Join(d.Paths.VolumesDir(sid), e.Name()))})
			}
		}
		if sizer != nil {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if n, err := sizer.ContainerSizeRw(cctx, ContainerName(sid)); err == nil {
				smp.RootfsBytes = n
			}
			cancel()
		}
		out.Services = append(out.Services, smp)
	}
	return out
}

// DirSize is the disk space a directory tree takes: allocated blocks (sparse files count what
// they use), each hard-linked inode once, symlinks not followed, other filesystems mounted
// inside not entered. Unreadable entries are skipped.
func DirSize(ctx context.Context, root string) int64 {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return 0
	}
	var dev uint64
	if st, ok := rootInfo.Sys().(*syscall.Stat_t); ok {
		dev = uint64(st.Dev)
	}
	type ino struct{ dev, ino uint64 }
	seen := map[ino]bool{}
	var total int64
	n := 0
	_ = filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if n++; n%4096 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := e.Info()
		if err != nil {
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			total += info.Size()
			return nil
		}
		if e.IsDir() && path != root && uint64(st.Dev) != dev {
			return filepath.SkipDir
		}
		if st.Nlink > 1 && !e.IsDir() {
			k := ino{uint64(st.Dev), uint64(st.Ino)}
			if seen[k] {
				return nil
			}
			seen[k] = true
		}
		total += int64(st.Blocks) * 512
		return nil
	})
	return total
}

// diskLoop sends every service's usage every api.DiskUsageEvery while connected (first one
// shortly after start).
func (a *Agent) diskLoop(ctx context.Context, first time.Duration) {
	if a.Disk == nil {
		return
	}
	every := a.DiskEvery
	if every <= 0 {
		every = api.DiskUsageEvery
	}
	wait := first
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = every
		a.connMu.Lock()
		connected := a.conn != nil
		a.connMu.Unlock()
		if !connected {
			continue
		}
		a.Emit(api.MsgDiskUsage, a.Disk.Measure(ctx, nil))
	}
}
