package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/server/registry"
)

// miniRegistry is an anonymous-token OCI registry with mutable tag digests.
type miniRegistry struct {
	mu      sync.Mutex
	digests map[string]string // tag -> digest (single repo)
	tags    []string
	heads   int
	srv     *httptest.Server
}

func newMiniRegistry(t *testing.T) *miniRegistry {
	m := &miniRegistry{digests: map[string]string{}}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if r.URL.Path == "/token" {
			fmt.Fprint(w, `{"token":"t1","expires_in":300}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer t1" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="mini"`, m.srv.URL))
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/acme/acme-api/manifests/"):
			dg, ok := m.digests[strings.TrimPrefix(r.URL.Path, "/v2/acme/acme-api/manifests/")]
			if !ok {
				w.WriteHeader(404)
				return
			}
			if r.Method != http.MethodHead {
				w.WriteHeader(405)
				return
			}
			m.heads++
			w.Header().Set("Docker-Content-Digest", dg)
		case r.URL.Path == "/v2/acme/acme-api/tags/list":
			b, _ := json.Marshal(map[string]any{"name": "acme/acme-api", "tags": m.tags})
			w.Write(b)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *miniRegistry) set(tag, digest string) {
	m.mu.Lock()
	m.digests[tag] = digest
	m.mu.Unlock()
}

func (m *miniRegistry) host() string { return strings.TrimPrefix(m.srv.URL, "http://") }

