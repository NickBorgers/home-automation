package music

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MusicConfig represents the music configuration structure
type MusicConfig struct {
	Music map[string]MusicMode `yaml:"music"`
}

// MusicMode represents a specific music mode (morning, day, evening, etc.)
type MusicMode struct {
	Participants    []Participant    `yaml:"participants"`
	PlaybackOptions []PlaybackOption `yaml:"playback_options"`
}

// Participant represents a Sonos speaker configuration for a music mode
type Participant struct {
	PlayerName   string          `yaml:"player_name"`
	BaseVolume   int             `yaml:"base_volume"`
	LeaveMutedIf []MuteCondition `yaml:"leave_muted_if"`
	ExcludeIf    []MuteCondition `yaml:"exclude_if"` // Phase 1: Zone exclusion conditions
}

// MuteCondition represents a condition under which a speaker should be muted
type MuteCondition struct {
	Variable string      `yaml:"variable"`
	Value    interface{} `yaml:"value"`
}

// PlaybackOption represents a specific playlist or media to play
type PlaybackOption struct {
	URI              string  `yaml:"uri"`
	MediaType        string  `yaml:"media_type"`
	VolumeMultiplier float64 `yaml:"volume_multiplier"`
}

// LoadConfig loads the music configuration from a YAML file
func LoadConfig(path string) (*MusicConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read music config file: %w", err)
	}

	var config MusicConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse music config: %w", err)
	}

	// Validate that we have all expected modes
	expectedModes := []string{"morning", "day", "evening", "winddown", "sleep", "sex", "wakeup"}
	for _, mode := range expectedModes {
		if _, ok := config.Music[mode]; !ok {
			return nil, fmt.Errorf("missing required music mode: %s", mode)
		}
	}

	return &config, nil
}
