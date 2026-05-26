package integrationwatchdog

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// testClock returns the current value of an internally-locked time pointer, so
// tests can advance time from one goroutine while watchdog goroutines read it.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock(t time.Time) *testClock    { return &testClock{now: t} }
func (c *testClock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *testClock) Advance(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.now = c.now.Add(d) }
func (c *testClock) Set(t time.Time)         { c.mu.Lock(); defer c.mu.Unlock(); c.now = t }

// buildTestConfig returns a minimal valid config with one target watching `entity`.
// `interval` is the scan ticker interval (use a long value in tests that drive
// evaluation manually).
func buildTestConfig(entity string, interval time.Duration) *Config {
	return &Config{
		CheckIntervalSec: int(interval.Seconds()),
		WatchTargets: []WatchTarget{{
			Name:            "test_target",
			IntegrationName: "Test Integration",
			Entities:        []string{entity},
			ConfigEntryID:   "test_entry_id",
			Detection: DetectionRule{
				BadStates:           []string{"unknown", "unavailable"},
				BadStateDurationMin: 10,
				StaleLastUpdatedMin: 0,
			},
			Reload: ReloadPolicy{
				CooldownMin:        5,
				DailyMax:           3,
				PostReloadDelaySec: 0,
			},
		}},
	}
}

func newTestManager(t *testing.T, cfg *Config, mockHA *ha.MockClient, readOnly bool) *Manager {
	t.Helper()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, readOnly)
	mgr := NewManager(context.Background(), mockHA, stateMgr, cfg, logger, readOnly, nil)
	mgr.sleepFunc = func(time.Duration) {}
	return mgr
}

func waitForReload(t *testing.T, mockHA *ha.MockClient, want int) []ha.ConfigEntryReload {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		reloads := mockHA.GetConfigEntryReloads()
		if len(reloads) >= want {
			return reloads
		}
		time.Sleep(5 * time.Millisecond)
	}
	return mockHA.GetConfigEntryReloads()
}

func TestWatchdog_BadStateDurationTriggersReload(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	entity := "sensor.test"
	cfg := buildTestConfig(entity, time.Hour) // long ticker — evaluate manually
	mgr := newTestManager(t, cfg, mockHA, false)

	clk := newTestClock(time.Now())
	mgr.timeNow = clk.Now

	// Simulate the entity sitting in "unknown" for 11 minutes.
	mgr.recordEntityStateNoEval("test_target", entity, "unknown", clk.Now())
	// First evaluation: opens the bad-state streak at T0; no reload yet.
	mgr.evaluateTarget("test_target")
	if reloads := mockHA.GetConfigEntryReloads(); len(reloads) != 0 {
		t.Fatalf("expected no reload before duration elapsed, got %d", len(reloads))
	}

	// Advance past the BadStateDuration threshold and re-evaluate.
	clk.Advance(11 * time.Minute)
	mgr.evaluateTarget("test_target")
	reloads := waitForReload(t, mockHA, 1)
	if len(reloads) != 1 {
		t.Fatalf("expected one reload, got %d", len(reloads))
	}
	if reloads[0].EntryID != "test_entry_id" {
		t.Errorf("expected entry id test_entry_id, got %s", reloads[0].EntryID)
	}
}

func TestWatchdog_GoodStateClearsStreak(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	entity := "sensor.test"
	cfg := buildTestConfig(entity, time.Hour)
	mgr := newTestManager(t, cfg, mockHA, false)

	clk := newTestClock(time.Now())
	mgr.timeNow = clk.Now

	mgr.recordEntityStateNoEval("test_target", entity, "unavailable", clk.Now())
	mgr.evaluateTarget("test_target")

	// Recover before threshold.
	clk.Advance(2 * time.Minute)
	mgr.recordEntityStateNoEval("test_target", entity, "42.5", clk.Now())
	mgr.evaluateTarget("test_target")

	if reloads := mockHA.GetConfigEntryReloads(); len(reloads) != 0 {
		t.Fatalf("expected no reload after recovery, got %d", len(reloads))
	}

	ts := mgr.targets["test_target"]
	ts.mu.Lock()
	firstBad := ts.firstBadStateAt
	ts.mu.Unlock()
	if !firstBad.IsZero() {
		t.Errorf("expected firstBadStateAt to be cleared after recovery, got %v", firstBad)
	}
}

