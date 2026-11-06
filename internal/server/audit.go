package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
)

// ---------- audit log ----------

func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	f := store.AuditFilter{Limit: 100, Actor: strings.TrimSpace(q.Get("actor")), Action: strings.TrimSpace(q.Get("action")), User: strings.TrimSpace(q.Get("user"))}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return badRequest("limit must be a positive number")
		}
		f.Limit = min(n, 500)
	}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return badRequest("before must be an audit entry id")
		}
		f.Before = n
	}
	rows, err := s.st.ListAudit(r.Context(), f)
	if err != nil {
		return err
	}
	page := api.AuditPage{Entries: make([]api.AuditEntry, 0, len(rows))}
	for _, x := range rows {
		e := api.AuditEntry{ID: x.ID, Actor: x.Actor, Action: x.Action, Target: x.Target, IP: x.IP, TokenID: x.TokenID, SessionID: x.SessionID, CreatedAt: x.CreatedAt,
			UserID: x.UserID, UserName: x.UserName, UserRole: x.UserRole}
		if e.UserName == "" && strings.HasPrefix(x.Actor, "user:") {
			e.UserName = strings.TrimPrefix(x.Actor, "user:") // older rows: name only
		}
		switch {
		case strings.HasPrefix(x.Actor, "user:"):
			e.ActorKind = "session"
		case strings.HasPrefix(x.Actor, "token:"):
			e.ActorKind = "token"
			if e.TokenID == "" {
				e.TokenID = strings.TrimPrefix(x.Actor, "token:")
			}
		case x.Actor == "cli":
			e.ActorKind = "cli"
		default:
			e.ActorKind = "system"
		}
		e.Detail = json.RawMessage(x.Detail)
		if !json.Valid(e.Detail) || len(e.Detail) == 0 {
			e.Detail = json.RawMessage("null")
		}
		// older entries carried these inside detail only
		if e.IP == "" || e.TokenID == "" {
			var d map[string]any
			if json.Unmarshal(e.Detail, &d) == nil {
				if v, ok := d["ip"].(string); ok && e.IP == "" {
					e.IP = v
				}
				if v, ok := d["token_id"].(string); ok && e.TokenID == "" {
					e.TokenID = v
				}
			}
		}
		page.Entries = append(page.Entries, e)
	}
	if len(rows) == f.Limit && len(rows) > 0 {
		page.NextBefore = rows[len(rows)-1].ID
	}
	writeJSON(w, 200, page)
	return nil
}
