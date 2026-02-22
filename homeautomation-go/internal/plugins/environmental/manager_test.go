package environmental

import (
	"strings"
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

	// Add two indoor sensors - only sensor 1 will be elevated
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

	// Set sensor 2 to a normal level (should NOT appear in resolution)
	manager.SimulateSensorChange(testIndoorSensor2, 40.0)

	// First, trigger a sustained warning on sensor 1 only
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

	// Verify resolution notification only mentions the sensor that was elevated (sensor 1),
	// not all indoor sensors
	resolutionNotification := getLastNtfyNotification(mockNtfy)
	if resolutionNotification == nil {
		t.Fatal("Expected resolution notification")
	}

	// Single sensor format: "<SensorName>: <value>% has returned to safe levels"
	if !strings.Contains(resolutionNotification.Body, "Indoor Humidity 1") {
		t.Errorf("Expected resolution to mention elevated sensor 'Indoor Humidity 1', got: %s",
			resolutionNotification.Body)
	}
	if strings.Contains(resolutionNotification.Body, "Indoor Humidity 2") {
		t.Errorf("Expected resolution NOT to mention non-elevated sensor 'Indoor Humidity 2', got: %s",
			resolutionNotification.Body)
	}
}

func TestEnvironmentalManager_MultipleSensorsElevated_Resolution(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add three indoor sensors - two will be elevated, one normal
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
	manager.AddSensor(&HumiditySensor{
		EntityID:     "sensor.indoor_humidity_3",
		FriendlyName: "Indoor Humidity 3",
		IsIndoor:     true,
		Valid:        true,
	})

	// Set sensor 3 to normal (should NOT appear in resolution)
	manager.SimulateSensorChange("sensor.indoor_humidity_3", 40.0)

	// Trigger sustained warning on sensors 1 and 2
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	manager.SimulateSensorChange(testIndoorSensor2, 56.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	manager.SimulateSensorChange(testIndoorSensor2, 56.0)

	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Fatalf("Expected alertLevel 'warning', got '%s'", alertLevel)
	}

	initialNotifications := countNtfyNotifications(mockNtfy)

	// Now lower both sensors below clear threshold
	manager.SimulateSensorChange(testIndoorSensor1, 48.0)
	manager.SimulateSensorChange(testIndoorSensor2, 47.0)

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

	// Verify resolution notification mentions both alerted sensors, not the third
	resolutionNotification := getLastNtfyNotification(mockNtfy)
	if resolutionNotification == nil {
		t.Fatal("Expected resolution notification")
	}

	// Multiple sensor format: "Humidity has returned to safe levels. Resolved sensors: ..."
	if !strings.Contains(resolutionNotification.Body, "Indoor Humidity 1") {
		t.Errorf("Expected resolution to mention elevated sensor 'Indoor Humidity 1', got: %s",
			resolutionNotification.Body)
	}
	if !strings.Contains(resolutionNotification.Body, "Indoor Humidity 2") {
		t.Errorf("Expected resolution to mention elevated sensor 'Indoor Humidity 2', got: %s",
			resolutionNotification.Body)
	}
	if strings.Contains(resolutionNotification.Body, "Indoor Humidity 3") {
		t.Errorf("Expected resolution NOT to mention non-elevated sensor 'Indoor Humidity 3', got: %s",
			resolutionNotification.Body)
	}
	if !strings.Contains(resolutionNotification.Body, "Resolved sensors:") {
		t.Errorf("Expected multi-sensor resolution format with 'Resolved sensors:', got: %s",
			resolutionNotification.Body)
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
// Water Leak Tests
// ============================================================================

// Test constants for water leak sensors
const (
	testWaterLeakSensor1 = "binary_sensor.water_leak_kitchen"
	testWaterLeakSensor2 = "binary_sensor.water_leak_bathroom"
	testWaterLeakSensor3 = "binary_sensor.water_leak_basement"
)

// setupMockWaterLeakEnvironment creates a mock HA client with water leak sensors
func setupMockWaterLeakEnvironment(mockHA *ha.MockClient) {
	// Add devices for water leak sensors
	mockHA.AddDevice(&ha.Device{
		ID:     "device_water_leak_1",
		Name:   "Kitchen Water Leak Sensor",
		Labels: []string{},
	})
	mockHA.AddDevice(&ha.Device{
		ID:     "device_water_leak_2",
		Name:   "Bathroom Water Leak Sensor",
		Labels: []string{},
	})
	mockHA.AddDevice(&ha.Device{
		ID:     "device_water_leak_3",
		Name:   "Basement Water Leak Sensor",
		Labels: []string{},
	})

	// Add entity registry entries
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testWaterLeakSensor1,
		DeviceID: "device_water_leak_1",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testWaterLeakSensor2,
		DeviceID: "device_water_leak_2",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testWaterLeakSensor3,
		DeviceID: "device_water_leak_3",
	})

	// Add water leak sensor states (device_class: moisture)
	mockHA.SetState(testWaterLeakSensor1, "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Kitchen Water Leak",
	})
	mockHA.SetState(testWaterLeakSensor2, "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Bathroom Water Leak",
	})
	mockHA.SetState(testWaterLeakSensor3, "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Basement Water Leak",
	})
}

