package loadshedding

import (
	"context"
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

const (
	// Rate limiting: minimum time between actions
	minActionInterval = 1 * time.Hour

	// Energy states
	energyStateRed    = "red"
	energyStateBlack  = "black"
	energyStateYellow = "yellow"
	energyStateGreen  = "green"
	energyStateWhite  = "white"

	// Thermostat entities
	thermostatHoldHouse = "switch.most_of_house_thermostat_hold"
	thermostatHoldSuite = "switch.primary_suite_thermostat_hold"
	climateHouse        = "climate.most_of_house_thermostat"
	climateSuite        = "climate.primary_suite_thermostat"

	// EV Charger entity
	evChargerSwitch = "switch.leaf_charger"

	// Dehumidifier entity
	dehumidifierSwitch = "switch.dehumidifier_power_control"

	// Temperature ranges
	tempLowRestricted  = 65.0
	tempHighRestricted = 80.0

	// Thermal battery: setpoint offset in degrees F
	thermalBatteryOffset = 2.0

	// Outdoor temperature sensor for thermal battery direction in heat_cool mode (fallback)
	outdoorTempSensor = "sensor.weather_station_temperature"

	// Forecast weather entities for thermal battery direction planning (tried in order)
	forecastWeatherEntityPrimary   = "weather.strawberry_creek" // WeatherFlow Tempest API (local microclimate)
	forecastWeatherEntitySecondary = "weather.forecast_home_2"  // Met.no (generic grid forecast)

	// Skip margin: if forecast high AND low are within this many degrees of the comfort band, skip thermal battery
	thermalBatterySkipMargin = 20.0

	// How long to cache forecast data before refreshing
	forecastCacheDuration = 1 * time.Hour

	// How long to cache a forecast fetch failure before retrying
	forecastNegativeCacheDuration = 15 * time.Minute

	// Thermal battery stepping: apply offset gradually to avoid triggering auxiliary heat
	thermalBatteryStepSize       = 1.0              // degrees F per step
	thermalBatteryDefaultPollInt = 2 * time.Minute  // how often to check if thermostat reached target
	thermalBatteryMaxStepWait    = 30 * time.Minute // safety timeout per step

	// Restart safety: delay after reverting stale holds to let thermostat schedule take effect
	thermalBatteryDefaultHoldRevertDelay = 5 * time.Second

	// Thermal battery: solar-tail timing.
	// Goal: charge the thermal battery using the tail end of solar production, so the
	// house reaches peak thermal storage just as solar fades. Activation is deferred
	// while remainingSolarGeneration exceeds the threshold, which naturally targets
	// the afternoon solar tail.
	//
	// Note: thermalBatteryHourlyComfortMargin (5°F) is intentionally tighter than
	// thermalBatterySkipMargin (20°F). The hourly path triggers on genuine near-term
	// stress; the daily path uses a wide band to skip only on truly mild days.
	thermalBatteryDefaultSolarTailThresholdKWh = 28.0             // activate when remaining solar forecast drops below this (~3hr window before solar ends)
	thermalBatteryHourlyComfortMargin          = 5.0              // °F outside comfort band to consider "stress" (hourly)
	thermalBatteryDeferredRecheckDefault       = 15 * time.Minute // how often to re-evaluate while deferred

	// Thermal battery: hysteresis window after a transient white→green dip.
	// On a green dip, instead of reverting setpoints (which can trigger HVAC counter-runs),
	// we widen the band so neither heating nor cooling engages, and hold for this duration.
	// If energy returns to white during the window, preheat resumes; if it drops further
	// (yellow/red/black) or the window expires, holds are disabled and the schedule resumes.
	thermalBatteryHysteresisDefault = 4 * time.Hour
)

// deferredAction represents a pending action that was rate-limited
type deferredAction struct {
	actionType  string // "enable" or "disable"
	energyLevel string
	trigger     string
}

// Manager manages thermostat control based on energy state
type Manager struct {
	ctx              context.Context
	haClient         ha.HAClient
	stateManager     *state.Manager
	logger           *zap.Logger
	readOnly         bool
	lastAction       time.Time
	lastActionMu     sync.Mutex
	enabled          bool
	loadSheddingOn   bool
	nonHVACLoadsShed bool // Whether non-HVAC loads (EV charger, dehumidifier) are shed
	stateMu          sync.Mutex
	shadowTracker    *shadowstate.LoadSheddingTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Configurable rate limit interval (defaults to minActionInterval)
	rateLimitInterval time.Duration

	// Deferred action mechanism for rate-limited actions
	deferredAction   *deferredAction
	deferredTimer    *time.Timer
	deferredMu       sync.Mutex
	deferredStopChan chan struct{}

	// Thermal battery state
	thermalBatteryActive          bool
	savedSetpoints                map[string]shadowstate.SavedSetpoint
	thermalBatteryMu              sync.Mutex
	thermalBatteryStepsDone       int
	thermalBatteryTargetSteps     int
	thermalBatteryDirection       string        // cached "up"/"down" from activation
	thermalBatteryStepCancel      chan struct{} // signal to stop stepping goroutine
	thermalBatteryPollInt         time.Duration
	thermalBatteryHoldRevertDelay time.Duration
	thermalBatteryStepStart       time.Time     // when the current step began (for safety timeout)
	thermalBatteryMaxStepWaitDur  time.Duration // configurable max wait per step (defaults to thermalBatteryMaxStepWait)

	// Solar-tail timing: deferred activation state (guarded by thermalBatteryMu)
	thermalBatterySolarTailThresholdKWh   float64       // activate when remaining solar drops below this (configurable for tests)
	thermalBatteryDeferredRecheckInterval time.Duration // how often to re-evaluate while deferred
	thermalBatteryDeferred                bool          // true when activation is deferred waiting for solar tail
	thermalBatteryDeferCancel             chan struct{} // signal to stop deferred timer goroutine

	// Hysteresis state (guarded by thermalBatteryMu). When in hysteresis, thermal battery
	// is still considered "active" but setpoints are widened so HVAC stays idle.
	thermalBatteryHysteresisActive    bool
	thermalBatteryHysteresisCancel    chan struct{}
	thermalBatteryHysteresisExpiresAt time.Time
	thermalBatteryHysteresisDuration  time.Duration

	// Forecast cache for thermal battery.
	// forecastMu is intentionally held across the network call in getForecastHighLow.
	// This is acceptable because: (1) callers are serialized via state-change handlers,
	// (2) the 1-hour cache and 15-minute negative cache mean the lock rarely blocks on I/O.
	forecastHigh     float64
	forecastLow      float64
	forecastCachedAt time.Time
	forecastFailedAt time.Time // negative cache: last forecast fetch failure time
	forecastMu       sync.Mutex

	// Hourly forecast cache (guarded by forecastMu)
	hourlyForecastEntries  []hourlyForecastEntry
	hourlyForecastAt       time.Time
	hourlyForecastFailedAt time.Time

	// Push notifications and TTS announcements
	alerter alert.Alerter

	// Test hooks
	deferredActionDoneCallback     func()
	thermalBatteryStepDoneCallback func(stepNumber int)
}

// NewManager creates a new Load Shedding manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, alerter alert.Alerter) *Manager {
	shadowTracker := shadowstate.NewLoadSheddingTracker()

	return &Manager{
		ctx:                                   ctx,
		haClient:                              haClient,
		stateManager:                          stateManager,
		logger:                                logger.Named("loadshedding"),
		readOnly:                              readOnly,
		enabled:                               false,
		shadowTracker:                         shadowTracker,
		subHelper:                             shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "loadshedding", logger.Named("loadshedding")),
		rateLimitInterval:                     minActionInterval,
		deferredStopChan:                      make(chan struct{}),
		alerter:                               alerter,
		thermalBatteryPollInt:                 thermalBatteryDefaultPollInt,
		thermalBatteryHoldRevertDelay:         thermalBatteryDefaultHoldRevertDelay,
		thermalBatteryMaxStepWaitDur:          thermalBatteryMaxStepWait,
		thermalBatterySolarTailThresholdKWh:   thermalBatteryDefaultSolarTailThresholdKWh,
		thermalBatteryDeferredRecheckInterval: thermalBatteryDeferredRecheckDefault,
		thermalBatteryHysteresisDuration:      thermalBatteryHysteresisDefault,
	}
}

