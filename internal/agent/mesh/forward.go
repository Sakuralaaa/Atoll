package mesh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rule is one forwarded port: listen on Listen, forward every connection (tcp) or datagram
// flow (udp) to Target. Encoded as "proto,listen,target", e.g. "tcp,:5432,100.64.0.2:41000".
type Rule struct {
	Proto  string // tcp | udp
	Listen string // host:port, host may be empty
	Target string // host:port, resolved per connection (container IPs change on redeploy)
}

func (r Rule) String() string { return r.Proto + "," + r.Listen + "," + r.Target }

// ParseRule parses "proto,listen,target".
func ParseRule(s string) (Rule, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return Rule{}, fmt.Errorf("rule %q: want proto,listen,target", s)
	}
	r := Rule{Proto: parts[0], Listen: parts[1], Target: parts[2]}
	if r.Proto != "tcp" && r.Proto != "udp" {
		return Rule{}, fmt.Errorf("rule %q: proto must be tcp or udp", s)
	}
	for _, hp := range []string{r.Listen, r.Target} {
		_, port, err := net.SplitHostPort(hp)
		if err != nil {
			return Rule{}, fmt.Errorf("rule %q: %w", s, err)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return Rule{}, fmt.Errorf("rule %q: bad port %q", s, port)
		}
	}
	if h, _, _ := net.SplitHostPort(r.Target); h == "" {
		return Rule{}, fmt.Errorf("rule %q: target needs a host", s)
	}
	return r, nil
}

// Forwarder runs rules until its context ends.
type Forwarder struct {
	Log         *slog.Logger
	DialTimeout time.Duration // default 10s
	UDPIdle     time.Duration // per-client UDP flow idle timeout, default 2m
	Dial        func(ctx context.Context, network, addr string) (net.Conn, error)

	// Auth and Token: per-project mesh token handling (auth.go). Auth "" forwards plainly.
	Auth             string
	Token            []byte
	HandshakeTimeout time.Duration // require: how long a client has to send the header (default 10s)
}

func (f *Forwarder) log() *slog.Logger {
	if f.Log == nil {
		return slog.Default()
	}
	return f.Log
}

func (f *Forwarder) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	to := f.DialTimeout
	if to <= 0 {
		to = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	if f.Dial != nil {
		return f.Dial(ctx, network, addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// Listener is a bound rule (tests read the address of ":0" listens).
type Listener struct {
	Rule Rule
	Addr net.Addr
	tcp  net.Listener
	udp  net.PacketConn
}

// Bind opens every listener first so a bad rule fails before any traffic is accepted.
func (f *Forwarder) Bind(rules []Rule) ([]*Listener, error) {
	var out []*Listener
	closeAll := func() {
		for _, l := range out {
			l.Close()
		}
	}
	for _, r := range rules {
		l := &Listener{Rule: r}
		var err error
		if r.Proto == "tcp" {
			l.tcp, err = net.Listen("tcp", r.Listen)
			if err == nil {
				l.Addr = l.tcp.Addr()
			}
		} else {
			l.udp, err = net.ListenPacket("udp", r.Listen)
			if err == nil {
				l.Addr = l.udp.LocalAddr()
			}
		}
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("listen %s %s: %w", r.Proto, r.Listen, err)
		}
		out = append(out, l)
	}
	return out, nil
}

func (l *Listener) Close() {
	if l.tcp != nil {
		l.tcp.Close()
	}
	if l.udp != nil {
		l.udp.Close()
	}
}

// Serve forwards on bound listeners until ctx ends.
func (f *Forwarder) Serve(ctx context.Context, ls []*Listener) error {
	var wg sync.WaitGroup
	for _, l := range ls {
		wg.Add(1)
		go func(l *Listener) {
			defer wg.Done()
			if l.tcp != nil {
				f.serveTCP(ctx, l.tcp, l.Rule.Target)
			} else {
				f.serveUDP(ctx, l.udp, l.Rule.Target)
			}
		}(l)
	}
	<-ctx.Done()
	for _, l := range ls {
		l.Close()
	}
	wg.Wait()
	return nil
}

// Run binds and serves rules.
func (f *Forwarder) Run(ctx context.Context, rules []Rule) error {
	ls, err := f.Bind(rules)
	if err != nil {
		return err
	}
	for _, l := range ls {
		f.log().Info("forwarding", "proto", l.Rule.Proto, "listen", l.Addr.String(), "target", l.Rule.Target, "auth", authLabel(f.Auth))
	}
	return f.Serve(ctx, ls)
}

func (f *Forwarder) serveTCP(ctx context.Context, ln net.Listener, target string) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			f.log().Warn("accept", "err", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go f.pipeTCP(ctx, c, target)
	}
}

func authLabel(a string) string {
	if a == AuthNone {
		return "none"
	}
	return a // never the token
}

func (f *Forwarder) pipeTCP(ctx context.Context, in net.Conn, target string) {
	defer in.Close()
	if f.Auth == AuthRequire {
		to := f.HandshakeTimeout
		if to <= 0 {
			to = 10 * time.Second
		}
		if _, err := readHeader(in, f.Token, true, to); err != nil {
			f.log().Warn("mesh: connection rejected (missing or invalid token)", "from", in.RemoteAddr().String())
			return
		}
	}
	out, err := f.dial(ctx, "tcp", target)
	if err != nil {
		f.log().Warn("dial target", "target", target, "err", err)
		return
	}
	defer out.Close()
	if f.Auth == AuthSend {
		if _, err := out.Write(Header(f.Token)); err != nil {
			f.log().Warn("mesh: send token", "target", target, "err", err)
			return
		}
	}
	done := make(chan struct{}, 2)
	cp := func(dst net.Conn, src io.Reader) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite() // half-close: let the other side finish its reply
		} else {
			dst.Close()
		}
		done <- struct{}{}
	}
	go cp(in, out) // target -> client first, so a server-first protocol is never held up
	var src io.Reader = in
	if f.Auth == AuthAccept {
		pre, err := readHeader(in, f.Token, false, 0)
		if err != nil && !errors.Is(err, io.EOF) {
			if errors.Is(err, errBadHeader) {
				f.log().Warn("mesh: connection rejected (invalid token)", "from", in.RemoteAddr().String())
			}
			in.Close()
			out.Close()
			<-done
			return
		}
		src = io.MultiReader(bytes.NewReader(pre), in)
	}
	go cp(out, src)
	select {
	case <-done:
		<-done
	case <-ctx.Done():
	}
}

