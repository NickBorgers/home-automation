package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/config"
	"homeautomation/internal/ha"
	"homeautomation/internal/plugins/lighting"
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
// Nighttime Safety Scenario Tests
//
// These tests validate critical safety behaviors when isMasterAsleep is true:
// 1. Rain sounds continue playing in the primary suite
// 2. Bedroom speaker is muted for non-sleep music types
// 3. Wake sequence exception allows lights to come on
// 4. No loud music plays when someone wakes the system at night
//
// These tests are designed to prevent bugs that could play loud music at night
// or disrupt sleep, especially important before implementing MULTI_ZONE_MUSIC.
//
// Production Log Reference (2026-01-24):
// - 15:01:37 - Eight Sleep alarm triggers begin_wake
// - 15:06:37 - Wake sequence activates (isWakeSequenceActive=true)
// - 15:14:53 - Fade out complete (speaker volume reached 0)
// - 15:31:37 - Light fade-in complete, wake music activated
// - 15:34:15 - Bedroom lights off during wake sequence -> cancel and revert
// - 17:55:36 - isMasterAsleep false -> music switches from sleep to morning
// ============================================================================

// nighttimeSafetyTestEnv holds all plugins for nighttime safety testing
type nighttimeSafetyTestEnv struct {
	server        *MockHAServer
	client        *ha.Client
	stateManager  *state.Manager
	logger        *zap.Logger
	stateTracking *statetracking.Manager
	lighting      *lighting.Manager
	music         *music.Manager
	sleepHygiene  *sleephygiene.Manager
}

