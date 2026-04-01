package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/plugins/security"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupSecurityScenarioTest creates a test environment with State Tracking and Security plugins
func setupSecurityScenarioTest(t *testing.T) (*MockHAServer, *statetracking.Manager, *security.Manager, *state.Manager, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	// Create logger
	logger := testlogger.New()

	// Create and start State Tracking plugin (must start before Security)
	stateTracking := statetracking.NewManager(context.Background(), client, stateManager, logger, false, nil)
	require.NoError(t, stateTracking.Start(), "State Tracking manager should start successfully")

	// Create and start Security plugin
	securityManager := security.NewManager(context.Background(), client, stateManager, logger, false, nil)
	require.NoError(t, securityManager.Start(), "Security manager should start successfully")

	cleanup := func() {
		securityManager.Stop()
		stateTracking.Stop()
		baseCleanup()
	}

	return server, stateTracking, securityManager, stateManager, cleanup
}

// setupSecurityScenarioTestWithMockClock creates a test environment with a mock clock for time-based tests
func setupSecurityScenarioTestWithMockClock(t *testing.T) (*MockHAServer, *statetracking.Manager, *security.Manager, *state.Manager, *clock.MockClock, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	// Create logger
	logger := testlogger.New()

	// Create mock clock
	mockClock := clock.NewMockClock(time.Now())

	// Create and start State Tracking plugin with mock clock
	stateTracking := statetracking.NewManager(context.Background(), client, stateManager, logger, false, nil)
	stateTracking.SetClock(mockClock)
	require.NoError(t, stateTracking.Start(), "State Tracking manager should start successfully")

	// Create and start Security plugin with mock clock
	securityManager := security.NewManager(context.Background(), client, stateManager, logger, false, nil)
	securityManager.SetClock(mockClock)
	require.NoError(t, securityManager.Start(), "Security manager should start successfully")

	cleanup := func() {
		securityManager.Stop()
		stateTracking.Stop()
		baseCleanup()
	}

	return server, stateTracking, securityManager, stateManager, mockClock, cleanup
}

// ============================================================================
// Security Scenario Tests - Owner Return Home & Garage Automation
// ============================================================================

// TestScenario_NickReturnsHomeGarageEmpty tests that the garage door opens
// automatically when Nick arrives home and the garage is empty
func TestScenario_NickReturnsHomeGarageEmpty(t *testing.T) {
	t.Parallel()
	server, _, _, manager, cleanup := setupSecurityScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Nick is not home, garage is empty")

	// Set initial states
	server.SetState("input_boolean.nick_home", "off", nil)
	server.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil) // Empty garage
	waitForBoolState(t, manager, "isNickHome", false, "isNickHome should be false initially")

	// Take snapshot before the action that generates calls
	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Nick arrives home (isNickHome changes from false → true)")

	// Simulate Nick arriving home
	server.SetState("input_boolean.nick_home", "on", nil)

	t.Log("THEN: didOwnerJustReturnHome should be set to true")

	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true after Nick arrives")

	t.Log("AND: Garage door should be opened")

	waitForServiceCallWithEntitySince(t, server, snapshot, "cover", "open_cover", "cover.garage_door_door", "Garage door should be opened when Nick returns and garage is empty")

	garageOpenCall := FindServiceCallWithEntityID(server.GetServiceCallsSince(snapshot), "cover", "open_cover", "cover.garage_door_door")
	if garageOpenCall != nil {
		t.Logf("✓ Garage door opened: %s.%s for %v",
			garageOpenCall.Domain,
			garageOpenCall.Service,
			garageOpenCall.ServiceData["entity_id"])
	}
}

// TestScenario_CarolineReturnsHomeGarageEmpty tests that the garage door opens
// when Caroline arrives home and the garage is empty
func TestScenario_CarolineReturnsHomeGarageEmpty(t *testing.T) {
	t.Parallel()
	server, _, _, manager, cleanup := setupSecurityScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Caroline is not home, garage is empty")

	server.SetState("input_boolean.caroline_home", "off", nil)
	server.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)
	waitForBoolState(t, manager, "isCarolineHome", false, "isCarolineHome should be false initially")

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Caroline arrives home")

	server.SetState("input_boolean.caroline_home", "on", nil)

	t.Log("THEN: didOwnerJustReturnHome should be set to true and garage should open")

	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true after Caroline arrives")
	waitForServiceCallWithEntitySince(t, server, snapshot, "cover", "open_cover", "cover.garage_door_door", "Garage door should be opened when Caroline returns")
}