type udpFlow struct {
	conn net.Conn
	last time.Time
}

func (f *Forwarder) serveUDP(ctx context.Context, pc net.PacketConn, target string) {
	idle := f.UDPIdle
	if idle <= 0 {
		idle = 2 * time.Minute
	}
	var mu sync.Mutex
	flows := map[string]*udpFlow{}
	go func() { // reap idle flows
		t := time.NewTicker(idle / 4)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				mu.Lock()
				for k, fl := range flows {
					fl.conn.Close()
					delete(flows, k)
				}
				mu.Unlock()
				return
			case <-t.C:
				mu.Lock()
				for k, fl := range flows {
					if time.Since(fl.last) > idle {
						fl.conn.Close()
						delete(flows, k)
					}
				}
				mu.Unlock()
			}
		}
	}()
	buf := make([]byte, 64*1024)
	for {
		n, src, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		data, ok := udpIn(buf[:n], f.Auth, f.Token)
		if !ok {
			continue
		}
		if f.Auth == AuthSend {
			data = append(Header(f.Token), data...)
		}
		key := src.String()
		mu.Lock()
		fl := flows[key]
		if fl == nil {
			c, err := f.dial(ctx, "udp", target)
			if err != nil {
				mu.Unlock()
				f.log().Warn("dial udp target", "target", target, "err", err)
				continue
			}
			fl = &udpFlow{conn: c}
			flows[key] = fl
			go func(c net.Conn, src net.Addr) { // replies back to the client
				rb := make([]byte, 64*1024)
				for {
					m, err := c.Read(rb)
					if err != nil {
						return
					}
					mu.Lock()
					if cur := flows[src.String()]; cur != nil {
						cur.last = time.Now()
					}
					mu.Unlock()
					if _, err := pc.WriteTo(rb[:m], src); err != nil {
						return
					}
				}
			}(c, src)
		}
		fl.last = time.Now()
		c := fl.conn
		mu.Unlock()
		_, _ = c.Write(data)
	}
}

// ParseArgs parses forwarder command-line rules.
func ParseArgs(args []string) ([]Rule, error) {
	if len(args) == 0 {
		return nil, errors.New("usage: atoll-agent forward proto,listen,target [proto,listen,target]")
	}
	var rules []Rule
	for _, a := range args {
		r, err := ParseRule(a)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}
