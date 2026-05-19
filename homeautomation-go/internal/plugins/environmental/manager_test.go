package environmental

import (
	"strings"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Test sensor entity IDs
const (
	testIndoorSensor1        = "sensor.indoor_humidity_1"
	testIndoorSensor2        = "sensor.indoor_humidity_2"
	testOutdoorSensor1       = "sensor.outdoor_humidity_1"
	testUnconditionedSensor1 = "sensor.unconditioned_humidity_1"
	testWeatherStation       = OutdoorHumidityEntityID // "sensor.weather_station_humidity"
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
func countAlerts(mockAlerter *alert.MockAlerter) int {
	return len(mockAlerter.Calls())
}

// Helper to get the last alert sent
func getLastAlert(mockAlerter *alert.MockAlerter) *alert.Alert {
	calls := mockAlerter.Calls()
	if len(calls) == 0 {
		return nil
	}
	last := calls[len(calls)-1]
	return &last
}

func TestEnvironmentalManager_DynamicDiscovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	setupMockEnvironment(mockHA)
	mockAlerter := &alert.MockAlerter{}

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for normal humidity, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_WarningThreshold_NotSustained(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for non-sustained warning, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_WarningThreshold_Sustained(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for sustained warning, got %d", notificationCount)
		return
	}

	notification := getLastAlert(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for sustained critical, got %d", notificationCount)
		return
	}

	notification := getLastAlert(mockAlerter)
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
	// Regression: humidity TTS must be deferable so it is suppressed while
	// master is asleep. ntfy push still fires at high priority (above) so the
	// user sees the alert on wake — but the Sonos announcement must not play.
	if notification.Urgency != notify.UrgencyDeferable {
		t.Errorf("Expected critical humidity Urgency=UrgencyDeferable (suppressed when asleep), got %v", notification.Urgency)
	}
}

func TestEnvironmentalManager_OutdoorSensor_NoAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for outdoor sensor, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_MixedSensors_OnlyIndoorAlerts(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notification := getLastAlert(mockAlerter)
	if notification == nil {
		t.Error("Expected to find a notification")
	}
}

func TestEnvironmentalManager_BothSensorsElevated(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount < 1 {
		t.Error("Expected at least 1 notification")
		return
	}

	notification := getLastAlert(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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

	initialNotifications := countAlerts(mockAlerter)

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
	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications <= initialNotifications {
		t.Error("Expected resolution notification to be sent")
	}

	// Verify resolution notification only mentions the sensor that was elevated (sensor 1),
	// not all indoor sensors
	resolutionNotification := getLastAlert(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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

	initialNotifications := countAlerts(mockAlerter)

	// Now lower both sensors below clear threshold
	manager.SimulateSensorChange(testIndoorSensor1, 48.0)
	manager.SimulateSensorChange(testIndoorSensor2, 47.0)

	// Should now be cleared
	alertLevel = manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alertLevel 'none' after clearing, got '%s'", alertLevel)
	}

	// Should have sent a resolution notification
	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications <= initialNotifications {
		t.Error("Expected resolution notification to be sent")
	}

	// Verify resolution notification mentions both alerted sensors, not the third
	resolutionNotification := getLastAlert(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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

	initialNotifications := countAlerts(mockAlerter)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification, got %d", initialNotifications)
	}

	// Trigger another evaluation (same incident)
	mockClock.Advance(1 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	// Should not have sent another notification for the same incident
	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications > initialNotifications {
		t.Errorf("Expected no additional notifications for same incident, got %d extra",
			finalNotifications-initialNotifications)
	}
}

func TestEnvironmentalManager_RateLimiting_WarningWhenTTSSuppressedAsleep(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{Err: notify.ErrSuppressedAsleep}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// GIVEN: Humidity has been above the warning threshold long enough to alert.
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	initialNotifications := countAlerts(mockAlerter)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification attempt, got %d", initialNotifications)
	}

	// WHEN: The sensor updates again while the same incident is active.
	mockClock.Advance(1 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 59.0)

	// THEN: ErrSuppressedAsleep still counts as delivered for rate limiting,
	// because ntfy push was already sent before TTS was suppressed.
	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications != initialNotifications {
		t.Errorf("Expected no repeated notification after TTS suppression, got %d extra",
			finalNotifications-initialNotifications)
	}
}

func TestEnvironmentalManager_RateLimiting_Critical(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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

	initialNotifications := countAlerts(mockAlerter)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification, got %d", initialNotifications)
	}

	// Trigger evaluation again within rate limit (1 hour for critical)
	mockClock.Advance(30 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)

	// Should not have sent another notification (already notified for this incident)
	finalNotifications := countAlerts(mockAlerter)
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
			mockAlerter := &alert.MockAlerter{}
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications in read-only mode, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_ShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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

	warningNotifications := countAlerts(mockAlerter)

	// Now escalate to critical level
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 70.0)

	alertLevel = manager.GetCurrentState()
	if alertLevel != "critical" {
		t.Errorf("Expected escalated alertLevel 'critical', got '%s'", alertLevel)
	}

	// Should have sent a critical notification
	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications <= warningNotifications {
		t.Error("Expected critical notification to be sent on escalation")
	}
}

func TestEnvironmentalManager_OneSensorHighOtherNormal(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
			mockAlerter := &alert.MockAlerter{}

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
			manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

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
// Unavailability / False Resolution Tests
// ============================================================================

func TestEnvironmentalManager_TransientUnavailability_NoFalseResolution(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add two indoor sensors (simulates Z-Wave sensor + non-Z-Wave sensor)
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor2,
		FriendlyName: "Most of House Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// GIVEN: Sensor 1 is at warning level (sustained 30+ min)
	t.Log("GIVEN: Barn sensor at 58% for 30+ minutes (warning active)")
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	manager.SimulateSensorChange(testIndoorSensor2, 35.0) // normal
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Fatalf("Expected warning alert active, got '%s'", alertLevel)
	}

	initialNotifications := countAlerts(mockAlerter)

	// WHEN: The alerted sensor goes unavailable (Z-Wave dropout)
	t.Log("WHEN: Barn sensor goes unavailable (Z-Wave dropout)")
	manager.SimulateSensorUnavailable(testIndoorSensor1)

	// AND: The non-elevated sensor sends a normal update
	t.Log("AND: Most of House sensor sends a normal update at 35%")
	manager.SimulateSensorChange(testIndoorSensor2, 35.0)

	// THEN: Alert should NOT be resolved (we lost visibility, not that it cleared)
	t.Log("THEN: Alert remains active, no false resolution notification")
	alertLevel = manager.GetCurrentState()
	if alertLevel == "none" {
		t.Error("Expected alert to remain active when alerted sensor goes unavailable, but got 'none'")
	}

	// No resolution notification should have been sent
	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications > initialNotifications {
		lastNotification := getLastAlert(mockAlerter)
		t.Errorf("Expected no new notifications, but got one: %s", lastNotification.Body)
	}
}

func TestEnvironmentalManager_UnavailableRecovery_StillElevated(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// GIVEN: Sensor at warning level (sustained)
	t.Log("GIVEN: Sensor at 58% for 30+ minutes")
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	if manager.GetCurrentState() != "warning" {
		t.Fatal("Expected warning alert active")
	}

	// WHEN: Sensor goes unavailable, then recovers still elevated
	t.Log("WHEN: Sensor goes unavailable, then recovers at 57%")
	manager.SimulateSensorUnavailable(testIndoorSensor1)
	mockClock.Advance(2 * time.Minute) // brief outage
	manager.SimulateSensorChange(testIndoorSensor1, 57.0)

	// THEN: Alert should still be warning (sensor is still above clear threshold)
	t.Log("THEN: Alert remains at warning level")
	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected alert to remain 'warning' after recovery still elevated, got '%s'", alertLevel)
	}
}

