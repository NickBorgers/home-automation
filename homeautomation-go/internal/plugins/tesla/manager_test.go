package tesla

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"homeautomation/internal/shadowstate"
	teslaapi "homeautomation/internal/tesla"
	"homeautomation/internal/testlogger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectVehicle(t *testing.T) {
	vehicles := []teslaapi.VehicleSummary{
		{VIN: "AAA", State: "asleep"},
		{VIN: "BBB", State: "online"},
	}

	tests := []struct {
		name      string
		vehicles  []teslaapi.VehicleSummary
		vin       string
		wantVIN   string
		wantFound bool
	}{
		{"matches configured VIN", vehicles, "BBB", "BBB", true},
		{"no VIN configured takes the first", vehicles, "", "AAA", true},
		{"unknown VIN", vehicles, "CCC", "", false},
		{"empty account", nil, "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, found := selectVehicle(tc.vehicles, tc.vin)
			assert.Equal(t, tc.wantFound, found)
			assert.Equal(t, tc.wantVIN, got.VIN)
		})
	}
}

// clearTeslaEnv removes every Tesla variable so the manager sees an
// unconfigured deployment.
func clearTeslaEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TESLA_CLIENT_ID", "TESLA_CLIENT_SECRET", "TESLA_DOMAIN", "TESLA_REDIRECT_URI",
		"TESLA_PRIVATE_KEY", "TESLA_TOKEN_STORE", "TESLA_VIN", "TESLA_POLL_MINUTES",
		"TESLA_AUTH_BASE", "TESLA_AUDIENCE",
	} {
		t.Setenv(key, "")
	}
}

func TestManagerIdleWithoutCredentials(t *testing.T) {
	clearTeslaEnv(t)

	m := NewManager(context.Background(), testlogger.New())

	assert.False(t, m.Enabled())
	assert.Nil(t, m.Authenticator())
	assert.Nil(t, m.Commander())
	require.NoError(t, m.Start())
	require.NoError(t, m.Reset())
	m.Stop()

	state := m.GetShadowState()
	require.NotNil(t, state)
	assert.False(t, state.Outputs.Configured)
	assert.False(t, state.Outputs.Authorized)
	assert.Equal(t, "unknown", state.Outputs.VehicleState)
}

func TestManagerRecordsInvalidConfiguration(t *testing.T) {
	clearTeslaEnv(t)
	// A client ID with nothing else is the classic half-finished setup.
	t.Setenv("TESLA_CLIENT_ID", "client-id")

	m := NewManager(context.Background(), testlogger.New())

	assert.False(t, m.Enabled())
	assert.Contains(t, m.GetShadowState().Outputs.LastError, "TESLA_CLIENT_SECRET")
}

func TestManagerConfiguredButUnauthorized(t *testing.T) {
	clearTeslaEnv(t)
	t.Setenv("TESLA_CLIENT_ID", "client-id")
	t.Setenv("TESLA_CLIENT_SECRET", "client-secret")
	t.Setenv("TESLA_DOMAIN", "tesla.example.ts.net")
	t.Setenv("TESLA_REDIRECT_URI", "https://home-automation.example.ts.net/api/tesla/callback")
	t.Setenv("TESLA_TOKEN_STORE", t.TempDir()+"/tokens.json")
	t.Setenv("TESLA_VIN", "5YJ3E1EA1JF000000")

	m := NewManager(context.Background(), testlogger.New())

	require.True(t, m.Enabled())
	require.NotNil(t, m.Authenticator())
	require.NotNil(t, m.Commander())

	// Polling without tokens must be a no-op rather than an error, and must not
	// spend a billable request.
	m.Poll()

	state := m.GetShadowState()
	assert.True(t, state.Outputs.Configured)
	assert.False(t, state.Outputs.Authorized)
	assert.Equal(t, "5YJ3E1EA1JF000000", state.Outputs.VIN)
	assert.Zero(t, state.Outputs.RequestCount)
}

func TestManagerStopIsIdempotent(t *testing.T) {
	clearTeslaEnv(t)

	m := NewManager(context.Background(), testlogger.New())
	m.Stop()
	m.Stop()
}

// fakeFleet stands in for the Fleet API client.
type fakeFleet struct {
	vehicles     []teslaapi.VehicleSummary
	vehiclesErr  error
	data         *teslaapi.VehicleData
	dataErr      error
	dataCalls    int
	requestCount int64
}

func (f *fakeFleet) Vehicles(context.Context) ([]teslaapi.VehicleSummary, error) {
	f.requestCount++
	return f.vehicles, f.vehiclesErr
}

func (f *fakeFleet) VehicleData(_ context.Context, _ string) (*teslaapi.VehicleData, error) {
	f.dataCalls++
	f.requestCount++
	return f.data, f.dataErr
}

func (f *fakeFleet) RequestCount() int64 { return f.requestCount }

type fakeAuth struct{ authorized bool }

func (f fakeAuth) Authorized() bool { return f.authorized }

// newPollingManager builds a Manager wired to fakes, bypassing the environment.
func newPollingManager(client fleetReader, authorized bool) *Manager {
	return &Manager{
		ctx:           context.Background(),
		logger:        testlogger.New(),
		cfg:           teslaapi.Config{VIN: "BBB"},
		enabled:       true,
		authorized:    fakeAuth{authorized: authorized},
		client:        client,
		shadowTracker: shadowstate.NewTeslaTracker(),
	}
}

