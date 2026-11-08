package lighting

import (
	"testing"
	"time"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the strip color-temperature curve. The Kitchen points were matched by eye on 2026-11-08
// (issue #1263), so these tests check the mechanism, with the measured points as fixtures.

var kitchenStripCurve = []StripCCTPoint{
	{49, 3000}, {76, 3250}, {89, 3500}, {95, 3750}, {100, 4000},
}

func TestStripCCT_FloorInterpolatesAndClamps(t *testing.T) {
	t.Parallel()
	f := LuxFollower{CCTCurve: kitchenStripCurve}
	for _, tc := range []struct {
		kelvin int
		want   float64
	}{
		{0, 0},       // brightness-only room: no target
		{2000, 0},    // warmer than the warmest measured point: no floor
		{3000, 0},    // at the warm end the brightness curve decides
		{3125, 62.5}, // halfway between 49 and 76
		{3250, 76},
		{3500, 89},
		{3875, 97.5},
		{4000, 100},
		{6000, 100}, // cooler than the strip can go: its brightest point
	} {
		assert.InDelta(t, tc.want, f.cctFloorPct(tc.kelvin), 1e-9, "%d K", tc.kelvin)
	}
	assert.Zero(t, LuxFollower{}.cctFloorPct(4000), "no curve, no floor")
}

func TestStripCCT_EstimateInterpolatesAndClamps(t *testing.T) {
	t.Parallel()
	f := LuxFollower{CCTCurve: kitchenStripCurve}
	for _, tc := range []struct {
		pct  float64
		want int
	}{
		{10, 3000}, {49, 3000}, {62.5, 3125}, {76, 3250}, {97.5, 3875}, {100, 4000},
	} {
		assert.Equal(t, tc.want, f.estimateKelvin(tc.pct), "%v%%", tc.pct)
	}
	assert.Zero(t, LuxFollower{}.estimateKelvin(60), "no curve, no estimate")
}

func TestStripCCT_FloorAndEstimateAreInverse(t *testing.T) {
	t.Parallel()
	f := LuxFollower{CCTCurve: kitchenStripCurve}
	for k := 3050; k <= 3950; k += 50 {
		assert.InDelta(t, float64(k), float64(f.estimateKelvin(f.cctFloorPct(k))), 1, "%d K", k)
	}
}

func TestStripCCT_ConfigValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		curve []StripCCTPoint
		ok    bool
	}{
		{"none", nil, true},
		{"valid", kitchenStripCurve, true},
		{"one point", []StripCCTPoint{{50, 3000}}, false},
		{"brightness not rising", []StripCCTPoint{{50, 3000}, {50, 3500}}, false},
		{"kelvin not rising", []StripCCTPoint{{50, 3500}, {80, 3000}}, false},
		{"brightness over 100", []StripCCTPoint{{50, 3000}, {101, 3500}}, false},
		{"zero brightness", []StripCCTPoint{{0, 3000}, {50, 3500}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &HueConfig{
				LuxCurve: luxTestCurve(),
				Rooms: []RoomConfig{{HueGroup: "R", LuxControl: &LuxControlConfig{
					Mode: LuxModeActive, Cans: "light.x", Calibrated: true, Calibration: luxTestCalibration,
					FollowerLights: []LuxFollower{{Entity: "light.s", FloorPct: 20, Gain: 0.8, CCTCurve: tc.curve}},
				}}},
			}
			if tc.ok {
				assert.NoError(t, cfg.validateLux())
			} else {
				assert.Error(t, cfg.validateLux())
			}
		})
	}
}

func TestProductionConfig_KitchenStripHasTheMeasuredCurve(t *testing.T) {
	t.Parallel()
	f := findRoom(loadProductionConfig(t), "Kitchen").LuxControl.FollowerLights[0]
	assert.Equal(t, kitchenStripCurve, f.CCTCurve)
	assert.Empty(t, findRoom(loadProductionConfig(t), "Pantry").LuxControl.FollowerLights[0].CCTCurve,
		"the Pantry strip has a fixed color and no curve")
}

