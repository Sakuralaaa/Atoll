package server

import (
	"context"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	pendingLoginTTL      = 5 * time.Minute
	pendingLoginAttempts = 5
	recoveryCodeCount    = 10
	totpIssuer           = "Atoll"
)

// ---------- pending second-factor logins (in memory; a restart just asks for the password again) ----------

type pendingLogin struct {
	userID   int64
	ip       string
	expires  time.Time
	attempts int
}

type pendingLogins struct {
	mu sync.Mutex
	m  map[string]*pendingLogin // by token hash
}

func newPendingLogins() *pendingLogins { return &pendingLogins{m: map[string]*pendingLogin{}} }

func (p *pendingLogins) create(userID int64, ip string) (string, time.Time) {
	tok := randString(43, tokenAlphabet)
	exp := time.Now().Add(pendingLoginTTL)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.m) > 10000 {
		p.pruneLocked()
	}
	p.m[hashToken(tok)] = &pendingLogin{userID: userID, ip: ip, expires: exp}
	return tok, exp
}

// get returns the live challenge for tok.
func (p *pendingLogins) get(tok string) (*pendingLogin, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pl := p.m[hashToken(tok)]
	if pl == nil || time.Now().After(pl.expires) || pl.attempts >= pendingLoginAttempts {
		delete(p.m, hashToken(tok))
		return nil, false
	}
	c := *pl
	return &c, true
}

// failed counts a wrong code; it reports whether the challenge is now used up.
func (p *pendingLogins) failed(tok string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	pl := p.m[hashToken(tok)]
	if pl == nil {
		return true
	}
	pl.attempts++
	if pl.attempts >= pendingLoginAttempts {
		delete(p.m, hashToken(tok))
		return true
	}
	return false
}

func (p *pendingLogins) done(tok string) {
	p.mu.Lock()
	delete(p.m, hashToken(tok))
	p.mu.Unlock()
}

func (p *pendingLogins) pruneLocked() {
	now := time.Now()
	for k, v := range p.m {
		if now.After(v.expires) {
			delete(p.m, k)
		}
	}
}

func (p *pendingLogins) prune() {
	p.mu.Lock()
	p.pruneLocked()
	p.mu.Unlock()
}

// ---------- second factor checks ----------

// checkSecondFactor verifies a TOTP code (or, with allowRecovery, a recovery code)
// for u. It returns the method that matched ("totp" | "recovery") or "".
func (s *Server) checkSecondFactor(ctx context.Context, u *store.User, code string, allowRecovery bool) (string, error) {
	tf, err := s.st.TOTPState(ctx, u.ID)
	if err != nil {
		return "", err
	}
	if !tf.Enabled {
		return "", nil
	}
	if c, ok := looksLikeTOTP(code); ok {
		secret, err := s.st.Box.Decrypt(tf.Secret)
		if err != nil {
			return "", err
		}
		step, ok := verifyTOTP(secret, c, totpNow())
		if !ok {
			return "", nil
		}
		fresh, err := s.st.AdvanceTOTPStep(ctx, u.ID, step)
		if err != nil || !fresh {
			return "", err // replayed code
		}
		return "totp", nil
	}
	if !allowRecovery {
		return "", nil
	}
	n := normalizeRecoveryCode(code)
	if len(n) != 10 {
		return "", nil
	}
	ok, err := s.st.ConsumeRecoveryCode(ctx, u.ID, recoveryHash(n))
	if err != nil || !ok {
		return "", err
	}
	return "recovery", nil
}

