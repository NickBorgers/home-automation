package lighting

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// LightingCondition represents a single condition for controlling a room's lighting
// Conditions are evaluated in order - the first matching condition wins
type LightingCondition struct {
	Action   string      `yaml:"action"`   // "on" or "off"
	Variable string      `yaml:"variable"` // state variable name to check
	Value    interface{} `yaml:"value"`    // expected value for the condition to match
}

// RoomConfig represents the configuration for a single room/area
type RoomConfig struct {
	HueGroup                 string              `yaml:"hue_group"`
	HASSAreaID               string              `yaml:"hass_area_id"`
	Conditions               []LightingCondition `yaml:"conditions"`                  // Ordered list of conditions (first match wins)
	IncreaseBrightnessIfTrue interface{}         `yaml:"increase_brightness_if_true"` // Can be string or []string
	TransitionSeconds        *int                `yaml:"transition_seconds"`          // Pointer to handle nil/~ values
	// SkipReactivationWhenOn opts the room into edge-triggered scene activation.
	// When true and the room's last applied action was already "on", a repeat "on"
	// evaluation from an occupancy/condition trigger is skipped — preserving any
	// manual brightness adjustments. Day phase and global triggers still re-fire.
	// Used by rooms with unreliable presence sensors (e.g. mmWave radar).
	SkipReactivationWhenOn bool `yaml:"skip_reactivation_when_on"`
}

// GetConditionVariables returns a list of all unique state variable names used in conditions
func (r *RoomConfig) GetConditionVariables() []string {
	seen := make(map[string]bool)
	result := make([]string, 0)

	for _, cond := range r.Conditions {
		if cond.Variable != "" && !seen[cond.Variable] {
			seen[cond.Variable] = true
			result = append(result, cond.Variable)
		}
	}

	return result
}

// GetIncreaseBrightnessIfTrueConditions returns the list of increase_brightness_if_true conditions
func (r *RoomConfig) GetIncreaseBrightnessIfTrueConditions() []string {
	return interfaceToStringSlice(r.IncreaseBrightnessIfTrue)
}

// interfaceToStringSlice converts an interface{} that can be string, []string, or nil to []string
func interfaceToStringSlice(val interface{}) []string {
	if val == nil {
		return []string{}
	}

	switch v := val.(type) {
	case string:
		if v == "" {
			return []string{}
		}
		return []string{v}
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok && str != "" {
				result = append(result, str)
			}
		}
		return result
	case []string:
		return v
	default:
		return []string{}
	}
}

// valuesMatch compares two interface{} values for equality using string representation
// This matches the pattern used in the music plugin for flexible type comparison
func valuesMatch(a, b interface{}) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

// HueConfig represents the Hue lighting configuration
type HueConfig struct {
	Rooms []RoomConfig `yaml:"rooms"`
}

// LoadConfig loads the Hue configuration from a YAML file
func LoadConfig(path string) (*HueConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config HueConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}