// SetRateLimitIntervalForTesting allows tests to use a shorter rate limit interval
func (m *Manager) SetRateLimitIntervalForTesting(interval time.Duration) {
	m.rateLimitInterval = interval
}

// SetDeferredActionDoneCallback sets a callback invoked after a deferred action
// completes execution. This allows tests to synchronize deterministically instead
// of using time.Sleep to wait for deferred actions.
func (m *Manager) SetDeferredActionDoneCallback(cb func()) {
	m.deferredActionDoneCallback = cb
}

// SetThermalBatteryPollIntervalForTesting allows tests to use a shorter poll interval
func (m *Manager) SetThermalBatteryPollIntervalForTesting(d time.Duration) {
	m.thermalBatteryPollInt = d
}

// SetThermalBatteryStepDoneCallback sets a callback invoked after each thermal battery step completes.
func (m *Manager) SetThermalBatteryStepDoneCallback(cb func(stepNumber int)) {
	m.thermalBatteryStepDoneCallback = cb
}

// SetThermalBatteryHoldRevertDelayForTesting allows tests to skip the hold revert delay.
func (m *Manager) SetThermalBatteryHoldRevertDelayForTesting(d time.Duration) {
	m.thermalBatteryHoldRevertDelay = d
}

// SetThermalBatteryMaxStepWaitForTesting allows tests to use a shorter safety timeout per step.
func (m *Manager) SetThermalBatteryMaxStepWaitForTesting(d time.Duration) {
	m.thermalBatteryMaxStepWaitDur = d
}

