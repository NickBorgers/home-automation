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
		{"41% charges at full speed at green", "green", 41, 80, 32, chargeNow, 32},
		{"41% charges at full speed at yellow", "yellow", 41, 80, 32, chargeNow, 32},
		{"41% charges at full speed at red", "red", 41, 80, 32, chargeNow, 32},
		{"green charges through the floor boundary", "green", 50, 80, 32, chargeNow, 32},
		{"white charges through the floor boundary", "white", 50, 80, 32, chargeNow, 32},
		{"red holds at the floor boundary", "red", 50, 80, 32, holdCharge, 0},
		{"unknown level leaves the car alone", "", 70, 80, 32, leaveAlone, 0},
		{"100% limit does not override red", "red", 70, 100, 32, holdCharge, 0},
		{"100% limit stays slow at green", "green", 70, 100, 32, chargeNow, greenChargeAmps},
		{"100% limit does not override unknown level", "", 70, 100, 32, leaveAlone, 0},
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

	plan := planCharging("green", 41, 80, 32)
	assert.Equal(t, "battery 41% is below the 50% floor: charge at full speed", plan.reason)
}

func TestDistanceMeters(t *testing.T) {
	assert.InDelta(t, 0, distanceMeters(homeLat, homeLon, homeLat, homeLon), 0.001)
	// One thousandth of a degree of latitude is about 111 m everywhere.
	assert.InDelta(t, 111, distanceMeters(homeLat, homeLon, homeLat+0.001, homeLon), 1)
}

