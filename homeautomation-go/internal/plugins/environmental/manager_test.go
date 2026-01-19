package environmental

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Test sensor entity IDs
const (
	testIndoorSensor1  = "sensor.indoor_humidity_1"
	testIndoorSensor2  = "sensor.indoor_humidity_2"
	testOutdoorSensor1 = "sensor.outdoor_humidity_1"
)

// setupMockEnvironment creates a mock HA client with test devices and entity registry
func setupMockEnvironment(mockHA *ha.MockClient) {
	// Add devices - one indoor (with "Indoor" label), one outdoor (no label)
	mockHA.AddDevice(&ha.Device{
		ID:     "device_indoor_1",
		Name:   "Indoor Sensor 1",
		Labels: []string{"Indoor"}, // Case matters - we use EqualFold
	})
	mockHA.AddDevice(&ha.Device{
		ID:     "device_indoor_2",
		Name:   "Indoor Sensor 2",
		Labels: []string{"indoor"}, // lowercase
	})
	mockHA.AddDevice(&ha.Device{
		ID:     "device_outdoor_1",
		Name:   "Outdoor Sensor 1",
		Labels: []string{}, // No indoor label
	})

	// Add entity registry entries linking entities to devices
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testIndoorSensor1,
		DeviceID: "device_indoor_1",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testIndoorSensor2,
		DeviceID: "device_indoor_2",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testOutdoorSensor1,
		DeviceID: "device_outdoor_1",
	})

	// Add initial sensor states with device_class: humidity
	mockHA.SetState(testIndoorSensor1, "45.0", map[string]interface{}{
		"device_class":  "humidity",
		"friendly_name": "Indoor Humidity 1",
	})
	mockHA.SetState(testIndoorSensor2, "42.0", map[string]interface{}{
		"device_class":  "humidity",
		"friendly_name": "Indoor Humidity 2",
	})
	mockHA.SetState(testOutdoorSensor1, "80.0", map[string]interface{}{
		"device_class":  "humidity",
		"friendly_name": "Outdoor Humidity 1",
	})
}

// Helper to count notifications sent via ntfy mock
func countNtfyNotifications(mockNtfy *ntfy.MockClient) int {
	return len(mockNtfy.GetCalls())
}

// Helper to get the last notification sent via ntfy mock
func getLastNtfyNotification(mockNtfy *ntfy.MockClient) *ntfy.Message {
	calls := mockNtfy.GetCalls()
	if len(calls) == 0 {
		return nil
	}
	return &calls[len(calls)-1]
}

func TestEnvironmentalManager_DynamicDiscovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	setupMockEnvironment(mockHA)
	mockNtfy := ntfy.NewMockClient()

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockNtfy)

	// Start discovery
	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify sensors were discovered
	sensors := manager.GetSensors()
	if len(sensors) != 3 {
		t.Errorf("Expected 3 sensors discovered, got %d", len(sensors))
	}

	// Verify indoor classification
	indoor1, ok := sensors[testIndoorSensor1]
	if !ok {
		t.Error("Expected indoor sensor 1 to be discovered")
	} else if !indoor1.IsIndoor {
		t.Error("Expected indoor sensor 1 to be classified as indoor")
	}

	indoor2, ok := sensors[testIndoorSensor2]
	if !ok {
		t.Error("Expected indoor sensor 2 to be discovered")
	} else if !indoor2.IsIndoor {
		t.Error("Expected indoor sensor 2 to be classified as indoor")
	}

	outdoor1, ok := sensors[testOutdoorSensor1]
	if !ok {
		t.Error("Expected outdoor sensor 1 to be discovered")
	} else if outdoor1.IsIndoor {
		t.Error("Expected outdoor sensor 1 to be classified as outdoor")
	}
}

