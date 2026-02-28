package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMusicManager_ZoneResolutionSelectsCorrectMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		isAnyoneHome      bool
		isAnyoneAsleep    bool
		dayPhase          string
		expectedMusicType string
		description       string
	}{
		{
			name:              "No one home - no zone active",
			isAnyoneHome:      false,
			isAnyoneAsleep:    false,
			dayPhase:          "day",
			expectedMusicType: "",
			description:       "When no one is home, no zone should be active",
		},
		{
			name:              "Someone asleep - sleep mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    true,
			dayPhase:          "day",
			expectedMusicType: "sleep",
			description:       "Sleep zone has highest priority",
		},
		{
			name:              "Morning - morning mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "morning",
			expectedMusicType: "morning",
			description:       "Morning phase triggers morning zone",
		},
		{
			name:              "Day - day mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "day",
			expectedMusicType: "day",
			description:       "Day phase triggers day zone",
		},
		{
			name:              "Sunset - evening mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "sunset",
			expectedMusicType: "evening",
			description:       "Sunset phase triggers evening zone",
		},
		{
			name:              "Dusk - evening mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "dusk",
			expectedMusicType: "evening",
			description:       "Dusk phase triggers evening zone",
		},
		{
			name:              "Winddown - winddown mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "winddown",
			expectedMusicType: "winddown",
			description:       "Winddown phase triggers winddown zone",
		},
		{
			name:              "Night - winddown mode",
			isAnyoneHome:      true,
			isAnyoneAsleep:    false,
			dayPhase:          "night",
			expectedMusicType: "winddown",
			description:       "Night phase triggers winddown zone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)

			// Create music config with participants and playback options (needed for zone start)
			testParticipant := []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}
			testPlayback := []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}
			config := &MusicConfig{
				Music: map[string]MusicMode{
					"morning":  {Participants: testParticipant, PlaybackOptions: testPlayback},
					"day":      {Participants: testParticipant, PlaybackOptions: testPlayback},
					"evening":  {Participants: testParticipant, PlaybackOptions: testPlayback},
					"winddown": {Participants: testParticipant, PlaybackOptions: testPlayback},
					"sleep":    {Participants: testParticipant, PlaybackOptions: testPlayback},
					"sex":      {Participants: testParticipant, PlaybackOptions: testPlayback},
					"wakeup":   {Participants: testParticipant, PlaybackOptions: testPlayback},
				},
			}
			// Set up mock speaker
			env.MockHA.SetState("media_player.kitchen", "idle", nil)

			fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC) // Monday
			timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, timeProvider, nil)

			// Set up state
			if err := env.StateMgr.SetBool("isAnyoneHome", tt.isAnyoneHome); err != nil {
				t.Fatalf("Failed to set isAnyoneHome: %v", err)
			}
			if err := env.StateMgr.SetBool("isAnyoneAsleep", tt.isAnyoneAsleep); err != nil {
				t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
			}
			if err := env.StateMgr.SetString("dayPhase", tt.dayPhase); err != nil {
				t.Fatalf("Failed to set dayPhase: %v", err)
			}
			if err := env.StateMgr.SetString("musicPlaybackType", ""); err != nil {
				t.Fatalf("Failed to set musicPlaybackType: %v", err)
			}

			// Initialize zone manager (ensureZones is called by Start, but
			// we call it directly here to test zone resolution without full startup)
			config.ensureZones()
			manager.zoneManager = NewZoneManager(manager, config, env.Logger)

			// Resolve zones
			err := manager.zoneManager.ResolveZones("test")
			if err != nil {
				t.Fatalf("Failed to resolve zones: %v", err)
			}

			// Verify result
			actualMusicType, err := env.StateMgr.GetString("musicPlaybackType")
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

func TestEnsureZones_DayPhaseMapping(t *testing.T) {
	t.Parallel()

	// Verify that ensureZones generates trigger configurations that correctly
	// map day phases to music zones, replacing the legacy determineMusicModeFromDayPhase.
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	config.ensureZones()

	// Find zones by name
	zoneByName := make(map[string]ZoneConfig)
	for _, z := range config.Zones {
		zoneByName[z.Name] = z
	}

	// Sleep: triggers on isAnyoneAsleep=true, isAnyoneHome=true, isWakeSequenceActive=false
	sleepZone := zoneByName["sleep"]
	if len(sleepZone.Triggers) != 3 {
		t.Errorf("Sleep zone should have 3 triggers, got %d", len(sleepZone.Triggers))
	}

	// Morning: trigger_groups for normal morning OR wake sequence active
	morningZone := zoneByName["morning"]
	if len(morningZone.TriggerGroups) != 2 {
		t.Errorf("Morning zone should have 2 trigger groups, got %d", len(morningZone.TriggerGroups))
	}

	// Evening: trigger_groups for sunset/dusk/evening
	eveningZone := zoneByName["evening"]
	if len(eveningZone.TriggerGroups) != 3 {
		t.Errorf("Evening zone should have 3 trigger groups, got %d", len(eveningZone.TriggerGroups))
	}

	// Winddown: trigger_groups for winddown/night
	winddownZone := zoneByName["winddown"]
	if len(winddownZone.TriggerGroups) != 2 {
		t.Errorf("Winddown zone should have 2 trigger groups, got %d", len(winddownZone.TriggerGroups))
	}

	// Sex/wakeup: no triggers (manually activated)
	sexZone := zoneByName["sex"]
	if len(sexZone.Triggers) != 0 {
		t.Errorf("Sex zone should have no triggers, got %d", len(sexZone.Triggers))
	}
	wakeupZone := zoneByName["wakeup"]
	if len(wakeupZone.Triggers) != 0 {
		t.Errorf("Wakeup zone should have no triggers, got %d", len(wakeupZone.Triggers))
	}
}

func TestMusicManager_StateChangeHandling(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	testParticipant := []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}
	defaultOption := []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning":  {Participants: testParticipant, PlaybackOptions: defaultOption},
			"day":      {Participants: testParticipant, PlaybackOptions: defaultOption},
			"evening":  {Participants: testParticipant, PlaybackOptions: defaultOption},
			"winddown": {Participants: testParticipant, PlaybackOptions: defaultOption},
			"sleep":    {Participants: testParticipant, PlaybackOptions: defaultOption},
			"sex":      {Participants: testParticipant, PlaybackOptions: defaultOption},
			"wakeup":   {Participants: testParticipant, PlaybackOptions: defaultOption},
		},
	}

	// Use a fixed time provider with a Monday (not Sunday) for testing
	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC) // Monday, January 6, 2025
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, timeProvider, nil)

	// Set up mock speaker
	env.MockHA.SetState("media_player.kitchen", "idle", nil)

	// Set initial state
	if err := env.StateMgr.SetBool("isAnyoneHome", true); err != nil {
		t.Fatalf("Failed to set isAnyoneHome: %v", err)
	}
	if err := env.StateMgr.SetBool("isAnyoneAsleep", false); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}
	if err := env.StateMgr.SetString("dayPhase", "day"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}
	if err := env.StateMgr.SetString("musicPlaybackType", ""); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Start manager (which subscribes to state changes and runs initial zone resolution)
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Initial zone resolution should activate day zone
	musicType, err := env.StateMgr.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "day" {
		t.Errorf("Expected initial music type 'day', got %q", musicType)
	}

	// Change to sunset phase - should trigger evening zone via zone resolution
	if err := env.StateMgr.SetString("dayPhase", "sunset"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}

	// Give the subscription callback time to execute
	time.Sleep(100 * time.Millisecond)

	musicType, err = env.StateMgr.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "evening" {
		t.Errorf("Expected music type 'evening' after sunset, got %q", musicType)
	}

	// Someone goes to sleep - should trigger sleep zone (highest priority)
	if err := env.StateMgr.SetBool("isAnyoneAsleep", true); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}

	// Give the subscription callback time to execute
	time.Sleep(100 * time.Millisecond)

	musicType, err = env.StateMgr.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType: %v", err)
	}
	if musicType != "sleep" {
		t.Errorf("Expected music type 'sleep' when someone is asleep, got %q", musicType)
	}
}

