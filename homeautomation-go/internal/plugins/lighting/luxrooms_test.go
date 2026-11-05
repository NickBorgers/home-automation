package lighting

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/pkg/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for the production lux_control blocks of the Living Room, Kitchen and Pantry.
// The numbers are a temporary phone-app calibration; see docs/flows/LUX_LIGHTING.md.

func loadProductionConfig(t *testing.T) *HueConfig {
	t.Helper()
	cfg, err := LoadConfig(filepath.Join("..", "..", "..", "..", "configs", "hue_config.yaml"))
	require.NoError(t, err)
	return cfg
}

func findRoom(cfg *HueConfig, name string) *RoomConfig {
	for i := range cfg.Rooms {
		if cfg.Rooms[i].HueGroup == name {
			return &cfg.Rooms[i]
		}
	}
	return nil
}

// luxPoint is one measured calibration point: the lux at a brightness.
type luxPoint struct {
	pct int
	lux float64
}

// productionLuxRooms lists the cans rooms with measured calibration. The Living Room and Kitchen come from a
// handheld lux meter (2026-10-31/11-01). The Pantry cans were measured on 2026-11-02 and its factor on 2026-11-05. The Mudroom (2026-11-05, daylight) is a single factor reading.
var productionLuxRooms = []struct {
	room   string
	cans   string
	factor float64
	points []luxPoint
}{
	{"Living Room", "light.living_cans", 0.0011,
		[]luxPoint{{5, 0.7}, {10, 1.65}, {15, 3.0}, {25, 7.9}, {50, 30.1}, {100, 119.0}}},
	{"Kitchen", "light.kitchen_cans", 0.0012,
		[]luxPoint{{5, 1.3}, {10, 3.1}, {15, 5.9}, {25, 15.8}, {50, 61.1}, {100, 237}}},
	{"Pantry", "light.pantry", 0.00082,
		[]luxPoint{{5, 1}, {10, 3}, {15, 6}, {25, 16}, {50, 64}, {100, 263}}},
	{"Mudroom", "light.mudroom", 0.00013,
		[]luxPoint{{5, 0.8}, {10, 1.8}, {15, 3.1}, {25, 8.5}, {50, 33.7}, {100, 131.4}}},
}

func TestProductionConfig_LuxRoomsAreActiveAndMeasured(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)
	for _, want := range productionLuxRooms {
		t.Run(want.room, func(t *testing.T) {
			room := findRoom(cfg, want.room)
			require.NotNil(t, room, "room missing from hue_config.yaml")
			lc := room.LuxControl
			require.NotNil(t, lc, "lux_control missing")
			assert.Equal(t, LuxModeActive, lc.Mode)
			assert.Equal(t, want.cans, lc.Cans)
			assert.True(t, lc.Calibrated)
			assert.InDelta(t, want.factor, lc.DaylightFactor, 1e-9)
			assert.InDelta(t, 0.5, lc.LuxScale, 1e-9)
			require.Len(t, lc.Calibration, len(want.points))
			for i, p := range want.points {
				assert.Equal(t, p.pct, lc.Calibration[i].BrightnessPct)
				assert.InDelta(t, p.lux, lc.Calibration[i].Lux, 1e-9)
			}
		})
	}
}

func TestProductionConfig_CalibrationMapsSanely(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)
	for _, want := range productionLuxRooms {
		t.Run(want.room, func(t *testing.T) {
			cal := findRoom(cfg, want.room).LuxControl.Calibration
			prev := 0.0
			lux := []float64{want.points[0].lux / 2}
			for _, p := range want.points {
				lux = append(lux, p.lux)
			}
			for _, l := range lux {
				pct := luxToBrightness(cal, l)
				assert.GreaterOrEqual(t, pct, prev, "brightness must not fall as needed lux rises")
				assert.LessOrEqual(t, pct, 100.0)
				assert.InDelta(t, l, brightnessToLux(cal, pct), 0.01, "round trip")
				prev = pct
			}
			for _, p := range want.points {
				assert.InDelta(t, float64(p.pct), luxToBrightness(cal, p.lux), 0.001)
			}
			top := want.points[len(want.points)-1].lux
			assert.Equal(t, 100.0, luxToBrightness(cal, top*2), "cans cannot give more than 100%")
			// The scaled night target (5 lux x 0.5) must be reachable and not switch the cans off.
			assert.True(t, shouldBeOn(cal, 2.5, false), "night target is above the off threshold")
		})
	}
}

func TestProductionConfig_PantryFollowsKitchenConditions(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)
	kitchen, pantry := findRoom(cfg, "Kitchen"), findRoom(cfg, "Pantry")
	require.NotNil(t, kitchen)
	require.NotNil(t, pantry)
	assert.Equal(t, kitchen.Conditions, pantry.Conditions)
	assert.NotEmpty(t, pantry.Conditions)
	assert.Empty(t, pantry.CompanionScenes, "the pantry strip follows the cans, no scenes")
	assert.Empty(t, kitchen.CompanionScenes, "the kitchen strip follows the cans, no scenes")
	assert.Empty(t, pantry.LuxControl.AccentSceneGroup, "the pantry has no accent lights")
	assert.Empty(t, kitchen.LuxControl.AccentSceneGroup, "the kitchen has no accent lights")
}

