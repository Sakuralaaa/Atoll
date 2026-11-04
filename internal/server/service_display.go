package server

// Service display names: a panel label next to the hostname (services.display_name).

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// displayName validates a display_name value: trimmed, at most 64 characters, no control characters.
func displayName(v json.RawMessage) (string, error) {
	var dn string
	if string(v) != "null" {
		if err := json.Unmarshal(v, &dn); err != nil {
			return "", badRequest("display_name must be a string")
		}
	}
	return checkDisplayName(dn)
}

func checkDisplayName(dn string) (string, error) {
	dn = strings.TrimSpace(dn)
	if utf8.RuneCountInString(dn) > 64 || strings.ContainsFunc(dn, unicode.IsControl) {
		return "", badRequest("display_name: at most 64 characters, no control characters")
	}
	return dn, nil
}

// onlyUnchanged reports whether every field of a PATCH equals the service's current value, as
// when the panel sends the whole form back with only the display name edited.
func onlyUnchanged(raw map[string]json.RawMessage, svc *api.Service) bool {
	b, _ := json.Marshal(svc)
	var cur map[string]json.RawMessage
	json.Unmarshal(b, &cur)
	for k, v := range raw {
		if !jsonEqual(cur[k], v) {
			return false
		}
	}
	return true
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if len(a) == 0 {
		a = json.RawMessage("null")
	}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}

// patchDisplayName takes display_name out of a service PATCH and stores it on its own: it is
// only a label, never a reason to redeploy. done means nothing else changed and the response
// has been written.
func (s *Server) patchDisplayName(w http.ResponseWriter, r *http.Request, raw map[string]json.RawMessage, svc *api.Service) (bool, error) {
	v, ok := raw["display_name"]
	if !ok {
		return false, nil
	}
	delete(raw, "display_name")
	dn, err := displayName(v)
	if err != nil {
		return false, err
	}
	ctx := r.Context()
	if dn != svc.DisplayName {
		if err := s.st.SetServiceDisplayName(ctx, svc.ID, dn); err != nil {
			return false, err
		}
		s.st.Audit(ctx, principalFrom(ctx).actor(), "service.update", svc.ID, []string{"display_name"})
		svc.DisplayName = dn
	}
	if len(raw) > 0 && !onlyUnchanged(raw, svc) {
		return false, nil
	}
	out, _, err := s.st.GetService(ctx, svc.ID)
	if err != nil {
		return false, err
	}
	s.publish("service", out)
	writeJSON(w, 200, out)
	return true, nil
}