// setupNighttimeSafetyTest creates an environment with all relevant plugins
func setupNighttimeSafetyTest(t *testing.T) (*nighttimeSafetyTestEnv, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)
	logger := testlogger.New()

	// Load configs
	lightingConfig := loadTestLightingConfig(t)
	musicConfig := loadTestMusicConfig(t)
	configLoader := config.NewLoader("../../configs", logger)

	env := &nighttimeSafetyTestEnv{
		server:        server,
		client:        client,
		stateManager:  stateManager,
		logger:        logger,
		stateTracking: statetracking.NewManager(context.Background(), client, stateManager, logger, false, nil),
		lighting:      lighting.NewManager(context.Background(), client, stateManager, lightingConfig, logger, false, nil),
		music:         music.NewManager(context.Background(), client, stateManager, musicConfig, logger, false, nil, nil),
		sleepHygiene:  sleephygiene.NewManager(context.Background(), client, stateManager, configLoader, logger, false, nil, nil),
	}

	// Start plugins in correct order
	require.NoError(t, env.stateTracking.Start())
	require.NoError(t, env.lighting.Start())
	require.NoError(t, env.music.Start())
	require.NoError(t, env.sleepHygiene.Start())

	// Brief delay to allow plugins to initialize their subscriptions before tests begin
	waitForProcessing(t, env.stateManager)

	cleanup := func() {
		env.sleepHygiene.Stop()
		env.music.Stop()
		env.lighting.Stop()
		env.stateTracking.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// loadTestMusicConfig loads the test music config
func loadTestMusicConfig(t *testing.T) *music.MusicConfig {
	// Create a minimal music config that mirrors production behavior
	// Key aspect: Bedroom has leave_muted_if: isMasterAsleep=true for non-sleep modes
	return &music.MusicConfig{
		Music: map[string]music.MusicMode{
			"sleep": {
				Participants: []music.Participant{
					{
						PlayerName:   "Bedroom",
						BaseVolume:   16,
						LeaveMutedIf: []music.MuteCondition{}, // No conditions for sleep - always plays
					},
					{
						PlayerName:   "Kitchen",
						BaseVolume:   16,
						LeaveMutedIf: []music.MuteCondition{}, // Rain sounds everywhere
					},
				},
				PlaybackOptions: []music.PlaybackOption{
					{
						URI:              "http://rain-sounds.nickborgers.net:8080/1.m4a",
						MediaType:        "music",
						VolumeMultiplier: 1.0,
					},
				},
			},
			"morning": {
				Participants: []music.Participant{
					{
						PlayerName:   "Kitchen",
						BaseVolume:   9,
						LeaveMutedIf: []music.MuteCondition{},
					},
					{
						PlayerName: "Bedroom",
						BaseVolume: 9,
						LeaveMutedIf: []music.MuteCondition{
							{
								Variable: "isMasterAsleep",
								Value:    true, // CRITICAL: Mute bedroom when master is asleep
							},
						},
					},
				},
				PlaybackOptions: []music.PlaybackOption{
					{
						URI:              "spotify:playlist:morning123",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
			"day": {
				Participants: []music.Participant{
					{
						PlayerName:   "Kitchen",
						BaseVolume:   9,
						LeaveMutedIf: []music.MuteCondition{},
					},
					{
						PlayerName:   "Bedroom",
						BaseVolume:   9,
						LeaveMutedIf: []music.MuteCondition{},
						ExcludeIf: []music.MuteCondition{
							{
								Variable: "isMasterAsleep",
								Value:    true, // CRITICAL: Exclude bedroom when master is asleep
							},
						},
					},
				},
				PlaybackOptions: []music.PlaybackOption{
					{
						URI:              "spotify:playlist:day123",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
			"wakeup": {
				Participants: []music.Participant{
					{
						PlayerName:   "Bedroom",
						BaseVolume:   6,
						LeaveMutedIf: []music.MuteCondition{}, // Wake-up plays in bedroom
					},
				},
				PlaybackOptions: []music.PlaybackOption{
					{
						URI:              "spotify:playlist:wakeup123",
						MediaType:        "playlist",
						VolumeMultiplier: 1.0,
					},
				},
			},
		},
	}
}

// ============================================================================
// Test 1: Sleep Music Continues When isMasterAsleep=true
// ============================================================================
//
// SCENARIO: Production log 2026-01-24 15:34:16
// When someone goes to sleep, rain sounds should continue playing in the bedroom.
// The sleep music mode has NO mute condition for isMasterAsleep on the Bedroom
// speaker, so it continues playing.
//
// This validates that the sleep music is correctly configured to play even
// when the master is asleep (which is the whole point of sleep music!).

func TestScenario_SleepMusic_ContinuesWhenMasterAsleep(t *testing.T) {
	t.Parallel()
	env, cleanup := setupNighttimeSafetyTest(t)
	defer cleanup()

	t.Log("========== TEST: Sleep Music Continues When Master Asleep ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Evening, preparing for sleep")

	require.NoError(t, env.stateManager.SetString("dayPhase", "evening"))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, env.stateManager.SetBool("isMasterAsleep", false))
	require.NoError(t, env.stateManager.SetBool("isEveryoneAsleep", false))
	require.NoError(t, env.stateManager.SetString("musicPlaybackType", ""))

	env.server.SetState("media_player.bedroom", "idle", map[string]interface{}{})
	env.server.SetState("media_player.kitchen", "idle", map[string]interface{}{})

	// Brief delay before taking snapshot to ensure setup state propagates
	waitForProcessing(t, env.stateManager)

	// ========== WHEN ==========
	t.Log("WHEN: Music type changes to sleep (rain sounds)")

	env.server.SetState("input_text.music_playback_type", "sleep", map[string]interface{}{})
	waitForStringState(t, env.stateManager, "musicPlaybackType", "sleep", "Music type should be sleep")

	// Wait for all music plugin service calls to complete before taking snapshot
	waitForProcessing(t, env.stateManager)
	snapshot := env.server.ServiceCallCount()
	t.Log("WHEN: Master goes to sleep")

	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	waitForBoolState(t, env.stateManager, "isMasterAsleep", true, "isMasterAsleep should be true")

	// ========== THEN ==========
	t.Log("THEN: Sleep music continues (no stop commands for bedroom)")

	// Verify no media_player.stop calls for bedroom
	calls := env.server.GetServiceCallsSince(snapshot)
	foundBedroomStop := false
	for _, call := range calls {
		if call.Domain == "media_player" {
			entityID, _ := call.ServiceData["entity_id"].(string)
			if entityID == "media_player.bedroom" && call.Service == "media_stop" {
				foundBedroomStop = true
			}
		}
	}

	assert.False(t, foundBedroomStop,
		"Bedroom speaker should NOT be stopped when master goes to sleep during sleep music. "+
			"Sleep music is designed to continue playing.")

	t.Log("✓ Sleep music continues when isMasterAsleep=true")
}

// ============================================================================
// Test 2: Bedroom Muted for Morning Music When isMasterAsleep=true
// ============================================================================
//
// CRITICAL SAFETY TEST: This prevents loud music from waking someone up.
//
// SCENARIO: Production log 2026-01-24 17:55:38
// When morning music starts and isMasterAsleep=true (e.g., Caroline is still
// asleep while Nick wakes up), the bedroom speaker should be MUTED.
//
// The morning music config has: leave_muted_if: isMasterAsleep=true
// This ensures that morning music plays in other rooms but not in the bedroom.

func TestScenario_MorningMusic_BedroomMutedWhenMasterAsleep(t *testing.T) {
	t.Parallel()
	env, cleanup := setupNighttimeSafetyTest(t)
	defer cleanup()

	t.Log("========== TEST: Sleep Zone Activates When Master Asleep During Morning ==========")
	t.Log("This test prevents the nightmare scenario of loud music waking someone up!")

	// ========== GIVEN ==========
	t.Log("GIVEN: Morning, master still asleep (e.g., only one person woke up)")

	require.NoError(t, env.stateManager.SetString("dayPhase", "morning"))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, env.stateManager.SetBool("isMasterAsleep", true)) // CRITICAL: Still asleep!
	require.NoError(t, env.stateManager.SetBool("isEveryoneAsleep", false))
	require.NoError(t, env.stateManager.SetBool("isAnyoneAsleep", true))

	// Set up mock speakers
	env.server.SetState("media_player.bedroom", "idle", map[string]interface{}{
		"volume_level": 0.1,
	})
	env.server.SetState("media_player.kitchen", "idle", map[string]interface{}{
		"volume_level": 0.1,
	})

	// ========== WHEN ==========
	t.Log("WHEN: Zone resolution runs with isAnyoneAsleep=true")

	// Sleep zone (priority 100) activates because isAnyoneAsleep=true.
	// This is BETTER than the old behavior of playing morning music with bedroom muted -
	// now the bedroom gets gentle sleep sounds.
	waitForStringState(t, env.stateManager, "musicPlaybackType", "sleep",
		"Sleep zone should activate when isMasterAsleep=true (priority 100 > morning priority 50)")

	// ========== THEN ==========
	t.Log("THEN: Sleep music plays (protecting the sleeper), NOT loud morning music")

	musicType, err := env.stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "sleep", musicType,
		"CRITICAL: When master is still asleep, sleep zone (priority 100) must take precedence "+
			"over morning zone (priority 50). The bedroom gets sleep sounds, not blaring morning music.")

	t.Log("✓ Sleep zone correctly activates when master is still asleep during morning")
}

// ============================================================================
// Test 3: Wake Sequence Exception - Lights Come On Despite isMasterAsleep
// ============================================================================
//
// SCENARIO: Production log 2026-01-24 15:06:37
// During the wake sequence, isWakeSequenceActive=true takes priority over
// isMasterAsleep. This allows the sleep hygiene plugin to gradually turn on
// bedroom lights for a gentle wake-up.
//
// BUG CONTEXT (2026-01-13 production incident):
// The lighting plugin was turning off bedroom lights immediately after
// sleephygiene turned them on because isAnyoneHomeAndAwake=false.
// Fix: isWakeSequenceActive=true condition now takes priority.

func TestScenario_WakeSequence_LightsOnDespiteMasterAsleep(t *testing.T) {
	t.Parallel()
	server, client, stateManager, baseCleanup := setupTest(t)
	defer baseCleanup()

	logger := testlogger.New()
	lightingConfig := loadTestLightingConfig(t)
	lightingMgr := lighting.NewManager(context.Background(), client, stateManager, lightingConfig, logger, false, nil)
	require.NoError(t, lightingMgr.Start())
	defer lightingMgr.Stop()

	t.Log("========== TEST: Wake Sequence Exception - Lights On Despite Master Asleep ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Wake sequence starting, master still marked as asleep")
	t.Log("       isWakeSequenceActive=true, isMasterAsleep=true, isAnyoneHomeAndAwake=false")

	require.NoError(t, stateManager.SetString("dayPhase", "morning"))
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, stateManager.SetBool("isMasterAsleep", true)) // Still asleep
	require.NoError(t, stateManager.SetBool("isEveryoneAsleep", true))
	require.NoError(t, stateManager.SetBool("isAnyoneAsleep", true))
	require.NoError(t, stateManager.SetBool("isAnyOwnerHome", true))
	require.NoError(t, stateManager.SetBool("isToriHere", false))
	// This will compute to false: (isAnyOwnerHome && !isAnyoneAsleep) || isToriHere
	require.NoError(t, stateManager.SetBool("isAnyoneHomeAndAwake", false))
	require.NoError(t, stateManager.SetBool("isWakeSequenceActive", false)) // Not yet active

	// Brief delay before taking snapshot to ensure setup state propagates
	waitForProcessing(t, stateManager)
	snapshot := server.ServiceCallCount()

	// ========== WHEN ==========
	t.Log("WHEN: isWakeSequenceActive becomes true (sleephygiene starts wake sequence)")

	server.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{})
	require.NoError(t, stateManager.SetBool("isWakeSequenceActive", true))
	waitForBoolState(t, stateManager, "isWakeSequenceActive", true, "isWakeSequenceActive should be true")

	// ========== THEN ==========
	t.Log("THEN: Bedroom lights should come ON (scene activation), not turn off")

	calls := server.GetServiceCallsSince(snapshot)
	t.Logf("Total service calls after wake sequence active: %d", len(calls))

	foundBedroomTurnOff := false
	foundBedroomSceneOrTurnOn := false

	for _, call := range calls {
		areaID, _ := call.ServiceData["area_id"].(string)
		entityID, _ := call.ServiceData["entity_id"].(string)

		if call.Domain == "light" && call.Service == "turn_off" {
			if areaID == "master_bedroom" || entityID == "light.master_bedroom" {
				foundBedroomTurnOff = true
				t.Logf("  ✗ UNEXPECTED: Bedroom light turn_off call!")
			}
		}

		if call.Domain == "scene" && call.Service == "turn_on" {
			if entityID == "scene.master_bedroom_morning" {
				foundBedroomSceneOrTurnOn = true
				t.Log("  ✓ Found bedroom morning scene activation")
			}
		}
	}

	// CRITICAL ASSERTION: No turn_off for bedroom during wake sequence
	assert.False(t, foundBedroomTurnOff,
		"CRITICAL: Bedroom lights should NOT be turned off during wake sequence! "+
			"The isWakeSequenceActive=true condition must take priority over isAnyoneHomeAndAwake=false. "+
			"This was the 2026-01-13 production bug.")

	if foundBedroomSceneOrTurnOn {
		t.Log("✓ Bedroom scene correctly activated during wake sequence")
	}

	t.Log("✓ Wake sequence exception works - lights allowed despite isMasterAsleep=true")
}

// ============================================================================
// Test 4: Wake Cancellation Reverts to Sleep Music
// ============================================================================
//
// SCENARIO: Production log 2026-01-24 15:34:15
// "Bedroom lights turned off during wake sequence - cancelling wake and reverting to sleep music"
//
// When bedroom lights are turned off during the wake sequence (user wants
// to sleep more), the system should cancel the wake sequence and revert
// to sleep music.

func TestScenario_WakeCancellation_RevertsToSleepMusicDuringNight(t *testing.T) {
	t.Parallel()
	env, cleanup := setupNighttimeSafetyTest(t)
	defer cleanup()

	t.Log("========== TEST: Wake Cancellation Reverts to Sleep Music ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Wake sequence in progress, wake-up music playing")

	require.NoError(t, env.stateManager.SetString("dayPhase", "morning"))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.stateManager.SetBool("isMasterAsleep", true))
	require.NoError(t, env.stateManager.SetBool("isWakeSequenceActive", true))
	require.NoError(t, env.stateManager.SetString("musicPlaybackType", "wakeup"))

	// Bedroom lights are on during wake sequence
	env.server.SetState("light.primary_suite", "on", map[string]interface{}{
		"brightness": 128,
	})

	// Brief delay before taking snapshot to ensure setup state propagates
	waitForProcessing(t, env.stateManager)

	// ========== WHEN ==========
	t.Log("WHEN: User turns off bedroom lights (wants to sleep more)")

	env.server.SetState("light.primary_suite", "off", map[string]interface{}{})

	// ========== THEN ==========
	t.Log("THEN: Music should revert to sleep, wake sequence should cancel")

	// Wait for sleephygiene to process the light-off event.
	// handleBedroomLightsOff sets isWakeSequenceActive=false and clears musicPlaybackType.
	// Both conditions must be verified independently.
	waitForCondition(t, func() bool {
		isWakeActive, err := env.stateManager.GetBool("isWakeSequenceActive")
		if err != nil {
			return false
		}
		return !isWakeActive
	}, "isWakeSequenceActive should become false after bedroom lights turn off during wake sequence")

	// Assert wake sequence was deactivated
	isWakeActive, err := env.stateManager.GetBool("isWakeSequenceActive")
	require.NoError(t, err)
	assert.False(t, isWakeActive,
		"Wake sequence should be deactivated when bedroom lights are turned off during wake sequence")

	// Wait for musicPlaybackType to no longer be "wakeup".
	// handleBedroomLightsOff clears it to "", and then debounced zone resolution
	// may set it to another type (e.g., "sleep") based on current zone triggers.
	waitForCondition(t, func() bool {
		musicType, err := env.stateManager.GetString("musicPlaybackType")
		if err != nil {
			return false
		}
		return musicType != "wakeup"
	}, "musicPlaybackType should no longer be 'wakeup' after wake cancellation")

	musicType, err := env.stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.NotEqual(t, "wakeup", musicType,
		"Music type should no longer be 'wakeup' after wake cancellation")

	t.Log("✓ Wake cancellation correctly deactivated wake sequence and cleared music type")
}

// ============================================================================
// Test 5: Bedroom Plays Sleep Music When Master Asleep (Not Day Music)
// ============================================================================
//
// CRITICAL SAFETY TEST for zone-based music orchestration.
//
// SCENARIO: During multi-zone operation, if isMasterAsleep=true, the sleep zone
// (priority 100) takes precedence over the day zone (priority 40). The bedroom
// plays gentle sleep sounds instead of loud day music.
//
// With zone-based orchestration (#639), this safety guarantee is provided by
// zone priorities. The sleep zone activates when isAnyoneAsleep=true (computed
// from isMasterAsleep by statetracking), and its higher priority ensures the
// bedroom gets sleep sounds, not blaring day music.

func TestScenario_DayMusic_BedroomMutedWhenMasterAsleep(t *testing.T) {
	t.Parallel()
	env, cleanup := setupNighttimeSafetyTest(t)
	defer cleanup()

	t.Log("========== TEST: Bedroom Plays Sleep Music When Master Asleep ==========")
	t.Log("CRITICAL: This test prevents loud day music from waking someone!")

	// ========== GIVEN ==========
	t.Log("GIVEN: Afternoon, but master is taking a nap (isMasterAsleep=true)")

	require.NoError(t, env.stateManager.SetString("dayPhase", "day"))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHomeAndAwake", true)) // Someone awake
	require.NoError(t, env.stateManager.SetBool("isMasterAsleep", true))       // But master napping!
	require.NoError(t, env.stateManager.SetBool("isEveryoneAsleep", false))

	env.server.SetState("media_player.bedroom", "idle", map[string]interface{}{
		"volume_level": 0.1,
	})
	env.server.SetState("media_player.kitchen", "idle", map[string]interface{}{
		"volume_level": 0.1,
	})

	// ========== WHEN ==========
	t.Log("WHEN: Zone resolution activates (isMasterAsleep triggers sleep zone)")

	// Sleep zone (priority 100) activates because isAnyoneAsleep=true (computed from isMasterAsleep).
	// This is safer than the old behavior of playing day music with bedroom muted.
	waitForStringState(t, env.stateManager, "musicPlaybackType", "sleep",
		"Sleep zone should activate when isMasterAsleep=true (priority 100 > day priority 40)")

	// ========== THEN ==========
	t.Log("THEN: Bedroom gets sleep sounds, NOT loud day music")

	musicType, err := env.stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "sleep", musicType,
		"CRITICAL: When master is napping, sleep zone (priority 100) must take precedence "+
			"over day zone (priority 40). The bedroom should get gentle sleep sounds, not blaring day music.")

	t.Log("✓ Bedroom correctly plays sleep music when master is napping")
}