func (s *Server) handleLogin2FA(w http.ResponseWriter, r *http.Request) error {
	if !sameOrigin(r) {
		return errf(403, "csrf", "cross-origin request rejected")
	}
	ip := s.clientIP(r)
	if wait := s.lock.wait(ipKey(ip)); wait > 0 {
		return tooMany(w, wait)
	}
	var req api.LoginTwoFactorRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	pl, ok := s.mfa.get(req.PendingToken)
	if !ok {
		return errf(401, "challenge_expired", "the login expired; enter your password again")
	}
	u, err := s.st.UserByID(r.Context(), pl.userID)
	if err != nil {
		s.mfa.done(req.PendingToken)
		return errf(401, "challenge_expired", "the login expired; enter your password again")
	}
	if wait := s.lock.wait(accountKey(u.Username)); wait > 0 {
		return tooMany(w, wait)
	}
	method, err := s.checkSecondFactor(r.Context(), u, req.Code, true)
	if err != nil {
		return err
	}
	if method == "" {
		s.loginFailed(ip, u.Username, "second factor")
		if s.mfa.failed(req.PendingToken) {
			return errf(401, "challenge_expired", "too many wrong codes; enter your password again")
		}
		return errf(401, "invalid_code", "invalid verification code")
	}
	s.mfa.done(req.PendingToken)
	s.lock.reset(ipKey(ip), accountKey(u.Username))
	s.st.Audit(store.WithAuditUser(r.Context(), u), "user:"+u.Username, "auth.login", ip, map[string]string{"second_factor": method})
	if method == "recovery" {
		left, _ := s.st.RecoveryCodesLeft(r.Context(), u.ID)
		s.log.Warn("login with a recovery code", "user", u.Username, "ip", ip, "codes_left", left)
	}
	return s.startSession(w, r, u)
}

// ---------- 2FA management (panel session only) ----------

// reauth checks the password and/or code of the logged-in user, counting failures
// against the account like a login does.
func (s *Server) reauth(w http.ResponseWriter, r *http.Request, req api.TwoFactorRequest, needPassword, needCode, allowRecovery bool) error {
	p := principalFrom(r.Context())
	ip := s.clientIP(r)
	if wait := s.lock.wait(ipKey(ip), accountKey(p.User.Username)); wait > 0 {
		return tooMany(w, wait)
	}
	if needPassword && bcrypt.CompareHashAndPassword([]byte(p.User.PasswordHash), []byte(req.Password)) != nil {
		s.loginFailed(ip, p.User.Username, "re-auth password")
		return errf(401, "invalid_credentials", "wrong password")
	}
	if needCode {
		m, err := s.checkSecondFactor(r.Context(), p.User, req.Code, allowRecovery)
		if err != nil {
			return err
		}
		if m == "" {
			s.loginFailed(ip, p.User.Username, "re-auth code")
			return errf(401, "invalid_code", "invalid verification code")
		}
	}
	return nil
}