// SetThermalBatterySolarTailThresholdForTesting allows tests to override the solar-tail
// activation threshold (remaining kWh below which activation fires).
func (m *Manager) SetThermalBatterySolarTailThresholdForTesting(kwh float64) {
	m.thermalBatterySolarTailThresholdKWh = kwh
}

// SetThermalBatteryDeferredRecheckIntervalForTesting allows tests to use a shorter recheck interval.
func (m *Manager) SetThermalBatteryDeferredRecheckIntervalForTesting(d time.Duration) {
	m.thermalBatteryDeferredRecheckInterval = d
}

// SetThermalBatteryHysteresisDurationForTesting allows tests to use a shorter hysteresis window.
func (m *Manager) SetThermalBatteryHysteresisDurationForTesting(d time.Duration) {
	m.thermalBatteryHysteresisDuration = d
}

// IsLoadSheddingOn returns whether load shedding is currently active (thread-safe)
func (m *Manager) IsLoadSheddingOn() bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.loadSheddingOn
}

// Start begins monitoring energy state and controlling thermostats
func (m *Manager) Start() error {
	if m.enabled {
		return fmt.Errorf("load shedding already started")
	}

	m.logger.Info("Starting Load Shedding Manager")

	// Restart safety: if a previous process left thermostat holds enabled (mid-thermal-battery
	// or mid-hysteresis), clear them so the climate schedule resumes. Skip when current
	// energy is red/black — load shedding may legitimately have those holds enabled, and
	// the energy-change handler below will re-evaluate and re-establish them as needed.
	if level, err := m.stateManager.GetString("currentEnergyLevel"); err == nil &&
		level != energyStateRed && level != energyStateBlack {
		m.clearStaleThermostatHolds()
	}

	// Subscribe to energy level changes (shadow inputs captured automatically)
	if err := m.subHelper.SubscribeToState("currentEnergyLevel", m.handleEnergyChange); err != nil {
		return fmt.Errorf("failed to subscribe to energy level: %w", err)
	}

	// Subscribe to presence/sleep states for thermal battery guard conditions
	// These are tracked as shadow inputs but don't trigger actions on their own
	if err := m.subHelper.SubscribeToState("isAnyoneHome", m.handlePresenceChange); err != nil {
		return fmt.Errorf("failed to subscribe to isAnyoneHome: %w", err)
	}
	if err := m.subHelper.SubscribeToState("isEveryoneAsleep", m.handlePresenceChange); err != nil {
		return fmt.Errorf("failed to subscribe to isEveryoneAsleep: %w", err)
	}

	// Initialize shadow state with current input values (after subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	// Process initial state
	currentLevel, err := m.stateManager.GetString("currentEnergyLevel")
	if err != nil {
		m.logger.Warn("Failed to get initial energy level", zap.Error(err))
	} else {
		m.logger.Info("Initial energy level", zap.String("level", currentLevel))
		m.handleEnergyChange("currentEnergyLevel", "", currentLevel)
	}

	m.enabled = true
	m.logger.Info("Load Shedding Manager started successfully")
	return nil
}

// Stop stops the Load Shedding Manager and cleans up subscriptions
func (m *Manager) Stop() {
	if !m.enabled {
		return
	}

	m.logger.Info("Stopping Load Shedding Manager")

	// Stop any thermal battery stepping, deferred, or hysteresis timer goroutines
	m.thermalBatteryMu.Lock()
	m.stopThermalBatteryStepping()
	m.stopDeferredActivationTimer()
	m.stopHysteresisTimer()
	m.thermalBatteryMu.Unlock()

	// Cancel any pending deferred action
	m.cancelDeferredAction()

	m.subHelper.UnsubscribeAll()
	m.enabled = false
	m.logger.Info("Load Shedding Manager stopped")
}

