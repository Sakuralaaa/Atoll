package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// ---------- API tokens ----------

// Members manage their own tokens from their panel session (sub-accounts B2): they list,
// create, change and delete only tokens they own, and a token never holds more than the
// member (memberTokenGrant). Each request with such a token is narrowed again to the member's
// current permissions and servers (applyMember), so an admin's change or a disable applies at
// once. Admins keep seeing and managing every token.

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) error {
	ts, err := s.st.ListAPITokens(r.Context())
	if err != nil {
		return err
	}
	if p := principalFrom(r.Context()); p.Member {
		ts = slices.DeleteFunc(ts, func(t api.APIToken) bool { return t.OwnerID != p.User.ID })
	}
	writeJSON(w, 200, ts)
	return nil
}

// checkTokenGrant validates a token's permission set and project scope, and makes sure a
// token caller never hands out more than it holds itself (no escalation).
func (s *Server) checkTokenGrant(ctx context.Context, caller *principal, perms, projects []string, requireApproval bool) ([]string, error) {
	projects = slices.Compact(slices.Sorted(slices.Values(projects)))
	projects = slices.DeleteFunc(projects, func(x string) bool { return strings.TrimSpace(x) == "" })
	if caller.Member {
		if requireApproval {
			return nil, badRequest("sub-account tokens cannot require approval (approvals are the admin's)")
		}
		return projects, memberTokenGrant(caller, perms, projects)
	}
	if len(projects) > 0 {
		for _, g := range api.GlobalPermissions {
			if slices.Contains(perms, g) {
				return nil, badRequest("permission %q cannot be combined with a project-scoped token", g)
			}
		}
		for _, id := range projects {
			if _, err := s.st.GetProject(ctx, id); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return nil, badRequest("unknown project %q", id)
				}
				return nil, err
			}
		}
	}
	if caller.Session {
		return projects, nil
	}
	for _, x := range perms {
		if !caller.Perms[x] {
			return nil, errf(403, "escalation", "an API token cannot grant the %q permission it does not hold itself", x)
		}
	}
	if caller.Projects != nil {
		if len(projects) == 0 {
			return nil, errf(403, "escalation", "a project-scoped API token cannot grant access to all projects")
		}
		for _, id := range projects {
			if !caller.Projects[id] {
				return nil, errf(403, "escalation", "an API token cannot grant access to project %q it cannot access itself", id)
			}
		}
	}
	if caller.RequireApproval && !requireApproval {
		return nil, errf(403, "escalation", "an API token with require_approval can only create or change tokens that also require approval")
	}
	return projects, nil
}

// memberTokenGrant: a member's token holds at most the member's permissions (never servers,
// settings or tokens) and at most the projects it sees; no projects = the member's own scope,
// whatever it is at each request.
func memberTokenGrant(caller *principal, perms, projects []string) error {
	for _, x := range perms {
		if !memberPermAllowed(x) || !caller.Perms[x] {
			return errf(403, "escalation", "your account does not hold the %q permission, so its tokens cannot either", x)
		}
	}
	for _, id := range projects {
		if !caller.Projects[id] {
			return badRequest("unknown project %q", id)
		}
	}
	return nil
}

// ownToken loads token id; a member gets 404 for a token it does not own.
func (s *Server) ownToken(r *http.Request) (*api.APIToken, error) {
	t, err := s.st.GetAPIToken(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if p := principalFrom(r.Context()); p.Member && t.OwnerID != p.User.ID {
		return nil, store.ErrNotFound
	}
	return t, nil
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) error {
	var req api.TokenRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		return badRequest("name required (max 100 chars)")
	}
	perms := req.Permissions
	if len(perms) == 0 {
		var ok bool
		if perms, ok = permsForScope(req.Scope); !ok {
			return badRequest("scope must be read, deploy or admin")
		}
	}
	perms, err := normalizePerms(perms)
	if err != nil {
		return err
	}
	p := principalFrom(r.Context())
	projects, err := s.checkTokenGrant(r.Context(), p, perms, req.Projects, req.RequireApproval)
	if err != nil {
		return err
	}
	tok := patPrefix + randString(32, tokenAlphabet)
	t := api.APIToken{ID: newID("tok"), Name: req.Name, Scope: scopeSummary(perms), Permissions: perms, Projects: projects,
		RequireApproval: req.RequireApproval, Prefix: tok[:len(patPrefix)+4], CreatedAt: time.Now().UTC()}
	if err := s.st.CreateAPIToken(r.Context(), p.User.ID, t, hashToken(tok)); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "token.create", t.ID, map[string]string{"scope": t.Scope, "name": t.Name,
		"permissions": strings.Join(perms, ","), "projects": strings.Join(projects, ","), "require_approval": strconv.FormatBool(t.RequireApproval)})
	t.Token, t.Owner, t.OwnerRole = tok, p.User.Username, p.User.Role
	if t.OwnerRole == "" {
		t.OwnerRole = store.RoleAdmin
	}
	writeJSON(w, 201, t)
	return nil
}

func (s *Server) handlePatchToken(w http.ResponseWriter, r *http.Request) error {
	var req api.TokenUpdate
	if err := decode(r, &req); err != nil {
		return err
	}
	t, err := s.ownToken(r)
	if err != nil {
		return err
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if n == "" || len(n) > 100 {
			return badRequest("name required (max 100 chars)")
		}
		t.Name = n
	}
	if req.Permissions != nil {
		if t.Permissions, err = normalizePerms(*req.Permissions); err != nil {
			return err
		}
	}
	if req.Projects != nil {
		t.Projects = *req.Projects
	}
	if req.RequireApproval != nil {
		t.RequireApproval = *req.RequireApproval
	}
	p := principalFrom(r.Context())
	if t.Projects, err = s.checkTokenGrant(r.Context(), p, t.Permissions, t.Projects, t.RequireApproval); err != nil {
		return err
	}
	t.Scope = scopeSummary(t.Permissions)
	if err := s.st.UpdateAPIToken(r.Context(), *t); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "token.update", t.ID, map[string]string{"name": t.Name,
		"permissions": strings.Join(t.Permissions, ","), "projects": strings.Join(t.Projects, ","), "require_approval": strconv.FormatBool(t.RequireApproval)})
	writeJSON(w, 200, t)
	return nil
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.ownToken(r); err != nil {
		return err
	}
	if err := s.st.DeleteAPIToken(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "token.delete", r.PathValue("id"), nil)
	w.WriteHeader(204)
	return nil
}
