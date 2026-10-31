package zeabur_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
	"github.com/Sakuralaaa/Atoll/internal/agent/zeabur"
	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/zeabur/k8s"
)

const k3sBin = "/usr/local/bin/k3s"

// fakeDS is one DaemonSet in the fake cluster.
type fakeDS struct {
	NS, Name     string
	HostPorts    []int
	HostNetwork  bool
	NodeSelector map[string]string
	// Operated: owned by an operator that reverts edits while operator-system/op runs.
	Operated bool
}

// fakeHost replays a single-node k3s host: kubectl/ctr/du/uninstall commands are answered
// from fixtures and a small mutable cluster model (DaemonSets, node labels, replicas).
type fakeHost struct {
	mu          sync.Mutex
	t           *testing.T
	k3s         bool
	uninstaller string
	calls       []string

	nsYAML    []byte            // `get namespaces` and per-namespace object listing
	ctrLs     string            // `ctr images ls`
	du        string            // `du -sb`
	ds        []*fakeDS         // daemonsets
	node      string            // single node name
	labels    map[string]string // node labels
	replicas  map[string]int    // "<ns>/<workload>" -> replicas
	archive   []byte            // `ctr images export` output
	exportErr error
}

func newHost(t *testing.T) *fakeHost {
	return &fakeHost{
		t: t, k3s: true, node: "vps1", labels: map[string]string{"kubernetes.io/os": "linux"},
		replicas: map[string]int{},
		ds: []*fakeDS{
			{NS: "kube-system", Name: "traefik", HostPorts: []int{80, 443}, NodeSelector: map[string]string{"kubernetes.io/os": "linux"}},
			{NS: "kube-system", Name: "svclb-traefik-abc", HostPorts: []int{80, 443}},
			{NS: "kube-system", Name: "fluent-bit"}, // not an ingress: must be ignored
		},
	}
}

func (h *fakeHost) log() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.calls...)
}

func (h *fakeHost) called(prefix string) bool {
	for _, c := range h.log() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (h *fakeHost) LookPath(name string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case (name == "k3s" || name == k3sBin) && h.k3s:
		return k3sBin, nil
	case h.uninstaller != "" && name == h.uninstaller:
		return name, nil
	}
	return "", errors.New("not found: " + name)
}

func (h *fakeHost) running(d *fakeDS) bool {
	if d.NodeSelector[zeabur.ParkLabel] != "" {
		return false
	}
	if strings.HasPrefix(d.Name, "svclb-") && h.labels["svccontroller.k3s.cattle.io/enablelb"] == "false" {
		return false
	}
	return true
}

func (h *fakeHost) dsJSON() []byte {
	var items []any
	for _, d := range h.ds {
		var ports []any
		for _, p := range d.HostPorts {
			ports = append(ports, map[string]any{"containerPort": p, "hostPort": p, "protocol": "TCP"})
		}
		n := 0
		if h.running(d) {
			n = 1
		}
		meta := map[string]any{"name": d.Name, "namespace": d.NS}
		if d.Operated {
			meta["ownerReferences"] = []any{map[string]any{"kind": "ZeaburService", "name": "zeabur-cluster-ingress", "controller": true}}
		}
		items = append(items, map[string]any{
			"metadata": meta,
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"nodeSelector": d.NodeSelector, "hostNetwork": d.HostNetwork,
				"containers": []any{map[string]any{"name": "c", "ports": ports}},
			}}},
			"status": map[string]any{"desiredNumberScheduled": n, "currentNumberScheduled": n, "numberReady": n},
		})
	}
	b, _ := json.Marshal(map[string]any{"items": items})
	return b
}

