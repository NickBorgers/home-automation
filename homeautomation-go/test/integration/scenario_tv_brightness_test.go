package integration

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/bravia"
	"homeautomation/internal/clock"
	"homeautomation/internal/plugins/lighting"
	"homeautomation/internal/plugins/tv"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/require"
)

// fakeBraviaTV stands in for the TV's REST API. Its brightness range is 0-50, like the A80K.
type fakeBraviaTV struct {
	mu      sync.Mutex
	current int
	writes  []int
}

func (f *fakeBraviaTV) GetBrightness(context.Context) (bravia.Setting, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bravia.Setting{Current: f.current, Min: 0, Max: 50}, nil
}

func (f *fakeBraviaTV) SetBrightness(_ context.Context, v int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = v
	f.writes = append(f.writes, v)
	return nil
}

// pressRemote changes brightness the way a person with the remote does: no write is recorded.
func (f *fakeBraviaTV) pressRemote(v int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.current = v
}

func (f *fakeBraviaTV) brightness() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current
}

func (f *fakeBraviaTV) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func syncBoxBrightness(server *MockHAServer) int {
	st := server.GetState(tv.SyncBoxBrightnessEntity)
	if st == nil {
		return -1
	}
	v, err := strconv.ParseFloat(st.State, 64)
	if err != nil {
		return -1
	}
	return int(v)
}

// TestScenario_UserStory_TVAndHueBacklightFollowRoomTargetLux:
// "As the evening gets darker, I want the TV and its Hue backlight to dim with the room,
// so the screen never glares in a dark room. When I pick a brightness with the remote or the
// Hue app, it should stay until I turn the TV off."
//
// INVARIANTS:
//   - TV and sync box brightness follow the Living room's target lux while the TV is on,
//     even while the room is in `skip` because Hue Sync owns the lights.
//   - A manual change holds that device until the TV turns off and on again.
func TestScenario_UserStory_TVAndHueBacklightFollowRoomTargetLux(t *testing.T) {
	server, client, stateManager, baseCleanup := setupTest(t)
	defer baseCleanup()

	t0 := time.Date(2026, 11, 15, 22, 0, 0, 0, time.UTC)
	mockClock := clock.NewMockClock(t0)
	anchors := map[string]time.Time{
		"test.day":     t0,
		"test.evening": t0.Add(time.Hour),
		"test.night":   t0.Add(2 * time.Hour),
	}
	config := &lighting.HueConfig{
		LuxCurve: &lighting.LuxCurveConfig{
			// Room targets after lux_scale: bright day, mid evening, dark night.
			Points: []lighting.LuxCurvePoint{
				{Anchor: "test.day", Lux: 150, Kelvin: 4000},
				{Anchor: "test.evening", Lux: 20, Kelvin: 3000},
				{Anchor: "test.night", Lux: 2.5, Kelvin: 3000},
			},
			OutdoorSensor: "sensor.outdoor_lux",
			TickSeconds:   60,
		},
		Rooms: []lighting.RoomConfig{{
			HueGroup: "Living",
			Conditions: []lighting.LightingCondition{
				{Action: "skip", Variable: "isTVPlaying", Value: true},
				{Action: "on", Variable: "isAnyoneHome", Value: true},
			},
			LuxControl: &lighting.LuxControlConfig{
				Mode:        lighting.LuxModeActive,
				Cans:        "light.living_cans",
				Calibrated:  true,
				TVRoom:      true,
				Calibration: []lighting.CalibrationPoint{{BrightnessPct: 100, Lux: 100}},
			},
		}},
	}

	// GIVEN: the TV is on and playing, so Hue Sync owns the Living lights.
	t.Log("GIVEN: TV on and playing; Living room in skip; target lux 150 (day)")
	require.NoError(t, stateManager.SetBool("isAnyoneHome", true))
	server.SetState("sensor.outdoor_lux", "0", nil)
	server.SetState("light.living_cans", "off", nil)
	server.SetState(tv.TVRemoteEntity, "on", nil)
	server.SetState(tv.SyncBoxBrightnessEntity, "46", map[string]interface{}{"min": 1.0, "max": 100.0})

	fake := &fakeBraviaTV{current: 25}
	tvMgr := tv.NewManager(context.Background(), client, stateManager, testlogger.New(), false, nil)
	tvMgr.SetPictureClient(fake)
	tvMgr.SetBrightnessPowerOnDelay(0)
	require.NoError(t, tvMgr.Start())
	defer tvMgr.Stop()

	lightMgr := lighting.NewManager(context.Background(), client, stateManager, config, testlogger.New(), false, nil)
	lightMgr.SetClock(mockClock)
	lightMgr.SetAnchorResolver(func(a string, _ time.Time) (time.Time, bool) { at, ok := anchors[a]; return at, ok })
	require.NoError(t, lightMgr.Start())
	defer lightMgr.Stop()

	// THEN: day target -> both at their brightest.
	t.Log("THEN: TV at its maximum (50) and sync box at 100%")
	require.Eventually(t, func() bool { return fake.brightness() == 50 && syncBoxBrightness(server) == 100 },
		2*time.Second, 10*time.Millisecond, "day: TV=%d sync=%d", fake.brightness(), syncBoxBrightness(server))

	// WHEN: the curve reaches the evening point.
	t.Log("WHEN: T+1h, target lux falls to 20")
	mockClock.AdvanceAndProcess(time.Hour)

	// THEN: both dim part of the way. 20 lux sits at about 51% of the log range 2.5-150.
	t.Log("THEN: TV and sync box dim to mid values (TV 33, sync box 61)")
	require.Eventually(t, func() bool { return fake.brightness() == 33 && syncBoxBrightness(server) == 61 },
		2*time.Second, 10*time.Millisecond, "evening: TV=%d sync=%d", fake.brightness(), syncBoxBrightness(server))

	// WHEN: someone picks brighter settings by hand.
	t.Log("WHEN: someone sets TV brightness 40 with the remote and sync box 70 in the Hue app")
	fake.pressRemote(40)
	server.SetState(tv.SyncBoxBrightnessEntity, "70", map[string]interface{}{"min": 1.0, "max": 100.0})
	client.WaitForHandlers()
	writesBefore := fake.writeCount()

	mockClock.AdvanceAndProcess(time.Hour)
	time.Sleep(200 * time.Millisecond) // give a wrongful write time to happen

	// THEN: the night target does not undo the manual choices.
	t.Log("THEN: at T+2h (night target) both manual values are kept")
	require.Equal(t, 40, fake.brightness(), "TV manual brightness must hold")
	require.Equal(t, writesBefore, fake.writeCount(), "no TV write while held")
	require.Equal(t, 70, syncBoxBrightness(server), "sync box manual brightness must hold")

	// WHEN: the TV is turned off and on again.
	t.Log("WHEN: TV turns off, then on")
	server.SetState(tv.TVRemoteEntity, "standby", nil)
	client.WaitForHandlers()
	server.SetState(tv.TVRemoteEntity, "on", nil)
	client.WaitForHandlers()

	// THEN: the hold is gone and both follow the night target.
	t.Log("THEN: TV at night brightness 15 (30% of range) and sync box at 20%")
	require.Eventually(t, func() bool { return fake.brightness() == 15 && syncBoxBrightness(server) == 20 },
		2*time.Second, 10*time.Millisecond, "night: TV=%d sync=%d", fake.brightness(), syncBoxBrightness(server))
}
