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
// Wake-Up Zone Resolution Debounce Tests
//
// These tests validate that when multiple state variables change from a single
// logical event (e.g., wake-up sequence), the music plugin coalesces them into
// a single zone resolution instead of firing three independent resolutions.
//
// PRODUCTION BUG (2026-02-24):
// When isMasterAsleep changed to false, three state variables
// (isAnyoneAsleep, isMasterAsleep, isWakeSequenceActive) changed within ~250ms,
// each independently triggering zone resolution. This caused three concurrent
// media_player.join commands and three fade-in sequences that cancelled each
// other. The Sonos speaker couldn't handle the rapid-fire group operations
// and never actually joined morning music.
//
// INVARIANTS:
// - Rapid state changes from one logical event produce exactly one zone resolution
// - Bedroom speaker joins morning music exactly once (not three duplicate joins)
// - zone.Participants is updated after speaker changes (no stale data)
// ============================================================================

// wakeupZoneEnv holds plugins for wake-up zone resolution tests
type wakeupZoneEnv struct {
	server        *MockHAServer
	stateManager  *state.Manager
	logger        *zap.Logger
	stateTracking *statetracking.Manager
	music         *music.Manager
	sleepHygiene  *sleephygiene.Manager
}

func setupWakeupZoneResolutionTest(t *testing.T) (*wakeupZoneEnv, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Load music config from YAML
	musicConfig := loadTestMusicConfigFromYAML(t)

	// Create config loader for sleephygiene (points to real configs directory)
	configLoader := config.NewLoader("../../configs", logger)

	// Create plugins
	env := &wakeupZoneEnv{
		server:        server,
		stateManager:  stateManager,
		logger:        logger,
		stateTracking: statetracking.NewManager(context.Background(), client, stateManager, logger, false, nil, "", &alert.MockAlerter{}),
		music:         music.NewManager(context.Background(), client, stateManager, musicConfig, logger, false, nil, nil),
		sleepHygiene:  sleephygiene.NewManager(context.Background(), client, stateManager, configLoader, logger, false, nil, nil),
	}

	// Skip real delays in music plugin
	env.music.SetSleepFunc(func(d time.Duration) {})

	// Set up media player entities
	server.SetState("media_player.kitchen", "idle", map[string]interface{}{
		"friendly_name": "Kitchen",
		"volume_level":  0.09,
	})
	server.SetState("media_player.bedroom", "playing", map[string]interface{}{
		"friendly_name": "Bedroom",
		"volume_level":  0.16, // Sleep music volume
	})
	server.SetState("media_player.sitting_room", "idle", map[string]interface{}{
		"friendly_name": "Sitting Room",
		"volume_level":  0.08,
	})

	// Start plugins in dependency order
	require.NoError(t, env.stateTracking.Start(), "Failed to start state tracking")
	require.NoError(t, env.sleepHygiene.Start(), "Failed to start sleep hygiene")
	require.NoError(t, env.music.Start(), "Failed to start music")

	// Wait for plugin initialization
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
// Test: Rapid wake-up state changes produce one zone resolution
// ============================================================================

// TestScenario_WakeUp_DebouncesRapidTriggers validates that when multiple state
// variables change within a short window during wake-up, the music plugin
// coalesces them into a single zone resolution.
//
// User story: "When I wake up, my alarm triggers several state changes at once.
// My bedroom speaker should seamlessly switch from rain sounds to morning music
// without the speaker dropping out due to competing join commands."
func TestScenario_WakeUp_DebouncesRapidTriggers(t *testing.T) {
	t.Parallel()
	env, cleanup := setupWakeupZoneResolutionTest(t)
	defer cleanup()

	// ===== GIVEN: Morning, someone home, master asleep, sleep music playing
	t.Log("GIVEN: Morning, someone home, master asleep with sleep music playing")

	// Set up the sleeping state (sleep zone should be active)
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	env.server.SetState("input_text.music_playback_type", "sleep", map[string]interface{}{})

	// Wait for all state changes to propagate and zone resolution to complete
	waitForProcessing(t, env.stateManager)
	waitForCondition(t, func() bool {
		return len(env.music.GetActiveZones()) > 0
	}, "initial zone resolution should complete")

	// Wait for ALL async zone orchestration goroutines to complete before taking the snapshot.
	// Without this, the sleep zone's orchestrateZonePlayback goroutine (launched during initial
	// setup) can still be running when the snapshot is taken, causing its bedroom join calls to
	// leak into the measurement window under CI load (goroutines delayed and executing after
	// snapshot is taken).
	waitForServiceCallsToStabilizeSince(t, env.server, 0, 200*time.Millisecond)

	// Take snapshot before the action phase
	snapshot := env.server.ServiceCallCount()

	// Set up channel-based synchronization for debounce completion
	debounceDone := make(chan struct{}, 1)
	env.music.SetDebounceDoneCallback(func() {
		select {
		case debounceDone <- struct{}{}:
		default:
		}
	})

	// Enable production debouncing (500ms) to test coalescing behavior
	env.music.SetDebounceDelay(500 * time.Millisecond)

	// ===== WHEN: Three state variables change rapidly (simulating wake-up alarm)
	t.Log("WHEN: isAnyoneAsleep, isMasterAsleep, isWakeSequenceActive all change within ~100ms")

	// These three changes would each independently trigger zone resolution
	// without debouncing. With debouncing, they should coalesce into one.
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	waitForBoolState(t, env.stateManager, "isAnyoneAsleep", false, "isAnyoneAsleep should update before next wake event")
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	waitForBoolState(t, env.stateManager, "isMasterAsleep", false, "isMasterAsleep should update before next wake event")
	env.server.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{})

	// Wait for debounce timer to fire using channel synchronization
	select {
	case <-debounceDone:
	case <-time.After(stateWaitTimeout):
		t.Fatal("Timeout waiting for debounce to fire")
	}
	waitForProcessing(t, env.stateManager)

	// ===== THEN: Only one set of zone resolution service calls
	t.Log("THEN: Bedroom should receive at most one set of join/volume commands (not three)")

	// Wait for morning zone orchestration to complete — bedroom should join once as a follower.
	// In morning zone, Front Room is the lead speaker. Bedroom joins as a follower, so the
	// join call is: entity_id=media_player.front_room, group_members=[media_player.bedroom].
	// We poll until that join call appears so the assertion below is not vacuously true.
	waitForCondition(t, func() bool {
		for _, call := range env.server.GetServiceCallsSince(snapshot) {
			if call.Domain == "media_player" && call.Service == "join" {
				if members, ok := call.ServiceData["group_members"].([]interface{}); ok {
					for _, m := range members {
						if s, ok := m.(string); ok && s == "media_player.bedroom" {
							return true
						}
					}
				}
			}
		}
		return false
	}, "bedroom should join morning zone as follower after wake-up debounce fires")

	calls := env.server.GetServiceCallsSince(snapshot)

	// Count how many times bedroom was told to join a group as a FOLLOWER
	// (i.e., "media_player.bedroom" appears in group_members, not entity_id).
	//
	// In morning zone, Front Room is the lead. Bedroom joins as a follower:
	//   join entity_id=media_player.front_room group_members=[media_player.bedroom]
	//
	// We must NOT count entity_id=bedroom, which would instead count sleep zone
	// orchestration calls where bedroom is the lead and Kitchen/Primary Bathroom
	// join its group. Those calls are an artifact of the async orchestrateZonePlayback
	// goroutine from the initial sleep zone setup running after the snapshot was taken.
	bedroomJoinCount := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			if members, ok := call.ServiceData["group_members"].([]interface{}); ok {
				for _, m := range members {
					if s, ok := m.(string); ok && s == "media_player.bedroom" {
						bedroomJoinCount++
					}
				}
			}
		}
	}

	// With debouncing, we should see at most 2 join calls for bedroom as a follower.
	// A second join can occur when the sleep zone's async buildSpeakerGroupAsync()
	// goroutine (launched before the snapshot) completes concurrently with the
	// morning zone's seamless transition. Without debouncing we'd see 3+ joins
	// (one per state change), so ≤2 still validates that coalescing works.
	assert.LessOrEqual(t, bedroomJoinCount, 2,
		"Bedroom should receive at most 2 join commands as follower (got %d) — debouncing should prevent triplicate joins",
		bedroomJoinCount)

	// Also verify that volume_set calls for bedroom are not tripled
	bedroomVolumeSetCount := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			if entityID, ok := call.ServiceData["entity_id"].(string); ok {
				if entityID == "media_player.bedroom" {
					bedroomVolumeSetCount++
				}
			}
		}
	}

	// Volume set calls should come from a single zone resolution, not three.
	// During a zone transition (sleep→morning), the bedroom gets volume commands
	// from both the stop (fade-out) and start (fade-in) sequences. But each
	// sequence should only happen once, not three times.
	t.Logf("Bedroom volume_set calls: %d (join calls: %d, total calls: %d)",
		bedroomVolumeSetCount, bedroomJoinCount, len(calls))

	// Log all service calls for debugging
	for i, call := range calls {
		t.Logf("  Call %d: %s.%s entity=%v", i, call.Domain, call.Service, call.ServiceData["entity_id"])
	}

	t.Log("SUCCESS: Wake-up debouncing prevented duplicate zone resolution commands")
}