// handleEnergyChange is called when currentEnergyLevel changes
func (m *Manager) handleEnergyChange(key string, oldValue, newValue interface{}) {
	m.handleEnergyChangeWithTrigger(key, oldValue, newValue, key)
}

// handleEnergyChangeWithTrigger processes energy level changes with a specific trigger
func (m *Manager) handleEnergyChangeWithTrigger(key string, oldValue, newValue interface{}, trigger string) {
	// Shadow state inputs are automatically captured by SubscriptionHelper

	// Convert values to strings
	oldLevel := ""
	if oldValue != nil {
		if s, ok := oldValue.(string); ok {
			oldLevel = s
		}
	}

	newLevel := ""
	if newValue != nil {
		if s, ok := newValue.(string); ok {
			newLevel = s
		}
	}

	m.logger.Info("Energy level changed",
		zap.String("old_level", oldLevel),
		zap.String("new_level", newLevel),
		zap.String("trigger", trigger))

	// Determine action based on new state
	// Yellow is a hysteresis buffer - maintain current state to prevent rapid toggling
	switch newLevel {
	case energyStateRed, energyStateBlack:
		m.deactivateThermalBattery("energy level dropped to " + newLevel)
		m.enableLoadShedding(newLevel, trigger)
	case energyStateGreen:
		// Green is still a healthy state (battery ≥80% AND ≥10 kWh remaining solar).
		// Don't snap thermostats back — entering hysteresis widens the setpoint band so
		// HVAC stays idle, avoiding counter-runs from a transient white→green oscillation.
		// If thermal battery isn't active, this is a no-op.
		m.enterThermalBatteryHysteresis()
		m.restoreNonHVACLoads(newLevel)
		m.disableLoadShedding(newLevel, trigger)
	case energyStateWhite:
		m.restoreNonHVACLoads(newLevel)
		m.disableLoadShedding(newLevel, trigger)
		m.activateThermalBattery()
	case energyStateYellow:
		m.deactivateThermalBattery("energy level dropped to yellow")
		m.shedNonHVACLoads(newLevel)
		m.logger.Info("Energy state is yellow - shedding non-HVAC loads, HVAC maintains current state",
			zap.String("reason", "Hysteresis buffer for HVAC, but non-essential loads shed"))
	default:
		m.logger.Warn("Unknown energy state",
			zap.String("state", newLevel))
	}
}

// shedNonHVACLoads turns off non-essential loads (EV charger, dehumidifier) without
// touching HVAC. Used at yellow energy level for partial load shedding.
func (m *Manager) shedNonHVACLoads(energyLevel string) {
	m.stateMu.Lock()
	alreadyShed := m.nonHVACLoadsShed
	m.stateMu.Unlock()

	if alreadyShed {
		m.logger.Info("⏭  Non-HVAC loads already shed")
		return
	}

	m.logger.Info("=== NON-HVAC LOAD SHEDDING: ENABLE ===",
		zap.String("energy_level", energyLevel),
		zap.String("reason", "Shedding non-essential loads at "+energyLevel+" energy level"))

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would turn off EV charger and dehumidifier",
			zap.String("ev_charger_entity", evChargerSwitch),
			zap.String("dehumidifier_entity", dehumidifierSwitch))
		m.stateMu.Lock()
		m.nonHVACLoadsShed = true
		m.stateMu.Unlock()
		return
	}

	// Turn off EV charger
	if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
		"entity_id": evChargerSwitch,
	}); err != nil {
		m.logger.Error("Failed to turn off EV charger", zap.Error(err))
	} else {
		m.logger.Info("✓ Successfully turned off EV charger")
	}

	// Turn off dehumidifier
	if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
		"entity_id": dehumidifierSwitch,
	}); err != nil {
		m.logger.Error("Failed to turn off dehumidifier", zap.Error(err))
	} else {
		m.logger.Info("✓ Successfully turned off dehumidifier")
	}

	m.stateMu.Lock()
	m.nonHVACLoadsShed = true
	m.stateMu.Unlock()

	m.logger.Info("=== NON-HVAC LOADS SHED ===",
		zap.String("action", "EV charger and dehumidifier disabled to conserve battery"))
}