func TestEnvironmentalManager_NormalHumidity(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test sensors directly (skip discovery)
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// Simulate normal humidity readings (below warning threshold)
	manager.SimulateSensorChange(testIndoorSensor1, 45.0)

	// Verify no alert
	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' for normal humidity, got '%s'", alertLevel)
	}

	// Verify no notifications were sent
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for normal humidity, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_WarningThreshold_NotSustained(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// Simulate warning-level humidity
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	// Advance time but not enough to be sustained (15 minutes)
	mockClock.Advance(15 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	// Should not alert yet (not sustained 30 minutes)
	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' for non-sustained warning, got '%s'", alertLevel)
	}

	// Verify no notifications were sent
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for non-sustained warning, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_WarningThreshold_Sustained(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// Simulate warning-level humidity
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	// Advance time to sustained threshold (30+ minutes)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	// Should now be warning level
	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alertLevel 'warning' for sustained warning, got '%s'", alertLevel)
	}

	// Verify notification was sent
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for sustained warning, got %d", notificationCount)
		return
	}

	notification := getLastNtfyNotification(mockNtfy)
	if notification == nil {
		t.Error("Expected to find a notification")
		return
	}
	if notification.Title != "High Humidity Warning" {
		t.Errorf("Expected notification title 'High Humidity Warning', got '%s'", notification.Title)
	}
}

func TestEnvironmentalManager_CriticalThreshold_Sustained(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// Simulate critical-level humidity
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)

	// Advance time to sustained threshold (30+ minutes)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)

	// Should now be critical level
	alertLevel := manager.GetCurrentState()
	if alertLevel != "critical" {
		t.Errorf("Expected alertLevel 'critical' for sustained critical, got '%s'", alertLevel)
	}

	// Verify notification was sent
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for sustained critical, got %d", notificationCount)
		return
	}

	notification := getLastNtfyNotification(mockNtfy)
	if notification == nil {
		t.Error("Expected to find a notification")
		return
	}
	if notification.Title != "High Humidity Critical" {
		t.Errorf("Expected notification title 'High Humidity Critical', got '%s'", notification.Title)
	}
	// Verify critical notifications use high priority
	if notification.Priority != ntfy.PriorityHigh {
		t.Errorf("Expected critical notification to have high priority, got %d", notification.Priority)
	}
}

func TestEnvironmentalManager_OutdoorSensor_NoAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add outdoor sensor (should NOT trigger alerts)
	manager.AddSensor(&HumiditySensor{
		EntityID:     testOutdoorSensor1,
		FriendlyName: "Outdoor Humidity 1",
		IsIndoor:     false, // outdoor
		Valid:        true,
	})

	// Simulate critical-level humidity on outdoor sensor
	manager.SimulateSensorChange(testOutdoorSensor1, 90.0)

	// Advance time to sustained threshold
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testOutdoorSensor1, 90.0)

	// Should NOT trigger alert (outdoor sensor)
	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' for outdoor sensor, got '%s'", alertLevel)
	}

	// Verify no notifications were sent
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for outdoor sensor, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_MixedSensors_OnlyIndoorAlerts(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add both indoor and outdoor sensors
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:     testOutdoorSensor1,
		FriendlyName: "Outdoor Humidity 1",
		IsIndoor:     false,
		Valid:        true,
	})

	// Set outdoor sensor to critical (should be ignored)
	manager.SimulateSensorChange(testOutdoorSensor1, 90.0)

	// Set indoor sensor to warning level
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	// Advance time to sustained threshold
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	manager.SimulateSensorChange(testOutdoorSensor1, 90.0)

	// Should be warning (from indoor sensor only)
	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alertLevel 'warning' for indoor sensor, got '%s'", alertLevel)
	}

	// Verify notification was sent
	notification := getLastNtfyNotification(mockNtfy)
	if notification == nil {
		t.Error("Expected to find a notification")
	}
}

func TestEnvironmentalManager_BothSensorsElevated(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add two indoor sensors
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor2,
		FriendlyName: "Indoor Humidity 2",
		IsIndoor:     true,
		Valid:        true,
	})

	// Simulate both sensors at warning level
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	manager.SimulateSensorChange(testIndoorSensor2, 56.0)

	// Advance time to sustained threshold
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	manager.SimulateSensorChange(testIndoorSensor2, 56.0)

	// Verify at least 1 notification was sent
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount < 1 {
		t.Error("Expected at least 1 notification")
		return
	}

	notification := getLastNtfyNotification(mockNtfy)
	if notification == nil {
		t.Error("Expected to find a notification")
		return
	}
	if notification.Body == "" {
		t.Error("Expected notification body to be set")
	}
}