func TestMusicManager_Stop(t *testing.T) {
	t.Parallel()
	// Create mock HA client and state manager

	env := testutil.NewEnv(t)

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
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	// Set initial state
	if err := env.StateMgr.SetBool("isAnyoneHome", true); err != nil {
		t.Fatalf("Failed to set isAnyoneHome: %v", err)
	}
	if err := env.StateMgr.SetBool("isAnyoneAsleep", false); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}
	if err := env.StateMgr.SetString("dayPhase", "day"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}

	// Start manager
	if err := manager.Start(); err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Verify subscriptions were created (dayPhase, isAnyoneAsleep, isAnyoneHome, isWakeSequenceActive, musicPlaybackType)
	if len(manager.subscriptions) != 5 {
		t.Errorf("Expected 5 subscriptions, got %d", len(manager.subscriptions))
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
	t.Parallel()
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

// TestProductionConfig_PrimaryBathroomFollowsBedroom validates that the Primary Bathroom
// speaker is included in every music mode where Bedroom appears. This ensures the two
// speakers in the primary suite are always grouped together.
//
// Issue #739: User observed winddown music playing without Primary Bathroom.
func TestProductionConfig_PrimaryBathroomFollowsBedroom(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	configPath := filepath.Join(repoRoot, "configs", "music_config.yaml")

	config, err := LoadConfig(configPath)
	require.NoError(t, err, "Failed to load production music config")

	// For every mode where Bedroom appears, Primary Bathroom must also appear
	for modeName, mode := range config.Music {
		bedroomFound := false
		bathroomFound := false

		for _, p := range mode.Participants {
			if p.PlayerName == "Bedroom" {
				bedroomFound = true
			}
			if p.PlayerName == "Primary Bathroom" {
				bathroomFound = true
			}
		}

		if bedroomFound {
			assert.True(t, bathroomFound,
				"Mode %q includes Bedroom but not Primary Bathroom — "+
					"Primary Bathroom should follow Bedroom in all modes", modeName)
		}
	}
}

// TestProductionConfig_WinddownZoneAssignment validates that when dayPhase=winddown,
// both Bedroom and Primary Bathroom are assigned to the winddown zone.
//
// Issue #739: Primary Bathroom speaker not included in winddown music.
func TestProductionConfig_WinddownZoneAssignment(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	configPath := filepath.Join(repoRoot, "configs", "music_config.yaml")

	config, err := LoadConfig(configPath)
	require.NoError(t, err, "Failed to load production music config")

	// Create a manager with the production config to test zone assignment
	env := testutil.NewEnv(t)

	fixedTime := time.Date(2024, 1, 15, 22, 30, 0, 0, time.UTC) // 10:30 PM = winddown
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set state: winddown phase, someone home, nobody asleep
	_ = env.StateMgr.SetString("dayPhase", "winddown")
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isAnyoneAsleep", false)
	_ = env.StateMgr.SetBool("isMasterAsleep", false)
	_ = env.StateMgr.SetBool("isWakeSequenceActive", false)
	_ = env.StateMgr.SetString("musicPlaybackType", "")
	_ = env.StateMgr.SetBool("isTVPlaying", false)

	err = manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(100 * time.Millisecond)
	manager.WaitForSync()

	// Verify winddown zone is active
	zoneToSpeakers, _ := manager.zoneManager.assignSpeakersToZones()

	winddownSpeakers, winddownActive := zoneToSpeakers["winddown"]
	require.True(t, winddownActive, "Winddown zone should be active when dayPhase=winddown")

	// Both Bedroom and Primary Bathroom must be in the winddown zone
	speakerSet := make(map[string]bool)
	for _, s := range winddownSpeakers {
		speakerSet[s] = true
	}

	assert.True(t, speakerSet["Bedroom"],
		"Bedroom should be assigned to winddown zone, got speakers: %v", winddownSpeakers)
	assert.True(t, speakerSet["Primary Bathroom"],
		"Primary Bathroom should be assigned to winddown zone (follows Bedroom), got speakers: %v", winddownSpeakers)
}

func TestMusicManager_ReadOnlyMode(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	// Create state manager in read-only mode
	readOnlyStateMgr := state.NewManager(env.MockHA, env.Logger, true)

	// Initialize required state variables (can set because they're LocalOnly or initial sync)
	_ = readOnlyStateMgr.SetBool("isAnyoneHome", true)
	_ = readOnlyStateMgr.SetBool("isAnyoneAsleep", false)
	_ = readOnlyStateMgr.SetString("dayPhase", "day")
	_ = readOnlyStateMgr.SetString("musicPlaybackType", "")

	defaultOption := []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day":   {PlaybackOptions: defaultOption},
			"sleep": {PlaybackOptions: defaultOption},
		},
	}

	manager := NewManager(context.Background(), env.MockHA, readOnlyStateMgr, config, env.Logger, true, nil, nil)

	// Initialize zone manager (ensureZones populates zones from music modes)
	config.ensureZones()
	manager.zoneManager = NewZoneManager(manager, config, env.Logger)

	// Test zone resolution in read-only mode - should handle gracefully
	_ = manager.zoneManager.ResolveZones("test")

	// Test with sleep scenario
	_ = readOnlyStateMgr.SetBool("isAnyoneAsleep", true)
	_ = manager.zoneManager.ResolveZones("test-sleep")

	// Test with no one home
	_ = readOnlyStateMgr.SetBool("isAnyoneHome", false)
	_ = manager.zoneManager.ResolveZones("test-nohome")

	// If we get here without panicking, the read-only mode handling worked correctly
}

// TestCalculateVolume tests volume calculation
func TestCalculateVolume(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	tests := []struct {
		name       string
		baseVolume int
		multiplier float64
		expected   int
	}{
		{"No multiplier", 9, 1.0, 9},
		{"1.5x multiplier", 10, 1.5, 15},
		{"Rounds correctly", 9, 1.1, 10},
		{"Above old cap", 16, 1.1, 18},
		{"Caps at 30", 20, 2.0, 30},
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
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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

// TestRateLimiting tests that zone resolution is idempotent — resolving zones
// when a zone is already active doesn't restart playback (effectively rate-limiting).
func TestRateLimiting(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

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

	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isAnyoneAsleep", false)
	_ = env.StateMgr.SetString("dayPhase", "day")
	_ = env.StateMgr.SetString("musicPlaybackType", "")
	env.MockHA.SetState("media_player.kitchen", "idle", nil)

	fixedTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, timeProvider, nil)
	config.ensureZones()
	manager.zoneManager = NewZoneManager(manager, config, env.Logger)

	// First resolution should start the day zone
	err := manager.zoneManager.ResolveZones("test-first")
	if err != nil {
		t.Fatalf("First resolve failed: %v", err)
	}

	// Wait for async zone playback goroutine to complete
	time.Sleep(100 * time.Millisecond)

	manager.mu.RLock()
	playing := manager.currentlyPlaying
	manager.mu.RUnlock()
	if playing == nil {
		t.Fatal("First playback should have succeeded")
	}

	// Clear service calls
	env.MockHA.ClearServiceCalls()

	// Immediate second resolution should not restart (zone already active)
	err = manager.zoneManager.ResolveZones("test-second")
	if err != nil {
		t.Fatalf("Second resolve failed: %v", err)
	}

	// Verify no new service calls (zone was already active, effectively rate-limited)
	serviceCalls := env.MockHA.GetServiceCalls()
	mediaPlayerCalls := 0
	for _, call := range serviceCalls {
		if call.Domain == "media_player" {
			mediaPlayerCalls++
		}
	}
	if mediaPlayerCalls != 0 {
		t.Errorf("Expected 0 media_player calls on second resolution, got %d", mediaPlayerCalls)
	}
}

// TestResetRestartsSleepMusic verifies that Reset() can restart music via zone resolution.
// This replaces the legacy clear-then-set pattern used by sleep hygiene.
// Scenario: Sleep music playing → Reset → sleep zone re-activates.
func TestResetRestartsSleepMusic(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

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

	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isAnyoneAsleep", true)
	_ = env.StateMgr.SetString("dayPhase", "night")
	_ = env.StateMgr.SetString("musicPlaybackType", "")
	env.MockHA.SetState("media_player.bedroom", "idle", nil)

	initialStartTime := time.Date(2024, 1, 1, 22, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: initialStartTime}

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, timeProvider, nil)

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Sleep zone should be active
	musicType, _ := env.StateMgr.GetString("musicPlaybackType")
	if musicType != "sleep" {
		t.Fatalf("Expected sleep music after startup, got %q", musicType)
	}

	// Reset should stop and restart the sleep zone
	err = manager.Reset()
	if err != nil {
		t.Fatalf("Reset() failed: %v", err)
	}

	// Sleep zone should be active again
	musicType, _ = env.StateMgr.GetString("musicPlaybackType")
	if musicType != "sleep" {
		t.Errorf("Expected sleep music after reset, got %q", musicType)
	}
}

