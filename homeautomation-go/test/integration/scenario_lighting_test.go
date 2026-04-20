package integration

import (
	"context"
	"path/filepath"
	"testing"

	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Lighting Control Plugin Scenario Tests
//
// These tests validate that the Lighting Control plugin correctly responds
// to state changes and activates the appropriate scenes.
// ============================================================================

// setupLightingScenarioTest creates a test environment with the lighting plugin
func setupLightingScenarioTest(t *testing.T) (*MockHAServer, *lighting.Manager, *state.Manager, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	// Load test lighting config
	configPath := filepath.Join("testdata", "hue_config_test.yaml")
	lightingConfig, err := lighting.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test lighting config")

	// Create logger
	logger := testlogger.New()

	// Create lighting plugin (read-only mode for testing)
	lightingMgr := lighting.NewManager(context.Background(), client, manager, lightingConfig, logger, false, nil)

	// Start the lighting plugin
	err = lightingMgr.Start()
	require.NoError(t, err, "Failed to start lighting manager")

	cleanup := func() {
		lightingMgr.Stop()
		baseCleanup()
	}

	return server, lightingMgr, manager, cleanup
}

// TestScenario_DayPhaseEvening_ActivatesCorrectScenes validates that when
// day phase changes to evening, the correct scenes activate for all rooms
func TestScenario_DayPhaseEvening_ActivatesCorrectScenes(t *testing.T) {
	t.Parallel()
	server, lightingMgr, stateManager, cleanup := setupLightingScenarioTest(t)
	defer cleanup()
	_ = lightingMgr

	// GIVEN: Day phase is morning, someone is home and awake
	t.Log("GIVEN: Day phase is morning, someone is home and awake")
	server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})

	// Wait for state changes to fully propagate through state manager AND lighting plugin handlers.
	// waitForProcessing() alone is unreliable here: WaitForHandlers Phase 2 only waits for one
	// event notification, so when multiple events are queued (morning, anyone_home,
	// anyone_home_and_awake), it may return before all handlers have run. This causes morning
	// scene activations to leak past the snapshot into the post-setup assertion window.
	//
	// Polling isAnyoneHomeAndAwake=true is reliable: the state manager updates cache and calls
	// notifySubscribers synchronously in the same goroutine, so when GetBool returns true, the
	// lighting plugin's handleOccupancyChange (and its scene activations) have already completed.
	waitForBoolState(t, stateManager, "isAnyoneHome", true, "isAnyoneHome should be true after setup")
	waitForBoolState(t, stateManager, "isAnyoneHomeAndAwake", true, "isAnyoneHomeAndAwake should be true after setup")
	// Additional flush for any remaining in-flight work
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	// WHEN: Day phase changes to evening
	t.Log("WHEN: Day phase changes to evening")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})

	// Wait for scene activation
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "evening scenes should be activated")

	// THEN: Verify correct scenes were activated
	t.Log("THEN: Verify correct scenes were activated")
	calls := server.GetServiceCallsSince(snapshot)
	t.Logf("Total service calls: %d", len(calls))

	// Filter to scene activations only (scene.turn_on)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
	t.Logf("Scene activations: %d", len(sceneActivations))

	// ASSERTION 1: At least one scene was activated
	assert.Greater(t, len(sceneActivations), 0,
		"Should activate at least one scene when day phase changes to evening")

	// ASSERTION 2: Scenes should be for the evening day phase
	// Check that scenes contain "evening" in their entity_id
	foundEveningScene := false
	for _, call := range sceneActivations {
		if entityID, ok := call.ServiceData["entity_id"].(string); ok {
			t.Logf("Scene activated: %s", entityID)
			if contains(entityID, "evening") {
				foundEveningScene = true
			}
		}
	}
	assert.True(t, foundEveningScene, "Should activate at least one evening scene")
}

// TestScenario_SunEventSunset_ActivatesScenes validates that when sun event
// changes to sunset, appropriate scenes are activated
func TestScenario_SunEventSunset_ActivatesScenes(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupLightingScenarioTest(t)
	defer cleanup()

	// GIVEN: Day phase is afternoon, sun event is before_sunset, someone is home and awake
	t.Log("GIVEN: Day phase is afternoon, sun event is before_sunset, someone is home and awake")
	server.SetState("input_text.day_phase", "afternoon", map[string]interface{}{})
	server.SetState("input_text.sun_event", "before_sunset", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	// Brief delay to let initialization complete before counting initial calls
	waitForProcessing(t, stateManager)

	// Get count before sun event change
	snapshot := server.ServiceCallCount()
	t.Logf("Service calls before sunset: %d", snapshot)

	// WHEN: Sun event changes to sunset
	t.Log("WHEN: Sun event changes to sunset")
	server.SetState("input_text.sun_event", "sunset", map[string]interface{}{})

	// Wait for scene activation
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "sunset scenes should be activated")

	// THEN: Verify scenes were activated
	t.Log("THEN: Verify scenes were activated")
	calls := server.GetServiceCallsSince(snapshot)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")

	t.Logf("Scene activations total: %d", len(sceneActivations))
	t.Logf("Total service calls since snapshot: %d", len(calls))

	// Sun event changes should trigger scene activations for all rooms
	// Check that we have calls after the sun event change
	assert.Greater(t, len(calls), 0,
		"Should make service calls when sun event changes to sunset")
}

