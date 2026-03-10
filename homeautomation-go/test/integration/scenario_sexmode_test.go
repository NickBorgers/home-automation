package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/config"
	"homeautomation/internal/plugins/sexmode"
	"homeautomation/internal/plugins/sleephygiene"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getNumericValue converts interface{} to int, handling both int and float64 types
func getNumericValue(v interface{}) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	case int64:
		return int(val)
	default:
		return 0
	}
}

// setupSexModeScenarioTest creates a test environment with the Sex Mode plugin
func setupSexModeScenarioTest(t *testing.T) (*MockHAServer, *sexmode.Manager, *state.Manager, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	// Create logger
	logger := testlogger.New()

	// Initialize sex mode toggle to off
	server.SetState("input_boolean.sex", "off", nil)

	// Set up Eight Sleep entities with min_temp attribute for auto-detection
	// HA exposes temperatures in Fahrenheit (55-110°F range)
	server.SetState(sexmode.EightSleepNickEntity, "heat_cool", map[string]interface{}{
		"min_temp": float64(55),
		"max_temp": float64(110),
	})
	server.SetState(sexmode.EightSleepCarolineEntity, "heat_cool", map[string]interface{}{
		"min_temp": float64(55),
		"max_temp": float64(110),
	})
	// Allow async plugin initialization to complete
	waitForProcessing(t, stateManager)

	// Create and start Sex Mode plugin
	sexModeManager := sexmode.NewManager(context.Background(), client, stateManager, logger, false, nil)
	require.NoError(t, sexModeManager.Start(), "Sex Mode manager should start successfully")

	cleanup := func() {
		sexModeManager.Stop()
		baseCleanup()
	}

	return server, sexModeManager, stateManager, cleanup
}

// ============================================================================
// Sex Mode Activation Scenario Tests
// ============================================================================

// TestScenario_SexModeActivation_SetsMusicToSex tests that activating sex mode
// changes the music playback type to "sex"
func TestScenario_SexModeActivation_SetsMusicToSex(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is off and music is playing 'evening' type")

	// Set initial states
	require.NoError(t, stateManager.SetString("musicPlaybackType", "evening"))
	require.NoError(t, stateManager.SetString("dayPhase", "evening"))
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	t.Log("WHEN: Sex mode is activated (input_boolean.sex → on)")

	server.SetState("input_boolean.sex", "on", nil)

	t.Log("THEN: musicPlaybackType should change to 'sex'")

	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "musicPlaybackType should be 'sex' after activation")

	t.Log("✓ Music type correctly changed to 'sex'")
}

// TestScenario_SexModeActivation_ActivatesNightScene tests that activating sex mode
// turns on the Primary Suite night scene
func TestScenario_SexModeActivation_ActivatesNightScene(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is off")

	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Sex mode is activated")

	server.SetState("input_boolean.sex", "on", nil)

	t.Log("THEN: Primary Suite night scene should be activated")

	waitForServiceCallWithEntitySince(t, server, snapshot, "scene", "turn_on", "scene.primary_suite_night", "scene.primary_suite_night should be activated")
	calls := server.GetServiceCallsSince(snapshot)
	sceneCall := FindServiceCallWithEntityID(calls, "scene", "turn_on", "scene.primary_suite_night")

	if sceneCall != nil {
		t.Logf("✓ Night scene activated: %s.%s for %v",
			sceneCall.Domain,
			sceneCall.Service,
			sceneCall.ServiceData["entity_id"])
	}
}

