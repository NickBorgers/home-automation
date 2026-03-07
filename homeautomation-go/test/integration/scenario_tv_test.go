package integration

import (
	"context"
	"testing"

	"homeautomation/internal/plugins/tv"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

// setupTVScenarioTest creates a test environment with the TV plugin running
func setupTVScenarioTest(t *testing.T) (*MockHAServer, *state.Manager, func()) {
	server, client, stateManager, baseCleanup := setupTest(t)

	// Create logger for TV plugin
	logger := testlogger.New()

	// Create and start TV plugin
	tvManager := tv.NewManager(context.Background(), client, stateManager, logger, false, nil)
	require.NoError(t, tvManager.Start(), "TV manager should start successfully")

	cleanup := func() {
		tvManager.Stop()
		baseCleanup()
	}

	return server, stateManager, cleanup
}

// ============================================================================
// High Priority Tests
// ============================================================================

// TestScenario_AppleTVPlaying verifies that when Apple TV starts playing,
// isAppleTVPlaying and isTVPlaying are both set to true
func TestScenario_AppleTVPlaying(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Apple TV is idle, TV is on, sync box is on, HDMI input is AppleTV")

	// Set initial states
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("media_player.sony_xr_65a80k", "on", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})

	// Wait for initial state to propagate
	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when TV remote is on")

	t.Log("WHEN: Apple TV starts playing")

	// Apple TV starts playing
	server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	t.Log("THEN: Verify isAppleTVPlaying and isTVPlaying are both true")

	// Wait for and verify state manager was updated
	waitForBoolState(t, manager, "isAppleTVPlaying", true, "isAppleTVPlaying should be true when Apple TV is playing")
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true when Apple TV is playing on AppleTV input")
}

// TestScenario_HDMIInputSwitch verifies that when HDMI input switches from
// Apple TV to Xbox, isTVPlaying updates correctly based on the input
func TestScenario_HDMIInputSwitch(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Apple TV is playing, TV is on, sync box is on, HDMI input is AppleTV")

	// Set initial states - Apple TV playing
	server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("media_player.sony_xr_65a80k", "on", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})

	// Wait for initial state
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should initially be true when AppleTV is playing")

	t.Log("WHEN: HDMI input switches to Xbox")

	// Switch HDMI input to Xbox
	server.SetState("select.sync_box_hdmi_input", "Xbox", map[string]interface{}{})

	t.Log("THEN: Verify isTVPlaying is still true (Xbox input assumes playing)")

	// When switching to non-Apple TV input, the logic assumes TV is playing
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true for Xbox input")

	t.Log("WHEN: HDMI input switches back to AppleTV (which is still playing)")

	// Switch back to Apple TV
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})

	t.Log("THEN: Verify isTVPlaying remains true")

	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should still be true")

	t.Log("WHEN: Apple TV stops playing while selected")

	// Stop Apple TV playback
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	t.Log("THEN: Verify isTVPlaying is now false")

	waitForBoolState(t, manager, "isTVPlaying", false, "isTVPlaying should be false when AppleTV is idle")
}

// TestScenario_TVRemoteKillSwitch verifies that the TV remote entity acts as a
// kill switch: when the TV panel turns off, isTVPlaying is forced false and
// light sync turns off, even though isTVon (driven by sync box) may still be true.
func TestScenario_TVRemoteKillSwitch(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sync box is on, TV is on, Apple TV is playing on AppleTV input")

	// Set initial states - everything on and playing
	server.SetState("media_player.sony_xr_65a80k", "on", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})

	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when sync box is on")
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true when AppleTV is playing")

	t.Log("WHEN: TV panel turns off (remote entity goes off) but sync box stays on")

	// TV panel turns off - sync box stays on
	server.SetState("media_player.sony_xr_65a80k", "off", map[string]interface{}{})

	t.Log("THEN: isTVPlaying should be forced false (kill switch)")

	waitForBoolState(t, manager, "isTVPlaying", false, "isTVPlaying should be false when TV panel turns off")
	// Note: isTVon remains true because it's driven by sync box power
	waitForBoolState(t, manager, "isTVon", true, "isTVon should remain true (sync box is still on)")
}

