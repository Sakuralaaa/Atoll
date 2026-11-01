package server

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// ---------- route access rules ----------
//
// Every API route names the permission an API token needs (api.Perm*). Panel sessions
// may do everything except where a route is session-only. For project-scoped tokens the
// route's resource decides which project it touches (resolved from the path) — routes
// that are not about one project are refused, except a few harmless global reads and
// list endpoints that filter their answer.

type resKind int

const (
	resAuto       resKind = iota // infer from the route pattern
	resGlobal                    // not about one project: refused for project-scoped tokens
	resGlobalRead                // global but harmless to read (servers list, templates, settings)
	resList                      // list endpoint that filters by the token's projects itself
)

type access struct {
	perm    string
	session bool // panel session only (tokens get 403 session_required)
	res     resKind
}

// Route rules. The lvl* names are the original levels; the rest are the fine-grained ones.
var (
	lvlRead    = access{perm: api.PermRead}
	lvlDeploy  = access{perm: api.PermDeploy}
	lvlSession = access{session: true}

	needDeployGlobal = access{perm: api.PermDeploy, res: resGlobal}
	needReadGlobal   = access{perm: api.PermRead, res: resGlobalRead}
	needReadList     = access{perm: api.PermRead, res: resList}
	needDelete       = access{perm: api.PermDelete}
	needMigrate      = access{perm: api.PermMigrate}
	needDomains      = access{perm: api.PermDomains}
	needRestore      = access{perm: api.PermRestore}
	needServers      = access{perm: api.PermServers, res: resGlobal}
	needSettings     = access{perm: api.PermSettings, res: resGlobal}
	needTokens       = access{perm: api.PermTokens, res: resGlobal}
)

// destructivePerm marks permissions whose every token request is audited.
var destructivePerm = map[string]bool{
	api.PermDelete: true, api.PermMigrate: true, api.PermDomains: true, api.PermRestore: true,
	api.PermServers: true, api.PermSettings: true, api.PermTokens: true,
}

var permLabel = map[string]string{
	api.PermRead: "read", api.PermDeploy: "deploy", api.PermDelete: "delete", api.PermMigrate: "migrate",
	api.PermDomains: "domains", api.PermRestore: "restore", api.PermServers: "servers",
	api.PermSettings: "settings", api.PermTokens: "tokens",
}

func (s *Server) checkToken(r *http.Request, p *principal, need access) error {
	if need.session {
		return errf(403, "session_required", "this action is only allowed from the panel (log in with a browser session)")
	}
	if !p.Perms[need.perm] {
		return errf(403, "missing_permission", "this API token lacks the %q permission; grant it in the panel (CLI & API → tokens) or use a token that has it", permLabel[need.perm])
	}
	if p.Projects == nil {
		return nil
	}
	kind, res := need.res, ""
	if kind == resAuto {
		res = routeResource(r.Pattern)
		if res == "" {
			kind = resGlobal
		}
	}
	switch kind {
	case resGlobalRead, resList:
		return nil
	case resGlobal:
		return errf(403, "project_scope", "this API token is limited to specific projects and cannot use %s", r.URL.Path)
	}
	pid, err := s.projectOf(r.Context(), res, r.PathValue("id"))
	if err != nil {
		return err
	}
	if pid == "" || !p.Projects[pid] {
		return errf(403, "project_scope", "this API token is not allowed to access that project")
	}
	return nil
}

// routeResource names the resource a pattern like "GET /api/v1/services/{id}/env" is about.
func routeResource(pattern string) string {
	if i := strings.Index(pattern, " "); i >= 0 {
		pattern = pattern[i+1:]
	}
	pattern = strings.TrimPrefix(pattern, "/api/v1/")
	seg := strings.Split(pattern, "/")
	if len(seg) < 2 || seg[1] != "{id}" {
		return ""
	}
	switch seg[0] {
	case "projects", "services", "domains", "backups", "restores", "imports", "migrations":
		return seg[0]
	}
	return ""
}

// projectOf resolves the project a resource belongs to ("" when it has none).
func (s *Server) projectOf(ctx context.Context, res, id string) (string, error) {
	switch res {
	case "projects":
		return id, nil
	case "services":
		svc, _, err := s.st.GetService(ctx, id)
		if err != nil {
			return "", err
		}
		return svc.ProjectID, nil
	case "domains":
		d, err := s.st.GetDomain(ctx, id)
		if err != nil {
			return "", err
		}
		return s.projectOf(ctx, "services", d.ServiceID)
	case "backups":
		b, err := s.st.GetBackup(ctx, id)
		if err != nil {
			return "", err
		}
		return s.projectOf(ctx, "services", b.ServiceID)
	case "restores":
		j, err := s.st.GetRestore(ctx, id)
		if err != nil {
			return "", err
		}
		if j.ProjectID != "" {
			return j.ProjectID, nil
		}
		return s.projectOf(ctx, "services", j.ServiceID)
	case "imports":
		j, _, err := s.st.GetImport(ctx, id, nil)
		if err != nil {
			return "", err
		}
		return j.ProjectID, nil
	case "migrations":
		j, err := s.st.GetMigration(ctx, id)
		if err != nil {
			return "", err
		}
		return j.ProjectID, nil
	}
	return "", store.ErrNotFound
}

// ---------- token permission sets ----------

// permsForScope maps the legacy scope to permissions.
func permsForScope(scope string) ([]string, bool) {
	switch scope {
	case "read":
		return []string{api.PermRead}, true
	case "", "deploy":
		return []string{api.PermRead, api.PermDeploy}, true
	case "admin":
		return slices.Clone(api.AllPermissions), true
	}
	return nil, false
}

// normalizePerms validates, de-duplicates, adds "read" and sorts into display order.
func normalizePerms(in []string) ([]string, error) {
	set := map[string]bool{api.PermRead: true}
	for _, x := range in {
		x = strings.ToLower(strings.TrimSpace(x))
		if x == "" {
			continue
		}
		if x == "admin" || x == "all" {
			for _, a := range api.AllPermissions {
				set[a] = true
			}
			continue
		}
		if !slices.Contains(api.AllPermissions, x) {
			return nil, badRequest("unknown permission %q (known: %s)", x, strings.Join(api.AllPermissions, ", "))
		}
		set[x] = true
	}
	out := []string{}
	for _, a := range api.AllPermissions {
		if set[a] {
			out = append(out, a)
		}
	}
	return out, nil
}

// scopeSummary is the legacy scope shown for a permission set.
func scopeSummary(perms []string) string {
	if len(perms) == len(api.AllPermissions) {
		return "admin"
	}
	if slices.Contains(perms, api.PermDeploy) {
		return "deploy"
	}
	return "read"
}
