package sensorhealth

import (
	"strings"
	"testing"
	"time"

	"homeautomation/internal/alert"
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

func countAlerts(mockAlerter *alert.MockAlerter) int {
	return len(mockAlerter.Calls())
}

func getLastAlert(mockAlerter *alert.MockAlerter) *alert.Alert {
	calls := mockAlerter.Calls()
	if len(calls) == 0 {
		return nil
	}
	last := calls[len(calls)-1]
	return &last
}

func TestSensorHealthManager_BatterySensor_Discovery(t *testing.T) {
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

	// Verify a notification was sent for the low battery
	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 1 {
		t.Errorf("Expected 1 low battery notification, got %d", notificationCount)
	}
}

func TestSensorHealthManager_TemperatureLockup_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications in read-only mode, got %d", notificationCount)
	}
}

func TestSensorHealthManager_ShadowState(t *testing.T) {
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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 1 {
		t.Errorf("Expected 1 lockup notification, got %d", notificationCount)
		return
	}

	notification := getLastAlert(mockAlerter)
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
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

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
	lockupNotifications := countAlerts(mockAlerter)
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
	totalNotifications := countAlerts(mockAlerter)
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

	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

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
	mockAlerter := &alert.MockAlerter{}

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

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

// TestHandleBatteryChange tests the battery state change handler
func TestHandleBatteryChange(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, logger, false, nil, nil)

	// Add a battery sensor
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_1",
		FriendlyName: "Motion Sensor Battery",
	})

	// Simulate battery change using the SimulateBatteryChange helper
	manager.SimulateBatteryChange("sensor.battery_1", 85.0)

	// Verify sensor was updated
	sensors := manager.GetBatterySensors()
	if len(sensors) != 1 {
		t.Fatalf("Expected 1 sensor, got %d", len(sensors))
	}
	sensor := sensors["sensor.battery_1"]
	if sensor.BatteryLevel != 85.0 {
		t.Errorf("Expected battery level 85.0, got %f", sensor.BatteryLevel)
	}
	if sensor.IsLow {
		t.Error("Expected IsLow to be false at 85%")
	}
}

// TestHandleBatteryChangeLowBattery tests that low battery triggers notification
func TestHandleBatteryChangeLowBattery(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, logger, false, nil, nil)

	// Add a battery sensor
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_1",
		FriendlyName: "Motion Sensor Battery",
	})

	// Simulate low battery change
	manager.SimulateBatteryChange("sensor.battery_1", 15.0)

	// Verify sensor is marked as low
	sensors := manager.GetBatterySensors()
	if len(sensors) != 1 {
		t.Fatalf("Expected 1 sensor, got %d", len(sensors))
	}
	sensor := sensors["sensor.battery_1"]
	if sensor.BatteryLevel != 15.0 {
		t.Errorf("Expected battery level 15.0, got %f", sensor.BatteryLevel)
	}
	if !sensor.IsLow {
		t.Error("Expected IsLow to be true at 15%")
	}

	// Check low battery count
	lowCount := manager.GetLowBatteryCount()
	if lowCount != 1 {
		t.Errorf("Expected 1 low battery, got %d", lowCount)
	}
}

// TestHandleBatteryChangeBatteryRecovery tests battery recovery from low state
func TestHandleBatteryChangeBatteryRecovery(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, logger, false, nil, nil)

	// Add a battery sensor
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_1",
		FriendlyName: "Motion Sensor Battery",
	})

	// First, set to low battery
	manager.SimulateBatteryChange("sensor.battery_1", 15.0)

	// Verify low state
	sensors := manager.GetBatterySensors()
	sensor := sensors["sensor.battery_1"]
	if !sensor.IsLow {
		t.Error("Expected IsLow to be true at 15%")
	}

	// Now battery recovers
	manager.SimulateBatteryChange("sensor.battery_1", 100.0)

	// Verify recovery
	sensors = manager.GetBatterySensors()
	sensor = sensors["sensor.battery_1"]
	if sensor.IsLow {
		t.Error("Expected IsLow to be false at 100%")
	}
	if sensor.BatteryLevel != 100.0 {
		t.Errorf("Expected battery level 100.0, got %f", sensor.BatteryLevel)
	}
}

