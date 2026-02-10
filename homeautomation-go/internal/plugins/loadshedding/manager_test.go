package loadshedding

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
)

func TestLoadShedding_EnergyStateRed(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them off)
	mockClient.SetState(thermostatHoldHouse, "off", nil)
	mockClient.SetState(thermostatHoldSuite, "off", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	// Initialize state
	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)
	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set energy state to red
	err = stateManager.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)

	// Give time for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify service calls
	calls := mockClient.GetServiceCalls()
	assert.GreaterOrEqual(t, len(calls), 3, "Expected at least 3 service calls (thermostat hold, temp range, EV charger)")

	// Check for switch.turn_on call (thermostat holds)
	foundSwitchOn := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			foundSwitchOn = true
			entities, ok := call.Data["entity_id"].([]string)
			assert.True(t, ok, "entity_id should be []string")
			assert.Contains(t, entities, thermostatHoldHouse)
			assert.Contains(t, entities, thermostatHoldSuite)
		}
	}
	assert.True(t, foundSwitchOn, "Expected switch.turn_on service call for thermostat holds")

	// Check for climate.set_temperature call
	foundSetTemp := false
	for _, call := range calls {
		if call.Domain == "climate" && call.Service == "set_temperature" {
			foundSetTemp = true
			entities, ok := call.Data["entity_id"].([]string)
			assert.True(t, ok, "entity_id should be []string")
			assert.Contains(t, entities, climateHouse)
			assert.Contains(t, entities, climateSuite)
			assert.Equal(t, tempLowRestricted, call.Data["target_temp_low"])
			assert.Equal(t, tempHighRestricted, call.Data["target_temp_high"])
		}
	}
	assert.True(t, foundSetTemp, "Expected climate.set_temperature service call")

	// Check for switch.turn_off call (EV charger)
	foundEVChargerOff := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok && entityID == evChargerSwitch {
				foundEVChargerOff = true
			}
		}
	}
	assert.True(t, foundEVChargerOff, "Expected switch.turn_off service call for EV charger")
}

func TestLoadShedding_EnergyStateBlack(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them off)
	mockClient.SetState(thermostatHoldHouse, "off", nil)
	mockClient.SetState(thermostatHoldSuite, "off", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)
	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set energy state to black
	err = stateManager.SetString("currentEnergyLevel", "black")
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Verify service calls (should be same as red)
	calls := mockClient.GetServiceCalls()
	assert.GreaterOrEqual(t, len(calls), 3, "Expected at least 3 service calls (thermostat hold, temp range, EV charger)")

	foundSwitchOn := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			foundSwitchOn = true
		}
	}
	assert.True(t, foundSwitchOn, "Expected switch.turn_on for thermostat holds")

	// Check for switch.turn_off call (EV charger)
	foundEVChargerOff := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok && entityID == evChargerSwitch {
				foundEVChargerOff = true
			}
		}
	}
	assert.True(t, foundEVChargerOff, "Expected switch.turn_off service call for EV charger")
}

func TestLoadShedding_EnergyStateGreen(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them on - load shedding active)
	mockClient.SetState(thermostatHoldHouse, "on", nil)
	mockClient.SetState(thermostatHoldSuite, "on", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)
	// Manually set loadSheddingOn to true to simulate that load shedding was previously enabled
	ls.loadSheddingOn = true

	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set energy state to green (should disable load shedding)
	err = stateManager.SetString("currentEnergyLevel", "green")
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Verify service calls
	calls := mockClient.GetServiceCalls()
	assert.GreaterOrEqual(t, len(calls), 2, "Expected at least 2 service calls (thermostat hold off, EV charger on)")

	// Check for switch.turn_off call (thermostat holds)
	foundThermostatOff := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			entities, ok := call.Data["entity_id"].([]string)
			if ok {
				foundThermostatOff = true
				assert.Contains(t, entities, thermostatHoldHouse)
				assert.Contains(t, entities, thermostatHoldSuite)
			}
		}
	}
	assert.True(t, foundThermostatOff, "Expected switch.turn_off service call for thermostat holds")

	// Check for switch.turn_on call (EV charger)
	foundEVChargerOn := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok && entityID == evChargerSwitch {
				foundEVChargerOn = true
			}
		}
	}
	assert.True(t, foundEVChargerOn, "Expected switch.turn_on service call for EV charger")
}