// TestScenario_SexModeActivation_SetsEightSleepToColdest tests that activating sex mode
// sets both Eight Sleep beds to the coldest setting (auto-detected from entity attributes)
func TestScenario_SexModeActivation_SetsEightSleepToColdest(t *testing.T) {
	t.Parallel()
	server, _, _, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is off")

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Sex mode is activated")

	server.SetState("input_boolean.sex", "on", nil)

	t.Log("THEN: Both Eight Sleep sides should be set to coldest (auto-detected min_temp: 55)")

	// Wait for both Eight Sleep climate calls
	waitForServiceCallWithEntitySince(t, server, snapshot, "climate", "set_temperature", sexmode.EightSleepNickEntity, "Nick's Eight Sleep should be set")
	waitForServiceCallWithEntitySince(t, server, snapshot, "climate", "set_temperature", sexmode.EightSleepCarolineEntity, "Caroline's Eight Sleep should be set")
	calls := server.GetServiceCallsSince(snapshot)

	// Expected min temp from mock setup (55°F)
	expectedMinTemp := 55

	// Find Nick's Eight Sleep call
	nickCall := FindServiceCallWithEntityID(calls, "climate", "set_temperature", sexmode.EightSleepNickEntity)
	assert.NotNil(t, nickCall, "Nick's Eight Sleep should be set")
	if nickCall != nil {
		value := getNumericValue(nickCall.ServiceData["temperature"])
		assert.Equal(t, expectedMinTemp, value, "Nick's Eight Sleep should be set to coldest")
		t.Logf("✓ Nick's Eight Sleep set to %d", value)
	}

	// Find Caroline's Eight Sleep call
	carolineCall := FindServiceCallWithEntityID(calls, "climate", "set_temperature", sexmode.EightSleepCarolineEntity)
	assert.NotNil(t, carolineCall, "Caroline's Eight Sleep should be set")
	if carolineCall != nil {
		value := getNumericValue(carolineCall.ServiceData["temperature"])
		assert.Equal(t, expectedMinTemp, value, "Caroline's Eight Sleep should be set to coldest")
		t.Logf("✓ Caroline's Eight Sleep set to %d", value)
	}
}

// TestScenario_SexModeActivation_FullCoordination tests the complete activation sequence
func TestScenario_SexModeActivation_FullCoordination(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Normal evening conditions - music playing, lights on")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "evening"))
	require.NoError(t, stateManager.SetString("dayPhase", "evening"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Sex mode is activated")

	server.SetState("input_boolean.sex", "on", nil)

	t.Log("THEN: All three systems should be coordinated")

	// 1. Music
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "Music should switch to sex type")
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "sex", musicType, "Music should switch to sex type")
	t.Log("  ✓ Music: switched to 'sex'")

	// 2. Lighting
	waitForServiceCallWithEntitySince(t, server, snapshot, "scene", "turn_on", "scene.primary_suite_night", "Night scene should be activated")
	calls := server.GetServiceCallsSince(snapshot)
	sceneCall := FindServiceCallWithEntityID(calls, "scene", "turn_on", "scene.primary_suite_night")
	assert.NotNil(t, sceneCall, "Night scene should be activated")
	t.Log("  ✓ Lighting: night scene activated")

	// 3. Climate - wait for both Eight Sleep service calls to be made
	assert.Eventually(t, func() bool {
		sinceCalls := server.GetServiceCallsSince(snapshot)
		eightSleepCalls := FilterServiceCalls(sinceCalls, "climate", "set_temperature")
		return len(eightSleepCalls) >= 2
	}, stateWaitTimeout, statePollInterval, "Both Eight Sleep beds should be adjusted")

	calls = server.GetServiceCallsSince(snapshot)
	eightSleepCalls := FilterServiceCalls(calls, "climate", "set_temperature")
	assert.Equal(t, 2, len(eightSleepCalls), "Both Eight Sleep beds should be adjusted")
	t.Log("  ✓ Climate: Eight Sleep beds set to coldest")

	t.Log("✓ Full coordination successful")
}

// ============================================================================
// Sex Mode Deactivation Scenario Tests
// ============================================================================

// TestScenario_SexModeDeactivation_RestoresMusicType tests that deactivating sex mode
// restores the previous music playback type
func TestScenario_SexModeDeactivation_RestoresMusicType(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Music was 'working' type before sex mode was activated")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "working"))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	// Activate sex mode
	server.SetState("input_boolean.sex", "on", nil)

	// Verify music changed to sex
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "Music should be 'sex' during activation")
	// Wait for activation handler to fully complete (night scene + Eight Sleep calls)
	waitForServiceCallWithEntity(t, server, "scene", "turn_on", "scene.primary_suite_night", "activation should complete with night scene")
	waitForProcessing(t, stateManager)

	t.Log("WHEN: Sex mode is deactivated")

	server.SetState("input_boolean.sex", "off", nil)

	t.Log("THEN: musicPlaybackType should be restored to 'working'")

	waitForStringState(t, stateManager, "musicPlaybackType", "working", "musicPlaybackType should be restored")

	t.Log("Music type correctly restored to 'working'")
}