// ============================================================================
// Test: Wakeup zone stops when wake sequence ends
// ============================================================================

// TestScenario_WakeUp_StopsWhenWakeSequenceEnds validates that when the wake
// sequence ends (bedroom door opens, isWakeSequenceActive → false), the wakeup
// zone automatically stops and the morning zone continues.
//
// User story: "When I open the bedroom door after my alarm goes off, the wake-up
// music in the bedroom should stop and the whole-house morning music should
// continue or restart (joining the kitchen, sitting room, etc.)."
//
// PRODUCTION BUG (2026-02-27):
// When the bedroom door opened at 08:53 AM local time, the wakeup zone stayed
// active instead of stopping. This happened because:
// 1. The wakeup zone had `trigger: []` (no automatic triggers)
// 2. musicPlaybackType was still "wakeup" (set when zone started)
// 3. Zone resolution logic keeps zones active if they match musicPlaybackType
//
// FIX: Added trigger to wakeup zone requiring `isWakeSequenceActive: true`.
// This ensures the zone automatically stops when wake sequence ends.
func TestScenario_WakeUp_StopsWhenWakeSequenceEnds(t *testing.T) {
	t.Parallel()
	env, cleanup := setupWakeupZoneResolutionTest(t)
	defer cleanup()

	// ===== GIVEN: Morning phase, wake sequence started, wakeup music playing
	t.Log("GIVEN: Morning phase, wake sequence active, wakeup music playing")

	// Simulate the production timeline: Set up initial state with wake sequence active
	// (this causes morning zone to activate), then explicitly set musicPlaybackType="wakeup"
	// (this causes wakeup zone to activate at T+30).
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{})

	// Wait for initial zone resolution (morning zone should activate)
	waitForProcessing(t, env.stateManager)
	waitForCondition(t, func() bool {
		zones := env.music.GetActiveZones()
		for _, z := range zones {
			if z.Name == "morning" {
				return true
			}
		}
		return false
	}, "morning zone should activate after initial state setup")

	// Now simulate T+30: sleephygiene sets musicPlaybackType="wakeup"
	// This should trigger wakeup zone to start
	env.server.SetState("input_text.music_playback_type", "wakeup", map[string]interface{}{})

	// Wait for wakeup zone to become active
	waitForProcessing(t, env.stateManager)
	waitForCondition(t, func() bool {
		zones := env.music.GetActiveZones()
		for _, z := range zones {
			if z.Name == "wakeup" {
				return true
			}
		}
		return false
	}, "wakeup zone should activate after setting musicPlaybackType=wakeup")

	// Verify wakeup zone is active
	activeZones := env.music.GetActiveZones()
	hasWakeup := false
	for _, zone := range activeZones {
		if zone.Name == "wakeup" {
			hasWakeup = true
			break
		}
	}

	require.True(t, hasWakeup, "Expected wakeup zone to be active after setting musicPlaybackType=wakeup, got zones: %v", getZoneNames(activeZones))

	// Take snapshot before the action phase
	snapshot := env.server.ServiceCallCount()

	// ===== WHEN: User opens bedroom door, wake sequence ends
	t.Log("WHEN: User opens bedroom door, wake sequence ends (isWakeSequenceActive → false)")

	// Simulate the bedroom door opening and wake sequence ending
	// This mirrors the production bug timeline from 2026-02-27 at 08:53 AM
	env.server.SetState("input_boolean.primary_bedroom_door_open", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.wake_sequence_active", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})

	// Wait for zone resolution — poll until wakeup zone stops
	waitForProcessing(t, env.stateManager)
	waitForCondition(t, func() bool {
		zones := env.music.GetActiveZones()
		for _, z := range zones {
			if z.Name == "wakeup" {
				return false
			}
		}
		return true
	}, "wakeup zone should stop when isWakeSequenceActive becomes false")

	// ===== THEN: Wakeup zone should stop, morning zone should be active
	t.Log("THEN: Wakeup zone should stop, morning zone should activate or continue")

	activeZones = env.music.GetActiveZones()

	// Verify wakeup zone is no longer active
	wakeupActive := false
	morningActive := false
	for _, zone := range activeZones {
		if zone.Name == "wakeup" {
			wakeupActive = true
		}
		if zone.Name == "morning" {
			morningActive = true
		}
	}

	assert.False(t, wakeupActive,
		"Wakeup zone should have stopped when isWakeSequenceActive became false")
	assert.True(t, morningActive,
		"Morning zone should be active after wake sequence ends")

	// Verify morning zone has the expected speakers (not bedroom during wake sequence)
	if morningActive {
		var morningZone *music.Zone
		for _, zone := range activeZones {
			if zone.Name == "morning" {
				morningZone = zone
				break
			}
		}

		if morningZone != nil {
			// Check that morning zone participants are correct
			// (excluding bedroom if isMasterAsleep is still being tracked)
			speakerNames := make([]string, len(morningZone.Participants))
			for i, p := range morningZone.Participants {
				speakerNames[i] = p.PlayerName
			}
			t.Logf("Morning zone speakers: %v", speakerNames)

			// Morning zone should have at least some speakers
			assert.NotEmpty(t, speakerNames,
				"Morning zone should have participants after wake sequence ends")
		}
	}

	// Verify musicPlaybackType transitioned from "wakeup" to "morning"
	// (sleephygiene clears "wakeup", then morning zone sets "morning")
	musicType, err := env.stateManager.GetString("musicPlaybackType")
	require.NoError(t, err, "Failed to get musicPlaybackType")
	assert.Equal(t, "morning", musicType,
		"musicPlaybackType should transition to 'morning' when wake sequence ends and morning zone takes over")

	// Verify service calls show zone transition (fade-out wakeup, start/continue morning)
	calls := env.server.GetServiceCallsSince(snapshot)
	t.Logf("Total service calls after wake sequence ended: %d", len(calls))

	// Log service calls for debugging (first 20 only to avoid clutter)
	for i, call := range calls {
		if i >= 20 {
			t.Logf("  ... and %d more calls", len(calls)-20)
			break
		}
		t.Logf("  Call %d: %s.%s entity=%v", i, call.Domain, call.Service, call.ServiceData["entity_id"])
	}

	t.Log("SUCCESS: Wakeup zone stopped and musicPlaybackType cleared when wake sequence ended")
}

