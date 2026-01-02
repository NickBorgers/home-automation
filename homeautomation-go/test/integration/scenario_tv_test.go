package integration

import (
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
	tvManager := tv.NewManager(client, stateManager, logger, false, nil)
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
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Apple TV is idle, sync box is on, HDMI input is AppleTV")

	// Set initial states
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})

	// Wait for initial state to propagate
	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when sync box is on")

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
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Apple TV is playing, sync box is on, HDMI input is AppleTV")

	// Set initial states - Apple TV playing
	server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
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

// TestScenario_SyncBoxPower verifies that sync box power changes update isTVon
// and that turning off the sync box sets isTVPlaying to false
func TestScenario_SyncBoxPower(t *testing.T) {
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sync box is off, Apple TV is idle")

	// Set initial states
	server.SetState("switch.sync_box_power", "off", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})

	// Wait for initial state
	waitForBoolState(t, manager, "isTVon", false, "isTVon should be false when sync box is off")

	t.Log("WHEN: Sync box powers on")

	// Power on sync box
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})

	t.Log("THEN: Verify isTVon is true")

	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when sync box is on")

	t.Log("GIVEN: Apple TV starts playing while sync box is on")

	// Start Apple TV playback
	server.SetState("media_player.big_beautiful_oled", "playing", map[string]interface{}{
		"friendly_name": "Apple TV",
	})

	// Verify isTVPlaying is true
	waitForBoolState(t, manager, "isTVPlaying", true, "isTVPlaying should be true when AppleTV is playing")

	t.Log("WHEN: Sync box powers off")

	// Power off sync box
	server.SetState("switch.sync_box_power", "off", map[string]interface{}{})

	t.Log("THEN: Verify isTVon is false AND isTVPlaying is false")

	waitForBoolState(t, manager, "isTVon", false, "isTVon should be false when sync box is off")
	waitForBoolState(t, manager, "isTVPlaying", false, "isTVPlaying should be false when sync box is off (even if AppleTV is playing)")
}

// TestScenario_MultipleInputs tests behavior when inputs change rapidly
func TestScenario_MultipleInputs(t *testing.T) {
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sync box is on, Apple TV is idle, HDMI input is AppleTV")

	// Set initial states
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})
	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when sync box is on")

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
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: TV is initially on with Apple TV playing")

	// Set initial states - everything on and playing
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
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sync box is on, Apple TV is playing")

	// Set initial states
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
	server, manager, cleanup := setupTVScenarioTest(t)
	defer cleanup()

	t.Log("GIVEN: Sync box is on, HDMI input is AppleTV")

	// Set initial states
	server.SetState("switch.sync_box_power", "on", map[string]interface{}{})
	server.SetState("select.sync_box_hdmi_input", "AppleTV", map[string]interface{}{})
	server.SetState("media_player.big_beautiful_oled", "idle", map[string]interface{}{
		"friendly_name": "Apple TV",
	})
	waitForBoolState(t, manager, "isTVon", true, "isTVon should be true when sync box is on")

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