// TestDoubleActivationPrevention tests that zone resolution is idempotent -
// resolving zones when a zone is already active doesn't restart playback.
func TestDoubleActivationPrevention(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

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

	// Set state for day zone activation
	_ = env.StateMgr.SetBool("isAnyoneHome", true)
	_ = env.StateMgr.SetBool("isAnyoneAsleep", false)
	_ = env.StateMgr.SetString("dayPhase", "day")
	_ = env.StateMgr.SetString("musicPlaybackType", "")
	env.MockHA.SetState("media_player.kitchen", "idle", nil)

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)
	config.ensureZones()
	manager.zoneManager = NewZoneManager(manager, config, env.Logger)

	// First zone resolution should start the day zone
	err := manager.zoneManager.ResolveZones("test-first")
	if err != nil {
		t.Fatalf("First resolve failed: %v", err)
	}

	activeZones := manager.zoneManager.GetActiveZones()
	if len(activeZones) != 1 {
		t.Fatalf("Expected 1 active zone, got %d", len(activeZones))
	}

	// Clear service calls from first resolution
	env.MockHA.ClearServiceCalls()

	// Second resolution should NOT restart the zone (idempotent)
	err = manager.zoneManager.ResolveZones("test-second")
	if err != nil {
		t.Fatalf("Second resolve failed: %v", err)
	}

	// Verify no service calls (zone already active, nothing changed)
	serviceCalls := env.MockHA.GetServiceCalls()
	mediaPlayerCalls := 0
	for _, call := range serviceCalls {
		if call.Domain == "media_player" {
			mediaPlayerCalls++
		}
	}
	if mediaPlayerCalls != 0 {
		t.Errorf("Expected 0 media_player calls on second resolution (zone already active), got %d", mediaPlayerCalls)
	}
}

// TestMuteConditionEvaluation tests mute condition logic
func TestMuteConditionEvaluation(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	// Set up state variables
	_ = env.StateMgr.SetBool("isTVPlaying", true)
	_ = env.StateMgr.SetBool("isMasterAsleep", false)

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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
	env := testutil.NewEnv(t)

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

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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
	env := testutil.NewEnv(t)

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

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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
	calls := env.MockHA.GetServiceCalls()
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

// TestOrchestratePlayback tests the main orchestration flow
func TestOrchestratePlayback(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

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

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

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

// TestHandleMusicPlaybackTypeChange_EmptyString tests stopping playback
func TestHandleMusicPlaybackTypeChange_EmptyString(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

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

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

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

// TestExecutePlayback tests the complete execution flow
func TestExecutePlayback(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	// Set up state variables for mute conditions
	_ = env.StateMgr.SetBool("isTVPlaying", false)
	_ = env.StateMgr.SetBool("isMasterAsleep", false)
	_ = env.StateMgr.SetString("musicPlaybackType", "day")

	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Set up mock to return "playing" state for playback verification
	env.MockHA.SetState("media_player.kitchen", "playing", nil)

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
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Clear any previous calls
	env.MockHA.ClearServiceCalls()

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
	calls := env.MockHA.GetServiceCalls()

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

// TestBuildSpeakerGroupOutcomes tests different outcomes of speaker group building
func TestBuildSpeakerGroupOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		participants   []ParticipantWithVolume
		failCount      int // 0 = no failures, -1 = permanent error
		failErr        string
		expectErr      bool
		expectedActive int
		expectedFailed int
		leadActive     bool
	}{
		{
			name: "Retry succeeds after transient failures",
			participants: []ParticipantWithVolume{
				{PlayerName: "Kitchen", Volume: 9},
				{PlayerName: "Living Room", Volume: 10},
				{PlayerName: "Bedroom", Volume: 8},
			},
			failCount:      2,
			failErr:        "service call failed: timeout waiting for response",
			expectedActive: 3,
			leadActive:     true,
		},
		{
			name: "Partial success with some speakers unavailable",
			participants: []ParticipantWithVolume{
				{PlayerName: "Kitchen", Volume: 9},
				{PlayerName: "Living Room", Volume: 10},
				{PlayerName: "Bedroom", Volume: 8},
			},
			failCount:      12, // batch retries + Living Room individual all fail, Bedroom succeeds
			failErr:        "service call failed: Host is unreachable",
			expectedActive: 2,
			expectedFailed: 1,
			leadActive:     true,
		},
		{
			name: "All speakers fail",
			participants: []ParticipantWithVolume{
				{PlayerName: "Kitchen", Volume: 9},
				{PlayerName: "Living Room", Volume: 10},
			},
			failCount:      -1, // permanent error
			failErr:        "service call failed: Host is unreachable",
			expectErr:      true,
			expectedActive: 0,
			expectedFailed: 2,
			leadActive:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)
			config := &MusicConfig{Music: map[string]MusicMode{}}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
			manager.SetSleepFunc(func(d time.Duration) {})
			env.MockHA.ClearServiceCalls()

			if tt.failCount == -1 {
				env.MockHA.SetServiceError("media_player", "join", errors.New(tt.failErr))
			} else if tt.failCount > 0 {
				env.MockHA.SetServiceFailCount("media_player", "join", tt.failCount, errors.New(tt.failErr))
			}

			result, err := manager.buildSpeakerGroup(tt.participants, "media_player.kitchen")
			if tt.expectErr && err == nil {
				t.Error("Expected error")
			} else if !tt.expectErr && err != nil {
				t.Errorf("Expected success, got: %v", err)
			}
			if result == nil {
				t.Fatal("result should not be nil")
			}
			if result.ActiveCount != tt.expectedActive {
				t.Errorf("Expected %d active speakers, got %d", tt.expectedActive, result.ActiveCount)
			}
			if tt.expectedFailed > 0 && result.FailedCount != tt.expectedFailed {
				t.Errorf("Expected %d failed speakers, got %d", tt.expectedFailed, result.FailedCount)
			}
			if result.LeadActive != tt.leadActive {
				t.Errorf("Expected LeadActive=%v, got %v", tt.leadActive, result.LeadActive)
			}
		})
	}
}

func TestManagerReset(t *testing.T) {
	t.Parallel()
	testParticipant := []Participant{{PlayerName: "Kitchen", BaseVolume: 9}}
	testPlayback := []PlaybackOption{{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0}}

	tests := []struct {
		name             string
		modes            map[string]MusicMode
		isAnyoneHome     bool
		isMasterAsleep   bool
		isAnyoneAsleep   bool
		initialPlayback  string
		expectedPlayback string // empty means check it was cleared
	}{
		{
			name:            "Re-selects appropriate mode",
			modes:           map[string]MusicMode{"morning": {Participants: testParticipant, PlaybackOptions: testPlayback}},
			isAnyoneHome:    true,
			initialPlayback: "",
		},
		{
			name:             "No one home stops music",
			modes:            map[string]MusicMode{"morning": {Participants: testParticipant, PlaybackOptions: testPlayback}},
			isAnyoneHome:     false,
			initialPlayback:  "morning",
			expectedPlayback: "",
		},
		{
			name: "Someone asleep selects sleep mode",
			modes: map[string]MusicMode{
				"morning": {Participants: testParticipant, PlaybackOptions: testPlayback},
				"sleep":   {Participants: testParticipant, PlaybackOptions: testPlayback},
			},
			isAnyoneHome:     true,
			isMasterAsleep:   true,
			isAnyoneAsleep:   true,
			initialPlayback:  "morning",
			expectedPlayback: "sleep",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)

			musicConfig := &MusicConfig{Music: tt.modes}

			env.StateMgr.SetString("dayPhase", "morning")
			env.StateMgr.SetBool("isMasterAsleep", tt.isMasterAsleep)
			env.StateMgr.SetBool("isGuestAsleep", false)
			env.StateMgr.SetBool("isAnyoneHome", tt.isAnyoneHome)
			env.StateMgr.SetBool("isAnyoneAsleep", tt.isAnyoneAsleep)
			if tt.initialPlayback != "" {
				env.StateMgr.SetString("musicPlaybackType", tt.initialPlayback)
			}
			env.MockHA.SetState("media_player.kitchen", "idle", nil)

			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, musicConfig, env.Logger, false, &plugin.RealTimeProvider{}, nil)
			if err := manager.Start(); err != nil {
				t.Fatalf("Failed to start manager: %v", err)
			}
			defer manager.Stop()

			if err := manager.Reset(); err != nil {
				t.Fatalf("Reset() failed: %v", err)
			}

			if tt.expectedPlayback != "" || tt.initialPlayback != "" {
				musicType, err := env.StateMgr.GetString("musicPlaybackType")
				if err != nil {
					t.Fatalf("Failed to get musicPlaybackType: %v", err)
				}
				if musicType != tt.expectedPlayback {
					t.Errorf("Expected musicPlaybackType=%q, got %q", tt.expectedPlayback, musicType)
				}
			}
		})
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
	env := testutil.NewEnv(t)

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
	env.StateMgr.SetString("dayPhase", "day")
	env.StateMgr.SetBool("isMasterAsleep", false)
	env.StateMgr.SetBool("isGuestAsleep", false)
	env.StateMgr.SetBool("isAnyoneHome", true)
	env.StateMgr.SetBool("isAnyoneAsleep", false)
	env.StateMgr.SetString("musicPlaylistRotation", "{}")

	// Use mutable time provider to control rate limiting
	mockTime := &mutableTimeProvider{current: time.Date(2024, 1, 15, 12, 0, 0, 0, time.Local)} // Monday noon

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, musicConfig, env.Logger, false, mockTime, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip sleeps for faster tests

	err := manager.Start()
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}
	defer manager.Stop()

	// Trigger initial playback by setting musicPlaybackType to day
	err = env.StateMgr.SetString("musicPlaybackType", "day")
	if err != nil {
		t.Fatalf("Failed to set initial musicPlaybackType: %v", err)
	}

	// Wait for async sync
	manager.WaitForSync()

	// Get initial rotation index
	rotationJSON, err := env.StateMgr.GetString("musicPlaylistRotation")
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
	musicType, err := env.StateMgr.GetString("musicPlaybackType")
	if err != nil {
		t.Fatalf("Failed to get musicPlaybackType after reset: %v", err)
	}
	if musicType != "day" {
		t.Errorf("Expected musicPlaybackType to remain 'day', got %q", musicType)
	}

	// Verify playlist rotation incremented (proves playback restarted)
	rotationJSON, err = env.StateMgr.GetString("musicPlaylistRotation")
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

