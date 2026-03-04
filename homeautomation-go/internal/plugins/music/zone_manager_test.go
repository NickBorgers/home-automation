package music

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// createTestZoneConfig creates a config with zone definitions for testing
func createTestZoneConfig() *MusicConfig {
	return &MusicConfig{
		Zones: []ZoneConfig{
			{
				Name:     "sleep",
				Priority: 100,
				Triggers: []TriggerCondition{
					{Variable: "isAnyoneAsleep", Value: true},
					{Variable: "isAnyoneHome", Value: true},
				},
			},
			{
				Name:     "morning",
				Priority: 50,
				Triggers: []TriggerCondition{
					{Variable: "dayPhase", Value: "morning"},
					{Variable: "isAnyoneHome", Value: true},
					{Variable: "isAnyoneAsleep", Value: false},
				},
			},
			{
				Name:     "day",
				Priority: 40,
				Triggers: []TriggerCondition{
					{Variable: "dayPhase", Value: "day"},
					{Variable: "isAnyoneHome", Value: true},
					{Variable: "isAnyoneAsleep", Value: false},
				},
			},
		},
		Music: map[string]MusicMode{
			"sleep": {
				Participants: []Participant{
					{PlayerName: "Bedroom", BaseVolume: 10},
					{PlayerName: "Kitchen", BaseVolume: 8},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "http://rain.example/1.m4a", MediaType: "music", VolumeMultiplier: 1.0},
				},
			},
			"morning": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9},
					{PlayerName: "Bedroom", BaseVolume: 9},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:morning", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9},
					{PlayerName: "Bedroom", BaseVolume: 9},
					{PlayerName: "Office", BaseVolume: 6},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:day", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}
}

func TestEnsureZones_PopulatesFromMusicModes(t *testing.T) {
	t.Parallel()

	t.Run("generates zones with triggers for standard modes", func(t *testing.T) {
		config := &MusicConfig{
			Music: map[string]MusicMode{
				"morning":  {Participants: []Participant{}},
				"day":      {Participants: []Participant{}},
				"evening":  {Participants: []Participant{}},
				"winddown": {Participants: []Participant{}},
				"sleep":    {Participants: []Participant{}},
				"sex":      {Participants: []Participant{}},
				"wakeup":   {Participants: []Participant{}},
			},
		}

		config.ensureZones()

		assert.Len(t, config.Zones, 7)

		// Check sleep zone has triggers
		var sleepZone *ZoneConfig
		for i := range config.Zones {
			if config.Zones[i].Name == "sleep" {
				sleepZone = &config.Zones[i]
				break
			}
		}
		require.NotNil(t, sleepZone)
		assert.Equal(t, 100, sleepZone.Priority)
		assert.Len(t, sleepZone.Triggers, 3) // isAnyoneAsleep, isAnyoneHome, isWakeSequenceActive

		// Check day zone has triggers
		var dayZone *ZoneConfig
		for i := range config.Zones {
			if config.Zones[i].Name == "day" {
				dayZone = &config.Zones[i]
				break
			}
		}
		require.NotNil(t, dayZone)
		assert.Equal(t, 40, dayZone.Priority)
		assert.Len(t, dayZone.Triggers, 3) // dayPhase, isAnyoneHome, isAnyoneAsleep

		// Check manually-triggered zones have no triggers
		var sexZone *ZoneConfig
		for i := range config.Zones {
			if config.Zones[i].Name == "sex" {
				sexZone = &config.Zones[i]
				break
			}
		}
		require.NotNil(t, sexZone)
		assert.Nil(t, sexZone.Triggers)
	})

	t.Run("does not overwrite explicit zones", func(t *testing.T) {
		config := &MusicConfig{
			Zones: []ZoneConfig{{Name: "test", Priority: 1}},
			Music: map[string]MusicMode{
				"morning": {Participants: []Participant{}},
			},
		}

		config.ensureZones()

		assert.Len(t, config.Zones, 1)
		assert.Equal(t, "test", config.Zones[0].Name)
	})

	t.Run("handles empty music modes", func(t *testing.T) {
		config := &MusicConfig{
			Music: map[string]MusicMode{},
		}

		config.ensureZones()

		assert.Empty(t, config.Zones)
	})
}

func TestZoneConfig_GetZones(t *testing.T) {
	t.Parallel()

	t.Run("returns zones", func(t *testing.T) {
		config := createTestZoneConfig()
		zones := config.GetZones()

		assert.Len(t, zones, 3)
		assert.Equal(t, "sleep", zones[0].Name)
		assert.Equal(t, 100, zones[0].Priority)
	})

	t.Run("returns populated zones after ensureZones", func(t *testing.T) {
		config := &MusicConfig{
			Music: map[string]MusicMode{
				"morning": {Participants: []Participant{}},
				"day":     {Participants: []Participant{}},
				"sleep":   {Participants: []Participant{}},
			},
		}

		config.ensureZones()
		zones := config.GetZones()

		// Should have one zone per music mode
		assert.Len(t, zones, 3)

		// Sleep should have highest priority
		var sleepZone *ZoneConfig
		for i := range zones {
			if zones[i].Name == "sleep" {
				sleepZone = &zones[i]
				break
			}
		}
		require.NotNil(t, sleepZone)
		assert.Equal(t, 100, sleepZone.Priority)
	})
}

