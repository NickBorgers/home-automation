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

// IndicatorLightsConfig represents the configuration for energy indicator lights
type IndicatorLightsConfig struct {
	// FriendlyNamePattern is a regex to match entities by friendly_name attribute
	// Default: "Radar" (matches Apollo MTR sensors which have "Radar" in their names)
	FriendlyNamePattern string `yaml:"friendly_name_pattern"`
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
