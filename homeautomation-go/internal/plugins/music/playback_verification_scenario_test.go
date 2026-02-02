package music

// =============================================================================
// PLAYBACK VERIFICATION SCENARIO TESTS
// =============================================================================
//
// PURPOSE:
// These tests validate that the Music Manager correctly handles playback verification
// failures. When verification fails (speaker doesn't report "playing" state), the
// system should still proceed with fade-in rather than giving up entirely.
//
// BACKGROUND:
// The playback verification was added to ensure music actually starts before ramping
// volume. However, Sonos speakers at volume 0 may not report "playing" state even
// when the play_media command succeeded. This causes a false-negative where verification
// fails but music would actually play if we proceeded with the fade-in.
//
// PRODUCTION INCIDENT (2026-01-03):
// Morning music playback was started but volumes stayed at 0. The logs showed:
//   - Speaker group created successfully
//   - play_media command sent successfully
//   - Verification failed after 3 attempts ("speaker grouped but not playing")
//   - Fade-in never executed because the function returned an error
//
// ROOT CAUSE:
// Speakers were muted to volume 0 before play_media was sent. Sonos at volume 0
// may report state "idle" instead of "playing" since there's no audible output.
//
// FIX:
// Changed executePlayback() to log a warning but continue with fade-in when
// verification fails. The play_media command was sent and the group is built,
// so attempting the fade-in often results in working playback.
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
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// =============================================================================
// TEST: Playback Verification Succeeds - Normal Fade-In
// =============================================================================
//
// SCENARIO:
// - executePlayback is called directly
// - Speaker reports "playing" state after play_media command
// - Verification succeeds
// - Fade-in proceeds normally
// - No error is returned
//
// EXPECTED BEHAVIOR:
// - play_media command is sent
// - Verification passes (speaker state is "playing")
// - volume_set commands are sent for fade-in
// - No warning logs about verification failure
// - Function returns nil error
func TestScenario_PlaybackVerificationSucceeds_FadeInProceeds(t *testing.T) {
	t.Parallel()

	// Set up logger with observer to capture log messages
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{
						PlayerName:   "Kitchen",
						BaseVolume:   15,
						LeaveMutedIf: []MuteCondition{},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{
						URI:              "spotify:playlist:test_day_playlist",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
		},
	}

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip sleeps for fast test

	// Initialize state
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", true)
	_ = stateManager.SetBool("isTVPlaying", false)

	// Set speaker to "playing" state - verification will succeed
	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID: "media_player.kitchen",
		State:    "playing", // Speaker reports playing
		Attributes: map[string]interface{}{
			"friendly_name": "Kitchen",
			"volume_level":  0.0,
		},
	})

	// Start manager to initialize
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)
	mockClient.ClearServiceCalls()

	// ==========================================================
	// ACTION: Call executePlayback directly
	// ==========================================================
	participants := []ParticipantWithVolume{
		{
			PlayerName:   "Kitchen",
			BaseVolume:   15,
			Volume:       15,
			LeaveMutedIf: []MuteCondition{},
		},
	}
	option := PlaybackOption{
		URI:              "spotify:playlist:test_day_playlist",
		MediaType:        "playlist",
		VolumeMultiplier: 1.0,
	}

	groupResult, attempts, err := manager.executePlayback("day", option, participants, "Kitchen")

	// ==========================================================
	// VERIFICATION: Playback succeeded without errors
	// ==========================================================

	// No error should be returned
	assert.NoError(t, err, "executePlayback should not return error when verification succeeds")

	// Group result should show active speaker
	assert.NotNil(t, groupResult)
	assert.Equal(t, 1, groupResult.ActiveCount)

	// Verification should have passed on first attempt
	assert.Equal(t, 1, attempts, "Verification should succeed on first attempt")

	// Check that play_media was called
	serviceCalls := mockClient.GetServiceCalls()
	playMediaCalled := false
	for _, call := range serviceCalls {
		if call.Domain == "media_player" && call.Service == "play_media" {
			playMediaCalled = true
			break
		}
	}
	assert.True(t, playMediaCalled, "play_media should be called")

	// Check that NO warning about verification failure was logged
	verificationFailureLogged := false
	for _, entry := range logs.All() {
		if entry.Message == "Playback verification failed, continuing with fade-in anyway" {
			verificationFailureLogged = true
			break
		}
	}
	assert.False(t, verificationFailureLogged,
		"No verification failure warning should be logged when playback starts successfully")

	// Check for successful completion log (async mode uses different message)
	successLogged := false
	for _, entry := range logs.All() {
		// With async speaker grouping, the success message indicates lead started
		if entry.Message == "Playback started on lead (followers joining async)" ||
			entry.Message == "Playback sequence completed successfully" {
			successLogged = true
			break
		}
	}
	assert.True(t, successLogged,
		"Success message should be logged when playback verification passes")
}