func TestZoneConfig_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      *MusicConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid zones",
			config: &MusicConfig{
				Zones: []ZoneConfig{
					{Name: "sleep", Priority: 100, Triggers: []TriggerCondition{{Variable: "test", Value: true}}},
				},
				Music: map[string]MusicMode{
					"sleep":    {},
					"morning":  {},
					"day":      {},
					"evening":  {},
					"winddown": {},
					"sex":      {},
					"wakeup":   {},
				},
			},
			expectError: false,
		},
		{
			name: "duplicate zone names",
			config: &MusicConfig{
				Zones: []ZoneConfig{
					{Name: "sleep", Priority: 100},
					{Name: "sleep", Priority: 50},
				},
				Music: map[string]MusicMode{
					"sleep":    {},
					"morning":  {},
					"day":      {},
					"evening":  {},
					"winddown": {},
					"sex":      {},
					"wakeup":   {},
				},
			},
			expectError: true,
			errorMsg:    "duplicate zone name",
		},
		{
			name: "zone references unknown music mode",
			config: &MusicConfig{
				Zones: []ZoneConfig{
					{Name: "unknown_mode", Priority: 100},
				},
				Music: map[string]MusicMode{
					"sleep":    {},
					"morning":  {},
					"day":      {},
					"evening":  {},
					"winddown": {},
					"sex":      {},
					"wakeup":   {},
				},
			},
			expectError: true,
			errorMsg:    "does not match any music mode",
		},
		{
			name: "empty zone name",
			config: &MusicConfig{
				Zones: []ZoneConfig{
					{Name: "", Priority: 100},
				},
				Music: map[string]MusicMode{
					"sleep":    {},
					"morning":  {},
					"day":      {},
					"evening":  {},
					"winddown": {},
					"sex":      {},
					"wakeup":   {},
				},
			},
			expectError: true,
			errorMsg:    "empty name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validateZones()
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestZoneManager_EvaluateTriggers(t *testing.T) {
	t.Parallel()

	// Setup - use readOnly=false for state manager to allow setting values
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Set up state - use require to fail fast if state can't be set
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", true))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))

	// Test: sleep zone should trigger when isAnyoneAsleep=true
	sleepZone := config.Zones[0] // sleep zone
	assert.True(t, zm.evaluateTriggers(sleepZone), "Sleep zone should trigger when isAnyoneAsleep=true")

	// Test: day zone should NOT trigger when isAnyoneAsleep=true
	dayZone := config.Zones[2] // day zone
	assert.False(t, zm.evaluateTriggers(dayZone), "Day zone should not trigger when isAnyoneAsleep=true")

	// Change state: no one asleep
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))

	// Now day zone should trigger
	assert.True(t, zm.evaluateTriggers(dayZone), "Day zone should trigger when isAnyoneAsleep=false and dayPhase=day")

	// And sleep zone should NOT trigger
	assert.False(t, zm.evaluateTriggers(sleepZone), "Sleep zone should not trigger when isAnyoneAsleep=false")
}

func TestZoneManager_AssignSpeakersToZones(t *testing.T) {
	t.Parallel()

	// Setup - use readOnly=false for state manager to allow setting values
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	t.Run("sleep zone claims all speakers when isAnyoneAsleep=true", func(t *testing.T) {
		require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
		require.NoError(t, stateManager.SetBool("isAnyoneAsleep", true))
		require.NoError(t, stateManager.SetString("dayPhase", "day"))

		zoneToSpeakers, _ := zm.assignSpeakersToZones()

		// Sleep zone should claim Bedroom and Kitchen
		assert.Contains(t, zoneToSpeakers, "sleep")
		assert.ElementsMatch(t, []string{"Bedroom", "Kitchen"}, zoneToSpeakers["sleep"])

		// Day zone should NOT have any speakers (sleep has higher priority)
		_, hasDay := zoneToSpeakers["day"]
		assert.False(t, hasDay, "Day zone should not have speakers when sleep zone is active")
	})

	t.Run("day zone gets speakers when no one asleep", func(t *testing.T) {
		require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
		require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
		require.NoError(t, stateManager.SetString("dayPhase", "day"))

		zoneToSpeakers, _ := zm.assignSpeakersToZones()

		// Day zone should claim speakers
		assert.Contains(t, zoneToSpeakers, "day")
		assert.ElementsMatch(t, []string{"Kitchen", "Bedroom", "Office"}, zoneToSpeakers["day"])
	})

	t.Run("no zones active when no one home", func(t *testing.T) {
		require.NoError(t, stateManager.SetBool("isAnyoneHome", false))
		require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
		require.NoError(t, stateManager.SetString("dayPhase", "day"))

		zoneToSpeakers, _ := zm.assignSpeakersToZones()

		// No zones should be active
		assert.Empty(t, zoneToSpeakers)
	})
}

