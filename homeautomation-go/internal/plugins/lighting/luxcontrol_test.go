package lighting

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	luxTestCans   = "light.living_cans"
	luxTestSensor = "sensor.outdoor_lux"
)

type luxFixture struct {
	env     *testutil.Env
	manager *Manager
	clock   *clock.MockClock
	room    *RoomConfig
}

// newLuxFixture builds a Living Room with lux control in the given mode. The clock starts at
// noon on a fixed day, someone is home, the TV is off, and outdoor light is 10,000 lux.
func newLuxFixture(t *testing.T, mode string, readOnly bool) *luxFixture {
	t.Helper()
	env := testutil.NewEnv(t)
	cfg := &HueConfig{
		LuxCurve: luxTestCurve(),
		Rooms: []RoomConfig{{
			HueGroup:   "Living Room",
			HASSAreaID: "living_room_2",
			Conditions: []LightingCondition{
				{Action: "off", Variable: "isAnyoneHome", Value: false},
				{Action: "on", Variable: "isAnyoneHome", Value: true},
			},
			IncreaseBrightnessIfTrue: "isHaveGuests",
			LuxControl: &LuxControlConfig{
				Mode:             mode,
				Cans:             luxTestCans,
				AccentSceneGroup: "Living Accent",
				DaylightFactor:   0.02,
				Calibrated:       true,
				Calibration:      luxTestCalibration,
			},
		}},
	}
	require.NoError(t, cfg.validateLux())

	m := NewManager(context.Background(), env.MockHA, env.StateMgr, cfg, env.Logger, readOnly, nil)
	m.luxOutdoor = &smoother{window: luxDefaultSmoothing}
	clk := clock.NewMockClock(at(12, 0))
	m.SetClock(clk)
	m.SetAnchorResolver(luxTestAnchors)

	require.NoError(t, env.StateMgr.SetBool("isAnyoneHome", true))
	require.NoError(t, env.StateMgr.SetBool("isHaveGuests", false))
	env.MockHA.SetState(luxTestSensor, "10000", nil)
	return &luxFixture{env: env, manager: m, clock: clk, room: &cfg.Rooms[0]}
}

func (f *luxFixture) setOutdoor(lux string) { f.env.MockHA.SetState(luxTestSensor, lux, nil) }

// lightCalls returns the light.* service calls made since the last ClearServiceCalls.
func (f *luxFixture) lightCalls() []ha.ServiceCall {
	var out []ha.ServiceCall
	for _, c := range f.env.MockHA.GetServiceCalls() {
		if c.Domain == "light" {
			out = append(out, c)
		}
	}
	return out
}

func (f *luxFixture) decision() string {
	return f.manager.GetShadowState().Outputs.LuxControl["Living Room"].Decision
}

// canReport builds a state report for the cans.
func canReport(state string, brightness255 float64, kelvin float64) *ha.State {
	attrs := map[string]interface{}{}
	if brightness255 > 0 {
		attrs["brightness"] = brightness255
	}
	if kelvin > 0 {
		attrs["color_temp_kelvin"] = kelvin
	}
	return &ha.State{EntityID: luxTestCans, State: state, Attributes: attrs}
}

func TestStartLux_ConfiguresOutdoorSmoothingWindow(t *testing.T) {
	t.Parallel()
	f := newLuxFixture(t, LuxModeLogOnly, false)
	f.manager.config.LuxCurve.SmoothingMinutes = 12

	require.NoError(t, f.manager.startLux())
	t.Cleanup(f.manager.stopLux)

	require.NotNil(t, f.manager.luxOutdoor)
	assert.Equal(t, 12*time.Minute, f.manager.luxOutdoor.window)
}

