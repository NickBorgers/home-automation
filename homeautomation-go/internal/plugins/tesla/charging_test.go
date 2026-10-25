package tesla

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"
	teslaapi "homeautomation/internal/tesla"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	homeLat = 45.0
	homeLon = -93.0
)

// fakeCharger records charging commands instead of sending them.
type fakeCharger struct {
	mu       sync.Mutex
	commands []string
	err      error
}

func (f *fakeCharger) record(cmd string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, cmd)
	return f.err
}

func (f *fakeCharger) Wake(context.Context, string) error        { return f.record("wake") }
func (f *fakeCharger) ChargeStart(context.Context, string) error { return f.record("start") }
func (f *fakeCharger) ChargeStop(context.Context, string) error  { return f.record("stop") }
func (f *fakeCharger) SetChargingAmps(_ context.Context, _ string, amps int32) error {
	return f.record(fmt.Sprintf("amps=%d", amps))
}

func (f *fakeCharger) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

type fakeLevels struct{ level string }

func (f *fakeLevels) GetString(string) (string, error) {
	if f.level == "" {
		return "", errors.New("not set")
	}
	return f.level, nil
}

// carAt describes one vehicle_data read for the charging tests.
type carAt struct {
	lat, lon      float64
	chargingState string
	battery       int
	limit         int
	amps          int
	maxAmps       int
}

func (c carAt) data() *teslaapi.VehicleData {
	return &teslaapi.VehicleData{
		ChargeState: teslaapi.ChargeState{
			BatteryLevel:            c.battery,
			ChargeLimitSOC:          c.limit,
			ChargingState:           c.chargingState,
			ChargeCurrentRequest:    c.amps,
			ChargeCurrentRequestMax: c.maxAmps,
		},
		DriveState: teslaapi.DriveState{Latitude: c.lat, Longitude: c.lon},
	}
}

// home returns a car parked at the house with the given charging picture.
func home(chargingState string, battery, amps int) carAt {
	return carAt{
		lat: homeLat + 0.0005, lon: homeLon, // about 55 m away: inside the radius
		chargingState: chargingState, battery: battery, limit: 80, amps: amps, maxAmps: 32,
	}
}

// newChargingManager builds a Manager with energy-level charging on and every
// outside dependency faked.
func newChargingManager(fleet fleetReader, level string) (*Manager, *fakeCharger, *fakeLevels, *[]time.Duration) {
	m := newPollingManager(fleet, true)
	charger := &fakeCharger{}
	levels := &fakeLevels{level: level}
	scheduled := &[]time.Duration{}
	m.homeLat, m.homeLon = homeLat, homeLon
	m.charger = charger
	m.levels = levels
	m.schedulePoll = func(d time.Duration) { *scheduled = append(*scheduled, d) }
	return m, charger, levels, scheduled
}

func onlineFleet(car carAt) *fakeFleet {
	return &fakeFleet{
		vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "online"}},
		data:     car.data(),
	}
}

func TestPlanCharging(t *testing.T) {
	tests := []struct {
		name     string
		level    string
		battery  int
		limit    int
		maxAmps  int
		action   chargeAction
		wantAmps int
	}{
		{"white charges at full speed", "white", 70, 80, 32, chargeNow, 32},
		{"green charges slowly", "green", 70, 80, 32, chargeNow, greenChargeAmps},
		{"green never exceeds the charger", "green", 70, 80, 6, chargeNow, 6},
		{"green with unknown max still slows down", "green", 70, 80, 0, chargeNow, greenChargeAmps},
		{"yellow holds", "yellow", 70, 80, 32, holdCharge, 0},
		{"red holds", "red", 70, 80, 32, holdCharge, 0},
		{"below the floor charges even at red", "red", 49, 80, 32, chargeNow, 32},
		{"at the floor follows the level", "red", 50, 80, 32, holdCharge, 0},
		{"unknown level leaves the car alone", "", 70, 80, 32, leaveAlone, 0},
		{"100% limit charges at full speed at red", "red", 70, 100, 32, chargeNow, 32},
		{"100% limit charges at full speed at green", "green", 70, 100, 32, chargeNow, 32},
		{"100% limit charges even with unknown level", "", 70, 100, 32, chargeNow, 32},
		{"99% limit follows the level", "red", 70, 99, 32, holdCharge, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := planCharging(tc.level, tc.battery, tc.limit, tc.maxAmps)
			assert.Equal(t, tc.action, plan.action)
			assert.Equal(t, tc.wantAmps, plan.amps)
			assert.NotEmpty(t, plan.reason)
		})
	}
}

func TestDistanceMeters(t *testing.T) {
	assert.InDelta(t, 0, distanceMeters(homeLat, homeLon, homeLat, homeLon), 0.001)
	// One thousandth of a degree of latitude is about 111 m everywhere.
	assert.InDelta(t, 111, distanceMeters(homeLat, homeLon, homeLat+0.001, homeLon), 1)
}

