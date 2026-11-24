package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScenario_Lighting_AutomationReportDoesNotCreateManualHold(t *testing.T) {
	server, client, stateManager, cleanup := setupTest(t)
	defer cleanup()

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(now)
	config := &lighting.HueConfig{
		LuxCurve: &lighting.LuxCurveConfig{
			Points:        []lighting.LuxCurvePoint{{Anchor: "noon", Lux: 100, Kelvin: 3386}},
			OutdoorSensor: "sensor.outdoor_lux",
			DarkBelowLux:  10,
			LightAboveLux: 20,
		},
		Rooms: []lighting.RoomConfig{{
			HueGroup: "Example Mudroom",
			Conditions: []lighting.LightingCondition{
				{Action: "on", Variable: "isAnyoneHome", Value: true},
			},
			LuxControl: &lighting.LuxControlConfig{
				Mode:           lighting.LuxModeActive,
				Cans:           "light.example_mudroom",
				DaylightFactor: 0.25,
				Calibrated:     true,
				Calibration: []lighting.CalibrationPoint{
					{BrightnessPct: 1, Lux: 1},
					{BrightnessPct: 100, Lux: 100},
				},
			},
		}},
	}

	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	server.SetState("sensor.outdoor_lux", "100", nil)
	server.SetState("light.example_mudroom", "off", nil)
	server.SetState("input_boolean.lux_hold_example_mudroom", "off", nil)

	manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	manager.SetClock(mockClock)
	manager.SetAnchorResolver(func(_ string, _ time.Time) (time.Time, bool) { return now, true })
	require.NoError(t, manager.Start())
	defer manager.Stop()

	require.Eventually(t, func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Example Mudroom"].BrightnessPct > 0
	}, stateWaitTimeout, 10*time.Millisecond, "GIVEN: lux control has commanded the room on")

	t.Log("WHEN: a known automation reports the room off after the automated turn-on")
	server.SetStateWithContext("light.example_mudroom", "on",
		map[string]interface{}{"brightness": 191.0, "color_temp_kelvin": 3386.0},
		&StateContext{ID: "automation-on", ParentID: "automation-trigger"})
	mockClock.Advance(2 * time.Minute)
	server.SetStateWithContext("light.example_mudroom", "off", nil,
		&StateContext{ID: "automation-off", ParentID: "automation-trigger"})

	assert.Never(t, func() bool {
		return server.GetState("input_boolean.lux_hold_example_mudroom").State == "on"
	}, 2*time.Second, 10*time.Millisecond,
		"THEN: an automation-originated report must not create a manual hold")

	t.Log("WHEN: the room is on again and a Home Assistant user turns it off")
	server.SetStateWithContext("light.example_mudroom", "on",
		map[string]interface{}{"brightness": 191.0, "color_temp_kelvin": 3386.0},
		&StateContext{ID: "automation-restored", ParentID: "automation-trigger"})
	mockClock.Advance(2 * time.Second)
	server.SetStateWithContext("light.example_mudroom", "off", nil,
		&StateContext{ID: "user-off", UserID: "example-user"})

	assert.Eventually(t, func() bool {
		return server.GetState("input_boolean.lux_hold_example_mudroom").State == "on"
	}, stateWaitTimeout, 10*time.Millisecond,
		"THEN: a genuine user turn-off must create a hold")
}
