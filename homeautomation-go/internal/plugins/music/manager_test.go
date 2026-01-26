package music

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"go.uber.org/zap"
)

func TestMusicManager_SelectAppropriateMusicMode(t *testing.T) {
	t.Parallel()
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
			manager := NewManager(mockHA, stateMgr, config, logger, true, timeProvider, nil)

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
	t.Parallel()
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateMgr := state.NewManager(mockHA, logger, false)
	config := &MusicConfig{}

	// Use a fixed time provider with a Monday (not Sunday) for testing
	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC) // Monday, January 6, 2025
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockHA, stateMgr, config, logger, true, timeProvider, nil)

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
	t.Parallel()
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

	manager := NewManager(mockHA, stateMgr, config, logger, true, timeProvider, nil)

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
	t.Parallel(
	// Create mock HA client and state manager
	)

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
	manager := NewManager(mockHA, stateMgr, config, logger, true, nil, nil)

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
	t.Parallel(
	// Find the repository root and construct path to config file
	)

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
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, config, logger, true, timeProvider, nil)

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

// TestStopDoesNotTriggerRateLimiting verifies that stop operations (setting musicPlaybackType
// to empty string) do not update the rate limiter, allowing the clear-then-set pattern
// used by sleep hygiene to force a music restart.
//
// This is the fix for: https://github.com/NickBorgers/home-automation/pull/486
// When cancelling a wake sequence while musicPlaybackType is already "sleep",
// sleep hygiene needs to clear the value first to force a notification, then set it
// back to "sleep" to restart playback. Without this fix, the second set would be
// rate-limited and music would stop but not restart.
func TestStopDoesNotTriggerRateLimiting(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Initialize state variables
	_ = stateManager.SetString("musicPlaybackType", "")

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"sleep": {
				Participants: []Participant{
					{PlayerName: "Bedroom", BaseVolume: 5, LeaveMutedIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:sleep", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	// Simulate the real scenario:
	// 1. Sleep music started hours ago when user went to bed (10 PM)
	// 2. User wakes up and cancels wake sequence (e.g., 3 AM)
	// 3. Sleep hygiene does clear-then-set pattern

	// Time when sleep music was initially started (10 PM)
	initialStartTime := time.Date(2024, 1, 1, 22, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: initialStartTime}

	manager := NewManager(mockClient, stateManager, config, logger, true, timeProvider, nil)

	// Start sleep music at 10 PM
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "", "sleep")
	if manager.currentlyPlaying == nil || manager.currentlyPlaying.Type != "sleep" {
		t.Fatal("Sleep music should have started")
	}

	// Fast forward to 3 AM (5 hours later) - user cancels wake sequence
	cancelWakeTime := initialStartTime.Add(5 * time.Hour)
	timeProvider.FixedTime = cancelWakeTime
	manager.timeProvider = timeProvider

	// Simulate clear-then-set pattern (what sleep hygiene does):
	// 1. Clear to "" to force a notification
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "sleep", "")
	if manager.currentlyPlaying != nil {
		t.Error("Music should have stopped after clearing")
	}

	// 2. Set back to "sleep" immediately (this should NOT be rate-limited)
	// Before the fix, step 1 would update lastPlaybackTime, causing this to be rate-limited.
	// After the fix, stop operations don't update lastPlaybackTime, so this succeeds.
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "", "sleep")
	if manager.currentlyPlaying == nil {
		t.Fatal("Music should have restarted - stop operations should not trigger rate limiting")
	}
	if manager.currentlyPlaying.Type != "sleep" {
		t.Errorf("Expected sleep music to restart, got type: %s", manager.currentlyPlaying.Type)
	}

	// Verify orchestratePlayback was called by checking playlist rotation
	manager.mu.RLock()
	rotationIndex := manager.playlistNumbers["sleep"]
	manager.mu.RUnlock()
	// After 2 plays (initial + restart), rotation should be at index 0 (wrapped from 1)
	// since there's only one playlist option
	if rotationIndex != 0 {
		t.Errorf("Expected rotation index to be 0 after 2 plays, got %d", rotationIndex)
	}
}

// TestDoubleActivationPrevention tests prevention of re-activating already playing music
func TestDoubleActivationPrevention(t *testing.T) {
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up state variables
	_ = stateManager.SetBool("isTVPlaying", true)
	_ = stateManager.SetBool("isMasterAsleep", false)

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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

// TestStopPlayback_OnlyAffectsActiveSpeakers verifies that stopPlayback only
// sets volume to 0 for speakers that were in the currentlyPlaying group,
// not all speakers across all modes in the config.
func TestStopPlayback_OnlyAffectsActiveSpeakers(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Config has speakers in multiple modes
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9},
					{PlayerName: "Soundbar", BaseVolume: 10}, // Soundbar only in morning mode
				},
				PlaybackOptions: []PlaybackOption{},
			},
			"evening": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9},
					{PlayerName: "Living Room", BaseVolume: 10}, // No Soundbar in evening
				},
				PlaybackOptions: []PlaybackOption{},
			},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Set up currently playing as EVENING mode (which does NOT include Soundbar)
	manager.currentlyPlaying = &CurrentlyPlayingMusic{
		Type: "evening",
		URI:  "spotify:playlist:evening",
		Participants: []ParticipantWithVolume{
			{PlayerName: "Kitchen", Volume: 9},
			{PlayerName: "Living Room", Volume: 10},
		},
	}

	// Stop playback
	manager.stopPlayback()

	// Get all volume_set calls
	calls := mockClient.GetServiceCalls()
	volumeSetCalls := make(map[string]bool)
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			if entityID, ok := call.Data["entity_id"].(string); ok {
				volumeSetCalls[entityID] = true
			}
		}
	}

	// Should have called volume_set on Kitchen and Living Room (evening mode speakers)
	if !volumeSetCalls["media_player.kitchen"] {
		t.Error("Expected volume_set call for Kitchen (was in evening mode)")
	}
	if !volumeSetCalls["media_player.living_room"] {
		t.Error("Expected volume_set call for Living Room (was in evening mode)")
	}

	// Should NOT have called volume_set on Soundbar (not in evening mode)
	if volumeSetCalls["media_player.soundbar"] {
		t.Error("Unexpected volume_set call for Soundbar (was NOT in evening mode)")
	}
}

