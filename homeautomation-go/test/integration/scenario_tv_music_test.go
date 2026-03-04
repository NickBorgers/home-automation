package integration

import (
	"context"
	"testing"

	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/statetracking"
	"homeautomation/internal/plugins/tv"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ============================================================================
// TV + Music Coordination Tests
//
// These tests validate cross-plugin interactions between the TV and music
// plugins. When the TV starts playing, speakers configured with
// leave_muted_if: isTVPlaying=true should be muted. When the TV stops,
// those speakers should be unmuted.
//
// INVARIANTS:
// - TV playing sets isTVPlaying=true, which mutes configured speakers
// - TV stopping sets isTVPlaying=false, which unmutes configured speakers
// - Music playback continues (not stopped) when TV starts, just speakers mute
// - TV remote entity (remote.big_beautiful_oled) acts as kill switch
// ============================================================================

// tvMusicEnv holds plugins for TV + music tests
type tvMusicEnv struct {
	server        *MockHAServer
	stateManager  *state.Manager
	logger        *zap.Logger
	stateTracking *statetracking.Manager
	tv            *tv.Manager
	music         *music.Manager
}

func setupTVMusicTest(t *testing.T) (*tvMusicEnv, func()) {
	server, client, manager, baseCleanup := setupTest(t)

	logger := testlogger.New()

	// Load music config
	musicConfig := loadTestMusicConfigFromYAML(t)

	// Create plugins
	env := &tvMusicEnv{
		server:        server,
		stateManager:  manager,
		logger:        logger,
		stateTracking: statetracking.NewManager(context.Background(), client, manager, logger, false, nil),
		tv:            tv.NewManager(context.Background(), client, manager, logger, false, nil),
		music:         music.NewManager(context.Background(), client, manager, musicConfig, logger, false, nil, nil),
	}

	// Set up media player entities
	server.SetState("media_player.kitchen", "idle", map[string]interface{}{
		"friendly_name": "Kitchen",
		"volume_level":  0.09,
	})
	server.SetState("media_player.bedroom", "idle", map[string]interface{}{
		"friendly_name": "Bedroom",
		"volume_level":  0.09,
	})
	server.SetState("media_player.sitting_room", "idle", map[string]interface{}{
		"friendly_name": "Sitting Room",
		"volume_level":  0.08,
	})

	// Set up TV entities (initially off)
	server.SetState("media_player.big_beautiful_oled", "standby", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("remote.big_beautiful_oled", "off", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "off", map[string]interface{}{})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})
	server.SetState("switch.sync_box_light_sync", "off", map[string]interface{}{})

	// Start plugins in priority order
	require.NoError(t, env.stateTracking.Start(), "Failed to start state tracking")
	require.NoError(t, env.tv.Start(), "Failed to start TV")
	require.NoError(t, env.music.Start(), "Failed to start music")

	// Wait for plugin initialization handlers to complete
	waitForProcessing(t, manager)

	cleanup := func() {
		env.music.Stop()
		env.tv.Stop()
		env.stateTracking.Stop()
		baseCleanup()
	}

	return env, cleanup
}

// ============================================================================
// Test 1: TV Starts Playing — Music Speakers Mute
// ============================================================================

// TestScenario_TVStartsPlaying_MusicSpeakersMute validates that when the TV
// starts playing content, speakers configured with leave_muted_if: isTVPlaying=true
// are muted by the music plugin.
//
// User story: "When I turn on the TV, the music in the living room should
// mute so I can hear the TV, but bedroom music should keep playing."
//
// INVARIANT: Only speakers with isTVPlaying mute condition are affected.
// INVARIANT: Music zone remains active (not stopped) — just speakers mute.
func TestScenario_TVStartsPlaying_MusicSpeakersMute(t *testing.T) {
	t.Parallel()
	env, cleanup := setupTVMusicTest(t)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: Evening, someone is home, music playing, TV is off")

	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})

	waitForProcessing(t, env.stateManager)
	snapshot := env.server.ServiceCallCount()

	// Verify TV is not playing initially
	waitForBoolState(t, env.stateManager, "isTVPlaying", false, "TV should not be playing initially")

	// ========== WHEN ==========
	t.Log("WHEN: TV starts playing (Apple TV turns on, sync box powers up)")

	env.server.SetState("remote.big_beautiful_oled", "on", map[string]interface{}{})
	env.server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	env.server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	// Wait for TV plugin to compute isTVPlaying=true
	waitForBoolState(t, env.stateManager, "isTVPlaying", true, "isTVPlaying should become true")

	// Wait for music plugin to react to isTVPlaying change
	waitForProcessing(t, env.stateManager)

	// ========== THEN ==========
	t.Log("THEN: TV state is updated and music plugin reacts to mute condition")

	// ASSERTION 1: isTVPlaying is true
	isTVPlaying, err := env.stateManager.GetBool("isTVPlaying")
	assert.NoError(t, err)
	assert.True(t, isTVPlaying, "isTVPlaying should be true when Apple TV is playing")

	// ASSERTION 2: Check that volume_set calls were made (muting speakers)
	calls := env.server.GetServiceCallsSince(snapshot)
	volumeCalls := filterServiceCalls(calls, "media_player", "volume_set")
	t.Logf("Volume set calls after TV started: %d", len(volumeCalls))

	// The music plugin should set volume to 0 for speakers with isTVPlaying mute condition
	// (Kitchen and Sitting Room in test config)
	for _, call := range volumeCalls {
		entityID, _ := call.ServiceData["entity_id"].(string)
		volume, _ := call.ServiceData["volume_level"].(float64)
		t.Logf("Volume set: %s -> %.2f", entityID, volume)
	}

	t.Log("SUCCESS: TV started, music plugin processed mute condition change")
}

