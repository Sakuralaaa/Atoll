package server

import (
	"bytes"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestAuthFlow(t *testing.T) {
	e := newTestEnv(t)
	var st map[string]bool
	e.mustDo("GET", "/api/v1/auth/status", nil, &st, 200)
	if !st["setup_required"] {
		t.Fatal("setup should be required on a fresh server")
	}
	if code := e.do("GET", "/api/v1/projects", nil, nil); code != 401 {
		t.Fatalf("unauthenticated: %d", code)
	}
	e.setup()
	e.mustDo("GET", "/api/v1/auth/status", nil, &st, 200)
	if st["setup_required"] {
		t.Fatal("setup still required")
	}
	// setup only once
	if code := e.doWith(http.DefaultClient, "", "POST", "/api/v1/auth/setup", map[string]string{"username": "evil", "password": "password123"}, nil); code != 409 {
		t.Fatalf("second setup: %d", code)
	}
	// session works
	e.mustDo("GET", "/api/v1/auth/me", nil, nil, 200)
	// logout, then login again
	e.mustDo("POST", "/api/v1/auth/logout", nil, nil, 204)
	if code := e.do("GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
	if code := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "wrong-password"}, nil); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	e.mustDo("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "correct-horse"}, nil, 200)

	// cross-origin browser POST with the session cookie is rejected
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/projects", bytes.NewBufferString(`{"name":"x"}`))
	req.Header.Set("Origin", "https://evil.example")
	resp, err := e.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("csrf: %d", resp.StatusCode)
	}

	// PAT scopes
	var read, deploy api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]string{"name": "ro", "scope": "read"}, &read, 201)
	e.mustDo("POST", "/api/v1/tokens", map[string]string{"name": "ci", "scope": "deploy"}, &deploy, 201)
	if code := e.doWith(http.DefaultClient, read.Token, "GET", "/api/v1/projects", nil, nil); code != 200 {
		t.Fatalf("read token GET: %d", code)
	}
	if code := e.doWith(http.DefaultClient, read.Token, "POST", "/api/v1/projects", map[string]string{"name": "x"}, nil); code != 403 {
		t.Fatalf("read token POST: %d", code)
	}
	if code := e.doWith(http.DefaultClient, deploy.Token, "PUT", "/api/v1/settings", api.Settings{}, nil); code != 403 {
		t.Fatalf("deploy token admin endpoint: %d", code)
	}
	if code := e.doWith(http.DefaultClient, deploy.Token, "POST", "/api/v1/confirmations/cfm_x/approve", nil, nil); code != 403 {
		t.Fatalf("token approving confirmation: %d", code)
	}
	if code := e.doWith(http.DefaultClient, "atl_pat_bogus", "GET", "/api/v1/projects", nil, nil); code != 401 {
		t.Fatalf("bogus token: %d", code)
	}
}

func TestResetPassword(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	name, pw, err := ResetPassword(e.srv.cfg.DataDir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "admin" || len(pw) < 8 {
		t.Fatalf("reset: %q %q", name, pw)
	}
	// old session revoked, new password works
	if code := e.do("GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Fatalf("old session after reset: %d", code)
	}
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}
	if code := e.doWith(hc, "", "POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": pw}, nil); code != 200 {
		t.Fatalf("login with new password: %d", code)
	}
	if _, _, err := ResetPassword(e.srv.cfg.DataDir, "ghost", "password123"); err == nil {
		t.Fatal("unknown user accepted")
	}
}
