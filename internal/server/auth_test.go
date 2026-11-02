package server

import (
	"bytes"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

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

func TestChangePassword(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	const path = "/api/v1/auth/password"

	// a second browser session that must be signed out by the change
	other := newJarClient()
	if c := e.doWith(other, "", "POST", "/api/v1/auth/login", credentials{"admin", "correct-horse"}, nil); c != 200 {
		t.Fatalf("second login: %d", c)
	}

	// API tokens cannot change the password, even with every permission
	var admin api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "all", "permissions": api.AllPermissions}, &admin, 201)
	if c := e.doWith(http.DefaultClient, admin.Token, "POST", path, api.ChangePasswordRequest{CurrentPassword: "correct-horse", NewPassword: "battery-staple"}, nil); c != 403 {
		t.Fatalf("token change: %d", c)
	}

	// validation: too short, same as current
	if c := e.do("POST", path, api.ChangePasswordRequest{CurrentPassword: "correct-horse", NewPassword: "short"}, nil); c != 400 {
		t.Fatalf("short password: %d", c)
	}
	if c := e.do("POST", path, api.ChangePasswordRequest{CurrentPassword: "correct-horse", NewPassword: "correct-horse"}, nil); c != 400 {
		t.Fatalf("same password: %d", c)
	}

	// wrong current password: 401 and counted toward the lockout
	if c := e.do("POST", path, api.ChangePasswordRequest{CurrentPassword: "wrong-password", NewPassword: "battery-staple"}, nil); c != 401 {
		t.Fatalf("wrong current: %d", c)
	}
	e.srv.lock.mu.Lock()
	acct := e.srv.lock.entries[accountKey("admin")]
	fails := 0
	if acct != nil {
		fails = acct.fails
	}
	e.srv.lock.mu.Unlock()
	if fails != 1 {
		t.Fatalf("account failures after wrong current password: %d", fails)
	}
	if c := e.do("POST", "/api/v1/auth/login", credentials{"admin", "battery-staple"}, nil); c != 401 {
		t.Fatalf("password changed by a failed request: %d", c)
	}

	// success: current session kept, other session signed out, audit entry written
	var res api.ChangePasswordResponse
	e.mustDo("POST", path, api.ChangePasswordRequest{CurrentPassword: "correct-horse", NewPassword: "battery-staple"}, &res, 200)
	if res.RevokedSessions < 1 {
		t.Fatalf("revoked: %+v", res)
	}
	e.mustDo("GET", "/api/v1/auth/me", nil, nil, 200)
	if c := e.doWith(other, "", "GET", "/api/v1/auth/me", nil, nil); c != 401 {
		t.Fatalf("other session after change: %d", c)
	}
	var n int
	e.srv.st.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='auth.password_change'`).Scan(&n)
	if n != 1 {
		t.Fatalf("audit entries: %d", n)
	}
	fresh := newJarClient()
	if c := e.doWith(fresh, "", "POST", "/api/v1/auth/login", credentials{"admin", "correct-horse"}, nil); c != 401 {
		t.Fatalf("old password still works: %d", c)
	}
	if c := e.doWith(fresh, "", "POST", "/api/v1/auth/login", credentials{"admin", "battery-staple"}, nil); c != 200 {
		t.Fatalf("new password: %d", c)
	}
}

func TestChangePasswordRequires2FA(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	clock := pinClock(t, time.Now().Truncate(30*time.Second).Add(10*time.Second))
	var en api.TwoFactorEnrollment
	e.mustDo("POST", "/api/v1/auth/2fa/enroll", api.TwoFactorRequest{Password: "correct-horse"}, &en, 200)
	c0, _ := totpCode(en.Secret, *clock)
	e.mustDo("POST", "/api/v1/auth/2fa/enable", api.TwoFactorRequest{Code: c0}, nil, 200)
	*clock = clock.Add(30 * time.Second)

	const path = "/api/v1/auth/password"
	req := api.ChangePasswordRequest{CurrentPassword: "correct-horse", NewPassword: "battery-staple"}
	if c := e.do("POST", path, req, nil); c != 401 {
		t.Fatalf("missing code: %d", c)
	}
	req.Code = "000000"
	if c := e.do("POST", path, req, nil); c != 401 {
		t.Fatalf("wrong code: %d", c)
	}
	req.Code, _ = totpCode(en.Secret, *clock)
	e.mustDo("POST", path, req, nil, 200)
	e.mustDo("GET", "/api/v1/auth/me", nil, nil, 200)
}

func TestChangePasswordLocked(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	bad := api.ChangePasswordRequest{CurrentPassword: "wrong-password", NewPassword: "battery-staple"}
	for i := 0; i < ipBackoff.free; i++ {
		if c := e.do("POST", "/api/v1/auth/password", bad, nil); c != 401 {
			t.Fatalf("attempt %d: %d", i, c)
		}
	}
	// locked: even the right current password gets 429
	if c := e.do("POST", "/api/v1/auth/password", api.ChangePasswordRequest{CurrentPassword: "correct-horse", NewPassword: "battery-staple"}, nil); c != 429 {
		t.Fatalf("locked: %d", c)
	}
}
