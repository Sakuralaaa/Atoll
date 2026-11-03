package store

import (
	"context"
	"testing"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
)

func TestAgentUpgradesPersist(t *testing.T) {
	dir := t.TempDir()
	s := openTest(t, dir)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.CreateNode(ctx, api.Server{ID: "srv_a", Name: "a", Role: "worker", Status: "online", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	u := api.AgentUpgrade{State: api.AgentUpgradeRunning, From: "v0.1.7", To: "v0.1.8", StartedAt: now}
	if err := s.SaveAgentUpgrade(ctx, "srv_a", u); err != nil {
		t.Fatal(err)
	}
	u.State, u.Error, u.RolledBack, u.FinishedAt = api.AgentUpgradeFailed, "did not reconnect", true, now.Add(time.Minute)
	if err := s.SaveAgentUpgrade(ctx, "srv_a", u); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, dir) // survives a restart
	defer s.Close()
	m, err := s.ListAgentUpgrades(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := m["srv_a"]
	if len(m) != 1 || got.State != api.AgentUpgradeFailed || !got.RolledBack || got.Error != u.Error || !got.StartedAt.Equal(now) || !got.FinishedAt.Equal(u.FinishedAt) || got.To != "v0.1.8" {
		t.Fatalf("got %+v", m)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM nodes WHERE id='srv_a'`); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.ListAgentUpgrades(ctx); len(m) != 0 {
		t.Fatal("upgrade row outlived its node")
	}
}
