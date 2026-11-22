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

// TestScenario_PantryPresenceOverridesOnlyPantryLuxTarget verifies that isPantryOccupied brightens
// only the Pantry and leaves the Kitchen on its curve (cross-room isolation invariant).
func TestScenario_PantryPresenceOverridesOnlyPantryLuxTarget(t *testing.T) {
	// GIVEN: Pantry and Kitchen each follow a low night curve; only Pantry has isPantryOccupied.
	t.Log("GIVEN: an unoccupied Pantry follows the night lux curve alongside an unoccupied Kitchen")
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	t0 := time.Date(2026, 11, 22, 22, 0, 0, 0, time.UTC)
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
		Rooms: []lighting.RoomConfig{
			{
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
			},
			{
				HueGroup: "Pantry",
				Conditions: []lighting.LightingCondition{
					{Action: "off", Variable: "isAnyoneHomeAndAwake", Value: false},
					{Action: "on", Variable: "isAnyoneHomeAndAwake", Value: true},
				},
				LuxControl: &lighting.LuxControlConfig{
					Mode:       lighting.LuxModeActive,
					Cans:       "light.pantry",
					Calibrated: true,
					Calibration: []lighting.CalibrationPoint{
						{BrightnessPct: 1, Lux: 1}, {BrightnessPct: 100, Lux: 100},
					},
					PresenceOverride: &lighting.LuxPresenceOverride{
						Variable:                "isPantryOccupied",
						Phases:                  []string{"dusk", "winddown", "night"},
						BrightnessMultiplierPct: 200,
					},
				},
			},
		},
	}

	require.NoError(t, stateManager.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, stateManager.SetBool("isKitchenOccupied", false))
	require.NoError(t, stateManager.SetBool("isPantryOccupied", false))
	require.NoError(t, stateManager.SetString("dayPhase", "night"))
	server.SetState("sensor.outdoor_lux", "0", nil)
	server.SetState("light.kitchen_cans", "on", map[string]interface{}{
		"brightness": 25.5, "color_temp_kelvin": 3000.0,
	})
	server.SetState("light.pantry", "on", map[string]interface{}{
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

	// Guard: in a non-eligible phase the Pantry override must NOT apply even when presence is true.
	t.Log("GUARD: Pantry presence true in morning phase must leave TargetSource as lux_curve")
	require.NoError(t, stateManager.SetString("dayPhase", "morning"))
	require.NoError(t, stateManager.SetBool("isPantryOccupied", true))
	client.WaitForHandlers()
	require.Equal(t, "lux_curve", manager.GetShadowState().Outputs.LuxControl["Pantry"].TargetSource,
		"pantry presence override must not apply outside configured phases")
	require.NoError(t, stateManager.SetBool("isPantryOccupied", false))
	require.NoError(t, stateManager.SetString("dayPhase", "night"))

	// WHEN: Pantry camera presence turns on.
	t.Log("WHEN: Pantry camera presence turns on")
	require.NoError(t, stateManager.SetBool("isPantryOccupied", true))
	require.Eventually(t, func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Pantry"].TargetSource == "presence_override"
	}, time.Second, 10*time.Millisecond)

	// THEN: Pantry brightens; Kitchen stays on its curve (cross-room isolation).
	t.Log("THEN: Pantry is brightened; Kitchen stays on lux_curve")
	require.Equal(t, "lux_curve", manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetSource,
		"Kitchen must not brighten when only isPantryOccupied is true")

	// THEN: clearing isPantryOccupied returns the Pantry to the curve.
	t.Log("THEN: clearing presence returns Pantry to lux_curve")
	require.NoError(t, stateManager.SetBool("isPantryOccupied", false))
	require.Eventually(t, func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Pantry"].TargetSource == "lux_curve"
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, "lux_curve", manager.GetShadowState().Outputs.LuxControl["Kitchen"].TargetSource,
		"Kitchen must remain on lux_curve throughout")
}
