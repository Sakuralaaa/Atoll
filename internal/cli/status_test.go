package cli

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestStatusListsProblemsAndCheckExits5(t *testing.T) {
	f, srv := newFake(t)
	now := time.Now()
	probs := []api.Problem{}
	f.extra = func(w http.ResponseWriter, r *http.Request, key string, body []byte) bool {
		switch key {
		case "GET /servers":
			writeJSON(w, 200, []api.Server{{ID: "srv_1", Name: "a", Role: "worker", Status: "online"}, {ID: "srv_2", Name: "b", Role: "worker", Status: "offline"}, {ID: "ctl", Role: "control", Status: "online"}})
		case "GET /problems":
			writeJSON(w, 200, probs)
		default:
			return false
		}
		return true
	}
	r := runCLI(t, srv.URL, "", "status", "--check")
	if r.e != nil || !strings.Contains(r.out, "servers: 1/2 online") || !strings.Contains(r.out, "no problems") {
		t.Fatalf("%v %q", r.e, r.out)
	}
	probs = []api.Problem{{ID: "prb_1", Kind: api.ProblemNodeOffline, SubjectID: "srv_2", SubjectName: "b", Message: "agent 已断开", Since: now.Add(-5 * time.Minute), FiringAt: &now}}
	r = runCLI(t, srv.URL, "", "status", "--check")
	var pf *problemsFoundError
	if !errors.As(r.e, &pf) || !strings.Contains(r.out, "server offline") || !strings.Contains(r.out, "5m ago") {
		t.Fatalf("%v %q", r.e, r.out)
	}
}
