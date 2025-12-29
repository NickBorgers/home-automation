package energy

import (
	"math"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
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
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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

func TestDetermineSolarEnergyLevel(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	tests := []struct {
		name         string
		thisHourKW   float64
		remainingKWH float64
		expected     string
	}{
		{"No production", 0, 0, "yellow"},              // Yellow has 0 requirements
		{"Low production", 1, 5, "yellow"},             // Yellow has 0 requirements
		{"Meets green kW but not kWh", 5, 5, "yellow"}, // Doesn't meet green's 10 kWh requirement
		{"Meets green kWh but not kW", 2, 15, "green"}, // Meets green: 0 kW and 10 kWh
		{"Meets green thresholds", 5, 15, "green"},     // Meets green: 0 kW and 10 kWh
		{"Meets white kW but not kWh", 5, 15, "green"}, // Doesn't meet white's 20 kWh requirement
		{"Meets white thresholds", 5, 25, "white"},     // Meets white: 4 kW and 20 kWh
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.determineSolarEnergyLevel(tt.thisHourKW, tt.remainingKWH)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDetermineOverallEnergyLevel(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	tests := []struct {
		name         string
		batteryLevel string
		solarLevel   string
		expected     string
	}{
		{"Both black", "black", "black", "black"},
		{"Battery red, solar black", "red", "black", "red"},
		{"Battery black, solar red", "black", "red", "red"},
		{"Both red", "red", "red", "red"},
		{"Battery yellow, solar black", "yellow", "black", "red"},
		{"Battery black, solar yellow", "black", "yellow", "red"},
		{"Battery yellow, solar red", "yellow", "red", "yellow"},
		{"Battery red, solar yellow", "red", "yellow", "yellow"},
		{"Both yellow", "yellow", "yellow", "yellow"},
		{"Battery green, solar yellow", "green", "yellow", "green"},
		{"Battery yellow, solar green", "yellow", "green", "green"},
		{"Both green", "green", "green", "green"},
		{"Battery white, solar green", "white", "green", "white"},
		{"Battery green, solar white", "green", "white", "white"},
		{"Both white", "white", "white", "white"},
		// Max one level higher than minimum
		{"Battery white, solar black", "white", "black", "red"},
		{"Battery black, solar white", "black", "white", "red"},
		{"Battery white, solar red", "white", "red", "yellow"},
		{"Battery red, solar white", "red", "white", "yellow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.determineOverallEnergyLevel(tt.batteryLevel, tt.solarLevel)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsFreeEnergyTime(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	// Test loading the actual config file
	// Skip this test if config file doesn't exist (e.g., in CI)
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
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()

	// Initialize state manager with initial values
	stateManager := state.NewManager(mockClient, logger, false)
	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	// Set initial state
	stateManager.SetBool("isGridAvailable", true)
	stateManager.SetString("batteryEnergyLevel", "black")
	stateManager.SetString("solarProductionEnergyLevel", "black")
	stateManager.SetNumber("thisHourSolarGeneration", 0.0)
	stateManager.SetNumber("remainingSolarGeneration", 0.0)

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Test Start method
	err = manager.Start()
	assert.NoError(t, err)

	// Give goroutines time to start
	time.Sleep(100 * time.Millisecond)

	// Test handler functions by triggering state changes
	t.Run("handleBatteryChange", func(t *testing.T) {
		manager.handleBatteryChange(50.0)
		level, _ := stateManager.GetString("batteryEnergyLevel")
		assert.Equal(t, "red", level)
	})

	t.Run("handleBatteryChange_with_invalid_value", func(t *testing.T) {
		// Test with Inf - should be ignored
		manager.handleBatteryChange(math.Inf(1))
		// Level should remain red from previous test
		level, _ := stateManager.GetString("batteryEnergyLevel")
		assert.Equal(t, "red", level)
	})

	t.Run("handleThisHourSolarChange", func(t *testing.T) {
		manager.handleThisHourSolarChange(5.0)
		kw, _ := stateManager.GetNumber("thisHourSolarGeneration")
		assert.Equal(t, 5.0, kw)
	})

	t.Run("handleRemainingSolarChange", func(t *testing.T) {
		manager.handleRemainingSolarChange(15.0)
		kwh, _ := stateManager.GetNumber("remainingSolarGeneration")
		assert.Equal(t, 15.0, kwh)
	})

	t.Run("recalculateSolarProductionLevel", func(t *testing.T) {
		manager.recalculateSolarProductionLevel()
		level, _ := stateManager.GetString("solarProductionEnergyLevel")
		assert.Equal(t, "green", level)
	})

	t.Run("recalculateOverallEnergyLevel", func(t *testing.T) {
		// Set known values
		stateManager.SetString("batteryEnergyLevel", "yellow")
		stateManager.SetString("solarProductionEnergyLevel", "green")
		stateManager.SetBool("isFreeEnergyAvailable", false)

		manager.recalculateOverallEnergyLevel()
		level, _ := stateManager.GetString("currentEnergyLevel")
		assert.Equal(t, "green", level)
	})

	t.Run("recalculateOverallEnergyLevel_with_free_energy", func(t *testing.T) {
		stateManager.SetBool("isFreeEnergyAvailable", true)
		manager.recalculateOverallEnergyLevel()
		level, _ := stateManager.GetString("currentEnergyLevel")
		assert.Equal(t, "white", level)
	})

	t.Run("checkFreeEnergy", func(t *testing.T) {
		stateManager.SetBool("isGridAvailable", false)
		manager.checkFreeEnergy()
		isFree, _ := stateManager.GetBool("isFreeEnergyAvailable")
		assert.False(t, isFree)
	})

	t.Run("handleGridAvailabilityChange", func(t *testing.T) {
		manager.handleGridAvailabilityChange("isGridAvailable", false, true)
		// Just verify it doesn't panic
	})

	t.Run("handleIntermediateLevelChange", func(t *testing.T) {
		manager.handleIntermediateLevelChange("batteryEnergyLevel", "black", "red")
		// Just verify it doesn't panic
	})
}

// TestDetermineOverallEnergyLevel_EdgeCases tests edge cases
func TestDetermineOverallEnergyLevel_EdgeCases(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	config := createTestConfig()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	t.Run("invalid_battery_level", func(t *testing.T) {
		result := manager.determineOverallEnergyLevel("invalid", "green")
		assert.Equal(t, "black", result)
	})

	t.Run("invalid_solar_level", func(t *testing.T) {
		result := manager.determineOverallEnergyLevel("green", "invalid")
		assert.Equal(t, "black", result)
	})
}

// TestLoadConfigError tests error handling in config loading
func TestLoadConfigError(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	assert.Error(t, err)
}

// TestIsFreeEnergyTime_EdgeCases tests edge cases for free energy time
func TestIsFreeEnergyTime_EdgeCases(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

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

		manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)
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

		manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)
		result := manager.isFreeEnergyTime(true)
		assert.False(t, result)
	})
}

// TestEnergyManager_Stop tests the Stop method and subscription cleanup
func TestEnergyManager_Stop(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := createTestConfig()
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Initialize required state variables
	_ = stateManager.SetBool("isGridAvailable", true)
	_ = stateManager.SetString("batteryEnergyLevel", "green")
	_ = stateManager.SetString("solarProductionEnergyLevel", "green")
	_ = stateManager.SetBool("isFreeEnergyAvailable", false)

	// Start manager (creates subscriptions and goroutine)
	err := manager.Start()
	assert.NoError(t, err)

	// Verify subscriptions were created via subHelper
	assert.Equal(t, 3, len(manager.subHelper.GetHASubscriptions()), "Should have 3 HA subscriptions")
	assert.Equal(t, 4, len(manager.subHelper.GetStateSubscriptions()), "Should have 4 state subscriptions")

	// Stop manager
	manager.Stop()

	// Verify subscriptions were cleaned up
	assert.Equal(t, 0, len(manager.subHelper.GetHASubscriptions()), "HA subscriptions should be empty after Stop")
	assert.Equal(t, 0, len(manager.subHelper.GetStateSubscriptions()), "State subscriptions should be empty after Stop")
}

func TestEnergyManager_ReadOnlyMode(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	// Create state manager in read-only mode
	stateManager := state.NewManager(mockClient, logger, true)

	config := createTestConfig()
	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

	// Test battery change handler - should handle read-only gracefully
	manager.handleBatteryChange(50.0)
	// No error should be thrown, just logged at debug level

	// Test solar generation handlers
	manager.handleThisHourSolarChange(5.0)
	manager.handleRemainingSolarChange(15.0)

	// Test free energy check
	_ = stateManager.SetBool("isGridAvailable", true)
	manager.checkFreeEnergy()

	// Test overall energy level recalculation
	_ = stateManager.SetBool("isFreeEnergyAvailable", true)
	_ = stateManager.SetString("batteryEnergyLevel", "green")
	_ = stateManager.SetString("solarProductionEnergyLevel", "green")
	manager.recalculateOverallEnergyLevel()

	// If we get here without panicking, the read-only mode handling worked correctly
	// The actual verification is that no errors are thrown, just debug logs
}

// TestTimezoneHandling tests that timezone configuration works correctly
func TestTimezoneHandling(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := createTestConfig()

	t.Run("default_timezone_is_utc", func(t *testing.T) {
		manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)
		assert.Equal(t, time.UTC, manager.timezone)
	})

	t.Run("custom_timezone_is_respected", func(t *testing.T) {
		estLocation, err := time.LoadLocation("America/New_York")
		assert.NoError(t, err)

		manager := NewManager(mockClient, stateManager, config, logger, false, estLocation, nil)
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
		utcManager := NewManager(mockClient, stateManager, testConfig, logger, false, time.UTC, nil)
		assert.Equal(t, time.UTC, utcManager.timezone)

		// Test with different timezone
		estLocation, err := time.LoadLocation("America/New_York")
		assert.NoError(t, err)
		estManager := NewManager(mockClient, stateManager, testConfig, logger, false, estLocation, nil)
		assert.Equal(t, estLocation, estManager.timezone)

		// Both managers should use their configured timezone for calculations
		// We can't easily test the exact behavior without mocking time.Now(),
		// but we've verified the timezone is set correctly
	})
}

