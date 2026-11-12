package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/plugins/music"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Lighting Reset and Music Reset buttons
//
// "Turn on lighting reset" and "turn on music reset" in Siri flip
// input_boolean.lighting_reset and input_boolean.music_reset in Home Assistant.
//
// INVARIANTS:
// - Lighting reset re-applies scenes and never touches music.
// - Music reset re-resolves music and never re-applies scenes.
// - Each button turns itself off, so it can be used again.
// ============================================================================

// setupResetButtonsHome puts the house in a calm state: someone home and awake, evening.
func setupResetButtonsHome(t *testing.T, env *musicLightingEnv) {
	t.Helper()
	env.server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	env.server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	env.server.SetState("input_text.day_phase", "evening", map[string]interface{}{})
	env.server.SetState("input_boolean.lighting_reset", "off", map[string]interface{}{})
	env.server.SetState("input_boolean.music_reset", "off", map[string]interface{}{})
	waitForProcessing(t, env.stateManager)
	waitForServiceCallsToStabilize(t, env.server, 500*time.Millisecond)
}

// TestScenario_UserStory_LightingResetOnlyAffectsLighting verifies the lighting reset button.
//
// User story: "The lights look wrong. I say 'turn on lighting reset'. The lights
// return to the right scene. My music keeps playing."
func TestScenario_UserStory_LightingResetOnlyAffectsLighting(t *testing.T) {
	t.Parallel()
	env, cleanup := setupMusicLightingTest(t)
	defer cleanup()

	t.Log("GIVEN: Someone is home in the evening and the house is quiet")
	setupResetButtonsHome(t, env)
	snapshot := env.server.ServiceCallCount()

	t.Log("WHEN: Lighting reset is turned on")
	env.server.SetState("input_boolean.lighting_reset", "on", map[string]interface{}{})

	t.Log("THEN: Scenes are re-applied")
	waitForCondition(t, func() bool {
		calls := env.server.GetServiceCallsSince(snapshot)
		return len(filterServiceCalls(calls, "scene", "turn_on")) >= 1
	}, "lighting reset should activate scenes")

	t.Log("THEN: The button turns itself off")
	waitForBoolState(t, env.stateManager, "lightingReset", false, "lightingReset should clear itself")

	t.Log("THEN: Music is not touched")
	waitForServiceCallsToStabilizeSince(t, env.server, snapshot, 500*time.Millisecond)
	calls := env.server.GetServiceCallsSince(snapshot)
	assert.Empty(t, filterServiceCalls(calls, "media_player", "play_media"), "lighting reset must not start music")
	for _, call := range filterServiceCalls(calls, "input_text", "set_value") {
		entityID, _ := call.ServiceData["entity_id"].(string)
		assert.NotEqual(t, "input_text.music_playback_type", entityID, "lighting reset must not change music mode")
	}
}

// TestScenario_UserStory_MusicResetOnlyAffectsMusic verifies the music reset button.
//
// User story: "The music is stuck. I say 'turn on music reset'. The music restarts
// for the current mode. My lights stay as they are."
func TestScenario_UserStory_MusicResetOnlyAffectsMusic(t *testing.T) {
	t.Parallel()
	env, cleanup := setupMusicLightingTest(t)
	defer cleanup()

	t.Log("GIVEN: Someone is home in the evening and the house is quiet")
	setupResetButtonsHome(t, env)
	snapshot := env.server.ServiceCallCount()

	t.Log("WHEN: Music reset is turned on")
	env.server.SetState("input_boolean.music_reset", "on", map[string]interface{}{})

	t.Log("THEN: The button turns itself off")
	waitForBoolState(t, env.stateManager, "musicReset", false, "musicReset should clear itself")

	t.Log("THEN: Music re-resolves its mode")
	waitForCondition(t, func() bool {
		calls := env.server.GetServiceCallsSince(snapshot)
		return len(filterServiceCalls(calls, "input_text", "set_value")) >= 1 ||
			len(filterServiceCalls(calls, "media_player", "play_media")) >= 1
	}, "music reset should re-resolve music")

	t.Log("THEN: Lighting is not touched")
	waitForServiceCallsToStabilizeSince(t, env.server, snapshot, 500*time.Millisecond)
	calls := env.server.GetServiceCallsSince(snapshot)
	assert.Empty(t, filterServiceCalls(calls, "scene", "turn_on"), "music reset must not re-apply scenes")
}

