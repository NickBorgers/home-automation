package music

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"go.uber.org/zap"
)

func TestMusicManager_SelectAppropriateMusicMode(t *testing.T) {
	tests := []struct {
		name              string
		isAnyoneHome      bool
		isAnyoneAsleep    bool
		dayPhase          string
		currentMusicType  string
		expectedMusicType string
		description       string
	}{
		{
			name:              "No one home - stop music",
			isAnyoneHome:      false,
			isAnyoneAsleep:    false,
			dayPhase:          "day",
			currentMusicType:  "day",
			expectedMusicType: "",
			description:       "When no one is home, music should stop",
		},
		{
			name:              "Someone asleep - sleep mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    true,
			dayPhase:          "day",
			currentMusicType:  "day",
			expectedMusicType: "sleep",
			description:       "Sleep mode has highest priority",
		},
		{
			name:              "Morning - day mode (no wake-up event)",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "morning",
			currentMusicType:  "",
			expectedMusicType: "day",
			description:       "Morning phase without wake-up event triggers day music",
		},
		{
			name:              "Day - day mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "day",
			currentMusicType:  "",
			expectedMusicType: "day",
			description:       "Day phase triggers day music",
		},
		{
			name:              "Sunset - evening mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "sunset",
			currentMusicType:  "",
			expectedMusicType: "evening",
			description:       "Sunset phase triggers evening music",
		},
		{
			name:              "Dusk - evening mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "dusk",
			currentMusicType:  "",
			expectedMusicType: "evening",
			description:       "Dusk phase triggers evening music",
		},
		{
			name:              "Winddown - winddown mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "winddown",
			currentMusicType:  "",
			expectedMusicType: "winddown",
			description:       "Winddown phase triggers winddown music",
		},
		{
			name:              "Night - winddown mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "night",
			currentMusicType:  "",
			expectedMusicType: "winddown",
			description:       "Night phase triggers winddown music",
		},
		{
			name:              "Winddown but sleep playing - keep sleep",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "winddown",
			currentMusicType:  "sleep",
			expectedMusicType: "sleep",
			description:       "Don't override sleep music with winddown",
		},
		{
			name:              "Unknown phase - default to day",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "unknown",
			currentMusicType:  "",
			expectedMusicType: "day",
			description:       "Unknown phases default to day mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock HA client and state manager (NOT read-only for tests)
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateMgr := state.NewManager(mockHA, logger, false)

			// Create music config (minimal for testing)
			config := &MusicConfig{
				Music: map[string]MusicMode{
					"morning":  {},
					"day":      {},
					"evening":  {},
					"winddown": {},
					"sleep":    {},
					"sex":      {},
					"wakeup":   {},
				},
			}

			// Use a fixed time provider with a Monday (not Sunday) for testing
			// This ensures tests are independent of what day they run on
			fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC) // Monday, January 6, 2025
			timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

			// Create manager
			manager := NewManager(mockHA, stateMgr, config, logger, true, timeProvider)

			// Set up initial state
			if err := stateMgr.SetBool("isAnyoneHome", tt.isAnyoneHome); err != nil {
				t.Fatalf("Failed to set isAnyoneHome: %v", err)
			}
			if err := stateMgr.SetBool("isAnyoneAsleep", tt.isAnyoneAsleep); err != nil {
				t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
			}
			if err := stateMgr.SetString("dayPhase", tt.dayPhase); err != nil {
				t.Fatalf("Failed to set dayPhase: %v", err)
			}
			if err := stateMgr.SetString("musicPlaybackType", tt.currentMusicType); err != nil {
				t.Fatalf("Failed to set musicPlaybackType: %v", err)
			}

			// Execute music mode selection
			manager.selectAppropriateMusicMode()

			// Verify result
			actualMusicType, err := stateMgr.GetString("musicPlaybackType")
			if err != nil {
				t.Fatalf("Failed to get musicPlaybackType: %v", err)
			}

			if actualMusicType != tt.expectedMusicType {
				t.Errorf("Expected music type %q, got %q. Description: %s",
					tt.expectedMusicType, actualMusicType, tt.description)
			}
		})
	}
}

func TestMusicManager_DetermineMusicModeFromDayPhase(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	config := &MusicConfig{}

	// Use a fixed time provider with a Monday (not Sunday) for testing
	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC) // Monday, January 6, 2025
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockHA, stateMgr, config, logger, true, timeProvider)

	tests := []struct {
		dayPhase          string
		currentMusicType  string
		expectedMusicMode string
	}{
		{"morning", "", "day"}, // Morning without wake-up event = day music
		{"day", "", "day"},
		{"sunset", "", "evening"},
		{"dusk", "", "evening"},
		{"winddown", "", "winddown"},
		{"night", "", "winddown"},
		{"winddown", "sleep", "sleep"}, // Don't override sleep
		{"unknown", "", "day"},         // Default to day
	}

	for _, tt := range tests {
		t.Run(tt.dayPhase+"_"+tt.currentMusicType, func(t *testing.T) {
			result := manager.determineMusicModeFromDayPhase(tt.dayPhase, tt.currentMusicType, "", false)
			if result != tt.expectedMusicMode {
				t.Errorf("For dayPhase=%s, currentMusicType=%s: expected %s, got %s",
					tt.dayPhase, tt.currentMusicType, tt.expectedMusicMode, result)
			}
		})
	}
}

func TestMusicManager_StateChangeHandling(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning":  {},
			"day":      {},
			"evening":  {},
			"winddown": {},
			"sleep":    {},
			"sex":      {},
			"wakeup":   {},
		},
	}

	// Use a fixed time provider with a Monday (not Sunday) for testing
	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC) // Monday, January 6, 2025
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockHA, stateMgr, config, logger, true, timeProvider)

	// Set initial state
	if err := stateMgr.SetBool("isAnyoneHome", true); err != nil {
		t.Fatalf("Failed to set isAnyoneHome: %v", err)
	}
	if err := stateMgr.SetBool("isAnyoneAsleep", false); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}
	if err := stateMgr.SetString("dayPhase", "day"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}
	if err := stateMgr.SetString("musicPlaybackType", ""); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Start manager (which subscribes to state changes)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Initial selection should set day mode
	musicType, err := stateMgr.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "day" {
		t.Errorf("Expected initial music type 'day', got %q", musicType)
	}

	// Change to evening phase - should trigger music mode change
	if err := stateMgr.SetString("dayPhase", "sunset"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}

	// Give the subscription callback time to execute
	time.Sleep(100 * time.Millisecond)

	musicType, err = stateMgr.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "evening" {
		t.Errorf("Expected music type 'evening' after sunset, got %q", musicType)
	}

	// Someone goes to sleep - should trigger sleep mode
	if err := stateMgr.SetBool("isAnyoneAsleep", true); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}

	// Give the subscription callback time to execute
	time.Sleep(100 * time.Millisecond)

	musicType, err = stateMgr.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "sleep" {
		t.Errorf("Expected music type 'sleep' when someone is asleep, got %q", musicType)
	}
}

func TestMusicManager_Stop(t *testing.T) {
	// Create mock HA client and state manager
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)

	// Create music config
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning":  {},
			"day":      {},
			"evening":  {},
			"winddown": {},
			"sleep":    {},
			"sex":      {},
			"wakeup":   {},
		},
	}

	// Create manager
	manager := NewManager(mockHA, stateMgr, config, logger, true, nil)

	// Set initial state
	if err := stateMgr.SetBool("isAnyoneHome", true); err != nil {
		t.Fatalf("Failed to set isAnyoneHome: %v", err)
	}
	if err := stateMgr.SetBool("isAnyoneAsleep", false); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}
	if err := stateMgr.SetString("dayPhase", "day"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}

	// Start manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Verify subscriptions were created (dayPhase, isAnyoneAsleep, isAnyoneHome, musicPlaybackType)
	if len(manager.subscriptions) != 4 {
		t.Errorf("Expected 4 subscriptions, got %d", len(manager.subscriptions))
	}

	// Stop manager
	manager.Stop()

	// Verify subscriptions were cleaned up
	if manager.subscriptions != nil {
		t.Errorf("Expected subscriptions to be nil after Stop(), got %v", manager.subscriptions)
	}
}

