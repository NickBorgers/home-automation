package integration

import (
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/plugins/environmental"
	"homeautomation/internal/plugins/sensorhealth"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ============================================================================
// Sensor Monitoring Integration Tests
//
// These tests verify the complete sensor monitoring surface area across both
// sensorhealth and environmental plugins:
//
// sensorhealth plugin:
//   - Battery level monitoring (low battery alerts)
//   - Sensor staleness detection
//   - Temperature lockup detection
//
// environmental plugin:
//   - Humidity monitoring (indoor/outdoor classification, alerts)
//   - Water leak detection
//
// This ensures no monitoring functionality was lost during the refactor that
// separated sensorhealth from environmental monitoring.
// ============================================================================

// sensorMonitoringEnv holds all sensor monitoring plugins and test infrastructure
type sensorMonitoringEnv struct {
	server        *MockHAServer
	client        *ha.Client
	manager       *state.Manager
	logger        *zap.Logger
	sensorHealth  *sensorhealth.Manager
	environmental *environmental.Manager
	mockNtfy      *ntfy.MockClient
	mockClock     *clock.MockClock
}

// setupSensorMonitoringTest creates a test environment with sensor monitoring plugins
func setupSensorMonitoringTest(t *testing.T) (*sensorMonitoringEnv, func()) {
	// Setup base test infrastructure
	server, client, manager, baseCleanup := setupTest(t)

	logger := testlogger.New()
	mockNtfy := ntfy.NewMockClient()
	mockClock := clock.NewMockClock(time.Now())

	// Create plugin managers
	env := &sensorMonitoringEnv{
		server:        server,
		client:        client,
		manager:       manager,
		logger:        logger,
		mockNtfy:      mockNtfy,
		mockClock:     mockClock,
		sensorHealth:  sensorhealth.NewManagerWithClock(client, manager, logger, false, nil, mockNtfy, mockClock),
		environmental: environmental.NewManagerWithClock(client, manager, logger, false, nil, mockNtfy, mockClock),
	}

	cleanup := func() {
		env.sensorHealth.Stop()
		env.environmental.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// ============================================================================
// Test: Complete Sensor Monitoring Surface Area
// ============================================================================

func TestScenario_SensorMonitoring_CompleteSurfaceArea(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	t.Log("========== TEST: Complete Sensor Monitoring Surface Area ==========")
	t.Log("Verifies that sensorhealth + environmental plugins cover all sensor monitoring:")
	t.Log("  - Battery monitoring (sensorhealth)")
	t.Log("  - Temperature lockup (sensorhealth)")
	t.Log("  - Sensor staleness (sensorhealth)")
	t.Log("  - Humidity monitoring (environmental)")
	t.Log("  - Water leak detection (environmental)")

	// ========== SETUP: Add test entities to mock server ==========

	// Battery sensors (sensorhealth)
	env.server.SetState("sensor.living_room_battery", "15", map[string]interface{}{
		"device_class":        "battery",
		"friendly_name":       "Living Room Motion Battery",
		"unit_of_measurement": "%",
	})
	env.server.SetState("sensor.garage_battery", "85", map[string]interface{}{
		"device_class":        "battery",
		"friendly_name":       "Garage Sensor Battery",
		"unit_of_measurement": "%",
	})

	// Temperature sensors (sensorhealth - for lockup detection)
	env.server.SetState("sensor.garage_temperature", "72.0", map[string]interface{}{
		"device_class":  "temperature",
		"friendly_name": "Garage Temperature",
	})

	// Humidity sensors (environmental)
	env.server.SetState("sensor.basement_humidity", "45.0", map[string]interface{}{
		"device_class":  "humidity",
		"friendly_name": "Basement Humidity",
	})
	env.server.SetState("sensor.outdoor_humidity", "80.0", map[string]interface{}{
		"device_class":  "humidity",
		"friendly_name": "Outdoor Humidity",
	})

	// Water leak sensors (environmental)
	env.server.SetState("binary_sensor.kitchen_water_leak", "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Kitchen Water Leak",
	})
	env.server.SetState("binary_sensor.bathroom_water_leak", "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Bathroom Water Leak",
	})

	// Brief setup delay: Allow mock server state to propagate before starting plugins
	time.Sleep(100 * time.Millisecond)

	// ========== START PLUGINS ==========

	t.Log("Starting sensor monitoring plugins...")

	err := env.sensorHealth.Start()
	require.NoError(t, err, "Failed to start sensorhealth plugin")

	err = env.environmental.Start()
	require.NoError(t, err, "Failed to start environmental plugin")

	// Wait for plugins to complete initialization before assertions
	waitForCondition(t, func() bool {
		return len(env.sensorHealth.GetBatterySensors()) >= 1
	}, "sensorhealth should discover battery sensors")

	// ========== VERIFY: SensorHealth Plugin ==========

	t.Log("--- Verifying sensorhealth plugin functionality ---")

	// Check battery sensors discovered
	batterySensors := env.sensorHealth.GetBatterySensors()
	assert.GreaterOrEqual(t, len(batterySensors), 1,
		"SensorHealth should discover battery sensors")
	t.Logf("Battery sensors discovered: %d", len(batterySensors))

	// Check temperature sensors discovered (for lockup detection)
	tempSensors := env.sensorHealth.GetTemperatureSensors()
	assert.GreaterOrEqual(t, len(tempSensors), 1,
		"SensorHealth should discover temperature sensors for lockup detection")
	t.Logf("Temperature sensors discovered: %d", len(tempSensors))

	// Verify shadow state has battery sensors
	shShadowState := env.sensorHealth.GetShadowState()
	assert.Equal(t, "sensorhealth", shShadowState.Plugin)
	assert.GreaterOrEqual(t, len(shShadowState.Outputs.BatterySensors), 1,
		"Shadow state should track battery sensors")
	t.Logf("Battery sensors in shadow state: %d", len(shShadowState.Outputs.BatterySensors))

	// Verify shadow state has temperature sensors
	assert.GreaterOrEqual(t, len(shShadowState.Outputs.TemperatureSensors), 1,
		"Shadow state should track temperature sensors")
	t.Logf("Temperature sensors in shadow state: %d", len(shShadowState.Outputs.TemperatureSensors))

	// ========== VERIFY: Environmental Plugin ==========

	t.Log("--- Verifying environmental plugin functionality ---")

	// Check humidity sensors discovered
	humiditySensors := env.environmental.GetSensors()
	assert.GreaterOrEqual(t, len(humiditySensors), 1,
		"Environmental should discover humidity sensors")
	t.Logf("Humidity sensors discovered: %d", len(humiditySensors))

	// Check water leak sensors discovered
	waterLeakSensors := env.environmental.GetWaterLeakSensors()
	assert.GreaterOrEqual(t, len(waterLeakSensors), 1,
		"Environmental should discover water leak sensors")
	t.Logf("Water leak sensors discovered: %d", len(waterLeakSensors))

	// Verify shadow state has humidity sensors
	envShadowState := env.environmental.GetShadowState()
	assert.Equal(t, "environmental", envShadowState.Plugin)
	assert.GreaterOrEqual(t, len(envShadowState.Outputs.HumiditySensors), 1,
		"Shadow state should track humidity sensors")
	t.Logf("Humidity sensors in shadow state: %d", len(envShadowState.Outputs.HumiditySensors))

	// Verify shadow state has water leak sensors
	assert.GreaterOrEqual(t, len(envShadowState.Outputs.WaterLeakSensors), 1,
		"Shadow state should track water leak sensors")
	t.Logf("Water leak sensors in shadow state: %d", len(envShadowState.Outputs.WaterLeakSensors))

	// ========== SUMMARY ==========

	t.Log("--- Sensor Monitoring Surface Area Summary ---")
	t.Logf("SensorHealth plugin:")
	t.Logf("  - Battery sensors: %d", len(batterySensors))
	t.Logf("  - Temperature sensors: %d", len(tempSensors))
	t.Logf("Environmental plugin:")
	t.Logf("  - Humidity sensors: %d", len(humiditySensors))
	t.Logf("  - Water leak sensors: %d", len(waterLeakSensors))

	t.Log("✓ Complete sensor monitoring surface area verified")
}

// ============================================================================
// Test: Low Battery Detection (sensorhealth)
// ============================================================================

func TestScenario_SensorHealth_LowBatteryDetection(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	t.Log("========== TEST: Low Battery Detection ==========")

	// Add a low battery sensor
	env.server.SetState("sensor.front_door_battery", "12", map[string]interface{}{
		"device_class":        "battery",
		"friendly_name":       "Front Door Sensor Battery",
		"unit_of_measurement": "%",
	})
	// Brief setup delay: Allow entity to propagate before plugin start
	time.Sleep(50 * time.Millisecond)

	// Start sensorhealth plugin
	err := env.sensorHealth.Start()
	require.NoError(t, err)
	defer env.sensorHealth.Stop()

	// Wait for low battery notification to be sent
	waitForNtfyNotification(t, env.mockNtfy, "Low Battery", "low battery notification")

	// Verify notification was sent for low battery
	calls := env.mockNtfy.GetCalls()
	assert.GreaterOrEqual(t, len(calls), 1, "Should send notification for low battery")

	// Check that we got a low battery notification
	foundLowBatteryNotification := false
	for _, call := range calls {
		if call.Title == "Low Battery" {
			foundLowBatteryNotification = true
			break
		}
	}
	assert.True(t, foundLowBatteryNotification, "Should have sent a low battery notification")

	t.Log("✓ Low battery detection verified")
}

// ============================================================================
// Test: Water Leak Detection (environmental)
// ============================================================================

func TestScenario_Environmental_WaterLeakDetection(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	t.Log("========== TEST: Water Leak Detection ==========")

	// Add water leak sensor
	env.server.SetState("binary_sensor.laundry_water_leak", "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Laundry Room Water Leak",
	})
	// Brief setup delay: Allow entity to propagate before plugin start
	time.Sleep(50 * time.Millisecond)

	// Start environmental plugin
	err := env.environmental.Start()
	require.NoError(t, err)
	defer env.environmental.Stop()

	// Clear any initialization notifications
	env.mockNtfy.Reset()

	// Verify no active leaks initially
	assert.Equal(t, 0, env.environmental.GetActiveWaterLeakCount())

	// Simulate water leak detection
	t.Log("Simulating water leak...")
	env.server.SetState("binary_sensor.laundry_water_leak", "on", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Laundry Room Water Leak",
	})

	// Wait for water leak to be detected
	waitForCondition(t, func() bool {
		return env.environmental.GetActiveWaterLeakCount() == 1
	}, "water leak detection")

	// Verify leak is detected
	assert.Equal(t, 1, env.environmental.GetActiveWaterLeakCount(),
		"Should detect 1 active water leak")

	// Verify notification was sent
	calls := env.mockNtfy.GetCalls()
	assert.GreaterOrEqual(t, len(calls), 1, "Should send notification for water leak")

	foundWaterLeakNotification := false
	for _, call := range calls {
		if call.Title == "Water Leak Detected" {
			foundWaterLeakNotification = true
			assert.Equal(t, ntfy.PriorityUrgent, call.Priority,
				"Water leak notification should be urgent priority")
			break
		}
	}
	assert.True(t, foundWaterLeakNotification, "Should have sent a water leak notification")

	// Verify shadow state
	shadowState := env.environmental.GetShadowState()
	assert.Equal(t, 1, len(shadowState.Outputs.ActiveWaterLeaks),
		"Shadow state should show 1 active water leak")

	t.Log("✓ Water leak detection verified")
}