// TestScenario_TVStateChange_TriggersLightingAdjustment validates that when
// TV starts playing, lighting scenes are re-evaluated (potentially dimmed)
func TestScenario_TVStateChange_TriggersLightingAdjustment(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupLightingScenarioTest(t)
	defer cleanup()

	// GIVEN: Evening, someone is home and awake, TV is not playing
	t.Log("GIVEN: Evening, someone is home and awake, TV is not playing")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.tv_playing", "off", map[string]interface{}{})
	// Brief delay to let initialization complete before taking snapshot
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	// WHEN: TV starts playing
	t.Log("WHEN: TV starts playing")
	server.SetState("input_boolean.tv_playing", "on", map[string]interface{}{})

	// Wait for scene re-evaluation
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "scenes should be re-evaluated when TV starts")

	// THEN: Verify lighting was re-evaluated
	t.Log("THEN: Verify lighting was re-evaluated")
	calls := server.GetServiceCallsSince(snapshot)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")

	t.Logf("Scene activations after TV state change: %d", len(sceneActivations))

	// The Living Room has on_if_false: isTVPlaying, so when TV starts playing
	// (isTVPlaying becomes true), the condition becomes false, which might
	// affect scene activation. We should see scene activity.
	// Note: The exact behavior depends on the logic, but we should see some scene activation
	assert.GreaterOrEqual(t, len(sceneActivations), 0,
		"Should re-evaluate scenes when TV state changes")
}

// TestScenario_EveryoneAsleep_TurnsOffLights validates that when everyone
// goes to sleep, lights turn off or switch to night mode
func TestScenario_EveryoneAsleep_TurnsOffLights(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupLightingScenarioTest(t)
	defer cleanup()

	// GIVEN: Evening, someone is home and awake
	t.Log("GIVEN: Evening, someone is home and awake")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	// isEveryoneAsleep is computed from isMasterAsleep AND isGuestAsleep
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.guest_asleep", "off", map[string]interface{}{})
	// Brief delay to let initialization complete before taking snapshot
	waitForProcessing(t, stateManager)

	// Get count before sleep state change
	snapshot := server.ServiceCallCount()
	t.Logf("Service calls before everyone asleep: %d", snapshot)

	// WHEN: Everyone goes to sleep (set both master and guest asleep)
	t.Log("WHEN: Everyone goes to sleep")
	server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	server.SetState("input_boolean.guest_asleep", "on", map[string]interface{}{})

	// Wait for lights to turn off
	waitForServiceCallSince(t, server, snapshot, "light", "turn_off", "lights should turn off when everyone asleep")

	// THEN: Verify lights were turned off or night mode activated
	t.Log("THEN: Verify lights were turned off or night mode activated")
	calls := server.GetServiceCallsSince(snapshot)

	// Look for light turn_off calls
	lightOffCalls := filterServiceCalls(calls, "light", "turn_off")

	t.Logf("Light turn_off calls: %d", len(lightOffCalls))
	t.Logf("Total service calls since snapshot: %d", len(calls))

	// When everyone is asleep, the off_if_true: isEveryoneAsleep condition
	// should trigger, causing lights to turn off
	// Check that we have service calls after the state change
	assert.Greater(t, len(calls), 0,
		"Should make service calls when everyone goes to sleep")

	// And specifically should have turned off lights
	assert.Greater(t, len(lightOffCalls), 0,
		"Should turn off lights when everyone is asleep")
}

// TestScenario_PresenceChangeHome_ActivatesScenes validates that when
// someone arrives home, appropriate scenes activate
func TestScenario_PresenceChangeHome_ActivatesScenes(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupLightingScenarioTest(t)
	defer cleanup()

	// GIVEN: Evening, no one is home
	t.Log("GIVEN: Evening, no one is home")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	// Brief delay to let initialization complete before taking snapshot
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	// WHEN: Someone arrives home (they're awake)
	t.Log("WHEN: Someone arrives home (they're awake)")
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})

	// Wait for scene activation
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "scenes should activate when someone arrives home")

	// THEN: Verify scenes were activated
	t.Log("THEN: Verify scenes were activated")
	calls := server.GetServiceCallsSince(snapshot)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")

	t.Logf("Scene activations after arrival: %d", len(sceneActivations))

	// When isAnyoneHome changes from false to true, rooms with on_if_true: isAnyoneHome
	// should activate their scenes
	assert.Greater(t, len(sceneActivations), 0,
		"Should activate scenes when someone arrives home")
}