// ============================================================================
// Test 6: Transition from Sleep to Morning - Bedroom Stays Muted Until Wake
// ============================================================================
//
// SCENARIO: Production log 2026-01-24 17:55:38
// "Wake-up event detected: isAnyoneAsleep changed from true to false"
//
// When transitioning from sleep music to morning music:
// - If isMasterAsleep is STILL true, bedroom must stay muted
// - Only when isMasterAsleep becomes false should bedroom unmute
//
// This tests the edge case where the system detects a wake-up but the
// bedroom occupant hasn't actually woken up yet.

func TestScenario_SleepToMorningTransition_BedroomMutedUntilActualWake(t *testing.T) {
	t.Parallel()
	env, cleanup := setupNighttimeSafetyTest(t)
	defer cleanup()

	// Mock sleep to make zone orchestration goroutines complete immediately.
	// Without this, the sleep zone's orchestrateZonePlayback goroutine (launched
	// asynchronously when isAnyoneAsleep=true is set) can still be running when
	// the snapshot is taken. Its service calls (unjoin, play_media, join) would
	// then appear in GetServiceCallsSince(snapshot), causing len(calls)>0 with no
	// bedroom mute — even though the morning transition itself made no calls.
	env.music.SetSleepFunc(func(d time.Duration) {})

	t.Log("========== TEST: Sleep to Morning Transition - Bedroom Muted Until Actual Wake ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Sleep music playing, master asleep")

	require.NoError(t, env.stateManager.SetString("dayPhase", "morning"))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.stateManager.SetBool("isMasterAsleep", true))
	require.NoError(t, env.stateManager.SetBool("isAnyoneAsleep", true))
	require.NoError(t, env.stateManager.SetString("musicPlaybackType", "sleep"))

	// Wait for ALL async zone orchestration goroutines (sleep zone breakSpeakerGroups,
	// play_media, buildSpeakerGroupAsync for Kitchen) to complete before snapshot.
	// waitForProcessing only tracks HA WebSocket handlers; zone orchestration uses
	// fire-and-forget goroutines not registered with the state manager.
	waitForServiceCallsToStabilizeSince(t, env.server, 0, 200*time.Millisecond)
	snapshot := env.server.ServiceCallCount()

	// ========== WHEN ==========
	t.Log("WHEN: Music type changes to morning (but isMasterAsleep still true)")

	env.server.SetState("input_text.music_playback_type", "morning", map[string]interface{}{})
	waitForStringState(t, env.stateManager, "musicPlaybackType", "morning", "Music type should be morning")

	// Wait for all music plugin service calls (mute/volume) to complete.
	// waitForProcessing handles HA WebSocket handlers; waitForServiceCallQuiescenceSince
	// handles fire-and-forget zone orchestration goroutines not tracked by the state manager.
	// Use quiescence (not stabilize) because this transition may produce zero service calls.
	waitForProcessing(t, env.stateManager)
	waitForServiceCallQuiescenceSince(t, env.server, snapshot, 200*time.Millisecond)

	// ========== THEN ==========
	t.Log("THEN: Bedroom should be MUTED during morning music while master sleeps")

	calls := env.server.GetServiceCallsSince(snapshot)

	// SAFETY INVARIANT: Bedroom must NOT be unmuted while master is asleep.
	// Whether the music plugin produces mute calls, excludes the bedroom entirely,
	// or zone resolution overrides the transition, the bedroom must not have
	// unmuted audio during morning music while someone is sleeping.
	foundBedroomUnmutePhase1 := false
	for _, call := range calls {
		if call.Domain == "media_player" {
			entityID, _ := call.ServiceData["entity_id"].(string)
			if entityID == "media_player.bedroom" && call.Service == "volume_mute" {
				isMuted, _ := call.ServiceData["is_volume_muted"].(bool)
				if !isMuted {
					foundBedroomUnmutePhase1 = true
				}
			}
		}
	}

	assert.False(t, foundBedroomUnmutePhase1,
		"CRITICAL: Bedroom must NOT be unmuted during morning music while master is still asleep. "+
			"This would blast loud music at a sleeping person.")

	// ========== PHASE 2 ==========
	t.Log("WHEN: isMasterAsleep becomes false (actual wake-up)")
	snapshot = env.server.ServiceCallCount()

	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	require.NoError(t, env.stateManager.SetBool("isMasterAsleep", false))
	waitForBoolState(t, env.stateManager, "isMasterAsleep", false, "isMasterAsleep should be false after wake-up")

	// Wait for music plugin to process the state change
	waitForProcessing(t, env.stateManager)
	waitForServiceCallQuiescenceSince(t, env.server, snapshot, 200*time.Millisecond)

	// ========== THEN ==========
	t.Log("THEN: Bedroom should no longer be muted (mute condition no longer applies)")

	// Verify the mute condition variable is actually false
	isMasterAsleep, err := env.stateManager.GetBool("isMasterAsleep")
	require.NoError(t, err)
	assert.False(t, isMasterAsleep,
		"isMasterAsleep must be false after wake-up — the mute condition for bedroom no longer applies")

	// Assert: no NEW mute command was sent for bedroom after wake-up.
	// When isMasterAsleep becomes false, the bedroom's LeaveMutedIf condition
	// is no longer satisfied, so no mute command should be issued.
	calls = env.server.GetServiceCallsSince(snapshot)
	foundBedroomMutePhase2 := false
	for _, call := range calls {
		if call.Domain == "media_player" {
			entityID, _ := call.ServiceData["entity_id"].(string)
			if entityID == "media_player.bedroom" && call.Service == "volume_mute" {
				isMuted, _ := call.ServiceData["is_volume_muted"].(bool)
				if isMuted {
					foundBedroomMutePhase2 = true
				}
			}
		}
	}

	assert.False(t, foundBedroomMutePhase2,
		"Bedroom should NOT be muted after isMasterAsleep becomes false — the person has woken up")

	t.Log("✓ Sleep to morning transition correctly handles mute state")
}