func TestEnergyChargingCommands(t *testing.T) {
	fullAtRed := home("Stopped", 70, 8)
	fullAtRed.limit = 100
	fullAtGreen := home("Charging", 70, 8)
	fullAtGreen.limit = 100
	away := home("Charging", 70, 32)
	away.lat = homeLat + 0.1
	noLocation := home("Charging", 70, 32)
	noLocation.lat, noLocation.lon = 0, 0
	unplugged := home("Disconnected", 70, 32)

	tests := []struct {
		name     string
		level    string
		car      carAt
		want     []string
		decision string
	}{
		{"white starts a stopped car at full speed", "white", home("Stopped", 70, 8), []string{"amps=32", "start"}, "white"},
		{"white leaves a car already at full speed", "white", home("Charging", 70, 32), nil, "white"},
		{"green slows a fast charge", "green", home("Charging", 70, 32), []string{"amps=8"}, "green"},
		{"yellow stops charging", "yellow", home("Charging", 70, 8), []string{"stop"}, "yellow"},
		{"red with charging already stopped sends nothing", "red", home("Stopped", 70, 8), nil, "red"},
		{"below the floor charges at red", "red", home("Stopped", 40, 8), []string{"amps=32", "start"}, "floor"},
		{"100% limit restarts a held charge at red", "red", fullAtRed, []string{"amps=32", "start"}, "100%"},
		{"100% limit lifts the green slowdown", "green", fullAtGreen, []string{"amps=32"}, "100%"},
		{"full car is left alone", "white", home("Complete", 80, 8), nil, "limit reached"},
		{"unknown level is left alone", "", home("Charging", 70, 32), nil, "unknown"},
		{"away from home is left alone", "red", away, nil, "away"},
		{"unplugged is left alone", "white", unplugged, nil, "not plugged"},
		{"no location is left alone", "red", noLocation, nil, "location unavailable"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, charger, _, _ := newChargingManager(onlineFleet(tc.car), tc.level)

			m.Poll()

			assert.Equal(t, tc.want, charger.sent())
			ec := m.GetShadowState().Outputs.EnergyCharging
			assert.True(t, ec.Enabled)
			assert.Contains(t, ec.Decision, tc.decision)
		})
	}
}

func TestEnergyChargingOffSendsNothing(t *testing.T) {
	fleet := onlineFleet(home("Charging", 70, 32))
	m := newPollingManager(fleet, true)
	m.homeLat, m.homeLon = homeLat, homeLon

	m.Poll()

	state := m.GetShadowState()
	assert.False(t, state.Outputs.EnergyCharging.Enabled)
	assert.True(t, state.Outputs.AtHome, "location is recorded even when charging control is off")
	assert.Equal(t, 32, state.Outputs.ChargeAmps)
	assert.Equal(t, 32, state.Outputs.ChargeAmpsMax)
}

func TestEnergyChargingReadOnlySendsNothing(t *testing.T) {
	m, charger, _, _ := newChargingManager(onlineFleet(home("Charging", 70, 32)), "red")
	m.readOnly = true

	m.Poll()

	assert.Empty(t, charger.sent())
	assert.Equal(t, "read-only: would stop charging", m.GetShadowState().Outputs.EnergyCharging.LastAction)
}

func TestEnergyChargingRecordsCommandFailure(t *testing.T) {
	m, charger, _, _ := newChargingManager(onlineFleet(home("Charging", 70, 32)), "red")
	charger.err = errors.New("virtual key not paired")

	m.Poll()

	state := m.GetShadowState()
	assert.Contains(t, state.Outputs.LastError, "virtual key not paired")
	assert.Contains(t, state.Outputs.EnergyCharging.LastAction, "stop charging failed")
}

func TestEnergyChargingFullLimitOverride(t *testing.T) {
	// GIVEN: red, the car charging at home. The plugin stops it.
	fleet := onlineFleet(home("Charging", 70, 8))
	m, charger, _, _ := newChargingManager(fleet, "red")
	m.Poll()
	require.Equal(t, []string{"stop"}, charger.sent())

	// WHEN: someone sets the charge limit to 100% in the Tesla app.
	full := home("Stopped", 70, 8)
	full.limit = 100
	fleet.data = full.data()
	m.Poll()

	// THEN: the energy level no longer matters. The car charges at full speed.
	assert.Equal(t, []string{"stop", "amps=32", "start"}, charger.sent())

	// WHEN: the limit goes back below 100%.
	fleet.data = home("Charging", 75, 32).data()
	m.Poll()

	// THEN: the energy level is in charge again.
	assert.Equal(t, []string{"stop", "amps=32", "start", "stop"}, charger.sent())
}

