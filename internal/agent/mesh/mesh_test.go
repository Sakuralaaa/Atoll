package mesh_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
	"github.com/Sakuralaaa/Atoll/internal/agent/mesh"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

const img = "atoll/forwarder:test"

func newMesh(t *testing.T, f *agenttest.Fake, dir string, ipUp *bool) *mesh.Mesh {
	t.Helper()
	m := mesh.New(f, agent.Paths{Root: dir}, nil)
	m.Image = img
	m.HasIP = func(string) bool { return *ipUp }
	return m
}

func payload() api.MeshPayload {
	return api.MeshPayload{
		TailnetIP: "100.70.1.2",
		Exports: []api.MeshExport{{ServiceID: "svc_db", ProjectID: "prj_1", Network: "atoll-prj_1", Name: "postgres",
			Ports: []api.MeshPort{{Port: 5432, Protocol: "tcp", HostPort: 41000}}}},
		Imports: []api.MeshImport{{ServiceID: "svc_cache", ProjectID: "prj_1", Network: "atoll-prj_1", Name: "redis", ServerID: "srv_b",
			Ports: []api.MeshPort{{Port: 6379, Protocol: "tcp", Target: "100.70.9.9:41001"}, {Port: 9999, Protocol: "udp", Target: "100.70.9.9:41002"}}}},
	}
}

func send(t *testing.T, m *mesh.Mesh, p api.MeshPayload) error {
	t.Helper()
	b, _ := json.Marshal(p)
	_, err := m.Handle(context.Background(), api.MsgMesh, b)
	return err
}

func TestMeshCreatesForwarders(t *testing.T) {
	f := agenttest.New()
	f.Images[img] = agent.ImageInfo{ID: "sha256:fwd"}
	up := true
	m := newMesh(t, f, t.TempDir(), &up)
	if !m.Handles(api.MsgMesh) || m.Handles(api.MsgApply) {
		t.Fatal("Handles")
	}
	if err := send(t, m, payload()); err != nil {
		t.Fatal(err)
	}
	exp, ok := f.Get("atoll-mx-svc_db")
	if !ok || exp.Info.State != "running" {
		t.Fatalf("exporter %+v", exp)
	}
	// published on the tailnet IP only, never all interfaces
	wantPorts := []agent.PortBinding{{ContainerPort: 41000, HostPort: 41000, Protocol: "tcp", HostIP: "100.70.1.2"}}
	if !reflect.DeepEqual(exp.Spec.Ports, wantPorts) {
		t.Fatalf("exporter ports %+v", exp.Spec.Ports)
	}
	if !reflect.DeepEqual(exp.Spec.Cmd, []string{"tcp,:41000,postgres:5432"}) || !reflect.DeepEqual(exp.Spec.Entrypoint, []string{"/atoll-agent", "forward"}) {
		t.Fatalf("exporter cmd %v %v", exp.Spec.Entrypoint, exp.Spec.Cmd)
	}
	if exp.Spec.Networks[0].Name != "atoll-prj_1" || len(exp.Spec.Networks[0].Aliases) != 0 {
		t.Fatalf("exporter networks %+v", exp.Spec.Networks)
	}
	imp, ok := f.Get("atoll-mi-svc_cache")
	if !ok || imp.Info.State != "running" {
		t.Fatal("importer missing")
	}
	if len(imp.Spec.Ports) != 0 {
		t.Fatal("importers never publish host ports")
	}
	if !reflect.DeepEqual(imp.Spec.Networks, []agent.NetworkAttach{{Name: "atoll-prj_1", Aliases: []string{"redis"}}}) {
		t.Fatalf("importer networks %+v", imp.Spec.Networks)
	}
	if !reflect.DeepEqual(imp.Spec.Cmd, []string{"tcp,:6379,100.70.9.9:41001", "udp,:9999,100.70.9.9:41002"}) {
		t.Fatalf("importer cmd %v", imp.Spec.Cmd)
	}
	for _, c := range []*agenttest.Container{exp, imp} {
		if c.Spec.Labels[agent.LabelService] != "" {
			t.Fatal("forwarders must not carry atoll.service (Sync would remove them, status would list them)")
		}
		if c.Spec.RestartPolicy != "unless-stopped" || c.Spec.Labels[agent.LabelRole] != "mesh" {
			t.Fatalf("labels/restart %+v", c.Spec)
		}
	}
	if m.Err() != "" {
		t.Fatal(m.Err())
	}
	// unchanged payload: no recreate
	n := len(f.CallLog())
	send(t, m, payload())
	for _, c := range f.CallLog()[n:] {
		if strings.HasPrefix(c, "create") || strings.HasPrefix(c, "remove") {
			t.Fatalf("unexpected %s", c)
		}
	}
}