// TestHandleBatteryChangeUnknownSensor tests handling of unknown sensor updates
func TestHandleBatteryChangeUnknownSensor(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, logger, false, nil, nil)

	// Try to update unknown sensor - should not panic
	manager.SimulateBatteryChange("sensor.unknown", 50.0)

	// Verify no sensors exist
	sensors := manager.GetBatterySensors()
	if len(sensors) != 0 {
		t.Errorf("Expected 0 sensors, got %d", len(sensors))
	}
}

// TestGetLowBatteryCount tests the low battery count getter
func TestGetLowBatteryCount(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, logger, false, nil, nil)

	// Initially no low batteries
	if manager.GetLowBatteryCount() != 0 {
		t.Error("Expected 0 low batteries initially")
	}

	// Add some sensors with varying battery levels
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_1",
		FriendlyName: "Sensor 1",
	})
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_2",
		FriendlyName: "Sensor 2",
	})
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_3",
		FriendlyName: "Sensor 3",
	})

	// Set battery levels - 2 low, 1 normal
	manager.SimulateBatteryChange("sensor.battery_1", 10.0) // Low
	manager.SimulateBatteryChange("sensor.battery_2", 85.0) // Normal
	manager.SimulateBatteryChange("sensor.battery_3", 5.0)  // Low

	// Verify count
	lowCount := manager.GetLowBatteryCount()
	if lowCount != 2 {
		t.Errorf("Expected 2 low batteries, got %d", lowCount)
	}
}

// TestAddBatterySensor tests adding battery sensors
func TestAddBatterySensor(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, logger, false, nil, nil)

	// Add sensors
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_1",
		FriendlyName: "Sensor One",
	})
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_2",
		FriendlyName: "Sensor Two",
	})

	sensors := manager.GetBatterySensors()
	if len(sensors) != 2 {
		t.Fatalf("Expected 2 sensors, got %d", len(sensors))
	}

	// Verify sensor names
	foundOne, foundTwo := false, false
	for _, s := range sensors {
		if s.FriendlyName == "Sensor One" {
			foundOne = true
		}
		if s.FriendlyName == "Sensor Two" {
			foundTwo = true
		}
	}
	if !foundOne || !foundTwo {
		t.Error("Expected to find both sensors by friendly name")
	}
}

// TestShadowStateUpdatesOnBatteryChange tests that shadow state is updated when battery changes
func TestShadowStateUpdatesOnBatteryChange(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, logger, false, nil, nil)

	// Add a battery sensor
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_1",
		FriendlyName: "Motion Sensor",
	})

	// First battery change to populate shadow state
	manager.SimulateBatteryChange("sensor.battery_1", 90.0)

	// Verify shadow state has sensor with initial battery level
	initialState := manager.GetShadowState()
	if len(initialState.Outputs.BatterySensors) != 1 {
		t.Fatalf("Expected 1 battery sensor in shadow state, got %d", len(initialState.Outputs.BatterySensors))
	}

	// Simulate another battery change
	manager.SimulateBatteryChange("sensor.battery_1", 25.0)

	// Check shadow state updated
	shadowState := manager.GetShadowState()
	if len(shadowState.Outputs.BatterySensors) != 1 {
		t.Fatalf("Expected 1 battery sensor in shadow state, got %d", len(shadowState.Outputs.BatterySensors))
	}

	found := false
	for _, s := range shadowState.Outputs.BatterySensors {
		if s.EntityID == "sensor.battery_1" && s.BatteryLevel == 25.0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected battery sensor with updated level in shadow state")
	}
}

