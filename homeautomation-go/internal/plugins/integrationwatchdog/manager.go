package integrationwatchdog

import (
	"context"
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// targetState holds per-target mutable bookkeeping that doesn't belong in
// shadow state (the runtime detection cursor + the rolling-window counters).
type targetState struct {
	mu sync.Mutex

	cfg WatchTarget

	// entityStates is the latest observed state per watched entity.
	entityStates map[string]string

	// entityLastUpdated is the most recent last_updated timestamp per entity.
	entityLastUpdated map[string]time.Time

	// firstBadStateAt is when the current bad-state streak began. Zero when
	// none of the entities are currently in a bad state.
	firstBadStateAt time.Time

	// lastReloadAt is when the most recent reload was attempted.
	lastReloadAt time.Time

	// dailyReloadCount is the count of reloads in the current 24-hour window.
	dailyReloadCount int

	// dailyResetAt is when the daily window started.
	dailyResetAt time.Time

	// reloadInProgress prevents overlapping reload goroutines for the same target.
	reloadInProgress bool

	// lastReloadFailed reflects the outcome of the most recent attempt.
	lastReloadFailed bool
}

// Manager is the integrationwatchdog plugin.
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	cfg          *Config

	shadowTracker *shadowstate.IntegrationWatchdogTracker
	subHelper     *shadowstate.SubscriptionHelper

	targets map[string]*targetState

	stopChan chan struct{}
	stopOnce sync.Once
	doneChan chan struct{}

	// reloadWG tracks in-flight reload goroutines so Stop() can drain them
	// before returning. Without this, Stop() returns while reload goroutines
	// are still sleeping in the post-reload delay and touching m.haClient /
	// ts.mu — benign at process shutdown, but a footgun for tests or any
	// future caller that tears down shared objects after Stop().
	reloadWG sync.WaitGroup

	// Injectable for tests.
	timeNow   func() time.Time
	sleepFunc func(time.Duration)
}

// NewManager constructs a new watchdog Manager. The registry argument is
// optional (may be nil in unit tests).
func NewManager(
	ctx context.Context,
	haClient ha.HAClient,
	stateManager *state.Manager,
	cfg *Config,
	logger *zap.Logger,
	readOnly bool,
	registry *shadowstate.SubscriptionRegistry,
) *Manager {
	tracker := shadowstate.NewIntegrationWatchdogTracker()

	targets := make(map[string]*targetState, len(cfg.WatchTargets))
	for i := range cfg.WatchTargets {
		t := cfg.WatchTargets[i]
		targets[t.Name] = &targetState{
			cfg:               t,
			entityStates:      make(map[string]string),
			entityLastUpdated: make(map[string]time.Time),
		}
		tracker.InitTarget(t.Name, t.IntegrationName, t.ConfigEntryID)
	}

	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("integrationwatchdog"),
		readOnly:      readOnly,
		cfg:           cfg,
		shadowTracker: tracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, tracker, "integrationwatchdog", logger.Named("integrationwatchdog")),
		targets:       targets,
		stopChan:      make(chan struct{}),
		doneChan:      make(chan struct{}),
		timeNow:       time.Now,
		sleepFunc:     time.Sleep,
	}
}

// GetShadowState returns the latest shadow state snapshot.
func (m *Manager) GetShadowState() *shadowstate.IntegrationWatchdogShadowState {
	return m.shadowTracker.GetState()
}

// Start sets up subscriptions, seeds initial state, and launches the scan loop.
func (m *Manager) Start() error {
	m.logger.Info("Starting integration watchdog",
		zap.Int("target_count", len(m.cfg.WatchTargets)),
		zap.Duration("check_interval", m.cfg.CheckInterval()))

	// Build a reverse index entity -> targets, then subscribe once per entity.
	entityToTargets := make(map[string][]string)
	for name, ts := range m.targets {
		for _, eid := range ts.cfg.Entities {
			entityToTargets[eid] = append(entityToTargets[eid], name)
		}
	}

	for entityID, targetNames := range entityToTargets {
		entityID := entityID       // capture
		targetNames := targetNames // capture
		handler := func(_ string, _, newState *ha.State) {
			if newState == nil {
				return
			}
			for _, name := range targetNames {
				m.recordEntityState(name, entityID, newState.State, newState.LastUpdated)
			}
		}
		if err := m.subHelper.SubscribeToEntity(entityID, handler); err != nil {
			return fmt.Errorf("subscribe to %s: %w", entityID, err)
		}
	}

	// Seed each entity from current HA state so detection has data immediately.
	for entityID, targetNames := range entityToTargets {
		st, err := m.haClient.GetState(entityID)
		if err != nil || st == nil {
			m.logger.Debug("Initial GetState failed for watched entity (will rely on subscription)",
				zap.String("entity_id", entityID),
				zap.Error(err))
			continue
		}
		for _, name := range targetNames {
			m.recordEntityState(name, entityID, st.State, st.LastUpdated)
		}
	}

	m.subHelper.CaptureInitialInputs()

	go m.runScanLoop()

	m.logger.Info("Integration watchdog started",
		zap.Int("subscribed_entities", len(entityToTargets)))
	return nil
}

