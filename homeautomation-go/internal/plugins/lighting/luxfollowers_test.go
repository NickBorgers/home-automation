package lighting

import (
	"math"
	"testing"
	"time"

	"homeautomation/internal/ha"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for follower lights: the under-cabinet strips that follow their room's ceiling cans.
// floor_pct 20 and gain 0.8 are user-chosen starting guesses; see docs/flows/LUX_LIGHTING.md.

var followerRooms = []struct {
	room     string
	strip    string
	cans     string
	areaRoom bool // true when the room has a hass_area_id
	floor    float64
	gain     float64
}{
	// Kitchen strip is dim-to-warm: it needs real dimming to reach its warm evening color.
	{"Kitchen", "light.kitchen_main_lights", "light.kitchen_cans", true, 20, 0.8},
	// Pantry strip is a fixed 3000 K task strip, so it can stay brighter and flatter.
	{"Pantry", "light.pantry_under_cabinet", "light.pantry_cans", false, 50, 0.5},
}

// callsTo returns the light service calls for one entity (the entity may be a list member).
func callsTo(env interface{ GetServiceCalls() []ha.ServiceCall }, service, entity string) []ha.ServiceCall {
	var out []ha.ServiceCall
	for _, c := range env.GetServiceCalls() {
		if c.Domain != "light" || c.Service != service {
			continue
		}
		switch v := c.Data["entity_id"].(type) {
		case string:
			if v == entity {
				out = append(out, c)
			}
		case []string:
			for _, e := range v {
				if e == entity {
					out = append(out, c)
				}
			}
		}
	}
	return out
}

func TestLuxFollower_PercentIsFloorPlusGainTimesCans(t *testing.T) {
	t.Parallel()
	f := LuxFollower{Entity: "light.x", FloorPct: 20, Gain: 0.8}
	for _, tc := range []struct{ cans, want float64 }{
		{100, 100}, {50, 60}, {10, 28}, {1, 21}, {0, 20},
	} {
		assert.Equal(t, tc.want, f.followerPct(tc.cans), "cans %v%%", tc.cans)
	}
	assert.Equal(t, 100.0, LuxFollower{FloorPct: 90, Gain: 1.5}.followerPct(100), "clamped to 100")
	assert.Equal(t, 1.0, LuxFollower{FloorPct: 0, Gain: 0}.followerPct(0), "clamped to at least 1")
}

func TestProductionConfig_UnderCabinetStripsFollowTheCans(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)
	for _, want := range followerRooms {
		t.Run(want.room, func(t *testing.T) {
			room := findRoom(cfg, want.room)
			require.NotNil(t, room)
			require.Len(t, room.LuxControl.FollowerLights, 1)
			f := room.LuxControl.FollowerLights[0]
			assert.Equal(t, want.strip, f.Entity)
			assert.InDelta(t, want.floor, f.FloorPct, 1e-9)
			assert.InDelta(t, want.gain, f.Gain, 1e-9)
			assert.Empty(t, room.CompanionScenes, "the strip is driven by lux control, not scenes")
		})
	}
}

func TestLuxFollowers_TrackTheCansBrightness(t *testing.T) {
	t.Parallel()
	for _, want := range followerRooms {
		t.Run(want.room, func(t *testing.T) {
			m, env, _ := newProductionRoomFixture(t) // noon, 7,500 outdoor lux, cans on
			room := findRoom(m.config, want.room)
			m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")

			cans := callsTo(env.MockHA, "turn_on", want.cans)
			strip := callsTo(env.MockHA, "turn_on", want.strip)
			require.Len(t, cans, 1)
			require.Len(t, strip, 1)
			cansPct := float64(cans[0].Data["brightness_pct"].(int))
			assert.Equal(t, math.Min(100, math.Round(want.floor+want.gain*cansPct)), float64(strip[0].Data["brightness_pct"].(int)))
			assert.GreaterOrEqual(t, float64(strip[0].Data["brightness_pct"].(int)), cansPct, "strip is at least as bright as the cans")
			assert.NotContains(t, strip[0].Data, "color_temp_kelvin")
			assert.NotContains(t, strip[0].Data, "area_id")
			assert.Equal(t, cans[0].Data["transition"], strip[0].Data["transition"], "same transition as the cans")
			assert.Equal(t, float64(strip[0].Data["brightness_pct"].(int)),
				m.GetShadowState().Outputs.LuxControl[want.room].Followers[want.strip].Pct)
		})
	}
}

