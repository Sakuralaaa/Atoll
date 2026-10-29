package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

const idAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// newID returns prefix + "_" + 12 random lowercase base32 chars, e.g. "svc_k3j2...".
func newID(prefix string) string {
	return prefix + "_" + randString(12, idAlphabet)
}

const tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// randString returns n chars from alphabet using crypto/rand (rejection sampling, no bias).
func randString(n int, alphabet string) string {
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	limit := 256 - 256%len(alphabet)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		for _, b := range buf {
			if int(b) < limit {
				out = append(out, alphabet[int(b)%len(alphabet)])
				if len(out) == n {
					break
				}
			}
		}
	}
	return string(out)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}