func TestEnergyChargingCommands(t *testing.T) {
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
		{"white first sets a stopped car to full speed", "white", home("Stopped", 70, 8), []string{"amps=32"}, "white"},
		{"green starts a stopped car already set to slow speed", "green", home("Stopped", 70, 8), []string{"start"}, "green"},
		{"white leaves a car already at full speed", "white", home("Charging", 70, 32), nil, "white"},
		{"green slows a fast charge", "green", home("Charging", 70, 32), []string{"amps=8"}, "green"},
		{"yellow stops charging", "yellow", home("Charging", 70, 8), []string{"stop"}, "yellow"},
		{"red with charging already stopped sends nothing", "red", home("Stopped", 70, 8), nil, "red"},
		{"41% at green recovers at full speed", "green", home("Charging", 41, 8), []string{"amps=32"}, "below the 50% floor"},
		{"below the floor recovers at full speed at red", "red", home("Charging", 49, 8),
			[]string{"amps=32"}, "below the 50% floor"},
		{"at the floor holds at yellow", "yellow", home("Charging", 50, 8), []string{"stop"}, "yellow"},
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

func TestEnergyChargingReconcilesStopAgainstFreshTelemetry(t *testing.T) {
	// GIVEN: yellow and stale telemetry says a 50% car is still charging.
	fleet := onlineFleet(home("Charging", 50, 8))
	m, charger, _, scheduled := newChargingManager(fleet, "yellow")

	// WHEN: a stop is accepted, then the verification read still says Charging.
	m.Poll()
	assert.Equal(t, reconcilePendingStop,
		m.GetShadowState().Outputs.EnergyCharging.ReconciliationStatus)
	assert.Equal(t, []time.Duration{commandVerificationDelay}, *scheduled)
	m.Poll()

	// THEN: stale/contradictory telemetry is not confirmation; stop is retried.
	assert.Equal(t, []string{"stop", "stop"}, charger.sent())
	assert.Equal(t, reconcilePendingStop,
		m.GetShadowState().Outputs.EnergyCharging.ReconciliationStatus)

	// WHEN: a later verification read reports Stopped at the 50% boundary.
	fleet.data = home("Stopped", 50, 8).data()
	m.Poll()

	// THEN: only that fresh compliant state is considered verified.
	state := m.GetShadowState().Outputs.EnergyCharging
	assert.Equal(t, reconcileVerifiedHeld, state.ReconciliationStatus)
	assert.Contains(t, state.LastVerifiedVehicleState, "battery=50%")
	assert.Contains(t, state.LastVerifiedVehicleState, "chargingState=Stopped")
}

func TestEnergyChargingFloorThenYellowStopsAt50(t *testing.T) {
	// GIVEN: the below-50% floor allows a 49% car to charge at full speed.
	fleet := onlineFleet(home("Charging", 49, 32))
	m, charger, levels, _ := newChargingManager(fleet, "green")
	m.Poll()
	require.Empty(t, charger.sent())
	assert.Equal(t, reconcileVerifiedAtReserve,
		m.GetShadowState().Outputs.EnergyCharging.ReconciliationStatus)

	// WHEN: the house turns yellow as the car reaches 50%.
	levels.level = "yellow"
	fleet.data = home("Charging", 50, greenChargeAmps).data()
	m.Poll()

	// THEN: charging is stopped and remains pending until a read-back confirms it.
	assert.Equal(t, []string{"stop"}, charger.sent())
	assert.Equal(t, reconcilePendingStop,
		m.GetShadowState().Outputs.EnergyCharging.ReconciliationStatus)
}

func TestScenario_EnergyCharging_GreenContinuesPast50PercentFloor(t *testing.T) {
	// GIVEN: green energy and a car below the 50% minimum reserve.
	t.Log("GIVEN: green energy and the car charging at 49%")
	fleet := onlineFleet(home("Charging", 49, 32))
	m, charger, _, _ := newChargingManager(fleet, "green")
	m.Poll()
	require.Empty(t, charger.sent())
	assert.Equal(t, reconcileVerifiedAtReserve,
		m.GetShadowState().Outputs.EnergyCharging.ReconciliationStatus)

	// WHEN: the car reaches exactly 50%.
	t.Log("WHEN: the car reaches the 50% minimum reserve")
	fleet.data = home("Charging", 50, 32).data()
	m.Poll()

	// THEN: it must keep charging at full speed so the reserve is not an effective ceiling.
	t.Log("THEN: charging continues at full speed beyond the reserve boundary")
	state := m.GetShadowState().Outputs.EnergyCharging
	assert.Empty(t, charger.sent())
	assert.Contains(t, state.Decision, "50% minimum reserve")
	assert.Contains(t, state.ReconciliationStatus, "minimum reserve")

	// WHEN: fresh telemetry proves the car has progressed beyond the floor.
	t.Log("WHEN: the car progresses to 51%")
	fleet.data = home("Charging", 51, 32).data()
	m.Poll()

	// THEN: green may slow charging toward the configured Tesla limit.
	t.Log("THEN: green charging continues toward the configured limit at 8A")
	state = m.GetShadowState().Outputs.EnergyCharging
	assert.Equal(t, []string{"amps=8"}, charger.sent())
	assert.Contains(t, state.Decision, "toward the 80% charge limit")
	assert.Equal(t, reconcilePendingCurrent, state.ReconciliationStatus)

	// WHEN: a reconciliation read confirms the requested green charging speed.
	t.Log("WHEN: fresh telemetry confirms charging at 8A")
	fleet.data = home("Charging", 51, greenChargeAmps).data()
	m.Poll()

	// THEN: shadow state says charging is progressing toward Tesla's configured limit.
	t.Log("THEN: reconciliation distinguishes charging toward the configured limit")
	state = m.GetShadowState().Outputs.EnergyCharging
	assert.Equal(t, []string{"amps=8"}, charger.sent())
	assert.Equal(t, reconcileVerifiedTowardLimit, state.ReconciliationStatus)
}

func TestEnergyChargingPollingInterval(t *testing.T) {
	m, _, _, _ := newChargingManager(onlineFleet(home("Stopped", 70, 8)), "yellow")
	m.cfg.PollInterval = time.Hour

	assert.Equal(t, teslaapi.DefaultPollInterval, m.nextPollInterval(),
		"startup reconciliation must not wait for a long configured interval")

	m.charge = chargeMemory{seen: true, atHome: true, plugged: false}
	assert.Equal(t, teslaapi.DefaultPollInterval, m.nextPollInterval(),
		"an unplugged car at home must be checked often enough to detect a plug-in")

	m.charge = chargeMemory{seen: true, atHome: false, plugged: false}
	assert.Equal(t, time.Hour, m.nextPollInterval(), "an away car uses the configured interval")

	m.charge.record.ReconciliationStatus = reconcileVerifiedTowardLimit
	assert.Equal(t, chargingPollInterval, m.nextPollInterval(), "active charging uses the shorter interval")

	m.charge.record.ReconciliationStatus = reconcilePendingStop
	assert.Equal(t, chargingPollInterval, m.nextPollInterval(), "reconciliation stays on the shorter interval")

	m.charge.record.ReconciliationStatus = reconcileVerifiedHeld
	assert.Equal(t, time.Hour, m.nextPollInterval(), "verified stopped state returns to idle polling")

	m.cfg.PollInterval = 30 * time.Second
	m.charge.record.ReconciliationStatus = reconcileVerifiedTowardLimit
	assert.Equal(t, 30*time.Second, m.nextPollInterval(), "charging never makes an already shorter interval slower")
}

func TestEnergyChargingDiscoversPlugInWithoutEnergyLevelChange(t *testing.T) {
	// GIVEN: green was already active when startup found the car unplugged at home.
	fleet := onlineFleet(home("Disconnected", 36, 0))
	m, charger, _, _ := newChargingManager(fleet, "green")
	m.cfg.PollInterval = time.Hour
	m.Poll()
	require.Empty(t, charger.sent())
	require.Equal(t, teslaapi.DefaultPollInterval, m.nextPollInterval())

	// WHEN: the car is plugged in before the reconciliation poll, with no energy-level transition.
	fleet.data = home("Stopped", 36, 32).data()
	m.Poll()

	// THEN: the existing below-50% plan starts charging at full available current.
	assert.Equal(t, []string{"start"}, charger.sent())
	assert.Contains(t, m.GetShadowState().Outputs.EnergyCharging.Decision, "below the 50% floor")
}

func TestEnergyChargingReconcilesAfterTransientOfflineAtStartup(t *testing.T) {
	// GIVEN: the first vehicle-list response after restart is transiently offline.
	fleet := &fakeFleet{vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "offline"}}}
	m, charger, _, _ := newChargingManager(fleet, "green")
	m.cfg.PollInterval = time.Hour
	m.Poll()
	require.Empty(t, charger.sent())
	require.Equal(t, teslaapi.DefaultPollInterval, m.nextPollInterval(),
		"offline startup must retain a bounded reconciliation interval")

	// WHEN: a later poll can read the already plugged-in car below 50%.
	fleet.vehicles = []teslaapi.VehicleSummary{{VIN: "BBB", State: "online"}}
	fleet.data = home("Stopped", 36, 32).data()
	m.Poll()

	// THEN: charging starts without an energy-level transition or unsafe blind wake.
	assert.Equal(t, []string{"start"}, charger.sent())
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