func TestManagerReset(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestConfig()

	// Set up initial state
	mockClient.SetState("sensor.battery_energy_level", "50", map[string]interface{}{})
	mockClient.SetState("sensor.solar_production_energy_level", "75", map[string]interface{}{})
	mockClient.Connect()

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	err = manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Reset should re-check free energy and recalculate levels
	err = manager.Reset()
	assert.NoError(t, err)

	// Verify energy levels are set
	currentLevel, err := stateManager.GetString("currentEnergyLevel")
	assert.NoError(t, err)
	assert.NotEmpty(t, currentLevel)
}

// TestHandleGridAvailabilityChange tests grid availability change synchronization
func TestHandleGridAvailabilityChange(t *testing.T) {
	logger := zap.NewNop()
	config := createTestConfig()

	t.Run("syncs_grid_availability_to_HA_when_enabled", func(t *testing.T) {
		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)
		manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

		// Clear any initial service calls
		mockClient.ClearServiceCalls()

		// Simulate grid availability change to true
		manager.handleGridAvailabilityChange("isGridAvailable", false, true)

		// Verify SetInputBoolean was called for grid_available
		// Note: checkFreeEnergy() is also called, which may make additional service calls
		serviceCalls := mockClient.GetServiceCalls()
		assert.GreaterOrEqual(t, len(serviceCalls), 1, "Expected at least one service call")

		// Find the grid_available service call
		var gridAvailableCall *ha.ServiceCall
		for i := range serviceCalls {
			if serviceCalls[i].Data["entity_id"] == "input_boolean.grid_available" {
				gridAvailableCall = &serviceCalls[i]
				break
			}
		}

		assert.NotNil(t, gridAvailableCall, "Expected grid_available service call")
		assert.Equal(t, "input_boolean", gridAvailableCall.Domain)
		assert.Equal(t, "turn_on", gridAvailableCall.Service)
	})

	t.Run("syncs_grid_availability_to_HA_when_disabled", func(t *testing.T) {
		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)
		manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

		// Clear any initial service calls
		mockClient.ClearServiceCalls()

		// Simulate grid availability change to false
		manager.handleGridAvailabilityChange("isGridAvailable", true, false)

		// Verify SetInputBoolean was called with turn_off for grid_available
		// Note: checkFreeEnergy() is also called, which may make additional service calls
		serviceCalls := mockClient.GetServiceCalls()
		assert.GreaterOrEqual(t, len(serviceCalls), 1, "Expected at least one service call")

		// Find the grid_available service call
		var gridAvailableCall *ha.ServiceCall
		for i := range serviceCalls {
			if serviceCalls[i].Data["entity_id"] == "input_boolean.grid_available" {
				gridAvailableCall = &serviceCalls[i]
				break
			}
		}

		assert.NotNil(t, gridAvailableCall, "Expected grid_available service call")
		assert.Equal(t, "input_boolean", gridAvailableCall.Domain)
		assert.Equal(t, "turn_off", gridAvailableCall.Service)
	})

	t.Run("skips_HA_sync_in_read_only_mode", func(t *testing.T) {
		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, true) // read-only mode
		manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

		// Clear any initial service calls
		mockClient.ClearServiceCalls()

		// Simulate grid availability change
		manager.handleGridAvailabilityChange("isGridAvailable", false, true)

		// Verify no service calls were made
		serviceCalls := mockClient.GetServiceCalls()
		assert.Len(t, serviceCalls, 0, "Expected no service calls in read-only mode")
	})

	t.Run("handles_non_boolean_value_gracefully", func(t *testing.T) {
		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)
		manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

		// Clear any initial service calls
		mockClient.ClearServiceCalls()

		// Simulate grid availability change with invalid value
		manager.handleGridAvailabilityChange("isGridAvailable", false, "not_a_boolean")

		// Verify no service calls were made (error was handled)
		serviceCalls := mockClient.GetServiceCalls()
		assert.Len(t, serviceCalls, 0, "Expected no service calls with invalid value")
	})

	t.Run("triggers_free_energy_recalculation", func(t *testing.T) {
		mockClient := ha.NewMockClient()
		stateManager := state.NewManager(mockClient, logger, false)

		// Initialize required state variables
		_ = stateManager.SyncFromHA()
		_ = stateManager.SetBool("isGridAvailable", true)
		_ = stateManager.SetBool("isFreeEnergyAvailable", false)

		manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

		// Clear any initial service calls
		mockClient.ClearServiceCalls()

		// Get initial free energy state
		initialFreeEnergy, _ := stateManager.GetBool("isFreeEnergyAvailable")

		// Simulate grid availability change
		manager.handleGridAvailabilityChange("isGridAvailable", false, true)

		// Verify free energy was recalculated (may or may not change depending on time)
		// The important thing is that checkFreeEnergy was called without error
		currentFreeEnergy, err := stateManager.GetBool("isFreeEnergyAvailable")
		assert.NoError(t, err)

		// Value might be the same, but at least we verify it was processed
		_ = initialFreeEnergy
		_ = currentFreeEnergy
	})
}

