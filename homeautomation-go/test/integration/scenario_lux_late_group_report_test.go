package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

// User story: "The lights fade on their own. I did not touch them, so the system must keep
// following the curve. When I do change them, it must leave them alone."
//
// Invariant: a single late brightness report from a light group, off the fade path, does not hold
// the room when a fresh read a few seconds later is back on the path. A change that stays off the
// path still holds the room.
func TestScenario_LuxLateGroupReportDoesNotHoldButManualChangeDoes(t *testing.T) {
	// GIVEN: the plugin faded the cans from 96% to 75% and the fade ended long ago
	t.Log("GIVEN: the plugin's fade to 75% ended two minutes ago")
	server, client, stateManager, baseCleanup := setupTest(t)
	defer baseCleanup()

	t0 := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(t0)
	config := &lighting.HueConfig{
		LuxCurve: &lighting.LuxCurveConfig{
			Points: []lighting.LuxCurvePoint{
				{Anchor: "test.start", Lux: 75, Kelvin: 3000},
				{Anchor: "test.end", Lux: 75, Kelvin: 3000},
			},
			OutdoorSensor: "sensor.outdoor_lux",
			TickSeconds:   60,
		},
		Rooms: []lighting.RoomConfig{{
			HueGroup: "Living Room",
			Conditions: []lighting.LightingCondition{{
				Action: "on", Variable: "isAnyoneHome", Value: true,
			}},
			LuxControl: &lighting.LuxControlConfig{
				Mode:        lighting.LuxModeActive,
				Cans:        "light.living_cans",
				Calibrated:  true,
				Calibration: []lighting.CalibrationPoint{{BrightnessPct: 1, Lux: 1}, {BrightnessPct: 100, Lux: 100}},
			},
		}},
	}

	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	server.SetState("sensor.outdoor_lux", "0", nil)
	server.SetState("input_boolean.lux_hold_living_room", "off", nil)
	server.SetState("light.living_cans", "on", map[string]interface{}{"brightness": 245.0, "color_temp_kelvin": 3000.0})

	manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	manager.SetClock(mockClock)
	manager.SetAnchorResolver(func(anchor string, _ time.Time) (time.Time, bool) {
		anchors := map[string]time.Time{"test.start": t0.Add(-time.Hour), "test.end": t0.Add(time.Hour)}
		at, ok := anchors[anchor]
		return at, ok
	})
	require.NoError(t, manager.Start())
	defer manager.Stop()

	require.Eventually(t, func() bool {
		return len(filterServiceCalls(server.GetServiceCalls(), "light", "turn_on")) >= 1
	}, time.Second, 10*time.Millisecond, "the plugin fades the cans to 75%")
	report := func(brightness float64) {
		server.SetState("light.living_cans", "on", map[string]interface{}{"brightness": brightness, "color_temp_kelvin": 3000.0})
		client.WaitForHandlers()
	}
	report(191) // 75%, as commanded
	mockClock.AdvanceAndProcess(3 * time.Minute)
	held := func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Living Room"].Held
	}
	holdHelperTurnedOn := func() bool {
		for _, call := range filterServiceCalls(server.GetServiceCalls(), "input_boolean", "turn_on") {
			if call.ServiceData["entity_id"] == "input_boolean.lux_hold_living_room" {
				return true
			}
		}
		return false
	}

	// WHEN: one late report lands 8 points past the target, and the light reads 76% a few seconds later
	t.Log("WHEN: a late report shows 83%, then the light settles back to 75%")
	report(212) // 83%: off the fade path
	mockClock.AdvanceAndProcess(3 * time.Second)
	report(194)                                  // 76%: back within tolerance
	mockClock.AdvanceAndProcess(3 * time.Second) // the confirming read
	report(191)
	mockClock.AdvanceAndProcess(time.Minute)

	// THEN: the room is not held
	t.Log("THEN: the room is not held")
	require.False(t, held(), "a late report the light does not confirm must not hold the room")
	require.False(t, holdHelperTurnedOn())

	// WHEN: a person turns the cans down to 40% and leaves them there
	t.Log("WHEN: a person turns the cans down to 40%")
	report(102)
	mockClock.AdvanceAndProcess(6 * time.Second) // the confirming read

	// THEN: the room is held, and the hold is stored in Home Assistant
	t.Log("THEN: the room is held")
	require.Eventually(t, func() bool {
		mockClock.AdvanceAndProcess(time.Minute) // shadow state records the hold on the next tick
		return held()
	}, 2*time.Second, 50*time.Millisecond, "a person's change must hold the room")
	require.True(t, holdHelperTurnedOn())
}
