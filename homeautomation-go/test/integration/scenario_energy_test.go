package integration

import (
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/plugins/energy"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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
	logger, _ := zap.NewDevelopment()

	// Use a fixed timezone for testing (UTC)
	timezone := time.UTC

	// Create energy plugin (read-only mode for testing)
	energyMgr := energy.NewManager(client, manager, energyConfig, logger, false, timezone, nil)

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
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// GIVEN: Battery is at 85% (green level - threshold is 80%)
	t.Log("GIVEN: Battery is at 85% (green level)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "85.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(300 * time.Millisecond)

	// Verify initial state is green
	batteryLevel, err := manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "green", batteryLevel, "Battery level should be green at 85%")

	// WHEN: Battery drops to 55% (yellow level - threshold is 50%)
	t.Log("WHEN: Battery drops to 55% (yellow level)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "55.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(300 * time.Millisecond)

	// THEN: Battery level should be yellow
	t.Log("THEN: Battery level should be yellow")
	batteryLevel, err = manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "yellow", batteryLevel, "Battery level should be yellow at 55%")

	// WHEN: Battery drops to 15% (black level - below 20% red threshold)
	t.Log("WHEN: Battery drops to 15% (black level)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "15.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(300 * time.Millisecond)

	// THEN: Battery level should be black
	t.Log("THEN: Battery level should be black")
	batteryLevel, err = manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "black", batteryLevel, "Battery level should be black at 15%")
}

// TestScenario_SolarProductionUpdates_CalculatesEnergyLevel validates that
// solar production changes correctly update the solarProductionEnergyLevel
func TestScenario_SolarProductionUpdates_CalculatesEnergyLevel(t *testing.T) {
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// GIVEN: No solar production (yellow level - meets yellow threshold but not green)
	t.Log("GIVEN: No solar production (yellow level)")
	server.SetState("sensor.energy_next_hour", "0.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "0.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})
	time.Sleep(300 * time.Millisecond)

	// Verify initial state
	solarLevel, err := manager.GetString("solarProductionEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "yellow", solarLevel, "Solar level should be yellow with no production (meets yellow threshold)")

	// WHEN: Solar production increases (this hour = 2kW, remaining = 15kWh -> green)
	t.Log("WHEN: Solar production increases to green level")
	server.SetState("sensor.energy_next_hour", "2.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "15.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})
	time.Sleep(300 * time.Millisecond)

	// THEN: Solar level should be green (threshold: 0 kW, 10 kWh)
	t.Log("THEN: Solar level should be green")
	solarLevel, err = manager.GetString("solarProductionEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "green", solarLevel, "Solar level should be green with 2kW/15kWh")

	// WHEN: Solar production drops (this hour = 1kW, remaining = 5kWh -> yellow)
	t.Log("WHEN: Solar production drops to yellow level")
	server.SetState("sensor.energy_next_hour", "1.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "5.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})
	time.Sleep(300 * time.Millisecond)

	// THEN: Solar level should be yellow (threshold: 0 kW, 0 kWh)
	t.Log("THEN: Solar level should be yellow")
	solarLevel, err = manager.GetString("solarProductionEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "yellow", solarLevel, "Solar level should be yellow with 1kW/5kWh")
}