func TestEnergyChargingWakesSleepingCarBelowFloorAtRed(t *testing.T) {
	// GIVEN: red, a sleeping car plugged in at home with 41% battery (below the 50% floor).
	fleet := &fakeFleet{vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "asleep"}}}
	m, charger, _, scheduled := newChargingManager(fleet, "red")
	m.charge = chargeMemory{seen: true, atHome: true, plugged: true, battery: 41, limit: 80}

	// WHEN: a poll runs.
	m.Poll()

	// THEN: the car is woken to charge at full speed despite the red energy level.
	assert.Equal(t, []string{"wake"}, charger.sent())
	assert.Equal(t, []time.Duration{wakeSettleDelay}, *scheduled)
	assert.Contains(t, m.GetShadowState().Outputs.EnergyCharging.Decision, "below the 50% floor")
	assert.Equal(t, 1, m.GetShadowState().Outputs.EnergyCharging.WakesLast24h)
}

func TestEnergyChargingDoesNotWakeForFullLimitAtRed(t *testing.T) {
	fleet := &fakeFleet{vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "asleep"}}}
	m, charger, _, _ := newChargingManager(fleet, "red")
	m.charge = chargeMemory{seen: true, atHome: true, plugged: true, battery: 70, limit: 100}

	m.Poll()

	assert.Empty(t, charger.sent())
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

// withLimit returns the car with a different charge limit.
func (c carAt) withLimit(limit int) carAt {
	c.limit = limit
	return c
}

func TestEnergyChargingManualOverride_LastNight(t *testing.T) {
	// GIVEN: yellow, and the car comes home at 32% and starts charging on its own.
	fleet := onlineFleet(home("Charging", 32, 32))
	m, charger, _, _ := newChargingManager(fleet, "yellow")

	// WHEN: the plugin polls below the floor.
	m.Poll()

	// THEN: the floor keeps it charging at full speed.
	require.Empty(t, charger.sent(), "below the floor the car keeps charging")

	// WHEN: the car reaches 50%.
	fleet.data = home("Charging", 50, 32).data()
	m.Poll()
	fleet.data = home("Stopped", 50, 32).data()
	m.Poll()

	// THEN: yellow stops it once, and the stop is verified.
	assert.Equal(t, []string{"stop"}, charger.sent())

	// WHEN: the person raises the limit to 100% and starts charging in the app.
	fleet.data = home("Charging", 51, 32).withLimit(100).data()
	m.Poll()
	fleet.data = home("Charging", 60, 32).withLimit(100).data()
	m.Poll()

	// THEN: the plugin leaves charging alone.
	assert.Equal(t, []string{"stop"}, charger.sent(), "no stop after a manual override")
	state := m.GetShadowState().Outputs.EnergyCharging
	assert.True(t, state.ManualOverride)
	assert.Contains(t, state.Decision, "manual override until unplugged")
	assert.Equal(t, "leave unchanged", state.PlannedAction)
}

