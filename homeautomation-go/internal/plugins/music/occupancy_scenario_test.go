package music

// =============================================================================
// OCCUPANCY-BASED SPEAKER MUTE SCENARIO TESTS
// =============================================================================
//
// PURPOSE:
// These tests validate that the Music Manager correctly handles speaker mute
// conditions based on room occupancy. A speaker with a leave_muted_if condition
// should be muted when the condition matches and unmuted when it doesn't.
//
// CURRENT STATUS: PASSING
// The Music Manager now subscribes to mute condition variables and responds
// to state changes by unmuting/muting speakers during active playback.
//
// HOW leave_muted_if WORKS:
// - If ALL conditions in leave_muted_if match current state, speaker stays MUTED
// - If ANY condition does NOT match, speaker is UNMUTED
// - Empty leave_muted_if means speaker is always unmuted (e.g., Kitchen)
//
// EXAMPLE (using hypothetical "Study" speaker for testing):
// - Study speaker has: leave_muted_if: isNickOfficeOccupied = false
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
	"context"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"
	"homeautomation/pkg/plugin"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// createOccupancyMusicConfig creates a test configuration demonstrating
// occupancy-based speaker muting via leave_muted_if conditions.
//
// Uses a hypothetical "Study" speaker to test the pattern:
// - Kitchen speaker has NO mute conditions (always plays)
// - Study speaker is MUTED when room is unoccupied (isNickOfficeOccupied = false)
// - Study speaker is UNMUTED when room is occupied (isNickOfficeOccupied = true)
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
						PlayerName: "Study",
						BaseVolume: 6,
						LeaveMutedIf: []MuteCondition{
							{
								Variable: "isNickOfficeOccupied",
								Value:    false, // Mute when room is NOT occupied
							},
						},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{
						URI:              "https://tidal.com/browse/playlist/test123",
						MediaType:        "tidal",
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
						PlayerName: "Study",
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
						URI:              "https://tidal.com/browse/playlist/test-morning",
						MediaType:        "tidal",
						VolumeMultiplier: 1.0,
					},
				},
			},
		},
	}
}

// =============================================================================
// TEST: shouldUnmuteSpeaker() Logic - Study Occupied
// =============================================================================
//
// WHAT THIS TEST VALIDATES:
// The core mute condition evaluation logic in shouldUnmuteSpeaker() works correctly.
// This is a UNIT TEST of the decision logic.
//
// SCENARIO:
// 1. Start with isNickOfficeOccupied = false → Study should be MUTED
// 2. Change to isNickOfficeOccupied = true → Study should be UNMUTED
func TestScenario_StudySpeaker_UnmutedWhenOccupied(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyMusicConfig()

	// Use fixed time provider for deterministic testing
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC) // Monday 10am
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initialize required state variables
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("musicPlaybackType", "")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false) // Study NOT occupied initially
	_ = stateManager.SetBool("isTVPlaying", false)

	// Start manager to initialize subscriptions
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Allow initial processing
	time.Sleep(100 * time.Millisecond)

	// Create participant with mute conditions from config
	participant := ParticipantWithVolume{
		PlayerName:   "Study",
		BaseVolume:   6,
		Volume:       6,
		LeaveMutedIf: config.Music["day"].Participants[1].LeaveMutedIf,
	}

	// ==========================================================
	// VERIFICATION 1: Study should be MUTED when unoccupied
	// ==========================================================
	// Mute condition: isNickOfficeOccupied = false
	// Current state: isNickOfficeOccupied = false
	// Condition MATCHES → speaker stays MUTED
	shouldUnmute := manager.shouldUnmuteSpeaker(participant)
	assert.False(t, shouldUnmute,
		"Study speaker should stay MUTED when isNickOfficeOccupied = false. "+
			"The mute condition (value: false) matches current state (false).")

	// ==========================================================
	// ACTION: Someone enters the study
	// ==========================================================
	_ = stateManager.SetBool("isNickOfficeOccupied", true)

	// ==========================================================
	// VERIFICATION 2: Study should be UNMUTED when occupied
	// ==========================================================
	// Mute condition: isNickOfficeOccupied = false
	// Current state: isNickOfficeOccupied = true
	// Condition does NOT match → speaker is UNMUTED
	shouldUnmute = manager.shouldUnmuteSpeaker(participant)
	assert.True(t, shouldUnmute,
		"Study speaker should be UNMUTED when isNickOfficeOccupied = true. "+
			"The mute condition (value: false) does NOT match current state (true).")
}