func TestZoneManager_GetActiveZones(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Initially no active zones
	activeZones := zm.GetActiveZones()
	assert.Empty(t, activeZones)

	// Manually add a zone (simulating startZone)
	zm.mu.Lock()
	zm.activeZones["test"] = &Zone{
		Name:        "test",
		MusicType:   "day",
		Priority:    40,
		LeadSpeaker: "Kitchen",
		StartedAt:   time.Now(),
	}
	zm.mu.Unlock()

	activeZones = zm.GetActiveZones()
	assert.Len(t, activeZones, 1)
	assert.Equal(t, "test", activeZones[0].Name)
}

func TestZoneManager_GetZone(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, true)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Zone doesn't exist
	zone, exists := zm.GetZone("nonexistent")
	assert.False(t, exists)
	assert.Nil(t, zone)

	// Add a zone
	zm.mu.Lock()
	zm.activeZones["test"] = &Zone{
		Name:        "test",
		MusicType:   "day",
		Priority:    40,
		LeadSpeaker: "Kitchen",
	}
	zm.mu.Unlock()

	// Now zone exists
	zone, exists = zm.GetZone("test")
	assert.True(t, exists)
	assert.Equal(t, "test", zone.Name)
}

func TestZoneManager_GetSpeakerZone(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, true)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Speaker not in any zone
	zoneName, exists := zm.GetSpeakerZone("Kitchen")
	assert.False(t, exists)
	assert.Empty(t, zoneName)

	// Assign speaker to zone
	zm.mu.Lock()
	zm.speakerZone["Kitchen"] = "day"
	zm.mu.Unlock()

	// Now speaker is in a zone
	zoneName, exists = zm.GetSpeakerZone("Kitchen")
	assert.True(t, exists)
	assert.Equal(t, "day", zoneName)
}

func TestZoneManager_StopAllZones(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, true)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Add some zones
	zm.mu.Lock()
	zm.activeZones["zone1"] = &Zone{Name: "zone1"}
	zm.activeZones["zone2"] = &Zone{Name: "zone2"}
	zm.speakerZone["Kitchen"] = "zone1"
	zm.speakerZone["Bedroom"] = "zone2"
	zm.mu.Unlock()

	// Stop all zones
	zm.StopAllZones("test")

	// Verify all cleared
	zm.mu.RLock()
	assert.Empty(t, zm.activeZones)
	assert.Empty(t, zm.speakerZone)
	zm.mu.RUnlock()
}

func TestStringSlicesEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a        []string
		b        []string
		expected bool
	}{
		{
			name:     "equal slices same order",
			a:        []string{"a", "b", "c"},
			b:        []string{"a", "b", "c"},
			expected: true,
		},
		{
			name:     "equal slices different order",
			a:        []string{"a", "b", "c"},
			b:        []string{"c", "a", "b"},
			expected: true,
		},
		{
			name:     "different lengths",
			a:        []string{"a", "b"},
			b:        []string{"a", "b", "c"},
			expected: false,
		},
		{
			name:     "different elements",
			a:        []string{"a", "b", "c"},
			b:        []string{"a", "b", "d"},
			expected: false,
		},
		{
			name:     "empty slices",
			a:        []string{},
			b:        []string{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stringSlicesEqual(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestZoneManager_BackwardCompatibility(t *testing.T) {
	t.Parallel()

	// Test that config without zones still works (ensureZones generates them)
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false) // readOnly=false to allow SetBool/SetString

	testPlayback := []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}
	// Config without explicit zones
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning":  {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: testPlayback},
			"day":      {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: testPlayback},
			"evening":  {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: testPlayback},
			"winddown": {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}, PlaybackOptions: testPlayback},
			"sleep":    {Participants: []Participant{{PlayerName: "Bedroom", BaseVolume: 10}}, PlaybackOptions: testPlayback},
			"sex":      {Participants: []Participant{{PlayerName: "Bedroom", BaseVolume: 10}}, PlaybackOptions: testPlayback},
			"wakeup":   {Participants: []Participant{{PlayerName: "Bedroom", BaseVolume: 6}}, PlaybackOptions: testPlayback},
		},
	}

	mockClient.SetState("media_player.kitchen", "idle", nil)
	mockClient.SetState("media_player.bedroom", "idle", nil)

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)

	// Manager should still start without errors
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))

	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Zone manager should be initialized
	assert.NotNil(t, manager.zoneManager)

	// Should generate implicit zones
	zones := config.GetZones()
	assert.Len(t, zones, 7) // One for each music mode
}

