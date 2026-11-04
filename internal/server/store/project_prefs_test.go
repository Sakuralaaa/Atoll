package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestProjectPrefs(t *testing.T) {
	s := openTest(t, t.TempDir())
	defer s.Close()
	ctx := context.Background()
	now := time.Now()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	u, err := s.CreateOwner(ctx, "admin", "x")
	must(err)
	must(s.CreateNode(ctx, api.Server{ID: "srv_a", Name: "a", Role: "worker", Status: "online", CreatedAt: now}))
	for _, id := range []string{"prj_1", "prj_2", "prj_3"} {
		must(s.CreateProject(ctx, api.Project{ID: id, Name: id, ServerID: "srv_a", CreatedAt: now}, false))
	}
	must(s.SetProjectPrefs(ctx, u.ID, []string{"prj_2", "nope"}, []string{"prj_3", "prj_1", "prj_3"}))
	p, err := s.ProjectPrefs(ctx, u.ID)
	must(err)
	if !slices.Equal(p.Favorites, []string{"prj_2"}) || !slices.Equal(p.Pinned, []string{"prj_3", "prj_1"}) {
		t.Fatalf("prefs: %+v", p)
	}
	// nil leaves a list alone; a reorder keeps favorites
	must(s.SetProjectPrefs(ctx, u.ID, nil, []string{"prj_1", "prj_3"}))
	p, _ = s.ProjectPrefs(ctx, u.ID)
	if !slices.Equal(p.Favorites, []string{"prj_2"}) || !slices.Equal(p.Pinned, []string{"prj_1", "prj_3"}) {
		t.Fatalf("reorder: %+v", p)
	}
	// deleting a project drops its prefs
	must(s.DeleteProject(ctx, "prj_1"))
	p, _ = s.ProjectPrefs(ctx, u.ID)
	if !slices.Equal(p.Pinned, []string{"prj_3"}) {
		t.Fatalf("after delete: %+v", p)
	}
	must(s.SetProjectPrefs(ctx, u.ID, []string{}, []string{}))
	var n int
	must(s.DB.QueryRow(`SELECT COUNT(*) FROM project_prefs`).Scan(&n))
	if n != 0 {
		t.Fatalf("rows left: %d", n)
	}
}
