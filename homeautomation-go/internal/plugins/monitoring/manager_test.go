package monitoring

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Test entity IDs
const (
	testWaterLeakSensor1 = "binary_sensor.water_leak_1"
	testWaterLeakSensor2 = "binary_sensor.water_leak_2"
	testBatterySensor1   = "sensor.battery_1"
	testBatterySensor2   = "sensor.battery_2"
	testBatterySensor3   = "sensor.battery_3"
)

// setupMockEnvironment creates a mock HA client with test devices and entity registry
func setupMockEnvironment(mockHA *ha.MockClient) {
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
		EntityID: testBatterySensor1,
		DeviceID: "device_battery_1",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testBatterySensor2,
		DeviceID: "device_battery_2",
	})
	mockHA.AddEntityRegistryEntry(&ha.EntityRegistryEntry{
		EntityID: testBatterySensor3,
		DeviceID: "device_battery_3",
	})

	// Add water leak sensor states
	mockHA.SetState(testWaterLeakSensor1, "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Kitchen Water Leak Sensor",
	})
	mockHA.SetState(testWaterLeakSensor2, "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Bathroom Water Leak Sensor",
	})

	// Add battery sensor states
	mockHA.SetState(testBatterySensor1, "85.0", map[string]interface{}{
		"device_class":        "battery",
		"unit_of_measurement": "%",
		"friendly_name":       "Motion Sensor Battery",
	})
	mockHA.SetState(testBatterySensor2, "15.0", map[string]interface{}{
		"device_class":        "battery",
		"unit_of_measurement": "%",
		"friendly_name":       "Door Sensor Battery",
	})
	mockHA.SetState(testBatterySensor3, "unavailable", map[string]interface{}{
		"device_class":        "battery",
		"unit_of_measurement": "%",
		"friendly_name":       "Offline Sensor Battery",
	})
}

// Helper to count notifications
func countNotifications(mockHA *ha.MockClient) int {
	count := 0
	for _, call := range mockHA.GetServiceCalls() {
		if call.Domain == "notify" {
			count++
		}
	}
	return count
}

// Helper to get the last notification message
func getLastNotificationMessage(mockHA *ha.MockClient) string {
	calls := mockHA.GetServiceCalls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Domain == "notify" {
			if msg, ok := calls[i].Data["message"].(string); ok {
				return msg
			}
		}
	}
	return ""
}

func TestMonitoringManager_DynamicDiscovery(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	setupMockEnvironment(mockHA)

	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	manager := NewManager(mockHA, stateMgr, logger, false, nil)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Verify water leak sensors were discovered
	waterLeakSensors := manager.GetWaterLeakSensors()
	if len(waterLeakSensors) != 2 {
		t.Errorf("Expected 2 water leak sensors discovered, got %d", len(waterLeakSensors))
	}

	// Verify battery sensors were discovered
	batterySensors := manager.GetBatterySensors()
	if len(batterySensors) != 3 {
		t.Errorf("Expected 3 battery sensors discovered, got %d", len(batterySensors))
	}
}

func TestMonitoringManager_WaterLeakDetection(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// Add test sensor directly
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak Sensor",
		State:        "off",
	})

	// Verify no active leaks initially
	if count := manager.GetActiveWaterLeakCount(); count != 0 {
		t.Errorf("Expected 0 active leaks initially, got %d", count)
	}

	// Simulate water leak detected
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Verify active leak
	if count := manager.GetActiveWaterLeakCount(); count != 1 {
		t.Errorf("Expected 1 active leak, got %d", count)
	}

	// Verify notification was sent
	notificationCount := countNotifications(mockHA)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for water leak, got %d", notificationCount)
	}

	msg := getLastNotificationMessage(mockHA)
	if msg == "" {
		t.Error("Expected notification message, got empty")
	}
}

