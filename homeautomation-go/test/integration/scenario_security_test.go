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

	// Clear any initialization calls
	server.ClearServiceCalls()

	t.Log("WHEN: Nick arrives home (isNickHome changes from false → true)")

	// Simulate Nick arriving home
	server.SetState("input_boolean.nick_home", "on", nil)

	t.Log("THEN: didOwnerJustReturnHome should be set to true")

	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true after Nick arrives")

	t.Log("AND: Garage door should be opened")

	waitForServiceCallWithEntity(t, server, "cover", "open_cover", "cover.garage_door_door", "Garage door should be opened when Nick returns and garage is empty")

	garageOpenCall := server.FindServiceCall("cover", "open_cover", "cover.garage_door_door")
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

	server.ClearServiceCalls()

	t.Log("WHEN: Caroline arrives home")

	server.SetState("input_boolean.caroline_home", "on", nil)

	t.Log("THEN: didOwnerJustReturnHome should be set to true and garage should open")

	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true after Caroline arrives")
	waitForServiceCallWithEntity(t, server, "cover", "open_cover", "cover.garage_door_door", "Garage door should be opened when Caroline returns")
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

	server.ClearServiceCalls()

	t.Log("WHEN: Nick arrives home")

	server.SetState("input_boolean.nick_home", "on", nil)

	t.Log("THEN: didOwnerJustReturnHome should be set to true")

	waitForBoolState(t, manager, "didOwnerJustReturnHome", true, "didOwnerJustReturnHome should be true after Nick arrives")

	t.Log("BUT: Garage door should NOT be opened (occupied)")

	// Wait a bit for any potential service calls, then verify none were made
	time.Sleep(100 * time.Millisecond)

	garageOpenCall := server.FindServiceCall("cover", "open_cover", "cover.garage_door_door")
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
	time.Sleep(100 * time.Millisecond) // Real sleep for event processing

	server.SetState("input_boolean.nick_home", "on", nil)
	time.Sleep(100 * time.Millisecond) // Real sleep for event processing

	t.Log("THEN: didOwnerJustReturnHome should be true initially")

	didReturn, err := manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, didReturn, "didOwnerJustReturnHome should be true after arrival")

	t.Log("WHEN: 10 minutes pass (simulated via mock clock)")

	// Use mock clock to advance time instantly instead of real sleep
	mockClock.Advance(11 * time.Minute)
	time.Sleep(100 * time.Millisecond) // Allow timer callback to execute

	t.Log("THEN: didOwnerJustReturnHome should auto-reset to false")

	didReturn, err = manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.False(t, didReturn, "didOwnerJustReturnHome should reset to false after 10 minutes")

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
	time.Sleep(100 * time.Millisecond) // Real sleep for event processing

	server.SetState("input_boolean.nick_home", "on", nil)
	time.Sleep(100 * time.Millisecond) // Real sleep for event processing

	didReturn, err := manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, didReturn, "didOwnerJustReturnHome should be true after Nick arrives")

	t.Log("WHEN: Caroline arrives 2 minutes later (simulated)")

	// Use mock clock to advance time instantly
	mockClock.Advance(2 * time.Minute)
	time.Sleep(100 * time.Millisecond) // Allow any timer callbacks

	server.SetState("input_boolean.caroline_home", "on", nil)
	time.Sleep(100 * time.Millisecond) // Real sleep for event processing

	t.Log("THEN: didOwnerJustReturnHome should still be true")

	didReturn, err = manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, didReturn, "didOwnerJustReturnHome should still be true")

	t.Log("AND: Timer should have been extended (10 minutes from Caroline's arrival)")

	// Advance 9 minutes - should still be true (timer was reset by Caroline's arrival)
	mockClock.Advance(9 * time.Minute)
	time.Sleep(100 * time.Millisecond)

	didReturn, err = manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, didReturn, "didOwnerJustReturnHome should still be true after 9 more minutes")

	// Advance 2 more minutes - should now be false (10+ minutes from Caroline's arrival)
	mockClock.Advance(2 * time.Minute)
	time.Sleep(100 * time.Millisecond)

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
	time.Sleep(100 * time.Millisecond)

	server.SetState("input_boolean.nick_home", "off", nil)
	time.Sleep(100 * time.Millisecond)

	didReturn, err := manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.False(t, didReturn, "didOwnerJustReturnHome should be false when owner leaves")

	server.ClearServiceCalls()

	t.Log("WHEN: Nick returns 5 minutes later")

	time.Sleep(100 * time.Millisecond) // Simulate some time passing

	server.SetState("input_boolean.nick_home", "on", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: didOwnerJustReturnHome should be set to true again")

	didReturn, err = manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.True(t, didReturn, "didOwnerJustReturnHome should be true on return")

	t.Log("AND: Garage should open again")

	time.Sleep(50 * time.Millisecond)

	garageOpenCall := server.FindServiceCall("cover", "open_cover", "cover.garage_door_door")
	assert.NotNil(t, garageOpenCall, "Garage should open on second arrival")
}

// TestScenario_OnlyOwnersTriggersGarage tests that only owners (Nick/Caroline)
// trigger the garage automation, not guests (Tori)
func TestScenario_OnlyOwnersTriggersGarage(t *testing.T) {
	t.Parallel()
	server, _, _, manager, cleanup := setupSecurityScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Tori is not here, garage is empty")

	server.SetState("input_boolean.tori_here", "off", nil)
	server.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)
	time.Sleep(100 * time.Millisecond)

	server.ClearServiceCalls()

	t.Log("WHEN: Tori arrives (isToriHere changes to true)")

	server.SetState("input_boolean.tori_here", "on", nil)
	time.Sleep(100 * time.Millisecond)

	t.Log("THEN: didOwnerJustReturnHome should remain false (Tori is not an owner)")

	didReturn, err := manager.GetBool("didOwnerJustReturnHome")
	require.NoError(t, err)
	assert.False(t, didReturn, "didOwnerJustReturnHome should be false for non-owner arrival")

	t.Log("AND: Garage door should NOT be opened")

	time.Sleep(50 * time.Millisecond)

	garageOpenCall := server.FindServiceCall("cover", "open_cover", "cover.garage_door_door")
	assert.Nil(t, garageOpenCall, "Garage should not open for guest arrival")

	t.Log("✓ Garage automation only triggers for owners, not guests")
}
