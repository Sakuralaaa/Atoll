package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// twoProjectInventory is zeaburInventory plus a second project ("shop", environment-def456)
// served by the same Zeabur ingress.
func twoProjectInventory() api.ZeaburInventory {
	inv := zeaburInventory()
	raw, _ := json.Marshal(inv.Projects[0])
	r := strings.NewReplacer(strings.Repeat("a", 24), strings.Repeat("c", 24), strings.Repeat("b", 24), strings.Repeat("d", 24),
		"environment-abc123", "environment-def456", "myblog", "myshop", "blog.example.org", "shop.example.org",
		`"project_name":"blog"`, `"project_name":"shop"`, `"project_id":"p1"`, `"project_id":"p2"`)
	var shop api.ZeaburProject
	if err := json.Unmarshal([]byte(r.Replace(string(raw))), &shop); err != nil {
		panic(err)
	}
	inv.Projects = append(inv.Projects, shop)
	return inv
}

func ingressCalls(a *fakeAgent) []api.ZeaburIngressPayload {
	var out []api.ZeaburIngressPayload
	for _, raw := range a.received(api.MsgZeaburIngress) {
		var p api.ZeaburIngressPayload
		json.Unmarshal(raw, &p)
		out = append(out, p)
	}
	return out
}

func importRehearsed(t *testing.T, e *testEnv, nodeID, ns string) api.ImportJob {
	t.Helper()
	var job api.ImportJob
	e.mustDo("POST", "/api/v1/imports/zeabur-local", api.ZeaburLocalImportRequest{ServerID: nodeID, Namespace: ns}, &job, 201)
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rehearse", nil, nil, 200)
	if j := waitJob(t, e, job.ID, "rehearsed", "failed"); j.Status != "rehearsed" {
		t.Fatalf("rehearse %s: %+v", ns, j.Steps)
	}
	return job
}

// Cutting over one project on a node that hosts another serving project is refused; both cut
// over together park the ingress once; one of them cannot be rolled back alone; the node
// rollback restores the ingress once.
func TestZeaburNodeCutover(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, twoProjectInventory())
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "cf-token", CloudflareZoneID: "zone1"}, nil, 200)

	blog := importRehearsed(t, e, nodeID, "environment-abc123")

	// single cutover: shop still serves through the shared ingress
	var eb api.ErrorBody
	if code := e.do("POST", "/api/v1/imports/"+blog.ID+"/cutover", nil, &eb); code != 409 || eb.Code != "other_zeabur_projects" ||
		!strings.Contains(eb.Error, "environment-def456") || !strings.Contains(eb.Error, "myshop.zeabur.app") || strings.Contains(eb.Error, "environment-abc123") {
		t.Fatalf("partial cutover: %d %+v", code, eb)
	}
	if j := waitJob(t, e, blog.ID, "rehearsed"); j.Status != "rehearsed" || len(agent.received(api.MsgZeaburScale)) != 0 {
		t.Fatalf("refused cutover must change nothing: %s", j.Status)
	}

	shop := importRehearsed(t, e, nodeID, "environment-def456")
	// a node cutover naming only one of them is refused the same way
	if code := e.do("POST", "/api/v1/servers/"+nodeID+"/zeabur/cutover", api.ZeaburNodeCutoverRequest{ImportIDs: []string{shop.ID}}, &eb); code != 409 || eb.Code != "other_zeabur_projects" {
		t.Fatalf("node cutover of one: %d %+v", code, eb)
	}
	var op api.ZeaburNodeOp
	e.mustDo("POST", "/api/v1/servers/"+nodeID+"/zeabur/cutover", api.ZeaburNodeCutoverRequest{ImportIDs: []string{blog.ID, shop.ID}}, &op, 200)
	if op.ServerID != nodeID || len(op.Jobs) != 2 || op.Jobs[0].Status != zsCuttingOver {
		t.Fatalf("node op %+v", op)
	}
	for _, id := range []string{blog.ID, shop.ID} {
		if j := waitJob(t, e, id, "live", "rolled_back", "failed"); j.Status != "live" {
			t.Fatalf("job %s: %s %+v", id, j.Status, j.Steps)
		}
	}
	calls := ingressCalls(agent)
	if len(calls) != 1 || !calls[0].Park || calls[0].Force || strings.Join(calls[0].Namespaces, ",") != "environment-abc123,environment-def456" {
		t.Fatalf("ingress must be parked once for both namespaces: %+v", calls)
	}
	if n := len(agent.received(api.MsgZeaburScale)); n != 4 {
		t.Fatalf("scale calls %d", n)
	}

	// one job alone cannot be rolled back: the ingress is shared
	if code := e.do("POST", "/api/v1/imports/"+blog.ID+"/rollback", nil, &eb); code != 409 || eb.Code != "node_rollback_required" || !strings.Contains(eb.Error, shop.ID) {
		t.Fatalf("single rollback: %d %+v", code, eb)
	}
	if j := waitJob(t, e, blog.ID, "live"); j.Status != "live" {
		t.Fatal("refused rollback must change nothing")
	}

	e.mustDo("POST", "/api/v1/servers/"+nodeID+"/zeabur/rollback", nil, &op, 200)
	if len(op.Jobs) != 2 {
		t.Fatalf("node rollback jobs %+v", op.Jobs)
	}
	for _, id := range []string{blog.ID, shop.ID} {
		if j := waitJob(t, e, id, "rolled_back", "failed"); j.Status != "rolled_back" {
			t.Fatalf("rollback %s: %s", id, j.Status)
		}
	}
	calls = ingressCalls(agent)
	if len(calls) != 2 || calls[1].Park {
		t.Fatalf("ingress must be restored once: %+v", calls)
	}
	restored := 0
	for _, raw := range agent.received(api.MsgZeaburScale) {
		var p api.ZeaburScalePayload
		json.Unmarshal(raw, &p)
		if p.Replicas == -1 {
			restored++
		}
	}
	if restored != 4 {
		t.Fatalf("scaled back %d workloads", restored)
	}
	if code := e.do("POST", "/api/v1/servers/"+nodeID+"/zeabur/rollback", nil, &eb); code != 409 {
		t.Fatalf("nothing left to roll back: %d", code)
	}

	// a single job that is the only one cut over rolls back alone (and unparks)
	e.mustDo("POST", "/api/v1/imports/"+blog.ID+"/cutover", api.ZeaburCutoverRequest{Force: true}, nil, 200)
	if j := waitJob(t, e, blog.ID, "live", "rolled_back", "failed"); j.Status != "live" {
		t.Fatalf("forced cutover: %s %+v", j.Status, j.Steps)
	}
	if calls = ingressCalls(agent); len(calls) != 3 || !calls[2].Force {
		t.Fatalf("forced park: %+v", calls)
	}
	e.mustDo("POST", "/api/v1/imports/"+blog.ID+"/rollback", nil, nil, 200)
	if j := waitJob(t, e, blog.ID, "rolled_back", "failed"); j.Status != "rolled_back" {
		t.Fatalf("single rollback: %s", j.Status)
	}
	if calls = ingressCalls(agent); len(calls) != 4 || calls[3].Park {
		t.Fatalf("single rollback must unpark: %+v", calls)
	}
}

