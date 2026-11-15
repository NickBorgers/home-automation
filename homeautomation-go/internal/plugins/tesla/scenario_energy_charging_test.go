package tesla

// Scenario tests for energy-level charging, told from the household's point of
// view. The intended behavior:
//
//  1. The car always charges to the 50% floor, whatever the energy level.
//  2. Above the floor it charges only on generated energy: slowly at green,
//     at full speed at white, and not at all at red or yellow.
//  3. Both happen automatically. The car is normally plugged in at home, so a
//     sleeping car or an app restart must not stop it from charging.
//
// These tests live in package tesla rather than test/integration because the
// Fleet API client and the commander are unexported interfaces that a test
// must fake. fakeCar below plays the part of the car: it answers polls the way
// Tesla does, and it reacts to wake, start, stop, and amps commands.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	teslaapi "homeautomation/internal/tesla"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const scenarioMaxAmps = 48

// fakeCar is a stateful stand-in for the car and the Fleet API. It implements
// fleetReader and chargeCommander.
type fakeCar struct {
	mu sync.Mutex

	online   bool
	atHome   bool
	plugged  bool
	charging bool
	battery  int
	limit    int
	amps     int

	// wakeFails keeps the car asleep after a wake, like a car with no signal.
	wakeFails bool

	wakes    int
	commands []string
	requests int64
}

// newFakeCar returns a car parked at home, plugged in, not charging, asleep,
// with an 80% limit and the charger at full current.
func newFakeCar(battery int) *fakeCar {
	return &fakeCar{atHome: true, plugged: true, battery: battery, limit: 80, amps: scenarioMaxAmps}
}

func (c *fakeCar) Vehicles(context.Context) ([]teslaapi.VehicleSummary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
	state := "asleep"
	if c.online {
		state = "online"
	}
	return []teslaapi.VehicleSummary{{VIN: "BBB", State: state}}, nil
}

func (c *fakeCar) VehicleData(context.Context, string) (*teslaapi.VehicleData, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests++
	if !c.online {
		return nil, errors.New("vehicle unavailable: asleep")
	}
	state := "Stopped"
	switch {
	case !c.plugged:
		state = "Disconnected"
	case c.battery >= c.limit:
		state = "Complete"
	case c.charging:
		state = "Charging"
	}
	lat := homeLat + 0.0005
	if !c.atHome {
		lat = homeLat + 1 // about 110 km away
	}
	return &teslaapi.VehicleData{
		ChargeState: teslaapi.ChargeState{
			BatteryLevel:            c.battery,
			ChargeLimitSOC:          c.limit,
			ChargingState:           state,
			ChargeCurrentRequest:    c.amps,
			ChargeCurrentRequestMax: scenarioMaxAmps,
		},
		DriveState: teslaapi.DriveState{Latitude: lat, Longitude: homeLon},
	}, nil
}

func (c *fakeCar) RequestCount() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func (c *fakeCar) Wake(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wakes++
	c.commands = append(c.commands, "wake")
	if !c.wakeFails {
		c.online = true
	}
	return nil
}

func (c *fakeCar) ChargeStart(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commands = append(c.commands, "start")
	if !c.online {
		return errors.New("vehicle unavailable: asleep")
	}
	if c.plugged {
		c.charging = true
	}
	return nil
}

func (c *fakeCar) ChargeStop(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commands = append(c.commands, "stop")
	if !c.online {
		return errors.New("vehicle unavailable: asleep")
	}
	c.charging = false
	return nil
}

func (c *fakeCar) SetChargingAmps(_ context.Context, _ string, amps int32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commands = append(c.commands, fmt.Sprintf("amps=%d", amps))
	if !c.online {
		return errors.New("vehicle unavailable: asleep")
	}
	c.amps = int(amps)
	return nil
}

// set changes the car under its lock.
func (c *fakeCar) set(change func(c *fakeCar)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	change(c)
}