// findRepoRoot finds the repository root by looking for go.mod
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	// Walk up the directory tree until we find the parent of homeautomation-go
	for {
		// Check if we're at or can find the homeautomation-go directory
		if filepath.Base(dir) == "homeautomation-go" {
			return filepath.Dir(dir) // Return parent directory
		}

		// Check if configs directory exists here
		configsDir := filepath.Join(dir, "configs")
		if _, err := os.Stat(configsDir); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find repository root")
		}
		dir = parent
	}
}

func TestLoadMusicConfig(t *testing.T) {
	// Find the repository root and construct path to config file
	repoRoot := findRepoRoot(t)
	configPath := filepath.Join(repoRoot, "configs", "music_config.yaml")

	// Test with the actual config file
	config, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load music config: %v", err)
	}

	// Verify all expected modes are present
	expectedModes := []string{"morning", "day", "evening", "winddown", "sleep", "sex", "wakeup"}
	for _, mode := range expectedModes {
		if _, ok := config.Music[mode]; !ok {
			t.Errorf("Missing expected music mode: %s", mode)
		}
	}

	// Verify morning mode has expected structure
	morningMode, ok := config.Music["morning"]
	if !ok {
		t.Fatal("Morning mode not found")
	}

	if len(morningMode.Participants) == 0 {
		t.Error("Morning mode should have participants")
	}

	if len(morningMode.PlaybackOptions) == 0 {
		t.Error("Morning mode should have playback options")
	}

	// Verify a participant has expected fields
	if len(morningMode.Participants) > 0 {
		participant := morningMode.Participants[0]
		if participant.PlayerName == "" {
			t.Error("Participant should have player_name")
		}
		if participant.BaseVolume == 0 {
			t.Error("Participant should have base_volume")
		}
	}

	// Verify a playback option has expected fields
	if len(morningMode.PlaybackOptions) > 0 {
		option := morningMode.PlaybackOptions[0]
		if option.URI == "" {
			t.Error("Playback option should have uri")
		}
		if option.MediaType == "" {
			t.Error("Playback option should have media_type")
		}
		if option.VolumeMultiplier == 0 {
			t.Error("Playback option should have volume_multiplier")
		}
	}
}

func TestMusicManager_ReadOnlyMode(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	// Create state manager in read-only mode
	stateManager := state.NewManager(mockClient, logger, true)

	// Initialize required state variables (can set because they're LocalOnly or initial sync)
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("musicPlaybackType", "")

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day":   {},
			"sleep": {},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil)

	// Test selecting music mode in read-only mode - should handle gracefully
	manager.selectAppropriateMusicMode()

	// Test with sleep scenario
	_ = stateManager.SetBool("isAnyoneAsleep", true)
	manager.selectAppropriateMusicMode()

	// Test with no one home
	_ = stateManager.SetBool("isAnyoneHome", false)
	manager.selectAppropriateMusicMode()

	// If we get here without panicking, the read-only mode handling worked correctly
	// The actual verification is that no errors are thrown, just debug logs
}

// TestCalculateVolume tests volume calculation
func TestCalculateVolume(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name       string
		baseVolume int
		multiplier float64
		expected   int
	}{
		{"No multiplier", 9, 1.0, 9},
		{"1.5x multiplier", 10, 1.5, 15},
		{"Rounds correctly", 9, 1.1, 10},
		{"Caps at 15", 16, 1.1, 15},
		{"Zero base", 0, 1.5, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.calculateVolume(tt.baseVolume, tt.multiplier)
			if result != tt.expected {
				t.Errorf("calculateVolume(%d, %.1f) = %d, want %d",
					tt.baseVolume, tt.multiplier, result, tt.expected)
			}
		})
	}
}

// TestPlaylistRotation tests playlist rotation logic
func TestPlaylistRotation(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Test rotation for "day" music type with 3 playlists
	musicType := "day"
	optionsCount := 3

	// First call should return 0
	index1 := manager.getNextPlaylistIndex(musicType, optionsCount)
	if index1 != 0 {
		t.Errorf("First call should return 0, got %d", index1)
	}

	// Second call should return 1
	index2 := manager.getNextPlaylistIndex(musicType, optionsCount)
	if index2 != 1 {
		t.Errorf("Second call should return 1, got %d", index2)
	}

	// Third call should return 2
	index3 := manager.getNextPlaylistIndex(musicType, optionsCount)
	if index3 != 2 {
		t.Errorf("Third call should return 2, got %d", index3)
	}

	// Fourth call should wrap around to 0
	index4 := manager.getNextPlaylistIndex(musicType, optionsCount)
	if index4 != 0 {
		t.Errorf("Fourth call should wrap to 0, got %d", index4)
	}

	// Test different music type starts at 0
	index5 := manager.getNextPlaylistIndex("evening", optionsCount)
	if index5 != 0 {
		t.Errorf("Different music type should start at 0, got %d", index5)
	}
}

// TestRateLimiting tests rate limiting functionality
func TestRateLimiting(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Initialize state variables
	_ = stateManager.SetString("musicPlaybackType", "")

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	// Use a fixed time for testing
	fixedTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, true, timeProvider)

	// First playback should succeed
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "", "day")
	if manager.currentlyPlaying == nil {
		t.Error("First playback should have succeeded")
	}

	// Immediate second playback should be rate limited
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "day", "evening")
	if manager.currentlyPlaying.Type != "day" {
		t.Error("Second immediate playback should have been rate limited")
	}

	// Update time to 11 seconds later
	timeProvider.FixedTime = fixedTime.Add(11 * time.Second)
	manager.timeProvider = timeProvider

	// Now it should succeed
	_ = stateManager.SetString("musicPlaybackType", "evening")
	config.Music["evening"] = config.Music["day"] // Add evening config
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "day", "evening")
	if manager.currentlyPlaying.Type != "evening" {
		t.Error("Playback after 11 seconds should have succeeded")
	}
}

// TestDoubleActivationPrevention tests prevention of re-activating already playing music
func TestDoubleActivationPrevention(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil)

	// First playback
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "", "day")
	firstURI := manager.currentlyPlaying.URI

	// Second activation of same type should be blocked
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "day", "day")
	if manager.currentlyPlaying.URI != firstURI {
		t.Error("Double activation should not have changed the playlist")
	}
}

// TestMuteConditionEvaluation tests mute condition logic
func TestMuteConditionEvaluation(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up state variables
	_ = stateManager.SetBool("isTVPlaying", true)
	_ = stateManager.SetBool("isMasterAsleep", false)

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name          string
		participant   ParticipantWithVolume
		expectedMuted bool
	}{
		{
			name: "No mute conditions - should unmute",
			participant: ParticipantWithVolume{
				PlayerName:   "Kitchen",
				LeaveMutedIf: []MuteCondition{},
			},
			expectedMuted: false,
		},
		{
			name: "TV playing condition matches - should stay muted",
			participant: ParticipantWithVolume{
				PlayerName: "Living Room",
				LeaveMutedIf: []MuteCondition{
					{Variable: "isTVPlaying", Value: true},
				},
			},
			expectedMuted: true,
		},
		{
			name: "Master asleep condition doesn't match - should unmute",
			participant: ParticipantWithVolume{
				PlayerName: "Bedroom",
				LeaveMutedIf: []MuteCondition{
					{Variable: "isMasterAsleep", Value: true},
				},
			},
			expectedMuted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldUnmute := manager.shouldUnmuteSpeaker(tt.participant)
			if shouldUnmute == tt.expectedMuted {
				t.Errorf("shouldUnmuteSpeaker() = %v, expectedMuted = %v",
					shouldUnmute, tt.expectedMuted)
			}
		})
	}
}

// TestGetSpeakerEntityID tests entity ID conversion
func TestGetSpeakerEntityID(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		speakerName string
		expected    string
	}{
		{"Kitchen", "media_player.kitchen"},
		{"Kids Bathroom", "media_player.kids_bathroom"},
		{"Soundbar", "media_player.soundbar"},
		{"Dining Room", "media_player.dining_room"},
	}

	for _, tt := range tests {
		t.Run(tt.speakerName, func(t *testing.T) {
			result := manager.getSpeakerEntityID(tt.speakerName)
			if result != tt.expected {
				t.Errorf("getSpeakerEntityID(%q) = %q, want %q",
					tt.speakerName, result, tt.expected)
			}
		})
	}
}