// TestScenario_SexModeDeactivation_ActivatesDayPhaseScene tests that deactivating sex mode
// activates the appropriate scene based on current day phase
func TestScenario_SexModeDeactivation_ActivatesDayPhaseScene(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is active and day phase is 'sunset'")

	require.NoError(t, stateManager.SetString("dayPhase", "sunset"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))

	// Activate sex mode
	server.SetState("input_boolean.sex", "on", nil)
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "sex mode should activate")
	// Wait for activation handler to fully complete (night scene + Eight Sleep calls)
	waitForServiceCallWithEntity(t, server, "scene", "turn_on", "scene.primary_suite_night", "activation should complete with night scene")
	// Wait for all Eight Sleep climate calls to complete (these happen asynchronously)
	assert.Eventually(t, func() bool {
		allCalls := server.GetServiceCalls()
		eightSleepCalls := FilterServiceCalls(allCalls, "climate", "set_temperature")
		return len(eightSleepCalls) >= 2
	}, stateWaitTimeout, statePollInterval, "Both Eight Sleep beds should be adjusted during activation")

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Sex mode is deactivated")

	server.SetState("input_boolean.sex", "off", nil)

	t.Log("THEN: Primary Suite should activate the sunset scene with transition")

	waitForServiceCallWithEntitySince(t, server, snapshot, "scene", "turn_on", "scene.primary_suite_sunset", "scene.primary_suite_sunset should be activated")
	calls := server.GetServiceCallsSince(snapshot)
	sceneCall := FindServiceCallWithEntityID(calls, "scene", "turn_on", "scene.primary_suite_sunset")

	if sceneCall != nil {
		// Verify transition is set (may be int or float64 depending on serialization)
		transition, hasTransition := sceneCall.ServiceData["transition"]
		assert.True(t, hasTransition, "Scene call should include transition")
		assert.Equal(t, 30, getNumericValue(transition), "Transition should be 30 seconds")
		t.Logf("✓ Sunset scene activated with %v second transition", transition)
	}
}

// TestScenario_SexModeDeactivation_TurnsOffLightsWhenAsleep tests that deactivating sex mode
// turns off lights when master is asleep
func TestScenario_SexModeDeactivation_TurnsOffLightsWhenAsleep(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is active")

	require.NoError(t, stateManager.SetString("dayPhase", "night"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false)) // Not asleep during activation
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	// Activate sex mode
	server.SetState("input_boolean.sex", "on", nil)
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "sex mode should activate")

	t.Log("AND: Master goes to sleep during the session")

	require.NoError(t, stateManager.SetBool("isMasterAsleep", true))
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Sex mode is deactivated")

	server.SetState("input_boolean.sex", "off", nil)

	t.Log("THEN: Primary Suite lights should be turned off (not scene activated)")

	// Wait for light turn off call
	waitForServiceCallSince(t, server, snapshot, "light", "turn_off", "lights should be turned off")

	// Should NOT activate a scene
	calls := server.GetServiceCallsSince(snapshot)
	sceneCall := FindServiceCallWithEntityID(calls, "scene", "turn_on", "scene.primary_suite_night")
	assert.Nil(t, sceneCall, "Should not activate night scene when master is asleep")

	// Should turn off lights
	lightOffCall := FindServiceCallWithData(server.GetServiceCallsSince(snapshot), "light", "turn_off", "area_id", "master_bedroom")
	assert.NotNil(t, lightOffCall, "Primary Suite lights should be turned off")

	if lightOffCall != nil {
		t.Log("✓ Lights correctly turned off when master is asleep")
	}
}

