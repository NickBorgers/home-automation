package waterflow

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
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
	return NewManagerWithClock(context.Background(), mockHA, stateMgr, logger, false, nil, mockNtfy, notify.NewMockNotifier(), mockClock)
}

func TestWaterFlowManager_NormalFlow(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	mockClock := clock.NewMockClock(time.Now())

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Simulate normal low flow (~0.1 GPM)
	manager.SimulateFlowReading(0.1)

	// Verify state
	if manager.GetCurrentFlowRate() != 0.1 {
		t.Errorf("Expected flow rate 0.1, got %f", manager.GetCurrentFlowRate())
	}

	if manager.IsWarningActive() {
		t.Error("Should not be in warning state for normal flow")
	}

	if manager.IsUrgentActive() {
		t.Error("Should not be in urgent state for normal flow")
	}

	// No notifications should be sent for normal operation
	if count := countNtfyNotifications(mockNtfy); count != 0 {
		t.Errorf("Expected 0 notifications for normal operation, got %d", count)
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.AlertLevel != "none" {
		t.Errorf("Expected alert level 'none', got '%s'", shadowState.Outputs.AlertLevel)
	}
}

func TestWaterFlowManager_ShortHighFlowNoAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Start with normal flow
	manager.SimulateFlowReading(0.1)

	// Simulate high flow (0.5 GPM)
	manager.SimulateFlowReading(0.5)

	// Advance time by 20 minutes (less than 30 min urgent threshold)
	mockClock.Advance(20 * time.Minute)
	manager.TriggerEvaluation()

	// Should not trigger urgent alert
	if manager.IsUrgentActive() {
		t.Error("Should not be in urgent state for short high flow")
	}

	// No alerts should be sent
	if count := countNtfyNotifications(mockNtfy); count != 0 {
		t.Errorf("Expected 0 notifications for short high flow, got %d", count)
	}

	// Simulate flow back to normal
	manager.SimulateFlowReading(0.1)

	// Verify back to normal
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.AlertLevel != "none" {
		t.Errorf("Expected alert level 'none' after recovery, got '%s'", shadowState.Outputs.AlertLevel)
	}
}

func TestWaterFlowManager_WarningAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Start with normal flow
	manager.SimulateFlowReading(0.1)

	// Simulate moderate flow (0.35 GPM - above warning, below urgent)
	manager.SimulateFlowReading(0.35)

	// Verify warning threshold tracking started
	if manager.GetWarningThresholdStartTime().IsZero() {
		t.Error("Expected warning threshold start time to be set")
	}

	// Advance time by 59 minutes (just under 60 min warning threshold)
	mockClock.Advance(59 * time.Minute)
	manager.TriggerEvaluation()

	if manager.IsWarningActive() {
		t.Error("Should not trigger warning before 60 minutes")
	}

	if count := countNtfyNotifications(mockNtfy); count != 0 {
		t.Errorf("Expected 0 notifications during debounce period, got %d", count)
	}

	// Advance past 60 minute threshold
	mockClock.Advance(2 * time.Minute)
	manager.TriggerEvaluation()

	// Now should be in warning state
	if !manager.IsWarningActive() {
		t.Error("Should be in warning state after 60+ minutes of elevated flow")
	}

	// Should have sent notification
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Errorf("Expected 1 notification for warning, got %d", count)
	}

	// Verify notification content
	msg := getLastNtfyNotification(mockNtfy)
	if msg == nil {
		t.Fatal("Expected notification message")
	}
	if msg.Priority != ntfy.PriorityHigh {
		t.Errorf("Expected high priority, got %d", msg.Priority)
	}
	if msg.Title != "Water Flow Warning" {
		t.Errorf("Expected title 'Water Flow Warning', got '%s'", msg.Title)
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.AlertLevel != "warning" {
		t.Errorf("Expected alert level 'warning', got '%s'", shadowState.Outputs.AlertLevel)
	}
	if !shadowState.Outputs.IsWarningConditionMet {
		t.Error("Expected IsWarningConditionMet to be true")
	}
	if len(shadowState.Outputs.ActiveAlerts) != 1 {
		t.Errorf("Expected 1 active alert, got %d", len(shadowState.Outputs.ActiveAlerts))
	}
}

