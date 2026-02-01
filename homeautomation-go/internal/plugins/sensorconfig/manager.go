package sensorconfig

import (
	"context"
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"

	"go.uber.org/zap"
)

// Manager handles one-time configuration of Zigbee sensor thresholds at startup
type Manager struct {
	haClient      ha.HAClient
	config        *Config
	logger        *zap.Logger
	readOnly      bool
	shadowTracker *shadowstate.SensorConfigTracker

	// configuredAt tracks when configuration was applied
	configuredAt time.Time
	configMu     sync.RWMutex
}

// NewManager creates a new Sensor Config manager
func NewManager(haClient ha.HAClient, config *Config, logger *zap.Logger, readOnly bool) *Manager {
	return &Manager{
		haClient:      haClient,
		config:        config,
		logger:        logger.Named("sensorconfig"),
		readOnly:      readOnly,
		shadowTracker: shadowstate.NewSensorConfigTracker(),
	}
}

// Start configures all sensor thresholds
func (m *Manager) Start() error {
	m.logger.Info("Starting Sensor Config Manager - configuring Zigbee sensor thresholds")

	var errors []error

	// Configure temperature report thresholds
	if len(m.config.Sensors.TemperatureReportThreshold.Entities) > 0 {
		if err := m.configureThresholds(
			"temperature_report_threshold",
			m.config.Sensors.TemperatureReportThreshold.Entities,
			m.config.Sensors.TemperatureReportThreshold.Value,
			"Temperature report threshold (0.1°F units)",
		); err != nil {
			errors = append(errors, err)
		}
	}

	// Configure low temperature alert thresholds
	if len(m.config.Sensors.LowTemperatureAlert.Entities) > 0 {
		if err := m.configureThresholds(
			"low_temperature_alert",
			m.config.Sensors.LowTemperatureAlert.Entities,
			m.config.Sensors.LowTemperatureAlert.Value,
			"Low temperature alert threshold (°F)",
		); err != nil {
			errors = append(errors, err)
		}
	}

	// Configure battery report thresholds
	if len(m.config.Sensors.BatteryReportThreshold.Entities) > 0 {
		if err := m.configureThresholds(
			"battery_report_threshold",
			m.config.Sensors.BatteryReportThreshold.Entities,
			m.config.Sensors.BatteryReportThreshold.Value,
			"Battery report threshold (%)",
		); err != nil {
			errors = append(errors, err)
		}
	}

	// Record configuration time
	m.configMu.Lock()
	m.configuredAt = time.Now()
	m.configMu.Unlock()

	if len(errors) > 0 {
		m.logger.Warn("Sensor Config Manager started with errors",
			zap.Int("error_count", len(errors)))
		// Don't fail startup - some entities may be temporarily unavailable
	} else {
		m.logger.Info("Sensor Config Manager started successfully - all thresholds configured")
	}

	return nil
}

// configureThresholds sets a threshold value on multiple entities
func (m *Manager) configureThresholds(configType string, entities []string, value float64, description string) error {
	m.logger.Info("Configuring sensor thresholds",
		zap.String("type", configType),
		zap.Int("entity_count", len(entities)),
		zap.Float64("value", value),
		zap.String("description", description))

	var configuredEntities []string
	var failedEntities []string

	for _, entityID := range entities {
		// Check current value before setting
		currentValue, err := m.getCurrentValue(entityID)
		if err != nil {
			m.logger.Warn("Could not get current value for entity",
				zap.String("entity_id", entityID),
				zap.Error(err))
			// Continue anyway - entity might not be available yet
		} else if currentValue == value {
			m.logger.Debug("Entity already has correct value, skipping",
				zap.String("entity_id", entityID),
				zap.Float64("current_value", currentValue))
			configuredEntities = append(configuredEntities, entityID)
			continue
		} else {
			m.logger.Info("Entity needs configuration",
				zap.String("entity_id", entityID),
				zap.Float64("current_value", currentValue),
				zap.Float64("target_value", value))
		}

		if m.readOnly {
			m.logger.Info("READ-ONLY: Would set threshold",
				zap.String("entity_id", entityID),
				zap.Float64("value", value))
			configuredEntities = append(configuredEntities, entityID)
			continue
		}

		// Call number.set_value service
		err = m.haClient.CallService(context.Background(), "number", "set_value", map[string]interface{}{
			"entity_id": entityID,
			"value":     value,
		})
		if err != nil {
			m.logger.Error("Failed to set threshold",
				zap.String("entity_id", entityID),
				zap.Float64("value", value),
				zap.Error(err))
			failedEntities = append(failedEntities, entityID)
			continue
		}

		m.logger.Info("Successfully configured threshold",
			zap.String("entity_id", entityID),
			zap.Float64("value", value))
		configuredEntities = append(configuredEntities, entityID)
	}

	// Record in shadow state
	m.shadowTracker.RecordConfiguration(configType, description, value, configuredEntities, failedEntities)

	if len(failedEntities) > 0 {
		return fmt.Errorf("failed to configure %d/%d entities for %s", len(failedEntities), len(entities), configType)
	}

	return nil
}

// getCurrentValue gets the current value of a number entity
func (m *Manager) getCurrentValue(entityID string) (float64, error) {
	state, err := m.haClient.GetState(entityID)
	if err != nil {
		return 0, fmt.Errorf("failed to get state: %w", err)
	}

	if state == nil || state.State == "" || state.State == "unavailable" || state.State == "unknown" {
		return 0, fmt.Errorf("entity state is unavailable or unknown")
	}

	// Parse the state as a float
	var value float64
	_, err = fmt.Sscanf(state.State, "%f", &value)
	if err != nil {
		return 0, fmt.Errorf("failed to parse state as float: %w", err)
	}

	return value, nil
}

// Stop is a no-op for this plugin (no subscriptions to clean up)
func (m *Manager) Stop() {
	m.logger.Info("Sensor Config Manager stopped")
}

// Reset re-applies all sensor configurations
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Sensor Config - re-applying all thresholds")

	// Clear shadow state
	m.shadowTracker.Clear()

	// Re-run configuration
	return m.Start()
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.SensorConfigShadowState {
	return m.shadowTracker.GetState()
}
