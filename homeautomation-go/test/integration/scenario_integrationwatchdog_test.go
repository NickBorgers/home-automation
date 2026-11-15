package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/plugins/integrationwatchdog"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

// ============================================================================
// Integration Watchdog Plugin Scenario Tests
//
// Validates the watchdog detects entity staleness and triggers a
// homeassistant config_entries/reload call against the mock HA server.
// ============================================================================

// setupWatchdogScenarioTest creates a test environment with the integration
// watchdog plugin started against an in-memory mock HA server. The returned
// cleanup stops the plugin and the server.
func setupWatchdogScenarioTest(t *testing.T, cfg *integrationwatchdog.Config) (*MockHAServer, *integrationwatchdog.Manager, func()) {
	t.Helper()
	server, client, stateMgr, baseCleanup := setupTest(t)

	logger := testlogger.New()

	mgr := integrationwatchdog.NewManager(context.Background(), client, stateMgr, cfg, logger, false, nil)

	cleanup := func() {
		mgr.Stop()
		baseCleanup()
	}
	return server, mgr, cleanup
}

// makeImmediateCfg builds a config with a single target watching `entity` that
// fires immediately on any "unknown" or "unavailable" reading.
func makeImmediateCfg(entity string) *integrationwatchdog.Config {
	return &integrationwatchdog.Config{
		CheckIntervalSec: 1,
		WatchTargets: []integrationwatchdog.WatchTarget{{
			Name:            "test_target",
			IntegrationName: "Test Integration",
			Entities:        []string{entity},
			ConfigEntryID:   "test_entry_id",
			Detection: integrationwatchdog.DetectionRule{
				BadStates:           []string{"unknown", "unavailable"},
				BadStateDurationMin: 0, // fire on first bad observation
				StaleLastUpdatedMin: 0,
			},
			Reload: integrationwatchdog.ReloadPolicy{
				CooldownMin:        5,
				DailyMax:           5,
				PostReloadDelaySec: 0,
			},
		}},
	}
}