// TestCurrentlyPlayingMusicUri_Lifecycle tests that currentlyPlayingMusicUri
// is set on playback and cleared on stop
func TestCurrentlyPlayingMusicUri_Lifecycle(t *testing.T) {
	t.Parallel()
	testURI := "spotify:playlist:37i9dQZF1DX4dyzvuaRJ0n"

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants:    []Participant{{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}}},
				PlaybackOptions: []PlaybackOption{{URI: testURI, MediaType: "playlist", VolumeMultiplier: 1.0}},
			},
		},
	}

	t.Run("Set on playback", func(t *testing.T) {
		env := testutil.NewEnv(t)
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

		if err := manager.orchestratePlayback("day", "test_trigger"); err != nil {
			t.Fatalf("orchestratePlayback() failed: %v", err)
		}

		currentURI, err := env.StateMgr.GetString("currentlyPlayingMusicUri")
		if err != nil {
			t.Fatalf("Failed to get currentlyPlayingMusicUri: %v", err)
		}
		if currentURI != testURI {
			t.Errorf("Expected currentlyPlayingMusicUri = %q, got %q", testURI, currentURI)
		}
	})

	t.Run("Clear on stop", func(t *testing.T) {
		env := testutil.NewEnv(t)
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

		manager.currentlyPlaying = &CurrentlyPlayingMusic{Type: "day", URI: testURI}
		_ = env.StateMgr.SetString("currentlyPlayingMusicUri", testURI)

		manager.stopPlayback()

		currentURI, err := env.StateMgr.GetString("currentlyPlayingMusicUri")
		if err != nil {
			t.Fatalf("Failed to get currentlyPlayingMusicUri: %v", err)
		}
		if currentURI != "" {
			t.Errorf("Expected currentlyPlayingMusicUri to be empty after stop, got %q", currentURI)
		}
	})
}

// TestCurrentlyPlayingMusicUri_UpdateOnModeChange tests that currentlyPlayingMusicUri
// is updated when music mode changes
func TestCurrentlyPlayingMusicUri_UpdateOnModeChange(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

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

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, timeProvider, nil)

	// Start with day music
	err := manager.orchestratePlayback("day", "test_trigger")
	if err != nil {
		t.Fatalf("orchestratePlayback(day) failed: %v", err)
	}

	// Verify day URI is set
	currentURI, err := env.StateMgr.GetString("currentlyPlayingMusicUri")
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
	currentURI, err = env.StateMgr.GetString("currentlyPlayingMusicUri")
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
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"evening": {},
		},
	}

	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	// NOT read-only so service calls actually go through
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, timeProvider, nil)
	// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set up musicPlaybackType so fade-in doesn't abort early
	if err := env.StateMgr.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Execute fade-in with a low target volume to complete quickly
	manager.fadeInSpeaker(context.Background(), "Kitchen", 3, "evening")

	// Get all service calls
	calls := env.MockHA.GetServiceCalls()

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

			env := testutil.NewEnv(t)

			config := &MusicConfig{
				Music: map[string]MusicMode{
					"evening": {},
				},
			}

			fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
			timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, timeProvider, nil)
			// Skip sleep delays in tests (fade-in uses very slow timing to match Node-RED behavior)
			manager.SetSleepFunc(func(d time.Duration) {})

			// Set up musicPlaybackType so fade-in doesn't abort
			if err := env.StateMgr.SetString("musicPlaybackType", "evening"); err != nil {
				t.Fatalf("Failed to set musicPlaybackType: %v", err)
			}

			manager.fadeInSpeaker(context.Background(), "Kitchen", tc.targetVolume, "evening")

			// Get the final volume_set call
			calls := env.MockHA.GetServiceCalls()
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

// TestFadeInSpeaker_ServiceFailures verifies that fadeInSpeaker aborts safely
// when service calls fail at different stages.
func TestFadeInSpeaker_ServiceFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		failDomain     string
		failService    string
		failErr        error
		checkNoService string // verify this service was NOT called
		checkCallCount *struct {
			service string
			count   int
		}
	}{
		{
			name:           "Volume set failure prevents unmute",
			failDomain:     "media_player",
			failService:    "volume_set",
			failErr:        fmt.Errorf("simulated failure"),
			checkNoService: "volume_mute",
		},
		{
			name:        "Unmute failure prevents fade-in",
			failDomain:  "media_player",
			failService: "volume_mute",
			failErr:     fmt.Errorf("simulated unmute failure"),
			checkCallCount: &struct {
				service string
				count   int
			}{
				service: "volume_set", count: 1, // only initial safety set to 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)
			config := &MusicConfig{Music: map[string]MusicMode{"evening": {}}}
			fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
			timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, timeProvider, nil)
			manager.SetSleepFunc(func(d time.Duration) {})

			if err := env.StateMgr.SetString("musicPlaybackType", "evening"); err != nil {
				t.Fatalf("Failed to set musicPlaybackType: %v", err)
			}

			env.MockHA.SetServiceError(tt.failDomain, tt.failService, tt.failErr)
			manager.fadeInSpeaker(context.Background(), "Kitchen", 10, "evening")

			calls := env.MockHA.GetServiceCalls()
			if tt.checkNoService != "" {
				for _, call := range calls {
					if call.Service == tt.checkNoService {
						t.Errorf("%s should NOT be called when %s fails", tt.checkNoService, tt.failService)
					}
				}
			}
			if tt.checkCallCount != nil {
				count := 0
				for _, call := range calls {
					if call.Service == tt.checkCallCount.service {
						count++
					}
				}
				if count != tt.checkCallCount.count {
					t.Errorf("Expected %d %s calls, got %d", tt.checkCallCount.count, tt.checkCallCount.service, count)
				}
			}
		})
	}
}