// waitApplyImage waits for an apply of serviceID with the given image and returns the spec.
func waitApplyImage(t *testing.T, a *fakeAgent, serviceID, image string) api.ResolvedSpec {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, raw := range a.received(api.MsgApply) {
			var ap api.ApplyPayload
			json.Unmarshal(raw, &ap)
			if ap.Spec.ServiceID == serviceID && ap.Spec.Image == image {
				return ap.Spec
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no apply of %s with image %s", serviceID, image)
	return api.ResolvedSpec{}
}

func countApplies(a *fakeAgent, serviceID string) int {
	n := 0
	for _, raw := range a.received(api.MsgApply) {
		var ap api.ApplyPayload
		json.Unmarshal(raw, &ap)
		if ap.Spec.ServiceID == serviceID {
			n++
		}
	}
	return n
}

// setRunningDigest makes the fake agent report digest for serviceID and waits until the
// server stored it.
func setRunningDigest(t *testing.T, e *testEnv, a *fakeAgent, serviceID, digest string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		c := a.ctrs[serviceID]
		c.ImageDigest = digest
		a.ctrs[serviceID] = c
		a.mu.Unlock()
		if cs, _, err := e.srv.st.ContainerState(e.srv.bg, serviceID); err == nil && cs.ImageDigest == digest {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("server never saw digest %s", digest)
}

func waitDeploymentRunning(t *testing.T, e *testEnv, serviceID, trigger string) api.Deployment {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var deps []api.Deployment
		e.mustDo("GET", "/api/v1/services/"+serviceID+"/deployments", nil, &deps, 200)
		if len(deps) > 0 && deps[0].Trigger == trigger && deps[0].Status == "running" {
			return deps[0]
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("no running %s deployment", trigger)
	return api.Deployment{}
}

func TestImageUpdateDetectAndAutoUpdate(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	reg := newMiniRegistry(t)
	rc := registry.New()
	rc.PlainHTTP = func(string) bool { return true }
	rc.DigestTTL, rc.TagsTTL = 0, 0 // every check reaches the fake registry
	e.srv.upd.reg = rc
	clock := time.Now().UTC()
	var clockMu sync.Mutex
	e.srv.upd.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return clock }
	advance := func(d time.Duration) { clockMu.Lock(); clock = clock.Add(d); clockMu.Unlock() }

	reg.set("1.2", "sha256:0001")
	reg.set("1.3", "sha256:1300")
	reg.tags = []string{"1.1", "1.2", "1.3", "2.0", "latest"}

	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "acme", "server_id": nodeID}, &p, 201)
	image := reg.host() + "/acme/acme-api:1.2"
	var svc api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{
		"name": "acme-api", "kind": "image", "image": image,
		"ports": []api.Port{{Name: "web", ContainerPort: 8080, Protocol: "http"}},
	}, &svc, 201)
	waitApplyImage(t, agent, svc.ID, image)
	waitDeploymentRunning(t, e, svc.ID, "create")
	setRunningDigest(t, e, agent, svc.ID, "sha256:0001")

	var iu api.ImageUpdate
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/image-update", nil, &iu, 200)
	if iu.Status != "unknown" || iu.Policy != "notify" {
		t.Fatalf("before check: %+v", iu)
	}

	// check now: digest unchanged, but a newer same-major tag and a major bump exist
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/image-update/check", nil, &iu, 200)
	if iu.Status != "update_available" || iu.DigestChanged || iu.NewerTag != "1.3" || iu.MajorTag != "2.0" ||
		iu.CurrentDigest != "sha256:0001" || iu.LatestDigest != "sha256:0001" || iu.CheckedAt == nil {
		t.Fatalf("after check: %+v", iu)
	}

	// policy validation
	if code := e.do("PUT", "/api/v1/services/"+svc.ID+"/image-update/policy", api.ImageUpdatePolicy{Policy: "sometimes"}, nil); code != 400 {
		t.Fatalf("bad policy: %d", code)
	}
	if code := e.do("PUT", "/api/v1/services/"+svc.ID+"/image-update/policy", api.ImageUpdatePolicy{Policy: "auto", Window: "25:00-03:00"}, nil); code != 400 {
		t.Fatalf("bad window: %d", code)
	}
	// auto, with a window that is closed right now
	now := e.srv.upd.now()
	closed := fmt.Sprintf("%02d:00-%02d:00", (now.Hour()+2)%24, (now.Hour()+3)%24)
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/image-update/policy", api.ImageUpdatePolicy{Policy: "auto", Window: closed}, &iu, 200)
	if iu.Policy != "auto" || iu.Window != closed {
		t.Fatalf("policy: %+v", iu)
	}

	// the tag moves; a scheduled check before the interval does nothing
	reg.set("1.2", "sha256:0002")
	before := countApplies(agent, svc.ID)
	e.srv.runDueImageChecks(e.srv.bg)
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/image-update", nil, &iu, 200)
	if iu.LatestDigest != "sha256:0001" {
		t.Fatalf("checked before the interval: %+v", iu)
	}
	// after the interval: detected, but the window is closed -> no deploy
	advance(6*time.Hour + time.Minute)
	e.srv.runDueImageChecks(e.srv.bg)
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/image-update", nil, &iu, 200)
	if !iu.DigestChanged || iu.LatestDigest != "sha256:0002" || !strings.Contains(iu.Note, "window") {
		t.Fatalf("closed window: %+v", iu)
	}
	time.Sleep(150 * time.Millisecond)
	if countApplies(agent, svc.ID) != before {
		t.Fatal("auto-update deployed outside its window")
	}

	// open window: the next tick deploys name@digest without another registry request
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/image-update/policy", api.ImageUpdatePolicy{Policy: "auto"}, nil, 200)
	reg.mu.Lock()
	heads := reg.heads
	reg.mu.Unlock()
	e.srv.runDueImageChecks(e.srv.bg)
	pinned := reg.host() + "/acme/acme-api@sha256:0002"
	waitApplyImage(t, agent, svc.ID, pinned)
	d := waitDeploymentRunning(t, e, svc.ID, "image-update")
	if !strings.Contains(d.Message, "auto-update") {
		t.Fatalf("deployment message %q", d.Message)
	}
	reg.mu.Lock()
	if reg.heads != heads {
		t.Fatal("auto-apply re-queried the registry")
	}
	reg.mu.Unlock()
	var got api.Service
	e.mustDo("GET", "/api/v1/services/"+svc.ID, nil, &got, 200)
	if got.Image != image {
		t.Fatalf("service must keep following the tag, image=%s", got.Image)
	}
	setRunningDigest(t, e, agent, svc.ID, "sha256:0002")
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/image-update", nil, &iu, 200)
	if iu.DigestChanged || iu.LastAutoDigest != "sha256:0002" || iu.NewerTag != "1.3" {
		t.Fatalf("after auto-update: %+v", iu)
	}
	n := countApplies(agent, svc.ID)
	e.srv.runDueImageChecks(e.srv.bg)
	time.Sleep(150 * time.Millisecond)
	if countApplies(agent, svc.ID) != n {
		t.Fatal("redeployed although up to date")
	}

	// roll back to the original deployment: auto-update pauses even when the tag moves again
	var deps []api.Deployment
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/deployments", nil, &deps, 200)
	first := deps[len(deps)-1]
	// the real agent reports the repo digest in deploy events; the fake does not
	e.srv.st.UpdateDeployment(e.srv.bg, first.ID, "superseded", "", "sha256:0001")
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/rollback", map[string]string{"deployment_id": first.ID}, nil, 200)
	// rollback is pinned to the digest the old deployment ran, not the moved tag
	waitApplyImage(t, agent, svc.ID, reg.host()+"/acme/acme-api@sha256:0001")
	waitDeploymentRunning(t, e, svc.ID, "rollback")
	setRunningDigest(t, e, agent, svc.ID, "sha256:0001")
	reg.set("1.2", "sha256:0003")
	advance(6*time.Hour + time.Minute)
	n = countApplies(agent, svc.ID)
	e.srv.runDueImageChecks(e.srv.bg)
	time.Sleep(150 * time.Millisecond)
	if countApplies(agent, svc.ID) != n {
		t.Fatal("auto-update ignored a rollback")
	}
	e.mustDo("GET", "/api/v1/services/"+svc.ID+"/image-update", nil, &iu, 200)
	if !iu.DigestChanged || !strings.Contains(iu.Note, "rolled back") {
		t.Fatalf("after rollback: %+v", iu)
	}

	// manual apply of the newer tag switches the service to it, pinned to its digest
	var dep api.Deployment
	e.mustDo("POST", "/api/v1/services/"+svc.ID+"/image-update/apply", api.ImageUpdateApplyRequest{Tag: "1.3"}, &dep, 200)
	waitApplyImage(t, agent, svc.ID, reg.host()+"/acme/acme-api@sha256:1300")
	e.mustDo("GET", "/api/v1/services/"+svc.ID, nil, &got, 200)
	if got.Image != reg.host()+"/acme/acme-api:1.3" {
		t.Fatalf("image after tag apply: %s", got.Image)
	}
	if code := e.do("POST", "/api/v1/services/"+svc.ID+"/image-update/apply", api.ImageUpdateApplyRequest{Tag: "9.9"}, nil); code != 502 {
		t.Fatalf("unknown tag: %d", code)
	}
	waitDeploymentRunning(t, e, svc.ID, "image-update")
	setRunningDigest(t, e, agent, svc.ID, "sha256:1300")
	var list []api.ImageUpdate
	e.mustDo("GET", "/api/v1/image-updates", nil, &list, 200)
	if len(list) != 1 || list[0].CurrentTag != "1.3" || list[0].NewerTag != "" || list[0].MajorTag != "2.0" || list[0].UpdateAvailable {
		t.Fatalf("list: %+v", list)
	}
	e.mustDo("GET", "/api/v1/image-updates?available=1", nil, &list, 200)
	if len(list) != 0 {
		t.Fatalf("available list: %+v", list)
	}

	// policy off disables checks
	e.mustDo("PUT", "/api/v1/services/"+svc.ID+"/image-update/policy", api.ImageUpdatePolicy{Policy: "off"}, &iu, 200)
	if iu.Status != "disabled" {
		t.Fatalf("off: %+v", iu)
	}
}