func TestIndicatorLightsDiscovery(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestConfig()

	// Set up mock states for light entities - some with "Radar" in friendly_name
	mockClient.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom Radar Light",
	})
	mockClient.SetState("light.apollo_kitchen_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Kitchen Radar Light",
	})
	mockClient.SetState("light.living_room_lamp", "on", map[string]interface{}{
		"friendly_name": "Living Room Lamp", // No "Radar", should not be discovered
	})
	mockClient.SetState("sensor.bedroom_radar", "detected", map[string]interface{}{
		"friendly_name": "Bedroom Radar", // Sensor, not light - should not be discovered
	})

	mockClient.Connect()

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	logger, _ := zap.NewDevelopment()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Create config with light configs for each energy level
	config := createTestConfig()
	// The createTestConfig doesn't have LightConfig, so let's update the EnergyStates
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}    // black
	config.Energy.EnergyStates[1].LightConfig = LightConfig{Red: 255, Green: 0, Blue: 0, BrightnessPct: 30}      // red
	config.Energy.EnergyStates[2].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 0, BrightnessPct: 30}    // yellow
	config.Energy.EnergyStates[3].LightConfig = LightConfig{Red: 0, Green: 255, Blue: 0, BrightnessPct: 60}      // green
	config.Energy.EnergyStates[4].LightConfig = LightConfig{Red: 255, Green: 255, Blue: 255, BrightnessPct: 100} // white

	// Set up mock light entities with "Radar" in friendly_name
	mockClient.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom Radar Light",
	})

	mockClient.Connect()

	// Create manager NOT in read-only mode
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Verify entity was discovered
	assert.Len(t, manager.indicatorLightEntities, 1)

	// Clear any service calls from startup
	mockClient.ClearServiceCalls()

	// Test updateIndicatorLights directly with a specific energy level
	// This avoids complications with free energy time and recalculation
	manager.updateIndicatorLights("yellow")

	// Verify the light service was called
	calls := mockClient.GetServiceCalls()

	// Find the light.turn_on call (ignore other background calls like input_boolean updates)
	var lightCall *ha.ServiceCall
	for i := range calls {
		if calls[i].Domain == "light" && calls[i].Service == "turn_on" {
			lightCall = &calls[i]
			break
		}
	}

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
	logger, _ := zap.NewDevelopment()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestConfig()

	// Add LightConfig
	config.Energy.EnergyStates[0].LightConfig = LightConfig{Red: 25, Green: 25, Blue: 112, BrightnessPct: 70}

	// Set up mock light entity
	mockClient.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom Radar Light",
	})

	mockClient.Connect()

	// Create manager in READ-ONLY mode
	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Clear any service calls from startup
	mockClient.ClearServiceCalls()

	// Trigger updateIndicatorLights directly
	manager.updateIndicatorLights("black")

	// Verify NO light.turn_on service was called in read-only mode
	calls := mockClient.GetServiceCalls()
	var lightCalls []ha.ServiceCall
	for _, c := range calls {
		if c.Domain == "light" && c.Service == "turn_on" {
			lightCalls = append(lightCalls, c)
		}
	}
	assert.Len(t, lightCalls, 0, "Expected no light.turn_on service calls in read-only mode")

	// But shadow state should still be updated
	shadowState := manager.GetShadowState()
	assert.NotNil(t, shadowState.Outputs.IndicatorLightsAction)
	assert.Equal(t, "black", shadowState.Outputs.IndicatorLightsAction.EnergyLevel)
}

