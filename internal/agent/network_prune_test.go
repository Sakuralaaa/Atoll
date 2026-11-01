package agent_test

import (
	"context"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/agent"
)

// The project network goes away with the project's last service on the node (project
// delete), not before, and the shared ingress network is never touched.
func TestRemoveLastServiceRemovesProjectNetwork(t *testing.T) {
	ctx := context.Background()
	m, f, _, _ := newManager(t)
	for _, s := range []func() error{
		func() error { return m.Apply(ctx, webSpec()) },
		func() error { return m.Apply(ctx, dbSpec("svc_db")) },
	} {
		if err := s(); err != nil {
			t.Fatal(err)
		}
	}
	if !f.Networks["atoll-prj_1"] || !f.Networks[agent.IngressNetwork] {
		t.Fatalf("networks = %v", f.Networks)
	}
	if err := m.Remove(ctx, "svc_web", false); err != nil {
		t.Fatal(err)
	}
	if !f.Networks["atoll-prj_1"] {
		t.Fatal("project network removed while svc_db still uses it")
	}
	if err := m.Remove(ctx, "svc_db", false); err != nil {
		t.Fatal(err)
	}
	if f.Networks["atoll-prj_1"] {
		t.Fatal("project network atoll-prj_1 leaked after its last service was removed")
	}
	if !f.Networks[agent.IngressNetwork] {
		t.Fatal("ingress network must never be pruned")
	}
	// Redeploying into the project re-creates the network.
	if err := m.Apply(ctx, dbSpec("svc_db2")); err != nil {
		t.Fatal(err)
	}
	if !f.Networks["atoll-prj_1"] {
		t.Fatal("network not re-created on next deploy")
	}
}

// Sync dropping every service of a project (server-side project delete while the agent was
// offline) prunes the network too.
func TestSyncPrunesNetworkOfDeletedProject(t *testing.T) {
	ctx := context.Background()
	m, f, _, _ := newManager(t)
	other := dbSpec("svc_other")
	other.ProjectID, other.Network = "prj_2", "atoll-prj_2"
	for _, s := range []string{"svc_a", "svc_b"} {
		if err := m.Apply(ctx, dbSpec(s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Apply(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := m.Sync(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if f.Networks["atoll-prj_1"] || f.Networks["atoll-prj_2"] {
		t.Fatalf("networks left after sync to empty: %v", f.Networks)
	}
}

// A network something else is still attached to (a mesh forwarder, a hand-started
// container) is kept; removal of the service still succeeds.
func TestRemoveKeepsNetworkWithAttachedContainers(t *testing.T) {
	ctx := context.Background()
	m, f, _, _ := newManager(t)
	if err := m.Apply(ctx, dbSpec("svc_db")); err != nil {
		t.Fatal(err)
	}
	if err := f.CreateContainer(ctx, agent.ContainerSpec{Name: "atoll-mesh-i-svc_x", Image: "fwd",
		Networks: []agent.NetworkAttach{{Name: "atoll-prj_1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, "svc_db", false); err != nil {
		t.Fatalf("remove must not fail on a busy network: %v", err)
	}
	if !f.Networks["atoll-prj_1"] {
		t.Fatal("network with an attached container was removed")
	}
	// Once the forwarder is gone, the mesh hook prunes it.
	if err := f.RemoveContainer(ctx, "atoll-mesh-i-svc_x"); err != nil {
		t.Fatal(err)
	}
	m.PruneProjectNetwork(ctx, "atoll-prj_1")
	if f.Networks["atoll-prj_1"] {
		t.Fatal("network not pruned once free")
	}
	m.PruneProjectNetwork(ctx, "atoll-prj_1") // already gone: no panic, no error
}
