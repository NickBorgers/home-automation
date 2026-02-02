// Package waterflow provides monitoring for sustained high water flow to detect possible broken pipes.
package waterflow

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Configuration constants for water flow monitoring
const (
	// FlowSensorEntity is the Home Assistant entity for the water flow sensor
	FlowSensorEntity = "sensor.droplet_flow_rate"

	// WarningFlowRateGPM is the flow rate threshold for warning alerts (gallons per minute)
	WarningFlowRateGPM = 0.3

	// WarningDurationMinutes is how long flow must exceed warning threshold before alerting
	WarningDurationMinutes = 60

	// UrgentFlowRateGPM is the flow rate threshold for urgent alerts (gallons per minute)
	UrgentFlowRateGPM = 0.4

	// UrgentDurationMinutes is how long flow must exceed urgent threshold before alerting
	UrgentDurationMinutes = 30

	// AlertCheckInterval is how often to check for duration-based alerts
	AlertCheckInterval = 1 * time.Minute

	// RecoveryNotificationCooldown prevents recovery notification spam
	RecoveryNotificationCooldown = 30 * time.Minute

	// RepeatAlertCooldown prevents repeat alert spam
	RepeatAlertCooldown = 4 * time.Hour

	// RecoveryDebounceSeconds is how long flow must stay below threshold before declaring recovery
	// This prevents rapid alert/recovery flapping from sensor noise
	RecoveryDebounceSeconds = 30
)

// Manager handles water flow monitoring
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	clock        clock.Clock
	ntfyClient   ntfy.Notifier

	// Shadow state tracking
	shadowTracker *shadowstate.WaterFlowTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// State tracking
	mu                        sync.Mutex
	currentFlowRateGPM        float64
	warningThresholdStartTime time.Time // When flow exceeded WarningFlowRateGPM
	urgentThresholdStartTime  time.Time // When flow exceeded UrgentFlowRateGPM
	recoveryStartTime         time.Time // When flow first dropped below threshold (for debounce)
	lastAlertNotification     time.Time // Last time we sent an alert notification
	lastRecoveryNotification  time.Time // Last time we sent a recovery notification
	isWarningActive           bool      // Currently in warning alert state
	isUrgentActive            bool      // Currently in urgent alert state

	// Periodic checker
	stopChecker chan struct{}
}

// NewManager creates a new water flow manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier) *Manager {
	shadowTracker := shadowstate.NewWaterFlowTracker()

	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("waterflow"),
		readOnly:      readOnly,
		clock:         clock.NewRealClock(),
		ntfyClient:    ntfyClient,
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "waterflow", logger.Named("waterflow")),
	}
}

// NewManagerWithClock creates a new water flow manager with a custom clock (for testing)
func NewManagerWithClock(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier, c clock.Clock) *Manager {
	m := NewManager(ctx, haClient, stateManager, logger, readOnly, registry, ntfyClient)
	m.clock = c
	return m
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.WaterFlowShadowState {
	return m.shadowTracker.GetState()
}

// Start begins water flow monitoring
func (m *Manager) Start() error {
	m.logger.Info("Starting Water Flow Manager")

	// Subscribe to the water flow sensor
	if err := m.subHelper.SubscribeToEntity(FlowSensorEntity, m.handleFlowChange); err != nil {
		return fmt.Errorf("failed to subscribe to water flow sensor: %w", err)
	}

	// Capture initial inputs after subscriptions registered
	m.subHelper.CaptureInitialInputs()

	// Start periodic checker for duration-based alerts
	m.startPeriodicChecker()

	m.logger.Info("Water Flow Manager started successfully")
	return nil
}

// Stop stops the manager and cleans up
func (m *Manager) Stop() {
	m.logger.Info("Stopping Water Flow Manager")

	m.stopPeriodicCheckerFunc()
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Water Flow Manager stopped")
}

// Reset re-evaluates conditions and clears rate limiters
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Water Flow Manager")

	m.mu.Lock()
	m.lastAlertNotification = time.Time{}
	m.lastRecoveryNotification = time.Time{}
	m.mu.Unlock()

	// Re-evaluate current state
	m.evaluateConditions()

	m.logger.Info("Successfully reset Water Flow Manager")
	return nil
}