// restoreNonHVACLoads turns on non-essential loads (EV charger, dehumidifier).
// Called at green/white energy levels to restore loads that were shed at yellow.
func (m *Manager) restoreNonHVACLoads(energyLevel string) {
	m.stateMu.Lock()
	needsRestore := m.nonHVACLoadsShed && !m.loadSheddingOn
	m.stateMu.Unlock()

	if !needsRestore {
		return
	}

	m.logger.Info("=== NON-HVAC LOAD SHEDDING: DISABLE ===",
		zap.String("energy_level", energyLevel),
		zap.String("reason", "Restoring non-essential loads at "+energyLevel+" energy level"))

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would turn on EV charger and dehumidifier",
			zap.String("ev_charger_entity", evChargerSwitch),
			zap.String("dehumidifier_entity", dehumidifierSwitch))
		m.stateMu.Lock()
		m.nonHVACLoadsShed = false
		m.stateMu.Unlock()
		return
	}

	// Turn on EV charger
	if err := m.haClient.CallService(m.ctx, "switch", "turn_on", map[string]interface{}{
		"entity_id": evChargerSwitch,
	}); err != nil {
		m.logger.Error("Failed to turn on EV charger", zap.Error(err))
	} else {
		m.logger.Info("✓ Successfully turned on EV charger")
	}

	// Turn on dehumidifier
	if err := m.haClient.CallService(m.ctx, "switch", "turn_on", map[string]interface{}{
		"entity_id": dehumidifierSwitch,
	}); err != nil {
		m.logger.Error("Failed to turn on dehumidifier", zap.Error(err))
	} else {
		m.logger.Info("✓ Successfully turned on dehumidifier")
	}

	m.stateMu.Lock()
	m.nonHVACLoadsShed = false
	m.stateMu.Unlock()

	m.logger.Info("=== NON-HVAC LOADS RESTORED ===",
		zap.String("action", "EV charger and dehumidifier re-enabled"))
}

// enableLoadShedding activates load shedding (energy state red/black)
func (m *Manager) enableLoadShedding(energyLevel string, trigger string) {
	m.logger.Info("=== LOAD SHEDDING DECISION: ENABLE ===",
		zap.String("energy_level", energyLevel),
		zap.String("trigger", trigger),
		zap.String("reason", "Energy state is "+energyLevel+" (low battery)"))

	// Cancel any pending disable action since we're now enabling
	m.cancelDeferredAction()

	// Check if load shedding is already enabled
	m.stateMu.Lock()
	alreadyEnabled := m.loadSheddingOn
	m.stateMu.Unlock()

	if alreadyEnabled {
		m.logger.Info("⏭  Action skipped: Load shedding already enabled",
			zap.String("reason", "Preventing unnecessary thermostat changes"))
		return
	}

	// Check current thermostat hold state
	holdOn, err := m.checkThermostatHoldState()
	if err != nil {
		m.logger.Warn("Failed to check thermostat hold state, proceeding with action",
			zap.Error(err))
	} else if holdOn {
		m.logger.Info("⏭  Action skipped: Thermostat holds already enabled",
			zap.String("reason", "Thermostats already in desired state"))
		// Update our state tracking to match reality
		m.stateMu.Lock()
		m.loadSheddingOn = true
		m.stateMu.Unlock()
		return
	}

	// Check rate limiting - if rate limited, schedule deferred action
	passed, timeRemaining := m.checkRateLimit()
	if !passed {
		m.scheduleDeferredAction("enable", energyLevel, trigger, timeRemaining)
		return
	}

	m.executeEnableLoadShedding(energyLevel, trigger)
}

