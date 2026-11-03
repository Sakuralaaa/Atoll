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
	m.reportInterruptedVerify()
	m.cleanupStaleDrills(ctx)
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
	vdue := m.dueVerifications(cfg, now)
	m.mu.Unlock()
	for i, t := range due {
		if ctx.Err() != nil {
			return
		}
		if i > 0 { // an earlier run took a while: a catch-up may have covered this one
			m.mu.Lock()
			st := m.state[t.ServiceID]
			still := st == nil || Due(t, *st, m.Now(), m.RetryAfter, m.MaxRetries)
			m.mu.Unlock()
			if !still {
				continue
			}
		}
		m.RunBackup(ctx, newBackupID(), "schedule", t, *cfg.Repo)
	}
	for _, p := range vdue {
		if ctx.Err() != nil {
			return
		}
		key := verifyKey(p.Kind, p.ServiceID())
		if !m.claim(key) {
			continue // a manual one is running
		}
		m.runVerify(ctx, p)
		m.release(key)
	}
}

// dueVerifications returns the scheduled checks and drills that are due (m.mu held). They
// are not retried after a failure: the next slot is a week or a month away, and the failure
// is reported as a problem the user can act on (or re-run from the panel).
func (m *Manager) dueVerifications(cfg api.BackupConfigPayload, now time.Time) []api.BackupVerifyPayload {
	v := cfg.Verify
	if cfg.Repo == nil || v == nil {
		return nil
	}
	state := func(key string) svcState {
		st := m.vstate[key]
		if st == nil {
			st = &svcState{LastAttempt: now}
			m.vstate[key] = st
		}
		return *st
	}
	var out []api.BackupVerifyPayload
	if v.RunCheck && v.CheckSchedule != "" {
		slot := api.BackupTarget{Enabled: true, Schedule: v.CheckSchedule, Timezone: v.Timezone}
		if Due(slot, state(checkKey), now, m.RetryAfter, 1) {
			subset := v.CheckSubset
			if subset == "" {
				subset = api.DefaultBackupCheckSubset
			}
			out = append(out, api.BackupVerifyPayload{ID: newVerifyID(), Kind: api.BackupVerifyCheck, Trigger: "schedule", Repo: *cfg.Repo, Subset: subset})
		}
	}
	if v.DrillSchedule != "" {
		for _, t := range cfg.Targets {
			if !t.Enabled {
				continue
			}
			slot := api.BackupTarget{Enabled: true, Schedule: v.DrillSchedule, Timezone: v.Timezone}
			if Due(slot, state(drillKey(t.ServiceID)), now, m.RetryAfter, 1) {
				tc := t
				out = append(out, api.BackupVerifyPayload{ID: newVerifyID(), Kind: api.BackupVerifyDrill, Trigger: "schedule", Repo: *cfg.Repo, Target: &tc})
			}
		}
	}
	return out
}
