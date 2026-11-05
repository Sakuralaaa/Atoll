package server

import (
	"net/http"
	"slices"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Project favorites and pins belong to the account (not the browser), so they follow the
// user across devices. A project-scoped API token sees and changes only its own projects;
// entries for other projects are kept untouched (appended after the ones it sent). Both
// lists keep the order they are sent in: favorites are the sidebar 收藏 order, pins the
// order at the top of 我的项目.

const maxProjectPrefs = 1000

func (s *Server) handleGetProjectPrefs(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	prefs, err := s.st.ProjectPrefs(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, visiblePrefs(p, prefs))
	return nil
}

func visiblePrefs(p *principal, prefs api.ProjectPrefs) api.ProjectPrefs {
	hide := func(id string) bool { return !p.inProject(id) }
	prefs.Favorites = slices.DeleteFunc(prefs.Favorites, hide)
	prefs.Pinned = slices.DeleteFunc(prefs.Pinned, hide)
	return prefs
}

func (s *Server) handlePutProjectPrefs(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	var req struct {
		Favorites []string `json:"favorites"`
		Pinned    []string `json:"pinned"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if len(req.Favorites) > maxProjectPrefs || len(req.Pinned) > maxProjectPrefs {
		return badRequest("too many projects")
	}
	for _, id := range append(slices.Clone(req.Favorites), req.Pinned...) {
		if !p.inProject(id) {
			return errf(403, "forbidden", "token has no access to project %s", id)
		}
	}
	if p.Projects != nil { // keep what a project-scoped token cannot see
		cur, err := s.st.ProjectPrefs(r.Context(), p.User.ID)
		if err != nil {
			return err
		}
		keep := func(in, cur []string) []string {
			if in == nil {
				return nil
			}
			return append(in, slices.DeleteFunc(cur, p.inProject)...)
		}
		req.Favorites, req.Pinned = keep(req.Favorites, cur.Favorites), keep(req.Pinned, cur.Pinned)
	}
	if err := s.st.SetProjectPrefs(r.Context(), p.User.ID, req.Favorites, req.Pinned); err != nil {
		return err
	}
	prefs, err := s.st.ProjectPrefs(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, visiblePrefs(p, prefs))
	return nil
}
