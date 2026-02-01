package music

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MusicConfig represents the music configuration structure
type MusicConfig struct {
	Music map[string]MusicMode `yaml:"music"`
	Zones []ZoneConfig         `yaml:"zones,omitempty"` // Phase 2: Zone definitions
}

// ZoneConfig represents a zone definition for multi-zone playback
type ZoneConfig struct {
	Name          string             `yaml:"name"`
	Priority      int                `yaml:"priority"`
	Triggers      []TriggerCondition `yaml:"trigger,omitempty"`        // Legacy: AND logic between conditions
	TriggerGroups []TriggerGroup     `yaml:"trigger_groups,omitempty"` // New: OR between groups, AND within each
	Default       bool               `yaml:"default,omitempty"`        // Fallback zone when no triggers match
}

// TriggerGroup represents a group of trigger conditions that are ANDed together.
// Multiple TriggerGroups in a zone are ORed - any matching group activates the zone.
type TriggerGroup struct {
	Triggers []TriggerCondition `yaml:"triggers"` // AND within group
}

// TriggerCondition represents a condition that activates a zone
type TriggerCondition struct {
	Variable string      `yaml:"variable"`
	Value    interface{} `yaml:"value"`
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

// HasZones returns true if explicit zone definitions are present
func (c *MusicConfig) HasZones() bool {
	return len(c.Zones) > 0
}

// GetZones returns the zone configurations, generating implicit zones if none defined
func (c *MusicConfig) GetZones() []ZoneConfig {
	if c.HasZones() {
		return c.Zones
	}
	return c.getImplicitZones()
}

// getImplicitZones generates zone configs from music modes for backward compatibility
// This maintains existing behavior when no explicit zones are defined
func (c *MusicConfig) getImplicitZones() []ZoneConfig {
	// Priority map matches existing selectAppropriateMusicMode logic:
	// - sleep has highest priority (isAnyoneAsleep check happens first)
	// - morning/day/evening/winddown are day-phase based
	// - sex and wakeup are manually triggered
	priorityMap := map[string]int{
		"sleep":    100, // Highest - isAnyoneAsleep check happens first
		"wakeup":   90,  // Wake sequence
		"sex":      80,  // Manual override
		"morning":  50,  // Day phase based
		"day":      40,
		"evening":  40,
		"winddown": 40,
	}

	zones := make([]ZoneConfig, 0, len(c.Music))
	for musicType := range c.Music {
		priority := priorityMap[musicType]
		if priority == 0 {
			priority = 10 // Default for unknown modes
		}
		zones = append(zones, ZoneConfig{
			Name:     musicType,
			Priority: priority,
			Triggers: nil, // No triggers = activated by musicPlaybackType
		})
	}
	return zones
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

	// Validate zone configurations if present
	if len(config.Zones) > 0 {
		if err := config.validateZones(); err != nil {
			return nil, err
		}
	}

	return &config, nil
}

// validateZones validates zone configuration
func (c *MusicConfig) validateZones() error {
	seenNames := make(map[string]bool)
	hasDefault := false

	for i, zone := range c.Zones {
		// Check for empty name
		if zone.Name == "" {
			return fmt.Errorf("zone at index %d has empty name", i)
		}

		// Check for duplicate names
		if seenNames[zone.Name] {
			return fmt.Errorf("duplicate zone name: %s", zone.Name)
		}
		seenNames[zone.Name] = true

		// Validate zone references a valid music mode (name should match a music mode)
		if _, ok := c.Music[zone.Name]; !ok {
			return fmt.Errorf("zone '%s' does not match any music mode", zone.Name)
		}

		// Track if we have a default zone
		if zone.Default {
			if hasDefault {
				return fmt.Errorf("multiple default zones defined")
			}
			hasDefault = true
		}

		// Validate legacy trigger conditions have required fields
		for j, trigger := range zone.Triggers {
			if trigger.Variable == "" {
				return fmt.Errorf("zone '%s' trigger %d has empty variable", zone.Name, j)
			}
		}

		// Validate trigger_groups conditions have required fields
		for groupIdx, group := range zone.TriggerGroups {
			for triggerIdx, trigger := range group.Triggers {
				if trigger.Variable == "" {
					return fmt.Errorf("zone '%s' trigger_groups[%d].triggers[%d] has empty variable",
						zone.Name, groupIdx, triggerIdx)
				}
			}
		}
	}

	return nil
}