// TestFadeInSpeaker_HumanOverrideDetection verifies that fadeInSpeaker detects
// when a human manually lowers the speaker volume and aborts gracefully.
func TestFadeInSpeaker_HumanOverrideDetection(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"evening": {},
		},
	}

	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, timeProvider, nil)

	var volumeStep int
	manager.SetSleepFunc(func(d time.Duration) {
		// Simulate human override: after a few steps, set speaker volume lower than expected
		volumeStep++
		if volumeStep == 5 {
			// Set the speaker volume to 0, simulating human turning it down
			env.MockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{
				"volume_level": 0.0, // Human turned volume down to 0
			})
		}
	})

	// Set up musicPlaybackType so fade-in doesn't abort due to type change
	if err := env.StateMgr.SetString("musicPlaybackType", "evening"); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	// Initially speaker is available with volume 0
	env.MockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{
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
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"evening": {},
		},
	}

	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, timeProvider, nil)

	var volumeStep int
	manager.SetSleepFunc(func(d time.Duration) {
		volumeStep++
		// Simulate normal volume - always matching what automation set
		env.MockHA.SetState("media_player.kitchen", "playing", map[string]interface{}{
			"volume_level": float64(volumeStep) / 100.0,
		})
	})

	if err := env.StateMgr.SetString("musicPlaybackType", "evening"); err != nil {
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
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"evening": {},
		},
	}

	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, timeProvider, nil)

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

	if err := env.StateMgr.SetString("musicPlaybackType", "evening"); err != nil {
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
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"evening": {},
		},
	}

	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, timeProvider, nil)

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
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"evening": {},
		},
	}

	fixedTime := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, timeProvider, nil)

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
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {}, "day": {}, "evening": {}, "winddown": {},
			"sleep": {}, "sex": {}, "wakeup": {},
		},
	}

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Set up mock states with various entity types
	env.MockHA.SetState("media_player.kitchen", "idle", nil)
	env.MockHA.SetState("media_player.bedroom", "playing", nil)
	env.MockHA.SetState("light.living_room", "on", nil)
	env.MockHA.SetState("sensor.temperature", "22", nil)
	env.MockHA.SetState("media_player.soundbar", "off", nil)

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

func TestCallServiceWithRetry(t *testing.T) {
	t.Parallel()
	allModes := map[string]MusicMode{
		"morning": {}, "day": {}, "evening": {}, "winddown": {},
		"sleep": {}, "sex": {}, "wakeup": {},
	}

	tests := []struct {
		name          string
		entityID      string
		setupEntity   bool  // whether to create the media_player entity
		serviceError  error // nil = no error
		expectErr     bool
		errContains   string // substring to check in error message
		expectedCalls int    // expected number of successful calls (0 = don't check)
	}{
		{
			name:          "Success on first attempt",
			entityID:      "media_player.kitchen",
			setupEntity:   true,
			expectErr:     false,
			expectedCalls: 1,
		},
		{
			name:         "Persistent error returns error",
			entityID:     "media_player.kitchen",
			setupEntity:  true,
			serviceError: fmt.Errorf("persistent failure"),
			expectErr:    true,
			errContains:  "persistent failure",
		},
		{
			name:         "Speaker not available returns clear error",
			entityID:     "media_player.nonexistent",
			setupEntity:  false,
			serviceError: fmt.Errorf("entity not found"),
			expectErr:    true,
			errContains:  "not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)
			config := &MusicConfig{Music: allModes}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

			if tt.setupEntity {
				env.MockHA.SetState(tt.entityID, "idle", nil)
			}
			if tt.serviceError != nil {
				env.MockHA.SetServiceError("media_player", "volume_set", tt.serviceError)
			}

			err := manager.callServiceWithRetry("media_player", "volume_set", map[string]interface{}{
				"entity_id":    tt.entityID,
				"volume_level": 0.5,
			})

			if tt.expectErr {
				if err == nil {
					t.Error("Expected error")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing %q, got: %v", tt.errContains, err)
				}
			} else if err != nil {
				t.Errorf("Expected success, got error: %v", err)
			}

			if tt.expectedCalls > 0 {
				count := 0
				for _, call := range env.MockHA.GetServiceCalls() {
					if call.Domain == "media_player" && call.Service == "volume_set" {
						count++
					}
				}
				if count != tt.expectedCalls {
					t.Errorf("Expected %d service calls, got %d", tt.expectedCalls, count)
				}
			}
		})
	}
}

func TestBreakSpeakerGroups(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		participants     []ParticipantWithVolume
		failCount        int
		setupEntities    []string
		minUnjoinCalls   int
		expectedSpeakers []string
	}{
		{
			name: "All speakers unjoined successfully",
			participants: []ParticipantWithVolume{
				{PlayerName: "Kitchen", Volume: 9},
				{PlayerName: "Living Room", Volume: 10},
				{PlayerName: "Bedroom", Volume: 8},
			},
			minUnjoinCalls:   3,
			expectedSpeakers: []string{"media_player.kitchen", "media_player.living_room", "media_player.bedroom"},
		},
		{
			name: "Continues processing after unjoin failure",
			participants: []ParticipantWithVolume{
				{PlayerName: "Kitchen", Volume: 9},
				{PlayerName: "Living Room", Volume: 10},
			},
			failCount:        1,
			setupEntities:    []string{"media_player.kitchen", "media_player.living_room"},
			minUnjoinCalls:   2,
			expectedSpeakers: []string{"media_player.kitchen", "media_player.living_room"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)
			config := &MusicConfig{Music: map[string]MusicMode{}}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
			manager.SetSleepFunc(func(d time.Duration) {})

			for _, entity := range tt.setupEntities {
				env.MockHA.SetState(entity, "idle", nil)
			}
			if tt.failCount > 0 {
				env.MockHA.SetServiceFailCount("media_player", "unjoin", tt.failCount, fmt.Errorf("speaker not reachable"))
			}
			env.MockHA.ClearServiceCalls()

			manager.breakSpeakerGroups(tt.participants)

			calls := env.MockHA.GetServiceCalls()
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

			if unjoinCalls < tt.minUnjoinCalls {
				t.Errorf("Expected at least %d unjoin calls, got %d", tt.minUnjoinCalls, unjoinCalls)
			}
			for _, expected := range tt.expectedSpeakers {
				if !unjoinedSpeakers[expected] {
					t.Errorf("Expected unjoin call for %s", expected)
				}
			}
		})
	}
}

func TestExecutePlayback_BreakThenBuildSequence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		participants []ParticipantWithVolume
		expectJoin   bool
		unjoinCount  int
	}{
		{
			name: "Multi-speaker: break before build",
			participants: []ParticipantWithVolume{
				{PlayerName: "Kitchen", BaseVolume: 9, Volume: 9, LeaveMutedIf: []MuteCondition{}},
				{PlayerName: "Living Room", BaseVolume: 10, Volume: 10, LeaveMutedIf: []MuteCondition{}},
			},
			expectJoin:  true,
			unjoinCount: 2,
		},
		{
			name: "Single speaker: break still called, no join needed",
			participants: []ParticipantWithVolume{
				{PlayerName: "Kitchen", BaseVolume: 9, Volume: 9, LeaveMutedIf: []MuteCondition{}},
			},
			expectJoin:  false,
			unjoinCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)
			_ = env.StateMgr.SetBool("isTVPlaying", false)
			_ = env.StateMgr.SetString("musicPlaybackType", "day")

			config := &MusicConfig{Music: map[string]MusicMode{}}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
			manager.SetSleepFunc(func(d time.Duration) {})
			env.MockHA.SetState("media_player.kitchen", "playing", nil)
			env.MockHA.ClearServiceCalls()

			option := PlaybackOption{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0}
			_, _, err := manager.executePlayback("day", option, tt.participants, "Kitchen")
			if err != nil {
				t.Fatalf("executePlayback() failed: %v", err)
			}

			// Poll for async join call if expected
			var calls []ha.ServiceCall
			if tt.expectJoin {
				for attempt := 0; attempt < 100; attempt++ {
					calls = env.MockHA.GetServiceCalls()
					for _, call := range calls {
						if call.Domain == "media_player" && call.Service == "join" {
							goto donePolling
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		donePolling:
			if calls == nil {
				calls = env.MockHA.GetServiceCalls()
			}

			unjoinCalls, joinCalls := 0, 0
			firstUnjoinIdx, firstJoinIdx := -1, -1
			for i, call := range calls {
				if call.Domain == "media_player" && call.Service == "unjoin" {
					unjoinCalls++
					if firstUnjoinIdx == -1 {
						firstUnjoinIdx = i
					}
				}
				if call.Domain == "media_player" && call.Service == "join" {
					joinCalls++
					if firstJoinIdx == -1 {
						firstJoinIdx = i
					}
				}
			}

			if unjoinCalls < tt.unjoinCount {
				t.Errorf("Expected at least %d unjoin calls, got %d", tt.unjoinCount, unjoinCalls)
			}
			if tt.expectJoin {
				if firstJoinIdx == -1 {
					t.Error("Expected join call")
				}
				if firstUnjoinIdx >= firstJoinIdx {
					t.Errorf("SEQUENCE ERROR: unjoin (idx %d) must come BEFORE join (idx %d)", firstUnjoinIdx, firstJoinIdx)
				}
			} else {
				if joinCalls != 0 {
					t.Errorf("Expected 0 join calls for single speaker, got %d", joinCalls)
				}
			}
		})
	}
}

// TestStartValidatesSpeakers verifies that Start() refreshes and validates
// configured speakers on startup.
func TestStartValidatesSpeakers(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

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
	if err := env.StateMgr.SetString("dayPhase", "day"); err != nil {
		t.Fatalf("Failed to set dayPhase: %v", err)
	}
	if err := env.StateMgr.SetBool("isAnyoneHome", true); err != nil {
		t.Fatalf("Failed to set isAnyoneHome: %v", err)
	}
	if err := env.StateMgr.SetBool("isAnyoneAsleep", false); err != nil {
		t.Fatalf("Failed to set isAnyoneAsleep: %v", err)
	}
	if err := env.StateMgr.SetString("musicPlaybackType", ""); err != nil {
		t.Fatalf("Failed to set musicPlaybackType: %v", err)
	}

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Set up only one of the configured speakers
	env.MockHA.SetState("media_player.kitchen", "idle", nil)
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

			env := testutil.NewEnv(t)

			// Set up the rotation value in HA
			_ = env.StateMgr.SetString("musicPlaylistRotation", tt.haValue)

			// Create config with music types
			config := &MusicConfig{
				Music: map[string]MusicMode{
					"morning": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}},
					"day":     {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}, {URI: "test4"}, {URI: "test5"}, {URI: "test6"}}},
					"evening": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}}},
				},
			}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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

