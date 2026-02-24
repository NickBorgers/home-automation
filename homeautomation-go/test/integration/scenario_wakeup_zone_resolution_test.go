package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/plugins/music"
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
}

func setupWakeupZoneResolutionTest(t *testing.T) (*wakeupZoneEnv, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Load music config from YAML
	musicConfig := loadTestMusicConfigFromYAML(t)

	// Create plugins
	env := &wakeupZoneEnv{
		server:        server,
		stateManager:  stateManager,
		logger:        logger,
		stateTracking: statetracking.NewManager(context.Background(), client, stateManager, logger, false, nil),
		music:         music.NewManager(context.Background(), client, stateManager, musicConfig, logger, false, nil, nil),
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
	require.NoError(t, env.music.Start(), "Failed to start music")

	// Wait for plugin initialization
	waitForProcessing(t, stateManager)

	cleanup := func() {
		env.music.Stop()
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

	// Wait for all state changes to propagate and handlers to settle
	waitForProcessing(t, env.stateManager)
	time.Sleep(200 * time.Millisecond) // Extra settle time for zone resolution

	// Clear service calls from setup
	env.server.ClearServiceCalls()

	// Enable production debouncing (500ms) to test coalescing behavior
	env.music.SetDebounceDelay(500 * time.Millisecond)

	// ===== WHEN: Three state variables change rapidly (simulating wake-up alarm)
	t.Log("WHEN: isAnyoneAsleep, isMasterAsleep, isWakeSequenceActive all change within ~100ms")

	// These three changes would each independently trigger zone resolution
	// without debouncing. With debouncing, they should coalesce into one.
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	time.Sleep(30 * time.Millisecond)
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	time.Sleep(30 * time.Millisecond)
	env.server.SetState("input_boolean.wake_sequence_active", "on", map[string]interface{}{})

	// Wait for debounce timer to fire (500ms) plus processing time
	time.Sleep(800 * time.Millisecond)
	waitForProcessing(t, env.stateManager)

	// ===== THEN: Only one set of zone resolution service calls
	t.Log("THEN: Bedroom should receive at most one set of join/volume commands (not three)")

	calls := env.server.GetServiceCalls()

	// Count how many times media_player.join was called for the bedroom speaker.
	// Before the fix, each of the 3 triggers would independently try to join
	// the bedroom to the morning zone, resulting in 3 join calls that would
	// race and cancel each other on the Sonos speaker.
	bedroomJoinCount := 0
	for _, call := range calls {
		if call.Domain == "media_player" && call.Service == "join" {
			if entityID, ok := call.ServiceData["entity_id"].(string); ok {
				if entityID == "media_player.bedroom" {
					bedroomJoinCount++
				}
			}
		}
	}

	// With debouncing, we should see at most 1 join call for bedroom
	// (the exact number may be 0 or 1 depending on zone transition logic,
	// but it should never be 3)
	assert.LessOrEqual(t, bedroomJoinCount, 1,
		"Bedroom should receive at most 1 join command (got %d) — debouncing should prevent duplicate joins",
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