func TestLoadShedding_EnergyStateWhite(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them on - load shedding active)
	mockClient.SetState(thermostatHoldHouse, "on", nil)
	mockClient.SetState(thermostatHoldSuite, "on", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)
	// Manually set loadSheddingOn to true to simulate that load shedding was previously enabled
	ls.loadSheddingOn = true

	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set energy state to white (should disable load shedding)
	err = stateManager.SetString("currentEnergyLevel", "white")
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Verify service calls (should be same as green)
	calls := mockClient.GetServiceCalls()
	assert.GreaterOrEqual(t, len(calls), 2, "Expected at least 2 service calls (thermostat hold off, EV charger on)")

	foundThermostatOff := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			_, ok := call.Data["entity_id"].([]string)
			if ok {
				foundThermostatOff = true
			}
		}
	}
	assert.True(t, foundThermostatOff, "Expected switch.turn_off for thermostat holds")

	// Check for switch.turn_on call (EV charger)
	foundEVChargerOn := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok && entityID == evChargerSwitch {
				foundEVChargerOn = true
			}
		}
	}
	assert.True(t, foundEVChargerOn, "Expected switch.turn_on service call for EV charger")
}

func TestLoadShedding_RateLimiting(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them off)
	mockClient.SetState(thermostatHoldHouse, "off", nil)
	mockClient.SetState(thermostatHoldSuite, "off", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)

	// Override minimum action interval for testing
	// (In production, we'd use dependency injection for the time source)
	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// First change to red
	err = stateManager.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	initialCallCount := len(mockClient.GetServiceCalls())
	assert.Greater(t, initialCallCount, 0, "First action should execute")

	// Clear service calls to make counting easier
	mockClient.ClearServiceCalls()

	// Immediately change to green (should be rate limited)
	err = stateManager.SetString("currentEnergyLevel", "green")
	assert.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// Should only have the SetString call, not the load shedding action (rate limited)
	finalCallCount := len(mockClient.GetServiceCalls())
	assert.Equal(t, 1, finalCallCount,
		"Should only have SetString call, load shedding action should be rate limited")
}

func TestLoadShedding_StartStop(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them off)
	mockClient.SetState(thermostatHoldHouse, "off", nil)
	mockClient.SetState(thermostatHoldSuite, "off", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)

	// Start
	err = ls.Start()
	assert.NoError(t, err)
	assert.True(t, ls.enabled)

	// Try starting again (should fail)
	err = ls.Start()
	assert.Error(t, err)

	// Stop
	ls.Stop()
	assert.False(t, ls.enabled)

	// Stopping again should be safe
	ls.Stop()
	assert.False(t, ls.enabled)
}

func TestLoadShedding_UnknownState(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them off)
	mockClient.SetState(thermostatHoldHouse, "off", nil)
	mockClient.SetState(thermostatHoldSuite, "off", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)
	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set unknown state
	err = stateManager.SetString("currentEnergyLevel", "purple")
	assert.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// Should only have the SetString call, no load shedding actions for unknown state
	calls := mockClient.GetServiceCalls()
	assert.Equal(t, 1, len(calls), "Unknown state should only have SetString call, no load shedding actions")
	// Verify it's the SetString call
	assert.Equal(t, "input_text", calls[0].Domain)
	assert.Equal(t, "set_value", calls[0].Service)
}

func TestLoadShedding_RedToGreenTransition(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them off)
	mockClient.SetState(thermostatHoldHouse, "off", nil)
	mockClient.SetState(thermostatHoldSuite, "off", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)

	// Manually set last action to past to avoid rate limiting
	ls.lastAction = time.Now().Add(-2 * time.Hour)

	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set to red
	err = stateManager.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	// Wait to avoid rate limiting
	ls.lastAction = time.Now().Add(-2 * time.Hour)
	time.Sleep(100 * time.Millisecond)

	// Set to green
	err = stateManager.SetString("currentEnergyLevel", "green")
	assert.NoError(t, err)
	time.Sleep(100 * time.Millisecond)

	calls := mockClient.GetServiceCalls()

	// Should have both turn_on and turn_off calls
	foundTurnOn := false
	foundTurnOff := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			foundTurnOn = true
		}
		if call.Domain == "switch" && call.Service == "turn_off" {
			foundTurnOff = true
		}
	}

	assert.True(t, foundTurnOn, "Should have turn_on from red state")
	assert.True(t, foundTurnOff, "Should have turn_off from green state")
}

func TestManagerReset(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up initial state
	stateManager.SetString("currentEnergyLevel", "high")

	manager := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Reset should re-evaluate thermostat control
	err = manager.Reset()
	assert.NoError(t, err)
}