// TestScenario_Watchdog_BadStateTriggersReload validates the GIVEN/WHEN/THEN
// regression scenario for Span Panel's failure mode:
//
//	GIVEN sensor.scenario_test_entity reports "unknown" (Span integration broken)
//	WHEN the watchdog observes that state
//	THEN the mock HA server receives a config_entries/reload for the right entry
func TestScenario_Watchdog_BadStateTriggersReload(t *testing.T) {
	t.Parallel()

	const entity = "sensor.scenario_test_entity"
	cfg := makeImmediateCfg(entity)
	server, mgr, cleanup := setupWatchdogScenarioTest(t, cfg)
	defer cleanup()

	// GIVEN: the watched entity is in a bad state before the plugin starts so
	// the initial subscribe-time GetState seeds the bad observation.
	t.Log("GIVEN: scenario_test_entity reports 'unknown' (Span-style staleness)")
	server.SetState(entity, "unknown", nil)

	require.NoError(t, mgr.Start(), "watchdog should start cleanly")

	// WHEN: the scan loop runs (1s interval — give it up to 5s to fire).
	t.Log("WHEN: watchdog scan loop evaluates the target")

	// THEN: a config_entries/reload arrives at the mock with the right entry_id.
	t.Log("THEN: mock HA server receives a config_entries/reload for test_entry_id")
	require.Eventually(t, func() bool {
		reloads := server.GetConfigEntryReloads()
		for _, r := range reloads {
			if r.EntryID == "test_entry_id" {
				return true
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "expected reload of test_entry_id within 5s")
}

// TestScenario_Watchdog_RecoveryClearsStaleness verifies the watchdog returns
// to a healthy state once the entity recovers — and does not issue an
// additional reload during the cooldown.
func TestScenario_Watchdog_RecoveryClearsStaleness(t *testing.T) {
	t.Parallel()

	const entity = "sensor.scenario_test_entity"
	cfg := makeImmediateCfg(entity)
	server, mgr, cleanup := setupWatchdogScenarioTest(t, cfg)
	defer cleanup()

	// GIVEN: entity is unknown when watchdog starts.
	server.SetState(entity, "unknown", nil)
	require.NoError(t, mgr.Start())

	// Wait for the first reload to land.
	require.Eventually(t, func() bool {
		return len(server.GetConfigEntryReloads()) >= 1
	}, 5*time.Second, 50*time.Millisecond, "expected first reload")

	reloadCountAfterFirst := len(server.GetConfigEntryReloads())

	// WHEN: the entity recovers (integration came back up).
	server.SetState(entity, "42.5", nil)

	// Give the watchdog a moment to observe & re-evaluate. We then verify the
	// streak was cleared via shadow state, AND that the reload count did NOT
	// grow during the cooldown window.
	require.Eventually(t, func() bool {
		s := mgr.GetShadowState()
		tgt, ok := s.Outputs.Targets["test_target"]
		return ok && !tgt.CurrentlyStale && tgt.EntityStates[entity] == "42.5"
	}, 5*time.Second, 50*time.Millisecond, "shadow state should report recovery")

	// THEN: no additional reloads during recovery + cooldown.
	require.Equal(t, reloadCountAfterFirst, len(server.GetConfigEntryReloads()),
		"watchdog should not re-fire after recovery; cooldown also prevents it")
}

// makeContradictionCfg builds a config like the Bravia target: the panel entity is watched,
// and "off" is not credible while the Apple TV is "playing".
func makeContradictionCfg(panel, appleTV string, durationMin int) *integrationwatchdog.Config {
	return &integrationwatchdog.Config{
		CheckIntervalSec: 1,
		WatchTargets: []integrationwatchdog.WatchTarget{{
			Name:            "test_target",
			IntegrationName: "Test Bravia",
			Entities:        []string{panel},
			ConfigEntryID:   "test_entry_id",
			Detection: integrationwatchdog.DetectionRule{
				Contradictions: []integrationwatchdog.Contradiction{{
					Entity:         panel,
					State:          "off",
					ContradictedBy: integrationwatchdog.EntityCondition{Entity: appleTV, State: "playing"},
					DurationMin:    durationMin,
				}},
			},
			Reload: integrationwatchdog.ReloadPolicy{
				CooldownMin:        5,
				DailyMax:           5,
				PostReloadDelaySec: 0,
			},
		}},
	}
}

// TestScenario_Watchdog_ContradictionTriggersReload covers the Bravia failure from production.
//
//	GIVEN the Bravia panel entity says "off" (frozen, never "unavailable")
//	WHEN the Apple TV plays, which cannot happen with the TV panel off
//	THEN the watchdog reloads the Bravia integration
//
// Invariant: a frozen integration that reports a plausible state is still caught when another
// source disproves it.
func TestScenario_Watchdog_ContradictionTriggersReload(t *testing.T) {
	t.Parallel()

	const panel = "media_player.scenario_panel"
	const appleTV = "media_player.scenario_apple_tv"
	server, mgr, cleanup := setupWatchdogScenarioTest(t, makeContradictionCfg(panel, appleTV, 0))
	defer cleanup()

	t.Log("GIVEN: the panel entity reports 'off' and the Apple TV reports 'idle'")
	server.SetState(panel, "off", nil)
	server.SetState(appleTV, "idle", nil)
	require.NoError(t, mgr.Start(), "watchdog should start cleanly")

	t.Log("WHEN: the Apple TV starts playing")
	server.SetState(appleTV, "playing", nil)

	t.Log("THEN: the mock HA server receives a config_entries/reload for test_entry_id")
	require.Eventually(t, func() bool {
		for _, r := range server.GetConfigEntryReloads() {
			if r.EntryID == "test_entry_id" {
				return true
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "expected a reload of test_entry_id within 5s")
}

// TestScenario_Watchdog_NoContradiction_NoReload checks that a healthy TV is left alone.
//
// Invariants:
//   - Panel "off" with an idle Apple TV is a normal evening. No reload.
//   - Panel "on" with a playing Apple TV is a normal movie. No reload.
func TestScenario_Watchdog_NoContradiction_NoReload(t *testing.T) {
	t.Parallel()

	const panel = "media_player.scenario_panel"
	const appleTV = "media_player.scenario_apple_tv"
	server, mgr, cleanup := setupWatchdogScenarioTest(t, makeContradictionCfg(panel, appleTV, 0))
	defer cleanup()

	t.Log("GIVEN: the panel is off and the Apple TV is idle")
	server.SetState(panel, "off", nil)
	server.SetState(appleTV, "idle", nil)
	require.NoError(t, mgr.Start(), "watchdog should start cleanly")

	t.Log("WHEN: the TV turns on and the Apple TV plays")
	server.SetState(panel, "on", nil)
	server.SetState(appleTV, "playing", nil)

	t.Log("THEN: no reload happens across several scan intervals")
	require.Never(t, func() bool {
		return len(server.GetConfigEntryReloads()) > 0
	}, 3*time.Second, 100*time.Millisecond, "a healthy TV must not be reloaded")
}

// TestScenario_Watchdog_ContradictionWaitsForDuration checks the dwell time.
// Briefly overlapping states happen when someone turns the TV off during a show. The Apple TV
// needs a moment to notice.
//
// Invariant: no reload until the contradiction has lasted duration_min.
func TestScenario_Watchdog_ContradictionWaitsForDuration(t *testing.T) {
	t.Parallel()

	const panel = "media_player.scenario_panel"
	const appleTV = "media_player.scenario_apple_tv"
	server, mgr, cleanup := setupWatchdogScenarioTest(t, makeContradictionCfg(panel, appleTV, 5))
	defer cleanup()

	t.Log("GIVEN: the panel is off and the Apple TV is playing, with a 5 minute dwell time")
	server.SetState(panel, "off", nil)
	server.SetState(appleTV, "playing", nil)
	require.NoError(t, mgr.Start(), "watchdog should start cleanly")

	t.Log("THEN: no reload happens in the first seconds")
	require.Never(t, func() bool {
		return len(server.GetConfigEntryReloads()) > 0
	}, 3*time.Second, 100*time.Millisecond, "the watchdog must wait out duration_min")
}