func TestEnvironmentalManager_UnavailableRecovery_BelowThreshold(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// GIVEN: Sensor at warning level (sustained)
	t.Log("GIVEN: Sensor at 58% for 30+ minutes")
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	if manager.GetCurrentState() != "warning" {
		t.Fatal("Expected warning alert active")
	}

	initialNotifications := countAlerts(mockAlerter)

	// WHEN: Sensor goes unavailable, then recovers below clear threshold
	t.Log("WHEN: Sensor goes unavailable, then recovers at 45%")
	manager.SimulateSensorUnavailable(testIndoorSensor1)
	mockClock.Advance(2 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 45.0)

	// THEN: Alert should be resolved now
	t.Log("THEN: Alert resolves and resolution notification mentions Barn Humidity")
	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alert to resolve after recovery below threshold, got '%s'", alertLevel)
	}

	// Resolution notification should have been sent
	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications <= initialNotifications {
		t.Error("Expected resolution notification to be sent")
	}

	// Resolution should mention the correct sensor
	lastNotification := getLastAlert(mockAlerter)
	if lastNotification == nil {
		t.Fatal("Expected a resolution notification")
	}
	if !strings.Contains(lastNotification.Body, "Barn Humidity") {
		t.Errorf("Expected resolution to mention 'Barn Humidity', got: %s", lastNotification.Body)
	}
}

