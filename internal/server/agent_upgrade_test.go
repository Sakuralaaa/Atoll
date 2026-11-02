package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestAgentUpgradeTooOld(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, id := startFakeAgent(t, e, api.ZeaburInventory{})
	var body api.ErrorBody
	if code := e.do("POST", "/api/v1/servers/"+id+"/agent-upgrade", nil, &body); code != 409 || body.Code != "agent_too_old" {
		t.Fatalf("got %d %+v", code, body)
	}
}

func TestAgentUpgradeFlow(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	hello := api.HelloPayload{AgentVersion: "v0.0.1", Hostname: "w1", OS: "linux", Arch: "amd64", PublicIPv4: "203.0.113.9", SelfUpgrade: true}
	a, id := startFakeAgentHello(t, e, api.ZeaburInventory{}, hello)

	var body api.ErrorBody
	if code := e.do("POST", "/api/v1/servers/"+id+"/agent-upgrade", nil, &body); code != 409 || body.Code != "not_hosted" {
		t.Fatalf("without hosted binary: %d %+v", code, body)
	}
	dir := filepath.Join(e.srv.cfg.DataDir, "downloads")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "atoll-agent-linux-amd64"), []byte("new agent"), 0o755)

	var n api.Server
	e.mustDo("GET", "/api/v1/servers/"+id, nil, &n, 200)
	if n.AgentLatest != Version || !n.AgentSelfUpgrade || n.AgentUpgrade != nil {
		t.Fatalf("before: %+v", n)
	}

	var u api.AgentUpgrade
	e.mustDo("POST", "/api/v1/servers/"+id+"/agent-upgrade", nil, &u, 202)
	if u.State != api.AgentUpgradeRunning || u.From != "v0.0.1" || u.To != Version {
		t.Fatalf("started: %+v", u)
	}
	msgs := a.received(api.MsgUpgrade)
	if len(msgs) != 1 {
		t.Fatalf("upgrade messages: %d", len(msgs))
	}
	var p api.UpgradePayload
	json.Unmarshal(msgs[0], &p)
	if p.Version != Version || p.Path != "/downloads/atoll-agent-linux-amd64" || p.SHA256 != sha256hex([]byte("new agent")) {
		t.Fatalf("payload %+v", p)
	}
	if code := e.do("POST", "/api/v1/servers/"+id+"/agent-upgrade", nil, &body); code != 409 || body.Code != "busy" {
		t.Fatalf("second start: %d %+v", code, body)
	}

	// the helper rolled back; the old agent reports it after reconnecting
	ctx := context.Background()
	a.send(ctx, api.Envelope{Type: api.MsgUpgradeResult, Payload: api.UpdateResult{Mode: "update", FromVersion: "v0.0.1", ToVersion: Version,
		OK: false, RolledBack: true, Error: "did not reconnect", StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		e.mustDo("GET", "/api/v1/servers/"+id, nil, &n, 200)
		if n.AgentUpgrade != nil && n.AgentUpgrade.State == api.AgentUpgradeFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("result not recorded: %+v", n.AgentUpgrade)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !n.AgentUpgrade.RolledBack || n.AgentUpgrade.Error != "did not reconnect" {
		t.Fatalf("failed state %+v", n.AgentUpgrade)
	}

	// batch: every online server not on this version
	var rows []agentUpgradeBatchRow
	e.mustDo("POST", "/api/v1/servers/agent-upgrade", nil, &rows, 200)
	if len(rows) != 1 || rows[0].ID != id || rows[0].Upgrade == nil {
		t.Fatalf("batch %+v", rows)
	}

	// the new agent comes back on the target version
	e.srv.onAgentHello(id, api.HelloPayload{AgentVersion: Version})
	e.mustDo("GET", "/api/v1/servers/"+id, nil, &n, 200)
	if n.AgentUpgrade == nil || n.AgentUpgrade.State != api.AgentUpgradeDone {
		t.Fatalf("after reconnect %+v", n.AgentUpgrade)
	}
}