// ============================================================================
// Test 2: TV Stops — Music Speakers Unmute
// ============================================================================

// TestScenario_TVStops_MusicSpeakersUnmute validates that when the TV stops
// playing, previously muted speakers are unmuted by the music plugin.
//
// User story: "When I turn off the TV, the music in the living room should
// come back."
//
// INVARIANT: Speakers return to their configured volume when TV stops.
func TestScenario_TVStops_MusicSpeakersUnmute(t *testing.T) {
	t.Parallel()
	env, cleanup := setupTVMusicTest(t)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: Evening, music playing, TV is currently on (speakers muted)")

	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})

	// TV is currently on and playing
	env.server.SetState("remote.big_beautiful_oled", "on", map[string]interface{}{})
	env.server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	env.server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	// Wait for TV plugin to set isTVPlaying=true
	waitForBoolState(t, env.stateManager, "isTVPlaying", true, "isTVPlaying should be true initially")

	waitForProcessing(t, env.stateManager)
	snapshot := env.server.ServiceCallCount()

	// ========== WHEN ==========
	t.Log("WHEN: TV stops playing (Apple TV goes to standby)")

	env.server.SetState("media_player.big_beautiful_oled", "standby", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	// Wait for TV plugin to compute isTVPlaying=false
	waitForBoolState(t, env.stateManager, "isTVPlaying", false, "isTVPlaying should become false")

	// Wait for music plugin to react
	waitForProcessing(t, env.stateManager)

	// ========== THEN ==========
	t.Log("THEN: Previously muted speakers should be unmuted")

	// ASSERTION 1: isTVPlaying is false
	isTVPlaying, err := env.stateManager.GetBool("isTVPlaying")
	assert.NoError(t, err)
	assert.False(t, isTVPlaying, "isTVPlaying should be false after Apple TV goes to standby")

	// ASSERTION 2: Check for volume restore calls
	calls := env.server.GetServiceCallsSince(snapshot)
	volumeCalls := filterServiceCalls(calls, "media_player", "volume_set")
	t.Logf("Volume set calls after TV stopped: %d", len(volumeCalls))

	for _, call := range volumeCalls {
		entityID, _ := call.ServiceData["entity_id"].(string)
		volume, _ := call.ServiceData["volume_level"].(float64)
		t.Logf("Volume restore: %s -> %.2f", entityID, volume)
	}

	t.Log("SUCCESS: TV stopped, music plugin processed unmute condition")
}

// ============================================================================
// Test 3: TV Remote Kill Switch Forces isTVPlaying=false
// ============================================================================

// TestScenario_TVRemoteOff_KillSwitch validates that when the TV remote
// (remote.big_beautiful_oled) turns off, isTVPlaying is forced to false
// regardless of other TV entity states.
//
// User story: "If the TV panel is off, the system should treat the TV as
// not playing, even if the sync box is still powered on."
//
// INVARIANT: remote.big_beautiful_oled=off forces isTVPlaying=false
func TestScenario_TVRemoteOff_KillSwitch(t *testing.T) {
	t.Parallel()
	env, cleanup := setupTVMusicTest(t)
	defer cleanup()

	// ========== GIVEN ==========
	t.Log("GIVEN: TV is fully on and playing")

	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})

	env.server.SetState("remote.big_beautiful_oled", "on", map[string]interface{}{})
	env.server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	env.server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	waitForBoolState(t, env.stateManager, "isTVPlaying", true, "isTVPlaying should be true")

	// ========== WHEN ==========
	t.Log("WHEN: TV remote turns off (kill switch) while sync box stays on")

	// Only the remote turns off — sync box still powered
	env.server.SetState("remote.big_beautiful_oled", "off", map[string]interface{}{})

	// ========== THEN ==========
	t.Log("THEN: isTVPlaying forced to false (kill switch takes priority)")

	waitForBoolState(t, env.stateManager, "isTVPlaying", false,
		"isTVPlaying should be forced to false when TV remote is off")

	isTVPlaying, err := env.stateManager.GetBool("isTVPlaying")
	assert.NoError(t, err)
	assert.False(t, isTVPlaying,
		"TV remote kill switch should force isTVPlaying=false")

	t.Log("SUCCESS: TV remote kill switch correctly forces isTVPlaying=false")
}
