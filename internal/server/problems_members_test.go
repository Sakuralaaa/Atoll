package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

// B5: a member sees, and is notified of, only the problems of its servers and of its
// projects' services on them; the admin keeps seeing and getting everything.
func TestMemberProblemsAndNotifications(t *testing.T) {
	e := newMemberEnvWith(t, func(c *Config) {
		c.ProblemsTick = time.Hour
		c.MemberNotifyHTTP = http.DefaultClient // the sink listens on loopback
	})
	ctx := context.Background()
	adminSink, memberSink := &hookSink{}, &hookSink{}
	ah, mh := httptest.NewServer(adminSink), httptest.NewServer(memberSink)
	t.Cleanup(ah.Close)
	t.Cleanup(mh.Close)
	e.mustDo("PUT", "/api/v1/settings/notifications", api.NotificationSettings{NotifyResolved: true,
		Channels: []api.NotificationChannel{{Type: "webhook", Enabled: true, URL: ah.URL}}}, nil, 200)

	// member channels: webhook/telegram only, secrets write-only; admins use settings
	var mn api.MemberNotifications
	if code := e.mdo("PUT", "/api/v1/me/notifications", api.MemberNotifications{Channels: []api.NotificationChannel{{Type: "email", Enabled: true, SMTPHost: "smtp.example.com", From: "a@example.com", To: "b@example.com"}}}, nil); code != 400 {
		t.Fatalf("member email channel: %d", code)
	}
	if code := e.mdo("PUT", "/api/v1/me/notifications", api.MemberNotifications{Channels: []api.NotificationChannel{{Type: "webhook", Enabled: true, URL: mh.URL}}}, &mn); code != 200 || len(mn.Channels) != 1 || mn.Channels[0].URL != "" || !mn.Channels[0].HasURL {
		t.Fatalf("member channels %d %+v", code, mn)
	}
	if code := e.mdo("POST", "/api/v1/me/notifications/test", api.NotificationTestRequest{Channel: mn.Channels[0]}, nil); code != 200 {
		t.Fatalf("member test %d", code)
	}
	if code := e.do("GET", "/api/v1/me/notifications", nil, nil); code != 400 {
		t.Fatalf("admin /me/notifications: %d", code)
	}
	if code := e.mdo("GET", "/api/v1/settings/notifications", nil, nil); code != 403 {
		t.Fatalf("member reads admin channels: %d", code)
	}

	observed := []api.Problem{
		{Kind: api.ProblemNodeOffline, SubjectID: "srv_a", SubjectName: "srv_a", ServerID: "srv_a"},
		{Kind: api.ProblemNodeOffline, SubjectID: "srv_b", SubjectName: "srv_b", ServerID: "srv_b"},
		{Kind: api.ProblemServiceDown, SubjectID: "svc_a", SubjectName: "web-a", ProjectID: "prj_a", ServerID: "srv_a"},
		{Kind: api.ProblemServiceDown, SubjectID: "svc_b", SubjectName: "web-b", ProjectID: "prj_b", ServerID: "srv_b"},
		// its project, but the service runs on a server it does not have
		{Kind: api.ProblemDomainError, SubjectID: "dom_x", SubjectName: "x.example.com", ProjectID: "prj_a", ServerID: "srv_b"},
		// panel-wide: the backup repository, checked from its server
		{Kind: api.ProblemBackupVerifyFailed, SubjectID: api.BackupRepoSubjectID, SubjectName: "备份仓库", ServerID: "srv_a"},
	}
	now := time.Now()
	for _, at := range []time.Time{now, now.Add(time.Hour)} {
		if _, err := e.srv.st.SyncProblems(ctx, at, observed); err != nil {
			t.Fatal(err)
		}
	}
	e.srv.deliverNotifications(ctx, now.Add(time.Hour))

	subjects := func(list []api.Problem) string {
		var out []string
		for _, p := range list {
			out = append(out, p.SubjectID)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	var mine, all []api.Problem
	if code := e.mdo("GET", "/api/v1/problems", nil, &mine); code != 200 || subjects(mine) != "srv_a,svc_a" {
		t.Fatalf("member problems %d %s", code, subjects(mine))
	}
	e.mustDo("GET", "/api/v1/problems", nil, &all, 200)
	if len(all) != len(observed) {
		t.Fatalf("admin problems %s", subjects(all))
	}
	got := func(h *hookSink) string {
		h.mu.Lock()
		defer h.mu.Unlock()
		var out []string
		for _, m := range h.msgs {
			if m.Event == "firing" {
				out = append(out, m.Problem.SubjectID)
			}
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	if g := got(memberSink); g != "srv_a,svc_a" {
		t.Fatalf("member notified of %s", g)
	}
	if g := got(adminSink); strings.Count(g, ",") != len(observed)-1 {
		t.Fatalf("admin notified of %s", g)
	}
	// once only: a later pass sends nothing new
	e.srv.deliverNotifications(ctx, now.Add(2*time.Hour))
	if g := got(memberSink); g != "srv_a,svc_a" {
		t.Fatalf("member notified again: %s", g)
	}
	// disabled member: no more messages; it recovers -> the admin hears, the member does not
	yes := true
	if err := e.srv.st.UpdateMember(ctx, e.member.ID, storeMemberDisabled(yes)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.srv.st.SyncProblems(ctx, now.Add(3*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	e.srv.deliverNotifications(ctx, now.Add(3*time.Hour))
	memberSink.mu.Lock()
	n := len(memberSink.msgs)
	memberSink.mu.Unlock()
	if n != 3 { // test + 2 firing
		t.Fatalf("disabled member got %d messages", n)
	}
}

// The default client for member webhooks refuses private and loopback addresses.
func TestMemberWebhookRefusesPrivateAddresses(t *testing.T) {
	sink := &hookSink{}
	h := httptest.NewServer(sink)
	t.Cleanup(h.Close)
	resp, err := publicOnlyHTTP().Post(h.URL, "application/json", strings.NewReader("{}"))
	if err == nil {
		resp.Body.Close()
		t.Fatal("loopback webhook was reached")
	}
	if !strings.Contains(err.Error(), "address not allowed") {
		t.Fatalf("err %v", err)
	}
}

// The "problems" frame carries only a change count; members get it to refetch GET /problems.
func TestMemberSeesProblemsFrame(t *testing.T) {
	e := newTestEnv(t)
	p := &principal{Member: true, Servers: map[string]bool{}, Projects: map[string]bool{}}
	if !e.srv.memberSeesEvent(context.Background(), p, []byte(`{"type":"problems","payload":{"changed":2}}`)) {
		t.Fatal("problems frame dropped for a member")
	}
}
