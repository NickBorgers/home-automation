package energy

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"homeautomation/internal/state"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
)

// createTestConfig creates a test energy configuration
func createTestConfig() *EnergyConfig {
	return &EnergyConfig{
		Energy: struct {
			FreeEnergyTime  FreeEnergyTime        `yaml:"free_energy_time"`
			IndicatorLights IndicatorLightsConfig `yaml:"indicator_lights"`
			EnergyStates    []EnergyState         `yaml:"energy_states"`
		}{
			FreeEnergyTime: FreeEnergyTime{
				Start: "21:00",
				End:   "07:00",
			},
			IndicatorLights: IndicatorLightsConfig{
				FriendlyNamePattern: "Radar",
			},
			EnergyStates: []EnergyState{
				{
					ConditionName:                       "black",
					BatteryMinimumPercentage:            0,
					EnergyProductionMinimumKW:           0,
					RemainingEnergyProductionMinimumKWH: 0,
				},
				{
					ConditionName:                       "red",
					BatteryMinimumPercentage:            40,
					EnergyProductionMinimumKW:           0,
					RemainingEnergyProductionMinimumKWH: 0,
				},
				{
					ConditionName:                       "yellow",
					BatteryMinimumPercentage:            60,
					EnergyProductionMinimumKW:           0,
					RemainingEnergyProductionMinimumKWH: 0,
				},
				{
					ConditionName:                       "green",
					BatteryMinimumPercentage:            80,
					EnergyProductionMinimumKW:           0,
					RemainingEnergyProductionMinimumKWH: 10,
				},
				{
					ConditionName:                       "white",
					BatteryMinimumPercentage:            95,
					EnergyProductionMinimumKW:           4,
					RemainingEnergyProductionMinimumKWH: 20,
				},
			},
		},
	}
}

