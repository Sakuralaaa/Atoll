package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Restore replaces the local service's data with the given snapshots (of SourceServiceID,
// which differs from the target after a disaster-recovery move). The current data dir is
// moved aside (one generation, backup/pre-restore/<service>) and put back if the restore
// fails. Databases with a dump are re-initialised empty by their image and then loaded
// from the dump; everything else gets its volume dirs restored file by file.
func (m *Manager) Restore(ctx context.Context, p api.BackupRestorePayload) (api.BackupRestoreResult, error) {
	var res api.BackupRestoreResult
	t := p.Target
	if err := validTarget(t); err != nil {
		return res, err
	}
	if p.SourceServiceID == "" {
		p.SourceServiceID = t.ServiceID
	}
	if err := validTarget(api.BackupTarget{ServiceID: p.SourceServiceID}); err != nil {
		return res, err
	}
	var dump, vols *api.BackupSnapshot
	for i := range p.Snapshots {
		s := &p.Snapshots[i]
		if s.Pruned || s.ID == "" {
			continue
		}
		switch s.Part {
		case "dump":
			dump = s
		case "volumes":
			vols = s
		}
	}
	useDump := dump != nil && DBKinds[t.Kind]
	if !useDump && vols == nil {
		return res, errors.New("backup has no restorable snapshot for this service")
	}
	m.runMu.Lock()
	defer m.runMu.Unlock()
	if err := m.ensureRepo(ctx, p.Repo); err != nil {
		return res, err
	}
	m.Log.Info("restore started", "service", t.ServiceID, "source", p.SourceServiceID, "repo", p.Repo)
	err := m.Locked(t.ServiceID, func() error {
		spec, err := agent.NewStateStore(m.Paths).Load(t.ServiceID)
		if err != nil {
			return fmt.Errorf("service %s is not deployed on this server", t.ServiceID)
		}
		if agent.HasHostPath(spec) {
			return agent.ErrHostPathVolumes(t.ServiceID)
		}
		name := agent.ContainerName(t.ServiceID)
		if err := m.RT.StopContainer(ctx, name, m.StopTimeout); err != nil && !errors.Is(err, agent.ErrNotFound) {
			return fmt.Errorf("stop: %w", err)
		}
		aside, err := m.moveAside(spec)
		if err != nil {
			return err
		}
		res.MovedAside = aside
		rollback := func(cause error) error {
			m.RT.StopContainer(context.Background(), name, m.StopTimeout)
			if aside != "" {
				os.RemoveAll(m.Paths.VolumesDir(t.ServiceID))
				if rerr := os.Rename(aside, m.Paths.VolumesDir(t.ServiceID)); rerr == nil {
					res.MovedAside = ""
					cause = fmt.Errorf("%w (previous data put back)", cause)
				}
			}
			if !spec.Stopped {
				m.RT.StartContainer(context.Background(), name)
			}
			return cause
		}
		if useDump {
			if vols != nil {
				res.Warnings = append(res.Warnings, "volume snapshot not applied: the database is restored from its logical dump")
			}
			n, err := m.restoreDump(ctx, p, *dump, spec)
			if err != nil {
				return rollback(err)
			}
			res.Restored, res.Bytes = append(res.Restored, "dump"), n
		} else {
			if err := m.restoreVolumes(ctx, p, *vols); err != nil {
				return rollback(err)
			}
			res.Restored, res.Bytes = append(res.Restored, "volumes"), vols.Bytes
			if !spec.Stopped {
				if err := m.RT.StartContainer(ctx, name); err != nil {
					return fmt.Errorf("start: %w", err)
				}
			}
		}
		if spec.Stopped {
			m.RT.StopContainer(ctx, name, m.StopTimeout)
		}
		return nil
	})
	if err != nil {
		m.Log.Warn("restore failed", "service", t.ServiceID, "err", err)
		return res, err
	}
	m.Log.Info("restore done", "service", t.ServiceID, "parts", res.Restored)
	return res, nil
}

// moveAside renames the service's volumes dir out of the way (replacing an older one) and
// recreates empty volume dirs with the previous owner and mode.
func (m *Manager) moveAside(spec api.ResolvedSpec) (string, error) {
	src := m.Paths.VolumesDir(spec.ServiceID)
	type own struct {
		uid, gid int
		mode     os.FileMode
	}
	owners := map[string]own{}
	for _, v := range spec.Volumes {
		if fi, err := os.Stat(filepath.Join(src, v.Name)); err == nil {
			o := own{uid: -1, gid: -1, mode: fi.Mode().Perm()}
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				o.uid, o.gid = int(st.Uid), int(st.Gid)
			}
			owners[v.Name] = o
		}
	}
	aside := ""
	if _, err := os.Stat(src); err == nil {
		aside = m.asideDir(spec.ServiceID)
		if err := os.RemoveAll(aside); err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(aside), 0o700); err != nil {
			return "", err
		}
		if err := os.Rename(src, aside); err != nil {
			return "", fmt.Errorf("move current data aside: %w", err)
		}
	}
	for _, v := range spec.Volumes {
		d := filepath.Join(src, v.Name)
		o, ok := owners[v.Name]
		mode := os.FileMode(0o755)
		if ok {
			mode = o.mode
		}
		if err := os.MkdirAll(d, mode); err != nil {
			return aside, err
		}
		if ok && o.uid >= 0 {
			os.Chown(d, o.uid, o.gid)
		}
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		return aside, err
	}
	return aside, nil
}

