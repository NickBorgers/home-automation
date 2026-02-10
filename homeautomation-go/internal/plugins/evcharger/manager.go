// Package evcharger provides safety monitoring for the EV charger plug.
// It monitors for overheat, overcurrent, and overvoltage conditions and
// immediately turns off the plug when any condition is detected.
package evcharger

import (
	"context"
	"fmt"
	"sync"
	"time"

	"homeautomation/internal/ha"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Entity IDs for the Shelly Wave Plug US used as the EV (Leaf) charger plug.
// The "leaf_charger" prefix comes from the Home Assistant device name.
const (
	SwitchEntity      = "switch.leaf_charger"
	OverheatSensor    = "binary_sensor.leaf_charger_overheat_detected"
	OverCurrentSensor = "binary_sensor.leaf_charger_over_current_detected"
	OverVoltageSensor = "binary_sensor.leaf_charger_over_voltage_detected"
	PowerSensor       = "sensor.leaf_charger_electric_consumption_w"

	// NotificationCooldown prevents notification spam
	NotificationCooldown = 5 * time.Minute

	// shutoffMaxRetries is the number of attempts for emergency shutoff service calls.
	// Retries protect against transient WebSocket errors during safety-critical operations.
	shutoffMaxRetries = 3
	// shutoffRetryDelay is the delay between emergency shutoff retry attempts.
	shutoffRetryDelay = 1 * time.Second
)

// Manager handles EV charger safety monitoring
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	ntfyClient   ntfy.Notifier

	// Shadow state tracking
	shadowTracker *shadowstate.EVChargerTracker
	subHelper     *shadowstate.SubscriptionHelper

	// Rate limiting for notifications
	mu                   sync.Mutex
	lastNotificationTime time.Time
}

// NewManager creates a new EV charger safety manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier) *Manager {
	shadowTracker := shadowstate.NewEVChargerTracker()

	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("evcharger"),
		readOnly:      readOnly,
		ntfyClient:    ntfyClient,
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "evcharger", logger.Named("evcharger")),
	}
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.EVChargerShadowState {
	return m.shadowTracker.GetState()
}

// Start begins EV charger safety monitoring
func (m *Manager) Start() error {
	m.logger.Info("Starting EV Charger Safety Manager")

	// Subscribe to safety sensors
	if err := m.subHelper.SubscribeToEntity(OverheatSensor, m.handleOverheat); err != nil {
		return fmt.Errorf("failed to subscribe to overheat sensor: %w", err)
	}

	if err := m.subHelper.SubscribeToEntity(OverCurrentSensor, m.handleOverCurrent); err != nil {
		return fmt.Errorf("failed to subscribe to overcurrent sensor: %w", err)
	}

	if err := m.subHelper.SubscribeToEntity(OverVoltageSensor, m.handleOverVoltage); err != nil {
		return fmt.Errorf("failed to subscribe to overvoltage sensor: %w", err)
	}

	// Subscribe to power sensors for monitoring (informational)
	if err := m.subHelper.SubscribeToEntity(PowerSensor, m.handlePowerChange); err != nil {
		return fmt.Errorf("failed to subscribe to power sensor: %w", err)
	}

	if err := m.subHelper.SubscribeToEntity(SwitchEntity, m.handleSwitchChange); err != nil {
		return fmt.Errorf("failed to subscribe to switch: %w", err)
	}

	// Capture initial inputs
	m.subHelper.CaptureInitialInputs()

	// Check current state of safety sensors
	m.checkInitialSafetyState()

	m.logger.Info("EV Charger Safety Manager started successfully")
	return nil
}

// checkInitialSafetyState checks if any safety condition is already active
func (m *Manager) checkInitialSafetyState() {
	sensors := []string{OverheatSensor, OverCurrentSensor, OverVoltageSensor}
	for _, sensor := range sensors {
		state, err := m.haClient.GetState(sensor)
		if err != nil {
			m.logger.Warn("Failed to get initial state", zap.String("sensor", sensor), zap.Error(err))
			continue
		}
		if state.State == "on" {
			m.logger.Warn("Safety condition already active on startup!",
				zap.String("sensor", sensor))
			m.handleSafetyCondition(sensor, "active on startup")
		}
	}
}

