package notify

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config governs Notifier behavior.
type Config struct {
	// DefaultSpeakers are used when a caller does not pass WithSpeakers.
	DefaultSpeakers []string `yaml:"default_speakers"`

	// AwakeVolumePercent is the speaker volume (0-100) the announcement is
	// played at. It is passed to media_player.play_media as extra.volume so
	// the Sonos integration can duck/play at this level and then restore the
	// speaker's prior volume when the announcement ends. Announcements while
	// master is asleep either play at this volume (UrgencyUrgent) or are
	// dropped entirely (UrgencyDeferable) — there is no quieter mid-tier.
	AwakeVolumePercent int `yaml:"awake_volume_percent"`
}

type fileShape struct {
	Notification Config `yaml:"notification"`
}

const (
	defaultAwakeVolumePercent = 60
)

// defaultSpeakers is the union of speaker lists currently used by plugins.
// Used when no notification_config.yaml is present and no plugin override is given.
var defaultSpeakers = []string{
	"media_player.kitchen",
	"media_player.front_room",
	"media_player.sitting_room",
	"media_player.bedroom",
	"media_player.dining_room",
	"media_player.kids_bathroom",
}

// LoadConfig reads the notifier config from a YAML file. Missing fields fall
// back to safe defaults so a partially-specified config still works.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read notification config: %w", err)
	}
	var f fileShape
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse notification config: %w", err)
	}
	cfg := f.Notification
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// DefaultConfig returns a Config populated with safe defaults. Used when no
// notification_config.yaml file is present.
func DefaultConfig() Config {
	c := Config{}
	c.applyDefaults()
	return c
}

func (c *Config) applyDefaults() {
	if len(c.DefaultSpeakers) == 0 {
		c.DefaultSpeakers = append([]string(nil), defaultSpeakers...)
	}
	if c.AwakeVolumePercent == 0 {
		c.AwakeVolumePercent = defaultAwakeVolumePercent
	}
}

func (c *Config) validate() error {
	if c.AwakeVolumePercent < 0 || c.AwakeVolumePercent > 100 {
		return fmt.Errorf("awake_volume_percent must be 0-100, got %d", c.AwakeVolumePercent)
	}
	return nil
}