// Stop tears down the scan loop and unsubscribes.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		m.logger.Info("Stopping integration watchdog")
		close(m.stopChan)
	})
	<-m.doneChan
	m.reloadWG.Wait()
	m.subHelper.UnsubscribeAll()
}

// runScanLoop scans all targets on a ticker.
func (m *Manager) runScanLoop() {
	defer close(m.doneChan)

	ticker := time.NewTicker(m.cfg.CheckInterval())
	defer ticker.Stop()

	// Run one immediate scan so first detection doesn't wait a full interval.
	m.scanAllTargets()

	for {
		select {
		case <-ticker.C:
			m.scanAllTargets()
		case <-m.stopChan:
			return
		case <-m.ctx.Done():
			return
		}
	}
}

func (m *Manager) scanAllTargets() {
	for name := range m.targets {
		m.evaluateTarget(name)
	}
}

// recordEntityState updates the cached state for one entity within one target.
// Bad-state streak start times are recomputed in evaluateTarget — this method
// only stores the inputs.
func (m *Manager) recordEntityState(targetName, entityID, state string, lastUpdated time.Time) {
	ts, ok := m.targets[targetName]
	if !ok {
		return
	}

	ts.mu.Lock()
	ts.entityStates[entityID] = state
	if !lastUpdated.IsZero() {
		ts.entityLastUpdated[entityID] = lastUpdated
	}
	ts.mu.Unlock()

	// Mirror into shadow state for observability.
	m.shadowTracker.UpdateEntityState(targetName, entityID, state, lastUpdated)

	// Opportunistically evaluate now so that a fast state transition can trigger
	// a reload without waiting for the next tick (the tick is the safety net).
	m.evaluateTarget(targetName)
}

// evaluateTarget runs the detection rules for one target and triggers a reload
// if appropriate.
func (m *Manager) evaluateTarget(name string) {
	ts, ok := m.targets[name]
	if !ok {
		return
	}

	now := m.timeNow()

	ts.mu.Lock()
	cfg := ts.cfg
	anyBad := false
	for _, eid := range cfg.Entities {
		st, present := ts.entityStates[eid]
		if !present {
			continue
		}
		if cfg.Detection.IsBadState(st) {
			anyBad = true
			break
		}
	}

	// Track / clear the bad-state streak.
	if anyBad {
		if ts.firstBadStateAt.IsZero() {
			ts.firstBadStateAt = now
		}
	} else {
		ts.firstBadStateAt = time.Time{}
	}

	// Determine staleness. The bad-state rule is enabled iff BadStates is
	// non-empty. BadStateDurationMin == 0 fires immediately.
	stale := false
	reason := ""
	if anyBad && len(cfg.Detection.BadStates) > 0 {
		if cfg.Detection.BadStateDurationMin == 0 {
			stale = true
			reason = "bad_state"
		} else if now.Sub(ts.firstBadStateAt) >= cfg.Detection.BadStateDuration() {
			stale = true
			reason = fmt.Sprintf("bad_state>=%dmin", cfg.Detection.BadStateDurationMin)
		}
	}
	if !stale && cfg.Detection.StaleLastUpdatedMin > 0 {
		// Use the most recent last_updated across the watched entities. If none
		// have been observed yet, the timestamp rule is skipped.
		var newest time.Time
		for _, t := range ts.entityLastUpdated {
			if t.After(newest) {
				newest = t
			}
		}
		if !newest.IsZero() && now.Sub(newest) >= cfg.Detection.StaleLastUpdatedDuration() {
			stale = true
			reason = fmt.Sprintf("last_updated>=%dmin", cfg.Detection.StaleLastUpdatedMin)
		}
	}

	firstBad := ts.firstBadStateAt
	ts.mu.Unlock()

	m.shadowTracker.UpdateStaleness(name, stale, firstBad)

	if !stale {
		return
	}

	// Trigger reload (async — caller may be a subscription handler we mustn't block).
	m.reloadWG.Add(1)
	go m.reloadIntegration(name, reason)
}

