package server

import (
	"fmt"
	"net/http"
	"regexp"
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
	Scope    string `json:"scope"` // "session" or the token scope
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
	writeJSON(w, 200, meResponse{Username: u.Username, Auth: "session", Scope: "session"})
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
	auth := "token"
	if p.Session {
		auth = "session"
	}
	writeJSON(w, 200, meResponse{Username: p.User.Username, Auth: auth, Scope: p.Scope})
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

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Name  string `json:"name"`
		Scope string `json:"scope"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 100 {
		return badRequest("name required (max 100 chars)")
	}
	if req.Scope == "" {
		req.Scope = "deploy"
	}
	if req.Scope != "read" && req.Scope != "deploy" && req.Scope != "admin" {
		return badRequest("scope must be read, deploy or admin")
	}
	p := principalFrom(r.Context())
	tok := patPrefix + randString(32, tokenAlphabet)
	t := api.APIToken{ID: newID("tok"), Name: req.Name, Scope: req.Scope, Prefix: tok[:len(patPrefix)+4], CreatedAt: time.Now().UTC()}
	if err := s.st.CreateAPIToken(r.Context(), p.User.ID, t, hashToken(tok)); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "token.create", t.ID, map[string]string{"scope": t.Scope, "name": t.Name})
	t.Token = tok
	writeJSON(w, 201, t)
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