// ============================================================================
// Day Phase Variation Tests
// ============================================================================

// TestScenario_SexModeDeactivation_DifferentDayPhases tests lighting restoration
// for various day phases
func TestScenario_SexModeDeactivation_DifferentDayPhases(t *testing.T) {
	t.Parallel()
	dayPhases := []string{"morning", "day", "sunset", "evening", "night"}

	for _, phase := range dayPhases {
		t.Run("dayPhase_"+phase, func(t *testing.T) {
			server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
			defer cleanup()

			t.Logf("GIVEN: Sex mode is active and day phase is '%s'", phase)

			require.NoError(t, stateManager.SetString("dayPhase", phase))
			require.NoError(t, stateManager.SetBool("isMasterAsleep", false))

			// Activate sex mode
			server.SetState("input_boolean.sex", "on", nil)
			waitForStringState(t, stateManager, "musicPlaybackType", "sex", "sex mode should activate")
			// Wait for activation handler to fully complete (night scene + Eight Sleep calls)
			waitForServiceCallWithEntity(t, server, "scene", "turn_on", "scene.primary_suite_night", "activation should complete with night scene")
			// Wait for all Eight Sleep climate calls to complete (these happen asynchronously)
			assert.Eventually(t, func() bool {
				allCalls := server.GetServiceCalls()
				eightSleepCalls := FilterServiceCalls(allCalls, "climate", "set_temperature")
				return len(eightSleepCalls) >= 2
			}, stateWaitTimeout, statePollInterval, "Both Eight Sleep beds should be adjusted during activation")
			// Wait for all activation handler goroutines to fully complete before snapshot
			waitForProcessing(t, stateManager)

			snapshot := server.ServiceCallCount()

			t.Log("WHEN: Sex mode is deactivated")

			server.SetState("input_boolean.sex", "off", nil)

			t.Logf("THEN: Primary Suite should activate scene.primary_suite_%s", phase)

			expectedScene := "scene.primary_suite_" + phase
			waitForServiceCallWithEntitySince(t, server, snapshot, "scene", "turn_on", expectedScene, "Expected scene %s to be activated", expectedScene)
			calls := server.GetServiceCallsSince(snapshot)
			sceneCall := FindServiceCallWithEntityID(calls, "scene", "turn_on", expectedScene)

			if sceneCall != nil {
				t.Logf("✓ Correct scene activated: %s", expectedScene)
			}
		})
	}
}

// ============================================================================
// Edge Case and Error Handling Tests
// ============================================================================

// TestScenario_SexModeDuplicateActivation_Ignored tests that activating sex mode
// twice does not trigger duplicate actions
func TestScenario_SexModeDuplicateActivation_Ignored(t *testing.T) {
	t.Parallel()
	server, sexModeManager, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is already active")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "evening"))
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	// Activate sex mode
	server.SetState("input_boolean.sex", "on", nil)
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "sex mode should activate")
	// Wait for night scene call (happens immediately before isActive is set in handleSexModeOn)
	waitForServiceCallWithEntity(t, server, "scene", "turn_on", "scene.primary_suite_night", "Primary Suite night scene should activate during sex mode")
	// Wait for all Eight Sleep climate calls to complete (these happen asynchronously)
	assert.Eventually(t, func() bool {
		allCalls := server.GetServiceCalls()
		eightSleepCalls := FilterServiceCalls(allCalls, "climate", "set_temperature")
		return len(eightSleepCalls) >= 2
	}, stateWaitTimeout, statePollInterval, "Both Eight Sleep beds should be adjusted")

	// Count initial service calls
	initialCalls := len(server.GetServiceCalls())
	t.Logf("Initial service calls: %d", initialCalls)

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Sex mode activation is triggered again (duplicate)")

	// Manually trigger handleSexModeOn to simulate duplicate
	// This tests the internal guard
	sexModeManager.Reset() // Reset checks current state and won't re-activate
	// Allow async Reset to complete
	waitForProcessing(t, stateManager)

	t.Log("THEN: No additional service calls should be made")

	calls := server.GetServiceCallsSince(snapshot)
	// Reset when already active should not make service calls
	assert.Equal(t, 0, len(calls), "No service calls should be made on duplicate activation")

	t.Log("✓ Duplicate activation correctly ignored")
}