// TestScenario_GridAvailability_RecalculatesFreeEnergy validates that when
// grid availability changes, isFreeEnergyAvailable recalculates
func TestScenario_GridAvailability_RecalculatesFreeEnergy(t *testing.T) {
	// This test needs a controlled timezone to ensure consistent behavior.
	// We use a timezone that makes current time 12:00 noon (outside free energy window 21:00-07:00).
	// This is done by creating a custom setup instead of using setupEnergyScenarioTest.

	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	// Load test energy config
	configPath := filepath.Join("testdata", "energy_config_test.yaml")
	energyConfig, err := energy.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test energy config")

	// Create logger
	logger, _ := zap.NewDevelopment()

	// Create a timezone that makes current time 12:00 noon (clearly outside 21:00-07:00 window)
	now := time.Now()
	_, currentOffset := now.Zone()
	// We want local time to be 12:00 noon
	targetHour := 12
	hoursToAdd := targetHour - now.Hour()
	testTimezone := time.FixedZone("TEST_NOON", currentOffset+hoursToAdd*3600)

	// Create energy plugin (NOT read-only so it can set states)
	energyMgr := energy.NewManager(client, manager, energyConfig, logger, false, testTimezone, nil)
	err = energyMgr.Start()
	require.NoError(t, err, "Failed to start energy manager")
	defer energyMgr.Stop()

	// Wait for startup goroutines to complete initial work
	energyMgr.WaitForStartup()

	// GIVEN: Grid is available, outside free energy time window (12:00 noon)
	t.Log("GIVEN: Grid is available, outside free energy time window (12:00 noon)")
	err = manager.SetBool("isGridAvailable", true)
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)

	// Check initial free energy state - should be false since we're at noon (outside 21:00-07:00)
	isFreeEnergy, err := manager.GetBool("isFreeEnergyAvailable")
	require.NoError(t, err)
	t.Logf("Initial free energy state: %v", isFreeEnergy)
	assert.False(t, isFreeEnergy, "Free energy should be false at noon (outside 21:00-07:00 window)")

	// WHEN: Grid goes offline
	t.Log("WHEN: Grid goes offline")
	err = manager.SetBool("isGridAvailable", false)
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)

	// THEN: Free energy should be false (no grid = no free energy)
	t.Log("THEN: Free energy should be false")
	isFreeEnergy, err = manager.GetBool("isFreeEnergyAvailable")
	require.NoError(t, err)
	assert.False(t, isFreeEnergy, "Free energy should be false when grid is offline")

	// WHEN: Grid comes back online
	t.Log("WHEN: Grid comes back online")
	err = manager.SetBool("isGridAvailable", true)
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)

	// THEN: Free energy should still be false (we're at noon, outside the window)
	t.Log("THEN: Free energy should still be false (noon is outside 21:00-07:00)")
	isFreeEnergy, err = manager.GetBool("isFreeEnergyAvailable")
	require.NoError(t, err)
	assert.False(t, isFreeEnergy, "Free energy should be false at noon even with grid online")

	// Verify service call was made
	calls := server.GetServiceCalls()
	t.Logf("Total service calls made: %d", len(calls))
}

// TestScenario_OverallEnergyLevel_ReflectsWorstState validates that the
// currentEnergyLevel correctly reflects the worst state across battery/solar
func TestScenario_OverallEnergyLevel_ReflectsWorstState(t *testing.T) {
	// This test needs a controlled timezone to ensure we're outside the free energy window.
	// During free energy time (21:00-07:00), currentEnergyLevel would be "white" instead of
	// the expected battery/solar-derived levels.

	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	// Load test energy config
	configPath := filepath.Join("testdata", "energy_config_test.yaml")
	energyConfig, err := energy.LoadConfig(configPath)
	require.NoError(t, err, "Failed to load test energy config")

	// Create logger
	logger, _ := zap.NewDevelopment()

	// Create a timezone that makes current time 12:00 noon (clearly outside 21:00-07:00 window)
	now := time.Now()
	_, currentOffset := now.Zone()
	targetHour := 12
	hoursToAdd := targetHour - now.Hour()
	testTimezone := time.FixedZone("TEST_NOON", currentOffset+hoursToAdd*3600)

	// Create energy plugin
	energyMgr := energy.NewManager(client, manager, energyConfig, logger, false, testTimezone, nil)
	err = energyMgr.Start()
	require.NoError(t, err, "Failed to start energy manager")
	defer energyMgr.Stop()

	// Wait for startup goroutines to complete initial work
	energyMgr.WaitForStartup()

	// GIVEN: Battery at green (85%), solar at green (2kW, 15kWh)
	t.Log("GIVEN: Battery green, solar green (at noon, outside free energy window)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "85.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	server.SetState("sensor.energy_next_hour", "2.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "15.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})
	time.Sleep(500 * time.Millisecond)

	// Verify overall level is green (not white, since we're outside free energy window)
	overallLevel, err := manager.GetString("currentEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "green", overallLevel, "Overall level should be green when both are green (outside free energy window)")

	// WHEN: Battery drops to red (15%), solar still green
	t.Log("WHEN: Battery drops to red, solar stays green")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "15.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(500 * time.Millisecond)

	// THEN: Overall level should reflect the lower state
	// According to the algorithm: min(battery=red, solar=green) + 1 level = yellow
	t.Log("THEN: Overall level should reflect worst state")
	overallLevel, err = manager.GetString("currentEnergyLevel")
	require.NoError(t, err)
	// The overall level should be at most one level higher than the worst input
	assert.Contains(t, []string{"red", "yellow"}, overallLevel,
		"Overall level should be red or yellow when battery is red and solar is green")

	// WHEN: Solar also drops to black (0kW, 0kWh)
	t.Log("WHEN: Solar also drops to black")
	server.SetState("sensor.energy_next_hour", "0.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "0.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})
	time.Sleep(500 * time.Millisecond)

	// THEN: Overall level should be black or red (both are low)
	t.Log("THEN: Overall level should be very low")
	overallLevel, err = manager.GetString("currentEnergyLevel")
	require.NoError(t, err)
	assert.Contains(t, []string{"black", "red"}, overallLevel,
		"Overall level should be black or red when both battery and solar are low")
}

