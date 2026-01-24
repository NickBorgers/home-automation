package music

import (
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

func TestZoneConfig_HasZones(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   *MusicConfig
		expected bool
	}{
		{
			name: "has zones",
			config: &MusicConfig{
				Zones: []ZoneConfig{{Name: "test", Priority: 1}},
			},
			expected: true,
		},
		{
			name: "no zones",
			config: &MusicConfig{
				Zones: nil,
			},
			expected: false,
		},
		{
			name: "empty zones",
			config: &MusicConfig{
				Zones: []ZoneConfig{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.HasZones()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestZoneConfig_GetZones(t *testing.T) {
	t.Parallel()

	t.Run("returns explicit zones when defined", func(t *testing.T) {
		config := createTestZoneConfig()
		zones := config.GetZones()

		assert.Len(t, zones, 3)
		assert.Equal(t, "sleep", zones[0].Name)
		assert.Equal(t, 100, zones[0].Priority)
	})

	t.Run("generates implicit zones when none defined", func(t *testing.T) {
		config := &MusicConfig{
			Music: map[string]MusicMode{
				"morning": {Participants: []Participant{}},
				"day":     {Participants: []Participant{}},
				"sleep":   {Participants: []Participant{}},
			},
		}

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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)
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

	// Test that config without zones still works
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false) // readOnly=false to allow SetBool/SetString

	// Config without explicit zones
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning":  {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}},
			"day":      {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}},
			"evening":  {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}},
			"winddown": {Participants: []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}},
			"sleep":    {Participants: []Participant{{PlayerName: "Bedroom", BaseVolume: 10}}},
			"sex":      {Participants: []Participant{{PlayerName: "Bedroom", BaseVolume: 10}}},
			"wakeup":   {Participants: []Participant{{PlayerName: "Bedroom", BaseVolume: 6}}},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

	vars := manager.collectZoneTriggerVariables()

	// Should not include already-subscribed variables
	assert.NotContains(t, vars, "dayPhase")
	assert.NotContains(t, vars, "isAnyoneAsleep")
	assert.NotContains(t, vars, "isAnyoneHome")

	// For this test config, all trigger variables are in the already-subscribed list
	// so the result should be empty
	assert.Empty(t, vars)
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

	manager := NewManager(mockClient, stateManager, config, logger, true, timeProvider, time.UTC)

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