// RestoreVolumeArgs is the restic command restoring a source service's volume snapshot into
// the target service's volumes dir (mounted at /backup/<target>).
func RestoreVolumeArgs(snapshotID, sourceService, targetService string) []string {
	return []string{"restore", snapshotID + ":/backup/" + sourceService, "--target", "/backup/" + targetService}
}

func (m *Manager) restoreVolumes(ctx context.Context, p api.BackupRestorePayload, sn api.BackupSnapshot) error {
	t := p.Target
	mount := m.Paths.VolumesDir(t.ServiceID) + ":/backup/" + t.ServiceID
	if err := m.restic(ctx, p.Repo, nil, nil, []string{mount}, RestoreVolumeArgs(sn.ID, p.SourceServiceID, t.ServiceID)...); err != nil {
		return fmt.Errorf("restore volumes: %w", err)
	}
	return nil
}

func (m *Manager) restoreDump(ctx context.Context, p api.BackupRestorePayload, sn api.BackupSnapshot, spec api.ResolvedSpec) (int64, error) {
	t := p.Target
	name := agent.ContainerName(t.ServiceID)
	if t.Kind == "redis" {
		return m.restoreRedis(ctx, p, sn, spec)
	}
	script, err := RestoreScript(t.Kind)
	if err != nil {
		return 0, err
	}
	// fresh, empty data dir: the image initialises the database with the service's env
	if err := m.RT.StartContainer(ctx, name); err != nil {
		return 0, fmt.Errorf("start: %w", err)
	}
	if err := m.waitReady(ctx, name, t.Kind); err != nil {
		return 0, err
	}
	return m.loadDump(ctx, p.Repo, name, t.Kind, script, sn.ID)
}

// loadDump streams `restic dump` of a dump snapshot into script (RestoreScript) running in
// container name. It returns the dump bytes read from the repository.
func (m *Manager) loadDump(ctx context.Context, repo api.BackupRepo, name, kind, script, snapshotID string) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	cw := &countWriter{w: pw}
	dumpErr := make(chan error, 1)
	go func() {
		args := append([]string{"dump", snapshotID, DumpFile(kind)}, m.retryLock()...)
		err := m.restic(ctx, repo, nil, cw, nil, args...)
		pw.CloseWithError(err)
		dumpErr <- err
	}()
	var errb bytes.Buffer
	code, xerr := m.RT.Exec(ctx, name, []string{"sh", "-c", script}, pr, io.Discard, &errb)
	pr.CloseWithError(errors.New("load finished"))
	if xerr != nil || code != 0 {
		cancel()
	}
	derr := <-dumpErr
	switch {
	case derr != nil && !errors.Is(derr, context.Canceled):
		return cw.n.Load(), fmt.Errorf("read dump from repository: %w", derr)
	case xerr != nil:
		return cw.n.Load(), fmt.Errorf("load dump: %w", xerr)
	case code != 0:
		return cw.n.Load(), fmt.Errorf("load dump exited %d: %s", code, lastLines(errb.String(), 4, 1000))
	}
	return cw.n.Load(), nil
}

func (m *Manager) restoreRedis(ctx context.Context, p api.BackupRestorePayload, sn api.BackupSnapshot, spec api.ResolvedSpec) (int64, error) {
	var dataDir string
	for _, v := range spec.Volumes {
		if v.MountPath == "/data" {
			dataDir = m.Paths.VolumeDir(spec.ServiceID, v.Name)
		}
	}
	if dataDir == "" {
		return 0, errors.New("redis service has no volume mounted at /data")
	}
	tmp := filepath.Join(dataDir, ".atoll-restore.rdb.tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	cw := &countWriter{w: f}
	err = m.restic(ctx, p.Repo, nil, cw, nil, "dump", sn.ID, DumpFile("redis"))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return cw.n.Load(), fmt.Errorf("read dump from repository: %w", err)
	}
	if cw.n.Load() < 9 {
		os.Remove(tmp)
		return cw.n.Load(), errors.New("rdb in snapshot is empty")
	}
	if fi, err := os.Stat(dataDir); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			os.Chown(tmp, int(st.Uid), int(st.Gid))
		}
	}
	if err := os.Rename(tmp, filepath.Join(dataDir, "dump.rdb")); err != nil {
		return cw.n.Load(), err
	}
	if !spec.Stopped {
		if err := m.RT.StartContainer(ctx, agent.ContainerName(spec.ServiceID)); err != nil {
			return cw.n.Load(), fmt.Errorf("start: %w", err)
		}
	}
	return cw.n.Load(), nil
}

// waitReady polls the readiness script; database images restart once after initialising,
// so a success is confirmed again after Settle.
func (m *Manager) waitReady(ctx context.Context, name, kind string) error {
	deadline := time.Now().Add(m.ReadyTimeout)
	probe := func() error {
		var out, errb bytes.Buffer
		code, err := m.RT.Exec(ctx, name, []string{"sh", "-c", ReadyScript(kind)}, nil, &out, &errb)
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("exit %d: %s", code, lastLines(errb.String(), 2, 300))
		}
		return nil
	}
	confirmed := false
	var last error
	for {
		if info, err := m.RT.InspectContainer(ctx, name); err == nil && (info.State == "exited" || info.State == "dead") {
			return fmt.Errorf("database container stopped while initialising (exit %d)", info.ExitCode)
		}
		if last = probe(); last == nil {
			if confirmed {
				return nil
			}
			confirmed = true
			if err := sleep(ctx, m.Settle); err != nil {
				return err
			}
			continue
		}
		confirmed = false
		if time.Now().After(deadline) {
			return fmt.Errorf("database not ready after %s: %v", m.ReadyTimeout, last)
		}
		if err := sleep(ctx, m.ReadyEvery); err != nil {
			return err
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
