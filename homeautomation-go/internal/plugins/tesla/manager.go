// Package tesla polls the Tesla Fleet API for vehicle state and exposes signed
// vehicle commands to the rest of the system.
//
// The plugin is deliberately read-only on its own: it polls, records what it
// sees in shadow state, and never issues a command by itself. Commands are
// available through the Commander accessor for callers that act on a person's
// request. Tesla bills per request against a small monthly credit, so every
// call this plugin makes on a timer is a recurring cost.
package tesla

import (
	"context"
	"sync"
	"time"

	"homeautomation/internal/shadowstate"
	teslaapi "homeautomation/internal/tesla"

	"go.uber.org/zap"
)

// fleetReader is the part of the Fleet API client this plugin uses. It exists
// so tests can drive the poll loop without talking to Tesla.
type fleetReader interface {
	Vehicles(ctx context.Context) ([]teslaapi.VehicleSummary, error)
	VehicleData(ctx context.Context, vin string) (*teslaapi.VehicleData, error)
	RequestCount() int64
}

// authChecker reports whether the account has completed authorization.
type authChecker interface {
	Authorized() bool
}

// Manager polls the Fleet API on a timer.
type Manager struct {
	ctx    context.Context
	logger *zap.Logger

	cfg     teslaapi.Config
	enabled bool

	auth       *teslaapi.Authenticator
	authorized authChecker
	client     fleetReader
	commander  *teslaapi.Commander

	shadowTracker *shadowstate.TeslaTracker

	// pollNow lets tests drive a single poll without waiting for the ticker.
	mu       sync.Mutex
	stopPoll chan struct{}
	stopped  bool
}

// NewManager creates a Tesla manager. When the Tesla environment variables are
// absent the manager still starts, but it does nothing except record that it is
// unconfigured — that keeps a deployment without Tesla credentials quiet.
func NewManager(ctx context.Context, logger *zap.Logger) *Manager {
	log := logger.Named("tesla")

	tracker := shadowstate.NewTeslaTracker()

	cfg, enabled, err := teslaapi.ConfigFromEnv()
	if err != nil {
		log.Error("Tesla configuration is invalid, plugin disabled", zap.Error(err))
		tracker.UpdateLastError(err.Error())
		return &Manager{ctx: ctx, logger: log, shadowTracker: tracker}
	}
	if !enabled {
		log.Info("Tesla Fleet API not configured (TESLA_CLIENT_ID unset), plugin idle")
		return &Manager{ctx: ctx, logger: log, shadowTracker: tracker}
	}

	store := teslaapi.NewTokenStore(cfg.TokenStorePath)
	auth := teslaapi.NewAuthenticator(cfg, store)

	m := &Manager{
		ctx:           ctx,
		logger:        log,
		cfg:           cfg,
		enabled:       true,
		auth:          auth,
		authorized:    auth,
		client:        teslaapi.NewClient(auth),
		commander:     teslaapi.NewCommander(auth, cfg.PrivateKeyPath),
		shadowTracker: tracker,
	}
	tracker.UpdateAvailability(true, auth.Authorized(), cfg.VIN)
	return m
}

// Authenticator exposes the OAuth handler so the HTTP API can run the login and
// callback endpoints. It is nil when Tesla is not configured.
func (m *Manager) Authenticator() *teslaapi.Authenticator { return m.auth }

// Commander exposes signed vehicle commands. It is nil when Tesla is not
// configured.
func (m *Manager) Commander() *teslaapi.Commander { return m.commander }

// VIN returns the configured vehicle identifier.
func (m *Manager) VIN() string { return m.cfg.VIN }

// Enabled reports whether Tesla credentials were found.
func (m *Manager) Enabled() bool { return m.enabled }

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.TeslaShadowState {
	return m.shadowTracker.GetState()
}

// Start begins polling.
func (m *Manager) Start() error {
	if !m.enabled {
		return nil
	}

	m.logger.Info("Starting Tesla manager",
		zap.String("vin", m.cfg.VIN),
		zap.Duration("pollInterval", m.cfg.PollInterval),
		zap.Bool("authorized", m.authorized.Authorized()),
	)

	m.mu.Lock()
	m.stopPoll = make(chan struct{})
	stop := m.stopPoll
	m.mu.Unlock()

	go m.pollLoop(stop)
	return nil
}

