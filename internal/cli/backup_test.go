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
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// newBackupPanel fakes the backup API. Restore answers 202 + Confirmation like the server
// does for an API token.
func newBackupPanel(t *testing.T) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	calls := []string{}
	policy := api.BackupPolicy{ServiceID: "svc_db", Schedule: "0 3 * * *", Timezone: "UTC", Keep: api.BackupKeep{Daily: 7, Weekly: 4, Monthly: 6},
		RepoConfigured: true, Parts: []string{"dump"}}
	started := time.Now().Add(-2 * time.Hour).UTC()
	bak := api.Backup{ID: "bak_one", ServiceID: "svc_db", ServerID: "srv_1", Trigger: "schedule", Status: "done",
		Snapshots: []api.BackupSnapshot{{ID: "abcdef0123456789", Part: "dump", Bytes: 5 << 20}}, Bytes: 5 << 20, Added: 1 << 20, DurationMs: 4200, StartedAt: started}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + strings.TrimPrefix(r.URL.RequestURI(), "/api/v1")
		mu.Lock()
		calls = append(calls, key+" "+string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch key {
		case "GET /projects":
			json.NewEncoder(w).Encode([]api.Project{{ID: "prj_1", Name: "acme"}})
		case "GET /projects/prj_1":
			json.NewEncoder(w).Encode(api.Project{ID: "prj_1", Name: "acme", Services: []api.Service{{ID: "svc_db", Name: "acme-db", ProjectID: "prj_1"}}})
		case "GET /projects/prj_1/services":
			json.NewEncoder(w).Encode([]api.Service{{ID: "svc_db", Name: "acme-db", ProjectID: "prj_1"}})
		case "GET /servers":
			json.NewEncoder(w).Encode([]api.Server{{ID: "srv_1", Name: "box-a"}, {ID: "srv_2", Name: "box-b"}})
		case "GET /services/svc_db/backups":
			json.NewEncoder(w).Encode([]api.Backup{bak})
		case "POST /services/svc_db/backups":
			w.WriteHeader(202) // a queued Backup, not a confirmation
			json.NewEncoder(w).Encode(api.Backup{ID: "bak_new", ServiceID: "svc_db", Trigger: "manual", Status: "running", StartedAt: time.Now().UTC()})
		case "GET /backups/bak_one", "GET /backups/abcdef01":
			json.NewEncoder(w).Encode(bak)
		case "POST /backups/bak_one/restore":
			w.WriteHeader(202)
			json.NewEncoder(w).Encode(api.Confirmation{ID: "cfm_rst", Action: "backup.restore", Target: "bak_one", Summary: "Restore acme-db from backup bak_one", Status: "pending"})
		case "GET /services/svc_db/backup-policy":
			json.NewEncoder(w).Encode(policy)
		case "PUT /services/svc_db/backup-policy":
			var p api.BackupPolicy
			json.Unmarshal(body, &p)
			p.ServiceID, p.RepoConfigured, p.Parts = "svc_db", true, []string{"dump"}
			json.NewEncoder(w).Encode(p)
		default:
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(api.ErrorBody{Error: "no route " + key})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), calls...) }
}

func findCall(calls []string, prefix string) string {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return c
		}
	}
	return ""
}

