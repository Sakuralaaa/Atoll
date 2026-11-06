package mesh

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var (
	tokA = bytes.Repeat([]byte{0xab}, TokenLen)
	tokB = bytes.Repeat([]byte{0xcd}, TokenLen)
)

func startAuthForwarder(t *testing.T, auth string, tok []byte, rules []Rule) []*Listener {
	t.Helper()
	f := &Forwarder{UDPIdle: time.Second, Auth: auth, Token: tok, HandshakeTimeout: 500 * time.Millisecond}
	ls, err := f.Bind(rules)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Serve(ctx, ls); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return ls
}

// upperServer upper-cases what a client sends until it half-closes; dials counts connections.
func upperServer(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { up.Close() })
	n := new(atomic.Int32)
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			go func(c net.Conn) {
				defer c.Close()
				b, _ := io.ReadAll(c)
				c.Write([]byte(strings.ToUpper(string(b))))
			}(c)
		}
	}()
	return up.Addr().String(), n
}

func roundTrip(t *testing.T, addr string, pre []byte, msg string) (string, error) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(append(append([]byte{}, pre...), msg...))
	c.(*net.TCPConn).CloseWrite()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := io.ReadAll(c)
	return string(b), err
}

func TestHandshakeRequire(t *testing.T) {
	target, dials := upperServer(t)
	exp := startAuthForwarder(t, AuthRequire, tokA, []Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: target}})
	if got, _ := roundTrip(t, exp[0].Addr.String(), Header(tokA), "hello"); got != "HELLO" {
		t.Fatalf("right token: %q", got)
	}
	if got, _ := roundTrip(t, exp[0].Addr.String(), Header(tokB), "hello"); got != "" {
		t.Fatalf("wrong token got through: %q", got)
	}
	if got, _ := roundTrip(t, exp[0].Addr.String(), nil, "hello"); got != "" {
		t.Fatalf("missing token got through: %q", got)
	}
	bad := Header(tokA)
	bad[4] = 2 // unknown version
	if got, _ := roundTrip(t, exp[0].Addr.String(), bad, "hello"); got != "" {
		t.Fatalf("unknown version got through: %q", got)
	}
	// a silent client is dropped after the handshake timeout, without dialing the target
	c, _ := net.Dial("tcp", exp[0].Addr.String())
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("silent client kept open")
	}
	c.Close()
	if d := dials.Load(); d != 1 {
		t.Fatalf("target dialed %d times, want only for the valid client", d)
	}
}

// Importer (send) -> exporter (require): the full path with tokens, and a mismatch.
func TestHandshakeSendToRequire(t *testing.T) {
	target, _ := upperServer(t)
	exp := startAuthForwarder(t, AuthRequire, tokA, []Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: target}})
	imp := startAuthForwarder(t, AuthSend, tokA, []Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: exp[0].Addr.String()}})
	if got, _ := roundTrip(t, imp[0].Addr.String(), nil, "via mesh"); got != "VIA MESH" {
		t.Fatalf("got %q", got)
	}
	other := startAuthForwarder(t, AuthSend, tokB, []Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: exp[0].Addr.String()}})
	if got, _ := roundTrip(t, other[0].Addr.String(), nil, "other project"); got != "" {
		t.Fatalf("another project's importer got through: %q", got)
	}
	plain := startAuthForwarder(t, AuthNone, nil, []Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: exp[0].Addr.String()}})
	if got, _ := roundTrip(t, plain[0].Addr.String(), nil, "old importer"); got != "" {
		t.Fatalf("plain importer got through a requiring exporter: %q", got)
	}
}

// Accept (rollout) mode: old importers (plain) and new ones (handshake) both work; a wrong
// token is still refused.
func TestHandshakeAccept(t *testing.T) {
	target, _ := upperServer(t)
	exp := startAuthForwarder(t, AuthAccept, tokA, []Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: target}})
	addr := exp[0].Addr.String()
	if got, _ := roundTrip(t, addr, nil, "plain"); got != "PLAIN" {
		t.Fatalf("plain: %q", got)
	}
	if got, _ := roundTrip(t, addr, Header(tokA), "token"); got != "TOKEN" {
		t.Fatalf("handshake not stripped: %q", got)
	}
	if got, _ := roundTrip(t, addr, nil, "AT"); got != "AT" { // magic prefix, then EOF: plain
		t.Fatalf("short magic-like prefix: %q", got)
	}
	if got, _ := roundTrip(t, addr, nil, ""); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if got, _ := roundTrip(t, addr, Header(tokB), "wrong"); got != "" {
		t.Fatalf("wrong token accepted: %q", got)
	}
}