// TestScenario_OwnerReturnsHomeGarageOccupied tests that the garage door does NOT
// open when an owner arrives home but the garage is already occupied (vehicle detected)
func TestScenario_OwnerReturnsHomeGarageOccupied(t *testing.T) {
	t.Parallel()
	server, _, _, manager, cleanup := setupSecurityScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Nick is not home, garage is occupied (vehicle detected)")

	server.SetState("input_boolean.nick_home", "off", nil)
	server.SetState("binary_sensor.garage_door_vehicle_detected", "on", nil) // Occupied garage
	waitForBoolState(t, manager, "isNickHome", false, "isNickHome should be false initially")

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Nick arrives home")

	server.SetState("input_boolean.nick_home", "on", nil)

	t.Log("THEN: didOwnerJustReturnHome should be set to true")

	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true after Nick arrives")

	t.Log("BUT: Garage door should NOT be opened (occupied)")

	// Wait for all handlers to complete, then verify no garage open call was made
	waitForProcessing(t, manager)

	garageOpenCall := FindServiceCallWithEntityID(server.GetServiceCallsSince(snapshot), "cover", "open_cover", "cover.garage_door_door")
	assert.Nil(t, garageOpenCall, "Garage door should NOT open when garage is occupied")

	t.Log("✓ Garage door correctly NOT opened (garage occupied)")
}

// TestScenario_DidOwnerJustReturnHomeAutoReset tests that didOwnerJustReturnHome
// automatically resets to false after 10 minutes
func TestScenario_DidOwnerJustReturnHomeAutoReset(t *testing.T) {
	t.Parallel()
	server, _, _, manager, mockClock, cleanup := setupSecurityScenarioTestWithMockClock(t)
	defer cleanup()

	t.Log("GIVEN: Nick arrives home")

	server.SetState("input_boolean.nick_home", "off", nil)
	waitForBoolState(t, manager, "isNickHome", false, "isNickHome should be false initially")

	server.SetState("input_boolean.nick_home", "on", nil)
	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true after arrival")

	t.Log("THEN: didOwnerJustReturnHome should be true initially")

	didReturn, err := manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, didReturn, "didOwnerJustReturnHome should be true after arrival")

	t.Log("WHEN: 10 minutes pass (simulated via mock clock)")

	// Wait for all handler goroutines to complete, including timer registration
	// in setOwnerJustReturnedHome(). waitForBoolState above confirms the state was
	// set, but the timer registration happens after SetBool in the handler goroutine.
	waitForProcessing(t, manager)

	// Use mock clock to advance time instantly; AdvanceAndProcess fires callbacks
	// and yields to the scheduler so any woken goroutines can complete
	mockClock.AdvanceAndProcess(11 * time.Minute)

	t.Log("THEN: didOwnerJustReturnHome should auto-reset to false")

	waitForBoolState(t, manager, "didOwnerJustReturnHome", false, "didOwnerJustReturnHome should reset to false after 10 minutes")

	t.Log("✓ Auto-reset after 10 minutes works correctly")
}