// TestStopPlayback_NoCurrentPlayback verifies that stopPlayback handles
// the case where there is no active playback gracefully.
func TestStopPlayback_NoCurrentPlayback(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9},
					{PlayerName: "Soundbar", BaseVolume: 10},
				},
				PlaybackOptions: []PlaybackOption{},
			},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// No currently playing music
	manager.currentlyPlaying = nil

	// Stop playback - should not panic and should not call any volume_set
	manager.stopPlayback()

	// Should NOT have any volume_set calls since nothing was playing
	calls := mockClient.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			t.Errorf("Unexpected volume_set call when nothing was playing: %v", call.Data)
		}
	}
}

// TestOrchestratePlayback tests the main orchestration flow
func TestOrchestratePlayback(t *testing.T) {
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

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
	t.Parallel()
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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up various state variables
	_ = stateManager.SetBool("isTVPlaying", true)
	_ = stateManager.SetString("dayPhase", "evening")
	_ = stateManager.SetNumber("alarmTime", 7.5)

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}

	// Test in normal mode
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)
	err := manager.callService("media_player", "play_media", map[string]interface{}{
		"entity_id": "media_player.kitchen",
	})
	if err != nil {
		t.Errorf("callService() in normal mode failed: %v", err)
	}

	// Test in read-only mode
	managerRO := NewManager(mockClient, stateManager, config, logger, true, nil, nil)
	err = managerRO.callService("media_player", "play_media", map[string]interface{}{
		"entity_id": "media_player.kitchen",
	})
	if err != nil {
		t.Errorf("callService() in read-only mode failed: %v", err)
	}
}

// TestHandleMusicPlaybackTypeChange_EmptyString tests stopping playback
func TestHandleMusicPlaybackTypeChange_EmptyString(t *testing.T) {
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Pass non-string value (should log error and return)
	manager.handleMusicPlaybackTypeChange("musicPlaybackType", "", 123)

	// If we reach here without panic, the invalid type handling worked
}

// TestExecutePlayback tests the complete execution flow
func TestExecutePlayback(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up state variables for mute conditions
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetString("musicPlaybackType", "day")

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, musicConfig, logger, false, &plugin.RealTimeProvider{}, nil)

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

func TestManagerReset_WhenNoOneHome_StopsMusic(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	musicConfig := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {},
		},
	}

	// Set up state: no one is home
	stateManager.SetString("dayPhase", "morning")
	stateManager.SetBool("isMasterAsleep", false)
	stateManager.SetBool("isGuestAsleep", false)
	stateManager.SetBool("isAnyoneHome", false)
	stateManager.SetBool("isAnyoneAsleep", false)
	stateManager.SetString("musicPlaybackType", "morning")

	manager := NewManager(mockClient, stateManager, musicConfig, logger, false, &plugin.RealTimeProvider{}, nil)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Reset when no one is home should stop music and clear musicPlaybackType
	err = manager.Reset()
	if err != nil {
		t.Fatalf("Reset() failed: %v", err)
	}

	// Verify musicPlaybackType was cleared
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "" {
		t.Errorf("Expected musicPlaybackType to be empty when no one home, got %q", musicType)
	}
}

func TestManagerReset_WhenSomeoneAsleep_SelectsSleepMode(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	musicConfig := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {},
			"sleep":   {},
		},
	}

	// Set up state: someone is home and asleep
	stateManager.SetString("dayPhase", "morning")
	stateManager.SetBool("isMasterAsleep", true)
	stateManager.SetBool("isGuestAsleep", false)
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isAnyoneAsleep", true)
	stateManager.SetString("musicPlaybackType", "morning")

	manager := NewManager(mockClient, stateManager, musicConfig, logger, false, &plugin.RealTimeProvider{}, nil)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Reset when someone is asleep should select sleep mode (highest priority)
	err = manager.Reset()
	if err != nil {
		t.Fatalf("Reset() failed: %v", err)
	}

	// Verify sleep mode was selected
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "sleep" {
		t.Errorf("Expected musicPlaybackType to be 'sleep' when someone is asleep, got %q", musicType)
	}
}

// mutableTimeProvider is a test helper that allows changing the current time.
type mutableTimeProvider struct {
	mu      sync.Mutex
	current time.Time
}

func (m *mutableTimeProvider) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

func (m *mutableTimeProvider) SetNow(t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = t
}

// TestManagerReset_RestartsSameMode tests that Reset() can restart playback
// even when the current music mode is the same as the target mode.
// This validates the clear-then-set pattern fix (commit f9ea940).
func TestManagerReset_RestartsSameMode(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Create config with multiple playlists so we can detect rotation
	musicConfig := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:day1", MediaType: "playlist"},
					{URI: "spotify:playlist:day2", MediaType: "playlist"},
					{URI: "spotify:playlist:day3", MediaType: "playlist"},
				},
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9},
				},
			},
		},
	}

	// Set up initial state: day phase, someone home, no one asleep
	// Use dayPhase="day" so Reset() will also select "day" mode
	stateManager.SetString("dayPhase", "day")
	stateManager.SetBool("isMasterAsleep", false)
	stateManager.SetBool("isGuestAsleep", false)
	stateManager.SetBool("isAnyoneHome", true)
	stateManager.SetBool("isAnyoneAsleep", false)
	stateManager.SetString("musicPlaylistRotation", "{}")

	// Use mutable time provider to control rate limiting
	mockTime := &mutableTimeProvider{current: time.Date(2024, 1, 15, 12, 0, 0, 0, time.Local)} // Monday noon

	manager := NewManager(mockClient, stateManager, musicConfig, logger, false, mockTime, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip sleeps for faster tests

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Trigger initial playback by setting musicPlaybackType to day
	err = stateManager.SetString("musicPlaybackType", "day")
	if err != nil {
		t.Fatalf("Failed to set initial musicPlaybackType: %v", err)
	}

	// Wait for async sync
	manager.WaitForSync()

	// Get initial rotation index
	rotationJSON, err := stateManager.GetString("musicPlaylistRotation")
	if err != nil {
		t.Fatalf("Failed to get initial musicPlaylistRotation: %v", err)
	}
	var initialRotation map[string]int
	if err := json.Unmarshal([]byte(rotationJSON), &initialRotation); err != nil {
		t.Fatalf("Failed to parse initial rotation JSON: %v", err)
	}
	initialIndex := initialRotation["day"]

	// Advance time to avoid rate limiting (need > 10 seconds)
	mockTime.SetNow(time.Date(2024, 1, 15, 12, 1, 0, 0, time.Local)) // 1 minute later

	// Call Reset() - this should restart playback even though we're already in day mode
	err = manager.Reset()
	if err != nil {
		t.Fatalf("Reset() failed: %v", err)
	}

	// Wait for async sync
	manager.WaitForSync()

	// Verify musicPlaybackType is still "day"
	musicType, err := stateManager.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType after reset: %v", err)
	}
	if musicType != "day" {
		t.Errorf("Expected musicPlaybackType to remain 'day', got %q", musicType)
	}

	// Verify playlist rotation incremented (proves playback restarted)
	rotationJSON, err = stateManager.GetString("musicPlaylistRotation")
	if err != nil {
		t.Fatalf("Failed to get musicPlaylistRotation after reset: %v", err)
	}
	var finalRotation map[string]int
	if err := json.Unmarshal([]byte(rotationJSON), &finalRotation); err != nil {
		t.Fatalf("Failed to parse final rotation JSON: %v", err)
	}
	finalIndex := finalRotation["day"]

	// The rotation index should have incremented, proving playback was triggered
	if finalIndex <= initialIndex {
		t.Errorf("Expected playlist rotation to increment after Reset() (proving playback restarted), "+
			"initial=%d, final=%d", initialIndex, finalIndex)
	}
}

