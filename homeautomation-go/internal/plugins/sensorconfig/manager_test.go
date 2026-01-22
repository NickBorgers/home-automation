package sensorconfig

import (
	"testing"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestManager_Start_ConfiguresThresholds(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.temp_sensor_threshold"},
			},
			LowTemperatureAlert: SensorThresholdConfig{
				Value:    32,
				Entities: []string{"number.temp_sensor_low_alert"},
			},
			BatteryReportThreshold: SensorThresholdConfig{
				Value:    5,
				Entities: []string{"number.battery_threshold"},
			},
		},
	}

	// Set up initial states (different from target values)
	mockClient.SetState("number.temp_sensor_threshold", "10", nil)
	mockClient.SetState("number.temp_sensor_low_alert", "10", nil)
	mockClient.SetState("number.battery_threshold", "10", nil)

	manager := NewManager(mockClient, config, logger, false)
	err := manager.Start()

	assert.NoError(t, err)

	// Verify service calls were made
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 3)

	// Check each service call
	var foundTemp, foundLowAlert, foundBattery bool
	for _, call := range calls {
		assert.Equal(t, "number", call.Domain)
		assert.Equal(t, "set_value", call.Service)

		switch call.Data["entity_id"].(string) {
		case "number.temp_sensor_threshold":
			assert.Equal(t, 50.0, call.Data["value"])
			foundTemp = true
		case "number.temp_sensor_low_alert":
			assert.Equal(t, 32.0, call.Data["value"])
			foundLowAlert = true
		case "number.battery_threshold":
			assert.Equal(t, 5.0, call.Data["value"])
			foundBattery = true
		}
	}

	assert.True(t, foundTemp, "temperature threshold service call not found")
	assert.True(t, foundLowAlert, "low temperature alert service call not found")
	assert.True(t, foundBattery, "battery threshold service call not found")
}

func TestManager_Start_SkipsAlreadyConfigured(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.temp_sensor_threshold"},
			},
		},
	}

	// Set up state with target value already configured
	mockClient.SetState("number.temp_sensor_threshold", "50", nil)

	manager := NewManager(mockClient, config, logger, false)
	err := manager.Start()

	assert.NoError(t, err)

	// Verify no service calls were made since value is already correct
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 0)
}

func TestManager_Start_ReadOnlyMode(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.temp_sensor_threshold"},
			},
		},
	}

	// Set up state with different value
	mockClient.SetState("number.temp_sensor_threshold", "10", nil)

	// Create manager in read-only mode
	manager := NewManager(mockClient, config, logger, true)
	err := manager.Start()

	assert.NoError(t, err)

	// Verify no service calls were made in read-only mode
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 0)
}

func TestManager_Start_EmptyConfig(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{},
	}

	manager := NewManager(mockClient, config, logger, false)
	err := manager.Start()

	assert.NoError(t, err)

	// Verify no service calls were made
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 0)
}

func TestManager_GetShadowState(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.temp_sensor_threshold"},
			},
		},
	}

	// Set up initial state
	mockClient.SetState("number.temp_sensor_threshold", "10", nil)

	manager := NewManager(mockClient, config, logger, false)
	err := manager.Start()
	assert.NoError(t, err)

	// Check shadow state
	shadowState := manager.GetShadowState()
	assert.NotNil(t, shadowState)
	assert.Equal(t, "sensorconfig", shadowState.Plugin)
	assert.Len(t, shadowState.Outputs.Configurations, 1)
	assert.Equal(t, "temperature_report_threshold", shadowState.Outputs.Configurations[0].ConfigType)
	assert.Equal(t, 50.0, shadowState.Outputs.Configurations[0].Value)
	assert.Contains(t, shadowState.Outputs.Configurations[0].ConfiguredEntities, "number.temp_sensor_threshold")
}

func TestManager_Reset(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.temp_sensor_threshold"},
			},
		},
	}

	// Set up initial state
	mockClient.SetState("number.temp_sensor_threshold", "10", nil)

	manager := NewManager(mockClient, config, logger, false)

	// Start
	err := manager.Start()
	assert.NoError(t, err)

	// Clear service calls to track reset
	mockClient.ClearServiceCalls()

	// After first Start, the state will be "50.00" due to mock's updateStateFromServiceCall
	// So Reset should not make any more calls since value is correct

	// Reset
	err = manager.Reset()
	assert.NoError(t, err)

	// Verify shadow state was cleared and re-populated
	shadowState := manager.GetShadowState()
	assert.Len(t, shadowState.Outputs.Configurations, 1)
}

func TestManager_Start_MultipleEntities(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value: 50,
				Entities: []string{
					"number.sensor1_threshold",
					"number.sensor2_threshold",
					"number.sensor3_threshold",
				},
			},
		},
	}

	// Set up initial states (different from target)
	mockClient.SetState("number.sensor1_threshold", "10", nil)
	mockClient.SetState("number.sensor2_threshold", "20", nil)
	mockClient.SetState("number.sensor3_threshold", "30", nil)

	manager := NewManager(mockClient, config, logger, false)
	err := manager.Start()

	assert.NoError(t, err)

	// Verify all three service calls were made
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 3)

	// Check shadow state
	shadowState := manager.GetShadowState()
	assert.Len(t, shadowState.Outputs.Configurations[0].ConfiguredEntities, 3)
}

func TestManager_Start_EntityNotFound(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.nonexistent_sensor"},
			},
		},
	}

	// Don't set up any state - entity will not be found

	manager := NewManager(mockClient, config, logger, false)
	err := manager.Start()

	// Should not fail - just log and continue
	assert.NoError(t, err)

	// Service call should still be attempted
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 1)
}

func TestManager_Start_UnavailableEntity(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.unavailable_sensor"},
			},
		},
	}

	// Set up unavailable state
	mockClient.SetState("number.unavailable_sensor", "unavailable", nil)

	manager := NewManager(mockClient, config, logger, false)
	err := manager.Start()

	// Should not fail
	assert.NoError(t, err)

	// Service call should still be attempted
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 1)
}