func TestEnvironmentalManager_AllSensorsUnavailable_NoResolution(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Single indoor sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// GIVEN: Sensor at warning level (sustained)
	t.Log("GIVEN: Single sensor at 58% for 30+ minutes")
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	if manager.GetCurrentState() != "warning" {
		t.Fatal("Expected warning alert active")
	}

	initialNotifications := countAlerts(mockAlerter)

	// WHEN: The only sensor goes unavailable
	t.Log("WHEN: The only sensor goes unavailable")
	manager.SimulateSensorUnavailable(testIndoorSensor1)

	// THEN: Alert should NOT resolve (no valid sensors to confirm resolution)
	t.Log("THEN: Alert remains active, no resolution notification")
	alertLevel := manager.GetCurrentState()
	if alertLevel == "none" {
		t.Error("Expected alert to remain active when all sensors unavailable, but got 'none'")
	}

	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications > initialNotifications {
		t.Error("Expected no new notifications when sensor goes unavailable")
	}
}

func TestEnvironmentalManager_RateLimited_AlertedSensorNamesPopulated(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Barn Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// GIVEN: First alert fires and clears
	t.Log("GIVEN: First warning fires, then clears")
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0) // triggers warning + notification

	if manager.GetCurrentState() != "warning" {
		t.Fatal("Expected warning alert")
	}

	// Clear the condition
	manager.SimulateSensorChange(testIndoorSensor1, 45.0)
	if manager.GetCurrentState() != "none" {
		t.Fatal("Expected alert to clear")
	}

	// WHEN: Re-trigger within 4h rate limit window
	t.Log("WHEN: Warning re-triggers within 4h rate limit, then clears again")
	mockClock.Advance(1 * time.Hour) // still within 4h rate limit
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0) // rate-limited, but alertedSensorNames should still be populated

	if manager.GetCurrentState() != "warning" {
		t.Fatal("Expected warning alert on re-trigger")
	}

	notificationsBeforeResolution := countAlerts(mockAlerter)

	// Clear the condition again
	manager.SimulateSensorChange(testIndoorSensor1, 45.0)

	// THEN: Resolution should mention the correct sensor (not fallback to all sensors)
	t.Log("THEN: Resolution notification mentions 'Barn Humidity' specifically")
	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected alert to clear, got '%s'", alertLevel)
	}

	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications <= notificationsBeforeResolution {
		t.Error("Expected resolution notification to be sent")
		return
	}

	lastNotification := getLastAlert(mockAlerter)
	if lastNotification == nil {
		t.Fatal("Expected a notification")
	}

	// The resolution should mention the specific sensor, not be a generic fallback
	if !strings.Contains(lastNotification.Body, "Barn Humidity") {
		t.Errorf("Expected resolution to mention 'Barn Humidity' (not fallback to all sensors), got: %s",
			lastNotification.Body)
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
	mockAlerter := &alert.MockAlerter{}

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

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
	mockAlerter := &alert.MockAlerter{}

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

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

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
	mockAlerter := &alert.MockAlerter{}

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

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 1 {
		t.Errorf("Expected 1 water leak notification, got %d", notificationCount)
	}

	notification := getLastAlert(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add water leak sensor
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak",
		State:        "off",
	})

	// Trigger leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	initialNotifications := countAlerts(mockAlerter)
	if initialNotifications != 1 {
		t.Fatalf("Expected 1 initial notification, got %d", initialNotifications)
	}

	// Simulate state change event again while still leaking (same state)
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Should not have sent another notification
	finalNotifications := countAlerts(mockAlerter)
	if finalNotifications != initialNotifications {
		t.Errorf("Expected no additional notifications for same leak, got %d extra",
			finalNotifications-initialNotifications)
	}
}

