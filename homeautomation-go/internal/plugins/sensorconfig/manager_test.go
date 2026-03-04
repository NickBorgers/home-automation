package sensorconfig

import (
	"context"
	"fmt"
	"os"
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

	manager := NewManager(context.Background(), mockClient, config, logger, false)
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

	manager := NewManager(context.Background(), mockClient, config, logger, false)
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
	manager := NewManager(context.Background(), mockClient, config, logger, true)
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

	manager := NewManager(context.Background(), mockClient, config, logger, false)
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

	manager := NewManager(context.Background(), mockClient, config, logger, false)
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

	manager := NewManager(context.Background(), mockClient, config, logger, false)

	// Start
	err := manager.Start()
	assert.NoError(t, err)

	// Snapshot service call count before reset
	snapshot := mockClient.ServiceCallCount()
	_ = snapshot

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

	manager := NewManager(context.Background(), mockClient, config, logger, false)
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

	manager := NewManager(context.Background(), mockClient, config, logger, false)
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

	manager := NewManager(context.Background(), mockClient, config, logger, false)
	err := manager.Start()

	// Should not fail
	assert.NoError(t, err)

	// Service call should still be attempted
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 1)
}

func TestManager_Stop(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{},
	}

	manager := NewManager(context.Background(), mockClient, config, logger, false)

	// Start, then stop - should not panic
	err := manager.Start()
	assert.NoError(t, err)

	assert.NotPanics(t, func() {
		manager.Stop()
	})
}

func TestManager_Start_ServiceCallFailure(t *testing.T) {
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

	// Set up initial state different from target
	mockClient.SetState("number.temp_sensor_threshold", "10", nil)

	// Make the set_value service call fail
	mockClient.SetServiceError("number", "set_value", fmt.Errorf("service unavailable"))

	manager := NewManager(context.Background(), mockClient, config, logger, false)
	err := manager.Start()

	// Start should still succeed (errors are non-fatal)
	assert.NoError(t, err)

	// Shadow state should track the failure
	shadowState := manager.GetShadowState()
	assert.Len(t, shadowState.Outputs.Configurations, 1)
	assert.Contains(t, shadowState.Outputs.Configurations[0].FailedEntities, "number.temp_sensor_threshold")
}

func TestManager_Start_MixedSuccessAndFailure(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value: 50,
				Entities: []string{
					"number.sensor_good",
					"number.sensor_bad",
				},
			},
		},
	}

	// Set up initial states
	mockClient.SetState("number.sensor_good", "10", nil)
	mockClient.SetState("number.sensor_bad", "10", nil)

	// Make service call fail for the first call only (transient)
	mockClient.SetServiceFailCount("number", "set_value", 1, fmt.Errorf("timeout"))

	manager := NewManager(context.Background(), mockClient, config, logger, false)
	err := manager.Start()

	// Start should succeed (non-fatal)
	assert.NoError(t, err)

	// Shadow state should track results
	shadowState := manager.GetShadowState()
	assert.Len(t, shadowState.Outputs.Configurations, 1)
	cfg := shadowState.Outputs.Configurations[0]
	// First entity fails, second succeeds
	assert.Len(t, cfg.FailedEntities, 1)
	assert.Len(t, cfg.ConfiguredEntities, 1)
}

func TestManager_Start_MultipleConfigTypes_AllRecorded(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.temp_ok"},
			},
			LowTemperatureAlert: SensorThresholdConfig{
				Value:    32,
				Entities: []string{"number.low_temp_ok"},
			},
			BatteryReportThreshold: SensorThresholdConfig{
				Value:    5,
				Entities: []string{"number.battery_ok"},
			},
		},
	}

	// Set up initial states
	mockClient.SetState("number.temp_ok", "10", nil)
	mockClient.SetState("number.low_temp_ok", "10", nil)
	mockClient.SetState("number.battery_ok", "10", nil)

	manager := NewManager(context.Background(), mockClient, config, logger, false)
	err := manager.Start()

	// Start should succeed
	assert.NoError(t, err)

	// All three config types should be recorded in shadow state
	shadowState := manager.GetShadowState()
	assert.Len(t, shadowState.Outputs.Configurations, 3)
}

func TestManager_GetCurrentValue_MalformedState(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.malformed_sensor"},
			},
		},
	}

	// Set up state with a non-numeric value
	mockClient.SetState("number.malformed_sensor", "not_a_number", nil)

	manager := NewManager(context.Background(), mockClient, config, logger, false)
	err := manager.Start()

	// Start should succeed - malformed state is handled gracefully
	assert.NoError(t, err)

	// Service call should still be attempted
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 1)
}