// TestCurrentlyPlayingMusicUri_SetOnPlayback tests that currentlyPlayingMusicUri
// is set when playback starts
func TestCurrentlyPlayingMusicUri_SetOnPlayback(t *testing.T) {
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

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
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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

	manager := NewManager(mockClient, stateManager, config, logger, true, timeProvider, nil)

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
	t.Parallel()
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
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider, nil)
	// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up musicPlaybackType so fade-in doesn't abort early
	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Execute fade-in with a low target volume to complete quickly
	manager.fadeInSpeaker(context.Background(), "Kitchen", 3, "evening")

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
	t.Parallel()
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
			manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider, nil)
			// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
			manager.SetSleepFunc(func(d time.Duration) {})

			// Set up musicPlaybackType so fade-in doesn't abort
			if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
				t.Fatalf("Failed to set musicPlaybackType: %v", err)
			}

			manager.fadeInSpeaker(context.Background(), "Kitchen", tc.targetVolume, "evening")

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
	t.Parallel()
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
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider, nil)
	// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up musicPlaybackType
	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Inject error for volume_set service call
	mockHA.SetServiceError("media_player", "volume_set", fmt.Errorf("simulated failure"))

	// Execute fade-in
	manager.fadeInSpeaker(context.Background(), "Kitchen", 10, "evening")

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
	t.Parallel()
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
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider, nil)
	// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up musicPlaybackType
	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Inject error for volume_mute service call only
	mockHA.SetServiceError("media_player", "volume_mute", fmt.Errorf("simulated unmute failure"))

	// Execute fade-in
	manager.fadeInSpeaker(context.Background(), "Kitchen", 10, "evening")

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

// TestFadeInSpeaker_HumanOverrideDetection verifies that fadeInSpeaker detects
// when a human manually lowers the speaker volume and aborts gracefully.
func TestFadeInSpeaker_HumanOverrideDetection(t *testing.T) {
	t.Parallel()
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
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider, nil)

	var volumeStep int
	manager.SetSleepFunc(func(d time.Duration) {
		// Simulate human override: after a few steps, set speaker volume lower than expected
		volumeStep++
		if volumeStep == 5 {
			// Set the speaker volume to 0, simulating human turning it down
			mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{
				"volume_level": 0.0, // Human turned volume down to 0
			})
		}
	})

	// Set up musicPlaybackType so fade-in doesn't abort due to type change
	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Initially speaker is available with volume 0
	mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{
		"volume_level": 0.5, // Initial volume
	})

	// Execute fade-in to 20% - should abort when human override is detected
	manager.fadeInSpeaker(context.Background(), "Kitchen", 20, "evening")

	// Verify shadow state shows human override was detected
	shadowState := manager.GetShadowState()
	fadeIn, exists := shadowState.Outputs.FadeInProgress["media_player.kitchen"]
	if !exists {
		t.Fatal("Expected fade-in progress to be recorded for media_player.kitchen")
	}

	if !fadeIn.HumanOverrideDetected {
		t.Error("Expected HumanOverrideDetected to be true")
	}

	// Fade should not complete to target volume (20) since it was aborted
	// The shadow state should show what volume was expected when override was detected
	if fadeIn.ExpectedVolume == 0 && fadeIn.ActualVolume == 0 && !fadeIn.HumanOverrideDetected {
		t.Error("Expected override detection to record expected and actual volumes")
	}
}

// TestFadeInSpeaker_NoHumanOverrideWithMatchingVolume verifies that fadeInSpeaker
// completes normally when the actual volume matches or exceeds expected volume.
func TestFadeInSpeaker_NoHumanOverrideWithMatchingVolume(t *testing.T) {
	t.Parallel()
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
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider, nil)

	var volumeStep int
	manager.SetSleepFunc(func(d time.Duration) {
		volumeStep++
		// Simulate normal volume - always matching what automation set
		mockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{
			"volume_level": float64(volumeStep) / 100.0,
		})
	})

	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Execute fade-in to 5%
	manager.fadeInSpeaker(context.Background(), "Kitchen", 5, "evening")

	// Verify shadow state does NOT show human override
	shadowState := manager.GetShadowState()
	fadeIn, exists := shadowState.Outputs.FadeInProgress["media_player.kitchen"]
	if !exists {
		t.Fatal("Expected fade-in progress to be recorded for media_player.kitchen")
	}

	if fadeIn.HumanOverrideDetected {
		t.Error("Expected HumanOverrideDetected to be false when volume matches")
	}

	// Fade state should be idle (completed)
	if shadowState.Outputs.FadeState != "idle" {
		t.Errorf("Expected FadeState to be 'idle', got '%s'", shadowState.Outputs.FadeState)
	}
}

// TestFadeInSpeaker_ContextCancellation verifies that fadeInSpeaker exits gracefully
// when its context is cancelled (e.g., when a new playback sequence starts).
// This prevents false "human override" detection when the new playback sets volume to 0.
func TestFadeInSpeaker_ContextCancellation(t *testing.T) {
	t.Parallel()
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
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider, nil)

	// Track if cancellation was detected
	var cancelledAtVolume int = -1

	// Use a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	manager.SetSleepFunc(func(d time.Duration) {
		// Cancel after the first few volume steps
		cancelledAtVolume++
		if cancelledAtVolume == 3 {
			cancel()
		}
	})

	if err := stateManager.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Execute fade-in to 10% - should be cancelled after ~3 steps
	manager.fadeInSpeaker(ctx, "Kitchen", 10, "evening")

	// Verify that fade-in stopped due to cancellation (not human override)
	shadowState := manager.GetShadowState()
	fadeIn, exists := shadowState.Outputs.FadeInProgress["media_player.kitchen"]
	if !exists {
		t.Fatal("Expected fade-in progress to be recorded for media_player.kitchen")
	}

	// Should NOT be marked as human override - context was cancelled
	if fadeIn.HumanOverrideDetected {
		t.Error("Expected HumanOverrideDetected to be false when cancelled via context")
	}

	// Fade should not have completed to target (10)
	if fadeIn.CurrentVolume >= 10 {
		t.Errorf("Expected fade-in to stop before reaching target volume 10, but got %d", fadeIn.CurrentVolume)
	}
}