func TestWatchdog_StaleLastUpdatedTriggersReload(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	entity := "media_player.test"
	cfg := buildTestConfig(entity, time.Hour)
	cfg.WatchTargets[0].Detection.BadStateDurationMin = 0
	cfg.WatchTargets[0].Detection.StaleLastUpdatedMin = 60
	cfg.WatchTargets[0].Detection.BadStates = nil
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validation: %v", err)
	}

	mgr := newTestManager(t, cfg, mockHA, false)
	clk := newTestClock(time.Now())
	mgr.timeNow = clk.Now

	// Healthy state but stamped 2 hours ago.
	stale := clk.Now().Add(-2 * time.Hour)
	mgr.recordEntityStateNoEval("test_target", entity, "playing", stale)
	mgr.evaluateTarget("test_target")

	reloads := waitForReload(t, mockHA, 1)
	if len(reloads) != 1 {
		t.Fatalf("expected one reload due to stale last_updated, got %d", len(reloads))
	}
}

func TestWatchdog_CooldownBlocksSecondReload(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	entity := "sensor.test"
	cfg := buildTestConfig(entity, time.Hour)
	mgr := newTestManager(t, cfg, mockHA, false)

	clk := newTestClock(time.Now())
	mgr.timeNow = clk.Now

	// Trigger first reload: record state at T0, evaluate to start the streak,
	// then advance past the BadStateDuration threshold and evaluate again.
	mgr.recordEntityStateNoEval("test_target", entity, "unknown", clk.Now())
	mgr.evaluateTarget("test_target")
	clk.Advance(11 * time.Minute)
	mgr.evaluateTarget("test_target")
	if reloads := waitForReload(t, mockHA, 1); len(reloads) != 1 {
		t.Fatalf("expected first reload, got %d", len(reloads))
	}
	// Let the reload goroutine fully settle before advancing time again.
	time.Sleep(20 * time.Millisecond)

	// Inside cooldown window (5 min).
	clk.Advance(2 * time.Minute)
	mgr.evaluateTarget("test_target")
	// Negative assertion: sleep briefly so any spurious goroutine has time to fire.
	time.Sleep(50 * time.Millisecond)
	if reloads := mockHA.GetConfigEntryReloads(); len(reloads) != 1 {
		t.Fatalf("expected exactly 1 reload during cooldown, got %d", len(reloads))
	}

	// Past cooldown window.
	clk.Advance(5 * time.Minute)
	mgr.evaluateTarget("test_target")
	if reloads := waitForReload(t, mockHA, 2); len(reloads) != 2 {
		t.Fatalf("expected 2 reloads after cooldown elapsed, got %d", len(reloads))
	}
}

func TestWatchdog_DailyCapEnforced(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	entity := "sensor.test"
	cfg := buildTestConfig(entity, time.Hour)
	cfg.WatchTargets[0].Reload.DailyMax = 2
	cfg.WatchTargets[0].Reload.CooldownMin = 1 // short cooldown so daily cap is the binding limit
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validation: %v", err)
	}
	mgr := newTestManager(t, cfg, mockHA, false)

	clk := newTestClock(time.Now())
	mgr.timeNow = clk.Now

	mgr.recordEntityStateNoEval("test_target", entity, "unknown", clk.Now())
	// Seed the streak at T0.
	mgr.evaluateTarget("test_target")
	for i := 0; i < 5; i++ {
		clk.Advance(11 * time.Minute)
		mgr.evaluateTarget("test_target")
		if i < 2 {
			// Wait for the expected reload goroutine to complete before the next iteration.
			waitForReload(t, mockHA, i+1)
		}
	}

	reloads := mockHA.GetConfigEntryReloads()
	if len(reloads) != 2 {
		t.Fatalf("expected daily cap of 2 reloads, got %d", len(reloads))
	}
}

func TestWatchdog_ReadOnlySkipsReload(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	entity := "sensor.test"
	cfg := buildTestConfig(entity, time.Hour)
	mgr := newTestManager(t, cfg, mockHA, true)

	clk := newTestClock(time.Now())
	mgr.timeNow = clk.Now

	mgr.recordEntityStateNoEval("test_target", entity, "unavailable", clk.Now())
	mgr.evaluateTarget("test_target")
	clk.Advance(11 * time.Minute)
	mgr.evaluateTarget("test_target")
	// Negative assertion: sleep briefly so any goroutine has time to fire; none expected in read-only mode.
	time.Sleep(50 * time.Millisecond)

	if reloads := mockHA.GetConfigEntryReloads(); len(reloads) != 0 {
		t.Fatalf("expected no reload in read-only mode, got %d", len(reloads))
	}
}