// TestScenario_SexModeDeactivationWithoutActivation_Ignored tests that deactivating
// sex mode when it wasn't active does nothing
func TestScenario_SexModeDeactivationWithoutActivation_Ignored(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode was never activated")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "day"))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Sex mode is turned off (was already off)")

	server.SetState("input_boolean.sex", "off", nil)
	// Wait for state to be processed (expecting no change)
	waitForProcessing(t, stateManager)

	t.Log("THEN: No actions should be taken and music type should remain 'day'")

	// Music should remain unchanged
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "day", musicType, "Music type should remain unchanged")

	// No service calls for lighting
	calls := server.GetServiceCallsSince(snapshot)
	sceneCalls := FilterServiceCalls(calls, "scene", "turn_on")
	lightCalls := FilterServiceCalls(calls, "light", "turn_off")
	assert.Equal(t, 0, len(sceneCalls), "No scene should be activated")
	assert.Equal(t, 0, len(lightCalls), "No lights should be turned off")

	t.Log("✓ Deactivation without prior activation correctly ignored")
}

// TestScenario_SexModeReset_SyncsState tests that Reset() correctly syncs
// the plugin state with Home Assistant when there's a mismatch
func TestScenario_SexModeReset_SyncsState(t *testing.T) {
	t.Parallel()
	// This test creates a scenario where HA state is "on" BEFORE plugin starts,
	// simulating the plugin starting when sex mode is already active in HA
	server, client, stateManager, baseCleanup := setupTest(t)

	// Set sex mode to ON in HA before the plugin starts
	server.SetState("input_boolean.sex", "on", nil)
	// Allow HA state to be set before plugin starts
	waitForProcessing(t, stateManager)

	require.NoError(t, stateManager.SetString("musicPlaybackType", "morning"))
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	// Now create and start the sex mode manager
	logger := testlogger.New()
	sexModeManager := sexmode.NewManager(context.Background(), client, stateManager, logger, false, nil)
	require.NoError(t, sexModeManager.Start(), "Sex Mode manager should start successfully")

	cleanup := func() {
		sexModeManager.Stop()
		baseCleanup()
	}
	defer cleanup()

	t.Log("GIVEN: Sex mode is ON in Home Assistant but plugin just started (no subscription event yet)")

	// The plugin started but hasn't received a state change event
	// It doesn't know the current HA state
	// Verify it's not active yet (plugin internal state is off)
	shadowState := sexModeManager.GetShadowState()
	assert.False(t, shadowState.Outputs.IsActive, "Plugin should not be active before Reset")

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Reset() is called to sync with HA state")

	err := sexModeManager.Reset()
	require.NoError(t, err)

	t.Log("THEN: Sex mode should detect HA state and activate")

	// Wait for music type to change
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "Music should switch to sex type after reset")

	// Plugin should now be active
	shadowState = sexModeManager.GetShadowState()
	assert.True(t, shadowState.Outputs.IsActive, "Plugin should be active after Reset")

	// Night scene should be activated
	calls := server.GetServiceCallsSince(snapshot)
	sceneCall := FindServiceCallWithEntityID(calls, "scene", "turn_on", "scene.primary_suite_night")
	assert.NotNil(t, sceneCall, "Night scene should be activated after reset")

	t.Log("✓ Reset correctly synced state and activated sex mode")
}

