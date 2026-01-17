package environmental

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Helper to count notifications sent to a specific device
func countNotifications(mockHA *ha.MockClient, deviceName string) int {
	count := 0
	serviceName := "mobile_app_" + deviceName
	for _, call := range mockHA.GetServiceCalls() {
		if call.Domain == "notify" && call.Service == serviceName {
			count++
		}
	}
	return count
}

// Helper to get the last notification sent to a device
func getLastNotification(mockHA *ha.MockClient, deviceName string) *ha.ServiceCall {
	serviceName := "mobile_app_" + deviceName
	calls := mockHA.GetServiceCalls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Domain == "notify" && calls[i].Service == serviceName {
			return &calls[i]
		}
	}
	return nil
}

func TestEnvironmentalManager_NormalHumidity(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	// Simulate normal humidity readings (below warning threshold)
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "45.0",
	}
	lowState := &ha.State{
		EntityID: AtticLowHumiditySensor,
		State:    "42.0",
	}

	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	manager.handleAtticLowHumidityChange(AtticLowHumiditySensor, nil, lowState)

	// Verify no alert
	_, _, alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' for normal humidity, got '%s'", alertLevel)
	}

	// Verify no notifications were sent
	notificationCount := countNotifications(mockHA, NotificationTarget)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for normal humidity, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_WarningThreshold_NotSustained(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// Simulate warning-level humidity
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "58.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Advance time but not enough to be sustained (15 minutes)
	mockClock.Advance(15 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Should not alert yet (not sustained 30 minutes)
	_, _, alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' for non-sustained warning, got '%s'", alertLevel)
	}

	// Verify no notifications were sent
	notificationCount := countNotifications(mockHA, NotificationTarget)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for non-sustained warning, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_WarningThreshold_Sustained(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// Simulate warning-level humidity
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "58.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Advance time to sustained threshold (30+ minutes)
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Should now be warning level
	_, _, alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alertLevel 'warning' for sustained warning, got '%s'", alertLevel)
	}

	// Verify notification was sent
	notificationCount := countNotifications(mockHA, NotificationTarget)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for sustained warning, got %d", notificationCount)
		return
	}

	notification := getLastNotification(mockHA, NotificationTarget)
	if notification == nil {
		t.Error("Expected to find a notification")
		return
	}
	title, _ := notification.Data["title"].(string)
	if title != "Attic Humidity Warning" {
		t.Errorf("Expected notification title 'Attic Humidity Warning', got '%s'", title)
	}
}

func TestEnvironmentalManager_CriticalThreshold_Sustained(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// Simulate critical-level humidity
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "70.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Advance time to sustained threshold (30+ minutes)
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Should now be critical level
	_, _, alertLevel := manager.GetCurrentState()
	if alertLevel != "critical" {
		t.Errorf("Expected alertLevel 'critical' for sustained critical, got '%s'", alertLevel)
	}

	// Verify notification was sent
	notificationCount := countNotifications(mockHA, NotificationTarget)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for sustained critical, got %d", notificationCount)
		return
	}

	notification := getLastNotification(mockHA, NotificationTarget)
	if notification == nil {
		t.Error("Expected to find a notification")
		return
	}
	title, _ := notification.Data["title"].(string)
	if title != "Attic Humidity Critical" {
		t.Errorf("Expected notification title 'Attic Humidity Critical', got '%s'", title)
	}
	// Check for sticky in the data map
	data, hasData := notification.Data["data"].(map[string]interface{})
	if !hasData || data["sticky"] != true {
		t.Error("Expected critical notification to be sticky")
	}
}

func TestEnvironmentalManager_BothSensorsElevated(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// Simulate both sensors at warning level
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "58.0",
	}
	lowState := &ha.State{
		EntityID: AtticLowHumiditySensor,
		State:    "56.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	manager.handleAtticLowHumidityChange(AtticLowHumiditySensor, nil, lowState)

	// Advance time to sustained threshold
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	manager.handleAtticLowHumidityChange(AtticLowHumiditySensor, nil, lowState)

	// Verify at least 1 notification was sent
	notificationCount := countNotifications(mockHA, NotificationTarget)
	if notificationCount < 1 {
		t.Error("Expected at least 1 notification")
		return
	}

	notification := getLastNotification(mockHA, NotificationTarget)
	if notification == nil {
		t.Error("Expected to find a notification")
		return
	}
	message, _ := notification.Data["message"].(string)
	if message == "" {
		t.Error("Expected notification message to be set")
	}
}

func TestEnvironmentalManager_Hysteresis_WarningClear(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// First, trigger a sustained warning
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "58.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	_, _, alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Fatalf("Expected alertLevel 'warning', got '%s'", alertLevel)
	}

	initialNotifications := countNotifications(mockHA, NotificationTarget)

	// Now lower humidity to just below warning threshold (but above clear threshold)
	highState.State = "52.0"
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Should still be in warning due to hysteresis (clear threshold is 50%)
	_, _, alertLevel = manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alertLevel 'warning' due to hysteresis, got '%s'", alertLevel)
	}

	// Now lower below clear threshold
	highState.State = "48.0"
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Should now be cleared
	_, _, alertLevel = manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' after clearing, got '%s'", alertLevel)
	}

	// Should have sent a resolution notification
	finalNotifications := countNotifications(mockHA, NotificationTarget)
	if finalNotifications <= initialNotifications {
		t.Error("Expected resolution notification to be sent")
	}
}

func TestEnvironmentalManager_RateLimiting_Warning(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// First, trigger a sustained warning
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "58.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	initialNotifications := countNotifications(mockHA, NotificationTarget)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification, got %d", initialNotifications)
	}

	// Trigger another evaluation (same incident)
	mockClock.Advance(1 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Should not have sent another notification for the same incident
	finalNotifications := countNotifications(mockHA, NotificationTarget)
	if finalNotifications > initialNotifications {
		t.Errorf("Expected no additional notifications for same incident, got %d extra",
			finalNotifications-initialNotifications)
	}
}