// TestScenario_MultipleArrivalsWithin10Minutes tests edge case where both owners
// arrive within 10 minutes - the timer should extend
func TestScenario_MultipleArrivalsWithin10Minutes(t *testing.T) {
	t.Parallel()
	server, _, _, manager, mockClock, cleanup := setupSecurityScenarioTestWithMockClock(t)
	defer cleanup()

	t.Log("GIVEN: Nick arrives home first")

	server.SetState("input_boolean.nick_home", "off", nil)
	server.SetState("input_boolean.caroline_home", "off", nil)
	waitForBoolState(t, manager, "isNickHome", false, "initial states should propagate")

	server.SetState("input_boolean.nick_home", "on", nil)
	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true after Nick arrives")

	didReturn, err := manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, didReturn, "didOwnerJustReturnHome should be true after Nick arrives")

	t.Log("WHEN: Caroline arrives 2 minutes later (simulated)")

	// Wait for all handler goroutines to complete, including Nick's timer
	// registration in setOwnerJustReturnedHome(), before advancing the clock.
	waitForProcessing(t, manager)

	// Use mock clock to advance time instantly
	mockClock.AdvanceAndProcess(2 * time.Minute)

	server.SetState("input_boolean.caroline_home", "on", nil)
	waitForBoolState(t, manager, "isCarolineHome", true, "isCarolineHome should be true")

	t.Log("THEN: didOwnerJustReturnHome should still be true")

	didReturn, err = manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, didReturn, "didOwnerJustReturnHome should still be true")

	t.Log("AND: Timer should have been extended (10 minutes from Caroline's arrival)")

	// Wait for all handler goroutines to complete, including Caroline's timer
	// registration in setOwnerJustReturnedHome(), before advancing the clock.
	waitForProcessing(t, manager)

	// Advance 9 minutes - should still be true (timer was reset by Caroline's arrival)
	mockClock.AdvanceAndProcess(9 * time.Minute)

	didReturn, err = manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, didReturn, "didOwnerJustReturnHome should still be true after 9 more minutes")

	// Advance 2 more minutes - should now be false (10+ minutes from Caroline's arrival)
	mockClock.AdvanceAndProcess(2 * time.Minute)

	didReturn, err = manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.False(t, didReturn, "didOwnerJustReturnHome should be false after 10+ minutes from Caroline's arrival")

	t.Log("✓ Timer correctly extended by second owner arrival")
}

// TestScenario_OwnerLeavesAndReturns tests that leaving and returning
// within 10 minutes still triggers the automation
func TestScenario_OwnerLeavesAndReturns(t *testing.T) {
	t.Parallel()
	server, _, _, manager, cleanup := setupSecurityScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Nick is home, then leaves")

	server.SetState("input_boolean.nick_home", "on", nil)
	server.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)

	// Wait for the arrival event to be fully processed before sending the departure.
	// Without this, under load the "on" event's setOwnerJustReturnedHome() can execute
	// AFTER the "off" event's clearOwnerJustReturnedHome(), leaving the flag as true.
	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true after Nick arrives")

	server.SetState("input_boolean.nick_home", "off", nil)

	// Use polling helper instead of time.Sleep to wait for the departure to be processed
	waitForBoolState(t, manager, "didOwnerJustReturnHome", false, "didOwnerJustReturnHome should be false when owner leaves")

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Nick returns 5 minutes later")

	server.SetState("input_boolean.nick_home", "on", nil)

	t.Log("THEN: didOwnerJustReturnHome should be set to true again")

	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true on return")

	t.Log("AND: Garage should open again")

	waitForServiceCallWithEntitySince(t, server, snapshot, "cover", "open_cover", "cover.garage_door_door", "Garage should open on second arrival")
}

// TestScenario_OnlyOwnersTriggersGarage tests that only owners (Nick/Caroline)
// trigger the garage automation, not guests (Assistant)
func TestScenario_OnlyOwnersTriggersGarage(t *testing.T) {
	t.Parallel()
	server, _, _, manager, cleanup := setupSecurityScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Assistant is not here, garage is empty")

	server.SetState("input_boolean.assistant_here", "off", nil)
	server.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)
	waitForProcessing(t, manager)

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Assistant arrives (isAssistantHere changes to true)")

	server.SetState("input_boolean.assistant_here", "on", nil)
	waitForBoolState(t, manager, "isAssistantHere", true, "isAssistantHere should become true")

	t.Log("THEN: didOwnerJustReturnHome should remain false (Assistant is not an owner)")

	didReturn, err := manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.False(t, didReturn, "didOwnerJustReturnHome should be false for non-owner arrival")

	t.Log("AND: Garage door should NOT be opened")

	waitForProcessing(t, manager)

	garageOpenCall := FindServiceCallWithEntityID(server.GetServiceCallsSince(snapshot), "cover", "open_cover", "cover.garage_door_door")
	assert.Nil(t, garageOpenCall, "Garage should not open for guest arrival")

	t.Log("✓ Garage automation only triggers for owners, not guests")
}

