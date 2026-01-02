package loadshedding

import (
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

	ls := NewManager(mockClient, stateManager, logger, false, nil)
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
	assert.GreaterOrEqual(t, len(calls), 2, "Expected at least 2 service calls")

	// Check for switch.turn_on call
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
	assert.True(t, foundSwitchOn, "Expected switch.turn_on service call")

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

	ls := NewManager(mockClient, stateManager, logger, false, nil)
	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set energy state to black
	err = stateManager.SetString("currentEnergyLevel", "black")
	assert.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Verify service calls (should be same as red)
	calls := mockClient.GetServiceCalls()
	assert.GreaterOrEqual(t, len(calls), 2)

	foundSwitchOn := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_on" {
			foundSwitchOn = true
		}
	}
	assert.True(t, foundSwitchOn)
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

	ls := NewManager(mockClient, stateManager, logger, false, nil)
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
	assert.GreaterOrEqual(t, len(calls), 1)

	// Check for switch.turn_off call
	foundSwitchOff := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			foundSwitchOff = true
			entities, ok := call.Data["entity_id"].([]string)
			assert.True(t, ok, "entity_id should be []string")
			assert.Contains(t, entities, thermostatHoldHouse)
			assert.Contains(t, entities, thermostatHoldSuite)
		}
	}
	assert.True(t, foundSwitchOff, "Expected switch.turn_off service call")
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

	ls := NewManager(mockClient, stateManager, logger, false, nil)
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
	assert.GreaterOrEqual(t, len(calls), 1)

	foundSwitchOff := false
	for _, call := range calls {
		if call.Domain == "switch" && call.Service == "turn_off" {
			foundSwitchOff = true
		}
	}
	assert.True(t, foundSwitchOff)
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

	ls := NewManager(mockClient, stateManager, logger, false, nil)

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

	ls := NewManager(mockClient, stateManager, logger, false, nil)

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

	ls := NewManager(mockClient, stateManager, logger, false, nil)
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

	ls := NewManager(mockClient, stateManager, logger, false, nil)

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

	manager := NewManager(mockClient, stateManager, logger, false, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Reset should re-evaluate thermostat control
	err = manager.Reset()
	assert.NoError(t, err)
}