// TestCancelAllFadeIns verifies that cancelAllFadeIns properly cancels all active fade-ins.
func TestCancelAllFadeIns(t *testing.T) {
	t.Parallel()
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
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider, nil)

	// Register some fake fade-in contexts
	ctx1 := manager.startFadeInWithContext("media_player.kitchen")
	ctx2 := manager.startFadeInWithContext("media_player.bedroom")

	// Verify contexts are not cancelled yet
	select {
	case <-ctx1.Done():
		t.Error("Context 1 should not be cancelled yet")
	default:
	}
	select {
	case <-ctx2.Done():
		t.Error("Context 2 should not be cancelled yet")
	default:
	}

	// Cancel all fade-ins
	manager.cancelAllFadeIns()

	// Verify both contexts are now cancelled
	select {
	case <-ctx1.Done():
		// Good, cancelled
	default:
		t.Error("Context 1 should be cancelled after cancelAllFadeIns")
	}
	select {
	case <-ctx2.Done():
		// Good, cancelled
	default:
		t.Error("Context 2 should be cancelled after cancelAllFadeIns")
	}
}

// TestStartFadeInWithContext_CancelsExisting verifies that starting a new fade-in
// for a speaker cancels any existing fade-in for that speaker.
func TestStartFadeInWithContext_CancelsExisting(t *testing.T) {
	t.Parallel()
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
	manager := NewManager(mockHA, stateManager, config, logger, false, timeProvider, nil)

	// Start first fade-in context
	ctx1 := manager.startFadeInWithContext("media_player.kitchen")

	// Start second fade-in context for same speaker
	ctx2 := manager.startFadeInWithContext("media_player.kitchen")

	// First context should be cancelled
	select {
	case <-ctx1.Done():
		// Good, cancelled
	default:
		t.Error("First context should be cancelled when second starts for same speaker")
	}

	// Second context should still be active
	select {
	case <-ctx2.Done():
		t.Error("Second context should not be cancelled yet")
	default:
		// Good, not cancelled
	}
}