// sleep puts the car to sleep. A sleeping car does not charge.
func (c *fakeCar) sleep() { c.set(func(c *fakeCar) { c.online = false; c.charging = false }) }

func (c *fakeCar) isCharging() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.charging
}

func (c *fakeCar) chargingAmps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.amps
}

func (c *fakeCar) wakeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wakes
}

func (c *fakeCar) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.commands...)
}

// scenarioClock is a settable clock for the wake budget.
type scenarioClock struct{ now time.Time }

func (s *scenarioClock) advance(d time.Duration) { s.now = s.now.Add(d) }

// startApp builds a fresh Manager for the car, the way an app start or restart
// does: it remembers nothing about the car. The poll interval is the 60 minutes
// production uses.
func startApp(car *fakeCar, levels *fakeLevels, clock *scenarioClock) *Manager {
	m := newPollingManager(car, true)
	m.cfg.PollInterval = time.Hour
	m.homeLat, m.homeLon = homeLat, homeLon
	m.charger = car
	m.levels = levels
	m.now = func() time.Time { return clock.now }
	m.schedulePoll = func(time.Duration) {} // the test drives every poll itself
	return m
}

// pollUntilQuiet runs polls the way the follow-up timers would, until a poll
// sends no new command. It stops after a few rounds so a loop cannot hang.
func pollUntilQuiet(m *Manager, car *fakeCar) {
	for i := 0; i < 6; i++ {
		before := len(car.sent())
		m.Poll()
		if len(car.sent()) == before {
			return
		}
	}
}

func TestScenario_TeslaCharging_RestartWithSleepingPluggedCarBelowFloor(t *testing.T) {
	// GIVEN: the car is plugged in at home at 37%, asleep, and not charging. The
	// energy level is red.
	t.Log("GIVEN: car asleep, plugged in at home at 37%, energy red")
	car := newFakeCar(37)
	levels := &fakeLevels{level: "red"}
	clock := &scenarioClock{now: time.Date(2026, 11, 14, 4, 47, 0, 0, time.UTC)}

	// WHEN: the app restarts and polls.
	t.Log("WHEN: the app restarts, remembering nothing about the car")
	m := startApp(car, levels, clock)
	pollUntilQuiet(m, car)

	// THEN: the plugin wakes the car to learn its state, then charges it at full
	// speed, because the floor applies whatever the energy level.
	t.Log("THEN: the car is woken and charges at full speed toward the 50% floor")
	assert.Equal(t, 1, car.wakeCount(), "a sleeping car of unknown state must be woken once to check it")
	assert.True(t, car.isCharging(), "a plugged-in car below 50% must charge, even at red")
	assert.Equal(t, scenarioMaxAmps, car.chargingAmps(), "below the floor the car charges at full speed")

	// WHEN: the car reaches the floor.
	t.Log("WHEN: the battery reaches 50% while red")
	car.set(func(c *fakeCar) { c.battery = 50 })
	clock.advance(time.Hour)
	pollUntilQuiet(m, car)

	// THEN: red holds charging at the floor.
	t.Log("THEN: charging stops at the floor")
	assert.False(t, car.isCharging(), "above the floor, red must not charge")
}

