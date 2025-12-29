package energy

import (
	"os"

	"gopkg.in/yaml.v3"
)

// FreeEnergyTime represents the time range for free energy
type FreeEnergyTime struct {
	Start string `yaml:"start"` // Format: "21:00"
	End   string `yaml:"end"`   // Format: "07:00"
}

// EnergyState represents a single energy state level
type EnergyState struct {
	ConditionName                       string      `yaml:"condition_name"`
	BatteryMinimumPercentage            float64     `yaml:"battery_minimum_percentage"`
	EnergyProductionMinimumKW           float64     `yaml:"energy_production_minimum_kw"`
	RemainingEnergyProductionMinimumKWH float64     `yaml:"remaining_energy_production_minimum_kwh"`
	LightConfig                         LightConfig `yaml:"light_config"`
}

// LightConfig represents the light configuration for an energy state
type LightConfig struct {
	Red           int `yaml:"red"`
	Green         int `yaml:"green"`
	Blue          int `yaml:"blue"`
	BrightnessPct int `yaml:"brightness_pct"`
}

// BrightnessCurvePoint defines a single point on the lux-to-brightness curve
type BrightnessCurvePoint struct {
	LuxMax        float64 `yaml:"lux_max"`
	BrightnessPct int     `yaml:"brightness_pct"`
}

// AdaptiveBrightnessConfig contains settings for lux-based adaptive brightness
type AdaptiveBrightnessConfig struct {
	// Enabled toggles adaptive brightness (default: false for backward compatibility)
	Enabled bool `yaml:"enabled"`

	// LuxSensorPattern is a substring to match lux sensor entities by entity_id
	// Default: "ltr390_light" (matches Apollo MSR-2 light sensors)
	LuxSensorPattern string `yaml:"lux_sensor_pattern"`

	// BrightnessCurve defines the lux-to-brightness mapping (must be sorted by LuxMax ascending)
	// If empty, uses default curve: 10->20%, 100->40%, 500->60%, 1000->80%
	BrightnessCurve []BrightnessCurvePoint `yaml:"brightness_curve"`

	// DebounceDurationSec is the minimum time between brightness updates per device
	// Default: 5 seconds
	DebounceDurationSec int `yaml:"debounce_duration_sec"`

	// HysteresisPercent is the percentage band around thresholds to prevent oscillation
	// Default: 10 (meaning 10% above/below threshold before changing)
	HysteresisPercent int `yaml:"hysteresis_percent"`
}

// IndicatorLightsConfig represents the configuration for energy indicator lights
type IndicatorLightsConfig struct {
	// FriendlyNamePattern is a regex to match entities by friendly_name attribute
	// Default: "Radar" (matches Apollo MTR sensors which have "Radar" in their names)
	FriendlyNamePattern string `yaml:"friendly_name_pattern"`

	// AdaptiveBrightness configures lux-based brightness adjustment
	AdaptiveBrightness AdaptiveBrightnessConfig `yaml:"adaptive_brightness"`
}

// EnergyConfig represents the energy configuration
type EnergyConfig struct {
	Energy struct {
		FreeEnergyTime  FreeEnergyTime        `yaml:"free_energy_time"`
		IndicatorLights IndicatorLightsConfig `yaml:"indicator_lights"`
		EnergyStates    []EnergyState         `yaml:"energy_states"`
	} `yaml:"energy"`
}

// LoadConfig loads the energy configuration from a YAML file
func LoadConfig(path string) (*EnergyConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config EnergyConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