// TestRefreshAvailableSpeakers verifies that refreshAvailableSpeakers correctly
// caches media_player entities from Home Assistant.
func TestRefreshAvailableSpeakers(t *testing.T) {
	t.Parallel()
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	manager := NewManager(mockHA, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	manager := NewManager(mockHA, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	manager := NewManager(mockHA, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	mockHA := ha.NewMockClient()
	logger := zap.NewNop()
	stateManager := state.NewManager(mockHA, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	manager := NewManager(mockHA, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Set up state variables for mute conditions
	_ = stateManager.SetBool("isTVPlaying", false)
	_ = stateManager.SetString("musicPlaybackType", "day")

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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

	// Wait for async speaker group building to complete
	// With no-op sleep and mock client, the goroutine runs almost immediately
	time.Sleep(50 * time.Millisecond)

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
		t.Error("Expected join call to build new group (async)")
	}

	// CRITICAL: Verify break happens BEFORE build (even with async, unjoin is synchronous)
	if firstJoinIndex != -1 && firstUnjoinIndex >= firstJoinIndex {
		t.Errorf("SEQUENCE ERROR: unjoin (index %d) must come BEFORE join (index %d)",
			firstUnjoinIndex, firstJoinIndex)
	}
}

// TestExecutePlayback_BreakThenBuildSequence_SingleSpeaker verifies that
// breakSpeakerGroups() is still called even for a single speaker.
func TestExecutePlayback_BreakThenBuildSequence_SingleSpeaker(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	_ = stateManager.SetString("musicPlaybackType", "day")

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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

	manager := NewManager(mockHA, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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
			manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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
	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

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
	t.Parallel()
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
			manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
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
			manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

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

// ============================================================================
// Phase 1: Zone Assignment Policy Tests
// ============================================================================

// TestShouldIncludeInZone_NoConditions tests that speakers without exclude_if conditions are always included
func TestShouldIncludeInZone_NoConditions(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	participant := Participant{
		PlayerName:   "Kitchen",
		BaseVolume:   9,
		LeaveMutedIf: []MuteCondition{},
		ExcludeIf:    []MuteCondition{}, // No exclude conditions
	}

	if !manager.shouldIncludeInZone(participant) {
		t.Error("Speaker with no exclude_if conditions should be included in zone")
	}
}

// TestShouldIncludeInZone_ConditionNotMatched tests that speakers are included when exclude_if condition doesn't match
func TestShouldIncludeInZone_ConditionNotMatched(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Set up state: isMasterAsleep = false
	if err := stateManager.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	participant := Participant{
		PlayerName:   "Bedroom",
		BaseVolume:   9,
		LeaveMutedIf: []MuteCondition{},
		ExcludeIf: []MuteCondition{
			{Variable: "isMasterAsleep", Value: true}, // Exclude if asleep
		},
	}

	// Condition is isMasterAsleep=true, but actual value is false
	// So the condition does NOT match, speaker should be INCLUDED
	if !manager.shouldIncludeInZone(participant) {
		t.Error("Speaker should be included when exclude_if condition (isMasterAsleep=true) doesn't match (value is false)")
	}
}

// TestShouldIncludeInZone_ConditionMatched tests that speakers are excluded when exclude_if condition matches
func TestShouldIncludeInZone_ConditionMatched(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Set up state: isMasterAsleep = true
	if err := stateManager.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}

	participant := Participant{
		PlayerName:   "Bedroom",
		BaseVolume:   9,
		LeaveMutedIf: []MuteCondition{},
		ExcludeIf: []MuteCondition{
			{Variable: "isMasterAsleep", Value: true}, // Exclude if asleep
		},
	}

	// Condition matches, speaker should be EXCLUDED
	if manager.shouldIncludeInZone(participant) {
		t.Error("Speaker should be excluded when exclude_if condition (isMasterAsleep=true) matches (value is true)")
	}
}

// TestShouldIncludeInZone_MultipleConditions tests that any matching condition excludes the speaker
func TestShouldIncludeInZone_MultipleConditions(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	tests := []struct {
		name             string
		isMasterAsleep   bool
		isGuestAsleep    bool
		expectedIncluded bool
	}{
		{
			name:             "Both conditions false - include",
			isMasterAsleep:   false,
			isGuestAsleep:    false,
			expectedIncluded: true,
		},
		{
			name:             "First condition true - exclude",
			isMasterAsleep:   true,
			isGuestAsleep:    false,
			expectedIncluded: false,
		},
		{
			name:             "Second condition true - exclude",
			isMasterAsleep:   false,
			isGuestAsleep:    true,
			expectedIncluded: false,
		},
		{
			name:             "Both conditions true - exclude",
			isMasterAsleep:   true,
			isGuestAsleep:    true,
			expectedIncluded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up state
			if err := stateManager.SetBool("isMasterAsleep", tt.isMasterAsleep); err != nil {
				t.Fatalf("Failed to set isMasterAsleep: %v", err)
			}
			if err := stateManager.SetBool("isGuestAsleep", tt.isGuestAsleep); err != nil {
				t.Fatalf("Failed to set isGuestAsleep: %v", err)
			}

			participant := Participant{
				PlayerName:   "Bedroom",
				BaseVolume:   9,
				LeaveMutedIf: []MuteCondition{},
				ExcludeIf: []MuteCondition{
					{Variable: "isMasterAsleep", Value: true},
					{Variable: "isGuestAsleep", Value: true},
				},
			}

			result := manager.shouldIncludeInZone(participant)
			if result != tt.expectedIncluded {
				t.Errorf("Expected shouldIncludeInZone=%v, got %v", tt.expectedIncluded, result)
			}
		})
	}
}

// TestOrchestratePlayback_WithExcludeIf tests that orchestratePlayback filters out excluded speakers
func TestOrchestratePlayback_WithExcludeIf(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{}},
					{PlayerName: "Bedroom", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{
						{Variable: "isMasterAsleep", Value: true}, // Exclude if asleep
					}},
					{PlayerName: "Living Room", BaseVolume: 10, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
			"day":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"evening":  {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"winddown": {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sleep":    {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sex":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"wakeup":   {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil) // read-only mode

	// Set up state: isMasterAsleep = true (Bedroom should be excluded)
	if err := stateManager.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateManager.SetString("currentlyPlayingMusicUri", ""); err != nil {
		t.Fatalf("Failed to set currentlyPlayingMusicUri: %v", err)
	}

	// Orchestrate playback
	err := manager.orchestratePlayback("morning", "test")
	if err != nil {
		t.Fatalf("orchestratePlayback failed: %v", err)
	}

	// Verify that only Kitchen and Living Room are in the participant list (Bedroom excluded)
	manager.mu.RLock()
	defer manager.mu.RUnlock()

	if manager.currentlyPlaying == nil {
		t.Fatal("currentlyPlaying should not be nil")
	}

	participants := manager.currentlyPlaying.Participants
	if len(participants) != 2 {
		t.Errorf("Expected 2 participants (Bedroom excluded), got %d", len(participants))
	}

	// Check that Kitchen and Living Room are present, Bedroom is not
	speakerNames := make(map[string]bool)
	for _, p := range participants {
		speakerNames[p.PlayerName] = true
	}

	if !speakerNames["Kitchen"] {
		t.Error("Kitchen should be in participants")
	}
	if !speakerNames["Living Room"] {
		t.Error("Living Room should be in participants")
	}
	if speakerNames["Bedroom"] {
		t.Error("Bedroom should NOT be in participants (excluded by isMasterAsleep=true)")
	}

	// Verify that Kitchen is the lead player (first non-excluded participant)
	if manager.currentlyPlaying.LeadPlayer != "Kitchen" {
		t.Errorf("Expected lead player to be Kitchen, got %s", manager.currentlyPlaying.LeadPlayer)
	}
}

// TestOrchestratePlayback_AllSpeakersExcluded tests error when all speakers are excluded
func TestOrchestratePlayback_AllSpeakersExcluded(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{PlayerName: "Bedroom", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{
						{Variable: "isMasterAsleep", Value: true},
					}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
			"day":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"evening":  {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"winddown": {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sleep":    {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sex":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"wakeup":   {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

	// Set up state: all speakers excluded
	if err := stateManager.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateManager.SetString("currentlyPlayingMusicUri", ""); err != nil {
		t.Fatalf("Failed to set currentlyPlayingMusicUri: %v", err)
	}

	// Orchestrate playback should fail
	err := manager.orchestratePlayback("morning", "test")
	if err == nil {
		t.Error("Expected error when all speakers are excluded")
	}

	// Check error message mentions exclude_if
	if err != nil && !strings.Contains(err.Error(), "exclude_if") {
		t.Errorf("Error message should mention exclude_if, got: %v", err)
	}
}

// TestCollectMuteConditionVariables_IncludesExcludeIf tests that exclude_if variables are collected
func TestCollectMuteConditionVariables_IncludesExcludeIf(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{
						PlayerName: "Kitchen",
						BaseVolume: 9,
						LeaveMutedIf: []MuteCondition{
							{Variable: "isTVPlaying", Value: true},
						},
						ExcludeIf: []MuteCondition{
							{Variable: "isMasterAsleep", Value: true},
						},
					},
					{
						PlayerName:   "Bedroom",
						BaseVolume:   9,
						LeaveMutedIf: []MuteCondition{},
						ExcludeIf: []MuteCondition{
							{Variable: "isGuestAsleep", Value: true},
						},
					},
				},
				PlaybackOptions: []PlaybackOption{},
			},
			"day":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"evening":  {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"winddown": {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sleep":    {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sex":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"wakeup":   {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	variables := manager.collectMuteConditionVariables()

	// Create map for easy lookup
	varMap := make(map[string]bool)
	for _, v := range variables {
		varMap[v] = true
	}

	// Check that leave_muted_if variables are collected
	if !varMap["isTVPlaying"] {
		t.Error("Expected isTVPlaying to be collected from leave_muted_if")
	}

	// Check that exclude_if variables are collected
	if !varMap["isMasterAsleep"] {
		t.Error("Expected isMasterAsleep to be collected from exclude_if")
	}
	if !varMap["isGuestAsleep"] {
		t.Error("Expected isGuestAsleep to be collected from exclude_if")
	}
}

// TestExcludeIf_LeadPlayerSelection tests that lead player is selected from included participants
func TestExcludeIf_LeadPlayerSelection(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					// First speaker is excluded
					{PlayerName: "Bedroom", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{
						{Variable: "isMasterAsleep", Value: true},
					}},
					// Second speaker should become lead
					{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{}},
					{PlayerName: "Living Room", BaseVolume: 10, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{}},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
			"day":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"evening":  {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"winddown": {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sleep":    {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sex":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"wakeup":   {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

	// Set up state: first participant (Bedroom) is excluded
	if err := stateManager.SetBool("isMasterAsleep", true); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateManager.SetString("currentlyPlayingMusicUri", ""); err != nil {
		t.Fatalf("Failed to set currentlyPlayingMusicUri: %v", err)
	}

	err := manager.orchestratePlayback("morning", "test")
	if err != nil {
		t.Fatalf("orchestratePlayback failed: %v", err)
	}

	// Kitchen (second in config) should be lead because Bedroom is excluded
	if manager.currentlyPlaying.LeadPlayer != "Kitchen" {
		t.Errorf("Expected Kitchen to be lead player (first non-excluded), got %s", manager.currentlyPlaying.LeadPlayer)
	}
}

// TestExcludeIf_ParticipantWithVolumePreservesExcludeIf tests that ExcludeIf is copied to ParticipantWithVolume
func TestExcludeIf_ParticipantWithVolumePreservesExcludeIf(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9,
						LeaveMutedIf: []MuteCondition{{Variable: "isTVPlaying", Value: true}},
						ExcludeIf:    []MuteCondition{{Variable: "isMasterAsleep", Value: true}},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
			"day":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"evening":  {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"winddown": {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sleep":    {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"sex":      {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
			"wakeup":   {Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}},
		},
	}

	manager := NewManager(mockClient, stateManager, config, logger, true, nil, nil)

	// Kitchen is not excluded (isMasterAsleep is false by default)
	if err := stateManager.SetBool("isMasterAsleep", false); err != nil {
		t.Fatalf("Failed to set isMasterAsleep: %v", err)
	}
	if err := stateManager.SetString("currentlyPlayingMusicUri", ""); err != nil {
		t.Fatalf("Failed to set currentlyPlayingMusicUri: %v", err)
	}

	err := manager.orchestratePlayback("morning", "test")
	if err != nil {
		t.Fatalf("orchestratePlayback failed: %v", err)
	}

	// Check that ExcludeIf was preserved in ParticipantWithVolume
	if len(manager.currentlyPlaying.Participants) != 1 {
		t.Fatalf("Expected 1 participant, got %d", len(manager.currentlyPlaying.Participants))
	}

	participant := manager.currentlyPlaying.Participants[0]
	if len(participant.ExcludeIf) != 1 {
		t.Errorf("Expected 1 ExcludeIf condition, got %d", len(participant.ExcludeIf))
	}
	if participant.ExcludeIf[0].Variable != "isMasterAsleep" {
		t.Errorf("Expected ExcludeIf variable isMasterAsleep, got %s", participant.ExcludeIf[0].Variable)
	}
}

// TestRandomJitter tests that randomJitter returns values within expected bounds
func TestRandomJitter(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Run multiple iterations to test the distribution
	const iterations = 1000
	for i := 0; i < iterations; i++ {
		jitter := manager.randomJitter()

		// Jitter should be >= 0 and < asyncJoinJitterMax (15s)
		if jitter < 0 {
			t.Errorf("randomJitter() returned negative value: %v", jitter)
		}
		if jitter >= asyncJoinJitterMax {
			t.Errorf("randomJitter() returned value >= max: %v >= %v", jitter, asyncJoinJitterMax)
		}
	}
}

// TestBuildSpeakerGroupAsync_StaggeredDelays verifies that speakers launch with staggered delays
func TestBuildSpeakerGroupAsync_StaggeredDelays(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Track sleep durations to verify staggered delays
	var sleepMu sync.Mutex
	var sleepDurations []time.Duration

	manager.SetSleepFunc(func(d time.Duration) {
		sleepMu.Lock()
		sleepDurations = append(sleepDurations, d)
		sleepMu.Unlock()
	})

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},      // Lead - no delay
		{PlayerName: "Living Room", Volume: 10}, // Follower 1 - 0 * stagger + jitter
		{PlayerName: "Bedroom", Volume: 8},      // Follower 2 - 1 * stagger + jitter
		{PlayerName: "Office", Volume: 7},       // Follower 3 - 2 * stagger + jitter
	}

	manager.buildSpeakerGroupAsync(participants, "media_player.kitchen", "day")

	sleepMu.Lock()
	defer sleepMu.Unlock()

	// We should have at least 3 stagger delays (one for each follower's initial delay)
	// Plus potentially more for retries. The first follower's delay is just jitter (0-15s),
	// second follower is 15s + jitter, third is 30s + jitter.
	if len(sleepDurations) < 3 {
		t.Errorf("Expected at least 3 sleep calls for stagger delays, got %d", len(sleepDurations))
	}

	// Verify join calls were made
	calls := mockClient.GetServiceCalls()
	joinCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls++
		}
	}

	// Should have 3 join calls (one per follower)
	if joinCalls != 3 {
		t.Errorf("Expected 3 join calls for 3 followers, got %d", joinCalls)
	}
}

// TestBuildSpeakerGroupAsync_ParallelExecution verifies goroutines run in parallel, not blocked
func TestBuildSpeakerGroupAsync_ParallelExecution(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Track which speakers had their join called
	var joinMu sync.Mutex
	var joinedSpeakers []string

	// Capture the join calls to track order
	originalCallService := mockClient.GetServiceCalls
	mockClient.ClearServiceCalls()

	// Use no-op sleep so the test runs fast
	manager.SetSleepFunc(func(d time.Duration) {})

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},      // Lead
		{PlayerName: "Living Room", Volume: 10}, // Follower 1
		{PlayerName: "Bedroom", Volume: 8},      // Follower 2
	}

	manager.buildSpeakerGroupAsync(participants, "media_player.kitchen", "day")

	// Get all service calls
	calls := originalCallService()

	// Extract joined speakers from calls
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			if members, ok := call.Data["group_members"].([]string); ok {
				joinMu.Lock()
				joinedSpeakers = append(joinedSpeakers, members...)
				joinMu.Unlock()
			}
		}
	}

	// Both followers should have been joined
	if len(joinedSpeakers) != 2 {
		t.Errorf("Expected 2 speakers to join, got %d", len(joinedSpeakers))
	}

	expectedSpeakers := map[string]bool{
		"media_player.living_room": true,
		"media_player.bedroom":     true,
	}
	for _, speaker := range joinedSpeakers {
		if !expectedSpeakers[speaker] {
			t.Errorf("Unexpected speaker joined: %s", speaker)
		}
	}
}

// TestBuildSpeakerGroupAsync_SingleFollower tests with just one follower
func TestBuildSpeakerGroupAsync_SingleFollower(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	manager.SetSleepFunc(func(d time.Duration) {})

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},      // Lead
		{PlayerName: "Living Room", Volume: 10}, // Only follower
	}

	mockClient.ClearServiceCalls()
	manager.buildSpeakerGroupAsync(participants, "media_player.kitchen", "day")

	calls := mockClient.GetServiceCalls()
	joinCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls++
		}
	}

	if joinCalls != 1 {
		t.Errorf("Expected 1 join call for single follower, got %d", joinCalls)
	}
}

// TestJoinSpeakerWithRetry_Success tests successful speaker join on first attempt
func TestJoinSpeakerWithRetry_Success(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	manager.SetSleepFunc(func(d time.Duration) {})

	participant := ParticipantWithVolume{
		PlayerName:   "Living Room",
		Volume:       10,
		LeaveMutedIf: []MuteCondition{},
	}

	mockClient.ClearServiceCalls()
	manager.joinSpeakerWithRetry(participant, "media_player.kitchen", "day")

	calls := mockClient.GetServiceCalls()

	// Should have exactly 1 join call (success on first attempt)
	joinCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls++
			// Verify correct parameters
			if call.Data["entity_id"] != "media_player.kitchen" {
				t.Errorf("Expected entity_id 'media_player.kitchen', got %v", call.Data["entity_id"])
			}
			if members, ok := call.Data["group_members"].([]string); ok {
				if len(members) != 1 || members[0] != "media_player.living_room" {
					t.Errorf("Expected group_members ['media_player.living_room'], got %v", members)
				}
			}
		}
	}

	if joinCalls != 1 {
		t.Errorf("Expected 1 join call on success, got %d", joinCalls)
	}
}

