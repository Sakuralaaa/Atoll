package zeabur

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/zeabur/k8s"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

// Takeover implements agent.Extension for the zeabur.* messages.
type Takeover struct {
	X           Exec
	RT          agent.Runtime
	Paths       agent.Paths
	State       *StateFile
	Log         *slog.Logger
	K3s         string // k3s binary (default "k3s", falling back to /usr/local/bin/k3s)
	StorageRoot string // /var/lib/rancher/k3s/storage
	Uninstaller string // /usr/local/bin/k3s-uninstall.sh

	// Host readings, replaceable in tests.
	DiskFree func(path string) (uint64, error)
	MemFree  func() (uint64, error)
	// Recover re-runs the local reconcile + ingress/firewall apply (agent.Recover).
	Recover func(ctx context.Context) error
	// RestartDocker restarts dockerd (after k3s-uninstall flushed iptables).
	RestartDocker func(ctx context.Context) error
	// ProbeIngress checks that atoll-caddy answers on 127.0.0.1:80.
	ProbeIngress func(ctx context.Context) error
	// PortsFree checks host ports (used for the "Zeabur ingress gone" verification).
	PortsFree func(ctx context.Context) error
	// ParkWait bounds how long to wait for parked ingress pods to disappear.
	ParkWait  time.Duration
	PollEvery time.Duration
}

func New(rt agent.Runtime, p agent.Paths, log *slog.Logger) *Takeover {
	if log == nil {
		log = slog.Default()
	}
	t := &Takeover{
		X: HostExec{}, RT: rt, Paths: p, Log: log,
		State:       &StateFile{Path: filepath.Join(p.Root, "zeabur-takeover.json")},
		K3s:         "k3s",
		StorageRoot: k8s.StorageRoot,
		Uninstaller: "/usr/local/bin/k3s-uninstall.sh",
		DiskFree: func(path string) (uint64, error) {
			u, err := disk.Usage(path)
			if err != nil {
				return 0, err
			}
			return u.Free, nil
		},
		MemFree: func() (uint64, error) {
			v, err := mem.VirtualMemory()
			if err != nil {
				return 0, err
			}
			return v.Available, nil
		},
		ParkWait:  2 * time.Minute,
		PollEvery: 3 * time.Second,
	}
	t.RestartDocker = t.restartDocker
	t.ProbeIngress = probeHTTP("http://127.0.0.1:80/")
	t.PortsFree = agent.HostPortsFree(80, 443)
	return t
}

func (t *Takeover) Handles(msgType string) bool { return strings.HasPrefix(msgType, "zeabur.") }

func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

func asJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func (t *Takeover) Handle(ctx context.Context, msgType string, raw json.RawMessage) (string, error) {
	switch msgType {
	case api.MsgZeaburScan:
		inv, err := t.Scan(ctx)
		if err != nil {
			return "", err
		}
		return asJSON(inv)
	case api.MsgZeaburImage:
		p, err := decode[api.ZeaburImagePayload](raw)
		if err != nil {
			return "", err
		}
		return t.ImportImage(ctx, p)
	case api.MsgZeaburVolume:
		p, err := decode[api.ZeaburVolumePayload](raw)
		if err != nil {
			return "", err
		}
		res, err := t.CopyVolume(ctx, p)
		if err != nil {
			return "", err
		}
		return asJSON(res)
	case api.MsgZeaburDB:
		p, err := decode[api.ZeaburDBPayload](raw)
		if err != nil {
			return "", err
		}
		res, err := t.MigrateDB(ctx, p)
		out, _ := asJSON(res)
		return out, err
	case api.MsgZeaburScale:
		p, err := decode[api.ZeaburScalePayload](raw)
		if err != nil {
			return "", err
		}
		rec, err := t.Scale(ctx, p)
		if err != nil {
			return "", err
		}
		return asJSON(rec)
	case api.MsgZeaburIngress:
		p, err := decode[api.ZeaburIngressPayload](raw)
		if err != nil {
			return "", err
		}
		if p.Park {
			return t.ParkIngress(ctx)
		}
		return t.RestoreIngress(ctx)
	case api.MsgZeaburUninstall:
		p, err := decode[api.ZeaburUninstallPayload](raw)
		if err != nil {
			return "", err
		}
		return t.Uninstall(ctx, p)
	}
	return "", fmt.Errorf("unsupported message type %q", msgType)
}

// k3sBin returns the k3s binary, or ErrNoK3s.
var ErrNoK3s = errors.New("k3s is not installed on this host")