func (h *fakeHost) run(name string, args []string, stdin io.Reader, w io.Writer) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	h.calls = append(h.calls, line)
	if name == "du" {
		return []byte(h.du), nil
	}
	if h.uninstaller != "" && name == h.uninstaller {
		fmt.Fprintln(w, "+ systemctl stop k3s\n+ rm -rf /var/lib/rancher")
		h.k3s = false
		return nil, nil
	}
	if name != k3sBin || !h.k3s {
		return nil, fmt.Errorf("unexpected command %q", line)
	}
	if len(args) == 1 && args[0] == "--version" {
		return []byte("k3s version v1.30.4+k3s1 (98262b5d)\ngo version go1.22.5\n"), nil
	}
	if len(args) >= 2 && args[0] == "ctr" {
		switch {
		case strings.Join(args, " ") == "ctr -n k8s.io images ls":
			return []byte(h.ctrLs), nil
		case len(args) >= 5 && args[3] == "images" && args[4] == "export":
			if h.exportErr != nil {
				return nil, h.exportErr
			}
			_, err := w.Write(h.archive)
			return nil, err
		}
		return nil, fmt.Errorf("unexpected ctr %q", line)
	}
	if args[0] != "kubectl" {
		return nil, fmt.Errorf("unexpected %q", line)
	}
	a := args[1:]
	j := strings.Join(a, " ")
	switch {
	case j == "get namespaces -o yaml":
		return h.nsYAML, nil
	case len(a) == 6 && a[0] == "get" && a[1] == zeabur.ScanKinds && a[2] == "-n":
		return h.nsYAML, nil
	case j == "get daemonsets -A -o json":
		return h.dsJSON(), nil
	case j == "get nodes -o json":
		b, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": h.node, "labels": h.labels}}}})
		return b, nil
	case a[0] == "patch" && a[1] == "daemonset":
		var patch struct {
			Spec struct {
				Template struct {
					Spec struct {
						NodeSelector map[string]*string `json:"nodeSelector"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		}
		if err := json.Unmarshal([]byte(a[len(a)-1]), &patch); err != nil {
			return nil, err
		}
		for _, d := range h.ds {
			if d.Name == a[2] && d.NS == a[4] {
				if d.Operated && h.replicas["operator-system/deployment/op"] > 0 {
					return nil, nil // the operator reconciles the edit away
				}
				if d.NodeSelector == nil {
					d.NodeSelector = map[string]string{}
				}
				for k, v := range patch.Spec.Template.Spec.NodeSelector {
					if v == nil {
						delete(d.NodeSelector, k)
					} else {
						d.NodeSelector[k] = *v
					}
				}
				return nil, nil
			}
		}
		return nil, fmt.Errorf("daemonset %s not found", a[2])
	case a[0] == "label" && a[1] == "node":
		kv := a[3]
		if strings.HasSuffix(kv, "-") {
			delete(h.labels, strings.TrimSuffix(kv, "-"))
		} else {
			k, v, _ := strings.Cut(kv, "=")
			h.labels[k] = v
		}
		return nil, nil
	case j == "get deployments -A -l "+zeabur.OperatorSelector+" -o json":
		var items []any
		if _, ok := h.replicas["operator-system/deployment/op"]; ok {
			items = append(items, map[string]any{"metadata": map[string]any{"name": "op", "namespace": "operator-system"}})
		}
		b, _ := json.Marshal(map[string]any{"items": items})
		return b, nil
	case a[0] == "get" && len(a) == 6 && a[5] == "jsonpath={.status.replicas}":
		return []byte(fmt.Sprint(h.replicas[a[3]+"/"+a[1]])), nil
	case a[0] == "get" && len(a) == 6 && a[5] == "jsonpath={.spec.replicas}":
		n, ok := h.replicas[a[3]+"/"+a[1]]
		if !ok {
			return nil, fmt.Errorf("NotFound %s", a[1])
		}
		return []byte(fmt.Sprint(n)), nil
	case a[0] == "scale":
		var n int
		fmt.Sscanf(a[4], "--replicas=%d", &n)
		h.replicas[a[3]+"/"+a[1]] = n
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected kubectl %q", line)
}

func (h *fakeHost) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	return h.run(name, args, nil, io.Discard)
}

func (h *fakeHost) Stream(_ context.Context, stdin io.Reader, w io.Writer, name string, args ...string) error {
	out, err := h.run(name, args, stdin, w)
	if len(out) > 0 {
		w.Write(out)
	}
	return err
}

func newTakeover(t *testing.T) (*zeabur.Takeover, *fakeHost, *agenttest.Fake) {
	t.Helper()
	rt := agenttest.New()
	p := agent.Paths{Root: t.TempDir()}
	tk := zeabur.New(rt, p, nil)
	h := newHost(t)
	tk.X = h
	tk.StorageRoot = t.TempDir()
	tk.DiskFree = func(string) (uint64, error) { return 50 << 30, nil }
	tk.MemFree = func() (uint64, error) { return 2048 << 20, nil }
	tk.PortsFree = func(context.Context) error { return nil }
	tk.ProbeIngress = func(context.Context) error { return nil }
	tk.RestartDocker = func(context.Context) error { return nil }
	tk.ParkWait, tk.PollEvery = time.Second, time.Millisecond
	return tk, h, rt
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "zeabur", "k8s", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// startCaddy puts a running atoll-caddy into the fake runtime.
func startCaddy(t *testing.T, rt *agenttest.Fake) {
	t.Helper()
	ctx := context.Background()
	_ = rt.EnsureNetwork(ctx, agent.IngressNetwork)
	if err := rt.CreateContainer(ctx, agent.ContainerSpec{Name: agent.CaddyContainer, Image: "caddy:2",
		Labels:   map[string]string{agent.LabelManaged: "true", agent.LabelRole: "ingress"},
		Networks: []agent.NetworkAttach{{Name: agent.IngressNetwork}}}); err != nil {
		t.Fatal(err)
	}
	if err := rt.StartContainer(ctx, agent.CaddyContainer); err != nil {
		t.Fatal(err)
	}
}

func TestScanFixtures(t *testing.T) {
	tk, h, _ := newTakeover(t)
	h.nsYAML = append(append(readFixture(t, "namespace.yaml"), []byte("\n---\n")...), readFixture(t, "zeabur-cloud.yaml")...)
	h.ctrLs = string(readFixture(t, "ctr.txt"))
	const cloudNS = "environment-67000000000000000000e001"
	h.du = string(readFixture(t, "du.txt")) +
		"5242880\t" + k8s.StorageRoot + "/pvc-aaaa_" + cloudNS + "_media-service-67000000000000000000a001\n"
	// Glob must find at least one dir for du to run.
	os.MkdirAll(filepath.Join(tk.StorageRoot, "pvc-x"), 0o755)

	out, err := tk.Handle(context.Background(), api.MsgZeaburScan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "c3VwZXIt") || strings.Contains(out, "super-secret") {
		t.Fatal("secret data leaked into the inventory")
	}
	var inv api.ZeaburInventory
	if err := json.Unmarshal([]byte(out), &inv); err != nil {
		t.Fatal(err)
	}
	if !inv.K3s || inv.K3sVersion != "v1.30.4+k3s1" || !inv.IngressRunning || inv.MemFreeMB != 2048 || inv.DiskFreeGB != 50 {
		t.Fatalf("inventory header %+v", inv)
	}
	if len(inv.Projects) != 2 {
		t.Fatalf("projects: %d", len(inv.Projects))
	}
	if !h.called(k3sBin + " kubectl get " + zeabur.ScanKinds + " -n " + cloudNS + " -o yaml") {
		t.Fatalf("scan must list secrets/cronjobs too: %v", h.log())
	}
	var shop, blog api.ZeaburProject
	for _, p := range inv.Projects {
		switch p.ProjectName {
		case "shop":
			shop = p
		case "blog":
			blog = p
		}
	}
	if shop.Namespace == "" || blog.Namespace != cloudNS {
		t.Fatalf("projects %+v", inv.Projects)
	}
	find := func(svcs []api.ZeaburService, id string) api.ZeaburService {
		for _, s := range svcs {
			if s.ServiceID == id {
				return s
			}
		}
		t.Fatalf("service %s missing", id)
		return api.ZeaburService{}
	}
	web := find(shop.Services, "66500000000000000000a001")
	if !web.ZeaburRegistry || !web.ImageCached || len(web.Volumes) != 1 || web.Volumes[0].Bytes != 1048576 {
		t.Fatalf("shop web %+v", web)
	}
	if len(blog.Services) != 4 {
		t.Fatalf("blog services %d", len(blog.Services))
	}
	app := find(blog.Services, "67000000000000000000a001")
	if app.Status != k8s.StatusRunning || app.DataStore != "" || len(app.K8sServices) == 0 || app.Volumes[0].Bytes != 5242880 {
		t.Fatalf("blog app %+v", app)
	}
	var shared bool
	for _, r := range app.References {
		if r.Key == "DB_PASSWORD" && r.Via == k8s.ViaSharedSecret && r.TargetKey == "POSTGRES_PASSWORD" {
			shared = true
		}
	}
	if !shared {
		t.Fatalf("shared-secret reference needs the Secret objects in the scan: %+v", app.References)
	}
	pg := find(blog.Services, "67000000000000000000a002")
	if pg.DataStore != "postgres" || pg.Kind != "postgres" {
		t.Fatalf("pg %+v", pg)
	}
	cache := find(blog.Services, "67000000000000000000a003")
	if cache.Status != k8s.StatusFailed {
		t.Fatalf("cache status %q", cache.Status)
	}
	var cron bool
	for _, w := range blog.Warnings {
		cron = cron || strings.Contains(w, "cronjob/backup")
	}
	if !cron {
		t.Fatalf("cronjob warning missing (cronjobs must be listed): %v", blog.Warnings)
	}
}

func TestScanWithoutK3s(t *testing.T) {
	tk, h, _ := newTakeover(t)
	h.k3s = false
	inv, err := tk.Scan(context.Background())
	if err != nil || inv.K3s || inv.Projects == nil || len(inv.Projects) != 0 {
		t.Fatalf("inv %+v err %v", inv, err)
	}
}

func TestParkRestoreIngress(t *testing.T) {
	tk, h, rt := newTakeover(t)
	ctx := context.Background()
	if err := tk.IngressGuard(ctx); err == nil || !strings.Contains(err.Error(), "kube-system/traefik") {
		t.Fatalf("guard must block while traefik runs: %v", err)
	}
	if _, err := tk.Handle(ctx, api.MsgZeaburIngress, json.RawMessage(`{"park":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := tk.IngressGuard(ctx); err != nil {
		t.Fatalf("guard after park: %v", err)
	}
	st, err := tk.State.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !st.IngressParked || st.CutoverAt == nil || len(st.ParkedDaemonSets) != 1 ||
		!reflect.DeepEqual(st.ParkedDaemonSets[0].OriginalNodeSelector, map[string]string{"kubernetes.io/os": "linux"}) {
		t.Fatalf("state %+v", st)
	}
	if len(st.NodeLabels) != 1 || st.NodeLabels[0].Had || st.NodeLabels[0].Node != "vps1" {
		t.Fatalf("node labels %+v", st.NodeLabels)
	}
	for _, c := range h.log() {
		if strings.Contains(c, "fluent-bit") {
			t.Fatalf("non-ingress daemonset touched: %s", c)
		}
	}
	// Re-park is idempotent and does not duplicate records.
	if _, err := tk.ParkIngress(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := tk.State.Load(); len(st.ParkedDaemonSets) != 1 || len(st.NodeLabels) != 1 {
		t.Fatalf("re-park duplicated records: %+v", st)
	}

	startCaddy(t, rt)
	if _, err := tk.Handle(ctx, api.MsgZeaburIngress, json.RawMessage(`{"park":false}`)); err != nil {
		t.Fatal(err)
	}
	if c, _ := rt.Get(agent.CaddyContainer); c.Info.State != "exited" {
		t.Fatalf("caddy must be stopped before restoring zeabur ingress: %s", c.Info.State)
	}
	if !reflect.DeepEqual(h.ds[0].NodeSelector, map[string]string{"kubernetes.io/os": "linux"}) {
		t.Fatalf("traefik selector not restored: %v", h.ds[0].NodeSelector)
	}
	if _, ok := h.labels["svccontroller.k3s.cattle.io/enablelb"]; ok {
		t.Fatal("svclb node label must be removed (was absent)")
	}
	if st, _ := tk.State.Load(); st.IngressParked || st.CutoverAt != nil || len(st.ParkedDaemonSets) != 0 {
		t.Fatalf("state after restore %+v", st)
	}
	if err := tk.IngressGuard(ctx); err == nil {
		t.Fatal("guard must block again after restore")
	}
}

func TestParkFailsWhenPortsStayBusy(t *testing.T) {
	tk, _, _ := newTakeover(t)
	tk.PortsFree = func(context.Context) error { return errors.New("port 80 busy") }
	if _, err := tk.ParkIngress(context.Background()); err == nil || !strings.Contains(err.Error(), "ports still busy") {
		t.Fatalf("err %v", err)
	}
	if st, _ := tk.State.Load(); st.IngressParked || st.CutoverAt != nil {
		t.Fatal("cutover must not be recorded when ports are busy")
	}
}

func TestScaleAndRestore(t *testing.T) {
	tk, h, _ := newTakeover(t)
	ctx := context.Background()
	const ns, wl = "environment-1", "deployment/service-abc"
	h.replicas[ns+"/"+wl] = 2
	out, err := tk.Handle(ctx, api.MsgZeaburScale, json.RawMessage(`{"namespace":"environment-1","workload":"deployment/service-abc","replicas":0}`))
	if err != nil {
		t.Fatal(err)
	}
	var rec zeabur.WorkloadRecord
	json.Unmarshal([]byte(out), &rec)
	if rec.OriginalReplicas != 2 || rec.CurrentReplicas != 0 || h.replicas[ns+"/"+wl] != 0 {
		t.Fatalf("rec %+v replicas %d", rec, h.replicas[ns+"/"+wl])
	}
	st, _ := tk.State.Load()
	if r := st.Workloads[ns+"/"+wl]; r.OriginalReplicas != 2 || r.CurrentReplicas != 0 {
		t.Fatalf("state %+v", st.Workloads)
	}
	// Scaling again keeps the first recorded original.
	if _, err := tk.Scale(ctx, api.ZeaburScalePayload{Namespace: ns, Workload: wl, Replicas: 1}); err != nil {
		t.Fatal(err)
	}
	if st, _ := tk.State.Load(); st.Workloads[ns+"/"+wl].OriginalReplicas != 2 {
		t.Fatalf("original overwritten: %+v", st.Workloads)
	}
	if _, err := tk.Scale(ctx, api.ZeaburScalePayload{Namespace: ns, Workload: wl, Replicas: zeabur.RestoreReplicas}); err != nil {
		t.Fatal(err)
	}
	if h.replicas[ns+"/"+wl] != 2 {
		t.Fatalf("restore scaled to %d", h.replicas[ns+"/"+wl])
	}
	if st, _ := tk.State.Load(); len(st.Workloads) != 0 {
		t.Fatalf("record must be dropped once back to original: %+v", st.Workloads)
	}
	for _, bad := range []api.ZeaburScalePayload{
		{Namespace: ns, Workload: "deployment/../x", Replicas: 0},
		{Namespace: "Bad NS", Workload: wl, Replicas: 0},
		{Namespace: ns, Workload: "pod/x", Replicas: 0},
		{Namespace: ns, Workload: wl, Replicas: -5},
	} {
		if _, err := tk.Scale(ctx, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestUninstallRefusals(t *testing.T) {
	tk, h, rt := newTakeover(t)
	h.uninstaller = tk.Uninstaller
	ctx := context.Background()
	_, err := tk.Uninstall(ctx, api.ZeaburUninstallPayload{})
	if err == nil || !strings.Contains(err.Error(), "no completed cutover") {
		t.Fatalf("no cutover: %v", err)
	}
	if _, err := tk.ParkIngress(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = tk.Uninstall(ctx, api.ZeaburUninstallPayload{})
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("caddy missing: %v", err)
	}
	startCaddy(t, rt)
	rt.SetState(agent.CaddyContainer, "exited")
	if _, err := tk.Uninstall(ctx, api.ZeaburUninstallPayload{}); err == nil {
		t.Fatal("caddy stopped: must refuse")
	}
	if h.called(tk.Uninstaller) {
		t.Fatal("uninstall script ran despite refusal")
	}
	if st, _ := tk.State.Load(); st.K3sRemoved {
		t.Fatal("k3s marked removed")
	}
}

func TestUninstallWithBackup(t *testing.T) {
	tk, h, rt := newTakeover(t)
	h.uninstaller = tk.Uninstaller
	ctx := context.Background()
	pvc := filepath.Join(tk.StorageRoot, "pvc-1_environment-1_data")
	os.MkdirAll(filepath.Join(pvc, "sub"), 0o755)
	os.WriteFile(filepath.Join(pvc, "sub", "a.txt"), []byte("hello"), 0o644)
	recovered := 0
	tk.Recover = func(context.Context) error { recovered++; return nil }

	if _, err := tk.ParkIngress(ctx); err != nil {
		t.Fatal(err)
	}
	startCaddy(t, rt)
	out, err := tk.Handle(ctx, api.MsgZeaburUninstall, json.RawMessage(`{"keep_backup":true}`))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var res zeabur.UninstallResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !res.ScriptOK || !res.IngressOK || len(res.Backups) != 1 || res.BackupBytes != 5 || !strings.Contains(res.Log, "systemctl stop k3s") || recovered != 1 {
		t.Fatalf("result %+v recovered=%d", res, recovered)
	}
	if want := filepath.Join(tk.Paths.Root, "zeabur-backup", "pvc-1_environment-1_data.tgz"); res.Backups[0] != want {
		t.Fatalf("backup path %s", res.Backups[0])
	}
	got := tgzFiles(t, res.Backups[0])
	if got["sub/a.txt"] != "hello" {
		t.Fatalf("backup content %v", got)
	}
	st, _ := tk.State.Load()
	if !st.K3sRemoved || st.K3sRemovedAt == nil || st.BackupDir == "" {
		t.Fatalf("state %+v", st)
	}
	// After removal: rollback refused, repeat uninstall is a no-op, guard passes.
	if _, err := tk.RestoreIngress(ctx); err == nil {
		t.Fatal("restore after uninstall must be refused")
	}
	if _, err := tk.Scale(ctx, api.ZeaburScalePayload{Namespace: "environment-1", Workload: "deployment/x", Replicas: -1}); err == nil {
		t.Fatal("scale after uninstall must be refused")
	}
	if out, err := tk.Uninstall(ctx, api.ZeaburUninstallPayload{}); err != nil || !strings.Contains(out, "already_removed") {
		t.Fatalf("repeat: %s %v", out, err)
	}
	if err := tk.IngressGuard(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallVerificationFailure(t *testing.T) {
	tk, h, rt := newTakeover(t)
	h.uninstaller = tk.Uninstaller
	ctx := context.Background()
	if _, err := tk.ParkIngress(ctx); err != nil {
		t.Fatal(err)
	}
	startCaddy(t, rt)
	restarted := false
	tk.ProbeIngress = func(context.Context) error { return errors.New("connection refused") }
	tk.RestartDocker = func(context.Context) error { restarted = true; return nil }
	out, err := tk.Uninstall(ctx, api.ZeaburUninstallPayload{})
	if err == nil || !restarted || !strings.Contains(out, `"docker_restarted":true`) {
		t.Fatalf("out %s err %v restarted %v", out, err, restarted)
	}
	if st, _ := tk.State.Load(); !st.K3sRemoved {
		t.Fatal("k3s must be marked removed even when verification fails")
	}
}

func tgzFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			b, _ := io.ReadAll(tr)
			out[h.Name] = string(b)
		}
	}
}

func TestStateFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sf := &zeabur.StateFile{Path: filepath.Join(dir, "sub", "zeabur-takeover.json")}
	st, err := sf.Load()
	if err != nil || st.Workloads == nil || len(st.Workloads) != 0 {
		t.Fatalf("missing file: %+v %v", st, err)
	}
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	want := zeabur.State{
		Workloads: map[string]zeabur.WorkloadRecord{
			"ns/deployment/a": {Namespace: "ns", Workload: "deployment/a", OriginalReplicas: 3, CurrentReplicas: 0, ChangedAt: now},
		},
		ParkedDaemonSets: []zeabur.ParkedDaemonSet{{Namespace: "kube-system", Name: "traefik", OriginalNodeSelector: map[string]string{"a": "b"}}},
		NodeLabels:       []zeabur.NodeLabelRecord{{Node: "n1", Key: "k", Previous: "true", Had: true}},
		IngressParked:    true, CutoverAt: &now, BackupDir: "/x",
	}
	if _, err := sf.Update(func(s *zeabur.State) error { u := s.UpdatedAt; *s = want; s.UpdatedAt = u; return nil }); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(sf.Path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v %v", fi.Mode(), err)
	}
	got, err := sf.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt not stamped")
	}
	got.UpdatedAt = time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip\n got %+v\nwant %+v", got, want)
	}
	// A failing update writes nothing.
	if _, err := sf.Update(func(s *zeabur.State) error { s.BackupDir = "/changed"; return errors.New("boom") }); err == nil {
		t.Fatal("want error")
	}
	if again, _ := sf.Load(); again.BackupDir != "/x" {
		t.Fatal("failed update was persisted")
	}
	os.WriteFile(sf.Path, []byte("{nope"), 0o600)
	if _, err := sf.Load(); err == nil {
		t.Fatal("corrupt state must error, not reset (would lose rollback info)")
	}
}

func TestImportImagePipe(t *testing.T) {
	tk, h, rt := newTakeover(t)
	h.ctrLs = string(readFixture(t, "ctr.txt"))
	const ref = "registry-oci.zeabur.cloud/e-66500000000000000000e001/s-66500000000000000000a001:d-66500000000000000000d001"
	h.archive = bytes.Repeat([]byte("OCI-ARCHIVE-"), 50000) // > pipe buffer: proves streaming
	rt.LoadRefs = []string{ref}
	ctx := context.Background()
	out, err := tk.Handle(ctx, api.MsgZeaburImage, json.RawMessage(`{"image":"`+ref+`","service_id":"svc_web"}`))
	if err != nil {
		t.Fatal(err)
	}
	if out != "atoll-import/svc_web:aaaa11111111" {
		t.Fatalf("local ref %q", out)
	}
	if len(rt.Loaded) != 1 || !bytes.Equal(rt.Loaded[0], h.archive) {
		t.Fatal("docker load did not receive the ctr export stream")
	}
	if !h.called(k3sBin + " ctr -n k8s.io images export --platform linux/" + runtime.GOARCH + " - " + ref) {
		t.Fatalf("export args: %v", h.log())
	}
	if _, err := rt.ImageInfo(ctx, out); err != nil {
		t.Fatal("target tag missing")
	}
	// Repeat: already imported, no second export.
	if out2, err := tk.ImportImage(ctx, api.ZeaburImagePayload{Image: ref, ServiceID: "svc_web"}); err != nil || out2 != out || len(rt.Loaded) != 1 {
		t.Fatalf("repeat %q %v loads=%d", out2, err, len(rt.Loaded))
	}
	// Not in cache.
	if _, err := tk.ImportImage(ctx, api.ZeaburImagePayload{Image: "ghcr.io/x/y:1", ServiceID: "svc_x"}); err == nil || !strings.Contains(err.Error(), "not in k3s") {
		t.Fatalf("uncached: %v", err)
	}
	// Bad service id.
	if _, err := tk.ImportImage(ctx, api.ZeaburImagePayload{Image: ref, ServiceID: "../x"}); err == nil {
		t.Fatal("bad id accepted")
	}
}

func TestImportImageExportFailure(t *testing.T) {
	tk, h, rt := newTakeover(t)
	h.ctrLs = string(readFixture(t, "ctr.txt"))
	h.exportErr = errors.New("ctr: content digest not found")
	rt.LoadRefs = []string{"docker.io/library/postgres:latest"}
	_, err := tk.ImportImage(context.Background(), api.ZeaburImagePayload{Image: "docker.io/library/postgres:latest", ServiceID: "svc_db"})
	if err == nil || !strings.Contains(err.Error(), "ctr export") {
		t.Fatalf("err %v", err)
	}
	if _, err := rt.ImageInfo(context.Background(), zeabur.LocalTag("svc_db", "sha256:0123456789abcdef")); err == nil {
		t.Fatal("must not tag after a failed export")
	}
}

// Public registry images keep their original ref: pulled when possible, exported from
// containerd and tagged with that same ref when the pull fails.
func TestImportImagePullsPublicImages(t *testing.T) {
	tk, h, rt := newTakeover(t)
	h.ctrLs = string(readFixture(t, "ctr.txt"))
	h.archive = []byte("OCI")
	ctx := context.Background()
	const ref = "docker.io/library/postgres:latest"

	out, err := tk.Handle(ctx, api.MsgZeaburImage, json.RawMessage(`{"image":"`+ref+`","service_id":"svc_db","pull":true}`))
	if err != nil || out != ref {
		t.Fatalf("pull: %q %v", out, err)
	}
	if len(rt.Loaded) != 0 || h.called(k3sBin+" ctr -n k8s.io images export --platform linux/"+runtime.GOARCH+" - "+ref) {
		t.Fatal("pulled image must not be exported from containerd")
	}

	// registry unreachable: containerd copy, tagged with the original ref
	delete(rt.Images, ref)
	rt.PullErr[ref] = errors.New("toomanyrequests: rate limit")
	rt.LoadRefs = []string{"docker.io/library/postgres@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	out, err = tk.ImportImage(ctx, api.ZeaburImagePayload{Image: ref, ServiceID: "svc_db", Pull: true})
	if err != nil || out != ref {
		t.Fatalf("fallback: %q %v", out, err)
	}
	if len(rt.Loaded) != 1 {
		t.Fatalf("containerd fallback not used: %d loads", len(rt.Loaded))
	}
	if _, err := rt.ImageInfo(ctx, ref); err != nil {
		t.Fatal("fallback image not tagged with the original ref")
	}

	// neither pull nor cache: both reasons reported
	rt.PullErr["ghcr.io/acme/api:1.2"] = errors.New("denied")
	if _, err := tk.ImportImage(ctx, api.ZeaburImagePayload{Image: "ghcr.io/acme/api:1.2", ServiceID: "svc_api", Pull: true}); err == nil ||
		!strings.Contains(err.Error(), "denied") || !strings.Contains(err.Error(), "not in k3s") {
		t.Fatalf("both failed: %v", err)
	}
}

// Zeabur's operator owns its ingress DaemonSet and reverts a parked nodeSelector: parking
// stops the operator first and the restore brings it back.
func TestParkStopsOperator(t *testing.T) {
	tk, h, _ := newTakeover(t)
	ctx := context.Background()
	h.ds = []*fakeDS{{NS: "zeabur-system", Name: "ingress-controller", HostPorts: []int{80, 443}, Operated: true}}
	h.replicas["operator-system/deployment/op"] = 1
	if _, err := tk.ParkIngress(ctx); err != nil {
		t.Fatal(err)
	}
	if h.replicas["operator-system/deployment/op"] != 0 || h.ds[0].NodeSelector[zeabur.ParkLabel] == "" {
		t.Fatalf("operator %d selector %v", h.replicas["operator-system/deployment/op"], h.ds[0].NodeSelector)
	}
	if st, _ := tk.State.Load(); len(st.ParkedOperators) != 1 {
		t.Fatalf("operator not recorded: %+v", st)
	}
	if _, err := tk.RestoreIngress(ctx); err != nil {
		t.Fatal(err)
	}
	if h.replicas["operator-system/deployment/op"] != 1 || h.ds[0].NodeSelector[zeabur.ParkLabel] != "" {
		t.Fatalf("after restore: operator %d selector %v", h.replicas["operator-system/deployment/op"], h.ds[0].NodeSelector)
	}
	if st, _ := tk.State.Load(); len(st.ParkedOperators) != 0 || len(st.Workloads) != 0 {
		t.Fatalf("state after restore %+v", st)
	}
}