// TestLoadPlaylistRotationEdgeCases tests bounds checking and unconfigured types
func TestLoadPlaylistRotationEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		haValue        string
		config         map[string]MusicMode
		expectedValues map[string]int
	}{
		{
			name:    "Indices exceeding playlist count are wrapped",
			haValue: `{"morning":5,"day":10}`,
			config: map[string]MusicMode{
				"morning": {PlaybackOptions: []PlaybackOption{{URI: "t1"}, {URI: "t2"}, {URI: "t3"}}},
				"day":     {PlaybackOptions: []PlaybackOption{{URI: "t1"}, {URI: "t2"}, {URI: "t3"}}},
			},
			expectedValues: map[string]int{"morning": 2, "day": 1}, // 5%3=2, 10%3=1
		},
		{
			name:    "Unconfigured types are preserved",
			haValue: `{"oldtype":3,"morning":1}`,
			config: map[string]MusicMode{
				"morning": {PlaybackOptions: []PlaybackOption{{URI: "t1"}, {URI: "t2"}, {URI: "t3"}}},
			},
			expectedValues: map[string]int{"oldtype": 3, "morning": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)
			_ = env.StateMgr.SetString("musicPlaylistRotation", tt.haValue)

			config := &MusicConfig{Music: tt.config}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
			manager.loadPlaylistRotationFromHA()

			for key, expected := range tt.expectedValues {
				if manager.playlistNumbers[key] != expected {
					t.Errorf("Expected playlistNumbers[%q]=%d, got %d", key, expected, manager.playlistNumbers[key])
				}
			}
		})
	}
}

// TestSyncPlaylistRotationToHA tests that playlist rotation is synced to HA after changes
func TestPlaylistRotationSync(t *testing.T) {
	t.Parallel()
	t.Run("Syncs rotation to HA", func(t *testing.T) {
		env := testutil.NewEnv(t)
		_ = env.StateMgr.SetString("musicPlaylistRotation", "{}")

		config := &MusicConfig{
			Music: map[string]MusicMode{
				"day": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}},
			},
		}
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

		index := manager.getNextPlaylistIndex("day", 3)
		if index != 0 {
			t.Errorf("Expected first index to be 0, got %d", index)
		}
		manager.WaitForSync()

		rotationJSON, err := env.StateMgr.GetString("musicPlaylistRotation")
		if err != nil {
			t.Fatalf("Failed to get rotation: %v", err)
		}
		var rotation map[string]int
		if err := json.Unmarshal([]byte(rotationJSON), &rotation); err != nil {
			t.Fatalf("Failed to parse rotation JSON: %v", err)
		}
		if rotation["day"] != 1 {
			t.Errorf("Expected synced rotation[day]=1, got %d", rotation["day"])
		}

		index2 := manager.getNextPlaylistIndex("day", 3)
		if index2 != 1 {
			t.Errorf("Expected second index to be 1, got %d", index2)
		}
		manager.WaitForSync()

		rotationJSON, _ = env.StateMgr.GetString("musicPlaylistRotation")
		_ = json.Unmarshal([]byte(rotationJSON), &rotation)
		if rotation["day"] != 2 {
			t.Errorf("Expected synced rotation[day]=2, got %d", rotation["day"])
		}
	})

	t.Run("Skips sync in read-only mode", func(t *testing.T) {
		env := testutil.NewEnv(t)
		readOnlyStateMgr := state.NewManager(env.MockHA, env.Logger, true)
		_ = readOnlyStateMgr.SetString("musicPlaylistRotation", "{}")

		config := &MusicConfig{
			Music: map[string]MusicMode{
				"day": {PlaybackOptions: []PlaybackOption{{URI: "test1"}, {URI: "test2"}, {URI: "test3"}}},
			},
		}
		manager := NewManager(context.Background(), env.MockHA, readOnlyStateMgr, config, env.Logger, true, nil, nil)

		index := manager.getNextPlaylistIndex("day", 3)
		if index != 0 {
			t.Errorf("Expected first index to be 0, got %d", index)
		}
		manager.WaitForSync()

		rotationJSON, _ := readOnlyStateMgr.GetString("musicPlaylistRotation")
		if rotationJSON != "{}" {
			t.Errorf("Expected rotation to remain '{}' in read-only mode, got %s", rotationJSON)
		}

		index2 := manager.getNextPlaylistIndex("day", 3)
		if index2 != 1 {
			t.Errorf("Expected second index to be 1, got %d", index2)
		}
	})
}

// TestPlaybackVerification tests that playback verification detects and handles
// various speaker states correctly.
func TestPlaybackVerification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		speakerState   string   // Single state (used if stateSequence is nil)
		stateSequence  []string // State sequence for recovery scenarios
		expectAttempts int
		expectError    bool
		expectNudge    bool // Whether media_play nudge should be called
	}{
		{
			name:           "Speaker playing on first try",
			speakerState:   "playing",
			expectAttempts: 1,
		},
		{
			name:           "Speaker paused - requires retry",
			speakerState:   "paused",
			expectAttempts: 3,
			expectError:    true,
		},
		{
			name:           "Speaker idle - requires retry",
			speakerState:   "idle",
			expectAttempts: 3,
			expectError:    true,
		},
		{
			name:           "Recovery after nudge",
			stateSequence:  []string{"idle", "playing"},
			expectAttempts: 1,
			expectNudge:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)
			config := &MusicConfig{Music: map[string]MusicMode{}}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
			manager.SetSleepFunc(func(d time.Duration) {})

			if tt.stateSequence != nil {
				env.MockHA.SetStateSequence("media_player.kitchen", tt.stateSequence)
			} else {
				env.MockHA.SetState("media_player.kitchen", tt.speakerState, nil)
			}

			option := PlaybackOption{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0}
			attempts, err := manager.startPlaybackWithVerification("media_player.kitchen", "Kitchen", option)

			if tt.expectError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if tt.expectAttempts > 0 && attempts != tt.expectAttempts {
				t.Errorf("Expected %d attempts, got %d", tt.expectAttempts, attempts)
			}
			if tt.expectNudge {
				var hasNudge bool
				for _, call := range env.MockHA.GetServiceCalls() {
					if call.Domain == "media_player" && call.Service == "media_play" {
						hasNudge = true
					}
				}
				if !hasNudge {
					t.Error("Expected media_play nudge service call")
				}
			}
		})
	}
}

