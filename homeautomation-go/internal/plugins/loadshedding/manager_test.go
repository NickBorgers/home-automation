package loadshedding

import (
	"context"
	"testing"
	"time"

	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
)

// setupLoadSheddingEnv creates a test environment with thermostat hold switches initialized.
func setupLoadSheddingEnv(t *testing.T) *testutil.Env {
	t.Helper()
	env := testutil.NewEnv(t)
	env.MockHA.SetState(thermostatHoldHouse, "off", nil)
	env.MockHA.SetState(thermostatHoldSuite, "off", nil)
	err := env.StateMgr.SyncFromHA()
	assert.NoError(t, err)
	return env
}

func TestLoadShedding_EnergyStateRed(t *testing.T) {
	t.Parallel()
	env := setupLoadSheddingEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set energy state to red (handler executes synchronously via state subscriber)
	err = env.StateMgr.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)

	// Verify service calls
	calls := env.MockHA.GetServiceCalls()
	assert.GreaterOrEqual(t, len(calls), 3, "Expected at least 3 service calls (thermostat hold, temp range, EV charger)")

	// Check for switch.turn_on call (thermostat holds)
	call := testutil.AssertServiceCall(t, calls, "switch", "turn_on")
	entities, ok := call.Data["entity_id"].([]string)
	assert.True(t, ok, "entity_id should be []string")
	assert.Contains(t, entities, thermostatHoldHouse)
	assert.Contains(t, entities, thermostatHoldSuite)

	// Check for climate.set_temperature call
	climateCall := testutil.AssertServiceCall(t, calls, "climate", "set_temperature")
	climateEntities, ok := climateCall.Data["entity_id"].([]string)
	assert.True(t, ok, "entity_id should be []string")
	assert.Contains(t, climateEntities, climateHouse)
	assert.Contains(t, climateEntities, climateSuite)
	assert.Equal(t, tempLowRestricted, climateCall.Data["target_temp_low"])
	assert.Equal(t, tempHighRestricted, climateCall.Data["target_temp_high"])

	// Check for switch.turn_off call (EV charger)
	evCall := testutil.FindServiceCallWithEntity(calls, "switch", "turn_off", evChargerSwitch)
	assert.NotNil(t, evCall, "Expected switch.turn_off service call for EV charger")
}

func TestLoadShedding_EnergyStateBlack(t *testing.T) {
	t.Parallel()
	env := setupLoadSheddingEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set energy state to black (handler executes synchronously)
	err = env.StateMgr.SetString("currentEnergyLevel", "black")
	assert.NoError(t, err)

	// Verify service calls (should be same as red)
	calls := env.MockHA.GetServiceCalls()
	assert.GreaterOrEqual(t, len(calls), 3, "Expected at least 3 service calls (thermostat hold, temp range, EV charger)")

	testutil.AssertServiceCall(t, calls, "switch", "turn_on")

	// Check for switch.turn_off call (EV charger)
	evCall := testutil.FindServiceCallWithEntity(calls, "switch", "turn_off", evChargerSwitch)
	assert.NotNil(t, evCall, "Expected switch.turn_off service call for EV charger")
}

func TestLoadShedding_EnergyStateGreen(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	// Initialize thermostat hold switches in mock (start with them on - load shedding active)
	env.MockHA.SetState(thermostatHoldHouse, "on", nil)
	env.MockHA.SetState(thermostatHoldSuite, "on", nil)

	err := env.StateMgr.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	// Manually set loadSheddingOn to true to simulate that load shedding was previously enabled
	ls.loadSheddingOn = true

	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set energy state to green (handler executes synchronously, should disable load shedding)
	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	assert.NoError(t, err)

	// Verify service calls
	calls := env.MockHA.GetServiceCalls()
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
	evCall := testutil.FindServiceCallWithEntity(calls, "switch", "turn_on", evChargerSwitch)
	assert.NotNil(t, evCall, "Expected switch.turn_on service call for EV charger")
}