// =============================================================================
// TEST: Playback Verification Fails - Fade-In Still Proceeds
// =============================================================================
//
// SCENARIO:
// - executePlayback is called directly
// - Speaker does NOT report "playing" state (stays "idle")
// - Verification fails after all retry attempts
// - Fade-in STILL proceeds (this is the fix!)
// - Function returns nil error (not an error anymore!)
//
// EXPECTED BEHAVIOR:
// - play_media command is sent
// - Verification fails (speaker state stays "idle")
// - Warning is logged about verification failure
// - Fade-in still happens (volume_set > 0 after play_media)
// - Playback sequence completes (no error returned)
//
// WHY THIS MATTERS:
// Before the fix, verification failure caused the function to return an error,
// which prevented fade-in from ever happening. This left speakers at volume 0
// even though the play_media command had been sent successfully.
func TestScenario_PlaybackVerificationFails_FadeInStillProceeds(t *testing.T) {
	t.Parallel()

	// Set up logger with observer to capture log messages (warn level for the failure warning)
	core, logs := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{
						PlayerName:   "Kitchen",
						BaseVolume:   15,
						LeaveMutedIf: []MuteCondition{},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{
						URI:              "spotify:playlist:test_day_playlist",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
		},
	}

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip sleeps for fast test

	// Initialize state
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", true)
	_ = stateManager.SetBool("isTVPlaying", false)

	// Set speaker to "idle" state - verification will FAIL
	// This simulates Sonos at volume 0 not reporting "playing"
	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID: "media_player.kitchen",
		State:    "idle", // Speaker does NOT report playing
		Attributes: map[string]interface{}{
			"friendly_name": "Kitchen",
			"volume_level":  0.0,
		},
	})

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)
	mockClient.ClearServiceCalls()

	// ==========================================================
	// ACTION: Call executePlayback directly
	// ==========================================================
	participants := []ParticipantWithVolume{
		{
			PlayerName:   "Kitchen",
			BaseVolume:   15,
			Volume:       15,
			LeaveMutedIf: []MuteCondition{},
		},
	}
	option := PlaybackOption{
		URI:              "spotify:playlist:test_day_playlist",
		MediaType:        "playlist",
		VolumeMultiplier: 1.0,
	}

	groupResult, attempts, err := manager.executePlayback("day", option, participants, "Kitchen")

	// ==========================================================
	// VERIFICATION: Fade-in proceeds despite verification failure
	// ==========================================================

	// KEY ASSERTION: No error should be returned (this is the fix!)
	// Before the fix, this would return an error
	assert.NoError(t, err,
		"executePlayback should NOT return error when verification fails - "+
			"it should continue with fade-in anyway")

	// Group result should show active speaker
	assert.NotNil(t, groupResult)
	assert.Equal(t, 1, groupResult.ActiveCount)

	// Verification exhausted all retries
	assert.Equal(t, 3, attempts, "Verification should have exhausted all 3 retry attempts")

	// Check that play_media was called
	serviceCalls := mockClient.GetServiceCalls()
	playMediaCalled := false
	for _, call := range serviceCalls {
		if call.Domain == "media_player" && call.Service == "play_media" {
			playMediaCalled = true
			break
		}
	}
	assert.True(t, playMediaCalled, "play_media should be called")

	// Check that warning about verification failure WAS logged
	verificationFailureLogged := false
	for _, entry := range logs.All() {
		if entry.Message == "Playback verification failed, continuing with fade-in anyway" {
			verificationFailureLogged = true
			break
		}
	}
	assert.True(t, verificationFailureLogged,
		"Warning about verification failure should be logged")

	// Check that completion message (with failure note) was logged
	// Note: This is logged at Info level, need to capture it separately
	coreInfo, logsInfo := observer.New(zap.InfoLevel)
	loggerInfo := zap.New(coreInfo)

	// Run again with info logger to capture the completion message
	mockClient2 := ha.NewMockClient()
	stateManager2 := state.NewManager(mockClient2, loggerInfo, false)
	manager2 := NewManager(context.Background(), mockClient2, stateManager2, config, loggerInfo, false, timeProvider, nil)
	manager2.SetSleepFunc(func(d time.Duration) {})

	_ = stateManager2.SetString("dayPhase", "day")
	_ = stateManager2.SetBool("isAnyoneHome", true)
	_ = stateManager2.SetBool("isAnyoneAsleep", false)
	_ = stateManager2.SetBool("isMasterAsleep", false)
	_ = stateManager2.SetBool("isNickOfficeOccupied", true)
	_ = stateManager2.SetBool("isTVPlaying", false)

	mockClient2.SetMockState("media_player.kitchen", &ha.State{
		EntityID:   "media_player.kitchen",
		State:      "idle",
		Attributes: map[string]interface{}{"friendly_name": "Kitchen", "volume_level": 0.0},
	})

	err = manager2.Start()
	require.NoError(t, err)
	defer manager2.Stop()

	time.Sleep(50 * time.Millisecond)

	_, _, _ = manager2.executePlayback("day", option, participants, "Kitchen")

	completionWithFailureLogged := false
	for _, entry := range logsInfo.All() {
		// With async speaker grouping, the message is different for multi-speaker vs single speaker
		if entry.Message == "Playback started with verification failure (lead fade-in attempted, followers joining async)" ||
			entry.Message == "Playback sequence completed with verification failure (fade-in attempted anyway)" {
			completionWithFailureLogged = true
			break
		}
	}
	assert.True(t, completionWithFailureLogged,
		"Completion message (with verification failure note) should be logged")
}

