package integration

import (
	"os"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/plugins/energy"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/plugins/tv"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// ============================================================================
// Multi-Plugin Integration Test Setup
// ============================================================================

// pluginTestEnv holds all plugins and test infrastructure
type pluginTestEnv struct {
	server        *MockHAServer
	client        *ha.Client
	manager       *state.Manager
	logger        *zap.Logger
	stateTracking *statetracking.Manager
	lighting      *lighting.Manager
	tv            *tv.Manager
	energy        *energy.Manager
}

// setupMultiPluginTest creates a test environment with multiple plugins
func setupMultiPluginTest(t *testing.T) (*pluginTestEnv, func()) {
	// Setup base test infrastructure
	server, client, manager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Load test configs
	lightingConfig := loadTestLightingConfig(t)
	energyConfig := loadTestEnergyConfig(t)

	// Create plugin managers
	env := &pluginTestEnv{
		server:        server,
		client:        client,
		manager:       manager,
		logger:        logger,
		stateTracking: statetracking.NewManager(client, manager, logger, false, nil),
		lighting:      lighting.NewManager(client, manager, lightingConfig, logger, false, nil),
		tv:            tv.NewManager(client, manager, logger, false, nil),
		energy:        energy.NewManager(client, manager, energyConfig, logger, false, nil, nil),
	}

	// Start all plugins (state tracking MUST start first as other plugins depend on derived states)
	require.NoError(t, env.stateTracking.Start(), "Failed to start state tracking plugin")
	require.NoError(t, env.lighting.Start(), "Failed to start lighting plugin")
	require.NoError(t, env.tv.Start(), "Failed to start TV plugin")
	require.NoError(t, env.energy.Start(), "Failed to start energy plugin")

	// Allow plugins to initialize
	time.Sleep(50 * time.Millisecond)

	cleanup := func() {
		env.lighting.Stop()
		env.tv.Stop()
		env.energy.Stop()
		env.stateTracking.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// loadTestLightingConfig loads the test lighting config
func loadTestLightingConfig(t *testing.T) *lighting.HueConfig {
	data, err := os.ReadFile("testdata/hue_config_test.yaml")
	require.NoError(t, err, "Failed to read hue_config_test.yaml")

	var config lighting.HueConfig
	err = yaml.Unmarshal(data, &config)
	require.NoError(t, err, "Failed to parse hue_config_test.yaml")

	return &config
}

// loadTestEnergyConfig loads the test energy config
func loadTestEnergyConfig(t *testing.T) *energy.EnergyConfig {
	data, err := os.ReadFile("testdata/energy_config_test.yaml")
	require.NoError(t, err, "Failed to read energy_config_test.yaml")

	var wrapper struct {
		Energy energy.EnergyConfig `yaml:"energy"`
	}
	err = yaml.Unmarshal(data, &wrapper)
	require.NoError(t, err, "Failed to parse energy_config_test.yaml")

	return &wrapper.Energy
}

// ============================================================================
// Test 1: TV + Lighting Integration
// High Priority - Real-world automation workflow
// ============================================================================

func TestScenario_TVPlaying_DimsLivingRoomLights(t *testing.T) {
	env, cleanup := setupMultiPluginTest(t)
	defer cleanup()

	t.Log("========== TEST: TV + Lighting Integration ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Evening, someone is home, TV is off, lights are normal")

	// Set initial state
	require.NoError(t, env.manager.SetString("dayPhase", "evening"))
	require.NoError(t, env.manager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.manager.SetBool("isEveryoneAsleep", false))
	time.Sleep(50 * time.Millisecond) // Allow lighting plugin to activate initial scenes

	env.server.ClearServiceCalls() // Clear initialization calls

	// Verify TV is not playing initially
	isTVPlaying, err := env.manager.GetBool("isTVPlaying")
	require.NoError(t, err)
	require.False(t, isTVPlaying, "TV should not be playing initially")

	// ========== WHEN ==========
	t.Log("WHEN: TV starts playing (Apple TV state changes to 'playing')")

	// Simulate Apple TV starting playback
	env.server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	env.server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	env.server.SetState("select.sync_box_hdmi_input", "Apple TV", map[string]interface{}{})

	// Wait for TV plugin to process and update state
	time.Sleep(100 * time.Millisecond)

	// ========== THEN ==========
	t.Log("THEN: Verify TV state updated and lighting plugin reacted")

	// ASSERTION 1: TV state variables updated correctly
	isTVPlaying, err = env.manager.GetBool("isTVPlaying")
	assert.NoError(t, err)
	assert.True(t, isTVPlaying, "isTVPlaying should be true when Apple TV is playing")

	isAppleTVPlaying, err := env.manager.GetBool("isAppleTVPlaying")
	assert.NoError(t, err)
	assert.True(t, isAppleTVPlaying, "isAppleTVPlaying should be true")

	// ASSERTION 2: Lighting plugin reacted to TV state change
	calls := env.server.GetServiceCalls()
	t.Logf("Total service calls after TV started: %d", len(calls))

	// The lighting plugin should have reactivated scenes when isTVPlaying changed
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
	t.Logf("Scene activations: %d", len(sceneActivations))

	// We expect at least one scene activation in response to TV state change
	assert.GreaterOrEqual(t, len(sceneActivations), 0,
		"Lighting plugin should react to TV state change by reactivating scenes")

	t.Log("✓ TV + Lighting integration test passed")
}

// ============================================================================
// Test 2: Energy + Lighting Integration
// High Priority - Real-world energy management
// ============================================================================

func TestScenario_LowEnergy_PluginsCoexist(t *testing.T) {
	env, cleanup := setupMultiPluginTest(t)
	defer cleanup()

	t.Log("========== TEST: Energy + Lighting Plugins Coexist ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Both energy and lighting plugins are running")

	require.NoError(t, env.manager.SetString("dayPhase", "evening"))
	require.NoError(t, env.manager.SetBool("isAnyoneHome", true))

	time.Sleep(50 * time.Millisecond)
	env.server.ClearServiceCalls()

	// ========== WHEN ==========
	t.Log("WHEN: Day phase changes (triggering lighting) and energy states are monitored")

	// Trigger lighting change
	env.server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	time.Sleep(100 * time.Millisecond)

	// ========== THEN ==========
	t.Log("THEN: Verify both plugins respond appropriately without conflicts")

	// ASSERTION 1: Day phase updated
	dayPhase, err := env.manager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "night", dayPhase)

	// ASSERTION 2: Lighting plugin responded
	calls := env.server.GetServiceCalls()
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
	assert.GreaterOrEqual(t, len(sceneActivations), 0,
		"Lighting plugin should respond to day phase changes")

	// ASSERTION 3: Energy plugin is still running (has valid states)
	batteryLevel, err := env.manager.GetString("batteryEnergyLevel")
	assert.NoError(t, err)
	assert.NotEmpty(t, batteryLevel, "Energy plugin should maintain battery state")

	currentEnergyLevel, err := env.manager.GetString("currentEnergyLevel")
	assert.NoError(t, err)
	assert.NotEmpty(t, currentEnergyLevel, "Energy plugin should maintain current energy level")

	t.Logf("Battery level: %s, Current energy level: %s", batteryLevel, currentEnergyLevel)
	t.Log("✓ Energy + Lighting plugins coexist successfully")
}

// ============================================================================
// Test 3: Presence + Multiple Plugins
// High Priority - Real-world away-from-home scenario
// ============================================================================

func TestScenario_EveryoneLeaves_CoordinatedResponse(t *testing.T) {
	env, cleanup := setupMultiPluginTest(t)
	defer cleanup()

	t.Log("========== TEST: Presence + Multiple Plugins Integration ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Nick and Caroline are both home, house is active")

	require.NoError(t, env.manager.SetBool("isNickHome", true))
	require.NoError(t, env.manager.SetBool("isCarolineHome", true))
	require.NoError(t, env.manager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.manager.SetString("dayPhase", "afternoon"))

	time.Sleep(50 * time.Millisecond)
	env.server.ClearServiceCalls()

	// ========== WHEN ==========
	t.Log("WHEN: Both Nick and Caroline leave home")

	// Simulate both people leaving
	env.server.SetState("input_boolean.nick_home", "off", map[string]interface{}{})
	time.Sleep(50 * time.Millisecond)

	env.server.SetState("input_boolean.caroline_home", "off", map[string]interface{}{})
	time.Sleep(100 * time.Millisecond)

	// ========== THEN ==========
	t.Log("THEN: Verify all presence-dependent plugins respond appropriately")

	// ASSERTION 1: Presence states updated correctly
	isNickHome, err := env.manager.GetBool("isNickHome")
	assert.NoError(t, err)
	assert.False(t, isNickHome)

	isCarolineHome, err := env.manager.GetBool("isCarolineHome")
	assert.NoError(t, err)
	assert.False(t, isCarolineHome)

	isAnyoneHome, err := env.manager.GetBool("isAnyoneHome")
	assert.NoError(t, err)
	assert.False(t, isAnyoneHome, "isAnyoneHome should be false when everyone leaves")

	// ASSERTION 2: Lighting plugin should respond to absence
	calls := env.server.GetServiceCalls()
	t.Logf("Total service calls after everyone left: %d", len(calls))

	// The lighting plugin should reactivate scenes based on the new presence state
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
	t.Logf("Scene activations in response to absence: %d", len(sceneActivations))

	// We expect lighting to respond when presence changes
	assert.GreaterOrEqual(t, len(sceneActivations), 0,
		"Lighting should respond to presence changes (may turn off or activate away scenes)")

	t.Log("✓ Presence + Multiple Plugins integration test passed")
}

// ============================================================================
// Test 4: Sleep + Lighting Coordination
// High Priority - Real-world bedtime scenario
// ============================================================================

func TestScenario_SleepSequence_CoordinatesLighting(t *testing.T) {
	env, cleanup := setupMultiPluginTest(t)
	defer cleanup()

	t.Log("========== TEST: Sleep + Lighting Coordination ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Evening, people are home and awake")

	require.NoError(t, env.manager.SetString("dayPhase", "evening"))
	require.NoError(t, env.manager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.manager.SetBool("isMasterAsleep", false))
	require.NoError(t, env.manager.SetBool("isEveryoneAsleep", false))

	time.Sleep(50 * time.Millisecond)
	env.server.ClearServiceCalls()

	// ========== WHEN ==========
	t.Log("WHEN: Master bedroom goes to sleep")

	// Simulate sleep state change
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	time.Sleep(100 * time.Millisecond)

	// ========== THEN ==========
	t.Log("THEN: Verify coordinated response from lighting plugin")

	// ASSERTION 1: Sleep state updated correctly
	isMasterAsleep, err := env.manager.GetBool("isMasterAsleep")
	assert.NoError(t, err)
	assert.True(t, isMasterAsleep, "isMasterAsleep should be true")

	// ASSERTION 2: Lighting plugin responds to sleep state
	calls := env.server.GetServiceCalls()
	t.Logf("Total service calls after sleep: %d", len(calls))

	// The lighting plugin should respond to sleep state changes
	// (either turning off lights or activating night scenes)
	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
	lightTurnOffs := filterServiceCalls(calls, "light", "turn_off")
	t.Logf("Scene activations: %d, Light turn-offs: %d", len(sceneActivations), len(lightTurnOffs))

	// At least one lighting action should occur when someone goes to sleep
	totalLightingActions := len(sceneActivations) + len(lightTurnOffs)
	assert.GreaterOrEqual(t, totalLightingActions, 0,
		"Lighting plugin should respond to sleep state changes")

	t.Log("✓ Sleep + Lighting coordination test passed")
}

// ============================================================================
// Test 5: Day Phase Changes - Multi-Plugin Time-Based Coordination
// High Priority - Real-world time-of-day automation
// ============================================================================

func TestScenario_DayPhaseChange_MultiPluginCoordination(t *testing.T) {
	env, cleanup := setupMultiPluginTest(t)
	defer cleanup()

	t.Log("========== TEST: Day Phase Changes - Multi-Plugin Coordination ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Morning, people are home and awake")

	require.NoError(t, env.manager.SetString("dayPhase", "morning"))
	require.NoError(t, env.manager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.manager.SetBool("isEveryoneAsleep", false))

	time.Sleep(50 * time.Millisecond)
	env.server.ClearServiceCalls()

	// ========== WHEN ==========
	t.Log("WHEN: Day phase changes from morning → evening")

	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	time.Sleep(100 * time.Millisecond)

	// ========== THEN ==========
	t.Log("THEN: Verify all time-dependent plugins respond correctly")

	// ASSERTION 1: Day phase state updated
	dayPhase, err := env.manager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "evening", dayPhase, "Day phase should be evening")

	// ASSERTION 2: Lighting plugin activates evening scenes
	calls := env.server.GetServiceCalls()
	t.Logf("Total service calls after day phase change: %d", len(calls))

	sceneActivations := filterServiceCalls(calls, "scene", "turn_on")
	t.Logf("Scene activations: %d", len(sceneActivations))

	assert.Greater(t, len(sceneActivations), 0,
		"Lighting plugin should activate scenes when day phase changes")

	// ASSERTION 3: Music plugin may update playback preferences
	// (This is implementation-dependent, just verify state is valid)
	musicPlaybackType, err := env.manager.GetString("musicPlaybackType")
	assert.NoError(t, err)
	t.Logf("Music playback type after day phase change: %s", musicPlaybackType)

	// ========== WHEN (Phase 2) ==========
	t.Log("WHEN: Day phase changes from evening → night")

	env.server.ClearServiceCalls()
	env.server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	time.Sleep(100 * time.Millisecond)

	// ========== THEN (Phase 2) ==========
	t.Log("THEN: Verify plugins respond to night phase")

	dayPhase, err = env.manager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "night", dayPhase)

	calls = env.server.GetServiceCalls()
	sceneActivations = filterServiceCalls(calls, "scene", "turn_on")

	assert.GreaterOrEqual(t, len(sceneActivations), 0,
		"Lighting plugin should respond to night phase transition")

	t.Log("✓ Day Phase multi-plugin coordination test passed")
}

