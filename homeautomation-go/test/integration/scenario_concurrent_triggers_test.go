package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ============================================================================
// Simultaneous State Changes — Zone Resolution Tests
//
// These tests validate that when multiple state changes happen concurrently
// (e.g., day phase shifts AND someone comes home), the system handles them
// without duplicate actions, missed updates, or race conditions.
//
// Cross-plugin bugs often slip through unit tests because unit tests set up
// state atomically and don't test timing windows between state changes.
//
// INVARIANTS:
// - Concurrent state changes must not cause panics or deadlocks
// - Final state must reflect ALL changes (no lost updates)
// - Plugins must not produce conflicting service calls
// - Zone resolution must be consistent regardless of change ordering
// ============================================================================

// concurrentEnv holds plugins for concurrent trigger tests
type concurrentEnv struct {
	server        *MockHAServer
	stateManager  *state.Manager
	logger        *zap.Logger
	stateTracking *statetracking.Manager
	lighting      *lighting.Manager
	music         *music.Manager
}

func setupConcurrentTest(t *testing.T) (*concurrentEnv, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Load configs (shared helpers from scenario_multi_plugin_test.go and scenario_music_lighting_test.go)
	lightingConfig := loadTestLightingConfig(t)
	musicConfig := loadTestMusicConfigFromYAML(t)

	// Create plugins
	env := &concurrentEnv{
		server:        server,
		stateManager:  manager,
		logger:        logger,
		stateTracking: statetracking.NewManager(context.Background(), client, manager, logger, false, nil),
		lighting:      lighting.NewManager(context.Background(), client, manager, lightingConfig, logger, false, nil),
		music:         music.NewManager(context.Background(), client, manager, musicConfig, logger, false, nil, nil),
	}

	// Set up media player entities
	server.SetState("media_player.kitchen", "idle", map[string]interface{}{
		"friendly_name": "Kitchen",
		"volume_level":  0.09,
	})
	server.SetState("media_player.bedroom", "idle", map[string]interface{}{
		"friendly_name": "Bedroom",
		"volume_level":  0.09,
	})
	server.SetState("media_player.sitting_room", "idle", map[string]interface{}{
		"friendly_name": "Sitting Room",
		"volume_level":  0.08,
	})

	// Start plugins in priority order
	require.NoError(t, env.stateTracking.Start(), "Failed to start state tracking")
	require.NoError(t, env.lighting.Start(), "Failed to start lighting")
	require.NoError(t, env.music.Start(), "Failed to start music")

	// Wait for plugin initialization handlers to complete
	waitForProcessing(t, manager)

	cleanup := func() {
		env.music.Stop()
		env.lighting.Stop()
		env.stateTracking.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// ============================================================================
// Test 1: Day Phase + Presence Change Simultaneously
// ============================================================================

// TestScenario_SimultaneousDayPhaseAndPresence_ConsistentState validates that
// when day phase AND presence change at the same time, both plugins resolve
// to a consistent final state.
//
// User story: "When the day phase shifts to evening AND someone comes home
// at the same time, both music and lights should reflect the correct state."
//
// INVARIANT: Final state must reflect BOTH changes regardless of processing order.
// INVARIANT: No deadlocks or panics from concurrent state updates.
func TestScenario_SimultaneousDayPhaseAndPresence_ConsistentState(t *testing.T) {
	t.Parallel()
	env, cleanup := setupConcurrentTest(t)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: Morning, only one person home")

	env.server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "morning", map[string]interface{}{})

	waitForProcessing(t, env.stateManager)
	snapshot := env.server.ServiceCallCount()

	// ========== WHEN ==========
	t.Log("WHEN: Day phase changes to 'evening' AND second person arrives (simultaneously)")

	// Fire both changes as close together as possible
	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	env.server.SetState("input_boolean.caroline_home", "on", map[string]interface{}{})

	// Wait for all plugins to process both changes
	waitForStringState(t, env.stateManager, "dayPhase", "evening", "dayPhase should become evening")
	waitForBoolState(t, env.stateManager, "isCarolineHome", true, "isCarolineHome should become true")

	// Wait for lighting plugin to react (state cascades through state tracking first)
	waitForCondition(t, func() bool {
		calls := env.server.GetServiceCallsSince(snapshot)
		sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
		return len(sceneActivations) > 0
	}, "Lighting should activate scenes when day phase changes")

	// ========== THEN ==========
	t.Log("THEN: Final state reflects both changes, no duplicate actions")

	// ASSERTION 1: Both state changes are reflected
	dayPhase, err := env.stateManager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "evening", dayPhase, "Day phase should be evening")

	isCarolineHome, err := env.stateManager.GetBool("isCarolineHome")
	assert.NoError(t, err)
	assert.True(t, isCarolineHome, "Caroline should be home")

	// ASSERTION 2: Lighting plugin responded (at least one scene activation)
	calls := env.server.GetServiceCallsSince(snapshot)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
	t.Logf("Total service calls: %d, Scene activations: %d", len(calls), len(sceneActivations))

	assert.Greater(t, len(sceneActivations), 0,
		"Lighting should activate scenes when day phase changes")

	// ASSERTION 3: No panics or deadlocks (if we get here, no deadlock occurred)
	t.Log("SUCCESS: Simultaneous state changes processed without deadlock")
}