// executeEnableLoadShedding performs the actual enable action (called directly or deferred)
func (m *Manager) executeEnableLoadShedding(energyLevel string, trigger string) {
	// Re-check if load shedding is already enabled (state may have changed)
	m.stateMu.Lock()
	alreadyEnabled := m.loadSheddingOn
	m.stateMu.Unlock()

	if alreadyEnabled {
		m.logger.Info("⏭  Deferred action skipped: Load shedding already enabled",
			zap.String("reason", "State changed while waiting"))
		return
	}

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would enable thermostat hold mode, turn off EV charger, and turn off dehumidifier",
			zap.Strings("thermostat_entities", []string{thermostatHoldHouse, thermostatHoldSuite}),
			zap.String("ev_charger_entity", evChargerSwitch),
			zap.String("dehumidifier_entity", dehumidifierSwitch))
		// Record shadow state even in read-only mode for consistency
		reason := fmt.Sprintf("Energy state is %s (low battery) - would restrict HVAC, disable EV charger, and disable dehumidifier", energyLevel)
		m.recordAction(true, "enable", reason, true, tempLowRestricted, tempHighRestricted, trigger)
		return
	}

	// Turn on thermostat hold mode
	m.logger.Info("Executing: Enable thermostat hold mode",
		zap.Strings("entities", []string{thermostatHoldHouse, thermostatHoldSuite}))

	if err := m.haClient.CallService(m.ctx, "switch", "turn_on", map[string]interface{}{
		"entity_id": []string{thermostatHoldHouse, thermostatHoldSuite},
	}); err != nil {
		m.logger.Error("Failed to enable thermostat hold mode",
			zap.Error(err))
		return
	}

	m.logger.Info("✓ Successfully enabled thermostat hold mode")

	// Set wider temperature range
	m.logger.Info("Executing: Set wider temperature range",
		zap.Float64("temp_low", tempLowRestricted),
		zap.Float64("temp_high", tempHighRestricted),
		zap.Strings("entities", []string{climateHouse, climateSuite}))

	if err := m.haClient.CallService(m.ctx, "climate", "set_temperature", map[string]interface{}{
		"entity_id":        []string{climateHouse, climateSuite},
		"target_temp_low":  tempLowRestricted,
		"target_temp_high": tempHighRestricted,
	}); err != nil {
		m.logger.Error("Failed to set thermostat temperature range",
			zap.Error(err))
		return
	}

	m.logger.Info("✓ Successfully set wider temperature range")

	// Turn off EV charger to reduce load
	m.logger.Info("Executing: Turn off EV charger",
		zap.String("entity", evChargerSwitch))

	if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
		"entity_id": evChargerSwitch,
	}); err != nil {
		m.logger.Error("Failed to turn off EV charger", zap.Error(err))
		// Continue - thermostat control already succeeded
	} else {
		m.logger.Info("✓ Successfully turned off EV charger")
	}

	// Turn off dehumidifier to reduce load
	m.logger.Info("Executing: Turn off dehumidifier",
		zap.String("entity", dehumidifierSwitch))

	if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
		"entity_id": dehumidifierSwitch,
	}); err != nil {
		m.logger.Error("Failed to turn off dehumidifier", zap.Error(err))
		// Continue - previous controls already succeeded
	} else {
		m.logger.Info("✓ Successfully turned off dehumidifier")
	}

	m.logger.Info("=== LOAD SHEDDING ACTIVATED ===",
		zap.String("action", "HVAC restricted, EV charger disabled, and dehumidifier disabled to conserve battery"))

	// Update state tracking and last action time
	m.stateMu.Lock()
	m.loadSheddingOn = true
	m.nonHVACLoadsShed = true
	m.stateMu.Unlock()

	m.lastActionMu.Lock()
	m.lastAction = time.Now()
	m.lastActionMu.Unlock()

	// Record action in shadow state
	reason := fmt.Sprintf("Energy state is %s (low battery) - restricting HVAC", energyLevel)
	m.recordAction(true, "enable", reason, true, tempLowRestricted, tempHighRestricted, trigger)
}

// disableLoadShedding deactivates load shedding (energy state green/white)
func (m *Manager) disableLoadShedding(energyLevel string, trigger string) {
	m.logger.Info("=== LOAD SHEDDING DECISION: DISABLE ===",
		zap.String("energy_level", energyLevel),
		zap.String("trigger", trigger),
		zap.String("reason", "Energy state is "+energyLevel+" (battery restored)"))

	// Cancel any pending enable action since we're now disabling
	m.cancelDeferredAction()

	// Check if load shedding is already disabled
	m.stateMu.Lock()
	alreadyDisabled := !m.loadSheddingOn
	m.stateMu.Unlock()

	if alreadyDisabled {
		m.logger.Info("⏭  Action skipped: Load shedding already disabled",
			zap.String("reason", "Preventing unnecessary thermostat changes"))
		return
	}

	// Note: we used to bail here if HA showed holds already off, on the assumption
	// "holds off → load shedding fully disabled". That assumption no longer holds —
	// thermal-battery hysteresis and the Start() stale-hold cleanup can independently
	// turn holds off, leaving EV/dehumidifier still shed. executeDisableLoadShedding
	// is idempotent on holds (turn_off-when-off is a no-op in HA), so we always run
	// the full disable to guarantee EV/dehumidifier get restored.

	// Check rate limiting - if rate limited, schedule deferred action
	passed, timeRemaining := m.checkRateLimit()
	if !passed {
		m.scheduleDeferredAction("disable", energyLevel, trigger, timeRemaining)
		return
	}

	m.executeDisableLoadShedding(energyLevel, trigger)
}