func TestEnvironmentalManager_Hysteresis_WarningClear(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// First, trigger a sustained warning
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Fatalf("Expected alertLevel 'warning', got '%s'", alertLevel)
	}

	initialNotifications := countNtfyNotifications(mockNtfy)

	// Now lower humidity to just below warning threshold (but above clear threshold)
	manager.SimulateSensorChange(testIndoorSensor1, 52.0)

	// Should still be in warning due to hysteresis (clear threshold is 50%)
	alertLevel = manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alertLevel 'warning' due to hysteresis, got '%s'", alertLevel)
	}

	// Now lower below clear threshold
	manager.SimulateSensorChange(testIndoorSensor1, 48.0)

	// Should now be cleared
	alertLevel = manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' after clearing, got '%s'", alertLevel)
	}

	// Should have sent a resolution notification
	finalNotifications := countNtfyNotifications(mockNtfy)
	if finalNotifications <= initialNotifications {
		t.Error("Expected resolution notification to be sent")
	}
}

func TestEnvironmentalManager_RateLimiting_Warning(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// First, trigger a sustained warning
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	initialNotifications := countNtfyNotifications(mockNtfy)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification, got %d", initialNotifications)
	}

	// Trigger another evaluation (same incident)
	mockClock.Advance(1 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	// Should not have sent another notification for the same incident
	finalNotifications := countNtfyNotifications(mockNtfy)
	if finalNotifications > initialNotifications {
		t.Errorf("Expected no additional notifications for same incident, got %d extra",
			finalNotifications-initialNotifications)
	}
}

func TestEnvironmentalManager_RateLimiting_Critical(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// First, trigger a sustained critical
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)

	initialNotifications := countNtfyNotifications(mockNtfy)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification, got %d", initialNotifications)
	}

	// Trigger evaluation again within rate limit (1 hour for critical)
	mockClock.Advance(30 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)

	// Should not have sent another notification (already notified for this incident)
	finalNotifications := countNtfyNotifications(mockNtfy)
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
			mockNtfy := ntfy.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			manager := NewManager(mockHA, stateMgr, logger, false, nil, mockNtfy)

			// Add test sensor
			manager.AddSensor(&HumiditySensor{
				EntityID:     testIndoorSensor1,
				FriendlyName: "Indoor Humidity 1",
				IsIndoor:     true,
				Valid:        true,
			})

			// Simulate invalid humidity reading via direct handler call
			manager.handleHumidityChange(testIndoorSensor1, nil, &ha.State{
				EntityID: testIndoorSensor1,
				State:    tt.value,
			})

			// Should not crash and should have no alerts
			alertLevel := manager.GetCurrentState()
			if alertLevel != "none" {
				t.Errorf("Expected alertLevel 'none' for invalid value, got '%s'", alertLevel)
			}

			// Verify sensor is marked invalid
			sensors := manager.GetSensors()
			if sensor, ok := sensors[testIndoorSensor1]; ok {
				if sensor.Valid {
					t.Error("Expected sensor to be marked invalid")
				}
			}
		})
	}
}