func TestEnvironmentalManager_WaterLeakSensor_Discovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	setupMockWaterLeakEnvironment(mockHA)
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

	// Verify water leak sensors were discovered
	sensors := manager.GetWaterLeakSensors()
	if len(sensors) != 3 {
		t.Errorf("Expected 3 water leak sensors, got %d", len(sensors))
	}

	// Verify each sensor was discovered with correct properties
	for _, entityID := range []string{testWaterLeakSensor1, testWaterLeakSensor2, testWaterLeakSensor3} {
		sensor, ok := sensors[entityID]
		if !ok {
			t.Errorf("Expected water leak sensor %s to be discovered", entityID)
			continue
		}
		if sensor.State != "off" {
			t.Errorf("Expected sensor %s to have state 'off', got '%s'", entityID, sensor.State)
		}
	}
}

func TestEnvironmentalManager_WaterLeakSensor_DiscoveryByEntityID(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	// Add device
	mockHA.AddDevice(&ha.Device{
		ID:     "device_water_sensor",
		Name:   "Water Sensor",
		Labels: []string{},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "binary_sensor.some_water_leak_detector",
		DeviceID: "device_water_sensor",
	})

	// Add sensor without device_class but with "water_leak" in entity_id
	mockHA.SetState("binary_sensor.some_water_leak_detector", "off", map[string]interface{}{
		"friendly_name": "Some Water Leak Detector",
		// No device_class - should still be discovered by entity_id pattern
	})

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockNtfy)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify sensor was discovered by entity_id pattern
	sensors := manager.GetWaterLeakSensors()
	if len(sensors) != 1 {
		t.Errorf("Expected 1 water leak sensor (discovered by entity_id), got %d", len(sensors))
	}

	if _, ok := sensors["binary_sensor.some_water_leak_detector"]; !ok {
		t.Error("Expected water leak sensor to be discovered by entity_id pattern")
	}
}

func TestEnvironmentalManager_WaterLeakSensor_MonitoringIgnoreLabel(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()

	// Add device with monitoring_ignore label
	mockHA.AddDevice(&ha.Device{
		ID:     "device_ignored",
		Name:   "Ignored Water Leak Device",
		Labels: []string{"monitoring_ignore"},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "binary_sensor.ignored_water_leak",
		DeviceID: "device_ignored",
	})
	mockHA.SetState("binary_sensor.ignored_water_leak", "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Ignored Water Leak",
	})

	// Add device without the label
	mockHA.AddDevice(&ha.Device{
		ID:     "device_monitored",
		Name:   "Monitored Water Leak Device",
		Labels: []string{},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "binary_sensor.monitored_water_leak",
		DeviceID: "device_monitored",
	})
	mockHA.SetState("binary_sensor.monitored_water_leak", "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Monitored Water Leak",
	})

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockNtfy)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify only non-ignored sensor was discovered
	sensors := manager.GetWaterLeakSensors()
	if len(sensors) != 1 {
		t.Errorf("Expected 1 water leak sensor (ignored one filtered), got %d", len(sensors))
	}
	if _, ok := sensors["binary_sensor.ignored_water_leak"]; ok {
		t.Error("Expected ignored water leak sensor to be filtered out")
	}
	if _, ok := sensors["binary_sensor.monitored_water_leak"]; !ok {
		t.Error("Expected monitored water leak sensor to be discovered")
	}
}

