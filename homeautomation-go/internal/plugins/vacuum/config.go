package vacuum

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config models the vacuum plugin's YAML configuration.
//
// The file is intentionally shaped to grow: today only the announcement subtree
// is consumed, but schedule/rooms/entity_id fields are reserved for future
// plugin features (vacuum triggering, per-room parameters).
type Config struct {
	Vacuum struct {
		// EntityID is reserved for future use (start/pause/stop service calls).
		EntityID      string `yaml:"entity_id"`
		ErrorSensorID string `yaml:"error_sensor_id"`
		NoErrorValue  string `yaml:"no_error_value"`

		Announcement AnnouncementConfig `yaml:"announcement"`
	} `yaml:"vacuum"`
}

// AnnouncementConfig governs error TTS behavior.
type AnnouncementConfig struct {
	// RepeatInterval is parsed from a Go duration string (e.g. "2h").
	RepeatIntervalRaw string `yaml:"repeat_interval"`

	SuppressWhileMasterAsleep bool     `yaml:"suppress_while_master_asleep"`
	MessagePrefix             string   `yaml:"message_prefix"`
	Speakers                  []string `yaml:"speakers"`

	// RepeatInterval holds the parsed duration. Populated by LoadConfig.
	RepeatInterval time.Duration `yaml:"-"`
}

// Default values applied when fields are missing from the YAML file.
const (
	defaultErrorSensorID  = "sensor.valetudo_mellowslimyloris_error"
	defaultNoErrorValue   = "No error"
	defaultRepeatInterval = 2 * time.Hour
	defaultMessagePrefix  = "Robot vacuum needs attention"
)

var defaultSpeakers = []string{
	"media_player.kitchen",
	"media_player.sitting_room",
	"media_player.front_room",
	"media_player.kids_bathroom",
}

// LoadConfig reads the vacuum plugin config from a YAML file. Missing fields
// fall back to safe defaults so a partially-specified config still works.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vacuum config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse vacuum config: %w", err)
	}

	if err := cfg.applyDefaultsAndValidate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaultsAndValidate() error {
	if c.Vacuum.ErrorSensorID == "" {
		c.Vacuum.ErrorSensorID = defaultErrorSensorID
	}
	if c.Vacuum.NoErrorValue == "" {
		c.Vacuum.NoErrorValue = defaultNoErrorValue
	}
	if c.Vacuum.Announcement.MessagePrefix == "" {
		c.Vacuum.Announcement.MessagePrefix = defaultMessagePrefix
	}
	if len(c.Vacuum.Announcement.Speakers) == 0 {
		c.Vacuum.Announcement.Speakers = append([]string(nil), defaultSpeakers...)
	}

	if c.Vacuum.Announcement.RepeatIntervalRaw == "" {
		c.Vacuum.Announcement.RepeatInterval = defaultRepeatInterval
	} else {
		d, err := time.ParseDuration(c.Vacuum.Announcement.RepeatIntervalRaw)
		if err != nil {
			return fmt.Errorf("parse vacuum.announcement.repeat_interval %q: %w",
				c.Vacuum.Announcement.RepeatIntervalRaw, err)
		}
		if d <= 0 {
			return fmt.Errorf("vacuum.announcement.repeat_interval must be positive, got %s", d)
		}
		c.Vacuum.Announcement.RepeatInterval = d
	}
	return nil
}
