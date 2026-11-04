package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func dockerInventory() api.DockerInventory {
	return api.DockerInventory{DockerVersion: "28.0.0", MemFreeMB: 4096, DiskFreeGB: 40, Groups: []api.DockerGroup{{
		Key: "compose:blog", Kind: "compose", Name: "blog", WorkingDir: "/srv/blog", Adoptable: true,
		Warnings: []string{"network blog_default is shared with tools"},
		Containers: []api.DockerContainer{
			{ID: "c-web", Name: "blog-web-1", Service: "web", Image: "nginx:1.27", ImageID: "sha256:web", PinnedImage: "nginx@sha256:feed", State: "running",
				Env:   []api.EnvVar{{Key: "SITE", Value: "example.com"}, {Key: "TEMPLATE", Value: "${HOST}:80"}},
				Ports: []api.DockerPort{{ContainerPort: 80, HostPort: 8080, Protocol: "tcp"}, {ContainerPort: 9000, HostPort: 9000, Protocol: "tcp", HostIP: "127.0.0.1"}, {ContainerPort: 8443, Protocol: "tcp"}},
				Mounts: []api.DockerMount{{Type: "bind", Source: "/srv/blog/html", Destination: "/usr/share/nginx/html", ReadOnly: true},
					{Type: "volume", Name: "blog_data", Source: "/var/lib/docker/volumes/blog_data/_data", Destination: "/data"},
					{Type: "bind", Source: "/srv/blog/app.conf", Destination: "/etc/app.conf", IsFile: true}},
				RestartPolicy: "unless-stopped", Risk: api.DockerRiskAuto, MemUsageMB: 30},
			{ID: "c-db", Name: "blog-db-1", Service: "db", Image: "postgres:16", ImageID: "sha256:db", State: "running",
				Env:           []api.EnvVar{{Key: "POSTGRES_PASSWORD", Value: "s3cret-value"}},
				Ports:         []api.DockerPort{{ContainerPort: 5432, Protocol: "tcp"}},
				Mounts:        []api.DockerMount{{Type: "volume", Name: "blog_pg", Source: "/var/lib/docker/volumes/blog_pg/_data", Destination: "/var/lib/postgresql/data"}},
				RestartPolicy: "always", Risk: api.DockerRiskAuto, MemUsageMB: 60},
			{ID: "c-wt", Name: "blog-watchtower-1", Service: "watchtower", Image: "containrrr/watchtower", ImageID: "sha256:wt", State: "running",
				Risk: api.DockerRiskManual, RiskReasons: []string{"mounts the container runtime socket /var/run/docker.sock"}, RestartPolicy: "always"},
		},
	}}}
}

// dockerHost is the fake agent's view of the user's containers.
type dockerHost struct {
	mu      sync.Mutex
	inv     api.DockerInventory
	state   map[string]string // container name -> state
	policy  map[string]string
	failPub bool // fail applies that publish a host port (port already allocated)
}

func (h *dockerHost) hook(env inEnvelope) (api.ResultPayload, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch env.Type {
	case api.MsgDockerScan:
		inv := h.inv
		inv.Groups = append([]api.DockerGroup(nil), inv.Groups...)
		for gi := range inv.Groups {
			cs := append([]api.DockerContainer(nil), inv.Groups[gi].Containers...)
			for i := range cs {
				if st, ok := h.state[cs[i].Name]; ok {
					cs[i].State = st
				}
				if p, ok := h.policy[cs[i].Name]; ok {
					cs[i].RestartPolicy = p
				}
			}
			inv.Groups[gi].Containers = cs
		}
		inv.ScannedAt = time.Now().UTC()
		raw, _ := json.Marshal(inv)
		return api.ResultPayload{OK: true, Output: string(raw)}, true
	case api.MsgDockerImage:
		var p api.DockerImagePayload
		json.Unmarshal(env.Payload, &p)
		res := api.DockerImageResult{Ref: p.Pinned}
		if p.Pinned == "" {
			res = api.DockerImageResult{Ref: "atoll-adopt/" + p.ServiceID + ":0123456789ab", Local: true}
		}
		raw, _ := json.Marshal(res)
		return api.ResultPayload{OK: true, Output: string(raw)}, true
	case api.MsgDockerContainers:
		var p api.DockerContainersPayload
		json.Unmarshal(env.Payload, &p)
		for _, c := range p.Containers {
			if p.Action == "stop" {
				h.state[c.Name], h.policy[c.Name] = "exited", "no"
			} else {
				if c.RestartPolicy != "" {
					h.policy[c.Name] = c.RestartPolicy
				}
				if c.Start {
					h.state[c.Name] = "running"
				}
			}
		}
		return api.ResultPayload{OK: true, Output: `{"containers":[]}`}, true
	case api.MsgApply:
		var p api.ApplyPayload
		json.Unmarshal(env.Payload, &p)
		if h.failPub {
			for _, pt := range p.Spec.Ports {
				if pt.PublicPort > 0 {
					return api.ResultPayload{OK: false, Error: "port is already allocated"}, true
				}
			}
		}
	}
	return api.ResultPayload{}, false
}

