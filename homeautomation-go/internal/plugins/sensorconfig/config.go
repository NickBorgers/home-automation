package sensorconfig

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the sensor configuration settings
type Config struct {
	Sensors SensorsConfig `yaml:"sensors"`
}

// SensorsConfig contains all sensor threshold configurations
type SensorsConfig struct {
	// TemperatureReportThreshold is the threshold for temperature change reporting (in 0.1°F units)
	// Default: 50 (5°F) - set this to reduce battery drain from frequent wake-ups
	TemperatureReportThreshold SensorThresholdConfig `yaml:"temperature_report_threshold"`

	// LowTemperatureAlert is the threshold for freezing temperature alerts
	// Default: 32°F - for pipe freeze protection
	LowTemperatureAlert SensorThresholdConfig `yaml:"low_temperature_alert"`

	// BatteryReportThreshold is the threshold for battery level change reporting
	// Default: 5% - lower threshold improves staleness detection
	BatteryReportThreshold SensorThresholdConfig `yaml:"battery_report_threshold"`
}

// SensorThresholdConfig defines entities and the value to set
type SensorThresholdConfig struct {
	// Value is the threshold value to set
	Value float64 `yaml:"value"`

	// Entities is the list of Home Assistant entity_ids to configure
	Entities []string `yaml:"entities"`
}

// LoadConfig loads the sensor configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", path, err)
	}

	return &config, nil
}
