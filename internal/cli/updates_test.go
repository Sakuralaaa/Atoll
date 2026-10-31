package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func newUpdatesPanel(t *testing.T) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	calls := []string{}
	upd := []api.ImageUpdate{
		{ServiceID: "svc_api", ProjectID: "prj_1", Service: "acme-api", Image: "ghcr.io/acme/acme-api:1.2", Status: "update_available",
			UpdateAvailable: true, DigestChanged: true, CurrentDigest: "sha256:aaaaaaaaaaaaaaaa", LatestDigest: "sha256:bbbbbbbbbbbbbbbb", NewerTag: "1.3", MajorTag: "2.0", Policy: "notify"},
		{ServiceID: "svc_mail", ProjectID: "prj_1", Service: "mail-relay", Image: "mail-relay:3", Status: "up_to_date", Policy: "auto"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + strings.TrimPrefix(r.URL.RequestURI(), "/api/v1")
		mu.Lock()
		calls = append(calls, key+" "+string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case key == "GET /image-updates":
			json.NewEncoder(w).Encode(upd)
		case key == "GET /image-updates?available=1":
			json.NewEncoder(w).Encode(upd[:1])
		case key == "GET /projects":
			json.NewEncoder(w).Encode([]api.Project{{ID: "prj_1", Name: "acme"}})
		case key == "GET /projects/prj_1/services":
			json.NewEncoder(w).Encode([]api.Service{{ID: "svc_api", Name: "acme-api"}, {ID: "svc_mail", Name: "mail-relay"}})
		case key == "POST /services/svc_api/image-update/apply":
			json.NewEncoder(w).Encode(api.Deployment{ID: "dep_new", ServiceID: "svc_api", Trigger: "image-update", Status: "queued"})
		case key == "PUT /services/svc_mail/image-update/policy":
			var p api.ImageUpdatePolicy
			json.Unmarshal(body, &p)
			json.NewEncoder(w).Encode(api.ImageUpdate{ServiceID: "svc_mail", Policy: p.Policy, Window: p.Window})
		default:
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(api.ErrorBody{Error: "no route " + key})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestUpdatesCommand(t *testing.T) {
	srv, calls := newUpdatesPanel(t)
	r := runCLI(t, srv.URL, "", "updates")
	if r.e != nil || !strings.Contains(r.out, "acme-api") || !strings.Contains(r.out, "mail-relay") || !strings.Contains(r.out, "2.0") || !strings.Contains(r.out, "aaaaaaaaaaaa") {
		t.Fatalf("list: %v\n%s", r.e, r.out)
	}
	r = runCLI(t, srv.URL, "", "updates", "--available")
	if r.e != nil || strings.Contains(r.out, "mail-relay") {
		t.Fatalf("available: %v\n%s", r.e, r.out)
	}
	r = runCLI(t, srv.URL, "", "updates", "--apply", "acme-api", "--tag", "1.3")
	if r.e != nil || !strings.Contains(r.out, "dep_new") {
		t.Fatalf("apply: %v\n%s", r.e, r.out)
	}
	found := false
	for _, c := range *calls {
		if strings.HasPrefix(c, "POST /services/svc_api/image-update/apply") && strings.Contains(c, `"tag":"1.3"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("apply call missing: %v", *calls)
	}
	r = runCLI(t, srv.URL, "", "updates", "policy", "mail-relay", "auto", "--window", "02:00-05:00")
	if r.e != nil || !strings.Contains(r.out, "auto (window 02:00-05:00 UTC)") {
		t.Fatalf("policy: %v\n%s", r.e, r.out)
	}
	if r := runCLI(t, srv.URL, "", "updates", "--tag", "1.3"); r.e == nil {
		t.Fatal("--tag without --apply must fail")
	}
}

func TestMCPListUpdates(t *testing.T) {
	srv, _ := newUpdatesPanel(t)
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", Out: io.Discard, Err: io.Discard}
	NewMCPServer(app)
	for _, st := range MCPTools(app) {
		if st.Tool.Name != "list_updates" {
			continue
		}
		var req mcp.CallToolRequest
		req.Params.Arguments = map[string]any{}
		res, err := st.Handler(context.Background(), req)
		if err != nil || res.IsError {
			t.Fatalf("list_updates: %v %+v", err, res)
		}
		txt := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(txt, "acme-api") || strings.Contains(txt, "mail-relay") {
			t.Fatalf("list_updates output: %s", txt)
		}
		return
	}
	t.Fatal("list_updates tool not registered")
}