// TestScenario_UserStory_LightingResetPressedWhileAppIsDown verifies that a lighting reset
// turned on while the app is not running still runs when the app starts.
//
// User story: "The lights look wrong. I say 'turn on lighting reset'. The app was
// restarting at that moment. When it comes back, the lights return to the right
// scene, and the button turns itself off."
//
// Regression: on 2026-11-12 the app crash-looped for three hours. The reset was
// turned on during that time. Lighting reacts only to changes, so the new process
// never saw the reset. The button stayed on and the lights stayed at the dusk scene.
func TestScenario_UserStory_LightingResetPressedWhileAppIsDown(t *testing.T) {
	t.Parallel()
	env, cleanup := setupMusicLightingTest(t)
	defer cleanup()

	t.Log("GIVEN: Someone is home in the evening and the lighting plugin is not running")
	setupResetButtonsHome(t, env)
	env.lighting.Stop()

	t.Log("WHEN: Lighting reset is turned on while the lighting plugin is down")
	env.server.SetState("input_boolean.lighting_reset", "on", map[string]interface{}{})
	snapshot := env.server.ServiceCallCount()

	t.Log("WHEN: The app starts: it reads all state from Home Assistant, then starts lighting")
	require.NoError(t, env.stateManager.SyncFromHA())
	waitForBoolState(t, env.stateManager, "lightingReset", true, "startup sync should read the reset")
	env.lighting = lighting.NewManager(context.Background(), env.client, env.stateManager,
		loadTestLightingConfig(t), env.logger, false, nil)
	require.NoError(t, env.lighting.Start())

	t.Log("THEN: Scenes are re-applied")
	waitForCondition(t, func() bool {
		calls := env.server.GetServiceCallsSince(snapshot)
		return len(filterServiceCalls(calls, "scene", "turn_on")) >= 1
	}, "a reset that was on at startup should activate scenes")

	t.Log("THEN: The button turns itself off")
	waitForBoolState(t, env.stateManager, "lightingReset", false, "lightingReset should clear itself")
}

// TestScenario_UserStory_MusicResetPressedWhileAppIsDown verifies that a music reset
// turned on while the app is not running still runs when the app starts.
//
// User story: "The music is stuck. I say 'turn on music reset'. The app was
// restarting at that moment. When it comes back, the music restarts, and the
// button turns itself off."
func TestScenario_UserStory_MusicResetPressedWhileAppIsDown(t *testing.T) {
	t.Parallel()
	env, cleanup := setupMusicLightingTest(t)
	defer cleanup()

	t.Log("GIVEN: Someone is home in the evening and the music plugin is not running")
	setupResetButtonsHome(t, env)
	env.music.Stop()

	t.Log("WHEN: Music reset is turned on while the music plugin is down")
	env.server.SetState("input_boolean.music_reset", "on", map[string]interface{}{})
	snapshot := env.server.ServiceCallCount()

	t.Log("WHEN: The app starts: it reads all state from Home Assistant, then starts music")
	require.NoError(t, env.stateManager.SyncFromHA())
	waitForBoolState(t, env.stateManager, "musicReset", true, "startup sync should read the reset")
	env.music = music.NewManager(context.Background(), env.client, env.stateManager,
		loadTestMusicConfigFromYAML(t), env.logger, false, nil, nil)
	require.NoError(t, env.music.Start())

	t.Log("THEN: The button turns itself off after the pending reset runs")
	waitForBoolState(t, env.stateManager, "musicReset", false, "musicReset should clear itself")

	t.Log("THEN: Music re-resolves its mode")
	waitForCondition(t, func() bool {
		calls := env.server.GetServiceCallsSince(snapshot)
		return len(filterServiceCalls(calls, "input_text", "set_value")) >= 1 ||
			len(filterServiceCalls(calls, "media_player", "play_media")) >= 1
	}, "a music reset that was on at startup should re-resolve music")
}