// TestJoinSpeakerWithRetry_RetryOnTransientError tests retry behavior on transient errors
func TestJoinSpeakerWithRetry_RetryOnTransientError(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Track retry delays
	var sleepMu sync.Mutex
	var sleepDurations []time.Duration

	manager.SetSleepFunc(func(d time.Duration) {
		sleepMu.Lock()
		sleepDurations = append(sleepDurations, d)
		sleepMu.Unlock()
	})

	// Fail first 2 calls (callServiceWithRetry makes 2 calls per attempt: initial + internal retry)
	// So 2 failures means the first joinSpeakerWithRetry attempt fails completely,
	// then the second attempt (calls 3+) succeeds
	mockClient.SetServiceFailCount("media_player", "join", 2, fmt.Errorf("service call failed: timeout"))

	participant := ParticipantWithVolume{
		PlayerName:   "Living Room",
		Volume:       10,
		LeaveMutedIf: []MuteCondition{},
	}

	mockClient.ClearServiceCalls()
	manager.joinSpeakerWithRetry(participant, "media_player.kitchen", "day")

	calls := mockClient.GetServiceCalls()

	// Mock only records SUCCESSFUL calls. With 2 failures configured:
	// - Attempt 1: calls fail (not recorded)
	// - Attempt 2: succeeds (recorded)
	// So we expect 1 recorded join call
	joinCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls++
		}
	}

	if joinCalls != 1 {
		t.Errorf("Expected 1 successful join call recorded, got %d", joinCalls)
	}

	// Should have 1 retry delay (after first attempt failed)
	sleepMu.Lock()
	defer sleepMu.Unlock()

	if len(sleepDurations) < 1 {
		t.Errorf("Expected at least 1 retry delay, got %d", len(sleepDurations))
	}

	// Verify exponential backoff: first delay should be ~30s + jitter
	if len(sleepDurations) >= 1 {
		// First retry delay: 30s base + 0-15s jitter
		if sleepDurations[0] < 30*time.Second || sleepDurations[0] >= 45*time.Second {
			t.Errorf("First retry delay should be 30-45s, got %v", sleepDurations[0])
		}
	}
}