func TestLoadShedding_EnergyStateWhite(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	// Initialize thermostat hold switches in mock (start with them on - load shedding active)
	env.MockHA.SetState(thermostatHoldHouse, "on", nil)
	env.MockHA.SetState(thermostatHoldSuite, "on", nil)

	err := env.StateMgr.SyncFromHA()
	assert.NoError(t, err)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	// Manually set loadSheddingOn to true to simulate that load shedding was previously enabled
	ls.loadSheddingOn = true

	err = ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set energy state to white (handler executes synchronously, should disable load shedding)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	assert.NoError(t, err)

	// Verify service calls (should be same as green)
	calls := env.MockHA.GetServiceCalls()
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
	evCall := testutil.FindServiceCallWithEntity(calls, "switch", "turn_on", evChargerSwitch)
	assert.NotNil(t, evCall, "Expected switch.turn_on service call for EV charger")
}

func TestLoadShedding_RateLimiting(t *testing.T) {
	t.Parallel()
	env := setupLoadSheddingEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)

	// Override minimum action interval for testing
	err := ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// First change to red (handler executes synchronously)
	err = env.StateMgr.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)

	initialCallCount := len(env.MockHA.GetServiceCalls())
	assert.Greater(t, initialCallCount, 0, "First action should execute")

	// Snapshot service call count to make counting easier
	snapshot := env.MockHA.ServiceCallCount()

	// Immediately change to green (should be rate limited)
	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	assert.NoError(t, err)

	// Should only have the SetString call, not the load shedding action (rate limited)
	finalCallCount := len(env.MockHA.GetServiceCallsSince(snapshot))
	assert.Equal(t, 1, finalCallCount,
		"Should only have SetString call, load shedding action should be rate limited")
}

func TestLoadShedding_StartStop(t *testing.T) {
	t.Parallel()
	env := setupLoadSheddingEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)

	// Start
	err := ls.Start()
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
	env := setupLoadSheddingEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)
	err := ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set unknown state (handler executes synchronously)
	err = env.StateMgr.SetString("currentEnergyLevel", "purple")
	assert.NoError(t, err)

	// Should only have the SetString call, no load shedding actions for unknown state
	calls := env.MockHA.GetServiceCalls()
	assert.Equal(t, 1, len(calls), "Unknown state should only have SetString call, no load shedding actions")
	// Verify it's the SetString call
	assert.Equal(t, "input_text", calls[0].Domain)
	assert.Equal(t, "set_value", calls[0].Service)
}

func TestLoadShedding_RedToGreenTransition(t *testing.T) {
	t.Parallel()
	env := setupLoadSheddingEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)

	// Manually set last action to past to avoid rate limiting
	ls.lastAction = time.Now().Add(-2 * time.Hour)

	err := ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Set to red (handler executes synchronously)
	err = env.StateMgr.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)

	// Reset rate limit to allow next action
	ls.lastAction = time.Now().Add(-2 * time.Hour)

	// Set to green (handler executes synchronously)
	err = env.StateMgr.SetString("currentEnergyLevel", "green")
	assert.NoError(t, err)

	calls := env.MockHA.GetServiceCalls()

	// Should have both turn_on and turn_off calls
	testutil.AssertServiceCall(t, calls, "switch", "turn_on")
	testutil.AssertServiceCall(t, calls, "switch", "turn_off")
}

func TestManagerReset(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	// Set up initial state
	env.StateMgr.SetString("currentEnergyLevel", "high")

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Reset should re-evaluate thermostat control
	err = manager.Reset()
	assert.NoError(t, err)
}

