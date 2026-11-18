package backup

// Database major-version upgrades (api.MsgDBUpgrade).
//
// Everything happens under the service lock, with the service stopped:
//  1. pull the new image and check there is room for a dump and a second copy of the data;
//  2. move the volumes dir to backup/upgrade/<service>/old (it is never written by the new
//     version and stays until the user removes it, so the upgrade can be rolled back);
//  3. start the OLD image on the old data in a temporary container with no network, count
//     databases and tables, dump everything to backup/upgrade/<service>/dump.sql;
//  4. start the NEW image on fresh dirs in backup/upgrade/<service>/new (it initialises with
//     the service's env), load the dump, count again, and check the data directory is inside
//     a volume (an image whose PGDATA moved would otherwise write to an anonymous volume);
//  5. move the new dirs into place. The server then deploys the new image.
//
// Any failure before step 5 removes the temporary containers, puts the old data back and
// starts the service again with the old image. A marker file lets an agent that died midway
// do the same when it starts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

func (m *Manager) upgradeRoot() string           { return filepath.Join(m.dir(), "upgrade") }
func (m *Manager) upgradeDir(svc string) string  { return filepath.Join(m.upgradeRoot(), svc) }
func (m *Manager) upgradeOld(svc string) string  { return filepath.Join(m.upgradeDir(svc), "old") }
func (m *Manager) upgradeNew(svc string) string  { return filepath.Join(m.upgradeDir(svc), "new") }
func (m *Manager) upgradeDump(svc string) string { return filepath.Join(m.upgradeDir(svc), "dump.sql") }
func (m *Manager) upgradeMarker(svc string) string {
	return filepath.Join(m.upgradeDir(svc), "converting")
}

// UpgradeContainer names the temporary containers of an upgrade ("old" or "new").
func UpgradeContainer(serviceID, which string) string {
	return "atoll-dbup-" + which + "-" + serviceID
}

func (m *Manager) handleUpgrade(ctx context.Context, t string, raw json.RawMessage) (string, bool, error) {
	switch t {
	case api.MsgDBUpgrade:
		var p api.DBUpgradePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", true, fmt.Errorf("bad payload: %w", err)
		}
		res, err := m.Upgrade(ctx, p)
		b, _ := json.Marshal(res)
		return string(b), true, err
	case api.MsgDBUpgradeRollback, api.MsgDBUpgradeCleanup:
		var p api.DBUpgradeRef
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", true, fmt.Errorf("bad payload: %w", err)
		}
		if t == api.MsgDBUpgradeRollback {
			return "", true, m.UpgradeRollback(ctx, p.ServiceID)
		}
		return "", true, m.UpgradeCleanup(p.ServiceID)
	}
	return "", false, nil
}

// Upgrade converts a database service's data to p.Image (see the file comment).
func (m *Manager) Upgrade(ctx context.Context, p api.DBUpgradePayload) (api.DBUpgradeResult, error) {
	var res api.DBUpgradeResult
	if !api.DBUpgradeKinds[p.Kind] {
		return res, fmt.Errorf("no major upgrade for kind %q", p.Kind)
	}
	if err := validTarget(api.BackupTarget{ServiceID: p.ServiceID}); err != nil {
		return res, err
	}
	if strings.TrimSpace(p.Image) == "" {
		return res, errors.New("upgrade without an image")
	}
	m.runMu.Lock()
	defer m.runMu.Unlock()
	err := m.Locked(p.ServiceID, func() error {
		spec, err := agent.NewStateStore(m.Paths).Load(p.ServiceID)
		if err != nil {
			return fmt.Errorf("service %s is not deployed on this server", p.ServiceID)
		}
		if agent.HasHostPath(spec) {
			return agent.ErrHostPathVolumes(p.ServiceID)
		}
		if len(spec.Volumes) == 0 {
			return errors.New("the service has no volume: there is no data to upgrade")
		}
		live := m.Paths.VolumesDir(p.ServiceID)
		if _, err := os.Stat(live); err != nil {
			return fmt.Errorf("data dir: %w", err)
		}
		if _, err := os.Stat(m.upgradeOld(p.ServiceID)); err == nil {
			return errors.New("the data kept from the previous upgrade is still on this server; delete it or roll back first")
		}
		if err := m.RT.PullImage(ctx, p.Image, p.RegistryAuth); err != nil {
			return fmt.Errorf("pull %s: %w", p.Image, err)
		}
		used := agent.DirSize(ctx, live)
		if err := m.ensureSpace(2*used + 512<<20); err != nil {
			return err
		}
		return m.convert(ctx, p, spec, &res)
	})
	if err != nil {
		m.Log.Warn("db upgrade failed", "service", p.ServiceID, "err", err)
		return res, err
	}
	m.Log.Info("db upgrade converted", "service", p.ServiceID, "image", p.Image, "before", res.Before, "after", res.After)
	return res, nil
}

