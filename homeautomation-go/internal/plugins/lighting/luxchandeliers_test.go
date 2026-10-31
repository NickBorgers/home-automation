package lighting

import (
	"testing"
	"time"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the brightness-only Lutron Diva chandeliers (Front Foyer, Front Room).
// The lux numbers come from a rough phone-app calibration; see docs/flows/LUX_LIGHTING.md.

var chandeliers = []struct {
	room   string
	light  string
	points map[int]float64 // measured lux at night, by brightness
}{
	{"Front Foyer", "light.front_foyer_chandelier", map[int]float64{5: 15, 10: 32, 25: 92, 50: 353}},
	{"Front Room", "light.front_room_chandelier", map[int]float64{10: 8, 25: 47, 50: 228}},
}

func TestProductionConfig_ChandeliersAreBrightnessOnlyAndMeasured(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)
	for _, want := range chandeliers {
		t.Run(want.room, func(t *testing.T) {
			room := findRoom(cfg, want.room)
			require.NotNil(t, room, "room missing from hue_config.yaml")
			lc := room.LuxControl
			require.NotNil(t, lc)
			assert.Equal(t, LuxModeActive, lc.Mode)
			assert.Equal(t, want.light, lc.Cans)
			assert.True(t, lc.Calibrated)
			assert.False(t, lc.HasColorTemp(), "Diva dimmers have no color temperature")
			assert.InDelta(t, 0.2, lc.LuxScale, 1e-9)
			assert.InDelta(t, 0.01, lc.DaylightFactor, 1e-9)
			assert.Empty(t, room.HASSAreaID)
			assert.Empty(t, lc.AccentSceneGroup)
			require.Len(t, lc.Calibration, len(want.points))
			for _, cp := range lc.Calibration {
				assert.InDelta(t, want.points[cp.BrightnessPct], cp.Lux, 1e-9, "point %d%%", cp.BrightnessPct)
			}
			assert.Equal(t, []LightingCondition{
				{Action: "off", Variable: "isAnyoneHomeAndAwake", Value: false},
				{Action: "on", Variable: "isAnyoneHomeAndAwake", Value: true},
			}, room.Conditions)
		})
	}
}

func TestProductionConfig_OtherRoomsKeepColorTemp(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)
	for _, name := range []string{"Living Room", "Kitchen", "Pantry"} {
		assert.True(t, findRoom(cfg, name).LuxControl.HasColorTemp(), name)
	}
}

func TestProductionConfig_ChandelierCalibrationMapsSanely(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)
	for _, want := range chandeliers {
		t.Run(want.room, func(t *testing.T) {
			cal := findRoom(cfg, want.room).LuxControl.Calibration
			// Each measured point maps back to its brightness.
			for pct, lux := range want.points {
				assert.InDelta(t, float64(pct), luxToBrightness(cal, lux), 0.001)
			}
			// Evening targets (winddown 30 lux x 0.2 = 6, dusk 100 x 0.2 = 20) are low but on.
			winddown := luxToBrightness(cal, 6)
			dusk := luxToBrightness(cal, 20)
			assert.Greater(t, winddown, 0.0)
			assert.Less(t, winddown, dusk)
			assert.Less(t, dusk, 25.0, "evening chandelier stays dim")
			assert.True(t, shouldBeOn(cal, 6, false), "winddown target keeps the chandelier on")
			// Brightest daytime target (300 x 0.2 x guests 1.5 = 90) is under the last point.
			assert.Less(t, luxToBrightness(cal, 90), 50.0)
			// Above the last measured point the table clamps to 100%, so no 100% point is needed.
			assert.Equal(t, 100.0, luxToBrightness(cal, want.points[50]*2))
			// Daylight covering the target switches it off.
			assert.False(t, shouldBeOn(cal, 0, true))
		})
	}
}

// Below the first calibration point the table is interpolated from (0%, 0 lux), so a small
// needed lux still maps to a small, nonzero brightness (clamped to at least 1% when sent).
func TestChandelierBelowFirstPointInterpolatesFromZero(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)
	foyer := findRoom(cfg, "Front Foyer").LuxControl.Calibration
	assert.InDelta(t, 2.5, luxToBrightness(foyer, 7.5), 0.001, "half of 15 lux at 5%")
	assert.True(t, shouldBeOn(foyer, 6, false), "6 lux is about 2%, still on")
	room := findRoom(cfg, "Front Room").LuxControl.Calibration
	assert.InDelta(t, 5.0, luxToBrightness(room, 4), 0.001, "half of 8 lux at 10%")
	// The 22:00 night target (5 x 0.2 = 1 lux) is under the foyer's 1% threshold (3 lux): off.
	assert.False(t, shouldBeOn(foyer, 1, false))
	assert.True(t, shouldBeOn(room, 1.5, false))
}