// TestStopPlayback tests stopping music playback
func TestStopPlayback(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{},
			},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Set up currently playing music
	manager.currentlyPlaying = &CurrentlyPlayingMusic{
		Type: "day",
		URI:  "spotify:playlist:test",
	}

	// Stop playback
	manager.stopPlayback()

	// Verify currently playing is cleared
	if manager.currentlyPlaying != nil {
		t.Error("currentlyPlaying should be nil after stopPlayback()")
	}
}

// TestOrchestratePlayback tests the main orchestration flow
func TestOrchestratePlayback(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
					{PlayerName: "Living Room", BaseVolume: 10, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test1", MediaType: "playlist", VolumeMultiplier: 1.0},
					{URI: "spotify:playlist:test2", MediaType: "playlist", VolumeMultiplier: 1.5},
				},
			},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil)

	// Test orchestration
	err := manager.orchestratePlayback("day", "test_trigger")
	if err != nil {
		t.Fatalf("orchestratePlayback() failed: %v", err)
	}

	// Verify currently playing was set
	if manager.currentlyPlaying == nil {
		t.Fatal("currentlyPlaying should be set after orchestration")
	}

	if manager.currentlyPlaying.Type != "day" {
		t.Errorf("currentlyPlaying.Type = %q, want %q", manager.currentlyPlaying.Type, "day")
	}

	if len(manager.currentlyPlaying.Participants) != 2 {
		t.Errorf("currentlyPlaying.Participants count = %d, want 2", len(manager.currentlyPlaying.Participants))
	}

	if manager.currentlyPlaying.LeadPlayer != "Kitchen" {
		t.Errorf("currentlyPlaying.LeadPlayer = %q, want %q", manager.currentlyPlaying.LeadPlayer, "Kitchen")
	}

	// Test with unknown music type
	err = manager.orchestratePlayback("unknown", "test_trigger")
	if err == nil {
		t.Error("orchestratePlayback() with unknown type should return error")
	}
}

// TestToLower tests the toLower helper function
func TestToLower(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Kitchen", "kitchen"},
		{"Kids Bathroom", "kids bathroom"},
		{"DINING ROOM", "dining room"},
		{"soundbar", "soundbar"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := toLower(tt.input)
			if result != tt.expected {
				t.Errorf("toLower(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestValuesMatch tests value matching logic
func TestValuesMatch(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	tests := []struct {
		name     string
		a        interface{}
		b        interface{}
		expected bool
	}{
		{"Matching bools", true, true, true},
		{"Non-matching bools", true, false, false},
		{"Matching strings", "test", "test", true},
		{"Non-matching strings", "test", "other", false},
		{"Matching numbers", 42, 42, true},
		{"Non-matching numbers", 42, 43, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := manager.valuesMatch(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("valuesMatch(%v, %v) = %v, want %v",
					tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

// TestGetStateValue tests state value retrieval
func TestGetStateValue(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up various state variables
	_ = stateManager.SetBool("isTVPlaying", true)
	_ = stateManager.SetString("dayPhase", "evening")
	_ = stateManager.SetNumber("alarmTime", 7.5)

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Test getting boolean
	val, err := manager.getStateValue("isTVPlaying")
	if err != nil {
		t.Errorf("getStateValue(isTVPlaying) failed: %v", err)
	}
	if val != true {
		t.Errorf("getStateValue(isTVPlaying) = %v, want true", val)
	}

	// Test getting string
	val, err = manager.getStateValue("dayPhase")
	if err != nil {
		t.Errorf("getStateValue(dayPhase) failed: %v", err)
	}
	if val != "evening" {
		t.Errorf("getStateValue(dayPhase) = %v, want 'evening'", val)
	}

	// Test getting number
	val, err = manager.getStateValue("alarmTime")
	if err != nil {
		t.Errorf("getStateValue(alarmTime) failed: %v", err)
	}
	if val != 7.5 {
		t.Errorf("getStateValue(alarmTime) = %v, want 7.5", val)
	}

	// Test non-existent variable
	_, err = manager.getStateValue("nonExistent")
	if err == nil {
		t.Error("getStateValue(nonExistent) should return error")
	}
}

// TestCallService tests service calling
func TestCallService(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}

	// Test in normal mode
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)
	err := manager.callService("media_player", "play_media", map[string]interface{}{
		"entity_id": "media_player.kitchen",
	})
	if err != nil {
		t.Errorf("callService() in normal mode failed: %v", err)
	}

	// Test in read-only mode
	managerRO := NewManager(mockClient, stateManager, config, logger, true, nil)
	err = managerRO.callService("media_player", "play_media", map[string]interface{}{
		"entity_id": "media_player.kitchen",
	})
	if err != nil {
		t.Errorf("callService() in read-only mode failed: %v", err)
	}
}

// TestHandleMusicPlaybackTypeChange_EmptyString tests stopping playback
func TestHandleMusicPlaybackTypeChange_EmptyString(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{},
			},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil)

	// Set up currently playing music
	manager.currentlyPlaying = &CurrentlyPlayingMusic{
		Type: "day",
		URI:  "spotify:playlist:test",
	}

	// Trigger empty music type (stop)
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "day", "")

	// Verify playback was stopped
	if manager.currentlyPlaying != nil {
		t.Error("handleMusicPlaybackTypeChange with empty string should stop playback")
	}
}

// TestHandleMusicPlaybackTypeChange_InvalidType tests handling of invalid type values
func TestHandleMusicPlaybackTypeChange_InvalidType(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Pass non-string value (should log error and return)
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "", 123)

	// If we reach here without panic, the invalid type handling worked
}

// TestExecutePlayback tests the complete execution flow
func TestExecutePlayback(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up state variables for mute conditions
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetString("musicPlaybackType", "day")

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Set up mock to return "playing" state for playback verification
	mockClient.SetState("media_player.kitchen", "playing", nil)

	participants := []ParticipantWithVolume{
		{
			PlayerName:   "Kitchen",
			BaseVolume:   9,
			Volume:       9,
			LeaveMutedIf: []MuteCondition{},
		},
		{
			PlayerName: "Living Room",
			BaseVolume: 10,
			Volume:     10,
			LeaveMutedIf: []MuteCondition{
				{Variable: "isTVPlaying", Value: true},
			},
		},
	}

	option := PlaybackOption{
		URI:              "spotify:playlist:test",
		MediaType:        "playlist",
		VolumeMultiplier: 1.0,
	}

	_, _, err := manager.executePlayback("day", option, participants, "Kitchen")
	if err != nil {
		t.Errorf("executePlayback() failed: %v", err)
	}
}

// TestBuildSpeakerGroup tests speaker group building
func TestBuildSpeakerGroup(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Clear any previous calls
	mockClient.ClearServiceCalls()

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},
		{PlayerName: "Living Room", Volume: 10},
		{PlayerName: "Bedroom", Volume: 8},
	}

	result, err := manager.buildSpeakerGroup(participants, "media_player.kitchen")
	if err != nil {
		t.Errorf("buildSpeakerGroup() failed: %v", err)
	}
	if result == nil {
		t.Fatal("buildSpeakerGroup() returned nil result")
	}
	if result.ActiveCount != 3 {
		t.Errorf("Expected 3 active speakers, got %d", result.ActiveCount)
	}

	// Verify the service call was made with correct parameters
	calls := mockClient.GetServiceCalls()

	// Should be exactly one call (single call with all followers)
	joinCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls++

			// Verify entity_id is the lead speaker (coordinator)
			entityID, ok := call.Data["entity_id"].(string)
			if !ok || entityID != "media_player.kitchen" {
				t.Errorf("Expected entity_id to be lead speaker 'media_player.kitchen', got: %v", call.Data["entity_id"])
			}

			// Verify group_members contains all followers
			groupMembers, ok := call.Data["group_members"].([]string)
			if !ok {
				t.Errorf("Expected group_members to be []string, got: %T", call.Data["group_members"])
			} else {
				if len(groupMembers) != 2 {
					t.Errorf("Expected 2 group members, got %d", len(groupMembers))
				}
				// Check that Living Room and Bedroom are in group_members
				expectedMembers := map[string]bool{
					"media_player.living_room": true,
					"media_player.bedroom":     true,
				}
				for _, member := range groupMembers {
					if !expectedMembers[member] {
						t.Errorf("Unexpected group member: %s", member)
					}
				}
			}
		}
	}

	if joinCalls != 1 {
		t.Errorf("Expected exactly 1 media_player.join call, got %d", joinCalls)
	}
}