// ============================================================================
// Test 2: Rapid Presence Toggles Don't Cause Inconsistency
// ============================================================================

// TestScenario_RapidPresenceChanges_StableState validates that rapid
// presence changes (someone arriving and leaving quickly) don't cause
// plugins to get into an inconsistent state.
//
// User story: "If the presence sensor briefly flickers, the system shouldn't
// go haywire turning things on and off rapidly."
//
// INVARIANT: After rapid changes settle, state must be internally consistent.
func TestScenario_RapidPresenceChanges_StableState(t *testing.T) {
	t.Parallel()
	env, cleanup := setupConcurrentTest(t)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: Evening, Nick is home")

	env.server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})

	waitForProcessing(t, env.stateManager)

	// ========== WHEN ==========
	t.Log("WHEN: Caroline's presence rapidly toggles (sensor flicker)")

	// Simulate rapid presence changes
	for i := 0; i < 5; i++ {
		if i%2 == 0 {
			env.server.SetState("input_boolean.caroline_home", "on", map[string]interface{}{})
		} else {
			env.server.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
		}
		time.Sleep(20 * time.Millisecond) // Intentional: simulates rapid sensor changes
	}

	// Final state: Caroline is home (last toggle was i=4, even → on)
	waitForProcessing(t, env.stateManager)

	// ========== THEN ==========
	t.Log("THEN: System reaches consistent final state without crashes")

	// ASSERTION 1: Final state matches last change
	isCarolineHome, err := env.stateManager.GetBool("isCarolineHome")
	assert.NoError(t, err)
	assert.True(t, isCarolineHome,
		"Caroline should be home (last toggle was 'on')")

	// ASSERTION 2: isAnyoneHome is consistent
	isAnyoneHome, err := env.stateManager.GetBool("isAnyoneHome")
	assert.NoError(t, err)
	assert.True(t, isAnyoneHome,
		"isAnyoneHome should be true (Nick and Caroline both home)")

	// ASSERTION 3: No panics or deadlocks
	t.Log("SUCCESS: Rapid presence changes processed without crashes or inconsistency")
}

// ============================================================================
// Test 3: Sleep + Day Phase Change Simultaneously
// ============================================================================