func TestPollSkipsDataReadWhileAsleep(t *testing.T) {
	fleet := &fakeFleet{vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "asleep"}}}
	m := newPollingManager(fleet, true)

	m.Poll()

	assert.Zero(t, fleet.dataCalls, "a sleeping car must not be polled for data, and must never be woken on a timer")
	state := m.GetShadowState()
	assert.Equal(t, "asleep", state.Outputs.VehicleState)
	assert.Equal(t, int64(1), state.Outputs.RequestCount)
}

func TestPollReadsDataWhileOnline(t *testing.T) {
	fleet := &fakeFleet{
		vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "online"}},
		data: &teslaapi.VehicleData{
			ChargeState: teslaapi.ChargeState{
				BatteryLevel:        62,
				ChargeLimitSOC:      80,
				ChargingState:       "Charging",
				MinutesToFullCharge: 95,
			},
			ClimateState: teslaapi.ClimateState{InsideTemp: 21.5},
			VehicleState: teslaapi.VehicleState{Locked: true},
		},
	}
	m := newPollingManager(fleet, true)

	m.Poll()

	state := m.GetShadowState()
	assert.Equal(t, "online", state.Outputs.VehicleState)
	assert.Equal(t, 62, state.Outputs.BatteryLevel)
	assert.Equal(t, 80, state.Outputs.ChargeLimitSOC)
	assert.Equal(t, 95, state.Outputs.MinutesToFullCharge)
	assert.Equal(t, "Charging", state.Outputs.ChargingState)
	assert.True(t, state.Outputs.PluggedIn)
	assert.InDelta(t, 21.5, state.Outputs.InsideTemp, 0.001)
	assert.True(t, state.Outputs.Locked)
	assert.Empty(t, state.Outputs.LastError)
	assert.Equal(t, int64(2), state.Outputs.RequestCount)
}

func TestPollRecordsVehicleListFailure(t *testing.T) {
	fleet := &fakeFleet{vehiclesErr: errors.New("fleet api down")}
	m := newPollingManager(fleet, true)

	m.Poll()

	assert.Contains(t, m.GetShadowState().Outputs.LastError, "fleet api down")
	assert.Zero(t, fleet.dataCalls)
}

func TestPollRecordsMissingVIN(t *testing.T) {
	fleet := &fakeFleet{vehicles: []teslaapi.VehicleSummary{{VIN: "AAA", State: "online"}}}
	m := newPollingManager(fleet, true)

	m.Poll()

	assert.Contains(t, m.GetShadowState().Outputs.LastError, "VIN not found")
	assert.Zero(t, fleet.dataCalls)
}

func TestPollRecordsDataFailure(t *testing.T) {
	fleet := &fakeFleet{
		vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "online"}},
		dataErr:  errors.New("vehicle unavailable"),
	}
	m := newPollingManager(fleet, true)

	m.Poll()

	assert.Contains(t, m.GetShadowState().Outputs.LastError, "vehicle unavailable")
}

func TestPollSkippedWhenUnauthorized(t *testing.T) {
	fleet := &fakeFleet{vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "online"}}}
	m := newPollingManager(fleet, false)

	m.Poll()

	assert.Zero(t, fleet.requestCount, "an unauthorized plugin must not spend billable requests")
	assert.False(t, m.GetShadowState().Outputs.Authorized)
}

func TestStartPollsImmediatelyThenStops(t *testing.T) {
	fleet := &fakeFleet{vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "asleep"}}}
	m := newPollingManager(fleet, true)
	m.cfg.PollInterval = time.Hour

	require.NoError(t, m.Start())
	require.Eventually(t, func() bool {
		return m.GetShadowState().Outputs.VehicleState == "asleep"
	}, 2*time.Second, 10*time.Millisecond, "Start must poll once without waiting for the first tick")

	m.Stop()
	require.NoError(t, m.Reset())
}

// blockingFleet holds the vehicle list open until released, so a second Poll()
// arrives while the first is still in flight.
type blockingFleet struct {
	entered  chan struct{}
	release  chan struct{}
	calls    atomic.Int64
	vehicles []teslaapi.VehicleSummary
}

func (f *blockingFleet) Vehicles(context.Context) ([]teslaapi.VehicleSummary, error) {
	f.calls.Add(1)
	select {
	case f.entered <- struct{}{}:
	default:
	}
	<-f.release
	return f.vehicles, nil
}

func (f *blockingFleet) VehicleData(_ context.Context, _ string) (*teslaapi.VehicleData, error) {
	return &teslaapi.VehicleData{}, nil
}

func (f *blockingFleet) RequestCount() int64 { return f.calls.Load() }

// Reset() can land while the ticker is mid-poll. Two polls at once would read
// the same state twice and pay Tesla twice for it, so the second is dropped.
func TestPollAdmitsOneAtATime(t *testing.T) {
	fleet := &blockingFleet{
		entered:  make(chan struct{}, 1),
		release:  make(chan struct{}),
		vehicles: []teslaapi.VehicleSummary{{VIN: "BBB", State: "asleep"}},
	}
	m := newPollingManager(fleet, true)

	first := make(chan struct{})
	go func() {
		defer close(first)
		m.Poll()
	}()

	// Wait until the first poll is inside the Fleet API call.
	select {
	case <-fleet.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first poll never reached the Fleet API")
	}

	// This one arrives mid-flight and must be dropped, not queued.
	m.Poll()

	close(fleet.release)
	<-first

	assert.Equal(t, int64(1), fleet.RequestCount(), "a poll arriving mid-poll must not spend a second request")

	// Once the first finishes, polling is allowed again.
	fleet.release = make(chan struct{})
	close(fleet.release)
	m.Poll()
	assert.Equal(t, int64(2), fleet.RequestCount(), "the guard must clear after a poll finishes")
}