func TestEnvironmentalManager_WaterLeak_Recovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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

	notificationsAfterLeak := countAlerts(mockAlerter)

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
	notificationsAfterClear := countAlerts(mockAlerter)
	if notificationsAfterClear != notificationsAfterLeak {
		t.Errorf("Expected no additional notifications after clearing, got %d",
			notificationsAfterClear-notificationsAfterLeak)
	}
}

func TestEnvironmentalManager_WaterLeak_MultipleLeaks(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications in read-only mode, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_WaterLeak_ShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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

func TestEnvironmentalManager_WaterLeak_RenotificationAfterClear(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add water leak sensor
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak",
		State:        "off",
	})

	// First leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")
	if countAlerts(mockAlerter) != 1 {
		t.Fatal("Expected first leak notification")
	}

	// Clear leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "off")

	// New leak should trigger new notification
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Should have 2 notifications total (one per leak event)
	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 2 {
		t.Errorf("Expected 2 notifications (new leak after clearing), got %d", notificationCount)
	}
}

// ============================================================================
// Unconditioned Space / Outdoor Humidity Comparison Tests
// ============================================================================

func TestEnvironmentalManager_UnconditionedSensor_SuppressedByOutdoor(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add unconditioned sensor (barn) and set outdoor humidity
	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Barn Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	manager.SetOutdoorHumidity(70.0) // Outdoor at 70%

	// Simulate barn at 61% (below outdoor 70% + 5% margin = 75%)
	// This matches the issue scenario: barn at 61%, outdoor at ~70%
	manager.SimulateSensorChange(testUnconditionedSensor1, 61.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 61.0)

	// Should NOT trigger alert (suppressed - tracking outdoor humidity)
	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected no alert for unconditioned sensor tracking outdoor humidity, got '%s'", alertLevel)
	}

	notificationCount := countAlerts(mockAlerter)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications for suppressed unconditioned sensor, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_UnconditionedSensor_AtticSuppressedByOutdoor(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add unconditioned sensor (attic) and set outdoor humidity
	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Attic High Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	manager.SetOutdoorHumidity(69.6) // From the issue

	// Simulate attic at 56% (well below outdoor)
	manager.SimulateSensorChange(testUnconditionedSensor1, 56.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 56.0)

	// Should NOT trigger alert (suppressed)
	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected no alert for attic at 56%% with outdoor at 69.6%%, got '%s'", alertLevel)
	}
}

func TestEnvironmentalManager_UnconditionedSensor_HigherThresholds(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add unconditioned sensor with outdoor humidity available
	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Barn Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	manager.SetOutdoorHumidity(50.0) // Outdoor is moderate

	// Barn at 60% - above conditioned threshold (55%) but below unconditioned (75%)
	// Also exceeds outdoor + margin (50+5=55), so not suppressed
	manager.SimulateSensorChange(testUnconditionedSensor1, 60.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 60.0)

	// Should NOT alert - below unconditioned warning threshold of 75%
	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected no alert for unconditioned sensor at 60%% (below 75%% threshold), got '%s'", alertLevel)
	}
}

func TestEnvironmentalManager_UnconditionedSensor_WarningAboveThreshold(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add unconditioned sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Barn Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	manager.SetOutdoorHumidity(50.0) // Outdoor moderate

	// Barn at 78% - exceeds outdoor + margin AND above unconditioned warning (75%)
	manager.SimulateSensorChange(testUnconditionedSensor1, 78.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 78.0)

	// Should trigger warning
	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected 'warning' for unconditioned sensor at 78%%, got '%s'", alertLevel)
	}

	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification, got %d", notificationCount)
	}
}

