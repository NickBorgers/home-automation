package integration

import (
	"context"
	"testing"
	"time"

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
// Sleep-Prep → Sleep Seamless Transition Tests
//
// These tests validate that when transitioning from sleep-prep to sleep zone,
// shared speakers (Bedroom, Kitchen, Primary Bathroom) continue playing
// without interruption, while non-shared speakers (Sitting Room, Front Room)
// are correctly faded out and removed from the group.
//
// PRODUCTION BUG (2026-03-03, Issue #767):
// When isMasterAsleep triggered the sleep zone, the music plugin did a full
// tear-down and rebuild: fade-out all speakers, break all groups, start new
// playlist from scratch, fade back in from zero. For shared speakers, this
// created a ~12-second silence gap followed by an 8+ minute fade-in — very
// disruptive when someone is falling asleep to rain sounds.
//
// INVARIANTS:
// - Shared speakers (in both zones) must NOT be unjoined, re-joined, or have
//   playback restarted during the transition
// - Shared speakers should smoothly adjust volume to the new zone's target
// - Non-shared speakers (only in sleep-prep) must be faded out and unjoined
// - Sleep zone must become active and sleep-prep must stop
// ============================================================================

// sleepPrepTransitionEnv holds plugins for sleep-prep → sleep transition tests
type sleepPrepTransitionEnv struct {
	server        *MockHAServer
	stateManager  *state.Manager
	logger        *zap.Logger
	stateTracking *statetracking.Manager
	music         *music.Manager
	sleepHygiene  *sleephygiene.Manager
}

func setupSleepPrepTransitionTest(t *testing.T) (*sleepPrepTransitionEnv, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	logger := testlogger.New()
	musicConfig := loadTestMusicConfigFromYAML(t)
	configLoader := config.NewLoader("../../configs", logger)

	env := &sleepPrepTransitionEnv{
		server:        server,
		stateManager:  stateManager,
		logger:        logger,
		stateTracking: statetracking.NewManager(context.Background(), client, stateManager, logger, false, nil),
		music:         music.NewManager(context.Background(), client, stateManager, musicConfig, logger, false, nil, nil),
		sleepHygiene:  sleephygiene.NewManager(context.Background(), client, stateManager, configLoader, logger, false, nil, nil),
	}

	// Skip real delays in music plugin
	env.music.SetSleepFunc(func(d time.Duration) {})

	// Enable debouncing to coalesce rapid state changes from sleephygiene plugin.
	// When isMasterAsleep changes, sleephygiene also sets isAnyoneAsleep, isEveryoneAsleep, etc.
	// Without debouncing, each fires a separate zone resolution, and concurrent resolutions
	// can race during the seamless transition, causing the second to fall back to stop/start.
	// Production uses 500ms; we match it here for realistic behavior.
	env.music.SetDebounceDelay(500 * time.Millisecond)

	// Set up all 5 media player entities used in sleep-prep and sleep zones
	server.SetState("media_player.bedroom", "playing", map[string]interface{}{
		"friendly_name": "Bedroom",
		"volume_level":  0.20, // base 16 × 1.3 = 20.8 → 20
	})
	server.SetState("media_player.kitchen", "playing", map[string]interface{}{
		"friendly_name": "Kitchen",
		"volume_level":  0.20,
	})
	server.SetState("media_player.primary_bathroom", "playing", map[string]interface{}{
		"friendly_name": "Primary Bathroom",
		"volume_level":  0.20,
	})
	server.SetState("media_player.sitting_room", "playing", map[string]interface{}{
		"friendly_name": "Sitting Room",
		"volume_level":  0.11, // base 9 × 1.3 = 11.7 → 11
	})
	server.SetState("media_player.front_room", "playing", map[string]interface{}{
		"friendly_name": "Front Room",
		"volume_level":  0.11,
	})

	// Start plugins in dependency order
	require.NoError(t, env.stateTracking.Start(), "Failed to start state tracking")
	require.NoError(t, env.sleepHygiene.Start(), "Failed to start sleep hygiene")
	require.NoError(t, env.music.Start(), "Failed to start music")

	waitForProcessing(t, stateManager)

	cleanup := func() {
		env.music.Stop()
		env.sleepHygiene.Stop()
		env.stateTracking.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// ============================================================================
// Test: Non-bedroom speakers are correctly removed during sleep-prep → sleep
// ============================================================================

// TestScenario_SleepPrepToSleep_NonBedroomSpeakersBehavior validates that when
// transitioning from sleep-prep to sleep, speakers only in sleep-prep (Sitting Room,
// Front Room) are correctly faded out and removed, while shared speakers (Bedroom,
// Kitchen, Primary Bathroom) continue playing without interruption.
//
// User story: "When I go to bed, the rain sounds in my bedroom should continue
// seamlessly. The living room speakers should fade out, but my bedroom shouldn't
// skip a beat."
//
// INVARIANTS:
// - Sitting Room and Front Room: fade-out to 0 + unjoin
// - Bedroom, Kitchen, Primary Bathroom: NO unjoin, NO play_media restart
// - Shared speakers get smooth volume adjustment to new zone's target
func TestScenario_SleepPrepToSleep_NonBedroomSpeakersBehavior(t *testing.T) {
	t.Parallel()
	env, cleanup := setupSleepPrepTransitionTest(t)
	defer cleanup()

	// ===== GIVEN: Sleep-prep zone is active with all 5 speakers playing rain =====
	t.Log("GIVEN: Sleep-prep zone is active with all 5 speakers playing rain")

	// Set up initial state that triggers sleep-prep zone
	// sleep-prep triggers: dayPhase=night, isAnyoneHome=true, isMasterAsleep=false
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "night", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})

	waitForProcessing(t, env.stateManager)

	// Wait for sleep-prep zone to fully activate (with playlist URI set)
	// Using polling instead of fixed sleep to be robust under CPU contention
	waitForCondition(t, func() bool {
		zones := env.music.GetActiveZones()
		for _, z := range zones {
			if z.Name == "sleep-prep" && z.PlaylistURI != "" {
				return true
			}
		}
		return false
	}, "sleep-prep zone should be active with a playlist URI")

	activeZones := env.music.GetActiveZones()
	zoneNames := getZoneNames(activeZones)
	require.Contains(t, zoneNames, "sleep-prep",
		"Expected sleep-prep zone to be active, got: %v", zoneNames)

	var sleepPrepZone *music.Zone
	for _, z := range activeZones {
		if z.Name == "sleep-prep" {
			sleepPrepZone = z
			break
		}
	}
	require.NotNil(t, sleepPrepZone, "Sleep-prep zone should exist")

	t.Logf("Sleep-prep active with %d participants, URI: %s",
		len(sleepPrepZone.Participants), sleepPrepZone.PlaylistURI)

	// Wait for orchestration goroutines from startZone to fully complete.
	// startZone launches orchestrateZonePlayback in a goroutine, which spawns
	// sub-goroutines (buildSpeakerGroupAsync, fadeInSpeaker). Even with no-op
	// sleepFunc, these make service calls to the mock server. We must wait for
	// ALL calls to finish before ClearServiceCalls(), otherwise they leak into
	// the THEN phase assertions.
	//
	// Strategy: wait until service call count stabilizes (no new calls for 200ms).
	waitForServiceCallsToStabilize(t, env.server, 200*time.Millisecond)

	// Clear service calls to isolate the transition
	env.server.ClearServiceCalls()

	// ===== WHEN: isMasterAsleep becomes true (triggering sleep zone) =====
	t.Log("WHEN: isMasterAsleep becomes true (triggering sleep zone)")

	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})

	waitForProcessing(t, env.stateManager)

	// Wait for sleep zone to become active and sleep-prep to stop
	// This handles the case where sleephygiene fires additional state changes
	// (isAnyoneAsleep, isEveryoneAsleep) that cause secondary zone resolutions
	waitForCondition(t, func() bool {
		zones := env.music.GetActiveZones()
		hasSleep := false
		hasSleepPrep := false
		for _, z := range zones {
			if z.Name == "sleep" {
				hasSleep = true
			}
			if z.Name == "sleep-prep" {
				hasSleepPrep = true
			}
		}
		return hasSleep && !hasSleepPrep
	}, "sleep zone should be active and sleep-prep should be stopped")

	// Wait for all service calls to settle (secondary zone resolutions
	// from sleephygiene state changes may still be completing)
	waitForServiceCallsToStabilize(t, env.server, 300*time.Millisecond)

	// ===== THEN: Verify correct transition behavior =====
	t.Log("THEN: Verify transition behavior for all speakers")

	// 1. Sleep zone should be active, sleep-prep should not
	activeZones = env.music.GetActiveZones()
	zoneNames = getZoneNames(activeZones)
	assert.Contains(t, zoneNames, "sleep",
		"Sleep zone should be active after isMasterAsleep=true")
	assert.NotContains(t, zoneNames, "sleep-prep",
		"Sleep-prep zone should stop after isMasterAsleep=true")

	calls := env.server.GetServiceCalls()
	t.Logf("Total service calls during transition: %d", len(calls))
	for i, call := range calls {
		if i >= 30 {
			t.Logf("  ... and %d more calls", len(calls)-30)
			break
		}
		t.Logf("  Call %d: %s.%s entity=%v vol=%v",
			i, call.Domain, call.Service,
			call.ServiceData["entity_id"], call.ServiceData["volume_level"])
	}

	// 2. Sitting Room should receive fade-out (volume_set to 0) and unjoin
	sittingRoomFadeOut := false
	sittingRoomUnjoin := false
	for _, call := range calls {
		entityID, _ := call.ServiceData["entity_id"].(string)
		if entityID != "media_player.sitting_room" {
			continue
		}
		if call.Domain == "media_player" && call.Service == "volume_set" {
			vol, _ := call.ServiceData["volume_level"].(float64)
			if vol == 0.0 {
				sittingRoomFadeOut = true
			}
		}
		if call.Domain == "media_player" && call.Service == "unjoin" {
			sittingRoomUnjoin = true
		}
	}
	assert.True(t, sittingRoomFadeOut,
		"Sitting Room should receive fade-out to 0")
	assert.True(t, sittingRoomUnjoin,
		"Sitting Room should be unjoined from speaker group")

	// 3. Front Room should receive fade-out (volume_set to 0) and unjoin
	frontRoomFadeOut := false
	frontRoomUnjoin := false
	for _, call := range calls {
		entityID, _ := call.ServiceData["entity_id"].(string)
		if entityID != "media_player.front_room" {
			continue
		}
		if call.Domain == "media_player" && call.Service == "volume_set" {
			vol, _ := call.ServiceData["volume_level"].(float64)
			if vol == 0.0 {
				frontRoomFadeOut = true
			}
		}
		if call.Domain == "media_player" && call.Service == "unjoin" {
			frontRoomUnjoin = true
		}
	}
	assert.True(t, frontRoomFadeOut,
		"Front Room should receive fade-out to 0")
	assert.True(t, frontRoomUnjoin,
		"Front Room should be unjoined from speaker group")

	// 4. Shared speakers should NOT be torn down (no unjoin, no play_media restart)
	sharedSpeakers := map[string]string{
		"media_player.bedroom":          "Bedroom",
		"media_player.kitchen":          "Kitchen",
		"media_player.primary_bathroom": "Primary Bathroom",
	}
	for _, call := range calls {
		entityID, _ := call.ServiceData["entity_id"].(string)
		speakerName, isShared := sharedSpeakers[entityID]
		if !isShared {
			continue
		}

		// No unjoin for shared speakers
		if call.Domain == "media_player" && call.Service == "unjoin" {
			t.Errorf("Shared speaker %s (%s) should NOT receive unjoin during seamless transition",
				speakerName, entityID)
		}

		// No play_media for shared speakers (no playback restart)
		if call.Domain == "media_player" && call.Service == "play_media" {
			t.Errorf("Shared speaker %s (%s) should NOT receive play_media during seamless transition",
				speakerName, entityID)
		}
	}

	// 5. Shared speakers should NOT appear in any join calls
	// (join entity_id is the lead, group_members are followers being joined)
	for _, call := range calls {
		if call.Domain != "media_player" || call.Service != "join" {
			continue
		}
		entityID, _ := call.ServiceData["entity_id"].(string)
		if _, isShared := sharedSpeakers[entityID]; isShared {
			t.Errorf("Shared speaker %s should NOT be the target of a join call during seamless transition",
				entityID)
		}
		// Check group_members field (may be []string or []interface{})
		if members, ok := call.ServiceData["group_members"].([]string); ok {
			for _, member := range members {
				if _, isShared := sharedSpeakers[member]; isShared {
					t.Errorf("Shared speaker %s should NOT be re-joined during seamless transition", member)
				}
			}
		}
		if members, ok := call.ServiceData["group_members"].([]interface{}); ok {
			for _, member := range members {
				if memberStr, ok := member.(string); ok {
					if _, isShared := sharedSpeakers[memberStr]; isShared {
						t.Errorf("Shared speaker %s should NOT be re-joined during seamless transition", memberStr)
					}
				}
			}
		}
	}

	// 6. Shared speakers should NOT have volume set to 0 (no fade-out)
	for _, call := range calls {
		entityID, _ := call.ServiceData["entity_id"].(string)
		speakerName, isShared := sharedSpeakers[entityID]
		if !isShared {
			continue
		}
		if call.Domain == "media_player" && call.Service == "volume_set" {
			vol, _ := call.ServiceData["volume_level"].(float64)
			if vol == 0.0 {
				t.Errorf("Shared speaker %s (%s) should NOT be faded out to 0 during seamless transition",
					speakerName, entityID)
			}
		}
	}

	// 7. Verify volume adjustment for shared speakers
	// Sleep-prep used multiplier 1.3 (vol 20), sleep uses 1.45 (vol 23)
	// So shared speakers should get volume_set calls with values > 0
	for speakerEntityID, speakerName := range sharedSpeakers {
		hasVolumeAdjust := false
		for _, call := range calls {
			callEntityID, _ := call.ServiceData["entity_id"].(string)
			if callEntityID == speakerEntityID &&
				call.Domain == "media_player" && call.Service == "volume_set" {
				vol, _ := call.ServiceData["volume_level"].(float64)
				if vol > 0 {
					hasVolumeAdjust = true
				}
			}
		}
		assert.True(t, hasVolumeAdjust,
			"Shared speaker %s (%s) should receive volume adjustment during seamless transition",
			speakerName, speakerEntityID)
	}

	t.Log("SUCCESS: Sleep-prep → sleep transition handled shared speakers seamlessly")
}
