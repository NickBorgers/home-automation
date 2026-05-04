package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/config"
	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/sleephygiene"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ============================================================================
// Wake Sequence Music Fade-Out Tests (Cross-Plugin: sleephygiene + music)
//
// These tests validate that the sleep music fade-out during the morning wake
// sequence is GRADUAL (25+ minutes), not abrupt (~1 second).
//
// PRODUCTION BUG (2026-02-12):
// Rain (sleep) music stopped abruptly during the wake sequence because:
// 1. handleBeginWake() sets isWakeSequenceActive = true
// 2. Music plugin's zone manager re-evaluates zones concurrently
// 3. Sleep zone stops (requires isWakeSequenceActive=false), morning zone starts
// 4. Morning zone sets musicPlaybackType = "morning"
// 5. fadeOutSpeaker checks musicPlaybackType == "sleep", finds "morning", aborts
//
// The existing TriggerBeginWakeForTest() does NOT set isWakeSequenceActive,
// so the music plugin never re-evaluates zones and the race never manifests.
//
// INVARIANTS:
// - Sleep music fade-out must complete with gradual volume steps (1% at a time)
// - Volume must decrease monotonically during fade-out
// - No single step should drop more than 2% (rules out quick-fade abort patterns)
// - Fade-out must reach volume 0 (not abort partway)
// ============================================================================

// wakeMusicFadeEnv holds plugins for wake sequence + music fade tests
type wakeMusicFadeEnv struct {
	server        *MockHAServer
	logger        *zap.Logger
	stateManager  *state.Manager
	stateTracking *statetracking.Manager
	music         *music.Manager
	sleepHygiene  *sleephygiene.Manager
}

