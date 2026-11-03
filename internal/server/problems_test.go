package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

type hookSink struct {
	mu   sync.Mutex
	msgs []notifyMessage
	fail bool
}

func (h *hookSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fail {
		w.WriteHeader(500)
		return
	}
	var m notifyMessage
	json.NewDecoder(r.Body).Decode(&m)
	h.msgs = append(h.msgs, m)
}

func (h *hookSink) events() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, m := range h.msgs {
		out = append(out, m.Event+":"+m.Problem.Kind)
	}
	return out
}

func TestNodeOfflineProblemFiresNotifiesAndResolves(t *testing.T) {
	e := newTestEnvWith(t, func(c *Config) { c.ProblemsTick = time.Hour })
	e.setup()
	sink := &hookSink{}
	hook := httptest.NewServer(sink)
	t.Cleanup(hook.Close)

	// settings: secrets are write-only and kept when omitted
	var ns api.NotificationSettings
	e.mustDo("PUT", "/api/v1/settings/notifications", api.NotificationSettings{NotifyResolved: true,
		Channels: []api.NotificationChannel{{Type: "webhook", Name: "ops", Enabled: true, URL: hook.URL}}}, &ns, 200)
	if len(ns.Channels) != 1 || ns.Channels[0].URL != "" || !ns.Channels[0].HasURL || ns.Channels[0].ID == "" {
		t.Fatalf("put notifications %+v", ns)
	}
	ns.Channels[0].Name = "ops2" // URL omitted: stored one stays
	e.mustDo("PUT", "/api/v1/settings/notifications", ns, &ns, 200)
	e.mustDo("GET", "/api/v1/settings/notifications", nil, &ns, 200)
	if ns.Channels[0].Name != "ops2" || !ns.Channels[0].HasURL {
		t.Fatalf("get notifications %+v", ns)
	}
	if code := e.do("PUT", "/api/v1/settings/notifications", api.NotificationSettings{Channels: []api.NotificationChannel{{Type: "telegram"}}}, nil); code != 400 {
		t.Fatalf("telegram without token: %d", code)
	}
	e.mustDo("POST", "/api/v1/settings/notifications/test", api.NotificationTestRequest{Channel: ns.Channels[0]}, nil, 200)
	if ev := sink.events(); len(ev) != 1 || ev[0] != "test:test" {
		t.Fatalf("test event %v", ev)
	}

	agent, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	ctx := context.Background()
	agent.ws.CloseNow()
	waitFor(t, "node offline", func() bool { n, err := e.srv.st.GetNode(ctx, nodeID); return err == nil && n.Status == "offline" })

	now := time.Now()
	e.srv.evaluateProblems(ctx, now) // observed, still pending
	var list []api.Problem
	e.mustDo("GET", "/api/v1/problems", nil, &list, 200)
	if len(list) != 0 || len(sink.events()) != 1 {
		t.Fatalf("pending problem visible or notified: %+v %v", list, sink.events())
	}
	e.srv.evaluateProblems(ctx, now.Add(3*time.Minute)) // held past the 2m threshold
	e.mustDo("GET", "/api/v1/problems", nil, &list, 200)
	if len(list) != 1 || list[0].Kind != api.ProblemNodeOffline || list[0].SubjectID != nodeID || list[0].FiringAt == nil {
		t.Fatalf("problems %+v", list)
	}
	e.srv.evaluateProblems(ctx, now.Add(4*time.Minute)) // no duplicate notification
	if ev := sink.events(); len(ev) != 2 || ev[1] != "firing:node_offline" {
		t.Fatalf("events %v", ev)
	}

	// back online: resolved, one recovery message, gone from the list (kept with ?resolved=1)
	e.srv.st.SetNodeStatus(ctx, nodeID, "online", true)
	e.srv.evaluateProblems(ctx, now.Add(5*time.Minute))
	if ev := sink.events(); len(ev) != 3 || ev[2] != "resolved:node_offline" {
		t.Fatalf("events %v", ev)
	}
	e.mustDo("GET", "/api/v1/problems", nil, &list, 200)
	if len(list) != 0 {
		t.Fatalf("resolved still listed %+v", list)
	}
	e.mustDo("GET", "/api/v1/problems?resolved=1", nil, &list, 200)
	if len(list) != 1 || list[0].ResolvedAt == nil {
		t.Fatalf("resolved list %+v", list)
	}
}

func TestProblemBlipNeverFiresAndFailedDeliveryRetries(t *testing.T) {
	e := newTestEnvWith(t, func(c *Config) { c.ProblemsTick = time.Hour })
	e.setup()
	sink := &hookSink{fail: true}
	hook := httptest.NewServer(sink)
	t.Cleanup(hook.Close)
	e.mustDo("PUT", "/api/v1/settings/notifications", api.NotificationSettings{NotifyResolved: true,
		Channels: []api.NotificationChannel{{Type: "webhook", Enabled: true, URL: hook.URL}}}, nil, 200)
	_, nodeID := startFakeAgent(t, e, api.ZeaburInventory{})
	ctx := context.Background()
	now := time.Now()

	// offline for less than the threshold: nothing fires, nothing is kept
	e.srv.st.SetNodeStatus(ctx, nodeID, "offline", false)
	e.srv.evaluateProblems(ctx, now)
	e.srv.st.SetNodeStatus(ctx, nodeID, "online", true)
	e.srv.evaluateProblems(ctx, now.Add(time.Minute))
	if all, _ := e.srv.st.ListProblems(ctx, true); len(all) != 0 {
		t.Fatalf("blip left %+v", all)
	}

	// a failing channel is retried on the next tick
	e.srv.st.SetNodeStatus(ctx, nodeID, "offline", false)
	e.srv.evaluateProblems(ctx, now.Add(2*time.Minute))
	e.srv.evaluateProblems(ctx, now.Add(5*time.Minute))
	if len(sink.events()) != 0 {
		t.Fatal("failing sink recorded events")
	}
	sink.mu.Lock()
	sink.fail = false
	sink.mu.Unlock()
	e.srv.evaluateProblems(ctx, now.Add(6*time.Minute))
	if ev := sink.events(); len(ev) != 1 || ev[0] != "firing:node_offline" {
		t.Fatalf("retry events %v", ev)
	}
}

func TestQuietHours(t *testing.T) {
	ns := api.NotificationSettings{QuietStart: "23:00", QuietEnd: "08:00", Timezone: "Asia/Shanghai"}
	at := func(hhmm string) time.Time {
		loc, _ := time.LoadLocation("Asia/Shanghai")
		tm, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-03 "+hhmm, loc)
		return tm
	}
	for hhmm, want := range map[string]bool{"23:30": true, "02:00": true, "07:59": true, "08:00": false, "12:00": false, "22:59": false} {
		if got := inQuietHours(ns, at(hhmm)); got != want {
			t.Errorf("%s: got %v", hhmm, got)
		}
	}
	if inQuietHours(api.NotificationSettings{}, time.Now()) {
		t.Error("no quiet hours configured")
	}
	if txt := problemText(api.Problem{Kind: api.ProblemDiskHigh, SubjectID: "srv_1", SubjectName: "box", Message: "磁盘已用 95%"}, false, "https://panel.example.com"); !strings.Contains(txt, "#/servers/srv_1") || !strings.Contains(txt, "box") {
		t.Errorf("text %q", txt)
	}
}
