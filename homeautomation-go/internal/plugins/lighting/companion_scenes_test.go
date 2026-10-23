package lighting

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingSceneClient wraps a mock client and fails scene.turn_on for chosen entity IDs.
type failingSceneClient struct {
	*ha.MockClient
	failEntities map[string]bool
}

func (c *failingSceneClient) CallService(ctx context.Context, domain, service string,
	data map[string]interface{}) error {
	if id, ok := data["entity_id"].(string); ok && c.failEntities[id] {
		return errors.New("scene not found")
	}
	return c.MockClient.CallService(ctx, domain, service, data)
}

func companionRoom(transition *int, hasDynamics bool, companions ...string) *RoomConfig {
	return &RoomConfig{
		HueGroup:          "Kitchen",
		HASSAreaID:        "kitchen",
		TransitionSeconds: transition,
		HasDynamics:       hasDynamics,
		CompanionScenes:   companions,
	}
}

func entityIDs(calls []ha.ServiceCall) []string {
	ids := make([]string, 0, len(calls))
	for _, c := range calls {
		ids = append(ids, c.Data["entity_id"].(string))
	}
	return ids
}

func TestActivateScene_CompanionScenes(t *testing.T) {
	t.Parallel()
	ten := 10
	three := 3

	tests := []struct {
		name           string
		room           *RoomConfig
		trigger        string
		readOnly       bool
		failEntities   map[string]bool
		wantEntities   []string
		wantTransition interface{} // expected transition on every call, nil means no key
	}{
		{
			name:           "companions follow main scene with same transition",
			room:           companionRoom(&ten, false, "Kitchen Under Cabinet"),
			trigger:        "dayPhase",
			wantEntities:   []string{"scene.kitchen_morning", "scene.kitchen_under_cabinet_morning"},
			wantTransition: 10,
		},
		{
			name:           "presence trigger caps companion transition",
			room:           companionRoom(&ten, false, "Kitchen Under Cabinet"),
			trigger:        "isAnyoneHomeAndAwake",
			wantEntities:   []string{"scene.kitchen_morning", "scene.kitchen_under_cabinet_morning"},
			wantTransition: maxReturnHomeTransition,
		},
		{
			name:           "short presence transition is kept",
			room:           companionRoom(&three, false, "Kitchen Under Cabinet"),
			trigger:        "isAnyoneHomeAndAwake",
			wantEntities:   []string{"scene.kitchen_morning", "scene.kitchen_under_cabinet_morning"},
			wantTransition: 3,
		},
		{
			name:           "nil transition is omitted from companions",
			room:           companionRoom(nil, false, "Kitchen Under Cabinet"),
			trigger:        "dayPhase",
			wantEntities:   []string{"scene.kitchen_morning", "scene.kitchen_under_cabinet_morning"},
			wantTransition: nil,
		},
		{
			name:    "failing companion does not stop later companions",
			room:    companionRoom(&ten, false, "Kitchen Under Cabinet", "Kitchen Island", "Kitchen Pantry"),
			trigger: "dayPhase",
			failEntities: map[string]bool{
				"scene.kitchen_under_cabinet_morning": true,
			},
			wantEntities: []string{
				"scene.kitchen_morning", "scene.kitchen_island_morning", "scene.kitchen_pantry_morning",
			},
			wantTransition: 10,
		},
		{
			name:           "empty companion list changes nothing",
			room:           companionRoom(&ten, false),
			trigger:        "dayPhase",
			wantEntities:   []string{"scene.kitchen_morning"},
			wantTransition: 10,
		},
		{
			name:         "read-only makes no calls",
			room:         companionRoom(&ten, false, "Kitchen Under Cabinet"),
			trigger:      "dayPhase",
			readOnly:     true,
			wantEntities: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := testutil.NewEnv(t)
			client := &failingSceneClient{MockClient: env.MockHA, failEntities: tt.failEntities}
			manager := NewManager(context.Background(), client, env.StateMgr, &HueConfig{}, env.Logger, tt.readOnly, nil)

			manager.activateScene(context.Background(), tt.room, "morning", tt.trigger)

			calls := filterSceneCalls(env.MockHA.GetServiceCalls())
			assert.Equal(t, tt.wantEntities, entityIDs(calls))
			for _, c := range calls {
				got, has := c.Data["transition"]
				if tt.wantTransition == nil {
					assert.False(t, has, "no transition key expected")
				} else {
					assert.Equal(t, tt.wantTransition, got)
				}
			}
		})
	}
}

func TestActivateScene_CompanionFailureKeepsMainScene(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	client := &failingSceneClient{
		MockClient:   env.MockHA,
		failEntities: map[string]bool{"scene.kitchen_under_cabinet_morning": true},
	}
	manager := NewManager(context.Background(), client, env.StateMgr, &HueConfig{}, env.Logger, false, nil)

	manager.activateScene(context.Background(), companionRoom(nil, false, "Kitchen Under Cabinet"), "morning", "dayPhase")

	assert.Equal(t, []string{"scene.kitchen_morning"}, entityIDs(filterSceneCalls(env.MockHA.GetServiceCalls())))
	action := manager.GetShadowState().Outputs.Rooms["Kitchen"]
	assert.Equal(t, "Activated scene 'morning'", action.Reason)
}

