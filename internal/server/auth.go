package server

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/atoll-sh/atoll/internal/api"
	"github.com/atoll-sh/atoll/internal/server/store"
)

const (
	sessionCookie = "atoll_session"
	sessionTTL    = 30 * 24 * time.Hour
	patPrefix     = "atl_pat_"
)

// Access levels for routes.
const (
	lvlRead    = iota // any authenticated principal
	lvlDeploy         // session, or PAT with deploy/admin
	lvlAdmin          // session, or PAT with admin
	lvlSession        // panel session only
)

type principal struct {
	User    *store.User
	Session bool
	TokenID string
	Scope   string // "session" for panel sessions
}

func (p *principal) actor() string {
	if p.Session {
		return "user:" + p.User.Username
	}
	return "token:" + p.TokenID
}

type ctxKey struct{}

func principalFrom(ctx context.Context) *principal {
	p, _ := ctx.Value(ctxKey{}).(*principal)
	return p
}

func (s *Server) authenticate(r *http.Request) *principal {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if strings.HasPrefix(tok, patPrefix) {
			ta, err := s.st.AuthAPIToken(r.Context(), hashToken(tok))
			if err == nil {
				return &principal{User: ta.User, TokenID: ta.TokenID, Scope: ta.Scope}
			}
		}
		return nil
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		u, err := s.st.SessionUser(r.Context(), hashToken(c.Value))
		if err == nil {
			return &principal{User: u, Session: true, Scope: "session"}
		}
	}
	return nil
}

// authed wraps a handler with authentication and an access level check.
func (s *Server) authed(level int, fn handlerFunc) http.HandlerFunc {
	return s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		p := s.authenticate(r)
		if p == nil {
			return errf(401, "unauthorized", "authentication required")
		}
		if p.Session && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			return errf(403, "csrf", "cross-origin request rejected")
		}
		if !p.Session {
			switch {
			case level == lvlSession:
				return errf(403, "session_required", "this action is only allowed from the panel (log in with a browser session)")
			case p.Scope == "read" && (r.Method != http.MethodGet || level > lvlRead):
				return errf(403, "insufficient_scope", "token scope %q does not allow this request", p.Scope)
			case level == lvlAdmin && p.Scope != "admin":
				return errf(403, "insufficient_scope", "admin scope required")
			}
		}
		return fn(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

// sameOrigin rejects browser requests whose Origin header names another host.
// Non-browser clients send no Origin and pass.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" || o == "null" {
		return o == ""
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if u.Host == r.Host {
		return true
	}
	if fh := r.Header.Get("X-Forwarded-Host"); fh != "" && u.Host == fh {
		return true
	}
	return false
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isTLS(r),
		MaxAge:   maxAge,
	})
}

// requireConfirm runs exec directly for panel sessions; for PATs it records a
// pending Confirmation (executed later by approve) and answers 202.
func (s *Server) requireConfirm(w http.ResponseWriter, r *http.Request, action, target, summary string, payload any, exec func(ctx context.Context) (int, any, error)) error {
	p := principalFrom(r.Context())
	if p.Session {
		status, body, err := exec(r.Context())
		if err != nil {
			return err
		}
		if body == nil {
			w.WriteHeader(status)
			return nil
		}
		writeJSON(w, status, body)
		return nil
	}
	c := api.Confirmation{
		ID: newID("cfm"), Action: action, Target: target, Summary: summary,
		Status: "pending", CreatedBy: p.actor(), CreatedAt: time.Now().UTC(),
	}
	if err := s.st.CreateConfirmation(r.Context(), c, payload); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "confirmation.request", c.ID, map[string]string{"action": action, "target": target})
	s.publish("confirmation", c)
	writeJSON(w, http.StatusAccepted, c)
	return nil
}

// ---------- login rate limiting ----------

type loginLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, fails: map[string][]time.Time{}}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *loginLimiter) prune(ip string, now time.Time) []time.Time {
	keep := l.fails[ip][:0]
	for _, t := range l.fails[ip] {
		if now.Sub(t) < l.window {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, ip)
		return nil
	}
	l.fails[ip] = keep
	return keep
}

// blocked reports whether ip is over the limit and for how long.
func (l *loginLimiter) blocked(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	f := l.prune(ip, now)
	if len(f) >= l.max {
		return true, l.window - now.Sub(f[0])
	}
	return false, 0
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.prune(ip, time.Now()), time.Now())
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.fails, ip)
	l.mu.Unlock()
}
