package security

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// TestSecurityManager_LockdownOnEveryoneAsleep tests lockdown activation when everyone is asleep
func TestSecurityManager_LockdownOnEveryoneAsleep(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.everyone_asleep", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager (not read-only so it can call services)
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)

	// Use MockClock so the 30-second delay is instant (MockClock.Sleep is a no-op)
	mockClock := clock.NewMockClock(time.Now())
	securityManager.SetClock(mockClock)

	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Set everyone asleep
	if err := stateManager.SetBool("isEveryoneAsleep", true); err != nil {
		t.Fatalf("Failed to set isEveryoneAsleep: %v", err)
	}

	// Wait for async processing (goroutine runs off-wait-on sequence instantly with MockClock)
	time.Sleep(100 * time.Millisecond)

	// Verify lockdown activation sequence: turn_off first, then turn_on
	calls := mockHA.GetServiceCalls()
	turnOffIdx := -1
	turnOnIdx := -1

	for i, call := range calls {
		if call.Domain == "input_boolean" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "input_boolean.lockdown" {
				if call.Service == "turn_off" && turnOffIdx == -1 {
					turnOffIdx = i
				} else if call.Service == "turn_on" && turnOnIdx == -1 {
					turnOnIdx = i
				}
			}
		}
	}

	if turnOffIdx == -1 {
		t.Errorf("Expected lockdown to be turned off first, but turn_off was not called")
	}
	if turnOnIdx == -1 {
		t.Errorf("Expected lockdown to be activated, but turn_on was not called")
	}
	if turnOffIdx != -1 && turnOnIdx != -1 && turnOffIdx >= turnOnIdx {
		t.Errorf("Expected turn_off (idx %d) to happen before turn_on (idx %d)", turnOffIdx, turnOnIdx)
	}
}

// TestSecurityManager_LockdownOnNoOneHome tests lockdown activation when no one is home
func TestSecurityManager_LockdownOnNoOneHome(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.anyone_home", "on", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)

	// Use MockClock so the 30-second delay is instant (MockClock.Sleep is a no-op)
	mockClock := clock.NewMockClock(time.Now())
	securityManager.SetClock(mockClock)

	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Set no one home
	if err := stateManager.SetBool("isAnyoneHome", false); err != nil {
		t.Fatalf("Failed to set isAnyoneHome: %v", err)
	}

	// Wait for async processing (goroutine runs off-wait-on sequence instantly with MockClock)
	time.Sleep(100 * time.Millisecond)

	// Verify lockdown activation sequence: turn_off first, then turn_on
	calls := mockHA.GetServiceCalls()
	turnOffIdx := -1
	turnOnIdx := -1

	for i, call := range calls {
		if call.Domain == "input_boolean" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "input_boolean.lockdown" {
				if call.Service == "turn_off" && turnOffIdx == -1 {
					turnOffIdx = i
				} else if call.Service == "turn_on" && turnOnIdx == -1 {
					turnOnIdx = i
				}
			}
		}
	}

	if turnOffIdx == -1 {
		t.Errorf("Expected lockdown to be turned off first, but turn_off was not called")
	}
	if turnOnIdx == -1 {
		t.Errorf("Expected lockdown to be activated, but turn_on was not called")
	}
	if turnOffIdx != -1 && turnOnIdx != -1 && turnOffIdx >= turnOnIdx {
		t.Errorf("Expected turn_off (idx %d) to happen before turn_on (idx %d)", turnOffIdx, turnOnIdx)
	}
}

// TestSecurityManager_LockdownAutoReset tests auto-reset of lockdown after 5 seconds
func TestSecurityManager_LockdownAutoReset(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)

	// Use MockClock so the 5-second delay is instant (MockClock.Sleep is a no-op)
	mockClock := clock.NewMockClock(time.Now())
	securityManager.SetClock(mockClock)

	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate lockdown being turned on in HA
	mockHA.SimulateStateChange("input_boolean.lockdown", "on")

	// Wait for async processing (goroutine runs instantly with MockClock.Sleep as no-op)
	time.Sleep(100 * time.Millisecond)

	// Verify lockdown was reset
	calls := mockHA.GetServiceCalls()
	found := false
	for _, call := range calls {
		if call.Domain == "input_boolean" && call.Service == "turn_off" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "input_boolean.lockdown" {
				found = true
				break
			}
		}
	}

	if !found {
		t.Errorf("Expected lockdown to be reset after 5 seconds, but service was not called")
	}
}

