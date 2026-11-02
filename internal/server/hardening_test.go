package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestExecNeedsExecPermission(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "shop", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "web", "image": "nginx:1"}, &svc, 201)

	newToken := func(perms ...string) string {
		var tok api.APIToken
		e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": strings.Join(perms, "-"), "permissions": perms}, &tok, 201)
		return tok.Token
	}
	body := map[string]any{"cmd": []string{"true"}}
	var errBody struct {
		Code string `json:"code"`
	}
	if code := e.doWith(http.DefaultClient, newToken("deploy"), "POST", "/api/v1/services/"+svc.ID+"/exec", body, &errBody); code != 403 || errBody.Code != "missing_permission" {
		t.Fatalf("exec with deploy only: %d %+v", code, errBody)
	}
	if code := e.doWith(http.DefaultClient, newToken("exec"), "POST", "/api/v1/services/"+svc.ID+"/exec", body, nil); code == 403 || code == 401 {
		t.Fatalf("exec with exec permission refused: %d", code)
	}
}

func TestReservedPublicPorts(t *testing.T) {
	for _, port := range []int{22, 7301, 41000, 41500, 41999, 41641} {
		v := api.Service{Name: "db", Image: "postgres:16", Ports: []api.Port{{ContainerPort: 5432, Protocol: "tcp", PublicPort: port}}}
		if err := validateServiceSpec(&v); err == nil {
			t.Fatalf("public_port %d accepted", port)
		}
		// an existing service that already publishes it stays editable
		v = api.Service{Name: "db", Image: "postgres:16", Ports: []api.Port{{ContainerPort: 5432, Protocol: "tcp", PublicPort: port}}}
		if err := validateServiceSpecKeeping(&v, []api.Port{{PublicPort: port}}); err != nil {
			t.Fatalf("grandfathered public_port %d: %v", port, err)
		}
	}
	v := api.Service{Name: "db", Image: "postgres:16", Ports: []api.Port{{ContainerPort: 5432, Protocol: "tcp", PublicPort: 5432}}}
	if err := validateServiceSpec(&v); err != nil {
		t.Fatalf("public_port 5432: %v", err)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newTestEnv(t)
	resp, err := http.Get(e.ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("panel CSP: %q", csp)
	}
	if resp.Header.Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS on plain http")
	}
	req, _ := http.NewRequest("GET", e.ts.URL+"/healthz", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Strict-Transport-Security"), "max-age=") {
		t.Fatalf("HSTS behind TLS proxy: %q", resp.Header.Get("Strict-Transport-Security"))
	}
}

func TestAgentConnectRejectsQueryToken(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	_ = nodeID
	// any token in the query string is ignored: without the header the handshake is 401
	resp, err := http.Get(e.ts.URL + "/agent/v1/connect?token=atl_node_whatever")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("query token: %d", resp.StatusCode)
	}
}

func TestWebhookDeliveryReplayIgnored(t *testing.T) {
	e, agent, _, svc := newGitService(t)
	body, _ := json.Marshal(map[string]any{"ref": "refs/heads/main", "after": gitPush, "repository": map[string]string{"full_name": "example/app"}})
	send := func() (int, map[string]any) {
		req, _ := http.NewRequest("POST", e.ts.URL+"/api/v1/hooks/github/"+svc.ID, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Event", "push")
		req.Header.Set("X-GitHub-Delivery", "72d3162e-cc78-11e3-81ab-4c9367dc0958")
		req.Header.Set("X-Hub-Signature-256", signHook(svc.Webhook.Secret, body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := map[string]any{}
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	code, out := send()
	if code != 202 || out["commit"] != gitPush {
		t.Fatalf("first delivery: %d %v", code, out)
	}
	depID, _ := out["deployment_id"].(string)
	waitDepStatus(t, e, depID, "running") // the first delivery's build has been recorded
	before := len(agent.builds())
	if code, out := send(); code != 202 || out["ignored"] != true {
		t.Fatalf("replayed delivery: %d %v", code, out)
	}
	if n := len(agent.builds()); n != before {
		t.Fatalf("replay queued a build (%d -> %d)", before, n)
	}
}

func TestDeliverySetBounded(t *testing.T) {
	var d deliverySet
	now := time.Now()
	if !d.first("a", now) || d.first("a", now.Add(time.Hour)) {
		t.Fatal("duplicate within TTL accepted")
	}
	if !d.first("a", now.Add(deliveryTTL+time.Second)) {
		t.Fatal("expired id rejected")
	}
	for i := 0; i < deliveryMax+50; i++ {
		d.first(fmt.Sprint("id", i), now.Add(time.Duration(i)*time.Millisecond))
	}
	if len(d.seen) > deliveryMax {
		t.Fatalf("set grew to %d", len(d.seen))
	}
}

func TestBlobUploadStopsOnLowDisk(t *testing.T) {
	g := &diskGuard{r: strings.NewReader("data"), dir: t.TempDir(), free: ^uint64(0)}
	if _, err := io.ReadAll(g); !errors.Is(err, errDiskLow) {
		t.Fatalf("guard with impossible free-space floor: %v", err)
	}
	g = &diskGuard{r: strings.NewReader("data"), dir: t.TempDir(), free: 0}
	if b, err := io.ReadAll(g); err != nil || string(b) != "data" {
		t.Fatalf("guard with no floor: %q %v", b, err)
	}
}

func TestIngressHeldIsNotAFailure(t *testing.T) {
	held := errors.New("agent: not starting atoll-caddy: Zeabur/k3s ingress is still running (hostPort 80/443)")
	if !isIngressHeld(held) {
		t.Fatal("guard refusal not recognised")
	}
	if isIngressHeld(errors.New("docker: no such image")) || isIngressHeld(nil) {
		t.Fatal("other errors treated as held")
	}
}
