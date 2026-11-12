package integration

import (
	"context"
	"testing"

	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/plugins/music"
	"homeautomation/internal/plugins/reset"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

// ============================================================================
// Reset buttons turned on while the app is down
//
// Plugins react to changes. A reset button turned on while the app is not
// running produces no change for the new process to see. Before this fix the
// button stayed on, the reset never ran, and saying "turn on reset" again did
// nothing, because the button was already on.
//
// INVARIANTS:
// - A reset button that is on when the app starts runs once.
// - The button then turns itself off, so it can be used again.
// ============================================================================

// setResetAtStartupHome puts the house in a calm evening state with someone home and awake.
func setResetAtStartupHome(server *MockHAServer) {
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
}

// TestScenario_UserStory_ResetPressedWhileAppIsDown verifies that the system-wide reset
// (input_boolean.reset) runs when it is already on at startup.
//
// User story: "Things look wrong. I say 'turn on reset'. The app was restarting at
// that moment. When it comes back, the lights return to the right scene, and the
// button turns itself off."
func TestScenario_UserStory_ResetPressedWhileAppIsDown(t *testing.T) {
	t.Parallel()
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()
	logger := testlogger.New()

	t.Log("GIVEN: Someone is home in the evening, and reset was turned on while the app was down")
	setResetAtStartupHome(server)
	server.SetState("input_boolean.reset", "on", map[string]interface{}{})

	t.Log("WHEN: The app starts: it reads all state from Home Assistant, starts plugins, then the reset coordinator")
	require.NoError(t, stateManager.SyncFromHA())
	waitForBoolState(t, stateManager, "reset", true, "startup sync should read the reset")
	lightingManager := lighting.NewManager(context.Background(), client, stateManager,
		loadTestLightingConfig(t), logger, false, nil)
	require.NoError(t, lightingManager.Start())
	defer lightingManager.Stop()
	waitForProcessing(t, stateManager)
	snapshot := server.ServiceCallCount()

	coordinator := reset.NewCoordinator(stateManager, logger, false,
		[]reset.PluginWithName{{Name: "lighting", Plugin: lightingManager}})
	require.NoError(t, coordinator.Start())
	defer coordinator.Stop()

	t.Log("THEN: The reset runs, so scenes are re-applied")
	waitForCondition(t, func() bool {
		calls := server.GetServiceCallsSince(snapshot)
		return len(filterServiceCalls(calls, "scene", "turn_on")) >= 1
	}, "a reset that was on at startup should re-apply scenes")

	t.Log("THEN: The button turns itself off")
	waitForBoolState(t, stateManager, "reset", false, "reset should clear itself")
}

// TestScenario_UserStory_MusicResetPressedWhileAppIsDown verifies that the music reset
// (input_boolean.music_reset) runs when it is already on at startup.
//
// User story: "The music is stuck. I say 'turn on music reset'. The app was
// restarting at that moment. When it comes back, the music restarts and the button
// turns itself off, so I can use it again."
func TestScenario_UserStory_MusicResetPressedWhileAppIsDown(t *testing.T) {
	t.Parallel()
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()
	logger := testlogger.New()

	t.Log("GIVEN: Someone is home in the evening, and music reset was turned on while the app was down")
	setResetAtStartupHome(server)
	server.SetState("input_boolean.music_reset", "on", map[string]interface{}{})

	t.Log("WHEN: The app starts: it reads all state from Home Assistant, then starts music")
	require.NoError(t, stateManager.SyncFromHA())
	waitForBoolState(t, stateManager, "musicReset", true, "startup sync should read the music reset")
	musicManager := music.NewManager(context.Background(), client, stateManager,
		loadTestMusicConfigFromYAML(t), logger, false, nil, nil)
	require.NoError(t, musicManager.Start())
	defer musicManager.Stop()

	t.Log("THEN: The button turns itself off")
	waitForBoolState(t, stateManager, "musicReset", false, "musicReset should clear itself")
}
