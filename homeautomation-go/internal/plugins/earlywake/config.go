// Package earlywake detects that someone got up while a bedroom still sleeps.
//
// A person turning on a main-living-space light by hand while someone is asleep
// sets isEarlyWakeActive. That latches isAnyoneHomeAndAwake (see
// state.WakeSequenceLatch), so the common areas wake up while the bedroom keeps
// sleeping. A normal wake (isAnyoneAsleep true->false) clears it.
//
// "By hand" is read from the Home Assistant context of the state change. The
// app's own commands carry the app's user ID (auth/current_user). Automation
// changes carry a parent ID. The Hue app, a wall switch, HomeKit, voice and the
// HA dashboard carry neither, or a person's user ID.
package earlywake

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the early_wake_config.yaml structure.
type Config struct {
	// WakeLights are light entity IDs whose manual turn-on wakes the house.
	// The night path to the bedroom (the pantry) must not be listed.
	WakeLights []string `yaml:"wake_lights"`
}

// LoadConfig reads and validates an early wake config file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read early wake config: %w", err)
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse early wake config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate returns the first problem found in the config.
func (c *Config) Validate() error {
	if len(c.WakeLights) == 0 {
		return fmt.Errorf("wake_lights: at least one light is required")
	}
	seen := make(map[string]bool, len(c.WakeLights))
	for i, l := range c.WakeLights {
		if !strings.HasPrefix(l, "light.") || len(l) == len("light.") {
			return fmt.Errorf("wake_lights[%d]: %q is not a light entity ID", i, l)
		}
		if seen[l] {
			return fmt.Errorf("wake_lights[%d]: duplicate light %q", i, l)
		}
		seen[l] = true
	}
	return nil
}