func (s *Server) handle2FAStatus(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	tf, err := s.st.TOTPState(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	st := api.TwoFactorStatus{Enabled: tf.Enabled, EnabledAt: tf.EnabledAt}
	if tf.Enabled {
		st.RecoveryCodesLeft, _ = s.st.RecoveryCodesLeft(r.Context(), p.User.ID)
	}
	writeJSON(w, 200, st)
	return nil
}

func (s *Server) handle2FAEnroll(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	var req api.TwoFactorRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	tf, err := s.st.TOTPState(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	if tf.Enabled {
		return errf(409, "already_enabled", "two-factor authentication is already on; disable it first")
	}
	if err := s.reauth(w, r, req, true, false, false); err != nil {
		return err
	}
	secret := newTOTPSecret()
	enc, err := s.st.Box.Encrypt(secret)
	if err != nil {
		return err
	}
	if err := s.st.SetTOTPPending(r.Context(), p.User.ID, enc); err != nil {
		return err
	}
	account := p.User.Username
	if h := r.Host; h != "" {
		account += "@" + h
	}
	writeJSON(w, 200, api.TwoFactorEnrollment{Secret: secret, OTPAuthURI: otpauthURI(totpIssuer, account, secret)})
	return nil
}

func (s *Server) newRecoveryCodeSet() ([]string, []string) {
	codes := newRecoveryCodes(recoveryCodeCount)
	hashes := make([]string, len(codes))
	for i, c := range codes {
		hashes[i] = recoveryHash(c)
	}
	return codes, hashes
}

func (s *Server) handle2FAEnable(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	var req api.TwoFactorRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	ip := s.clientIP(r)
	if wait := s.lock.wait(ipKey(ip), accountKey(p.User.Username)); wait > 0 {
		return tooMany(w, wait)
	}
	tf, err := s.st.TOTPState(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	if tf.Enabled {
		return errf(409, "already_enabled", "two-factor authentication is already on")
	}
	if tf.Pending == "" {
		return errf(409, "not_enrolled", "start enrollment first (POST /auth/2fa/enroll)")
	}
	secret, err := s.st.Box.Decrypt(tf.Pending)
	if err != nil {
		return err
	}
	code, _ := looksLikeTOTP(req.Code)
	step, ok := verifyTOTP(secret, code, totpNow())
	if !ok {
		s.loginFailed(ip, p.User.Username, "2fa confirm")
		return errf(401, "invalid_code", "invalid verification code; check the time on your phone and try the current code")
	}
	codes, hashes := s.newRecoveryCodeSet()
	if err := s.st.EnableTOTP(r.Context(), p.User.ID, step, hashes, p.SessionHash); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "auth.2fa_enable", p.User.Username, nil)
	s.log.Info("two-factor authentication enabled; other sessions signed out", "user", p.User.Username, "ip", ip)
	writeJSON(w, 200, api.RecoveryCodes{Codes: codes})
	return nil
}

func (s *Server) handle2FADisable(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	var req api.TwoFactorRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	tf, err := s.st.TOTPState(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	if !tf.Enabled {
		// cancel an unfinished enrollment
		if tf.Pending != "" {
			s.st.SetTOTPPending(r.Context(), p.User.ID, "")
		}
		w.WriteHeader(204)
		return nil
	}
	if err := s.reauth(w, r, req, true, true, true); err != nil {
		return err
	}
	if err := s.st.DisableTOTP(r.Context(), p.User.ID, p.SessionHash); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "auth.2fa_disable", p.User.Username, nil)
	s.log.Warn("two-factor authentication disabled; other sessions signed out", "user", p.User.Username, "ip", s.clientIP(r))
	w.WriteHeader(204)
	return nil
}

func (s *Server) handle2FARecoveryCodes(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	var req api.TwoFactorRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	tf, err := s.st.TOTPState(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	if !tf.Enabled {
		return errf(409, "not_enabled", "two-factor authentication is off")
	}
	if err := s.reauth(w, r, req, true, true, false); err != nil {
		return err
	}
	codes, hashes := s.newRecoveryCodeSet()
	if err := s.st.ReplaceRecoveryCodes(r.Context(), p.User.ID, hashes); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "auth.recovery_codes_regenerate", p.User.Username, nil)
	writeJSON(w, 200, api.RecoveryCodes{Codes: codes})
	return nil
}

// ---------- sessions ----------

const sessionIDLen = 16

var sessionIDRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	rows, err := s.st.ListSessions(r.Context(), p.User.ID)
	if err != nil {
		return err
	}
	out := make([]api.SessionInfo, 0, len(rows))
	for _, x := range rows {
		out = append(out, api.SessionInfo{ID: x.Hash[:sessionIDLen], IP: x.IP, UserAgent: x.UserAgent, Current: x.Hash == p.SessionHash, CreatedAt: x.CreatedAt, ExpiresAt: x.ExpiresAt})
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	id := r.PathValue("id")
	if !sessionIDRe.MatchString(id) {
		return store.ErrNotFound
	}
	if err := s.st.DeleteUserSessionByPrefix(r.Context(), p.User.ID, id); err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "auth.session_revoke", id, nil)
	if p.SessionHash[:sessionIDLen] == id {
		setSessionCookie(w, r, "", -1)
	}
	w.WriteHeader(204)
	return nil
}

func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	n, err := s.st.DeleteOtherSessions(r.Context(), p.User.ID, p.SessionHash)
	if err != nil {
		return err
	}
	s.st.Audit(r.Context(), p.actor(), "auth.session_revoke_others", p.User.Username, map[string]string{"count": intStr(int(n))})
	writeJSON(w, 200, map[string]int64{"revoked": n})
	return nil
}
