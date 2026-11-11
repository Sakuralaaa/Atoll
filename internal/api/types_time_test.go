package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// An unfinished agent upgrade (and every other optional time) must leave its field out instead
// of sending Go's zero time, which the panel showed as year 1.
func TestOptionalTimesOmittedWhenZero(t *testing.T) {
	b, err := json.Marshal(AgentUpgrade{State: AgentUpgradeRunning, From: "v0.1.43", To: "v0.1.45", StartedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "finished_at") || strings.Contains(string(b), "0001-01-01") {
		t.Fatalf("running upgrade leaks a zero finished_at: %s", b)
	}
	done := time.Date(2026, 11, 11, 8, 1, 0, 0, time.UTC)
	b, _ = json.Marshal(AgentUpgrade{State: AgentUpgradeDone, StartedAt: done, FinishedAt: done})
	if !strings.Contains(string(b), `"finished_at":"2026-11-11T08:01:00Z"`) {
		t.Fatalf("finished upgrade lost finished_at: %s", b)
	}
	// older peers read a missing field as the zero time, so decoding stays compatible
	var u AgentUpgrade
	if err := json.Unmarshal([]byte(`{"state":"upgrading","started_at":"2026-11-11T08:01:00Z"}`), &u); err != nil || !u.FinishedAt.IsZero() {
		t.Fatalf("decode: %v %+v", err, u)
	}
}