func TestCollectZoneTriggerVariables(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, true)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)

	vars := manager.collectZoneTriggerVariables()

	// dayPhase, isAnyoneAsleep, and isAnyoneHome SHOULD be included because
	// when zones are configured, handleStateChange returns early (skipping legacy
	// selectAppropriateMusicMode), so these variables must be subscribed to
	// handleZoneTriggerChangeWithContext to trigger zone resolution.
	assert.Contains(t, vars, "dayPhase")
	assert.Contains(t, vars, "isAnyoneAsleep")
	assert.Contains(t, vars, "isAnyoneHome")

	// musicPlaybackType should still be excluded (has its own dedicated handler)
	assert.NotContains(t, vars, "musicPlaybackType")
}

func TestZoneManager_Integration_WithTimeProvider(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false) // readOnly=false to allow SetBool/SetString
	config := createTestZoneConfig()

	// Use fixed time provider
	fixedTime := time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, timeProvider, time.UTC)

	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	require.NoError(t, stateManager.SetString("musicPlaybackType", ""))

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	// Verify zone manager is initialized
	assert.NotNil(t, manager.zoneManager)
}

// Test captureStateSnapshot function
func TestZoneManager_CaptureStateSnapshot(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Set up state values
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", true))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", false))
	require.NoError(t, stateManager.SetString("dayPhase", "evening"))
	require.NoError(t, stateManager.SetString("musicPlaybackType", "sleep"))

	snapshot := zm.captureStateSnapshot()

	// Verify core state variables are captured
	assert.Equal(t, true, snapshot["isAnyoneHome"])
	assert.Equal(t, true, snapshot["isAnyoneAsleep"])
	assert.Equal(t, "evening", snapshot["dayPhase"])
	assert.Equal(t, "sleep", snapshot["musicPlaybackType"])
}

