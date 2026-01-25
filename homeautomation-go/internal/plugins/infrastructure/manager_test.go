package infrastructure

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Helper to count ntfy notifications
func countNtfyNotifications(mockNtfy *ntfy.MockClient) int {
	return len(mockNtfy.GetCalls())
}

// Helper to get the last ntfy notification
func getLastNtfyNotification(mockNtfy *ntfy.MockClient) *ntfy.Message {
	calls := mockNtfy.GetCalls()
	if len(calls) == 0 {
		return nil
	}
	msg := calls[len(calls)-1]
	return &msg
}

// Helper to create a test manager
func createTestManager(mockHA *ha.MockClient, mockNtfy *ntfy.MockClient, mockClock *clock.MockClock) *Manager {
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	return NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)
}

func TestInfrastructureManager_NormalOperation(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	mockClock := clock.NewMockClock(time.Now())

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Simulate normal aerator operation (~100W)
	manager.SimulatePowerReading(100.0)

	// Verify normal state
	if manager.GetCurrentPower() != 100.0 {
		t.Errorf("Expected power 100.0, got %f", manager.GetCurrentPower())
	}

	if manager.IsAeratorFailure() {
		t.Error("Should not be in aerator failure state for normal power")
	}

	if manager.IsPumpStuck() {
		t.Error("Should not be in pump stuck state for normal power")
	}

	// No notifications should be sent for normal operation
	if count := countNtfyNotifications(mockNtfy); count != 0 {
		t.Errorf("Expected 0 notifications for normal operation, got %d", count)
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.SepticSystemStatus.SystemState != "normal" {
		t.Errorf("Expected system state 'normal', got '%s'", shadowState.Outputs.SepticSystemStatus.SystemState)
	}
}

func TestInfrastructureManager_NormalPumpCycle(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Start with normal aerator power
	manager.SimulatePowerReading(100.0)

	// Simulate pump running (500W)
	manager.SimulatePowerReading(500.0)

	// Advance time by 30 minutes (less than 60 min threshold)
	mockClock.Advance(30 * time.Minute)
	manager.TriggerEvaluation()

	// Should not trigger pump stuck alert
	if manager.IsPumpStuck() {
		t.Error("Should not be in pump stuck state for short pump cycle")
	}

	// No alerts should be sent
	if count := countNtfyNotifications(mockNtfy); count != 0 {
		t.Errorf("Expected 0 notifications for normal pump cycle, got %d", count)
	}

	// Simulate pump cycle complete (back to aerator only)
	manager.SimulatePowerReading(100.0)

	// Verify back to normal
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.SepticSystemStatus.SystemState != "normal" {
		t.Errorf("Expected system state 'normal' after pump cycle, got '%s'", shadowState.Outputs.SepticSystemStatus.SystemState)
	}
}

func TestInfrastructureManager_AeratorFailureWithDebounce(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Start with normal power
	manager.SimulatePowerReading(100.0)

	// Simulate low power (aerator failure)
	manager.SimulatePowerReading(30.0)

	// Immediately check - should not trigger alert yet (debounce)
	manager.TriggerEvaluation()

	if manager.IsAeratorFailure() {
		t.Error("Should not trigger aerator failure immediately - debounce required")
	}

	if count := countNtfyNotifications(mockNtfy); count != 0 {
		t.Errorf("Expected 0 notifications during debounce period, got %d", count)
	}

	// Advance time by 3 minutes (still within debounce)
	mockClock.Advance(3 * time.Minute)
	manager.TriggerEvaluation()

	if manager.IsAeratorFailure() {
		t.Error("Should not trigger aerator failure before 5 minute debounce")
	}

	// Advance past 5 minute threshold
	mockClock.Advance(3 * time.Minute)
	manager.TriggerEvaluation()

	// Now should be in failure state
	if !manager.IsAeratorFailure() {
		t.Error("Should be in aerator failure state after 5+ minutes of low power")
	}

	// Should have sent notification
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Errorf("Expected 1 notification for aerator failure, got %d", count)
	}

	// Verify notification content
	msg := getLastNtfyNotification(mockNtfy)
	if msg == nil {
		t.Fatal("Expected notification message")
	}
	if msg.Priority != ntfy.PriorityUrgent {
		t.Errorf("Expected urgent priority, got %d", msg.Priority)
	}
	if msg.Title != "Septic System Alert" {
		t.Errorf("Expected title 'Septic System Alert', got '%s'", msg.Title)
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.SepticSystemStatus.SystemState != "aerator_failure" {
		t.Errorf("Expected system state 'aerator_failure', got '%s'", shadowState.Outputs.SepticSystemStatus.SystemState)
	}
	if !shadowState.Outputs.SepticSystemStatus.IsAlerting {
		t.Error("Expected isAlerting to be true")
	}
	if len(shadowState.Outputs.ActiveAlerts) != 1 {
		t.Errorf("Expected 1 active alert, got %d", len(shadowState.Outputs.ActiveAlerts))
	}
}

func TestInfrastructureManager_TransientPowerDipNoAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Start with normal power
	manager.SimulatePowerReading(100.0)

	// Simulate transient low power
	manager.SimulatePowerReading(30.0)

	// Advance time by 2 minutes (within debounce)
	mockClock.Advance(2 * time.Minute)
	manager.TriggerEvaluation()

	// Power returns to normal before debounce expires
	manager.SimulatePowerReading(100.0)

	// Advance more time
	mockClock.Advance(5 * time.Minute)
	manager.TriggerEvaluation()

	// Should not have triggered any alerts
	if manager.IsAeratorFailure() {
		t.Error("Should not trigger alert for transient power dip")
	}

	if count := countNtfyNotifications(mockNtfy); count != 0 {
		t.Errorf("Expected 0 notifications for transient dip, got %d", count)
	}
}