func startDockerHost(t *testing.T, e *testEnv) (*fakeAgent, string, *dockerHost) {
	a, node := startFakeAgent(t, e, api.ZeaburInventory{})
	h := &dockerHost{inv: dockerInventory(), state: map[string]string{}, policy: map[string]string{}}
	a.mu.Lock()
	a.hook = h.hook
	a.mu.Unlock()
	return a, node, h
}

func lastApply(t *testing.T, a *fakeAgent, serviceID string) api.ResolvedSpec {
	t.Helper()
	var last *api.ResolvedSpec
	for _, raw := range a.received(api.MsgApply) {
		var p api.ApplyPayload
		json.Unmarshal(raw, &p)
		if p.Spec.ServiceID == serviceID {
			s := p.Spec
			last = &s
		}
	}
	if last == nil {
		t.Fatalf("no apply for %s", serviceID)
	}
	return *last
}

func dockerServices(t *testing.T, e *testEnv, projectID string) map[string]api.Service {
	var p api.Project
	e.mustDo("GET", "/api/v1/projects/"+projectID, nil, &p, 200)
	out := map[string]api.Service{}
	for _, s := range p.Services {
		out[s.Name] = s
	}
	return out
}

func TestDockerAdoptFlow(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	a, node, h := startDockerHost(t, e)

	var inv api.DockerInventory
	e.mustDo("GET", "/api/v1/servers/"+node+"/docker", nil, &inv, 200)
	if len(inv.Groups) != 1 || len(inv.Groups[0].Containers) != 3 {
		t.Fatalf("inventory %+v", inv)
	}
	if v := inv.Groups[0].Containers[1].Env[0].Value; strings.Contains(v, "s3cret-value") {
		t.Fatalf("env not masked: %q", v)
	}

	// a manual container cannot be picked
	e.mustDo("POST", "/api/v1/imports/docker", api.DockerAdoptRequest{ServerID: node, Group: "compose:blog", Containers: []string{"blog-watchtower-1"}}, nil, 400)
	var job api.ImportJob
	e.mustDo("POST", "/api/v1/imports/docker", api.DockerAdoptRequest{ServerID: node, Group: "compose:blog"}, &job, 201)
	if job.Mode != "docker" || job.Group != "compose:blog" || job.Status != "planned" || len(job.Plan.Services) != 2 {
		t.Fatalf("job %+v", job)
	}
	if ws := strings.Join(job.Plan.Warnings, "|"); !strings.Contains(ws, "blog-watchtower-1 is not adopted") || !strings.Contains(ws, "shared with tools") {
		t.Fatalf("warnings %v", job.Plan.Warnings)
	}
	svcs := dockerServices(t, e, job.ProjectID)
	web, db := svcs["web"], svcs["db"]
	if web.ID == "" || db.ID == "" {
		t.Fatalf("services %+v", svcs)
	}
	// created in the rehearsal shape: nothing published, writable dirs not in place
	for _, p := range web.Ports {
		if p.PublicPort != 0 || p.HostIP != "" {
			t.Fatalf("rehearsal port published: %+v", web.Ports)
		}
	}
	vols := map[string]api.Volume{}
	for _, v := range web.Volumes {
		vols[v.MountPath] = v
	}
	if vols["/usr/share/nginx/html"].HostPath != "/srv/blog/html" || !vols["/usr/share/nginx/html"].ReadOnly ||
		vols["/data"].HostPath != "" || vols["/etc/app.conf"].HostPath != "/srv/blog/app.conf" {
		t.Fatalf("rehearsal volumes %+v", web.Volumes)
	}
	// a literal ${ is escaped for the env resolver
	var env []api.EnvVar
	e.mustDo("GET", "/api/v1/services/"+web.ID+"/env?reveal=1", nil, &env, 200)
	foundTpl := false
	for _, v := range env {
		foundTpl = foundTpl || (v.Key == "TEMPLATE" && v.Value == "$${HOST}:80")
	}
	if !foundTpl {
		t.Fatalf("env %+v", env)
	}
	// the API cannot add or change host paths
	e.mustDo("POST", "/api/v1/projects/"+job.ProjectID+"/services", map[string]any{"name": "evil", "image": "alpine",
		"volumes": []api.Volume{{Name: "root", MountPath: "/host", HostPath: "/srv"}}}, nil, 400)
	e.mustDo("PATCH", "/api/v1/services/"+web.ID, map[string]any{"volumes": []api.Volume{{Name: "html", MountPath: "/usr/share/nginx/html", HostPath: "/srv/other"}}}, nil, 400)

	// rehearse
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rehearse", nil, nil, 200)
	job = waitJob(t, e, job.ID, "rehearsed", "failed")
	if job.Status != "rehearsed" {
		t.Fatalf("rehearsal: %+v", job.Steps)
	}
	ws, ds := lastApply(t, a, web.ID), lastApply(t, a, db.ID)
	if ws.Image != "nginx@sha256:feed" || ws.LocalImage || ds.Image != "atoll-adopt/"+db.ID+":0123456789ab" || !ds.LocalImage {
		t.Fatalf("images: web %s/%v db %s/%v", ws.Image, ws.LocalImage, ds.Image, ds.LocalImage)
	}
	for _, p := range ws.Ports {
		if p.PublicPort != 0 {
			t.Fatalf("rehearsal apply publishes %+v", ws.Ports)
		}
	}
	if h.state["blog-web-1"] == "exited" {
		t.Fatal("rehearsal stopped the old container")
	}
	if n := len(a.received(api.MsgAction)); n < 2 {
		t.Fatalf("rehearsal copies not stopped again: %d actions", n)
	}

	// cutover
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cutover", nil, nil, 200)
	job = waitJob(t, e, job.ID, "live", "rolled_back", "failed")
	if job.Status != "live" {
		t.Fatalf("cutover: %+v", job.Steps)
	}
	if h.state["blog-web-1"] != "exited" || h.policy["blog-db-1"] != "no" || h.state["blog-watchtower-1"] == "exited" {
		t.Fatalf("old containers: %v %v", h.state, h.policy)
	}
	ws = lastApply(t, a, web.ID)
	pub := map[int]api.Port{}
	for _, p := range ws.Ports {
		pub[p.ContainerPort] = p
	}
	if pub[80].PublicPort != 8080 || pub[9000].PublicPort != 9000 || pub[9000].HostIP != "127.0.0.1" || pub[8443].PublicPort != 0 {
		t.Fatalf("live ports %+v", ws.Ports)
	}
	for _, v := range ws.Volumes {
		if v.HostPath == "" {
			t.Fatalf("live volume not in place: %+v", ws.Volumes)
		}
	}
	// editing an adopted service keeps its host paths
	e.mustDo("PATCH", "/api/v1/services/"+db.ID, map[string]any{"volumes": []api.Volume{{Name: "blog-pg", MountPath: "/var/lib/postgresql/data"}}}, nil, 200)
	if v := dockerServices(t, e, job.ProjectID)["db"].Volumes[0]; v.HostPath != "/var/lib/docker/volumes/blog_pg/_data" {
		t.Fatalf("host path lost on patch: %+v", v)
	}
	// no cleanup for docker, and the data cannot be migrated
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cleanup", api.ZeaburCleanupRequest{Confirm: "x"}, nil, 400)

	// rollback
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rollback", nil, nil, 200)
	job = waitJob(t, e, job.ID, "rolled_back", "failed")
	if job.Status != "rolled_back" {
		t.Fatalf("rollback: %+v", job.Steps)
	}
	if h.state["blog-web-1"] != "running" || h.policy["blog-web-1"] != "unless-stopped" || h.policy["blog-db-1"] != "always" {
		t.Fatalf("old containers not restored: %v %v", h.state, h.policy)
	}
	for _, p := range dockerServices(t, e, job.ProjectID)["web"].Ports {
		if p.PublicPort != 0 {
			t.Fatalf("rolled-back service still publishes %+v", p)
		}
	}
	// adopting the same group again is refused while one is live; after rollback it is fine
	// to cut over again
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cutover", nil, nil, 200)
	job = waitJob(t, e, job.ID, "live", "rolled_back", "failed")
	if job.Status != "live" {
		t.Fatalf("second cutover: %+v", job.Steps)
	}
	e.mustDo("POST", "/api/v1/imports/docker", api.DockerAdoptRequest{ServerID: node, Group: "compose:blog"}, nil, 409)
}

