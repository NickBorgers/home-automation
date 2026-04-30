// Package infrastructure provides monitoring for infrastructure systems like the aerobic septic system.
package infrastructure

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Configuration constants for septic system monitoring
const (
	// SepticSensorEntity is the Home Assistant entity for the septic system power sensor
	SepticSensorEntity = "sensor.span_left_most_of_house_aerobic_septic_system_power"

	// AeratorMinPowerW is the minimum power expected from the aerator (below = failure)
	AeratorMinPowerW = 50.0

	// PumpMaxNormalPowerW is the max power before considering pump is running
	PumpMaxNormalPowerW = 300.0

	// AeratorFailureDebounceMinutes is how long low power must persist before alerting
	AeratorFailureDebounceMinutes = 5

	// PumpStuckThresholdMinutes is how long pump can run before alerting
	PumpStuckThresholdMinutes = 60

	// AlertCheckInterval is how often to check for duration-based alerts
	AlertCheckInterval = 1 * time.Minute

	// RecoveryNotificationCooldown prevents recovery spam
	RecoveryNotificationCooldown = 30 * time.Minute

	// RepeatAlertCooldown prevents repeat alert spam
	RepeatAlertCooldown = 4 * time.Hour
)

// Thermostat entity constants
const (
	// WellThermostatEntity is the well shed heater thermostat (light that generates heat)
	WellThermostatEntity = "climate.well_light_thermostat"

	// BarnThermostatEntity is the barn dehumidifier control thermostat
	BarnThermostatEntity = "climate.barn_dehumidifier_thermostat"
)

// Manager handles infrastructure monitoring for the septic system
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	clock        clock.Clock
	ntfyClient   ntfy.Notifier
	notifier     notify.Notifier

	// Shadow state tracking
	shadowTracker *shadowstate.InfrastructureTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// State tracking
	mu                       sync.Mutex
	currentPowerW            float64
	lowPowerStartTime        time.Time // When power dropped below AeratorMinPowerW
	highPowerStartTime       time.Time // When power went above PumpMaxNormalPowerW
	lastAlertNotification    time.Time // Last time we sent an alert notification
	lastRecoveryNotification time.Time // Last time we sent a recovery notification
	isAeratorFailure         bool      // Currently in aerator failure state
	isPumpStuck              bool      // Currently in pump stuck state

	// Thermostat state tracking
	wellHVACAction string // Current hvac_action for well thermostat
	barnHVACAction string // Current hvac_action for barn thermostat

	// Periodic checker
	stopChecker chan struct{}
}

// NewManager creates a new infrastructure manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier, notifier notify.Notifier) *Manager {
	shadowTracker := shadowstate.NewInfrastructureTracker()

	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("infrastructure"),
		readOnly:      readOnly,
		clock:         clock.NewRealClock(),
		ntfyClient:    ntfyClient,
		notifier:      notifier,
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "infrastructure", logger.Named("infrastructure")),
	}
}

// NewManagerWithClock creates a new infrastructure manager with a custom clock (for testing)
func NewManagerWithClock(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier, notifier notify.Notifier, c clock.Clock) *Manager {
	m := NewManager(ctx, haClient, stateManager, logger, readOnly, registry, ntfyClient, notifier)
	m.clock = c
	return m
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.InfrastructureShadowState {
	return m.shadowTracker.GetState()
}

// Start begins infrastructure monitoring
func (m *Manager) Start() error {
	m.logger.Info("Starting Infrastructure Manager")

	// Subscribe to the septic power sensor
	if err := m.subHelper.SubscribeToEntity(SepticSensorEntity, m.handlePowerChange); err != nil {
		return fmt.Errorf("failed to subscribe to septic power sensor: %w", err)
	}

	// Subscribe to thermostat entities
	if err := m.subHelper.SubscribeToEntity(WellThermostatEntity, m.handleThermostatChange); err != nil {
		return fmt.Errorf("failed to subscribe to well thermostat: %w", err)
	}
	if err := m.subHelper.SubscribeToEntity(BarnThermostatEntity, m.handleThermostatChange); err != nil {
		return fmt.Errorf("failed to subscribe to barn thermostat: %w", err)
	}

	// Capture initial inputs after subscriptions registered
	m.subHelper.CaptureInitialInputs()

	// Start periodic checker for duration-based alerts
	m.startPeriodicChecker()

	m.logger.Info("Infrastructure Manager started successfully")
	return nil
}

// Stop stops the manager and cleans up
func (m *Manager) Stop() {
	m.logger.Info("Stopping Infrastructure Manager")

	m.stopPeriodicCheckerFunc()
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Infrastructure Manager stopped")
}

// Reset re-evaluates conditions and clears rate limiters
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Infrastructure Manager")

	m.mu.Lock()
	m.lastAlertNotification = time.Time{}
	m.lastRecoveryNotification = time.Time{}
	m.mu.Unlock()

	// Re-evaluate current state
	m.evaluateConditions()

	m.logger.Info("Successfully reset Infrastructure Manager")
	return nil
}