// TestLoadShedding_DeferredActionAfterRateLimit tests that when an action is
// rate-limited, it gets automatically retried after the rate limit expires.
// This is the bug fix for: when energy goes red->white quickly, the disable
// action was rate-limited and never retried, leaving thermostats in hold mode.
func TestLoadShedding_DeferredActionAfterRateLimit(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them off)
	mockClient.SetState(thermostatHoldHouse, "off", nil)
	mockClient.SetState(thermostatHoldSuite, "off", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)

	// Use a short rate limit interval for testing (100ms instead of 1 hour)
	ls.SetRateLimitIntervalForTesting(100 * time.Millisecond)

	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Step 1: Set energy state to red (enables load shedding)
	err = stateManager.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// Verify load shedding was enabled
	calls := mockClient.GetServiceCalls()
	foundSwitchOn := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			foundSwitchOn = true
		}
	}
	assert.True(t, foundSwitchOn, "Load shedding should be enabled (switch.turn_on called)")

	// Simulate thermostats now being in hold mode
	mockClient.SetState(thermostatHoldHouse, "on", nil)
	mockClient.SetState(thermostatHoldSuite, "on", nil)

	// Clear service calls for cleaner verification
	mockClient.ClearServiceCalls()

	// Step 2: Immediately set energy state to white (should be rate-limited)
	err = stateManager.SetString("currentEnergyLevel", "white")
	assert.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// At this point, the disable action should be rate-limited (not executed yet)
	// We don't assert on this because the deferred action mechanism is tested below

	// Step 3: Wait for the rate limit to expire plus buffer for deferred action
	time.Sleep(150 * time.Millisecond)

	// Step 4: Verify the deferred disable action was executed
	calls = mockClient.GetServiceCalls()
	foundThermostatOff := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			entities, ok := call.Data["entity_id"].([]string)
			if ok {
				foundThermostatOff = true
				assert.Contains(t, entities, thermostatHoldHouse)
				assert.Contains(t, entities, thermostatHoldSuite)
			}
		}
	}
	assert.True(t, foundThermostatOff,
		"Deferred action should execute switch.turn_off for thermostat holds after rate limit expires")

	// Verify EV charger was turned on in deferred action
	foundEVChargerOn := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok && entityID == evChargerSwitch {
				foundEVChargerOn = true
			}
		}
	}
	assert.True(t, foundEVChargerOn,
		"Deferred action should execute switch.turn_on for EV charger after rate limit expires")

	// Verify load shedding state is now disabled
	assert.False(t, ls.IsLoadSheddingOn(), "Load shedding should be disabled after deferred action")
}

// TestLoadShedding_DeferredActionCancelledByNewAction tests that if a new
// action comes in that supersedes the deferred action, the deferred action
// is cancelled appropriately.
func TestLoadShedding_DeferredActionCancelledByNewAction(t *testing.T) {
	t.Parallel()
	logger := testlogger.New()
	mockClient := ha.NewMockClient()

	// Initialize thermostat hold switches in mock (start with them off)
	mockClient.SetState(thermostatHoldHouse, "off", nil)
	mockClient.SetState(thermostatHoldSuite, "off", nil)

	stateManager := state.NewManager(mockClient, logger, false)

	err := stateManager.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), mockClient, stateManager, logger, false, nil)

	// Use a short rate limit interval for testing
	ls.SetRateLimitIntervalForTesting(200 * time.Millisecond)

	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Step 1: Set energy state to red (enables load shedding)
	err = stateManager.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// Simulate thermostats now being in hold mode
	mockClient.SetState(thermostatHoldHouse, "on", nil)
	mockClient.SetState(thermostatHoldSuite, "on", nil)

	mockClient.ClearServiceCalls()

	// Step 2: Set energy state to white (rate-limited, deferred)
	err = stateManager.SetString("currentEnergyLevel", "white")
	assert.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// Step 3: Before deferred action fires, go back to red
	// This should cancel the pending disable and keep load shedding on
	err = stateManager.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)
	time.Sleep(50 * time.Millisecond)

	// Step 4: Wait for what would have been the deferred action time
	time.Sleep(200 * time.Millisecond)

	// Verify that switch.turn_off was NOT called (deferred disable was cancelled)
	calls := mockClient.GetServiceCalls()
	foundSwitchOff := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			foundSwitchOff = true
		}
	}
	assert.False(t, foundSwitchOff,
		"Deferred disable should be cancelled when energy goes back to red")

	// Load shedding should still be on
	assert.True(t, ls.IsLoadSheddingOn(), "Load shedding should remain enabled")
}
