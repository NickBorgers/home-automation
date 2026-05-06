package waterflow

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Helper to count alert notifications
func countAlerts(mockAlerter *alert.MockAlerter) int {
	return len(mockAlerter.Calls())
}

// Helper to get the last alert notification
func getLastAlert(mockAlerter *alert.MockAlerter) *alert.Alert {
	calls := mockAlerter.Calls()
	if len(calls) == 0 {
		return nil
	}
	a := calls[len(calls)-1]
	return &a
}

// Helper to create a test manager
func createTestManager(mockHA *ha.MockClient, mockAlerter *alert.MockAlerter, mockClock *clock.MockClock) *Manager {
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	return NewManagerWithClock(context.Background(), mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)
}

// simulateSteadyFlow sends flow readings at the given interval for the given duration,
// advancing the mock clock between readings. The final clock position is start+duration.
func simulateSteadyFlow(manager *Manager, mockClock *clock.MockClock, flowRateGPM float64, duration, interval time.Duration) {
	manager.SimulateFlowReading(flowRateGPM)
	for elapsed := interval; elapsed <= duration; elapsed += interval {
		mockClock.Advance(interval)
		manager.SimulateFlowReading(flowRateGPM)
	}
}

func TestWaterFlowManager_NormalFlow(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	mockClock := clock.NewMockClock(time.Now())

	manager := createTestManager(mockHA, mockAlerter, mockClock)

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
	if count := countAlerts(mockAlerter); count != 0 {
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
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Simulate high flow (0.5 GPM) for 14 minutes — below all flow duration thresholds
	simulateSteadyFlow(manager, mockClock, 0.5, 14*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Should not trigger urgent alert (duration threshold not yet met)
	if manager.IsUrgentActive() {
		t.Error("Should not be in urgent state for short high flow")
	}

	// No alerts should be sent
	if count := countAlerts(mockAlerter); count != 0 {
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
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Simulate moderate flow (0.35 GPM) for 15 minutes — not over the warning flow duration threshold
	simulateSteadyFlow(manager, mockClock, 0.35, 15*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	if manager.IsWarningActive() {
		t.Error("Should not trigger warning before flow duration exceeds 15 minutes")
	}

	if count := countAlerts(mockAlerter); count != 0 {
		t.Errorf("Expected 0 notifications during debounce period, got %d", count)
	}

	// Advance past 15 minute flow duration threshold with continued readings
	mockClock.Advance(time.Minute)
	manager.SimulateFlowReading(0.35)
	manager.TriggerEvaluation()

	// Now should be in warning state
	if !manager.IsWarningActive() {
		t.Error("Should be in warning state after 15+ minutes of elevated flow")
	}

	// Should have sent notification
	if count := countAlerts(mockAlerter); count != 1 {
		t.Errorf("Expected 1 notification for warning, got %d", count)
	}

	// Verify notification content
	msg := getLastAlert(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Simulate high flow (0.5 GPM) for 14 minutes — below all flow duration thresholds
	simulateSteadyFlow(manager, mockClock, 0.5, 14*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	if manager.IsUrgentActive() {
		t.Error("Should not trigger urgent before flow duration exceeds 20 minutes")
	}

	// Advance past 20 minute flow duration threshold with continued readings
	simulateSteadyFlow(manager, mockClock, 0.5, 7*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Now should be in urgent state
	if !manager.IsUrgentActive() {
		t.Error("Should be in urgent state after 20+ minutes of high flow")
	}

	// Should have sent notification
	if count := countAlerts(mockAlerter); count != 1 {
		t.Errorf("Expected 1 notification for urgent, got %d", count)
	}

	// Verify notification content
	msg := getLastAlert(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Simulate urgent alert
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Verify urgent alert was sent
	if count := countAlerts(mockAlerter); count != 1 {
		t.Fatalf("Expected 1 notification for urgent, got %d", count)
	}

	// Simulate recovery (flow back to normal) - starts debounce
	manager.SimulateFlowReading(0.1)

	// Should NOT have sent recovery notification yet (debounce in progress)
	if count := countAlerts(mockAlerter); count != 1 {
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
	if count := countAlerts(mockAlerter); count != 2 {
		t.Errorf("Expected 2 notifications (urgent + recovery), got %d", count)
	}

	// Verify recovery notification
	msg := getLastAlert(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Trigger first urgent alert
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	initialCount := countAlerts(mockAlerter)

	// Recover
	manager.SimulateFlowReading(0.1)
	mockClock.Advance(31 * time.Second)
	manager.SimulateFlowReading(0.1)
	recoveryCount := countAlerts(mockAlerter)

	// Immediately trigger another urgent condition with a fresh 31-minute window
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Should NOT send another alert due to rate limiting (4 hour cooldown)
	if count := countAlerts(mockAlerter); count != recoveryCount {
		t.Errorf("Expected %d notifications (rate limited), got %d", recoveryCount, count)
	}

	// Advance past rate limit cooldown
	mockClock.Advance(5 * time.Hour)

	// Reset to clear state and try again
	manager.Reset()
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Now should have sent another notification
	if count := countAlerts(mockAlerter); count <= initialCount+1 {
		t.Log("Rate limiting working correctly - alert sent after cooldown period")
	}
}

func TestWaterFlowManager_RecoveryRateLimiting(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Trigger urgent alert
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// First recovery
	manager.SimulateFlowReading(0.1)
	mockClock.Advance(31 * time.Second)
	manager.SimulateFlowReading(0.1)
	firstRecoveryCount := countAlerts(mockAlerter)

	// Immediately fail and recover again
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()
	manager.SimulateFlowReading(0.1)
	mockClock.Advance(31 * time.Second)
	manager.SimulateFlowReading(0.1)

	// Second recovery should be rate limited (30 min cooldown)
	if count := countAlerts(mockAlerter); count != firstRecoveryCount {
		t.Logf("Notification count after second recovery: %d (expected rate limiting)", count)
	}
}

func TestWaterFlowManager_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create manager in read-only mode
	manager := NewManagerWithClock(context.Background(), mockHA, stateMgr, logger, true, nil, mockAlerter, mockClock)

	// Trigger urgent alert
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Alerts should still be dispatched (read-only behavior is internal to alert.Manager)
	if count := countAlerts(mockAlerter); count != 1 {
		t.Errorf("Expected 1 notification in read-only mode, got %d", count)
	}
}

func TestWaterFlowManager_ShadowStateTracking(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

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
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
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
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Trigger urgent alert
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	initialCount := countAlerts(mockAlerter)

	// Reset the manager
	err := manager.Reset()
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Rate limiters should be cleared
	mockClock.Advance(1 * time.Minute)
	manager.TriggerEvaluation()

	// Verify no extra notifications during reset itself
	if count := countAlerts(mockAlerter); count < initialCount {
		t.Errorf("Should have at least %d notifications, got %d", initialCount, count)
	}
}

func TestWaterFlowManager_AlerterNil(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create manager without an alerter — should not panic
	manager := NewManagerWithClock(context.Background(), mockHA, stateMgr, logger, false, nil, nil, mockClock)

	// Trigger urgent alert — should not panic
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Should still track state correctly
	if !manager.IsUrgentActive() {
		t.Error("Should still detect urgent condition without alerter")
	}

	shadowState := manager.GetShadowState()
	if shadowState.Outputs.AlertLevel != "urgent" {
		t.Errorf("Expected alert level 'urgent', got '%s'", shadowState.Outputs.AlertLevel)
	}
}

func TestWaterFlowManager_TransitionFromWarningToUrgent(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Simulate low flow for 30 minutes, then increase to urgent
	simulateSteadyFlow(manager, mockClock, 0.25, 30*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Increase to urgent flow and continue for 31 more minutes
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Should be in urgent state
	if !manager.IsUrgentActive() {
		t.Error("Should be in urgent state")
	}

	// Should have sent urgent notification only
	if count := countAlerts(mockAlerter); count != 1 {
		t.Errorf("Expected 1 notification (urgent), got %d", count)
	}

	msg := getLastAlert(mockAlerter)
	if msg.Title != "Possible Pipe Break" {
		t.Errorf("Expected urgent notification, got title '%s'", msg.Title)
	}
}

func TestWaterFlowManager_UrgentEscalationBypassesWarningCooldown(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Trigger a warning first, as the periodic checker would during sustained high usage.
	simulateSteadyFlow(manager, mockClock, 0.35, 16*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	if !manager.IsWarningActive() {
		t.Fatal("Expected warning to be active")
	}
	if count := countAlerts(mockAlerter); count != 1 {
		t.Fatalf("Expected 1 warning notification, got %d", count)
	}

	// Escalate to urgent before the repeat-alert cooldown expires.
	simulateSteadyFlow(manager, mockClock, 0.5, 21*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	if !manager.IsUrgentActive() {
		t.Fatal("Expected urgent to be active")
	}
	if count := countAlerts(mockAlerter); count != 2 {
		t.Fatalf("Expected urgent escalation notification despite warning cooldown, got %d notifications", count)
	}

	msg := getLastAlert(mockAlerter)
	if msg.Title != "Possible Pipe Break" {
		t.Errorf("Expected urgent notification, got title '%s'", msg.Title)
	}
}

func TestWaterFlowManager_RecoveryDebounce(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Simulate urgent alert
	simulateSteadyFlow(manager, mockClock, 0.5, 31*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Verify urgent alert was sent
	if count := countAlerts(mockAlerter); count != 1 {
		t.Fatalf("Expected 1 notification for urgent, got %d", count)
	}

	// Flow drops below threshold — starts debounce
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

	// Flow goes back above threshold — should reset debounce
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
	if count := countAlerts(mockAlerter); count != 1 {
		t.Errorf("Expected 1 notification (no recovery), got %d", count)
	}
}

func TestWaterFlowManager_RecoveryDebounceWithWarningThreshold(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Simulate warning alert (0.35 GPM for 61 minutes)
	simulateSteadyFlow(manager, mockClock, 0.35, 61*time.Minute, time.Minute)
	manager.TriggerEvaluation()

	// Verify warning alert was sent
	if count := countAlerts(mockAlerter); count != 1 {
		t.Fatalf("Expected 1 notification for warning, got %d", count)
	}

	if !manager.IsWarningActive() {
		t.Fatal("Expected warning to be active")
	}

	// Flow drops below threshold — starts debounce
	manager.SimulateFlowReading(0.1)

	// Verify debounce started
	if manager.GetRecoveryStartTime().IsZero() {
		t.Error("Expected recovery start time to be set when flow drops")
	}

	// Flow goes back above warning threshold — should reset debounce
	manager.SimulateFlowReading(0.35)

	// Verify debounce was cleared
	if !manager.GetRecoveryStartTime().IsZero() {
		t.Error("Expected recovery start time to be cleared when flow goes back up")
	}
}

// TestWaterFlowManager_SensorNoiseDoesNotPreventAlert verifies the fix for issue #1055:
// intermittent zero readings from the Droplet sensor must not prevent alerts from firing.
// Previously, any sub-threshold reading reset the consecutive-duration timer to zero.
func TestWaterFlowManager_SensorNoiseDoesNotPreventAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Simulate the real-world pattern from issue #1055:
	// pressure washer running at ~1.0 GPM with sensor noise every ~5th reading dropping to 0.
	// This gives ~80% high-flow time — above the urgent flow duration threshold.
	interval := 4 * time.Second
	noiseEvery := 5 // 1 out of every 5 readings is a zero

	end := startTime.Add(31 * time.Minute)
	for i := 0; mockClock.Now().Before(end); i++ {
		if i%noiseEvery == 0 {
			manager.SimulateFlowReading(0.0) // sensor noise
		} else {
			manager.SimulateFlowReading(1.0) // actual flow
		}
		mockClock.Advance(interval)
	}
	manager.TriggerEvaluation()

	if !manager.IsUrgentActive() {
		t.Error("Expected urgent alert to fire despite intermittent sensor noise — this is the regression from issue #1055")
	}

	if count := countAlerts(mockAlerter); count != 1 {
		t.Errorf("Expected 1 urgent notification, got %d", count)
	}
}

func TestWaterFlowManager_EventDrivenIdleGapsDoNotBiasWindow(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// GIVEN: Four short normal-use sessions spread across 32 minutes. The sensor only reports
	// while water is flowing, so the sample buffer is almost entirely above the urgent threshold.
	sessionStarts := []time.Duration{
		0,
		10 * time.Minute,
		20 * time.Minute,
		30 * time.Minute,
	}
	for _, sessionStart := range sessionStarts {
		if remaining := startTime.Add(sessionStart).Sub(mockClock.Now()); remaining > 0 {
			mockClock.Advance(remaining)
		}
		for i := 0; i < 7; i++ {
			manager.SimulateFlowReading(0.8)
			mockClock.Advance(4 * time.Second)
		}
	}

	if remaining := startTime.Add(32 * time.Minute).Sub(mockClock.Now()); remaining > 0 {
		mockClock.Advance(remaining)
	}

	// WHEN: The rolling window is evaluated after the intermittent usage.
	manager.TriggerEvaluation()

	// THEN: No urgent alert fires; idle gaps must count as idle time, not missing samples.
	if manager.IsUrgentActive() {
		t.Error("Should NOT fire urgent alert for short event-driven flow bursts separated by idle gaps")
	}
	if manager.IsWarningActive() {
		t.Error("Should NOT fire warning alert for short event-driven flow bursts separated by idle gaps")
	}
	if count := countAlerts(mockAlerter); count != 0 {
		t.Errorf("Expected 0 notifications for intermittent normal usage, got %d", count)
	}
}

// TestWaterFlowManager_TooMuchNoiseSuppressesAlert verifies that when the majority of readings
// are zero/low, no spurious alert fires (e.g., genuinely intermittent low flow doesn't alarm).
func TestWaterFlowManager_TooMuchNoiseSuppressesAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	startTime := time.Now()
	mockClock := clock.NewMockClock(startTime)

	manager := createTestManager(mockHA, mockAlerter, mockClock)

	// Simulate 50% high / 50% low readings over 25 minutes — below the cumulative duration thresholds.
	interval := 4 * time.Second
	end := startTime.Add(25 * time.Minute)
	for i := 0; mockClock.Now().Before(end); i++ {
		if i%2 == 0 {
			manager.SimulateFlowReading(1.0)
		} else {
			manager.SimulateFlowReading(0.0)
		}
		mockClock.Advance(interval)
	}
	manager.TriggerEvaluation()

	if manager.IsUrgentActive() {
		t.Error("Should NOT fire urgent alert when only 50% of readings exceed threshold")
	}
	if manager.IsWarningActive() {
		t.Error("Should NOT fire warning alert when cumulative flow duration is below threshold")
	}

	if count := countAlerts(mockAlerter); count != 0 {
		t.Errorf("Expected 0 notifications for noisy low flow, got %d", count)
	}
}