// TestStartPlaybackWithVerification_TidalDispatch tests that tidal media type routes through SoCo-CLI
func TestStartPlaybackWithVerification_TidalDispatch(t *testing.T) {
	t.Parallel()

	t.Run("tidal dispatches to SoCo-CLI client", func(t *testing.T) {
		var actions []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/Kitchen/play_from_queue" {
				actions = append(actions, "play_from_queue")
			} else {
				actions = append(actions, "sharelink")
			}
			resp := SoCoResponse{Result: "ok", ExitCode: 0}
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		env := testutil.NewEnv(t)
		config := &MusicConfig{Music: map[string]MusicMode{}}
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
		manager.SetSleepFunc(func(d time.Duration) {})
		manager.SetSoCoClient(NewSoCoClient(server.URL, env.Logger, false))

		// Set speaker to "playing" so verification passes on first attempt
		env.MockHA.SetState("media_player.kitchen", "playing", nil)

		option := PlaybackOption{
			URI:              "https://tidal.com/browse/playlist/abc123",
			MediaType:        "tidal",
			VolumeMultiplier: 1.0,
		}
		attempts, err := manager.startPlaybackWithVerification("media_player.kitchen", "Kitchen", option)
		require.NoError(t, err)
		assert.Equal(t, 1, attempts)
		assert.Equal(t, []string{"sharelink", "play_from_queue"}, actions)

		// Verify no HA play_media calls were made (Tidal goes through SoCo-CLI)
		for _, call := range env.MockHA.GetServiceCalls() {
			if call.Domain == "media_player" && call.Service == "play_media" {
				t.Error("Expected no HA play_media calls for tidal media type")
			}
		}
	})

	t.Run("tidal returns error when SoCo-CLI client not configured", func(t *testing.T) {
		env := testutil.NewEnv(t)
		config := &MusicConfig{Music: map[string]MusicMode{}}
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
		manager.SetSleepFunc(func(d time.Duration) {})
		// No SoCoClient set

		option := PlaybackOption{
			URI:              "https://tidal.com/browse/playlist/abc123",
			MediaType:        "tidal",
			VolumeMultiplier: 1.0,
		}
		_, err := manager.startPlaybackWithVerification("media_player.kitchen", "Kitchen", option)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SoCo-CLI client is not configured")
	})
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
			env := testutil.NewEnv(t)
			config := &MusicConfig{Music: map[string]MusicMode{}}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
			env.MockHA.SetState("media_player.kitchen", tt.speakerState, nil)

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

// ============================================================================
// Phase 1: Zone Assignment Policy Tests
// ============================================================================

// TestShouldIncludeInZone_MultipleConditions tests zone inclusion with various condition combinations
func TestShouldIncludeInZone_MultipleConditions(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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
			if err := env.StateMgr.SetBool("isMasterAsleep", tt.isMasterAsleep); err != nil {
				t.Fatalf("Failed to set isMasterAsleep: %v", err)
			}
			if err := env.StateMgr.SetBool("isGuestAsleep", tt.isGuestAsleep); err != nil {
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

// TestOrchestratePlayback_ExcludeIf tests that orchestratePlayback filters out excluded speakers
func TestOrchestratePlayback_ExcludeIf(t *testing.T) {
	t.Parallel()
	emptyMode := MusicMode{Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}}
	playbackOpt := []PlaybackOption{{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0}}

	tests := []struct {
		name               string
		participants       []Participant
		expectErr          bool
		expectErrContains  string
		expectedCount      int
		expectedLeadPlayer string
		expectedSpeakers   []string
		excludedSpeakers   []string
	}{
		{
			name: "Bedroom excluded when master asleep",
			participants: []Participant{
				{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{}},
				{PlayerName: "Bedroom", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{
					{Variable: "isMasterAsleep", Value: true},
				}},
				{PlayerName: "Living Room", BaseVolume: 10, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{}},
			},
			expectedCount:      2,
			expectedLeadPlayer: "Kitchen",
			expectedSpeakers:   []string{"Kitchen", "Living Room"},
			excludedSpeakers:   []string{"Bedroom"},
		},
		{
			name: "All speakers excluded returns error",
			participants: []Participant{
				{PlayerName: "Bedroom", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{
					{Variable: "isMasterAsleep", Value: true},
				}},
			},
			expectErr:         true,
			expectErrContains: "exclude_if",
		},
		{
			name: "Lead player selected from non-excluded participants",
			participants: []Participant{
				{PlayerName: "Bedroom", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{
					{Variable: "isMasterAsleep", Value: true},
				}},
				{PlayerName: "Kitchen", BaseVolume: 9, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{}},
				{PlayerName: "Living Room", BaseVolume: 10, LeaveMutedIf: []MuteCondition{}, ExcludeIf: []MuteCondition{}},
			},
			expectedCount:      2,
			expectedLeadPlayer: "Kitchen",
			expectedSpeakers:   []string{"Kitchen", "Living Room"},
			excludedSpeakers:   []string{"Bedroom"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewEnv(t)

			config := &MusicConfig{
				Music: map[string]MusicMode{
					"morning": {Participants: tt.participants, PlaybackOptions: playbackOpt},
					"day":     emptyMode, "evening": emptyMode, "winddown": emptyMode,
					"sleep": emptyMode, "sex": emptyMode, "wakeup": emptyMode,
				},
			}
			manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

			_ = env.StateMgr.SetBool("isMasterAsleep", true)
			_ = env.StateMgr.SetString("currentlyPlayingMusicUri", "")

			err := manager.orchestratePlayback("morning", "test")
			if tt.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				} else if tt.expectErrContains != "" && !strings.Contains(err.Error(), tt.expectErrContains) {
					t.Errorf("Error should mention %q, got: %v", tt.expectErrContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("orchestratePlayback failed: %v", err)
			}

			manager.mu.RLock()
			defer manager.mu.RUnlock()
			if manager.currentlyPlaying == nil {
				t.Fatal("currentlyPlaying should not be nil")
			}
			if len(manager.currentlyPlaying.Participants) != tt.expectedCount {
				t.Errorf("Expected %d participants, got %d", tt.expectedCount, len(manager.currentlyPlaying.Participants))
			}
			speakerNames := make(map[string]bool)
			for _, p := range manager.currentlyPlaying.Participants {
				speakerNames[p.PlayerName] = true
			}
			for _, s := range tt.expectedSpeakers {
				if !speakerNames[s] {
					t.Errorf("%s should be in participants", s)
				}
			}
			for _, s := range tt.excludedSpeakers {
				if speakerNames[s] {
					t.Errorf("%s should NOT be in participants", s)
				}
			}
			if tt.expectedLeadPlayer != "" && manager.currentlyPlaying.LeadPlayer != tt.expectedLeadPlayer {
				t.Errorf("Expected lead player %s, got %s", tt.expectedLeadPlayer, manager.currentlyPlaying.LeadPlayer)
			}
		})
	}
}

// TestCollectMuteConditionVariables_IncludesExcludeIf tests that exclude_if variables are collected
func TestCollectMuteConditionVariables_IncludesExcludeIf(t *testing.T) {
	t.Parallel()
	emptyMode := MusicMode{Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}}
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9,
						LeaveMutedIf: []MuteCondition{{Variable: "isTVPlaying", Value: true}},
						ExcludeIf:    []MuteCondition{{Variable: "isMasterAsleep", Value: true}}},
					{PlayerName: "Bedroom", BaseVolume: 9, LeaveMutedIf: []MuteCondition{},
						ExcludeIf: []MuteCondition{{Variable: "isGuestAsleep", Value: true}}},
				},
			},
			"day": emptyMode, "evening": emptyMode, "winddown": emptyMode,
			"sleep": emptyMode, "sex": emptyMode, "wakeup": emptyMode,
		},
	}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	varMap := make(map[string]bool)
	for _, v := range manager.collectMuteConditionVariables() {
		varMap[v] = true
	}
	for _, expected := range []string{"isTVPlaying", "isMasterAsleep", "isGuestAsleep"} {
		if !varMap[expected] {
			t.Errorf("Expected %s to be collected", expected)
		}
	}
}

// TestExcludeIf_ParticipantWithVolumePreservesExcludeIf tests that ExcludeIf is copied to ParticipantWithVolume
func TestExcludeIf_ParticipantWithVolumePreservesExcludeIf(t *testing.T) {
	t.Parallel()
	emptyMode := MusicMode{Participants: []Participant{}, PlaybackOptions: []PlaybackOption{}}
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"morning": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 9,
						LeaveMutedIf: []MuteCondition{{Variable: "isTVPlaying", Value: true}},
						ExcludeIf:    []MuteCondition{{Variable: "isMasterAsleep", Value: true}}},
				},
				PlaybackOptions: []PlaybackOption{{URI: "spotify:playlist:test", MediaType: "playlist", VolumeMultiplier: 1.0}},
			},
			"day": emptyMode, "evening": emptyMode, "winddown": emptyMode,
			"sleep": emptyMode, "sex": emptyMode, "wakeup": emptyMode,
		},
	}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, true, nil, nil)

	_ = env.StateMgr.SetBool("isMasterAsleep", false)
	_ = env.StateMgr.SetString("currentlyPlayingMusicUri", "")

	if err := manager.orchestratePlayback("morning", "test"); err != nil {
		t.Fatalf("orchestratePlayback failed: %v", err)
	}
	if len(manager.currentlyPlaying.Participants) != 1 {
		t.Fatalf("Expected 1 participant, got %d", len(manager.currentlyPlaying.Participants))
	}
	participant := manager.currentlyPlaying.Participants[0]
	if len(participant.ExcludeIf) != 1 || participant.ExcludeIf[0].Variable != "isMasterAsleep" {
		t.Errorf("Expected ExcludeIf with isMasterAsleep, got %+v", participant.ExcludeIf)
	}
}

