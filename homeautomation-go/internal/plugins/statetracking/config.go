package statetracking

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigFileName is the optional state tracking config file in the config directory.
const ConfigFileName = "state_tracking_config.yaml"

// Config is the state_tracking_config.yaml structure. Every field is optional.
type Config struct {
	// MasterBedroomDoorSensor is a binary_sensor entity on the master bedroom door
	// (on = open). Opening it in the morning day phase clears isMasterAsleep.
	// Leave it empty to turn door wake off.
	MasterBedroomDoorSensor string `yaml:"master_bedroom_door_sensor"`
}

// LoadConfig reads the config file. A missing file gives an empty config.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state tracking config: %w", err)
	}

	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse state tracking config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate returns the first problem found in the config.
func (c *Config) Validate() error {
	s := c.MasterBedroomDoorSensor
	if s != "" && (!strings.HasPrefix(s, "binary_sensor.") || len(s) == len("binary_sensor.")) {
		return fmt.Errorf("master_bedroom_door_sensor: %q is not a binary_sensor entity ID", s)
	}
	return nil
}