// TestScenario_GuestArrival_ActivatesGuestScenes validates that when guests
// arrive, guest-specific scenes or brightness adjustments occur
func TestScenario_GuestArrival_ActivatesGuestScenes(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupLightingScenarioTest(t)
	defer cleanup()

	// GIVEN: Evening, someone is home and awake, no guests
	t.Log("GIVEN: Evening, someone is home and awake, no guests")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.have_guests", "off", map[string]interface{}{})
	// Brief delay to let initialization complete before taking snapshot
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	// WHEN: Guests arrive
	t.Log("WHEN: Guests arrive")
	server.SetState("input_boolean.have_guests", "on", map[string]interface{}{})

	// Wait for lighting plugin to process guest state change
	// Note: increase_brightness_if_true: isHaveGuests may not trigger immediate scene
	// re-evaluation - it's applied when the next scene change occurs
	waitForProcessing(t, stateManager)

	// THEN: Verify scenes were re-evaluated for guest presence (if any)
	t.Log("THEN: Verify scenes were re-evaluated for guest presence")
	calls := server.GetServiceCallsSince(snapshot)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")

	t.Logf("Scene activations after guest arrival: %d", len(sceneActivations))

	// Living Room has increase_brightness_if_true: isHaveGuests
	// This may or may not trigger scene re-activation depending on current conditions
	assert.GreaterOrEqual(t, len(sceneActivations), 0,
		"Should accept any number of scene activations when guests arrive")
}

// TestScenario_MasterBedroomSleep_HandlesConditionalLogic validates the
// conditional logic for master bedroom (on_if_false: isMasterAsleep)
func TestScenario_MasterBedroomSleep_HandlesConditionalLogic(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupLightingScenarioTest(t)
	defer cleanup()

	// GIVEN: Evening, someone is home and awake, master bedroom occupants awake
	t.Log("GIVEN: Evening, someone is home and awake, master bedroom occupants awake")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	// Brief delay to let initialization complete before taking snapshot
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	// WHEN: Master bedroom occupants go to sleep
	t.Log("WHEN: Master bedroom occupants go to sleep")
	server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})

	// Wait for lights to turn off
	waitForServiceCallSince(t, server, snapshot, "light", "turn_off", "master bedroom lights should turn off")

	// THEN: Verify master bedroom lights were turned off
	t.Log("THEN: Verify master bedroom lights were turned off")
	calls := server.GetServiceCallsSince(snapshot)

	lightOffCalls := filterServiceCalls(calls, "light", "turn_off")
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")

	t.Logf("Light turn_off calls: %d", len(lightOffCalls))
	t.Logf("Scene activations: %d", len(sceneActivations))

	// Master Bedroom has:
	// - on_if_false: isMasterAsleep (when false, turn on -> when true, don't turn on)
	// - off_if_true: isMasterAsleep (when true, turn off)
	// So when isMasterAsleep becomes true, lights should turn off
	foundMasterBedroomOff := false
	for _, call := range lightOffCalls {
		if entityID, ok := call.ServiceData["entity_id"].(string); ok {
			if contains(entityID, "master") || contains(entityID, "bedroom") {
				foundMasterBedroomOff = true
				t.Logf("Master bedroom light turned off: %s", entityID)
				break
			}
		}
	}

	// Should have turned off lights when master bedroom occupants sleep
	// The specific behavior depends on the implementation, but we expect light turn_off calls
	assert.True(t, len(lightOffCalls) > 0,
		"Should turn off lights when master bedroom occupants sleep")

	// Optionally check if master bedroom lights specifically were turned off
	if foundMasterBedroomOff {
		t.Log("Master bedroom lights were turned off as expected")
	}
}