// A failure in one job of a node cutover rolls back every job of it; the ingress is never parked.
func TestZeaburNodeCutoverFailureRollsBackAll(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	agent, nodeID := startFakeAgent(t, e, twoProjectInventory())
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "cf-token", CloudflareZoneID: "zone1"}, nil, 200)
	blog := importRehearsed(t, e, nodeID, "environment-abc123")
	shop := importRehearsed(t, e, nodeID, "environment-def456")
	agent.mu.Lock()
	agent.hook = func(env inEnvelope) (api.ResultPayload, bool) {
		if env.Type == api.MsgZeaburScale {
			var p api.ZeaburScalePayload
			json.Unmarshal(env.Payload, &p)
			if p.Namespace == "environment-def456" && p.Replicas == 0 {
				return api.ResultPayload{OK: false, Error: "kubectl scale: boom"}, true
			}
		}
		return api.ResultPayload{}, false
	}
	agent.mu.Unlock()
	e.mustDo("POST", "/api/v1/servers/"+nodeID+"/zeabur/cutover", api.ZeaburNodeCutoverRequest{ImportIDs: []string{blog.ID, shop.ID}}, nil, 200)
	for _, id := range []string{blog.ID, shop.ID} {
		if j := waitJob(t, e, id, "rolled_back", "failed", "live"); j.Status != "rolled_back" {
			t.Fatalf("job %s: %s %+v", id, j.Status, j.Steps)
		}
	}
	if n := len(agent.received(api.MsgZeaburIngress)); n != 0 {
		t.Fatalf("ingress touched %d times", n)
	}
	restored := 0
	for _, raw := range agent.received(api.MsgZeaburScale) {
		var p api.ZeaburScalePayload
		json.Unmarshal(raw, &p)
		if p.Replicas == -1 {
			restored++
		}
	}
	if restored != 2 { // blog (app + database) was fully prepared before shop failed: both back
		t.Fatalf("restored %d", restored)
	}
}

// The rehearsal refuses when the server lacks the memory for both copies, unless overridden.
func TestZeaburRehearseMemoryCheck(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	inv := zeaburInventory()
	inv.MemFreeMB = 300
	inv.Projects[0].Services[1].MemUsageMB = 200 // web: measured; postgres: default 256
	agent, nodeID := startFakeAgent(t, e, inv)
	_ = agent
	e.mustDo("PUT", "/api/v1/settings", api.Settings{BaseDomain: "example.com", CloudflareToken: "cf-token", CloudflareZoneID: "zone1"}, nil, 200)
	var job api.ImportJob
	e.mustDo("POST", "/api/v1/imports/zeabur-local", api.ZeaburLocalImportRequest{ServerID: nodeID, Namespace: "environment-abc123"}, &job, 201)
	var eb api.ErrorBody
	if code := e.do("POST", "/api/v1/imports/"+job.ID+"/rehearse", nil, &eb); code != 409 || eb.Code != "insufficient_memory" ||
		!strings.Contains(eb.Error, "456 MB") || !strings.Contains(eb.Error, "web 200 MB (pod usage)") || !strings.Contains(eb.Error, "postgresql 256 MB (default)") {
		t.Fatalf("memory check: %d %+v", code, eb)
	}
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rehearse", api.ZeaburRehearseRequest{IgnoreMemory: true}, nil, 200)
	if j := waitJob(t, e, job.ID, "rehearsed", "failed"); j.Status != "rehearsed" {
		t.Fatalf("override: %s", j.Status)
	}
}

func TestOtherServing(t *testing.T) {
	inv := twoProjectInventory()
	inv.Projects[1].Services[1].Replicas = 0
	inv.Projects[1].Services[0].Replicas = 0
	if got := otherServing(&inv, []string{"environment-abc123"}); len(got) != 0 {
		t.Fatalf("scaled-down project counted: %v", got)
	}
	inv.Projects[1].Services[0].Replicas = 1 // the database runs; the project still has hosts
	if got := otherServing(&inv, nil); len(got) != 2 || !strings.HasPrefix(got[1], "shop [environment-def456: myshop.zeabur.app, shop.example.org]") {
		t.Fatalf("got %v", got)
	}
}