// TestBuildSpeakerGroupRetrySuccess tests that speaker group building retries on failure and succeeds
func TestBuildSpeakerGroupRetrySuccess(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Configure mock to fail twice, then succeed (simulating transient Sonos errors)
	mockClient.SetServiceFailCount("media_player", "join", 2, fmt.Errorf("service call failed: timeout waiting for response"))

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},
		{PlayerName: "Living Room", Volume: 10},
		{PlayerName: "Bedroom", Volume: 8},
	}

	// Should succeed after retries
	result, err := manager.buildSpeakerGroup(participants, "media_player.kitchen")
	if err != nil {
		t.Errorf("buildSpeakerGroup() should have succeeded after retries, got: %v", err)
	}
	if result == nil {
		t.Fatal("buildSpeakerGroup() returned nil result")
	}
	if result.ActiveCount != 3 {
		t.Errorf("Expected 3 active speakers, got %d", result.ActiveCount)
	}
}

// TestBuildSpeakerGroupPartialSuccess tests that speaker group building succeeds with partial group
// when some speakers are unavailable but at least one follower joins (proving lead is responsive)
func TestBuildSpeakerGroupPartialSuccess(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Configure mock to fail first 12 calls then succeed:
	// - Calls 1-6: batch join retries (all fail, maxSpeakerGroupRetries = 6)
	// - Calls 7-12: Living Room individual retries (all fail)
	// - Call 13+: Bedroom individual (succeeds)
	mockClient.SetServiceFailCount("media_player", "join", 12, fmt.Errorf("service call failed: Host is unreachable"))

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},
		{PlayerName: "Living Room", Volume: 10},
		{PlayerName: "Bedroom", Volume: 8},
	}

	// Should succeed with partial group (lead + Bedroom)
	result, err := manager.buildSpeakerGroup(participants, "media_player.kitchen")
	if err != nil {
		t.Errorf("buildSpeakerGroup() should succeed with partial group, got: %v", err)
	}
	if result == nil {
		t.Fatal("buildSpeakerGroup() returned nil result")
	}

	// Verify partial group results: lead + Bedroom active, Living Room failed
	if result.ActiveCount != 2 {
		t.Errorf("Expected 2 active speakers (lead + Bedroom), got %d", result.ActiveCount)
	}
	if result.FailedCount != 1 {
		t.Errorf("Expected 1 failed speaker, got %d", result.FailedCount)
	}
	if !result.LeadActive {
		t.Error("Expected lead speaker to be active")
	}

	// Verify individual speaker states
	if len(result.Results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(result.Results))
	}

	// First speaker (Kitchen/lead) should be active
	if !result.Results[0].Active {
		t.Error("Expected lead speaker (Kitchen) to be active")
	}
	// Second speaker (Living Room) should be failed
	if result.Results[1].Active {
		t.Error("Expected Living Room to be marked as failed")
	}
	if result.Results[1].FailureReason == "" {
		t.Error("Expected Living Room to have a failure reason")
	}
	// Third speaker (Bedroom) should be active
	if !result.Results[2].Active {
		t.Error("Expected Bedroom to be active")
	}
}

// TestBuildSpeakerGroupAllFail tests that speaker group building fails when all speakers are unavailable
func TestBuildSpeakerGroupAllFail(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Configure mock to always fail for join (simulating all speakers unreachable)
	mockClient.SetServiceError("media_player", "join", fmt.Errorf("service call failed: Host is unreachable"))

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},
		{PlayerName: "Living Room", Volume: 10},
	}

	// Should fail when all speakers are unavailable
	result, err := manager.buildSpeakerGroup(participants, "media_player.kitchen")
	if err == nil {
		t.Error("buildSpeakerGroup() should fail when all speakers are unavailable")
	}
	if result == nil {
		t.Fatal("buildSpeakerGroup() returned nil result")
	}

	// Verify failure results
	if result.ActiveCount != 0 {
		t.Errorf("Expected 0 active speakers, got %d", result.ActiveCount)
	}
	if result.FailedCount != 2 {
		t.Errorf("Expected 2 failed speakers, got %d", result.FailedCount)
	}
	if result.LeadActive {
		t.Error("Expected lead speaker to be marked as inactive")
	}

	// Both speakers should be marked as failed
	if result.Results[0].Active {
		t.Error("Expected lead speaker (Kitchen) to be marked as failed")
	}
	if result.Results[1].Active {
		t.Error("Expected Living Room to be marked as failed")
	}
}

// TestBuildSpeakerGroupRetryOnSecondAttempt tests successful retry on second attempt
func TestBuildSpeakerGroupRetryOnSecondAttempt(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Configure mock to fail once, then succeed
	mockClient.SetServiceFailCount("media_player", "join", 1, fmt.Errorf("service call failed: timeout waiting for response"))

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},
		{PlayerName: "Living Room", Volume: 10},
	}

	// Should succeed on second attempt
	result, err := manager.buildSpeakerGroup(participants, "media_player.kitchen")
	if err != nil {
		t.Errorf("buildSpeakerGroup() should have succeeded on retry, got: %v", err)
	}
	if result == nil {
		t.Fatal("buildSpeakerGroup() returned nil result")
	}
	if result.ActiveCount != 2 {
		t.Errorf("Expected 2 active speakers, got %d", result.ActiveCount)
	}
}

func TestManagerReset(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Create minimal music config
	musicConfig := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {},
		},
	}

	// Set up initial state
	stateManager.SetString("dayPhase", "morning")
	stateManager.SetBool("isMasterAsleep", false)
	stateManager.SetBool("isGuestAsleep", false)
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isAnyoneAsleep", false)

	manager := NewManager(mockClient, stateManager, musicConfig, logger, false, &plugin.RealTimeProvider{})

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Reset should re-select appropriate music mode
	err = manager.Reset()
	if err != nil {
		t.Fatalf("Reset() failed: %v", err)
	}
}

// TestCurrentlyPlayingMusicUri_SetOnPlayback tests that currentlyPlayingMusicUri
// is set when playback starts
func TestCurrentlyPlayingMusicUri_SetOnPlayback(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	testURI := "spotify:playlist:37i9dQZF1DX4dyzvuaRJ0n"

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: testURI, MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil)

	// Orchestrate playback
	err := manager.orchestratePlayback("day", "test_trigger")
	if err != nil {
		t.Fatalf("orchestratePlayback() failed: %v", err)
	}

	// Verify currentlyPlayingMusicUri was set
	currentURI, err := stateManager.GetString("currentlyPlayingMusicUri")
	if err != nil {
		t.Fatalf("Failed to get currentlyPlayingMusicUri: %v", err)
	}

	if currentURI != testURI {
		t.Errorf("Expected currentlyPlayingMusicUri = %q, got %q", testURI, currentURI)
	}
}

// TestCurrentlyPlayingMusicUri_ClearOnStop tests that currentlyPlayingMusicUri
// is cleared when playback stops
func TestCurrentlyPlayingMusicUri_ClearOnStop(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	testURI := "spotify:playlist:37i9dQZF1DX4dyzvuaRJ0n"

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: testURI, MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Set up currently playing music and URI
	manager.currentlyPlaying = &CurrentlyPlayingMusic{
		Type: "day",
		URI:  testURI,
	}
	_ = stateManager.SetString("currentlyPlayingMusicUri", testURI)

	// Verify URI is set before stopping
	currentURI, err := stateManager.GetString("currentlyPlayingMusicUri")
	if err != nil {
		t.Fatalf("Failed to get currentlyPlayingMusicUri before stop: %v", err)
	}
	if currentURI != testURI {
		t.Errorf("Before stop: expected currentlyPlayingMusicUri = %q, got %q", testURI, currentURI)
	}

	// Stop playback
	manager.stopPlayback()

	// Verify currentlyPlayingMusicUri was cleared
	currentURI, err = stateManager.GetString("currentlyPlayingMusicUri")
	if err != nil {
		t.Fatalf("Failed to get currentlyPlayingMusicUri after stop: %v", err)
	}

	if currentURI != "" {
		t.Errorf("Expected currentlyPlayingMusicUri to be empty after stop, got %q", currentURI)
	}
}