// TestLowBatteryAlertsInShadowState tests that low battery alerts are tracked in shadow state
func TestLowBatteryAlertsInShadowState(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	manager := NewManager(mockClient, stateManager, logger, false, nil, nil)

	// Add a battery sensor
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     "sensor.battery_1",
		FriendlyName: "Door Sensor",
	})

	// Set to low battery
	manager.SimulateBatteryChange("sensor.battery_1", 10.0)

	// Check shadow state has low battery alert
	shadowState := manager.GetShadowState()
	if len(shadowState.Outputs.LowBatteryAlerts) != 1 {
		t.Fatalf("Expected 1 low battery alert in shadow state, got %d", len(shadowState.Outputs.LowBatteryAlerts))
	}

	alert := shadowState.Outputs.LowBatteryAlerts[0]
	if alert.EntityID != "sensor.battery_1" {
		t.Errorf("Expected alert for sensor.battery_1, got %s", alert.EntityID)
	}
	if alert.BatteryLevel != 10.0 {
		t.Errorf("Expected battery level 10.0 in alert, got %f", alert.BatteryLevel)
	}
}

// ============================================================================
// Node Status Monitoring Tests
// ============================================================================

const (
	testNodeStatus1 = "sensor.device_1_node_status"
	testNodeStatus2 = "sensor.device_2_node_status"
)

func TestSensorHealthManager_NodeStatus_Discovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}

	// Add node status sensors
	mockHA.AddDevice(&ha.Device{
		ID:     "device_zwave_1",
		Name:   "Z-Wave Device 1",
		Labels: []string{},
	})
	mockHA.AddDevice(&ha.Device{
		ID:     "device_zwave_2",
		Name:   "Z-Wave Device 2",
		Labels: []string{},
	})

	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testNodeStatus1,
		DeviceID: "device_zwave_1",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testNodeStatus2,
		DeviceID: "device_zwave_2",
	})

	mockHA.SetState(testNodeStatus1, "alive", map[string]interface{}{
		"friendly_name": "Z-Wave Device 1 Node Status",
	})
	mockHA.SetState(testNodeStatus2, "asleep", map[string]interface{}{
		"friendly_name": "Z-Wave Device 2 Node Status",
	})

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify node status sensors were discovered
	nodeStatuses := manager.GetNodeStatuses()
	if len(nodeStatuses) != 2 {
		t.Errorf("Expected 2 node status sensors, got %d", len(nodeStatuses))
	}
}

func TestSensorHealthManager_NodeStatus_DeadDevice_Notification(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add a node status sensor manually
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   testNodeStatus1,
		DeviceID:   "device_zwave_1",
		DeviceName: "Front Door Lock",
		Status:     "alive",
	})

	// Simulate the device becoming dead
	manager.SimulateNodeStatusChange(testNodeStatus1, "dead")

	// Verify the device is marked as dead
	nodeStatuses := manager.GetNodeStatuses()
	if nodeStatuses[testNodeStatus1].Status != "dead" {
		t.Errorf("Expected status 'dead', got '%s'", nodeStatuses[testNodeStatus1].Status)
	}

	// Notification should NOT be sent yet (debounce timer is pending)
	if countAlerts(mockAlerter) != 0 {
		t.Errorf("Expected 0 notifications before debounce expires, got %d", countAlerts(mockAlerter))
	}

	// Advance clock past the debounce delay to fire the timer
	mockClock.Advance(NodeDeadDebounceDelay)

	// Verify notification was sent after debounce
	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 1 {
		t.Errorf("Expected 1 dead device notification, got %d", notificationCount)
		return
	}

	notification := getLastAlert(mockAlerter)
	if notification == nil {
		t.Error("Expected to find a notification")
		return
	}
	if notification.Title != "Device Offline" {
		t.Errorf("Expected notification title 'Device Offline', got '%s'", notification.Title)
	}
	if notification.Priority != ntfy.PriorityHigh {
		t.Errorf("Expected priority high, got %d", notification.Priority)
	}
}

func TestSensorHealthManager_NodeStatus_DeviceRecovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add a node status sensor that's already dead with notification sent
	manager.AddNodeStatus(&NodeStatus{
		EntityID:         testNodeStatus1,
		DeviceID:         "device_zwave_1",
		DeviceName:       "Front Door Lock",
		Status:           "dead",
		NotificationSent: true,
	})

	// Simulate the device recovering
	manager.SimulateNodeStatusChange(testNodeStatus1, "alive")

	// Verify the device is marked as alive
	nodeStatuses := manager.GetNodeStatuses()
	if nodeStatuses[testNodeStatus1].Status != "alive" {
		t.Errorf("Expected status 'alive', got '%s'", nodeStatuses[testNodeStatus1].Status)
	}

	// Verify recovery notification was sent
	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 1 {
		t.Errorf("Expected 1 recovery notification, got %d", notificationCount)
		return
	}

	notification := getLastAlert(mockAlerter)
	if notification == nil {
		t.Error("Expected to find a notification")
		return
	}
	if notification.Title != "Device Online" {
		t.Errorf("Expected notification title 'Device Online', got '%s'", notification.Title)
	}
}