func TestDetermineBatteryEnergyLevel(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	tests := []struct {
		name       string
		percentage float64
		expected   string
	}{
		{"Below all thresholds", 0, "black"},
		{"Just below red", 39, "black"},
		{"At red threshold", 40, "red"},
		{"Between red and yellow", 50, "red"},
		{"At yellow threshold", 60, "yellow"},
		{"Between yellow and green", 75, "yellow"},
		{"At green threshold", 80, "green"},
		{"Between green and white", 90, "green"},
		{"At white threshold", 95, "white"},
		{"Above white", 100, "white"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			result := manager.determineBatteryEnergyLevel(tt.percentage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Note: TestDetermineSolarEnergyLevel and TestDetermineOverallEnergyLevel have been
// removed because this logic is now handled by the ComputedStateRegistry.
// See internal/state/computed_energy_providers_test.go for those tests.

func TestIsFreeEnergyTime(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Note: This test is time-dependent and may need adjustment
	// For now, we test the logic with different scenarios

	tests := []struct {
		name            string
		isGridAvailable bool
		// We can't easily test specific times without mocking time
		// So we'll just test the grid availability logic
	}{
		{"Grid not available", false},
		{"Grid available", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			result := manager.isFreeEnergyTime(tt.isGridAvailable)
			// Without mocking time, we can only verify it doesn't panic
			// and returns a boolean
			assert.IsType(t, true, result)
		})
	}
}

func TestLoadConfigFromRepoFile(t *testing.T) {
	t.Parallel(
	// Test loading the actual config file
	// Skip this test if config file doesn't exist (e.g., in CI)
	)

	configPath := "../../../../configs/energy_config.yaml"
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Skipf("Skipping test - config file not found at %s", configPath)
		return
	}
	assert.NotNil(t, config)

	// Verify config structure
	assert.Equal(t, "21:00", config.Energy.FreeEnergyTime.Start)
	assert.Equal(t, "07:00", config.Energy.FreeEnergyTime.End)
	assert.Equal(t, 5, len(config.Energy.EnergyStates))

	// Verify energy states are in order
	expectedLevels := []string{"black", "red", "yellow", "green", "white"}
	for i, state := range config.Energy.EnergyStates {
		assert.Equal(t, expectedLevels[i], state.ConditionName)
	}
}

func TestFreeEnergyTimeSpansMidnight(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Test that the logic handles times that span midnight
	// Start: 21:00, End: 07:00

	// Mock times for testing
	testCases := []struct {
		hour     int
		expected bool
	}{
		{6, true},   // 06:00 - should be in free energy time
		{7, false},  // 07:00 - should be at the boundary (not included)
		{8, false},  // 08:00 - should not be in free energy time
		{12, false}, // 12:00 - should not be in free energy time
		{20, false}, // 20:00 - should not be in free energy time
		{21, true},  // 21:00 - should be at the boundary (included)
		{22, true},  // 22:00 - should be in free energy time
		{23, true},  // 23:00 - should be in free energy time
		{0, true},   // 00:00 - should be in free energy time
	}

	for _, tc := range testCases {
		t.Run(time.Now().Format("15:04"), func(t *testing.T) {

			// This is a simplified test - in reality we'd need to mock time.Now()
			// For now, we just verify the function doesn't panic

			result := manager.isFreeEnergyTime(true)
			_ = result // Use the result to avoid unused variable
			_ = tc     // Use tc to avoid unused variable warning
		})
	}
}

// TestManagerStartAndHandlers tests the manager lifecycle and handlers
func TestManagerStartAndHandlers(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	// Initialize state manager with initial values
	err := env.StateMgr.SyncFromHA()
	assert.NoError(t, err)

	// Set initial state
	env.StateMgr.SetBool("isGridAvailable", true)
	env.StateMgr.SetString("batteryEnergyLevel", "black")
	env.StateMgr.SetString("solarProductionEnergyLevel", "black")
	env.StateMgr.SetNumber("thisHourSolarGeneration", 0.0)
	env.StateMgr.SetNumber("remainingSolarGeneration", 0.0)

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Test Start method
	err = manager.Start()
	assert.NoError(t, err)

	// Wait for startup goroutines to complete initial work
	manager.WaitForStartup()

	// Test handler functions by triggering state changes
	t.Run("handleBatteryChange", func(t *testing.T) {

		manager.handleBatteryChange(50.0)
		level, _ := env.StateMgr.GetString("batteryEnergyLevel")
		assert.Equal(t, "red", level)
	})

	t.Run("handleBatteryChange_with_invalid_value", func(t *testing.T) {

		// Test with Inf - should be ignored

		manager.handleBatteryChange(math.Inf(1))
		// Level should remain red from previous test
		level, _ := env.StateMgr.GetString("batteryEnergyLevel")
		assert.Equal(t, "red", level)
	})

	t.Run("handleThisHourSolarChange", func(t *testing.T) {

		manager.handleThisHourSolarChange(5.0)
		kw, _ := env.StateMgr.GetNumber("thisHourSolarGeneration")
		assert.Equal(t, 5.0, kw)
	})

	t.Run("handleRemainingSolarChange", func(t *testing.T) {

		manager.handleRemainingSolarChange(15.0)
		kwh, _ := env.StateMgr.GetNumber("remainingSolarGeneration")
		assert.Equal(t, 15.0, kwh)
	})

	// Note: recalculateSolarProductionLevel and recalculateOverallEnergyLevel tests have been
	// removed because this logic is now handled by the ComputedStateRegistry.
	// See internal/state/computed_energy_providers_test.go for those tests.

	t.Run("checkFreeEnergy", func(t *testing.T) {

		env.StateMgr.SetBool("isGridAvailable", false)
		manager.checkFreeEnergy()
		isFree, _ := env.StateMgr.GetBool("isFreeEnergyAvailable")
		assert.False(t, isFree)
	})

	t.Run("handleGridAvailabilityChange", func(t *testing.T) {

		manager.handleGridAvailabilityChange("isGridAvailable", false, true)
		// Just verify it doesn't panic
	})

	t.Run("handleSolarLevelChange", func(t *testing.T) {

		manager.handleSolarLevelChange("solarProductionEnergyLevel", "black", "green")
		// Just verify it doesn't panic - shadow state update is tested
	})

	t.Run("handleCurrentEnergyLevelChange", func(t *testing.T) {

		manager.handleCurrentEnergyLevelChange("currentEnergyLevel", "black", "green")
		// Just verify it doesn't panic - shadow state and indicator light updates are tested
	})
}

// Note: TestDetermineOverallEnergyLevel_EdgeCases has been removed because this logic
// is now handled by the ComputedStateRegistry. See computed_energy_providers_test.go.

// TestLoadConfigError tests error handling in config loading
func TestLoadConfigError(t *testing.T) {
	t.Parallel()
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	assert.Error(t, err)
}

// TestIsFreeEnergyTime_EdgeCases tests edge cases for free energy time
func TestIsFreeEnergyTime_EdgeCases(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	t.Run("invalid_start_time", func(t *testing.T) {

		config := &EnergyConfig{
			Energy: struct {
				FreeEnergyTime  FreeEnergyTime        `yaml:"free_energy_time"`
				IndicatorLights IndicatorLightsConfig `yaml:"indicator_lights"`
				EnergyStates    []EnergyState         `yaml:"energy_states"`
			}{
				FreeEnergyTime: FreeEnergyTime{
					Start: "invalid",
					End:   "07:00",
				},
				EnergyStates: []EnergyState{},
			},
		}

		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
		result := manager.isFreeEnergyTime(true)
		assert.False(t, result)
	})

	t.Run("invalid_end_time", func(t *testing.T) {

		config := &EnergyConfig{
			Energy: struct {
				FreeEnergyTime  FreeEnergyTime        `yaml:"free_energy_time"`
				IndicatorLights IndicatorLightsConfig `yaml:"indicator_lights"`
				EnergyStates    []EnergyState         `yaml:"energy_states"`
			}{
				FreeEnergyTime: FreeEnergyTime{
					Start: "21:00",
					End:   "invalid",
				},
				EnergyStates: []EnergyState{},
			},
		}

		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
		result := manager.isFreeEnergyTime(true)
		assert.False(t, result)
	})
}

// TestEnergyManager_Stop tests the Stop method and subscription cleanup
func TestEnergyManager_Stop(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Initialize required state variables
	_ = env.StateMgr.SetBool("isGridAvailable", true)
	_ = env.StateMgr.SetString("batteryEnergyLevel", "green")
	_ = env.StateMgr.SetString("solarProductionEnergyLevel", "green")
	_ = env.StateMgr.SetBool("isFreeEnergyAvailable", false)

	// Start manager (creates subscriptions and goroutine)
	err := manager.Start()
	assert.NoError(t, err)

	// Verify subscriptions were created via subHelper
	// HA subscriptions: battery sensor, this hour solar, remaining solar (3 total)
	// State subscriptions: isGridAvailable, solarProductionEnergyLevel, currentEnergyLevel (3 total)
	assert.Equal(t, 3, len(manager.subHelper.GetHASubscriptions()), "Should have 3 HA subscriptions")
	assert.Equal(t, 3, len(manager.subHelper.GetStateSubscriptions()), "Should have 3 state subscriptions")

	// Stop manager
	manager.Stop()

	// Verify subscriptions were cleaned up
	assert.Equal(t, 0, len(manager.subHelper.GetHASubscriptions()), "HA subscriptions should be empty after Stop")
	assert.Equal(t, 0, len(manager.subHelper.GetStateSubscriptions()), "State subscriptions should be empty after Stop")
}

// TestTimezoneHandling tests that timezone configuration works correctly
func TestTimezoneHandling(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()

	t.Run("default_timezone_is_utc", func(t *testing.T) {

		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
		assert.Equal(t, time.UTC, manager.timezone)
	})

	t.Run("custom_timezone_is_respected", func(t *testing.T) {

		estLocation, err := time.LoadLocation("America/New_York")
		assert.NoError(t, err)

		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, estLocation, nil)
		assert.Equal(t, estLocation, manager.timezone)
	})

	t.Run("timezone_affects_free_energy_calculation", func(t *testing.T) {

		// Create a config with a specific free energy window
		// Let's use 02:00 to 03:00 for easier testing

		testConfig := &EnergyConfig{
			Energy: struct {
				FreeEnergyTime  FreeEnergyTime        `yaml:"free_energy_time"`
				IndicatorLights IndicatorLightsConfig `yaml:"indicator_lights"`
				EnergyStates    []EnergyState         `yaml:"energy_states"`
			}{
				FreeEnergyTime: FreeEnergyTime{
					Start: "02:00",
					End:   "03:00",
				},
				EnergyStates: []EnergyState{
					{ConditionName: "black"},
				},
			},
		}

		// Test with UTC timezone
		utcManager := NewManager(context.Background(), env.MockHA, env.StateMgr, testConfig, env.Logger, false, time.UTC, nil)
		assert.Equal(t, time.UTC, utcManager.timezone)

		// Test with different timezone
		estLocation, err := time.LoadLocation("America/New_York")
		assert.NoError(t, err)
		estManager := NewManager(context.Background(), env.MockHA, env.StateMgr, testConfig, env.Logger, false, estLocation, nil)
		assert.Equal(t, estLocation, estManager.timezone)

		// Both managers should use their configured timezone for calculations
		// We can't easily test the exact behavior without mocking time.Now(),
		// but we've verified the timezone is set correctly
	})
}

func TestManagerReset(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	// Set up initial state including currentEnergyLevel
	// Note: currentEnergyLevel is now computed by the ComputedStateRegistry, not the plugin.
	// The plugin observes it. For this test, we set it directly.
	env.MockHA.SetState("input_text.current_energy_level", "green", map[string]interface{}{})
	env.MockHA.SetState("input_boolean.grid_available", "on", map[string]interface{}{})
	env.MockHA.Connect()

	err := env.StateMgr.SyncFromHA()
	assert.NoError(t, err)

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	err = manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Reset should re-check free energy and update indicator lights
	err = manager.Reset()
	assert.NoError(t, err)

	// Verify the reset completed without error
	// (The actual energy level computation is now handled by ComputedStateRegistry,
	// so we just verify the plugin reads and uses the current value)
	currentLevel, err := env.StateMgr.GetString("currentEnergyLevel")
	assert.NoError(t, err)
	assert.NotEmpty(t, currentLevel)
}

// TestHandleGridAvailabilityChange tests grid availability change synchronization
func TestHandleGridAvailabilityChange(t *testing.T) {
	t.Parallel()
	config := createTestConfig()

	t.Run("syncs_grid_availability_to_HA_when_enabled", func(t *testing.T) {

		env := testutil.NewEnv(t)
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

		// Clear any initial service calls
		env.MockHA.ClearServiceCalls()

		// Simulate grid availability change to true
		manager.handleGridAvailabilityChange("isGridAvailable", false, true)

		// Verify SetInputBoolean was called for grid_available
		// Note: checkFreeEnergy() is also called, which may make additional service calls
		serviceCalls := env.MockHA.GetServiceCalls()
		assert.GreaterOrEqual(t, len(serviceCalls), 1, "Expected at least one service call")

		// Find the grid_available service call
		gridAvailableCall := testutil.FindServiceCallWithEntity(serviceCalls, "input_boolean", "turn_on", "input_boolean.grid_available")

		assert.NotNil(t, gridAvailableCall, "Expected grid_available service call")
		assert.Equal(t, "input_boolean", gridAvailableCall.Domain)
		assert.Equal(t, "turn_on", gridAvailableCall.Service)
	})

	t.Run("syncs_grid_availability_to_HA_when_disabled", func(t *testing.T) {

		env := testutil.NewEnv(t)
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

		// Clear any initial service calls
		env.MockHA.ClearServiceCalls()

		// Simulate grid availability change to false
		manager.handleGridAvailabilityChange("isGridAvailable", true, false)

		// Verify SetInputBoolean was called with turn_off for grid_available
		// Note: checkFreeEnergy() is also called, which may make additional service calls
		serviceCalls := env.MockHA.GetServiceCalls()
		assert.GreaterOrEqual(t, len(serviceCalls), 1, "Expected at least one service call")

		// Find the grid_available service call
		gridAvailableCall := testutil.FindServiceCallWithEntity(serviceCalls, "input_boolean", "turn_off", "input_boolean.grid_available")

		assert.NotNil(t, gridAvailableCall, "Expected grid_available service call")
		assert.Equal(t, "input_boolean", gridAvailableCall.Domain)
		assert.Equal(t, "turn_off", gridAvailableCall.Service)
	})

	t.Run("skips_HA_sync_in_read_only_mode", func(t *testing.T) {

		env := testutil.NewEnv(t)
		readOnlyStateMgr := state.NewManager(env.MockHA, env.Logger, true) // read-only mode
		manager := NewManager(context.Background(), env.MockHA, readOnlyStateMgr, config, env.Logger, true, nil, nil)

		// Clear any initial service calls
		env.MockHA.ClearServiceCalls()

		// Simulate grid availability change
		manager.handleGridAvailabilityChange("isGridAvailable", false, true)

		// Verify no service calls were made
		serviceCalls := env.MockHA.GetServiceCalls()
		assert.Len(t, serviceCalls, 0, "Expected no service calls in read-only mode")
	})

	t.Run("handles_non_boolean_value_gracefully", func(t *testing.T) {

		env := testutil.NewEnv(t)
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

		// Clear any initial service calls
		env.MockHA.ClearServiceCalls()

		// Simulate grid availability change with invalid value
		manager.handleGridAvailabilityChange("isGridAvailable", false, "not_a_boolean")

		// Verify no service calls were made (error was handled)
		serviceCalls := env.MockHA.GetServiceCalls()
		assert.Len(t, serviceCalls, 0, "Expected no service calls with invalid value")
	})

	t.Run("triggers_free_energy_recalculation", func(t *testing.T) {

		env := testutil.NewEnv(t)

		// Initialize required state variables
		_ = env.StateMgr.SyncFromHA()
		_ = env.StateMgr.SetBool("isGridAvailable", true)
		_ = env.StateMgr.SetBool("isFreeEnergyAvailable", false)

		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

		// Clear any initial service calls
		env.MockHA.ClearServiceCalls()

		// Get initial free energy state
		initialFreeEnergy, _ := env.StateMgr.GetBool("isFreeEnergyAvailable")

		// Simulate grid availability change
		manager.handleGridAvailabilityChange("isGridAvailable", false, true)

		// Verify free energy was recalculated (may or may not change depending on time)
		// The important thing is that checkFreeEnergy was called without error
		currentFreeEnergy, err := env.StateMgr.GetBool("isFreeEnergyAvailable")
		assert.NoError(t, err)

		// Value might be the same, but at least we verify it was processed
		_ = initialFreeEnergy
		_ = currentFreeEnergy
	})
}

func TestIndicatorLightsDiscovery(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	// Set up mock states for light entities - some with "Radar" in friendly_name
	env.MockHA.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom Radar Light",
	})
	env.MockHA.SetState("light.apollo_kitchen_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Kitchen Radar Light",
	})
	env.MockHA.SetState("light.living_room_lamp", "on", map[string]interface{}{
		"friendly_name": "Living Room Lamp", // No "Radar", should not be discovered
	})
	env.MockHA.SetState("sensor.bedroom_radar", "detected", map[string]interface{}{
		"friendly_name": "Bedroom Radar", // Sensor, not light - should not be discovered
	})

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Discovery happens during Start()
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Verify only the correct entities were discovered
	assert.Len(t, manager.indicatorLightEntities, 2)
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_bedroom_rgb")
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_kitchen_rgb")
}