func TestLux_CloudyNoonSendsFillInBrightness(t *testing.T) {
	t.Parallel()
	// GIVEN: noon (target 300 lux, 5000 K), outdoor 10,000 lux × 0.02 = 200 lux of daylight
	f := newLuxFixture(t, LuxModeActive, false)

	// WHEN: the loop ticks
	f.manager.luxTick()

	// THEN: the cans supply the missing 100 lux: 44% on the calibration table, at 5000 K,
	// with a short transition because the room was dark.
	calls := f.lightCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "turn_on", calls[0].Service)
	assert.Equal(t, luxTestCans, calls[0].Data["entity_id"])
	assert.Equal(t, 44, calls[0].Data["brightness_pct"])
	assert.Equal(t, 5000, calls[0].Data["color_temp_kelvin"])
	assert.Equal(t, float64(maxReturnHomeTransition), calls[0].Data["transition"])

	st := f.manager.GetShadowState().Outputs.LuxControl["Living Room"]
	assert.InDelta(t, 300, st.TargetLux, 0.1)
	assert.InDelta(t, 200, st.DaylightEstimate, 0.1)
	assert.InDelta(t, 100, st.NeededLux, 0.1)
	assert.Equal(t, LuxModeActive, st.Mode)
	assert.Contains(t, st.Decision, "sent")
}

func TestLux_SunnyDayTurnsCansOff(t *testing.T) {
	t.Parallel()
	// GIVEN: bright sun, 40,000 lux × 0.02 = 800 lux, more than the 300 target
	f := newLuxFixture(t, LuxModeActive, false)
	f.setOutdoor("40000")

	// WHEN/THEN: the cans are turned off, not left dim
	f.manager.luxTick()
	calls := f.lightCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "turn_off", calls[0].Service)
}

func TestLux_MissingOutdoorSensorLightsRoomToFullTarget(t *testing.T) {
	t.Parallel()
	// INVARIANT: a missing or unavailable reading must never leave the room dark.
	f := newLuxFixture(t, LuxModeActive, false)
	f.setOutdoor("unavailable")

	f.manager.luxTick()

	calls := f.lightCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "turn_on", calls[0].Service)
	// 300 lux needed with no daylight: the top of the calibration table.
	assert.Equal(t, 100, calls[0].Data["brightness_pct"])
	assert.Contains(t, f.decision(), "outdoor sensor missing")
}

func TestLux_SmallChangesDoNotSendCommands(t *testing.T) {
	t.Parallel()
	f := newLuxFixture(t, LuxModeActive, false)
	f.manager.luxTick()
	f.env.MockHA.ClearServiceCalls()

	// WHEN: one minute later nothing meaningful changed
	f.clock.Advance(time.Minute)
	f.manager.luxTick()

	// THEN: no command
	assert.Empty(t, f.lightCalls())
	assert.Contains(t, f.decision(), "no change")
}

func TestLux_EveningCurveDimsAndWarmsTheCans(t *testing.T) {
	t.Parallel()
	// Timeline: noon → winddown. Dark outside, so the cans supply the whole target.
	f := newLuxFixture(t, LuxModeActive, false)
	f.setOutdoor("0")

	f.clock.Set(at(19, 30)) // dusk: 100 lux, 2700 K
	f.manager.luxTick()
	calls := f.lightCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, 2700, calls[0].Data["color_temp_kelvin"])
	duskPct := calls[0].Data["brightness_pct"].(int)

	f.env.MockHA.ClearServiceCalls()
	f.clock.Set(at(21, 15)) // winddown: 30 lux, 2200 K
	f.manager.luxTick()
	calls = f.lightCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, 2200, calls[0].Data["color_temp_kelvin"])
	assert.Less(t, calls[0].Data["brightness_pct"].(int), duskPct, "dimmer at winddown than at dusk")
	assert.Equal(t, 60.0, calls[0].Data["transition"], "routine changes use the long transition")
}

func TestLux_LogOnlyRecordsButNeverCommands(t *testing.T) {
	t.Parallel()
	f := newLuxFixture(t, LuxModeLogOnly, false)

	f.manager.luxTick()

	assert.Empty(t, f.lightCalls())
	st := f.manager.GetShadowState().Outputs.LuxControl["Living Room"]
	assert.Equal(t, LuxModeLogOnly, st.Mode)
	assert.InDelta(t, 44, st.BrightnessPct, 0.1)
	assert.Contains(t, st.Decision, "would send")
}

func TestLux_ReadOnlyNeverCommands(t *testing.T) {
	t.Parallel()
	f := newLuxFixture(t, LuxModeActive, true)
	f.manager.luxTick()
	assert.Empty(t, f.lightCalls())
	assert.Contains(t, f.decision(), "would send")
}

