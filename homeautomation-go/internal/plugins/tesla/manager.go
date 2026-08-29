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
	"fmt"
	"sync"
	"sync/atomic"
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

// energyCommander is the Powerwall control surface. Telemetry is deliberately
// not part of it: Powerwall data is meant to come from the gateways on the LAN,
// which is free and higher resolution, so nothing here polls Tesla's cloud for
// it. BackupReserve exists only to confirm a command took effect, and
// EnergySites only so an operator can look up a site id.
type energyCommander interface {
	SetBackupReserve(ctx context.Context, siteID int64, percent int) error
	BackupReserve(ctx context.Context, siteID int64) (int, error)
	EnergySites(ctx context.Context) ([]teslaapi.EnergySite, error)
}

// Manager polls the Fleet API on a timer.
type Manager struct {
	ctx    context.Context
	logger *zap.Logger

	cfg      teslaapi.Config
	enabled  bool
	readOnly bool

	auth       *teslaapi.Authenticator
	authorized authChecker
	client     fleetReader
	energy     energyCommander
	commander  *teslaapi.Commander

	shadowTracker *shadowstate.TeslaTracker

	mu       sync.Mutex
	stopPoll chan struct{}
	stopped  bool

	// polling admits one poll at a time. Reset() can fire while the ticker is
	// mid-poll, and every poll spends Fleet API requests, so a second one is
	// dropped rather than queued — it would only re-read what the first is
	// already fetching, at twice the price.
	polling atomic.Bool

	// energyCommands counts Powerwall commands. The vehicle Commander keeps its
	// own count, and the two are summed for shadow state.
	energyCommands atomic.Int64
}

// NewManager creates a Tesla manager. When the Tesla environment variables are
// absent the manager still starts, but it does nothing except record that it is
// unconfigured — that keeps a deployment without Tesla credentials quiet.
//
// readOnly mirrors the application-wide READ_ONLY setting. When it is set the
// manager still reads from Tesla, but refuses to send any command.
func NewManager(ctx context.Context, logger *zap.Logger, readOnly bool) *Manager {
	log := logger.Named("tesla")

	tracker := shadowstate.NewTeslaTracker()

	cfg, enabled, err := teslaapi.ConfigFromEnv()
	if err != nil {
		log.Error("Tesla configuration is invalid, plugin disabled", zap.Error(err))
		tracker.UpdateLastError(err.Error())
		return &Manager{ctx: ctx, logger: log, readOnly: readOnly, shadowTracker: tracker}
	}
	if !enabled {
		log.Info("Tesla Fleet API not configured (TESLA_CLIENT_ID unset), plugin idle")
		return &Manager{ctx: ctx, logger: log, readOnly: readOnly, shadowTracker: tracker}
	}

	store := teslaapi.NewTokenStore(cfg.TokenStorePath)
	auth := teslaapi.NewAuthenticator(cfg, store)

	client := teslaapi.NewClient(auth)

	m := &Manager{
		ctx:           ctx,
		logger:        log,
		cfg:           cfg,
		enabled:       true,
		readOnly:      readOnly,
		auth:          auth,
		authorized:    auth,
		client:        client,
		energy:        client,
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

// SetBackupReserve sets how much charge a Powerwall system holds back for an
// outage — the closest thing a Powerwall has to a target charge level.
//
// This is the one Powerwall operation that must go through Tesla's cloud. The
// local gateway API is read-only, so reserve changes have no local path.
// Unlike vehicle commands it needs no signing and no virtual key, but it does
// need a token carrying the energy_cmds scope.
//
// Like Commander, this has no HTTP surface. This system decides its own
// behavior, so the reserve is for an automation to set, not for a caller
// outside the process to set on request.
//
// It reads the value back afterwards to report what actually landed; Tesla
// clamps and rounds, so the applied value is not always the requested one.
func (m *Manager) SetBackupReserve(ctx context.Context, siteID int64, percent int) (applied int, err error) {
	if err := m.energyReady(); err != nil {
		return 0, err
	}
	if m.readOnly {
		m.logger.Info("Read-only mode: skipping Powerwall backup reserve change",
			zap.Int64("siteId", siteID), zap.Int("percent", percent))
		return 0, teslaapi.ErrReadOnly
	}

	if err := m.energy.SetBackupReserve(ctx, siteID, percent); err != nil {
		m.logger.Warn("Powerwall backup reserve change failed",
			zap.Int64("siteId", siteID), zap.Int("percent", percent), zap.Error(err))
		return 0, err
	}

	m.energyCommands.Add(1)

	applied, err = m.energy.BackupReserve(ctx, siteID)
	if err != nil {
		// The command went through; only the confirmation failed.
		m.logger.Warn("Powerwall backup reserve set, but reading it back failed",
			zap.Int64("siteId", siteID), zap.Int("percent", percent), zap.Error(err))
		m.recordUsage()
		return percent, nil
	}

	m.logger.Info("Powerwall backup reserve set",
		zap.Int64("siteId", siteID),
		zap.Int("requested", percent),
		zap.Int("applied", applied),
	)
	m.recordUsage()
	return applied, nil
}

// EnergySites lists the Powerwall systems on the account, so an operator can
// look up the site id that SetBackupReserve needs. It costs one billed Fleet
// API request, so it is meant for occasional use rather than polling.
func (m *Manager) EnergySites(ctx context.Context) ([]teslaapi.EnergySite, error) {
	if err := m.energyReady(); err != nil {
		return nil, err
	}

	sites, err := m.energy.EnergySites(ctx)
	m.recordUsage()
	if err != nil {
		m.logger.Warn("Listing Powerwall systems failed", zap.Error(err))
		return nil, err
	}
	return sites, nil
}

// energyReady reports why the Powerwall surface cannot be used, or nil when it
// can.
func (m *Manager) energyReady() error {
	if !m.enabled {
		return fmt.Errorf("tesla is not configured")
	}
	if m.energy == nil {
		return fmt.Errorf("tesla energy control is unavailable")
	}
	if !m.authorized.Authorized() {
		return fmt.Errorf("tesla account is not authorized yet")
	}
	return nil
}

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
	if !m.polling.CompareAndSwap(false, true) {
		m.logger.Debug("Skipping Tesla poll: one is already running")
		return
	}
	defer m.polling.Store(false)

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
	commands := m.energyCommands.Load()
	if m.commander != nil {
		commands += m.commander.CommandCount()
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