func TestBackupCommands(t *testing.T) {
	srv, calls := newBackupPanel(t)

	r := runCLI(t, srv.URL, "", "backup", "list", "acme/acme-db")
	if r.e != nil || !strings.Contains(r.out, "bak_one") || !strings.Contains(r.out, "abcdef01") || !strings.Contains(r.out, "5.0M") {
		t.Fatalf("list: %v\n%s", r.e, r.out)
	}

	r = runCLI(t, srv.URL, "", "backup", "run", "acme-db", "-p", "acme")
	if r.e != nil || !strings.Contains(r.out, "bak_new") {
		t.Fatalf("run must treat a 202 Backup as success: %v\n%s%s", r.e, r.out, r.err)
	}

	r = runCLI(t, srv.URL, "", "backup", "policy", "svc_db", "--enable", "--schedule", "30 2 * * *", "--keep-daily", "14")
	if r.e != nil || !strings.Contains(r.out, "enabled") || !strings.Contains(r.out, `"30 2 * * *"`) || !strings.Contains(r.out, "daily 14 / weekly 4 / monthly 6") {
		t.Fatalf("policy: %v\n%s", r.e, r.out)
	}
	put := findCall(calls(), "PUT /services/svc_db/backup-policy")
	if !strings.Contains(put, `"enabled":true`) || !strings.Contains(put, `"weekly":4`) || !strings.Contains(put, `"daily":14`) {
		t.Fatalf("policy body: %q", put)
	}

	before := len(calls())
	r = runCLI(t, srv.URL, "", "backup", "policy", "acme-db")
	if r.e != nil || !strings.Contains(r.out, "disabled") {
		t.Fatalf("policy show: %v\n%s", r.e, r.out)
	}
	for _, c := range calls()[before:] {
		if strings.HasPrefix(c, "PUT ") {
			t.Fatalf("policy without flags must not write: %s", c)
		}
	}
	if r := runCLI(t, srv.URL, "", "backup", "policy", "acme-db", "--enable", "--disable"); r.e == nil {
		t.Fatal("--enable with --disable must fail")
	}

	// restore with a PAT: pending confirmation, exit as pending, nothing forced
	r = runCLI(t, srv.URL, "", "backup", "restore", "abcdef01", "--to", "box-b")
	var pe *pendingError
	if r.e == nil || !asPending(r.e, &pe) || !strings.Contains(r.e.Error(), "cfm_rst") {
		t.Fatalf("restore must stop at the confirmation: %v\n%s", r.e, r.out)
	}
	rc := findCall(calls(), "POST /backups/bak_one/restore")
	if !strings.Contains(rc, `"target_server_id":"srv_2"`) {
		t.Fatalf("restore body: %q", rc)
	}
}

func asPending(err error, pe **pendingError) bool {
	p, ok := err.(*pendingError)
	*pe = p
	return ok
}

func TestMCPBackupTools(t *testing.T) {
	srv, calls := newBackupPanel(t)
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", Out: io.Discard, Err: io.Discard}
	NewMCPServer(app)
	tools := map[string]func(map[string]any) *mcp.CallToolResult{}
	for _, st := range MCPTools(app) {
		st := st
		tools[st.Tool.Name] = func(args map[string]any) *mcp.CallToolResult {
			var req mcp.CallToolRequest
			req.Params.Arguments = args
			res, err := st.Handler(context.Background(), req)
			if err != nil {
				t.Fatalf("%s: %v", st.Tool.Name, err)
			}
			return res
		}
	}
	for _, n := range []string{"backup_list", "backup_run", "backup_restore"} {
		if tools[n] == nil {
			t.Fatalf("%s not registered", n)
		}
	}
	text := func(res *mcp.CallToolResult) string { return res.Content[0].(mcp.TextContent).Text }

	if res := tools["backup_list"](map[string]any{"service": "acme-db"}); res.IsError || !strings.Contains(text(res), "bak_one") {
		t.Fatalf("backup_list: %s", text(res))
	}
	if res := tools["backup_run"](map[string]any{"service": "acme-db", "project": "acme"}); res.IsError || !strings.Contains(text(res), "bak_new") {
		t.Fatalf("backup_run: %s", text(res))
	}
	res := tools["backup_restore"](map[string]any{"backup_id": "bak_one"})
	if res.IsError || !strings.HasPrefix(text(res), "PENDING CONFIRMATION cfm_rst") {
		t.Fatalf("backup_restore must only create a pending confirmation: %s", text(res))
	}
	n := 0
	for _, c := range calls() {
		if strings.HasPrefix(c, "POST /backups/bak_one/restore") {
			n++
			if strings.Contains(c, "target_server_id") {
				t.Fatalf("in-place restore must not send a target: %s", c)
			}
		}
	}
	if n != 1 {
		t.Fatalf("restore must be requested exactly once (no retry), got %d", n)
	}
	for _, c := range calls() {
		if strings.Contains(c, "/confirmations") {
			t.Fatalf("MCP must never touch confirmations: %s", c)
		}
	}
}
