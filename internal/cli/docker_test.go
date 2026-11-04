package cli

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// `atoll docker scan|import|rehearse|cutover|rollback` and the docker_* MCP tools.
func TestDockerCLI(t *testing.T) {
	f, srv := newFake(t)
	var mu sync.Mutex
	status := "planned"
	inv := api.DockerInventory{DockerVersion: "28.0.0", MemFreeMB: 2048, Groups: []api.DockerGroup{
		{Key: "compose:blog", Kind: "compose", Name: "blog", WorkingDir: "/srv/blog", Adoptable: true, Containers: []api.DockerContainer{
			{Name: "blog-web-1", Service: "web", Image: "nginx:1.27", State: "running", Risk: api.DockerRiskAuto,
				Ports: []api.DockerPort{{ContainerPort: 80, HostPort: 8080, Protocol: "tcp"}}, MemUsageMB: 12},
			{Name: "blog-watchtower-1", Service: "watchtower", Image: "containrrr/watchtower", State: "running", Risk: api.DockerRiskManual,
				RiskReasons: []string{"mounts the container runtime socket /var/run/docker.sock"}},
		}},
		{Key: "container:cache", Kind: "standalone", Name: "cache", Containers: []api.DockerContainer{{Name: "cache", Image: "redis:7", State: "running", Risk: api.DockerRiskAuto}}},
	}}
	f.extra = func(w http.ResponseWriter, r *http.Request, key string, body []byte) bool {
		mu.Lock()
		defer mu.Unlock()
		job := api.ImportJob{ID: "imp_d", Status: status, Mode: "docker", ServerID: "srv_1", Group: "compose:blog",
			Plan: api.ImportPlan{ProjectName: "blog", Services: []api.ImportService{{Name: "web", Image: "nginx@sha256:feed",
				Ports: []api.Port{{Name: "p8080", ContainerPort: 80, Protocol: "tcp", PublicPort: 8080}}, Volumes: []api.Volume{{Name: "html", MountPath: "/usr/share/nginx/html", HostPath: "/srv/blog/html"}}}}}}
		switch key {
		case "GET /servers/srv_1/docker":
			writeJSON(w, 200, inv)
		case "POST /imports/docker":
			writeJSON(w, 201, job)
		case "GET /imports/imp_d":
			writeJSON(w, 200, job)
		case "POST /imports/imp_d/rehearse":
			status = "rehearsed"
			job.Status = "rehearsing"
			writeJSON(w, 200, job)
		case "POST /imports/imp_d/cutover":
			status = "live"
			job.Status = "cutting_over"
			writeJSON(w, 200, job)
		case "POST /imports/imp_d/rollback":
			status = "rolled_back"
			job.Status = "rolling_back"
			writeJSON(w, 200, job)
		default:
			return false
		}
		return true
	}
	r := runCLI(t, srv.URL, "", "docker", "scan", "vps1")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	for _, want := range []string{"compose:blog", "8080→80/tcp", "✗ blog-watchtower-1: mounts the container runtime socket", "container:cache", "Next: atoll docker import vps1"} {
		if !strings.Contains(r.out, want) {
			t.Fatalf("scan output lacks %q:\n%s", want, r.out)
		}
	}
	r = runCLI(t, srv.URL, "", "docker", "import", "vps1", "blog", "--name", "blog")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	var req api.DockerAdoptRequest
	json.Unmarshal(f.bodies["POST /imports/docker"], &req)
	if req.ServerID != "srv_1" || req.Group != "compose:blog" || req.ProjectName != "blog" {
		t.Fatalf("adopt body %+v", req)
	}
	if !strings.Contains(r.out, "/srv/blog/html:/usr/share/nginx/html") || !strings.Contains(r.out, "atoll docker rehearse imp_d") {
		t.Fatalf("import output %s", r.out)
	}
	// a bare name that is not a compose project means the standalone container
	runCLI(t, srv.URL, "", "docker", "import", "vps1", "cache")
	json.Unmarshal(f.bodies["POST /imports/docker"], &req)
	if req.Group != "container:cache" {
		t.Fatalf("standalone group %q", req.Group)
	}
	r = runCLI(t, srv.URL, "", "docker", "rehearse", "imp_d", "--yes", "--ignore-memory")
	if r.e != nil || !strings.Contains(string(f.bodies["POST /imports/imp_d/rehearse"]), `"ignore_memory":true`) {
		t.Fatal(r.e, r.err, string(f.bodies["POST /imports/imp_d/rehearse"]))
	}
	r = runCLI(t, srv.URL, "", "docker", "cutover", "imp_d", "--yes")
	if r.e != nil || !strings.Contains(r.out, "atoll docker rollback imp_d") {
		t.Fatal(r.e, r.err, r.out)
	}
	r = runCLI(t, srv.URL, "", "docker", "rollback", "imp_d", "--yes")
	if r.e != nil || !strings.Contains(r.out, "old containers run again") {
		t.Fatal(r.e, r.err, r.out)
	}

	// MCP
	app := &App{Version: "test", URL: srv.URL, Token: "atl_pat_test", ConfigPath: filepath.Join(t.TempDir(), "c.json")}
	if isErr, txt := mcpCall(t, app, "docker_scan", map[string]any{"server": "vps1"}); isErr || !strings.Contains(txt, "compose:blog") {
		t.Fatalf("docker_scan: %v %s", isErr, txt)
	}
	if isErr, txt := mcpCall(t, app, "docker_adopt", map[string]any{"server": "vps1", "group": "container:cache", "containers": []any{"cache"}}); isErr || !strings.Contains(txt, "imp_d") {
		t.Fatalf("docker_adopt: %v %s", isErr, txt)
	}
	json.Unmarshal(f.bodies["POST /imports/docker"], &req)
	if req.Group != "container:cache" || len(req.Containers) != 1 {
		t.Fatalf("mcp adopt body %+v", req)
	}
	for _, tool := range []string{"docker_rehearse", "docker_cutover", "docker_rollback"} {
		if isErr, txt := mcpCall(t, app, tool, map[string]any{"import_id": "imp_d"}); isErr {
			t.Fatalf("%s: %s", tool, txt)
		}
	}
	if MCPToolPermission("docker_cutover") != api.PermMigrate || MCPToolPermission("docker_scan") != api.PermRead {
		t.Fatal("docker tool permissions")
	}
}