// TestScenario_MultipleInputs tests behavior when inputs change rapidly
func TestScenario_MultipleInputs(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: TV is on, sync box is on, Apple TV is idle, HDMI input is AppleTV")

	// Set initial states
	server.SetState("media_player.sony_xr_65a80k", "on", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})
	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when TV remote is on")

	t.Log("WHEN: Switching between multiple HDMI inputs")

	// Switch to Xbox
	server.SetState("select.sync_box_hdmi_input", "Xbox", map[string]interface{}{})

	// Verify isTVPlaying is true for Xbox
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true for Xbox")

	// Switch to Cable
	server.SetState("select.sync_box_hdmi_input", "Cable", map[string]interface{}{})

	// Verify isTVPlaying is true for Cable
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true for Cable")

	// Switch back to AppleTV (idle)
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})

	t.Log("THEN: Verify isTVPlaying is false (AppleTV is idle)")

	waitForBoolState(t, manager, "isTVPlaying", false, "isTVPlaying should be false when AppleTV input is selected but not playing")
}

// TestScenario_TVOffState verifies that when all inputs are inactive,
// all TV state variables are false
func TestScenario_TVOffState(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: TV is initially on with Apple TV playing")

	// Set initial states - everything on and playing
	server.SetState("media_player.sony_xr_65a80k", "on", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})

	// Wait for initial state to propagate before proceeding
	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when sync box is on")
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true when AppleTV is playing")

	t.Log("WHEN: TV is turned off (sync box powers off)")

	// Turn off sync box
	server.SetState("switch.sync_box_power", "off", map[string]interface{}{})

	t.Log("THEN: Verify all TV state variables are false")

	// Use polling to wait for state changes to propagate
	waitForBoolState(t, manager, "isTVon", false, "isTVon should be false when sync box is off")
	waitForBoolState(t, manager, "isTVPlaying", false, "isTVPlaying should be false when sync box is off")

	t.Log("WHEN: Apple TV also stops playing")

	// Stop Apple TV
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	t.Log("THEN: Verify isAppleTVPlaying is also false")

	waitForBoolState(t, manager, "isAppleTVPlaying", false, "isAppleTVPlaying should be false when Apple TV is idle")
}

// ============================================================================
// Medium Priority Tests - Edge Cases
// ============================================================================