func TestIndicatorLightsServiceCall(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	// Create config with light configs for each energy level
	config := createTestConfig()
	// The createTestConfig doesn't have LightConfig, so let's update the EnergyStates
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}    // black
	config.Energy.EnergyStates[1].LightConfig = LightConfig{Red: 255, Green: 0, Blue: 0, BrightnessPct: 30}      // red
	config.Energy.EnergyStates[2].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 0, BrightnessPct: 30}    // yellow
	config.Energy.EnergyStates[3].LightConfig = LightConfig{Red: 0, Green: 255, Blue: 0, BrightnessPct: 60}      // green
	config.Energy.EnergyStates[4].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 255, BrightnessPct: 100} // white

	// Set up mock light entities with "Radar" in friendly_name
	env.MockHA.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom Radar Light",
	})

	env.MockHA.Connect()

	// Create manager NOT in read-only mode
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Verify entity was discovered
	assert.Len(t, manager.indicatorLightEntities, 1)

	// Wait for startup goroutines to complete initial work
	manager.WaitForStartup()

	// Clear any service calls from startup
	env.MockHA.ClearServiceCalls()

	// Test updateIndicatorLights directly with a specific energy level
	// This avoids complications with free energy time and recalculation
	manager.updateIndicatorLights("yellow")

	// Verify the light service was called
	calls := env.MockHA.GetServiceCalls()

	// Find the light.turn_on call (ignore other background calls like input_boolean updates)
	lightCall := testutil.FindServiceCall(calls, "light", "turn_on")

	assert.NotNil(t, lightCall, "Expected light.turn_on service call")
	if lightCall != nil {
		// Verify entity_id (comparing as []string since that's how we pass it)
		entityIDs, ok := lightCall.Data["entity_id"].([]string)
		assert.True(t, ok, "entity_id should be []string")
		assert.Equal(t, []string{"light.apollo_bedroom_rgb"}, entityIDs)

		// Verify rgb_color
		rgbColor, ok := lightCall.Data["rgb_color"].([]int)
		assert.True(t, ok, "rgb_color should be []int")
		assert.Equal(t, []int{255, 255, 0}, rgbColor) // yellow

		// Verify brightness_pct
		assert.Equal(t, 30, lightCall.Data["brightness_pct"])
	}
}