// executeDisableLoadShedding performs the actual disable action (called directly or deferred)
func (m *Manager) executeDisableLoadShedding(energyLevel string, trigger string) {
	// Re-check if load shedding is already disabled (state may have changed)
	m.stateMu.Lock()
	alreadyDisabled := !m.loadSheddingOn
	m.stateMu.Unlock()

	if alreadyDisabled {
		m.logger.Info("⏭  Deferred action skipped: Load shedding already disabled",
			zap.String("reason", "State changed while waiting"))
		return
	}

	if m.readOnly {
		m.logger.Info("READ-ONLY: Would disable thermostat hold mode, turn on EV charger, and turn on dehumidifier (restore schedule)",
			zap.Strings("thermostat_entities", []string{thermostatHoldHouse, thermostatHoldSuite}),
			zap.String("ev_charger_entity", evChargerSwitch),
			zap.String("dehumidifier_entity", dehumidifierSwitch))
		// Record shadow state even in read-only mode for consistency
		reason := fmt.Sprintf("Energy state is %s (battery restored) - would return to normal HVAC, re-enable EV charger, and re-enable dehumidifier", energyLevel)
		m.recordAction(false, "disable", reason, false, 0, 0, trigger)
		return
	}

	// Turn off thermostat hold mode (return to schedule)
	m.logger.Info("Executing: Disable thermostat hold mode (restore schedule)",
		zap.Strings("entities", []string{thermostatHoldHouse, thermostatHoldSuite}))

	if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
		"entity_id": []string{thermostatHoldHouse, thermostatHoldSuite},
	}); err != nil {
		m.logger.Error("Failed to disable thermostat hold mode",
			zap.Error(err))
		return
	}

	m.logger.Info("✓ Successfully disabled thermostat hold mode")

	// Turn on EV charger (restore normal operation)
	m.logger.Info("Executing: Turn on EV charger",
		zap.String("entity", evChargerSwitch))

	if err := m.haClient.CallService(m.ctx, "switch", "turn_on", map[string]interface{}{
		"entity_id": evChargerSwitch,
	}); err != nil {
		m.logger.Error("Failed to turn on EV charger", zap.Error(err))
		// Continue - thermostat control already succeeded
	} else {
		m.logger.Info("✓ Successfully turned on EV charger")
	}

	// Turn on dehumidifier (restore normal operation)
	m.logger.Info("Executing: Turn on dehumidifier",
		zap.String("entity", dehumidifierSwitch))

	if err := m.haClient.CallService(m.ctx, "switch", "turn_on", map[string]interface{}{
		"entity_id": dehumidifierSwitch,
	}); err != nil {
		m.logger.Error("Failed to turn on dehumidifier", zap.Error(err))
		// Continue - previous controls already succeeded
	} else {
		m.logger.Info("✓ Successfully turned on dehumidifier")
	}

	m.logger.Info("=== LOAD SHEDDING DEACTIVATED ===",
		zap.String("action", "HVAC returned to normal schedule, EV charger re-enabled, and dehumidifier re-enabled"))

	// Update state tracking and last action time
	m.stateMu.Lock()
	m.loadSheddingOn = false
	m.nonHVACLoadsShed = false
	m.stateMu.Unlock()

	m.lastActionMu.Lock()
	m.lastAction = time.Now()
	m.lastActionMu.Unlock()

	// Record action in shadow state
	reason := fmt.Sprintf("Energy state is %s (battery restored) - returning to normal HVAC", energyLevel)
	m.recordAction(false, "disable", reason, false, 0, 0, trigger)
}

// checkRateLimit ensures we don't take actions too frequently
// Returns (passed, timeRemaining) where timeRemaining is how long until rate limit expires
func (m *Manager) checkRateLimit() (bool, time.Duration) {
	m.lastActionMu.Lock()
	defer m.lastActionMu.Unlock()

	now := time.Now()
	timeSinceLastAction := now.Sub(m.lastAction)

	if !m.lastAction.IsZero() && timeSinceLastAction < m.rateLimitInterval {
		timeRemaining := m.rateLimitInterval - timeSinceLastAction
		m.logger.Info("⏱  RATE LIMIT: Action will be deferred",
			zap.Duration("time_since_last_action", timeSinceLastAction),
			zap.Duration("min_interval", m.rateLimitInterval),
			zap.Duration("time_remaining", timeRemaining),
			zap.String("reason", "Scheduling deferred action after rate limit expires"))
		return false, timeRemaining
	}

	m.logger.Info("✓ Rate limit check passed",
		zap.Duration("time_since_last_action", timeSinceLastAction))
	return true, 0
}

