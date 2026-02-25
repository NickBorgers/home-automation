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

// GetZones returns the zone configurations
func (c *MusicConfig) GetZones() []ZoneConfig {
	return c.Zones
}

// ensureZones populates Zones from music modes if none are defined.
// This is called during LoadConfig to ensure zones are always present,
// eliminating the dual-path branching between zone and legacy orchestration.
//
// Generated zones match the legacy selectAppropriateMusicMode logic:
//   - sleep: highest priority, triggers on isMasterAsleep=true
//   - day-phase zones: trigger on their respective dayPhase value
//   - sex/wakeup: no triggers (manually activated via musicPlaybackType)
func (c *MusicConfig) ensureZones() {
	if len(c.Zones) > 0 {
		return
	}

	// Build zones with proper triggers matching the legacy selectAppropriateMusicMode behavior.
	// This replaces the runtime branching with config-time zone generation.
	//
	// Day phase mappings from the legacy path:
	//   morning → morning (only on wake-up event; without zones this falls back to day)
	//   day → day
	//   sunset, dusk → evening (multiple dayPhase values → use trigger_groups)
	//   winddown, night → winddown (multiple dayPhase values → use trigger_groups)
	//   sleep → isAnyoneAsleep=true
	//   sex, wakeup → manually triggered via musicPlaybackType
	type zoneDef struct {
		name          string
		priority      int
		triggers      []TriggerCondition
		triggerGroups []TriggerGroup
	}

	homeAndAwake := []TriggerCondition{
		{Variable: "isAnyoneHome", Value: true},
		{Variable: "isAnyoneAsleep", Value: false},
	}

	definitions := []zoneDef{
		{
			name:     "sleep",
			priority: 100,
			triggers: []TriggerCondition{
				{Variable: "isAnyoneAsleep", Value: true},
				{Variable: "isAnyoneHome", Value: true},
				{Variable: "isWakeSequenceActive", Value: false},
			},
		},
		{
			name:     "morning",
			priority: 50,
			triggerGroups: []TriggerGroup{
				{Triggers: append([]TriggerCondition{
					{Variable: "dayPhase", Value: "morning"},
				}, homeAndAwake...)},
				{Triggers: []TriggerCondition{
					{Variable: "isWakeSequenceActive", Value: true},
					{Variable: "dayPhase", Value: "morning"},
					{Variable: "isAnyoneHome", Value: true},
				}},
			},
		},
		{
			name:     "day",
			priority: 40,
			triggers: append([]TriggerCondition{
				{Variable: "dayPhase", Value: "day"},
			}, homeAndAwake...),
		},
		{
			// Evening matches dayPhase "sunset", "dusk", or "evening"
			name:     "evening",
			priority: 40,
			triggerGroups: []TriggerGroup{
				{Triggers: append([]TriggerCondition{{Variable: "dayPhase", Value: "sunset"}}, homeAndAwake...)},
				{Triggers: append([]TriggerCondition{{Variable: "dayPhase", Value: "dusk"}}, homeAndAwake...)},
				{Triggers: append([]TriggerCondition{{Variable: "dayPhase", Value: "evening"}}, homeAndAwake...)},
			},
		},
		{
			// Winddown matches dayPhase "winddown" or "night"
			name:     "winddown",
			priority: 40,
			triggerGroups: []TriggerGroup{
				{Triggers: append([]TriggerCondition{{Variable: "dayPhase", Value: "winddown"}}, homeAndAwake...)},
				{Triggers: append([]TriggerCondition{{Variable: "dayPhase", Value: "night"}}, homeAndAwake...)},
			},
		},
		{
			name:     "wakeup",
			priority: 90,
			triggers: nil, // Manually triggered via musicPlaybackType
		},
		{
			name:     "sex",
			priority: 80,
			triggers: nil, // Manually triggered via musicPlaybackType
		},
	}

	c.Zones = make([]ZoneConfig, 0, len(definitions))
	for _, def := range definitions {
		// Only generate a zone if the corresponding music mode exists
		if _, ok := c.Music[def.name]; !ok {
			continue
		}
		c.Zones = append(c.Zones, ZoneConfig{
			Name:          def.name,
			Priority:      def.priority,
			Triggers:      def.triggers,
			TriggerGroups: def.triggerGroups,
		})
	}

	// Also generate zones for any music modes not covered above (custom modes)
	covered := map[string]bool{
		"sleep": true, "morning": true, "day": true,
		"evening": true, "winddown": true, "wakeup": true, "sex": true,
	}
	for musicType := range c.Music {
		if covered[musicType] {
			continue
		}
		c.Zones = append(c.Zones, ZoneConfig{
			Name:     musicType,
			Priority: 10,
			Triggers: nil, // No triggers = activated by musicPlaybackType only
		})
	}
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

	// Generate zones from music modes if none are explicitly defined.
	// This ensures the zone-based code path is always used, eliminating
	// the dual-path branching between zone and legacy orchestration (#639).
	config.ensureZones()

	// Validate zone configurations
	if err := config.validateZones(); err != nil {
		return nil, err
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