func TestEnvironmentalManager_UnconditionedSensor_AbsoluteCeiling(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add unconditioned sensor with HIGH outdoor humidity
	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Barn Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	manager.SetOutdoorHumidity(85.0) // Very high outdoor

	// Barn at 82% - below outdoor + margin (85+5=90), BUT >= absolute ceiling (80%)
	// Should NOT be suppressed because 82% >= UnconditionedCriticalThreshold
	manager.SimulateSensorChange(testUnconditionedSensor1, 82.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 82.0)

	// Should trigger critical (absolute ceiling)
	alertLevel := manager.GetCurrentState()
	if alertLevel != "critical" {
		t.Errorf("Expected 'critical' for unconditioned sensor at 82%% (absolute ceiling), got '%s'", alertLevel)
	}

	notification := getLastAlert(mockAlerter)
	if notification == nil {
		t.Fatal("Expected notification for absolute ceiling breach")
	}
	if notification.Title != "High Humidity Critical" {
		t.Errorf("Expected critical notification, got '%s'", notification.Title)
	}
}

func TestEnvironmentalManager_UnconditionedSensor_OutdoorUnavailable(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add unconditioned sensor WITHOUT outdoor reference
	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Barn Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	// Do NOT set outdoor humidity - simulates weather station unavailable

	// Barn at 60% - above conditioned threshold (55%) but below unconditioned (75%)
	manager.SimulateSensorChange(testUnconditionedSensor1, 60.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 60.0)

	// Should NOT alert - uses unconditioned thresholds (75%) as fallback
	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected no alert at 60%% with unconditioned thresholds (75%%), got '%s'", alertLevel)
	}

	// Now raise to 78% - above unconditioned warning threshold
	manager.SimulateSensorChange(testUnconditionedSensor1, 78.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 78.0)

	alertLevel = manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected 'warning' at 78%% with unconditioned thresholds, got '%s'", alertLevel)
	}
}

func TestEnvironmentalManager_UnconditionedSensor_SuppressionAtExactMargin(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Barn Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	manager.SetOutdoorHumidity(70.0) // Outdoor at 70%

	// At exactly outdoor + margin (70 + 5 = 75), should still be suppressed
	manager.SimulateSensorChange(testUnconditionedSensor1, 75.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 75.0)

	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected no alert at exact margin boundary (75%% with outdoor 70%%), got '%s'", alertLevel)
	}

	// Just above margin: 75.1% > 70 + 5 = 75, NOT suppressed, and >= 75 = warning
	manager.Reset()
	manager.SetOutdoorHumidity(70.0)
	manager.SimulateSensorChange(testUnconditionedSensor1, 75.1)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 75.1)

	alertLevel = manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected 'warning' just above margin (75.1%% with outdoor 70%%), got '%s'", alertLevel)
	}
}

func TestEnvironmentalManager_MixedConditionedAndUnconditioned(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add one conditioned (living room) and one unconditioned (barn)
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Living Room Humidity",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Barn Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	manager.SetOutdoorHumidity(70.0)

	// Barn at 61% (suppressed - tracking outdoor) + Living room at 58% (conditioned warning)
	manager.SimulateSensorChange(testUnconditionedSensor1, 61.0)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	// Should trigger warning from the conditioned sensor only
	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected 'warning' from conditioned sensor, got '%s'", alertLevel)
	}

	// Notification should mention the living room, not the barn
	notification := getLastAlert(mockAlerter)
	if notification == nil {
		t.Fatal("Expected notification")
	}
	if !strings.Contains(notification.Body, "Living Room") {
		t.Errorf("Expected notification to mention Living Room, got: %s", notification.Body)
	}
	if strings.Contains(notification.Body, "Barn") {
		t.Errorf("Expected notification NOT to mention suppressed Barn sensor, got: %s", notification.Body)
	}
}