func (m *Manager) convert(ctx context.Context, p api.DBUpgradePayload, spec api.ResolvedSpec, res *api.DBUpgradeResult) (err error) {
	svc := p.ServiceID
	name := agent.ContainerName(svc)
	live := m.Paths.VolumesDir(svc)
	if err := os.RemoveAll(m.upgradeDir(svc)); err != nil {
		return err
	}
	if err := os.MkdirAll(m.upgradeDir(svc), 0o700); err != nil {
		return err
	}
	if err := m.RT.StopContainer(ctx, name, m.StopTimeout); err != nil && !errors.Is(err, agent.ErrNotFound) {
		return fmt.Errorf("stop: %w", err)
	}
	if err := writeFile(m.upgradeMarker(svc), []byte(p.Image+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(live, m.upgradeOld(svc)); err != nil {
		os.Remove(m.upgradeMarker(svc))
		m.restart(spec)
		return fmt.Errorf("move data aside: %w", err)
	}
	defer func() {
		m.removeUpgradeContainers(svc)
		if err != nil {
			m.putOldBack(svc)
			m.restart(spec)
			err = fmt.Errorf("%w (nothing changed: the service runs the old version on its old data)", err)
		}
	}()

	// 3. old image on the old data
	oldName := UpgradeContainer(svc, "old")
	oldCS, err := m.upgradeSpec(spec, spec.Image, oldName, m.upgradeOld(svc))
	if err != nil {
		return err
	}
	if err := m.startTemp(ctx, oldCS, p.Kind); err != nil {
		return fmt.Errorf("start the current version on the current data: %w", err)
	}
	before, err := m.counts(ctx, oldName, p.Kind)
	if err != nil {
		return fmt.Errorf("count the current data: %w", err)
	}
	res.Before = before
	if res.DumpBytes, err = m.dumpTo(ctx, oldName, p.Kind, m.upgradeDump(svc)); err != nil {
		return err
	}
	m.removeDrillContainer(context.Background(), oldName)

	// 4. new image on fresh dirs
	if err := cloneDirs(m.upgradeOld(svc), m.upgradeNew(svc), spec); err != nil {
		return err
	}
	newName := UpgradeContainer(svc, "new")
	newCS, err := m.upgradeSpec(spec, p.Image, newName, m.upgradeNew(svc))
	if err != nil {
		return err
	}
	if err := m.startTemp(ctx, newCS, p.Kind); err != nil {
		return fmt.Errorf("start the new version: %w", err)
	}
	if err := m.loadFile(ctx, newName, p.Kind, m.upgradeDump(svc)); err != nil {
		return err
	}
	after, err := m.counts(ctx, newName, p.Kind)
	if err != nil {
		return fmt.Errorf("count the converted data: %w", err)
	}
	res.After = after
	if before != after {
		return fmt.Errorf("the converted data differs: %s before, %s after", before, after)
	}
	dir, err := m.dataDirectory(ctx, newName)
	if err != nil {
		return err
	}
	res.DataDir = dir
	if !insideVolume(dir, spec) {
		return fmt.Errorf("the new version keeps its data in %s, which is not on a volume of the service (set PGDATA to a path inside a volume)", dir)
	}
	if err := m.RT.StopContainer(ctx, newName, m.StopTimeout); err != nil && !errors.Is(err, agent.ErrNotFound) {
		return fmt.Errorf("stop the new version: %w", err)
	}
	m.removeDrillContainer(context.Background(), newName)

	// 5. swap in
	if err := os.Rename(m.upgradeNew(svc), live); err != nil {
		return fmt.Errorf("move the converted data into place: %w", err)
	}
	if err := os.Remove(m.upgradeMarker(svc)); err != nil {
		os.Rename(live, m.upgradeNew(svc))
		return err
	}
	return nil
}

// upgradeSpec is a temporary database container of image on dataRoot (same layout as the
// service's volumes dir), with no network and no published ports.
func (m *Manager) upgradeSpec(spec api.ResolvedSpec, image, name, dataRoot string) (agent.ContainerSpec, error) {
	spec.Image = image
	cs, err := DrillContainerSpec(m.Paths, spec, name, dataRoot)
	if err != nil {
		return cs, err
	}
	cs.Name = name
	cs.Labels = map[string]string{agent.LabelRole: "db-upgrade", LabelDrill: name}
	return cs, nil
}

func (m *Manager) startTemp(ctx context.Context, cs agent.ContainerSpec, kind string) error {
	m.removeDrillContainer(ctx, cs.Name)
	if err := m.RT.CreateContainer(ctx, cs); err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := m.RT.StartContainer(ctx, cs.Name); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	return m.waitReady(ctx, cs.Name, kind)
}

func (m *Manager) counts(ctx context.Context, name, kind string) (string, error) {
	q, err := QueryScript(kind)
	if err != nil {
		return "", err
	}
	var out, errb strings.Builder
	code, err := m.RT.Exec(ctx, name, []string{"sh", "-c", q}, nil, &out, &errb)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("exit %d: %s", code, lastLines(errb.String()+"\n"+out.String(), 4, 1000))
	}
	_, line, err := ParseCounts(out.String())
	return line, err
}

func (m *Manager) dumpTo(ctx context.Context, name, kind, path string) (int64, error) {
	script, err := DumpScript(kind)
	if err != nil {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	cw := &countWriter{w: f}
	var errb strings.Builder
	code, xerr := m.RT.Exec(ctx, name, []string{"sh", "-c", script}, nil, cw, &errb)
	cerr := f.Close()
	switch {
	case xerr != nil:
		return cw.n.Load(), fmt.Errorf("dump: %w", xerr)
	case code != 0:
		return cw.n.Load(), fmt.Errorf("dump exited %d: %s", code, lastLines(errb.String(), 4, 1000))
	case cerr != nil:
		return cw.n.Load(), fmt.Errorf("dump: %w", cerr)
	case cw.n.Load() == 0:
		return 0, errors.New("dump is empty")
	}
	return cw.n.Load(), nil
}

func (m *Manager) loadFile(ctx context.Context, name, kind, path string) error {
	script, err := RestoreScript(kind)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var errb strings.Builder
	code, err := m.RT.Exec(ctx, name, []string{"sh", "-c", script}, f, io.Discard, &errb)
	if err != nil {
		return fmt.Errorf("load dump: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("load dump exited %d: %s", code, lastLines(errb.String(), 4, 1000))
	}
	return nil
}

// DataDirScript prints the running postgres server's data directory.
const DataDirScript = pgEnv + `psql -U "$U" -d postgres -Atc "show data_directory"`

func (m *Manager) dataDirectory(ctx context.Context, name string) (string, error) {
	var out, errb strings.Builder
	code, err := m.RT.Exec(ctx, name, []string{"sh", "-c", DataDirScript}, nil, &out, &errb)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("read data directory: exit %d: %s", code, lastLines(errb.String(), 2, 300))
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	return strings.TrimSpace(lines[len(lines)-1]), nil
}

// insideVolume reports whether dir (a path in the container) lies on one of the service's volumes.
func insideVolume(dir string, spec api.ResolvedSpec) bool {
	if !strings.HasPrefix(dir, "/") {
		return false
	}
	dir = filepath.Clean(dir)
	for _, v := range spec.Volumes {
		mp := filepath.Clean(v.MountPath)
		if dir == mp || strings.HasPrefix(dir, mp+"/") {
			return true
		}
	}
	return false
}

// cloneDirs creates empty volume dirs under dst with the owner and mode of those under src.
func cloneDirs(src, dst string, spec api.ResolvedSpec) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, v := range spec.Volumes {
		mode, uid, gid := os.FileMode(0o755), -1, -1
		if fi, err := os.Stat(filepath.Join(src, v.Name)); err == nil {
			mode = fi.Mode().Perm()
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				uid, gid = int(st.Uid), int(st.Gid)
			}
		}
		d := filepath.Join(dst, v.Name)
		if err := os.MkdirAll(d, mode); err != nil {
			return err
		}
		os.Chmod(d, mode)
		if uid >= 0 {
			os.Chown(d, uid, gid)
		}
	}
	return nil
}

func (m *Manager) removeUpgradeContainers(svc string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, w := range []string{"old", "new"} {
		if err := m.removeDrillContainer(ctx, UpgradeContainer(svc, w)); err != nil && !errors.Is(err, agent.ErrNotFound) {
			m.Log.Warn("db upgrade: remove temporary container", "service", svc, "err", err)
		}
	}
}

// putOldBack restores the volumes dir moved aside by an unfinished conversion.
func (m *Manager) putOldBack(svc string) {
	live, old := m.Paths.VolumesDir(svc), m.upgradeOld(svc)
	if _, err := os.Stat(old); err == nil {
		os.RemoveAll(live)
		if err := os.Rename(old, live); err != nil {
			m.Log.Error("db upgrade: put old data back", "service", svc, "kept_at", old, "err", err)
			return
		}
	}
	os.RemoveAll(m.upgradeDir(svc))
}

func (m *Manager) restart(spec api.ResolvedSpec) {
	if spec.Stopped {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := m.RT.StartContainer(ctx, agent.ContainerName(spec.ServiceID)); err != nil {
		m.Log.Warn("db upgrade: start service", "service", spec.ServiceID, "err", err)
	}
}

// UpgradeRollback puts the data kept by the last upgrade back in place, discarding what the
// new version wrote since. The service stays stopped: the server deploys the old image next.
func (m *Manager) UpgradeRollback(ctx context.Context, svc string) error {
	if err := validTarget(api.BackupTarget{ServiceID: svc}); err != nil {
		return err
	}
	return m.Locked(svc, func() error {
		old := m.upgradeOld(svc)
		if _, err := os.Stat(old); err != nil {
			return errors.New("no data from before the upgrade is kept on this server")
		}
		if err := m.RT.StopContainer(ctx, agent.ContainerName(svc), m.StopTimeout); err != nil && !errors.Is(err, agent.ErrNotFound) {
			return fmt.Errorf("stop: %w", err)
		}
		live := m.Paths.VolumesDir(svc)
		discard := filepath.Join(m.upgradeDir(svc), "discarded")
		os.RemoveAll(discard)
		if _, err := os.Stat(live); err == nil {
			if err := os.Rename(live, discard); err != nil {
				return fmt.Errorf("move the upgraded data aside: %w", err)
			}
		}
		if err := os.Rename(old, live); err != nil {
			os.Rename(discard, live)
			return fmt.Errorf("put the old data back: %w", err)
		}
		os.RemoveAll(m.upgradeDir(svc))
		return nil
	})
}

// UpgradeCleanup deletes the data and dump kept by the last upgrade.
func (m *Manager) UpgradeCleanup(svc string) error {
	if err := validTarget(api.BackupTarget{ServiceID: svc}); err != nil {
		return err
	}
	return m.Locked(svc, func() error { return os.RemoveAll(m.upgradeDir(svc)) })
}

// recoverUpgrades finishes conversions an agent stop interrupted: the old data goes back.
func (m *Manager) recoverUpgrades(ctx context.Context) {
	ents, err := os.ReadDir(m.upgradeRoot())
	if err != nil {
		return
	}
	for _, e := range ents {
		svc := e.Name()
		if _, err := os.Stat(m.upgradeMarker(svc)); err != nil {
			continue
		}
		m.Log.Warn("db upgrade interrupted: putting the old data back", "service", svc)
		m.Locked(svc, func() error {
			m.removeUpgradeContainers(svc)
			m.putOldBack(svc)
			if spec, err := agent.NewStateStore(m.Paths).Load(svc); err == nil {
				m.restart(spec)
			}
			return nil
		})
	}
}
