package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// Without a DNS provider a domain whose name already resolves to the service's server
// (wildcard DNS like sslip.io, or a record the user made) is active, not pending forever.
func TestAddDomainWithoutDNSProviderResolvesHere(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	e.srv.cfg.LookupHost = func(ctx context.Context, host string) ([]string, error) {
		switch host {
		case "app.203-0-113-7.sslip.io":
			return []string{"203.0.113.7"}, nil
		case "elsewhere.example.org":
			return []string{"198.51.100.9"}, nil
		}
		return nil, fmt.Errorf("no such host %s", host)
	}
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "web", "server_id": nodeID}, &p, 201)
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "app", "kind": "image", "image": "traefik/whoami",
		"ports": []api.Port{{Name: "web", ContainerPort: 80, Protocol: "http"}},
	}, &svc, 201)

	add := func(host string) api.Domain {
		var d api.Domain
		e.mustDo("POST", "/api/v1/services/"+svc.ID+"/domains", map[string]any{"hostname": host, "proxied": false}, &d, 201)
		return d
	}
	if d := add("app.203-0-113-7.sslip.io"); d.Status != "active" || d.Error != "" {
		t.Fatalf("resolving domain: %+v", d)
	}
	for _, host := range []string{"elsewhere.example.org", "nowhere.example.org"} {
		if d := add(host); d.Status != "pending" || !strings.Contains(d.Error, "Create an A record "+host+" -> 203.0.113.7") {
			t.Fatalf("%s: %+v", host, d)
		}
	}
}

// Token requests to destructive routes are audited as api.<perm>, including denied ones,
// so they cannot be mistaken for token.delete (a revoked token).
func TestTokenDestructiveRequestsAudited(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "one", "server_id": nodeID}, &p, 201)
	var deploy, del api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "ci", "permissions": []string{"deploy"}}, &deploy, 201)
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "cleaner", "permissions": []string{"delete"}}, &del, 201)

	if code := e.doWith(http.DefaultClient, deploy.Token, "DELETE", "/api/v1/projects/"+p.ID, nil, nil); code != 403 {
		t.Fatalf("denied delete: %d", code)
	}
	if code := e.doWith(http.DefaultClient, del.Token, "DELETE", "/api/v1/projects/"+p.ID, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	rows, err := e.srv.st.DB.Query(`SELECT actor, action, target, detail FROM audit_log WHERE action LIKE 'api.%' OR action = 'token.delete' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var actor, action, target, detail string
		rows.Scan(&actor, &action, &target, &detail)
		got = append(got, actor+" "+action+" "+target+" "+detail)
	}
	if len(got) != 2 {
		t.Fatalf("audit rows %q", got)
	}
	if !strings.HasPrefix(got[0], "token:"+deploy.ID+" api.delete DELETE /api/v1/projects/"+p.ID) || !strings.Contains(got[0], `"result":"denied: `) || !strings.Contains(got[0], deploy.ID) {
		t.Fatalf("denied row %q", got[0])
	}
	if !strings.HasPrefix(got[1], "token:"+del.ID+" api.delete DELETE /api/v1/projects/"+p.ID) || !strings.Contains(got[1], `"result":"ok"`) {
		t.Fatalf("ok row %q", got[1])
	}
}
