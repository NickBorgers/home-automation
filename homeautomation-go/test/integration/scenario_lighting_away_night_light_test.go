package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

// Story: "While nobody is home and it is dark outside, I want a few chosen lights to stay on at a very
// low level instead of off, so the house is not pitch black. When it gets light, or someone is home,
// the room behaves as before."
//
// Invariants:
//   - While the night light is active, no turn-off covers a night light. That includes a light group
//     (here light.example_den) that contains a night light, and the turn-off of another room (here
//     Example Nook, whose light group also contains the night light).
//   - The night lights are turned on at the configured brightness and color temperature.
//   - The night light is never active while someone is home, even when everyone home is asleep.
func TestScenario_LightingAwayNightLight(t *testing.T) {
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	server.SetRegistries([]map[string]interface{}{
		{"entity_id": "light.example_den", "area_id": "example_den"},
		{"entity_id": "light.example_can_a", "area_id": "example_den"},
		{"entity_id": "light.example_can_b", "area_id": "example_den"},
		{"entity_id": "light.example_lamp", "area_id": "example_den"},
		{"entity_id": "light.example_nook", "area_id": "example_nook"},
		{"entity_id": "light.example_nook_lamp", "area_id": "example_nook"},
	}, nil)
	// light.example_den is a light group that holds both cans, like a Hue room.
	server.SetState("light.example_den", "off", map[string]interface{}{
		"is_hue_group": true,
		"entity_id":    []interface{}{"light.example_can_a", "light.example_can_b"},
	})
	// The neighboring room's light group also holds the night light.
	server.SetState("light.example_nook", "off", map[string]interface{}{
		"is_hue_group": true,
		"entity_id":    []interface{}{"light.example_can_a", "light.example_nook_lamp"},
	})

	config := &lighting.HueConfig{Rooms: []lighting.RoomConfig{{
		HueGroup:   "Example Den",
		HASSAreaID: "example_den",
		Conditions: []lighting.LightingCondition{
			{Action: "off", Variable: "isAnyoneHomeAndAwake", Value: false},
		},
		AwayNightLight: &lighting.AwayNightLightConfig{
			Lights:        []string{"light.example_can_a"},
			BrightnessPct: 1,
			Kelvin:        3000,
		},
	}, {
		HueGroup:   "Example Nook",
		HASSAreaID: "example_nook",
		Conditions: []lighting.LightingCondition{
			{Action: "off", Variable: "isAnyoneHomeAndAwake", Value: false},
		},
	}}}
	manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	manager.SetDebounceDelay(0)
	require.NoError(t, manager.Start())
	defer manager.Stop()

	// GIVEN: nobody is home, and it is light outside.
	t.Log("GIVEN: nobody is home during the day")
	server.SetState("input_text.day_phase", "day", nil)
	server.SetState("input_boolean.anyone_home_and_awake", "off", nil)
	server.SetState("input_boolean.anyone_home", "off", nil)
	server.SetState("input_boolean.dark_outside", "off", nil)
	waitForProcessing(t, stateManager)

	// WHEN: it gets dark outside.
	t.Log("WHEN: it gets dark outside while nobody is home")
	snapshot := server.ServiceCallCount()
	server.SetState("input_boolean.dark_outside", "on", nil)
	waitForServiceCallSince(t, server, snapshot, "light", "turn_on", "the night light should turn on")

	// THEN: the night light turns on at the configured level; the other room lights turn off.
	t.Log("THEN: the night light is on at 1% and 3000 K, other lights off")
	assertNightLightOn(t, server.GetServiceCallsSince(snapshot), "light.example_can_a", 1, 3000)
	assertNightLightNotTurnedOff(t, server.GetServiceCallsSince(snapshot))
	assertTurnOffCovers(t, server.GetServiceCallsSince(snapshot), "light.example_can_b", true)
	assertTurnOffCovers(t, server.GetServiceCallsSince(snapshot), "light.example_lamp", true)

	// WHEN: the day phase changes while the night light is active, so the room is turned off again.
	t.Log("WHEN: the day phase changes while nobody is home in the dark")
	snapshot = server.ServiceCallCount()
	server.SetState("input_text.day_phase", "night", nil)
	waitForServiceCallSince(t, server, snapshot, "light", "turn_off", "the other lights should turn off")
	require.Eventually(t, func() bool {
		return len(filterServiceCalls(server.GetServiceCallsSince(snapshot), "light", "turn_off")) >= 2
	}, 5*time.Second, 20*time.Millisecond, "both rooms should turn off")

	// THEN: the room's other lights turn off one by one, and the night light stays on.
	t.Log("THEN: the other lights turn off and the night light stays on")
	calls := server.GetServiceCallsSince(snapshot)
	assertNightLightNotTurnedOff(t, calls)
	assertTurnOffCovers(t, calls, "light.example_can_b", true)
	assertTurnOffCovers(t, calls, "light.example_lamp", true)
	assertTurnOffCovers(t, calls, "light.example_nook_lamp", true)
	assertNightLightOn(t, calls, "light.example_can_a", 1, 3000)

	// WHEN: it gets light outside while nobody is home.
	t.Log("WHEN: it gets light outside")
	snapshot = server.ServiceCallCount()
	server.SetState("input_boolean.dark_outside", "off", nil)
	waitForServiceCallSince(t, server, snapshot, "light", "turn_off", "the room should turn off")

	// THEN: the night light turns off with the rest of the room.
	t.Log("THEN: the night light turns off")
	require.True(t, turnOffCovers(server.GetServiceCallsSince(snapshot), "light.example_can_a"),
		"the night light should turn off when it gets light")

	// WHEN: it is dark again, but someone is home and asleep.
	t.Log("WHEN: someone is home and asleep in the dark")
	server.SetState("input_boolean.anyone_home", "on", nil)
	waitForProcessing(t, stateManager)
	snapshot = server.ServiceCallCount()
	server.SetState("input_boolean.dark_outside", "on", nil)
	waitForServiceCallSince(t, server, snapshot, "light", "turn_off", "the room should turn off")

	// THEN: the night light is not used. The whole room turns off.
	t.Log("THEN: the night light stays off while someone is home")
	calls = server.GetServiceCallsSince(snapshot)
	require.True(t, turnOffCovers(calls, "light.example_can_a"), "the night light should turn off while someone is home")
	require.Empty(t, filterServiceCalls(calls, "light", "turn_on"), "the night light must not turn on while someone is home")
}