// TestCurrentlyPlayingMusicUri_UpdateOnModeChange tests that currentlyPlayingMusicUri
// is updated when music mode changes
func TestCurrentlyPlayingMusicUri_UpdateOnModeChange(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	dayURI := "spotify:playlist:day-playlist"
	eveningURI := "spotify:playlist:evening-playlist"

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: dayURI, MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
			"evening": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: eveningURI, MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	// Use a fixed time for testing (to avoid rate limiting)
	fixedTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, true, timeProvider)

	// Start with day music
	err := manager.orchestratePlayback("day", "test_trigger")
	if err != nil {
		t.Fatalf("orchestratePlayback(day) failed: %v", err)
	}

	// Verify day URI is set
	currentURI, err := stateManager.GetString("currentlyPlayingMusicUri")
	if err != nil {
		t.Fatalf("Failed to get currentlyPlayingMusicUri for day: %v", err)
	}
	if currentURI != dayURI {
		t.Errorf("Expected currentlyPlayingMusicUri = %q for day, got %q", dayURI, currentURI)
	}

	// Update time to avoid rate limiting
	timeProvider.FixedTime = fixedTime.Add(11 * time.Second)
	manager.timeProvider = timeProvider

	// Switch to evening music
	err = manager.orchestratePlayback("evening", "test_trigger")
	if err != nil {
		t.Fatalf("orchestratePlayback(evening) failed: %v", err)
	}

	// Verify evening URI is set
	currentURI, err = stateManager.GetString("currentlyPlayingMusicUri")
	if err != nil {
		t.Fatalf("Failed to get currentlyPlayingMusicUri for evening: %v", err)
	}
	if currentURI != eveningURI {
		t.Errorf("Expected currentlyPlayingMusicUri = %q for evening, got %q", eveningURI, currentURI)
	}
}

// TestFadeInSpeaker_SafeUnmuteSequence verifies that fadeInSpeaker follows the safe sequence:
// 1. Set volume to 0 (while still muted) to prevent sudden loud noise
// 2. Unmute the speaker
// 3. Fade in from 0 to target volume
// This is critical because Sonos speakers maintain mute state independently of volume,
// so unmuting before setting volume to 0 could cause sudden loud playback.
func TestFadeInSpeaker_SafeUnmuteSequence(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"evening": {},
		},
	}

	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	// NOT read-only so service calls actually go through
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider)
	// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up musicPlaybackType so fade-in doesn't abort early
	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Execute fade-in with a low target volume to complete quickly
	manager.fadeInSpeaker("Kitchen", 3, "evening")

	// Get all service calls
	calls := mockHA.GetServiceCalls()

	// Find the indices of key calls
	var initialVolumeSetIndex, unmuteIndex, firstFadeVolumeSetIndex int = -1, -1, -1
	for i, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			// First volume_set should be the safety set to 0
			if initialVolumeSetIndex == -1 {
				initialVolumeSetIndex = i
				// Verify it sets volume to 0
				if level, ok := call.Data["volume_level"].(float64); !ok || level != 0.0 {
					t.Errorf("First volume_set should set volume to 0.0, got %v", call.Data["volume_level"])
				}
			} else if firstFadeVolumeSetIndex == -1 {
				// Second volume_set is the start of the fade
				firstFadeVolumeSetIndex = i
			}
		}
		if call.Domain == "media_player" && call.Service == "volume_mute" && unmuteIndex == -1 {
			unmuteIndex = i

			// Verify entity_id
			if entityID, ok := call.Data["entity_id"].(string); !ok || entityID != "media_player.kitchen" {
				t.Errorf("Expected entity_id=media_player.kitchen, got %v", call.Data["entity_id"])
			}

			// Verify is_volume_muted is false (unmuting)
			if isMuted, ok := call.Data["is_volume_muted"].(bool); !ok || isMuted {
				t.Errorf("Expected is_volume_muted=false, got %v", call.Data["is_volume_muted"])
			}
		}
	}

	// Verify all expected calls were made
	if initialVolumeSetIndex == -1 {
		t.Error("fadeInSpeaker must set volume to 0 before unmuting")
	}
	if unmuteIndex == -1 {
		t.Error("fadeInSpeaker must call volume_mute service to unmute speaker")
	}

	// CRITICAL: Verify safe ordering - volume must be set to 0 BEFORE unmuting
	if initialVolumeSetIndex >= unmuteIndex {
		t.Errorf("SAFETY VIOLATION: volume_set to 0 (index %d) must come BEFORE volume_mute (index %d) to prevent sudden loud noise",
			initialVolumeSetIndex, unmuteIndex)
	}

	// Verify unmute happens before fade-in volume sets
	if firstFadeVolumeSetIndex != -1 && unmuteIndex >= firstFadeVolumeSetIndex {
		t.Errorf("volume_mute (index %d) must be called before fade-in volume_set (index %d)",
			unmuteIndex, firstFadeVolumeSetIndex)
	}
}

// TestFadeInSpeaker_VolumeNormalization verifies that fadeInSpeaker normalizes
// volume percentages (0-100) to Home Assistant's 0.0-1.0 scale correctly.
func TestFadeInSpeaker_VolumeNormalization(t *testing.T) {
	testCases := []struct {
		targetVolume  int
		expectedLevel float64
	}{
		{10, 0.10},  // 10% -> 0.10
		{25, 0.25},  // 25% -> 0.25
		{50, 0.50},  // 50% -> 0.50
		{100, 1.00}, // 100% -> 1.00
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("volume_%d_percent", tc.targetVolume), func(t *testing.T) {
			mockHA := ha.NewMockClient()
			logger := zap.NewNop()
			stateManager := state.NewManager(mockHA, logger, false)

			config := &MusicConfig{
				Music: map[string]MusicMode{
					"evening": {},
				},
			}

			fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
			timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
			manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider)
			// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
			manager.SetSleepFunc(func(d time.Duration) {})

			// Set up musicPlaybackType so fade-in doesn't abort
			if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
				t.Fatalf("Failed to set musicPlaybackType: %v", err)
			}

			manager.fadeInSpeaker("Kitchen", tc.targetVolume, "evening")

			// Get the final volume_set call
			calls := mockHA.GetServiceCalls()
			var lastVolumeSetCall *ha.ServiceCall
			for i := len(calls) - 1; i >= 0; i-- {
				if calls[i].Domain == "media_player" && calls[i].Service == "volume_set" {
					lastVolumeSetCall = &calls[i]
					break
				}
			}

			if lastVolumeSetCall == nil {
				t.Fatal("No volume_set call found")
			}

			actualLevel, ok := lastVolumeSetCall.Data["volume_level"].(float64)
			if !ok {
				t.Fatalf("volume_level is not a float64: %v", lastVolumeSetCall.Data["volume_level"])
			}

			// Allow small floating point tolerance
			if actualLevel < tc.expectedLevel-0.01 || actualLevel > tc.expectedLevel+0.01 {
				t.Errorf("Volume %d%% should normalize to %.2f, got %.2f",
					tc.targetVolume, tc.expectedLevel, actualLevel)
			}
		})
	}
}

// TestFadeInSpeaker_InitialVolumeSetFailure verifies that fadeInSpeaker aborts safely
// if the initial volume_set to 0 fails, without attempting to unmute.
func TestFadeInSpeaker_InitialVolumeSetFailure(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"evening": {},
		},
	}

	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider)
	// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up musicPlaybackType
	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Inject error for volume_set service call
	mockHA.SetServiceError("media_player", "volume_set", fmt.Errorf("simulated failure"))

	// Execute fade-in
	manager.fadeInSpeaker("Kitchen", 10, "evening")

	// Verify no unmute call was made (safety: don't unmute if we can't control volume)
	calls := mockHA.GetServiceCalls()
	for _, call := range calls {
		if call.Service == "volume_mute" {
			t.Error("volume_mute should NOT be called if initial volume_set fails")
		}
	}
}

// TestFadeInSpeaker_UnmuteFailure verifies that fadeInSpeaker aborts safely
// if the unmute call fails, without proceeding to fade-in.
func TestFadeInSpeaker_UnmuteFailure(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"evening": {},
		},
	}

	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider)
	// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up musicPlaybackType
	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Inject error for volume_mute service call only
	mockHA.SetServiceError("media_player", "volume_mute", fmt.Errorf("simulated unmute failure"))

	// Execute fade-in
	manager.fadeInSpeaker("Kitchen", 10, "evening")

	calls := mockHA.GetServiceCalls()

	// Should have exactly 1 call: the initial volume_set to 0
	// (volume_mute fails and returns error, so no further calls)
	volumeSetCount := 0
	for _, call := range calls {
		if call.Service == "volume_set" {
			volumeSetCount++
		}
	}

	// Only the initial safety volume_set to 0 should be recorded
	if volumeSetCount != 1 {
		t.Errorf("Expected exactly 1 volume_set call (initial safety set to 0), got %d", volumeSetCount)
	}
}

