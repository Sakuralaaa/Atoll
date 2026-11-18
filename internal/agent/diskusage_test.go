package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/agenttest"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDirSizeCountsBlocksAndHardLinksOnce(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.bin"), 1<<20)
	write(t, filepath.Join(dir, "sub", "b.bin"), 256<<10)
	one := agent.DirSize(context.Background(), dir)
	if one < (1<<20)+(256<<10) {
		t.Fatalf("size %d smaller than the files", one)
	}
	// a hard link and a symlink add (almost) nothing
	if err := os.Link(filepath.Join(dir, "a.bin"), filepath.Join(dir, "sub", "a-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(dir, "etc")); err != nil {
		t.Fatal(err)
	}
	if two := agent.DirSize(context.Background(), dir); two-one > 64<<10 {
		t.Fatalf("hard link or symlink counted: %d -> %d", one, two)
	}
	if n := agent.DirSize(context.Background(), filepath.Join(dir, "missing")); n != 0 {
		t.Fatalf("missing dir: %d", n)
	}
}

func TestDiskMeasureMessage(t *testing.T) {
	root := t.TempDir()
	a := agent.New(&agent.Config{DataDir: root, NodeID: "n1"}, agenttest.New(), nil, nil)
	write(t, filepath.Join(root, "volumes", "svc_a", "data", "f"), 512<<10)
	write(t, filepath.Join(root, "volumes", "svc_a", "old", "f"), 64<<10)
	write(t, filepath.Join(root, "volumes", "svc_b", "data", "f"), 1<<10)

	out, err := a.Handle(context.Background(), api.MsgDiskMeasure, json.RawMessage(`{"service_ids":["svc_a"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var du api.DiskUsagePayload
	if err := json.Unmarshal([]byte(out), &du); err != nil {
		t.Fatal(err)
	}
	if len(du.Services) != 1 || du.Services[0].ServiceID != "svc_a" || len(du.Services[0].Volumes) != 2 {
		t.Fatalf("measure svc_a: %+v", du)
	}
	if v := du.Services[0].Volumes[0]; v.Name != "data" || v.Bytes < 512<<10 {
		t.Fatalf("data volume: %+v", v)
	}
	if du.Services[0].RootfsBytes != -1 {
		t.Fatalf("rootfs without a sizer: %d", du.Services[0].RootfsBytes)
	}
	// no ids = every service
	out, err = a.Handle(context.Background(), api.MsgDiskMeasure, nil)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(out), &du)
	if len(du.Services) != 2 {
		t.Fatalf("measure all: %+v", du)
	}
}
