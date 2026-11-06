package mesh

import (
	"bytes"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// Per-project mesh tokens.
//
// The control server derives one token per project and sends it with the mesh payload. An
// importer forwarder (Auth "send") opens every TCP connection to the exporting node with a
// fixed 22-byte header and prefixes every UDP datagram with it:
//
//	magic "ATMH" (4) | version 0x01 (1) | token length 16 (1) | token (16 raw bytes)
//
// An exporter forwarder checks it:
//   - Auth "require": a connection or datagram without a valid header is dropped before the
//     target is dialed.
//   - Auth "accept" (rollout): a valid header is stripped, a header with the wrong token is
//     dropped, and traffic without the magic passes through unchanged so importers that do
//     not send it yet (older agents) keep working. The TCP target is dialed first, so
//     server-first protocols (MySQL, SMTP) from such importers are not delayed.
//   - Auth "" (no token in the payload): plain forwarding, exactly as before tokens existed.
//
// The token reaches the forwarder through the container environment (EnvToken), never the
// command line or a log line.
const (
	AuthNone    = ""
	AuthSend    = "send"
	AuthAccept  = "accept"
	AuthRequire = "require"

	EnvAuth  = "ATOLL_MESH_AUTH"
	EnvToken = "ATOLL_MESH_TOKEN"

	TokenLen      = 16
	HeaderVersion = 1
	HeaderLen     = 4 + 1 + 1 + TokenLen
)

var headerMagic = []byte("ATMH")

// ParseToken decodes a hex token (32 hex chars).
func ParseToken(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != TokenLen {
		return nil, errors.New("mesh token must be 32 hex characters")
	}
	return b, nil
}

// Header is the handshake an importer sends.
func Header(token []byte) []byte {
	h := make([]byte, 0, HeaderLen)
	h = append(h, headerMagic...)
	h = append(h, HeaderVersion, TokenLen)
	return append(h, token...)
}

// AuthFromEnv reads the forwarder's auth mode and token. An unknown mode or a bad token is an
// error (the forwarder exits rather than forwarding unchecked).
func AuthFromEnv(getenv func(string) string) (string, []byte, error) {
	mode := getenv(EnvAuth)
	switch mode {
	case AuthNone:
		return AuthNone, nil, nil
	case AuthSend, AuthAccept, AuthRequire:
	default:
		return "", nil, fmt.Errorf("%s: unknown mode %q", EnvAuth, mode)
	}
	tok, err := ParseToken(getenv(EnvToken))
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", EnvToken, err)
	}
	return mode, tok, nil
}

var errBadHeader = errors.New("mesh: missing or invalid token")

// checkHeader decides on the first bytes of a connection or datagram. hasHeader: b starts
// with the magic (then valid reports whether the whole header and token are right).
// needMore: b is a strict prefix of the magic/header and more bytes are needed to decide.
func checkHeader(b, token []byte) (hasHeader, valid, needMore bool) {
	n := len(b)
	m := n
	if m > len(headerMagic) {
		m = len(headerMagic)
	}
	if !bytes.Equal(b[:m], headerMagic[:m]) {
		return false, false, false
	}
	if n < HeaderLen {
		return true, false, true
	}
	ok := b[4] == HeaderVersion && b[5] == TokenLen && subtle.ConstantTimeCompare(b[6:HeaderLen], token) == 1
	return true, ok, false
}

// readHeader reads from c until the header is decided. It returns the bytes read that belong
// to the application (nil when a header was consumed). require: plain traffic is an error.
func readHeader(c net.Conn, token []byte, require bool, timeout time.Duration) ([]byte, error) {
	if timeout > 0 {
		_ = c.SetReadDeadline(time.Now().Add(timeout))
		defer c.SetReadDeadline(time.Time{})
	}
	buf := make([]byte, HeaderLen)
	n := 0
	for {
		k, err := c.Read(buf[n:])
		n += k
		if n > 0 {
			has, valid, more := checkHeader(buf[:n], token)
			switch {
			case !has:
				if require {
					return nil, errBadHeader
				}
				return buf[:n], nil
			case !more:
				if !valid {
					return nil, errBadHeader
				}
				return nil, nil
			}
		}
		if err != nil {
			if require {
				return nil, errBadHeader
			}
			if n > 0 && errors.Is(err, io.EOF) {
				return buf[:n], nil // a short magic-looking prefix, then the client closed: plain
			}
			return nil, err
		}
	}
}

// udpIn checks one datagram arriving at an exporter. It returns the payload to forward and
// whether to forward it.
func udpIn(b []byte, auth string, token []byte) ([]byte, bool) {
	if auth != AuthAccept && auth != AuthRequire {
		return b, true
	}
	has, valid, more := checkHeader(b, token)
	switch {
	case !has:
		return b, auth == AuthAccept
	case more || !valid:
		if more && auth == AuthAccept && len(b) < len(headerMagic) {
			return b, true // shorter than the magic: plain
		}
		return nil, false
	}
	return b[HeaderLen:], true
}
