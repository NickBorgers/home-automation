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

func TestScenario_LuxOverlappingTransitionsDoNotCreateFalseManualHold(t *testing.T) {
	// GIVEN: the lux loop is changing the cans from 3600 K to 3200 K in back-to-back 60 s fades.
	t.Log("GIVEN: the lux loop sends back-to-back 60-second color transitions")
	server, client, stateManager, baseCleanup := setupTest(t)
	defer baseCleanup()

	t0 := time.Date(2026, 11, 13, 1, 21, 30, 0, time.UTC)
	mockClock := clock.NewMockClock(t0)
	config := &lighting.HueConfig{
		LuxCurve: &lighting.LuxCurveConfig{
			Points: []lighting.LuxCurvePoint{
				{Anchor: "test.start", Lux: 62, Kelvin: 3600},
				{Anchor: "test.next", Lux: 62, Kelvin: 3200},
				{Anchor: "test.later", Lux: 62, Kelvin: 3000},
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
				Mode:       lighting.LuxModeActive,
				Cans:       "light.living_cans",
				Calibrated: true,
				Calibration: []lighting.CalibrationPoint{
					{BrightnessPct: 1, Lux: 1}, {BrightnessPct: 100, Lux: 100},
				},
				DaylightFactor: 0,
			},
		}},
	}

	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	server.SetState("sensor.outdoor_lux", "0", nil)
	server.SetState("light.living_cans", "on", map[string]interface{}{
		"brightness":        153.0, // 60%, so the first tick sends the 62% target.
		"color_temp_kelvin": 4000.0,
	})

	manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	manager.SetClock(mockClock)
	manager.SetAnchorResolver(func(anchor string, _ time.Time) (time.Time, bool) {
		anchors := map[string]time.Time{
			"test.start": t0,
			"test.next":  t0.Add(time.Minute),
			"test.later": t0.Add(2 * time.Minute),
		}
		at, ok := anchors[anchor]
		return at, ok
	})
	require.NoError(t, manager.Start())
	defer manager.Stop()

	require.Eventually(t, func() bool {
		return server.ServiceCallCount() >= 1
	}, time.Second, 10*time.Millisecond, "command A should be sent")

	mockClock.AdvanceAndProcess(time.Minute)
	require.Eventually(t, func() bool {
		return server.ServiceCallCount() >= 2
	}, time.Second, 10*time.Millisecond, "command B should be sent")

	// WHEN: Hue reports an intermediate value from command A just after command B was sent.
	t.Log("WHEN: Hue reports the prior transition's intermediate color at T+60.954s")
	mockClock.AdvanceAndProcess(954 * time.Millisecond)
	server.SetState("light.living_cans", "on", map[string]interface{}{
		"brightness":        158.0,
		"color_temp_kelvin": 3821.0,
	})
	client.WaitForHandlers()

	// THEN: the controller's own delayed report must never create a manual hold.
	t.Log("THEN: the delayed report does not hold the room, and the next lux command is sent")
	mockClock.AdvanceAndProcess(61 * time.Second)
	require.Eventually(t, func() bool {
		return server.ServiceCallCount() >= 3
	}, time.Second, 10*time.Millisecond, "the lux loop must continue after its own delayed report")
	require.False(t, manager.GetShadowState().Outputs.LuxControl["Living Room"].Held)

	// WHEN/THEN: after command B settles, a genuine color change still creates a hold.
	t.Log("WHEN: a materially different color is reported after command B settles")
	server.SetState("light.living_cans", "on", map[string]interface{}{
		"brightness":        158.0,
		"color_temp_kelvin": 6500.0,
	})
	client.WaitForHandlers()
	mockClock.AdvanceAndProcess(time.Minute)
	require.Eventually(t, func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Living Room"].Held
	}, time.Second, 10*time.Millisecond, "a real manual change must hold the room")
}