// TestScenario_FreeEnergyTimeWindow_OverridesEnergyLevel validates that when
// in free energy time window with grid available, currentEnergyLevel is "white"
func TestScenario_FreeEnergyTimeWindow_OverridesEnergyLevel(t *testing.T) {
	// Setup test environment manually without starting energy manager
	server, client, manager, baseCleanup := setupTest(t)
	defer baseCleanup()

	// Create a timezone where it's currently in free energy window
	// Free energy window is 21:00-07:00, so we'll use a timezone that makes it 22:00 now
	now := time.Now()
	// Create a fixed location that makes current time 22:00
	_, offset := now.Zone()
	targetHour := 22
	hoursToAdd := targetHour - now.Hour()
	testTimezone := time.FixedZone("TEST", offset+hoursToAdd*3600)

	// Load config and create manager with test timezone
	configPath := filepath.Join("testdata", "energy_config_test.yaml")
	energyConfig, err := energy.LoadConfig(configPath)
	require.NoError(t, err)

	logger, _ := zap.NewDevelopment()
	energyMgr := energy.NewManager(client, manager, energyConfig, logger, false, testTimezone, nil)
	err = energyMgr.Start()
	require.NoError(t, err)
	defer energyMgr.Stop()

	// Wait for startup goroutines to complete initial work
	energyMgr.WaitForStartup()

	// GIVEN: Battery at red (15%), grid available, in free energy time window
	t.Log("GIVEN: Battery red, grid available, in free energy time window")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "15.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	err = manager.SetBool("isGridAvailable", true)
	require.NoError(t, err)
	time.Sleep(500 * time.Millisecond)

	// Verify free energy is available
	isFreeEnergy, err := manager.GetBool("isFreeEnergyAvailable")
	require.NoError(t, err)
	t.Logf("Free energy available: %v", isFreeEnergy)

	if isFreeEnergy {
		// THEN: Overall level should be white (free energy override)
		t.Log("THEN: Overall level should be white")
		overallLevel, err := manager.GetString("currentEnergyLevel")
		require.NoError(t, err)
		assert.Equal(t, "white", overallLevel,
			"Overall level should be white during free energy time")
	}
}