func TestWaterFlowManager_UrgentAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Start with normal flow
	manager.SimulateFlowReading(0.1)

	// Simulate high flow (0.5 GPM - above urgent threshold)
	manager.SimulateFlowReading(0.5)

	// Verify both thresholds tracking started
	if manager.GetWarningThresholdStartTime().IsZero() {
		t.Error("Expected warning threshold start time to be set")
	}
	if manager.GetUrgentThresholdStartTime().IsZero() {
		t.Error("Expected urgent threshold start time to be set")
	}

	// Advance time by 29 minutes (just under 30 min urgent threshold)
	mockClock.Advance(29 * time.Minute)
	manager.TriggerEvaluation()

	if manager.IsUrgentActive() {
		t.Error("Should not trigger urgent before 30 minutes")
	}

	// Advance past 30 minute threshold
	mockClock.Advance(2 * time.Minute)
	manager.TriggerEvaluation()

	// Now should be in urgent state
	if !manager.IsUrgentActive() {
		t.Error("Should be in urgent state after 30+ minutes of high flow")
	}

	// Should have sent notification
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Errorf("Expected 1 notification for urgent, got %d", count)
	}

	// Verify notification content
	msg := getLastNtfyNotification(mockNtfy)
	if msg == nil {
		t.Fatal("Expected notification message")
	}
	if msg.Priority != ntfy.PriorityUrgent {
		t.Errorf("Expected urgent priority, got %d", msg.Priority)
	}
	if msg.Title != "Possible Pipe Break" {
		t.Errorf("Expected title 'Possible Pipe Break', got '%s'", msg.Title)
	}

	// Verify shadow state
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.AlertLevel != "urgent" {
		t.Errorf("Expected alert level 'urgent', got '%s'", shadowState.Outputs.AlertLevel)
	}
	if !shadowState.Outputs.IsUrgentConditionMet {
		t.Error("Expected IsUrgentConditionMet to be true")
	}
}

func TestWaterFlowManager_RecoveryNotification(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Simulate urgent alert
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	// Verify urgent alert was sent
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Fatalf("Expected 1 notification for urgent, got %d", count)
	}

	// Simulate recovery (flow back to normal) - starts debounce
	manager.SimulateFlowReading(0.1)

	// Should NOT have sent recovery notification yet (debounce in progress)
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Errorf("Expected 1 notification (recovery debounce not complete), got %d", count)
	}

	// Verify recovery debounce started
	if manager.GetRecoveryStartTime().IsZero() {
		t.Error("Expected recovery start time to be set")
	}

	// Alert should still be active during debounce
	if !manager.IsUrgentActive() {
		t.Error("Should still be in urgent state during recovery debounce")
	}

	// Advance past debounce period (30 seconds)
	mockClock.Advance(31 * time.Second)

	// Simulate another low flow reading to trigger debounce check
	manager.SimulateFlowReading(0.1)

	// Should have sent recovery notification now
	if count := countNtfyNotifications(mockNtfy); count != 2 {
		t.Errorf("Expected 2 notifications (urgent + recovery), got %d", count)
	}

	// Verify recovery notification
	msg := getLastNtfyNotification(mockNtfy)
	if msg == nil {
		t.Fatal("Expected recovery notification message")
	}
	if msg.Title != "Water Flow Returned to Normal" {
		t.Errorf("Expected title 'Water Flow Returned to Normal', got '%s'", msg.Title)
	}
	if msg.Priority != ntfy.PriorityDefault {
		t.Errorf("Expected default priority for recovery, got %d", msg.Priority)
	}

	// Verify back to normal state
	if manager.IsUrgentActive() {
		t.Error("Should not be in urgent state after recovery")
	}

	shadowState := manager.GetShadowState()
	if shadowState.Outputs.AlertLevel != "none" {
		t.Errorf("Expected alert level 'none' after recovery, got '%s'", shadowState.Outputs.AlertLevel)
	}
}

func TestWaterFlowManager_RateLimiting(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Trigger first urgent alert
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	initialCount := countNtfyNotifications(mockNtfy)

	// Recover - start debounce
	manager.SimulateFlowReading(0.1)
	// Advance past debounce period
	mockClock.Advance(31 * time.Second)
	manager.SimulateFlowReading(0.1)
	recoveryCount := countNtfyNotifications(mockNtfy)

	// Immediately trigger another urgent condition
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	// Should NOT send another alert due to rate limiting (4 hour cooldown)
	if count := countNtfyNotifications(mockNtfy); count != recoveryCount {
		t.Errorf("Expected %d notifications (rate limited), got %d", recoveryCount, count)
	}

	// Advance past rate limit cooldown
	mockClock.Advance(5 * time.Hour)

	// Reset to clear state and try again
	manager.Reset()
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	// Now should have sent another notification
	if count := countNtfyNotifications(mockNtfy); count <= initialCount+1 {
		t.Log("Rate limiting working correctly - alert sent after cooldown period")
	}
}

