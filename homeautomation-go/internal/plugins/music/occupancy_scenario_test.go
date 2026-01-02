package music

// =============================================================================
// OCCUPANCY-BASED SPEAKER MUTE SCENARIO TESTS
// =============================================================================
//
// PURPOSE:
// These tests validate that the Music Manager correctly handles speaker mute
// conditions based on room occupancy. Specifically, the Office speaker should
// be muted when Nick's office is unoccupied, and unmuted when he enters.
//
// CURRENT STATUS: PASSING
// The Music Manager now subscribes to mute condition variables and responds
// to state changes by unmuting/muting speakers during active playback.
//
// NODE-RED REFERENCE:
// - Flow: Music (90f5fe8cb80ae6a7)
// - URL: https://node-red.featherback-mermaid.ts.net/#flow/90f5fe8cb80ae6a7
// - The Node-RED flow subscribes to occupancy changes and re-evaluates
//   speaker participation during active playback.
//
// CONFIGURATION REFERENCE:
// - File: configs/music_config.yaml
// - Relevant speaker config (appears in multiple music modes):
//   - player_name: "Office"
//     base_volume: 6-8 (varies by mode)
//     leave_muted_if:
//       - variable: isNickOfficeOccupied
//         value: false
//
// HOW leave_muted_if WORKS:
// - If ALL conditions in leave_muted_if match current state, speaker stays MUTED
// - If ANY condition does NOT match, speaker is UNMUTED
// - Empty leave_muted_if means speaker is always unmuted (e.g., Kitchen)
//
// EXAMPLE:
// - Office speaker has: leave_muted_if: isNickOfficeOccupied = false
// - When isNickOfficeOccupied = false: condition MATCHES → speaker MUTED
// - When isNickOfficeOccupied = true: condition does NOT match → speaker UNMUTED
//
// IMPLEMENTATION:
// The Music Manager (internal/plugins/music/manager.go) implements:
//
// 1. collectMuteConditionVariables(): Collects all variables from leave_muted_if
//    conditions across all music modes
//
// 2. Subscribe to state changes for these variables in Start()
//    - Calls stateManager.Subscribe() for each mute condition variable
//    - The subscription callback triggers re-evaluation of speaker states
//
// 3. handleMuteConditionChange(): When a subscribed variable changes during playback
//    - Checks if music is currently playing
//    - Re-evaluates shouldUnmuteSpeaker() for affected participants
//    - Calls fadeInSpeaker() or muteSpeaker() as appropriate
//
// =============================================================================