// handlePowerChange processes changes to the septic system power sensor
func (m *Manager) handlePowerChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Parse the power value
	powerW, err := strconv.ParseFloat(newState.State, 64)
	if err != nil {
		m.logger.Warn("Failed to parse septic power value",
			zap.String("state", newState.State),
			zap.Error(err))
		return
	}

	m.logger.Debug("Septic power changed",
		zap.Float64("power_w", powerW),
		zap.String("old_state", func() string {
			if oldState != nil {
				return oldState.State
			}
			return "nil"
		}()))

	now := m.clock.Now()

	m.mu.Lock()
	m.currentPowerW = powerW
	m.shadowTracker.UpdateSepticPower(powerW)

	// Update condition tracking based on power level
	if powerW < AeratorMinPowerW {
		// Low power - aerator may have failed
		if m.lowPowerStartTime.IsZero() {
			m.lowPowerStartTime = now
			m.shadowTracker.UpdateAeratorFailureStart(now)
			m.logger.Info("Low power condition detected, starting debounce timer",
				zap.Float64("power_w", powerW))
		}
		// Clear high power tracking
		m.highPowerStartTime = time.Time{}
		m.shadowTracker.UpdatePumpRunningStart(time.Time{})
	} else if powerW > PumpMaxNormalPowerW {
		// High power - pump is running
		if m.highPowerStartTime.IsZero() {
			m.highPowerStartTime = now
			m.shadowTracker.UpdatePumpRunningStart(now)
			m.logger.Info("Pump running detected",
				zap.Float64("power_w", powerW))
		}
		// Clear low power tracking
		m.lowPowerStartTime = time.Time{}
		m.shadowTracker.UpdateAeratorFailureStart(time.Time{})
	} else {
		// Normal range (50-300W) - aerator is running, pump is off
		wasAlerting := m.isAeratorFailure || m.isPumpStuck
		m.lowPowerStartTime = time.Time{}
		m.highPowerStartTime = time.Time{}
		m.shadowTracker.UpdateAeratorFailureStart(time.Time{})
		m.shadowTracker.UpdatePumpRunningStart(time.Time{})
		m.shadowTracker.UpdateLastNormalPowerTime(now)
		m.shadowTracker.UpdateSystemState("normal")

		// Check if we should send recovery notification
		if wasAlerting {
			m.isAeratorFailure = false
			m.isPumpStuck = false
			m.shadowTracker.UpdateIsAlerting(false)
			m.shadowTracker.ClearAlerts()
			m.mu.Unlock()
			m.sendRecoveryNotification()
			return
		}
	}
	m.mu.Unlock()
}

// startPeriodicChecker starts the goroutine that checks for duration-based alerts
func (m *Manager) startPeriodicChecker() {
	m.mu.Lock()
	if m.stopChecker != nil {
		m.mu.Unlock()
		return // Already running
	}
	m.stopChecker = make(chan struct{})
	stopCh := m.stopChecker
	m.mu.Unlock()

	go func() {
		ticker := m.clock.NewTicker(AlertCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C():
				m.evaluateConditions()
			}
		}
	}()
}

// stopPeriodicCheckerFunc stops the periodic checker goroutine
func (m *Manager) stopPeriodicCheckerFunc() {
	m.mu.Lock()
	if m.stopChecker != nil {
		close(m.stopChecker)
		m.stopChecker = nil
	}
	m.mu.Unlock()
}