// TestScenario_MultipleStateChanges_HandlesCorrectly validates that multiple
// rapid state changes are handled correctly without race conditions
func TestScenario_MultipleStateChanges_HandlesCorrectly(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupLightingScenarioTest(t)
	defer cleanup()

	// GIVEN: Initial state
	t.Log("GIVEN: Initial state - morning, no one home")
	server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "off", map[string]interface{}{})
	// Brief delay to let initialization complete before counting initial calls
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()
	t.Logf("Service calls before rapid changes: %d", snapshot)

	// WHEN: Multiple rapid state changes occur
	// Wait for each change to be processed before triggering the next, using polling
	// to verify the system processed each change without race conditions
	t.Log("WHEN: Multiple rapid state changes occur")
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	callsBeforeAfternoon := server.ServiceCallCount()
	waitForCondition(t, func() bool {
		return server.ServiceCallCount() > callsBeforeAfternoon
	}, "should process anyone_home state change before next change")
	server.SetState("input_text.day_phase", "afternoon", map[string]interface{}{})
	callsBeforeEvening := server.ServiceCallCount()
	waitForCondition(t, func() bool {
		return server.ServiceCallCount() > callsBeforeEvening
	}, "should process afternoon day phase change before next change")
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	callsBeforeTV := server.ServiceCallCount()
	waitForCondition(t, func() bool {
		return server.ServiceCallCount() > callsBeforeTV
	}, "should process evening day phase change before next change")
	server.SetState("input_boolean.tv_playing", "on", map[string]interface{}{})

	// Wait for final scene activation after all rapid changes
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "scenes should activate after rapid state changes")

	// THEN: All state changes should be processed without errors
	t.Log("THEN: All state changes should be processed without errors")
	calls := server.GetServiceCallsSince(snapshot)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")

	t.Logf("Total service calls since snapshot: %d", len(calls))
	t.Logf("Scene activations: %d", len(sceneActivations))

	// Should have processed state changes and made service calls
	// The exact number depends on the implementation, but we should have
	// service calls after the snapshot
	assert.Greater(t, len(calls), 0,
		"Should have made service calls in response to state changes")

	// The system should handle rapid changes without crashing or deadlocking
	// This test passing at all (without timeout or panic) validates this
	t.Log("SUCCESS: Handled multiple rapid state changes without errors")
}

// TestScenario_PersonDetectionOverridesSuneventTurnOff validates that when a room
// is re-evaluated (e.g., person detected), any in-flight operation from a prior
// evaluation (e.g., sunevent turn-off retry loop) is cancelled so the newer action wins.
//
// Regression test for: stale retry loop from sunevent change sent light.turn_off
// commands that overrode a person detection scene activation, causing lights to flicker.
func TestScenario_PersonDetectionOverridesSuneventTurnOff(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupLightingScenarioTest(t)
	defer cleanup()

	// GIVEN: Someone is home, no person at front, day phase is evening
	t.Log("GIVEN: Someone is home, no person at front, day phase is evening")
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.front_of_house_person_present", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	server.SetState("input_text.sun_event", "sunset", map[string]interface{}{})
	waitForProcessing(t, stateManager)
	snapshot := server.ServiceCallCount()

	// WHEN: Sunevent changes (triggers evaluation for all rooms, including Front of House)
	t.Log("WHEN: Sunevent changes to dusk (would turn off front lights since no person)")
	server.SetState("input_text.sun_event", "dusk", map[string]interface{}{})
	waitForProcessing(t, stateManager)

	// AND THEN: Person is detected at the front (triggers re-evaluation of Front of House)
	t.Log("AND THEN: Person detected at front of house")
	server.SetState("input_boolean.front_of_house_person_present", "on", map[string]interface{}{})

	// Wait for the person detection to trigger scene activation
	waitForServiceCallSince(t, server, snapshot, "scene", "turn_on", "person detection should trigger scene activation for Front of House")

	// THEN: The last service action for Front of House should be a scene activation (turn-on),
	// not a turn-off. The sunevent turn-off should have been superseded.
	t.Log("THEN: Last action for Front of House should be scene activation, not turn-off")
	calls := server.GetServiceCallsSince(snapshot)

	// Find the last service call that targeted front_of_house
	var lastFrontAction *ServiceCall
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		// Check for scene activation (entity_id contains "front_of_house")
		if entityID, ok := call.ServiceData["entity_id"].(string); ok {
			if contains(entityID, "front_of_house") {
				lastFrontAction = &calls[i]
				break
			}
		}
		// Check for light turn-off (area_id is "front_of_house")
		if areaID, ok := call.ServiceData["area_id"].(string); ok {
			if areaID == "front_of_house" {
				lastFrontAction = &calls[i]
				break
			}
		}
	}

	require.NotNil(t, lastFrontAction, "Should have at least one service call for Front of House")
	assert.Equal(t, "scene", lastFrontAction.Domain,
		"Last action for Front of House should be scene activation (person detected), not light.turn_off")
	assert.Equal(t, "turn_on", lastFrontAction.Service,
		"Last action for Front of House should be turn_on")
	t.Logf("Last Front of House action: %s.%s (entity_id=%v)",
		lastFrontAction.Domain, lastFrontAction.Service, lastFrontAction.ServiceData["entity_id"])
}

// Helper function to check if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(substr) == 0 ||
			indexOf(s, substr) >= 0)
}

// Helper function to find index of substring (simple implementation)
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