// turnOffCovers reports whether a light.turn_off call reaches entityID: by name, or through a room
// area or light group. Every area and light group in this test holds the night light.
func turnOffCovers(calls []ServiceCall, entityID string) bool {
	for _, call := range filterServiceCalls(calls, "light", "turn_off") {
		if call.ServiceData["area_id"] != nil {
			return true
		}
		for _, entity := range serviceEntities(call) {
			if entity == entityID || entity == "light.example_den" || entity == "light.example_nook" {
				return true
			}
		}
	}
	return false
}

func assertTurnOffCovers(t *testing.T, calls []ServiceCall, entityID string, want bool) {
	t.Helper()
	require.Equal(t, want, turnOffCovers(calls, entityID), "turn-off coverage of %s", entityID)
}

func assertNightLightNotTurnedOff(t *testing.T, calls []ServiceCall) {
	t.Helper()
	require.False(t, turnOffCovers(calls, "light.example_can_a"), "no turn-off may cover the night light")
}

func assertNightLightOn(t *testing.T, calls []ServiceCall, entityID string, pct, kelvin float64) {
	t.Helper()
	for _, call := range filterServiceCalls(calls, "light", "turn_on") {
		for _, entity := range serviceEntities(call) {
			if entity != entityID {
				continue
			}
			require.EqualValues(t, pct, call.ServiceData["brightness_pct"])
			require.EqualValues(t, kelvin, call.ServiceData["color_temp_kelvin"])
			return
		}
	}
	require.Failf(t, "night light not turned on", "no light.turn_on call for %s", entityID)
}

func serviceEntities(call ServiceCall) []string {
	switch entities := call.ServiceData["entity_id"].(type) {
	case string:
		return []string{entities}
	case []string:
		return entities
	case []interface{}:
		out := make([]string, 0, len(entities))
		for _, entity := range entities {
			if s, ok := entity.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