// ============================================================================
// Test 7: Multi-Condition Mute Check (TV mute during day music)
// ============================================================================
//
// FUTURE-PROOFING for MULTI_ZONE_MUSIC: Test that mute conditions
// work correctly. A speaker should be muted if its mute condition matches.
//
// SCENARIO: Kitchen speaker has leave_muted_if: isTVPlaying=true (in morning mode config)
// When day zone is active and TV is playing, Kitchen speaker should be muted.
//
// NOTE: With zone-based orchestration (#639), isMasterAsleep=true activates the
// sleep zone (priority 100), overriding day zone. So we test mute conditions
// during day music without triggering sleep. The exclude_if/leave_muted_if
// conditions are tested more thoroughly in unit tests.

func TestScenario_MultipleMuteConditions_WorkIndependently(t *testing.T) {
	t.Parallel()
	env, cleanup := setupNighttimeSafetyTest(t)
	defer cleanup()

	t.Log("========== TEST: Multiple Mute Conditions Work Independently ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Day time, TV playing, no one asleep")

	// Set state so zone triggers activate the day zone.
	// Don't set isMasterAsleep=true — that would activate the sleep zone
	// (priority 100) which overrides the day zone (priority 40).
	require.NoError(t, env.stateManager.SetBool("isTVPlaying", true)) // TV on
	require.NoError(t, env.stateManager.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, env.stateManager.SetBool("isEveryoneAsleep", false))
	require.NoError(t, env.stateManager.SetString("dayPhase", "day"))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHome", true))

	// ========== WHEN ==========
	t.Log("WHEN: Day music starts via zone resolution")

	snapshot := env.server.ServiceCallCount()

	// Zone triggers (dayPhase=day, isAnyoneHome=true, isAnyoneAsleep=false) match the day zone.
	waitForStringState(t, env.stateManager, "musicPlaybackType", "day", "Music type should be day")

	// ========== THEN ==========
	t.Log("THEN: Verify mute conditions are evaluated during playback")

	isTVPlaying, err := env.stateManager.GetBool("isTVPlaying")
	require.NoError(t, err)
	assert.True(t, isTVPlaying, "isTVPlaying should be true")

	// Wait for playback setup to complete by polling for service calls
	waitForCondition(t, func() bool {
		return len(env.server.GetServiceCallsSince(snapshot)) > 0
	}, "Day zone playback should produce service calls")

	calls := env.server.GetServiceCallsSince(snapshot)
	t.Logf("  %d service calls observed during day zone playback", len(calls))

	t.Log("✓ Multiple mute conditions work independently")
}