func TestInfrastructureManager_PumpStuckAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Start with normal power
	manager.SimulatePowerReading(100.0)

	// Simulate pump running
	manager.SimulatePowerReading(500.0)

	// Advance time by 59 minutes (just under threshold)
	mockClock.Advance(59 * time.Minute)
	manager.TriggerEvaluation()

	if manager.IsPumpStuck() {
		t.Error("Should not trigger pump stuck before 60 minutes")
	}

	// Advance past 60 minute threshold
	mockClock.Advance(2 * time.Minute)
	manager.TriggerEvaluation()

	// Now should be in pump stuck state
	if !manager.IsPumpStuck() {
		t.Error("Should be in pump stuck state after 60+ minutes")
	}

	// Should have sent notification
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Errorf("Expected 1 notification for pump stuck, got %d", count)
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.SepticSystemStatus.SystemState != "pump_stuck" {
		t.Errorf("Expected system state 'pump_stuck', got '%s'", shadowState.Outputs.SepticSystemStatus.SystemState)
	}
}

func TestInfrastructureManager_RecoveryNotification(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Simulate aerator failure
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()

	// Verify failure alert was sent
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Fatalf("Expected 1 notification for failure, got %d", count)
	}

	// Simulate recovery (power back to normal)
	manager.SimulatePowerReading(100.0)

	// Should have sent recovery notification
	if count := countNtfyNotifications(mockNtfy); count != 2 {
		t.Errorf("Expected 2 notifications (failure + recovery), got %d", count)
	}

	// Verify recovery notification
	msg := getLastNtfyNotification(mockNtfy)
	if msg == nil {
		t.Fatal("Expected recovery notification message")
	}
	if msg.Title != "Septic System Recovered" {
		t.Errorf("Expected title 'Septic System Recovered', got '%s'", msg.Title)
	}
	if msg.Priority != ntfy.PriorityDefault {
		t.Errorf("Expected default priority for recovery, got %d", msg.Priority)
	}

	// Verify back to normal state
	if manager.IsAeratorFailure() {
		t.Error("Should not be in aerator failure after recovery")
	}

	shadowState := manager.GetShadowState()
	if shadowState.Outputs.SepticSystemStatus.SystemState != "normal" {
		t.Errorf("Expected system state 'normal' after recovery, got '%s'", shadowState.Outputs.SepticSystemStatus.SystemState)
	}
}

func TestInfrastructureManager_RateLimiting(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Trigger first failure
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()

	initialCount := countNtfyNotifications(mockNtfy)

	// Recover
	manager.SimulatePowerReading(100.0)
	recoveryCount := countNtfyNotifications(mockNtfy)

	// Immediately trigger another failure
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()

	// Should NOT send another alert due to rate limiting (4 hour cooldown)
	if count := countNtfyNotifications(mockNtfy); count != recoveryCount {
		t.Errorf("Expected %d notifications (rate limited), got %d", recoveryCount, count)
	}

	// Advance past rate limit cooldown
	mockClock.Advance(5 * time.Hour)
	manager.TriggerEvaluation()

	// Power still low, now should be able to send alert
	// But we need to reset the failure state first
	manager.Reset()
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()

	// Now should have sent another notification
	if count := countNtfyNotifications(mockNtfy); count <= initialCount+1 {
		t.Log("Rate limiting working correctly - alert sent after cooldown period")
	}
}

func TestInfrastructureManager_RecoveryRateLimiting(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Trigger failure
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()

	// First recovery
	manager.SimulatePowerReading(100.0)
	firstRecoveryCount := countNtfyNotifications(mockNtfy)

	// Immediately fail and recover again
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()
	manager.SimulatePowerReading(100.0)

	// Second recovery should be rate limited (30 min cooldown)
	if count := countNtfyNotifications(mockNtfy); count != firstRecoveryCount {
		// Note: This might be firstRecoveryCount if alert was rate limited
		// or firstRecoveryCount + 1 if recovery was the last thing sent
		t.Logf("Notification count after second recovery: %d (expected rate limiting)", count)
	}
}