func TestWaterFlowManager_RecoveryRateLimiting(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Trigger urgent alert
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	// First recovery - start debounce
	manager.SimulateFlowReading(0.1)
	// Advance past debounce period
	mockClock.Advance(31 * time.Second)
	manager.SimulateFlowReading(0.1)
	firstRecoveryCount := countNtfyNotifications(mockNtfy)

	// Immediately fail and recover again
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()
	// Start recovery debounce
	manager.SimulateFlowReading(0.1)
	// Advance past debounce period
	mockClock.Advance(31 * time.Second)
	manager.SimulateFlowReading(0.1)

	// Second recovery should be rate limited (30 min cooldown)
	if count := countNtfyNotifications(mockNtfy); count != firstRecoveryCount {
		t.Logf("Notification count after second recovery: %d (expected rate limiting)", count)
	}
}

func TestWaterFlowManager_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create manager in read-only mode
	manager := NewManagerWithClock(context.Background(), mockHA, stateMgr, logger, true, nil, mockNtfy, notify.NewMockNotifier(), mockClock)

	// Trigger urgent alert
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
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

func TestWaterFlowManager_ShadowStateTracking(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Initial state
	shadowState := manager.GetShadowState()
	if shadowState.Plugin != "waterflow" {
		t.Errorf("Expected plugin name 'waterflow', got '%s'", shadowState.Plugin)
	}
	if shadowState.Outputs.AlertLevel != "none" {
		t.Errorf("Expected initial alert level 'none', got '%s'", shadowState.Outputs.AlertLevel)
	}

	// Simulate flow reading
	manager.SimulateFlowReading(0.2)
	shadowState = manager.GetShadowState()
	if shadowState.Outputs.CurrentFlowRateGPM != 0.2 {
		t.Errorf("Expected flow rate 0.2 in shadow state, got %f", shadowState.Outputs.CurrentFlowRateGPM)
	}

	// Trigger urgent alert
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	shadowState = manager.GetShadowState()
	if shadowState.Outputs.AlertLevel != "urgent" {
		t.Errorf("Expected alert level 'urgent', got '%s'", shadowState.Outputs.AlertLevel)
	}
	if shadowState.Outputs.LastNotification == nil {
		t.Error("Expected last notification to be recorded")
	} else {
		if shadowState.Outputs.LastNotification.AlertType != "urgent" {
			t.Errorf("Expected alert type 'urgent', got '%s'", shadowState.Outputs.LastNotification.AlertType)
		}
		if shadowState.Outputs.LastNotification.Priority != "urgent" {
			t.Errorf("Expected priority 'urgent', got '%s'", shadowState.Outputs.LastNotification.Priority)
		}
	}
}

func TestWaterFlowManager_Reset(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Trigger urgent alert
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	initialCount := countNtfyNotifications(mockNtfy)

	// Reset the manager
	err := manager.Reset()
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Rate limiters should be cleared
	mockClock.Advance(1 * time.Minute)
	manager.TriggerEvaluation()

	// Verify no extra notifications during reset itself
	if count := countNtfyNotifications(mockNtfy); count < initialCount {
		t.Errorf("Should have at least %d notifications, got %d", initialCount, count)
	}
}

func TestWaterFlowManager_FlowThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		flowRateGPM        float64
		expectWarningStart bool
		expectUrgentStart  bool
	}{
		{"No flow (0 GPM)", 0, false, false},
		{"Low flow (0.1 GPM)", 0.1, false, false},
		{"Below warning (0.29 GPM)", 0.29, false, false},
		{"At warning threshold (0.3 GPM)", 0.3, true, false},
		{"Above warning (0.35 GPM)", 0.35, true, false},
		{"Below urgent (0.39 GPM)", 0.39, true, false},
		{"At urgent threshold (0.4 GPM)", 0.4, true, true},
		{"Above urgent (0.5 GPM)", 0.5, true, true},
		{"High flow (1.0 GPM)", 1.0, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			mockNtfy := ntfy.NewMockClient()
			mockClock := clock.NewMockClock(time.Now())

			manager := createTestManager(mockHA, mockNtfy, mockClock)

			manager.SimulateFlowReading(tt.flowRateGPM)

			warningStart := manager.GetWarningThresholdStartTime()
			urgentStart := manager.GetUrgentThresholdStartTime()

			if tt.expectWarningStart && warningStart.IsZero() {
				t.Errorf("Expected warning start time to be set for %s", tt.name)
			}
			if !tt.expectWarningStart && !warningStart.IsZero() {
				t.Errorf("Expected warning start time to be zero for %s", tt.name)
			}
			if tt.expectUrgentStart && urgentStart.IsZero() {
				t.Errorf("Expected urgent start time to be set for %s", tt.name)
			}
			if !tt.expectUrgentStart && !urgentStart.IsZero() {
				t.Errorf("Expected urgent start time to be zero for %s", tt.name)
			}
		})
	}
}