func TestScenario_TeslaCharging_ArrivesHomeWhilePollingHourly(t *testing.T) {
	// GIVEN: the car is out, unplugged, at 45%. Production polls hourly.
	t.Log("GIVEN: car away at 45%, configured poll interval 60 minutes")
	car := newFakeCar(45)
	car.set(func(c *fakeCar) { c.online, c.atHome, c.plugged = true, false, false })
	levels := &fakeLevels{level: "yellow"}
	clock := &scenarioClock{now: time.Date(2026, 11, 14, 17, 0, 0, 0, time.UTC)}
	m := startApp(car, levels, clock)
	pollUntilQuiet(m, car)
	require.False(t, car.isCharging())

	// THEN: an away car is still checked often. A car stays awake only a few
	// minutes after it is parked and plugged in, so an hourly poll misses it.
	t.Log("THEN: polling stays at the discovery rate while the car is away")
	assert.LessOrEqual(t, m.nextPollInterval(), teslaapi.DefaultPollInterval,
		"an away car must be polled often enough to catch it arriving and being plugged in")

	// WHEN: the car comes home and is plugged in, still awake.
	t.Log("WHEN: the car arrives home and is plugged in")
	car.set(func(c *fakeCar) { c.atHome, c.plugged = true, true })
	clock.advance(teslaapi.DefaultPollInterval)
	pollUntilQuiet(m, car)

	// THEN: it charges at full speed toward the floor.
	t.Log("THEN: the car charges at full speed toward 50%")
	assert.True(t, car.isCharging())
	assert.Equal(t, scenarioMaxAmps, car.chargingAmps())
}

func TestScenario_TeslaCharging_SleptThroughArrivalBelowFloor(t *testing.T) {
	// GIVEN: the plugin last saw the car away, unplugged, at 40%.
	t.Log("GIVEN: car last seen away and unplugged at 40%")
	car := newFakeCar(40)
	car.set(func(c *fakeCar) { c.online, c.atHome, c.plugged = true, false, false })
	levels := &fakeLevels{level: "red"}
	clock := &scenarioClock{now: time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)}
	m := startApp(car, levels, clock)
	pollUntilQuiet(m, car)

	// WHEN: the car came home, was plugged in, and fell asleep between polls.
	t.Log("WHEN: the car came home, was plugged in, and fell asleep unseen")
	car.set(func(c *fakeCar) { c.atHome, c.plugged = true, true })
	car.sleep()
	clock.advance(time.Hour)
	pollUntilQuiet(m, car)

	// THEN: a car last known below the floor is woken to check, and it charges.
	t.Log("THEN: the plugin wakes the car and starts charging toward 50%")
	assert.Equal(t, 1, car.wakeCount(), "a car last known below the floor must be woken to check it")
	assert.True(t, car.isCharging())
	assert.Equal(t, scenarioMaxAmps, car.chargingAmps())
}

func TestScenario_TeslaCharging_GreenAfterRestartWakesAndChargesSlowly(t *testing.T) {
	// GIVEN: the car is plugged in at home at 60%, asleep, held at yellow.
	t.Log("GIVEN: car asleep, plugged in at home at 60%, energy yellow")
	car := newFakeCar(60)
	levels := &fakeLevels{level: "yellow"}
	clock := &scenarioClock{now: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)}

	// WHEN: the app restarts.
	t.Log("WHEN: the app restarts")
	m := startApp(car, levels, clock)
	pollUntilQuiet(m, car)

	// THEN: the plugin looks once, sees a held car above the floor, and leaves
	// it stopped.
	t.Log("THEN: the car is checked once and stays stopped at yellow")
	assert.Equal(t, 1, car.wakeCount())
	assert.False(t, car.isCharging())
	car.sleep()

	// WHEN: the energy level turns green while the car sleeps.
	t.Log("WHEN: the energy level turns green")
	levels.level = "green"
	clock.advance(30 * time.Minute)
	pollUntilQuiet(m, car)

	// THEN: the car is woken and charges slowly.
	t.Log("THEN: the car is woken and charges slowly at green")
	assert.Equal(t, 2, car.wakeCount())
	assert.True(t, car.isCharging(), "green must charge a plugged-in car above the floor")
	assert.Equal(t, greenChargeAmps, car.chargingAmps(), "green charges slowly")

	// WHEN: the level turns white.
	t.Log("WHEN: the energy level turns white")
	levels.level = "white"
	clock.advance(30 * time.Minute)
	pollUntilQuiet(m, car)

	// THEN: it charges at full speed.
	t.Log("THEN: the car charges at full speed")
	assert.True(t, car.isCharging())
	assert.Equal(t, scenarioMaxAmps, car.chargingAmps(), "white charges at full speed")

	// WHEN: the level falls to yellow.
	t.Log("WHEN: the energy level falls to yellow")
	levels.level = "yellow"
	clock.advance(30 * time.Minute)
	pollUntilQuiet(m, car)

	// THEN: charging stops.
	t.Log("THEN: charging stops")
	assert.False(t, car.isCharging(), "yellow must not charge above the floor")
}