func TestEnvironmentalManager_WaterLeak_Detection(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add water leak sensor manually
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak",
		State:        "off",
	})

	// Simulate water leak detection (state changes to "on")
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Verify leak is detected
	activeLeaks := manager.GetActiveWaterLeakCount()
	if activeLeaks != 1 {
		t.Errorf("Expected 1 active water leak, got %d", activeLeaks)
	}

	// Verify notification was sent
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount != 1 {
		t.Errorf("Expected 1 water leak notification, got %d", notificationCount)
	}

	notification := getLastNtfyNotification(mockNtfy)
	if notification == nil {
		t.Fatal("Expected to find a notification")
	}
	if notification.Title != "Water Leak Detected" {
		t.Errorf("Expected notification title 'Water Leak Detected', got '%s'", notification.Title)
	}
	if notification.Priority != ntfy.PriorityUrgent {
		t.Errorf("Expected urgent priority for water leak, got %d", notification.Priority)
	}
}

func TestEnvironmentalManager_WaterLeak_NoDoubleNotification(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add water leak sensor
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak",
		State:        "off",
	})

	// Trigger leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	initialNotifications := countNtfyNotifications(mockNtfy)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification, got %d", initialNotifications)
	}

	// Simulate state change event again while still leaking (same state)
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Should not have sent another notification
	finalNotifications := countNtfyNotifications(mockNtfy)
	if finalNotifications != initialNotifications {
		t.Errorf("Expected no additional notifications for same leak, got %d extra",
			finalNotifications-initialNotifications)
	}
}

func TestEnvironmentalManager_WaterLeak_Recovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add water leak sensor
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak",
		State:        "off",
	})

	// First, detect a leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Verify leak is active
	if manager.GetActiveWaterLeakCount() != 1 {
		t.Error("Expected 1 active leak")
	}

	notificationsAfterLeak := countNtfyNotifications(mockNtfy)

	// Now clear the leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "off")

	// Verify leak is no longer active
	if manager.GetActiveWaterLeakCount() != 0 {
		t.Error("Expected 0 active leaks after clearing")
	}

	// Verify notification flag was reset (no recovery notification for water leaks by design)
	sensors := manager.GetWaterLeakSensors()
	sensor := sensors[testWaterLeakSensor1]
	if sensor.NotificationSent {
		t.Error("Expected NotificationSent to be reset after leak cleared")
	}

	// No additional notifications expected (water leaks don't have recovery notifications)
	notificationsAfterClear := countNtfyNotifications(mockNtfy)
	if notificationsAfterClear != notificationsAfterLeak {
		t.Errorf("Expected no additional notifications after clearing, got %d",
			notificationsAfterClear-notificationsAfterLeak)
	}
}

func TestEnvironmentalManager_WaterLeak_MultipleLeaks(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add multiple water leak sensors
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak",
		State:        "off",
	})
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor2,
		FriendlyName: "Bathroom Water Leak",
		State:        "off",
	})
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor3,
		FriendlyName: "Basement Water Leak",
		State:        "off",
	})

	// Trigger multiple leaks
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")
	manager.SimulateWaterLeakChange(testWaterLeakSensor2, "on")

	// Verify both leaks are active
	if manager.GetActiveWaterLeakCount() != 2 {
		t.Errorf("Expected 2 active leaks, got %d", manager.GetActiveWaterLeakCount())
	}

	// Verify both sent notifications
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount != 2 {
		t.Errorf("Expected 2 notifications (one per leak), got %d", notificationCount)
	}

	// Clear one leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "off")

	// Verify only one leak remains active
	if manager.GetActiveWaterLeakCount() != 1 {
		t.Errorf("Expected 1 active leak after clearing one, got %d", manager.GetActiveWaterLeakCount())
	}

	// Clear remaining leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor2, "off")

	// Verify no leaks active
	if manager.GetActiveWaterLeakCount() != 0 {
		t.Errorf("Expected 0 active leaks after clearing all, got %d", manager.GetActiveWaterLeakCount())
	}
}

func TestEnvironmentalManager_WaterLeak_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockNtfy, mockClock)

	// Add water leak sensor
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak",
		State:        "off",
	})

	// Trigger leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Verify leak is detected in state
	if manager.GetActiveWaterLeakCount() != 1 {
		t.Error("Expected 1 active leak even in read-only mode")
	}

	// But no actual notifications should be sent (read-only mode)
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications in read-only mode, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_WaterLeak_ShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add water leak sensors
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak",
		State:        "off",
	})
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor2,
		FriendlyName: "Bathroom Water Leak",
		State:        "off",
	})

	// Update shadow state
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "off")
	manager.SimulateWaterLeakChange(testWaterLeakSensor2, "off")

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify shadow state contains water leak sensors
	if len(shadowState.Outputs.WaterLeakSensors) != 2 {
		t.Errorf("Expected 2 water leak sensors in shadow state, got %d",
			len(shadowState.Outputs.WaterLeakSensors))
	}

	// Trigger a leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Get updated shadow state
	shadowState = manager.GetShadowState()

	// Verify active leaks are tracked
	if len(shadowState.Outputs.ActiveWaterLeaks) != 1 {
		t.Errorf("Expected 1 active water leak in shadow state, got %d",
			len(shadowState.Outputs.ActiveWaterLeaks))
	}

	// Verify notification was recorded
	if shadowState.Outputs.LastWaterLeakNotice == nil {
		t.Error("Expected water leak notification to be recorded in shadow state")
	}
}

