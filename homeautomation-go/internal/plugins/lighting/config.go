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

	// HasDynamics opts the room into two-step scene recall for Hue scenes that
	// have `auto_dynamic: true` (an animated palette). The bridge can fail to
	// start the dynamic palette when bulbs are mid-transition or cold; each
	// affected bulb reverts to its pre-recall state. Two-step recall uses
	// hue.activate_scene first with `dynamic: false` (lights settle at the
	// scene's static colors), waits briefly, then sends it again with
	// `dynamic: true` so the palette starts from a stable known state.
	HasDynamics bool `yaml:"has_dynamics"`

	// CompanionScenes lists extra scene base names to activate right after the
	// room's main scene, for the same day phase. Each entry becomes the entity ID
	// `scene.` + snake_case(entry + " " + dayPhase), e.g. "Kitchen Under Cabinet"
	// in phase "morning" gives scene.kitchen_under_cabinet_morning. Companions use
	// the same transition as the main scene and never undo it if they fail.
	CompanionScenes []string `yaml:"companion_scenes"`

	// LuxControl opts the room's ceiling cans into lux-target control.
	// See docs/flows/LUX_LIGHTING.md. Nil means the room stays on scenes.
	LuxControl *LuxControlConfig `yaml:"lux_control"`
}

// Lux control modes. In log_only mode the plugin computes and records what it would
// send, but scenes still drive the room. In active mode the plugin drives the cans.
const (
	LuxModeLogOnly = "log_only"
	LuxModeActive  = "active"
)

// LuxControlConfig configures lux-target control for one room.
type LuxControlConfig struct {
	Mode             string             `yaml:"mode"`               // log_only | active
	Cans             string             `yaml:"cans"`               // light entity for the ceiling cans
	AccentSceneGroup string             `yaml:"accent_scene_group"` // Hue group whose per-phase scenes drive accents (empty: no accent lights)
	DaylightFactor   float64            `yaml:"daylight_factor"`    // share of outdoor lux that reaches the room
	LuxScale         float64            `yaml:"lux_scale"`          // scales the whole curve for this room (0 means 1)
	GuestFactor      float64            `yaml:"guest_factor"`       // lux multiplier when increase_brightness_if_true holds (0 means 1.5)
	Calibrated       bool               `yaml:"calibrated"`         // true once calibration values were measured
	Calibration      []CalibrationPoint `yaml:"calibration"`        // cans-only lux at night, by brightness
}

// CalibrationPoint is the lux the cans give at one brightness, measured at night with no daylight.
type CalibrationPoint struct {
	BrightnessPct int     `yaml:"brightness_pct"`
	Lux           float64 `yaml:"lux"`
}

// LuxCurvePoint is one point of the daily target curve.
type LuxCurvePoint struct {
	Anchor string  `yaml:"anchor"` // sun.<event> or schedule.<time>
	Lux    float64 `yaml:"lux"`
	Kelvin int     `yaml:"kelvin"`
}

// LuxCurveConfig is the daily target curve shared by all lux-controlled rooms.
type LuxCurveConfig struct {
	Points           []LuxCurvePoint `yaml:"points"`
	OutdoorSensor    string          `yaml:"outdoor_sensor"`
	SmoothingMinutes int             `yaml:"smoothing_minutes"`
	TickSeconds      int             `yaml:"tick_seconds"`
}

// validateLux checks the curve and every lux-controlled room. It returns the first problem.
func (c *HueConfig) validateLux() error {
	controlled := false
	for _, r := range c.Rooms {
		if r.LuxControl != nil {
			controlled = true
			break
		}
	}
	if !controlled {
		return nil
	}
	if c.LuxCurve == nil || len(c.LuxCurve.Points) == 0 {
		return fmt.Errorf("lux_control is set on a room but lux_curve has no points")
	}
	if c.LuxCurve.OutdoorSensor == "" {
		return fmt.Errorf("lux_curve.outdoor_sensor is required")
	}
	for _, p := range c.LuxCurve.Points {
		if p.Anchor == "" || p.Lux <= 0 || p.Kelvin <= 0 {
			return fmt.Errorf("lux_curve point %q needs an anchor, lux > 0 and kelvin > 0", p.Anchor)
		}
	}
	for _, r := range c.Rooms {
		lc := r.LuxControl
		if lc == nil {
			continue
		}
		if lc.Mode != LuxModeLogOnly && lc.Mode != LuxModeActive {
			return fmt.Errorf("room %q: lux_control.mode must be %q or %q", r.HueGroup, LuxModeLogOnly, LuxModeActive)
		}
		if lc.Cans == "" || lc.DaylightFactor < 0 {
			return fmt.Errorf("room %q: lux_control needs cans and a non-negative daylight_factor", r.HueGroup)
		}
		prevPct, prevLux := 0, 0.0
		for _, cp := range lc.Calibration {
			if cp.BrightnessPct <= prevPct || cp.BrightnessPct > 100 || cp.Lux < prevLux {
				return fmt.Errorf("room %q: calibration must rise in brightness_pct (1-100) and lux", r.HueGroup)
			}
			prevPct, prevLux = cp.BrightnessPct, cp.Lux
		}
		if lc.Mode == LuxModeActive {
			if !lc.Calibrated || len(lc.Calibration) == 0 {
				return fmt.Errorf("room %q: active mode needs calibrated: true and a calibration table", r.HueGroup)
			}
		}
	}
	return nil
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
	Rooms    []RoomConfig    `yaml:"rooms"`
	LuxCurve *LuxCurveConfig `yaml:"lux_curve"`
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

	if err := config.validateLux(); err != nil {
		return nil, fmt.Errorf("invalid lux config: %w", err)
	}

	return &config, nil
}