// TestScenario_SexModeActivationDeactivationCycle tests a complete cycle
func TestScenario_SexModeActivationDeactivationCycle(t *testing.T) {
	t.Parallel()
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Normal evening conditions")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "evening"))
	require.NoError(t, stateManager.SetString("dayPhase", "evening"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
	// Allow async handlers to process before clearing
	waitForProcessing(t, stateManager)

	t.Log("WHEN: Sex mode is activated")

	snapshot := server.ServiceCallCount()
	server.SetState("input_boolean.sex", "on", nil)

	// Verify activation - wait for ALL side-effects, not just music state change
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "music should switch to sex")
	t.Log("  Activation: music set to 'sex'")

	// Wait for night scene and Eight Sleep climate calls to complete before next phase
	waitForServiceCallWithEntitySince(t, server, snapshot, "scene", "turn_on", "scene.primary_suite_night", "activation should complete with night scene")
	assert.Eventually(t, func() bool {
		sinceCalls := server.GetServiceCallsSince(snapshot)
		eightSleepCalls := FilterServiceCalls(sinceCalls, "climate", "set_temperature")
		return len(eightSleepCalls) >= 2
	}, stateWaitTimeout, statePollInterval, "Both Eight Sleep beds should be adjusted during activation")

	activationCalls := len(server.GetServiceCallsSince(snapshot))
	t.Logf("  Activation made %d service calls", activationCalls)

	t.Log("AND WHEN: Sex mode is deactivated")

	snapshot = server.ServiceCallCount()
	server.SetState("input_boolean.sex", "off", nil)

	t.Log("THEN: Previous state should be fully restored")

	// Music should be restored
	waitForStringState(t, stateManager, "musicPlaybackType", "evening", "Music should be restored to 'evening'")
	t.Log("  ✓ Deactivation: music restored to 'evening'")

	// Evening scene should be activated
	waitForServiceCallWithEntitySince(t, server, snapshot, "scene", "turn_on", "scene.primary_suite_evening", "Evening scene should be activated")
	calls := server.GetServiceCallsSince(snapshot)
	sceneCall := FindServiceCallWithEntityID(calls, "scene", "turn_on", "scene.primary_suite_evening")
	assert.NotNil(t, sceneCall, "Evening scene should be activated")
	t.Log("  Deactivation: evening scene activated")

	t.Log("Full activation/deactivation cycle completed successfully")
}

// TestScenario_SexModeShadowState_TracksCorrectly tests that shadow state
// is properly maintained throughout the lifecycle
func TestScenario_SexModeShadowState_TracksCorrectly(t *testing.T) {
	t.Parallel()
	server, sexModeManager, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Initial state with music playing")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "day"))
	// Allow async handlers to process before checking state
	waitForProcessing(t, stateManager)

	t.Log("THEN: Shadow state should show inactive")

	shadowState := sexModeManager.GetShadowState()
	assert.False(t, shadowState.Outputs.IsActive, "IsActive should be false initially")

	t.Log("WHEN: Sex mode is activated")

	server.SetState("input_boolean.sex", "on", nil)
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "music should switch to sex")
	// Brief delay for shadow state to update after state change
	waitForProcessing(t, stateManager)

	t.Log("THEN: Shadow state should reflect activation")

	shadowState = sexModeManager.GetShadowState()
	assert.True(t, shadowState.Outputs.IsActive, "IsActive should be true after activation")
	assert.Equal(t, "activate", shadowState.Outputs.LastActionType, "LastActionType should be 'activate'")
	assert.Equal(t, "day", shadowState.Outputs.PreSexMusicType, "PreSexMusicType should be 'day'")
	assert.False(t, shadowState.Outputs.ActivatedAt.IsZero(), "ActivatedAt should be set")

	t.Log("  ✓ Shadow state correctly tracks activation")

	t.Log("WHEN: Sex mode is deactivated")

	server.SetState("input_boolean.sex", "off", nil)
	waitForStringState(t, stateManager, "musicPlaybackType", "day", "music should be restored")
	// Brief delay for shadow state to update after state change
	waitForProcessing(t, stateManager)

	t.Log("THEN: Shadow state should reflect deactivation")

	shadowState = sexModeManager.GetShadowState()
	assert.False(t, shadowState.Outputs.IsActive, "IsActive should be false after deactivation")
	assert.Equal(t, "deactivate", shadowState.Outputs.LastActionType, "LastActionType should be 'deactivate'")

	t.Log("  ✓ Shadow state correctly tracks deactivation")
	t.Log("✓ Shadow state tracking verified")
}

