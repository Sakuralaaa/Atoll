package agent_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/agent"
	"github.com/Sakuralaaa/Atoll/internal/agent/tailnet"
	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestConfigSwitchServer(t *testing.T) {
	c := &agent.Config{ServerURL: "https://panel.example.com/", FallbackURLs: []string{"https://old.example.com"}}
	c.SwitchServer("http://100.64.0.1:7301", "https://panel.example.com")
	if c.ServerURL != "http://100.64.0.1:7301" {
		t.Fatalf("server %q", c.ServerURL)
	}
	want := []string{"https://panel.example.com", "https://old.example.com"}
	if !reflect.DeepEqual(c.FallbackURLs, want) {
		t.Fatalf("fallbacks %v", c.FallbackURLs)
	}
	if got := c.ControlURLs(); !reflect.DeepEqual(got, append([]string{"http://100.64.0.1:7301"}, want...)) {
		t.Fatalf("urls %v", got)
	}
	// switching back keeps the tailnet URL as a fallback, no duplicates
	c.SwitchServer("https://panel.example.com", "")
	if c.ServerURL != "https://panel.example.com" || !reflect.DeepEqual(c.FallbackURLs, []string{"http://100.64.0.1:7301", "https://old.example.com"}) {
		t.Fatalf("after switch back: %+v", c)
	}
}

func TestFailoverToFallbackURL(t *testing.T) {
	const token = "atl_node_secret"
	fs := newFakeServer(t, token)
	dead := httptest.NewServer(nil)
	deadURL := dead.URL
	dead.Close()
	a, _ := newTestAgent(t, deadURL, token)
	a.Cfg.FallbackURLs = []string{fs.srv.URL}
	a.FailoverAfter = 1
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	hf := fs.wait("hello via fallback", func(f frame) bool { return f.Type == api.MsgHello })
	var hello api.HelloPayload
	json.Unmarshal(hf.Payload, &hello)
	if hello.Network == nil || hello.Network.ControlURL != fs.srv.URL {
		t.Fatalf("hello network %+v (want control url = fallback)", hello.Network)
	}
}

func runAgent(t *testing.T, a *agent.Agent) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestNetworkSwitchVerifiesAndPersists(t *testing.T) {
	const token = "atl_node_secret"
	pub := newFakeServer(t, token)
	tnet := newFakeServer(t, token)
	a, _ := newTestAgent(t, pub.srv.URL, token)
	a.CfgPath = filepath.Join(t.TempDir(), "agent.json")
	if err := agent.SaveConfig(a.CfgPath, a.Cfg); err != nil {
		t.Fatal(err)
	}
	runAgent(t, a)
	pub.wait("hello", func(f frame) bool { return f.Type == api.MsgHello })

	pub.send(api.MsgNetwork, "n1", api.NetworkPayload{Provider: "tailscale", ControlURL: tnet.srv.URL, FallbackURL: pub.srv.URL})
	res := pub.result("n1")
	if !res.OK {
		t.Fatalf("switch failed: %s", res.Error)
	}
	var nr api.NetworkResult
	json.Unmarshal([]byte(res.Output), &nr)
	if !nr.Switched || nr.ControlURL != tnet.srv.URL {
		t.Fatalf("result %+v", nr)
	}
	// the agent reconnects through the new URL and reports it
	hf := tnet.wait("hello on new url", func(f frame) bool { return f.Type == api.MsgHello })
	var hello api.HelloPayload
	json.Unmarshal(hf.Payload, &hello)
	if hello.Network == nil || hello.Network.ControlURL != tnet.srv.URL || len(hello.Network.FallbackURLs) == 0 || hello.Network.FallbackURLs[0] != pub.srv.URL {
		t.Fatalf("hello network %+v", hello.Network)
	}
	saved, err := agent.LoadConfig(a.CfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ServerURL != tnet.srv.URL || len(saved.FallbackURLs) != 1 || saved.FallbackURLs[0] != pub.srv.URL {
		t.Fatalf("saved config %+v", saved)
	}
}

func TestNetworkSwitchKeepsOldURLOnFailure(t *testing.T) {
	const token = "atl_node_secret"
	pub := newFakeServer(t, token)
	other := newFakeServer(t, "someone-elses-token") // answers ping, but not for our token
	a, _ := newTestAgent(t, pub.srv.URL, token)
	a.ProbeTimeout = 300 * time.Millisecond
	a.CfgPath = filepath.Join(t.TempDir(), "agent.json")
	agent.SaveConfig(a.CfgPath, a.Cfg)
	runAgent(t, a)
	pub.wait("hello", func(f frame) bool { return f.Type == api.MsgHello })

	pub.send(api.MsgNetwork, "n1", api.NetworkPayload{ControlURL: other.srv.URL})
	if res := pub.result("n1"); res.OK || !strings.Contains(res.Error, "keeping "+pub.srv.URL) {
		t.Fatalf("want refusal keeping the old URL, got %+v", res)
	}
	// a tailnet URL needs this node on the tailnet first
	old := tailnet.Interfaces
	tailnet.Interfaces = func() []tailnet.IfAddr { return []tailnet.IfAddr{{Name: "eth0", Addr: "203.0.113.5"}} }
	defer func() { tailnet.Interfaces = old }()
	pub.send(api.MsgNetwork, "n2", api.NetworkPayload{ControlURL: "http://100.100.1.1:7301"})
	if res := pub.result("n2"); res.OK || !strings.Contains(res.Error, "not on the tailnet") {
		t.Fatalf("want not-on-tailnet refusal, got %+v", res)
	}
	saved, _ := agent.LoadConfig(a.CfgPath)
	if saved.ServerURL != pub.srv.URL || len(saved.FallbackURLs) != 0 {
		t.Fatalf("config changed: %+v", saved)
	}
}

// A fallback-only push (no control URL) adds the URL and keeps the primary.
func TestNetworkAddsFallbackOnly(t *testing.T) {
	const token = "atl_node_secret"
	pub := newFakeServer(t, token)
	a, _ := newTestAgent(t, pub.srv.URL, token)
	a.CfgPath = filepath.Join(t.TempDir(), "agent.json")
	agent.SaveConfig(a.CfgPath, a.Cfg)
	runAgent(t, a)
	pub.wait("hello", func(f frame) bool { return f.Type == api.MsgHello })
	pub.send(api.MsgNetwork, "f1", api.NetworkPayload{FallbackURL: "https://panel.example.com/"})
	if res := pub.result("f1"); !res.OK || !strings.Contains(res.Output, "added fallback https://panel.example.com") {
		t.Fatalf("result %+v", res)
	}
	pub.send(api.MsgNetwork, "f2", api.NetworkPayload{FallbackURL: "https://panel.example.com"})
	pub.result("f2")
	saved, _ := agent.LoadConfig(a.CfgPath)
	if saved.ServerURL != pub.srv.URL || len(saved.FallbackURLs) != 1 || saved.FallbackURLs[0] != "https://panel.example.com" {
		t.Fatalf("saved config %+v", saved)
	}
}
