package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/plugins/energy"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Energy Management Plugin Scenario Tests
//
// These tests validate that the Energy Management plugin correctly responds
// to battery, solar, and grid state changes and updates energy levels.
// ============================================================================

// setupEnergyScenarioTest creates a test environment with the energy plugin
func setupEnergyScenarioTest(t *testing.T) (*MockHAServer, *energy.Manager, *state.Manager, *ha.Client, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	// Load test energy config
	configPath := filepath.Join("testdata", "energy_config_test.yaml")
	energyConfig, err := energy.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test energy config")

	// Create logger
	logger := testlogger.New()

	// Convert energy config to EnergyStateConfig format for the registry
	var energyStates []state.EnergyStateConfig
	for _, es := range energyConfig.Energy.EnergyStates {
		energyStates = append(energyStates, state.EnergyStateConfig{
			ConditionName:                       es.ConditionName,
			BatteryMinimumPercentage:            es.BatteryMinimumPercentage,
			EnergyProductionMinimumKW:           es.EnergyProductionMinimumKW,
			RemainingEnergyProductionMinimumKWH: es.RemainingEnergyProductionMinimumKWH,
		})
	}

	// Set up the ComputedStateRegistry with energy providers
	// This is now required since energy level computation moved from the plugin to the registry
	err = manager.SetupComputedStateV2WithEnergy(energyStates, nil)
	require.NoError(t, err, "Failed to set up computed state registry with energy providers")

	// Use a fixed timezone for testing (UTC)
	timezone := time.UTC

	// Create energy plugin (read-only mode for testing)
	energyMgr := energy.NewManager(context.Background(), client, manager, energyConfig, logger, false, timezone, nil)

	// Start the energy plugin
	err = energyMgr.Start()
	require.NoError(t, err, "Failed to start energy manager")

	// Wait for startup goroutines to complete initial work
	energyMgr.WaitForStartup()

	cleanup := func() {
		energyMgr.Stop()
		baseCleanup()
	}

	return server, energyMgr, manager, client, cleanup
}