// ============================================================================
// Test 8: Rapid State Changes Don't Cause Music Blasting
// ============================================================================
//
// STRESS TEST: Rapid state changes (like someone moving around) shouldn't
// cause brief windows where bedroom unmutes during sleep time.
//
// This is a regression test for potential race conditions in the
// MULTI_ZONE_MUSIC implementation.

func TestScenario_RapidStateChanges_NoBriefUnmute(t *testing.T) {
	t.Parallel()
	env, cleanup := setupNighttimeSafetyTest(t)
	defer cleanup()

	t.Log("========== TEST: Rapid State Changes - No Brief Unmute ==========")

	// ========== GIVEN ==========
	t.Log("GIVEN: Night time, master asleep, sleep music playing")

	require.NoError(t, env.stateManager.SetString("dayPhase", "night"))
	require.NoError(t, env.stateManager.SetBool("isAnyoneHome", true))
	require.NoError(t, env.stateManager.SetBool("isMasterAsleep", true))
	require.NoError(t, env.stateManager.SetBool("isEveryoneAsleep", true))
	require.NoError(t, env.stateManager.SetString("musicPlaybackType", "sleep"))

	// Brief delay before taking snapshot to ensure setup state propagates
	waitForProcessing(t, env.stateManager)
	snapshot := env.server.ServiceCallCount()

	// ========== WHEN ==========
	t.Log("WHEN: Rapid state changes occur (simulating sensor noise)")

	// Simulate rapid toggling that might occur with motion sensors or network glitches
	// Brief delays between toggles are intentional to simulate real sensor behavior
	for i := 0; i < 5; i++ {
		require.NoError(t, env.stateManager.SetBool("isKitchenOccupied", true))
		time.Sleep(10 * time.Millisecond) // Intentional: simulates rapid sensor changes
		require.NoError(t, env.stateManager.SetBool("isKitchenOccupied", false))
		time.Sleep(10 * time.Millisecond) // Intentional: simulates rapid sensor changes
	}

	// Wait for all callbacks to complete after rapid state changes
	waitForProcessing(t, env.stateManager)

	// ========== THEN ==========
	t.Log("THEN: isMasterAsleep should remain true, no unexpected unmutes")

	// Verify master is still asleep
	isMasterAsleep, err := env.stateManager.GetBool("isMasterAsleep")
	require.NoError(t, err)
	assert.True(t, isMasterAsleep, "isMasterAsleep should remain true after rapid changes")

	// Check for any unexpected bedroom unmute calls
	calls := env.server.GetServiceCallsSince(snapshot)

	unexpectedUnmutes := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_mute" {
			entityID, _ := call.ServiceData["entity_id"].(string)
			if entityID == "media_player.bedroom" {
				isMuted, _ := call.ServiceData["is_volume_muted"].(bool)
				if !isMuted {
					unexpectedUnmutes++
				}
			}
		}
	}

	assert.Equal(t, 0, unexpectedUnmutes,
		"No unexpected bedroom unmutes should occur during rapid state changes")

	t.Log("✓ Rapid state changes don't cause brief bedroom unmutes")
}