// User story: when the kitchen needs a cool color, the strip is bright enough to be cool too.
// Invariant: the strip is never dimmer than the brightness that reaches the cans' Kelvin.
func TestStripCCT_CoolTargetRaisesStripAboveTheBrightnessCurve(t *testing.T) {
	t.Parallel()
	// GIVEN: noon. The cans target 4000 K at a low brightness, so the brightness curve alone gives a dim strip.
	m, env, _ := newProductionRoomFixture(t)
	env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "20000", nil)
	room := findRoom(m.config, "Kitchen")
	m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")

	cans := callsTo(env.MockHA, "turn_on", "light.kitchen_cans")
	strip := callsTo(env.MockHA, "turn_on", "light.kitchen_main_lights")
	require.Len(t, cans, 1)
	require.Len(t, strip, 1)
	cansPct := float64(cans[0].Data["brightness_pct"].(int))
	kelvin := cans[0].Data["color_temp_kelvin"].(int)
	brightnessCurve := LuxFollower{FloorPct: 20, Gain: 0.8}.followerPct(cansPct)

	// THEN: the strip is at least as bright as the CCT curve needs, and shadow state shows why.
	f := room.LuxControl.FollowerLights[0]
	got := float64(strip[0].Data["brightness_pct"].(int))
	assert.GreaterOrEqual(t, got, f.cctFloorPct(kelvin)-0.5)
	assert.GreaterOrEqual(t, got, brightnessCurve)
	fs := m.GetShadowState().Outputs.LuxControl["Kitchen"].Followers["light.kitchen_main_lights"]
	assert.Equal(t, got, fs.Pct)
	assert.Equal(t, cansPct, fs.CansPct)
	assert.Equal(t, kelvin, fs.TargetKelvin)
	assert.InDelta(t, f.cctFloorPct(kelvin), fs.CCTFloorPct, 1e-9)
	assert.Equal(t, f.estimateKelvin(got), fs.EstimatedKelvin)
	assert.NotContains(t, strip[0].Data, "color_temp_kelvin", "the strip stays brightness-only")
}

// The brightness curve and the CCT curve diverge: a warm evening target, a bright brightness curve.
func TestStripCCT_WarmTargetLeavesTheBrightnessCurveInCharge(t *testing.T) {
	t.Parallel()
	m, env, clk := newProductionRoomFixture(t)
	room := findRoom(m.config, "Kitchen")
	clk.Set(at(23, 0))
	env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "0", nil)
	m.evaluateAndActivateRoom(room, "night", "isKitchenOccupied")

	strip := callsTo(env.MockHA, "turn_on", "light.kitchen_main_lights")
	cans := callsTo(env.MockHA, "turn_on", "light.kitchen_cans")
	require.Len(t, strip, 1)
	require.Len(t, cans, 1)
	f := room.LuxControl.FollowerLights[0]
	cansPct := float64(cans[0].Data["brightness_pct"].(int))
	floor := f.cctFloorPct(cans[0].Data["color_temp_kelvin"].(int))
	assert.Equal(t, max(f.followerPct(cansPct), float64(int(floor+0.5))), float64(strip[0].Data["brightness_pct"].(int)))
}

func TestStripCCT_RecomputesAsTheCansKelvinMoves(t *testing.T) {
	t.Parallel()
	m, env, clk := newProductionRoomFixture(t)
	room := findRoom(m.config, "Kitchen")
	m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")
	fs := func() float64 {
		return m.GetShadowState().Outputs.LuxControl["Kitchen"].Followers["light.kitchen_main_lights"].CCTFloorPct
	}
	noon := fs()

	clk.Set(at(21, 0))
	env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "0", nil)
	for i := 0; i < 6; i++ { // the slew limit closes the gap over several ticks
		clk.Advance(2 * time.Minute)
		m.luxTick()
	}
	assert.Less(t, fs(), noon, "a warmer target needs a lower strip brightness")
}

// A manual strip change still holds only that strip, and the shadow state keeps the last estimate.
func TestStripCCT_ManualHoldStillHoldsOnlyTheStrip(t *testing.T) {
	t.Parallel()
	m, _, clk := newProductionRoomFixture(t)
	room := findRoom(m.config, "Kitchen")
	m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")
	clk.Advance(2 * time.Minute)
	m.handleFollowerChange(room, &ha.State{EntityID: "light.kitchen_main_lights", State: "on",
		Attributes: map[string]interface{}{"brightness": 30.0}})
	assert.False(t, m.luxHeld(room))
	m.luxEvaluate(room, "test")
	fs := m.GetShadowState().Outputs.LuxControl["Kitchen"].Followers["light.kitchen_main_lights"]
	assert.True(t, fs.Held)
	assert.NotZero(t, fs.TargetKelvin)
}