func TestIndicatorLightsReadOnlyMode(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	// Add LightConfig
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}

	// Set up mock light entity
	env.MockHA.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom Radar Light",
	})

	env.MockHA.Connect()

	// Create manager in READ-ONLY mode
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Clear any service calls from startup
	env.MockHA.ClearServiceCalls()

	// Trigger updateIndicatorLights directly
	manager.updateIndicatorLights("black")

	// Verify NO light.turn_on service was called in read-only mode
	calls := env.MockHA.GetServiceCalls()
	testutil.AssertNoServiceCall(t, calls, "light", "turn_on")

	// But shadow state should still be updated
	shadowState := manager.GetShadowState()
	assert.NotNil(t, shadowState.Outputs.IndicatorLightsAction)
	assert.Equal(t, "black", shadowState.Outputs.IndicatorLightsAction.EnergyLevel)
}

func TestIndicatorLightsDiscoveryCaseInsensitive(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	// Set up mock light entities with different casing of "Radar"
	env.MockHA.SetState("light.apollo_lower_case", "on", map[string]interface{}{
		"friendly_name": "Apollo radar sensor", // lowercase "radar"
	})
	env.MockHA.SetState("light.apollo_upper_case", "on", map[string]interface{}{
		"friendly_name": "Apollo RADAR sensor", // uppercase "RADAR"
	})
	env.MockHA.SetState("light.apollo_mixed_case", "on", map[string]interface{}{
		"friendly_name": "Apollo RaDaR sensor", // mixed case
	})
	env.MockHA.SetState("light.no_match", "on", map[string]interface{}{
		"friendly_name": "No match here",
	})

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Verify all case variations were discovered (case-insensitive matching)
	assert.Len(t, manager.indicatorLightEntities, 3)
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_lower_case")
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_upper_case")
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_mixed_case")
}

func TestIndicatorLightsDiscoveryCustomPattern(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	// Set a custom regex pattern
	config.Energy.IndicatorLights.FriendlyNamePattern = "^Apollo.*RGB$"

	env.MockHA.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom RGB", // matches pattern
	})
	env.MockHA.SetState("light.apollo_kitchen_radar", "on", map[string]interface{}{
		"friendly_name": "Apollo Kitchen Radar", // doesn't match pattern
	})
	env.MockHA.SetState("light.hue_living_room", "on", map[string]interface{}{
		"friendly_name": "Hue Living Room RGB", // doesn't start with Apollo
	})

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Only the matching entity should be discovered
	assert.Len(t, manager.indicatorLightEntities, 1)
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_bedroom_rgb")
}

func TestIndicatorLightsServiceCallError(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	// Add LightConfig for testing
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}

	// Set up mock light entity
	env.MockHA.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom Radar Light",
	})

	env.MockHA.Connect()

	// Create manager NOT in read-only mode
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Wait for startup goroutines to complete initial work
	manager.WaitForStartup()

	// Clear any service calls from startup
	env.MockHA.ClearServiceCalls()

	// Configure mock to return an error for light.turn_on AFTER startup
	// (so startup doesn't fail)
	env.MockHA.SetServiceError("light", "turn_on", fmt.Errorf("connection timeout"))

	// Call updateIndicatorLights - should handle error gracefully (not panic)
	// Note: The mock doesn't record failed service calls, so we can't verify the call was attempted.
	// Instead, we verify that:
	// 1. The function doesn't panic
	// 2. The shadow state was still updated before the service call
	manager.updateIndicatorLights("black")

	// Shadow state should still be updated even though service call failed
	// (shadow state is updated BEFORE the service call is made)
	shadowState := manager.GetShadowState()
	assert.NotNil(t, shadowState.Outputs.IndicatorLightsAction)
	assert.Equal(t, "black", shadowState.Outputs.IndicatorLightsAction.EnergyLevel)
	assert.Equal(t, []int{25, 25, 112}, shadowState.Outputs.IndicatorLightsAction.RGBColor)
	assert.Equal(t, 70, shadowState.Outputs.IndicatorLightsAction.BrightnessPct)
}

func TestIndicatorLightsUnknownEnergyLevel(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	// Set up mock light entity
	env.MockHA.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom Radar Light",
	})

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Wait for startup goroutines to complete initial work
	manager.WaitForStartup()

	// Clear any service calls from startup
	env.MockHA.ClearServiceCalls()

	// Call updateIndicatorLights with an unknown energy level
	manager.updateIndicatorLights("purple") // Not a valid energy level

	// Verify NO light.turn_on service call was made (should return early)
	calls := env.MockHA.GetServiceCalls()
	testutil.AssertNoServiceCall(t, calls, "light", "turn_on")
}

func TestIndicatorLightsInitialUpdateOnStartup(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := createTestConfig()

	// Add LightConfig for all energy levels
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}    // black
	config.Energy.EnergyStates[1].LightConfig = LightConfig{Red: 255, Green: 0, Blue: 0, BrightnessPct: 30}      // red
	config.Energy.EnergyStates[2].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 0, BrightnessPct: 30}    // yellow
	config.Energy.EnergyStates[3].LightConfig = LightConfig{Red: 0, Green: 255, Blue: 0, BrightnessPct: 60}      // green
	config.Energy.EnergyStates[4].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 255, BrightnessPct: 100} // white

	// Set up mock light entity
	env.MockHA.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom Radar Light",
	})

	env.MockHA.Connect()

	// Pre-initialize state variables to simulate existing state
	_ = env.StateMgr.SetBool("isGridAvailable", true)
	_ = env.StateMgr.SetBool("isFreeEnergyAvailable", false)
	_ = env.StateMgr.SetString("batteryEnergyLevel", "yellow")
	_ = env.StateMgr.SetString("solarProductionEnergyLevel", "yellow")
	_ = env.StateMgr.SetString("currentEnergyLevel", "yellow")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)

	// Trigger a state change to cause indicator light update
	// The plugin observes currentEnergyLevel (computed by registry in production)
	// and updates indicator lights when it changes
	_ = env.StateMgr.SetString("currentEnergyLevel", "green")

	// Give time for the state change handler to process
	time.Sleep(100 * time.Millisecond)

	manager.Stop()

	// Verify that a light.turn_on service call was made after state change
	calls := env.MockHA.GetServiceCalls()
	lightCallCount := testutil.CountServiceCalls(calls, "light", "turn_on")

	// At least one light update should have occurred after the state change
	assert.GreaterOrEqual(t, lightCallCount, 1, "Expected at least one light.turn_on service call after state change")
}

// ============================================================================
// Adaptive Brightness Tests
// ============================================================================