// ============================================================================
// Cross-Plugin Scenario Tests: Sex Mode + Sleep Hygiene (Issue #750)
//
// INVARIANT: Sex mode activation MUST cancel any active wake sequence.
// If isWakeSequenceActive remains true while sex mode is active, the
// sleephygiene plugin will eventually set musicPlaybackType="wakeup",
// overriding the sex zone.
// ============================================================================

// setupSexModeWithSleepHygieneTest creates a test environment with both
// the Sex Mode and Sleep Hygiene plugins running, simulating the real
// production interaction between these plugins.
func setupSexModeWithSleepHygieneTest(t *testing.T) (*MockHAServer, *sexmode.Manager, *sleephygiene.Manager, *state.Manager, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Initialize sex mode toggle to off
	server.SetState("input_boolean.sex", "off", nil)

	// Set up Eight Sleep entities with min_temp attribute for auto-detection
	server.SetState(sexmode.EightSleepNickEntity, "heat_cool", map[string]interface{}{
		"min_temp": float64(55),
		"max_temp": float64(110),
	})
	server.SetState(sexmode.EightSleepCarolineEntity, "heat_cool", map[string]interface{}{
		"min_temp": float64(55),
		"max_temp": float64(110),
	})

	// Allow async plugin initialization to complete
	waitForProcessing(t, stateManager)

	// Create config loader for sleephygiene
	configLoader := config.NewLoader("../../configs", logger)

	// Create and start Sleep Hygiene plugin first (it manages the wake sequence)
	sleepMgr := sleephygiene.NewManager(context.Background(), client, stateManager, configLoader, logger, false, nil, nil)
	// Make sleepFunc instant for testing so scheduleWakeMusic doesn't block
	sleepMgr.SetSleepFunc(func(d time.Duration) {})
	require.NoError(t, sleepMgr.Start(), "Sleep Hygiene manager should start successfully")

	// Create and start Sex Mode plugin
	sexModeManager := sexmode.NewManager(context.Background(), client, stateManager, logger, false, nil)
	require.NoError(t, sexModeManager.Start(), "Sex Mode manager should start successfully")

	cleanup := func() {
		sexModeManager.Stop()
		sleepMgr.Stop()
		baseCleanup()
	}

	return server, sexModeManager, sleepMgr, stateManager, cleanup
}

// TestScenario_SexModeDuringWakeSequence_CancelsWakeSequence validates that
// activating sex mode during an active wake sequence cancels the wake sequence.
//
// Issue #750: Sex mode should cancel active wake sequence
//
// Timeline from production bug (2026-03-01):
// 1. 15:24 — input_boolean.sex turned on via Siri
// 2. 15:24 — sexmode activates: sets musicPlaybackType="sex"
// 3. 15:33 — sleephygiene sets musicPlaybackType="wakeup" (wake sequence still active!)
//
// INVARIANT: When sex mode activates, isWakeSequenceActive MUST be set to false.
// This prevents sleephygiene from overwriting musicPlaybackType later.
func TestScenario_SexModeDuringWakeSequence_CancelsWakeSequence(t *testing.T) {
	t.Parallel()
	server, _, sleepMgr, stateManager, cleanup := setupSexModeWithSleepHygieneTest(t)
	defer cleanup()

	// GIVEN: Master is asleep, sleep music playing, wake sequence is active
	// (Eight Sleep alarm has fired, begin_wake completed, lights fading in)
	t.Log("GIVEN: Master is asleep with sleep music, wake sequence is active (begin_wake fired)")

	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", true))
	require.NoError(t, stateManager.SetString("musicPlaybackType", "sleep"))
	require.NoError(t, stateManager.SetString("dayPhase", "morning"))

	// Set up bedroom speaker for the wake sequence
	server.SetState("media_player.bedroom", "playing", map[string]interface{}{
		"volume_level": 0.30,
	})
	waitForProcessing(t, stateManager)

	// Trigger begin_wake to set isWakeSequenceActive=true
	sleepMgr.TriggerBeginWakeForTest()
	waitForProcessing(t, stateManager)

	// Verify wake sequence is active
	isWakeActive, err := stateManager.GetBool("isWakeSequenceActive")
	require.NoError(t, err)
	require.True(t, isWakeActive, "isWakeSequenceActive should be true after begin_wake")
	t.Log("  Wake sequence confirmed active (isWakeSequenceActive=true)")

	// WHEN: Sex mode is activated (e.g., via Siri)
	t.Log("WHEN: Sex mode is activated (input_boolean.sex → on)")

	server.SetState("input_boolean.sex", "on", nil)

	// THEN: musicPlaybackType should be "sex"
	t.Log("THEN: musicPlaybackType should be 'sex'")
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "musicPlaybackType should be 'sex' after sex mode activation")

	// AND: isWakeSequenceActive should be cancelled (set to false)
	t.Log("AND: isWakeSequenceActive should be cancelled")
	waitForBoolState(t, stateManager, "isWakeSequenceActive", false, "isWakeSequenceActive should be false after sex mode cancels it")

	t.Log("  ✓ musicPlaybackType = 'sex'")
	t.Log("  ✓ isWakeSequenceActive = false (wake sequence cancelled)")
}