// TestRefreshAvailableSpeakers verifies that refreshAvailableSpeakers correctly
// caches media_player entities from Home Assistant.
func TestRefreshAvailableSpeakers(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	manager := NewManager(mockHA, stateManager, config, logger, false, nil)

	// Set up mock states with various entity types
	mockHA.SetState("media_player.kitchen", "idle", nil)
	mockHA.SetState("media_player.bedroom", "playing", nil)
	mockHA.SetState("light.living_room", "on", nil)
	mockHA.SetState("sensor.temperature", "22", nil)
	mockHA.SetState("media_player.soundbar", "off", nil)

	// Refresh speakers
	err := manager.refreshAvailableSpeakers()
	if err != nil {
		t.Fatalf("refreshAvailableSpeakers failed: %v", err)
	}

	// Verify only media_player entities are in the cache
	if !manager.isSpeakerAvailable("media_player.kitchen") {
		t.Error("Expected media_player.kitchen to be available")
	}
	if !manager.isSpeakerAvailable("media_player.bedroom") {
		t.Error("Expected media_player.bedroom to be available")
	}
	if !manager.isSpeakerAvailable("media_player.soundbar") {
		t.Error("Expected media_player.soundbar to be available")
	}

	// Non-media_player entities should not be available
	if manager.isSpeakerAvailable("light.living_room") {
		t.Error("light.living_room should NOT be in speaker cache")
	}
	if manager.isSpeakerAvailable("sensor.temperature") {
		t.Error("sensor.temperature should NOT be in speaker cache")
	}

	// Non-existent entity should not be available
	if manager.isSpeakerAvailable("media_player.nonexistent") {
		t.Error("media_player.nonexistent should NOT be available")
	}
}

// TestCallServiceWithRetry_Success verifies that callServiceWithRetry returns
// success on first attempt when service call succeeds.
func TestCallServiceWithRetry_Success(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	manager := NewManager(mockHA, stateManager, config, logger, false, nil)

	// Set up a speaker entity
	mockHA.SetState("media_player.kitchen", "idle", nil)

	err := manager.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
		"entity_id":    "media_player.kitchen",
		"volume_level": 0.5,
	})

	if err != nil {
		t.Errorf("Expected success, got error: %v", err)
	}

	// Verify only one service call was made (no retry needed)
	calls := mockHA.GetServiceCalls()
	count := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Expected 1 service call, got %d", count)
	}
}

// TestCallServiceWithRetry_PersistentError verifies that callServiceWithRetry
// returns an error when both the initial call and retry fail.
func TestCallServiceWithRetry_PersistentError(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	manager := NewManager(mockHA, stateManager, config, logger, false, nil)

	// Set up a speaker entity so refresh will find it (retry path is triggered)
	mockHA.SetState("media_player.kitchen", "idle", nil)

	// Set persistent error - both initial call and retry will fail
	mockHA.SetServiceError("media_player", "volume_set", fmt.Errorf("persistent failure"))

	err := manager.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
		"entity_id":    "media_player.kitchen",
		"volume_level": 0.5,
	})

	// Error should be returned since both attempts fail
	if err == nil {
		t.Error("Expected error when service call persistently fails")
	}

	// Verify the error contains useful context
	if err != nil && !strings.Contains(err.Error(), "persistent failure") {
		t.Errorf("Expected error to contain 'persistent failure', got: %v", err)
	}
}

// TestCallServiceWithRetry_SpeakerNotAvailable verifies that callServiceWithRetry
// returns a clear error when the speaker doesn't exist after refresh.
func TestCallServiceWithRetry_SpeakerNotAvailable(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	manager := NewManager(mockHA, stateManager, config, logger, false, nil)

	// Don't set up any media_player entities - speaker doesn't exist

	// Set error to trigger retry logic
	mockHA.SetServiceError("media_player", "volume_set", fmt.Errorf("entity not found"))

	err := manager.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
		"entity_id":    "media_player.nonexistent",
		"volume_level": 0.5,
	})

	if err == nil {
		t.Error("Expected error when speaker doesn't exist")
	}

	// Verify error message mentions speaker not available
	if err != nil && !strings.Contains(err.Error(), "not available") {
		t.Errorf("Expected 'not available' in error, got: %v", err)
	}
}

// TestBreakSpeakerGroups verifies that breakSpeakerGroups() calls unjoin
// on all participants to break them from existing groups.
func TestBreakSpeakerGroups(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Clear any previous calls
	mockClient.ClearServiceCalls()

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},
		{PlayerName: "Living Room", Volume: 10},
		{PlayerName: "Bedroom", Volume: 8},
	}

	manager.breakSpeakerGroups(participants)

	// Verify unjoin was called for each speaker
	calls := mockClient.GetServiceCalls()

	unjoinCalls := 0
	unjoinedSpeakers := make(map[string]bool)
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "unjoin" {
			unjoinCalls++
			entityID, ok := call.Data["entity_id"].(string)
			if ok {
				unjoinedSpeakers[entityID] = true
			}
		}
	}

	// Should have exactly 3 unjoin calls (one per speaker)
	if unjoinCalls != 3 {
		t.Errorf("Expected 3 unjoin calls, got %d", unjoinCalls)
	}

	// Verify each speaker was unjoined
	expectedSpeakers := []string{
		"media_player.kitchen",
		"media_player.living_room",
		"media_player.bedroom",
	}
	for _, expected := range expectedSpeakers {
		if !unjoinedSpeakers[expected] {
			t.Errorf("Expected unjoin call for %s", expected)
		}
	}
}

// TestBreakSpeakerGroups_UnjoinFailure verifies that breakSpeakerGroups()
// continues processing even if some unjoin calls fail.
func TestBreakSpeakerGroups_UnjoinFailure(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up speakers in mock so callServiceWithRetry can find them on retry
	mockClient.SetState("media_player.kitchen", "idle", nil)
	mockClient.SetState("media_player.living_room", "idle", nil)

	// Configure first unjoin to fail (will succeed on retry since speakers exist)
	mockClient.SetServiceFailCount("media_player", "unjoin", 1, fmt.Errorf("speaker not reachable"))

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},
		{PlayerName: "Living Room", Volume: 10},
	}

	// Should not panic or return error - just logs warning on initial failure, then succeeds on retry
	manager.breakSpeakerGroups(participants)

	// Verify speakers were processed
	calls := mockClient.GetServiceCalls()
	unjoinCalls := 0
	unjoinedSpeakers := make(map[string]bool)
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "unjoin" {
			unjoinCalls++
			if entityID, ok := call.Data["entity_id"].(string); ok {
				unjoinedSpeakers[entityID] = true
			}
		}
	}

	// Should have at least 2 unjoin calls (Kitchen may have retry + Living Room)
	if unjoinCalls < 2 {
		t.Errorf("Expected at least 2 unjoin calls, got %d", unjoinCalls)
	}

	// Both speakers should have been attempted
	if !unjoinedSpeakers["media_player.kitchen"] {
		t.Error("Expected unjoin call for media_player.kitchen")
	}
	if !unjoinedSpeakers["media_player.living_room"] {
		t.Error("Expected unjoin call for media_player.living_room")
	}
}

// TestExecutePlayback_BreakThenBuildSequence verifies that executePlayback()
// calls breakSpeakerGroups() before buildSpeakerGroup().
func TestExecutePlayback_BreakThenBuildSequence(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up state variables for mute conditions
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetString("musicPlaybackType", "day")

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up mock to return "playing" state for playback verification
	mockClient.SetState("media_player.kitchen", "playing", nil)

	// Clear any previous calls
	mockClient.ClearServiceCalls()

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", BaseVolume: 9, Volume: 9, LeaveMutedIf: []MuteCondition{}},
		{PlayerName: "Living Room", BaseVolume: 10, Volume: 10, LeaveMutedIf: []MuteCondition{}},
	}

	option := PlaybackOption{
		URI:              "spotify:playlist:test",
		MediaType:        "playlist",
		VolumeMultiplier: 1.0,
	}

	_, _, err := manager.executePlayback("day", option, participants, "Kitchen")
	if err != nil {
		t.Fatalf("executePlayback() failed: %v", err)
	}

	// Get all service calls
	calls := mockClient.GetServiceCalls()

	// Find the indices of unjoin and join calls
	var firstUnjoinIndex, firstJoinIndex int = -1, -1
	for i, call := range calls {
		if call.Domain == "media_player" && call.Service == "unjoin" && firstUnjoinIndex == -1 {
			firstUnjoinIndex = i
		}
		if call.Domain == "media_player" && call.Service == "join" && firstJoinIndex == -1 {
			firstJoinIndex = i
		}
	}

	// Verify both operations occurred
	if firstUnjoinIndex == -1 {
		t.Error("Expected unjoin calls to break existing groups")
	}
	if firstJoinIndex == -1 {
		t.Error("Expected join call to build new group")
	}

	// CRITICAL: Verify break happens BEFORE build
	if firstUnjoinIndex >= firstJoinIndex {
		t.Errorf("SEQUENCE ERROR: unjoin (index %d) must come BEFORE join (index %d)",
			firstUnjoinIndex, firstJoinIndex)
	}
}