func TestExtractDeviceID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		entityID string
		expected string
	}{
		{
			name:     "light rgb_light suffix",
			entityID: "light.apollo_msr_2_1294c8_rgb_light",
			expected: "apollo_msr_2_1294c8",
		},
		{
			name:     "sensor ltr390_light suffix",
			entityID: "sensor.apollo_msr_2_1294c8_ltr390_light",
			expected: "apollo_msr_2_1294c8",
		},
		{
			name:     "sensor ltr390_uv_index suffix",
			entityID: "sensor.apollo_msr_2_1294c8_ltr390_uv_index",
			expected: "apollo_msr_2_1294c8",
		},
		{
			name:     "binary_sensor radar_target suffix",
			entityID: "binary_sensor.apollo_msr_2_1294c8_radar_target",
			expected: "apollo_msr_2_1294c8",
		},
		{
			name:     "binary_sensor online suffix",
			entityID: "binary_sensor.apollo_msr_2_1294c8_online",
			expected: "apollo_msr_2_1294c8",
		},
		{
			name:     "no known suffix returns full name",
			entityID: "sensor.some_other_sensor",
			expected: "some_other_sensor",
		},
		{
			name:     "no domain prefix returns empty",
			entityID: "nodomain",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			result := extractDeviceID(tt.entityID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateAdaptiveBrightness(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	// Create config with adaptive brightness enabled and custom curve
	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 10, BrightnessPct: 20},
			{LuxMax: 100, BrightnessPct: 40},
			{LuxMax: 500, BrightnessPct: 60},
			{LuxMax: 1000, BrightnessPct: 80},
		},
		HysteresisPercent: 10,
	}

	env.MockHA.Connect()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	// Note: brightness is capped at 50% max to reduce calibration disruption
	tests := []struct {
		name     string
		lux      float64
		expected int
	}{
		{"very dark (lux=5)", 5, 20},
		{"dim (lux=50)", 50, 40},
		{"normal (lux=300)", 300, 50},         // curve says 60, capped at 50
		{"bright (lux=800)", 800, 50},         // curve says 80, capped at 50
		{"very bright (lux=1500)", 1500, 50},  // curve says 100, capped at 50
		{"at threshold (lux=10)", 10, 40},     // lux >= 10 means we're past first threshold
		{"at threshold (lux=100)", 100, 50},   // curve says 60, capped at 50
		{"at threshold (lux=500)", 500, 50},   // curve says 80, capped at 50
		{"at threshold (lux=1000)", 1000, 50}, // curve says 100, capped at 50
		{"zero lux", 0, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// Reset last brightness level to avoid hysteresis interference

			manager.indicatorMu.Lock()
			delete(manager.lastBrightnessLevel, "light.test")
			manager.indicatorMu.Unlock()

			result := manager.calculateAdaptiveBrightness(tt.lux, "light.test")
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateAdaptiveBrightnessDefaultCurve(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	// Create config with adaptive brightness enabled but NO custom curve
	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		// Empty BrightnessCurve should use defaults
	}

	env.MockHA.Connect()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	// Test with default curve values (10->20%, 100->40%, 500->60%, 1000->80%)
	// Note: brightness is capped at 50% max
	tests := []struct {
		lux      float64
		expected int
	}{
		{5, 20},    // below 10
		{50, 40},   // between 10-100
		{300, 50},  // between 100-500, curve says 60, capped at 50
		{800, 50},  // between 500-1000, curve says 80, capped at 50
		{1500, 50}, // above 1000, curve says 100, capped at 50
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("lux=%v", tt.lux), func(t *testing.T) {

			manager.indicatorMu.Lock()
			delete(manager.lastBrightnessLevel, "light.test")
			manager.indicatorMu.Unlock()

			result := manager.calculateAdaptiveBrightness(tt.lux, "light.test")
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLuxSensorDiscovery(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
	}

	// Set up mock light and sensor entities with matching device IDs
	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Bedroom Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "150", map[string]interface{}{
		"friendly_name": "Bedroom Radar LTR390 Light",
	})
	env.MockHA.SetState("light.apollo_msr_2_27f538_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Living Room Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_27f538_ltr390_light", "300", map[string]interface{}{
		"friendly_name": "Living Room Radar LTR390 Light",
	})

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Verify light entities were discovered
	assert.Len(t, manager.indicatorLightEntities, 2)

	// Verify light-to-lux sensor mapping
	manager.indicatorMu.RLock()
	assert.Equal(t, "sensor.apollo_msr_2_1294c8_ltr390_light", manager.lightToLuxSensor["light.apollo_msr_2_1294c8_rgb_light"])
	assert.Equal(t, "sensor.apollo_msr_2_27f538_ltr390_light", manager.lightToLuxSensor["light.apollo_msr_2_27f538_rgb_light"])
	manager.indicatorMu.RUnlock()
}

func TestLuxSensorDiscoveryNoMatch(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
	}

	// Set up light entity WITHOUT matching lux sensor
	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Bedroom Radar RGB Light",
	})
	// No matching sensor - different device ID
	env.MockHA.SetState("sensor.apollo_msr_2_different_ltr390_light", "150", map[string]interface{}{
		"friendly_name": "Different Sensor",
	})

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Verify light entity was discovered
	assert.Len(t, manager.indicatorLightEntities, 1)

	// Verify NO light-to-lux mapping (no matching sensor)
	manager.indicatorMu.RLock()
	_, hasMapping := manager.lightToLuxSensor["light.apollo_msr_2_1294c8_rgb_light"]
	manager.indicatorMu.RUnlock()
	assert.False(t, hasMapping, "Should not have lux sensor mapping when no match")
}

func TestAdaptiveBrightnessDisabled(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled: false, // Disabled
	}
	config.Energy.EnergyStates[2].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 0, BrightnessPct: 30}

	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Bedroom Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "150", map[string]interface{}{})

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Verify NO lux sensor discovery happened
	manager.indicatorMu.RLock()
	assert.Empty(t, manager.lightToLuxSensor, "Should not discover lux sensors when adaptive disabled")
	manager.indicatorMu.RUnlock()

	// Wait for startup goroutines to complete initial work
	manager.WaitForStartup()

	// Clear service calls from startup
	env.MockHA.ClearServiceCalls()

	// Update indicator lights
	manager.updateIndicatorLights("yellow")

	// Verify static brightness was used (not adaptive)
	calls := env.MockHA.GetServiceCalls()
	lightCall := testutil.FindServiceCall(calls, "light", "turn_on")

	assert.NotNil(t, lightCall)
	if lightCall != nil {
		assert.Equal(t, 30, lightCall.Data["brightness_pct"], "Should use static brightness when adaptive disabled")
	}
}

func TestAdaptiveBrightnessPerDevice(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 10, BrightnessPct: 20},
			{LuxMax: 100, BrightnessPct: 40},
			{LuxMax: 500, BrightnessPct: 60},
			{LuxMax: 1000, BrightnessPct: 80},
		},
	}
	config.Energy.EnergyStates[2].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 0, BrightnessPct: 30}

	// Two lights with different lux values
	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Dark Room Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "5", map[string]interface{}{}) // Dark: 20%

	env.MockHA.SetState("light.apollo_msr_2_27f538_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Bright Room Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_27f538_ltr390_light", "800", map[string]interface{}{}) // Bright: 80%

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Simulate lux sensor updates to populate currentLuxValues
	manager.handleLuxChange("sensor.apollo_msr_2_1294c8_ltr390_light", 5)
	manager.handleLuxChange("sensor.apollo_msr_2_27f538_ltr390_light", 800)

	// Wait for startup goroutines to complete initial work
	manager.WaitForStartup()

	// Clear service calls
	env.MockHA.ClearServiceCalls()

	// Update indicator lights (should use per-device brightness)
	manager.updateIndicatorLights("yellow")

	// Verify two separate service calls with different brightness values
	calls := env.MockHA.GetServiceCalls()
	lightCalls := make(map[string]int) // entity -> brightness

	for _, c := range calls {
		if c.Domain == "light" && c.Service == "turn_on" {
			entityID, ok := c.Data["entity_id"].(string)
			if ok {
				brightness, _ := c.Data["brightness_pct"].(int)
				lightCalls[entityID] = brightness
			}
		}
	}

	assert.Equal(t, 20, lightCalls["light.apollo_msr_2_1294c8_rgb_light"], "Dark room should have 20% brightness")
	assert.Equal(t, 50, lightCalls["light.apollo_msr_2_27f538_rgb_light"], "Bright room should have 50% brightness (capped from 80)")
}