func TestEnergyChargingWakesSleepingCarToCharge(t *testing.T) {
	// GIVEN: red, the car plugged in at home with charging held.
	fleet := onlineFleet(home("Stopped", 60, 8))
	m, charger, levels, scheduled := newChargingManager(fleet, "red")
	m.Poll()
	require.Empty(t, charger.sent())

	// WHEN: the car falls asleep and the level rises to white.
	fleet.vehicles = []teslaapi.VehicleSummary{{VIN: "BBB", State: "asleep"}}
	levels.level = "white"
	m.Poll()

	// THEN: the car is woken, and a follow-up poll is scheduled to start it.
	assert.Equal(t, []string{"wake"}, charger.sent())
	assert.Equal(t, []time.Duration{wakeSettleDelay}, *scheduled)
	assert.Equal(t, 1, m.GetShadowState().Outputs.EnergyCharging.WakesLast24h)
}

func TestEnergyChargingWakesForFullLimitAtRed(t *testing.T) {
	fleet := &fakeFleet{vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "asleep"}}}
	m, charger, _, _ := newChargingManager(fleet, "red")
	m.charge = chargeMemory{seen: true, atHome: true, plugged: true, battery: 70, limit: 100}

	m.Poll()

	assert.Equal(t, []string{"wake"}, charger.sent())
}

func TestEnergyChargingWakeLimit(t *testing.T) {
	fleet := onlineFleet(home("Stopped", 60, 8))
	m, charger, _, _ := newChargingManager(fleet, "white")
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }

	// A car that keeps falling back asleep must not be woken on every poll.
	m.charge = chargeMemory{seen: true, atHome: true, plugged: true, battery: 60, limit: 80}
	fleet.vehicles = []teslaapi.VehicleSummary{{VIN: "BBB", State: "asleep"}}
	for i := 0; i < 5; i++ {
		m.Poll()
		now = now.Add(5 * time.Minute)
	}
	assert.Len(t, charger.sent(), maxWakesPer24h)
	assert.Contains(t, m.GetShadowState().Outputs.EnergyCharging.Decision, "wake limit")

	// A day later the budget is back.
	now = now.Add(24 * time.Hour)
	m.Poll()
	assert.Len(t, charger.sent(), maxWakesPer24h+1)
}

func TestEnergyChargingDoesNotWake(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		memory  chargeMemory
		vehicle string
	}{
		{"level says hold", "red", chargeMemory{seen: true, atHome: true, plugged: true, battery: 60, limit: 80}, "asleep"},
		{"car already full", "white", chargeMemory{seen: true, atHome: true, plugged: true, battery: 80, limit: 80}, "asleep"},
		{"car last seen away", "white", chargeMemory{seen: true, plugged: true, battery: 60, limit: 80}, "asleep"},
		{"car last seen unplugged", "white", chargeMemory{seen: true, atHome: true, battery: 60, limit: 80}, "asleep"},
		{"never seen online", "white", chargeMemory{}, "asleep"},
		{"offline car cannot be woken", "white",
			chargeMemory{seen: true, atHome: true, plugged: true, battery: 60, limit: 80}, "offline"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fleet := &fakeFleet{vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: tc.vehicle}}}
			m, charger, _, _ := newChargingManager(fleet, tc.level)
			m.charge = tc.memory

			m.Poll()

			assert.Empty(t, charger.sent())
		})
	}
}

// syncFleet is a fakeFleet that is safe to change while the poll loop runs.
type syncFleet struct {
	mu  sync.Mutex
	car carAt
}

func (f *syncFleet) Vehicles(context.Context) ([]teslaapi.VehicleSummary, error) {
	return []teslaapi.VehicleSummary{{VIN: "BBB", State: "online"}}, nil
}

func (f *syncFleet) VehicleData(context.Context, string) (*teslaapi.VehicleData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.car.data(), nil
}

func (f *syncFleet) RequestCount() int64 { return 0 }

func TestEnergyLevelChangeRepollsAndActs(t *testing.T) {
	stateManager := state.NewManager(ha.NewMockClient(), zap.NewNop(), true)
	require.NoError(t, stateManager.SetString("currentEnergyLevel", "white"))

	fleet := &syncFleet{car: home("Charging", 70, 32)}
	m := newPollingManager(fleet, true)
	m.cfg.PollInterval = time.Hour
	m.homeLat, m.homeLon = homeLat, homeLon
	charger := &fakeCharger{}
	m.charger = charger
	m.levels = stateManager
	m.subHelper = shadowstate.NewSubscriptionHelper(nil, stateManager, nil, m.shadowTracker, "tesla", testlogger.New())

	// GIVEN: white, charging at full speed. Start's first poll sends nothing.
	require.NoError(t, m.Start())
	defer m.Stop()
	require.Eventually(t, func() bool {
		return m.GetShadowState().Outputs.EnergyCharging.EnergyLevel == "white"
	}, 2*time.Second, 10*time.Millisecond)
	assert.Empty(t, charger.sent())
	assert.Equal(t, "white", m.GetShadowState().Inputs.Current["currentEnergyLevel"])

	// WHEN: the level drops to red, well before the next timed poll.
	require.NoError(t, stateManager.SetString("currentEnergyLevel", "red"))

	// THEN: the plugin re-polls at once and stops charging.
	require.Eventually(t, func() bool {
		return len(charger.sent()) == 1
	}, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"stop"}, charger.sent())
}
