package server

import (
	"net/http"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// Sub-accounts (notes/subaccounts.md). A member reaches only:
//   - project resources (projects/services/domains/backups/restores/imports/migrations
//     {id} routes) of projects on its assigned servers, a service or domain only when the
//     service's own server is assigned too;
//   - the global routes listed in memberAllow (each one filters its answer or checks the
//     server itself).
// Everything else is refused: fail closed, so a route added later is admin-only until it
// is reviewed and listed here.

// memberPermAllowed: permissions an admin may grant a member. servers, settings and
// tokens are panel-wide and stay with admins.
func memberPermAllowed(p string) bool {
	switch p {
	case api.PermServers, api.PermSettings, api.PermTokens:
		return false
	}
	for _, x := range api.AllPermissions {
		if x == p {
			return true
		}
	}
	return false
}

// memberAllow lists the non-project routes (method + path after /api/v1) a member may use.
// Only list a route here once its handler filters by principal.inProject / Servers.
var memberAllow = map[string]bool{
	"GET /auth/me":                      true,
	"POST /auth/password":               true,
	"POST /auth/username":               true,
	"GET /auth/2fa":                     true,
	"POST /auth/2fa/enroll":             true,
	"POST /auth/2fa/enable":             true,
	"POST /auth/2fa/disable":            true,
	"POST /auth/2fa/recovery-codes":     true,
	"GET /auth/sessions":                true,
	"DELETE /auth/sessions/{id}":        true,
	"POST /auth/sessions/revoke-others": true,
	"PUT /me/avatar":                    true,
	"GET /me/project-prefs":             true,
	"PUT /me/project-prefs":             true,
	"PUT /me/nav-order":                 true,
	"GET /projects":                     true,
	"POST /projects":                    true, // server_id checked (memberMayUse)
	"GET /servers":                      true, // filtered to Servers
	"GET /events":                       true, // frames filtered (memberSeesEvent)
	"GET /servers/{id}":                 true, // memberMayUse
	"GET /image-updates":                true,
	"GET /domains":                      true,
	"GET /backup-verifications":         true,
	"GET /templates":                    true,
	"GET /app-templates":                true,
	"POST /app-templates/{id}":          true, // server_id checked (memberMayUse); a domain needs the domains permission
	"GET /root-domains":                 true, // names only (handleListRootDomains)
}

// memberRoute is r.Pattern without the /api/v1 prefix ("GET /services/{id}").
func memberRoute(pattern string) string {
	m, path, ok := strings.Cut(pattern, " ")
	if !ok {
		return pattern
	}
	return m + " " + strings.TrimPrefix(path, "/api/v1")
}

// errMemberHidden answers for anything outside a member's reach: it does not exist.
func errMemberHidden() error { return errf(404, "not_found", "not found") }

// checkMember applies the member rules to a request (p.Member is true).
func (s *Server) checkMember(r *http.Request, p *principal, need access) error {
	if !need.session && need.perm != "" && !p.Perms[need.perm] {
		return errf(403, "missing_permission", "your account lacks the %q permission; ask the panel admin", permLabel[need.perm])
	}
	if p.Session && p.User.MustChangePassword {
		route := memberRoute(r.Pattern)
		_, path, _ := strings.Cut(route, " ")
		if !strings.HasPrefix(path, "/auth/") && !strings.HasPrefix(path, "/me/") {
			return errf(403, "must_change_password", "change the initial password first (Account & security)")
		}
	}
	if res := routeResource(r.Pattern); res != "" {
		pid, err := s.projectOf(r.Context(), res, r.PathValue("id"))
		if err != nil {
			if err == store.ErrNotFound {
				return errMemberHidden()
			}
			return err
		}
		if pid == "" || !p.Projects[pid] {
			return errMemberHidden()
		}
		if sid := s.memberServiceOf(r, res); sid != "" {
			svc, _, err := s.st.GetService(r.Context(), sid)
			if err != nil {
				return errMemberHidden()
			}
			if svc.ServerID != "" && !p.Servers[svc.ServerID] {
				return errMemberHidden()
			}
		}
		return nil
	}
	if memberAllow[memberRoute(r.Pattern)] {
		return nil
	}
	return errf(403, "admin_only", "only the panel admin can use %s", r.URL.Path)
}

// memberServiceOf names the service a services/domains/backups route touches ("" = none).
func (s *Server) memberServiceOf(r *http.Request, res string) string {
	id := r.PathValue("id")
	switch res {
	case "services":
		return id
	case "domains":
		if d, err := s.st.GetDomain(r.Context(), id); err == nil {
			return d.ServiceID
		}
	case "backups":
		if b, err := s.st.GetBackup(r.Context(), id); err == nil {
			return b.ServiceID
		}
	}
	return ""
}

// memberMayUse: false when the caller is a member and serverID is not assigned to it.
// Use it wherever a request names a server to place, move or create something on.
func memberMayUse(r *http.Request, serverID string) bool {
	p := principalFrom(r.Context())
	return p == nil || !p.Member || p.Servers[serverID]
}