func TestActivateScene_CompanionsNotRunWhenMainSceneFails(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	client := &failingSceneClient{
		MockClient:   env.MockHA,
		failEntities: map[string]bool{"scene.kitchen_morning": true},
	}
	manager := NewManager(context.Background(), client, env.StateMgr, &HueConfig{}, env.Logger, false, nil)

	manager.activateScene(context.Background(), companionRoom(nil, false, "Kitchen Under Cabinet"), "morning", "dayPhase")

	assert.Empty(t, env.MockHA.GetServiceCalls())
}

func TestActivateScene_CompanionShadowDescription(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, &HueConfig{}, env.Logger, false, nil)

	manager.activateScene(context.Background(), companionRoom(nil, false, "Kitchen Under Cabinet"), "morning", "dayPhase")

	action := manager.GetShadowState().Outputs.Rooms["Kitchen"]
	assert.Equal(t, "Activated scene 'morning' + companions: scene.kitchen_under_cabinet_morning",
		action.Reason)
}

func TestActivateScene_CompanionsRunForDynamicRoom(t *testing.T) {
	t.Parallel()
	ten := 10

	t.Run("two-step path", func(t *testing.T) {
		t.Parallel()
		env := testutil.NewEnv(t)
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, &HueConfig{}, env.Logger, false, nil)
		mockClock := clock.NewMockClock(time.Now())
		manager.SetClock(mockClock)

		manager.activateScene(context.Background(), companionRoom(&ten, true, "Kitchen Under Cabinet"), "morning", "dayPhase")
		mockClock.AdvanceAndProcess(twoStepRecallGap + 100*time.Millisecond)

		hue := filterHueActivateSceneCalls(env.MockHA.GetServiceCalls())
		assert.Equal(t, []string{"scene.kitchen_morning", "scene.kitchen_morning"}, entityIDs(hue))
		scene := filterSceneCalls(env.MockHA.GetServiceCalls())
		require.Equal(t, []string{"scene.kitchen_under_cabinet_morning"}, entityIDs(scene))
		assert.Equal(t, 10, scene[0].Data["transition"])
		_, hasDynamic := scene[0].Data["dynamic"]
		assert.False(t, hasDynamic, "companions are plain scene.turn_on calls")
	})

	t.Run("fallback path", func(t *testing.T) {
		t.Parallel()
		env := testutil.NewEnv(t)
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, &HueConfig{}, env.Logger, false, nil)
		env.MockHA.SetServiceError("hue", "activate_scene", errors.New("invalid_format"))

		manager.activateScene(context.Background(), companionRoom(&ten, true, "Kitchen Under Cabinet"), "morning", "dayPhase")

		assert.Equal(t, []string{"scene.kitchen_morning", "scene.kitchen_under_cabinet_morning"},
			entityIDs(filterSceneCalls(env.MockHA.GetServiceCalls())))
	})

	t.Run("main static phase fails and fallback fails", func(t *testing.T) {
		t.Parallel()
		env := testutil.NewEnv(t)
		manager := NewManager(context.Background(), env.MockHA, env.StateMgr, &HueConfig{}, env.Logger, false, nil)
		env.MockHA.SetServiceError("hue", "activate_scene", errors.New("invalid_format"))
		env.MockHA.SetServiceError("scene", "turn_on", errors.New("boom"))

		manager.activateScene(context.Background(), companionRoom(&ten, true, "Kitchen Under Cabinet"), "morning", "dayPhase")

		assert.Empty(t, env.MockHA.GetServiceCalls())
	})
}

func TestActivateScene_CompanionsStopOnCancelledContext(t *testing.T) {
	t.Parallel()
	env := testutil.NewEnv(t)
	manager := NewManager(context.Background(), env.MockHA, env.StateMgr, &HueConfig{}, env.Logger, false, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	activated := manager.activateCompanionScenes(ctx, companionRoom(nil, false, "A", "B"), "morning", nil)

	assert.Empty(t, activated)
	assert.Empty(t, env.MockHA.GetServiceCalls())
}

func TestLoadConfig_CompanionScenes(t *testing.T) {
	t.Parallel()
	configPath := filepath.Join(t.TempDir(), "hue_config.yaml")
	content := `---
rooms:
  - hue_group: Kitchen
    hass_area_id: kitchen
    conditions:
      - action: on
        variable: isAnyoneHome
        value: true
    companion_scenes: ["Kitchen Under Cabinet"]
  - hue_group: Den
    hass_area_id: den
    conditions:
      - action: on
        variable: isAnyoneHome
        value: true
`
	require.NoError(t, os.WriteFile(configPath, []byte(content), 0o644))

	cfg, err := LoadConfig(configPath)
	require.NoError(t, err)
	require.Len(t, cfg.Rooms, 2)
	assert.Equal(t, []string{"Kitchen Under Cabinet"}, cfg.Rooms[0].CompanionScenes)
	assert.Empty(t, cfg.Rooms[1].CompanionScenes)
}
