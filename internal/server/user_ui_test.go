package server

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"slices"
	"strings"
	"testing"
)

func pngDataURL(t *testing.T) string {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestUserUIAPI(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	type me struct {
		Avatar   string   `json:"avatar"`
		NavOrder []string `json:"nav_order"`
	}
	var m me
	e.mustDo("GET", "/api/v1/auth/me", nil, &m, 200)
	if m.Avatar != "" || len(m.NavOrder) != 0 {
		t.Fatalf("fresh: %+v", m)
	}
	av := pngDataURL(t)
	e.mustDo("PUT", "/api/v1/me/avatar", map[string]string{"avatar": av}, nil, 200)
	e.mustDo("PUT", "/api/v1/me/nav-order", map[string]any{"order": []string{"servers", "projects"}}, nil, 200)
	e.mustDo("GET", "/api/v1/auth/me", nil, &m, 200)
	if m.Avatar != av || !slices.Equal(m.NavOrder, []string{"servers", "projects"}) {
		t.Fatalf("saved: %+v", m)
	}
	for _, bad := range []string{
		"data:image/jpeg;base64," + strings.TrimPrefix(av, "data:image/png;base64,"), // type mismatch
		"data:image/svg+xml;base64,PHN2Zy8+",
		"https://example.com/a.png",
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, maxAvatarBytes+10)),
	} {
		e.mustDo("PUT", "/api/v1/me/avatar", map[string]string{"avatar": bad}, nil, 400)
	}
	e.mustDo("PUT", "/api/v1/me/nav-order", map[string]any{"order": []string{"a", "a"}}, nil, 400)
	e.mustDo("PUT", "/api/v1/me/avatar", map[string]string{"avatar": ""}, nil, 200)
	e.mustDo("PUT", "/api/v1/me/nav-order", map[string]any{"order": []string{}}, nil, 200)
	m = me{}
	e.mustDo("GET", "/api/v1/auth/me", nil, &m, 200)
	if m.Avatar != "" || len(m.NavOrder) != 0 {
		t.Fatalf("cleared: %+v", m)
	}
}
