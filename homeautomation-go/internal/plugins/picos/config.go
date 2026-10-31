// Package picos turns Lutron Pico remotes into dimmers for groups of lights.
//
// A Pico controls nothing inside Lutron. It fires a lutron_caseta_button_event in Home Assistant,
// and this plugin reacts by stepping the brightness of the lights its target names.
package picos

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Defaults for the step behavior.
const (
	defaultOnPct             = 30
	defaultStepPct           = 15
	defaultOffAtOrBelowPct   = 30
	defaultTransitionSeconds = 0.5
	defaultRecentSeconds     = 5
)

// Config is the top-level YAML structure.
type Config struct {
	// OnPct is the brightness that an "up" press sets when the lights are off.
	OnPct int `yaml:"on_pct"`

	// StepPct is how far each press moves the brightness.
	StepPct int `yaml:"step_pct"`

	// OffAtOrBelowPct is the brightness at or below which a "down" press turns the lights off.
	OffAtOrBelowPct int `yaml:"off_at_or_below_pct"`

	// TransitionSeconds is the fade time sent with each command.
	TransitionSeconds float64 `yaml:"transition_seconds"`

	// RecentSeconds is how long a press trusts its own last command over the light's reported
	// state. Hue and Lutron report a new level a moment late, so a fast second press would
	// otherwise step from the old level.
	RecentSeconds int `yaml:"recent_seconds"`

	// Targets are the groups of lights a button can step.
	Targets []Target `yaml:"targets"`

	// Buttons are the Pico devices, each pointed at one target.
	Buttons []Button `yaml:"buttons"`
}

// Target is a named group of lights that step together.
type Target struct {
	Name string `yaml:"name"`

	// Lights are light entity IDs. The first light is the reference: its brightness decides the
	// next level, and every other light is set to that level.
	Lights []string `yaml:"lights"`
}

// Button is one Pico device.
type Button struct {
	// Name is a label for logs and shadow state.
	Name string `yaml:"name"`

	// DeviceID is the Home Assistant device ID. It appears as device_id in the button event.
	DeviceID string `yaml:"device_id"`

	// Target is the name of the target this Pico steps.
	Target string `yaml:"target"`
}

// LoadConfig reads, defaults, and validates a picos config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse picos config: %w", err)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.OnPct == 0 {
		c.OnPct = defaultOnPct
	}
	if c.StepPct == 0 {
		c.StepPct = defaultStepPct
	}
	if c.OffAtOrBelowPct == 0 {
		c.OffAtOrBelowPct = defaultOffAtOrBelowPct
	}
	if c.TransitionSeconds == 0 {
		c.TransitionSeconds = defaultTransitionSeconds
	}
	if c.RecentSeconds == 0 {
		c.RecentSeconds = defaultRecentSeconds
	}
}

// Validate returns the first problem found in the config.
func (c *Config) Validate() error {
	if c.OnPct < 1 || c.OnPct > 100 {
		return fmt.Errorf("on_pct must be 1-100, got %d", c.OnPct)
	}
	if c.StepPct < 1 || c.StepPct > 100 {
		return fmt.Errorf("step_pct must be 1-100, got %d", c.StepPct)
	}
	if c.OffAtOrBelowPct < 0 || c.OffAtOrBelowPct > 100 {
		return fmt.Errorf("off_at_or_below_pct must be 0-100, got %d", c.OffAtOrBelowPct)
	}
	if c.TransitionSeconds < 0 {
		return fmt.Errorf("transition_seconds must not be negative, got %v", c.TransitionSeconds)
	}

	targets := make(map[string]bool, len(c.Targets))
	for i, t := range c.Targets {
		if t.Name == "" {
			return fmt.Errorf("targets[%d]: name is required", i)
		}
		if targets[t.Name] {
			return fmt.Errorf("targets[%d]: duplicate name %q", i, t.Name)
		}
		if len(t.Lights) == 0 {
			return fmt.Errorf("target %q: at least one light is required", t.Name)
		}
		targets[t.Name] = true
	}

	devices := make(map[string]bool, len(c.Buttons))
	for i, b := range c.Buttons {
		if b.DeviceID == "" {
			return fmt.Errorf("buttons[%d] (%s): device_id is required", i, b.Name)
		}
		if devices[b.DeviceID] {
			return fmt.Errorf("buttons[%d] (%s): duplicate device_id %q", i, b.Name, b.DeviceID)
		}
		if !targets[b.Target] {
			return fmt.Errorf("buttons[%d] (%s): unknown target %q", i, b.Name, b.Target)
		}
		devices[b.DeviceID] = true
	}
	return nil
}
