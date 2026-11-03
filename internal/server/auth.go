package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

const (
	sessionCookie = "atoll_session"
	sessionTTL    = 30 * 24 * time.Hour
	patPrefix     = "atl_pat_"
)

type principal struct {
	User        *store.User
	Session     bool
	TokenID     string
	Scope       string // "session" for panel sessions, else the token's legacy scope summary
	SessionHash string // hash of the session token (panel sessions only)

	// API tokens only
	Perms           map[string]bool
	Projects        map[string]bool // nil = every project
	RequireApproval bool
}

func (p *principal) actor() string {
	if p.Session {
		return "user:" + p.User.Username
	}
	return "token:" + p.TokenID
}

// inProject reports whether p may touch projectID.
func (p *principal) inProject(projectID string) bool {
	return p.Session || p.Projects == nil || p.Projects[projectID]
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
				p := &principal{User: ta.User, TokenID: ta.TokenID, Scope: ta.Scope, Perms: map[string]bool{}, RequireApproval: ta.RequireApproval}
				for _, x := range ta.Permissions {
					p.Perms[x] = true
				}
				if len(ta.Projects) > 0 {
					p.Projects = map[string]bool{}
					for _, x := range ta.Projects {
						p.Projects[x] = true
					}
				}
				return p
			}
		}
		return nil
	}
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		h := hashToken(c.Value)
		u, err := s.st.SessionUser(r.Context(), h)
		if err == nil {
			return &principal{User: u, Session: true, Scope: "session", SessionHash: h}
		}
	}
	return nil
}

// authed wraps a handler with authentication and the route's access rule (perms.go).
func (s *Server) authed(need access, fn handlerFunc) http.HandlerFunc {
	return s.wrap(func(w http.ResponseWriter, r *http.Request) error {
		p := s.authenticate(r)
		if p == nil {
			return errf(401, "unauthorized", "authentication required")
		}
		if p.Session && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			return errf(403, "csrf", "cross-origin request rejected")
		}
		if !p.Session {
			if err := s.checkToken(r, p, need); err != nil {
				if destructivePerm[need.perm] {
					// denied attempts at destructive routes are audited too
					s.st.Audit(r.Context(), p.actor(), "api."+need.perm, r.Method+" "+r.URL.Path, map[string]string{"token_id": p.TokenID, "result": "denied: " + err.Error()})
				}
				return err
			}
		}
		meta := store.AuditMeta{IP: s.clientIP(r), TokenID: p.TokenID}
		if p.Session && len(p.SessionHash) >= sessionIDLen {
			meta.SessionID = p.SessionHash[:sessionIDLen]
		}
		ctx := store.WithAuditMeta(context.WithValue(r.Context(), ctxKey{}, p), meta)
		err := fn(w, r.WithContext(ctx))
		// "api.<perm>" (not "token.<perm>", which would read like "token.delete" = a revoked token)
		if !p.Session && destructivePerm[need.perm] {
			outcome := "ok"
			if err != nil {
				outcome = err.Error()
			}
			s.st.Audit(ctx, p.actor(), "api."+need.perm, r.Method+" "+r.URL.Path, map[string]string{"token_id": p.TokenID, "result": outcome})
		}
		return err
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
		SameSite: http.SameSiteStrictMode,
		Secure:   isTLS(r),
		MaxAge:   maxAge,
	})
}

// requireConfirm runs a destructive operation. Panel sessions and API tokens that hold
// the route's permission run it directly; a token created with require_approval instead
// records a pending Confirmation (executed later by approve) and answers 202. (A token
// without the permission never gets here: authed answers 403.)
func (s *Server) requireConfirm(w http.ResponseWriter, r *http.Request, action, target, summary string, payload any, exec func(ctx context.Context) (int, any, error)) error {
	p := principalFrom(r.Context())
	if p.Session || !p.RequireApproval {
		detail := map[string]string{"summary": summary}
		if !p.Session {
			detail["token_id"] = p.TokenID
		}
		s.st.Audit(r.Context(), p.actor(), action, target, detail)
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
