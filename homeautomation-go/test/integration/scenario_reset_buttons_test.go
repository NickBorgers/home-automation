package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