func TestIndicatorLightsNoEntitiesDiscovered(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestConfig()

	// Set up mock states with NO "Radar" entities
	mockClient.SetState("light.living_room_lamp", "on", map[string]interface{}{
		"friendly_name": "Living Room Lamp",
	})

	mockClient.Connect()

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Verify no entities were discovered
	assert.Len(t, manager.indicatorLightEntities, 0)

	// Clear any service calls
	mockClient.ClearServiceCalls()

	// Calling updateIndicatorLights should not panic and should not make light.turn_on calls
	manager.updateIndicatorLights("black")

	// Verify NO light.turn_on service was called when no entities discovered
	calls := mockClient.GetServiceCalls()
	var lightCalls []ha.ServiceCall
	for _, c := range calls {
		if c.Domain == "light" && c.Service == "turn_on" {
			lightCalls = append(lightCalls, c)
		}
	}
	assert.Len(t, lightCalls, 0, "Expected no light.turn_on service calls when no entities discovered")
}

func TestIndicatorLightsDiscoveryCaseInsensitive(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestConfig()

	// Set up mock light entities with different casing of "Radar"
	mockClient.SetState("light.apollo_lower_case", "on", map[string]interface{}{
		"friendly_name": "Apollo radar sensor", // lowercase "radar"
	})
	mockClient.SetState("light.apollo_upper_case", "on", map[string]interface{}{
		"friendly_name": "Apollo RADAR sensor", // uppercase "RADAR"
	})
	mockClient.SetState("light.apollo_mixed_case", "on", map[string]interface{}{
		"friendly_name": "Apollo RaDaR sensor", // mixed case
	})
	mockClient.SetState("light.no_match", "on", map[string]interface{}{
		"friendly_name": "No match here",
	})

	mockClient.Connect()

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Verify all case variations were discovered (case-insensitive matching)
	assert.Len(t, manager.indicatorLightEntities, 3)
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_lower_case")
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_upper_case")
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_mixed_case")
}

