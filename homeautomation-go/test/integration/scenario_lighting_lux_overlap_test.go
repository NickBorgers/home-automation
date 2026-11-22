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
		"brightness":        158.0, // 62%, the target, so only the color changes and fades keep 60 s.
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
	// The hold is confirmed by a fresh read one confirmation delay later. Advance in two
	// steps so the hold check completes before the lux tick fires; otherwise the tick can
	// run luxEvaluate while the check is still pending, writing Held=false to shadow state.
	t.Log("THEN: the confirming read 5s later finds the light still off the path")
	mockClock.AdvanceAndProcess(6 * time.Second)  // luxHoldConfirmDelay(5s) + 1s margin
	mockClock.AdvanceAndProcess(54 * time.Second) // advance into the next lux tick so shadow state reflects the hold
	require.Eventually(t, func() bool {
		return manager.GetShadowState().Outputs.LuxControl["Living Room"].Held
	}, time.Second, 10*time.Millisecond, "a real manual change must hold the room")
}

func TestScenario_LuxFadeUpOverlapGuardEndsAfter31Seconds(t *testing.T) {
	// GIVEN: cans 30 percentage points below target, so the lux loop sends a 30 s fade-up.
	t.Log("GIVEN: the lux loop sends back-to-back 30-second brightness fade-ups")
	server, client, stateManager, baseCleanup := setupTest(t)
	defer baseCleanup()

	t0 := time.Date(2026, 11, 13, 1, 21, 30, 0, time.UTC)
	mockClock := clock.NewMockClock(t0)
	config := &lighting.HueConfig{
		LuxCurve: &lighting.LuxCurveConfig{
			Points: []lighting.LuxCurvePoint{
				{Anchor: "test.start", Lux: 80, Kelvin: 4000},
				{Anchor: "test.next", Lux: 90, Kelvin: 4000},
				{Anchor: "test.later", Lux: 100, Kelvin: 4000},
			},
			OutdoorSensor: "sensor.outdoor_lux",
			TickSeconds:   30,
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
	server.SetState("input_boolean.lux_hold_living_room", "off", nil)
	server.SetState("light.living_cans", "on", map[string]interface{}{
		"brightness":        77.0, // about 30%; the first command rises by the 25pp maximum step.
		"color_temp_kelvin": 4000.0,
	})

	manager := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	manager.SetClock(mockClock)
	manager.SetAnchorResolver(func(anchor string, _ time.Time) (time.Time, bool) {
		anchors := map[string]time.Time{
			"test.start": t0,
			"test.next":  t0.Add(30 * time.Second),
			"test.later": t0.Add(time.Minute),
		}
		at, ok := anchors[anchor]
		return at, ok
	})
	require.NoError(t, manager.Start())
	defer manager.Stop()

	require.Eventually(t, func() bool {
		return len(filterServiceCalls(server.GetServiceCallsSince(0), "light", "turn_on")) >= 1
	}, time.Second, 10*time.Millisecond, "command A should be sent")
	firstCall := filterServiceCalls(server.GetServiceCallsSince(0), "light", "turn_on")[0]
	require.Equal(t, 30.0, firstCall.ServiceData["transition"], "command A must exercise the fade-up path")

	mockClock.AdvanceAndProcess(30 * time.Second)
	require.Eventually(t, func() bool {
		return len(filterServiceCalls(server.GetServiceCallsSince(0), "light", "turn_on")) >= 2
	}, time.Second, 10*time.Millisecond, "command B should be sent")
	secondCall := filterServiceCalls(server.GetServiceCallsSince(0), "light", "turn_on")[1]
	require.Equal(t, 30.0, secondCall.ServiceData["transition"], "command B must also be a fade-up")

	// WHEN: Hue reports an intermediate value from command A within its 31 s guard window.
	t.Log("WHEN: Hue reports command A's intermediate brightness at T+30.954s")
	mockClock.AdvanceAndProcess(954 * time.Millisecond)
	server.SetState("light.living_cans", "on", map[string]interface{}{
		"brightness":        115.0, // about 45%, on command A's path but behind command B.
		"color_temp_kelvin": 4000.0,
	})
	client.WaitForHandlers()

	// THEN: the delayed report is automation overlap, not a manual change.
	t.Log("THEN: a report within transition + 1s does not hold the room")
	held := func() bool {
		state := server.GetState("input_boolean.lux_hold_living_room")
		return state != nil && state.State == "on"
	}
	require.False(t, held())

	// WHEN/THEN: outside the window, hold detection resumes.
	// Both brightness (115 ≈ 45%, below command B's target) and color_temp (6500K vs 4000K) diverge
	// from the automation's expected state — either dimension is sufficient to trigger the hold.
	t.Log("WHEN: a report with diverged brightness and color arrives just after T+31s")
	mockClock.AdvanceAndProcess(47 * time.Millisecond)
	server.SetState("light.living_cans", "on", map[string]interface{}{
		"brightness":        115.0,
		"color_temp_kelvin": 6500.0,
	})
	client.WaitForHandlers()
	// The hold is created once a fresh read, one confirmation delay later, still shows the light off the path.
	t.Log("THEN: the confirming read 5s later finds the light still off the path")
	mockClock.AdvanceAndProcess(6 * time.Second) // luxHoldConfirmDelay(5s) + 1s margin
	require.Eventually(t, held, time.Second, 10*time.Millisecond,
		"a report outside the 31-second overlap guard must create a manual hold")
}