// scheduleDeferredAction schedules an action to execute after the rate limit expires
func (m *Manager) scheduleDeferredAction(actionType string, energyLevel string, trigger string, delay time.Duration) {
	m.deferredMu.Lock()
	defer m.deferredMu.Unlock()

	// Cancel any existing deferred action
	if m.deferredTimer != nil {
		m.deferredTimer.Stop()
		m.deferredTimer = nil
	}

	m.deferredAction = &deferredAction{
		actionType:  actionType,
		energyLevel: energyLevel,
		trigger:     trigger,
	}

	m.logger.Info("📅 Scheduling deferred action",
		zap.String("action_type", actionType),
		zap.String("energy_level", energyLevel),
		zap.Duration("delay", delay))

	m.deferredTimer = time.AfterFunc(delay, func() {
		m.executeDeferredAction()
	})
}

// cancelDeferredAction cancels any pending deferred action
func (m *Manager) cancelDeferredAction() {
	m.deferredMu.Lock()
	defer m.deferredMu.Unlock()

	if m.deferredTimer != nil {
		m.deferredTimer.Stop()
		m.deferredTimer = nil
	}

	if m.deferredAction != nil {
		m.logger.Info("🚫 Cancelled deferred action",
			zap.String("action_type", m.deferredAction.actionType),
			zap.String("energy_level", m.deferredAction.energyLevel))
		m.deferredAction = nil
	}
}

// executeDeferredAction executes the pending deferred action
func (m *Manager) executeDeferredAction() {
	m.deferredMu.Lock()
	action := m.deferredAction
	m.deferredAction = nil
	m.deferredTimer = nil
	m.deferredMu.Unlock()

	if action == nil {
		return
	}

	m.logger.Info("⏰ Executing deferred action after rate limit expired",
		zap.String("action_type", action.actionType),
		zap.String("energy_level", action.energyLevel),
		zap.String("trigger", action.trigger))

	// Execute the deferred action
	switch action.actionType {
	case "enable":
		m.executeEnableLoadShedding(action.energyLevel, action.trigger+" (deferred)")
	case "disable":
		m.executeDisableLoadShedding(action.energyLevel, action.trigger+" (deferred)")
	}

	// Notify test callback if set
	if m.deferredActionDoneCallback != nil {
		m.deferredActionDoneCallback()
	}
}

// checkThermostatHoldState checks if thermostat holds are currently enabled
// Returns true if at least one hold is on, false otherwise
func (m *Manager) checkThermostatHoldState() (bool, error) {
	// Get state of both thermostat hold switches
	houseState, err := m.haClient.GetState(thermostatHoldHouse)
	if err != nil {
		return false, fmt.Errorf("failed to get house thermostat hold state: %w", err)
	}

	suiteState, err := m.haClient.GetState(thermostatHoldSuite)
	if err != nil {
		return false, fmt.Errorf("failed to get suite thermostat hold state: %w", err)
	}

	// Check if either hold is on
	houseOn := houseState.State == "on"
	suiteOn := suiteState.State == "on"

	m.logger.Debug("Current thermostat hold states",
		zap.Bool("house_hold", houseOn),
		zap.Bool("suite_hold", suiteOn))

	return houseOn || suiteOn, nil
}

// Reset re-evaluates current energy level and applies appropriate thermostat control
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Load Shedding - re-evaluating thermostat control based on current energy level")

	// Get current energy level
	currentLevel, err := m.stateManager.GetString("currentEnergyLevel")
	if err != nil {
		return fmt.Errorf("failed to get current energy level: %w", err)
	}

	m.logger.Info("Re-processing energy level for reset",
		zap.String("energy_level", currentLevel))

	// Re-evaluate load shedding based on current energy level with reset trigger
	m.handleEnergyChangeWithTrigger("currentEnergyLevel", "", currentLevel, "reset")

	m.logger.Info("Successfully reset Load Shedding")
	return nil
}

// addTriggerToInputs adds the trigger field to the current shadow state inputs
// Note: Other inputs are automatically captured by SubscriptionHelper before handlers run
func (m *Manager) addTriggerToInputs(trigger string) {
	m.shadowTracker.UpdateCurrentInputs(map[string]interface{}{
		"trigger": trigger,
	})
}

// recordAction snapshots inputs and records an action in shadow state
func (m *Manager) recordAction(active bool, actionType string, reason string, holdMode bool, tempLow float64, tempHigh float64, trigger string) {
	// Add trigger to inputs (other inputs already captured by SubscriptionHelper)
	m.addTriggerToInputs(trigger)

	// Snapshot inputs for this action
	m.shadowTracker.SnapshotInputsForAction()

	// Record the action
	thermostatSettings := shadowstate.ThermostatSettings{
		HoldMode: holdMode,
		TempLow:  tempLow,
		TempHigh: tempHigh,
	}
	m.shadowTracker.RecordLoadSheddingAction(active, actionType, reason, thermostatSettings)
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.LoadSheddingShadowState {
	return m.shadowTracker.GetState()
}
