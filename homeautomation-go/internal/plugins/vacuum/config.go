package vacuum

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config models the vacuum plugin's YAML configuration.
//
// The announcement subtrees drive the error announcer. The scheduler subtree
// drives the dirty-room scheduler (see scheduler_config.go). entity_id is
// still reserved.
type Config struct {
	Vacuum struct {
		// EntityID is reserved for future use (start/pause/stop service calls).
		EntityID      string `yaml:"entity_id"`
		ErrorSensorID string `yaml:"error_sensor_id"`
		NoErrorValue  string `yaml:"no_error_value"`

		Announcement      AnnouncementConfig      `yaml:"announcement"`
		ClearAnnouncement ClearAnnouncementConfig `yaml:"clear_announcement"`

		// Scheduler is the dirty-room scheduler. Problems in it are kept in
		// Scheduler.Err and do not fail LoadConfig.
		Scheduler SchedulerConfig `yaml:"scheduler"`
	} `yaml:"vacuum"`
}

// AnnouncementConfig governs error TTS behavior.
type AnnouncementConfig struct {
	// RepeatInterval is parsed from a Go duration string (e.g. "2h").
	RepeatIntervalRaw string `yaml:"repeat_interval"`

	MessagePrefix string   `yaml:"message_prefix"`
	Speakers      []string `yaml:"speakers"`

	// RepeatInterval holds the parsed duration. Populated by LoadConfig.
	RepeatInterval time.Duration `yaml:"-"`
}

// ClearAnnouncementConfig governs the TTS confirmation sent when an error clears.
type ClearAnnouncementConfig struct {
	Message  string   `yaml:"message"`
	Speakers []string `yaml:"speakers"`
}

// Default values applied when fields are missing from the YAML file.
const (
	defaultErrorSensorID        = "sensor.valetudo_mellowslimyloris_error"
	defaultNoErrorValue         = "No error"
	defaultRepeatInterval       = 2 * time.Hour
	defaultMessagePrefix        = "Casper needs attention"
	defaultClearMessage         = "You have satisfied Casper"
	defaultClearSpeakerEntityID = "media_player.sitting"
)

var defaultSpeakers = []string{
	"media_player.lounge",
	"media_player.sitting",
	"media_player.primary_bed",
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
	if c.Vacuum.ClearAnnouncement.Message == "" {
		c.Vacuum.ClearAnnouncement.Message = defaultClearMessage
	}
	if len(c.Vacuum.ClearAnnouncement.Speakers) == 0 {
		c.Vacuum.ClearAnnouncement.Speakers = []string{defaultClearSpeakerEntityID}
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
	c.Vacuum.Scheduler.applyDefaultsAndValidate()
	return nil
}