func TestLux_RoomNotOnIsLeftToExistingLogic(t *testing.T) {
	t.Parallel()
	f := newLuxFixture(t, LuxModeActive, false)
	require.NoError(t, f.env.StateMgr.SetBool("isAnyoneHome", false))

	f.manager.luxTick()

	assert.Empty(t, f.lightCalls())
	assert.Contains(t, f.decision(), "room not on")
}

func TestLux_GuestsRaiseTheTarget(t *testing.T) {
	t.Parallel()
	f := newLuxFixture(t, LuxModeActive, false)
	f.setOutdoor("0")
	require.NoError(t, f.env.StateMgr.SetBool("isHaveGuests", true))

	f.manager.luxTick()

	st := f.manager.GetShadowState().Outputs.LuxControl["Living Room"]
	assert.InDelta(t, 450, st.TargetLux, 0.1, "300 lux × 1.5")
}

// ---- Manual hold ----

// heldFixture returns an active fixture where the plugin has commanded the cans and the
// command's transition has finished.
func heldFixtureAfterCommand(t *testing.T) *luxFixture {
	t.Helper()
	f := newLuxFixture(t, LuxModeActive, false)
	f.manager.luxTick()
	require.Len(t, f.lightCalls(), 1)
	f.env.MockHA.ClearServiceCalls()
	f.clock.Advance(2 * time.Minute)
	return f
}

func TestLux_ManualBrightnessChangeHoldsTheRoom(t *testing.T) {
	t.Parallel()
	// GIVEN: the plugin set the cans to 44%
	f := heldFixtureAfterCommand(t)

	// WHEN: a person turns them up to 90%
	f.manager.handleCansChange(f.room, canReport("on", 230, 5000))

	// THEN: the room is held, and the loop leaves the cans alone
	assert.True(t, f.manager.luxHeld(f.room))
	f.setOutdoor("0") // conditions that would otherwise force a command
	f.clock.Advance(time.Hour)
	f.manager.luxTick()
	assert.Empty(t, f.lightCalls())
	assert.Contains(t, f.decision(), "held")
	assert.True(t, f.manager.GetShadowState().Outputs.LuxControl["Living Room"].Held)
}

func TestLux_OwnCommandReportsDoNotCountAsManual(t *testing.T) {
	t.Parallel()
	f := newLuxFixture(t, LuxModeActive, false)
	f.manager.luxTick() // 44% at 5000 K, 5 s transition
	f.env.MockHA.ClearServiceCalls()

	// Mid-transition, Hue reports intermediate values: ignored.
	f.clock.Advance(2 * time.Second)
	f.manager.handleCansChange(f.room, canReport("on", 10, 3000))
	assert.False(t, f.manager.luxHeld(f.room))

	// After the transition, the final value matches the command (44% of 255 = 112): not manual.
	f.clock.Advance(time.Minute)
	f.manager.handleCansChange(f.room, canReport("on", 112, 5000))
	assert.False(t, f.manager.luxHeld(f.room))
}

func TestLux_ManualColorChangeAndPowerOffHold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		report *ha.State
	}{
		{"color temperature moved", canReport("on", 112, 3000)},
		{"cans switched off", canReport("off", 0, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := heldFixtureAfterCommand(t)
			f.manager.handleCansChange(f.room, tt.report)
			assert.True(t, f.manager.luxHeld(f.room))
		})
	}
}

func TestLux_ManualChangeWhileTVHasTheLightsIsIgnored(t *testing.T) {
	t.Parallel()
	// Hue Sync changes the cans while the room is in "skip". That is not a person's choice.
	f := heldFixtureAfterCommand(t)
	f.room.Conditions = append([]LightingCondition{{Action: "skip", Variable: "isTVPlaying", Value: true}}, f.room.Conditions...)
	require.NoError(t, f.env.StateMgr.SetBool("isTVPlaying", true))

	f.manager.handleCansChange(f.room, canReport("on", 255, 2000))

	assert.False(t, f.manager.luxHeld(f.room))
}

func TestLux_HoldSurvivesDayPhaseAndSunEventChanges(t *testing.T) {
	t.Parallel()
	// USER STORY: I turned the lights up in the evening. The system must not turn them back
	// down at dusk, winddown or night.
	f := heldFixtureAfterCommand(t)
	f.manager.handleCansChange(f.room, canReport("on", 255, 3000))
	require.True(t, f.manager.luxHeld(f.room))
	f.env.MockHA.ClearServiceCalls()

	for _, phase := range []string{"dusk", "winddown", "night"} {
		f.manager.activateScenesForAllRooms(phase, "dayPhase")
	}
	f.manager.activateScenesForAllRooms("night", "sunevent")

	assert.Empty(t, f.env.MockHA.GetServiceCalls(), "no scene recall, no light command")
	assert.True(t, f.manager.luxHeld(f.room))
}

