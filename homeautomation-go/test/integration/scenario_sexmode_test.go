package integration

import (
	"testing"
	"time"

	"homeautomation/internal/plugins/sexmode"
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
	time.Sleep(50 * time.Millisecond)

	// Create and start Sex Mode plugin
	sexModeManager := sexmode.NewManager(client, stateManager, logger, false, nil)
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
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is off and music is playing 'evening' type")

	// Set initial states
	require.NoError(t, stateManager.SetString("musicPlaybackType", "evening"))
	require.NoError(t, stateManager.SetString("dayPhase", "evening"))
	time.Sleep(100 * time.Millisecond)

	server.ClearServiceCalls()

	t.Log("WHEN: Sex mode is activated (input_boolean.sex → on)")

	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: musicPlaybackType should change to 'sex'")

	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "sex", musicType, "musicPlaybackType should be 'sex' after activation")

	t.Log("✓ Music type correctly changed to 'sex'")
}

// TestScenario_SexModeActivation_ActivatesNightScene tests that activating sex mode
// turns on the Primary Suite night scene
func TestScenario_SexModeActivation_ActivatesNightScene(t *testing.T) {
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is off")

	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	time.Sleep(100 * time.Millisecond)

	server.ClearServiceCalls()

	t.Log("WHEN: Sex mode is activated")

	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: Primary Suite night scene should be activated")

	sceneCall := server.FindServiceCall("scene", "turn_on", "scene.primary_suite_night")
	assert.NotNil(t, sceneCall, "scene.primary_suite_night should be activated")

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
	server, _, _, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is off")

	server.ClearServiceCalls()

	t.Log("WHEN: Sex mode is activated")

	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: Both Eight Sleep sides should be set to coldest (auto-detected min_temp: 55)")

	calls := server.GetServiceCalls()

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
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Normal evening conditions - music playing, lights on")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "evening"))
	require.NoError(t, stateManager.SetString("dayPhase", "evening"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
	time.Sleep(100 * time.Millisecond)

	server.ClearServiceCalls()

	t.Log("WHEN: Sex mode is activated")

	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: All three systems should be coordinated")

	// 1. Music
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "sex", musicType, "Music should switch to sex type")
	t.Log("  ✓ Music: switched to 'sex'")

	// 2. Lighting
	sceneCall := server.FindServiceCall("scene", "turn_on", "scene.primary_suite_night")
	assert.NotNil(t, sceneCall, "Night scene should be activated")
	t.Log("  ✓ Lighting: night scene activated")

	// 3. Climate
	calls := server.GetServiceCalls()
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
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Music was 'working' type before sex mode was activated")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "working"))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
	time.Sleep(100 * time.Millisecond)

	// Activate sex mode
	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	// Verify music changed to sex
	musicType, _ := stateManager.GetString("musicPlaybackType")
	assert.Equal(t, "sex", musicType, "Music should be 'sex' during activation")

	server.ClearServiceCalls()

	t.Log("WHEN: Sex mode is deactivated")

	server.SetState("input_boolean.sex", "off", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: musicPlaybackType should be restored to 'working'")

	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "working", musicType, "musicPlaybackType should be restored")

	t.Log("✓ Music type correctly restored to 'working'")
}

// TestScenario_SexModeDeactivation_ActivatesDayPhaseScene tests that deactivating sex mode
// activates the appropriate scene based on current day phase
func TestScenario_SexModeDeactivation_ActivatesDayPhaseScene(t *testing.T) {
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is active and day phase is 'sunset'")

	require.NoError(t, stateManager.SetString("dayPhase", "sunset"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
	time.Sleep(100 * time.Millisecond)

	// Activate sex mode
	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	server.ClearServiceCalls()

	t.Log("WHEN: Sex mode is deactivated")

	server.SetState("input_boolean.sex", "off", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: Primary Suite should activate the sunset scene with transition")

	sceneCall := server.FindServiceCall("scene", "turn_on", "scene.primary_suite_sunset")
	assert.NotNil(t, sceneCall, "scene.primary_suite_sunset should be activated")

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
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is active")

	require.NoError(t, stateManager.SetString("dayPhase", "night"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false)) // Not asleep during activation
	time.Sleep(100 * time.Millisecond)

	// Activate sex mode
	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("AND: Master goes to sleep during the session")

	require.NoError(t, stateManager.SetBool("isMasterAsleep", true))
	time.Sleep(100 * time.Millisecond)

	server.ClearServiceCalls()

	t.Log("WHEN: Sex mode is deactivated")

	server.SetState("input_boolean.sex", "off", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: Primary Suite lights should be turned off (not scene activated)")

	// Should NOT activate a scene
	sceneCall := server.FindServiceCall("scene", "turn_on", "scene.primary_suite_night")
	assert.Nil(t, sceneCall, "Should not activate night scene when master is asleep")

	// Should turn off lights
	lightOffCall := FindServiceCallWithData(server.GetServiceCalls(), "light", "turn_off", "area_id", "master_bedroom")
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
	dayPhases := []string{"morning", "day", "sunset", "evening", "night"}

	for _, phase := range dayPhases {
		t.Run("dayPhase_"+phase, func(t *testing.T) {
			server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
			defer cleanup()

			t.Logf("GIVEN: Sex mode is active and day phase is '%s'", phase)

			require.NoError(t, stateManager.SetString("dayPhase", phase))
			require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
			time.Sleep(100 * time.Millisecond)

			// Activate sex mode
			server.SetState("input_boolean.sex", "on", nil)
			time.Sleep(100 * time.Millisecond)

			server.ClearServiceCalls()

			t.Log("WHEN: Sex mode is deactivated")

			server.SetState("input_boolean.sex", "off", nil)
			time.Sleep(100 * time.Millisecond)

			t.Logf("THEN: Primary Suite should activate scene.primary_suite_%s", phase)

			expectedScene := "scene.primary_suite_" + phase
			sceneCall := server.FindServiceCall("scene", "turn_on", expectedScene)
			assert.NotNil(t, sceneCall, "Expected scene %s to be activated", expectedScene)

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
	server, sexModeManager, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode is already active")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "evening"))
	time.Sleep(100 * time.Millisecond)

	// Activate sex mode
	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	// Count initial service calls
	initialCalls := len(server.GetServiceCalls())
	t.Logf("Initial service calls: %d", initialCalls)

	server.ClearServiceCalls()

	t.Log("WHEN: Sex mode activation is triggered again (duplicate)")

	// Manually trigger handleSexModeOn to simulate duplicate
	// This tests the internal guard
	sexModeManager.Reset() // Reset checks current state and won't re-activate
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: No additional service calls should be made")

	calls := server.GetServiceCalls()
	// Reset when already active should not make service calls
	assert.Equal(t, 0, len(calls), "No service calls should be made on duplicate activation")

	t.Log("✓ Duplicate activation correctly ignored")
}

// TestScenario_SexModeDeactivationWithoutActivation_Ignored tests that deactivating
// sex mode when it wasn't active does nothing
func TestScenario_SexModeDeactivationWithoutActivation_Ignored(t *testing.T) {
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sex mode was never activated")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "day"))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	time.Sleep(100 * time.Millisecond)

	server.ClearServiceCalls()

	t.Log("WHEN: Sex mode is turned off (was already off)")

	server.SetState("input_boolean.sex", "off", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: No actions should be taken and music type should remain 'day'")

	// Music should remain unchanged
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "day", musicType, "Music type should remain unchanged")

	// No service calls for lighting
	calls := server.GetServiceCalls()
	sceneCalls := FilterServiceCalls(calls, "scene", "turn_on")
	lightCalls := FilterServiceCalls(calls, "light", "turn_off")
	assert.Equal(t, 0, len(sceneCalls), "No scene should be activated")
	assert.Equal(t, 0, len(lightCalls), "No lights should be turned off")

	t.Log("✓ Deactivation without prior activation correctly ignored")
}

// TestScenario_SexModeReset_SyncsState tests that Reset() correctly syncs
// the plugin state with Home Assistant when there's a mismatch
func TestScenario_SexModeReset_SyncsState(t *testing.T) {
	// This test creates a scenario where HA state is "on" BEFORE plugin starts,
	// simulating the plugin starting when sex mode is already active in HA
	server, client, stateManager, baseCleanup := setupTest(t)

	// Set sex mode to ON in HA before the plugin starts
	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, stateManager.SetString("musicPlaybackType", "morning"))
	time.Sleep(100 * time.Millisecond)

	// Now create and start the sex mode manager
	logger := testlogger.New()
	sexModeManager := sexmode.NewManager(client, stateManager, logger, false, nil)
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

	server.ClearServiceCalls()

	t.Log("WHEN: Reset() is called to sync with HA state")

	err := sexModeManager.Reset()
	require.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: Sex mode should detect HA state and activate")

	// Plugin should now be active
	shadowState = sexModeManager.GetShadowState()
	assert.True(t, shadowState.Outputs.IsActive, "Plugin should be active after Reset")

	// Music should change to sex
	musicType, err := stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "sex", musicType, "Music should switch to sex type after reset")

	// Night scene should be activated
	sceneCall := server.FindServiceCall("scene", "turn_on", "scene.primary_suite_night")
	assert.NotNil(t, sceneCall, "Night scene should be activated after reset")

	t.Log("✓ Reset correctly synced state and activated sex mode")
}

// TestScenario_SexModeActivationDeactivationCycle tests a complete cycle
func TestScenario_SexModeActivationDeactivationCycle(t *testing.T) {
	server, _, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Normal evening conditions")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "evening"))
	require.NoError(t, stateManager.SetString("dayPhase", "evening"))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
	time.Sleep(100 * time.Millisecond)

	t.Log("WHEN: Sex mode is activated")

	server.ClearServiceCalls()
	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	// Verify activation
	musicType, _ := stateManager.GetString("musicPlaybackType")
	assert.Equal(t, "sex", musicType)
	t.Log("  ✓ Activation: music set to 'sex'")

	activationCalls := len(server.GetServiceCalls())
	t.Logf("  ✓ Activation made %d service calls", activationCalls)

	t.Log("AND WHEN: Sex mode is deactivated")

	server.ClearServiceCalls()
	server.SetState("input_boolean.sex", "off", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: Previous state should be fully restored")

	// Music should be restored
	musicType, _ = stateManager.GetString("musicPlaybackType")
	assert.Equal(t, "evening", musicType, "Music should be restored to 'evening'")
	t.Log("  ✓ Deactivation: music restored to 'evening'")

	// Evening scene should be activated
	sceneCall := server.FindServiceCall("scene", "turn_on", "scene.primary_suite_evening")
	assert.NotNil(t, sceneCall, "Evening scene should be activated")
	t.Log("  ✓ Deactivation: evening scene activated")

	t.Log("✓ Full activation/deactivation cycle completed successfully")
}

// TestScenario_SexModeShadowState_TracksCorrectly tests that shadow state
// is properly maintained throughout the lifecycle
func TestScenario_SexModeShadowState_TracksCorrectly(t *testing.T) {
	server, sexModeManager, stateManager, cleanup := setupSexModeScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Initial state with music playing")

	require.NoError(t, stateManager.SetString("musicPlaybackType", "day"))
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: Shadow state should show inactive")

	shadowState := sexModeManager.GetShadowState()
	assert.False(t, shadowState.Outputs.IsActive, "IsActive should be false initially")

	t.Log("WHEN: Sex mode is activated")

	server.SetState("input_boolean.sex", "on", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: Shadow state should reflect activation")

	shadowState = sexModeManager.GetShadowState()
	assert.True(t, shadowState.Outputs.IsActive, "IsActive should be true after activation")
	assert.Equal(t, "activate", shadowState.Outputs.LastActionType, "LastActionType should be 'activate'")
	assert.Equal(t, "day", shadowState.Outputs.PreSexMusicType, "PreSexMusicType should be 'day'")
	assert.False(t, shadowState.Outputs.ActivatedAt.IsZero(), "ActivatedAt should be set")

	t.Log("  ✓ Shadow state correctly tracks activation")

	t.Log("WHEN: Sex mode is deactivated")

	server.SetState("input_boolean.sex", "off", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: Shadow state should reflect deactivation")

	shadowState = sexModeManager.GetShadowState()
	assert.False(t, shadowState.Outputs.IsActive, "IsActive should be false after deactivation")
	assert.Equal(t, "deactivate", shadowState.Outputs.LastActionType, "LastActionType should be 'deactivate'")

	t.Log("  ✓ Shadow state correctly tracks deactivation")
	t.Log("✓ Shadow state tracking verified")
}