func TestSensorHealthManager_NodeStatus_AsleepDoesNotTriggerAlert(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

	// Add a node status sensor that's alive
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   testNodeStatus1,
		DeviceID:   "device_zwave_1",
		DeviceName: "Motion Sensor",
		Status:     "alive",
	})

	// Simulate the device going to sleep (normal battery device behavior)
	manager.SimulateNodeStatusChange(testNodeStatus1, "asleep")

	// Verify the device is marked as asleep
	nodeStatuses := manager.GetNodeStatuses()
	if nodeStatuses[testNodeStatus1].Status != "asleep" {
		t.Errorf("Expected status 'asleep', got '%s'", nodeStatuses[testNodeStatus1].Status)
	}

	// Verify NO notification was sent (asleep is normal for battery devices)
	notificationCount := countAlerts(mockAlerter)
	if notificationCount != 0 {
		t.Errorf("Expected no notifications for asleep status, got %d", notificationCount)
	}

	// Verify the device is not counted as dead
	deadCount := manager.GetDeadDeviceCount()
	if deadCount != 0 {
		t.Errorf("Expected 0 dead devices, got %d", deadCount)
	}
}

func TestSensorHealthManager_NodeStatus_MonitoringIgnoreLabel(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()

	// Add device with monitoring_ignore label
	mockHA.AddDevice(&ha.Device{
		ID:     "device_ignored",
		Name:   "Ignored Device",
		Labels: []string{"monitoring_ignore"},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.ignored_device_node_status",
		DeviceID: "device_ignored",
	})
	mockHA.SetState("sensor.ignored_device_node_status", "alive", map[string]interface{}{
		"friendly_name": "Ignored Device Node Status",
	})

	// Add device without the label
	mockHA.AddDevice(&ha.Device{
		ID:     "device_monitored",
		Name:   "Monitored Device",
		Labels: []string{},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.monitored_device_node_status",
		DeviceID: "device_monitored",
	})
	mockHA.SetState("sensor.monitored_device_node_status", "alive", map[string]interface{}{
		"friendly_name": "Monitored Device Node Status",
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

	// Verify only non-ignored sensor was discovered
	nodeStatuses := manager.GetNodeStatuses()
	if len(nodeStatuses) != 1 {
		t.Errorf("Expected 1 node status sensor (ignored one filtered), got %d", len(nodeStatuses))
	}
	if _, ok := nodeStatuses["sensor.ignored_device_node_status"]; ok {
		t.Error("Expected ignored node status sensor to be filtered out")
	}
	if _, ok := nodeStatuses["sensor.monitored_device_node_status"]; !ok {
		t.Error("Expected monitored node status sensor to be discovered")
	}
}

func TestSensorHealthManager_NodeStatus_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, true) // read-only
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockAlerter, mockClock)

	// Add a node status sensor
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   testNodeStatus1,
		DeviceID:   "device_zwave_1",
		DeviceName: "Front Door Lock",
		Status:     "alive",
	})

	// Simulate device becoming dead
	manager.SimulateNodeStatusChange(testNodeStatus1, "dead")

	// Advance clock past debounce delay to fire the timer
	mockClock.Advance(NodeDeadDebounceDelay)

	// Verify device is marked as dead
	nodeStatuses := manager.GetNodeStatuses()
	if nodeStatuses[testNodeStatus1].Status != "dead" {
		t.Errorf("Expected status 'dead', got '%s'", nodeStatuses[testNodeStatus1].Status)
	}

	// But no actual notifications should be sent (read-only mode)
	notificationCount := countAlerts(mockAlerter)
	if notificationCount > 0 {
		t.Errorf("Expected no notifications in read-only mode, got %d", notificationCount)
	}
}