func TestLux_ResetClearsHold(t *testing.T) {
	t.Parallel()
	// USER STORY: I ran reset. The room must behave as if its lights were turned off and on again.
	tests := []struct {
		name   string
		report *ha.State
	}{
		{"brightness turned up", canReport("on", 255, 3000)},
		{"color temperature moved", canReport("on", 112, 3000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := heldFixtureAfterCommand(t)
			require.NoError(t, f.env.StateMgr.SetString("dayPhase", "day"))
			f.manager.handleCansChange(f.room, tt.report)
			require.True(t, f.manager.luxHeld(f.room))

			// WHEN: reset runs
			f.env.MockHA.ClearServiceCalls()
			require.NoError(t, f.manager.Reset())

			// THEN: the hold is gone, and the cans got lux values (as after off then on)
			assert.False(t, f.manager.luxHeld(f.room))
			assert.NotEmpty(t, f.lightCalls(), "cans commanded after reset")

			// AND: the next tick re-evaluates the room instead of leaving it alone
			f.clock.Advance(time.Minute)
			f.manager.luxTick()
			assert.NotContains(t, f.decision(), "held")
		})
	}
}

func TestLux_HoldClearsWhenRoomCyclesOffAndOn(t *testing.T) {
	t.Parallel()
	f := heldFixtureAfterCommand(t)
	f.manager.handleCansChange(f.room, canReport("on", 255, 3000))
	require.True(t, f.manager.luxHeld(f.room))

	// WHEN: everyone leaves (room off) and someone returns (room on)
	require.NoError(t, f.env.StateMgr.SetBool("isAnyoneHome", false))
	f.manager.evaluateAndActivateRoom(f.room, "day", "isAnyoneHome")
	require.NoError(t, f.env.StateMgr.SetBool("isAnyoneHome", true))
	f.env.MockHA.ClearServiceCalls()
	f.manager.evaluateAndActivateRoom(f.room, "day", "isAnyoneHome")

	// THEN: the hold is gone, the accent scene is recalled and the cans get lux values again
	assert.False(t, f.manager.luxHeld(f.room))
	var scene, light bool
	for _, c := range f.env.MockHA.GetServiceCalls() {
		if c.Domain == "scene" && c.Data["entity_id"] == "scene.living_accent_day" {
			scene = true
		}
		if c.Domain == "light" && c.Service == "turn_on" && c.Data["entity_id"] == luxTestCans {
			light = true
		}
	}
	assert.True(t, scene, "accent scene recalled, not the old whole-room scene")
	assert.True(t, light, "cans commanded with lux values")
}

func TestLux_HoldClearsWhenEveryoneAsleepOrLeaves(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		fire func(f *luxFixture)
	}{
		{"everyone asleep", func(f *luxFixture) {
			f.manager.handleSleepStateChange("isEveryoneAsleep", false, true)
		}},
		{"nobody home", func(f *luxFixture) {
			f.manager.handlePresenceChange("isAnyoneHome", true, false)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := heldFixtureAfterCommand(t)
			require.NoError(t, f.env.StateMgr.SetString("dayPhase", "night"))
			f.manager.handleCansChange(f.room, canReport("on", 255, 3000))
			require.True(t, f.manager.luxHeld(f.room))

			tt.fire(f)

			assert.False(t, f.manager.luxHeld(f.room))
		})
	}
}

func TestLux_PlainSleepStateFlipDoesNotClearHold(t *testing.T) {
	t.Parallel()
	f := heldFixtureAfterCommand(t)
	require.NoError(t, f.env.StateMgr.SetString("dayPhase", "day"))
	f.manager.handleCansChange(f.room, canReport("on", 255, 3000))

	f.manager.handleSleepStateChange("isEveryoneAsleep", true, false) // waking up

	assert.True(t, f.manager.luxHeld(f.room))
}