// TestScenario_SexModeDuringWakeSequence_PreventsWakeMusicOverride validates that
// after sex mode cancels the wake sequence, the sleephygiene plugin does NOT
// override musicPlaybackType back to "wakeup".
//
// This is the full timeline test that reproduces the production bug from issue #750.
func TestScenario_SexModeDuringWakeSequence_PreventsWakeMusicOverride(t *testing.T) {
	t.Parallel()
	server, _, sleepMgr, stateManager, cleanup := setupSexModeWithSleepHygieneTest(t)
	defer cleanup()

	// GIVEN: Master is asleep, wake sequence is active (begin_wake done, waiting for wake)
	t.Log("GIVEN: Master is asleep, wake sequence active, lights fading in")

	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", true))
	require.NoError(t, stateManager.SetString("musicPlaybackType", "sleep"))
	require.NoError(t, stateManager.SetString("dayPhase", "morning"))

	server.SetState("media_player.bedroom", "playing", map[string]interface{}{
		"volume_level": 0.30,
	})
	waitForProcessing(t, stateManager)

	// T+0: Begin wake fires, sets isWakeSequenceActive=true
	t.Log("T+0: Begin wake fires")
	sleepMgr.TriggerBeginWakeForTest()
	waitForProcessing(t, stateManager)
	waitForBoolState(t, stateManager, "isWakeSequenceActive", true, "wake sequence should be active")

	// T+5min (simulated): Wake fires, starts light fade-in and schedules wake music
	t.Log("T+5min: Wake fires (lights start, wake music scheduled)")
	sleepMgr.TriggerWakeForTest()
	waitForProcessing(t, stateManager)

	// T+9min (simulated): Sex mode activated before wake music fires at T+30
	t.Log("T+9min: Sex mode activated during wake sequence")
	server.SetState("input_boolean.sex", "on", nil)
	waitForStringState(t, stateManager, "musicPlaybackType", "sex", "musicPlaybackType should be 'sex'")

	// Verify wake sequence was cancelled
	waitForBoolState(t, stateManager, "isWakeSequenceActive", false, "wake sequence should be cancelled")
	t.Log("  ✓ Wake sequence cancelled by sex mode")

	// T+30min (simulated): scheduleWakeMusic goroutine runs (sleepFunc is instant)
	// Since isWakeSequenceActive is now false, scheduleWakeMusic should abort
	// waitForProcessing ensures all handler goroutines (including scheduleWakeMusic) complete
	waitForProcessing(t, stateManager)

	// THEN: musicPlaybackType should STILL be "sex" (not overridden to "wakeup")
	t.Log("T+30min: Verifying musicPlaybackType was NOT overridden by sleephygiene")

	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "sex", musicType,
		"INVARIANT VIOLATED: musicPlaybackType should still be 'sex', not overridden by sleephygiene wake music")

	t.Log("  ✓ musicPlaybackType still 'sex' — sleephygiene did NOT override it")
	t.Log("✓ Full timeline validated: sex mode protected from wake music override")
}