func setupWakeMusicFadeTest(t *testing.T) (*wakeMusicFadeEnv, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Load configs
	musicConfig := loadTestMusicConfigFromYAML(t)
	configLoader := config.NewLoader("../../configs", logger)

	// Create plugins
	env := &wakeMusicFadeEnv{
		server:        server,
		logger:        logger,
		stateManager:  stateManager,
		stateTracking: statetracking.NewManager(context.Background(), client, stateManager, logger, false, nil, "", &alert.MockAlerter{}),
		music:         music.NewManager(context.Background(), client, stateManager, musicConfig, logger, false, nil, nil),
		sleepHygiene:  sleephygiene.NewManager(context.Background(), client, stateManager, configLoader, logger, false, nil, nil),
	}

	// Skip real delays in both plugins
	env.music.SetSleepFunc(func(d time.Duration) {})
	env.sleepHygiene.SetSleepFunc(func(d time.Duration) {})

	// Set up media player entities that both plugins expect
	server.SetState("media_player.bedroom", "playing", map[string]interface{}{
		"friendly_name": "Bedroom",
		"volume_level":  0.14, // 14% volume - typical sleep music level
	})
	server.SetState("media_player.kitchen", "idle", map[string]interface{}{
		"friendly_name": "Kitchen",
		"volume_level":  0.1,
	})
	server.SetState("media_player.sitting_room", "idle", map[string]interface{}{
		"friendly_name": "Sitting Room",
		"volume_level":  0.1,
	})

	// Set up Eight Sleep sensor entities (required for subscription setup)
	server.SetState("sensor.nick_s_eight_sleep_side_bed_state_type", "none", map[string]interface{}{
		"friendly_name": "Nick's Eight Sleep Side Bed State Type",
	})
	server.SetState("sensor.caroline_s_eight_sleep_side_bed_state_type", "none", map[string]interface{}{
		"friendly_name": "Caroline's Eight Sleep Side Bed State Type",
	})

	// Set up bedroom light entity (required for sleephygiene subscription)
	server.SetState("light.primary_suite", "off", map[string]interface{}{
		"friendly_name": "Primary Suite",
	})

	// Start plugins in dependency order
	require.NoError(t, env.stateTracking.Start(), "Failed to start state tracking")
	require.NoError(t, env.music.Start(), "Failed to start music")
	require.NoError(t, env.sleepHygiene.Start(), "Failed to start sleep hygiene")

	// Wait for plugin initialization handlers to complete
	waitForProcessing(t, stateManager)

	cleanup := func() {
		env.sleepHygiene.Stop()
		env.music.Stop()
		env.stateTracking.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// ============================================================================
// Test: Wake Sequence Bedroom Fade Is Gradual, Not Abrupt
// ============================================================================

// TestScenario_WakeSequence_BedroomFadeIsGradualNotAbrupt validates that when
// the Eight Sleep alarm fires, the bedroom sleep music fades out GRADUALLY
// (one percentage point at a time over ~25 minutes) rather than stopping
// abruptly.
//
// User story: "When my Eight Sleep alarm goes off, rain sounds should fade
// out gently over 25 minutes so I wake up gradually, not with a jarring
// sudden silence."
//
// This test exercises the REAL cross-plugin interaction:
// 1. Eight Sleep sensor changes to "alarm" -> sleephygiene subscribes to this
// 2. sleephygiene.handleEightSleepAlarm -> handleBeginWake -> sets isWakeSequenceActive=true
// 3. Music plugin sees isWakeSequenceActive change -> zone manager re-evaluates
// 4. Sleep zone stops (requires isWakeSequenceActive=false), morning zone starts
// 5. Morning zone sets musicPlaybackType = "morning"
// 6. Meanwhile, fadeOutSpeaker is running and checks musicPlaybackType == "sleep"
// 7. BUG: fadeOutSpeaker finds "morning", aborts immediately
//
// EXPECTED BEHAVIOR (what this test asserts):
// - At least 10 gradual volume_set calls (14 steps from 13% down to 0%)
// - Monotonically decreasing volume
// - No abrupt drops > 2% between consecutive calls
// - Final volume reaches 0
func TestScenario_WakeSequence_BedroomFadeIsGradualNotAbrupt(t *testing.T) {
	t.Parallel()
	env, cleanup := setupWakeMusicFadeTest(t)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: Morning, someone home, master asleep, sleep music playing")
	t.Log("       Bedroom speaker at 14% volume with rain sounds")

	env.server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.everyone_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_text.music_playback_type", "sleep", map[string]interface{}{})
	env.server.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.fade_out_in_progress", "off", map[string]interface{}{})

	// Set up currentlyPlayingMusic state with bedroom speaker at 14% volume
	currentMusicJSON := `{"participants":[{"player_name":"media_player.bedroom","volume":14}]}`
	env.server.SetState("input_text.currently_playing_music", currentMusicJSON, map[string]interface{}{})

	// Allow all plugins to process the initial state. Use a stabilization window
	// in addition to waitForProcessing because zone orchestration spawns
	// fire-and-forget goroutines (volume_set for bedroom seeding) that would
	// otherwise land post-snapshot and pollute the bedroom fade-out measurement.
	waitForProcessing(t, env.stateManager)
	waitForServiceCallQuiescenceSince(t, env.server, 0, 200*time.Millisecond)
	snapshot := env.server.ServiceCallCount()

	// ========== WHEN ==========
	t.Log("WHEN: Eight Sleep alarm fires (sensor state -> 'alarm')")
	t.Log("      This triggers the REAL handleEightSleepAlarm -> handleBeginWake path")
	t.Log("      Both sleephygiene and music plugins process state changes concurrently")

	// Fire the Eight Sleep alarm - this is the REAL trigger path, not TriggerBeginWakeForTest
	env.server.SetState("sensor.nick_s_eight_sleep_side_bed_state_type", "alarm", map[string]interface{}{
		"friendly_name": "Nick's Eight Sleep Side Bed State Type",
	})

	// Wait for the fade-out to complete.
	// With sleepFunc mocked to no-op, the fade-out loop runs very quickly.
	// We need enough time for:
	// 1. Eight Sleep state change to propagate to sleephygiene plugin
	// 2. handleBeginWake to run and set isWakeSequenceActive=true
	// 3. Music plugin to react to isWakeSequenceActive change (zone re-evaluation)
	// 4. handleBeginWake to set isFadeOutInProgress=true and start goroutine
	// 5. fadeOutSpeaker to iterate through all volume steps (or abort)
	//
	// IMPORTANT: We must first wait for isFadeOutInProgress to become "on"
	// BEFORE checking for "off". Otherwise the condition matches immediately
	// because isFadeOutInProgress starts as "off" and the music plugin's
	// synchronous zone re-evaluation runs before handleBeginWake reaches
	// the SetBool("isFadeOutInProgress", true) call.
	waitForCondition(t, func() bool {
		fadeState := env.server.GetState("input_boolean.fade_out_in_progress")
		return fadeState != nil && fadeState.State == "on"
	}, "fade-out should start (isFadeOutInProgress should become on)")

	// Now wait for the fade-out to complete (isFadeOutInProgress goes back to off)
	waitForCondition(t, func() bool {
		fadeState := env.server.GetState("input_boolean.fade_out_in_progress")
		return fadeState != nil && fadeState.State == "off"
	}, "fade-out should complete (isFadeOutInProgress should return to off)")

	// ========== THEN ==========
	t.Log("THEN: Verify the bedroom volume fade-out was GRADUAL, not abrupt")

	calls := env.server.GetServiceCallsSince(snapshot)
	volumeCalls := filterServiceCalls(calls, "media_player", "volume_set")

	// Filter to only bedroom speaker volume calls
	var bedroomVolumeCalls []ServiceCall
	for _, call := range volumeCalls {
		entityID, _ := call.ServiceData["entity_id"].(string)
		if entityID == "media_player.bedroom" {
			bedroomVolumeCalls = append(bedroomVolumeCalls, call)
		}
	}

	// Extract ALL bedroom volume levels in order
	var allBedroomVolumes []float64
	for _, call := range bedroomVolumeCalls {
		volumeLevel, ok := call.ServiceData["volume_level"].(float64)
		if !ok {
			continue
		}
		allBedroomVolumes = append(allBedroomVolumes, volumeLevel)
	}

	t.Logf("Total bedroom volume_set calls: %d", len(bedroomVolumeCalls))
	t.Logf("All bedroom volume levels: %v", allBedroomVolumes)

	// Extract the FADE-OUT sequence from the interleaved calls.
	// The bedroom receives calls from two sources concurrently:
	//   1. sleephygiene fadeOutSpeaker: 0.13, 0.12, 0.11, ..., 0.01, 0.00 (1% steps)
	//   2. music plugin morning zone: fade-in with small increasing values
	//
	// The fade-out decrements by exactly 1 percentage point per step (0.01).
	// Extract calls that form this exact pattern: find the start (highest value
	// >= 0.10), then match each subsequent expected value.
	var fadeOutVolumes []float64
	fadeOutStarted := false
	var nextExpected float64
	tolerance := 0.001 // float comparison tolerance
	for _, vol := range allBedroomVolumes {
		if !fadeOutStarted {
			// Look for the first call at or near initial volume (>= 0.10)
			// The fade-out starts at currentVolume - 1 = 13% = 0.13
			if vol >= 0.10 {
				fadeOutStarted = true
				fadeOutVolumes = append(fadeOutVolumes, vol)
				nextExpected = vol - 0.01
			}
		} else if nextExpected >= -tolerance {
			// Match the next expected 1% step
			diff := vol - nextExpected
			if diff < 0 {
				diff = -diff
			}
			if diff < tolerance {
				fadeOutVolumes = append(fadeOutVolumes, vol)
				nextExpected = vol - 0.01
			}
		}
	}

	t.Logf("Fade-out volume sequence: %v", fadeOutVolumes)

	// ASSERTION 1: At least 10 gradual fade-out volume_set calls
	// Starting from 14%, there should be 14 steps (13, 12, 11, ... 1, 0) = 14 calls
	// We assert >= 10 to allow some tolerance
	assert.GreaterOrEqual(t, len(fadeOutVolumes), 10,
		"FADE-OUT WAS ABRUPT: Expected at least 10 gradual fade-out volume_set calls "+
			"(14%% down to 0%%), but got %d. This indicates the fade-out was aborted "+
			"prematurely.",
		len(fadeOutVolumes))

	// ASSERTION 2: Fade-out volume decreases monotonically
	for i := 1; i < len(fadeOutVolumes); i++ {
		assert.LessOrEqual(t, fadeOutVolumes[i], fadeOutVolumes[i-1],
			"Fade-out volume must DECREASE monotonically: "+
				"step %d (%.3f) should be <= step %d (%.3f)",
			i+1, fadeOutVolumes[i], i, fadeOutVolumes[i-1])
	}

	// ASSERTION 3: No abrupt drops > 2% (0.02) between consecutive fade-out calls
	for i := 1; i < len(fadeOutVolumes); i++ {
		drop := fadeOutVolumes[i-1] - fadeOutVolumes[i]
		assert.LessOrEqual(t, drop, 0.02,
			"ABRUPT DROP DETECTED: Fade-out volume dropped %.1f%% in a single step "+
				"(step %d: %.3f -> step %d: %.3f). Expected <= 2%% per step.",
			drop*100, i, fadeOutVolumes[i-1], i+1, fadeOutVolumes[i])
	}

	// ASSERTION 4: Fade completes to volume 0
	if len(fadeOutVolumes) > 0 {
		finalVolume := fadeOutVolumes[len(fadeOutVolumes)-1]
		assert.Equal(t, 0.0, finalVolume,
			"Fade-out should complete to volume 0, but got %.3f. "+
				"This suggests the fade was aborted before completing.",
			finalVolume)
	}

	// ASSERTION 5: Wake sequence is active (handleBeginWake ran)
	wakeState := env.server.GetState("input_boolean.wake_sequence_active")
	require.NotNil(t, wakeState, "isWakeSequenceActive state should exist")
	assert.Equal(t, "on", wakeState.State,
		"isWakeSequenceActive should be true after Eight Sleep alarm fires")

	// ASSERTION 6: Fade completed (not aborted) - isFadeOutInProgress back to false
	fadeState := env.server.GetState("input_boolean.fade_out_in_progress")
	require.NotNil(t, fadeState, "isFadeOutInProgress state should exist")
	assert.Equal(t, "off", fadeState.State,
		"isFadeOutInProgress should be false after fade-out completes")

	// Log summary
	t.Log("========================================")
	if len(fadeOutVolumes) >= 10 {
		t.Log("SUCCESS: Bedroom volume faded out gradually")
	} else {
		t.Log("FAILURE: Bedroom volume fade-out was abrupt")
	}
	t.Logf("  Fade-out steps: %d (expected >= 10)", len(fadeOutVolumes))
	t.Logf("  Fade-out sequence: %v", fadeOutVolumes)
	t.Log("========================================")
}