// TestBuildSpeakerGroupAsync_StaggeredDelays verifies that speakers launch with staggered delays
func TestBuildSpeakerGroupAsync_StaggeredDelays(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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
	calls := env.MockHA.GetServiceCalls()
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
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Track which speakers had their join called
	var joinMu sync.Mutex
	var joinedSpeakers []string

	// Capture the join calls to track order
	originalCallService := env.MockHA.GetServiceCalls
	env.MockHA.ClearServiceCalls()

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

// TestJoinSpeakerWithRetry_Success tests successful speaker join on first attempt
func TestJoinSpeakerWithRetry_Success(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	manager.SetSleepFunc(func(d time.Duration) {})

	participant := ParticipantWithVolume{PlayerName: "Living Room", Volume: 10, LeaveMutedIf: []MuteCondition{}}
	env.MockHA.ClearServiceCalls()
	manager.joinSpeakerWithRetry(participant, "media_player.kitchen", "day")

	joinCalls := 0
	for _, call := range env.MockHA.GetServiceCalls() {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls++
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
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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
	env.MockHA.SetServiceFailCount("media_player", "join", 2, fmt.Errorf("service call failed: timeout"))

	participant := ParticipantWithVolume{
		PlayerName:   "Living Room",
		Volume:       10,
		LeaveMutedIf: []MuteCondition{},
	}

	env.MockHA.ClearServiceCalls()
	manager.joinSpeakerWithRetry(participant, "media_player.kitchen", "day")

	calls := env.MockHA.GetServiceCalls()

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
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	// Track retry delays
	var sleepMu sync.Mutex
	var sleepDurations []time.Duration

	manager.SetSleepFunc(func(d time.Duration) {
		sleepMu.Lock()
		sleepDurations = append(sleepDurations, d)
		sleepMu.Unlock()
	})

	// Fail first 5 attempts to test backoff progression and capping
	env.MockHA.SetServiceFailCount("media_player", "join", 5, fmt.Errorf("service call failed: timeout"))

	participant := ParticipantWithVolume{
		PlayerName:   "Living Room",
		Volume:       10,
		LeaveMutedIf: []MuteCondition{},
	}

	env.MockHA.ClearServiceCalls()
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
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

	var sleepCallCount int
	manager.SetSleepFunc(func(d time.Duration) { sleepCallCount++ })

	env.MockHA.SetServiceFailCount("media_player", "join", 100, fmt.Errorf("service call failed: entity not found"))

	participant := ParticipantWithVolume{PlayerName: "Nonexistent Speaker", Volume: 10, LeaveMutedIf: []MuteCondition{}}
	env.MockHA.ClearServiceCalls()
	manager.joinSpeakerWithRetry(participant, "media_player.kitchen", "day")

	if sleepCallCount != 0 {
		t.Errorf("Expected no retry delays for permanent error, got %d", sleepCallCount)
	}
}

// TestBuildSpeakerGroupAsync_WaitGroupCompletion verifies WaitGroup waits for all goroutines
func TestBuildSpeakerGroupAsync_WaitGroupCompletion(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	config := &MusicConfig{Music: map[string]MusicMode{}}
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)

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
	env.MockHA.ClearServiceCalls()

	participants := []ParticipantWithVolume{
		{PlayerName: "Kitchen", Volume: 9},      // Lead
		{PlayerName: "Living Room", Volume: 10}, // Follower 1
		{PlayerName: "Bedroom", Volume: 8},      // Follower 2
		{PlayerName: "Office", Volume: 7},       // Follower 3
	}

	// buildSpeakerGroupAsync should block until all goroutines complete
	manager.buildSpeakerGroupAsync(participants, "media_player.kitchen", "day")

	// After buildSpeakerGroupAsync returns, all joins should be complete
	calls := env.MockHA.GetServiceCalls()
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

// TestAddSpeakersToZone_JoinParameterOrder verifies that addSpeakersToZone sends the
// media_player.join service call with correct parameter order:
//   - entity_id = lead speaker (group coordinator)
//   - group_members = [follower] (speaker joining the group)
//
// Regression test for issue #739: the parameters were previously reversed, causing
// the follower to be set as entity_id and the lead as group_member. This made the
// Sonos join fail or disrupt the existing group when speakers were dynamically added.
func TestAddSpeakersToZone_JoinParameterOrder(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"winddown": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 10},
					{PlayerName: "Primary Bathroom", BaseVolume: 6},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set state so shouldIncludeInZone passes (isMasterAsleep=false)
	_ = env.StateMgr.SetBool("isMasterAsleep", false)

	// Create an active zone with Kitchen as lead
	zone := &Zone{
		Name:        "winddown",
		MusicType:   "winddown",
		LeadSpeaker: "Kitchen",
		Participants: []ParticipantWithVolume{
			{PlayerName: "Kitchen", BaseVolume: 10, Volume: 10},
		},
	}

	env.MockHA.ClearServiceCalls()

	// Dynamically add Primary Bathroom to the active zone
	manager.addSpeakersToZone(zone, []string{"Primary Bathroom"}, "test")

	// Verify the join call has correct parameter order
	calls := env.MockHA.GetServiceCalls()
	var joinCalls []ha.ServiceCall
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCalls = append(joinCalls, call)
		}
	}
	require.NotEmpty(t, joinCalls, "Expected at least one media_player.join call")

	joinCall := joinCalls[0]
	// entity_id must be the LEAD speaker (group coordinator)
	entityID, ok := joinCall.Data["entity_id"].(string)
	require.True(t, ok, "entity_id should be a string")
	assert.Equal(t, "media_player.kitchen", entityID,
		"entity_id must be the lead speaker (group coordinator), not the follower")

	// group_members must contain the FOLLOWER (speaker joining the group)
	groupMembers, ok := joinCall.Data["group_members"].([]string)
	require.True(t, ok, "group_members should be a []string")
	assert.Contains(t, groupMembers, "media_player.primary_bathroom",
		"group_members must contain the follower speaker, not the lead")
}

// TestAddSpeakersToZone_ExcludeIfRespected verifies that addSpeakersToZone
// checks exclude_if conditions before joining a speaker to the Sonos group.
// This is defense-in-depth: assignSpeakersToZones already filters, but
// addSpeakersToZone should also validate to prevent bugs in callers.
func TestAddSpeakersToZone_ExcludeIfRespected(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"winddown": {
				Participants: []Participant{
					{PlayerName: "Kitchen", BaseVolume: 10},
					{
						PlayerName: "Primary Bathroom",
						BaseVolume: 6,
						ExcludeIf: []MuteCondition{
							{Variable: "isMasterAsleep", Value: true},
						},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{URI: "test:uri", MediaType: "playlist", VolumeMultiplier: 1.0},
				},
			},
		},
	}

	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, config, env.Logger, false, nil, nil)
	manager.SetSleepFunc(func(d time.Duration) {})

	// Set isMasterAsleep=true so Primary Bathroom should be excluded
	_ = env.StateMgr.SetBool("isMasterAsleep", true)

	zone := &Zone{
		Name:        "winddown",
		MusicType:   "winddown",
		LeadSpeaker: "Kitchen",
		Participants: []ParticipantWithVolume{
			{PlayerName: "Kitchen", BaseVolume: 10, Volume: 10},
		},
	}

	env.MockHA.ClearServiceCalls()

	// Try to add Primary Bathroom — should be excluded by exclude_if
	manager.addSpeakersToZone(zone, []string{"Primary Bathroom"}, "test")

	// No join call should be made for the excluded speaker
	calls := env.MockHA.GetServiceCalls()
	joinCallCount := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			joinCallCount++
		}
	}
	assert.Equal(t, 0, joinCallCount,
		"No join call should be made when speaker is excluded by exclude_if condition")
}
