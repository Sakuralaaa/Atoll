package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestCheckHostPath(t *testing.T) {
	p := Paths{Root: "/var/lib/atoll"}
	for _, ok := range []string{"/srv/app/data", "/var/lib/docker/volumes/x/_data", "/home/me/site", "/opt/x"} {
		if err := CheckHostPath(p, ok, false); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"/", "relative", "/srv/../etc", "/etc/ssl", "/var/run/docker.sock", "/run/containerd/containerd.sock", "/proc/1", "/var/lib/atoll/volumes/x", "/var/lib/docker", "/root"} {
		if err := CheckHostPath(p, bad, false); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if err := CheckHostPath(p, "/etc/localtime", true); err != nil {
		t.Errorf("localtime ro: %v", err)
	}
	if err := CheckHostPath(p, "/etc/localtime", false); err == nil {
		t.Error("localtime rw accepted")
	}
}

func TestBuildContainerSpecHostPath(t *testing.T) {
	p := Paths{Root: "/var/lib/atoll"}
	s := api.ResolvedSpec{ServiceID: "svc_a", ProjectID: "prj_a", Image: "nginx", Volumes: []api.Volume{
		{Name: "data", MountPath: "/data", HostPath: "/srv/blog/data"},
		{Name: "html", MountPath: "/html", HostPath: "/srv/blog/html", ReadOnly: true},
		{Name: "own", MountPath: "/own"},
	}, Ports: []api.Port{{Name: "p", ContainerPort: 9000, Protocol: "tcp", PublicPort: 9000, HostIP: "127.0.0.1"}}}
	cs, err := BuildContainerSpec(p, s)
	if err != nil {
		t.Fatal(err)
	}
	if cs.Mounts[0].Source != "/srv/blog/data" || cs.Mounts[0].ReadOnly || cs.Mounts[1].Source != "/srv/blog/html" || !cs.Mounts[1].ReadOnly ||
		cs.Mounts[2].Source != p.VolumeDir("svc_a", "own") {
		t.Fatalf("mounts %+v", cs.Mounts)
	}
	if cs.Ports[0].HostIP != "127.0.0.1" || cs.Ports[0].HostPort != 9000 {
		t.Fatalf("ports %+v", cs.Ports)
	}
	s.Volumes[0].HostPath = "/etc"
	if _, err := BuildContainerSpec(p, s); err == nil || !strings.Contains(err.Error(), "never mounts") {
		t.Fatalf("denied host path: %v", err)
	}
	// hash of a spec without the new fields is unchanged (no redeploy after upgrade)
	plain := api.ResolvedSpec{ServiceID: "svc_b", Image: "x", Volumes: []api.Volume{{Name: "v", MountPath: "/v"}}, Ports: []api.Port{{Name: "w", ContainerPort: 80, Protocol: "http"}}}
	if h := SpecHash(plain); h == "" || strings.Contains(mustJSON(plain), "host_") {
		t.Fatalf("new fields leak into old specs: %s", mustJSON(plain))
	}
}

func TestVolumeTransferRefusesHostPath(t *testing.T) {
	root := t.TempDir()
	p := Paths{Root: root}
	st := NewStateStore(p)
	if err := st.Save(api.ResolvedSpec{ServiceID: "svc_h", Image: "x", Volumes: []api.Volume{{Name: "data", MountPath: "/d", HostPath: filepath.Join(root, "..", "x")}, {Name: "own", MountPath: "/o"}}}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(p.VolumeDir("svc_h", "own"), 0o755)
	v := NewVolumeTransfer(nil, p, "")
	if _, err := v.volumeDir("svc_h", "data"); err == nil || !strings.Contains(err.Error(), "adopted host paths") {
		t.Fatalf("host path volume: %v", err)
	}
	if _, err := v.volumeDir("svc_h", "own"); err != nil {
		t.Fatalf("own volume: %v", err)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