// handleFlowChange processes changes to the water flow sensor
func (m *Manager) handleFlowChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Parse the flow rate value
	flowRateGPM, err := strconv.ParseFloat(newState.State, 64)
	if err != nil {
		m.logger.Warn("Failed to parse water flow rate value",
			zap.String("state", newState.State),
			zap.Error(err))
		return
	}

	m.logger.Debug("Water flow rate changed",
		zap.Float64("flow_rate_gpm", flowRateGPM),
		zap.String("old_state", func() string {
			if oldState != nil {
				return oldState.State
			}
			return "nil"
		}()))

	now := m.clock.Now()

	m.mu.Lock()
	m.currentFlowRateGPM = flowRateGPM
	m.shadowTracker.UpdateFlowRate(flowRateGPM)

	// Update threshold tracking based on flow rate
	if flowRateGPM >= UrgentFlowRateGPM {
		// High flow - possible broken pipe
		if m.urgentThresholdStartTime.IsZero() {
			m.urgentThresholdStartTime = now
			m.shadowTracker.UpdateUrgentThresholdStart(&now)
			m.logger.Info("Urgent flow threshold exceeded, starting timer",
				zap.Float64("flow_rate_gpm", flowRateGPM))
		}
		// Also track warning threshold if not already set
		if m.warningThresholdStartTime.IsZero() {
			m.warningThresholdStartTime = now
			m.shadowTracker.UpdateWarningThresholdStart(&now)
		}
		// Clear recovery tracking since we're above threshold
		m.recoveryStartTime = time.Time{}
		m.shadowTracker.UpdateRecoveryStart(nil)
	} else if flowRateGPM >= WarningFlowRateGPM {
		// Moderate flow - possible forgotten fixture
		if m.warningThresholdStartTime.IsZero() {
			m.warningThresholdStartTime = now
			m.shadowTracker.UpdateWarningThresholdStart(&now)
			m.logger.Info("Warning flow threshold exceeded, starting timer",
				zap.Float64("flow_rate_gpm", flowRateGPM))
		}
		// Clear urgent tracking since we're below that threshold
		m.urgentThresholdStartTime = time.Time{}
		m.shadowTracker.UpdateUrgentThresholdStart(nil)
		// Clear recovery tracking since we're above warning threshold
		m.recoveryStartTime = time.Time{}
		m.shadowTracker.UpdateRecoveryStart(nil)
	} else {
		// Flow below thresholds - start or continue recovery debounce
		wasAlerting := m.isWarningActive || m.isUrgentActive

		// Clear threshold tracking
		m.warningThresholdStartTime = time.Time{}
		m.urgentThresholdStartTime = time.Time{}
		m.shadowTracker.UpdateWarningThresholdStart(nil)
		m.shadowTracker.UpdateUrgentThresholdStart(nil)

		// If we were alerting, implement recovery debounce
		if wasAlerting {
			// Start recovery timer if not already started
			if m.recoveryStartTime.IsZero() {
				m.recoveryStartTime = now
				m.shadowTracker.UpdateRecoveryStart(&now)
				m.logger.Info("Flow dropped below threshold, starting recovery debounce",
					zap.Float64("flow_rate_gpm", flowRateGPM))
			}

			// Check if debounce period has passed
			if now.Sub(m.recoveryStartTime) >= RecoveryDebounceSeconds*time.Second {
				m.logger.Info("Recovery debounce complete, declaring recovery",
					zap.Float64("flow_rate_gpm", flowRateGPM),
					zap.Duration("debounce_duration", now.Sub(m.recoveryStartTime)))
				m.isWarningActive = false
				m.isUrgentActive = false
				m.recoveryStartTime = time.Time{}
				m.shadowTracker.UpdateConditionsMet(false, false)
				m.shadowTracker.ClearAlerts()
				m.shadowTracker.UpdateAlertLevel("none")
				m.shadowTracker.UpdateRecoveryStart(nil)
				m.mu.Unlock()
				m.sendRecoveryNotification()
				return
			}
			// Still in debounce period - don't clear alert state yet
		} else {
			// Not alerting - just clear alert level
			m.shadowTracker.UpdateAlertLevel("none")
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

	// Check for urgent condition (> 0.4 GPM for > 30 minutes)
	if !m.urgentThresholdStartTime.IsZero() {
		duration := now.Sub(m.urgentThresholdStartTime)
		if duration >= UrgentDurationMinutes*time.Minute && !m.isUrgentActive {
			m.logger.Warn("Urgent water flow condition detected",
				zap.Float64("flow_rate_gpm", m.currentFlowRateGPM),
				zap.Duration("duration", duration))
			m.isUrgentActive = true
			m.shadowTracker.UpdateAlertLevel("urgent")
			m.shadowTracker.UpdateConditionsMet(true, true)

			alerts := []shadowstate.WaterFlowAlert{{
				AlertType:       "urgent",
				Message:         fmt.Sprintf("High water flow of %.2f GPM for over 30 minutes. Possible broken pipe!", m.currentFlowRateGPM),
				FlowRateGPM:     m.currentFlowRateGPM,
				DurationMinutes: int(duration.Minutes()),
				DetectedAt:      m.urgentThresholdStartTime,
			}}
			m.shadowTracker.UpdateActiveAlerts(alerts)

			// Send urgent notification with TTS
			m.sendAlertNotification("urgent",
				fmt.Sprintf("High water flow of %.2f GPM for over 30 minutes. Possible broken pipe!", m.currentFlowRateGPM),
				true)
			return // Don't also send warning if urgent
		}
	}

	// Check for warning condition (> 0.3 GPM for > 60 minutes)
	if !m.warningThresholdStartTime.IsZero() && !m.isUrgentActive {
		duration := now.Sub(m.warningThresholdStartTime)
		if duration >= WarningDurationMinutes*time.Minute && !m.isWarningActive {
			m.logger.Warn("Warning water flow condition detected",
				zap.Float64("flow_rate_gpm", m.currentFlowRateGPM),
				zap.Duration("duration", duration))
			m.isWarningActive = true
			m.shadowTracker.UpdateAlertLevel("warning")
			m.shadowTracker.UpdateConditionsMet(true, false)

			alerts := []shadowstate.WaterFlowAlert{{
				AlertType:       "warning",
				Message:         fmt.Sprintf("Continuous water flow of %.2f GPM for over 60 minutes. Check for running fixtures.", m.currentFlowRateGPM),
				FlowRateGPM:     m.currentFlowRateGPM,
				DurationMinutes: int(duration.Minutes()),
				DetectedAt:      m.warningThresholdStartTime,
			}}
			m.shadowTracker.UpdateActiveAlerts(alerts)

			// Send warning notification (no TTS)
			m.sendAlertNotification("warning",
				fmt.Sprintf("Continuous water flow of %.2f GPM for over 60 minutes. Check for running fixtures.", m.currentFlowRateGPM),
				false)
		}
	}
}

// sendAlertNotification sends an alert via ntfy and optionally TTS
func (m *Manager) sendAlertNotification(alertType, message string, sendTTS bool) {
	// Check rate limiting (must be called with lock held)
	if !m.lastAlertNotification.IsZero() && m.clock.Since(m.lastAlertNotification) < RepeatAlertCooldown {
		m.logger.Debug("Skipping alert notification due to rate limiting",
			zap.String("alert_type", alertType),
			zap.Duration("since_last", m.clock.Since(m.lastAlertNotification)))
		return
	}
	m.lastAlertNotification = m.clock.Now()

	// Determine priority based on alert type
	priority := ntfy.PriorityHigh
	title := "Water Flow Warning"
	tags := []string{"warning", "droplet"}

	if alertType == "urgent" {
		priority = ntfy.PriorityUrgent
		title = "Possible Pipe Break"
		tags = []string{"rotating_light", "droplet"}
	}

	// Record notification in shadow state
	priorityStr := "high"
	if alertType == "urgent" {
		priorityStr = "urgent"
	}
	m.shadowTracker.RecordNotification(alertType, message, priorityStr)

	// Release lock before doing I/O
	m.mu.Unlock()
	defer m.mu.Lock()

	// Send ntfy notification
	if m.ntfyClient != nil {
		if err := m.ntfyClient.Send(&ntfy.Message{
			Title:    title,
			Body:     message,
			Priority: priority,
			Tags:     tags,
		}); err != nil {
			m.logger.Error("Failed to send water flow alert notification",
				zap.String("alert_type", alertType),
				zap.Error(err))
		} else {
			m.logger.Info("Water flow alert notification sent", zap.String("message", message))
		}
	} else {
		m.logger.Warn("ntfy client not configured, cannot send water flow alert notification")
	}

	// Send TTS announcement for urgent alerts only
	if sendTTS {
		m.sendTTSAnnouncement("Attention: High water flow detected for over 30 minutes. Possible broken pipe. Please check immediately.")
	}
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
	currentFlow := m.currentFlowRateGPM
	m.mu.Unlock()

	message := fmt.Sprintf("Water flow has returned to normal levels (%.2f GPM)", currentFlow)

	// Record notification in shadow state
	m.shadowTracker.RecordRecoveryNotification(message)

	m.logger.Info("Water flow recovered", zap.Float64("flow_rate_gpm", currentFlow))

	// Send ntfy notification (no TTS for recovery)
	if m.ntfyClient != nil {
		if err := m.ntfyClient.Send(&ntfy.Message{
			Title:    "Water Flow Returned to Normal",
			Body:     message,
			Priority: ntfy.PriorityDefault,
			Tags:     []string{"white_check_mark", "droplet"},
		}); err != nil {
			m.logger.Error("Failed to send water flow recovery notification", zap.Error(err))
		} else {
			m.logger.Info("Water flow recovery notification sent", zap.String("message", message))
		}
	}
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
		"media_player.kids_bathroom",
	}

	if err := m.haClient.CallService(m.ctx, "tts", "speak", map[string]interface{}{
		"entity_id":              "tts.google_translate_en_com",
		"media_player_entity_id": speakers,
		"message":                message,
		"cache":                  true,
	}); err != nil {
		m.logger.Error("Failed to send TTS announcement", zap.Error(err), zap.String("message", message))
	} else {
		m.logger.Info("TTS announcement sent", zap.String("message", message))
		m.shadowTracker.RecordTTSAnnouncement()
	}
}