// Stop stops the manager and cleans up
func (m *Manager) Stop() {
	m.logger.Info("Stopping EV Charger Safety Manager")
	m.subHelper.UnsubscribeAll()
	m.logger.Info("EV Charger Safety Manager stopped")
}

// Reset re-evaluates conditions and clears rate limiters
func (m *Manager) Reset() error {
	m.logger.Info("Resetting EV Charger Safety Manager")

	m.mu.Lock()
	m.lastNotificationTime = time.Time{}
	m.mu.Unlock()

	// Re-check safety sensors
	m.checkInitialSafetyState()

	m.logger.Info("Successfully reset EV Charger Safety Manager")
	return nil
}

// handleOverheat handles overheat sensor state changes
func (m *Manager) handleOverheat(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	m.shadowTracker.UpdateOverheatState(newState.State == "on")

	if newState.State == "on" {
		m.logger.Error("OVERHEAT DETECTED on EV charger plug!",
			zap.String("entity", entityID))
		m.handleSafetyCondition(entityID, "overheat")
	} else if oldState != nil && oldState.State == "on" {
		m.logger.Info("Overheat condition cleared", zap.String("entity", entityID))
		m.shadowTracker.RecordRecovery("overheat")
	}
}

// handleOverCurrent handles overcurrent sensor state changes
func (m *Manager) handleOverCurrent(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	m.shadowTracker.UpdateOverCurrentState(newState.State == "on")

	if newState.State == "on" {
		m.logger.Error("OVER-CURRENT DETECTED on EV charger plug!",
			zap.String("entity", entityID))
		m.handleSafetyCondition(entityID, "over-current")
	} else if oldState != nil && oldState.State == "on" {
		m.logger.Info("Over-current condition cleared", zap.String("entity", entityID))
		m.shadowTracker.RecordRecovery("over-current")
	}
}

// handleOverVoltage handles overvoltage sensor state changes
func (m *Manager) handleOverVoltage(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	m.shadowTracker.UpdateOverVoltageState(newState.State == "on")

	if newState.State == "on" {
		m.logger.Error("OVER-VOLTAGE DETECTED on EV charger plug!",
			zap.String("entity", entityID))
		m.handleSafetyCondition(entityID, "over-voltage")
	} else if oldState != nil && oldState.State == "on" {
		m.logger.Info("Over-voltage condition cleared", zap.String("entity", entityID))
		m.shadowTracker.RecordRecovery("over-voltage")
	}
}

// handlePowerChange handles power consumption changes (informational)
func (m *Manager) handlePowerChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}
	m.shadowTracker.UpdatePowerReading(newState.State)
}

// handleSwitchChange handles switch state changes
func (m *Manager) handleSwitchChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}
	m.shadowTracker.UpdateSwitchState(newState.State == "on")
}

// handleSafetyCondition is called when any safety condition is detected.
// If multiple conditions fire simultaneously (e.g., overheat + overcurrent), this method
// may be called concurrently. This is safe by design: the shutoff call is idempotent
// (turning off an already-off switch is a no-op), and the notification rate limiter
// will suppress duplicate notifications within the cooldown window.
func (m *Manager) handleSafetyCondition(sensor, conditionType string) {
	// IMMEDIATELY turn off the switch - this is safety critical
	m.emergencyShutoff(conditionType, sensor)

	// Record the event
	m.shadowTracker.RecordSafetyEvent(conditionType, sensor)

	// Send notifications (rate limited)
	m.sendAlertNotifications(conditionType, sensor)
}

