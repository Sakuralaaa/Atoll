package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// `atoll zeabur cutover A B` cuts both imports over with one node request and follows each;
// `atoll zeabur rollback --server` rolls the node back.
func TestZeaburNodeCutoverCLI(t *testing.T) {
	f, srv := newFake(t)
	var mu sync.Mutex
	status := map[string]string{"imp_a": "rehearsed", "imp_b": "rehearsed"}
	f.extra = func(w http.ResponseWriter, r *http.Request, key string, body []byte) bool {
		mu.Lock()
		defer mu.Unlock()
		switch key {
		case "GET /imports/imp_a", "GET /imports/imp_b":
			id := strings.TrimPrefix(key, "GET /imports/")
			writeJSON(w, 200, api.ImportJob{ID: id, Status: status[id], ServerID: "srv_1", Mode: "zeabur-local", Plan: api.ImportPlan{ProjectName: id}})
		case "POST /servers/srv_1/zeabur/cutover":
			var req api.ZeaburNodeCutoverRequest
			json.Unmarshal(body, &req)
			op := api.ZeaburNodeOp{ServerID: "srv_1"}
			for _, id := range req.ImportIDs {
				status[id] = "live"
				op.Jobs = append(op.Jobs, api.ImportJob{ID: id, Status: "cutting_over"})
			}
			writeJSON(w, 200, op)
		case "POST /servers/srv_1/zeabur/rollback":
			op := api.ZeaburNodeOp{ServerID: "srv_1"}
			for _, id := range []string{"imp_a", "imp_b"} {
				status[id] = "rolled_back"
				op.Jobs = append(op.Jobs, api.ImportJob{ID: id, Status: "rolling_back"})
			}
			writeJSON(w, 200, op)
		default:
			return false
		}
		return true
	}
	r := runCLI(t, srv.URL, "", "zeabur", "cutover", "imp_a", "imp_b", "--yes", "--force")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	var req api.ZeaburNodeCutoverRequest
	json.Unmarshal(f.bodies["POST /servers/srv_1/zeabur/cutover"], &req)
	if strings.Join(req.ImportIDs, ",") != "imp_a,imp_b" || !req.Force {
		t.Fatalf("node cutover body %+v", req)
	}
	if !strings.Contains(r.out, "imp_a  imp_a  live") || !strings.Contains(r.out, "imp_b  imp_b  live") || !strings.Contains(r.out, "rollback --server") {
		t.Fatalf("out %q", r.out)
	}
	if f.called("POST /imports/imp_a/cutover") {
		t.Fatal("several imports must go through the node endpoint")
	}
	r = runCLI(t, srv.URL, "", "zeabur", "rollback", "--server", "vps1", "--yes")
	if r.e != nil || !f.called("POST /servers/srv_1/zeabur/rollback") {
		t.Fatal(r.e, r.err)
	}
	if !strings.Contains(r.out, "rolled_back") {
		t.Fatalf("out %q", r.out)
	}
}