func TestImageUpdateUnsupportedAndErrors(t *testing.T) {
	e := newTestEnv(t)
	e.setup()
	reg := newMiniRegistry(t)
	rc := registry.New()
	rc.PlainHTTP = func(string) bool { return true }
	e.srv.upd.reg = rc
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	var p api.Project
	e.mustDo("POST", "/api/v1/projects", map[string]string{"name": "mail", "server_id": nodeID}, &p, 201)
	var local, missing api.Service
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "mail-relay", "kind": "image", "image": "atoll-import/svc_x:0123456789ab"}, &local, 201)
	e.mustDo("POST", "/api/v1/projects/"+p.ID+"/services", map[string]any{"name": "worker", "kind": "image", "image": reg.host() + "/acme/acme-api:nope"}, &missing, 201)
	var iu api.ImageUpdate
	e.mustDo("POST", "/api/v1/services/"+local.ID+"/image-update/check", nil, &iu, 200)
	if iu.Status != "unsupported" {
		t.Fatalf("local image: %+v", iu)
	}
	if code := e.do("POST", "/api/v1/services/"+local.ID+"/image-update/apply", nil, nil); code != 400 {
		t.Fatalf("apply local: %d", code)
	}
	e.mustDo("POST", "/api/v1/services/"+missing.ID+"/image-update/check", nil, &iu, 200)
	if iu.Status != "error" || !strings.Contains(iu.Error, "not found") || iu.NextCheckAt == nil {
		t.Fatalf("missing tag: %+v", iu)
	}
	// failures back off: next check ~5 minutes out, not the full interval
	if d := time.Until(*iu.NextCheckAt); d > 6*time.Minute || d < 3*time.Minute {
		t.Fatalf("backoff %v", d)
	}
	// settings expose the interval
	var st api.Settings
	e.mustDo("GET", "/api/v1/settings", nil, &st, 200)
	if st.ImageCheckIntervalHours != 6 {
		t.Fatalf("default interval %d", st.ImageCheckIntervalHours)
	}
	st.ImageCheckIntervalHours = 12
	e.mustDo("PUT", "/api/v1/settings", st, &st, 200)
	if st.ImageCheckIntervalHours != 12 {
		t.Fatalf("interval %d", st.ImageCheckIntervalHours)
	}
}

func TestInWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.UTC) }
	cases := []struct {
		w    string
		t    time.Time
		want bool
	}{
		{"", at(12, 0), true},
		{"02:00-05:00", at(3, 0), true},
		{"02:00-05:00", at(5, 0), false},
		{"02:00-05:00", at(1, 59), false},
		{"22:30-01:00", at(23, 0), true},
		{"22:30-01:00", at(0, 30), true},
		{"22:30-01:00", at(12, 0), false},
		{"bogus", at(3, 0), false},
	}
	for _, c := range cases {
		if got := inWindow(c.t, c.w); got != c.want {
			t.Errorf("inWindow(%s, %q) = %v", c.t.Format("15:04"), c.w, got)
		}
	}
	if _, _, err := parseWindow("03:00-03:00"); err == nil {
		t.Fatal("empty window accepted")
	}
}
