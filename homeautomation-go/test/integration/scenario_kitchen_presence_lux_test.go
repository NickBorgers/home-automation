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

func TestScenario_KitchenCameraPresenceOverridesOnlyAutomatedLuxTarget(t *testing.T) {
	// GIVEN: the kitchen follows a low night curve and its camera occupancy variable comes from config.
	t.Log("GIVEN: an unoccupied kitchen follows the night lux curve")
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t0 := time.Date(2026, 11, 16, 22, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(t0)
	config := &lighting.HueConfig{
		LuxCurve: &lighting.LuxCurveConfig{
			Points: []lighting.LuxCurvePoint{
				{Anchor: "test.start", Lux: 10, Kelvin: 3000},
				{Anchor: "test.end", Lux: 10, Kelvin: 3000},
			},
			OutdoorSensor: "sensor.outdoor_lux",
			TickSeconds:   3600,
		},
		Rooms: []lighting.RoomConfig{{
			HueGroup: "Kitchen",
			Conditions: []lighting.LightingCondition{
				{Action: "off", Variable: "isAnyoneHomeAndAwake", Value: false},
				{Action: "on", Variable: "isAnyoneHomeAndAwake", Value: true},
			},
			LuxControl: &lighting.LuxControlConfig{
				Mode:       lighting.LuxModeActive,
				Cans:       "light.kitchen_cans",
				Calibrated: true,
				Calibration: []lighting.CalibrationPoint{
					{BrightnessPct: 1, Lux: 1}, {BrightnessPct: 100, Lux: 100},
				},
				PresenceOverride: &lighting.LuxPresenceOverride{
					Variable:                "isKitchenOccupied",
					Phases:                  []string{"dusk", "night"},
					BrightnessMultiplierPct: 300,
				},
			},
		}},
	}

	require.NoError(t, stateManager.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", false))
	require.NoError(t, stateManager.SetString("dayPhase", "night"))
	server.SetState("sensor.outdoor_lux", "0", nil)
	server.SetState("light.kitchen_cans", "on", map[string]interface{}{
		"brightness": 25.5, "color_temp_kelvin": 3000.0,
	})

	manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	manager.SetClock(mockClock)
	manager.SetAnchorResolver(func(anchor string, _ time.Time) (time.Time, bool) {
		anchors := map[string]time.Time{"test.start": t0, "test.end": t0.Add(time.Hour)}
		at, ok := anchors[anchor]
		return at, ok
	})
	require.NoError(t, manager.Start())
	defer manager.Stop()
	require.NoError(t, stateManager.SetString("dayPhase", "dusk"))
	require.NoError(t, stateManager.SetString("dayPhase", "night"))

	// Guard: in a non-eligible phase the override must NOT apply even when presence is true.
	t.Log("GUARD: presence true in morning phase must leave TargetSource as lux_curve")
	require.NoError(t, stateManager.SetString("dayPhase", "morning"))
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", true))
	client.WaitForHandlers()
	require.Equal(t, "lux_curve", manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetSource,
		"presence override must not apply outside configured phases")
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", false))
	require.NoError(t, stateManager.SetString("dayPhase", "night"))

	// WHEN: camera presence turns on, then repeats the same occupied state.
	t.Log("WHEN: camera presence turns on and bounces while someone works in the kitchen")
	snapshot := server.ServiceCallCount()
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", true))
	require.Eventually(t, func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetSource == "presence_override"
	}, time.Second, 10*time.Millisecond)
	// The curve gives 10 lux, which is 10% on this linear calibration. 300% of that is 30%, which is 30 lux.
	require.InDelta(t, 30.0, manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetLux, 0.01)
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", true))
	client.WaitForHandlers()
	require.Empty(t, filterServiceCalls(server.GetServiceCallsSince(snapshot), "scene", "turn_on"),
		"presence must never recall a day-phase scene")

	// THEN: becoming empty resumes the curve, and a manual hold survives later presence changes.
	t.Log("THEN: empty resumes the curve, while camera bounces never alter a manual hold")
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", false))
	require.Eventually(t, func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetSource == "lux_curve"
	}, time.Second, 10*time.Millisecond)
	require.InDelta(t, 10, manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetLux, 0.01)

	mockClock.AdvanceAndProcess(2 * time.Minute)
	server.SetState("light.kitchen_cans", "on", map[string]interface{}{
		"brightness": 204.0, "color_temp_kelvin": 6500.0,
	})
	client.WaitForHandlers()
	mockClock.AdvanceAndProcess(5 * time.Second) // the fresh read that confirms the manual change
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", true))
	require.Eventually(t, func() bool {
		st := manager.GetShadowState().Outputs.LuxControl["Kitchen"]
		return st.Held && st.TargetSource == "presence_override" && st.Decision == "held: manual change, lights left alone"
	}, time.Second, 10*time.Millisecond)
}