// TestExecutePlayback_BreakThenBuildSequence_SingleSpeaker verifies that
// breakSpeakerGroups() is still called even for a single speaker.
func TestExecutePlayback_BreakThenBuildSequence_SingleSpeaker(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	_ = stateManager.SetString("musicPlaybackType", "day")

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up mock to return "playing" state for playback verification
	mockClient.SetState("media_player.kitchen", "playing", nil)

	// Clear any previous calls
	mockClient.ClearServiceCalls()

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", BaseVolume: 9, Volume: 9, LeaveMutedIf: []MuteCondition{}},
	}

	option := PlaybackOption{
		URI:              "spotify:playlist:test",
		MediaType:        "playlist",
		VolumeMultiplier: 1.0,
	}

	_, _, err := manager.executePlayback("day", option, participants, "Kitchen")
	if err != nil {
		t.Fatalf("executePlayback() failed: %v", err)
	}

	// Get all service calls
	calls := mockClient.GetServiceCalls()

	// Verify unjoin was still called for the single speaker
	unjoinCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "unjoin" {
			unjoinCalls++
		}
	}

	if unjoinCalls != 1 {
		t.Errorf("Expected 1 unjoin call for single speaker, got %d", unjoinCalls)
	}

	// Verify no join call (single speaker doesn't need grouping)
	joinCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls++
		}
	}

	if joinCalls != 0 {
		t.Errorf("Expected 0 join calls for single speaker, got %d", joinCalls)
	}
}

// TestStartValidatesSpeakers verifies that Start() refreshes and validates
// configured speakers on startup.
func TestStartValidatesSpeakers(t *testing.T) {
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 10},
					{PlayerName: "Missing Speaker", BaseVolume: 5},
				},
			},
			"day":      {},
			"evening":  {},
			"winddown": {},
			"sleep":    {},
			"sex":      {},
			"wakeup":   {},
		},
	}

	// Set up required state variables
	if err := stateManager.SetString("dayPhase", "day"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}
	if err := stateManager.SetBool("isAnyoneHome", true); err != nil {
		t.Fatalf("Failed to set isAnyoneHome: %v", err)
	}
	if err := stateManager.SetBool("isAnyoneAsleep", false); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}
	if err := stateManager.SetString("musicPlaybackType", ""); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	manager := NewManager(mockHA, stateManager, config, logger, false, nil)

	// Set up only one of the configured speakers
	mockHA.SetState("media_player.kitchen", "idle", nil)
	// Note: "Missing Speaker" (media_player.missing_speaker) is NOT set up

	// Start should succeed but log warning for missing speaker
	err := manager.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer manager.Stop()

	// Verify Kitchen is available after start
	if !manager.isSpeakerAvailable("media_player.kitchen") {
		t.Error("Expected media_player.kitchen to be available after Start")
	}

	// Verify Missing Speaker is NOT available
	if manager.isSpeakerAvailable("media_player.missing_speaker") {
		t.Error("Expected media_player.missing_speaker to NOT be available")
	}
}

// TestLoadPlaylistRotationFromHA tests loading playlist rotation from Home Assistant
func TestLoadPlaylistRotationFromHA(t *testing.T) {
	tests := []struct {
		name             string
		haValue          string
		expectedRotation map[string]int
		description      string
	}{
		{
			name:             "Valid JSON",
			haValue:          `{"morning":2,"day":5,"evening":1}`,
			expectedRotation: map[string]int{"morning": 2, "day": 5, "evening": 1},
			description:      "Valid JSON should be loaded correctly",
		},
		{
			name:             "Empty string",
			haValue:          "",
			expectedRotation: map[string]int{},
			description:      "Empty string should result in empty map",
		},
		{
			name:             "Empty JSON object",
			haValue:          "{}",
			expectedRotation: map[string]int{},
			description:      "Empty JSON object should result in empty map",
		},
		{
			name:             "Invalid JSON",
			haValue:          "not valid json",
			expectedRotation: map[string]int{},
			description:      "Invalid JSON should result in empty map (graceful fallback)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)

			// Set up the rotation value in HA
			_ = stateManager.SetString("musicPlaylistRotation", tt.haValue)

			// Create config with music types
			config := &MusicConfig{
				Music: map[string]MusicMode{
					"morning": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}},
					"day":     {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}, {URI: "test4"}, {URI: "test5"}, {URI: "test6"}}},
					"evening": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}}},
				},
			}
			manager := NewManager(mockClient, stateManager, config, logger, false, nil)

			// Load rotation from HA
			manager.loadPlaylistRotationFromHA()

			// Verify rotation was loaded correctly
			for musicType, expectedIndex := range tt.expectedRotation {
				actualIndex, exists := manager.playlistNumbers[musicType]
				if !exists {
					t.Errorf("%s: expected rotation for %s to exist", tt.description, musicType)
					continue
				}
				if actualIndex != expectedIndex {
					t.Errorf("%s: expected rotation[%s]=%d, got %d", tt.description, musicType, expectedIndex, actualIndex)
				}
			}

			// Verify no extra entries
			if len(manager.playlistNumbers) != len(tt.expectedRotation) {
				t.Errorf("%s: expected %d entries, got %d", tt.description, len(tt.expectedRotation), len(manager.playlistNumbers))
			}
		})
	}
}

// TestLoadPlaylistRotationBoundsCheck tests that indices exceeding playlist count are wrapped
func TestLoadPlaylistRotationBoundsCheck(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up rotation with index that exceeds playlist count
	// morning has 3 playlists (indices 0-2), but stored index is 5
	_ = stateManager.SetString("musicPlaylistRotation", `{"morning":5,"day":10}`)

	// Create config with limited playlists
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}}, // 3 options
			"day":     {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}}, // 3 options
			"evening": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}}, // not in HA
			"unknown": {PlaybackOptions: []PlaybackOption{}},                                               // empty options
		},
	}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Load rotation from HA
	manager.loadPlaylistRotationFromHA()

	// Verify morning index was wrapped: 5 % 3 = 2
	if manager.playlistNumbers["morning"] != 2 {
		t.Errorf("Expected morning index to be wrapped to 2 (5 %% 3), got %d", manager.playlistNumbers["morning"])
	}

	// Verify day index was wrapped: 10 % 3 = 1
	if manager.playlistNumbers["day"] != 1 {
		t.Errorf("Expected day index to be wrapped to 1 (10 %% 3), got %d", manager.playlistNumbers["day"])
	}
}

// TestLoadPlaylistRotationUnconfiguredType tests loading rotation for a music type not in config
func TestLoadPlaylistRotationUnconfiguredType(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up rotation with a type not in config
	_ = stateManager.SetString("musicPlaylistRotation", `{"oldtype":3,"morning":1}`)

	// Config doesn't have "oldtype"
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}},
		},
	}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Load rotation from HA
	manager.loadPlaylistRotationFromHA()

	// The unconfigured type should still be preserved (for future use)
	if manager.playlistNumbers["oldtype"] != 3 {
		t.Errorf("Expected unconfigured type 'oldtype' to be preserved with value 3, got %d", manager.playlistNumbers["oldtype"])
	}

	// Configured type should be loaded normally
	if manager.playlistNumbers["morning"] != 1 {
		t.Errorf("Expected morning to be 1, got %d", manager.playlistNumbers["morning"])
	}
}