// TestScenario_TeslaCharging_Nov14_PluggedInAllDayMissedFloorAndSolar replays
// the report that started this work: "I came out to the car today and it was at
// 37% in the evening after a day we generated a fair bit of power."
func TestScenario_TeslaCharging_Nov14_PluggedInAllDayMissedFloorAndSolar(t *testing.T) {
	// GIVEN: overnight, the car is plugged in at home at 52%, asleep. Energy is
	// red.
	t.Log("GIVEN: 22:00, car asleep, plugged in at home at 52%, energy red")
	car := newFakeCar(52)
	levels := &fakeLevels{level: "red"}
	clock := &scenarioClock{now: time.Date(2026, 11, 13, 22, 0, 0, 0, time.UTC)}

	// WHEN: a deploy restarts the app in the night.
	t.Log("WHEN: 04:47, a deploy restarts the app")
	clock.advance(6*time.Hour + 47*time.Minute)
	m := startApp(car, levels, clock)
	pollUntilQuiet(m, car)
	require.False(t, car.isCharging(), "52% at red is above the floor: hold")
	car.sleep()

	// WHEN: the night goes on. The battery drains while the car sleeps, which
	// the plugin cannot see.
	t.Log("WHEN: the night goes on and the battery drains to 45% unseen")
	for hour := 0; hour < 4; hour++ {
		clock.advance(time.Hour)
		m.Poll()
	}
	car.set(func(c *fakeCar) { c.battery = 45 })

	// WHEN: morning, yellow.
	t.Log("WHEN: 09:00, energy yellow")
	levels.level = "yellow"
	clock.advance(time.Hour)
	pollUntilQuiet(m, car)

	// WHEN: the sun comes out: green.
	t.Log("WHEN: 11:00, energy green")
	levels.level = "green"
	clock.advance(2 * time.Hour)
	pollUntilQuiet(m, car)

	// THEN: the car is charging, at full speed because it is below the floor.
	t.Log("THEN: the car charges at full speed, because it is below 50%")
	require.True(t, car.isCharging(), "a plugged-in car must charge when the sun is out")
	assert.Equal(t, scenarioMaxAmps, car.chargingAmps(), "below the floor the car charges at full speed")

	// WHEN: the car passes the floor while green.
	t.Log("WHEN: 12:00, the battery passes 50% while green")
	car.set(func(c *fakeCar) { c.battery = 51 })
	clock.advance(time.Hour)
	pollUntilQuiet(m, car)

	// THEN: it slows to the green rate.
	t.Log("THEN: the car charges slowly at green")
	assert.True(t, car.isCharging())
	assert.Equal(t, greenChargeAmps, car.chargingAmps())

	// WHEN: midday, white.
	t.Log("WHEN: 13:00, energy white")
	levels.level = "white"
	clock.advance(time.Hour)
	pollUntilQuiet(m, car)

	// THEN: full speed.
	t.Log("THEN: the car charges at full speed")
	assert.True(t, car.isCharging())
	assert.Equal(t, scenarioMaxAmps, car.chargingAmps())

	// WHEN: late afternoon, yellow, at 62%.
	t.Log("WHEN: 16:00, energy yellow at 62%")
	car.set(func(c *fakeCar) { c.battery = 62 })
	levels.level = "yellow"
	clock.advance(3 * time.Hour)
	pollUntilQuiet(m, car)

	// THEN: charging stops, and the evening finds the car above the floor with
	// the day's solar in it.
	t.Log("THEN: charging stops; in the evening the car is above 50%")
	assert.False(t, car.isCharging(), "yellow must not charge above the floor")
	car.mu.Lock()
	battery := car.battery
	car.mu.Unlock()
	assert.GreaterOrEqual(t, battery, energyChargingFloorPercent)
	assert.LessOrEqual(t, car.wakeCount(), maxWakesPer24h)
}