// ============================================================================
// Issue #707: False humidity resolution when Z-Wave sensors go unavailable
// ============================================================================

func TestScenario_HumidityAlertDoesNotResolveWhenSensorUnavailable(t *testing.T) {
	t.Parallel()
	// GIVEN: Barn sensor is indoor, valid, at 60% humidity
	// AND: A warning alert has been sent (sustained for 30+ min)
	// AND: alertedSensorNames contains the sensor
	t.Log("GIVEN: Active humidity warning for an indoor sensor at 60%")

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add an indoor sensor (simulating the Barn sensor)
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// Also add another indoor sensor at normal levels (simulating non-Z-Wave sensors)
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor2,
		FriendlyName: "SEN55 Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.SimulateSensorChange(testIndoorSensor2, 35.0)

	// Trigger sustained warning on the indoor sensor at 60%
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)

	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Fatalf("Expected alertLevel 'warning', got '%s'", alertLevel)
	}

	notificationsBeforeUnavailable := countNtfyNotifications(mockNtfy)

	// WHEN: Sensor goes unavailable (Z-Wave dropout)
	t.Log("WHEN: Barn sensor goes unavailable (Z-Wave dropout)")
	manager.SimulateSensorUnavailable(testIndoorSensor1)

	// THEN: checkConditionResolved should NOT resolve the alert
	// AND: No resolution notification should be sent
	// AND: currentAlertLevel should remain "warning"
	// AND: alertedSensorNames should still contain the sensor
	t.Log("THEN: Alert remains active, no resolution notification sent")

	alertLevel = manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alertLevel 'warning' after sensor goes unavailable, got '%s'", alertLevel)
	}

	notificationsAfterUnavailable := countNtfyNotifications(mockNtfy)
	if notificationsAfterUnavailable > notificationsBeforeUnavailable {
		lastNotification := getLastNtfyNotification(mockNtfy)
		t.Errorf("Expected no new notifications after sensor goes unavailable, but got one: %s",
			lastNotification.Body)
	}

	alertedNames := manager.GetAlertedSensorNames()
	if !alertedNames["Barn Humidity"] {
		t.Error("Expected alertedSensorNames to still contain 'Barn Humidity'")
	}
}

func TestScenario_HumidityResolvesCorrectlyAfterSensorRecovers(t *testing.T) {
	t.Parallel()
	// GIVEN: Sensor is indoor, valid, at 60% humidity with active warning
	t.Log("GIVEN: Active humidity warning for sensor at 60%")

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor2,
		FriendlyName: "SEN55 Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.SimulateSensorChange(testIndoorSensor2, 35.0)

	// Trigger sustained warning
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)

	if manager.GetCurrentState() != "warning" {
		t.Fatalf("Expected alertLevel 'warning'")
	}

	// WHEN: Sensor goes unavailable briefly, then recovers at 45% (below clear threshold of 50%)
	t.Log("WHEN: Barn goes unavailable, then recovers at 45%")
	manager.SimulateSensorUnavailable(testIndoorSensor1)

	// Alert should still be active while unavailable
	if manager.GetCurrentState() != "warning" {
		t.Errorf("Expected alertLevel 'warning' while sensor unavailable, got '%s'", manager.GetCurrentState())
	}

	// Sensor comes back at 45%
	manager.SimulateSensorChange(testIndoorSensor1, 45.0)

	// THEN: Resolution notification fires with correct sensor info
	t.Log("THEN: Resolution correctly identifies only Barn sensor")

	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' after genuine resolution, got '%s'", alertLevel)
	}

	// Find the resolution notification
	lastNotification := getLastNtfyNotification(mockNtfy)
	if lastNotification == nil {
		t.Fatal("Expected resolution notification to be sent")
	}

	if !strings.Contains(lastNotification.Body, "Barn Humidity") {
		t.Errorf("Expected resolution to mention 'Barn Humidity', got: %s", lastNotification.Body)
	}
	if strings.Contains(lastNotification.Body, "SEN55 Humidity") {
		t.Errorf("Expected resolution NOT to mention 'SEN55 Humidity', got: %s", lastNotification.Body)
	}
}