func TestSensorHealthManager_NodeStatus_ShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

	// Add node status sensors
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   testNodeStatus1,
		DeviceID:   "device_zwave_1",
		DeviceName: "Front Door Lock",
		Status:     "alive",
	})
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   testNodeStatus2,
		DeviceID:   "device_zwave_2",
		DeviceName: "Motion Sensor",
		Status:     "dead",
	})

	// Update shadow state
	manager.SimulateNodeStatusChange(testNodeStatus1, "alive")
	manager.SimulateNodeStatusChange(testNodeStatus2, "dead")

	// Get shadow state
	shadowState := manager.GetShadowState()

	// Verify node statuses in shadow state
	if len(shadowState.Outputs.NodeStatuses) != 2 {
		t.Errorf("Expected 2 node statuses in shadow state, got %d",
			len(shadowState.Outputs.NodeStatuses))
	}

	// Verify dead device alerts
	if len(shadowState.Outputs.DeadDeviceAlerts) != 1 {
		t.Errorf("Expected 1 dead device alert in shadow state, got %d",
			len(shadowState.Outputs.DeadDeviceAlerts))
	}
}

func TestGetDeadDeviceCount(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

	// Initially no dead devices
	if manager.GetDeadDeviceCount() != 0 {
		t.Error("Expected 0 dead devices initially")
	}

	// Add some node statuses with varying states
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   "sensor.device_1_node_status",
		DeviceName: "Device 1",
		Status:     "alive",
	})
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   "sensor.device_2_node_status",
		DeviceName: "Device 2",
		Status:     "dead",
	})
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   "sensor.device_3_node_status",
		DeviceName: "Device 3",
		Status:     "asleep",
	})
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   "sensor.device_4_node_status",
		DeviceName: "Device 4",
		Status:     "dead",
	})

	// Verify count
	deadCount := manager.GetDeadDeviceCount()
	if deadCount != 2 {
		t.Errorf("Expected 2 dead devices, got %d", deadCount)
	}
}

func TestSensorHealthManager_NotifyAlreadyDeadDevices_AtStartup(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

	// Add devices - some dead, some alive
	manager.AddNodeStatus(&NodeStatus{
		EntityID:         "sensor.device_1_node_status",
		DeviceName:       "Device 1",
		Status:           "dead",
		NotificationSent: false,
	})
	manager.AddNodeStatus(&NodeStatus{
		EntityID:         "sensor.device_2_node_status",
		DeviceName:       "Device 2",
		Status:           "alive",
		NotificationSent: false,
	})
	manager.AddNodeStatus(&NodeStatus{
		EntityID:         "sensor.device_3_node_status",
		DeviceName:       "Device 3",
		Status:           "dead",
		NotificationSent: false,
	})

	// Call the startup notification function
	manager.notifyAlreadyDeadDevices()

	// Verify notifications were sent for dead devices
	calls := mockAlerter.Calls()
	if len(calls) != 2 {
		t.Errorf("Expected 2 startup notifications for dead devices, got %d", len(calls))
	}

	// Verify NotificationSent flags were set
	nodeStatuses := manager.GetNodeStatuses()
	if !nodeStatuses["sensor.device_1_node_status"].NotificationSent {
		t.Error("Expected NotificationSent to be true for device 1")
	}
	if nodeStatuses["sensor.device_2_node_status"].NotificationSent {
		t.Error("Expected NotificationSent to be false for device 2 (alive)")
	}
	if !nodeStatuses["sensor.device_3_node_status"].NotificationSent {
		t.Error("Expected NotificationSent to be true for device 3")
	}

	// Verify notification messages contain "startup" context
	for _, msg := range calls {
		if msg.Title != "Device Offline (Startup Check)" {
			t.Errorf("Expected title 'Device Offline (Startup Check)', got '%s'", msg.Title)
		}
		if !strings.Contains(msg.Body, "was already dead when system started") {
			t.Errorf("Expected message to mention startup context, got: %s", msg.Body)
		}
	}
}

