package lighting

import (
	"context"
	"errors"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// lookupFailingClient fails every GetState call, as if Home Assistant were unreachable.
type lookupFailingClient struct {
	*ha.MockClient
}

func (c *lookupFailingClient) GetState(string) (*ha.State, error) {
	return nil, errors.New("connection closed")
}

func newObservedManager(t *testing.T, client ha.HAClient, env *testutil.Env, readOnly bool) (*Manager, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	return NewManager(context.Background(), client, env.StateMgr, &HueConfig{}, zap.New(core), readOnly, nil), logs
}

func hasLog(logs *observer.ObservedLogs, level zapcore.Level, msg string) bool {
	for _, e := range logs.All() {
		if e.Level == level && e.Message == msg {
			return true
		}
	}
	return false
}

func TestActivateScene_MissingSceneIsNotActivated(t *testing.T) {
	t.Parallel()
	ten := 10

	tests := []struct {
		name string
		room *RoomConfig
	}{
		{"single step", companionRoom(&ten, false, "Some Companion")},
		{"two step dynamic", companionRoom(&ten, true, "Some Companion")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := testutil.NewEnv(t)
			// Only the companion exists; the main scene does not.
			seedScenes(env.MockHA, "Some Companion")
			manager, logs := newObservedManager(t, env.MockHA, env, false)
			mockClock := clock.NewMockClock(time.Now())
			manager.SetClock(mockClock)

			manager.activateScene(context.Background(), tt.room, "dusk", "dayPhase")
			mockClock.AdvanceAndProcess(twoStepRecallGap + 100*time.Millisecond)

			assert.Empty(t, env.MockHA.GetServiceCalls(), "no service call for a missing scene, companions skipped")
			assert.False(t, hasLog(logs, zapcore.InfoLevel, "Scene activated successfully"))
			assert.False(t, hasLog(logs, zapcore.InfoLevel, "Scene activated successfully (dynamic phase)"))
			require.True(t, hasLog(logs, zapcore.WarnLevel, "Failed to activate scene: scene entity does not exist"))
			fields := logs.FilterMessage("Failed to activate scene: scene entity does not exist").All()[0].ContextMap()
			assert.Equal(t, "scene.kitchen_dusk", fields["entity_id"])
			assert.Equal(t, "Kitchen", fields["room"])
			assert.Equal(t, "dayPhase", fields["trigger"])

			action := manager.GetShadowState().Outputs.Rooms["Kitchen"]
			assert.Equal(t, "activate_scene_failed", action.ActionType)
			assert.Contains(t, action.Reason, "scene entity does not exist")
			assert.Empty(t, action.ActiveScene)
		})
	}
}

func TestActivateScene_UnavailableSceneIsNotActivated(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	env.MockHA.SetState("scene.kitchen_dusk", "unavailable", nil)
	manager, logs := newObservedManager(t, env.MockHA, env, false)

	manager.activateScene(context.Background(), companionRoom(nil, false), "dusk", "dayPhase")

	assert.Empty(t, env.MockHA.GetServiceCalls())
	assert.True(t, hasLog(logs, zapcore.WarnLevel, "Failed to activate scene: scene entity is unavailable"))
	assert.Equal(t, "activate_scene_failed", manager.GetShadowState().Outputs.Rooms["Kitchen"].ActionType)
}

func TestActivateScene_ExistingSceneStillActivates(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	seedScenes(env.MockHA, "Kitchen")
	manager, logs := newObservedManager(t, env.MockHA, env, false)

	manager.activateScene(context.Background(), companionRoom(nil, false), "dusk", "dayPhase")

	assert.Equal(t, []string{"scene.kitchen_dusk"}, entityIDs(env.MockHA.GetServiceCalls()))
	assert.True(t, hasLog(logs, zapcore.InfoLevel, "Scene activated successfully"))
	assert.False(t, hasLog(logs, zapcore.WarnLevel, "Failed to activate scene: scene entity does not exist"))
	assert.Equal(t, "activate_scene", manager.GetShadowState().Outputs.Rooms["Kitchen"].ActionType)
}

func TestActivateScene_MissingCompanionIsSkipped(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	seedScenes(env.MockHA, "Kitchen", "Present Companion")
	manager, logs := newObservedManager(t, env.MockHA, env, false)

	manager.activateScene(context.Background(),
		companionRoom(nil, false, "Missing Companion", "Present Companion"), "dusk", "dayPhase")

	assert.Equal(t, []string{"scene.kitchen_dusk", "scene.present_companion_dusk"},
		entityIDs(env.MockHA.GetServiceCalls()))
	assert.True(t, hasLog(logs, zapcore.WarnLevel, "Failed to activate companion scene: scene entity does not exist"))
	assert.Equal(t, "Activated scene 'dusk' + companions: scene.present_companion_dusk",
		manager.GetShadowState().Outputs.Rooms["Kitchen"].Reason)
}

func TestActivateScene_FailedLookupFallsBackToActivating(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	manager, logs := newObservedManager(t, &lookupFailingClient{env.MockHA}, env, false)

	manager.activateScene(context.Background(), companionRoom(nil, false), "dusk", "dayPhase")

	assert.Equal(t, []string{"scene.kitchen_dusk"}, entityIDs(env.MockHA.GetServiceCalls()))
	assert.True(t, hasLog(logs, zapcore.WarnLevel, "Could not check that scene entity exists; activating anyway"))
}

func TestActivateScene_ReadOnlyNotesMissingScene(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	manager, logs := newObservedManager(t, env.MockHA, env, true)

	manager.activateScene(context.Background(), companionRoom(nil, false), "dusk", "dayPhase")

	assert.Empty(t, env.MockHA.GetServiceCalls())
	assert.True(t, hasLog(logs, zapcore.WarnLevel, "READ-ONLY: Scene to activate has a problem: scene entity does not exist"))
	assert.Equal(t, "activate_scene", manager.GetShadowState().Outputs.Rooms["Kitchen"].ActionType)
}

func TestActivateScene_ReadOnlySkipsCompanionsWhenMainSceneMissing(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	seedScenes(env.MockHA, "Some Companion")
	manager, logs := newObservedManager(t, env.MockHA, env, true)

	manager.activateScene(context.Background(), companionRoom(nil, false, "Some Companion"), "dusk", "dayPhase")

	assert.Empty(t, env.MockHA.GetServiceCalls())
	assert.False(t, hasLog(logs, zapcore.InfoLevel, "READ-ONLY: Would activate companion scene"))
}

func TestActivateScene_ReadOnlyMissingCompanionHasReadOnlyPrefix(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	// Main scene exists; companion does not.
	seedScenes(env.MockHA, "Kitchen")
	manager, logs := newObservedManager(t, env.MockHA, env, true)

	manager.activateScene(context.Background(), companionRoom(nil, false, "Missing Companion"), "dusk", "dayPhase")

	assert.Empty(t, env.MockHA.GetServiceCalls())
	assert.True(t, hasLog(logs, zapcore.WarnLevel, "READ-ONLY: Companion scene has a problem: scene entity does not exist"),
		"missing companion in read-only mode must carry READ-ONLY prefix, not look like a live failure")
	assert.False(t, hasLog(logs, zapcore.WarnLevel, "Failed to activate companion scene: scene entity does not exist"),
		"must not log the live-mode message in read-only mode")
}