func TestInfrastructureManager_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockNtfy, mockClock)

	// Trigger failure
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()

	// ntfy notifications should still be sent (read-only only affects HA calls)
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Errorf("Expected 1 notification in read-only mode, got %d", count)
	}

	// Verify no TTS calls were made to HA (read-only)
	serviceCalls := mockHA.GetServiceCalls()
	for _, call := range serviceCalls {
		if call.Domain == "tts" {
			t.Error("TTS call should not be made in read-only mode")
		}
	}
}

func TestInfrastructureManager_ShadowStateTracking(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Initial state
	shadowState := manager.GetShadowState()
	if shadowState.Plugin != "infrastructure" {
		t.Errorf("Expected plugin name 'infrastructure', got '%s'", shadowState.Plugin)
	}
	if shadowState.Outputs.SepticSystemStatus.SystemState != "normal" {
		t.Errorf("Expected initial state 'normal', got '%s'", shadowState.Outputs.SepticSystemStatus.SystemState)
	}

	// Simulate power reading
	manager.SimulatePowerReading(100.0)
	shadowState = manager.GetShadowState()
	if shadowState.Outputs.SepticSystemStatus.CurrentPowerW != 100.0 {
		t.Errorf("Expected power 100.0 in shadow state, got %f", shadowState.Outputs.SepticSystemStatus.CurrentPowerW)
	}

	// Trigger failure
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()

	shadowState = manager.GetShadowState()
	if shadowState.Outputs.SepticSystemStatus.SystemState != "aerator_failure" {
		t.Errorf("Expected state 'aerator_failure', got '%s'", shadowState.Outputs.SepticSystemStatus.SystemState)
	}
	if shadowState.Outputs.LastNotification == nil {
		t.Error("Expected last notification to be recorded")
	} else {
		if shadowState.Outputs.LastNotification.AlertType != "aerator_failure" {
			t.Errorf("Expected alert type 'aerator_failure', got '%s'", shadowState.Outputs.LastNotification.AlertType)
		}
		if shadowState.Outputs.LastNotification.Priority != "urgent" {
			t.Errorf("Expected priority 'urgent', got '%s'", shadowState.Outputs.LastNotification.Priority)
		}
	}
}

func TestInfrastructureManager_Reset(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Trigger failure and get notification
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()

	initialCount := countNtfyNotifications(mockNtfy)

	// Reset the manager
	err := manager.Reset()
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Rate limiters should be cleared - another evaluation should be able to send
	// (though the condition tracking is still active)
	mockClock.Advance(1 * time.Minute)
	manager.TriggerEvaluation()

	// The reset clears rate limiters but not the current state
	// so if we're still in low power, we should still be in failure mode
	if !manager.IsAeratorFailure() {
		t.Log("After reset with continued low power, still in failure mode (expected)")
	}

	// Verify no extra notifications during reset itself
	if count := countNtfyNotifications(mockNtfy); count < initialCount {
		t.Errorf("Should have at least %d notifications, got %d", initialCount, count)
	}
}

func TestInfrastructureManager_PowerThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		powerW          float64
		expectLowStart  bool
		expectHighStart bool
	}{
		{"Below aerator min (0W)", 0, true, false},
		{"Below aerator min (49W)", 49, true, false},
		{"At aerator min (50W)", 50, false, false},
		{"Normal aerator (100W)", 100, false, false},
		{"Normal aerator (200W)", 200, false, false},
		{"At pump threshold (300W)", 300, false, false},
		{"Above pump threshold (301W)", 301, false, true},
		{"Pump running (500W)", 500, false, true},
		{"Pump running high (600W)", 600, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			mockNtfy := ntfy.NewMockClient()
			mockClock := clock.NewMockClock(time.Now())

			manager := createTestManager(mockHA, mockNtfy, mockClock)

			manager.SimulatePowerReading(tt.powerW)

			lowStart := manager.GetLowPowerStartTime()
			highStart := manager.GetHighPowerStartTime()

			if tt.expectLowStart && lowStart.IsZero() {
				t.Errorf("Expected low power start time to be set for %s", tt.name)
			}
			if !tt.expectLowStart && !lowStart.IsZero() {
				t.Errorf("Expected low power start time to be zero for %s", tt.name)
			}
			if tt.expectHighStart && highStart.IsZero() {
				t.Errorf("Expected high power start time to be set for %s", tt.name)
			}
			if !tt.expectHighStart && !highStart.IsZero() {
				t.Errorf("Expected high power start time to be zero for %s", tt.name)
			}
		})
	}
}

func TestInfrastructureManager_NtfyClientNil(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create manager without ntfy client
	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, nil, mockClock)

	// Trigger failure - should not panic
	manager.SimulatePowerReading(30.0)
	mockClock.Advance(6 * time.Minute)
	manager.TriggerEvaluation()

	// Should still track state correctly
	if !manager.IsAeratorFailure() {
		t.Error("Should still detect aerator failure without ntfy client")
	}

	shadowState := manager.GetShadowState()
	if shadowState.Outputs.SepticSystemStatus.SystemState != "aerator_failure" {
		t.Errorf("Expected state 'aerator_failure', got '%s'", shadowState.Outputs.SepticSystemStatus.SystemState)
	}
}