// =============================================================================
// TEST: Multiple Speakers - Verification Failure Doesn't Block Any Fade-In
// =============================================================================
//
// SCENARIO:
// - executePlayback called with multiple speakers
// - Lead speaker doesn't report "playing" state
// - Verification fails
// - ALL speakers should still get fade-in attempts
//
// This test ensures the fix works correctly with speaker groups.
func TestScenario_MultiSpeaker_VerificationFailureAllSpeakersFadeIn(t *testing.T) {
	t.Parallel()

	// Set up logger at Info level to capture "Unmuting speaker" logs
	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	mockClient := ha.NewMockClient()
	stateManager := state.NewManager(mockClient, logger, false)

	// Config with multiple speakers (no mute conditions so both should unmute)
	config := &MusicConfig{
		Music: map[string]MusicMode{
			"day": {
				Participants: []Participant{
					{
						PlayerName:   "Kitchen",
						BaseVolume:   15,
						LeaveMutedIf: []MuteCondition{},
					},
					{
						PlayerName:   "Living Room",
						BaseVolume:   12,
						LeaveMutedIf: []MuteCondition{},
					},
				},
				PlaybackOptions: []PlaybackOption{
					{
						URI:              "spotify:playlist:test_day_playlist",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
		},
	}

	fixedTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	timeProvider := plugin.FixedTimeProvider{FixedTime: fixedTime}

	manager := NewManager(context.Background(), mockClient, stateManager, config, logger, false, timeProvider, nil)
	manager.SetSleepFunc(func(d time.Duration) {}) // Skip sleeps

	// Initialize state
	_ = stateManager.SetString("dayPhase", "day")
	_ = stateManager.SetBool("isAnyoneHome", true)
	_ = stateManager.SetBool("isAnyoneAsleep", false)
	_ = stateManager.SetBool("isMasterAsleep", false)
	_ = stateManager.SetBool("isNickOfficeOccupied", true)
	_ = stateManager.SetBool("isTVPlaying", false)

	// Set both speakers to "idle" - verification will fail
	mockClient.SetMockState("media_player.kitchen", &ha.State{
		EntityID:   "media_player.kitchen",
		State:      "idle",
		Attributes: map[string]interface{}{"friendly_name": "Kitchen", "volume_level": 0.0},
	})
	mockClient.SetMockState("media_player.living_room", &ha.State{
		EntityID:   "media_player.living_room",
		State:      "idle",
		Attributes: map[string]interface{}{"friendly_name": "Living Room", "volume_level": 0.0},
	})

	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop()

	time.Sleep(50 * time.Millisecond)
	mockClient.ClearServiceCalls()

	// ==========================================================
	// ACTION: Call executePlayback with multiple speakers
	// ==========================================================
	participants := []ParticipantWithVolume{
		{
			PlayerName:   "Kitchen",
			BaseVolume:   15,
			Volume:       15,
			LeaveMutedIf: []MuteCondition{},
		},
		{
			PlayerName:   "Living Room",
			BaseVolume:   12,
			Volume:       12,
			LeaveMutedIf: []MuteCondition{},
		},
	}
	option := PlaybackOption{
		URI:              "spotify:playlist:test_day_playlist",
		MediaType:        "playlist",
		VolumeMultiplier: 1.0,
	}

	groupResult, _, err := manager.executePlayback("day", option, participants, "Kitchen")

	// Wait for async speaker group building to complete
	time.Sleep(50 * time.Millisecond)

	// ==========================================================
	// VERIFICATION: All speakers get fade-in despite verification failure
	// ==========================================================

	// No error should be returned
	assert.NoError(t, err,
		"executePlayback should NOT return error - fade-in should proceed")

	// With async mode, only lead is reported as definitely active
	// Followers are processed asynchronously
	assert.NotNil(t, groupResult)
	assert.Equal(t, 1, groupResult.ActiveCount, "Only lead speaker is reported active (followers are async)")

	// Check that fade-in was logged for both speakers
	// Lead uses "Starting lead speaker fade-in", followers use "Speaker joined (async), starting fade-in"
	fadeInKitchenLogged := false
	fadeInLivingRoomLogged := false

	for _, entry := range logs.All() {
		// Lead speaker fade-in message
		if entry.Message == "Starting lead speaker fade-in" {
			for _, field := range entry.Context {
				if field.Key == "speaker" && field.String == "Kitchen" {
					fadeInKitchenLogged = true
				}
			}
		}
		// Async speaker fade-in message
		if entry.Message == "Speaker joined (async), starting fade-in" {
			for _, field := range entry.Context {
				if field.Key == "speaker" && field.String == "Living Room" {
					fadeInLivingRoomLogged = true
				}
			}
		}
	}

	assert.True(t, fadeInKitchenLogged,
		"Kitchen speaker should receive fade-in even when verification fails")
	assert.True(t, fadeInLivingRoomLogged,
		"Living Room speaker should receive fade-in even when verification fails (async)")
}