import (
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// createOccupancyMusicConfig creates a configuration matching the actual music_config.yaml
// where the Office speaker has leave_muted_if: isNickOfficeOccupied = false.
//
// This mirrors the real configuration where:
// - Kitchen speaker has NO mute conditions (always plays)
// - Office speaker is MUTED when office is unoccupied (isNickOfficeOccupied = false)
// - Office speaker is UNMUTED when office is occupied (isNickOfficeOccupied = true)
func createOccupancyMusicConfig() *MusicConfig {
	return &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{
						PlayerName:   "Kitchen",
						BaseVolume:   9,
						LeaveMutedIf: []MuteCondition{}, // No conditions = always unmuted
					},
					{
						PlayerName: "Office",
						BaseVolume: 6,
						LeaveMutedIf: []MuteCondition{
							{
								Variable: "isNickOfficeOccupied",
								Value:    false, // Mute when office is NOT occupied
							},
						},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{
						URI:              "spotify:playlist:test123",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
			"morning": {
				Participants: []Participant{
					{
						PlayerName:   "Kitchen",
						BaseVolume:   9,
						LeaveMutedIf: []MuteCondition{},
					},
					{
						PlayerName: "Office",
						BaseVolume: 8, // Different volume in morning mode
						LeaveMutedIf: []MuteCondition{
							{
								Variable: "isNickOfficeOccupied",
								Value:    false,
							},
						},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{
						URI:              "spotify:playlist:morning123",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
		},
	}
}

// =============================================================================
// TEST: shouldUnmuteSpeaker() Logic - Office Occupied
// =============================================================================
//
// WHAT THIS TEST VALIDATES:
// The core mute condition evaluation logic in shouldUnmuteSpeaker() works correctly.
// This is a UNIT TEST of the decision logic.
//
// SCENARIO:
// 1. Start with isNickOfficeOccupied = false → Office should be MUTED
// 2. Change to isNickOfficeOccupied = true → Office should be UNMUTED
func TestScenario_OfficeSpeaker_UnmutedWhenOccupied(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyMusicConfig()

	// Use fixed time provider for deterministic testing
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC) // Monday 10am
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initialize required state variables
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("musicPlaybackType", "")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false) // Office NOT occupied initially
	_ = stateManager.SetBool("isTVPlaying", false)

	// Start manager to initialize subscriptions
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Allow initial processing
	time.Sleep(100 * time.Millisecond)

	// Clear initial service calls
	mockClient.ClearServiceCalls()

	// Create participant with mute conditions from config
	participant := ParticipantWithVolume{
		PlayerName:   "Office",
		BaseVolume:   6,
		Volume:       6,
		LeaveMutedIf: config.Music["day"].Participants[1].LeaveMutedIf,
	}

	// ==========================================================
	// VERIFICATION 1: Office should be MUTED when unoccupied
	// ==========================================================
	// Mute condition: isNickOfficeOccupied = false
	// Current state: isNickOfficeOccupied = false
	// Condition MATCHES → speaker stays MUTED
	shouldUnmute := manager.shouldUnmuteSpeaker(participant)
	assert.False(t, shouldUnmute,
		"Office speaker should stay MUTED when isNickOfficeOccupied = false. "+
			"The mute condition (value: false) matches current state (false).")

	// ==========================================================
	// ACTION: Nick enters the office
	// ==========================================================
	_ = stateManager.SetBool("isNickOfficeOccupied", true)

	// ==========================================================
	// VERIFICATION 2: Office should be UNMUTED when occupied
	// ==========================================================
	// Mute condition: isNickOfficeOccupied = false
	// Current state: isNickOfficeOccupied = true
	// Condition does NOT match → speaker is UNMUTED
	shouldUnmute = manager.shouldUnmuteSpeaker(participant)
	assert.True(t, shouldUnmute,
		"Office speaker should be UNMUTED when isNickOfficeOccupied = true. "+
			"The mute condition (value: false) does NOT match current state (true).")
}

// =============================================================================
// TEST: shouldUnmuteSpeaker() Logic - Office Unoccupied
// =============================================================================
//
// WHAT THIS TEST VALIDATES:
// The inverse of the above test - confirms speaker is correctly muted when
// Nick leaves his office.
//
// SCENARIO:
// 1. Start with isNickOfficeOccupied = true → Office should be UNMUTED
// 2. Change to isNickOfficeOccupied = false → Office should be MUTED
func TestScenario_OfficeSpeaker_MutedWhenUnoccupied(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyMusicConfig()

	// Use fixed time provider
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initialize - office IS occupied
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", true) // Office IS occupied initially

	// Create participant with mute conditions
	participant := ParticipantWithVolume{
		PlayerName:   "Office",
		BaseVolume:   6,
		Volume:       6,
		LeaveMutedIf: config.Music["day"].Participants[1].LeaveMutedIf,
	}

	// ==========================================================
	// VERIFICATION 1: Office should be UNMUTED when occupied
	// ==========================================================
	shouldUnmute := manager.shouldUnmuteSpeaker(participant)
	assert.True(t, shouldUnmute,
		"Office speaker should be UNMUTED when isNickOfficeOccupied = true")

	// ==========================================================
	// ACTION: Nick leaves the office
	// ==========================================================
	_ = stateManager.SetBool("isNickOfficeOccupied", false)

	// ==========================================================
	// VERIFICATION 2: Office should be MUTED when unoccupied
	// ==========================================================
	shouldUnmute = manager.shouldUnmuteSpeaker(participant)
	assert.False(t, shouldUnmute,
		"Office speaker should be MUTED when isNickOfficeOccupied = false")
}

// =============================================================================
// TEST: Real-Time Speaker Unmute During Active Playback
// =============================================================================
//
// WHAT THIS TEST VALIDATES:
// When music is actively playing and Nick enters his office, the Music Manager
// should AUTOMATICALLY unmute the Office speaker by setting its volume.
//
// THIS IS THE KEY INTEGRATION TEST that validates the subscription mechanism.
//
// SCENARIO:
// 1. Music starts playing in "day" mode
// 2. Office speaker is initially MUTED (isNickOfficeOccupied = false)
// 3. Nick enters his office (isNickOfficeOccupied = true)
// 4. Music Manager should detect the change and unmute the Office speaker
//
// EXPECTED BEHAVIOR:
// - Service call: media_player.volume_mute with is_volume_muted=false
// - Entity: media_player.office
// - No volume_set needed (volume was set during initial playback)
func TestScenario_OfficeSpeaker_UnmuteOnOccupancyChangeDuringPlayback(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyMusicConfig()

	// Use fixed time provider
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initialize required state variables - music will start playing
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("musicPlaybackType", "")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false) // Office NOT occupied
	_ = stateManager.SetBool("isTVPlaying", false)

	// Start manager - music should start playing (Kitchen speaker only)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Allow some processing time for initial music start
	time.Sleep(50 * time.Millisecond)

	// Clear service calls from initial music start
	mockClient.ClearServiceCalls()

	// ==========================================================
	// ACTION: Nick enters his office during active playback
	// ==========================================================
	// This should trigger the Music Manager to:
	// 1. Detect the isNickOfficeOccupied state change
	// 2. Re-evaluate speaker mute conditions
	// 3. Unmute the Office speaker via volume_mute service
	err = stateManager.SetBool("isNickOfficeOccupied", true)
	assert.NoError(t, err)

	// Allow processing time for the callback to execute
	time.Sleep(50 * time.Millisecond)

	// ==========================================================
	// VERIFICATION: Office speaker should be unmuted
	// ==========================================================
	calls := mockClient.GetServiceCalls()

	// Look for volume_mute call with is_volume_muted=false (unmute)
	foundOfficeUnmute := false
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_mute" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok && entityID == "media_player.office" {
				isMuted, hasMuted := call.Data["is_volume_muted"].(bool)
				if hasMuted && !isMuted {
					foundOfficeUnmute = true
				}
			}
		}
	}

	assert.True(t, foundOfficeUnmute,
		"Expected media_player.volume_mute for Office speaker with is_volume_muted=false. "+
			"Calls received: %+v", calls)
}

// =============================================================================
// TEST: Kitchen Speaker Always Unmuted
// =============================================================================
//
// WHAT THIS TEST VALIDATES:
// Speakers with NO mute conditions (empty leave_muted_if) should always be
// unmuted during playback, regardless of any state changes.
//
// SCENARIO:
// Kitchen speaker has no mute conditions, so shouldUnmuteSpeaker() should
// always return true. An empty leave_muted_if array means the speaker always
// participates.
func TestScenario_KitchenSpeaker_AlwaysUnmuted(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyMusicConfig()

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(mockClient, stateManager, config, logger, false, timeProvider)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Kitchen speaker has no mute conditions - empty array
	participant := ParticipantWithVolume{
		PlayerName:   "Kitchen",
		BaseVolume:   9,
		Volume:       9,
		LeaveMutedIf: []MuteCondition{}, // Empty = no conditions = always unmuted
	}

	// ==========================================================
	// VERIFICATION: Kitchen should always be unmuted
	// ==========================================================
	shouldUnmute := manager.shouldUnmuteSpeaker(participant)
	assert.True(t, shouldUnmute,
		"Kitchen speaker with no mute conditions (empty leave_muted_if) should "+
			"always be unmuted. Empty conditions means the speaker always participates.")
}