func TestEnvironmentalManager_RateLimiting_Critical(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// First, trigger a sustained critical
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "70.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	initialNotifications := countNotifications(mockHA, NotificationTarget)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification, got %d", initialNotifications)
	}

	// Trigger evaluation again within rate limit (1 hour for critical)
	mockClock.Advance(30 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Should not have sent another notification (already notified for this incident)
	finalNotifications := countNotifications(mockHA, NotificationTarget)
	if finalNotifications > initialNotifications {
		t.Errorf("Expected no additional notifications due to rate limiting, got %d",
			finalNotifications-initialNotifications)
	}
}

func TestEnvironmentalManager_InvalidHumidityValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"unknown", "unknown"},
		{"unavailable", "unavailable"},
		{"non-numeric", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			manager := NewManager(mockHA, stateMgr, logger, false, nil)

			// Simulate invalid humidity reading
			highState := &ha.State{
				EntityID: AtticHighHumiditySensor,
				State:    tt.value,
			}
			manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

			// Should not crash and should have no alerts
			_, _, alertLevel := manager.GetCurrentState()
			if alertLevel != "none" {
				t.Errorf("Expected alertLevel 'none' for invalid value, got '%s'", alertLevel)
			}
		})
	}
}

func TestEnvironmentalManager_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockClock)

	// Trigger a sustained critical condition
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "70.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Alert level should be set in shadow state
	_, _, alertLevel := manager.GetCurrentState()
	if alertLevel != "critical" {
		t.Errorf("Expected alertLevel 'critical', got '%s'", alertLevel)
	}

	// But no actual notifications should be sent (read-only mode)
	notificationCount := countNotifications(mockHA, NotificationTarget)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications in read-only mode, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_ShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// Set initial humidity values
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "45.0",
	}
	lowState := &ha.State{
		EntityID: AtticLowHumiditySensor,
		State:    "42.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	manager.handleAtticLowHumidityChange(AtticLowHumiditySensor, nil, lowState)

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify shadow state values
	if shadowState.Plugin != "environmental" {
		t.Errorf("Expected plugin 'environmental', got '%s'", shadowState.Plugin)
	}
	if shadowState.Outputs.AtticHumidity.HighSensorHumidity != 45.0 {
		t.Errorf("Expected high sensor humidity 45.0, got %f", shadowState.Outputs.AtticHumidity.HighSensorHumidity)
	}
	if shadowState.Outputs.AtticHumidity.LowSensorHumidity != 42.0 {
		t.Errorf("Expected low sensor humidity 42.0, got %f", shadowState.Outputs.AtticHumidity.LowSensorHumidity)
	}
	if shadowState.Outputs.AtticHumidity.AlertLevel != "none" {
		t.Errorf("Expected alert level 'none', got '%s'", shadowState.Outputs.AtticHumidity.AlertLevel)
	}
}

func TestEnvironmentalManager_EscalationFromWarningToCritical(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// First trigger warning level
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "58.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	_, _, alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Fatalf("Expected initial alertLevel 'warning', got '%s'", alertLevel)
	}

	warningNotifications := countNotifications(mockHA, NotificationTarget)

	// Now escalate to critical level
	highState.State = "70.0"
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	_, _, alertLevel = manager.GetCurrentState()
	if alertLevel != "critical" {
		t.Errorf("Expected escalated alertLevel 'critical', got '%s'", alertLevel)
	}

	// Should have sent a critical notification
	finalNotifications := countNotifications(mockHA, NotificationTarget)
	if finalNotifications <= warningNotifications {
		t.Error("Expected critical notification to be sent on escalation")
	}
}

func TestEnvironmentalManager_OneSensorHighOtherNormal(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// High sensor at critical, low sensor normal
	highState := &ha.State{
		EntityID: AtticHighHumiditySensor,
		State:    "70.0",
	}
	lowState := &ha.State{
		EntityID: AtticLowHumiditySensor,
		State:    "40.0",
	}
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	manager.handleAtticLowHumidityChange(AtticLowHumiditySensor, nil, lowState)

	// Sustain the condition
	mockClock.Advance(31 * time.Minute)
	manager.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)

	// Should trigger critical based on just the high sensor
	_, _, alertLevel := manager.GetCurrentState()
	if alertLevel != "critical" {
		t.Errorf("Expected alertLevel 'critical' when one sensor is critical, got '%s'", alertLevel)
	}
}

func TestParseHumidity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expected    float64
		shouldError bool
	}{
		{"valid integer", "55", 55.0, false},
		{"valid float", "55.5", 55.5, false},
		{"zero", "0", 0.0, false},
		{"empty string", "", 0.0, true},
		{"unknown", "unknown", 0.0, true},
		{"unavailable", "unavailable", 0.0, true},
		{"non-numeric", "abc", 0.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseHumidity(tt.input)

			if tt.shouldError && err == nil {
				t.Errorf("Expected error for input '%s', got nil", tt.input)
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Expected no error for input '%s', got %v", tt.input, err)
			}
			if !tt.shouldError && result != tt.expected {
				t.Errorf("Expected %f for input '%s', got %f", tt.expected, tt.input, result)
			}
		})
	}
}

func TestFormatSensorLocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{"empty", []string{}, "unknown"},
		{"single", []string{"high sensor"}, "high sensor"},
		{"two", []string{"high sensor", "low sensor"}, "both sensors"},
		{"multiple", []string{"a", "b", "c"}, "both sensors"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSensorLocations(tt.input)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}