func TestEnergyChargingManualOverride(t *testing.T) {
	tests := []struct {
		name       string
		polls      []carAt
		wantSent   []string
		wantReason string
	}{
		{
			name:       "raising the charge limit above 80% overrides a hold",
			polls:      []carAt{home("Stopped", 60, 32), home("Charging", 60, 32).withLimit(100)},
			wantSent:   nil,
			wantReason: "charge limit 100% is above the usual 80%",
		},
		{
			name:       "starting charging after a verified stop overrides a hold",
			polls:      []carAt{home("Charging", 60, 32), home("Stopped", 60, 32), home("Charging", 60, 32)},
			wantSent:   []string{"stop"},
			wantReason: "charging started after the plugin stopped it",
		},
		{
			name: "stale telemetry after a stop is not an override",
			polls: []carAt{
				home("Charging", 60, 32), home("Charging", 60, 32),
			},
			wantSent: []string{"stop", "stop"},
		},
		{
			name:       "a limit above 80% on the first poll overrides, so it survives a restart",
			polls:      []carAt{home("Charging", 60, 32).withLimit(81)},
			wantSent:   nil,
			wantReason: "charge limit 81% is above the usual 80%",
		},
		{
			name:     "an 80% limit is not an override",
			polls:    []carAt{home("Charging", 60, 32)},
			wantSent: []string{"stop"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fleet := onlineFleet(tt.polls[0])
			m, charger, _, _ := newChargingManager(fleet, "yellow")
			for _, car := range tt.polls {
				fleet.data = car.data()
				m.Poll()
			}
			if tt.wantSent == nil {
				assert.Empty(t, charger.sent())
			} else {
				assert.Equal(t, tt.wantSent, charger.sent())
			}
			state := m.GetShadowState().Outputs.EnergyCharging
			assert.Equal(t, tt.wantReason != "", state.ManualOverride)
			if tt.wantReason != "" {
				assert.Equal(t, "manual override until unplugged: "+tt.wantReason, state.Decision)
			}
		})
	}
}

func TestEnergyChargingManualOverrideEndsWhenUnplugged(t *testing.T) {
	// GIVEN: a manual override from a raised limit.
	fleet := onlineFleet(home("Stopped", 60, 32))
	m, charger, _, _ := newChargingManager(fleet, "yellow")
	m.Poll()
	fleet.data = home("Charging", 60, 32).withLimit(100).data()
	m.Poll()
	require.True(t, m.GetShadowState().Outputs.EnergyCharging.ManualOverride)

	// WHEN: the car is unplugged, then plugged in again later with the limit
	// back at 80%.
	fleet.data = home("Disconnected", 65, 0).withLimit(100).data()
	m.Poll()
	assert.False(t, m.GetShadowState().Outputs.EnergyCharging.ManualOverride)
	fleet.data = home("Charging", 65, 32).data()
	m.Poll()

	// THEN: the energy level is back in control.
	assert.Equal(t, []string{"stop"}, charger.sent())
}

func TestEnergyChargingManualOverrideRestartsWhenLimitStillHigh(t *testing.T) {
	// GIVEN: a manual override from a raised limit, then the car is unplugged.
	fleet := onlineFleet(home("Stopped", 60, 32))
	m, charger, _, _ := newChargingManager(fleet, "yellow")
	m.Poll()
	fleet.data = home("Charging", 60, 32).withLimit(100).data()
	m.Poll()
	require.True(t, m.GetShadowState().Outputs.EnergyCharging.ManualOverride)

	fleet.data = home("Disconnected", 65, 0).withLimit(100).data()
	m.Poll()
	assert.False(t, m.GetShadowState().Outputs.EnergyCharging.ManualOverride)

	// WHEN: the car is plugged in again with the limit still above 80%.
	fleet.data = home("Charging", 65, 32).withLimit(100).data()
	m.Poll()

	// THEN: the override starts again immediately — no stop command is sent.
	assert.Empty(t, charger.sent())
	assert.True(t, m.GetShadowState().Outputs.EnergyCharging.ManualOverride)
}

func TestEnergyChargingManualOverrideDoesNotWake(t *testing.T) {
	// GIVEN: a manual override, then the car falls asleep.
	fleet := onlineFleet(home("Stopped", 60, 32))
	m, charger, levels, _ := newChargingManager(fleet, "yellow")
	m.Poll()
	fleet.data = home("Stopped", 60, 32).withLimit(100).data()
	m.Poll()
	require.True(t, m.GetShadowState().Outputs.EnergyCharging.ManualOverride)
	fleet.vehicles = []teslaapi.VehicleSummary{{VIN: "BBB", State: "asleep"}}

	// WHEN: the house turns white.
	levels.level = "white"
	m.Poll()

	// THEN: the plugin does not wake the car.
	assert.Empty(t, charger.sent())
}
