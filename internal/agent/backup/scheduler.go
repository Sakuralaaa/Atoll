package backup

import (
	"context"
	"time"

	"github.com/Sakuralaaa/Atoll/internal/api"
	"github.com/Sakuralaaa/Atoll/internal/cronexpr"
)

// Loop runs the local schedule until ctx ends. It needs no server: the config is read from
// disk, so a rebooted agent catches up a run it missed while down (once, not per missed slot).
func (m *Manager) Loop(ctx context.Context) {
	m.load()
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
	m.reportInterrupted()
	m.CheckDue(ctx)
	t := time.NewTicker(m.Tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.CheckDue(ctx)
		}
	}
}

// reportInterrupted fails a run that was in progress when the agent stopped.
func (m *Manager) reportInterrupted() {
	fl := m.readInFlight()
	if fl == nil {
		return
	}
	now := m.Now().UTC()
	m.Emit(api.MsgBackupEvent, api.BackupEvent{BackupID: fl.BackupID, ServiceID: fl.ServiceID, Trigger: fl.Trigger, Status: "failed",
		Error: "interrupted: the agent stopped during the backup", StartedAt: fl.StartedAt, FinishedAt: &now,
		DurationMs: now.Sub(fl.StartedAt).Milliseconds()})
	m.saveState(nil, true)
}

// Due reports whether target t should run at now given its state.
func Due(t api.BackupTarget, st svcStateView, now time.Time, retryAfter time.Duration, maxRetries int) bool {
	if !t.Enabled {
		return false
	}
	sc, err := cronexpr.Parse(t.Schedule)
	if err != nil {
		return false
	}
	loc, err := cronexpr.LoadLocation(t.Timezone)
	if err != nil {
		return false
	}
	prev := sc.Prev(now, loc)
	if prev.IsZero() {
		return false
	}
	if st.LastAttempt.Before(prev) {
		return true // a scheduled time passed since the last attempt (incl. missed while down)
	}
	// the run for this slot failed: retry a few times with growing delay
	if st.Failures > 0 && st.Failures < maxRetries && st.LastSuccess.Before(prev) &&
		now.Sub(st.LastAttempt) >= retryAfter*time.Duration(st.Failures) {
		return true
	}
	return false
}

// svcStateView is the scheduling state of one service.
type svcStateView = svcState

// CheckDue runs every due target (sequentially).
func (m *Manager) CheckDue(ctx context.Context) {
	m.mu.Lock()
	cfg := m.cfg
	now := m.Now()
	var due []api.BackupTarget
	if cfg.Repo != nil {
		for _, t := range cfg.Targets {
			st := m.state[t.ServiceID]
			if st == nil {
				st = &svcState{LastAttempt: now}
				m.state[t.ServiceID] = st
			}
			if Due(t, *st, now, m.RetryAfter, m.MaxRetries) {
				due = append(due, t)
			}
		}
	}
	m.mu.Unlock()
	for _, t := range due {
		if ctx.Err() != nil {
			return
		}
		m.RunBackup(ctx, newBackupID(), "schedule", t, *cfg.Repo)
	}
}