func TestScenario_HumidityDoesNotResolveWhenSensorRecoversStillElevated(t *testing.T) {
	t.Parallel()
	// GIVEN: Sensor at 60% with active warning
	t.Log("GIVEN: Active humidity warning for sensor at 60%")

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// Trigger sustained warning
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)

	if manager.GetCurrentState() != "warning" {
		t.Fatalf("Expected alertLevel 'warning'")
	}

	notificationsBeforeUnavailable := countNtfyNotifications(mockNtfy)

	// WHEN: Sensor goes unavailable briefly, then recovers at 55% (above clear threshold of 50%)
	t.Log("WHEN: Barn goes unavailable, then recovers at 55%")
	manager.SimulateSensorUnavailable(testIndoorSensor1)
	manager.SimulateSensorChange(testIndoorSensor1, 55.0)

	// THEN: No resolution notification should be sent
	// AND: currentAlertLevel should remain "warning"
	t.Log("THEN: Alert remains active since Barn is still elevated")

	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alertLevel 'warning' when sensor recovers still elevated, got '%s'", alertLevel)
	}

	notificationsAfterRecovery := countNtfyNotifications(mockNtfy)
	if notificationsAfterRecovery > notificationsBeforeUnavailable {
		t.Errorf("Expected no new notifications while sensor is still elevated, got %d extra",
			notificationsAfterRecovery-notificationsBeforeUnavailable)
	}
}

func TestScenario_HumidityAlertSurvivesZWaveNetworkDropout(t *testing.T) {
	t.Parallel()
	// GIVEN: Barn sensor at 60% (warning alert active)
	// AND: Other Z-Wave indoor sensors are valid
	t.Log("GIVEN: Active humidity warning, multiple Z-Wave sensors online")

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Barn sensor (Z-Wave, will go unavailable)
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	// Caroline Office (Z-Wave, will go unavailable)
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor2,
		FriendlyName: "Caroline Office Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	// SEN55 (non-Z-Wave, stays valid)
	manager.AddSensor(&HumiditySensor{
		EntityID:     "sensor.indoor_humidity_3",
		FriendlyName: "SEN55 Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// Set normal levels for other sensors
	manager.SimulateSensorChange(testIndoorSensor2, 40.0)
	manager.SimulateSensorChange("sensor.indoor_humidity_3", 35.0)

	// Trigger sustained warning on Barn
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)

	if manager.GetCurrentState() != "warning" {
		t.Fatalf("Expected alertLevel 'warning'")
	}

	notificationsBeforeDropout := countNtfyNotifications(mockNtfy)

	// WHEN: All Z-Wave sensors go unavailable simultaneously (network issue)
	t.Log("WHEN: Z-Wave network drops — all Z-Wave sensors go unavailable")
	manager.SimulateSensorUnavailable(testIndoorSensor1)
	manager.SimulateSensorUnavailable(testIndoorSensor2)

	// THEN: Alert should NOT resolve
	// AND: No notification should be sent
	t.Log("THEN: Alert persists through Z-Wave dropout, no false notifications")

	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alertLevel 'warning' during Z-Wave dropout, got '%s'", alertLevel)
	}

	notificationsAfterDropout := countNtfyNotifications(mockNtfy)
	if notificationsAfterDropout > notificationsBeforeDropout {
		lastNotification := getLastNtfyNotification(mockNtfy)
		t.Errorf("Expected no new notifications during Z-Wave dropout, got: %s", lastNotification.Body)
	}

	// AND: When Z-Wave recovers, the alert cycle continues seamlessly
	t.Log("AND: Z-Wave recovers, Barn still elevated, alert continues")
	manager.SimulateSensorChange(testIndoorSensor2, 40.0) // Caroline Office back to normal
	manager.SimulateSensorChange(testIndoorSensor1, 60.0) // Barn still elevated

	alertLevel = manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alertLevel 'warning' after Z-Wave recovery, got '%s'", alertLevel)
	}
}