func TestScenario_TeslaCharging_HeldAboveFloorDoesNotSpendWakes(t *testing.T) {
	// GIVEN: the car is plugged in at home at 60%, held at red, then asleep.
	t.Log("GIVEN: car seen plugged in at home at 60%, held at red, now asleep")
	car := newFakeCar(60)
	car.set(func(c *fakeCar) { c.online = true })
	levels := &fakeLevels{level: "red"}
	clock := &scenarioClock{now: time.Date(2026, 11, 14, 20, 0, 0, 0, time.UTC)}
	m := startApp(car, levels, clock)
	pollUntilQuiet(m, car)
	require.False(t, car.isCharging())
	car.sleep()

	// WHEN: many polls run through the night.
	t.Log("WHEN: polls run every 5 minutes for 12 hours")
	for i := 0; i < 144; i++ {
		clock.advance(5 * time.Minute)
		m.Poll()
	}

	// THEN: invariant: a car known to be above the floor with nothing to charge
	// on is never woken.
	t.Log("THEN: no wake is spent")
	assert.Zero(t, car.wakeCount())
}

func TestScenario_TeslaCharging_AwayAboveFloorIsNotWoken(t *testing.T) {
	// GIVEN: the car was last seen away at 70%, and is now asleep. Energy is
	// white.
	t.Log("GIVEN: car last seen away at 70%, asleep, energy white")
	car := newFakeCar(70)
	car.set(func(c *fakeCar) { c.online, c.atHome, c.plugged = true, false, false })
	levels := &fakeLevels{level: "white"}
	clock := &scenarioClock{now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	m := startApp(car, levels, clock)
	pollUntilQuiet(m, car)
	car.sleep()

	// WHEN: polls run for hours.
	t.Log("WHEN: polls run every 5 minutes for 6 hours")
	for i := 0; i < 72; i++ {
		clock.advance(5 * time.Minute)
		m.Poll()
	}

	// THEN: invariant: a car last seen away above the floor is not woken.
	t.Log("THEN: no wake is spent")
	assert.Zero(t, car.wakeCount())
}

func TestScenario_TeslaCharging_WakeCapHolds(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m *Manager, car *fakeCar)
	}{
		{
			// The app just started and has never seen the car online.
			name:  "state unknown since restart",
			setup: func(*Manager, *fakeCar) {},
		},
		{
			// The car was last seen away below the floor.
			name: "last seen away below the floor",
			setup: func(m *Manager, car *fakeCar) {
				car.set(func(c *fakeCar) { c.online, c.atHome, c.plugged = true, false, false })
				m.Poll()
				car.set(func(c *fakeCar) { c.atHome, c.plugged = true, true })
				car.sleep()
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN: a car that stays asleep after every wake.
			t.Log("GIVEN: a car that does not come online when woken")
			car := newFakeCar(40)
			levels := &fakeLevels{level: "red"}
			clock := &scenarioClock{now: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)}
			m := startApp(car, levels, clock)
			tc.setup(m, car)
			car.set(func(c *fakeCar) { c.wakeFails = true })

			// WHEN: polls run every 5 minutes for a day.
			t.Log("WHEN: polls run every 5 minutes for 24 hours")
			for i := 0; i < 288; i++ {
				m.Poll()
				clock.advance(5 * time.Minute)
			}

			// THEN: invariant: wakes never exceed the daily cap.
			t.Log("THEN: wakes stay within the daily cap")
			assert.Equal(t, maxWakesPer24h, car.wakeCount())
		})
	}
}
