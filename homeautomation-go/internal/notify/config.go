package notify

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config governs Notifier behavior.
type Config struct {
	// DefaultSpeakers are used when a caller does not pass WithSpeakers.
	DefaultSpeakers []string `yaml:"default_speakers"`

	// AwakeVolumePercent is the speaker volume (0-100) used while master is awake.
	AwakeVolumePercent int `yaml:"awake_volume_percent"`

	// AsleepVolumePercent is the speaker volume (0-100) used for routine
	// announcements while master is asleep. Urgent announcements ignore this
	// and use AwakeVolumePercent.
	AsleepVolumePercent int `yaml:"asleep_volume_percent"`

	// TTSDomain / TTSService identify the Home Assistant service to call.
	// Defaults to tts.speak.
	TTSDomain  string `yaml:"tts_domain"`
	TTSService string `yaml:"tts_service"`

	// TTSEntityID is the TTS engine to use (e.g. tts.google_translate_en_com).
	TTSEntityID string `yaml:"tts_entity_id"`

	// SnapshotRestore controls whether speaker volume is captured before the
	// announcement and restored after RestoreDelay elapses. Set to false in
	// the YAML to disable (e.g. for tests).
	SnapshotRestore bool `yaml:"snapshot_restore"`

	// RestoreDelaySeconds is how long to wait after the TTS service call
	// returns before restoring speaker volumes. Sized to cover typical
	// announcement playback duration.
	RestoreDelaySeconds int `yaml:"restore_delay_seconds"`

	// RestoreDelay is the parsed RestoreDelaySeconds. Populated by applyDefaults.
	RestoreDelay time.Duration `yaml:"-"`
}

type fileShape struct {
	Notification Config `yaml:"notification"`
}

const (
	defaultAwakeVolumePercent  = 60
	defaultAsleepVolumePercent = 30
	defaultTTSDomain           = "tts"
	defaultTTSService          = "speak"
	defaultTTSEntityID         = "tts.google_translate_en_com"
	defaultRestoreDelaySeconds = 8
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
	c := Config{SnapshotRestore: true}
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
	if c.AsleepVolumePercent == 0 {
		c.AsleepVolumePercent = defaultAsleepVolumePercent
	}
	if c.TTSDomain == "" {
		c.TTSDomain = defaultTTSDomain
	}
	if c.TTSService == "" {
		c.TTSService = defaultTTSService
	}
	if c.TTSEntityID == "" {
		c.TTSEntityID = defaultTTSEntityID
	}
	if c.RestoreDelaySeconds == 0 {
		c.RestoreDelaySeconds = defaultRestoreDelaySeconds
	}
	c.RestoreDelay = time.Duration(c.RestoreDelaySeconds) * time.Second
}

func (c *Config) validate() error {
	if c.AwakeVolumePercent < 0 || c.AwakeVolumePercent > 100 {
		return fmt.Errorf("awake_volume_percent must be 0-100, got %d", c.AwakeVolumePercent)
	}
	if c.AsleepVolumePercent < 0 || c.AsleepVolumePercent > 100 {
		return fmt.Errorf("asleep_volume_percent must be 0-100, got %d", c.AsleepVolumePercent)
	}
	if c.RestoreDelaySeconds < 0 {
		return fmt.Errorf("restore_delay_seconds must be non-negative, got %d", c.RestoreDelaySeconds)
	}
	return nil
}