func TestScenario_GenuineResolutionAfterUnavailablePeriod(t *testing.T) {
	t.Parallel()
	// GIVEN: Sensor at 60% (warning alert active)
	t.Log("GIVEN: Active humidity warning for sensor at 60%")

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// Trigger sustained warning
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)

	if manager.GetCurrentState() != "warning" {
		t.Fatalf("Expected alertLevel 'warning'")
	}

	// WHEN: Sensor goes unavailable
	t.Log("WHEN: Barn goes unavailable")
	manager.SimulateSensorUnavailable(testIndoorSensor1)

	// No resolution while unavailable
	if manager.GetCurrentState() != "warning" {
		t.Errorf("Expected 'warning' while unavailable, got '%s'", manager.GetCurrentState())
	}

	// Sensor comes back still elevated
	t.Log("AND: Barn comes back at 60% (still elevated, no resolution)")
	manager.SimulateSensorChange(testIndoorSensor1, 60.0)

	if manager.GetCurrentState() != "warning" {
		t.Errorf("Expected 'warning' when sensor returns still elevated, got '%s'", manager.GetCurrentState())
	}

	// Later, sensor genuinely drops below threshold
	t.Log("AND: Later, Barn genuinely drops to 48%")
	manager.SimulateSensorChange(testIndoorSensor1, 48.0)

	// THEN: Resolution fires only when sensor is valid AND below threshold
	t.Log("THEN: Resolution correctly identifies Barn as the resolved sensor")

	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' after genuine resolution, got '%s'", alertLevel)
	}

	lastNotification := getLastNtfyNotification(mockNtfy)
	if lastNotification == nil {
		t.Fatal("Expected resolution notification")
	}
	if !strings.Contains(lastNotification.Body, "Barn Humidity") {
		t.Errorf("Expected resolution to mention 'Barn Humidity', got: %s", lastNotification.Body)
	}
	if !strings.Contains(lastNotification.Body, "48%") {
		t.Errorf("Expected resolution to show current humidity '48%%', got: %s", lastNotification.Body)
	}
}

func TestScenario_ResolutionNotificationIncludesUnavailableSensors(t *testing.T) {
	t.Parallel()
	// Test Fix 2: If a genuine resolution fires while some alerted sensors are still unavailable,
	// the notification should include them with "unavailable" status instead of falling back to
	// listing all indoor sensors.
	t.Log("GIVEN: Two sensors triggered a warning, one goes unavailable, the other drops")

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor2,
		FriendlyName: "Guest Bedroom Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:     "sensor.indoor_humidity_3",
		FriendlyName: "SEN55 Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.SimulateSensorChange("sensor.indoor_humidity_3", 30.0)

	// Both Barn and Guest Bedroom elevated
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	manager.SimulateSensorChange(testIndoorSensor2, 57.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	manager.SimulateSensorChange(testIndoorSensor2, 57.0)

	if manager.GetCurrentState() != "warning" {
		t.Fatalf("Expected alertLevel 'warning'")
	}

	// Both drop below threshold (genuine resolution)
	manager.SimulateSensorChange(testIndoorSensor1, 45.0)
	manager.SimulateSensorChange(testIndoorSensor2, 45.0)

	if manager.GetCurrentState() != "none" {
		t.Errorf("Expected resolution, got '%s'", manager.GetCurrentState())
	}

	// Verify resolution notification mentions ONLY the alerted sensors, not SEN55
	lastNotification := getLastNtfyNotification(mockNtfy)
	if lastNotification == nil {
		t.Fatal("Expected resolution notification")
	}
	if strings.Contains(lastNotification.Body, "SEN55 Humidity") {
		t.Errorf("Resolution should NOT mention SEN55 (was never alerted), got: %s", lastNotification.Body)
	}
	if !strings.Contains(lastNotification.Body, "Barn Humidity") {
		t.Errorf("Resolution should mention Barn Humidity, got: %s", lastNotification.Body)
	}
	if !strings.Contains(lastNotification.Body, "Guest Bedroom Humidity") {
		t.Errorf("Resolution should mention Guest Bedroom Humidity, got: %s", lastNotification.Body)
	}
}

func TestEnvironmentalManager_WaterLeak_RenotificationAfterClear(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add water leak sensor
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak",
		State:        "off",
	})

	// First leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")
	if countNtfyNotifications(mockNtfy) != 1 {
		t.Fatal("Expected first leak notification")
	}

	// Clear leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "off")

	// New leak should trigger new notification
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Should have 2 notifications total (one per leak event)
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount != 2 {
		t.Errorf("Expected 2 notifications (new leak after clearing), got %d", notificationCount)
	}
}