// Stop halts polling.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped || m.stopPoll == nil {
		return
	}
	close(m.stopPoll)
	m.stopped = true
	m.logger.Info("Tesla manager stopped")
}

// Reset re-polls immediately. There are no rate limiters or timers to clear.
func (m *Manager) Reset() error {
	if !m.enabled {
		return nil
	}
	m.Poll()
	return nil
}

func (m *Manager) pollLoop(stop <-chan struct{}) {
	// Poll once at startup so the shadow state is populated before the first
	// tick, then settle into the configured interval.
	m.Poll()

	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.Poll()
		}
	}
}

// Poll reads the current vehicle state. It costs one Fleet API request, plus a
// second one only when the car is awake.
func (m *Manager) Poll() {
	if !m.enabled {
		return
	}
	if !m.authorized.Authorized() {
		m.shadowTracker.UpdateAvailability(true, false, m.cfg.VIN)
		m.logger.Debug("Skipping Tesla poll: account not authorized yet")
		return
	}
	m.shadowTracker.UpdateAvailability(true, true, m.cfg.VIN)

	ctx, cancel := context.WithTimeout(m.ctx, 60*time.Second)
	defer cancel()

	vehicles, err := m.client.Vehicles(ctx)
	m.recordUsage()
	if err != nil {
		m.logger.Warn("Tesla vehicle list failed", zap.Error(err))
		m.shadowTracker.UpdateLastError(err.Error())
		return
	}

	vehicle, found := selectVehicle(vehicles, m.cfg.VIN)
	if !found {
		m.logger.Warn("Configured VIN not found on the Tesla account", zap.String("vin", m.cfg.VIN))
		m.shadowTracker.UpdateLastError("configured VIN not found on the account")
		return
	}

	m.shadowTracker.UpdateVehicleState(vehicle.State, time.Now())

	if !vehicle.Online() {
		// A sleeping car answers vehicle_data with an error and waking it costs
		// extra, so stop here and keep the last known values.
		m.shadowTracker.UpdateLastError("")
		m.logger.Debug("Tesla vehicle is not online, skipping data read", zap.String("state", vehicle.State))
		return
	}

	data, err := m.client.VehicleData(ctx, vehicle.VIN)
	m.recordUsage()
	if err != nil {
		m.logger.Warn("Tesla vehicle data failed", zap.Error(err))
		m.shadowTracker.UpdateLastError(err.Error())
		return
	}

	m.shadowTracker.UpdateChargeState(
		data.ChargeState.BatteryLevel,
		data.ChargeState.ChargeLimitSOC,
		data.ChargeState.MinutesToFullCharge,
		data.ChargeState.ChargingState,
		data.ChargeState.Plugged(),
	)
	m.shadowTracker.UpdateCabinState(data.ClimateState.InsideTemp, data.VehicleState.Locked)
	m.shadowTracker.UpdateLastError("")

	m.logger.Info("Tesla state updated",
		zap.String("vin", vehicle.VIN),
		zap.Int("batteryLevel", data.ChargeState.BatteryLevel),
		zap.String("chargingState", data.ChargeState.ChargingState),
		zap.Int64("fleetApiRequests", m.client.RequestCount()),
	)
}

func (m *Manager) recordUsage() {
	var commands int64
	if m.commander != nil {
		commands = m.commander.CommandCount()
	}
	m.shadowTracker.UpdateUsage(m.client.RequestCount(), commands)
}

// selectVehicle picks the configured VIN, or the only vehicle on the account
// when no VIN is configured.
func selectVehicle(vehicles []teslaapi.VehicleSummary, vin string) (teslaapi.VehicleSummary, bool) {
	if len(vehicles) == 0 {
		return teslaapi.VehicleSummary{}, false
	}
	if vin == "" {
		return vehicles[0], true
	}
	for _, vehicle := range vehicles {
		if vehicle.VIN == vin {
			return vehicle, true
		}
	}
	return teslaapi.VehicleSummary{}, false
}