// Test evaluateTriggersWithDetails populates GroupResults for trigger_groups
func TestZoneManager_EvaluateTriggersWithDetails_PopulatesGroupResults(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Config with trigger_groups
	config := &MusicConfig{
		Zones: []ZoneConfig{
			{
				Name:     "multi_trigger",
				Priority: 100,
				TriggerGroups: []TriggerGroup{
					{
						Triggers: []TriggerCondition{
							{Variable: "isAnyoneAsleep", Value: true},
							{Variable: "isAnyoneHome", Value: true},
						},
					},
					{
						Triggers: []TriggerCondition{
							{Variable: "dayPhase", Value: "night"},
						},
					},
				},
			},
		},
		Music: map[string]MusicMode{
			"multi_trigger": {
				Participants: []Participant{{PlayerName: "Bedroom", BaseVolume: 10}},
				PlaybackOptions: []PlaybackOption{
					{URI: "http://rain.example/1.m4a", MediaType: "music", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Set up state - first group should match
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", true))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))

	eval := zm.evaluateTriggersWithDetails(config.Zones[0])

	// Verify zone matched
	assert.True(t, eval.Matched)
	assert.Equal(t, "trigger_group", eval.MatchedVia)
	assert.Equal(t, 1, eval.MatchedGroupIndex)

	// Verify GroupResults is populated with ALL groups
	assert.Len(t, eval.GroupResults, 2)

	// First group should match
	assert.Equal(t, 1, eval.GroupResults[0].GroupIndex)
	assert.True(t, eval.GroupResults[0].Matched)
	assert.Len(t, eval.GroupResults[0].Triggers, 2)

	// Second group should not match (dayPhase != night)
	assert.Equal(t, 2, eval.GroupResults[1].GroupIndex)
	assert.False(t, eval.GroupResults[1].Matched)
	assert.Len(t, eval.GroupResults[1].Triggers, 1)
}

// Test evaluateTriggersWithDetails includes all groups even when one matches
func TestZoneManager_EvaluateTriggersWithDetails_AllGroupsEvaluated(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Config with 3 trigger_groups using existing state variables
	config := &MusicConfig{
		Zones: []ZoneConfig{
			{
				Name:     "test_zone",
				Priority: 100,
				TriggerGroups: []TriggerGroup{
					{Triggers: []TriggerCondition{{Variable: "isNickHome", Value: true}}},
					{Triggers: []TriggerCondition{{Variable: "isCarolineHome", Value: true}}},
					{Triggers: []TriggerCondition{{Variable: "isToriHere", Value: true}}},
				},
			},
		},
		Music: map[string]MusicMode{
			"test_zone": {
				Participants:    []Participant{{PlayerName: "Speaker1", BaseVolume: 10}},
				PlaybackOptions: []PlaybackOption{{URI: "test", MediaType: "music", VolumeMultiplier: 1.0}},
			},
		},
	}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Set all variables to false first
	require.NoError(t, stateManager.SetBool("isNickHome", false))
	require.NoError(t, stateManager.SetBool("isCarolineHome", false))
	require.NoError(t, stateManager.SetBool("isToriHere", false))

	// Test when no group matches
	eval := zm.evaluateTriggersWithDetails(config.Zones[0])
	assert.False(t, eval.Matched)
	assert.Len(t, eval.GroupResults, 3) // All groups evaluated

	// Set isCarolineHome to true - second group should match but all groups still evaluated
	require.NoError(t, stateManager.SetBool("isCarolineHome", true))
	eval = zm.evaluateTriggersWithDetails(config.Zones[0])

	assert.True(t, eval.Matched)
	assert.Equal(t, 2, eval.MatchedGroupIndex) // Second group matched
	assert.Len(t, eval.GroupResults, 3)        // Still all groups evaluated

	// Verify each group's match status
	assert.False(t, eval.GroupResults[0].Matched) // isNickHome=false
	assert.True(t, eval.GroupResults[1].Matched)  // isCarolineHome=true
	assert.False(t, eval.GroupResults[2].Matched) // isToriHere=false
}

// Test evaluateTriggerListWithDetails returns correct results
func TestZoneManager_EvaluateTriggerListWithDetails(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Set up state
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
	require.NoError(t, stateManager.SetString("dayPhase", "morning"))

	triggers := []TriggerCondition{
		{Variable: "dayPhase", Value: "morning"},
		{Variable: "isAnyoneHome", Value: true},
		{Variable: "isAnyoneAsleep", Value: true}, // This one will fail
	}

	matched, results, failed := zm.evaluateTriggerListWithDetails(triggers)

	// Should not match because isAnyoneAsleep is false
	assert.False(t, matched)

	// Should have 3 trigger results
	assert.Len(t, results, 3)

	// First two should match
	assert.True(t, results[0].Matched)
	assert.Equal(t, "dayPhase", results[0].Variable)
	assert.Equal(t, "morning", results[0].ExpectedValue)
	assert.Equal(t, "morning", results[0].ActualValue)

	assert.True(t, results[1].Matched)
	assert.Equal(t, "isAnyoneHome", results[1].Variable)

	// Third should fail
	assert.False(t, results[2].Matched)
	assert.Equal(t, "isAnyoneAsleep", results[2].Variable)
	assert.Equal(t, true, results[2].ExpectedValue)
	assert.Equal(t, false, results[2].ActualValue)

	// Should have 1 failed condition
	assert.Len(t, failed, 1)
	assert.Equal(t, "isAnyoneAsleep", failed[0].Variable)
}

// Test ZoneResolutionAudit struct JSON serialization
func TestZoneResolutionAudit_JSONSerialization(t *testing.T) {
	t.Parallel()

	audit := ZoneResolutionAudit{
		CorrelationID: "1706000000000-1",
		Trigger:       "trigger:isWakeSequenceActive",
		Timestamp:     time.Date(2026, 1, 23, 10, 0, 0, 0, time.UTC),
		StateSnapshot: map[string]interface{}{
			"isWakeSequenceActive": true,
			"dayPhase":             "morning",
		},
		ZoneEvaluations: []ZoneEvaluation{
			{
				Zone:       "sleep",
				Matched:    false,
				Reason:     "trigger conditions not met",
				MatchedVia: "",
				FailedConditions: []FailedCondition{
					{Variable: "isAnyoneAsleep", ExpectedValue: true, ActualValue: false},
				},
			},
		},
		ZoneToSpeakers:    map[string][]string{"morning": {"Kitchen", "Bedroom"}},
		SpeakersToTurnOff: []string{"Office"},
		ZoneChanges: ZoneChangesSummary{
			Start:  []string{"morning"},
			Stop:   []string{"sleep"},
			Update: nil,
		},
	}

	// Serialize to JSON
	jsonBytes, err := json.Marshal(audit)
	require.NoError(t, err)

	// Verify key fields are present in JSON
	jsonStr := string(jsonBytes)
	assert.Contains(t, jsonStr, `"correlationId":"1706000000000-1"`)
	assert.Contains(t, jsonStr, `"trigger":"trigger:isWakeSequenceActive"`)
	assert.Contains(t, jsonStr, `"stateSnapshot"`)
	assert.Contains(t, jsonStr, `"zoneEvaluations"`)
	assert.Contains(t, jsonStr, `"zoneToSpeakers"`)
	assert.Contains(t, jsonStr, `"speakersToTurnOff"`)
	assert.Contains(t, jsonStr, `"zoneChanges"`)

	// Deserialize back and verify
	var decoded ZoneResolutionAudit
	require.NoError(t, json.Unmarshal(jsonBytes, &decoded))

	assert.Equal(t, audit.CorrelationID, decoded.CorrelationID)
	assert.Equal(t, audit.Trigger, decoded.Trigger)
	assert.Equal(t, audit.ZoneChanges.Start, decoded.ZoneChanges.Start)
	assert.Equal(t, audit.ZoneChanges.Stop, decoded.ZoneChanges.Stop)
}

// Test ResolveZonesWithContext includes correlation ID in audit
func TestZoneManager_ResolveZonesWithContext(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Set up state
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	require.NoError(t, stateManager.SetString("musicPlaybackType", ""))

	// Create an event context
	eventCtx := state.NewEventContext("isWakeSequenceActive", false, true)

	// ResolveZonesWithContext should not error
	err := zm.ResolveZonesWithContext(eventCtx, "trigger:isWakeSequenceActive")
	assert.NoError(t, err)

	// Verify the correlation ID was generated correctly
	assert.NotEmpty(t, eventCtx.CorrelationID)
	assert.Equal(t, "isWakeSequenceActive", eventCtx.TriggerKey)
	assert.Equal(t, false, eventCtx.TriggerOldValue)
	assert.Equal(t, true, eventCtx.TriggerNewValue)
}

// Test ResolveZones backward compatibility (nil context)
func TestZoneManager_ResolveZones_BackwardCompatibility(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(manager, config, logger)

	// Set up state
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	require.NoError(t, stateManager.SetString("musicPlaybackType", ""))

	// ResolveZones (without context) should still work
	err := zm.ResolveZones("trigger:dayPhase")
	assert.NoError(t, err)
}

// TestScheduleResolve_CoalescesRapidTriggers verifies that 3 triggers arriving
// within the debounce window produce exactly 1 zone resolution.
func TestScheduleResolve_CoalescesRapidTriggers(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	mgr := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(mgr, config, logger)

	// Use a very short debounce delay for testing
	zm.SetDebounceDelay(50 * time.Millisecond)

	// Set up state so resolution can proceed
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	require.NoError(t, stateManager.SetString("musicPlaybackType", ""))

	// Fire 3 triggers in rapid succession (all within debounce window)
	ctx1 := state.NewEventContext("isAnyoneAsleep", true, false)
	ctx2 := state.NewEventContext("isMasterAsleep", true, false)
	ctx3 := state.NewEventContext("isWakeSequenceActive", false, true)

	zm.ScheduleResolve(ctx1, "trigger:isAnyoneAsleep")
	zm.ScheduleResolve(ctx2, "trigger:isMasterAsleep")
	zm.ScheduleResolve(ctx3, "trigger:isWakeSequenceActive")

	// Verify debounce state: should have 3 pending triggers
	zm.debounceMu.Lock()
	assert.True(t, zm.debouncePending, "Should have a pending debounce")
	assert.Len(t, zm.debounceTriggers, 3, "Should have accumulated 3 triggers")
	assert.Equal(t, ctx3, zm.debounceCtx, "Should keep the latest event context")
	zm.debounceMu.Unlock()

	// Wait for the debounce timer to fire
	time.Sleep(100 * time.Millisecond)

	// After firing, debounce state should be cleared
	zm.debounceMu.Lock()
	assert.False(t, zm.debouncePending, "Debounce should have fired")
	assert.Nil(t, zm.debounceTriggers, "Triggers should be cleared after firing")
	assert.Nil(t, zm.debounceCtx, "Context should be cleared after firing")
	zm.debounceMu.Unlock()
}

// TestScheduleResolve_SingleTriggerStillWorks verifies that a single trigger
// resolves correctly after the debounce delay.
func TestScheduleResolve_SingleTriggerStillWorks(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	mgr := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(mgr, config, logger)

	// Use a very short debounce delay for testing
	zm.SetDebounceDelay(50 * time.Millisecond)

	// Set up state
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	require.NoError(t, stateManager.SetString("musicPlaybackType", ""))

	// Fire a single trigger
	ctx := state.NewEventContext("dayPhase", "morning", "day")
	zm.ScheduleResolve(ctx, "trigger:dayPhase")

	// Verify debounce is pending
	zm.debounceMu.Lock()
	assert.True(t, zm.debouncePending, "Should have a pending debounce")
	assert.Len(t, zm.debounceTriggers, 1, "Should have 1 trigger")
	zm.debounceMu.Unlock()

	// Wait for the debounce timer to fire
	time.Sleep(100 * time.Millisecond)

	// After firing, debounce state should be cleared
	zm.debounceMu.Lock()
	assert.False(t, zm.debouncePending, "Debounce should have fired")
	zm.debounceMu.Unlock()
}

// TestUpdateZoneSpeakers_UpdatesParticipants verifies that zone.Participants
// is updated when speakers are added or removed, so subsequent concurrent
// resolutions see accurate data instead of stale participants from zone creation.
func TestUpdateZoneSpeakers_UpdatesParticipants(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createTestZoneConfig()

	mgr := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(mgr, config, logger)

	// Set up state
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", false))
	require.NoError(t, stateManager.SetString("dayPhase", "morning"))

	// Simulate an active zone with only Kitchen
	zm.mu.Lock()
	zm.activeZones["morning"] = &Zone{
		Name:        "morning",
		MusicType:   "morning",
		Priority:    50,
		LeadSpeaker: "Kitchen",
		Participants: []ParticipantWithVolume{
			{PlayerName: "Kitchen", BaseVolume: 9, Volume: 9, DefaultVolume: 9},
		},
		StartedAt: time.Now(),
	}
	zm.speakerZone["Kitchen"] = "morning"
	zm.mu.Unlock()

	// Update zone to add Bedroom speaker
	err := zm.updateZoneSpeakers("morning", []string{"Kitchen", "Bedroom"}, "test")
	assert.NoError(t, err)

	// Verify zone.Participants now includes both speakers
	zm.mu.RLock()
	zone := zm.activeZones["morning"]
	assert.Len(t, zone.Participants, 2, "Should have 2 participants after adding Bedroom")

	participantNames := make([]string, len(zone.Participants))
	for i, p := range zone.Participants {
		participantNames[i] = p.PlayerName
	}
	assert.ElementsMatch(t, []string{"Kitchen", "Bedroom"}, participantNames)

	// Verify speakerZone tracking is correct
	assert.Equal(t, "morning", zm.speakerZone["Kitchen"])
	assert.Equal(t, "morning", zm.speakerZone["Bedroom"])
	zm.mu.RUnlock()

	// Now remove Bedroom
	err = zm.updateZoneSpeakers("morning", []string{"Kitchen"}, "test-remove")
	assert.NoError(t, err)

	zm.mu.RLock()
	zone = zm.activeZones["morning"]
	assert.Len(t, zone.Participants, 1, "Should have 1 participant after removing Bedroom")
	assert.Equal(t, "Kitchen", zone.Participants[0].PlayerName)

	// Verify speakerZone tracking is correct
	assert.Equal(t, "morning", zm.speakerZone["Kitchen"])
	_, hasBedroom := zm.speakerZone["Bedroom"]
	assert.False(t, hasBedroom, "Bedroom should be removed from speakerZone")
	zm.mu.RUnlock()
}

// TestUpdateZoneSpeakers_UsesZoneVolumeMultiplier verifies that dynamically-added
// speakers inherit the zone's VolumeMultiplier from the active PlaybackOption,
// rather than using a hardcoded 1.0 multiplier. (Issue #746)
func TestUpdateZoneSpeakers_UsesZoneVolumeMultiplier(t *testing.T) {
	t.Parallel()

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Use a config with a non-1.0 VolumeMultiplier to expose the bug
	config := &MusicConfig{
		Zones: []ZoneConfig{
			{
				Name:     "sleep",
				Priority: 100,
				Triggers: []TriggerCondition{
					{Variable: "isAnyoneAsleep", Value: true},
					{Variable: "isAnyoneHome", Value: true},
				},
			},
		},
		Music: map[string]MusicMode{
			"sleep": {
				Participants: []Participant{
					{PlayerName: "Bedroom", BaseVolume: 10},
					{PlayerName: "Kitchen", BaseVolume: 20},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "http://rain.example/1.m4a", MediaType: "music", VolumeMultiplier: 0.5},
				},
			},
		},
	}

	mgr := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(mgr, config, logger)

	// Set up state
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", true))

	// Simulate an active sleep zone with only Bedroom, using VolumeMultiplier=0.5
	// BaseVolume=10 * 0.5 = volume 5
	zm.mu.Lock()
	zm.activeZones["sleep"] = &Zone{
		Name:             "sleep",
		MusicType:        "sleep",
		Priority:         100,
		LeadSpeaker:      "Bedroom",
		VolumeMultiplier: 0.5,
		Participants: []ParticipantWithVolume{
			{PlayerName: "Bedroom", BaseVolume: 10, Volume: 5, DefaultVolume: 5},
		},
		StartedAt: time.Now(),
	}
	zm.speakerZone["Bedroom"] = "sleep"
	zm.mu.Unlock()

	// Dynamically add Kitchen speaker to the zone
	err := zm.updateZoneSpeakers("sleep", []string{"Bedroom", "Kitchen"}, "test-dynamic-add")
	assert.NoError(t, err)

	// Verify the dynamically-added Kitchen speaker uses VolumeMultiplier=0.5
	// Kitchen BaseVolume=20, expected volume = 20 * 0.5 = 10 (not 20 from 1.0 multiplier)
	zm.mu.RLock()
	zone := zm.activeZones["sleep"]
	require.Len(t, zone.Participants, 2)

	var kitchenParticipant *ParticipantWithVolume
	for i, p := range zone.Participants {
		if p.PlayerName == "Kitchen" {
			kitchenParticipant = &zone.Participants[i]
			break
		}
	}
	require.NotNil(t, kitchenParticipant, "Kitchen should be in zone participants")
	assert.Equal(t, 10, kitchenParticipant.Volume,
		"Dynamically-added speaker should use zone's VolumeMultiplier (0.5), not hardcoded 1.0")
	assert.Equal(t, 10, kitchenParticipant.DefaultVolume,
		"DefaultVolume should also use zone's VolumeMultiplier")
	zm.mu.RUnlock()
}

func TestHasCompatibleMusic(t *testing.T) {
	t.Parallel()

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"sleep": {
				PlaybackOptions: []PlaybackOption{
					{URI: "http://rain.example/1.m4a"},
					{URI: "http://rain.example/2.m4a"},
				},
			},
		},
	}

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, true)
	mgr := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(mgr, config, logger)

	t.Run("returns true when URI matches a playback option", func(t *testing.T) {
		zone := &Zone{PlaylistURI: "http://rain.example/1.m4a"}
		assert.True(t, zm.hasCompatibleMusic(zone, "sleep"))
	})

	t.Run("returns false when URI does not match any playback option", func(t *testing.T) {
		zone := &Zone{PlaylistURI: "http://jazz.example/1.m4a"}
		assert.False(t, zm.hasCompatibleMusic(zone, "sleep"))
	})

	t.Run("returns false when PlaylistURI is empty", func(t *testing.T) {
		zone := &Zone{PlaylistURI: ""}
		assert.False(t, zm.hasCompatibleMusic(zone, "sleep"))
	})

	t.Run("returns false when target zone is not in config", func(t *testing.T) {
		zone := &Zone{PlaylistURI: "http://rain.example/1.m4a"}
		assert.False(t, zm.hasCompatibleMusic(zone, "nonexistent-zone"))
	})
}

func TestFindSeamlessTransitions(t *testing.T) {
	t.Parallel()

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"sleep-prep": {
				Participants: []Participant{
					{PlayerName: "Bedroom"},
					{PlayerName: "Sitting Room"},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "http://rain.example/1.m4a"},
				},
			},
			"sleep": {
				Participants: []Participant{
					{PlayerName: "Bedroom"},
					{PlayerName: "Kitchen"},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "http://rain.example/1.m4a"},
				},
			},
		},
	}

	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, true)
	mgr := NewManager(context.Background(), mockClient, stateManager, config, logger, true, nil, nil)
	zm := NewZoneManager(mgr, config, logger)

	t.Run("detects shared speakers and produces transition", func(t *testing.T) {
		zm.mu.Lock()
		zm.activeZones["sleep-prep"] = &Zone{
			Name:        "sleep-prep",
			PlaylistURI: "http://rain.example/1.m4a",
			Participants: []ParticipantWithVolume{
				{PlayerName: "Bedroom"},
				{PlayerName: "Sitting Room"},
			},
		}
		zm.mu.Unlock()

		zoneToSpeakers := map[string][]string{
			"sleep": {"Bedroom", "Kitchen"},
		}

		transitions := zm.findSeamlessTransitions([]string{"sleep-prep"}, []string{"sleep"}, zoneToSpeakers)

		require.Len(t, transitions, 1)
		assert.Equal(t, "sleep-prep", transitions[0].stoppingZone)
		assert.Equal(t, "sleep", transitions[0].startingZone)
		assert.Equal(t, []string{"Bedroom"}, transitions[0].sharedSpeakers)
		assert.Equal(t, []string{"Sitting Room"}, transitions[0].removeSpeakers)
		assert.Equal(t, []string{"Kitchen"}, transitions[0].addSpeakers)

		zm.mu.Lock()
		delete(zm.activeZones, "sleep-prep")
		zm.mu.Unlock()
	})

	t.Run("returns no transitions when no speakers are shared", func(t *testing.T) {
		zm.mu.Lock()
		zm.activeZones["sleep-prep"] = &Zone{
			Name:        "sleep-prep",
			PlaylistURI: "http://rain.example/1.m4a",
			Participants: []ParticipantWithVolume{
				{PlayerName: "Sitting Room"},
				{PlayerName: "Front Room"},
			},
		}
		zm.mu.Unlock()

		zoneToSpeakers := map[string][]string{
			"sleep": {"Bedroom", "Kitchen"},
		}

		transitions := zm.findSeamlessTransitions([]string{"sleep-prep"}, []string{"sleep"}, zoneToSpeakers)

		assert.Empty(t, transitions, "no shared speakers means no seamless transition")

		zm.mu.Lock()
		delete(zm.activeZones, "sleep-prep")
		zm.mu.Unlock()
	})

	t.Run("returns no transitions when music URIs are incompatible", func(t *testing.T) {
		zm.mu.Lock()
		zm.activeZones["sleep-prep"] = &Zone{
			Name:        "sleep-prep",
			PlaylistURI: "http://different-music.example/1.m4a",
			Participants: []ParticipantWithVolume{
				{PlayerName: "Bedroom"},
			},
		}
		zm.mu.Unlock()

		zoneToSpeakers := map[string][]string{
			"sleep": {"Bedroom"},
		}

		transitions := zm.findSeamlessTransitions([]string{"sleep-prep"}, []string{"sleep"}, zoneToSpeakers)

		assert.Empty(t, transitions, "incompatible music URI should prevent seamless transition")

		zm.mu.Lock()
		delete(zm.activeZones, "sleep-prep")
		zm.mu.Unlock()
	})

	t.Run("skips stopping zone not in activeZones", func(t *testing.T) {
		// No active zone for "sleep-prep"
		zoneToSpeakers := map[string][]string{
			"sleep": {"Bedroom"},
		}

		transitions := zm.findSeamlessTransitions([]string{"sleep-prep"}, []string{"sleep"}, zoneToSpeakers)

		assert.Empty(t, transitions)
	})
}