// TestSecurityManager_GarageAutoOpen tests garage auto-open when owner returns
func TestSecurityManager_GarageAutoOpen(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()

	// Set garage as empty (no vehicle detected)
	mockHA.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Trigger owner return
	if err := stateManager.SetBool("didOwnerJustReturnHome", true); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify garage door was opened
	calls := mockHA.GetServiceCalls()
	found := false
	for _, call := range calls {
		if call.Domain == "cover" && call.Service == "open_cover" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "cover.garage_door_door" {
				found = true
				break
			}
		}
	}

	if !found {
		t.Errorf("Expected garage door to be opened, but service was not called")
	}
}

// TestSecurityManager_GarageNotOpenedWhenOccupied tests garage does not open when occupied
func TestSecurityManager_GarageNotOpenedWhenOccupied(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()

	// Set garage as occupied (vehicle detected)
	mockHA.SetState("binary_sensor.garage_door_vehicle_detected", "on", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Trigger owner return
	if err := stateManager.SetBool("didOwnerJustReturnHome", true); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify garage door was NOT opened
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "cover" && call.Service == "open_cover" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "cover.garage_door_door" {
				t.Errorf("Expected garage door to NOT be opened when occupied, but service was called")
			}
		}
	}
}

// TestSecurityManager_DoorbellNotification tests doorbell notification
func TestSecurityManager_DoorbellNotification(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)

	// Use MockClock so the 2-second delay between light flashes is instant
	mockClock := clock.NewMockClock(time.Now())
	securityManager.SetClock(mockClock)

	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate doorbell press
	mockHA.SimulateStateChange("input_button.doorbell", "2024-01-01T12:00:01")

	// Wait for async processing (goroutine runs instantly with MockClock.Sleep as no-op)
	time.Sleep(100 * time.Millisecond)

	// Verify TTS was sent
	calls := mockHA.GetServiceCalls()
	ttsFound := false
	lightsFlashed := 0

	for _, call := range calls {
		if call.Domain == "tts" && call.Service == "speak" {
			if msg, ok := call.Data["message"].(string); ok && msg == "Doorbell ringing" {
				ttsFound = true
			}
		}
		if call.Domain == "light" && call.Service == "turn_on" {
			if flash, ok := call.Data["flash"].(string); ok && flash == "short" {
				lightsFlashed++
			}
		}
	}

	if !ttsFound {
		t.Errorf("Expected TTS notification for doorbell, but service was not called")
	}

	if lightsFlashed != 2 {
		t.Errorf("Expected lights to flash 2 times, but flashed %d times", lightsFlashed)
	}
}

// TestSecurityManager_DoorbellRateLimiting tests doorbell rate limiting
func TestSecurityManager_DoorbellRateLimiting(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate first doorbell press
	mockHA.SimulateStateChange("input_button.doorbell", "2024-01-01T12:00:01")

	time.Sleep(100 * time.Millisecond)
	firstCallCount := len(mockHA.GetServiceCalls())

	// Simulate second doorbell press (within 20 seconds - should be rate limited)
	mockHA.SimulateStateChange("input_button.doorbell", "2024-01-01T12:00:02")

	time.Sleep(100 * time.Millisecond)
	secondCallCount := len(mockHA.GetServiceCalls())

	// Verify second press did not trigger new notifications
	if secondCallCount > firstCallCount {
		t.Errorf("Expected doorbell to be rate limited, but new service calls were made")
	}
}

