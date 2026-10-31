package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP per RFC 6238: HMAC-SHA1, 30 s steps, 6 digits, accepting ±1 step of clock skew.
const (
	totpPeriod = 30
	totpDigits = 6
	totpSkew   = 1
)

// totpNow is the clock codes are checked against (tests pin it).
var totpNow = time.Now

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns a fresh 160-bit secret, base32 without padding.
func newTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b32.EncodeToString(b)
}

func totpAt(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1000000)
}

// totpCode returns the code for secret at time t (used by tests and the demo).
func totpCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	return totpAt(key, t.Unix()/totpPeriod), nil
}

// verifyTOTP checks code against secret around now. It returns the matching time
// step (for replay protection) and whether the code matched.
func verifyTOTP(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	var hit int64
	ok := 0
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		if subtle.ConstantTimeCompare([]byte(totpAt(key, cur+d)), []byte(code)) == 1 {
			hit, ok = cur+d, 1
		}
	}
	return hit, ok == 1
}

// otpauthURI builds the provisioning URI authenticator apps read from the QR code.
func otpauthURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// ---------- recovery codes ----------

const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o/1/l/i

// newRecoveryCodes returns n codes like "k7m2q-x9tpw" (~49 bits each).
func newRecoveryCodes(n int) []string {
	out := make([]string, n)
	for i := range out {
		s := randString(10, recoveryAlphabet)
		out[i] = s[:5] + "-" + s[5:]
	}
	return out
}

// normalizeRecoveryCode lowercases and strips separators and spaces.
func normalizeRecoveryCode(c string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(c) {
		if r == '-' || r == ' ' || r == '\t' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// recoveryHash is the at-rest form of a recovery code. The codes are random and long
// enough that a fast hash is safe; the prefix separates them from session hashes.
func recoveryHash(code string) string { return hashToken("recovery:" + normalizeRecoveryCode(code)) }

// looksLikeTOTP reports whether c is six digits (spaces allowed, e.g. "123 456").
func looksLikeTOTP(c string) (string, bool) {
	c = strings.ReplaceAll(strings.TrimSpace(c), " ", "")
	if len(c) != totpDigits {
		return c, false
	}
	for _, r := range c {
		if r < '0' || r > '9' {
			return c, false
		}
	}
	return c, true
}