// TestJoinSpeakerWithRetry_ExponentialBackoff verifies exponential backoff caps at max delay
func TestJoinSpeakerWithRetry_ExponentialBackoff(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Track retry delays
	var sleepMu sync.Mutex
	var sleepDurations []time.Duration

	manager.SetSleepFunc(func(d time.Duration) {
		sleepMu.Lock()
		sleepDurations = append(sleepDurations, d)
		sleepMu.Unlock()
	})

	// Fail first 5 attempts to test backoff progression and capping
	mockClient.SetServiceFailCount("media_player", "join", 5, fmt.Errorf("service call failed: timeout"))

	participant := ParticipantWithVolume{
		PlayerName:   "Living Room",
		Volume:       10,
		LeaveMutedIf: []MuteCondition{},
	}

	mockClient.ClearServiceCalls()
	manager.joinSpeakerWithRetry(participant, "media_player.kitchen", "day")

	sleepMu.Lock()
	defer sleepMu.Unlock()

	// Should have 5 retry delays (after attempts 1-5, no delay after last attempt 6)
	if len(sleepDurations) != 5 {
		t.Errorf("Expected 5 retry delays, got %d", len(sleepDurations))
	}

	// Verify backoff progression (base + jitter):
	// Attempt 1 fail: delay = 30s * 2^0 = 30s + jitter
	// Attempt 2 fail: delay = 30s * 2^1 = 60s + jitter (capped at 60s)
	// Attempt 3 fail: delay = 30s * 2^2 = 120s -> capped to 60s + jitter
	// Attempt 4 fail: delay = 30s * 2^3 = 240s -> capped to 60s + jitter
	// Attempt 5 fail: delay = 30s * 2^4 = 480s -> capped to 60s + jitter

	if len(sleepDurations) >= 3 {
		// After first failure: 30s + jitter
		if sleepDurations[0] < 30*time.Second || sleepDurations[0] >= 45*time.Second {
			t.Errorf("First retry delay should be 30-45s, got %v", sleepDurations[0])
		}

		// After second failure onward: capped at 60s + jitter (75s max)
		for i := 1; i < len(sleepDurations); i++ {
			if sleepDurations[i] < 60*time.Second || sleepDurations[i] >= 75*time.Second {
				t.Errorf("Retry delay %d should be 60-75s (capped), got %v", i+1, sleepDurations[i])
			}
		}
	}
}