// Accept mode must not hold up a protocol where the server speaks first (MySQL, SMTP).
func TestHandshakeAcceptServerFirst(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		c, err := up.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.Write([]byte("220 ready\n"))
		b := make([]byte, 64)
		n, _ := c.Read(b)
		c.Write([]byte("echo:" + string(b[:n])))
	}()
	exp := startAuthForwarder(t, AuthAccept, tokA, []Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: up.Addr().String()}})
	c, err := net.Dial("tcp", exp[0].Addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 64)
	n, err := c.Read(b)
	if err != nil || string(b[:n]) != "220 ready\n" {
		t.Fatalf("greeting %q %v", b[:n], err)
	}
	c.Write([]byte("HELO"))
	n, err = c.Read(b)
	if err != nil || string(b[:n]) != "echo:HELO" {
		t.Fatalf("reply %q %v", b[:n], err)
	}
}

func TestHandshakeUDP(t *testing.T) {
	up, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, src, err := up.ReadFrom(buf)
			if err != nil {
				return
			}
			up.WriteTo([]byte("pong:"+string(buf[:n])), src)
		}
	}()
	ask := func(addr string, msg []byte) string {
		c, err := net.Dial("udp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.Write(msg)
		c.SetReadDeadline(time.Now().Add(700 * time.Millisecond))
		buf := make([]byte, 128)
		n, err := c.Read(buf)
		if err != nil {
			return ""
		}
		return string(buf[:n])
	}
	req := startAuthForwarder(t, AuthRequire, tokA, []Rule{{Proto: "udp", Listen: "127.0.0.1:0", Target: up.LocalAddr().String()}})
	imp := startAuthForwarder(t, AuthSend, tokA, []Rule{{Proto: "udp", Listen: "127.0.0.1:0", Target: req[0].Addr.String()}})
	if got := ask(imp[0].Addr.String(), []byte("a")); got != "pong:a" {
		t.Fatalf("send->require %q", got)
	}
	if got := ask(req[0].Addr.String(), []byte("plain")); got != "" {
		t.Fatalf("plain datagram passed require: %q", got)
	}
	if got := ask(req[0].Addr.String(), append(Header(tokB), 'x')); got != "" {
		t.Fatalf("wrong token datagram passed: %q", got)
	}
	acc := startAuthForwarder(t, AuthAccept, tokA, []Rule{{Proto: "udp", Listen: "127.0.0.1:0", Target: up.LocalAddr().String()}})
	if got := ask(acc[0].Addr.String(), []byte("plain")); got != "pong:plain" {
		t.Fatalf("accept plain %q", got)
	}
	if got := ask(acc[0].Addr.String(), append(Header(tokA), 'y')); got != "pong:y" {
		t.Fatalf("accept stripped %q", got)
	}
}

func TestAuthFromEnv(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if m, tok, err := AuthFromEnv(get); err != nil || m != AuthNone || tok != nil {
		t.Fatalf("no env: %q %v %v", m, tok, err)
	}
	env[EnvAuth] = AuthRequire
	if _, _, err := AuthFromEnv(get); err == nil {
		t.Fatal("require without a token must fail")
	}
	env[EnvToken] = strings.Repeat("ab", TokenLen)
	if m, tok, err := AuthFromEnv(get); err != nil || m != AuthRequire || !bytes.Equal(tok, tokA) {
		t.Fatalf("%q %x %v", m, tok, err)
	}
	env[EnvAuth] = "bogus"
	if _, _, err := AuthFromEnv(get); err == nil {
		t.Fatal("unknown mode accepted")
	}
	env[EnvAuth], env[EnvToken] = AuthSend, "zz"
	_, _, err := AuthFromEnv(get)
	if err == nil || strings.Contains(err.Error(), "zz") {
		t.Fatalf("bad token error %v", err)
	}
}
