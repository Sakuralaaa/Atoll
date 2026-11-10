// Package secrets encrypts values at rest with AES-256-GCM using a master key
// kept in <data>/master.key (auto-generated, mode 0600).
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
)

const prefix = "v1:"

// Box encrypts and decrypts short strings.
type Box struct{ aead cipher.AEAD }

// LoadOrCreateKey reads a 32-byte key from path or creates one.
func LoadOrCreateKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) != 32 {
			return nil, fmt.Errorf("master key %s: want 32 bytes, got %d", path, len(b))
		}
		tightenKeyPerm(path)
		return b, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(key); err != nil {
		return nil, err
	}
	return key, f.Sync()
}

// tightenKeyPerm narrows an existing key file to 0600 when group/other bits
// are set (e.g. restored from a backup or copied with a loose umask). Failure
// is logged, not fatal: the key is still usable and the server must start.
func tightenKeyPerm(path string) {
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	if mode := st.Mode().Perm(); mode&0o077 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			log.Printf("warning: master key %s has mode %#o and could not be tightened to 0600: %v", path, mode, err)
			return
		}
		log.Printf("warning: master key %s had mode %#o; tightened to 0600", path, mode)
	}
}

// New builds a Box from a 32-byte key.
func New(key []byte) (*Box, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt returns "v1:" + base64(nonce|ciphertext).
func (b *Box) Encrypt(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(ct), nil
}

// Decrypt reverses Encrypt.
func (b *Box) Decrypt(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if !strings.HasPrefix(s, prefix) {
		return "", errors.New("secrets: unknown ciphertext format")
	}
	raw, err := base64.RawStdEncoding.DecodeString(s[len(prefix):])
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secrets: ciphertext too short")
	}
	pt, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