// TestSyncPlaylistRotationToHA tests that playlist rotation is synced to HA after changes
func TestSyncPlaylistRotationToHA(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Initialize the rotation state
	_ = stateManager.SetString("musicPlaylistRotation", "{}")

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}},
		},
	}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Call getNextPlaylistIndex which should trigger a sync
	index := manager.getNextPlaylistIndex("day", 3)
	if index != 0 {
		t.Errorf("Expected first index to be 0, got %d", index)
	}

	// Wait for the sync goroutine to complete
	manager.WaitForSync()

	// Verify the rotation was synced to HA
	rotationJSON, err := stateManager.GetString("musicPlaylistRotation")
	if err != nil {
		t.Fatalf("Failed to get rotation from state manager: %v", err)
	}

	var rotation map[string]int
	if err := json.Unmarshal([]byte(rotationJSON), &rotation); err != nil {
		t.Fatalf("Failed to parse rotation JSON: %v", err)
	}

	// After first call, the stored value should be 1 (the NEXT index to use)
	if rotation["day"] != 1 {
		t.Errorf("Expected synced rotation[day]=1, got %d", rotation["day"])
	}

	// Call again to advance rotation
	index2 := manager.getNextPlaylistIndex("day", 3)
	if index2 != 1 {
		t.Errorf("Expected second index to be 1, got %d", index2)
	}

	// Wait for sync
	manager.WaitForSync()

	// Verify updated rotation
	rotationJSON, _ = stateManager.GetString("musicPlaylistRotation")
	_ = json.Unmarshal([]byte(rotationJSON), &rotation)
	if rotation["day"] != 2 {
		t.Errorf("Expected synced rotation[day]=2, got %d", rotation["day"])
	}
}

// TestPlaylistRotationSyncReadOnlyMode tests that sync is skipped in read-only mode
func TestPlaylistRotationSyncReadOnlyMode(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, true) // read-only mode

	// Initialize the rotation state
	_ = stateManager.SetString("musicPlaylistRotation", "{}")

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}},
		},
	}
	manager := NewManager(mockClient, stateManager, config, logger, true, nil)

	// Call getNextPlaylistIndex
	index := manager.getNextPlaylistIndex("day", 3)
	if index != 0 {
		t.Errorf("Expected first index to be 0, got %d", index)
	}

	// Wait for sync attempt
	manager.WaitForSync()

	// In read-only mode, the HA value should still be empty (sync skipped)
	rotationJSON, _ := stateManager.GetString("musicPlaylistRotation")
	if rotationJSON != "{}" {
		t.Errorf("Expected rotation to remain '{}' in read-only mode, got %s", rotationJSON)
	}

	// But the in-memory state should still work
	index2 := manager.getNextPlaylistIndex("day", 3)
	if index2 != 1 {
		t.Errorf("Expected second index to be 1, got %d", index2)
	}
}

// TestPlaybackVerification tests that playback verification detects and handles
// various speaker states correctly.
func TestPlaybackVerification(t *testing.T) {
	tests := []struct {
		name           string
		speakerState   string // The state GetState returns
		expectAttempts int    // Expected number of attempts (0 means verification should pass)
		expectError    bool   // Whether we expect an error
		description    string
	}{
		{
			name:           "Speaker playing on first try",
			speakerState:   "playing",
			expectAttempts: 1,
			expectError:    false,
			description:    "If speaker is playing after first play_media, verification should pass immediately",
		},
		{
			name:           "Speaker paused - requires retry",
			speakerState:   "paused",
			expectAttempts: 3, // Will exhaust retries
			expectError:    true,
			description:    "If speaker stays paused, verification fails after retries",
		},
		{
			name:           "Speaker idle - requires retry",
			speakerState:   "idle",
			expectAttempts: 3, // Will exhaust retries
			expectError:    true,
			description:    "If speaker stays idle, verification fails after retries",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)

			config := &MusicConfig{Music: map[string]MusicMode{}}
			manager := NewManager(mockClient, stateManager, config, logger, false, nil)

			// Use no-op sleep to make test fast
			manager.SetSleepFunc(func(d time.Duration) {})

			// Set up mock to return the test state
			mockClient.SetState("media_player.kitchen", tt.speakerState, nil)

			option := PlaybackOption{
				URI:              "spotify:playlist:test",
				MediaType:        "playlist",
				VolumeMultiplier: 1.0,
			}

			attempts, err := manager.startPlaybackWithVerification("media_player.kitchen", option)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if tt.expectAttempts > 0 && attempts != tt.expectAttempts {
				t.Errorf("Expected %d attempts, got %d", tt.expectAttempts, attempts)
			}
		})
	}
}

// TestIsPlaybackActive tests the speaker state checking function
func TestIsPlaybackActive(t *testing.T) {
	tests := []struct {
		name          string
		speakerState  string
		expectPlaying bool
	}{
		{"Playing state", "playing", true},
		{"Paused state", "paused", false},
		{"Idle state", "idle", false},
		{"Off state", "off", false},
		{"Unavailable state", "unavailable", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			mockClient := ha.NewMockClient()
			stateManager := state.NewManager(mockClient, logger, false)

			config := &MusicConfig{Music: map[string]MusicMode{}}
			manager := NewManager(mockClient, stateManager, config, logger, false, nil)

			mockClient.SetState("media_player.kitchen", tt.speakerState, nil)

			isPlaying, err := manager.isPlaybackActive("media_player.kitchen")
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if isPlaying != tt.expectPlaying {
				t.Errorf("Expected isPlaying=%v for state '%s', got %v",
					tt.expectPlaying, tt.speakerState, isPlaying)
			}
		})
	}
}

// TestPlaybackVerification_RecoveryAfterNudge tests that playback verification
// succeeds when the speaker starts playing after receiving the media_play nudge.
// This simulates the scenario where play_media is accepted but doesn't start playback,
// but the follow-up media_play command kicks it into action.
func TestPlaybackVerification_RecoveryAfterNudge(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up state sequence: first check returns "idle", second check (after nudge) returns "playing"
	// The verification flow is:
	// 1. Send play_media
	// 2. Wait, then GetState (returns "idle")
	// 3. Send media_play nudge
	// 4. Wait, then GetState (returns "playing") - SUCCESS
	mockClient.SetStateSequence("media_player.kitchen", []string{"idle", "playing"})

	option := PlaybackOption{
		URI:              "spotify:playlist:test",
		MediaType:        "playlist",
		VolumeMultiplier: 1.0,
	}

	attempts, err := manager.startPlaybackWithVerification("media_player.kitchen", option)

	if err != nil {
		t.Errorf("Expected success after nudge recovery, got error: %v", err)
	}
	if attempts != 1 {
		t.Errorf("Expected 1 attempt (recovered on first try via nudge), got %d", attempts)
	}

	// Verify that both play_media and media_play were called
	calls := mockClient.GetServiceCalls()
	var hasPlayMedia, hasMediaPlay bool
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "play_media" {
			hasPlayMedia = true
		}
		if call.Domain == "media_player" && call.Service == "media_play" {
			hasMediaPlay = true
		}
	}

	if !hasPlayMedia {
		t.Error("Expected play_media service call")
	}
	if !hasMediaPlay {
		t.Error("Expected media_play nudge service call")
	}
}

// TestPlaybackVerification_RecoveryOnSecondAttempt tests that playback verification
// succeeds when the speaker requires a full retry (not just a nudge) to start playing.
// This simulates when the first play_media and nudge both fail, but retrying works.
func TestPlaybackVerification_RecoveryOnSecondAttempt(t *testing.T) {
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil)

	// Use no-op sleep to make test fast
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up state sequence for recovery on second attempt:
	// Attempt 1: check 1 = "idle", check 2 (after nudge) = "idle" -> fails, retry
	// Attempt 2: check 3 = "playing" -> SUCCESS
	mockClient.SetStateSequence("media_player.kitchen", []string{"idle", "idle", "playing"})

	option := PlaybackOption{
		URI:              "spotify:playlist:test",
		MediaType:        "playlist",
		VolumeMultiplier: 1.0,
	}

	attempts, err := manager.startPlaybackWithVerification("media_player.kitchen", option)

	if err != nil {
		t.Errorf("Expected success on second attempt, got error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", attempts)
	}

	// Verify play_media was called twice (once per attempt)
	calls := mockClient.GetServiceCalls()
	playMediaCount := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "play_media" {
			playMediaCount++
		}
	}

	if playMediaCount != 2 {
		t.Errorf("Expected 2 play_media calls, got %d", playMediaCount)
	}
}