// =============================================================================
// TEST: shouldUnmuteSpeaker() Logic - Study Unoccupied
// =============================================================================
//
// WHAT THIS TEST VALIDATES:
// The inverse of the above test - confirms speaker is correctly muted when
// Someone leaves the study.
//
// SCENARIO:
// 1. Start with isNickOfficeOccupied = true → Study should be UNMUTED
// 2. Change to isNickOfficeOccupied = false → Study should be MUTED
func TestScenario_StudySpeaker_MutedWhenUnoccupied(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyMusicConfig()

	// Use fixed time provider
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initialize - study IS occupied
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", true) // Study IS occupied initially

	// Create participant with mute conditions
	participant := ParticipantWithVolume{
		PlayerName:   "Study",
		BaseVolume:   6,
		Volume:       6,
		LeaveMutedIf: config.Music["day"].Participants[1].LeaveMutedIf,
	}

	// ==========================================================
	// VERIFICATION 1: Study should be UNMUTED when occupied
	// ==========================================================
	shouldUnmute := manager.shouldUnmuteSpeaker(participant)
	assert.True(t, shouldUnmute,
		"Study speaker should be UNMUTED when isNickOfficeOccupied = true")

	// ==========================================================
	// ACTION: Someone leaves the study
	// ==========================================================
	_ = stateManager.SetBool("isNickOfficeOccupied", false)

	// ==========================================================
	// VERIFICATION 2: Study should be MUTED when unoccupied
	// ==========================================================
	shouldUnmute = manager.shouldUnmuteSpeaker(participant)
	assert.False(t, shouldUnmute,
		"Study speaker should be MUTED when isNickOfficeOccupied = false")
}

// =============================================================================
// TEST: Real-Time Speaker Unmute During Active Playback
// =============================================================================
//
// WHAT THIS TEST VALIDATES:
// When music is actively playing and Someone enters the study, the Music Manager
// should AUTOMATICALLY unmute the Study speaker by setting its volume.
//
// THIS IS THE KEY INTEGRATION TEST that validates the subscription mechanism.
//
// SCENARIO:
// 1. Music starts playing in "day" mode
// 2. Study speaker is initially MUTED (isNickOfficeOccupied = false)
// 3. Someone enters the study (isNickOfficeOccupied = true)
// 4. Music Manager should detect the change and unmute the Study speaker
//
// EXPECTED BEHAVIOR:
// - Service call: media_player.volume_mute with is_volume_muted=false
// - Entity: media_player.study
// - No volume_set needed (volume was set during initial playback)
func TestScenario_StudySpeaker_UnmuteOnOccupancyChangeDuringPlayback(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyMusicConfig()

	// Use fixed time provider
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initialize required state variables - music will start playing
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("musicPlaybackType", "")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false) // Study NOT occupied
	_ = stateManager.SetBool("isTVPlaying", false)

	// Start manager - music should start playing (Kitchen speaker only)
	err := manager.Start()
	assert.NoError(t, err)
	defer manager.Stop()

	// Allow some processing time for initial music start
	time.Sleep(50 * time.Millisecond)

	// Snapshot service calls from initial music start
	snapshot := mockClient.ServiceCallCount()

	// ==========================================================
	// ACTION: Someone enters the study during active playback
	// ==========================================================
	// This should trigger the Music Manager to:
	// 1. Detect the isNickOfficeOccupied state change
	// 2. Re-evaluate speaker mute conditions
	// 3. Unmute the Study speaker via volume_mute service
	err = stateManager.SetBool("isNickOfficeOccupied", true)
	assert.NoError(t, err)

	// Allow processing time for the callback to execute
	time.Sleep(50 * time.Millisecond)

	// ==========================================================
	// VERIFICATION: Study speaker should be unmuted
	// ==========================================================
	calls := mockClient.GetServiceCallsSince(snapshot)

	// Look for volume_mute call with is_volume_muted=false (unmute)
	foundStudyUnmute := false
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_mute" {
			entityID, ok := call.Data["entity_id"].(string)
			if ok && entityID == "media_player.study" {
				isMuted, hasMuted := call.Data["is_volume_muted"].(bool)
				if hasMuted && !isMuted {
					foundStudyUnmute = true
				}
			}
		}
	}

	assert.True(t, foundStudyUnmute,
		"Expected media_player.volume_mute for Study speaker with is_volume_muted=false. "+
			"Calls received: %+v", calls)
}