func TestLuxFollowers_AreOffWhenDaylightTurnsTheCansOff(t *testing.T) {
	t.Parallel()
	for _, want := range followerRooms {
		t.Run(want.room, func(t *testing.T) {
			m, env, _ := newProductionRoomFixture(t)
			env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "400000", nil) // daylight covers any target
			room := findRoom(m.config, want.room)
			m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")

			assert.Empty(t, callsTo(env.MockHA, "turn_on", want.cans))
			assert.Empty(t, callsTo(env.MockHA, "turn_on", want.strip))
			require.Len(t, callsTo(env.MockHA, "turn_off", want.strip), 1, "strip follows the cans off")
			assert.Zero(t, m.GetShadowState().Outputs.LuxControl[want.room].Followers[want.strip].Pct)
		})
	}
}

func TestLuxFollowers_GoOffWhenTheRoomTurnsOff(t *testing.T) {
	t.Parallel()
	for _, want := range followerRooms {
		t.Run(want.room, func(t *testing.T) {
			m, env, _ := newProductionRoomFixture(t)
			room := findRoom(m.config, want.room)
			m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")
			require.NoError(t, env.StateMgr.SetBool("isAnyoneHomeAndAwake", false))
			env.MockHA.ClearServiceCalls()

			m.evaluateAndActivateRoom(room, "day", "isAnyoneHomeAndAwake")

			require.Len(t, callsTo(env.MockHA, "turn_off", want.strip), 1, "strip is part of the room-off call")
			if !want.areaRoom {
				assert.Len(t, env.MockHA.GetServiceCalls(), 1, "one call turns off the cans and the strip")
				require.Len(t, callsTo(env.MockHA, "turn_off", want.cans), 1)
			}
			// The strip going dark afterwards is the plugin's own doing, not a manual change.
			m.handleFollowerChange(room, &ha.State{EntityID: want.strip, State: "off"})
			assert.False(t, m.luxHeld(room))
		})
	}
}

func TestLuxFollowers_NotResentBelowTheMinimumStep(t *testing.T) {
	t.Parallel()
	m, env, clk := newProductionRoomFixture(t)
	room := findRoom(m.config, "Kitchen")
	m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")
	require.Len(t, callsTo(env.MockHA, "turn_on", "light.kitchen_main_lights"), 1)
	env.MockHA.ClearServiceCalls()

	clk.Advance(time.Minute)
	m.luxTick()
	assert.Empty(t, callsTo(env.MockHA, "turn_on", "light.kitchen_main_lights"), "nothing changed")
	assert.Empty(t, callsTo(env.MockHA, "turn_off", "light.kitchen_main_lights"))

	// The strip has its own last-sent value. A 2-point difference is under the step, 4 points is not.
	const strip = "light.kitchen_main_lights"
	m.luxMu.Lock()
	sent := m.luxRoom("Kitchen").followers[strip].pct
	m.luxRoom("Kitchen").followers[strip].pct = sent + 2
	m.luxMu.Unlock()
	m.luxTick()
	assert.Empty(t, callsTo(env.MockHA, "turn_on", strip), "2 points is under the step")

	m.luxMu.Lock()
	m.luxRoom("Kitchen").followers[strip].pct = sent + 4
	m.luxMu.Unlock()
	m.luxTick()
	assert.Len(t, callsTo(env.MockHA, "turn_on", strip), 1, "4 points is over the step")
}