// TestJoinSpeakerWithRetry_PermanentError tests that permanent errors skip retries
func TestJoinSpeakerWithRetry_PermanentError(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	var sleepCallCount int
	manager.SetSleepFunc(func(d time.Duration) {
		sleepCallCount++
	})

	// Configure permanent error (entity not found)
	// This error contains "entity not found" which is in the permanentErrors list
	mockClient.SetServiceFailCount("media_player", "join", 100, fmt.Errorf("service call failed: entity not found"))

	participant := ParticipantWithVolume{
		PlayerName:   "Nonexistent Speaker",
		Volume:       10,
		LeaveMutedIf: []MuteCondition{},
	}

	mockClient.ClearServiceCalls()
	manager.joinSpeakerWithRetry(participant, "media_player.kitchen", "day")

	// With permanent error detection, joinSpeakerWithRetry should exit after first attempt
	// without retrying. Since all calls fail (permanent error), no calls are recorded.
	// The key assertion is that sleepCallCount should be 0 (no retry delays)

	// No retry delays should have occurred because permanent error exits early
	if sleepCallCount != 0 {
		t.Errorf("Expected no retry delays for permanent error, got %d", sleepCallCount)
	}
}

// TestJoinSpeakerWithRetry_MaxRetriesExhausted tests behavior when all retries are exhausted
func TestJoinSpeakerWithRetry_MaxRetriesExhausted(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Track retry delays to verify we retry maxAsyncSpeakerRetries-1 times
	// (the last attempt doesn't sleep after failure)
	var sleepMu sync.Mutex
	var sleepCount int

	manager.SetSleepFunc(func(d time.Duration) {
		sleepMu.Lock()
		sleepCount++
		sleepMu.Unlock()
	})

	// Fail all attempts (more than maxAsyncSpeakerRetries * 2 for callServiceWithRetry internal retries)
	// Using transient error (timeout) that won't trigger permanent error detection
	mockClient.SetServiceFailCount("media_player", "join", 100, fmt.Errorf("service call failed: timeout waiting for response"))

	participant := ParticipantWithVolume{
		PlayerName:   "Living Room",
		Volume:       10,
		LeaveMutedIf: []MuteCondition{},
	}

	mockClient.ClearServiceCalls()
	manager.joinSpeakerWithRetry(participant, "media_player.kitchen", "day")

	sleepMu.Lock()
	actualSleepCount := sleepCount
	sleepMu.Unlock()

	// Since mock only records successful calls and all calls fail,
	// we verify retry count via sleepCount.
	// joinSpeakerWithRetry calls sleep after each failed attempt except the last.
	// With maxAsyncSpeakerRetries=6, we expect 5 retry delays (sleep after attempts 1-5, not after 6)
	expectedSleepCount := maxAsyncSpeakerRetries - 1
	if actualSleepCount != expectedSleepCount {
		t.Errorf("Expected %d retry delays (max retries - 1), got %d", expectedSleepCount, actualSleepCount)
	}
}

// TestBuildSpeakerGroupAsync_WaitGroupCompletion verifies WaitGroup waits for all goroutines
func TestBuildSpeakerGroupAsync_WaitGroupCompletion(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	// Use a channel to track when goroutines complete
	var completionMu sync.Mutex
	completedCount := 0

	// Track sleep calls to verify ordering
	manager.SetSleepFunc(func(d time.Duration) {
		// Simulate a brief delay so we can verify WaitGroup waits
		time.Sleep(10 * time.Millisecond)
	})

	// Wrap service calls to track completion
	originalCalls := 0
	mockClient.ClearServiceCalls()

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},      // Lead
		{PlayerName: "Living Room", Volume: 10}, // Follower 1
		{PlayerName: "Bedroom", Volume: 8},      // Follower 2
		{PlayerName: "Office", Volume: 7},       // Follower 3
	}

	// buildSpeakerGroupAsync should block until all goroutines complete
	manager.buildSpeakerGroupAsync(participants, "media_player.kitchen", "day")

	// After buildSpeakerGroupAsync returns, all joins should be complete
	calls := mockClient.GetServiceCalls()
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			completionMu.Lock()
			completedCount++
			completionMu.Unlock()
		}
	}

	originalCalls = completedCount

	// All 3 followers should have completed their join attempts
	if originalCalls != 3 {
		t.Errorf("Expected 3 completed join calls after WaitGroup.Wait(), got %d", originalCalls)
	}
}

// TestBuildSpeakerGroupAsync_NoFollowers tests with only lead speaker (no followers)
func TestBuildSpeakerGroupAsync_NoFollowers(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	manager.SetSleepFunc(func(d time.Duration) {})

	// Only lead speaker, no followers
	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9}, // Lead only
	}

	mockClient.ClearServiceCalls()

	// Should complete immediately without any join calls
	manager.buildSpeakerGroupAsync(participants, "media_player.kitchen", "day")

	calls := mockClient.GetServiceCalls()

	// Should have 0 join calls (no followers to join)
	joinCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls++
		}
	}

	if joinCalls != 0 {
		t.Errorf("Expected 0 join calls with no followers, got %d", joinCalls)
	}
}

// TestBuildSpeakerGroupAsync_IndependentFailures verifies one speaker failure doesn't block others
func TestBuildSpeakerGroupAsync_IndependentFailures(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(mockClient, stateManager, config, logger, false, nil, nil)

	manager.SetSleepFunc(func(d time.Duration) {})

	// Since goroutines run in parallel and the mock uses a global fail counter,
	// we can't easily isolate failures to specific speakers.
	// Instead, we test that both goroutines complete (function returns) even when
	// some calls fail, proving they don't block each other.

	// Fail first 10 calls, then succeed. With 2 followers running in parallel:
	// - Each follower makes multiple calls (due to callServiceWithRetry internal retry)
	// - Eventually calls succeed after the fail count is exhausted
	mockClient.SetServiceFailCount("media_player", "join", 10, fmt.Errorf("service call failed: timeout"))

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},      // Lead
		{PlayerName: "Living Room", Volume: 10}, // Follower 1
		{PlayerName: "Bedroom", Volume: 8},      // Follower 2
	}

	mockClient.ClearServiceCalls()

	// Key test: buildSpeakerGroupAsync should complete (not hang) even with failures
	// This proves goroutines don't block each other
	done := make(chan bool)
	go func() {
		manager.buildSpeakerGroupAsync(participants, "media_player.kitchen", "day")
		done <- true
	}()

	select {
	case <-done:
		// Success - function completed
	case <-time.After(5 * time.Second):
		t.Fatal("buildSpeakerGroupAsync timed out - goroutines may be blocking each other")
	}

	// Verify at least one successful call was made (after failures exhausted)
	calls := mockClient.GetServiceCalls()
	joinCalls := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls++
		}
	}

	// After 10 failures, subsequent calls should succeed. With 2 parallel goroutines
	// retrying, we expect at least some successful calls (from both followers)
	if joinCalls < 1 {
		t.Errorf("Expected at least 1 successful join call after failures exhausted, got %d", joinCalls)
	}
}
