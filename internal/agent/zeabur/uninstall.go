package zeabur

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/atoll-sh/atoll/internal/agent"
	"github.com/atoll-sh/atoll/internal/api"
)

type UninstallResult struct {
	Backups         []string          `json:"backups,omitempty"`
	BackupBytes     int64             `json:"backup_bytes,omitempty"`
	ScriptOK        bool              `json:"script_ok"`
	Log             string            `json:"log"` // tail of k3s-uninstall.sh output
	DockerRestarted bool              `json:"docker_restarted"`
	IngressOK       bool              `json:"ingress_ok"`
	NotRunning      map[string]string `json:"not_running,omitempty"` // container -> state
	Warnings        []string          `json:"warnings,omitempty"`
}

// tailBuffer keeps the last max bytes written.
type tailBuffer struct {
	mu  sync.Mutex
	b   []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { t.mu.Lock(); defer t.mu.Unlock(); return string(t.b) }

func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// Uninstall removes k3s after a completed cutover. It is the point of no return: afterwards
// rollback is refused.
func (t *Takeover) Uninstall(ctx context.Context, p api.ZeaburUninstallPayload) (string, error) {
	st, err := t.State.Load()
	if err != nil {
		return "", err
	}
	if st.K3sRemoved {
		return asJSON(map[string]any{"already_removed": true, "removed_at": st.K3sRemovedAt})
	}
	if st.CutoverAt == nil || !st.IngressParked {
		return "", errors.New("refusing to uninstall k3s: no completed cutover recorded on this host (park the Zeabur ingress during cutover first)")
	}
	if info, err := t.RT.InspectContainer(ctx, agent.CaddyContainer); err != nil || info.State != "running" {
		return "", fmt.Errorf("refusing to uninstall k3s: %s is not running, atoll is not serving this host yet", agent.CaddyContainer)
	}
	if _, err := t.X.LookPath(t.Uninstaller); err != nil {
		return "", fmt.Errorf("uninstall script %s not found: %w", t.Uninstaller, err)
	}
	res := UninstallResult{}

	if p.KeepBackup {
		backups, n, err := t.backupPVCs()
		if err != nil {
			return "", fmt.Errorf("backup before uninstall failed (k3s untouched): %w", err)
		}
		res.Backups, res.BackupBytes = backups, n
		if _, err := t.State.Update(func(s *State) error {
			s.BackupDir = filepath.Join(t.Paths.Root, "zeabur-backup")
			return nil
		}); err != nil {
			return "", err
		}
	}

	logBuf := &tailBuffer{max: 256 << 10}
	t.Log.Warn("running k3s uninstall", "script", t.Uninstaller)
	runErr := t.X.Stream(ctx, nil, logBuf, t.Uninstaller)
	res.Log = logBuf.String()
	res.ScriptOK = runErr == nil
	if runErr != nil {
		res.Warnings = append(res.Warnings, "uninstall script: "+runErr.Error())
	}
	// Whatever the exit code, k3s may be (partly) gone: rollback is no longer safe.
	now := time.Now().UTC()
	if _, err := t.State.Update(func(s *State) error {
		s.K3sRemoved, s.K3sRemovedAt = true, &now
		return nil
	}); err != nil {
		return "", err
	}

	// k3s-uninstall rewrites iptables and deletes CNI interfaces: verify Docker networking.
	if err := t.ProbeIngress(ctx); err != nil {
		res.Warnings = append(res.Warnings, "ingress probe failed after uninstall: "+err.Error()+"; restarting docker")
		if err := t.RestartDocker(ctx); err != nil {
			res.Warnings = append(res.Warnings, "restart docker: "+err.Error())
		} else {
			res.DockerRestarted = true
		}
	}
	if t.Recover != nil { // reconcile containers, re-apply ingress and firewall rules
		if err := t.Recover(ctx); err != nil {
			res.Warnings = append(res.Warnings, "recover: "+err.Error())
		}
	}
	res.IngressOK = t.ProbeIngress(ctx) == nil
	res.NotRunning = map[string]string{}
	if list, err := t.RT.ListContainers(ctx, agent.LabelManaged); err == nil {
		specs, _ := agent.NewStateStore(t.Paths).List()
		stopped := map[string]bool{}
		for _, s := range specs {
			stopped[s.ServiceID] = s.Stopped
		}
		for _, c := range list {
			sid := c.Labels[agent.LabelService]
			if c.State != "running" && !stopped[sid] && !strings.HasSuffix(c.Name, "-old") {
				res.NotRunning[c.Name] = c.State
			}
		}
	}
	if _, err := t.k3sBin(); err == nil {
		res.Warnings = append(res.Warnings, "k3s binary still present after uninstall script")
	}
	out, _ := asJSON(res)
	if !res.IngressOK || len(res.NotRunning) > 0 {
		return out, fmt.Errorf("k3s removed but verification failed (ingress_ok=%v, not running: %d)", res.IngressOK, len(res.NotRunning))
	}
	return out, nil
}

// backupPVCs tars every PVC dir under the k3s storage root into <data>/zeabur-backup/<dir>.tgz.
func (t *Takeover) backupPVCs() ([]string, int64, error) {
	entries, err := os.ReadDir(t.StorageRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var dirs []string
	var total int64
	for _, e := range entries {
		if e.IsDir() {
			d := filepath.Join(t.StorageRoot, e.Name())
			dirs = append(dirs, d)
			total += dirSize(d)
		}
	}
	dest := filepath.Join(t.Paths.Root, "zeabur-backup")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return nil, 0, err
	}
	free, err := t.DiskFree(dest)
	if err != nil {
		return nil, 0, err
	}
	if free <= uint64(total) {
		return nil, 0, fmt.Errorf("not enough free disk for backup: need %d bytes, %d free", total, free)
	}
	var out []string
	for _, d := range dirs {
		file := filepath.Join(dest, filepath.Base(d)+".tgz")
		tmp := file + ".tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return out, total, err
		}
		err = agent.TarGz(d, f)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(tmp)
			return out, total, fmt.Errorf("backup %s: %w", d, err)
		}
		if err := os.Rename(tmp, file); err != nil {
			return out, total, err
		}
		out = append(out, file)
	}
	return out, total, nil
}

func (t *Takeover) restartDocker(ctx context.Context) error {
	if _, err := t.X.Output(ctx, "systemctl", "restart", "docker"); err != nil {
		return err
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		vctx, c := context.WithTimeout(ctx, 5*time.Second)
		_, err := t.RT.Version(vctx)
		c()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("docker did not come back: %w", err)
		}
		time.Sleep(2 * time.Second)
	}
}

// probeHTTP succeeds on any HTTP response from atoll-caddy, and (when iptables is present)
// requires Docker's DNAT for port 80 so external traffic still reaches the container.
func probeHTTP(url string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		x := HostExec{}
		if ipt, err := x.LookPath("iptables"); err == nil {
			out, err := x.Output(ctx, ipt, "-t", "nat", "-S", "DOCKER")
			if err != nil {
				return fmt.Errorf("docker nat chain missing: %w", err)
			}
			if !strings.Contains(string(out), "--dport 80") {
				return errors.New("docker nat chain has no rule for port 80")
			}
		}
		return nil
	}
}
