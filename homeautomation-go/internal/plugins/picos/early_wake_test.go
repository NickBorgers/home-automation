package picos

import (
	"path/filepath"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ============================================================================
// Picos and early wake
//
// User stories:
//   - "I'm up early. I press the kitchen Pico. The house wakes up with me."
//     Targets marked wakes_house turn on isEarlyWakeActive when an up-press
//     turns them on while someone is asleep.
//   - "At 3 a.m. I press the pantry Pico on my way to the nursery." The
//     pantry target is not marked, so the house stays asleep.
//   - "A guest presses the Pico by their door to walk to the bathroom." The
//     back hall light comes on as before, and the two north living cans come
//     on very dim and warm if they are off. The house stays asleep.
//
// INVARIANTS:
//   - A press on a target without wakes_house never sets isEarlyWakeActive.
//   - Path lights never set isEarlyWakeActive.
//   - Path lights never change a light that is already on.
//   - Path lights act only while isAnyoneHomeAndAwake is false.
//   - A down-press to off turns off only path lights this plugin turned on.
// ============================================================================

const (
	livingPico      = "pico-living"
	guestDoorPico   = "pico-guest-door"
	livingCans      = "light.living_cans"
	backHallBottom  = "light.back_hall_bottom"
	northCanA       = "light.hue_color_downlight_1"
	northCanB       = "light.hue_color_downlight_3"
	pathBrightness  = 5
	pathKelvin      = 2200
	guestDoorButton = "Hallway Between Bedrooms"
)

func earlyWakeTestConfig() *Config {
	cfg := &Config{
		Targets: []Target{
			{Name: "pantry", Lights: []string{pantryZone, pantryUnderCab}},
			{Name: "kitchen", Lights: []string{kitchenZone, kitchenUnderCab}, WakesHouse: true},
			{Name: "living", Lights: []string{livingCans}, WakesHouse: true},
			{Name: "back hall", Lights: []string{backHallBottom}},
		},
		Buttons: []Button{
			{Name: pantryDoorButton, DeviceID: pantryDoorPico, Target: "pantry"},
			{Name: kitchenPicoButton, DeviceID: kitchenPico, Target: "kitchen"},
			{Name: "Hallway Living Switch", DeviceID: livingPico, Target: "living"},
			{
				Name: guestDoorButton, DeviceID: guestDoorPico, Target: "back hall",
				PathLights: &PathLights{
					Lights:        []string{northCanA, northCanB},
					BrightnessPct: pathBrightness,
					Kelvin:        pathKelvin,
					OffAfter:      15 * time.Minute,
				},
			},
		},
	}
	cfg.applyDefaults()
	return cfg
}

type wakeRig struct {
	*rig
	sm *state.Manager
}

// newWakeRig builds a rig whose Manager reads and writes house state. The house starts asleep:
// someone home, the bedroom asleep, the house not awake, every light off.
func newWakeRig(t *testing.T) *wakeRig {
	t.Helper()
	mock := ha.NewMockClient()
	sm := state.NewManager(mock, zap.NewNop(), false)
	r := &rig{t: t, ha: mock, time: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
	r.m = NewManager(t.Context(), mock, earlyWakeTestConfig(), zap.NewNop(), false)
	r.m.now = func() time.Time { return r.time }
	r.m.SetStateManager(sm)

	require.NoError(t, sm.SetBool("isAnyoneHome", true))
	require.NoError(t, sm.SetBool("isMasterAsleep", true))
	require.NoError(t, sm.SetBool("isAnyoneAsleep", true))
	require.NoError(t, sm.SetBool("isAnyoneHomeAndAwake", false))
	require.NoError(t, sm.SetBool("isEarlyWakeActive", false))

	for _, l := range []string{pantryZone, pantryUnderCab, kitchenZone, kitchenUnderCab, livingCans,
		backHallBottom, northCanA, northCanB} {
		r.setLight(l, 0)
	}
	return &wakeRig{rig: r, sm: sm}
}

func (w *wakeRig) earlyWake() bool {
	w.t.Helper()
	v, err := w.sm.GetBool("isEarlyWakeActive")
	require.NoError(w.t, err)
	return v
}

func TestEarlyWake_KitchenPicoUpFromOffWakesHouse(t *testing.T) {
	w := newWakeRig(t)
	w.start()

	w.press(kitchenPico, "on")

	w.requireTurnedOn(kitchenZone, 30)
	assert.Eventually(t, w.earlyWake, time.Second, 10*time.Millisecond,
		"an up-press that turns on a wakes_house target while asleep must start early wake")
}

func TestEarlyWake_LivingPicoUpFromOffWakesHouse(t *testing.T) {
	w := newWakeRig(t)
	w.start()

	w.press(livingPico, "on")

	assert.Eventually(t, w.earlyWake, time.Second, 10*time.Millisecond,
		"the living Pico must start early wake")
}

func TestEarlyWake_PantryPicoDoesNotWakeHouse(t *testing.T) {
	w := newWakeRig(t)
	w.start()

	w.press(pantryDoorPico, "on")

	w.requireTurnedOn(pantryZone, 30)
	assert.False(t, w.earlyWake(), "the pantry is the night path and must not wake the house")
}

func TestEarlyWake_KitchenPicoWhileEveryoneAwakeDoesNothingExtra(t *testing.T) {
	w := newWakeRig(t)
	require.NoError(t, w.sm.SetBool("isMasterAsleep", false))
	require.NoError(t, w.sm.SetBool("isAnyoneAsleep", false))
	w.start()

	w.press(kitchenPico, "on")

	w.requireTurnedOn(kitchenZone, 30)
	assert.False(t, w.earlyWake(), "early wake applies only while someone is asleep")
}

func TestEarlyWake_KitchenPicoDownPressDoesNotWakeHouse(t *testing.T) {
	w := newWakeRig(t)
	w.setLight(kitchenZone, 60)
	w.setLight(kitchenUnderCab, 60)
	w.start()

	w.press(kitchenPico, "off")

	assert.False(t, w.earlyWake(), "dimming is not a wake signal")
}

func TestGuestPath_UpPressLightsNorthCansDimAndWarm(t *testing.T) {
	w := newWakeRig(t)
	w.start()

	w.press(guestDoorPico, "on")

	w.requireTurnedOn(backHallBottom, 30)
	for _, can := range []string{northCanA, northCanB} {
		call, ok := w.lastCall(can)
		require.True(t, ok, "path light %s must turn on", can)
		assert.Equal(t, "turn_on", call.Service, can)
		assert.Equal(t, pathBrightness, call.Data["brightness_pct"], can)
		assert.Equal(t, pathKelvin, call.Data["color_temp_kelvin"], can)
	}
	assert.False(t, w.earlyWake(), "the guest path must never wake the house")
}

func TestGuestPath_LeavesCansThatAreAlreadyOn(t *testing.T) {
	w := newWakeRig(t)
	w.setLight(northCanA, 70)
	w.start()

	w.press(guestDoorPico, "on")

	w.requireTurnedOn(backHallBottom, 30)
	_, touchedA := w.lastCall(northCanA)
	assert.False(t, touchedA, "a can that is already on must be left alone")
	call, ok := w.lastCall(northCanB)
	require.True(t, ok, "the can that is off must still turn on")
	assert.Equal(t, pathBrightness, call.Data["brightness_pct"])
}

func TestGuestPath_NoPathLightsWhileHouseAwake(t *testing.T) {
	w := newWakeRig(t)
	require.NoError(t, w.sm.SetBool("isAnyoneHomeAndAwake", true))
	w.start()

	w.press(guestDoorPico, "on")

	w.requireTurnedOn(backHallBottom, 30)
	_, touchedA := w.lastCall(northCanA)
	_, touchedB := w.lastCall(northCanB)
	assert.False(t, touchedA || touchedB, "path lights are only for a sleeping house")
}

func TestGuestPath_DownToOffTurnsOffPathLightsItTurnedOn(t *testing.T) {
	w := newWakeRig(t)
	w.start()

	w.press(guestDoorPico, "on")
	w.settle()
	w.setLight(backHallBottom, 30)
	w.setLight(northCanA, pathBrightness)
	w.setLight(northCanB, pathBrightness)

	w.press(guestDoorPico, "off")

	w.requireTurnedOff(backHallBottom)
	w.requireTurnedOff(northCanA)
	w.requireTurnedOff(northCanB)
}

func TestGuestPath_DownToOffLeavesCansItDidNotTurnOn(t *testing.T) {
	w := newWakeRig(t)
	w.setLight(northCanA, 70)
	w.setLight(northCanB, 70)
	w.start()

	w.press(guestDoorPico, "on")
	w.settle()
	w.setLight(backHallBottom, 30)

	w.press(guestDoorPico, "off")

	w.requireTurnedOff(backHallBottom)
	_, touchedA := w.lastCall(northCanA)
	_, touchedB := w.lastCall(northCanB)
	assert.False(t, touchedA || touchedB, "cans someone else turned on must stay on")
}

func TestLoadConfig_ShippedEarlyWakeSettings(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", "..", "..", "configs", "pico_config.yaml"))
	require.NoError(t, err)

	wakes := make(map[string]bool)
	for _, target := range cfg.Targets {
		wakes[target.Name] = target.WakesHouse
	}
	assert.True(t, wakes["kitchen"], "the kitchen target wakes the house")
	assert.True(t, wakes["living"], "the living target wakes the house")
	assert.False(t, wakes["pantry"], "the pantry target is the night path")
	assert.False(t, wakes["back hall"], "the back hall target is the guest path")

	var guestDoor *Button
	for i := range cfg.Buttons {
		if cfg.Buttons[i].DeviceID == "80f92d7f29fdbef7643ed1b002532267" {
			guestDoor = &cfg.Buttons[i]
		}
	}
	require.NotNil(t, guestDoor, "the Pico between the bedrooms must be configured")
	require.NotNil(t, guestDoor.PathLights, "the Pico between the bedrooms lights the guest path")
	assert.ElementsMatch(t, []string{northCanA, northCanB}, guestDoor.PathLights.Lights)
	assert.Positive(t, guestDoor.PathLights.BrightnessPct)
	assert.LessOrEqual(t, guestDoor.PathLights.BrightnessPct, 10, "guest path lights must be very dim")
	assert.Positive(t, guestDoor.PathLights.OffAfter)

	for _, b := range cfg.Buttons {
		if b.DeviceID != guestDoor.DeviceID {
			assert.Nil(t, b.PathLights, "only the Pico between the bedrooms has path lights: %s", b.Name)
		}
	}
}
