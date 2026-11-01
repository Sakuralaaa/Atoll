package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
	"golang.org/x/crypto/bcrypt"
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type meResponse struct {
	Username string `json:"username"`
	Auth     string `json:"auth"`  // "session" | "token"
	Scope    string `json:"scope"` // "session" or the token's legacy scope summary
	// Permissions the caller holds (every permission for panel sessions).
	Permissions []string `json:"permissions"`
	// Projects the token is limited to (empty = all projects).
	Projects        []string `json:"projects,omitempty"`
	RequireApproval bool     `json:"require_approval,omitempty"`
}

var dummyHash = sync.OnceValue(func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("atoll-dummy-password"), bcrypt.DefaultCost)
	return string(h)
})

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,64}$`)

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) error {
	n, err := s.st.CountUsers(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"setup_required": n == 0})
	return nil
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) error {
	if !sameOrigin(r) {
		return errf(403, "csrf", "cross-origin request rejected")
	}
	var c credentials
	if err := decode(r, &c); err != nil {
		return err
	}
	c.Username = strings.TrimSpace(c.Username)
	if !usernameRe.MatchString(c.Username) {
		return badRequest("username must be 1-64 chars of letters, digits, _ . @ -")
	}
	if len(c.Password) < 8 || len(c.Password) > 72 {
		return badRequest("password must be 8-72 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u, err := s.st.CreateOwner(r.Context(), c.Username, string(hash))
	if err == store.ErrConflict {
		return errf(409, "already_setup", "setup already completed; log in instead")
	}
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), "user:"+u.Username, "auth.setup", u.Username, nil)
	return s.startSession(w, r, u)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *store.User) error {
	tok := randString(43, tokenAlphabet)
	if err := s.st.CreateSessionInfo(r.Context(), hashToken(tok), u.ID, sessionTTL, s.clientIP(r), r.UserAgent()); err != nil {
		return err
	}
	setSessionCookie(w, r, tok, int(sessionTTL/time.Second))
	writeJSON(w, 200, meResponse{Username: u.Username, Auth: "session", Scope: "session", Permissions: slices.Clone(api.AllPermissions)})
	return nil
}

// tooMany answers 429 with Retry-After.
func tooMany(w http.ResponseWriter, wait time.Duration) error {
	secs := int(wait.Seconds()) + 1
	w.Header().Set("Retry-After", fmt.Sprint(secs))
	if secs < 120 {
		return errf(429, "rate_limited", "too many failed attempts; try again in %d seconds", secs)
	}
	return errf(429, "rate_limited", "too many failed attempts; try again in %d minutes", (secs+59)/60)
}

// loginFailed counts a failed attempt against the client IP and the account name and logs it.
func (s *Server) loginFailed(ip, name, reason string) {
	d1 := s.lock.fail(ipKey(ip), ipBackoff)
	d2 := s.lock.fail(accountKey(name), accountBackoff)
	lock := max(d1, d2)
	if lock > 0 {
		s.log.Warn("login failed; locked", "user", name, "ip", ip, "reason", reason, "lock", lock.Round(time.Second))
	} else {
		s.log.Warn("login failed", "user", name, "ip", ip, "reason", reason)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) error {
	if !sameOrigin(r) {
		return errf(403, "csrf", "cross-origin request rejected")
	}
	ip := s.clientIP(r)
	if wait := s.lock.wait(ipKey(ip)); wait > 0 {
		return tooMany(w, wait)
	}
	var c credentials
	if err := decode(r, &c); err != nil {
		return err
	}
	name := strings.TrimSpace(c.Username)
	if wait := s.lock.wait(accountKey(name)); wait > 0 {
		return tooMany(w, wait)
	}
	u, err := s.st.UserByName(r.Context(), name)
	if err != nil && err != store.ErrNotFound {
		return err
	}
	hash := dummyHash() // equalise timing for unknown users
	if u != nil {
		hash = u.PasswordHash
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password)) != nil || u == nil {
		s.loginFailed(ip, name, "password")
		return errf(401, "invalid_credentials", "invalid username or password")
	}
	tf, err := s.st.TOTPState(r.Context(), u.ID)
	if err != nil {
		return err
	}
	if tf.Enabled {
		tok, exp := s.mfa.create(u.ID, ip)
		writeJSON(w, 200, api.LoginChallenge{TwoFactorRequired: true, PendingToken: tok, ExpiresAt: exp})
		return nil
	}
	s.lock.reset(ipKey(ip), accountKey(name))
	s.st.Audit(r.Context(), "user:"+u.Username, "auth.login", ip, nil)
	return s.startSession(w, r, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.st.DeleteSession(r.Context(), hashToken(c.Value))
	}
	setSessionCookie(w, r, "", -1)
	w.WriteHeader(204)
	return nil
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	if p.Session {
		writeJSON(w, 200, meResponse{Username: p.User.Username, Auth: "session", Scope: "session", Permissions: slices.Clone(api.AllPermissions)})
		return nil
	}
	me := meResponse{Username: p.User.Username, Auth: "token", Scope: p.Scope, Permissions: []string{}, RequireApproval: p.RequireApproval}
	for _, x := range api.AllPermissions {
		if p.Perms[x] {
			me.Permissions = append(me.Permissions, x)
		}
	}
	for x := range p.Projects {
		me.Projects = append(me.Projects, x)
	}
	slices.Sort(me.Projects)
	writeJSON(w, 200, me)
	return nil
}

// ---------- API tokens ----------

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) error {
	ts, err := s.st.ListAPITokens(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, ts)
	return nil
}

// checkTokenGrant validates a token's permission set and project scope, and makes sure a
// token caller never hands out more than it holds itself (no escalation).
func (s *Server) checkTokenGrant(ctx context.Context, caller *principal, perms, projects []string, requireApproval bool) ([]string, error) {
	projects = slices.Compact(slices.Sorted(slices.Values(projects)))
	projects = slices.DeleteFunc(projects, func(x string) bool { return strings.TrimSpace(x) == "" })
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
	t.Token = tok
	writeJSON(w, 201, t)
	return nil
}

func (s *Server) handlePatchToken(w http.ResponseWriter, r *http.Request) error {
	var req api.TokenUpdate
	if err := decode(r, &req); err != nil {
		return err
	}
	t, err := s.st.GetAPIToken(r.Context(), r.PathValue("id"))
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
	if err := s.st.DeleteAPIToken(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	s.st.Audit(r.Context(), principalFrom(r.Context()).actor(), "token.delete", r.PathValue("id"), nil)
	w.WriteHeader(204)
	return nil
}