func TestMonitoringManager_WaterLeakClearedNoRenotify(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak Sensor",
		State:        "off",
	})

	// Simulate leak detected
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")
	initialNotifications := countNotifications(mockHA)

	// Simulate same leak still detected (should not re-notify)
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	if count := countNotifications(mockHA); count != initialNotifications {
		t.Errorf("Should not re-notify for same leak, expected %d notifications, got %d",
			initialNotifications, count)
	}

	// Simulate leak cleared
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "off")

	if count := manager.GetActiveWaterLeakCount(); count != 0 {
		t.Errorf("Expected 0 active leaks after cleared, got %d", count)
	}

	// Simulate new leak (should notify again)
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	if count := countNotifications(mockHA); count != initialNotifications+1 {
		t.Errorf("Should notify for new leak, expected %d notifications, got %d",
			initialNotifications+1, count)
	}
}

func TestMonitoringManager_LowBatteryDetection(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// Add test sensor with normal battery level
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     testBatterySensor1,
		FriendlyName: "Motion Sensor Battery",
		BatteryLevel: 85.0,
		IsLow:        false,
		LastReported: mockClock.Now(),
	})

	// Verify no low batteries initially
	if count := manager.GetLowBatteryCount(); count != 0 {
		t.Errorf("Expected 0 low batteries initially, got %d", count)
	}

	// Simulate battery dropping below threshold (20%)
	manager.SimulateBatteryChange(testBatterySensor1, 15.0)

	// Verify low battery detected
	if count := manager.GetLowBatteryCount(); count != 1 {
		t.Errorf("Expected 1 low battery, got %d", count)
	}

	// Verify notification was sent
	notificationCount := countNotifications(mockHA)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for low battery, got %d", notificationCount)
	}
}

func TestMonitoringManager_BatteryRecoveryResetNotification(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	manager.AddBatterySensor(&BatterySensor{
		EntityID:     testBatterySensor1,
		FriendlyName: "Motion Sensor Battery",
		BatteryLevel: 85.0,
		IsLow:        false,
		LastReported: mockClock.Now(),
	})

	// Simulate low battery
	manager.SimulateBatteryChange(testBatterySensor1, 15.0)
	initialNotifications := countNotifications(mockHA)

	// Simulate battery recharged
	manager.SimulateBatteryChange(testBatterySensor1, 50.0)

	// Verify no longer low
	if count := manager.GetLowBatteryCount(); count != 0 {
		t.Errorf("Expected 0 low batteries after recharge, got %d", count)
	}

	// Simulate low again (should notify again)
	manager.SimulateBatteryChange(testBatterySensor1, 10.0)

	if count := countNotifications(mockHA); count != initialNotifications+1 {
		t.Errorf("Should notify for new low battery, expected %d notifications, got %d",
			initialNotifications+1, count)
	}
}

func TestMonitoringManager_StaleSensorDetection(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// Add sensor with recent update
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     testBatterySensor1,
		FriendlyName: "Motion Sensor Battery",
		BatteryLevel: 50.0,
		IsLow:        false,
		LastReported: mockClock.Now(),
	})

	// Check staleness - should not be stale yet
	manager.checkStaleSensors()

	sensors := manager.GetBatterySensors()
	if sensors[testBatterySensor1].IsStale {
		t.Error("Sensor should not be stale immediately after update")
	}

	// Advance time past staleness threshold (25 hours)
	mockClock.Advance(25 * time.Hour)

	// Check staleness again
	manager.checkStaleSensors()

	sensors = manager.GetBatterySensors()
	if !sensors[testBatterySensor1].IsStale {
		t.Error("Sensor should be stale after 25 hours without update")
	}

	// Verify notification was sent
	notificationCount := countNotifications(mockHA)
	if notificationCount != 1 {
		t.Errorf("Expected 1 notification for stale sensor, got %d", notificationCount)
	}
}

func TestMonitoringManager_ReadOnlyMode(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	// Create manager in read-only mode
	manager := NewManagerWithClock(mockHA, stateMgr, logger, true, nil, mockClock)

	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak Sensor",
		State:        "off",
	})

	// Simulate water leak
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")

	// Verify no actual notifications were sent in read-only mode
	notificationCount := countNotifications(mockHA)
	if notificationCount != 0 {
		t.Errorf("Expected 0 notifications in read-only mode, got %d", notificationCount)
	}
}