func TestEnvironmentalManager_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockNtfy, mockClock)

	// Add test sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// Trigger a sustained critical condition
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)

	// Alert level should be set in shadow state
	alertLevel := manager.GetCurrentState()
	if alertLevel != "critical" {
		t.Errorf("Expected alertLevel 'critical', got '%s'", alertLevel)
	}

	// But no actual notifications should be sent (read-only mode)
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications in read-only mode, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_ShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test sensors
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:     testOutdoorSensor1,
		FriendlyName: "Outdoor Humidity 1",
		IsIndoor:     false,
		Valid:        true,
	})

	// Trigger handler with state change
	manager.SimulateSensorChange(testIndoorSensor1, 45.0)
	manager.SimulateSensorChange(testOutdoorSensor1, 80.0)

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify shadow state plugin name
	if shadowState.Plugin != "environmental" {
		t.Errorf("Expected plugin 'environmental', got '%s'", shadowState.Plugin)
	}

	// Verify shadow state outputs contain all sensors
	if len(shadowState.Outputs.HumiditySensors) != 2 {
		t.Errorf("Expected 2 sensors in shadow state, got %d", len(shadowState.Outputs.HumiditySensors))
	}

	// Verify alert level
	if shadowState.Outputs.AlertLevel != "none" {
		t.Errorf("Expected alert level 'none', got '%s'", shadowState.Outputs.AlertLevel)
	}

	// Verify sensor data
	foundIndoor := false
	foundOutdoor := false
	for _, sensor := range shadowState.Outputs.HumiditySensors {
		if sensor.EntityID == testIndoorSensor1 {
			foundIndoor = true
			if !sensor.IsIndoor {
				t.Error("Expected indoor sensor to be marked as indoor")
			}
			if sensor.Value != 45.0 {
				t.Errorf("Expected indoor sensor value 45.0, got %f", sensor.Value)
			}
		}
		if sensor.EntityID == testOutdoorSensor1 {
			foundOutdoor = true
			if sensor.IsIndoor {
				t.Error("Expected outdoor sensor to be marked as outdoor")
			}
			if sensor.Value != 80.0 {
				t.Errorf("Expected outdoor sensor value 80.0, got %f", sensor.Value)
			}
		}
	}
	if !foundIndoor {
		t.Error("Indoor sensor not found in shadow state")
	}
	if !foundOutdoor {
		t.Error("Outdoor sensor not found in shadow state")
	}
}

func TestEnvironmentalManager_EscalationFromWarningToCritical(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// First trigger warning level
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Fatalf("Expected initial alertLevel 'warning', got '%s'", alertLevel)
	}

	warningNotifications := countNtfyNotifications(mockNtfy)

	// Now escalate to critical level
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)

	alertLevel = manager.GetCurrentState()
	if alertLevel != "critical" {
		t.Errorf("Expected escalated alertLevel 'critical', got '%s'", alertLevel)
	}

	// Should have sent a critical notification
	finalNotifications := countNtfyNotifications(mockNtfy)
	if finalNotifications <= warningNotifications {
		t.Error("Expected critical notification to be sent on escalation")
	}
}

func TestEnvironmentalManager_OneSensorHighOtherNormal(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add two indoor sensors
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor2,
		FriendlyName: "Indoor Humidity 2",
		IsIndoor:     true,
		Valid:        true,
	})

	// Sensor 1 at critical, sensor 2 normal
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)
	manager.SimulateSensorChange(testIndoorSensor2, 40.0)

	// Sustain the condition
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)

	// Should trigger critical based on just sensor 1
	alertLevel := manager.GetCurrentState()
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
		{"two", []string{"sensor 1", "sensor 2"}, "sensor 1 and sensor 2"},
		{"three or more", []string{"a", "b", "c"}, "a, b, c"},
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

func TestEnvironmentalManager_CaseInsensitiveLabelMatching(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		label    string
		expected bool
	}{
		{"lowercase", "indoor", true},
		{"uppercase", "INDOOR", true},
		{"mixed case", "Indoor", true},
		{"camelCase", "InDooR", true},
		{"different label", "outdoor", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockHA := ha.NewMockClient()
			mockNtfy := ntfy.NewMockClient()

			// Add device with the test label
			mockHA.AddDevice(&ha.Device{
				ID:     "device_test",
				Name:   "Test Sensor",
				Labels: []string{tc.label},
			})

			// Add entity registry entry
			mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
				EntityID: "sensor.test_humidity",
				DeviceID: "device_test",
			})

			// Add sensor state
			mockHA.SetState("sensor.test_humidity", "50.0", map[string]interface{}{
				"device_class":  "humidity",
				"friendly_name": "Test Humidity",
			})

			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)
			manager := NewManager(mockHA, stateMgr, logger, false, nil, mockNtfy)

			// Start discovery
			err := manager.Start()
			if err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			// Check if sensor was classified correctly
			sensors := manager.GetSensors()
			sensor, ok := sensors["sensor.test_humidity"]
			if !ok {
				t.Fatal("Expected sensor to be discovered")
			}

			if sensor.IsIndoor != tc.expected {
				t.Errorf("Expected IsIndoor=%v for label '%s', got %v",
					tc.expected, tc.label, sensor.IsIndoor)
			}
		})
	}
}