func (t *Takeover) k3sBin() (string, error) {
	if p, err := t.X.LookPath(t.K3s); err == nil {
		return p, nil
	}
	if p, err := t.X.LookPath("/usr/local/bin/k3s"); err == nil {
		return p, nil
	}
	return "", ErrNoK3s
}

func (t *Takeover) kubectl(ctx context.Context, args ...string) ([]byte, error) {
	bin, err := t.k3sBin()
	if err != nil {
		return nil, err
	}
	return t.X.Output(ctx, bin, append([]string{"kubectl"}, args...)...)
}

// ---------- scan ----------

// Scan inventories Zeabur projects on this host. Without k3s it returns K3s=false, no error.
func (t *Takeover) Scan(ctx context.Context) (api.ZeaburInventory, error) {
	inv := api.ZeaburInventory{Projects: []api.ZeaburProject{}, ScannedAt: time.Now().UTC()}
	if free, err := t.DiskFree(t.Paths.Root); err == nil {
		inv.DiskFreeGB = float64(free) / (1 << 30)
	} else if free, err := t.DiskFree("/"); err == nil {
		inv.DiskFreeGB = float64(free) / (1 << 30)
	}
	if free, err := t.MemFree(); err == nil {
		inv.MemFreeMB = int(free / (1 << 20))
	}
	bin, err := t.k3sBin()
	if err != nil {
		return inv, nil
	}
	if st, err := t.State.Load(); err == nil && st.K3sRemoved {
		return inv, nil
	}
	inv.K3s = true
	if out, err := t.X.Output(ctx, bin, "--version"); err == nil {
		inv.K3sVersion = parseK3sVersion(string(out))
	}
	nsOut, err := t.kubectl(ctx, "get", "namespaces", "-o", "yaml")
	if err != nil {
		return inv, fmt.Errorf("k3s is installed but the API is not reachable: %w", err)
	}
	nsObjs, err := k8s.Parse(nsOut)
	if err != nil {
		return inv, err
	}
	objs := []k8s.Object{}
	for _, ns := range ZeaburNamespaces(nsObjs) {
		objs = append(objs, ns)
		// Secrets are listed for their names/keys only (k8s.Parse drops data): they attribute
		// shared credentials to services. CronJobs/Jobs/DaemonSets are reported as non-service
		// workloads.
		out, err := t.kubectl(ctx, "get", ScanKinds, "-n", ns.Metadata.Name, "-o", "yaml")
		if err != nil {
			return inv, fmt.Errorf("list objects in %s: %w", ns.Metadata.Name, err)
		}
		list, err := k8s.Parse(out)
		if err != nil {
			return inv, fmt.Errorf("parse %s: %w", ns.Metadata.Name, err)
		}
		for i := range list {
			if list[i].Metadata.Namespace == "" {
				list[i].Metadata.Namespace = ns.Metadata.Name
			}
		}
		objs = append(objs, list...)
	}
	facts := k8s.HostFacts{DirBytes: map[string]int64{}, CachedImages: map[string]bool{}}
	if dirs, _ := filepath.Glob(filepath.Join(t.StorageRoot, "*")); len(dirs) > 0 {
		// du exits non-zero on a vanished file; keep whatever it printed.
		out, _ := t.X.Output(ctx, "du", append([]string{"-sb"}, dirs...)...)
		facts.DirBytes = k8s.ParseDu(string(out))
	}
	if out, err := t.X.Output(ctx, bin, "ctr", "-n", "k8s.io", "images", "ls"); err == nil {
		facts.CachedImages = k8s.ParseCtrImages(string(out))
	}
	inv.Projects = k8s.ScanNamespaces(objs, facts)
	if inv.Projects == nil {
		inv.Projects = []api.ZeaburProject{}
	}
	if ds, err := t.ingressDaemonSets(ctx); err == nil {
		for _, d := range ds {
			if d.Running() {
				inv.IngressRunning = true
			}
		}
	}
	return inv, nil
}

// ScanKinds are the object kinds read from each Zeabur namespace.
const ScanKinds = "deploy,sts,ds,cronjob,job,svc,pvc,cm,secret,ingress,pods"

// ZeaburNamespaces returns namespaces carrying any zeabur_* label.
func ZeaburNamespaces(objs []k8s.Object) []k8s.Object {
	var out []k8s.Object
	for _, o := range objs {
		if o.Kind != "Namespace" {
			continue
		}
		for k := range o.Metadata.Labels {
			if strings.HasPrefix(k, "zeabur_") {
				out = append(out, o)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metadata.Name < out[j].Metadata.Name })
	return out
}

// parseK3sVersion parses "k3s version v1.30.4+k3s1 (98262b5d)".
func parseK3sVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "k3s" && f[1] == "version" {
			return f[2]
		}
	}
	return ""
}
