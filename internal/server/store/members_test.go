package store

import (
	"context"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestMembersAndScope(t *testing.T) {
	s := openTest(t, t.TempDir())
	ctx := context.Background()
	owner, err := s.CreateOwner(ctx, "admin", "h")
	if err != nil || owner.Role != RoleAdmin || owner.IsMember() {
		t.Fatalf("owner %+v %v", owner, err)
	}
	for _, id := range []string{"srv_a", "srv_b"} {
		if err := s.CreateNode(ctx, api.Server{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateProject(ctx, api.Project{ID: "prj_a", Name: "a", ServerID: "srv_a"}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProject(ctx, api.Project{ID: "prj_b", Name: "b", ServerID: "srv_b"}, false); err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateMember(ctx, "bob", "h2", []string{"read", "deploy"}, []string{"srv_a"}, true)
	if err != nil || !m.IsMember() || len(m.Permissions) != 2 || !m.MustChangePassword {
		t.Fatalf("member %+v %v", m, err)
	}
	sc, err := s.MemberScopeOf(ctx, m.ID)
	if err != nil || !sc.Servers["srv_a"] || sc.Servers["srv_b"] || !sc.Projects["prj_a"] || sc.Projects["prj_b"] {
		t.Fatalf("scope %+v %v", sc, err)
	}
	if err := s.CreateSession(ctx, "sess", m.ID, 3600e9); err != nil {
		t.Fatal(err)
	}
	// reassign: takes effect on the next read
	srv := []string{"srv_b"}
	if err := s.UpdateMember(ctx, m.ID, MemberUpdate{Servers: &srv}); err != nil {
		t.Fatal(err)
	}
	if sc, _ = s.MemberScopeOf(ctx, m.ID); sc.Projects["prj_a"] || !sc.Projects["prj_b"] {
		t.Fatalf("after reassign %+v", sc)
	}
	if _, err := s.SessionUser(ctx, "sess"); err != nil {
		t.Fatalf("session should survive a reassign: %v", err)
	}
	// disable: sessions gone, user marked
	yes := true
	if err := s.UpdateMember(ctx, m.ID, MemberUpdate{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "sess"); err == nil {
		t.Fatal("disabled member still has a session")
	}
	if u, _ := s.UserByID(ctx, m.ID); !u.Disabled {
		t.Fatal("not disabled")
	}
	// admins cannot be changed or deleted through the member calls
	if err := s.UpdateMember(ctx, owner.ID, MemberUpdate{Disabled: &yes}); err != ErrConflict {
		t.Fatalf("update admin: %v", err)
	}
	if err := s.DeleteMember(ctx, owner.ID); err != ErrNotFound {
		t.Fatalf("delete admin: %v", err)
	}
	if err := s.DeleteMember(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if us, _ := s.ListUsers(ctx); len(us) != 1 {
		t.Fatalf("users %d", len(us))
	}
}