// ============================================================================
// Temperature Sensor Lockup Detection Tests
// ============================================================================

// Test sensor entity IDs for temperature
const (
	testTempSensor1 = "sensor.temp_sensor_1"
	testTempSensor2 = "sensor.temp_sensor_2"
)

func TestEnvironmentalManager_TemperatureSensor_NoLockup(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test temperature sensor
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Test Temperature 1",
		Value:           72.0,
		Valid:           true,
		LastValueChange: mockClock.Now(),
		LastValue:       72.0,
	})

	// Simulate temperature change (value changes)
	mockClock.Advance(6 * time.Hour)
	manager.SimulateTemperatureChange(testTempSensor1, 73.0)

	// Trigger lockup check
	manager.TriggerLockupCheck()

	// Verify sensor is not locked up
	sensors := manager.GetTemperatureSensors()
	sensor, ok := sensors[testTempSensor1]
	if !ok {
		t.Fatal("Expected temperature sensor to exist")
	}
	if sensor.IsLockedUp {
		t.Error("Expected temperature sensor to NOT be locked up (value changed)")
	}

	// Verify no notifications were sent
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for non-locked sensor, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_TemperatureSensor_Lockup_Detected(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test temperature sensor
	initialTime := mockClock.Now()
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Garage Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})

	// Simulate same value reported over 12 hours (lockup threshold)
	mockClock.Advance(13 * time.Hour)
	manager.SimulateTemperatureChange(testTempSensor1, 72.0) // Same value

	// Trigger lockup check
	manager.TriggerLockupCheck()

	// Verify sensor is marked as locked up
	sensors := manager.GetTemperatureSensors()
	sensor, ok := sensors[testTempSensor1]
	if !ok {
		t.Fatal("Expected temperature sensor to exist")
	}
	if !sensor.IsLockedUp {
		t.Error("Expected temperature sensor to be marked as locked up")
	}

	// Verify notification was sent
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount != 1 {
		t.Errorf("Expected 1 lockup notification, got %d", notificationCount)
		return
	}

	notification := getLastNtfyNotification(mockNtfy)
	if notification == nil {
		t.Error("Expected to find a notification")
		return
	}
	if notification.Title != "Temperature Sensor Locked Up" {
		t.Errorf("Expected notification title 'Temperature Sensor Locked Up', got '%s'", notification.Title)
	}
	if notification.Body == "" {
		t.Error("Expected notification body to be set")
	}
	// Verify body contains sensor name and hours
	if !contains(notification.Body, "Garage Temperature") {
		t.Errorf("Expected notification body to contain sensor name, got '%s'", notification.Body)
	}
	if !contains(notification.Body, "72.0") {
		t.Errorf("Expected notification body to contain stuck value, got '%s'", notification.Body)
	}
}

func TestEnvironmentalManager_TemperatureSensor_Lockup_Recovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test temperature sensor
	initialTime := mockClock.Now()
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Garage Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})

	// First, let the sensor become locked up
	mockClock.Advance(13 * time.Hour)
	manager.SimulateTemperatureChange(testTempSensor1, 72.0) // Same value
	manager.TriggerLockupCheck()

	// Verify locked up
	sensors := manager.GetTemperatureSensors()
	if !sensors[testTempSensor1].IsLockedUp {
		t.Fatal("Expected sensor to be locked up")
	}

	// Record the notification count after lockup
	lockupNotifications := countNtfyNotifications(mockNtfy)
	if lockupNotifications != 1 {
		t.Fatalf("Expected 1 lockup notification, got %d", lockupNotifications)
	}

	// Now simulate a value change (recovery)
	manager.SimulateTemperatureChange(testTempSensor1, 73.5) // Different value

	// Verify sensor recovered
	sensors = manager.GetTemperatureSensors()
	if sensors[testTempSensor1].IsLockedUp {
		t.Error("Expected sensor to recover from lockup after value change")
	}

	// Verify recovery notification was sent
	totalNotifications := countNtfyNotifications(mockNtfy)
	if totalNotifications != 2 {
		t.Errorf("Expected 2 notifications (1 lockup + 1 recovery), got %d", totalNotifications)
	}

	// Verify the recovery notification has correct content
	notification := getLastNtfyNotification(mockNtfy)
	if notification == nil {
		t.Fatal("Expected to find a notification")
	}
	if notification.Title != "Temperature Sensor Recovered" {
		t.Errorf("Expected notification title 'Temperature Sensor Recovered', got '%s'", notification.Title)
	}
	if !contains(notification.Body, "Garage Temperature") {
		t.Errorf("Expected notification body to contain sensor name, got '%s'", notification.Body)
	}
	if !contains(notification.Body, "recovered") {
		t.Errorf("Expected notification body to contain 'recovered', got '%s'", notification.Body)
	}
}

