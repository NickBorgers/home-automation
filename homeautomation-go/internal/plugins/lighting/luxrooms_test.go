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

// productionLuxRooms lists the rooms with measured calibration, with the measured cans-only lux.
var productionLuxRooms = []struct {
	room   string
	cans   string
	factor float64
	lux    [3]float64 // cans-only lux at 25%, 50%, 100%
}{
	{"Living Room", "light.living_cans", 0.0025, [3]float64{3, 9, 36}},
	{"Kitchen", "light.kitchen_cans", 0.0016, [3]float64{4, 14, 54}},
	{"Pantry", "light.pantry_cans", 0.0034, [3]float64{11, 41, 162}},
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
			require.Len(t, lc.Calibration, 3)
			for i, pct := range []int{25, 50, 100} {
				assert.Equal(t, pct, lc.Calibration[i].BrightnessPct)
				assert.InDelta(t, want.lux[i], lc.Calibration[i].Lux, 1e-9)
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
			for _, lux := range []float64{want.lux[0] / 2, want.lux[0], want.lux[1], want.lux[2]} {
				pct := luxToBrightness(cal, lux)
				assert.GreaterOrEqual(t, pct, prev, "brightness must not fall as needed lux rises")
				assert.LessOrEqual(t, pct, 100.0)
				assert.InDelta(t, lux, brightnessToLux(cal, pct), 0.01, "round trip")
				prev = pct
			}
			assert.InDelta(t, 25, luxToBrightness(cal, want.lux[0]), 0.001)
			assert.InDelta(t, 50, luxToBrightness(cal, want.lux[1]), 0.001)
			assert.Equal(t, 100.0, luxToBrightness(cal, want.lux[2]*2), "cans cannot give more than 100%")
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
	assert.Empty(t, pantry.CompanionScenes, "the pantry has no under-cabinet light")
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
		if c.Domain == "scene" {
			// Only the Kitchen under-cabinet companion may be recalled, never scene.kitchen_day or scene.pantry_day.
			assert.Equal(t, "scene.kitchen_under_cabinet_day", c.Data["entity_id"])
		}
		if c.Domain == "light" && c.Service == "turn_on" {
			cans = append(cans, c.Data["entity_id"].(string))
		}
	}
	assert.ElementsMatch(t, []string{"light.kitchen_cans", "light.pantry_cans"}, cans)
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
	assert.Equal(t, "light.pantry_cans", calls[0].Data["entity_id"], "no hass_area_id, so target the cans")
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
			assert.NotEqual(t, "light.pantry_cans", c.Data["entity_id"])
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