// ============================================================================
// Test: High Humidity Alert (environmental)
// ============================================================================

func TestScenario_Environmental_HighHumidityAlert(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	t.Log("========== TEST: High Humidity Alert ==========")

	// Start environmental plugin (will not discover humidity sensors since
	// we're adding them manually for testing)
	err := env.environmental.Start()
	require.NoError(t, err)
	defer env.environmental.Stop()

	// Add humidity sensor manually (classified as indoor)
	env.environmental.AddSensor(&environmental.HumiditySensor{
		EntityID:     "sensor.bathroom_humidity",
		FriendlyName: "Bathroom Humidity",
		IsIndoor:     true,
		Valid:        true,
	})

	// Clear any setup notifications
	env.mockNtfy.Reset()

	// Simulate warning-level humidity
	t.Log("Simulating warning-level humidity (58%)...")
	env.environmental.SimulateSensorChange("sensor.bathroom_humidity", 58.0)

	// Should not alert yet (not sustained)
	alertLevel := env.environmental.GetCurrentState()
	assert.Equal(t, "none", alertLevel, "Should not alert for non-sustained humidity")

	// Advance time past sustained threshold (30 minutes)
	t.Log("Advancing time past sustained threshold...")
	env.mockClock.Advance(31 * time.Minute)
	env.environmental.SimulateSensorChange("sensor.bathroom_humidity", 58.0)

	// Should now be warning level
	alertLevel = env.environmental.GetCurrentState()
	assert.Equal(t, "warning", alertLevel, "Should alert for sustained high humidity")

	// Verify notification was sent
	calls := env.mockNtfy.GetCalls()
	assert.GreaterOrEqual(t, len(calls), 1, "Should send notification for sustained humidity")

	foundHumidityNotification := false
	for _, call := range calls {
		if call.Title == "High Humidity Warning" || call.Title == "High Humidity Critical" {
			foundHumidityNotification = true
			break
		}
	}
	assert.True(t, foundHumidityNotification, "Should have sent a humidity notification")

	t.Log("✓ High humidity alert verified")
}