// TestScenario_RapidInputSwitching verifies that rapid HDMI input changes
// are handled correctly without race conditions
func TestScenario_RapidInputSwitching(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: TV is on, sync box is on, Apple TV is playing")

	// Set initial states
	server.SetState("media_player.sony_xr_65a80k", "on", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true when AppleTV is playing")

	t.Log("WHEN: Rapidly switching HDMI inputs")

	// Rapid input switching - set all inputs quickly
	inputs := []string{"Xbox", "Cable", "AppleTV", "Xbox", "AppleTV", "Cable", "AppleTV"}
	for _, input := range inputs {
		server.SetState("select.sync_box_hdmi_input", input, map[string]interface{}{})
	}

	t.Log("THEN: Verify final state is consistent (AppleTV playing)")

	// Final input was AppleTV, and Apple TV is playing, so should be true
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true for final state (AppleTV playing)")
}

// TestScenario_AppleTVPlaybackStateChanges tests various Apple TV states
func TestScenario_AppleTVPlaybackStateChanges(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: TV is on, sync box is on, HDMI input is AppleTV")

	// Set initial states
	server.SetState("media_player.sony_xr_65a80k", "on", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when TV remote is on")

	testCases := []struct {
		state           string
		expectedPlaying bool
	}{
		{"playing", true},
		{"paused", false},
		{"idle", false},
		{"playing", true},
		{"standby", false},
		{"off", false},
	}

	for _, tc := range testCases {
		t.Logf("WHEN: Apple TV state changes to %s", tc.state)

		server.SetState("media_player.big_beautiful_oled", tc.state, map[string]interface{}{
			"friendly_name": "Apple TV",
		})

		t.Logf("THEN: Verify isAppleTVPlaying is %v and isTVPlaying is %v", tc.expectedPlaying, tc.expectedPlaying)

		waitForBoolState(t, manager, "isAppleTVPlaying", tc.expectedPlaying,
			"isAppleTVPlaying should be %v when Apple TV is %s", tc.expectedPlaying, tc.state)
		waitForBoolState(t, manager, "isTVPlaying", tc.expectedPlaying,
			"isTVPlaying should be %v when Apple TV is %s and input is AppleTV", tc.expectedPlaying, tc.state)
	}
}

// TestScenario_SyncBoxUnavailable_ClearsTVStates verifies that when the sync box
// goes unavailable (crash), isTVon and isTVPlaying are cleared immediately.
// This prevents stale isTVPlaying=true from causing the lighting plugin to skip
// the living room (deferring to Hue Sync, which isn't running).
func TestScenario_SyncBoxUnavailable_ClearsTVStates(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: TV is on, playing via Apple TV")

	// Set initial states - everything on and playing
	server.SetState("media_player.sony_xr_65a80k", "on", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})

	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when sync box is on")
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true when AppleTV is playing")

	t.Log("WHEN: Sync box goes unavailable (crash)")

	server.SetState("switch.sync_box_power", "unavailable", map[string]interface{}{})

	t.Log("THEN: isTVon and isTVPlaying should both become false")

	waitForBoolState(t, manager, "isTVon", false, "isTVon should be false after sync box goes unavailable")
	waitForBoolState(t, manager, "isTVPlaying", false, "isTVPlaying should be false after sync box goes unavailable")
}

// TestScenario_TVOffSyncBoxOn verifies that when the TV is off but the sync box
// powers on independently, isTVPlaying remains false and light sync is not enabled.
// This prevents the bug where sync box turning on overnight caused 9+ hours of light sync.
func TestScenario_TVOffSyncBoxOn(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: TV is off, sync box is off, HDMI input is on a non-AppleTV input")

	// Set initial states - TV is off
	server.SetState("media_player.sony_xr_65a80k", "off", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "off", map[string]interface{}{})
	server.SetState("select.sync_box_hdmi_input", "HDMI 1", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "off", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	// Wait for initial state
	waitForBoolState(t, manager, "isTVon", false, "isTVon should be false when TV remote is off")

	t.Log("WHEN: Sync box powers on independently (e.g., after power restore)")

	// Sync box powers on but TV stays off
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})

	t.Log("THEN: isTVon becomes true (sync box drives it), but isTVPlaying stays false (TV panel is off)")

	// isTVon is driven by sync box, so it becomes true
	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true (sync box is on)")
	// But isTVPlaying stays false because calculateTVPlaying checks TV remote state
	waitForBoolState(t, manager, "isTVPlaying", false, "isTVPlaying should remain false (TV panel is off)")
}

// TestScenario_SyncBoxPowerOnRecalculates verifies that when the sync box powers on
// and the HDMI input changes at the same time, isTVPlaying is correctly recalculated.
// This reproduces a production race condition where both events arrive at the same second:
//
//	19:16:50 - select.sync_box_hdmi_input: AppleTV -> Nintendo Switch
//	19:16:50 - switch.sync_box_power: off -> on
//
// If the HDMI input change is processed first, calculateTVPlaying sees isTVon=false
// (sync box power hasn't been processed yet) and sets isTVPlaying=false. Then
// handleSyncBoxPowerChange sets isTVon=true but must recalculate isTVPlaying.
func TestScenario_SyncBoxPowerOnRecalculates(t *testing.T) {
	t.Parallel()
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: TV panel is on, sync box is off, HDMI input is AppleTV")

	// Set initial states - TV panel on, sync box off
	server.SetState("media_player.sony_xr_65a80k", "on", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "off", map[string]interface{}{})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	// Wait for initial state
	waitForBoolState(t, manager, "isTVon", false, "isTVon should be false when sync box is off")

	t.Log("WHEN: HDMI input switches to Nintendo Switch AND sync box powers on (simultaneous events)")

	// Simulate the race: HDMI input change arrives first, then power change
	server.SetState("select.sync_box_hdmi_input", "Nintendo Switch", map[string]interface{}{})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})

	t.Log("THEN: isTVon should be true AND isTVPlaying should be true (non-AppleTV input assumes playing)")

	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when sync box is on")
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true — sync box power-on must recalculate based on current HDMI input")
}