func TestEnvironmentalManager_TemperatureSensor_Lockup_RateLimiting(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test temperature sensor
	initialTime := mockClock.Now()
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Garage Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})

	// Let sensor become locked up and trigger first notification
	mockClock.Advance(13 * time.Hour)
	manager.TriggerLockupCheck()

	initialNotifications := countNtfyNotifications(mockNtfy)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification, got %d", initialNotifications)
	}

	// Trigger another check after a short time (within rate limit)
	mockClock.Advance(1 * time.Hour)
	manager.TriggerLockupCheck()

	// Should not have sent another notification (rate limited to 24 hours)
	afterRateLimit := countNtfyNotifications(mockNtfy)
	if afterRateLimit > initialNotifications {
		t.Errorf("Expected no additional notifications due to rate limiting, got %d extra",
			afterRateLimit-initialNotifications)
	}

	// Now advance past the rate limit (24 hours)
	mockClock.Advance(24 * time.Hour)
	manager.TriggerLockupCheck()

	// Should have sent another notification
	finalNotifications := countNtfyNotifications(mockNtfy)
	if finalNotifications <= initialNotifications {
		t.Error("Expected another notification after rate limit period")
	}
}

func TestEnvironmentalManager_TemperatureSensor_MultipleSensors(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add two temperature sensors
	initialTime := mockClock.Now()
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Garage Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor2,
		FriendlyName:    "Attic Temperature",
		Value:           85.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       85.0,
	})

	// Advance time past lockup threshold
	mockClock.Advance(13 * time.Hour)

	// Sensor 1 changes value, sensor 2 stays the same
	manager.SimulateTemperatureChange(testTempSensor1, 73.0) // Changed
	manager.SimulateTemperatureChange(testTempSensor2, 85.0) // Same

	// Trigger lockup check
	manager.TriggerLockupCheck()

	// Verify sensor 1 is NOT locked up (value changed)
	sensors := manager.GetTemperatureSensors()
	if sensors[testTempSensor1].IsLockedUp {
		t.Error("Expected sensor 1 to NOT be locked up (value changed)")
	}

	// Verify sensor 2 IS locked up (value stayed the same)
	if !sensors[testTempSensor2].IsLockedUp {
		t.Error("Expected sensor 2 to be locked up (value unchanged)")
	}

	// Should have only 1 notification (for sensor 2)
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for locked sensor 2, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_TemperatureSensor_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockNtfy, mockClock)

	// Add test temperature sensor
	initialTime := mockClock.Now()
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Garage Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})

	// Let sensor become locked up
	mockClock.Advance(13 * time.Hour)
	manager.TriggerLockupCheck()

	// Sensor should be marked as locked up
	sensors := manager.GetTemperatureSensors()
	if !sensors[testTempSensor1].IsLockedUp {
		t.Error("Expected sensor to be marked as locked up")
	}

	// But no actual notifications should be sent (read-only mode)
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications in read-only mode, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_TemperatureSensor_ShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test temperature sensors
	initialTime := mockClock.Now()
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Garage Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})

	// Update shadow state by simulating a change
	manager.SimulateTemperatureChange(testTempSensor1, 73.0)

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify shadow state plugin name
	if shadowState.Plugin != "environmental" {
		t.Errorf("Expected plugin 'environmental', got '%s'", shadowState.Plugin)
	}

	// Verify shadow state outputs contain temperature sensors
	if len(shadowState.Outputs.TemperatureSensors) != 1 {
		t.Errorf("Expected 1 temperature sensor in shadow state, got %d",
			len(shadowState.Outputs.TemperatureSensors))
		return
	}

	// Verify sensor data
	sensorData := shadowState.Outputs.TemperatureSensors[0]
	if sensorData.EntityID != testTempSensor1 {
		t.Errorf("Expected entity ID '%s', got '%s'", testTempSensor1, sensorData.EntityID)
	}
	if sensorData.Value != 73.0 {
		t.Errorf("Expected value 73.0, got %f", sensorData.Value)
	}
	if !sensorData.Valid {
		t.Error("Expected sensor to be valid")
	}
}