// ============================================================================
// Test: Temperature Lockup Detection (sensorhealth)
// ============================================================================

func TestScenario_SensorHealth_TemperatureLockupDetection(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	t.Log("========== TEST: Temperature Lockup Detection ==========")

	// Start sensorhealth plugin
	err := env.sensorHealth.Start()
	require.NoError(t, err)
	defer env.sensorHealth.Stop()

	// Add temperature sensor manually
	initialTime := env.mockClock.Now()
	env.sensorHealth.AddTemperatureSensor(&sensorhealth.TemperatureSensor{
		EntityID:        "sensor.test_temp",
		FriendlyName:    "Test Temperature",
		Value:           72.0,
		Valid:           true,
		LastValueChange: initialTime,
		LastValue:       72.0,
	})

	// Clear any setup notifications
	env.mockNtfy.Reset()

	// Verify sensor is not locked up initially
	sensors := env.sensorHealth.GetTemperatureSensors()
	require.Contains(t, sensors, "sensor.test_temp")
	assert.False(t, sensors["sensor.test_temp"].IsLockedUp, "Should not be locked up initially")

	// Advance time past lockup threshold (12 hours) without value change
	t.Log("Advancing time past lockup threshold (13 hours)...")
	env.mockClock.Advance(13 * time.Hour)
	env.sensorHealth.SimulateTemperatureChange("sensor.test_temp", 72.0) // Same value

	// Trigger lockup check
	env.sensorHealth.TriggerLockupCheck()

	// Verify sensor is now marked as locked up
	sensors = env.sensorHealth.GetTemperatureSensors()
	assert.True(t, sensors["sensor.test_temp"].IsLockedUp, "Should be marked as locked up")

	// Verify notification was sent
	calls := env.mockNtfy.GetCalls()
	assert.GreaterOrEqual(t, len(calls), 1, "Should send notification for temperature lockup")

	foundLockupNotification := false
	for _, call := range calls {
		if call.Title == "Temperature Sensor Locked Up" {
			foundLockupNotification = true
			break
		}
	}
	assert.True(t, foundLockupNotification, "Should have sent a lockup notification")

	t.Log("✓ Temperature lockup detection verified")
}