// ============================================================================
// Test Helpers - exported for testing only
// ============================================================================

// SimulateFlowReading simulates a flow sensor reading for testing
func (m *Manager) SimulateFlowReading(flowRateGPM float64) {
	newState := &ha.State{
		EntityID: FlowSensorEntity,
		State:    fmt.Sprintf("%.3f", flowRateGPM),
	}
	m.handleFlowChange(FlowSensorEntity, nil, newState)
}

// TriggerEvaluation triggers condition evaluation for testing
func (m *Manager) TriggerEvaluation() {
	m.evaluateConditions()
}

// GetCurrentFlowRate returns the current flow rate for testing
func (m *Manager) GetCurrentFlowRate() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentFlowRateGPM
}

// IsWarningActive returns whether warning alert is active for testing
func (m *Manager) IsWarningActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isWarningActive
}

// IsUrgentActive returns whether urgent alert is active for testing
func (m *Manager) IsUrgentActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isUrgentActive
}

// GetWarningThresholdStartTime returns when warning threshold was exceeded for testing
func (m *Manager) GetWarningThresholdStartTime() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.warningThresholdStartTime
}

// GetUrgentThresholdStartTime returns when urgent threshold was exceeded for testing
func (m *Manager) GetUrgentThresholdStartTime() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.urgentThresholdStartTime
}

// GetRecoveryStartTime returns when recovery debounce started for testing
func (m *Manager) GetRecoveryStartTime() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recoveryStartTime
}