func TestMonitoringManager_ShadowState(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak Sensor",
		State:        "off",
	})

	manager.AddBatterySensor(&BatterySensor{
		EntityID:     testBatterySensor1,
		FriendlyName: "Motion Sensor Battery",
		BatteryLevel: 50.0,
		LastReported: mockClock.Now(),
	})

	// Update shadow state
	manager.updateShadowState()

	// Get shadow state
	shadowState := manager.GetShadowState()

	if shadowState.Plugin != "monitoring" {
		t.Errorf("Expected plugin name 'monitoring', got '%s'", shadowState.Plugin)
	}

	if len(shadowState.Outputs.WaterLeakSensors) != 1 {
		t.Errorf("Expected 1 water leak sensor in shadow state, got %d",
			len(shadowState.Outputs.WaterLeakSensors))
	}

	if len(shadowState.Outputs.BatterySensors) != 1 {
		t.Errorf("Expected 1 battery sensor in shadow state, got %d",
			len(shadowState.Outputs.BatterySensors))
	}

	// Verify config defaults
	if shadowState.Outputs.Config.LowBatteryThreshold != 20 {
		t.Errorf("Expected low battery threshold 20, got %d",
			shadowState.Outputs.Config.LowBatteryThreshold)
	}
}

func TestMonitoringManager_MultipleSensors(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	// Add multiple water leak sensors
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak Sensor",
		State:        "off",
	})
	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor2,
		FriendlyName: "Bathroom Water Leak Sensor",
		State:        "off",
	})

	// Add multiple battery sensors
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     testBatterySensor1,
		FriendlyName: "Sensor 1",
		BatteryLevel: 50.0,
		LastReported: mockClock.Now(),
	})
	manager.AddBatterySensor(&BatterySensor{
		EntityID:     testBatterySensor2,
		FriendlyName: "Sensor 2",
		BatteryLevel: 80.0,
		LastReported: mockClock.Now(),
	})

	// Trigger multiple leaks
	manager.SimulateWaterLeakChange(testWaterLeakSensor1, "on")
	manager.SimulateWaterLeakChange(testWaterLeakSensor2, "on")

	// Verify both leaks detected
	if count := manager.GetActiveWaterLeakCount(); count != 2 {
		t.Errorf("Expected 2 active leaks, got %d", count)
	}

	// Verify 2 notifications sent
	if count := countNotifications(mockHA); count != 2 {
		t.Errorf("Expected 2 notifications, got %d", count)
	}

	// Trigger multiple low batteries
	manager.SimulateBatteryChange(testBatterySensor1, 10.0)
	manager.SimulateBatteryChange(testBatterySensor2, 5.0)

	// Verify both low batteries detected
	if count := manager.GetLowBatteryCount(); count != 2 {
		t.Errorf("Expected 2 low batteries, got %d", count)
	}

	// Verify total 4 notifications (2 leaks + 2 batteries)
	if count := countNotifications(mockHA); count != 4 {
		t.Errorf("Expected 4 total notifications, got %d", count)
	}
}

func TestMonitoringManager_Reset(t *testing.T) {
	t.Parallel()

	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	mockClock := clock.NewMockClock(time.Now())

	manager := NewManagerWithClock(mockHA, stateMgr, logger, false, nil, mockClock)

	manager.AddWaterLeakSensor(&WaterLeakSensor{
		EntityID:     testWaterLeakSensor1,
		FriendlyName: "Kitchen Water Leak Sensor",
		State:        "on",
	})

	// Reset should re-evaluate and detect the existing leak
	err := manager.Reset()
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify leak was detected during reset
	if count := manager.GetActiveWaterLeakCount(); count != 1 {
		t.Errorf("Expected 1 active leak after reset, got %d", count)
	}
}

func TestParseBatteryLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected float64
		wantErr  bool
	}{
		{"100.0", 100.0, false},
		{"50", 50.0, false},
		{"15.5", 15.5, false},
		{"0", 0.0, false},
		{"unavailable", 0, true},
		{"unknown", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseBatteryLevel(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseBatteryLevel(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && result != tt.expected {
				t.Errorf("parseBatteryLevel(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}
