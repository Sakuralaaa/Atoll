// Package ctlbundle writes and reads Atoll control backup bundles: a tar.gz of the control
// server's state (consistent SQLite snapshot, master.key, server.env, Caddyfile, manifest)
// encrypted with a passphrase.
//
// Encryption (format "ATOLLCB1"): scrypt(passphrase, 16-byte random salt, N=2^logN, r=8, p=1)
// derives a 256-bit key; the payload is split into 64 KiB chunks, each sealed with AES-256-GCM.
// The 12-byte nonce is an 11-byte big-endian chunk counter followed by a final-chunk flag
// (the construction age's STREAM uses), so chunks cannot be reordered, dropped or truncated
// without the last Read failing. A fresh salt per file means a fresh key per file.
//
//	header: "ATOLLCB1" | logN (1) | r (1) | p (1) | salt (16)
//	chunk:  length uint32 BE (ciphertext bytes) | ciphertext (plaintext + 16-byte tag)
package ctlbundle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"
)

const (
	magic     = "ATOLLCB1"
	chunkSize = 64 << 10
	saltLen   = 16
	// DefaultLogN is scrypt's cost (2^15: ~32 MiB of memory, well under a second on a small VPS).
	DefaultLogN = 15
	maxLogN     = 20
	// MinPassphrase is the minimum passphrase length the server accepts.
	MinPassphrase = 12
)

// ErrDecrypt is returned for a wrong passphrase or a damaged/truncated bundle.
var ErrDecrypt = errors.New("cannot decrypt the bundle: wrong passphrase, or the file is damaged or truncated")

// ErrNotBundle means the input does not start with the bundle header.
var ErrNotBundle = errors.New("not an Atoll control bundle (bad header)")

// logN can be lowered by tests.
var logN = DefaultLogN

func deriveKey(pass string, salt []byte, ln, r, p int) ([]byte, error) {
	return scrypt.Key([]byte(pass), salt, 1<<ln, r, p, 32)
}

func nonceFor(counter uint64, final bool) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[3:11], counter)
	if final {
		n[11] = 1
	}
	return n
}

type encWriter struct {
	w       io.Writer
	aead    cipher.AEAD
	buf     []byte
	counter uint64
	closed  bool
	err     error
}

// NewEncrypter returns a writer that encrypts everything written to it into w. Close must be
// called to write the final chunk (it does not close w).
func NewEncrypter(w io.Writer, passphrase string) (io.WriteCloser, error) {
	if passphrase == "" {
		return nil, errors.New("passphrase required")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := deriveKey(passphrase, salt, logN, 8, 1)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	hdr := append([]byte(magic), byte(logN), 8, 1)
	hdr = append(hdr, salt...)
	if _, err := w.Write(hdr); err != nil {
		return nil, err
	}
	return &encWriter{w: w, aead: aead, buf: make([]byte, 0, chunkSize)}, nil
}

func (e *encWriter) flush(final bool) error {
	ct := e.aead.Seal(nil, nonceFor(e.counter, final), e.buf, nil)
	e.counter++
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(ct)))
	if _, err := e.w.Write(l[:]); err != nil {
		return err
	}
	_, err := e.w.Write(ct)
	e.buf = e.buf[:0]
	return err
}

func (e *encWriter) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errors.New("write after close")
	}
	if e.err != nil {
		return 0, e.err
	}
	n := 0
	for len(p) > 0 {
		// a full buffer is only flushed once more data arrives, so the last chunk is the final one
		if len(e.buf) == chunkSize {
			if e.err = e.flush(false); e.err != nil {
				return n, e.err
			}
		}
		k := copy(e.buf[len(e.buf):chunkSize], p)
		e.buf = e.buf[:len(e.buf)+k]
		p = p[k:]
		n += k
	}
	return n, nil
}

func (e *encWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if e.err != nil {
		return e.err
	}
	return e.flush(true)
}

type decReader struct {
	r       io.Reader
	aead    cipher.AEAD
	buf     []byte
	counter uint64
	done    bool
	err     error
}

// NewDecrypter reads the header from r and returns a reader of the plaintext. A wrong
// passphrase fails on the first Read (or here, for an empty payload) with ErrDecrypt.
func NewDecrypter(r io.Reader, passphrase string) (io.Reader, error) {
	hdr := make([]byte, len(magic)+3+saltLen)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, ErrNotBundle
	}
	if string(hdr[:len(magic)]) != magic {
		return nil, ErrNotBundle
	}
	ln, rr, pp := int(hdr[len(magic)]), int(hdr[len(magic)+1]), int(hdr[len(magic)+2])
	if ln < 10 || ln > maxLogN || rr < 1 || rr > 32 || pp < 1 || pp > 16 {
		return nil, fmt.Errorf("%w: unsupported key parameters", ErrNotBundle)
	}
	key, err := deriveKey(passphrase, hdr[len(magic)+3:], ln, rr, pp)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	d := &decReader{r: r, aead: aead}
	// decrypt the first chunk now so a wrong passphrase is reported before anything is written
	if err := d.next(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *decReader) next() error {
	var l [4]byte
	if _, err := io.ReadFull(d.r, l[:]); err != nil {
		return ErrDecrypt // ended without a final chunk
	}
	n := binary.BigEndian.Uint32(l[:])
	if n < 16 || n > chunkSize+16 {
		return ErrDecrypt
	}
	ct := make([]byte, n)
	if _, err := io.ReadFull(d.r, ct); err != nil {
		return ErrDecrypt
	}
	if pt, err := d.aead.Open(nil, nonceFor(d.counter, false), ct, nil); err == nil {
		if len(pt) != chunkSize {
			return ErrDecrypt // only the final chunk may be short
		}
		d.buf, d.counter = pt, d.counter+1
		return nil
	}
	pt, err := d.aead.Open(nil, nonceFor(d.counter, true), ct, nil)
	if err != nil {
		return ErrDecrypt
	}
	d.buf, d.counter, d.done = pt, d.counter+1, true
	// nothing may follow the final chunk
	var one [1]byte
	if k, _ := d.r.Read(one[:]); k > 0 {
		return ErrDecrypt
	}
	return nil
}

func (d *decReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.err != nil {
			return 0, d.err
		}
		if d.done {
			return 0, io.EOF
		}
		if err := d.next(); err != nil {
			d.err = err
			return 0, err
		}
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}
