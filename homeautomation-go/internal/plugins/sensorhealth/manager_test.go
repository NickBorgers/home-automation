package sensorhealth

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
	testBatterySensor1 = "sensor.battery_sensor_1"
	testBatterySensor2 = "sensor.battery_sensor_2"
	testTempSensor1    = "sensor.temp_sensor_1"
	testTempSensor2    = "sensor.temp_sensor_2"
)

// setupMockEnvironment creates a mock HA client with test devices and entity registry
func setupMockEnvironment(mockHA *ha.MockClient) {
	// Add devices
	mockHA.AddDevice(&ha.Device{
		ID:     "device_battery_1",
		Name:   "Battery Device 1",
		Labels: []string{},
	})
	mockHA.AddDevice(&ha.Device{
		ID:     "device_battery_2",
		Name:   "Battery Device 2",
		Labels: []string{},
	})
	mockHA.AddDevice(&ha.Device{
		ID:     "device_temp_1",
		Name:   "Temperature Device 1",
		Labels: []string{},
	})
	mockHA.AddDevice(&ha.Device{
		ID:     "device_temp_2",
		Name:   "Temperature Device 2",
		Labels: []string{},
	})

	// Add entity registry entries linking entities to devices
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testBatterySensor1,
		DeviceID: "device_battery_1",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testBatterySensor2,
		DeviceID: "device_battery_2",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testTempSensor1,
		DeviceID: "device_temp_1",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testTempSensor2,
		DeviceID: "device_temp_2",
	})

	// Add initial sensor states
	mockHA.SetState(testBatterySensor1, "85", map[string]interface{}{
		"device_class":        "battery",
		"friendly_name":       "Battery Sensor 1",
		"unit_of_measurement": "%",
	})
	mockHA.SetState(testBatterySensor2, "15", map[string]interface{}{
		"device_class":        "battery",
		"friendly_name":       "Battery Sensor 2",
		"unit_of_measurement": "%",
	})
	mockHA.SetState(testTempSensor1, "72.5", map[string]interface{}{
		"device_class":  "temperature",
		"friendly_name": "Temperature Sensor 1",
	})
	mockHA.SetState(testTempSensor2, "68.0", map[string]interface{}{
		"device_class":  "temperature",
		"friendly_name": "Temperature Sensor 2",
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

func TestSensorHealthManager_BatterySensor_Discovery(t *testing.T) {
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

	// Verify battery sensors were discovered
	sensors := manager.GetBatterySensors()
	if len(sensors) != 2 {
		t.Errorf("Expected 2 battery sensors, got %d", len(sensors))
	}

	// Verify low battery detection
	if sensor, ok := sensors[testBatterySensor2]; ok {
		if !sensor.IsLow {
			t.Error("Expected battery sensor 2 to be marked as low (15%)")
		}
	} else {
		t.Error("Battery sensor 2 not found")
	}
}

func TestSensorHealthManager_TemperatureSensor_Discovery(t *testing.T) {
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

	// Verify temperature sensors were discovered
	sensors := manager.GetTemperatureSensors()
	if len(sensors) != 2 {
		t.Errorf("Expected 2 temperature sensors, got %d", len(sensors))
	}
}

func TestSensorHealthManager_LowBattery_Notification(t *testing.T) {
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

	// Verify a notification was sent for the low battery
	notificationCount := countNtfyNotifications(mockNtfy)
	if notificationCount != 1 {
		t.Errorf("Expected 1 low battery notification, got %d", notificationCount)
	}
}

func TestSensorHealthManager_TemperatureLockup_ReadOnlyMode(t *testing.T) {
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

func TestSensorHealthManager_ShadowState(t *testing.T) {
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

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify shadow state plugin name
	if shadowState.Plugin != "sensorhealth" {
		t.Errorf("Expected plugin 'sensorhealth', got '%s'", shadowState.Plugin)
	}

	// Verify shadow state has battery sensors
	if len(shadowState.Outputs.BatterySensors) != 2 {
		t.Errorf("Expected 2 battery sensors in shadow state, got %d",
			len(shadowState.Outputs.BatterySensors))
	}

	// Verify shadow state has temperature sensors
	if len(shadowState.Outputs.TemperatureSensors) != 2 {
		t.Errorf("Expected 2 temperature sensors in shadow state, got %d",
			len(shadowState.Outputs.TemperatureSensors))
	}
}

func TestSensorHealthManager_TemperatureLockup_NoLockup(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockNtfy, mockClock)

	// Add test temperature sensor manually
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
}

func TestSensorHealthManager_TemperatureLockup_Detected(t *testing.T) {
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
}

func TestSensorHealthManager_TemperatureLockup_Recovery(t *testing.T) {
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
}

func TestSensorHealthManager_MonitoringIgnoreLabel(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()

	// Add device with monitoring_ignore label
	mockHA.AddDevice(&ha.Device{
		ID:     "device_ignored",
		Name:   "Ignored Device",
		Labels: []string{"monitoring_ignore"},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.ignored_temp",
		DeviceID: "device_ignored",
	})
	mockHA.SetState("sensor.ignored_temp", "72.0", map[string]interface{}{
		"device_class":  "temperature",
		"friendly_name": "Ignored Temperature",
	})

	// Add device without the label
	mockHA.AddDevice(&ha.Device{
		ID:     "device_monitored",
		Name:   "Monitored Device",
		Labels: []string{},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.monitored_temp",
		DeviceID: "device_monitored",
	})
	mockHA.SetState("sensor.monitored_temp", "72.0", map[string]interface{}{
		"device_class":  "temperature",
		"friendly_name": "Monitored Temperature",
	})

	mockNtfy := ntfy.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockNtfy)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify only non-ignored sensor was discovered
	sensors := manager.GetTemperatureSensors()
	if len(sensors) != 1 {
		t.Errorf("Expected 1 temperature sensor (ignored one filtered), got %d", len(sensors))
	}
	if _, ok := sensors["sensor.ignored_temp"]; ok {
		t.Error("Expected ignored temperature sensor to be filtered out")
	}
	if _, ok := sensors["sensor.monitored_temp"]; !ok {
		t.Error("Expected monitored temperature sensor to be discovered")
	}
}

func TestSensorHealthManager_Reset(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	setupMockEnvironment(mockHA)
	mockNtfy := ntfy.NewMockClient()

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockNtfy)

	// Start
	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify sensors discovered
	if len(manager.GetBatterySensors()) == 0 {
		t.Fatal("Expected battery sensors to be discovered")
	}

	// Reset should clear notification states
	err = manager.Reset()
	if err != nil {
		t.Fatalf("Failed to reset manager: %v", err)
	}

	// Verify sensors still exist after reset
	if len(manager.GetBatterySensors()) == 0 {
		t.Error("Expected battery sensors to still exist after reset")
	}
}