// ============================================================================
// Test: Both Plugins Running Concurrently
// ============================================================================

func TestScenario_BothPlugins_ConcurrentOperation(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	t.Log("========== TEST: Both Plugins Running Concurrently ==========")

	// Add sensors for both plugins
	env.server.SetState("sensor.test_battery", "10", map[string]interface{}{
		"device_class":        "battery",
		"friendly_name":       "Test Battery",
		"unit_of_measurement": "%",
	})
	env.server.SetState("binary_sensor.test_water_leak", "off", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Test Water Leak",
	})
	// Brief setup delay: Allow entities to propagate before plugin start
	time.Sleep(50 * time.Millisecond)

	// Start both plugins
	err := env.sensorHealth.Start()
	require.NoError(t, err)

	err = env.environmental.Start()
	require.NoError(t, err)

	// Wait for low battery notification (from plugin initialization)
	waitForNtfyNotification(t, env.mockNtfy, "Low Battery", "low battery notification during init")

	// Record initial notifications (low battery should have triggered)
	initialCalls := len(env.mockNtfy.GetCalls())
	t.Logf("Initial notification count: %d (from low battery detection)", initialCalls)

	// Now trigger a water leak
	t.Log("Triggering water leak...")
	env.server.SetState("binary_sensor.test_water_leak", "on", map[string]interface{}{
		"device_class":  "moisture",
		"friendly_name": "Test Water Leak",
	})

	// Wait for water leak to be detected
	waitForCondition(t, func() bool {
		return env.environmental.GetActiveWaterLeakCount() == 1
	}, "water leak detection")

	// Verify water leak detection worked
	assert.Equal(t, 1, env.environmental.GetActiveWaterLeakCount(),
		"Environmental plugin should detect water leak while sensorhealth is running")

	// Verify additional notification was sent
	finalCalls := len(env.mockNtfy.GetCalls())
	assert.Greater(t, finalCalls, initialCalls,
		"Should have sent additional notification for water leak")

	// Verify both shadow states are accessible
	shShadow := env.sensorHealth.GetShadowState()
	envShadow := env.environmental.GetShadowState()

	assert.Equal(t, "sensorhealth", shShadow.Plugin)
	assert.Equal(t, "environmental", envShadow.Plugin)

	t.Logf("SensorHealth shadow state: %d battery sensors, %d temp sensors",
		len(shShadow.Outputs.BatterySensors), len(shShadow.Outputs.TemperatureSensors))
	t.Logf("Environmental shadow state: %d humidity sensors, %d water leak sensors, %d active leaks",
		len(envShadow.Outputs.HumiditySensors), len(envShadow.Outputs.WaterLeakSensors),
		len(envShadow.Outputs.ActiveWaterLeaks))

	t.Log("✓ Both plugins operating correctly and concurrently")
}

