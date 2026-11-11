// Package picos turns Lutron Pico remotes into dimmers for groups of lights.
//
// A Pico controls nothing inside Lutron. It fires a lutron_caseta_button_event in Home Assistant,
// and this plugin reacts by stepping the brightness of the lights its target names.
package picos

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Limits for path lights.
const (
	minPathKelvin = 1000
	maxPathKelvin = 10000
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

	// WakesHouse marks a main-living-space target. An up-press that turns it on from off while
	// someone is asleep sets isEarlyWakeActive: the person pressing is up for the day.
	WakesHouse bool `yaml:"wakes_house"`
}

// Button is one Pico device.
type Button struct {
	// Name is a label for logs and shadow state.
	Name string `yaml:"name"`

	// DeviceID is the Home Assistant device ID. It appears as device_id in the button event.
	DeviceID string `yaml:"device_id"`

	// Target is the name of the target this Pico steps.
	Target string `yaml:"target"`

	// PathLights, if set, light a dim night path on an up-press while the house is asleep.
	PathLights *PathLights `yaml:"path_lights"`
}

// PathLights are extra lights a Pico turns on, dim and warm, for a walk through a sleeping house.
//
// On an up-press while isAnyoneHomeAndAwake is false, each listed light that is off turns on at
// BrightnessPct and Kelvin. A down-press that turns the target off turns off the path lights this
// plugin turned on, and OffAfter turns them off if nobody does. Lights that were already on are
// never touched. Path lights never set isEarlyWakeActive.
type PathLights struct {
	// Lights are light entity IDs.
	Lights []string `yaml:"lights"`

	// BrightnessPct is the brightness (1-100) the path lights turn on at.
	BrightnessPct int `yaml:"brightness_pct"`

	// Kelvin is the color temperature the path lights turn on at.
	Kelvin int `yaml:"kelvin"`

	// OffAfter turns the path lights off if no down-press does it first. Required.
	OffAfter time.Duration `yaml:"off_after"`
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
		if b.PathLights != nil {
			if err := b.PathLights.validate(c.target(b.Target)); err != nil {
				return fmt.Errorf("buttons[%d] (%s): path_lights: %w", i, b.Name, err)
			}
		}
		devices[b.DeviceID] = true
	}
	return nil
}

// target returns the target with the given name. The name must exist.
func (c *Config) target(name string) Target {
	for _, t := range c.Targets {
		if t.Name == name {
			return t
		}
	}
	return Target{}
}

// validate checks a path light block against the target its button steps.
func (p *PathLights) validate(target Target) error {
	if len(p.Lights) == 0 {
		return fmt.Errorf("at least one light is required")
	}
	stepped := make(map[string]bool, len(target.Lights))
	for _, l := range target.Lights {
		stepped[l] = true
	}
	seen := make(map[string]bool, len(p.Lights))
	for _, l := range p.Lights {
		if !strings.HasPrefix(l, "light.") || len(l) == len("light.") {
			return fmt.Errorf("%q is not a light entity ID", l)
		}
		if seen[l] {
			return fmt.Errorf("duplicate light %q", l)
		}
		if stepped[l] {
			return fmt.Errorf("%q is also a light of target %q", l, target.Name)
		}
		seen[l] = true
	}
	if p.BrightnessPct < 1 || p.BrightnessPct > 100 {
		return fmt.Errorf("brightness_pct must be 1-100, got %d", p.BrightnessPct)
	}
	if p.Kelvin < minPathKelvin || p.Kelvin > maxPathKelvin {
		return fmt.Errorf("kelvin must be %d-%d, got %d", minPathKelvin, maxPathKelvin, p.Kelvin)
	}
	if p.OffAfter <= 0 {
		return fmt.Errorf("off_after must be positive, got %v", p.OffAfter)
	}
	return nil
}
