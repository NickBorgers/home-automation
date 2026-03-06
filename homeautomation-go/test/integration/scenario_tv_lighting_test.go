package integration

import (
	"context"
	"testing"

	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/plugins/tv"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// TV + Lighting Cross-Plugin Integration Tests
//
// These tests validate cross-plugin interactions between the TV and lighting
// plugins. The key invariant tested here:
//
//   When the Hue Sync Box recovers from a crash (unavailable → on), the TV
//   plugin calls ForceNotifyBool("isTVPlaying") so the lighting plugin
//   re-applies the correct scene — even if isTVPlaying value did not change.
//
// Production incident (2026-01-XX):
//   Sync box crashed → HA reported "unavailable" → isTVPlaying set to false
//   → Sync box recovered → Hue entertainment area reconnected & overrode lights
//   → No isTVPlaying state change → lighting plugin never re-applied scene
//   → Result: lights stuck in wrong state
//
// Fix: TV plugin calls ForceNotifyBool("isTVPlaying") after recovery, which
// sends (value, value) to subscribers. The lighting plugin's handleTVStateChange
// must NOT filter old==new — it must unconditionally call evaluateAllRooms().
//
// This test verifies that end-to-end invariant holds.
// ============================================================================

// tvLightingTestEnv holds the TV and lighting plugins for cross-plugin testing
type tvLightingTestEnv struct {
	server       *MockHAServer
	stateManager *state.Manager
	tvMgr        *tv.Manager
	lightingMgr  *lighting.Manager
}

// setupTVLightingTest creates a test environment with TV + lighting plugins
func setupTVLightingTest(t *testing.T) (*tvLightingTestEnv, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)
	logger := testlogger.New()
	lightingConfig := loadTestLightingConfig(t)

	tvMgr := tv.NewManager(context.Background(), client, stateManager, logger, false, nil)
	lightingMgr := lighting.NewManager(context.Background(), client, stateManager, lightingConfig, logger, false, nil)

	require.NoError(t, tvMgr.Start())
	require.NoError(t, lightingMgr.Start())

	waitForProcessing(t, stateManager)

	env := &tvLightingTestEnv{
		server:       server,
		stateManager: stateManager,
		tvMgr:        tvMgr,
		lightingMgr:  lightingMgr,
	}

	cleanup := func() {
		tvMgr.Stop()
		lightingMgr.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// TestScenario_SyncBoxRecovery_LightingPluginRestoresScene validates that the
// lighting plugin re-applies the correct scene after sync box recovery, even
// when isTVPlaying value does not change (old==new ForceNotifyBool behavior).
//
// INVARIANT: The lighting plugin's isTVPlaying subscriber must NOT filter
// old==new notifications. ForceNotifyBool passes (value, value) to subscribers.
// If the handler had a guard like `if oldValue == newValue { return }`, the
// lighting restore would silently fail after every sync box crash.
func TestScenario_SyncBoxRecovery_LightingPluginRestoresScene(t *testing.T) {
	t.Parallel()
	env, cleanup := setupTVLightingTest(t)
	defer cleanup()

	t.Log("========== TEST: Sync Box Recovery - Lighting Plugin Restores Scene ==========")
	t.Log("INVARIANT: lighting plugin must react to ForceNotifyBool(isTVPlaying) where old==new")

	// ========== GIVEN ==========
	t.Log("GIVEN: Sync box crashes (unavailable), isTVPlaying=false, someone is home and awake")

	require.NoError(t, env.stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, env.stateManager.SetBool("isEveryoneAsleep", false))
	require.NoError(t, env.stateManager.SetString("dayPhase", "evening"))

	// Set up required entities so the TV plugin can recalculate state on recovery.
	// Without these, GetState returns an error and the recovery handler exits early
	// before reaching ForceNotifyBool. In production these entities always exist.
	env.server.SetState("select.sync_box_hdmi_input", "Xbox", map[string]interface{}{})
	env.server.SetState("remote.big_beautiful_oled", "off", map[string]interface{}{})

	// Simulate sync box crash: state transitions to "unavailable"
	// This sets isTVPlaying=false (which it already is by default)
	env.server.SetState("switch.sync_box_power", "unavailable", map[string]interface{}{})

	// Wait for TV plugin to process the unavailable state
	waitForBoolState(t, env.stateManager, "isTVon", false, "isTVon should be false when sync box is unavailable")

	// Confirm isTVPlaying is false (set by the TV plugin during unavailable handling)
	isTVPlaying, err := env.stateManager.GetBool("isTVPlaying")
	require.NoError(t, err)
	require.False(t, isTVPlaying, "isTVPlaying should be false when sync box is unavailable")

	// Wait for all initial lighting reactions to complete, then take a snapshot
	waitForProcessing(t, env.stateManager)
	snapshot := env.server.ServiceCallCount()

	// ========== WHEN ==========
	t.Log("WHEN: Sync box recovers (unavailable → on)")
	t.Log("      The Hue entertainment area reconnects and overrides light states.")
	t.Log("      TV plugin calls ForceNotifyBool(isTVPlaying) — value is still false.")
	t.Log("      Lighting plugin must receive the notification and re-apply the scene.")

	// Simulate sync box recovery: unavailable → on
	// This triggers handleSyncBoxPowerChange with oldState.State=="unavailable",
	// which calls ForceNotifyBool("isTVPlaying") after recalculating TV state.
	env.server.SetState("switch.sync_box_power", "on", map[string]interface{}{})

	// ========== THEN ==========
	t.Log("THEN: Lighting plugin reacts to the force-notify and re-applies the correct scene")
	t.Log("      (scene.turn_on called for at least one room — confirming evaluateAllRooms fired)")

	// The lighting plugin must fire scene activations in response to the force-notify.
	// With isAnyoneHomeAndAwake=true and isTVPlaying=false (unchanged), the Living Room
	// condition "isTVPlaying=false → ON" should cause a scene.turn_on call.
	waitForServiceCallSince(t, env.server, snapshot, "scene", "turn_on",
		"Lighting plugin must re-apply scene after sync box recovery force-notify. "+
			"If this fails, the lighting plugin is filtering old==new notifications, "+
			"which breaks the sync box recovery fix.")

	calls := env.server.GetServiceCallsSince(snapshot)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")

	t.Logf("Scene activations after sync box recovery: %d", len(sceneActivations))
	assert.Greater(t, len(sceneActivations), 0,
		"CRITICAL: Lighting plugin must call scene.turn_on after sync box recovery. "+
			"ForceNotifyBool sends old==new to subscribers; the lighting plugin's "+
			"handleTVStateChange must NOT skip these notifications.")

	// Confirm isTVPlaying is still false — the value did NOT change.
	// This confirms the scene activation was triggered by the force-notify, not a value change.
	isTVPlaying, err = env.stateManager.GetBool("isTVPlaying")
	require.NoError(t, err)
	assert.False(t, isTVPlaying,
		"isTVPlaying should remain false — the scene activation was triggered by ForceNotifyBool, not a value change")

	t.Log("✓ Lighting plugin correctly responds to ForceNotifyBool(isTVPlaying) where old==new")
	t.Log("✓ Sync box recovery correctly restores lighting scenes")
}
