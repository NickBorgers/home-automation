package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

// The Kitchen strip is dim-to-warm: brighter is cooler. These points were matched by eye on 2026-11-08.
var stripColorMatchCurve = []lighting.StripCCTPoint{
	{BrightnessPct: 49, Kelvin: 3000}, {BrightnessPct: 76, Kelvin: 3250}, {BrightnessPct: 89, Kelvin: 3500},
	{BrightnessPct: 95, Kelvin: 3750}, {BrightnessPct: 100, Kelvin: 4000},
}

// runStripColorMatch starts a Kitchen whose cans follow a flat curve at curveLux and kelvin, and returns
// the brightness the plugin sends to the dim-to-warm under-cabinet strip.
func runStripColorMatch(t *testing.T, curveLux float64, kelvin int) float64 {
	t.Helper()
	server, client, stateManager, cleanup := setupTest(t)
	t.Cleanup(cleanup)

	t0 := time.Date(2026, 11, 18, 18, 55, 0, 0, time.UTC)
	config := &lighting.HueConfig{
		LuxCurve: &lighting.LuxCurveConfig{
			Points: []lighting.LuxCurvePoint{
				{Anchor: "test.start", Lux: curveLux, Kelvin: kelvin},
				{Anchor: "test.end", Lux: curveLux, Kelvin: kelvin},
			},
			OutdoorSensor: "sensor.outdoor_lux",
			TickSeconds:   3600,
		},
		Rooms: []lighting.RoomConfig{{
			HueGroup: "Kitchen",
			Conditions: []lighting.LightingCondition{
				{Action: "on", Variable: "isAnyoneHomeAndAwake", Value: true},
			},
			LuxControl: &lighting.LuxControlConfig{
				Mode:        lighting.LuxModeActive,
				Cans:        "light.kitchen_cans",
				Calibrated:  true,
				Calibration: []lighting.CalibrationPoint{{BrightnessPct: 1, Lux: 1}, {BrightnessPct: 100, Lux: 100}},
				FollowerLights: []lighting.LuxFollower{{
					Entity: "light.kitchen_main_lights", FloorPct: 20, Gain: 0.8, CCTCurve: stripColorMatchCurve,
				}},
			},
		}},
	}

	require.NoError(t, stateManager.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, stateManager.SetString("dayPhase", "dusk"))
	server.SetState("sensor.outdoor_lux", "0", nil)
	server.SetState("light.kitchen_cans", "off", nil)
	server.SetState("light.kitchen_main_lights", "off", nil)

	manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	manager.SetClock(clock.NewMockClock(t0))
	manager.SetAnchorResolver(func(anchor string, _ time.Time) (time.Time, bool) {
		anchors := map[string]time.Time{"test.start": t0.Add(-time.Hour), "test.end": t0.Add(time.Hour)}
		at, ok := anchors[anchor]
		return at, ok
	})
	require.NoError(t, manager.Start())
	t.Cleanup(manager.Stop)

	var pct float64
	require.Eventually(t, func() bool {
		for _, call := range filterServiceCalls(server.GetServiceCallsSince(0), "light", "turn_on") {
			if call.ServiceData["entity_id"] == "light.kitchen_main_lights" {
				_, err := fmt.Sscan(fmt.Sprint(call.ServiceData["brightness_pct"]), &pct)
				return err == nil
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "the strip gets a brightness")
	return pct
}

// User story: "The under-cabinet strip should look the same color as the cans."
// Invariant: a dim-to-warm strip is never brighter than the brightness that matches the cans' Kelvin,
// because a brighter strip is a cooler strip.
func TestScenario_KitchenStripIsNeverBrighterThanItsColorMatch(t *testing.T) {
	t.Run("warm target", func(t *testing.T) {
		// GIVEN: the cans at 46% and 3000 K, as on 2026-11-18 at 18:55. The brightness curve alone gives 57%.
		t.Log("GIVEN: the cans target 3000 K at 46%")
		// WHEN: the plugin drives the strip.
		// THEN: the strip stops at 49%, its 3000 K brightness, so it is not cooler than the cans.
		t.Log("THEN: the strip is at most its 3000 K brightness")
		require.Equal(t, 49.0, runStripColorMatch(t, 46, 3000))
	})
	t.Run("cool target", func(t *testing.T) {
		// GIVEN: the cans at 90% and 3250 K. The brightness curve alone gives 92%.
		t.Log("GIVEN: the cans target 3250 K at 90%")
		// THEN: the strip stops at 76%, its 3250 K brightness.
		t.Log("THEN: the strip is at its 3250 K brightness")
		require.Equal(t, 76.0, runStripColorMatch(t, 90, 3250))
	})
	t.Run("dim warm target", func(t *testing.T) {
		// GIVEN: the cans at 10% and 3000 K. The brightness curve gives 28%, below the 3000 K brightness.
		t.Log("GIVEN: the cans target 3000 K at 10%")
		// THEN: the brightness curve still decides below the color match.
		t.Log("THEN: the strip follows the brightness curve")
		require.Equal(t, 28.0, runStripColorMatch(t, 10, 3000))
	})
}