// evaluateConditions checks if any alert thresholds have been exceeded
func (m *Manager) evaluateConditions() {
	now := m.clock.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for aerator failure (low power for > 5 minutes)
	if !m.lowPowerStartTime.IsZero() {
		duration := now.Sub(m.lowPowerStartTime)
		if duration >= AeratorFailureDebounceMinutes*time.Minute && !m.isAeratorFailure {
			m.logger.Warn("Aerator failure detected",
				zap.Float64("power_w", m.currentPowerW),
				zap.Duration("duration", duration))
			m.isAeratorFailure = true
			m.shadowTracker.UpdateSystemState("aerator_failure")
			m.shadowTracker.UpdateIsAlerting(true)

			alerts := []shadowstate.InfrastructureAlert{{
				AlertType:  "aerator_failure",
				Message:    fmt.Sprintf("Septic aerator failure - power %.0fW (expected >50W)", m.currentPowerW),
				DetectedAt: m.lowPowerStartTime,
				Severity:   "urgent",
			}}
			m.shadowTracker.UpdateActiveAlerts(alerts)

			// Send notification
			m.sendAlertNotification("aerator_failure", fmt.Sprintf("Septic aerator failure - power %.0fW (expected >50W)", m.currentPowerW))
		}
	}

	// Check for pump stuck (high power for > 60 minutes)
	if !m.highPowerStartTime.IsZero() {
		duration := now.Sub(m.highPowerStartTime)
		if duration >= PumpStuckThresholdMinutes*time.Minute && !m.isPumpStuck {
			m.logger.Warn("Pump stuck running detected",
				zap.Float64("power_w", m.currentPowerW),
				zap.Duration("duration", duration))
			m.isPumpStuck = true
			m.shadowTracker.UpdateSystemState("pump_stuck")
			m.shadowTracker.UpdateIsAlerting(true)

			alerts := []shadowstate.InfrastructureAlert{{
				AlertType:  "pump_stuck",
				Message:    fmt.Sprintf("Septic pump stuck running - power %.0fW for %d+ minutes", m.currentPowerW, int(duration.Minutes())),
				DetectedAt: m.highPowerStartTime,
				Severity:   "urgent",
			}}
			m.shadowTracker.UpdateActiveAlerts(alerts)

			// Send notification
			m.sendAlertNotification("pump_stuck", fmt.Sprintf("Septic pump stuck running for %d+ minutes - power %.0fW", int(duration.Minutes()), m.currentPowerW))
		}
	}
}

// sendAlertNotification sends an alert via ntfy and TTS
func (m *Manager) sendAlertNotification(alertType, message string) {
	// Check rate limiting (must be called with lock held)
	if !m.lastAlertNotification.IsZero() && m.clock.Since(m.lastAlertNotification) < RepeatAlertCooldown {
		m.logger.Debug("Skipping alert notification due to rate limiting",
			zap.String("alert_type", alertType),
			zap.Duration("since_last", m.clock.Since(m.lastAlertNotification)))
		return
	}
	m.lastAlertNotification = m.clock.Now()

	// Record notification in shadow state
	m.shadowTracker.RecordNotification(alertType, message, "urgent")

	// Release lock before doing I/O
	m.mu.Unlock()
	defer m.mu.Lock()

	// Send ntfy notification
	if m.ntfyClient != nil {
		if err := m.ntfyClient.Send(&ntfy.Message{
			Title:    "Septic System Alert",
			Body:     message,
			Priority: ntfy.PriorityUrgent,
			Tags:     []string{"warning", "toilet"},
		}); err != nil {
			m.logger.Error("Failed to send septic alert notification",
				zap.String("alert_type", alertType),
				zap.Error(err))
		} else {
			m.logger.Info("Septic alert notification sent", zap.String("message", message))
		}
	} else {
		m.logger.Warn("ntfy client not configured, cannot send septic alert notification")
	}

	// Send TTS announcement
	m.sendTTSAnnouncement(message)
}

