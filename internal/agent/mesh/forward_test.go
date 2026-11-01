package mesh

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseRule(t *testing.T) {
	r, err := ParseRule("tcp,:5432,100.64.0.2:41000")
	if err != nil || r.Proto != "tcp" || r.Listen != ":5432" || r.Target != "100.64.0.2:41000" {
		t.Fatalf("got %+v %v", r, err)
	}
	if r.String() != "tcp,:5432,100.64.0.2:41000" {
		t.Fatalf("round trip %q", r.String())
	}
	for _, bad := range []string{"", "tcp,:1", "sctp,:1,a:2", "tcp,:0,a:1", "tcp,:1,:2", "tcp,:1,a:99999", "udp,x,a:1"} {
		if _, err := ParseRule(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
	if _, err := ParseArgs(nil); err == nil {
		t.Fatal("empty args must fail")
	}
}

func startForwarder(t *testing.T, rules []Rule) []*Listener {
	t.Helper()
	f := &Forwarder{UDPIdle: time.Second}
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

func TestForwardTCP(t *testing.T) {
	// upstream echo server that upper-cases one line, then closes after reading EOF
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				b, _ := io.ReadAll(c) // until the client half-closes
				c.Write([]byte(strings.ToUpper(string(b))))
			}(c)
		}
	}()
	ls := startForwarder(t, []Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: up.Addr().String()}})
	for i := 0; i < 3; i++ { // several connections, each re-dials the target
		c, err := net.Dial("tcp", ls[0].Addr.String())
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("hello mesh"))
		c.(*net.TCPConn).CloseWrite()
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		b, err := io.ReadAll(c)
		c.Close()
		if err != nil || string(b) != "HELLO MESH" {
			t.Fatalf("got %q %v", b, err)
		}
	}
}

func TestForwardTCPTargetDown(t *testing.T) {
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := dead.Addr().String()
	dead.Close()
	ls := startForwarder(t, []Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: addr}})
	c, err := net.Dial("tcp", ls[0].Addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := bufio.NewReader(c).ReadByte(); err == nil {
		t.Fatal("expected the forwarder to close the connection")
	}
}

func TestForwardUDP(t *testing.T) {
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
	ls := startForwarder(t, []Rule{{Proto: "udp", Listen: "127.0.0.1:0", Target: up.LocalAddr().String()}})
	for _, client := range []string{"a", "b"} { // two clients, two flows
		c, err := net.Dial("udp", ls[0].Addr.String())
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte(client))
		c.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 64)
		n, err := c.Read(buf)
		c.Close()
		if err != nil || string(buf[:n]) != "pong:"+client {
			t.Fatalf("client %s got %q %v", client, buf[:n], err)
		}
	}
}

func TestBindFailsOnBusyPort(t *testing.T) {
	busy, _ := net.Listen("tcp", "127.0.0.1:0")
	defer busy.Close()
	f := &Forwarder{}
	_, err := f.Bind([]Rule{{Proto: "tcp", Listen: "127.0.0.1:0", Target: "x:1"}, {Proto: "tcp", Listen: busy.Addr().String(), Target: "x:1"}})
	if err == nil {
		t.Fatal("want bind error")
	}
}