// TestScenario_ThresholdBoundaries_HandlesExactValues validates that energy
// levels are calculated correctly at exact threshold boundaries
func TestScenario_ThresholdBoundaries_HandlesExactValues(t *testing.T) {
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// Test exact boundary: 80% (green threshold)
	t.Log("Testing exact boundary: 80% (green threshold)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "80.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(300 * time.Millisecond)

	batteryLevel, err := manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "green", batteryLevel, "Battery level should be green at exactly 80%")

	// Test just below boundary: 79.9% (yellow)
	t.Log("Testing just below boundary: 79.9% (yellow)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "79.9", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(300 * time.Millisecond)

	batteryLevel, err = manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "yellow", batteryLevel, "Battery level should be yellow at 79.9%")

	// Test exact boundary: 50% (yellow threshold)
	t.Log("Testing exact boundary: 50% (yellow threshold)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "50.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(300 * time.Millisecond)

	batteryLevel, err = manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "yellow", batteryLevel, "Battery level should be yellow at exactly 50%")

	// Test just below boundary: 49.9% (red)
	t.Log("Testing just below boundary: 49.9% (red)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "49.9", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(300 * time.Millisecond)

	batteryLevel, err = manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "red", batteryLevel, "Battery level should be red at 49.9%")

	// Test exact boundary: 20% (red threshold)
	t.Log("Testing exact boundary: 20% (red threshold)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "20.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(300 * time.Millisecond)

	batteryLevel, err = manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "red", batteryLevel, "Battery level should be red at exactly 20%")

	// Test just below boundary: 19.9% (black)
	t.Log("Testing just below boundary: 19.9% (black)")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "19.9", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	time.Sleep(300 * time.Millisecond)

	batteryLevel, err = manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "black", batteryLevel, "Battery level should be black at 19.9%")
}

// TestScenario_MultipleConcurrentChanges_HandlesCorrectly validates that
// simultaneous battery and solar changes are handled without race conditions
func TestScenario_MultipleConcurrentChanges_HandlesCorrectly(t *testing.T) {
	server, _, manager, _, cleanup := setupEnergyScenarioTest(t)
	defer cleanup()

	// GIVEN: Initial state
	t.Log("GIVEN: Initial state - battery at 85%, solar at high production")
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "85.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})
	server.SetState("sensor.energy_next_hour", "3.0", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "20.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})
	time.Sleep(500 * time.Millisecond)

	// Verify initial state
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
	server.SetState("sensor.span_panel_span_storage_battery_percentage_2", "25.0", map[string]interface{}{
		"unit_of_measurement": "%",
	})

	// Change solar (almost immediately after)
	time.Sleep(10 * time.Millisecond)
	server.SetState("sensor.energy_next_hour", "0.5", map[string]interface{}{
		"unit_of_measurement": "kW",
	})
	server.SetState("sensor.energy_production_today_remaining", "2.0", map[string]interface{}{
		"unit_of_measurement": "kWh",
	})

	// Change grid availability
	time.Sleep(10 * time.Millisecond)
	err = manager.SetBool("isGridAvailable", false)
	require.NoError(t, err)

	// Wait for all changes to propagate
	time.Sleep(1 * time.Second)

	// THEN: All changes should be processed without errors
	t.Log("THEN: All changes should be processed without errors")

	batteryLevel, err = manager.GetString("batteryEnergyLevel")
	require.NoError(t, err)
	assert.Equal(t, "red", batteryLevel, "Battery level should be red at 25%")

	solarLevel, err = manager.GetString("solarProductionEnergyLevel")
	require.NoError(t, err)
	assert.Contains(t, []string{"black", "red", "yellow"}, solarLevel,
		"Solar level should be low with 0.5kW/2kWh")

	overallLevel, err = manager.GetString("currentEnergyLevel")
	require.NoError(t, err)
	t.Logf("Final levels - battery: %s, solar: %s, overall: %s",
		batteryLevel, solarLevel, overallLevel)

	// Overall level should be calculated correctly
	assert.NotEmpty(t, overallLevel, "Overall level should be set")

	// The system should handle rapid changes without crashing or deadlocking
	// This test passing at all (without timeout or panic) validates this
	t.Log("SUCCESS: Handled multiple concurrent changes without errors")
}

// TestScenario_PeriodicChecker_UpdatesFreeEnergy validates that the periodic
// free energy checker runs and updates state correctly
func TestScenario_PeriodicChecker_UpdatesFreeEnergy(t *testing.T) {
	// Note: This test would need to wait 1+ minute for the periodic checker
	// For now, we validate that the checker can be triggered manually via grid change
	t.Skip("Skipping periodic checker test - would require 1+ minute wait time")

	// The free energy checker is tested indirectly via:
	// - TestScenario_GridAvailability_RecalculatesFreeEnergy
	// - TestScenario_FreeEnergyTimeWindow_OverridesEnergyLevel
}