func TestLux_RoomTurnedOffByPluginIsNotAManualChange(t *testing.T) {
	t.Parallel()
	f := heldFixtureAfterCommand(t)
	require.NoError(t, f.env.StateMgr.SetBool("isAnyoneHome", false))
	f.manager.evaluateAndActivateRoom(f.room, "day", "isAnyoneHome")

	f.clock.Advance(time.Minute)
	f.manager.handleCansChange(f.room, canReport("off", 0, 0))

	assert.False(t, f.manager.luxHeld(f.room))
}

func TestLux_LogOnlyRoomKeepsTodaysSceneBehavior(t *testing.T) {
	t.Parallel()
	f := newLuxFixture(t, LuxModeLogOnly, false)

	f.manager.evaluateAndActivateRoom(f.room, "day", "dayPhase")

	var sawOld bool
	for _, c := range f.env.MockHA.GetServiceCalls() {
		if c.Domain == "scene" && c.Data["entity_id"] == "scene.living_room_day" {
			sawOld = true
		}
	}
	assert.True(t, sawOld, "log_only still recalls the whole-room scene (rollback path)")
	assert.Empty(t, f.lightCalls())
	assert.False(t, f.manager.luxHeld(f.room), "log_only never holds")
}

// ---- Config ----

func TestLuxConfigValidation(t *testing.T) {
	t.Parallel()
	valid := func() *HueConfig {
		return &HueConfig{
			LuxCurve: luxTestCurve(),
			Rooms: []RoomConfig{{HueGroup: "R", LuxControl: &LuxControlConfig{
				Mode: LuxModeActive, Cans: "light.x", AccentSceneGroup: "R Accent",
				Calibrated: true, Calibration: luxTestCalibration,
			}}},
		}
	}
	require.NoError(t, valid().validateLux())

	tests := []struct {
		name   string
		mutate func(c *HueConfig)
	}{
		{"no curve", func(c *HueConfig) { c.LuxCurve = nil }},
		{"no outdoor sensor", func(c *HueConfig) { c.LuxCurve.OutdoorSensor = "" }},
		{"bad point", func(c *HueConfig) { c.LuxCurve.Points[0].Lux = 0 }},
		{"bad mode", func(c *HueConfig) { c.Rooms[0].LuxControl.Mode = "sometimes" }},
		{"no cans", func(c *HueConfig) { c.Rooms[0].LuxControl.Cans = "" }},
		{"calibration not rising", func(c *HueConfig) {
			c.Rooms[0].LuxControl.Calibration = []CalibrationPoint{{BrightnessPct: 50, Lux: 100}, {BrightnessPct: 25, Lux: 40}}
		}},
		{"active but uncalibrated", func(c *HueConfig) { c.Rooms[0].LuxControl.Calibrated = false }},
		{"active without accent group", func(c *HueConfig) { c.Rooms[0].LuxControl.AccentSceneGroup = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := valid()
			tt.mutate(c)
			assert.Error(t, c.validateLux())
		})
	}
}

func TestProductionHueConfigLoads(t *testing.T) {
	t.Parallel()
	cfg, err := LoadConfig(filepath.Join("..", "..", "..", "..", "configs", "hue_config.yaml"))
	require.NoError(t, err)
	require.NotNil(t, cfg.LuxCurve)

	// Every production anchor must be one the resolver understands.
	for _, p := range cfg.LuxCurve.Points {
		kind, name := splitAnchor(p.Anchor)
		assert.Contains(t, []string{"sun", "schedule"}, kind, p.Anchor)
		assert.NotEmpty(t, name, p.Anchor)
	}
	// Active rooms must carry a calibration table: validateLux enforces it, so this also
	// documents that the shipped Living Room config is one the plugin accepts.
	for _, r := range cfg.Rooms {
		if r.LuxControl != nil && r.LuxControl.Mode == LuxModeActive {
			assert.True(t, r.LuxControl.Calibrated, r.HueGroup)
			assert.NotEmpty(t, r.LuxControl.Calibration, r.HueGroup)
		}
	}
}

func TestLoadConfigRejectsInvalidLuxConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "hue.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
rooms:
  - hue_group: R
    lux_control: { mode: log_only, cans: light.x }
`), 0o600))
	_, err := LoadConfig(path)
	assert.ErrorContains(t, err, "lux_curve")
}