func TestEnvironmentalManager_OutdoorHumidityDropTriggersReEvaluation(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add unconditioned sensor and weather station sensor
	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Barn Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	manager.AddSensor(&HumiditySensor{
		EntityID:     testWeatherStation,
		FriendlyName: "Weather Station Humidity",
		IsIndoor:     false,
		Valid:        true,
	})
	manager.SetOutdoorHumidity(80.0)

	// Barn at 78% - suppressed (78 <= 80+5)
	manager.SimulateSensorChange(testUnconditionedSensor1, 78.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 78.0)

	alertLevel := manager.GetCurrentState()
	if alertLevel != "none" {
		t.Errorf("Expected no alert while barn tracks outdoor humidity, got '%s'", alertLevel)
	}

	// Outdoor humidity drops significantly via weather station update
	// 78 > 50 + 5 = 55, and 78 >= 75 → warning should start tracking
	manager.SimulateSensorChange(testWeatherStation, 50.0)

	// Need sustained duration - advance time and re-trigger
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 78.0)

	alertLevel = manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected 'warning' after outdoor humidity dropped and barn exceeds threshold, got '%s'", alertLevel)
	}
}

func TestEnvironmentalManager_NotificationIncludesOutdoorHumidity(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.SetOutdoorHumidity(45.0)

	// Trigger a sustained warning
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	notification := getLastAlert(mockAlerter)
	if notification == nil {
		t.Fatal("Expected notification")
	}
	// Notification should include outdoor humidity context
	if !strings.Contains(notification.Body, "Outdoor: 45%") {
		t.Errorf("Expected notification to include outdoor humidity context, got: %s", notification.Body)
	}
}

func TestEnvironmentalManager_ConditionedSensorUnchangedByOutdoor(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Conditioned indoor sensor - should use original thresholds regardless of outdoor
	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})
	manager.SetOutdoorHumidity(90.0) // Very high outdoor

	// Indoor at 58% - above conditioned threshold (55%), should still alert
	// even though outdoor is 90% (conditioned spaces have HVAC)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	alertLevel := manager.GetCurrentState()
	if alertLevel != "warning" {
		t.Errorf("Expected 'warning' for conditioned sensor at 58%% even with high outdoor, got '%s'", alertLevel)
	}
}

func TestEnvironmentalManager_UnconditionedDiscovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()

	// Add device with "unconditioned" label (no "indoor" label needed)
	mockHA.AddDevice(&ha.Device{
		ID:     "device_barn",
		Name:   "Barn Sensor",
		Labels: []string{"Unconditioned"}, // Case-insensitive
	})
	// Add device with "indoor" label (conditioned)
	mockHA.AddDevice(&ha.Device{
		ID:     "device_living",
		Name:   "Living Room Sensor",
		Labels: []string{"Indoor"},
	})
	// Add outdoor device (no labels)
	mockHA.AddDevice(&ha.Device{
		ID:     "device_outdoor",
		Name:   "Weather Station",
		Labels: []string{},
	})

	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.barn_humidity",
		DeviceID: "device_barn",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.living_humidity",
		DeviceID: "device_living",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.weather_station_humidity",
		DeviceID: "device_outdoor",
	})

	mockHA.SetState("sensor.barn_humidity", "55.0", map[string]interface{}{
		"device_class":  "humidity",
		"friendly_name": "Barn Humidity",
	})
	mockHA.SetState("sensor.living_humidity", "45.0", map[string]interface{}{
		"device_class":  "humidity",
		"friendly_name": "Living Room Humidity",
	})
	mockHA.SetState("sensor.weather_station_humidity", "70.0", map[string]interface{}{
		"device_class":  "humidity",
		"friendly_name": "Weather Station Humidity",
	})

	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)
	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	sensors := manager.GetSensors()

	// Barn should be indoor AND unconditioned (label implies indoor)
	barn, ok := sensors["sensor.barn_humidity"]
	if !ok {
		t.Fatal("Barn sensor not discovered")
	}
	if !barn.IsIndoor {
		t.Error("Expected barn to be classified as indoor (unconditioned implies indoor)")
	}
	if !barn.IsUnconditioned {
		t.Error("Expected barn to be classified as unconditioned")
	}

	// Living room should be indoor but NOT unconditioned
	living, ok := sensors["sensor.living_humidity"]
	if !ok {
		t.Fatal("Living room sensor not discovered")
	}
	if !living.IsIndoor {
		t.Error("Expected living room to be classified as indoor")
	}
	if living.IsUnconditioned {
		t.Error("Expected living room NOT to be classified as unconditioned")
	}

	// Weather station should be neither indoor nor unconditioned
	weather, ok := sensors["sensor.weather_station_humidity"]
	if !ok {
		t.Fatal("Weather station not discovered")
	}
	if weather.IsIndoor {
		t.Error("Expected weather station NOT to be classified as indoor")
	}
	if weather.IsUnconditioned {
		t.Error("Expected weather station NOT to be classified as unconditioned")
	}
}