// =============================================================================
// TEST: Muted Speakers Get Target Volume Set During Playback
// =============================================================================
//
// WHAT THIS TEST VALIDATES:
// When executePlayback() runs with a speaker that has leave_muted_if conditions
// matching current state, the speaker should:
// 1. Have its target volume set via volume_set (NOT left at 0)
// 2. Be explicitly muted via volume_mute
//
// This ensures volume and mute state are independent concepts. When the room
// becomes occupied later and the speaker is unmuted, it will immediately
// play at the correct volume level instead of 0.
//
// BUG CONTEXT:
// Previously, muted speakers had their volume left at 0 after Step 3 of
// executePlayback(). When later unmuted (room became occupied), they would
// unmute but remain at volume 0. This test codifies the fix.
//
// SCENARIO:
// 1. Study is unoccupied (isNickOfficeOccupied = false)
// 2. executePlayback() is called with Kitchen + Study speakers
// 3. Study speaker should be muted but with target volume set
//
// EXPECTED SERVICE CALLS FOR STUDY:
// - media_player.volume_set with volume_level = 0.06 (6%)
// - media_player.volume_mute with is_volume_muted = true
func TestScenario_MutedSpeaker_GetsTargetVolumeSetDuringPlayback(t *testing.T) {
	t.Parallel()
	logger := zap.NewNop()
	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)
	config := createOccupancyMusicConfig()

	// Use fixed time provider
	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip internal sleeps for fast tests

	// Initialize state - Study is NOT occupied, so Study speaker should be muted
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetString("musicPlaybackType", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isEveryoneAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", false) // Study NOT occupied → speaker muted
	_ = stateManager.SetBool("isTVPlaying", false)

	// Set up mock to report "playing" state (to pass playback verification)
	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID: "media_player.kitchen",
		State:    "playing",
	})
	mockClient.SetMockState("media_player.study", &ha.State{
		EntityID: "media_player.study",
		State:    "playing",
	})

	// ==========================================================
	// ACTION: Call executePlayback with both Kitchen and Study
	// ==========================================================
	participants := []ParticipantWithVolume{
		{
			PlayerName:   "Kitchen",
			BaseVolume:   9,
			Volume:       9,
			LeaveMutedIf: []MuteCondition{}, // No conditions = always unmuted
		},
		{
			PlayerName: "Study",
			BaseVolume: 6,
			Volume:     6, // Target volume = 6%
			LeaveMutedIf: []MuteCondition{
				{
					Variable: "isNickOfficeOccupied",
					Value:    false, // Mute when study is NOT occupied
				},
			},
		},
	}

	option := PlaybackOption{
		URI:              "https://tidal.com/browse/playlist/test123",
		MediaType:        "tidal",
		VolumeMultiplier: 1.0,
	}

	// Wire up mock SoCo server for Tidal playback path
	socoPaths := setupSoCoForTest(t, manager, false)

	_, _, err := manager.executePlayback("day", option, participants, "Kitchen")
	assert.NoError(t, err, "executePlayback should succeed")

	// ==========================================================
	// VERIFICATION: Study speaker received volume and mute via SoCo-CLI
	// ==========================================================
	// With SoCo configured, volume and mute commands route through SoCo HTTP API
	// instead of HA service calls. Poll for expected paths since buildSpeakerGroupAsync
	// runs asynchronously and may not complete within a fixed sleep window.
	var allPaths []string
	foundStudyVolumeSet := false
	foundStudyMute := false
	for attempt := 0; attempt < 200; attempt++ {
		allPaths = socoPaths.All()
		for _, path := range allPaths {
			if path == "/Study/volume/6" {
				foundStudyVolumeSet = true
			}
			if path == "/Study/mute" {
				foundStudyMute = true
			}
		}
		if foundStudyVolumeSet && foundStudyMute {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.True(t, foundStudyVolumeSet,
		"Expected SoCo volume/6 for Study speaker. "+
			"This ensures muted speakers have correct volume pre-set via SoCo-CLI. Paths: %v", allPaths)

	assert.True(t, foundStudyMute,
		"Expected SoCo mute for Study speaker. "+
			"Muted speakers should be explicitly muted via SoCo-CLI. Paths: %v", allPaths)
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

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, timeProvider, nil)
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