func TestEnvironmentalManager_TemperatureSensor_InvalidValues(t *testing.T) {
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
			mockNtfy := ntfy.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)
			mockClock := clock.NewMockClock(time.Now())

			manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

			// Add test sensor
			manager.AddTemperatureSensor(&TemperatureSensor{
				EntityID:        testTempSensor1,
				FriendlyName:    "Test Temperature",
				Value:           72.0,
				Valid:           true,
				LastValueChange: mockClock.Now(),
				LastValue:       72.0,
			})

			// Simulate invalid temperature reading
			manager.handleTemperatureChange(testTempSensor1, nil, &ha.State{
				EntityID: testTempSensor1,
				State:    tt.value,
			})

			// Verify sensor is marked invalid
			sensors := manager.GetTemperatureSensors()
			if sensor, ok := sensors[testTempSensor1]; ok {
				if sensor.Valid {
					t.Error("Expected sensor to be marked invalid")
				}
			}
		})
	}
}

func TestEnvironmentalManager_TemperatureSensor_Recovery_RateLimiting(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test temperature sensor
	initialTime := mockClock.Now()
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Garage Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})

	// First, let the sensor become locked up
	mockClock.Advance(13 * time.Hour)
	manager.TriggerLockupCheck()

	// Verify lockup notification sent
	if countNtfyNotifications(mockNtfy) != 1 {
		t.Fatal("Expected 1 lockup notification")
	}

	// Recover the sensor
	manager.SimulateTemperatureChange(testTempSensor1, 73.0)

	// Verify recovery notification sent (2 total)
	if countNtfyNotifications(mockNtfy) != 2 {
		t.Fatal("Expected 2 notifications (lockup + recovery)")
	}

	// Now simulate another lockup cycle within rate limit period
	mockClock.Advance(13 * time.Hour)                        // 26 hours total from start
	manager.SimulateTemperatureChange(testTempSensor1, 73.0) // Same value to reset LastValueChange tracking
	mockClock.Advance(13 * time.Hour)                        // 39 hours from start, but only 13 from last change
	manager.TriggerLockupCheck()

	// Should have sent another lockup notification (3 total) - sensor was locked up again
	notificationsAfterSecondLockup := countNtfyNotifications(mockNtfy)
	if notificationsAfterSecondLockup != 3 {
		t.Fatalf("Expected 3 notifications after second lockup, got %d", notificationsAfterSecondLockup)
	}

	// Now recover within the rate limit period (less than 24 hours since last recovery)
	// The second lockup was at ~39 hours, first recovery was at ~13 hours
	// So ~26 hours have passed - beyond rate limit, should send
	manager.SimulateTemperatureChange(testTempSensor1, 74.0)

	// Should have sent another recovery notification
	notificationsAfterSecondRecovery := countNtfyNotifications(mockNtfy)
	if notificationsAfterSecondRecovery != 4 {
		t.Errorf("Expected 4 notifications after second recovery (beyond rate limit), got %d", notificationsAfterSecondRecovery)
	}

	// Now simulate a quick lockup/recovery cycle within rate limit
	mockClock.Advance(1 * time.Hour) // Advance just 1 hour
	// Manually set the sensor to locked up state for testing
	sensors := manager.GetTemperatureSensors()
	sensors[testTempSensor1].IsLockedUp = true

	// Get internal sensor and set it locked up
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:                 testTempSensor1,
		FriendlyName:             "Garage Temperature",
		Value:                    74.0,
		Valid:                    true,
		LastValueChange:          mockClock.Now().Add(-13 * time.Hour),
		LastValue:                74.0,
		IsLockedUp:               true,
		LastRecoveryNotification: mockClock.Now().Add(-1 * time.Hour), // Set recent recovery notification
	})

	// Try to recover - should be rate limited
	manager.SimulateTemperatureChange(testTempSensor1, 75.0)

	// Should NOT have sent another notification due to rate limiting
	notificationsAfterRateLimited := countNtfyNotifications(mockNtfy)
	if notificationsAfterRateLimited != 4 {
		t.Errorf("Expected 4 notifications (recovery rate limited), got %d", notificationsAfterRateLimited)
	}
}

