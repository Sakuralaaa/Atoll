package tailnet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsTailnet(t *testing.T) {
	for ip, want := range map[string]bool{"100.64.0.1": true, "100.127.255.254": true, "100.128.0.1": false, "10.0.0.1": false, "fd7a:115c:a1e0::1": false, "x": false} {
		if IsTailnetIP(ip) != want {
			t.Errorf("IsTailnetIP(%s) != %v", ip, want)
		}
	}
	for u, want := range map[string]bool{"http://100.101.102.103:7301": true, "http://atoll-control.tail1234.ts.net:7301/": true,
		"https://panel.example.com": false, "http://[fd7a::1]:1": false, "100.64.0.9": true} {
		if IsTailnetURL(u) != want {
			t.Errorf("IsTailnetURL(%s) != %v", u, want)
		}
	}
}

func TestParseStatus(t *testing.T) {
	out := []byte(`Warning: client version mismatch
{"BackendState":"Running","MagicDNSSuffix":"tail1234.ts.net","Self":{"DNSName":"atoll-vps1.tail1234.ts.net.","TailscaleIPs":["fd7a:115c:a1e0::1","100.70.1.2"]}}`)
	st := ParseStatus(out)
	if st.BackendState != "Running" || st.IP != "100.70.1.2" || st.DNSName != "atoll-vps1.tail1234.ts.net" || st.Tailnet != "tail1234.ts.net" {
		t.Fatalf("%+v", st)
	}
	st.Installed = true
	if st.State() != "running" {
		t.Fatal(st.State())
	}
	if (Status{}).State() != "not_installed" || (Status{Installed: true, BackendState: "NeedsLogin"}).State() != "needs_login" {
		t.Fatal("state mapping")
	}
}

func TestHostnameAndUpArgs(t *testing.T) {
	if h := Hostname("VPS_1.example"); h != "atoll-vps-1-example" {
		t.Fatal(h)
	}
	if h := Hostname("atoll-control"); h != "atoll-control" {
		t.Fatal(h)
	}
	args := strings.Join(UpArgs("tskey-auth-x", "atoll-a", false), " ")
	for _, want := range []string{"--authkey=tskey-auth-x", "--hostname=atoll-a", "--ssh=false", "--accept-dns=false", "--accept-routes=false"} {
		if !strings.Contains(args, want) {
			t.Fatalf("%s missing %s", args, want)
		}
	}
	if strings.Contains(args, "--reset") || strings.Contains(args, "exit-node") {
		t.Fatal("unexpected flags", args)
	}
}

func TestLocalIP(t *testing.T) {
	old := Interfaces
	defer func() { Interfaces = old }()
	Interfaces = func() []IfAddr {
		return []IfAddr{{"eth0", "203.0.113.4"}, {"wg0", "100.96.0.1"}, {"tailscale0", "100.70.1.2"}}
	}
	if ip := LocalIP(); ip != "100.70.1.2" {
		t.Fatal(ip)
	}
	if !HasIP("100.70.1.2") || HasIP("100.70.1.3") {
		t.Fatal("HasIP")
	}
	Interfaces = func() []IfAddr { return []IfAddr{{"eth0", "203.0.113.4"}} }
	if LocalIP() != "" {
		t.Fatal("no tailnet ip expected")
	}
}

type fakeRun struct {
	calls  [][]string
	status []string // successive `status --json` outputs
	upOut  string
	upErr  error
}

func (f *fakeRun) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if len(args) > 0 && args[0] == "status" {
		s := f.status[0]
		if len(f.status) > 1 {
			f.status = f.status[1:]
		}
		return []byte(s), nil
	}
	if len(args) > 0 && args[0] == "up" {
		return []byte(f.upOut), f.upErr
	}
	return nil, nil
}

func TestUp(t *testing.T) {
	running := `{"BackendState":"Running","Self":{"TailscaleIPs":["100.70.1.2"]}}`
	f := &fakeRun{status: []string{running}}
	c := &Client{Run: f.run, Lookup: func(string) (string, error) { return "/usr/bin/tailscale", nil }}
	st, err := c.Up(context.Background(), "tskey-auth-secret", "atoll-a")
	if err != nil || st.IP != "100.70.1.2" {
		t.Fatal(st, err)
	}
	for _, call := range f.calls {
		if call[1] == "up" {
			t.Fatal("already running: must not re-run tailscale up")
		}
	}
	f = &fakeRun{status: []string{`{"BackendState":"NeedsLogin"}`, running}}
	c.Run = f.run
	if st, err = c.Up(context.Background(), "tskey-auth-secret", "atoll-a"); err != nil || st.IP != "100.70.1.2" {
		t.Fatal(st, err)
	}
	if !strings.Contains(strings.Join(f.calls[1], " "), "--accept-dns=false") {
		t.Fatalf("calls %v", f.calls)
	}
	// errors never contain the key
	f = &fakeRun{status: []string{`{"BackendState":"NeedsLogin"}`}, upOut: "invalid key tskey-auth-secret", upErr: errors.New("exit 1")}
	c.Run = f.run
	_, err = c.Up(context.Background(), "tskey-auth-secret", "atoll-a")
	if err == nil || strings.Contains(err.Error(), "tskey-auth-secret") {
		t.Fatalf("err %v", err)
	}
	// not installed
	c.Lookup = func(string) (string, error) { return "", errors.New("nope") }
	if _, err := c.Up(context.Background(), "k", "h"); err == nil {
		t.Fatal("want not installed")
	}
}

func TestProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/v1/ping" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"ok":true,"node_id":"srv_a"}`))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := Probe(ctx, nil, srv.URL+"/", "good", "srv_a"); err != nil {
		t.Fatal(err)
	}
	short, c2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer c2()
	if err := Probe(short, nil, srv.URL, "good", "srv_b"); err == nil {
		t.Fatal("other node id must fail")
	}
	short3, c3 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer c3()
	if err := Probe(short3, nil, srv.URL, "bad", "srv_a"); err == nil {
		t.Fatal("bad token must fail")
	}
}

func TestInstallScriptUsesOfficialSources(t *testing.T) {
	s := InstallScript()
	for _, want := range []string{"https://tailscale.com/install.sh", "pkgs.tailscale.com/stable", "systemctl enable --now tailscaled"} {
		if !strings.Contains(s, want) {
			t.Fatalf("install script misses %s", want)
		}
	}
}
