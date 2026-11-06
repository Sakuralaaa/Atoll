package server

import (
	"encoding/base32"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestTOTPRFC6238Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	// RFC 6238 appendix B (SHA1), last six digits
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037"} {
		got, err := totpCode(secret, time.Unix(ts, 0))
		if err != nil || got != want {
			t.Fatalf("T=%d: got %q want %q (%v)", ts, got, want, err)
		}
	}
	now := time.Unix(1111111111, 0)
	for _, d := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		c, _ := totpCode(secret, now.Add(d))
		if _, ok := verifyTOTP(secret, c, now); !ok {
			t.Fatalf("skew %v rejected", d)
		}
	}
	c, _ := totpCode(secret, now.Add(-90*time.Second))
	if _, ok := verifyTOTP(secret, c, now); ok {
		t.Fatal("code three steps old accepted")
	}
	u := otpauthURI("Atoll", "admin@panel.example.com", secret)
	if !strings.HasPrefix(u, "otpauth://totp/Atoll:admin@panel.example.com?") || !strings.Contains(u, "secret="+secret) || !strings.Contains(u, "issuer=Atoll") {
		t.Fatalf("uri %s", u)
	}
}

func pinClock(t *testing.T, at time.Time) *time.Time {
	cur := at
	old := totpNow
	totpNow = func() time.Time { return cur }
	t.Cleanup(func() { totpNow = old })
	return &cur
}

// wrongTOTP is a six-digit code that secret does not accept at now. A fixed "000000"
// matches one of the three accepted steps about once in 333,000 runs, which made
// TestTwoFactorFlow fail in CI ("enable wrong code: 200").
func wrongTOTP(secret string, now time.Time) string {
	for n := 0; ; n++ {
		c := fmt.Sprintf("%06d", n)
		if _, ok := verifyTOTP(secret, c, now); !ok {
			return c
		}
	}
}

func newJarClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 20 * time.Second}
}