func TestEnvironmentalManager_TemperatureSensor_Recovery_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockNtfy, mockClock)

	// Add test temperature sensor
	initialTime := mockClock.Now()
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Garage Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})

	// Let sensor become locked up
	mockClock.Advance(13 * time.Hour)
	manager.TriggerLockupCheck()

	// Sensor should be marked as locked up
	sensors := manager.GetTemperatureSensors()
	if !sensors[testTempSensor1].IsLockedUp {
		t.Fatal("Expected sensor to be marked as locked up")
	}

	// No lockup notification should be sent (read-only mode)
	if countNtfyNotifications(mockNtfy) != 0 {
		t.Fatal("Expected no notifications in read-only mode")
	}

	// Now recover the sensor
	manager.SimulateTemperatureChange(testTempSensor1, 73.5)

	// Sensor should have recovered
	sensors = manager.GetTemperatureSensors()
	if sensors[testTempSensor1].IsLockedUp {
		t.Error("Expected sensor to recover from lockup")
	}

	// No recovery notification should be sent (read-only mode)
	if countNtfyNotifications(mockNtfy) != 0 {
		t.Errorf("Expected no notifications in read-only mode, got %d", countNtfyNotifications(mockNtfy))
	}

	// But shadow state should still be recorded
	shadowState := manager.GetShadowState()
	if shadowState.Outputs.LastTemperatureRecoveryNotice == nil {
		t.Error("Expected recovery notice to be recorded in shadow state")
	}
}

func TestEnvironmentalManager_TemperatureSensor_Recovery_ShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test temperature sensor
	initialTime := mockClock.Now()
	manager.AddTemperatureSensor(&TemperatureSensor{
		EntityID:        testTempSensor1,
		FriendlyName:    "Garage Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})

	// Let sensor become locked up and recover
	mockClock.Advance(13 * time.Hour)
	manager.TriggerLockupCheck()
	manager.SimulateTemperatureChange(testTempSensor1, 73.5) // Different value - recovery

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify recovery notice is recorded
	if shadowState.Outputs.LastTemperatureRecoveryNotice == nil {
		t.Fatal("Expected recovery notice to be recorded in shadow state")
	}
	if shadowState.Outputs.LastTemperatureRecoveryNotice.EntityID != testTempSensor1 {
		t.Errorf("Expected entity ID '%s', got '%s'",
			testTempSensor1, shadowState.Outputs.LastTemperatureRecoveryNotice.EntityID)
	}
	if shadowState.Outputs.LastTemperatureRecoveryNotice.FriendlyName != "Garage Temperature" {
		t.Errorf("Expected friendly name 'Garage Temperature', got '%s'",
			shadowState.Outputs.LastTemperatureRecoveryNotice.FriendlyName)
	}
	if !contains(shadowState.Outputs.LastTemperatureRecoveryNotice.Message, "recovered") {
		t.Errorf("Expected message to contain 'recovered', got '%s'",
			shadowState.Outputs.LastTemperatureRecoveryNotice.Message)
	}
}

func TestParseTemperature(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expected    float64
		shouldError bool
	}{
		{"valid integer", "72", 72.0, false},
		{"valid float", "72.5", 72.5, false},
		{"negative", "-10", -10.0, false},
		{"zero", "0", 0.0, false},
		{"empty string", "", 0.0, true},
		{"unknown", "unknown", 0.0, true},
		{"unavailable", "unavailable", 0.0, true},
		{"non-numeric", "abc", 0.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTemperature(tt.input)

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

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