// TestScenario_KitchenPresenceOverridesInDayPhase verifies that adding "day" to the
// presence_override phases causes the Kitchen to brighten during the day phase, while
// the morning phase (not in the list) still follows the lux curve.
func TestScenario_KitchenPresenceOverridesInDayPhase(t *testing.T) {
	// GIVEN: Kitchen has day phase included in its presence override phases.
	t.Log("GIVEN: an unoccupied kitchen with presence_override phases including day")
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t0 := time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(t0)
	config := &lighting.HueConfig{
		LuxCurve: &lighting.LuxCurveConfig{
			Points: []lighting.LuxCurvePoint{
				{Anchor: "test.start", Lux: 10, Kelvin: 3000},
				{Anchor: "test.end", Lux: 10, Kelvin: 3000},
			},
			OutdoorSensor: "sensor.outdoor_lux",
			TickSeconds:   3600,
		},
		Rooms: []lighting.RoomConfig{{
			HueGroup: "Kitchen",
			Conditions: []lighting.LightingCondition{
				{Action: "off", Variable: "isAnyoneHomeAndAwake", Value: false},
				{Action: "on", Variable: "isAnyoneHomeAndAwake", Value: true},
			},
			LuxControl: &lighting.LuxControlConfig{
				Mode:       lighting.LuxModeActive,
				Cans:       "light.kitchen_cans",
				Calibrated: true,
				Calibration: []lighting.CalibrationPoint{
					{BrightnessPct: 1, Lux: 1}, {BrightnessPct: 100, Lux: 100},
				},
				PresenceOverride: &lighting.LuxPresenceOverride{
					Variable:                "isKitchenOccupied",
					Phases:                  []string{"day", "sunset", "dusk", "winddown", "night"},
					BrightnessMultiplierPct: 200,
				},
			},
		}},
	}

	require.NoError(t, stateManager.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", false))
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	server.SetState("sensor.outdoor_lux", "0", nil)
	server.SetState("light.kitchen_cans", "on", map[string]interface{}{
		"brightness": 25.5, "color_temp_kelvin": 3000.0,
	})

	manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	manager.SetClock(mockClock)
	manager.SetAnchorResolver(func(anchor string, _ time.Time) (time.Time, bool) {
		anchors := map[string]time.Time{"test.start": t0.Add(-time.Hour), "test.end": t0.Add(time.Hour)}
		at, ok := anchors[anchor]
		return at, ok
	})
	require.NoError(t, manager.Start())
	defer manager.Stop()

	// Guard: morning phase must NOT trigger the override.
	t.Log("GUARD: presence true in morning phase must leave TargetSource as lux_curve")
	require.NoError(t, stateManager.SetString("dayPhase", "morning"))
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", true))
	client.WaitForHandlers()
	require.Equal(t, "lux_curve", manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetSource,
		"presence override must not apply in morning phase")
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", false))

	// WHEN: camera presence turns on during the day phase.
	t.Log("WHEN: kitchen presence turns on during day phase")
	require.NoError(t, stateManager.SetString("dayPhase", "day"))
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", true))
	require.Eventually(t, func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetSource == "presence_override"
	}, time.Second, 10*time.Millisecond)

	// THEN: clearing occupancy resumes the lux curve.
	t.Log("THEN: clearing presence returns to lux_curve")
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", false))
	require.Eventually(t, func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetSource == "lux_curve"
	}, time.Second, 10*time.Millisecond)
}