// newProductionRoomFixture builds a manager from the production config at noon with 7,500 outdoor lux.
func newProductionRoomFixture(t *testing.T) (*Manager, *testutil.Env, *clock.MockClock) {
	t.Helper()
	env := testutil.NewEnv(t)
	cfg := loadProductionConfig(t)
	m := NewManager(context.Background(), env.MockHA, env.StateMgr, cfg, env.Logger, false, nil)
	m.luxOutdoor = &smoother{window: luxDefaultSmoothing}
	clk := clock.NewMockClock(at(12, 0))
	m.SetClock(clk)
	m.SetAnchorResolver(luxTestAnchors)
	env.MockHA.SetState(cfg.LuxCurve.OutdoorSensor, "7500", nil)
	require.NoError(t, env.StateMgr.SetBool("isAnyoneHomeAndAwake", true))
	require.NoError(t, env.StateMgr.SetBool("isKitchenOccupied", true))
	require.NoError(t, env.StateMgr.SetBool("isTVPlaying", false))
	require.NoError(t, env.StateMgr.SetBool("isHaveGuests", false))
	return m, env, clk
}

func TestLuxRooms_KitchenAndPantryCansAreDrivenByLuxNotScenes(t *testing.T) {
	t.Parallel()
	m, env, _ := newProductionRoomFixture(t)

	for _, name := range []string{"Kitchen", "Pantry"} {
		m.evaluateAndActivateRoom(findRoom(m.config, name), "day", "isKitchenOccupied")
	}

	var cans []string
	for _, c := range env.MockHA.GetServiceCalls() {
		assert.NotEqual(t, "scene", c.Domain, "no scene is recalled; the strips follow the cans")
		if c.Domain == "light" && c.Service == "turn_on" {
			cans = append(cans, c.Data["entity_id"].(string))
		}
	}
	assert.ElementsMatch(t, []string{"light.kitchen_cans", "light.pantry", "light.kitchen_main_lights", "light.pantry_under_cabinet"}, cans)
}

func TestLuxRooms_PantryTurnsOffWithKitchenConditionsWithoutAnArea(t *testing.T) {
	t.Parallel()
	m, env, _ := newProductionRoomFixture(t)
	pantry := findRoom(m.config, "Pantry")
	m.evaluateAndActivateRoom(pantry, "day", "isKitchenOccupied")
	env.MockHA.ClearServiceCalls()

	require.NoError(t, env.StateMgr.SetBool("isAnyoneHomeAndAwake", false))
	env.MockHA.ClearServiceCalls() // drop the input_boolean write made by SetBool
	m.evaluateAndActivateRoom(pantry, "day", "isAnyoneHomeAndAwake")

	calls := env.MockHA.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "turn_off", calls[0].Service)
	assert.Equal(t, []string{"light.pantry", "light.pantry_under_cabinet"}, calls[0].Data["entity_id"],
		"no hass_area_id, so target the cans and the strip")
	assert.NotContains(t, calls[0].Data, "area_id")
}

func TestLuxRooms_PantryStaysQuietWhenKitchenIsNotOccupied(t *testing.T) {
	t.Parallel()
	m, env, _ := newProductionRoomFixture(t)
	require.NoError(t, env.StateMgr.SetBool("isKitchenOccupied", false))

	m.evaluateAndActivateRoom(findRoom(m.config, "Pantry"), "day", "isKitchenOccupied")
	m.luxTick() // the loop also leaves a room alone when its conditions do not say "on"

	for _, c := range env.MockHA.GetServiceCalls() {
		if c.Domain == "light" && c.Service == "turn_on" {
			assert.NotEqual(t, "light.pantry", c.Data["entity_id"])
		}
	}
}

func TestLuxConfigValidation_AccentSceneGroupIsOptional(t *testing.T) {
	t.Parallel()
	cfg := &HueConfig{
		LuxCurve: luxTestCurve(),
		Rooms: []RoomConfig{{HueGroup: "R", LuxControl: &LuxControlConfig{
			Mode: LuxModeActive, Cans: "light.x", Calibrated: true, Calibration: luxTestCalibration,
		}}},
	}
	assert.NoError(t, cfg.validateLux())
}

func TestLuxRooms_ManualChangeHoldsEachRoom(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Living Room", "Kitchen", "Pantry"} {
		t.Run(name, func(t *testing.T) {
			m, env, clk := newProductionRoomFixture(t)
			room := findRoom(m.config, name)
			m.evaluateAndActivateRoom(room, "day", "isAnyoneHomeAndAwake")
			m.luxMu.Lock()
			require.NotNil(t, m.luxRoom(name).cmd, "plugin should have commanded the cans")
			m.luxMu.Unlock()

			clk.Advance(2 * time.Minute)
			report := canReport("on", 255, 3000)
			report.EntityID = room.LuxControl.Cans
			m.handleCansChange(room, report)
			assert.True(t, m.luxHeld(room))

			env.MockHA.ClearServiceCalls()
			m.evaluateAndActivateRoom(room, "evening", "dayPhase")
			clk.Advance(time.Hour)
			m.luxTick()
			for _, c := range env.MockHA.GetServiceCalls() {
				assert.NotEqual(t, room.LuxControl.Cans, c.Data["entity_id"],
					"a held room's cans are left alone, even on a phase change")
			}
		})
	}
}