func TestDockerCutoverFailureRollsBack(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, node, h := startDockerHost(t, e)
	var job api.ImportJob
	e.mustDo("POST", "/api/v1/imports/docker", api.DockerAdoptRequest{ServerID: node, Group: "compose:blog", ProjectName: "blog-adopted"}, &job, 201)
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rehearse", map[string]bool{"ignore_memory": true}, nil, 200)
	if j := waitJob(t, e, job.ID, "rehearsed", "failed"); j.Status != "rehearsed" {
		t.Fatalf("rehearsal %+v", j.Steps)
	}
	h.mu.Lock()
	h.failPub = true
	h.mu.Unlock()
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/cutover", nil, nil, 200)
	job = waitJob(t, e, job.ID, "rolled_back", "failed", "live")
	if job.Status != "rolled_back" {
		t.Fatalf("want automatic rollback, got %s: %+v", job.Status, job.Steps)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state["blog-web-1"] != "running" || h.state["blog-db-1"] != "running" || h.policy["blog-db-1"] != "always" {
		t.Fatalf("old containers not restored: %v %v", h.state, h.policy)
	}
	rolled := false
	for _, s := range job.Steps {
		rolled = rolled || (s.Name == dstepRollback && s.Status == "done")
	}
	if !rolled {
		t.Fatalf("steps %+v", job.Steps)
	}
}

func TestDockerRehearseMemoryCheck(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, node, h := startDockerHost(t, e)
	h.inv.MemFreeMB = 50 // needs 30 + 60
	var job api.ImportJob
	e.mustDo("POST", "/api/v1/imports/docker", api.DockerAdoptRequest{ServerID: node, Group: "compose:blog"}, &job, 201)
	var eb api.ErrorBody
	if code := e.do("POST", "/api/v1/imports/"+job.ID+"/rehearse", nil, &eb); code != 409 || !strings.Contains(eb.Error+eb.Code, "insufficient_memory") {
		t.Fatalf("memory check: %d %+v", code, eb)
	}
}

func TestCheckHostPaths(t *testing.T) {
	prev := []api.Volume{{Name: "data", MountPath: "/data", HostPath: "/srv/data", ReadOnly: true}}
	next := []api.Volume{{Name: "data", MountPath: "/data"}, {Name: "new", MountPath: "/new"}}
	if err := checkHostPaths(next, prev); err != nil || next[0].HostPath != "/srv/data" || !next[0].ReadOnly {
		t.Fatalf("keep: %v %+v", err, next)
	}
	if err := checkHostPaths([]api.Volume{{Name: "data", MountPath: "/other"}}, prev); err == nil {
		t.Fatal("mount path change accepted")
	}
	if err := checkHostPaths([]api.Volume{{Name: "x", MountPath: "/x", HostPath: "/srv"}}, nil); err == nil {
		t.Fatal("new host path accepted")
	}
	if escapeRefs("a${B}c$D") != "a$${B}c$D" {
		t.Fatal(escapeRefs("a${B}c$D"))
	}
	ports := dockerPorts(api.DockerContainer{Ports: []api.DockerPort{{ContainerPort: 53, HostPort: 53, Protocol: "udp"}, {ContainerPort: 53, HostPort: 53, Protocol: "tcp", HostIP: "127.0.0.1"}, {ContainerPort: 80, Protocol: "tcp"}}})
	if len(ports) != 3 || ports[0].Name != "p53-udp" || ports[1].Name != "p53-127-0-0-1" || ports[2].Name != "c80" {
		t.Fatalf("ports %+v", ports)
	}
}

func TestDockerCutoverApprovalAndNoMigration(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	_, node, _ := startDockerHost(t, e)
	_, other := startFakeAgentAs(t, e, api.ZeaburInventory{}, "second-box", "203.0.113.8")
	var tok api.APIToken
	e.mustDo("POST", "/api/v1/tokens", map[string]any{"name": "ci", "permissions": []string{"deploy", "migrate"}, "require_approval": true}, &tok, 201)
	var job api.ImportJob
	if code := e.doWith(http.DefaultClient, tok.Token, "POST", "/api/v1/imports/docker", api.DockerAdoptRequest{ServerID: node, Group: "compose:blog"}, &job); code != 201 {
		t.Fatalf("adopt with PAT: %d", code)
	}
	e.mustDo("POST", "/api/v1/imports/"+job.ID+"/rehearse", nil, nil, 200)
	waitJob(t, e, job.ID, "rehearsed")
	var c api.Confirmation
	if code := e.doWith(http.DefaultClient, tok.Token, "POST", "/api/v1/imports/"+job.ID+"/cutover", nil, &c); code != 202 || c.Action != "import.cutover" {
		t.Fatalf("cutover with PAT: %d %+v", code, c)
	}
	e.mustDo("POST", fmt.Sprintf("/api/v1/confirmations/%s/approve", c.ID), nil, nil, 200)
	if job = waitJob(t, e, job.ID, "live", "rolled_back", "failed"); job.Status != "live" {
		t.Fatalf("approved cutover: %+v", job.Steps)
	}
	// data mounted in place cannot move to another server
	var eb api.ErrorBody
	if code := e.do("POST", "/api/v1/projects/"+job.ProjectID+"/migrate", map[string]string{"server_id": other}, &eb); code != 409 || eb.Code != "host_path" {
		t.Fatalf("migrate adopted project: %d %+v", code, eb)
	}
}