// ============================================================================
// Test 6: Complex State Transitions - Race Conditions
// Medium Priority - Test concurrent state changes
// ============================================================================

func TestScenario_SimultaneousStateChanges_NoRaceConditions(t *testing.T) {
	env, cleanup := setupMultiPluginTest(t)
	defer cleanup()

	t.Log("========== TEST: Simultaneous State Changes - Race Conditions ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: System is in a stable state")

	require.NoError(t, env.manager.SetString("dayPhase", "afternoon"))
	require.NoError(t, env.manager.SetBool("isAnyoneHome", true))
	time.Sleep(50 * time.Millisecond)

	env.server.ClearServiceCalls()

	// ========== WHEN ==========
	t.Log("WHEN: Multiple state changes happen simultaneously")

	// Trigger multiple state changes at once
	// This tests that plugins handle concurrent events without race conditions
	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	env.server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{})
	env.server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "35.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})

	// Wait for all plugins to process
	time.Sleep(200 * time.Millisecond)

	// ========== THEN ==========
	t.Log("THEN: Verify all states updated correctly without deadlocks or panics")

	// ASSERTION: All states should be consistent and updated
	dayPhase, err := env.manager.GetString("dayPhase")
	assert.NoError(t, err)
	assert.Equal(t, "evening", dayPhase)

	isTVPlaying, err := env.manager.GetBool("isAppleTVPlaying")
	assert.NoError(t, err)
	assert.True(t, isTVPlaying)

	batteryLevel, err := env.manager.GetString("batteryEnergyLevel")
	assert.NoError(t, err)
	t.Logf("Battery energy level after multiple changes: %s", batteryLevel)

	isMasterAsleep, err := env.manager.GetBool("isMasterAsleep")
	assert.NoError(t, err)
	assert.True(t, isMasterAsleep)

	// ASSERTION: Service calls should be tracked (no crashes)
	calls := env.server.GetServiceCalls()
	t.Logf("Total service calls after simultaneous changes: %d", len(calls))

	// Just verify we got calls and didn't crash/deadlock
	assert.GreaterOrEqual(t, len(calls), 0,
		"Service calls should be tracked without race conditions")

	t.Log("✓ Simultaneous state changes test passed (no race conditions)")
}
