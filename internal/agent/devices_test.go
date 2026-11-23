package agent

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

type fakeFI struct{ mode fs.FileMode }

func (f fakeFI) Name() string       { return "x" }
func (f fakeFI) Size() int64        { return 0 }
func (f fakeFI) Mode() fs.FileMode  { return f.mode }
func (f fakeFI) ModTime() time.Time { return time.Time{} }
func (f fakeFI) IsDir() bool        { return f.mode.IsDir() }
func (f fakeFI) Sys() any           { return nil }

func TestBuildContainerSpecDevices(t *testing.T) {
	p := Paths{Root: t.TempDir()}
	s := api.ResolvedSpec{ServiceID: "svc1", ProjectID: "prj1", Image: "img", Devices: []string{"/dev/kvm"}, ShmSizeMB: 2048}
	cs, err := BuildContainerSpec(p, s)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cs.Devices, []string{"/dev/kvm"}) || cs.ShmSizeBytes != 2048<<20 {
		t.Fatalf("%v %d", cs.Devices, cs.ShmSizeBytes)
	}
	// a spec without devices hashes as before (no needless recreate after the upgrade)
	if SpecHash(api.ResolvedSpec{ServiceID: "svc1"}) != SpecHash(api.ResolvedSpec{ServiceID: "svc1", Devices: []string{}}) {
		t.Fatal("empty devices changed the spec hash")
	}
	for _, bad := range []api.ResolvedSpec{
		{ServiceID: "svc1", Image: "img", Devices: []string{"/dev/sda"}},
		{ServiceID: "svc1", Image: "img", ShmSizeMB: -5},
	} {
		if _, err := BuildContainerSpec(p, bad); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

func TestCheckDevice(t *testing.T) {
	defer func(f func(string) (fs.FileInfo, error)) { statDevice = f }(statDevice)
	statDevice = func(string) (fs.FileInfo, error) { return nil, os.ErrNotExist }
	if err := checkDevice("/dev/kvm"); err == nil || !strings.Contains(err.Error(), "KVM is not available") {
		t.Fatalf("missing kvm: %v", err)
	}
	statDevice = func(string) (fs.FileInfo, error) { return fakeFI{fs.ModeDevice | fs.ModeCharDevice}, nil }
	if err := checkDevice("/dev/kvm"); err != nil {
		t.Fatal(err)
	}
	statDevice = func(string) (fs.FileInfo, error) { return fakeFI{0o644}, nil }
	if err := checkDevice("/dev/kvm"); err == nil {
		t.Fatal("regular file accepted as a device")
	}
	statDevice = func(string) (fs.FileInfo, error) { return fakeFI{fs.ModeDir}, nil }
	if err := checkDevice("/dev/dri"); err != nil {
		t.Fatal(err)
	}
}