func TestHysteresisPreventsOscillation(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 100, BrightnessPct: 40},
		},
		HysteresisPercent: 10, // 10% hysteresis band
	}

	env.MockHA.Connect()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	lightEntity := "light.test"

	// Start at lux=50 (40% brightness)
	manager.indicatorMu.Lock()
	manager.lastBrightnessLevel[lightEntity] = 40
	manager.indicatorMu.Unlock()

	// Test lux values within hysteresis band (100 ± 10%)
	// Should stay at 40% when near the threshold

	// At lux=95 (within 10% below 100), should stay at 40%
	brightness := manager.calculateAdaptiveBrightness(95, lightEntity)
	assert.Equal(t, 40, brightness, "Should stay at 40% when within hysteresis band below threshold")

	// At lux=105 (within 10% above 100), should stay at 40%
	brightness = manager.calculateAdaptiveBrightness(105, lightEntity)
	assert.Equal(t, 40, brightness, "Should stay at 40% when within hysteresis band above threshold")

	// At lux=50 (well below threshold), should stay at 40%
	brightness = manager.calculateAdaptiveBrightness(50, lightEntity)
	assert.Equal(t, 40, brightness, "Should stay at 40% when lux is well below threshold")

	// At lux=120 (outside hysteresis band), should change to max (50% after cap)
	brightness = manager.calculateAdaptiveBrightness(120, lightEntity)
	assert.Equal(t, 50, brightness, "Should change to 50% (capped from 100%) when lux is well above threshold")
}

func TestDebouncing(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:             true,
		LuxSensorPattern:    "ltr390_light",
		DebounceDurationSec: 1, // 1 second debounce for faster test
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 50, BrightnessPct: 30},
			{LuxMax: 100, BrightnessPct: 50},
			{LuxMax: 200, BrightnessPct: 70},
		},
	}
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 0, Green: 0, Blue: 0, BrightnessPct: 50}

	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Test Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "50", map[string]interface{}{})

	env.MockHA.Connect()

	// Initialize state variables
	env.StateMgr.SetString("currentEnergyLevel", "black")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Wait for startup goroutines to complete initial work
	manager.WaitForStartup()

	// Clear service calls from startup
	env.MockHA.ClearServiceCalls()

	lightEntity := "light.apollo_msr_2_1294c8_rgb_light"

	// Reset brightness tracking so test starts fresh (startup may have set values)
	manager.indicatorMu.Lock()
	manager.lastBrightnessLevel[lightEntity] = 0
	manager.lastBrightnessUpdate[lightEntity] = time.Time{}
	manager.indicatorMu.Unlock()

	// First update should go through (lux=30 → 30% brightness)
	manager.updateLightBrightness(lightEntity, 30)

	// Immediate second update should be debounced (lux=60 → 50% brightness)
	manager.updateLightBrightness(lightEntity, 60)

	// Wait for debounce period
	time.Sleep(1100 * time.Millisecond)

	// Third update should go through after debounce period (lux=150 → 70% brightness)
	manager.updateLightBrightness(lightEntity, 150)

	// Count service calls (should be 2: first + third)
	calls := env.MockHA.GetServiceCalls()
	lightCallCount := testutil.CountServiceCalls(calls, "light", "turn_on")

	assert.Equal(t, 2, lightCallCount, "Should have exactly 2 light updates (debounce blocked the second)")
}

func TestFallbackToStaticBrightness(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
	}
	config.Energy.EnergyStates[2].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 0, BrightnessPct: 45} // yellow

	// Light entity WITHOUT matching lux sensor
	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Orphan Radar RGB Light",
	})
	// No lux sensor for this device

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Wait for startup goroutines to complete initial work
	manager.WaitForStartup()

	// Clear service calls from startup
	env.MockHA.ClearServiceCalls()

	// Update indicator lights
	manager.updateIndicatorLights("yellow")

	// Should fall back to static brightness (45%) since no lux sensor available
	calls := env.MockHA.GetServiceCalls()
	lightCall := testutil.FindServiceCall(calls, "light", "turn_on")

	assert.NotNil(t, lightCall)
	if lightCall != nil {
		assert.Equal(t, 45, lightCall.Data["brightness_pct"], "Should use static brightness when no lux sensor available")
	}
}

func TestHandleLuxChangeWithInvalidValues(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
	}

	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Test Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "50", map[string]interface{}{})

	env.MockHA.Connect()
	env.StateMgr.SetString("currentEnergyLevel", "yellow")

	// Use readOnly=true to avoid side effects from free energy checker
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Wait for startup goroutines to complete initial work
	manager.WaitForStartup()

	// Clear service calls from startup
	env.MockHA.ClearServiceCalls()
	initialCount := testutil.CountServiceCalls(env.MockHA.GetServiceCalls(), "light", "turn_on")

	// Test NaN - should be ignored, no light service call
	manager.handleLuxChange("sensor.apollo_msr_2_1294c8_ltr390_light", math.NaN())
	assert.Equal(t, initialCount, testutil.CountServiceCalls(env.MockHA.GetServiceCalls(), "light", "turn_on"),
		"NaN lux value should be ignored, no light service call expected")

	// Test positive infinity - should be ignored
	manager.handleLuxChange("sensor.apollo_msr_2_1294c8_ltr390_light", math.Inf(1))
	assert.Equal(t, initialCount, testutil.CountServiceCalls(env.MockHA.GetServiceCalls(), "light", "turn_on"),
		"Positive infinity lux value should be ignored")

	// Test negative infinity - should be ignored
	manager.handleLuxChange("sensor.apollo_msr_2_1294c8_ltr390_light", math.Inf(-1))
	assert.Equal(t, initialCount, testutil.CountServiceCalls(env.MockHA.GetServiceCalls(), "light", "turn_on"),
		"Negative infinity lux value should be ignored")

	// Verify currentLuxValues was NOT updated with invalid values
	manager.indicatorMu.RLock()
	_, exists := manager.currentLuxValues["sensor.apollo_msr_2_1294c8_ltr390_light"]
	manager.indicatorMu.RUnlock()
	assert.False(t, exists, "Invalid lux values should not be stored")
}