// sendRecoveryNotification sends a recovery notification via ntfy only
func (m *Manager) sendRecoveryNotification() {
	m.mu.Lock()
	// Check rate limiting
	if !m.lastRecoveryNotification.IsZero() && m.clock.Since(m.lastRecoveryNotification) < RecoveryNotificationCooldown {
		m.logger.Debug("Skipping recovery notification due to rate limiting",
			zap.Duration("since_last", m.clock.Since(m.lastRecoveryNotification)))
		m.mu.Unlock()
		return
	}
	m.lastRecoveryNotification = m.clock.Now()
	currentPower := m.currentPowerW
	m.mu.Unlock()

	message := fmt.Sprintf("Septic system returned to normal operation - power %.0fW", currentPower)

	// Record notification in shadow state
	m.shadowTracker.RecordNotification("recovery", message, "default")

	m.logger.Info("Septic system recovered", zap.Float64("power_w", currentPower))

	// Send ntfy notification (no TTS for recovery)
	if m.ntfyClient != nil {
		if err := m.ntfyClient.Send(&ntfy.Message{
			Title:    "Septic System Recovered",
			Body:     message,
			Priority: ntfy.PriorityDefault,
			Tags:     []string{"white_check_mark"},
		}); err != nil {
			m.logger.Error("Failed to send septic recovery notification", zap.Error(err))
		} else {
			m.logger.Info("Septic recovery notification sent", zap.String("message", message))
		}
	}
}

// sendTTSAnnouncement sends a TTS message via the shared notifier.
func (m *Manager) sendTTSAnnouncement(message string) {
	if m.readOnly {
		m.logger.Info("READ-ONLY: Would send TTS announcement", zap.String("message", message))
		return
	}

	if m.notifier == nil {
		return
	}

	if err := m.notifier.Speak(m.ctx, message, notify.Urgent, nil); err != nil {
		m.logger.Error("Failed to send TTS announcement", zap.Error(err), zap.String("message", message))
	} else {
		m.logger.Info("TTS announcement sent", zap.String("message", message))
		m.shadowTracker.RecordTTSAnnouncement(message)
	}
}

// handleThermostatChange processes changes to thermostat entities
func (m *Manager) handleThermostatChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	hvacAction, ok := newState.Attributes["hvac_action"].(string)
	if !ok {
		m.logger.Debug("Thermostat state change without hvac_action attribute",
			zap.String("entity_id", entityID),
			zap.String("state", newState.State))
		return
	}

	// Extract temperature info for shadow state
	currentTemp, _ := newState.Attributes["current_temperature"].(float64)
	targetTemp, _ := newState.Attributes["temperature"].(float64)

	m.logger.Debug("Thermostat state changed",
		zap.String("entity_id", entityID),
		zap.String("hvac_action", hvacAction),
		zap.Float64("current_temp", currentTemp),
		zap.Float64("target_temp", targetTemp))

	now := m.clock.Now()

	m.mu.Lock()
	switch entityID {
	case WellThermostatEntity:
		oldAction := m.wellHVACAction
		m.wellHVACAction = hvacAction

		// Update shadow state
		m.shadowTracker.UpdateWellThermostat(shadowstate.ThermostatState{
			EntityID:    entityID,
			HVACAction:  hvacAction,
			CurrentTemp: currentTemp,
			TargetTemp:  targetTemp,
			LastChanged: now,
			IsActive:    hvacAction == "heating",
		})
		m.mu.Unlock()

		// Notify when heating starts (temperature dropped below threshold)
		if hvacAction == "heating" && oldAction != "heating" {
			m.sendThermostatNotification("well_heating",
				"Well shed temperature dropped - heater light activated",
				currentTemp, targetTemp)
		}

	case BarnThermostatEntity:
		oldAction := m.barnHVACAction
		m.barnHVACAction = hvacAction

		// Update shadow state
		m.shadowTracker.UpdateBarnThermostat(shadowstate.ThermostatState{
			EntityID:    entityID,
			HVACAction:  hvacAction,
			CurrentTemp: currentTemp,
			TargetTemp:  targetTemp,
			LastChanged: now,
			IsActive:    hvacAction == "cooling",
		})
		m.mu.Unlock()

		// Notify on state changes between idle/cooling
		if hvacAction != oldAction {
			if hvacAction == "idle" && oldAction == "cooling" {
				// Dehumidifier cut off due to cold temperature
				m.sendThermostatNotification("barn_cutoff",
					"Barn temperature too cold - dehumidifier cut off",
					currentTemp, targetTemp)
			} else if hvacAction == "cooling" && oldAction == "idle" {
				// Dehumidifier resumed
				m.sendThermostatNotification("barn_resumed",
					"Barn temperature recovered - dehumidifier resumed",
					currentTemp, targetTemp)
			}
		}

	default:
		m.mu.Unlock()
	}
}