func TestSensorHealthManager_NotifyAlreadyDeadDevices_SkipsAlreadyNotified(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

	// Add a dead device that was already notified (simulating a previous run)
	manager.AddNodeStatus(&NodeStatus{
		EntityID:         "sensor.device_1_node_status",
		DeviceName:       "Device 1",
		Status:           "dead",
		NotificationSent: true, // Already notified
	})
	// Add a dead device that hasn't been notified
	manager.AddNodeStatus(&NodeStatus{
		EntityID:         "sensor.device_2_node_status",
		DeviceName:       "Device 2",
		Status:           "dead",
		NotificationSent: false,
	})

	// Call the startup notification function
	manager.notifyAlreadyDeadDevices()

	// Verify only one notification was sent (for the un-notified device)
	calls := mockAlerter.Calls()
	if len(calls) != 1 {
		t.Errorf("Expected 1 startup notification (skipping already-notified), got %d", len(calls))
	}
}

func TestSensorHealthManager_NodeStatus_UsesNameByUserOverDefaultName(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}

	// Add device with both Name (product name) and NameByUser (user-assigned name)
	// This simulates a Z-Wave device like "Wave Plug US" renamed to "Humidifier Power Control"
	mockHA.AddDevice(&ha.Device{
		ID:         "device_zwave_renamed",
		Name:       "Wave Plug US",             // Default Z-Wave product name
		NameByUser: "Humidifier Power Control", // User-assigned name in HA
		Labels:     []string{},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.humidifier_power_control_node_status",
		DeviceID: "device_zwave_renamed",
	})
	mockHA.SetState("sensor.humidifier_power_control_node_status", "alive", map[string]interface{}{
		"friendly_name": "Humidifier Power Control Node Status",
	})

	// Add a device with only Name (no user customization)
	mockHA.AddDevice(&ha.Device{
		ID:         "device_zwave_default",
		Name:       "Z-Wave Thermostat",
		NameByUser: "", // No user-assigned name
		Labels:     []string{},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.thermostat_node_status",
		DeviceID: "device_zwave_default",
	})
	mockHA.SetState("sensor.thermostat_node_status", "alive", map[string]interface{}{
		"friendly_name": "Z-Wave Thermostat Node Status",
	})

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil, mockAlerter)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify node status sensors were discovered
	nodeStatuses := manager.GetNodeStatuses()
	if len(nodeStatuses) != 2 {
		t.Errorf("Expected 2 node status sensors, got %d", len(nodeStatuses))
	}

	// Verify that the renamed device uses NameByUser
	renamedNode := nodeStatuses["sensor.humidifier_power_control_node_status"]
	if renamedNode == nil {
		t.Fatal("Expected to find humidifier power control node status")
	}
	if renamedNode.DeviceName != "Humidifier Power Control" {
		t.Errorf("Expected device name 'Humidifier Power Control' (NameByUser), got '%s'", renamedNode.DeviceName)
	}

	// Verify that the non-renamed device uses Name
	defaultNode := nodeStatuses["sensor.thermostat_node_status"]
	if defaultNode == nil {
		t.Fatal("Expected to find thermostat node status")
	}
	if defaultNode.DeviceName != "Z-Wave Thermostat" {
		t.Errorf("Expected device name 'Z-Wave Thermostat' (Name fallback), got '%s'", defaultNode.DeviceName)
	}
}

func TestSensorHealthManager_NodeStatus_DeadDeviceNotification_UsesUserFriendlyName(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}

	// Add device with both Name (product name) and NameByUser (user-assigned name)
	mockHA.AddDevice(&ha.Device{
		ID:         "device_zwave_renamed",
		Name:       "Wave Plug US",
		NameByUser: "Humidifier Power Control",
		Labels:     []string{},
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: "sensor.humidifier_power_control_node_status",
		DeviceID: "device_zwave_renamed",
	})
	mockHA.SetState("sensor.humidifier_power_control_node_status", "alive", map[string]interface{}{
		"friendly_name": "Humidifier Power Control Node Status",
	})

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Simulate the device becoming dead
	manager.SimulateNodeStatusChange("sensor.humidifier_power_control_node_status", "dead")

	// Advance clock past debounce delay to fire the timer
	mockClock.Advance(NodeDeadDebounceDelay)

	// Verify notification was sent with user-friendly name
	calls := mockAlerter.Calls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 notification, got %d", len(calls))
	}

	notification := calls[0]

	// The notification should use the user-assigned name, NOT the Z-Wave product name
	if !strings.Contains(notification.Body, "Humidifier Power Control") {
		t.Errorf("Expected notification to contain user-friendly name 'Humidifier Power Control', got: %s", notification.Body)
	}
	if strings.Contains(notification.Body, "Wave Plug US") {
		t.Errorf("Notification should NOT contain Z-Wave product name 'Wave Plug US', got: %s", notification.Body)
	}
}