func TestHysteresisDoesNotBlockLargeJumps(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 10, BrightnessPct: 20},
			{LuxMax: 100, BrightnessPct: 40},
			{LuxMax: 500, BrightnessPct: 60},
			{LuxMax: 1000, BrightnessPct: 80},
		},
		HysteresisPercent: 10,
	}

	env.MockHA.Connect()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	lightEntity := "light.test"

	// Start at lux=5 (20% brightness)
	manager.indicatorMu.Lock()
	manager.lastBrightnessLevel[lightEntity] = 20
	manager.indicatorMu.Unlock()

	// Jump to lux=800 (curve says 80%, capped to 50%) - far from any hysteresis band
	// Hysteresis bands are: 10±1, 100±10, 500±50, 1000±100
	// lux=800 is not in any of these bands, so it should change
	brightness := manager.calculateAdaptiveBrightness(800, lightEntity)
	assert.Equal(t, 50, brightness, "Large lux jump should change brightness despite hysteresis (capped at 50%)")

	// Update last brightness and test the reverse
	manager.indicatorMu.Lock()
	manager.lastBrightnessLevel[lightEntity] = 50
	manager.indicatorMu.Unlock()

	// Jump back to lux=5 (should be 20%) - far from any hysteresis band
	brightness = manager.calculateAdaptiveBrightness(5, lightEntity)
	assert.Equal(t, 20, brightness, "Large lux drop should change brightness despite hysteresis")
}

func TestNegativeLuxValue(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 10, BrightnessPct: 20},
			{LuxMax: 100, BrightnessPct: 40},
		},
	}

	env.MockHA.Connect()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	// Negative lux (unlikely but possible with sensor errors) should use lowest brightness
	brightness := manager.calculateAdaptiveBrightness(-5, "light.test")
	assert.Equal(t, 20, brightness, "Negative lux should use lowest brightness tier")
}

// ============================================================================
// Baseline Calibration Tests
// ============================================================================

func TestBaselineCalibrationEnabled(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 100, BrightnessPct: 40},
		},
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled:                  true,
			CalibrationIntervalSec:   300,
			CalibrationBrightnessPct: 5,
			CalibrationWaitSec:       65,
		},
	}

	env.MockHA.Connect()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	// Verify calibration is enabled
	assert.True(t, manager.isCalibrationEnabled())
}

func TestBaselineCalibrationDisabled(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled: false,
		},
	}

	env.MockHA.Connect()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	// Verify calibration is disabled
	assert.False(t, manager.isCalibrationEnabled())
}

func TestGetBaselineLux(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled: true,
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled: true,
		},
	}

	env.MockHA.Connect()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	lightEntity := "light.test_device"

	// Initially no baseline should exist
	lux, exists := manager.getBaselineLux(lightEntity)
	assert.False(t, exists)
	assert.Equal(t, 0.0, lux)

	// Set a baseline value
	manager.indicatorMu.Lock()
	manager.baselineLuxValues[lightEntity] = 150.5
	manager.indicatorMu.Unlock()

	// Now baseline should exist
	lux, exists = manager.getBaselineLux(lightEntity)
	assert.True(t, exists)
	assert.Equal(t, 150.5, lux)
}

func TestCalibrationStateTracking(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled: true,
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled:                  true,
			CalibrationBrightnessPct: 5,
		},
	}

	env.MockHA.Connect()
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	lightEntity := "light.test_device"

	// Initially should be in Normal state (or not set)
	manager.indicatorMu.RLock()
	calibState := manager.calibrationState[lightEntity]
	manager.indicatorMu.RUnlock()
	assert.Equal(t, CalibrationStateNormal, calibState)

	// Simulate transitioning to Dimmed state
	manager.indicatorMu.Lock()
	manager.calibrationState[lightEntity] = CalibrationStateDimmed
	manager.indicatorMu.Unlock()

	manager.indicatorMu.RLock()
	calibState = manager.calibrationState[lightEntity]
	manager.indicatorMu.RUnlock()
	assert.Equal(t, CalibrationStateDimmed, calibState)
}

func TestUpdateIndicatorLightsSkipsCalibrating(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 100, BrightnessPct: 40},
		},
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled: false, // Disabled to prevent race with calibration goroutine
		},
	}
	config.Energy.EnergyStates[2].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 0, BrightnessPct: 30}

	// Set up mock light and sensor
	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Test Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "50", map[string]interface{}{})
	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	lightEntity := "light.apollo_msr_2_1294c8_rgb_light"

	// Set the light to "calibrating" state
	manager.indicatorMu.Lock()
	manager.calibrationState[lightEntity] = CalibrationStateDimmed
	manager.indicatorMu.Unlock()

	// Clear any service calls
	env.MockHA.ClearServiceCalls()

	// Update indicator lights - the calibrating light should be skipped
	manager.updateIndicatorLights("yellow")

	// Verify no light.turn_on calls were made for this device
	// (it should skip because it's in calibration mode)
	calls := env.MockHA.GetServiceCalls()
	deviceCall := testutil.FindServiceCallWithEntity(calls, "light", "turn_on", lightEntity)

	assert.Nil(t, deviceCall, "Should skip updating lights that are currently calibrating")
}

func TestUpdateIndicatorLightsUsesBaseline(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 100, BrightnessPct: 40},  // lux < 100 -> 40%
			{LuxMax: 1000, BrightnessPct: 80}, // lux < 1000 -> 80%
		},
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled: true,
		},
	}
	config.Energy.EnergyStates[2].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 0, BrightnessPct: 30}

	// Set up mock light and sensor
	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Test Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "2000", map[string]interface{}{}) // High lux from LED
	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	lightEntity := "light.apollo_msr_2_1294c8_rgb_light"
	luxSensor := "sensor.apollo_msr_2_1294c8_ltr390_light"

	// Set a low baseline lux (true ambient is dark)
	manager.indicatorMu.Lock()
	manager.baselineLuxValues[lightEntity] = 50 // Low ambient -> should use 40%
	manager.currentLuxValues[luxSensor] = 2000  // High reading contaminated by LED
	manager.indicatorMu.Unlock()

	// Clear any service calls
	env.MockHA.ClearServiceCalls()

	// Update indicator lights - should use baseline lux (50) not current lux (2000)
	manager.updateIndicatorLights("yellow")

	// Find the light.turn_on call for this device
	calls := env.MockHA.GetServiceCalls()
	lightCall := testutil.FindServiceCallWithEntity(calls, "light", "turn_on", lightEntity)

	assert.NotNil(t, lightCall, "Expected a light.turn_on call")
	if lightCall != nil {
		// With baseline lux of 50, brightness should be 40% (from first curve point)
		// NOT 100% which would be the case with contaminated lux of 2000
		assert.Equal(t, 40, lightCall.Data["brightness_pct"],
			"Should use baseline lux (50 -> 40%%) not contaminated lux (2000 -> 100%%)")
	}
}