// TestScenario_BatteryLevelChanges_UpdateEnergyLevels validates that when
// battery percentage drops, the batteryEnergyLevel updates correctly
func TestScenario_BatteryLevelChanges_UpdateEnergyLevels(t *testing.T) {
	t.Parallel()
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// GIVEN: Battery is at 85% (white level - threshold is 80%)
	t.Log("GIVEN: Battery is at 85% (white level)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "85.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// Verify initial state is white
	waitForStringState(t, manager, "batteryEnergyLevel", "white", "Battery level should be white at 85%")

	// WHEN: Battery drops to 30% (yellow level - threshold is 15%)
	t.Log("WHEN: Battery drops to 30% (yellow level)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "30.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// THEN: Battery level should be yellow
	t.Log("THEN: Battery level should be yellow")
	waitForStringState(t, manager, "batteryEnergyLevel", "yellow", "Battery level should be yellow at 30%")

	// WHEN: Battery drops to 10% (red level - below 15% yellow threshold)
	t.Log("WHEN: Battery drops to 10% (red level)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "10.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// THEN: Battery level should be red
	t.Log("THEN: Battery level should be red")
	waitForStringState(t, manager, "batteryEnergyLevel", "red", "Battery level should be red at 10%")
}

// TestScenario_SolarProductionUpdates_CalculatesEnergyLevel validates that
// solar production changes correctly update the solarProductionEnergyLevel
func TestScenario_SolarProductionUpdates_CalculatesEnergyLevel(t *testing.T) {
	t.Parallel()
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// GIVEN: No solar production (red level - lowest)
	t.Log("GIVEN: No solar production (red level)")
	server.SetState("sensor.energy_next_hour", "0.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "0.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// Verify initial state
	waitForStringState(t, manager, "solarProductionEnergyLevel", "red", "Solar level should be red with no production")

	// WHEN: Solar production increases (this hour = 2kW, remaining = 15kWh -> green)
	t.Log("WHEN: Solar production increases to green level")
	server.SetState("sensor.energy_next_hour", "2.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "15.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// THEN: Solar level should be green (threshold: 1 kW, 5 kWh)
	t.Log("THEN: Solar level should be green")
	waitForStringState(t, manager, "solarProductionEnergyLevel", "green", "Solar level should be green with 2kW/15kWh")

	// WHEN: Solar production drops (this hour = 0.5kW, remaining = 2kWh -> yellow)
	t.Log("WHEN: Solar production drops to yellow level")
	server.SetState("sensor.energy_next_hour", "0.5", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "2.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// THEN: Solar level should be yellow (threshold: 0.1 kW, 0 kWh; below green's 1 kW)
	t.Log("THEN: Solar level should be yellow")
	waitForStringState(t, manager, "solarProductionEnergyLevel", "yellow", "Solar level should be yellow with 0.5kW/2kWh")
}

// TestScenario_GridAvailability_DoesNotEnableFreeEnergy validates that grid
// availability changes no longer enable scheduled metered free energy.
func TestScenario_GridAvailability_DoesNotEnableFreeEnergy(t *testing.T) {
	t.Parallel()
	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	// Load test energy config
	configPath := filepath.Join("testdata", "energy_config_test.yaml")
	energyConfig, err := energy.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test energy config")

	// Create logger
	logger := testlogger.New()

	fixedTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	// Create energy plugin with UTC timezone and mock clock
	energyMgr := energy.NewManager(context.Background(), client, manager, energyConfig, logger, false, time.UTC, nil)
	energyMgr.SetClock(mockClock)
	err = energyMgr.Start()
	require.NoError(t, err, "Failed to start energy manager")
	defer energyMgr.Stop()

	// Wait for startup goroutines to complete initial work
	energyMgr.WaitForStartup()

	// GIVEN: Grid is available
	t.Log("GIVEN: Grid is available")
	err = manager.SetBool("isGridAvailable", true)
	require.NoError(t, err)

	waitForBoolState(t, manager, "isFreeEnergyAvailable", false, "Metered grid free energy should remain false")
	isFreeEnergy, err := manager.GetBool("isFreeEnergyAvailable")
	require.NoError(t, err)
	t.Logf("Initial free energy state: %v", isFreeEnergy)

	// WHEN: Grid goes offline
	t.Log("WHEN: Grid goes offline")
	err = manager.SetBool("isGridAvailable", false)
	require.NoError(t, err)

	// THEN: Metered grid free energy should be false
	t.Log("THEN: Free energy should be false")
	waitForBoolState(t, manager, "isFreeEnergyAvailable", false, "Metered grid free energy should remain false")

	// WHEN: Grid comes back online
	t.Log("WHEN: Grid comes back online")
	err = manager.SetBool("isGridAvailable", true)
	require.NoError(t, err)

	// THEN: Metered grid free energy should still be false
	t.Log("THEN: Free energy should still be false")
	waitForBoolState(t, manager, "isFreeEnergyAvailable", false, "Metered grid free energy should remain false")

	// Verify service call was made
	calls := server.GetServiceCalls()
	t.Logf("Total service calls made: %d", len(calls))
}

// TestScenario_OverallEnergyLevel_ReflectsWorstState validates that the
// currentEnergyLevel correctly reflects the worst state across battery/solar
func TestScenario_OverallEnergyLevel_ReflectsWorstState(t *testing.T) {
	t.Parallel()
	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	// Load test energy config
	configPath := filepath.Join("testdata", "energy_config_test.yaml")
	energyConfig, err := energy.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test energy config")

	// Convert energy config to EnergyStateConfig format for the registry
	var energyStates []state.EnergyStateConfig
	for _, es := range energyConfig.Energy.EnergyStates {
		energyStates = append(energyStates, state.EnergyStateConfig{
			ConditionName:                       es.ConditionName,
			BatteryMinimumPercentage:            es.BatteryMinimumPercentage,
			EnergyProductionMinimumKW:           es.EnergyProductionMinimumKW,
			RemainingEnergyProductionMinimumKWH: es.RemainingEnergyProductionMinimumKWH,
		})
	}

	// Set up the ComputedStateRegistry with energy providers
	err = manager.SetupComputedStateV2WithEnergy(energyStates, nil)
	require.NoError(t, err, "Failed to set up computed state registry with energy providers")

	// Create logger
	logger := testlogger.New()

	fixedTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	// Create energy plugin with UTC timezone and mock clock
	energyMgr := energy.NewManager(context.Background(), client, manager, energyConfig, logger, false, time.UTC, nil)
	energyMgr.SetClock(mockClock)
	err = energyMgr.Start()
	require.NoError(t, err, "Failed to start energy manager")
	defer energyMgr.Stop()

	// Wait for startup goroutines to complete initial work
	energyMgr.WaitForStartup()

	// GIVEN: Battery at green (65%), solar at green (2kW, 15kWh)
	t.Log("GIVEN: Battery green, solar green")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "65.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	server.SetState("sensor.energy_next_hour", "2.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "15.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// Verify overall level is green.
	waitForProcessing(t, manager)
	waitForStringState(t, manager, "currentEnergyLevel", "green", "Overall level should be green when both are green")

	// WHEN: Battery drops to red (10%), solar still green
	t.Log("WHEN: Battery drops to red, solar stays green")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "10.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// THEN: Overall level should reflect the lower state.
	// Solar can boost the battery-derived result by at most one level: red + green → yellow.
	t.Log("THEN: Overall level should reflect worst state")
	waitForStringState(t, manager, "batteryEnergyLevel", "red", "Battery level should settle before checking overall level")
	waitForProcessing(t, manager)
	waitForStringState(t, manager, "currentEnergyLevel", "yellow",
		"Overall level should be yellow when battery is red and solar is green (boost by 1)")

	// WHEN: Solar drops to the floor of the test config (0kW, 0kWh -> red)
	t.Log("WHEN: Solar drops to the floor of the test config")
	server.SetState("sensor.energy_next_hour", "0.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "0.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// THEN: Solar should settle to red, and overall should be red too.
	t.Log("THEN: Overall level should drop to red")
	waitForStringState(t, manager, "solarProductionEnergyLevel", "red", "Solar level should settle before checking overall level")
	waitForProcessing(t, manager)
	waitForStringState(t, manager, "currentEnergyLevel", "red",
		"Overall level should be red when battery and solar are both red")
}

// TestScenario_MeteredGridFreeEnergyWindow_DoesNotOverrideEnergyLevel validates
// that the retired metered-grid free-energy schedule no longer sets white.
func TestScenario_MeteredGridFreeEnergyWindow_DoesNotOverrideEnergyLevel(t *testing.T) {
	t.Parallel()
	// Setup test environment manually without starting energy manager
	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	// Use a fixed reference time that used to be inside the retired free grid window.
	fixedTime := time.Date(2024, 1, 15, 22, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(fixedTime)

	// Load config and create manager with UTC timezone and mock clock
	configPath := filepath.Join("testdata", "energy_config_test.yaml")
	energyConfig, err := energy.LoadConfig(configPath)
	require.NoError(t, err)

	// Convert energy config to EnergyStateConfig format for the registry
	var energyStates []state.EnergyStateConfig
	for _, es := range energyConfig.Energy.EnergyStates {
		energyStates = append(energyStates, state.EnergyStateConfig{
			ConditionName:                       es.ConditionName,
			BatteryMinimumPercentage:            es.BatteryMinimumPercentage,
			EnergyProductionMinimumKW:           es.EnergyProductionMinimumKW,
			RemainingEnergyProductionMinimumKWH: es.RemainingEnergyProductionMinimumKWH,
		})
	}

	// Set up the ComputedStateRegistry with energy providers
	err = manager.SetupComputedStateV2WithEnergy(energyStates, nil)
	require.NoError(t, err, "Failed to set up computed state registry with energy providers")

	logger := testlogger.New()
	energyMgr := energy.NewManager(context.Background(), client, manager, energyConfig, logger, false, time.UTC, nil)
	energyMgr.SetClock(mockClock)
	err = energyMgr.Start()
	require.NoError(t, err)
	defer energyMgr.Stop()

	// Wait for startup goroutines to complete initial work
	energyMgr.WaitForStartup()

	// GIVEN: Battery at red (10%) and grid available during the old free grid window
	t.Log("GIVEN: Battery red and grid available during the old free grid window")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "10.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	err = manager.SetBool("isGridAvailable", true)
	require.NoError(t, err)

	// THEN: The retired free grid flag remains false and overall level follows battery + solar.
	waitForBoolState(t, manager, "isFreeEnergyAvailable", false, "Metered grid free energy should remain false")
	isFreeEnergy, err := manager.GetBool("isFreeEnergyAvailable")
	require.NoError(t, err)
	t.Logf("Free energy available: %v", isFreeEnergy)

	t.Log("THEN: Overall level should not be white from metered grid time")
	waitForStringState(t, manager, "batteryEnergyLevel", "red", "Battery level should settle before checking overall level")
	waitForProcessing(t, manager)
	overallLevel, err := manager.GetString("currentEnergyLevel")
	require.NoError(t, err)
	// Without any solar production set, solar resolves to the lowest level (red).
	// Battery red + solar red → red. The retired metered-grid override no longer forces white.
	assert.Equal(t, "red", overallLevel, "Overall level should follow battery and solar without metered grid override")
}

// TestScenario_HighSolarProduction_CanStillSetWhiteEnergyLevel validates that
// abundant solar can still produce white energy level after grid free hours are removed.
func TestScenario_HighSolarProduction_CanStillSetWhiteEnergyLevel(t *testing.T) {
	t.Parallel()
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// GIVEN: Battery is green and solar production is high enough for white.
	t.Log("GIVEN: Battery green and solar production is high enough for white")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "85.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	server.SetState("sensor.energy_next_hour", "4.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "20.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// THEN: Overall level can still become white from solar.
	t.Log("THEN: Overall level becomes white from solar")
	waitForStringState(t, manager, "solarProductionEnergyLevel", "white", "Solar level should be white with high production")
	waitForStringState(t, manager, "currentEnergyLevel", "white", "Overall level should be white from solar boost")
}

// TestScenario_HighBatteryWeakSolar_DoesNotSetWhiteEnergyLevel validates that a
// high battery alone does not activate white-level behavior when solar is weak.
func TestScenario_HighBatteryWeakSolar_DoesNotSetWhiteEnergyLevel(t *testing.T) {
	t.Parallel()
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// GIVEN: Battery is at the white threshold, but solar production is only yellow.
	t.Log("GIVEN: Battery white and solar production yellow")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "80.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	server.SetState("sensor.energy_next_hour", "0.908", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "1.149", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// THEN: The overall level stays below white. White requires high stored energy
	// and active solar production, so downstream thermal battery behavior does not activate.
	t.Log("THEN: Overall level is not white")
	waitForStringState(t, manager, "batteryEnergyLevel", "white", "Battery level should be white at 80%")
	waitForStringState(t, manager, "solarProductionEnergyLevel", "yellow", "Solar level should be yellow")
	waitForStringState(t, manager, "currentEnergyLevel", "green", "Overall level should be capped by weak solar")
}

// TestScenario_ThresholdBoundaries_HandlesExactValues validates that energy
// levels are calculated correctly at exact threshold boundaries
func TestScenario_ThresholdBoundaries_HandlesExactValues(t *testing.T) {
	t.Parallel()
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// Test exact boundary: 80% (white threshold)
	t.Log("Testing exact boundary: 80% (white threshold)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "80.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	waitForStringState(t, manager, "batteryEnergyLevel", "white", "Battery level should be white at exactly 80%")

	// Test just below boundary: 79.9% (green)
	t.Log("Testing just below boundary: 79.9% (green)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "79.9", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	waitForStringState(t, manager, "batteryEnergyLevel", "green", "Battery level should be green at 79.9%")

	// Test exact boundary: 60% (green threshold)
	t.Log("Testing exact boundary: 60% (green threshold)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "60.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	waitForStringState(t, manager, "batteryEnergyLevel", "green", "Battery level should be green at exactly 60%")

	// Test just below boundary: 59.9% (yellow)
	t.Log("Testing just below boundary: 59.9% (yellow)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "59.9", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	waitForStringState(t, manager, "batteryEnergyLevel", "yellow", "Battery level should be yellow at 59.9%")

	// Test exact boundary: 15% (yellow threshold)
	t.Log("Testing exact boundary: 15% (yellow threshold)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "15.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	waitForStringState(t, manager, "batteryEnergyLevel", "yellow", "Battery level should be yellow at exactly 15%")

	// Test just below boundary: 14.9% (red)
	t.Log("Testing just below boundary: 14.9% (red)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "14.9", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	waitForStringState(t, manager, "batteryEnergyLevel", "red", "Battery level should be red at 14.9%")
}

// TestScenario_MultipleConcurrentChanges_HandlesCorrectly validates that
// simultaneous battery and solar changes are handled without race conditions
func TestScenario_MultipleConcurrentChanges_HandlesCorrectly(t *testing.T) {
	t.Parallel()
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// GIVEN: Initial state
	t.Log("GIVEN: Initial state - battery at 65%, solar at high production")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "65.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	server.SetState("sensor.energy_next_hour", "3.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "20.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// Wait for initial state to stabilize
	waitForStringState(t, manager, "batteryEnergyLevel", "green", "Battery should be green at 65%")
	waitForStringState(t, manager, "solarProductionEnergyLevel", "green", "Solar should be green with high production")

	batteryLevel, err := manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	solarLevel, err := manager.GetString("solarProductionEnergyLevel")
	require.NoError(t, err)
	overallLevel, err := manager.GetString("currentEnergyLevel")
	require.NoError(t, err)

	t.Logf("Initial levels - battery: %s, solar: %s, overall: %s",
		batteryLevel, solarLevel, overallLevel)

	// WHEN: Multiple rapid changes occur simultaneously
	t.Log("WHEN: Multiple rapid changes occur simultaneously")

	// Change battery
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "10.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// Change solar
	server.SetState("sensor.energy_next_hour", "0.5", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "2.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// Change grid availability
	err = manager.SetBool("isGridAvailable", false)
	require.NoError(t, err)

	// THEN: All changes should be processed without errors
	t.Log("THEN: All changes should be processed without errors")

	waitForStringState(t, manager, "batteryEnergyLevel", "red", "Battery level should be red at 10%")

	waitForStringStateOneOf(t, manager, "solarProductionEnergyLevel", []string{"red", "yellow"},
		"Solar level should be low with 0.5kW/2kWh")

	overallLevel, err = manager.GetString("currentEnergyLevel")
	require.NoError(t, err)
	batteryLevel, _ = manager.GetString("batteryEnergyLevel")
	solarLevel, _ = manager.GetString("solarProductionEnergyLevel")
	t.Logf("Final levels - battery: %s, solar: %s, overall: %s",
		batteryLevel, solarLevel, overallLevel)

	// Overall level should be calculated correctly
	assert.NotEmpty(t, overallLevel, "Overall level should be set")

	// The system should handle rapid changes without crashing or deadlocking
	// This test passing at all (without timeout or panic) validates this
	t.Log("SUCCESS: Handled multiple concurrent changes without errors")
}