func TestMeshUpdatesAndRemoves(t *testing.T) {
	f := agenttest.New()
	f.Images[img] = agent.ImageInfo{ID: "sha256:fwd"}
	up := true
	m := newMesh(t, f, t.TempDir(), &up)
	send(t, m, payload())
	p := payload()
	p.Imports[0].Ports = p.Imports[0].Ports[:1]
	p.Imports[0].Ports[0].Target = "100.70.9.10:41001" // service migrated to another node
	if err := send(t, m, p); err != nil {
		t.Fatal(err)
	}
	imp, _ := f.Get("atoll-mi-svc_cache")
	if !reflect.DeepEqual(imp.Spec.Cmd, []string{"tcp,:6379,100.70.9.10:41001"}) {
		t.Fatalf("not updated: %v", imp.Spec.Cmd)
	}
	// service moved here: no more import; project back on one node: no export either
	if err := send(t, m, api.MeshPayload{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Get("atoll-mi-svc_cache"); ok {
		t.Fatal("importer not removed")
	}
	if _, ok := f.Get("atoll-mx-svc_db"); ok {
		t.Fatal("exporter not removed")
	}
}

func TestMeshSurvivesRebootWithoutControl(t *testing.T) {
	f := agenttest.New()
	f.Images[img] = agent.ImageInfo{ID: "sha256:fwd"}
	dir := t.TempDir()
	up := true
	m := newMesh(t, f, dir, &up)
	send(t, m, payload())
	if fi, err := os.Stat(filepath.Join(dir, "mesh", "mesh.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("state file %v %v", fi, err)
	}
	// reboot: containers exited, tailscaled not up yet, a fresh agent process with no server
	f.SetState("atoll-mx-svc_db", "exited")
	f.SetState("atoll-mi-svc_cache", "exited")
	up = false
	m2 := newMesh(t, f, dir, &up)
	err := m2.Reconcile(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not up yet") || !strings.Contains(m2.Err(), "not up yet") {
		t.Fatalf("want waiting-for-tailnet error, got %v", err)
	}
	if c, _ := f.Get("atoll-mi-svc_cache"); c.Info.State != "running" {
		t.Fatal("importer must start without the tailnet IP")
	}
	c, ok := f.Get("atoll-mx-svc_db")
	if !ok {
		t.Fatal("exporter must be kept while the tailnet IP is down")
	}
	if c.Info.State == "running" {
		t.Fatal("exporter cannot bind before the IP exists")
	}
	up = true
	if err := m2.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c, _ := f.Get("atoll-mx-svc_db"); c.Info.State != "running" {
		t.Fatal("exporter not started once the tailnet IP is up")
	}
	if m2.Err() != "" {
		t.Fatal(m2.Err())
	}
	if got := m2.Current(); len(got.Exports) != 1 || len(got.Imports) != 1 {
		t.Fatalf("current %+v", got)
	}
}

func TestMeshValidation(t *testing.T) {
	bad := []func(*api.MeshPayload){
		func(p *api.MeshPayload) { p.TailnetIP = "203.0.113.4" },                 // export on a public IP
		func(p *api.MeshPayload) { p.TailnetIP = "" },                            // no IP
		func(p *api.MeshPayload) { p.Imports[0].Ports[0].Target = "8.8.8.8:53" }, // import target outside the tailnet
		func(p *api.MeshPayload) { p.Exports[0].Ports[0].HostPort = 80 },
		func(p *api.MeshPayload) { p.Exports[0].Name = "Bad Name" },
		func(p *api.MeshPayload) { p.Imports[0].ServiceID = "../etc" },
		func(p *api.MeshPayload) { p.Imports[0].Ports[0].Protocol = "sctp" },
		func(p *api.MeshPayload) { p.Exports[0].Ports = nil },
		func(p *api.MeshPayload) { p.Imports[0].Aliases = []string{"a b"} },
	}
	for i, mut := range bad {
		p := payload()
		mut(&p)
		if err := mesh.Validate(p); err == nil {
			t.Errorf("case %d: want error", i)
		}
	}
	if err := mesh.Validate(payload()); err != nil {
		t.Fatal(err)
	}
	// imports only: no tailnet IP needed on this node for the payload itself
	if err := mesh.Validate(api.MeshPayload{Imports: payload().Imports}); err != nil {
		t.Fatal(err)
	}
	f := agenttest.New()
	up := true
	m := newMesh(t, f, t.TempDir(), &up)
	p := payload()
	p.TailnetIP = "203.0.113.4"
	if err := send(t, m, p); err == nil {
		t.Fatal("handle must reject")
	}
	if len(f.Containers) != 0 {
		t.Fatal("nothing may be created for a rejected payload")
	}
}

func TestMeshBuildsForwarderImage(t *testing.T) {
	f := agenttest.New()
	dir := t.TempDir()
	bin := filepath.Join(dir, "atoll-agent")
	os.WriteFile(bin, []byte("binary"), 0o755)
	sum, _ := mesh.FileHash(bin)
	ref := mesh.ImageRef(sum)
	f.LoadRefs = []string{ref}
	up := true
	m := mesh.New(f, agent.Paths{Root: dir}, nil)
	m.Executable = func() (string, error) { return bin, nil }
	m.HasIP = func(string) bool { return up }
	if err := send(t, m, payload()); err != nil {
		t.Fatal(err)
	}
	if len(f.Loaded) != 1 || len(f.Loaded[0]) == 0 {
		t.Fatal("image archive not loaded")
	}
	if c, _ := f.Get("atoll-mi-svc_cache"); c.Spec.Image != ref {
		t.Fatalf("image %q", c.Spec.Image)
	}
	// second time: image exists, no reload
	m2 := mesh.New(f, agent.Paths{Root: dir}, nil)
	m2.Executable = m.Executable
	m2.HasIP = m.HasIP
	m2.Reconcile(context.Background())
	if len(f.Loaded) != 1 {
		t.Fatal("image rebuilt although present")
	}
}

// Removing a project's last forwarder offers its network for pruning; a network a wanted
// forwarder still uses is not offered.
func TestMeshOffersNetworkForPruning(t *testing.T) {
	f := agenttest.New()
	f.Images[img] = agent.ImageInfo{ID: "sha256:fwd"}
	up := true
	m := newMesh(t, f, t.TempDir(), &up)
	var offered []string
	m.PruneNetwork = func(_ context.Context, name string) { offered = append(offered, name) }
	if err := send(t, m, payload()); err != nil {
		t.Fatal(err)
	}
	p := payload()
	p.Imports = nil // exporter on atoll-prj_1 stays
	if err := send(t, m, p); err != nil {
		t.Fatal(err)
	}
	if len(offered) != 0 {
		t.Fatalf("network still in use offered: %v", offered)
	}
	if err := send(t, m, api.MeshPayload{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(offered, []string{"atoll-prj_1"}) {
		t.Fatalf("offered %v", offered)
	}
}