// reloadIntegration performs a reload attempt subject to cooldown, daily-cap,
// in-progress, and read-only safeguards.
func (m *Manager) reloadIntegration(name, trigger string) {
	defer m.reloadWG.Done()

	ts, ok := m.targets[name]
	if !ok {
		return
	}

	ts.mu.Lock()
	if ts.reloadInProgress {
		ts.mu.Unlock()
		return
	}
	ts.reloadInProgress = true
	cfg := ts.cfg
	ts.mu.Unlock()

	defer func() {
		ts.mu.Lock()
		ts.reloadInProgress = false
		ts.mu.Unlock()
	}()

	if !m.canAttemptReload(name) {
		return
	}

	if m.readOnly {
		m.logger.Info("Skipping integration reload in read-only mode",
			zap.String("target", name),
			zap.String("trigger", trigger),
			zap.String("entry_id", cfg.ConfigEntryID))
		return
	}

	now := m.timeNow()
	ts.mu.Lock()
	ts.lastReloadAt = now
	ts.dailyReloadCount++
	reloadCount := ts.dailyReloadCount
	resetAt := ts.dailyResetAt
	ts.mu.Unlock()

	m.shadowTracker.RecordReload(name, trigger, now, reloadCount, resetAt, false)

	m.logger.Warn("Reloading HA integration to recover from stale state",
		zap.String("target", name),
		zap.String("integration", cfg.IntegrationName),
		zap.String("trigger", trigger),
		zap.String("entry_id", cfg.ConfigEntryID),
		zap.Int("daily_reload_count", reloadCount))

	if err := m.haClient.ReloadConfigEntry(m.ctx, cfg.ConfigEntryID); err != nil {
		m.logger.Error("Integration reload failed",
			zap.String("target", name),
			zap.String("entry_id", cfg.ConfigEntryID),
			zap.Error(err))

		ts.mu.Lock()
		ts.lastReloadFailed = true
		ts.mu.Unlock()
		m.shadowTracker.RecordReload(name, trigger, now, reloadCount, resetAt, true)
		return
	}

	ts.mu.Lock()
	ts.lastReloadFailed = false
	ts.mu.Unlock()

	if d := cfg.Reload.PostReloadDelay(); d > 0 {
		m.sleepFunc(d)
	}

	m.logger.Info("Integration reload completed",
		zap.String("target", name),
		zap.String("entry_id", cfg.ConfigEntryID))

	// Re-check entity states; on success the integration usually publishes
	// fresh values, clearing the bad-state streak on the next tick anyway, but
	// we proactively refresh to update shadow state quickly.
	for _, eid := range cfg.Entities {
		st, err := m.haClient.GetState(eid)
		if err != nil || st == nil {
			continue
		}
		m.recordEntityStateNoEval(name, eid, st.State, st.LastUpdated)
	}
}

// recordEntityStateNoEval is recordEntityState without triggering a re-eval —
// used after a reload to avoid recursive reload attempts while waiting for the
// cooldown to govern the next attempt.
func (m *Manager) recordEntityStateNoEval(targetName, entityID, state string, lastUpdated time.Time) {
	ts, ok := m.targets[targetName]
	if !ok {
		return
	}
	ts.mu.Lock()
	ts.entityStates[entityID] = state
	if !lastUpdated.IsZero() {
		ts.entityLastUpdated[entityID] = lastUpdated
	}
	ts.mu.Unlock()
	m.shadowTracker.UpdateEntityState(targetName, entityID, state, lastUpdated)
}

// canAttemptReload enforces the cooldown and daily-cap policies.
func (m *Manager) canAttemptReload(name string) bool {
	ts, ok := m.targets[name]
	if !ok {
		return false
	}

	now := m.timeNow()

	ts.mu.Lock()
	defer ts.mu.Unlock()

	if !ts.dailyResetAt.IsZero() && now.Sub(ts.dailyResetAt) > 24*time.Hour {
		ts.dailyReloadCount = 0
		ts.dailyResetAt = now
	} else if ts.dailyResetAt.IsZero() {
		ts.dailyResetAt = now
	}

	if ts.dailyReloadCount >= ts.cfg.Reload.DailyMax {
		m.logger.Error("Daily reload cap reached, skipping",
			zap.String("target", name),
			zap.Int("daily_count", ts.dailyReloadCount),
			zap.Int("max_allowed", ts.cfg.Reload.DailyMax))
		return false
	}

	if !ts.lastReloadAt.IsZero() && now.Sub(ts.lastReloadAt) < ts.cfg.Reload.Cooldown() {
		m.logger.Warn("Reload in cooldown period",
			zap.String("target", name),
			zap.Duration("time_since_last", now.Sub(ts.lastReloadAt)),
			zap.Duration("cooldown_required", ts.cfg.Reload.Cooldown()))
		return false
	}

	return true
}