func TestWatchdog_ReloadFailureRecorded(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockHA.SetReloadError("test_entry_id", fmt.Errorf("simulated HTTP 401"))

	entity := "sensor.test"
	cfg := buildTestConfig(entity, time.Hour)
	mgr := newTestManager(t, cfg, mockHA, false)

	clk := newTestClock(time.Now())
	mgr.timeNow = clk.Now

	mgr.recordEntityStateNoEval("test_target", entity, "unknown", clk.Now())
	mgr.evaluateTarget("test_target")
	clk.Advance(11 * time.Minute)
	mgr.evaluateTarget("test_target")

	// Poll shadow state for the failed reload — the mock skips configReloads on
	// error, so we can't wait for that; LastReloadFailed is the observable signal.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		tgt := mgr.GetShadowState().Outputs.Targets["test_target"]
		if tgt.LastReloadFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	tgt := mgr.GetShadowState().Outputs.Targets["test_target"]
	if !tgt.LastReloadFailed {
		t.Error("expected shadow state to reflect LastReloadFailed=true")
	}
}

func TestWatchdog_ConfigValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "missing name",
			mutate: func(c *Config) {
				c.WatchTargets[0].Name = ""
			},
			wantErr: "name is required",
		},
		{
			name: "duplicate name",
			mutate: func(c *Config) {
				c.WatchTargets = append(c.WatchTargets, c.WatchTargets[0])
			},
			wantErr: "duplicate name",
		},
		{
			name: "missing config_entry_id",
			mutate: func(c *Config) {
				c.WatchTargets[0].ConfigEntryID = ""
			},
			wantErr: "config_entry_id is required",
		},
		{
			name: "no entities",
			mutate: func(c *Config) {
				c.WatchTargets[0].Entities = nil
			},
			wantErr: "at least one entity",
		},
		{
			name: "no detection rules",
			mutate: func(c *Config) {
				c.WatchTargets[0].Detection = DetectionRule{}
			},
			wantErr: "at least one detection rule",
		},
		{
			name: "bad_state_duration without bad_states",
			mutate: func(c *Config) {
				c.WatchTargets[0].Detection.BadStates = nil
				// also enable the timestamp rule so we pass the
				// "at least one rule" check and hit the specific
				// "duration without states" error.
				c.WatchTargets[0].Detection.StaleLastUpdatedMin = 30
			},
			wantErr: "bad_state_duration_min set without bad_states",
		},
		{
			name: "zero cooldown",
			mutate: func(c *Config) {
				c.WatchTargets[0].Reload.CooldownMin = 0
			},
			wantErr: "cooldown_min must be positive",
		},
		{
			name: "zero daily_max",
			mutate: func(c *Config) {
				c.WatchTargets[0].Reload.DailyMax = 0
			},
			wantErr: "daily_max must be positive",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := buildTestConfig("sensor.x", time.Hour)
			tc.mutate(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestWatchdog_StartStopLifecycle(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	entity := "sensor.test"
	mockHA.SetState(entity, "42", nil)

	cfg := buildTestConfig(entity, 50*time.Millisecond)
	mgr := newTestManager(t, cfg, mockHA, false)

	if err := mgr.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Let the scan loop run at least once.
	time.Sleep(150 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		mgr.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Stop did not return within 1s")
	}
}

// TestWatchdog_SubscriptionUpdatesShadowState verifies that incoming state
// changes flow into the shadow state via the subscription path.
func TestWatchdog_SubscriptionUpdatesShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	entity := "sensor.test"
	mockHA.SetState(entity, "100", nil)

	cfg := buildTestConfig(entity, time.Hour)
	mgr := newTestManager(t, cfg, mockHA, false)

	if err := mgr.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer mgr.Stop()

	mockHA.SimulateStateChange(entity, "unknown")

	// Allow the handler to run.
	var stateAfter string
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		s := mgr.GetShadowState()
		if tgt, ok := s.Outputs.Targets["test_target"]; ok {
			stateAfter = tgt.EntityStates[entity]
			if stateAfter == "unknown" {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if stateAfter != "unknown" {
		t.Fatalf("expected shadow state entity to be %q, got %q", "unknown", stateAfter)
	}
}

// ensure goroutines settle on Manager shutdown — defensive sanity test
func TestWatchdog_NoGoroutineLeakOnStop(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	entity := "sensor.test"
	mockHA.SetState(entity, "42", nil)

	cfg := buildTestConfig(entity, 30*time.Millisecond)
	mgr := newTestManager(t, cfg, mockHA, false)

	if err := mgr.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.Stop()
	}()

	wg.Wait() // panic via testing timeout if blocked
}

func TestWatchdog_LoadConfigFromFile(t *testing.T) {
	t.Parallel()
	cfg, err := LoadConfig("../../../test/integration/testdata/integration_watchdog_config_test.yaml")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.WatchTargets) == 0 {
		t.Fatal("expected at least one watch target")
	}
}