func TestTwoFactorFlow(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	clock := pinClock(t, time.Now().Truncate(30*time.Second).Add(10*time.Second))
	code := func(secret string) string {
		c, _ := totpCode(secret, *clock)
		return c
	}
	tick := func() { *clock = clock.Add(30 * time.Second) }

	var st api.TwoFactorStatus
	e.mustDo("GET", "/api/v1/auth/2fa", nil, &st, 200)
	if st.Enabled {
		t.Fatal("2fa on by default")
	}
	// enrolment needs the password
	if c := e.do("POST", "/api/v1/auth/2fa/enroll", api.TwoFactorRequest{Password: "nope"}, nil); c != 401 {
		t.Fatalf("enroll wrong password: %d", c)
	}
	var en api.TwoFactorEnrollment
	e.mustDo("POST", "/api/v1/auth/2fa/enroll", api.TwoFactorRequest{Password: "correct-horse"}, &en, 200)
	if en.Secret == "" || !strings.HasPrefix(en.OTPAuthURI, "otpauth://totp/") {
		t.Fatalf("enroll: %+v", en)
	}
	// secret is encrypted at rest
	var raw string
	e.srv.st.DB.QueryRow(`SELECT totp_pending FROM users`).Scan(&raw)
	if raw == "" || strings.Contains(raw, en.Secret) {
		t.Fatalf("pending secret not encrypted: %q", raw)
	}
	if c := e.do("POST", "/api/v1/auth/2fa/enable", api.TwoFactorRequest{Code: wrongTOTP(en.Secret, *clock)}, nil); c != 401 {
		t.Fatalf("enable wrong code: %d", c)
	}
	// a second browser session gets signed out when 2FA is enabled
	other := newJarClient()
	if c := e.doWith(other, "", "POST", "/api/v1/auth/login", credentials{"admin", "correct-horse"}, nil); c != 200 {
		t.Fatalf("second login: %d", c)
	}
	var rc api.RecoveryCodes
	e.mustDo("POST", "/api/v1/auth/2fa/enable", api.TwoFactorRequest{Code: code(en.Secret)}, &rc, 200)
	if len(rc.Codes) != 10 {
		t.Fatalf("recovery codes: %v", rc.Codes)
	}
	e.mustDo("GET", "/api/v1/auth/me", nil, nil, 200) // current session survives
	if c := e.doWith(other, "", "GET", "/api/v1/auth/me", nil, nil); c != 401 {
		t.Fatalf("other session after enable: %d", c)
	}
	var n int
	e.srv.st.DB.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE code_hash=?`, rc.Codes[0]).Scan(&n)
	if n != 0 {
		t.Fatal("recovery codes stored in clear")
	}
	e.mustDo("GET", "/api/v1/auth/2fa", nil, &st, 200)
	if !st.Enabled || st.RecoveryCodesLeft != 10 {
		t.Fatalf("status: %+v", st)
	}

	// password alone no longer creates a session
	hc := newJarClient()
	var ch api.LoginChallenge
	if c := e.doWith(hc, "", "POST", "/api/v1/auth/login", credentials{"admin", "correct-horse"}, &ch); c != 200 || !ch.TwoFactorRequired || ch.PendingToken == "" {
		t.Fatalf("login step 1: %d %+v", c, ch)
	}
	if c := e.doWith(hc, "", "GET", "/api/v1/auth/me", nil, nil); c != 401 {
		t.Fatalf("session before second factor: %d", c)
	}
	if c := e.doWith(hc, "", "POST", "/api/v1/auth/login/2fa", api.LoginTwoFactorRequest{PendingToken: ch.PendingToken, Code: wrongTOTP(en.Secret, *clock)}, nil); c != 401 {
		t.Fatalf("wrong code: %d", c)
	}
	// the code used to enable is spent (same step): replay rejected
	if c := e.doWith(hc, "", "POST", "/api/v1/auth/login/2fa", api.LoginTwoFactorRequest{PendingToken: ch.PendingToken, Code: code(en.Secret)}, nil); c != 401 {
		t.Fatalf("replayed code: %d", c)
	}
	tick()
	if c := e.doWith(hc, "", "POST", "/api/v1/auth/login/2fa", api.LoginTwoFactorRequest{PendingToken: ch.PendingToken, Code: code(en.Secret)}, nil); c != 200 {
		t.Fatalf("right code: %d", c)
	}
	if c := e.doWith(hc, "", "GET", "/api/v1/auth/me", nil, nil); c != 200 {
		t.Fatalf("session after 2fa: %d", c)
	}
	// the pending token is single use
	if c := e.doWith(hc, "", "POST", "/api/v1/auth/login/2fa", api.LoginTwoFactorRequest{PendingToken: ch.PendingToken, Code: code(en.Secret)}, nil); c != 401 {
		t.Fatalf("reused pending token: %d", c)
	}

	// recovery code works once
	login2 := func(c2 string) int {
		h := newJarClient()
		var ch api.LoginChallenge
		e.doWith(h, "", "POST", "/api/v1/auth/login", credentials{"admin", "correct-horse"}, &ch)
		return e.doWith(h, "", "POST", "/api/v1/auth/login/2fa", api.LoginTwoFactorRequest{PendingToken: ch.PendingToken, Code: c2}, nil)
	}
	if c := login2(strings.ToUpper(rc.Codes[0])); c != 200 {
		t.Fatalf("recovery code: %d", c)
	}
	if c := login2(rc.Codes[0]); c != 401 {
		t.Fatalf("recovery code reused: %d", c)
	}
	e.mustDo("GET", "/api/v1/auth/2fa", nil, &st, 200)
	if st.RecoveryCodesLeft != 9 {
		t.Fatalf("codes left %d", st.RecoveryCodesLeft)
	}

	// sessions list + revoke others
	var ss []api.SessionInfo
	e.mustDo("GET", "/api/v1/auth/sessions", nil, &ss, 200)
	cur := 0
	for _, s := range ss {
		if s.Current {
			cur++
		}
	}
	if len(ss) < 3 || cur != 1 {
		t.Fatalf("sessions: %+v", ss)
	}
	e.mustDo("POST", "/api/v1/auth/sessions/revoke-others", nil, nil, 200)
	if c := e.doWith(hc, "", "GET", "/api/v1/auth/me", nil, nil); c != 401 {
		t.Fatalf("revoked session: %d", c)
	}

	// regenerate needs password + current TOTP
	tick()
	var rc2 api.RecoveryCodes
	e.mustDo("POST", "/api/v1/auth/2fa/recovery-codes", api.TwoFactorRequest{Password: "correct-horse", Code: code(en.Secret)}, &rc2, 200)
	if len(rc2.Codes) != 10 || rc2.Codes[0] == rc.Codes[1] {
		t.Fatal("regenerate")
	}
	if c := login2(rc.Codes[1]); c != 401 {
		t.Fatalf("old recovery code after regenerate: %d", c)
	}

	// API tokens are unaffected by 2FA
	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]string{"name": "ci", "scope": "read"}, &tok, 201)
	if c := e.doWith(http.DefaultClient, tok.Token, "GET", "/api/v1/projects", nil, nil); c != 200 {
		t.Fatalf("token with 2fa on: %d", c)
	}
	if c := e.doWith(http.DefaultClient, tok.Token, "GET", "/api/v1/auth/2fa", nil, nil); c != 403 {
		t.Fatalf("token reading 2fa: %d", c)
	}

	// disable needs password + code (a recovery code is accepted)
	if c := e.do("POST", "/api/v1/auth/2fa/disable", api.TwoFactorRequest{Password: "correct-horse", Code: wrongTOTP(en.Secret, *clock)}, nil); c != 401 {
		t.Fatalf("disable wrong code: %d", c)
	}
	e.mustDo("POST", "/api/v1/auth/2fa/disable", api.TwoFactorRequest{Password: "correct-horse", Code: rc2.Codes[0]}, nil, 204)
	var ch2 api.LoginChallenge
	if c := e.doWith(newJarClient(), "", "POST", "/api/v1/auth/login", credentials{"admin", "correct-horse"}, &ch2); c != 200 || ch2.TwoFactorRequired {
		t.Fatalf("login after disable: %d %+v", c, ch2)
	}
}

func TestReset2FA(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	clock := pinClock(t, time.Now())
	var en api.TwoFactorEnrollment
	e.mustDo("POST", "/api/v1/auth/2fa/enroll", api.TwoFactorRequest{Password: "correct-horse"}, &en, 200)
	c, _ := totpCode(en.Secret, *clock)
	e.mustDo("POST", "/api/v1/auth/2fa/enable", api.TwoFactorRequest{Code: c}, nil, 200)
	name, err := Reset2FA(e.srv.cfg.DataDir, "admin")
	if err != nil || name != "admin" {
		t.Fatalf("reset: %q %v", name, err)
	}
	if code := e.do("GET", "/api/v1/auth/me", nil, nil); code != 401 {
		t.Fatalf("session after reset-2fa: %d", code)
	}
	var ch api.LoginChallenge
	if code := e.doWith(newJarClient(), "", "POST", "/api/v1/auth/login", credentials{"admin", "correct-horse"}, &ch); code != 200 || ch.TwoFactorRequired {
		t.Fatalf("login after reset: %d %+v", code, ch)
	}
	if _, err := Reset2FA(e.srv.cfg.DataDir, "ghost"); err == nil {
		t.Fatal("unknown user accepted")
	}
}

func TestLoginLockout(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	bad := credentials{"admin", "wrong-password"}
	for i := 0; i < ipBackoff.free-1; i++ {
		if c := e.do("POST", "/api/v1/auth/login", bad, nil); c != 401 {
			t.Fatalf("attempt %d: %d", i, c)
		}
	}
	if c := e.do("POST", "/api/v1/auth/login", bad, nil); c != 401 {
		t.Fatalf("fifth attempt: %d", c)
	}
	// now locked: even the right password gets 429
	if c := e.do("POST", "/api/v1/auth/login", credentials{"admin", "correct-horse"}, nil); c != 429 {
		t.Fatalf("locked: %d", c)
	}
	// a different client IP (via the trusted loopback proxy) is not IP-locked, but the
	// account key keeps counting; the right password still works for it
	req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("other ip: %d", resp.StatusCode)
	}
}

// Panel domain proxied by Cloudflare, Caddy on loopback in front: Caddy records the Cloudflare
// edge as the client, so the real address comes from CF-Connecting-IP — but only then.
func TestClientIPCloudflare(t *testing.T) {
	s := &Server{}
	mk := func(remote, xff, cf string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if cf != "" {
			r.Header.Set("CF-Connecting-IP", cf)
		}
		return r
	}
	cases := []struct {
		name string
		r    *http.Request
		want string
	}{
		{"edge via caddy", mk("127.0.0.1:1", "172.70.1.2", "203.0.113.9"), "203.0.113.9"},
		{"edge v6 via caddy", mk("127.0.0.1:1", "2a06:98c0:3600::103", "2001:db8::7"), "2001:db8::7"},
		{"caddy trusting cloudflare appends the edge", mk("127.0.0.1:1", "203.0.113.9, 162.158.9.9", "203.0.113.9"), "203.0.113.9"},
		{"edge without the header", mk("127.0.0.1:1", "172.70.1.2", ""), "172.70.1.2"},
		{"garbage header", mk("127.0.0.1:1", "172.70.1.2", "not-an-ip"), "172.70.1.2"},
		{"direct to caddy: header is forged", mk("127.0.0.1:1", "198.51.100.66", "203.0.113.9"), "198.51.100.66"},
		{"untrusted peer: nothing is believed", mk("172.70.1.2:1", "", "203.0.113.9"), "172.70.1.2"},
		{"no proxy at all", mk("198.51.100.66:1", "", "203.0.113.9"), "198.51.100.66"},
	}
	for _, c := range cases {
		if got := s.clientIP(c.r); got != c.want {
			t.Errorf("%s: %q want %q", c.name, got, c.want)
		}
	}
}

// The audit row for a login (and its session record) carries the same client address.
func TestLoginAuditClientIPBehindCloudflare(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	login := func(xff, cf string) {
		t.Helper()
		req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct-horse"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", xff)
		req.Header.Set("CF-Connecting-IP", cf)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("login: %d", resp.StatusCode)
		}
	}
	login("172.70.1.2", "203.0.113.50")    // through Cloudflare
	login("198.51.100.66", "203.0.113.51") // straight to Caddy with a forged header
	var page api.AuditPage
	e.mustDo("GET", "/api/v1/audit?action=auth.login", nil, &page, 200)
	if len(page.Entries) < 2 {
		t.Fatalf("entries %+v", page.Entries)
	}
	direct, viaCF := page.Entries[0], page.Entries[1] // newest first
	if viaCF.IP != "203.0.113.50" || viaCF.Target != "203.0.113.50" {
		t.Fatalf("via cloudflare: %+v", viaCF)
	}
	if direct.IP != "198.51.100.66" || direct.Target != "198.51.100.66" {
		t.Fatalf("direct: %+v", direct)
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	l := newLockout()
	now := time.Unix(1_000_000, 0)
	l.now = func() time.Time { return now }
	p := backoffPolicy{free: 3, base: 10 * time.Second, max: time.Minute}
	var got []time.Duration
	for i := 0; i < 7; i++ {
		got = append(got, l.fail("k", p))
	}
	want := []time.Duration{0, 0, 10 * time.Second, 20 * time.Second, 40 * time.Second, time.Minute, time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fail %d: %v want %v (%v)", i, got[i], want[i], got)
		}
	}
	if w := l.wait("x", "k"); w != time.Minute {
		t.Fatalf("wait %v", w)
	}
	now = now.Add(25 * time.Hour)
	if w := l.wait("k"); w != 0 {
		t.Fatalf("forgotten: %v", w)
	}
	if ipKey("2001:db8:1:2:3:4:5:6") != ipKey("2001:db8:1:2::9") {
		t.Fatal("ipv6 /64 grouping")
	}
}

func TestClientIPTrustedProxy(t *testing.T) {
	s := &Server{}
	mk := func(remote string, xff ...string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		for _, x := range xff {
			r.Header.Add("X-Forwarded-For", x)
		}
		return r
	}
	cases := []struct {
		r    *http.Request
		want string
	}{
		{mk("198.51.100.4:1234", "1.2.3.4"), "198.51.100.4"},          // untrusted peer: header ignored
		{mk("127.0.0.1:1234", "203.0.113.9"), "203.0.113.9"},          // Caddy on loopback
		{mk("127.0.0.1:1234", "6.6.6.6, 203.0.113.9"), "203.0.113.9"}, // client-supplied hop on the left is ignored
		{mk("127.0.0.1:1234", "6.6.6.6", "203.0.113.9, 127.0.0.1"), "203.0.113.9"},
		{mk("127.0.0.1:1234", "junk"), "127.0.0.1"},
		{mk("127.0.0.1:1234"), "127.0.0.1"},
		{mk("[::1]:1234", "2001:db8::1"), "2001:db8::1"},
	}
	for i, c := range cases {
		if got := s.clientIP(c.r); got != c.want {
			t.Fatalf("case %d: %q want %q", i, got, c.want)
		}
	}
	s.cfg.TrustedProxies, _ = ParseTrustedProxies("none")
	if got := s.clientIP(mk("127.0.0.1:1", "203.0.113.9")); got != "127.0.0.1" {
		t.Fatalf("none: %q", got)
	}
	tp, err := ParseTrustedProxies("10.0.0.0/8, 192.0.2.1")
	if err != nil || len(tp) != 2 || !tp[1].Contains(netip.MustParseAddr("192.0.2.1")) {
		t.Fatalf("parse: %v %v", tp, err)
	}
}
