package server

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestProjectPrefsAPI(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var a, b api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "alpha", "server_id": nodeID}, &a, 201)
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "beta", "server_id": nodeID}, &b, 201)

	var got api.ProjectPrefs
	e.mustDo("GET", "/api/v1/me/project-prefs", nil, &got, 200)
	if got.Favorites == nil || got.Pinned == nil || len(got.Favorites)+len(got.Pinned) != 0 {
		t.Fatalf("empty prefs: %+v", got)
	}
	e.mustDo("PUT", "/api/v1/me/project-prefs", map[string]any{"favorites": []string{a.ID, b.ID}, "pinned": []string{b.ID, a.ID}}, &got, 200)
	if len(got.Favorites) != 2 || !slices.Equal(got.Pinned, []string{b.ID, a.ID}) {
		t.Fatalf("put: %+v", got)
	}

	// a token limited to alpha sees only alpha and keeps beta's entries when it writes
	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "one", "permissions": []string{"read"}, "projects": []string{a.ID}}, &tok, 201)
	var scoped api.ProjectPrefs
	if c := e.doWith(http.DefaultClient, tok.Token, "GET", "/api/v1/me/project-prefs", nil, &scoped); c != 200 || !slices.Equal(scoped.Pinned, []string{a.ID}) {
		t.Fatalf("scoped get: %d %+v", c, scoped)
	}
	if c := e.doWith(http.DefaultClient, tok.Token, "PUT", "/api/v1/me/project-prefs", map[string]any{"favorites": []string{b.ID}}, nil); c != 403 {
		t.Fatalf("scoped put on other project: %d", c)
	}
	if c := e.doWith(http.DefaultClient, tok.Token, "PUT", "/api/v1/me/project-prefs", map[string]any{"favorites": []string{}}, nil); c != 200 {
		t.Fatalf("scoped put: %d", c)
	}
	e.mustDo("GET", "/api/v1/me/project-prefs", nil, &got, 200)
	if !slices.Equal(got.Favorites, []string{b.ID}) || len(got.Pinned) != 2 {
		t.Fatalf("after scoped put: %+v", got)
	}
}