// TestLoadShedding_DeferredActionAfterRateLimit tests that when an action is
// rate-limited, it gets automatically retried after the rate limit expires.
func TestLoadShedding_DeferredActionAfterRateLimit(t *testing.T) {
	t.Parallel()
	env := setupLoadSheddingEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)

	// Use a short rate limit interval for testing (100ms instead of 1 hour)
	ls.SetRateLimitIntervalForTesting(100 * time.Millisecond)

	// Set up a channel to signal when the deferred action completes
	deferredDone := make(chan struct{}, 1)
	ls.SetDeferredActionDoneCallback(func() {
		deferredDone <- struct{}{}
	})

	err := ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Step 1: Set energy state to red (enables load shedding synchronously)
	err = env.StateMgr.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)

	// Verify load shedding was enabled
	calls := env.MockHA.GetServiceCalls()
	testutil.AssertServiceCall(t, calls, "switch", "turn_on")

	// Simulate thermostats now being in hold mode
	env.MockHA.SetState(thermostatHoldHouse, "on", nil)
	env.MockHA.SetState(thermostatHoldSuite, "on", nil)

	// Snapshot service call count for cleaner verification
	snapshot := env.MockHA.ServiceCallCount()

	// Step 2: Immediately set energy state to white (should be rate-limited and deferred)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	assert.NoError(t, err)

	// Step 3: Wait for the deferred action to complete via callback
	select {
	case <-deferredDone:
		// Deferred action executed
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for deferred action to execute")
	}

	// Step 4: Verify the deferred disable action was executed
	calls = env.MockHA.GetServiceCallsSince(snapshot)
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
	evCall := testutil.FindServiceCallWithEntity(calls, "switch", "turn_on", evChargerSwitch)
	assert.NotNil(t, evCall,
		"Deferred action should execute switch.turn_on for EV charger after rate limit expires")

	// Verify load shedding state is now disabled
	assert.False(t, ls.IsLoadSheddingOn(), "Load shedding should be disabled after deferred action")
}

// TestLoadShedding_DeferredActionCancelledByNewAction tests that if a new
// action comes in that supersedes the deferred action, the deferred action
// is cancelled appropriately.
func TestLoadShedding_DeferredActionCancelledByNewAction(t *testing.T) {
	t.Parallel()
	env := setupLoadSheddingEnv(t)

	ls := NewManager(context.Background(), env.MockHA, env.StateMgr, env.Logger, false, nil, nil)

	// Use a short rate limit interval for testing
	ls.SetRateLimitIntervalForTesting(200 * time.Millisecond)

	// Track if deferred action fires (it should NOT in this test)
	deferredFired := make(chan struct{}, 1)
	ls.SetDeferredActionDoneCallback(func() {
		deferredFired <- struct{}{}
	})

	err := ls.Start()
	assert.NoError(t, err)
	defer ls.Stop()

	// Step 1: Set energy state to red (enables load shedding synchronously)
	err = env.StateMgr.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)

	// Simulate thermostats now being in hold mode
	env.MockHA.SetState(thermostatHoldHouse, "on", nil)
	env.MockHA.SetState(thermostatHoldSuite, "on", nil)

	snapshot := env.MockHA.ServiceCallCount()

	// Step 2: Set energy state to white (rate-limited, deferred)
	err = env.StateMgr.SetString("currentEnergyLevel", "white")
	assert.NoError(t, err)

	// Step 3: Before deferred action fires, go back to red
	err = env.StateMgr.SetString("currentEnergyLevel", "red")
	assert.NoError(t, err)

	// Step 4: Wait for what would have been the deferred action time plus buffer.
	// The deferred action should NOT fire because it was cancelled.
	select {
	case <-deferredFired:
		t.Fatal("Deferred action should have been cancelled but it fired")
	case <-time.After(500 * time.Millisecond):
		// Good - deferred action did not fire
	}

	// Verify that switch.turn_off was NOT called (deferred disable was cancelled)
	calls := env.MockHA.GetServiceCallsSince(snapshot)
	testutil.AssertNoServiceCall(t, calls, "switch", "turn_off")

	// Load shedding should still be on
	assert.True(t, ls.IsLoadSheddingOn(), "Load shedding should remain enabled")
}