// sendThermostatNotification sends a thermostat notification via ntfy
func (m *Manager) sendThermostatNotification(alertType, message string, currentTemp, targetTemp float64) {
	fullMessage := fmt.Sprintf("%s (current: %.1f°F, target: %.1f°F)", message, currentTemp, targetTemp)

	m.logger.Info("Thermostat notification",
		zap.String("alert_type", alertType),
		zap.String("message", fullMessage))

	// Record notification in shadow state
	m.shadowTracker.RecordNotification(alertType, fullMessage, "default")

	// Send ntfy notification
	if m.ntfyClient != nil {
		priority := ntfy.PriorityDefault
		tags := []string{"thermometer"}

		// Use different tags based on alert type
		switch alertType {
		case "well_heating":
			tags = append(tags, "fire")
		case "barn_cutoff":
			tags = append(tags, "cold_face")
		case "barn_resumed":
			tags = append(tags, "white_check_mark")
		}

		if err := m.ntfyClient.Send(&ntfy.Message{
			Title:    "Thermostat Alert",
			Body:     fullMessage,
			Priority: priority,
			Tags:     tags,
		}); err != nil {
			m.logger.Error("Failed to send thermostat notification",
				zap.String("alert_type", alertType),
				zap.Error(err))
		} else {
			m.logger.Info("Thermostat notification sent", zap.String("message", fullMessage))
		}
	} else {
		m.logger.Warn("ntfy client not configured, cannot send thermostat notification")
	}
}

// ============================================================================
// Test Helpers - exported for testing only
// ============================================================================

// SimulatePowerReading simulates a power sensor reading for testing
func (m *Manager) SimulatePowerReading(powerW float64) {
	newState := &ha.State{
		EntityID: SepticSensorEntity,
		State:    fmt.Sprintf("%.1f", powerW),
	}
	m.handlePowerChange(SepticSensorEntity, nil, newState)
}

// SimulateThermostatChange simulates a thermostat state change for testing
func (m *Manager) SimulateThermostatChange(entityID, hvacAction string, currentTemp, targetTemp float64) {
	newState := &ha.State{
		EntityID: entityID,
		State:    "heat", // or "cool" depending on thermostat mode
		Attributes: map[string]interface{}{
			"hvac_action":         hvacAction,
			"current_temperature": currentTemp,
			"temperature":         targetTemp,
		},
	}
	m.handleThermostatChange(entityID, nil, newState)
}

// GetWellHVACAction returns the current well thermostat HVAC action for testing
func (m *Manager) GetWellHVACAction() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wellHVACAction
}

// GetBarnHVACAction returns the current barn thermostat HVAC action for testing
func (m *Manager) GetBarnHVACAction() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.barnHVACAction
}

// TriggerEvaluation triggers condition evaluation for testing
func (m *Manager) TriggerEvaluation() {
	m.evaluateConditions()
}

// GetCurrentPower returns the current power reading for testing
func (m *Manager) GetCurrentPower() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentPowerW
}

// IsAeratorFailure returns whether aerator failure is detected for testing
func (m *Manager) IsAeratorFailure() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isAeratorFailure
}

// IsPumpStuck returns whether pump stuck is detected for testing
func (m *Manager) IsPumpStuck() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isPumpStuck
}

// GetLowPowerStartTime returns when low power condition started for testing
func (m *Manager) GetLowPowerStartTime() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lowPowerStartTime
}

// GetHighPowerStartTime returns when high power condition started for testing
func (m *Manager) GetHighPowerStartTime() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.highPowerStartTime
}