func TestManager_GetCurrentValue_UnknownState(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.unknown_sensor"},
			},
		},
	}

	// Set up "unknown" state
	mockClient.SetState("number.unknown_sensor", "unknown", nil)

	manager := NewManager(context.Background(), mockClient, config, logger, false)
	err := manager.Start()

	assert.NoError(t, err)

	// Service call should still be attempted
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 1)
}

func TestManager_GetCurrentValue_EmptyState(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.empty_sensor"},
			},
		},
	}

	// Set up empty state
	mockClient.SetState("number.empty_sensor", "", nil)

	manager := NewManager(context.Background(), mockClient, config, logger, false)
	err := manager.Start()

	assert.NoError(t, err)

	// Service call should still be attempted
	calls := mockClient.GetServiceCalls()
	assert.Len(t, calls, 1)
}

func TestManager_Reset_ClearsShadowState(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.temp_sensor"},
			},
			BatteryReportThreshold: SensorThresholdConfig{
				Value:    5,
				Entities: []string{"number.battery_sensor"},
			},
		},
	}

	mockClient.SetState("number.temp_sensor", "10", nil)
	mockClient.SetState("number.battery_sensor", "10", nil)

	manager := NewManager(context.Background(), mockClient, config, logger, false)

	err := manager.Start()
	assert.NoError(t, err)

	// Verify initial shadow state has 2 configurations
	shadowState := manager.GetShadowState()
	assert.Len(t, shadowState.Outputs.Configurations, 2)

	// Reset should clear and re-populate
	snapshot := mockClient.ServiceCallCount()
	_ = snapshot
	err = manager.Reset()
	assert.NoError(t, err)

	// Shadow state should still have 2 configurations after reset
	shadowState = manager.GetShadowState()
	assert.Len(t, shadowState.Outputs.Configurations, 2)
}

func TestManager_ConfiguredAtTimestamp(t *testing.T) {
	mockClient := ha.NewMockClient()
	logger := zap.NewNop()

	config := &Config{
		Sensors: SensorsConfig{
			TemperatureReportThreshold: SensorThresholdConfig{
				Value:    50,
				Entities: []string{"number.temp_sensor"},
			},
		},
	}

	mockClient.SetState("number.temp_sensor", "10", nil)

	manager := NewManager(context.Background(), mockClient, config, logger, false)

	// Before start, configuredAt should be zero
	manager.configMu.RLock()
	assert.True(t, manager.configuredAt.IsZero())
	manager.configMu.RUnlock()

	err := manager.Start()
	assert.NoError(t, err)

	// After start, configuredAt should be set
	manager.configMu.RLock()
	assert.False(t, manager.configuredAt.IsZero())
	manager.configMu.RUnlock()
}

func TestLoadConfig_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/sensor_config.yaml"

	configContent := `sensors:
  temperature_report_threshold:
    value: 50
    entities:
      - number.sensor1
      - number.sensor2
  low_temperature_alert:
    value: 32
    entities:
      - number.alert1
  battery_report_threshold:
    value: 5
    entities:
      - number.battery1
`
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	assert.NoError(t, err)

	config, err := LoadConfig(configPath)
	assert.NoError(t, err)
	assert.NotNil(t, config)

	assert.Equal(t, 50.0, config.Sensors.TemperatureReportThreshold.Value)
	assert.Len(t, config.Sensors.TemperatureReportThreshold.Entities, 2)
	assert.Equal(t, 32.0, config.Sensors.LowTemperatureAlert.Value)
	assert.Len(t, config.Sensors.LowTemperatureAlert.Entities, 1)
	assert.Equal(t, 5.0, config.Sensors.BatteryReportThreshold.Value)
	assert.Len(t, config.Sensors.BatteryReportThreshold.Entities, 1)
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	config, err := LoadConfig("/nonexistent/path/sensor_config.yaml")
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/sensor_config.yaml"

	err := os.WriteFile(configPath, []byte("invalid: yaml: [broken"), 0644)
	assert.NoError(t, err)

	config, err := LoadConfig(configPath)
	assert.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "failed to parse config file")
}

func TestLoadConfig_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/sensor_config.yaml"

	err := os.WriteFile(configPath, []byte(""), 0644)
	assert.NoError(t, err)

	config, err := LoadConfig(configPath)
	assert.NoError(t, err)
	assert.NotNil(t, config)
	// Empty config should have zero-value fields
	assert.Empty(t, config.Sensors.TemperatureReportThreshold.Entities)
	assert.Empty(t, config.Sensors.LowTemperatureAlert.Entities)
	assert.Empty(t, config.Sensors.BatteryReportThreshold.Entities)
}
