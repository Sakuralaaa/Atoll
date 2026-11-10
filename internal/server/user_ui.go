package server

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
)

// maxAvatarBytes caps an uploaded avatar after decoding. The panel resizes to 128x128
// before uploading, which lands well under this.
const maxAvatarBytes = 96 << 10

var navKeyRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// PUT /api/v1/me/avatar {avatar: "data:image/png;base64,…"}; an empty avatar clears it.
func (s *Server) handlePutAvatar(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	if !p.Session {
		return errf(403, "session_required", "only a panel session can change the avatar")
	}
	var req struct {
		Avatar string `json:"avatar"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Avatar != "" {
		if err := validAvatar(req.Avatar); err != nil {
			return err
		}
	}
	if err := s.st.SetAvatar(r.Context(), p.User.ID, req.Avatar); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]string{"avatar": req.Avatar})
	return nil
}

// validAvatar accepts a base64 data: URL whose bytes really are a PNG, JPEG or WebP image.
func validAvatar(v string) error {
	head, data, ok := strings.Cut(v, ",")
	mime, isB64 := strings.CutSuffix(strings.TrimPrefix(head, "data:"), ";base64")
	if !ok || !strings.HasPrefix(head, "data:") || !isB64 {
		return badRequest("avatar must be a base64 data: URL")
	}
	if base64.StdEncoding.DecodedLen(len(data)) > maxAvatarBytes+3 {
		return badRequest("avatar is too large (max 96 KB)")
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return badRequest("avatar is not valid base64")
	}
	if len(b) > maxAvatarBytes {
		return badRequest("avatar is too large (max 96 KB)")
	}
	got := http.DetectContentType(b)
	switch mime {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return badRequest("avatar must be PNG, JPEG or WebP")
	}
	if got != mime {
		return badRequest("avatar content does not match its type")
	}
	return nil
}

// PUT /api/v1/me/nav-order {order: ["projects","servers",…]}; an empty list resets it.
// Keys are the panel's own; unknown ones are kept so a newer panel's entries survive.
func (s *Server) handlePutNavOrder(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	var req struct {
		Order []string `json:"order"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if len(req.Order) > 32 {
		return badRequest("too many entries")
	}
	seen := map[string]bool{}
	for _, k := range req.Order {
		if !navKeyRe.MatchString(k) || seen[k] {
			return badRequest("invalid or duplicate entry: %s", k)
		}
		seen[k] = true
	}
	if err := s.st.SetNavOrder(r.Context(), p.User.ID, req.Order); err != nil {
		return err
	}
	writeJSON(w, 200, map[string][]string{"order": req.Order})
	return nil
}

// PUT /api/v1/me/theme {theme: "minimal"}; an empty theme resets it to the default.
// Ids are the panel's own; unknown ones are kept so a newer panel's themes survive.
func (s *Server) handlePutTheme(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	var req struct {
		Theme string `json:"theme"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Theme != "" && !navKeyRe.MatchString(req.Theme) {
		return badRequest("invalid theme: %s", req.Theme)
	}
	if err := s.st.SetTheme(r.Context(), p.User.ID, req.Theme); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]string{"theme": req.Theme})
	return nil
}