// ============================================================================
// Dead Device Debounce Tests
// ============================================================================

func TestSensorHealthManager_NodeStatus_DeadDevice_DebounceSuppress(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add a node status sensor
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   testNodeStatus1,
		DeviceID:   "device_zwave_1",
		DeviceName: "Front Door Lock",
		Status:     "alive",
	})

	// Device goes dead (starts debounce timer)
	manager.SimulateNodeStatusChange(testNodeStatus1, "dead")

	// No notification yet — timer is pending
	if countAlerts(mockAlerter) != 0 {
		t.Errorf("Expected 0 notifications before debounce, got %d", countAlerts(mockAlerter))
	}

	// Device recovers before the debounce timer fires (e.g., after 2 minutes)
	mockClock.Advance(2 * time.Minute)
	manager.SimulateNodeStatusChange(testNodeStatus1, "alive")

	// Now advance past the original debounce deadline — timer was cancelled, should not fire
	mockClock.Advance(NodeDeadDebounceDelay)

	// Zero notifications: the transient dead status was suppressed
	if countAlerts(mockAlerter) != 0 {
		t.Errorf("Expected 0 notifications (transient dead suppressed), got %d", countAlerts(mockAlerter))
	}

	// Verify device is back to alive
	nodeStatuses := manager.GetNodeStatuses()
	if nodeStatuses[testNodeStatus1].Status != "alive" {
		t.Errorf("Expected status 'alive', got '%s'", nodeStatuses[testNodeStatus1].Status)
	}
	if nodeStatuses[testNodeStatus1].NotificationSent {
		t.Error("Expected NotificationSent to be false (no notification was ever sent)")
	}
}

func TestSensorHealthManager_NodeStatus_DeadDevice_DebounceExpires(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add a node status sensor
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   testNodeStatus1,
		DeviceID:   "device_zwave_1",
		DeviceName: "Front Door Lock",
		Status:     "alive",
	})

	// Device goes dead (starts debounce timer)
	manager.SimulateNodeStatusChange(testNodeStatus1, "dead")

	// No notification yet
	if countAlerts(mockAlerter) != 0 {
		t.Errorf("Expected 0 notifications before debounce, got %d", countAlerts(mockAlerter))
	}

	// Debounce timer fires after 5 minutes — device is still dead
	mockClock.Advance(NodeDeadDebounceDelay)

	// Dead notification should now be sent
	if countAlerts(mockAlerter) != 1 {
		t.Fatalf("Expected 1 dead notification after debounce, got %d", countAlerts(mockAlerter))
	}
	notification := getLastAlert(mockAlerter)
	if notification.Title != "Device Offline" {
		t.Errorf("Expected title 'Device Offline', got '%s'", notification.Title)
	}

	// Device recovers — recovery notification should be sent
	manager.SimulateNodeStatusChange(testNodeStatus1, "alive")

	if countAlerts(mockAlerter) != 2 {
		t.Fatalf("Expected 2 notifications (1 dead + 1 recovery), got %d", countAlerts(mockAlerter))
	}
	recoveryNotification := getLastAlert(mockAlerter)
	if recoveryNotification.Title != "Device Online" {
		t.Errorf("Expected title 'Device Online', got '%s'", recoveryNotification.Title)
	}
}