func TestLuxFollowers_ManualChangeHoldsOnlyThatStrip(t *testing.T) {
	t.Parallel()
	for _, want := range followerRooms {
		t.Run(want.room, func(t *testing.T) {
			m, env, clk := newProductionRoomFixture(t)
			room := findRoom(m.config, want.room)
			m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")
			sent := float64(callsTo(env.MockHA, "turn_on", want.strip)[0].Data["brightness_pct"].(int))
			held := func() bool { return m.GetShadowState().Outputs.LuxControl[want.room].Followers[want.strip].Held }

			// Inside the transition window a report is ignored, whatever it says.
			clk.Advance(5 * time.Second)
			m.handleFollowerChange(room, &ha.State{EntityID: want.strip, State: "on",
				Attributes: map[string]interface{}{"brightness": 10.0}})
			m.luxEvaluate(room, "test")
			assert.False(t, held(), "mid-transition report is the plugin's own change")

			clk.Advance(2 * time.Minute)
			// The commanded brightness, with a stray kelvin: brightness-only, so not manual.
			m.handleFollowerChange(room, &ha.State{EntityID: want.strip, State: "on",
				Attributes: map[string]interface{}{"brightness": sent / 100 * 255, "color_temp_kelvin": 6500.0}})
			m.luxEvaluate(room, "test")
			assert.False(t, held())

			// A different brightness is manual. It holds the strip, never the room.
			m.handleFollowerChange(room, &ha.State{EntityID: want.strip, State: "on",
				Attributes: map[string]interface{}{"brightness": 51.0}})
			assert.False(t, m.luxHeld(room), "the room is not held")
			m.luxEvaluate(room, "test")
			assert.True(t, held(), "shadow state shows the strip held")
			assert.False(t, m.GetShadowState().Outputs.LuxControl[want.room].Held)

			// The cans keep following the curve (evening: brightness changes), the strip is left alone.
			env.MockHA.ClearServiceCalls()
			clk.Set(at(20, 0))
			env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "0", nil)
			m.luxTick()
			assert.NotEmpty(t, callsTo(env.MockHA, "turn_on", want.cans), "cans still commanded")
			assert.Empty(t, callsTo(env.MockHA, "turn_on", want.strip), "held strip is not driven")
			assert.Empty(t, callsTo(env.MockHA, "turn_off", want.strip))

			// The strip hold clears with the room hold, e.g. everyone asleep or nobody home.
			m.clearAllLuxHolds("test")
			m.luxTick()
			assert.NotEmpty(t, callsTo(env.MockHA, "turn_on", want.strip), "driven again after the clear")
			assert.False(t, held())
		})
	}
}

func TestLuxFollowers_StripTurnedOffByHandHoldsOnlyTheStrip(t *testing.T) {
	t.Parallel()
	m, _, clk := newProductionRoomFixture(t)
	room := findRoom(m.config, "Pantry")
	m.evaluateAndActivateRoom(room, "day", "isKitchenOccupied")
	clk.Advance(2 * time.Minute)
	m.handleFollowerChange(room, &ha.State{EntityID: "light.pantry_under_cabinet", State: "off"})
	assert.False(t, m.luxHeld(room))
	m.luxEvaluate(room, "test")
	assert.True(t, m.GetShadowState().Outputs.LuxControl["Pantry"].Followers["light.pantry_under_cabinet"].Held)
}

func TestLuxFollowers_KitchenNoLongerRecallsTheUnderCabinetScene(t *testing.T) {
	t.Parallel()
	m, env, _ := newProductionRoomFixture(t)
	m.evaluateAndActivateRoom(findRoom(m.config, "Kitchen"), "day", "isKitchenOccupied")
	for _, c := range env.MockHA.GetServiceCalls() {
		assert.NotEqual(t, "scene", c.Domain, "no scene may fight the follower control")
	}
}

func TestLuxFollowersConfigValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		f    []LuxFollower
		ok   bool
	}{
		{"valid", []LuxFollower{{Entity: "light.s", FloorPct: 20, Gain: 0.8}}, true},
		{"none", nil, true},
		{"empty entity", []LuxFollower{{FloorPct: 20, Gain: 0.8}}, false},
		{"not a light", []LuxFollower{{Entity: "switch.s", FloorPct: 20, Gain: 0.8}}, false},
		{"same as cans", []LuxFollower{{Entity: "light.x", FloorPct: 20, Gain: 0.8}}, false},
		{"duplicate", []LuxFollower{{Entity: "light.s", Gain: 1}, {Entity: "light.s", Gain: 1}}, false},
		{"negative floor", []LuxFollower{{Entity: "light.s", FloorPct: -1, Gain: 0.8}}, false},
		{"floor over 100", []LuxFollower{{Entity: "light.s", FloorPct: 101, Gain: 0.8}}, false},
		{"negative gain", []LuxFollower{{Entity: "light.s", FloorPct: 20, Gain: -0.1}}, false},
		{"gain over 2", []LuxFollower{{Entity: "light.s", FloorPct: 20, Gain: 2.1}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &HueConfig{
				LuxCurve: luxTestCurve(),
				Rooms: []RoomConfig{{HueGroup: "R", LuxControl: &LuxControlConfig{
					Mode: LuxModeActive, Cans: "light.x", Calibrated: true, Calibration: luxTestCalibration,
					FollowerLights: tc.f,
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