func TestLuxRooms_ChandeliersSendBrightnessOnlyInTheEvening(t *testing.T) {
	t.Parallel()
	for _, want := range chandeliers {
		t.Run(want.room, func(t *testing.T) {
			m, env, clk := newProductionRoomFixture(t)
			clk.Set(at(20, 0))
			env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "0", nil)
			room := findRoom(m.config, want.room)

			m.evaluateAndActivateRoom(room, "evening", "isAnyoneHomeAndAwake")

			var calls []ha.ServiceCall
			for _, c := range env.MockHA.GetServiceCalls() {
				if c.Domain == "light" && c.Service == "turn_on" && c.Data["entity_id"] == want.light {
					calls = append(calls, c)
				}
			}
			require.Len(t, calls, 1)
			assert.Contains(t, calls[0].Data, "brightness_pct")
			assert.Contains(t, calls[0].Data, "transition")
			assert.NotContains(t, calls[0].Data, "color_temp_kelvin")
			assert.NotContains(t, calls[0].Data, "area_id")
			st := m.GetShadowState().Outputs.LuxControl[want.room]
			assert.Zero(t, st.TargetKelvin, "no kelvin recorded")
			assert.Greater(t, st.BrightnessPct, 0.0)
		})
	}
}

func TestLuxRooms_ChandeliersGoOffWhenDaylightCoversTheTarget(t *testing.T) {
	t.Parallel()
	for _, want := range chandeliers {
		t.Run(want.room, func(t *testing.T) {
			m, env, _ := newProductionRoomFixture(t) // noon, 7,500 outdoor lux -> 75 lux estimate
			room := findRoom(m.config, want.room)
			m.evaluateAndActivateRoom(room, "day", "isAnyoneHomeAndAwake")
			for _, c := range env.MockHA.GetServiceCalls() {
				assert.False(t, c.Service == "turn_on" && c.Data["entity_id"] == want.light, "daylight covers 60 lux")
			}
		})
	}
}

func TestLuxRooms_ChandeliersFollowAwakeAndHomeWithoutAnArea(t *testing.T) {
	t.Parallel()
	for _, want := range chandeliers {
		t.Run(want.room, func(t *testing.T) {
			m, env, _ := newProductionRoomFixture(t)
			room := findRoom(m.config, want.room)
			action, _ := m.evaluateConditions(room)
			assert.Equal(t, "on", action)

			require.NoError(t, env.StateMgr.SetBool("isAnyoneHomeAndAwake", false))
			env.MockHA.ClearServiceCalls()
			action, _ = m.evaluateConditions(room)
			assert.Equal(t, "off", action)
			m.evaluateAndActivateRoom(room, "day", "isAnyoneHomeAndAwake")
			calls := env.MockHA.GetServiceCalls()
			require.Len(t, calls, 1)
			assert.Equal(t, "turn_off", calls[0].Service)
			assert.Equal(t, want.light, calls[0].Data["entity_id"])
			assert.NotContains(t, calls[0].Data, "area_id")
		})
	}
}

func TestLuxRooms_ChandelierManualChangeHoldsOnBrightnessOnly(t *testing.T) {
	t.Parallel()
	m, env, clk := newProductionRoomFixture(t)
	clk.Set(at(20, 0))
	env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "0", nil)
	room := findRoom(m.config, "Front Room")
	m.evaluateAndActivateRoom(room, "evening", "isAnyoneHomeAndAwake")
	m.luxMu.Lock()
	cmd := m.luxRoom("Front Room").cmd
	m.luxMu.Unlock()
	require.NotNil(t, cmd)
	require.True(t, cmd.on)
	clk.Advance(2 * time.Minute)

	// The light reports the commanded brightness plus a stray kelvin: not manual.
	m.handleCansChange(room, canReport("on", cmd.pct/100*255, 6500))
	assert.False(t, m.luxHeld(room), "kelvin is ignored on a brightness-only light")

	// A different brightness is manual.
	m.handleCansChange(room, canReport("on", 255, 0))
	assert.True(t, m.luxHeld(room))
}

func TestManualChangeReason_BrightnessOnlyIgnoresKelvin(t *testing.T) {
	t.Parallel()
	s := canReport("on", 102, 2000) // 40%
	assert.Equal(t, "", manualChangeReason(&luxCommand{on: true, pct: 40}, s, false))
	assert.Contains(t, manualChangeReason(&luxCommand{on: true, pct: 40, kelvin: 3000}, s, true), "color")
}