func TestEnvironmentalManager_UnconditionedHysteresis(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:        testUnconditionedSensor1,
		FriendlyName:    "Barn Humidity",
		IsIndoor:        true,
		IsUnconditioned: true,
		Valid:           true,
	})
	manager.SetOutdoorHumidity(40.0) // Low outdoor

	// Trigger warning at 78%
	manager.SimulateSensorChange(testUnconditionedSensor1, 78.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testUnconditionedSensor1, 78.0)

	if manager.GetCurrentState() != "warning" {
		t.Fatal("Expected warning at 78%")
	}

	// Drop to 72% - above unconditioned clear threshold (70%), should remain warning
	manager.SimulateSensorChange(testUnconditionedSensor1, 72.0)
	if manager.GetCurrentState() != "warning" {
		t.Error("Expected warning to persist at 72% (above unconditioned clear threshold of 70%)")
	}

	// Drop to 69% - below unconditioned clear threshold (70%), should resolve
	manager.SimulateSensorChange(testUnconditionedSensor1, 69.0)
	if manager.GetCurrentState() != "none" {
		t.Error("Expected alert to resolve at 69% (below unconditioned clear threshold of 70%)")
	}
}

func TestCleanSensorName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		// Real cases observed in production where HA appends entity name to a
		// device name that already ends in the same noun.
		{"Guest Bedroom Temperature and Humidity Humidity", "Guest Bedroom Temperature and Humidity"},
		{"Caroline Office Temperature and Humidity Humidity", "Caroline Office Temperature and Humidity"},
		{"Living Room Temperature Temperature", "Living Room Temperature"},

		// Should NOT dedupe — last token isn't a sensor noun, or no duplication.
		{"Indoor Humidity 1", "Indoor Humidity 1"},
		{"Bedroom 2 Bedroom", "Bedroom 2 Bedroom"},
		{"Barn Humidity", "Barn Humidity"},
		{"Humidity", "Humidity"},
		{"", ""},
	}

	for _, c := range cases {
		got := cleanSensorName(c.in)
		if got != c.want {
			t.Errorf("cleanSensorName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEnvironmentalManager_HumidityAlert_HasSpeechWithoutSensorList(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	manager.AddSensor(&HumiditySensor{
		EntityID:     testIndoorSensor1,
		FriendlyName: "Indoor Humidity 1",
		IsIndoor:     true,
		Valid:        true,
	})

	// Sustain warning for 30+ minutes to fire alert
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)
	mockClock.Advance(31 * time.Minute)
	manager.SimulateSensorChange(testIndoorSensor1, 58.0)

	notification := getLastAlert(mockAlerter)
	if notification == nil {
		t.Fatal("Expected an alert to be sent")
	}

	// Speech variant must exist and read as a short sentence — no sensor list
	if notification.Speech == "" {
		t.Fatal("Expected non-empty Speech for TTS-friendly variant")
	}
	if strings.Contains(notification.Speech, "Indoor Humidity 1") {
		t.Errorf("Speech should not contain sensor friendly_name, got %q", notification.Speech)
	}
	if !strings.Contains(notification.Speech, "percent") {
		t.Errorf("Speech should spell out 'percent' for TTS, got %q", notification.Speech)
	}
	if !strings.Contains(notification.Speech, "Check ventilation") {
		t.Errorf("Speech should retain action guidance, got %q", notification.Speech)
	}

	// Detailed Body still contains the sensor breakdown for ntfy push
	if !strings.Contains(notification.Body, "Indoor Humidity 1") {
		t.Errorf("Body should retain sensor detail for push notification, got %q", notification.Body)
	}
}