// TestScenario_NearHomeDepartureDoesNotOpenGarage tests that passing through the
// near_home geofence while LEAVING does not falsely trigger didOwnerJustReturnHome.
// Regression test for issue #918: on departure, the home zone (smaller) clears first,
// then the near_home zone (larger) fires. At that point isHome is already false,
// making it look like an arrival.
func TestScenario_NearHomeDepartureDoesNotOpenGarage(t *testing.T) {
	t.Parallel()
	server, _, _, manager, mockClock, cleanup := setupSecurityScenarioTestWithMockClock(t)
	defer cleanup()

	t.Log("GIVEN: Nick is home")
	server.SetState("input_boolean.nick_home", "on", nil)
	server.SetState("input_boolean.nick_near_home", "off", nil)
	server.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)
	waitForBoolState(t, manager, "isNickHome", true, "isNickHome should be true initially")

	// Clear the didOwnerJustReturnHome that was set by the arrival above
	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome set by initial arrival")
	waitForProcessing(t, manager)
	mockClock.AdvanceAndProcess(11 * time.Minute)
	waitForBoolState(t, manager, "didOwnerJustReturnHome", false, "didOwnerJustReturnHome should auto-reset")

	departureSnapshot := server.ServiceCallCount()

	t.Log("WHEN: Nick leaves home (home zone clears first)")
	server.SetState("input_boolean.nick_home", "off", nil)
	waitForBoolState(t, manager, "isNickHome", false, "isNickHome should be false after departure")
	waitForProcessing(t, manager)
	waitForServiceCallQuiescenceSince(t, server, departureSnapshot, 200*time.Millisecond)

	snapshot := server.ServiceCallCount()

	t.Log("AND: Nick passes through near_home geofence on the way out (1 minute later)")
	mockClock.AdvanceAndProcess(1 * time.Minute)
	server.SetState("input_boolean.nick_near_home", "on", nil)

	t.Log("THEN: didOwnerJustReturnHome should NOT be set (departure, not arrival)")
	waitForProcessing(t, manager)
	waitForServiceCallQuiescenceSince(t, server, snapshot, 200*time.Millisecond)

	didReturn, err := manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.False(t, didReturn, "didOwnerJustReturnHome should remain false during departure through near_home")

	t.Log("AND: Garage door should NOT be opened")
	garageOpenCall := FindServiceCallWithEntityID(server.GetServiceCallsSince(snapshot), "cover", "open_cover", "cover.garage_door_door")
	assert.Nil(t, garageOpenCall, "Garage should NOT open when passing through near_home on departure")

	t.Log("✓ Near-home geofence correctly suppressed during departure")
}

// TestScenario_NearHomeArrivalStillWorks tests that genuine arrivals through the
// near_home geofence still trigger didOwnerJustReturnHome correctly after the
// departure cooldown fix.
func TestScenario_NearHomeArrivalStillWorks(t *testing.T) {
	t.Parallel()
	server, _, _, manager, mockClock, cleanup := setupSecurityScenarioTestWithMockClock(t)
	defer cleanup()

	t.Log("GIVEN: Nick is not home (has been away for a while)")
	server.SetState("input_boolean.nick_home", "off", nil)
	server.SetState("input_boolean.nick_near_home", "off", nil)
	server.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)
	waitForBoolState(t, manager, "isNickHome", false, "isNickHome should be false")
	waitForProcessing(t, manager)

	// Advance well past any cooldown period
	mockClock.AdvanceAndProcess(10 * time.Minute)

	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Nick enters near_home geofence (genuine arrival)")
	server.SetState("input_boolean.nick_near_home", "on", nil)

	t.Log("THEN: didOwnerJustReturnHome should be set to true")
	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true for genuine arrival via near_home")

	t.Log("AND: Garage door should be opened")
	waitForServiceCallWithEntitySince(t, server, snapshot, "cover", "open_cover", "cover.garage_door_door", "Garage should open for genuine near_home arrival")

	t.Log("✓ Genuine near-home arrival still triggers garage correctly")
}