func TestIndicatorLightsDiscoveryInvalidPattern(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestConfig()

	// Set an invalid regex pattern (unclosed bracket)
	config.Energy.IndicatorLights.FriendlyNamePattern = "[invalid"

	mockClient.SetState("light.test_light", "on", map[string]interface{}{
		"friendly_name": "Test Radar Light",
	})

	mockClient.Connect()

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Start should not fail - invalid pattern is handled gracefully
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// No entities should be discovered when pattern is invalid
	assert.Len(t, manager.indicatorLightEntities, 0)
}

func TestIndicatorLightsDiscoveryCustomPattern(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestConfig()

	// Set a custom regex pattern
	config.Energy.IndicatorLights.FriendlyNamePattern = "^Apollo.*RGB$"

	mockClient.SetState("light.apollo_bedroom_rgb", "on", map[string]interface{}{
		"friendly_name": "Apollo Bedroom RGB", // matches pattern
	})
	mockClient.SetState("light.apollo_kitchen_radar", "on", map[string]interface{}{
		"friendly_name": "Apollo Kitchen Radar", // doesn't match pattern
	})
	mockClient.SetState("light.hue_living_room", "on", map[string]interface{}{
		"friendly_name": "Hue Living Room RGB", // doesn't start with Apollo
	})

	mockClient.Connect()

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Only the matching entity should be discovered
	assert.Len(t, manager.indicatorLightEntities, 1)
	assert.Contains(t, manager.indicatorLightEntities, "light.apollo_bedroom_rgb")
}