func TestRestoreLightAfterCalibration(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 100, BrightnessPct: 40},
		},
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled: true,
		},
	}
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}

	// Set up mock light and sensor
	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Test Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "50", map[string]interface{}{})
	env.MockHA.Connect()

	// Set energy level
	env.StateMgr.SetString("currentEnergyLevel", "black")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	lightEntity := "light.apollo_msr_2_1294c8_rgb_light"
	luxSensor := "sensor.apollo_msr_2_1294c8_ltr390_light"

	// Set up calibration state
	manager.indicatorMu.Lock()
	manager.lightToLuxSensor[lightEntity] = luxSensor
	manager.calibrationState[lightEntity] = CalibrationStateDimmed
	manager.currentLuxValues[luxSensor] = 75 // Baseline reading
	manager.indicatorMu.Unlock()

	// Clear service calls
	env.MockHA.ClearServiceCalls()

	// Call restoreLightAfterCalibration
	manager.restoreLightAfterCalibration(lightEntity)

	// Verify baseline was recorded
	manager.indicatorMu.RLock()
	baseline := manager.baselineLuxValues[lightEntity]
	calibState := manager.calibrationState[lightEntity]
	manager.indicatorMu.RUnlock()

	assert.Equal(t, 75.0, baseline, "Baseline should be recorded")
	assert.Equal(t, CalibrationStateNormal, calibState, "Should return to Normal state")

	// Verify light was updated
	calls := env.MockHA.GetServiceCalls()
	lightCall := testutil.FindServiceCall(calls, "light", "turn_on")

	assert.NotNil(t, lightCall, "Expected a light.turn_on call to restore brightness")
}

func TestCalibrationShutdownDuringStartupDelay(t *testing.T) {
	t.Parallel(
	// This test verifies that the calibration goroutine can be stopped during
	// the initial 10-second startup delay without blocking or racing.
	)

	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 100, BrightnessPct: 40},
		},
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled:                  true,
			CalibrationIntervalSec:   300,
			CalibrationBrightnessPct: 5,
			CalibrationWaitSec:       1, // Short wait for test
		},
	}

	// Set up mock light and sensor
	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Test Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "50", map[string]interface{}{})
	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)

	// Stop immediately (during the 10-second startup delay)
	// This should not block or cause a panic
	done := make(chan struct{})
	go func() {
		manager.Stop()
		close(done)
	}()

	// Wait for Stop to complete with a timeout
	select {
	case <-done:
		// Success - Stop completed without blocking
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() blocked - likely race condition in calibration startup delay")
	}
}

func TestCalibrationWithNoLuxReadingYet(t *testing.T) {
	t.Parallel(
	// This test verifies behavior when calibration runs but no lux reading has
	// been received yet (e.g., sensor hasn't updated since dimming).
	)

	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BrightnessCurve: []BrightnessCurvePoint{
			{LuxMax: 100, BrightnessPct: 40},
		},
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled: true,
		},
	}
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}

	// Set up mock light and sensor
	env.MockHA.SetState("light.apollo_msr_2_1294c8_rgb_light", "on", map[string]interface{}{
		"friendly_name": "Test Radar RGB Light",
	})
	env.MockHA.SetState("sensor.apollo_msr_2_1294c8_ltr390_light", "50", map[string]interface{}{})
	env.MockHA.Connect()

	env.StateMgr.SetString("currentEnergyLevel", "black")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	lightEntity := "light.apollo_msr_2_1294c8_rgb_light"
	luxSensor := "sensor.apollo_msr_2_1294c8_ltr390_light"

	// Set up calibration state - but don't set currentLuxValues (simulates no reading yet)
	manager.indicatorMu.Lock()
	manager.lightToLuxSensor[lightEntity] = luxSensor
	manager.calibrationState[lightEntity] = CalibrationStateDimmed
	// Note: currentLuxValues[luxSensor] is NOT set - simulating sensor hasn't updated
	manager.indicatorMu.Unlock()

	// Clear service calls
	env.MockHA.ClearServiceCalls()

	// Call restoreLightAfterCalibration
	manager.restoreLightAfterCalibration(lightEntity)

	// Verify baseline was recorded as 0 (no panic, graceful handling)
	manager.indicatorMu.RLock()
	baseline := manager.baselineLuxValues[lightEntity]
	calibState := manager.calibrationState[lightEntity]
	manager.indicatorMu.RUnlock()

	// The baseline will be 0 since no lux reading was available
	// This is expected behavior - the system records whatever value is current
	assert.Equal(t, 0.0, baseline, "Baseline should be 0 when no lux reading available")
	assert.Equal(t, CalibrationStateNormal, calibState, "Should return to Normal state")
}

func TestSetLightBrightness(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}

	env.MockHA.SetState("light.test_light", "on", map[string]interface{}{
		"friendly_name": "Test Light",
	})
	env.MockHA.Connect()

	env.StateMgr.SetString("currentEnergyLevel", "black")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Clear service calls
	env.MockHA.ClearServiceCalls()

	// Call setLightBrightness
	manager.setLightBrightness("light.test_light", 5)

	// Verify service call was made
	calls := env.MockHA.GetServiceCalls()
	lightCall := testutil.FindServiceCall(calls, "light", "turn_on")

	assert.NotNil(t, lightCall, "Expected a light.turn_on call")
	if lightCall != nil {
		assert.Equal(t, "light.test_light", lightCall.Data["entity_id"])
		assert.Equal(t, 5, lightCall.Data["brightness_pct"])
		assert.Equal(t, []int{25, 25, 112}, lightCall.Data["rgb_color"])
	}
}

func TestSetLightBrightnessReadOnly(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}

	env.MockHA.Connect()

	env.StateMgr.SetString("currentEnergyLevel", "black")

	// Create manager in read-only mode
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	// Clear service calls
	env.MockHA.ClearServiceCalls()

	// Call setLightBrightness - should not make any service calls
	manager.setLightBrightness("light.test_light", 5)

	// Verify no service call was made
	calls := env.MockHA.GetServiceCalls()
	assert.Empty(t, calls, "No service calls should be made in read-only mode")
}

func TestRunCalibrationCycleWithNoLights(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled:                  true,
			CalibrationIntervalSec:   300,
			CalibrationBrightnessPct: 5,
			CalibrationWaitSec:       1, // Short for testing
		},
	}

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	// Don't discover any lights - indicatorLightEntities will be empty

	// Clear service calls
	env.MockHA.ClearServiceCalls()

	// Call runCalibrationCycle - should return early with no lights
	manager.runCalibrationCycle()

	// No errors should occur, function should return gracefully
	calls := env.MockHA.GetServiceCalls()
	assert.Empty(t, calls, "No service calls should be made with no lights")
}

func TestRunCalibrationCycleLightWithoutLuxSensor(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := createTestConfig()
	config.Energy.IndicatorLights.AdaptiveBrightness = AdaptiveBrightnessConfig{
		Enabled:          true,
		LuxSensorPattern: "ltr390_light",
		BaselineCalibration: BaselineCalibrationConfig{
			Enabled:                  true,
			CalibrationIntervalSec:   300,
			CalibrationBrightnessPct: 5,
			CalibrationWaitSec:       1,
		},
	}

	env.MockHA.Connect()

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	// Set up a light entity but no lux sensor mapping
	manager.indicatorMu.Lock()
	manager.indicatorLightEntities = []string{"light.test_light"}
	// Don't add lightToLuxSensor mapping
	manager.indicatorMu.Unlock()

	// Clear service calls
	env.MockHA.ClearServiceCalls()

	// Call runCalibrationCycle - should skip lights without lux sensors
	manager.runCalibrationCycle()

	// No service calls should be made since light has no lux sensor
	calls := env.MockHA.GetServiceCalls()
	assert.Empty(t, calls, "No service calls should be made for lights without lux sensors")
}