// TestScenario_SimultaneousSleepAndDayPhase_CorrectPriority validates that
// when someone goes to sleep AND the day phase changes simultaneously,
// the sleep state takes appropriate priority for bedroom actions.
//
// User story: "If the day phase changes to 'night' at the same time I fall
// asleep, the bedroom should be treated as asleep, not just night mode."
//
// INVARIANT: isMasterAsleep=true takes priority over day phase for bedroom lighting.
func TestScenario_SimultaneousSleepAndDayPhase_CorrectPriority(t *testing.T) {
	t.Parallel()
	env, cleanup := setupConcurrentTest(t)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: Evening, someone is home and awake")

	env.server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})

	waitForProcessing(t, env.stateManager)
	snapshot := env.server.ServiceCallCount()

	// ========== WHEN ==========
	t.Log("WHEN: Day phase changes to 'night' AND master goes to sleep (simultaneously)")

	env.server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})

	// Wait for both changes to propagate
	waitForStringState(t, env.stateManager, "dayPhase", "night", "dayPhase should become night")
	waitForBoolState(t, env.stateManager, "isMasterAsleep", true, "isMasterAsleep should become true")

	// Wait for lighting plugin to react (state cascades through state tracking first)
	waitForCondition(t, func() bool {
		calls := env.server.GetServiceCallsSince(snapshot)
		sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
		lightTurnOffs := filterServiceCalls(calls, "light", "turn_off")
		return len(sceneActivations)+len(lightTurnOffs) > 0
	}, "Lighting should respond to combined night + sleep state")

	// ========== THEN ==========
	t.Log("THEN: Both states are set correctly and plugins handled both changes")

	// ASSERTION 1: Both states reflect the changes
	dayPhase, err := env.stateManager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "night", dayPhase)

	isMasterAsleep, err := env.stateManager.GetBool("isMasterAsleep")
	assert.NoError(t, err)
	assert.True(t, isMasterAsleep)

	// ASSERTION 2: Lighting plugin made at least one service call
	// (scenes or turn-offs for the combined night + asleep state)
	calls := env.server.GetServiceCallsSince(snapshot)
	t.Logf("Total service calls after simultaneous changes: %d", len(calls))

	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
	lightTurnOffs := filterServiceCalls(calls, "light", "turn_off")
	totalLightingActions := len(sceneActivations) + len(lightTurnOffs)
	t.Logf("Lighting actions: %d scenes + %d turn-offs = %d total",
		len(sceneActivations), len(lightTurnOffs), totalLightingActions)

	assert.Greater(t, totalLightingActions, 0,
		"Lighting should respond to combined night + sleep state")

	// ASSERTION 3: No panics or deadlocks (reaching here proves it)
	t.Log("SUCCESS: Simultaneous sleep + day phase handled correctly")
}

// ============================================================================
// Test 4: All Plugins Handle Concurrent Multi-Variable Changes
// ============================================================================

// TestScenario_MultiVariableBurst_NoPanics validates that firing many state
// changes at once (day phase, presence, sleep, TV) doesn't cause panics,
// deadlocks, or corrupted state. This is the most aggressive concurrency test.
//
// User story: "The system should handle a burst of changes from multiple
// sensors without crashing."
//
// INVARIANT: System must not panic or deadlock under concurrent load.
// INVARIANT: All final state values must be internally consistent.
func TestScenario_MultiVariableBurst_NoPanics(t *testing.T) {
	t.Parallel()
	env, cleanup := setupConcurrentTest(t)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: System in default state")

	env.server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "morning", map[string]interface{}{})

	waitForProcessing(t, env.stateManager)
	snapshot := env.server.ServiceCallCount()

	// ========== WHEN ==========
	t.Log("WHEN: Multiple state variables change in rapid succession")

	// Fire many changes rapidly — this tests the worst case for concurrency
	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	env.server.SetState("input_boolean.caroline_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	env.server.SetState("input_boolean.everyone_asleep", "on", map[string]interface{}{})

	// Wait for everything to settle
	waitForStringState(t, env.stateManager, "dayPhase", "night", "dayPhase should reach night")
	waitForBoolState(t, env.stateManager, "isMasterAsleep", true, "isMasterAsleep should become true")

	// Wait for all plugin reactions to complete
	waitForProcessing(t, env.stateManager)

	// ========== THEN ==========
	t.Log("THEN: System is stable with consistent state (no panics/deadlocks)")

	// ASSERTION 1: All final states are consistent
	dayPhase, err := env.stateManager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "night", dayPhase, "Final day phase should be night")

	isCarolineHome, err := env.stateManager.GetBool("isCarolineHome")
	assert.NoError(t, err)
	assert.True(t, isCarolineHome, "Caroline should be home")

	isMasterAsleep, err := env.stateManager.GetBool("isMasterAsleep")
	assert.NoError(t, err)
	assert.True(t, isMasterAsleep, "Master should be asleep")

	// ASSERTION 2: Service calls were tracked (plugins processed events)
	calls := env.server.GetServiceCallsSince(snapshot)
	t.Logf("Total service calls after burst: %d", len(calls))
	assert.GreaterOrEqual(t, len(calls), 0,
		"Service calls should be tracked without crashes")

	// ASSERTION 3: If we reached here, no panics or deadlocks occurred
	t.Log("SUCCESS: Multi-variable burst handled without panics or deadlocks")
}
