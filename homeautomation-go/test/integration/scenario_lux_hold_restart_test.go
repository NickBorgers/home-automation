package integration

import (
	"context"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/state"
	"homeautomation/internal/testlogger"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/require"
)

const kitchenHoldHelper = "input_boolean.lux_hold_kitchen"

// luxHoldFixture is a Kitchen with lux-controlled cans and an under-cabinet strip, and a way to
// start (and restart) the lighting plugin against the same Home Assistant.
type luxHoldFixture struct {
	t            *testing.T
	server       *testutil.MockHAServer
	stateManager *state.Manager
	start        func() *lighting.Manager
	clock        *clock.MockClock
}

func newLuxHoldFixture(t *testing.T) *luxHoldFixture {
	t.Helper()
	server, client, stateManager, cleanup := setupTest(t)
	t.Cleanup(cleanup)

	t0 := time.Date(2026, 11, 18, 22, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(t0)
	config := &lighting.HueConfig{
		LuxCurve: &lighting.LuxCurveConfig{
			Points: []lighting.LuxCurvePoint{
				{Anchor: "test.start", Lux: 20, Kelvin: 3000},
				{Anchor: "test.end", Lux: 20, Kelvin: 3000},
			},
			OutdoorSensor: "sensor.outdoor_lux",
			TickSeconds:   60,
		},
		Rooms: []lighting.RoomConfig{{
			HueGroup: "Kitchen",
			Conditions: []lighting.LightingCondition{
				{Action: "off", Variable: "isAnyoneHomeAndAwake", Value: false},
				{Action: "on", Variable: "isAnyoneHomeAndAwake", Value: true},
			},
			LuxControl: &lighting.LuxControlConfig{
				Mode:           lighting.LuxModeActive,
				Cans:           "light.kitchen_cans",
				Calibrated:     true,
				Calibration:    []lighting.CalibrationPoint{{BrightnessPct: 1, Lux: 1}, {BrightnessPct: 100, Lux: 100}},
				FollowerLights: []lighting.LuxFollower{{Entity: "light.kitchen_main_lights", FloorPct: 20, Gain: 0.8}},
			},
		}},
	}

	require.NoError(t, stateManager.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, stateManager.SetString("dayPhase", "night"))
	server.SetState("sensor.outdoor_lux", "0", nil)
	server.SetState(kitchenHoldHelper, "off", nil)
	// The lights start far from the 20% target, so the first tick always sends a command.
	server.SetState("light.kitchen_cans", "on", map[string]interface{}{"brightness": 204.0, "color_temp_kelvin": 3000.0})
	server.SetState("light.kitchen_main_lights", "on", map[string]interface{}{"brightness": 230.0})

	start := func() *lighting.Manager {
		m := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
		m.SetClock(mockClock)
		m.SetAnchorResolver(func(anchor string, _ time.Time) (time.Time, bool) {
			anchors := map[string]time.Time{"test.start": t0.Add(-time.Hour), "test.end": t0.Add(time.Hour)}
			at, ok := anchors[anchor]
			return at, ok
		})
		require.NoError(t, m.Start())
		return m
	}
	return &luxHoldFixture{t: t, server: server, stateManager: stateManager, start: start, clock: mockClock}
}

// lightCommands returns the light.turn_on calls to the Kitchen cans and strip since index.
func (f *luxHoldFixture) lightCommands(since int) []ServiceCall {
	var out []ServiceCall
	for _, call := range filterServiceCalls(f.server.GetServiceCallsSince(since), "light", "turn_on") {
		if id := call.ServiceData["entity_id"]; id == "light.kitchen_cans" || id == "light.kitchen_main_lights" {
			out = append(out, call)
		}
	}
	return out
}

func (f *luxHoldFixture) helperState() string {
	if st := f.server.GetState(kitchenHoldHelper); st != nil {
		return st.State
	}
	return ""
}

// holdByHand starts the plugin, waits for its first command, then changes entity by hand.
func (f *luxHoldFixture) holdByHand(entity string, brightness float64) *lighting.Manager {
	f.t.Helper()
	m := f.start()
	require.Eventually(f.t, func() bool { return len(f.lightCommands(0)) >= 2 }, 2*time.Second, 10*time.Millisecond,
		"the plugin drives the cans and the strip")
	f.clock.AdvanceAndProcess(2 * time.Minute) // past the fade
	attrs := map[string]interface{}{"brightness": brightness}
	if entity == "light.kitchen_cans" {
		attrs["color_temp_kelvin"] = 3000.0
	}
	f.server.SetState(entity, "on", attrs)
	require.Eventually(f.t, func() bool {
		f.clock.AdvanceAndProcess(time.Minute) // shadow state records the hold on the next tick
		return m.GetShadowState().Outputs.LuxControl["Kitchen"].Held
	}, 2*time.Second, 50*time.Millisecond, "a hand change holds the Kitchen")
	return m
}

// User story: "If we turn the kitchen lights up, a restart of the automation must not turn them back down."
// Invariant: a room's manual hold is stored in Home Assistant and survives a restart.
func TestScenario_LuxHoldSurvivesRestart(t *testing.T) {
	f := newLuxHoldFixture(t)

	t.Log("GIVEN: someone turned the Kitchen cans up by hand, so the Kitchen is held")
	m := f.holdByHand("light.kitchen_cans", 217)
	require.Eventually(t, func() bool { return f.helperState() == "on" }, 2*time.Second, 10*time.Millisecond,
		"the hold is stored in "+kitchenHoldHelper)

	t.Log("WHEN: the automation restarts")
	m.Stop()
	snapshot := f.server.ServiceCallCount()
	m = f.start()
	defer m.Stop()

	t.Log("THEN: the Kitchen is still held and its lights get no command")
	require.Eventually(t, func() bool {
		f.clock.AdvanceAndProcess(time.Minute)
		st := m.GetShadowState().Outputs.LuxControl["Kitchen"]
		return st.Held && st.Decision == "held: manual change, lights left alone"
	}, 2*time.Second, 10*time.Millisecond)
	require.Empty(t, f.lightCommands(snapshot), "a restored hold leaves the lights alone")
	require.Equal(t, "on", f.helperState())

	t.Log("AND: turning the helper off in Home Assistant releases the hold")
	f.server.SetState(kitchenHoldHelper, "off", nil)
	require.Eventually(t, func() bool {
		f.clock.AdvanceAndProcess(time.Minute)
		return !m.GetShadowState().Outputs.LuxControl["Kitchen"].Held && len(f.lightCommands(snapshot)) > 0
	}, 2*time.Second, 10*time.Millisecond, "the plugin drives the Kitchen again")
}

// User story: "The under-cabinet strip is part of the kitchen. Changing it by hand holds the kitchen."
// Invariant: a hand change to a follower strip holds its whole room, cans included.
func TestScenario_LuxStripHandChangeHoldsTheRoom(t *testing.T) {
	f := newLuxHoldFixture(t)

	t.Log("GIVEN/WHEN: someone dims the Kitchen under-cabinet strip by hand")
	m := f.holdByHand("light.kitchen_main_lights", 10)
	defer m.Stop()

	t.Log("THEN: the Kitchen is held, and the hold is stored in Home Assistant")
	require.Eventually(t, func() bool { return f.helperState() == "on" }, 2*time.Second, 10*time.Millisecond)

	t.Log("AND: a phase change and later ticks do not move the cans or the strip")
	snapshot := f.server.ServiceCallCount()
	require.NoError(t, f.stateManager.SetString("dayPhase", "winddown"))
	f.clock.AdvanceAndProcess(time.Hour)
	require.Eventually(t, func() bool {
		return m.GetShadowState().Outputs.LuxControl["Kitchen"].Decision == "held: manual change, lights left alone"
	}, 2*time.Second, 10*time.Millisecond)
	require.Empty(t, f.lightCommands(snapshot))
}

// User story: "A hold from yesterday evening must not stick after we went to bed, even if the
// automation was down when we did."
// Invariant: at startup a stored hold is kept only while the room's conditions still turn it on.
func TestScenario_LuxHoldClearedAtStartupWhenTheRoomIsOff(t *testing.T) {
	f := newLuxHoldFixture(t)

	t.Log("GIVEN: a stored Kitchen hold, and everyone went to bed while the automation was down")
	f.server.SetState(kitchenHoldHelper, "on", nil)
	require.NoError(t, f.stateManager.SetBool("isAnyoneHomeAndAwake", false))

	t.Log("WHEN: the automation starts")
	m := f.start()
	defer m.Stop()

	t.Log("THEN: the stored hold is cleared")
	require.Eventually(t, func() bool {
		f.clock.AdvanceAndProcess(time.Minute)
		return f.helperState() == "off" && !m.GetShadowState().Outputs.LuxControl["Kitchen"].Held
	}, 2*time.Second, 50*time.Millisecond)
}