func TestSensorHealthManager_NodeStatus_DeadDevice_CooldownSuppresses(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add a node status sensor (simulates a flapping device like Leaf Charger)
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   testNodeStatus1,
		DeviceID:   "device_zwave_1",
		DeviceName: "Leaf Charger",
		Status:     "alive",
	})

	// First cycle: device goes dead → debounce fires → notification sent
	manager.SimulateNodeStatusChange(testNodeStatus1, "dead")
	mockClock.Advance(NodeDeadDebounceDelay)

	if countAlerts(mockAlerter) != 1 {
		t.Fatalf("Expected 1 dead notification after first debounce, got %d", countAlerts(mockAlerter))
	}
	notification := getLastAlert(mockAlerter)
	if notification.Title != "Device Offline" {
		t.Errorf("Expected title 'Device Offline', got '%s'", notification.Title)
	}

	// Device recovers → recovery notification sent
	manager.SimulateNodeStatusChange(testNodeStatus1, "alive")

	if countAlerts(mockAlerter) != 2 {
		t.Fatalf("Expected 2 notifications (1 dead + 1 recovery), got %d", countAlerts(mockAlerter))
	}

	// Advance 30 minutes (well within the 48h cooldown)
	mockClock.Advance(30 * time.Minute)

	// Second cycle: device goes dead again within cooldown window
	manager.SimulateNodeStatusChange(testNodeStatus1, "dead")
	mockClock.Advance(NodeDeadDebounceDelay)

	// Notification should be suppressed by cooldown
	if countAlerts(mockAlerter) != 2 {
		t.Errorf("Expected 2 notifications (cooldown should suppress), got %d", countAlerts(mockAlerter))
	}

	// Device recovers — no recovery notification since dead notification was suppressed
	// (NotificationSent was never set to true for this cycle)
	manager.SimulateNodeStatusChange(testNodeStatus1, "alive")

	if countAlerts(mockAlerter) != 2 {
		t.Errorf("Expected 2 notifications (no recovery for suppressed dead), got %d", countAlerts(mockAlerter))
	}
}

func TestSensorHealthManager_NodeStatus_DeadDevice_CooldownExpires(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	mockAlerter := &alert.MockAlerter{}
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockAlerter, mockClock)

	// Add a node status sensor
	manager.AddNodeStatus(&NodeStatus{
		EntityID:   testNodeStatus1,
		DeviceID:   "device_zwave_1",
		DeviceName: "Leaf Charger",
		Status:     "alive",
	})

	// First cycle: device goes dead → debounce fires → notification sent
	manager.SimulateNodeStatusChange(testNodeStatus1, "dead")
	mockClock.Advance(NodeDeadDebounceDelay)

	if countAlerts(mockAlerter) != 1 {
		t.Fatalf("Expected 1 dead notification, got %d", countAlerts(mockAlerter))
	}

	// Device recovers
	manager.SimulateNodeStatusChange(testNodeStatus1, "alive")

	if countAlerts(mockAlerter) != 2 {
		t.Fatalf("Expected 2 notifications (dead + recovery), got %d", countAlerts(mockAlerter))
	}

	// Advance past the 48h cooldown
	mockClock.Advance(NodeDeadNotificationCooldown + 1*time.Hour)

	// Second cycle: device goes dead again after cooldown expired
	manager.SimulateNodeStatusChange(testNodeStatus1, "dead")
	mockClock.Advance(NodeDeadDebounceDelay)

	// Notification should be sent (cooldown expired)
	if countAlerts(mockAlerter) != 3 {
		t.Fatalf("Expected 3 notifications (cooldown expired, new dead notification), got %d", countAlerts(mockAlerter))
	}
	notification := getLastAlert(mockAlerter)
	if notification.Title != "Device Offline" {
		t.Errorf("Expected title 'Device Offline', got '%s'", notification.Title)
	}

	// Device recovers — recovery notification should be sent
	manager.SimulateNodeStatusChange(testNodeStatus1, "alive")

	if countAlerts(mockAlerter) != 4 {
		t.Fatalf("Expected 4 notifications (dead + recovery x2), got %d", countAlerts(mockAlerter))
	}
	recoveryNotification := getLastAlert(mockAlerter)
	if recoveryNotification.Title != "Device Online" {
		t.Errorf("Expected title 'Device Online', got '%s'", recoveryNotification.Title)
	}
}