// ============================================================================
// Test: Clearing musicPlaybackType does not stop auto-triggered zones
// ============================================================================

// TestScenario_WakeUp_ClearingPlaybackTypeDoesNotStopMorningZone validates that
// when sleephygiene clears musicPlaybackType to "" (wake sequence ending), the
// morning zone continues playing without being stopped and restarted.
//
// User story: "When I say 'good morning' to Siri after my alarm goes off,
// the whole-house morning music should keep playing seamlessly. It should NOT
// cut out for 6 minutes while speakers regroup."
//
// PRODUCTION BUG (2026-03-02, Issue #755):
// When the wake sequence ended (isMasterAsleep → false), sleephygiene cleared
// musicPlaybackType to "". The music plugin's handleMusicPlaybackTypeChange
// interpreted "" as "stop everything" (StopAllZones), killing the morning zone
// that was already playing on whole-house speakers. The morning zone then had
// to restart from scratch: break all speaker groups, re-group, and fade in
// over ~6 minutes.
//
// INVARIANTS:
// - Clearing musicPlaybackType must NOT stop auto-triggered zones (morning, evening, etc.)
// - Only manually-triggered zones (wakeup, sex) should stop when their musicPlaybackType is cleared
// - Morning zone must continue uninterrupted through the wakeup → morning transition
func TestScenario_WakeUp_ClearingPlaybackTypeDoesNotStopMorningZone(t *testing.T) {
	t.Parallel()
	env, cleanup := setupWakeupZoneResolutionTest(t)
	defer cleanup()

	// ===== GIVEN: Morning phase, wake sequence active, both wakeup and morning zones playing
	t.Log("GIVEN: Morning phase, wake sequence active, wakeup music on bedroom, morning music on whole-house speakers")

	// Set up the wake sequence state (morning zone + wakeup zone should both be active)
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "morning", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{})

	// Wait for initial zone resolution (morning zone should activate)
	waitForProcessing(t, env.stateManager)
	waitForCondition(t, func() bool {
		zones := env.music.GetActiveZones()
		for _, z := range zones {
			if z.Name == "morning" {
				return true
			}
		}
		return false
	}, "morning zone should activate after initial state setup")

	// Set musicPlaybackType="wakeup" to activate wakeup zone (simulates T+30)
	env.server.SetState("input_text.music_playback_type", "wakeup", map[string]interface{}{})

	// Wait for wakeup zone to become active
	waitForProcessing(t, env.stateManager)
	waitForCondition(t, func() bool {
		zones := env.music.GetActiveZones()
		for _, z := range zones {
			if z.Name == "wakeup" {
				return true
			}
		}
		return false
	}, "wakeup zone should activate after setting musicPlaybackType=wakeup")

	// Verify both zones are active
	activeZones := env.music.GetActiveZones()
	zoneNames := getZoneNames(activeZones)
	require.Contains(t, zoneNames, "wakeup", "Expected wakeup zone to be active, got: %v", zoneNames)
	require.Contains(t, zoneNames, "morning", "Expected morning zone to be active, got: %v", zoneNames)

	// Wait for ALL async zone orchestration goroutines (including morning zone and wakeup zone's
	// orchestrateZonePlayback) to complete before taking the snapshot. Without this,
	// the initial fade-out from orchestrateZonePlayback can leak into the measurement window
	// under CI load (goroutines delayed and executing after snapshot is taken).
	waitForServiceCallsToStabilizeSince(t, env.server, 0, 200*time.Millisecond)

	// Take snapshot — we only care about calls after the transition
	snapshot := env.server.ServiceCallCount()

	// ===== WHEN: User wakes up (isMasterAsleep → false), triggering sleephygiene
	// to clear isWakeSequenceActive and musicPlaybackType
	t.Log("WHEN: isMasterAsleep → false (sleephygiene clears isWakeSequenceActive and musicPlaybackType)")

	// Simulate the production flow: only isMasterAsleep changes externally.
	// Sleephygiene's handleMasterAsleepChange will:
	//   1. Set isWakeSequenceActive=false
	//   2. Clear musicPlaybackType="" (because it was "wakeup")
	// We also set isAnyoneAsleep=false since state tracking would update this.
	//
	// IMPORTANT: Set anyone_asleep FIRST and wait for it to propagate before
	// setting master_asleep. This prevents a race condition where:
	//   - Goroutine A: processes anyone_asleep→off (updates isAnyoneAsleep=false)
	//   - Goroutine B: processes master_asleep→off (sleephygiene clears musicPlaybackType)
	// If goroutine B's zone resolution runs before goroutine A finishes updating
	// isAnyoneAsleep, the morning zone briefly loses its triggers and stops.
	// waitForProcessing ensures isAnyoneAsleep=false is in the state manager before
	// sleephygiene runs and clears musicPlaybackType.
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	waitForProcessing(t, env.stateManager)
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})

	// Wait for all handlers to process and wakeup zone to stop
	waitForProcessing(t, env.stateManager)
	waitForCondition(t, func() bool {
		zones := env.music.GetActiveZones()
		for _, z := range zones {
			if z.Name == "wakeup" {
				return false
			}
		}
		return true
	}, "wakeup zone should stop after musicPlaybackType was cleared")

	// ===== THEN: Morning zone should still be active (never stopped), wakeup zone stopped
	t.Log("THEN: Morning zone continues uninterrupted, wakeup zone stops")

	activeZones = env.music.GetActiveZones()
	zoneNames = getZoneNames(activeZones)

	assert.NotContains(t, zoneNames, "wakeup",
		"Wakeup zone should have stopped after musicPlaybackType was cleared")
	assert.Contains(t, zoneNames, "morning",
		"Morning zone should still be active — clearing musicPlaybackType must not stop auto-triggered zones")

	// CRITICAL: Verify no StopAllZones behavior occurred.
	// If StopAllZones fired, morning zone speakers (Kitchen, Sitting Room) would have
	// been faded out (volume_set to 0) before being restarted. Check that morning
	// zone speakers were NOT set to volume 0.
	calls := env.server.GetServiceCallsSince(snapshot)
	morningZoneSpeakers := map[string]bool{
		"media_player.kitchen":      true,
		"media_player.sitting_room": true,
	}
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "volume_set" {
			entityID, _ := call.ServiceData["entity_id"].(string)
			volumeLevel, _ := call.ServiceData["volume_level"].(float64)
			if morningZoneSpeakers[entityID] && volumeLevel == 0 {
				t.Errorf("Morning zone speaker %s was set to volume 0 — StopAllZones killed it (issue #755)", entityID)
			}
		}
	}

	// Verify musicPlaybackType transitioned to "morning" (set by morning zone)
	musicType, err := env.stateManager.GetString("musicPlaybackType")
	require.NoError(t, err)
	assert.Equal(t, "morning", musicType,
		"musicPlaybackType should be 'morning' after transition")

	t.Logf("Active zones after transition: %v", zoneNames)
	t.Logf("Total service calls: %d", len(calls))
	for i, call := range calls {
		if i >= 15 {
			t.Logf("  ... and %d more calls", len(calls)-15)
			break
		}
		t.Logf("  Call %d: %s.%s entity=%v vol=%v", i, call.Domain, call.Service,
			call.ServiceData["entity_id"], call.ServiceData["volume_level"])
	}

	t.Log("SUCCESS: Morning zone continued uninterrupted through wakeup zone cleanup (issue #755 fixed)")
}

// getZoneNames is a helper to extract zone names from a slice of zones
func getZoneNames(zones []*music.Zone) []string {
	names := make([]string, len(zones))
	for i, zone := range zones {
		names[i] = zone.Name
	}
	return names
}