// TestSecurityManager_VehicleArrivalWithExpecting tests vehicle arrival when expecting someone
func TestSecurityManager_VehicleArrivalWithExpecting(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.expecting_someone", "on", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate vehicle arriving
	mockHA.SimulateStateChange("input_button.vehicle_arriving", "2024-01-01T12:00:01")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify TTS was sent
	calls := mockHA.GetServiceCalls()
	ttsFound := false
	expectingReset := false

	for _, call := range calls {
		if call.Domain == "tts" && call.Service == "speak" {
			if msg, ok := call.Data["message"].(string); ok && msg == "They have arrived" {
				ttsFound = true
			}
		}
		if call.Domain == "input_boolean" && call.Service == "turn_off" {
			if entityID, ok := call.Data["entity_id"].(string); ok && entityID == "input_boolean.expecting_someone" {
				expectingReset = true
			}
		}
	}

	if !ttsFound {
		t.Errorf("Expected TTS notification for vehicle arrival, but service was not called")
	}

	if !expectingReset {
		t.Errorf("Expected isExpectingSomeone to be reset, but service was not called")
	}
}

// TestSecurityManager_VehicleArrivalWithoutExpecting tests vehicle arrival when not expecting
func TestSecurityManager_VehicleArrivalWithoutExpecting(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.expecting_someone", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate vehicle arriving
	mockHA.SimulateStateChange("input_button.vehicle_arriving", "2024-01-01T12:00:01")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify TTS was NOT sent
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "tts" && call.Service == "speak" {
			if msg, ok := call.Data["message"].(string); ok && msg == "They have arrived" {
				t.Errorf("Expected NO TTS notification when not expecting someone, but service was called")
			}
		}
	}
}

// TestSecurityManager_ReadOnlyMode tests that read-only mode prevents service calls
func TestSecurityManager_ReadOnlyMode(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.everyone_asleep", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false) // Not read-only for state manager

	stateManager.SyncFromHA()

	// Create security manager in read-only mode (this is what we're testing)
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, true, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	// Clear any initial service calls
	mockHA.ClearServiceCalls()

	// Simulate HA state change that would trigger lockdown
	mockHA.SimulateStateChange("input_boolean.everyone_asleep", "on")

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify NO service calls were made (read-only mode)
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "input_boolean" && call.Service == "turn_on" {
			t.Errorf("Expected NO service calls in read-only mode, but got: %s.%s", call.Domain, call.Service)
		}
	}
}

// TestSecurityManager_InvalidTypeHandling tests handling of invalid state value types
func TestSecurityManager_InvalidTypeHandling(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	mockHA.ClearServiceCalls()

	// Test handleEveryoneAsleepChange with invalid type (should log error and return)
	securityManager.handleEveryoneAsleepChange("isEveryoneAsleep", false, "invalid_string")
	time.Sleep(50 * time.Millisecond)

	// Should not have called any services due to type error
	calls := mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls with invalid type, got %d calls", len(calls))
	}

	// Test handleAnyoneHomeChange with invalid type
	securityManager.handleAnyoneHomeChange("isAnyoneHome", true, 123) // invalid int instead of bool
	time.Sleep(50 * time.Millisecond)

	calls = mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls with invalid type, got %d calls", len(calls))
	}

	// Test handleOwnerReturnHome with invalid type
	securityManager.handleOwnerReturnHome("didOwnerJustReturnHome", false, map[string]string{"invalid": "map"})
	time.Sleep(50 * time.Millisecond)

	calls = mockHA.GetServiceCalls()
	if len(calls) > 0 {
		t.Errorf("Expected no service calls with invalid type, got %d calls", len(calls))
	}
}

// TestSecurityManager_OwnerReturnHome_DidNotReturn tests that nothing happens when owner did not return
func TestSecurityManager_OwnerReturnHome_DidNotReturn(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	mockHA.ClearServiceCalls()

	// Set didOwnerJustReturnHome to false (did NOT return)
	if err := stateManager.SetBool("didOwnerJustReturnHome", false); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify garage door was NOT opened
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "cover" && call.Service == "open_cover" {
			t.Errorf("Garage door should not open when owner did not return")
		}
	}
}

