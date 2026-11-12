package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Day phase that changes while the app starts
//
// The day phase plugin starts before lighting. If the phase boundary passed
// while the app was down, day phase sets the new phase during startup, before
// lighting subscribes. Lighting never saw the change, so the lights stayed at
// the old phase's scenes.
//
// INVARIANTS:
// - If the day phase at startup differs from the phase Home Assistant held
//   when the app connected, lighting applies the new phase's scenes once.
// - If the day phase did not change, lighting does not touch scenes at startup.
//   A restart must not undo lights that someone changed by hand.
// ============================================================================

// setLightingStartupHome puts the house in a calm state: someone home and awake, dusk.
func setLightingStartupHome(server *MockHAServer) {
	server.SetState("input_boolean.anyone_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.nick_home", "on", map[string]interface{}{})
	server.SetState("input_boolean.anyone_home_and_awake", "on", map[string]interface{}{})
	server.SetState("input_boolean.master_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.anyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_boolean.everyone_asleep", "off", map[string]interface{}{})
	server.SetState("input_text.day_phase", "dusk", map[string]interface{}{})
}

// TestScenario_UserStory_DayPhaseChangedWhileAppStarts verifies that lighting applies
// the new phase's scenes when the day phase changed during startup.
//
// User story: "The app restarts just as dusk turns to night. The lights still
// change to the night scenes."
//
// Regression: on 2026-11-12 at 04:52:07Z, day phase set dusk → night about 0.6 s
// before lighting subscribed. The lights stayed at dusk brightness.
func TestScenario_UserStory_DayPhaseChangedWhileAppStarts(t *testing.T) {
	t.Parallel()
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t.Log("GIVEN: Home Assistant holds day phase 'dusk' when the app connects")
	setLightingStartupHome(server)
	require.NoError(t, stateManager.SyncFromHA())
	waitForStringState(t, stateManager, "dayPhase", "dusk", "startup sync should read dusk")

	t.Log("WHEN: The day phase plugin starts first and sets 'night'")
	require.NoError(t, stateManager.SetString("dayPhase", "night"))
	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Lighting starts after that")
	lightingManager := lighting.NewManager(context.Background(), client, stateManager,
		loadTestLightingConfig(t), testlogger.New(), false, nil)
	require.NoError(t, lightingManager.Start())
	defer lightingManager.Stop()

	t.Log("THEN: Lighting applies scenes for the new phase")
	waitForCondition(t, func() bool {
		calls := server.GetServiceCallsSince(snapshot)
		return len(filterServiceCalls(calls, "scene", "turn_on")) >= 1
	}, "lighting should apply night scenes after a phase change it missed at startup")
}

// TestScenario_UserStory_RestartWithoutPhaseChangeLeavesLightsAlone verifies that a
// restart with no day phase change does not re-apply scenes.
//
// User story: "I dimmed the kitchen by hand. The app restarts for a deploy. My
// kitchen stays the way I set it."
func TestScenario_UserStory_RestartWithoutPhaseChangeLeavesLightsAlone(t *testing.T) {
	t.Parallel()
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t.Log("GIVEN: Home Assistant holds day phase 'dusk' when the app connects")
	setLightingStartupHome(server)
	require.NoError(t, stateManager.SyncFromHA())
	waitForStringState(t, stateManager, "dayPhase", "dusk", "startup sync should read dusk")

	t.Log("WHEN: The day phase plugin starts and finds it is still dusk")
	require.NoError(t, stateManager.SetString("dayPhase", "dusk"))
	snapshot := server.ServiceCallCount()

	t.Log("WHEN: Lighting starts")
	lightingManager := lighting.NewManager(context.Background(), client, stateManager,
		loadTestLightingConfig(t), testlogger.New(), false, nil)
	require.NoError(t, lightingManager.Start())
	defer lightingManager.Stop()

	t.Log("THEN: No scenes are applied")
	time.Sleep(500 * time.Millisecond)
	calls := server.GetServiceCallsSince(snapshot)
	assert.Empty(t, filterServiceCalls(calls, "scene", "turn_on"), "a restart with no phase change must not re-apply scenes")
}