func TestLuxRooms_PantryManualTurnOnDuringFadeToOffWins(t *testing.T) {
	t.Parallel()
	m, env, clk := newProductionRoomFixture(t)
	pantry := findRoom(m.config, "Pantry")
	env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "0", nil)
	m.luxTick()
	clk.Advance(6 * time.Minute)

	// Pantry factor 0.00082: 300,000 outdoor lux gives about 246 indoor lux, above the scaled day target.
	env.MockHA.SetState(m.config.LuxCurve.OutdoorSensor, "300000", nil)
	env.MockHA.ClearServiceCalls()
	m.luxTick()
	calls := env.MockHA.GetServiceCalls()
	offCall := -1
	for i := range calls {
		if calls[i].Service == "turn_off" && calls[i].Data["entity_id"] == pantry.LuxControl.Cans {
			offCall = i
		}
	}
	require.NotEqual(t, -1, offCall)
	assert.Equal(t, float64(60), calls[offCall].Data["transition"])

	clk.Advance(15 * time.Second)
	report := canReport("on", 204, 4000)
	report.EntityID = pantry.LuxControl.Cans
	m.handleCansChange(pantry, report)
	assert.True(t, m.luxHeld(pantry))
	m.luxMu.Lock()
	assert.Equal(t, "cans turned on by hand", m.luxRoom("Pantry").heldReason)
	m.luxMu.Unlock()
	calls = env.MockHA.GetServiceCalls()
	require.Equal(t, "turn_on", calls[len(calls)-1].Service)
	assert.Equal(t, pantry.LuxControl.Cans, calls[len(calls)-1].Data["entity_id"])
	assert.Equal(t, 80, calls[len(calls)-1].Data["brightness_pct"])
	assert.Equal(t, 4000, calls[len(calls)-1].Data["color_temp_kelvin"])
	assert.Equal(t, float64(0), calls[len(calls)-1].Data["transition"])

	clk.Advance(time.Minute)
	m.handleCansChange(pantry, report)
	assert.True(t, m.luxHeld(pantry))
	env.MockHA.ClearServiceCalls()
	m.evaluateAndActivateRoom(pantry, "evening", "dayPhase")
	m.luxTick()
	for _, call := range env.MockHA.GetServiceCalls() {
		assert.NotEqual(t, pantry.LuxControl.Cans, call.Data["entity_id"],
			"phase and lux evaluations must preserve the held manual state")
	}
}

func TestProductionConfig_MudroomIsSeparateFromPantryButSharesConditions(t *testing.T) {
	t.Parallel()
	cfg := loadProductionConfig(t)
	pantry, mudroom := findRoom(cfg, "Pantry"), findRoom(cfg, "Mudroom")
	require.NotNil(t, pantry)
	require.NotNil(t, mudroom)
	assert.Equal(t, "light.pantry", pantry.LuxControl.Cans)
	assert.Equal(t, "light.mudroom", mudroom.LuxControl.Cans)
	assert.NotEqual(t, pantry.LuxControl.Cans, mudroom.LuxControl.Cans, "separate cans entities")
	assert.Equal(t, pantry.Conditions, mudroom.Conditions)
	assert.Empty(t, mudroom.HASSAreaID)
	assert.Empty(t, mudroom.LuxControl.AccentSceneGroup)
	assert.Empty(t, mudroom.LuxControl.FollowerLights, "the mudroom has no strip")
	assert.Equal(t, pantry.LuxControl.LuxScale, mudroom.LuxControl.LuxScale)
	assert.Equal(t, pantry.TransitionSeconds, mudroom.TransitionSeconds)
}

func TestLuxRooms_MudroomTurnsOffOnItsOwnWithoutAnArea(t *testing.T) {
	t.Parallel()
	m, env, _ := newProductionRoomFixture(t)
	mudroom := findRoom(m.config, "Mudroom")
	m.evaluateAndActivateRoom(mudroom, "day", "isKitchenOccupied")
	require.NoError(t, env.StateMgr.SetBool("isAnyoneHomeAndAwake", false))
	env.MockHA.ClearServiceCalls()
	m.evaluateAndActivateRoom(mudroom, "day", "isAnyoneHomeAndAwake")
	calls := env.MockHA.GetServiceCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "turn_off", calls[0].Service)
	assert.Equal(t, "light.mudroom", calls[0].Data["entity_id"], "no hass_area_id, so target the cans")
	assert.NotContains(t, calls[0].Data, "area_id")
}
