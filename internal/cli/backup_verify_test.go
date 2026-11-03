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

func newVerifyPanel(t *testing.T) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	calls := []string{}
	started := time.Now().Add(-time.Hour).UTC()
	fin := started.Add(90 * time.Second)
	next := time.Now().Add(48 * time.Hour).UTC()
	check := api.BackupVerification{ID: "bvf_chk", Kind: "check", ServerID: "srv_1", Trigger: "schedule", Status: "done", Subset: "5%",
		Steps: []api.BackupVerifyStep{{Part: "check", Status: "done", Detail: "no errors were found"}}, DurationMs: 90000, StartedAt: started, FinishedAt: &fin}
	drill := api.BackupVerification{ID: "bvf_drl", Kind: "drill", ServiceID: "svc_db", ServerID: "srv_1", Trigger: "manual", Status: "failed",
		Error: "dump: query exited 1", Steps: []api.BackupVerifyStep{{Part: "dump", Status: "failed", Error: "query exited 1"}}, StartedAt: started, FinishedAt: &fin}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + strings.TrimPrefix(r.URL.RequestURI(), "/api/v1")
		mu.Lock()
		calls = append(calls, key+" "+string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		switch {
		case key == "GET /projects":
			enc.Encode([]api.Project{{ID: "prj_1", Name: "acme"}})
		case key == "GET /projects/prj_1":
			enc.Encode(api.Project{ID: "prj_1", Name: "acme", Services: []api.Service{{ID: "svc_db", Name: "acme-db", ProjectID: "prj_1"}}})
		case key == "GET /projects/prj_1/services":
			enc.Encode([]api.Service{{ID: "svc_db", Name: "acme-db", ProjectID: "prj_1"}})
		case key == "GET /servers":
			enc.Encode([]api.Server{{ID: "srv_1", Name: "box-a"}})
		case key == "GET /backup-verify":
			enc.Encode(api.BackupVerifyStatus{Check: &check, CheckSchedule: "30 1 * * 0", CheckSubset: "5%", DrillSchedule: "30 1 1 * *", Timezone: "UTC",
				CheckServerID: "srv_1", NextCheckAt: &next, RepoConfigured: true})
		case key == "GET /services/svc_db/backup-verify":
			enc.Encode(api.BackupVerifyStatus{Check: &check, Drill: &drill, CheckSchedule: "30 1 * * 0", CheckSubset: "5%", DrillSchedule: "30 1 1 * *",
				Timezone: "UTC", NextDrillAt: &next, RepoConfigured: true})
		case key == "POST /backup-verify/check":
			w.WriteHeader(202)
			enc.Encode(api.BackupVerification{ID: "bvf_new", Kind: "check", ServerID: "srv_1", Trigger: "manual", Status: "running", Subset: "1/20", StartedAt: time.Now().UTC()})
		case key == "POST /services/svc_db/backup-drill":
			w.WriteHeader(202)
			enc.Encode(api.BackupVerification{ID: "bvf_drl", Kind: "drill", ServiceID: "svc_db", ServerID: "srv_1", Trigger: "manual", Status: "running", StartedAt: started})
		case strings.HasPrefix(key, "GET /backup-verifications"):
			enc.Encode([]api.BackupVerification{drill, check})
		default:
			w.WriteHeader(404)
			enc.Encode(api.ErrorBody{Error: "no route " + key})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), calls...) }
}

func TestBackupVerifyCommands(t *testing.T) {
	srv, calls := newVerifyPanel(t)

	r := runCLI(t, srv.URL, "", "backup", "verify")
	if r.e != nil || !strings.Contains(r.out, "Repository check: \"30 1 * * 0\" (UTC), next") || !strings.Contains(r.out, "reads 5% of the data on srv_1") ||
		!strings.Contains(r.out, "last: done") || !strings.Contains(r.out, "Restore drills:") {
		t.Fatalf("verify: %v\n%s", r.e, r.out)
	}
	r = runCLI(t, srv.URL, "", "backup", "verify", "acme/acme-db", "--history", "5")
	if r.e != nil || !strings.Contains(r.out, "Restore drill of acme-db") || !strings.Contains(r.out, "failed") || !strings.Contains(r.out, "dump failed: query exited 1") ||
		!strings.Contains(r.out, "bvf_chk") {
		t.Fatalf("verify service: %v\n%s", r.e, r.out)
	}
	if c := findCall(calls(), "GET /backup-verifications"); !strings.Contains(c, "service_id=svc_db") || !strings.Contains(c, "limit=5") {
		t.Fatalf("history call %q", c)
	}

	r = runCLI(t, srv.URL, "", "backup", "check", "--subset", "1/20", "--server", "box-a")
	if r.e != nil || !strings.Contains(r.out, "bvf_new started on srv_1") {
		t.Fatalf("check: %v\n%s%s", r.e, r.out, r.err)
	}
	if c := findCall(calls(), "POST /backup-verify/check"); !strings.Contains(c, `"subset":"1/20"`) || !strings.Contains(c, `"server_id":"srv_1"`) {
		t.Fatalf("check body %q", c)
	}
	// --wait returns the outcome and fails when the drill failed
	r = runCLI(t, srv.URL, "", "backup", "drill", "acme-db", "-p", "acme", "--wait")
	if r.e == nil || !strings.Contains(r.out, "dump failed: query exited 1") {
		t.Fatalf("drill --wait: %v\n%s", r.e, r.out)
	}
}

func TestMCPBackupVerifyTools(t *testing.T) {
	srv, calls := newVerifyPanel(t)
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", Out: io.Discard, Err: io.Discard}
	NewMCPServer(app)
	tools := map[string]func(map[string]any) string{}
	for _, st := range MCPTools(app) {
		st := st
		tools[st.Tool.Name] = func(args map[string]any) string {
			var req mcp.CallToolRequest
			req.Params.Arguments = args
			res, err := st.Handler(context.Background(), req)
			if err != nil || res.IsError {
				t.Fatalf("%s: %v %+v", st.Tool.Name, err, res)
			}
			return res.Content[0].(mcp.TextContent).Text
		}
	}
	if out := tools["backup_verify"](map[string]any{}); !strings.Contains(out, "bvf_chk") || strings.Contains(out, "bvf_drl") {
		t.Fatalf("backup_verify: %s", out)
	}
	if out := tools["backup_verify"](map[string]any{"service": "acme-db"}); !strings.Contains(out, "bvf_drl") {
		t.Fatalf("backup_verify service: %s", out)
	}
	if out := tools["backup_check"](map[string]any{"subset": "2%"}); !strings.Contains(out, "bvf_new") {
		t.Fatalf("backup_check: %s", out)
	}
	if out := tools["backup_drill"](map[string]any{"service": "acme-db"}); !strings.Contains(out, "running") {
		t.Fatalf("backup_drill: %s", out)
	}
	if c := findCall(calls(), "POST /backup-verify/check"); !strings.Contains(c, `"subset":"2%"`) {
		t.Fatalf("check body %q", c)
	}
	if MCPToolPermission("backup_verify") != api.PermRead || MCPToolPermission("backup_drill") != api.PermDeploy || MCPToolPermission("backup_check") != api.PermDeploy {
		t.Fatal("permissions")
	}
}