// emergencyShutoff turns off the EV charger switch immediately.
// It retries up to shutoffMaxRetries times with shutoffRetryDelay between attempts
// to handle transient WebSocket or network errors during this safety-critical operation.
func (m *Manager) emergencyShutoff(conditionType, sensor string) {
	m.logger.Warn("EMERGENCY SHUTOFF - Turning off EV charger due to safety condition",
		zap.String("condition", conditionType),
		zap.String("sensor", sensor))

	if m.readOnly {
		m.logger.Warn("READ-ONLY: Would turn off EV charger switch",
			zap.String("condition", conditionType))
		return
	}

	var lastErr error
	for attempt := 1; attempt <= shutoffMaxRetries; attempt++ {
		if err := m.haClient.CallService(m.ctx, "switch", "turn_off", map[string]interface{}{
			"entity_id": SwitchEntity,
		}); err != nil {
			lastErr = err
			m.logger.Error("Failed to turn off EV charger (will retry)",
				zap.String("condition", conditionType),
				zap.Int("attempt", attempt),
				zap.Int("max_attempts", shutoffMaxRetries),
				zap.Error(err))
			if attempt < shutoffMaxRetries {
				time.Sleep(shutoffRetryDelay)
			}
			continue
		}
		// Success
		if attempt > 1 {
			m.logger.Info("EV charger successfully turned off after retry",
				zap.String("condition", conditionType),
				zap.Int("attempt", attempt))
		} else {
			m.logger.Info("EV charger successfully turned off",
				zap.String("condition", conditionType))
		}
		m.shadowTracker.RecordShutoff(conditionType)
		return
	}

	// All retries exhausted
	m.logger.Error("CRITICAL: All attempts to turn off EV charger failed!",
		zap.String("condition", conditionType),
		zap.Int("attempts", shutoffMaxRetries),
		zap.Error(lastErr))
}

// sendAlertNotifications sends urgent notifications about the safety event
func (m *Manager) sendAlertNotifications(conditionType, sensor string) {
	m.mu.Lock()
	if time.Since(m.lastNotificationTime) < NotificationCooldown {
		m.logger.Debug("Skipping notification due to rate limiting",
			zap.Duration("since_last", time.Since(m.lastNotificationTime)))
		m.mu.Unlock()
		return
	}
	m.lastNotificationTime = time.Now()
	m.mu.Unlock()

	message := fmt.Sprintf("EV Charger %s detected! Plug has been automatically turned off for safety.", conditionType)

	// Send ntfy notification (urgent priority)
	if m.ntfyClient != nil {
		if err := m.ntfyClient.Send(&ntfy.Message{
			Title:    "EV Charger Safety Alert",
			Body:     message,
			Priority: ntfy.PriorityUrgent,
			Tags:     []string{"rotating_light", "electric_plug", "warning"},
		}); err != nil {
			m.logger.Error("Failed to send ntfy notification", zap.Error(err))
		} else {
			m.logger.Info("Safety alert notification sent", zap.String("condition", conditionType))
			m.shadowTracker.RecordNotification(conditionType, message)
		}
	} else {
		m.logger.Warn("ntfy client not configured, cannot send safety notification")
	}

	// Send TTS announcement
	m.sendTTSAnnouncement(fmt.Sprintf("Warning: EV charger %s detected. The charger has been automatically turned off.", conditionType))
}

// sendTTSAnnouncement sends a TTS message to Sonos speakers
func (m *Manager) sendTTSAnnouncement(message string) {
	if m.readOnly {
		m.logger.Info("READ-ONLY: Would send TTS announcement", zap.String("message", message))
		return
	}

	speakers := []string{
		"media_player.bedroom",
		"media_player.kitchen",
		"media_player.dining_room",
		"media_player.soundbar",
	}

	if err := m.haClient.CallService(m.ctx, "tts", "speak", map[string]interface{}{
		"entity_id":              "tts.google_translate_en_com",
		"media_player_entity_id": speakers,
		"message":                message,
		"cache":                  true,
	}); err != nil {
		m.logger.Error("Failed to send TTS announcement", zap.Error(err))
	} else {
		m.logger.Info("TTS announcement sent", zap.String("message", message))
		m.shadowTracker.RecordTTSAnnouncement()
	}
}