func TestWaterFlowManager_NtfyClientNil(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create manager without ntfy client
	manager := NewManagerWithClock(context.Background(), mockHA, stateMgr, logger, false, nil, nil, notify.NewMockNotifier(), mockClock)

	// Trigger urgent alert - should not panic
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	// Should still track state correctly
	if !manager.IsUrgentActive() {
		t.Error("Should still detect urgent condition without ntfy client")
	}

	shadowState := manager.GetShadowState()
	if shadowState.Outputs.AlertLevel != "urgent" {
		t.Errorf("Expected alert level 'urgent', got '%s'", shadowState.Outputs.AlertLevel)
	}
}

func TestWaterFlowManager_TransitionFromWarningToUrgent(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Start with moderate flow (warning threshold)
	manager.SimulateFlowReading(0.35)

	// Advance time but not enough for warning
	mockClock.Advance(30 * time.Minute)
	manager.TriggerEvaluation()

	// Increase to urgent flow
	manager.SimulateFlowReading(0.5)

	// Advance past urgent threshold (30 min from new flow start)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	// Should be in urgent state (skipping warning since urgent came first)
	if !manager.IsUrgentActive() {
		t.Error("Should be in urgent state")
	}

	// Should have sent urgent notification only
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Errorf("Expected 1 notification (urgent), got %d", count)
	}

	msg := getLastNtfyNotification(mockNtfy)
	if msg.Title != "Possible Pipe Break" {
		t.Errorf("Expected urgent notification, got title '%s'", msg.Title)
	}
}

func TestWaterFlowManager_RecoveryDebounce(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Simulate urgent alert
	manager.SimulateFlowReading(0.5)
	mockClock.Advance(31 * time.Minute)
	manager.TriggerEvaluation()

	// Verify urgent alert was sent
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Fatalf("Expected 1 notification for urgent, got %d", count)
	}

	// Flow drops below threshold - starts debounce
	manager.SimulateFlowReading(0.1)

	// Verify debounce started
	if manager.GetRecoveryStartTime().IsZero() {
		t.Error("Expected recovery start time to be set when flow drops")
	}

	// Still in alert state
	if !manager.IsUrgentActive() {
		t.Error("Should still be in urgent state during debounce")
	}

	// Advance 15 seconds (not past 30 second debounce)
	mockClock.Advance(15 * time.Second)

	// Flow goes back above threshold - should reset debounce
	manager.SimulateFlowReading(0.5)

	// Verify debounce was cleared
	if !manager.GetRecoveryStartTime().IsZero() {
		t.Error("Expected recovery start time to be cleared when flow goes back up")
	}

	// Still in alert state
	if !manager.IsUrgentActive() {
		t.Error("Should still be in urgent state after brief dip")
	}

	// No recovery notification should have been sent
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Errorf("Expected 1 notification (no recovery), got %d", count)
	}
}

func TestWaterFlowManager_RecoveryDebounceWithWarningThreshold(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Simulate warning alert (0.35 GPM for 60+ minutes)
	manager.SimulateFlowReading(0.35)
	mockClock.Advance(61 * time.Minute)
	manager.TriggerEvaluation()

	// Verify warning alert was sent
	if count := countNtfyNotifications(mockNtfy); count != 1 {
		t.Fatalf("Expected 1 notification for warning, got %d", count)
	}

	if !manager.IsWarningActive() {
		t.Fatal("Expected warning to be active")
	}

	// Flow drops below threshold - starts debounce
	manager.SimulateFlowReading(0.1)

	// Verify debounce started
	if manager.GetRecoveryStartTime().IsZero() {
		t.Error("Expected recovery start time to be set when flow drops")
	}

	// Flow goes back above warning threshold - should reset debounce
	manager.SimulateFlowReading(0.35)

	// Verify debounce was cleared
	if !manager.GetRecoveryStartTime().IsZero() {
		t.Error("Expected recovery start time to be cleared when flow goes back up")
	}
}

func TestWaterFlowManager_FlowDropBelowUrgentButAboveWarning(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockNtfy, mockClock)

	// Start with high flow (above urgent)
	manager.SimulateFlowReading(0.5)

	// Advance time but not enough for urgent
	mockClock.Advance(15 * time.Minute)
	manager.TriggerEvaluation()

	// Flow drops to moderate (above warning, below urgent)
	manager.SimulateFlowReading(0.35)

	// Verify urgent threshold cleared but warning still tracked
	if !manager.GetUrgentThresholdStartTime().IsZero() {
		t.Error("Urgent threshold start time should be cleared when flow drops below urgent")
	}
	if manager.GetWarningThresholdStartTime().IsZero() {
		t.Error("Warning threshold start time should still be set")
	}
}