// TestSecurityManager_VehicleArrivalRateLimiting tests vehicle arrival rate limiting
func TestSecurityManager_VehicleArrivalRateLimiting(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.expecting_someone", "on", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, false, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	mockHA.ClearServiceCalls()

	// Simulate first vehicle arriving
	mockHA.SimulateStateChange("input_button.vehicle_arriving", "2024-01-01T12:00:01")

	time.Sleep(100 * time.Millisecond)
	firstCallCount := len(mockHA.GetServiceCalls())

	// Reset expecting someone for second test
	mockHA.SetState("input_boolean.expecting_someone", "on", nil)

	// Simulate second vehicle arriving (within 20 seconds - should be rate limited)
	mockHA.SimulateStateChange("input_button.vehicle_arriving", "2024-01-01T12:00:02")

	time.Sleep(100 * time.Millisecond)
	secondCallCount := len(mockHA.GetServiceCalls())

	// Verify second arrival was rate limited (should have same or fewer calls, not more)
	// Note: We already reset expecting_someone in first call, so second won't trigger
	if secondCallCount > firstCallCount+1 {
		t.Errorf("Expected vehicle arrival to be rate limited")
	}
}

// TestSecurityManager_ReadOnlyModeGarage tests garage operations in read-only mode
func TestSecurityManager_ReadOnlyModeGarage(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("binary_sensor.garage_door_vehicle_detected", "off", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager in READ-ONLY mode
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, true, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	mockHA.ClearServiceCalls()

	// Trigger owner return
	if err := stateManager.SetBool("didOwnerJustReturnHome", true); err != nil {
		t.Fatalf("Failed to set didOwnerJustReturnHome: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify NO garage door service calls in read-only mode
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "cover" {
			t.Errorf("Expected NO cover service calls in read-only mode, but got: %s.%s", call.Domain, call.Service)
		}
	}
}

// TestSecurityManager_ReadOnlyModeLockdownReset tests lockdown reset in read-only mode
func TestSecurityManager_ReadOnlyModeLockdownReset(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager in READ-ONLY mode
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, true, nil)

	// Use MockClock so the 5-second delay is instant (MockClock.Sleep is a no-op)
	mockClock := clock.NewMockClock(time.Now())
	securityManager.SetClock(mockClock)

	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	mockHA.ClearServiceCalls()

	// Simulate lockdown being turned on in HA
	mockHA.SimulateStateChange("input_boolean.lockdown", "on")

	// Wait for async processing (goroutine runs instantly with MockClock.Sleep as no-op)
	time.Sleep(100 * time.Millisecond)

	// Verify NO lockdown reset in read-only mode
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "input_boolean" && call.Service == "turn_off" {
			t.Errorf("Expected NO service calls in read-only mode for lockdown reset")
		}
	}
}

// TestSecurityManager_ReadOnlyModeVehicleArrival tests vehicle arrival in read-only mode
func TestSecurityManager_ReadOnlyModeVehicleArrival(t *testing.T) {
	t.Parallel(
	// Setup
	)

	mockHA := ha.NewMockClient()
	mockHA.SetState("input_boolean.expecting_someone", "on", nil)
	mockHA.Connect()

	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)
	stateManager.SyncFromHA()

	// Create security manager in READ-ONLY mode
	securityManager := NewManager(context.Background(), mockHA, stateManager, logger, true, nil)
	if err := securityManager.Start(); err != nil {
		t.Fatalf("Failed to start security manager: %v", err)
	}

	mockHA.ClearServiceCalls()

	// Simulate vehicle arriving
	mockHA.SimulateStateChange("input_button.vehicle_arriving", "2024-01-01T12:00:01")

	time.Sleep(100 * time.Millisecond)

	// Verify NO service calls in read-only mode
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "input_boolean" && call.Service == "turn_off" {
			t.Errorf("Expected NO reset of expecting_someone in read-only mode")
		}
	}
}
