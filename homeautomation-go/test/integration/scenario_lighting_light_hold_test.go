package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/testlogger"
	"homeautomation/pkg/lighthold"

	"github.com/stretchr/testify/require"
)

func TestScenario_LightingOffRespectsLightHold(t *testing.T) {
	triggers := []struct {
		name         string
		resolveFirst bool
		trigger      func(*MockHAServer, *lighting.Manager)
	}{
		{"sun event", true, func(server *MockHAServer, _ *lighting.Manager) {
			server.SetState("input_text.sun_event", "night", nil)
		}},
		{"day phase", true, func(server *MockHAServer, _ *lighting.Manager) {
			server.SetState("input_text.day_phase", "night", nil)
		}},
		{"condition variable", false, func(server *MockHAServer, _ *lighting.Manager) {
			server.SetState("input_boolean.anyone_home", "off", nil)
		}},
		{"reset", true, func(_ *MockHAServer, manager *lighting.Manager) {
			require.NoError(t, manager.Reset())
		}},
	}

	for _, tt := range triggers {
		t.Run(tt.name, func(t *testing.T) {
			server, client, stateManager, cleanup := setupTest(t)
			defer cleanup()

			server.SetRegistries([]map[string]interface{}{
				{"entity_id": "light.example_path", "area_id": "example_yard"},
				{"entity_id": "light.example_wall", "area_id": "example_yard"},
			}, nil)
			config := &lighting.HueConfig{Rooms: []lighting.RoomConfig{{
				HueGroup:   "Example Yard",
				HASSAreaID: "example_yard",
				Conditions: []lighting.LightingCondition{{Action: "off", Variable: "isAnyoneHome", Value: false}},
			}}}
			manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
			start := time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC)
			mockClock := clock.NewMockClock(start)
			manager.SetClock(mockClock)
			holds := lighthold.NewHub()
			manager.SetLightHolds(holds)
			require.NoError(t, manager.Start())
			defer manager.Stop()

			// GIVEN: another plugin holds one light on for five minutes while the room resolves to off.
			server.SetState("input_text.day_phase", "evening", nil)
			server.SetState("input_text.sun_event", "dusk", nil)
			server.SetState("input_boolean.anyone_home", "on", nil)
			waitForProcessing(t, stateManager)
			holds.Hold("light.example_path", "example-plugin", start.Add(5*time.Minute))

			// WHEN: the room is re-evaluated and resolves to off.
			if tt.resolveFirst {
				server.SetState("input_boolean.anyone_home", "off", nil)
				waitForProcessing(t, stateManager)
			}
			snapshot := server.ServiceCallCount()
			tt.trigger(server, manager)
			waitForServiceCallSince(t, server, snapshot, "light", "turn_off", "other room lights should turn off")

			// THEN: the held light is never covered, while the room's other light is turned off explicitly.
			assertTurnOffTargets(t, server.GetServiceCallsSince(snapshot), "light.example_path", false)
			assertTurnOffTargets(t, server.GetServiceCallsSince(snapshot), "light.example_wall", true)

			// AND WHEN: the clock passes the hold end, the next evaluation may cover that light again.
			mockClock.Advance(6 * time.Minute)
			snapshot = server.ServiceCallCount()
			require.NoError(t, manager.Reset())
			waitForServiceCallSince(t, server, snapshot, "light", "turn_off", "released light should turn off")
			assertTurnOffTargets(t, server.GetServiceCallsSince(snapshot), "light.example_path", true)
		})
	}
}

func assertTurnOffTargets(t *testing.T, calls []ServiceCall, entityID string, want bool) {
	t.Helper()
	found := false
	for _, call := range filterServiceCalls(calls, "light", "turn_off") {
		if call.ServiceData["area_id"] == "example_yard" {
			found = true
		}
		switch entities := call.ServiceData["entity_id"].(type) {
		case string:
			found = found || entities == entityID
		case []interface{}:
			for _, entity := range entities {
				found = found || entity == entityID
			}
		}
	}
	require.Equal(t, want, found)
}