// ============================================================================
// Test: Node Status Monitoring
// ============================================================================

func TestScenario_SensorHealth_NodeStatusMonitoring(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSensorMonitoringTest(t)
	defer cleanup()

	t.Log("========== TEST: Node Status Monitoring ==========")
	t.Log("Verifies Z-Wave node status monitoring:")
	t.Log("  - Discovery of node_status sensors")
	t.Log("  - Dead device detection and notification")
	t.Log("  - Device recovery detection and notification")
	t.Log("  - Shadow state tracking of node statuses")

	// ========== SETUP: Add Z-Wave node status sensor ==========
	env.server.SetState("sensor.front_door_lock_node_status", "alive", map[string]interface{}{
		"friendly_name": "Front Door Lock Node Status",
	})
	// Brief setup delay: Allow entity to propagate before plugin start
	time.Sleep(50 * time.Millisecond)

	// Start sensorhealth plugin
	err := env.sensorHealth.Start()
	require.NoError(t, err)
	defer env.sensorHealth.Stop()

	// Wait for node status sensor to be discovered
	waitForCondition(t, func() bool {
		shadowState := env.sensorHealth.GetShadowState()
		return len(shadowState.Outputs.NodeStatuses) >= 1
	}, "node status sensor discovery")

	// Verify sensor was discovered
	shadowState := env.sensorHealth.GetShadowState()
	assert.GreaterOrEqual(t, len(shadowState.Outputs.NodeStatuses), 1,
		"Should discover node status sensor")

	t.Log("✓ Node status sensor discovered")

	// ========== TEST: Dead Device Detection ==========
	env.mockNtfy.Reset()

	// Simulate device going dead
	env.server.SetState("sensor.front_door_lock_node_status", "dead", map[string]interface{}{
		"friendly_name": "Front Door Lock Node Status",
	})
	// Wait for entity state change to propagate through event processing
	waitForCondition(t, func() bool {
		for _, ns := range env.sensorHealth.GetShadowState().Outputs.NodeStatuses {
			if ns.EntityID == "sensor.front_door_lock_node_status" {
				return ns.Status == "dead"
			}
		}
		return false
	}, "node status should update to dead")

	// Advance mock clock past the debounce delay and process timer callbacks
	env.mockClock.AdvanceAndProcess(sensorhealth.NodeDeadDebounceDelay + 1*time.Second)

	// Wait for dead device notification (notification is sent by AfterFunc callback)
	waitForCondition(t, func() bool {
		calls := env.mockNtfy.GetCalls()
		return len(calls) >= 1
	}, "dead device notification")

	// Verify notification sent
	calls := env.mockNtfy.GetCalls()
	assert.GreaterOrEqual(t, len(calls), 1, "Should send notification for dead device")

	if len(calls) > 0 {
		assert.Contains(t, calls[0].Body, "dead", "Notification should mention dead status")
		assert.Equal(t, ntfy.PriorityHigh, calls[0].Priority, "Dead device should be high priority")
	}

	// Verify shadow state shows dead device alert
	shadowState = env.sensorHealth.GetShadowState()
	assert.GreaterOrEqual(t, len(shadowState.Outputs.DeadDeviceAlerts), 1,
		"Should have dead device alert in shadow state")

	if len(shadowState.Outputs.DeadDeviceAlerts) > 0 {
		alert := shadowState.Outputs.DeadDeviceAlerts[0]
		assert.Equal(t, "sensor.front_door_lock_node_status", alert.EntityID)
	}

	// Verify node status shows dead in shadow state
	foundDead := false
	for _, ns := range shadowState.Outputs.NodeStatuses {
		if ns.EntityID == "sensor.front_door_lock_node_status" {
			assert.Equal(t, "dead", ns.Status, "Node status should be dead")
			foundDead = true
			break
		}
	}
	assert.True(t, foundDead, "Should find dead node status in shadow state")

	t.Log("✓ Dead device detected and notification sent")

	// ========== TEST: Device Recovery ==========
	env.mockNtfy.Reset()

	// Simulate device recovery
	env.server.SetState("sensor.front_door_lock_node_status", "alive", map[string]interface{}{
		"friendly_name": "Front Door Lock Node Status",
	})

	// Wait for recovery notification
	waitForCondition(t, func() bool {
		calls := env.mockNtfy.GetCalls()
		return len(calls) >= 1
	}, "device recovery notification")

	// Verify recovery notification sent
	calls = env.mockNtfy.GetCalls()
	assert.GreaterOrEqual(t, len(calls), 1, "Should send notification for device recovery")

	if len(calls) > 0 {
		assert.Contains(t, calls[0].Body, "back online", "Notification should mention device is back online")
		assert.Equal(t, ntfy.PriorityDefault, calls[0].Priority, "Recovery should be default priority")
	}

	// Verify shadow state no longer shows dead device alert
	shadowState = env.sensorHealth.GetShadowState()
	assert.Equal(t, 0, len(shadowState.Outputs.DeadDeviceAlerts),
		"Should have no dead device alerts after recovery")

	// Verify node status shows alive in shadow state
	found := false
	for _, ns := range shadowState.Outputs.NodeStatuses {
		if ns.EntityID == "sensor.front_door_lock_node_status" {
			assert.Equal(t, "alive", ns.Status, "Node status should be alive")
			found = true
			break
		}
	}
	assert.True(t, found, "Should find node status in shadow state")

	t.Log("✓ Device recovery detected and notification sent")
	t.Log("✓ Node status monitoring complete")
}
