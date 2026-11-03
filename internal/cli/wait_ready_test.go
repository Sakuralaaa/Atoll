package cli

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// readyPanel scripts a redeploy: the new deployment goes queued → pulling → final.
func readyPanel(t *testing.T, final, message string) (*fakePanel, string) {
	f, srv := newFake(t)
	f.services = []api.Service{{ID: "svc_1", Name: "api", ProjectID: "prj_1", Image: "ghcr.io/me/api:1"}}
	var mu sync.Mutex
	started := false
	polls := 0
	f.extra = func(w http.ResponseWriter, r *http.Request, key string, body []byte) bool {
		mu.Lock()
		defer mu.Unlock()
		switch key {
		case "GET /services/svc_1/deployments":
			deps := []api.Deployment{{ID: "dep_old", ServiceID: "svc_1", Status: "running"}}
			if started {
				deps = append([]api.Deployment{{ID: "dep_new", ServiceID: "svc_1", Status: "queued"}}, deps...)
			}
			writeJSON(w, 200, deps)
		case "POST /services/svc_1/actions", "POST /services/svc_1/rollback":
			started = true
			writeJSON(w, 200, api.Deployment{ID: "dep_new", ServiceID: "svc_1", Status: "queued"})
		case "GET /services/svc_1/deployments/dep_new":
			if r.URL.Query().Get("wait") == "" {
				t.Errorf("deployment polled without ?wait")
			}
			polls++
			d := api.Deployment{ID: "dep_new", ServiceID: "svc_1", Status: "pulling"}
			if polls > 1 {
				d.Status, d.Message = final, message
			}
			writeJSON(w, 200, d)
		case "GET /services/svc_1/deployments/dep_new/logs":
			writeJSON(w, 200, api.DeploymentLogs{DeploymentID: "dep_new", Lines: []api.DeploymentLogLine{{Seq: 1, Line: "step 3/5: npm ci"}, {Seq: 2, Line: "ERR! missing script"}}})
		default:
			return false
		}
		return true
	}
	return f, srv.URL
}

func init() { pollEvery = 5 * time.Millisecond }

func TestRedeployWaitReadySucceeds(t *testing.T) {
	_, url := readyPanel(t, "running", "")
	r := runCLI(t, url, "", "--wait-ready", "redeploy", "api", "-p", "web")
	if r.e != nil {
		t.Fatalf("err %v\n%s", r.e, r.err)
	}
	if !strings.Contains(r.out, "redeploy done, running") || !strings.Contains(r.err, "dep_new pulling") || !strings.Contains(r.err, "dep_new running") {
		t.Fatalf("out %q err %q", r.out, r.err)
	}
}

func TestRedeployWaitReadyFailureIsExit4WithLogTail(t *testing.T) {
	_, url := readyPanel(t, "failed", "build failed")
	r := runCLI(t, url, "", "--wait-ready", "redeploy", "api", "-p", "web")
	var rf *readyFailedError
	if !errors.As(r.e, &rf) {
		t.Fatalf("want readyFailedError, got %v", r.e)
	}
	if !strings.Contains(rf.Error(), "deployment dep_new failed: build failed") || !strings.Contains(rf.Error(), "ERR! missing script") {
		t.Fatalf("message %q", rf.Error())
	}
}

func TestRedeployWithoutWaitReadyDoesNotPoll(t *testing.T) {
	f, url := readyPanel(t, "running", "")
	r := runCLI(t, url, "", "redeploy", "api", "-p", "web")
	if r.e != nil || !strings.Contains(r.out, "redeploy requested") {
		t.Fatalf("%v %q", r.e, r.out)
	}
	if f.called("GET /services/svc_1/deployments") || f.called("GET /services/svc_1/deployments/dep_new") {
		t.Fatal("polled deployments without --wait-ready")
	}
}

func TestRedeployWaitReadyTimeout(t *testing.T) {
	_, url := readyPanel(t, "pulling", "")
	r := runCLI(t, url, "", "--wait-ready", "--ready-timeout", "50ms", "redeploy", "api", "-p", "web")
	if r.e == nil || !strings.Contains(r.e.Error(), "not ready after 50ms") {
		t.Fatalf("err %v", r.e)
	}
}

func TestMCPWaitDeployment(t *testing.T) {
	for _, tc := range []struct{ final, want string }{{"running", ""}, {"failed", "ERR! missing script"}} {
		f, url := readyPanel(t, tc.final, "x")
		f.mu.Lock()
		f.services[0].ID = "svc_1"
		f.mu.Unlock()
		app := &App{Version: "test", URL: url, Token: "atl_pat_test", ConfigPath: filepath.Join(t.TempDir(), "c.json")}
		NewMCPServer(app)
		var h func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
		for _, st := range MCPTools(app) {
			if st.Tool.Name == "wait_deployment" {
				h = st.Handler
			}
		}
		// start the deployment the way redeploy would
		runCLI(t, url, "", "redeploy", "api", "-p", "web")
		var req mcp.CallToolRequest
		req.Params.Name = "wait_deployment"
		req.Params.Arguments = map[string]any{"project": "web", "service": "api", "timeout_sec": 5}
		res, err := h(context.Background(), req)
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", tc.final, err, res)
		}
		txt := res.Content[0].(mcp.TextContent).Text
		if !strings.Contains(txt, `"status": "`+tc.final+`"`) && !strings.Contains(txt, `"status":"`+tc.final+`"`) {
			t.Fatalf("%s: %s", tc.final, txt)
		}
		if tc.want != "" && !strings.Contains(txt, tc.want) {
			t.Fatalf("%s: no log tail in %s", tc.final, txt)
		}
	}
}
