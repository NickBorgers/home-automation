// Package waterflow provides monitoring for sustained high water flow to detect possible broken pipes.
package waterflow

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"homeautomation/internal/alert"
	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/notify"
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

	// WarningDurationMinutes is the rolling window size for warning alerts
	WarningDurationMinutes = 60

	// WarningFlowDurationMinutes is the cumulative flow duration in the warning window that must
	// exceed WarningFlowRateGPM before alerting. Handles event-driven sensors that are silent at idle.
	WarningFlowDurationMinutes = 15

	// UrgentFlowRateGPM is the flow rate threshold for urgent alerts (gallons per minute)
	UrgentFlowRateGPM = 0.4

	// UrgentDurationMinutes is the rolling window size for urgent alerts
	UrgentDurationMinutes = 30

	// UrgentFlowDurationMinutes is the cumulative flow duration in the urgent window that must
	// exceed UrgentFlowRateGPM before alerting. Handles event-driven sensors that are silent at idle.
	UrgentFlowDurationMinutes = 20

	// maxReadingAgeMinutes is how long to keep readings in the rolling window buffer.
	// Must cover the largest alert window so evaluations have enough history.
	maxReadingAgeMinutes = WarningDurationMinutes * 2

	// AlertCheckInterval is how often to check for duration-based alerts
	AlertCheckInterval = 1 * time.Minute

	// maxFlowReadingDuration caps how much time one event-driven reading can represent. Droplet
	// reports frequently while flowing but is silent at idle; the cap prevents a final high reading
	// before an idle gap from being counted as sustained flow. One minute matches the expected
	// inter-reading interval during active flow and is independent of AlertCheckInterval.
	maxFlowReadingDuration = 1 * time.Minute

	// RecoveryNotificationCooldown prevents recovery notification spam
	RecoveryNotificationCooldown = 30 * time.Minute

	// RepeatAlertCooldown prevents repeat alert spam
	RepeatAlertCooldown = 4 * time.Hour

	// RecoveryDebounceSeconds is how long flow must stay below threshold before declaring recovery
	// This prevents rapid alert/recovery flapping from sensor noise
	RecoveryDebounceSeconds = 30
)

// flowReading is a timestamped flow sensor sample stored in the rolling window buffer.
type flowReading struct {
	at  time.Time
	gpm float64
}

// Manager handles water flow monitoring
type Manager struct {
	ctx          context.Context
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	clock        clock.Clock
	alerter      alert.Alerter

	// Shadow state tracking
	shadowTracker *shadowstate.WaterFlowTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// State tracking
	mu                       sync.Mutex
	currentFlowRateGPM       float64
	flowReadings             []flowReading // Rolling window buffer of timestamped sensor readings
	recoveryStartTime        time.Time     // When flow first dropped below threshold (for debounce)
	lastAlertNotification    time.Time     // Last time we sent an alert notification
	lastAlertType            string        // Last alert notification type for escalation handling
	lastRecoveryNotification time.Time     // Last time we sent a recovery notification
	isWarningActive          bool          // Currently in warning alert state
	isUrgentActive           bool          // Currently in urgent alert state

	// Periodic checker
	stopChecker chan struct{}
}

// NewManager creates a new water flow manager
func NewManager(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, alerter alert.Alerter) *Manager {
	shadowTracker := shadowstate.NewWaterFlowTracker()

	return &Manager{
		ctx:           ctx,
		haClient:      haClient,
		stateManager:  stateManager,
		logger:        logger.Named("waterflow"),
		readOnly:      readOnly,
		clock:         clock.NewRealClock(),
		alerter:       alerter,
		shadowTracker: shadowTracker,
		subHelper:     shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "waterflow", logger.Named("waterflow")),
	}
}

// NewManagerWithClock creates a new water flow manager with a custom clock (for testing)
func NewManagerWithClock(ctx context.Context, haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, alerter alert.Alerter, c clock.Clock) *Manager {
	m := NewManager(ctx, haClient, stateManager, logger, readOnly, registry, alerter)
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

// Reset re-evaluates conditions and clears rate limiters.
// flowReadings is intentionally not cleared so that a reset mid-flow immediately re-fires any
// in-progress alert once the rate limiter is lifted.
func (m *Manager) Reset() error {
	m.logger.Info("Resetting Water Flow Manager")

	m.mu.Lock()
	m.lastAlertNotification = time.Time{}
	m.lastAlertType = ""
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

	// Append reading to the rolling window buffer. Alert evaluation happens in evaluateConditions.
	m.flowReadings = append(m.flowReadings, flowReading{at: now, gpm: flowRateGPM})

	wasAlerting := m.isWarningActive || m.isUrgentActive

	if flowRateGPM >= WarningFlowRateGPM {
		// Flow above threshold — clear any pending recovery
		m.recoveryStartTime = time.Time{}
		m.shadowTracker.UpdateRecoveryStart(nil)
	} else if wasAlerting {
		// Flow below threshold while alert is active — handle recovery debounce
		if m.recoveryStartTime.IsZero() {
			m.recoveryStartTime = now
			m.shadowTracker.UpdateRecoveryStart(&now)
			m.logger.Info("Flow dropped below threshold, starting recovery debounce",
				zap.Float64("flow_rate_gpm", flowRateGPM))
		}

		if now.Sub(m.recoveryStartTime) >= RecoveryDebounceSeconds*time.Second {
			m.logger.Info("Recovery debounce complete, declaring recovery",
				zap.Float64("flow_rate_gpm", flowRateGPM),
				zap.Duration("debounce_duration", now.Sub(m.recoveryStartTime)))
			m.isWarningActive = false
			m.isUrgentActive = false
			m.lastAlertType = ""
			// Clear the rolling window so the next evaluateConditions tick cannot immediately
			// re-fire the alert using stale high-flow readings.
			m.flowReadings = nil
			m.recoveryStartTime = time.Time{}
			m.shadowTracker.UpdateConditionsMet(false, false)
			m.shadowTracker.ClearAlerts()
			m.shadowTracker.UpdateAlertLevel("none")
			m.shadowTracker.UpdateRecoveryStart(nil)
			m.mu.Unlock()
			m.sendRecoveryNotification()
			return
		}
		// Still within debounce period — leave alert state unchanged
	} else {
		// Not alerting and below threshold — ensure alert level is cleared
		m.shadowTracker.UpdateAlertLevel("none")
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

// evaluateConditions checks if any alert thresholds have been exceeded using a rolling window.
// It fires an alert when flow above UrgentFlowRateGPM exceeds UrgentFlowDurationMinutes in the
// last UrgentDurationMinutes, or flow above WarningFlowRateGPM exceeds WarningFlowDurationMinutes
// in the last WarningDurationMinutes. Each reading represents the capped interval until the next
// reading so event-driven idle silence does not bias the rolling window.
func (m *Manager) evaluateConditions() {
	now := m.clock.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Prune readings older than the buffer retention period
	cutoff := now.Add(-maxReadingAgeMinutes * time.Minute)
	i := 0
	for i < len(m.flowReadings) && m.flowReadings[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		m.flowReadings = m.flowReadings[i:]
	}

	if len(m.flowReadings) == 0 {
		return
	}

	urgentWindowStart := now.Add(-UrgentDurationMinutes * time.Minute)
	if !m.isUrgentActive {
		flowDuration := m.flowDurationAboveThreshold(urgentWindowStart, now, UrgentFlowRateGPM)
		if flowDuration > UrgentFlowDurationMinutes*time.Minute {
			m.logger.Warn("Urgent water flow condition detected",
				zap.Float64("flow_rate_gpm", m.currentFlowRateGPM),
				zap.Duration("flow_duration", flowDuration),
				zap.Int("window_minutes", UrgentDurationMinutes))
			m.isUrgentActive = true
			m.shadowTracker.UpdateAlertLevel("urgent")
			m.shadowTracker.UpdateConditionsMet(true, true)

			// DetectedAt is the start of the evaluation window, not the precise onset of high flow.
			alerts := []shadowstate.WaterFlowAlert{{
				AlertType:       "urgent",
				Message:         fmt.Sprintf("High water flow of %.2f GPM for over 20 minutes. Possible broken pipe!", m.currentFlowRateGPM),
				FlowRateGPM:     m.currentFlowRateGPM,
				DurationMinutes: UrgentFlowDurationMinutes,
				DetectedAt:      urgentWindowStart,
			}}
			m.shadowTracker.UpdateActiveAlerts(alerts)

			m.sendAlertNotification("urgent",
				fmt.Sprintf("High water flow of %.2f GPM for over 20 minutes. Possible broken pipe!", m.currentFlowRateGPM))
			return // Don't also send warning if urgent
		}
	}

	warningWindowStart := now.Add(-WarningDurationMinutes * time.Minute)
	if !m.isWarningActive && !m.isUrgentActive {
		flowDuration := m.flowDurationAboveThreshold(warningWindowStart, now, WarningFlowRateGPM)
		if flowDuration > WarningFlowDurationMinutes*time.Minute {
			m.logger.Warn("Warning water flow condition detected",
				zap.Float64("flow_rate_gpm", m.currentFlowRateGPM),
				zap.Duration("flow_duration", flowDuration),
				zap.Int("window_minutes", WarningDurationMinutes))
			m.isWarningActive = true
			m.shadowTracker.UpdateAlertLevel("warning")
			m.shadowTracker.UpdateConditionsMet(true, false)

			alerts := []shadowstate.WaterFlowAlert{{
				AlertType:       "warning",
				Message:         fmt.Sprintf("Water flow of %.2f GPM for over 15 minutes. Check for running fixtures.", m.currentFlowRateGPM),
				FlowRateGPM:     m.currentFlowRateGPM,
				DurationMinutes: WarningFlowDurationMinutes,
				DetectedAt:      warningWindowStart,
			}}
			m.shadowTracker.UpdateActiveAlerts(alerts)

			m.sendAlertNotification("warning",
				fmt.Sprintf("Water flow of %.2f GPM for over 15 minutes. Check for running fixtures.", m.currentFlowRateGPM))
		}
	}
}

// flowDurationAboveThreshold returns the capped time represented by readings above threshold.
// m.mu must be held by the caller.
func (m *Manager) flowDurationAboveThreshold(windowStart, now time.Time, threshold float64) time.Duration {
	var flowDuration time.Duration
	for i, r := range m.flowReadings {
		if r.gpm < threshold {
			continue
		}

		intervalStart := r.at
		if intervalStart.Before(windowStart) {
			intervalStart = windowStart
		}
		if !intervalStart.Before(now) {
			continue
		}

		// Readings are always appended in chronological order, so m.flowReadings[i+1].at
		// is guaranteed to be >= r.at and provides the natural interval end for reading i.
		intervalEnd := now
		if i+1 < len(m.flowReadings) && m.flowReadings[i+1].at.Before(intervalEnd) {
			intervalEnd = m.flowReadings[i+1].at
		}
		maxEnd := r.at.Add(maxFlowReadingDuration)
		if intervalEnd.After(maxEnd) {
			intervalEnd = maxEnd
		}
		if intervalEnd.After(now) {
			intervalEnd = now
		}
		if intervalEnd.After(intervalStart) {
			flowDuration += intervalEnd.Sub(intervalStart)
		}
	}
	return flowDuration
}

// sendAlertNotification sends an alert via push and TTS channels.
func (m *Manager) sendAlertNotification(alertType, message string) {
	// Check rate limiting (must be called with lock held)
	isEscalation := alertType == "urgent" && m.lastAlertType != "urgent"
	if !isEscalation && !m.lastAlertNotification.IsZero() && m.clock.Since(m.lastAlertNotification) < RepeatAlertCooldown {
		m.logger.Debug("Skipping alert notification due to rate limiting",
			zap.String("alert_type", alertType),
			zap.Duration("since_last", m.clock.Since(m.lastAlertNotification)))
		return
	}
	m.lastAlertNotification = m.clock.Now()
	m.lastAlertType = alertType

	urgency := notify.UrgencyDeferable
	priority := ntfy.PriorityHigh
	title := "Water Flow Warning"
	tags := []string{"warning", "droplet"}
	ttsMessage := message

	if alertType == "urgent" {
		urgency = notify.UrgencyUrgent
		priority = ntfy.PriorityUrgent
		title = "Possible Pipe Break"
		tags = []string{"rotating_light", "droplet"}
		ttsMessage = "Attention: High water flow detected for over 20 minutes. Possible broken pipe. Please check immediately."
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

	if m.alerter != nil {
		speakers := []string{
			"media_player.bedroom",
			"media_player.kitchen",
			"media_player.dining_room",
			"media_player.kids_bathroom",
		}
		if err := m.alerter.Send(m.ctx, alert.Alert{
			Title:    title,
			Body:     ttsMessage,
			Urgency:  urgency,
			Tags:     tags,
			Speakers: speakers,
			Priority: priority,
		}); err != nil {
			m.logger.Error("Failed to send water flow alert notification",
				zap.String("alert_type", alertType),
				zap.Error(err))
		} else {
			m.logger.Info("Water flow alert notification sent", zap.String("message", message))
		}
	}

	if alertType == "urgent" {
		m.shadowTracker.RecordTTSAnnouncement()
	}
}

// sendRecoveryNotification sends a recovery notification via push and TTS channels.
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

	if m.alerter != nil {
		if err := m.alerter.Send(m.ctx, alert.Alert{
			Title:    "Water Flow Returned to Normal",
			Body:     message,
			Urgency:  notify.UrgencyDeferable,
			Tags:     []string{"white_check_mark", "droplet"},
			Priority: ntfy.PriorityDefault,
		}); err != nil {
			m.logger.Error("Failed to send water flow recovery notification", zap.Error(err))
		} else {
			m.logger.Info("Water flow recovery notification sent", zap.String("message", message))
		}
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

// GetRecoveryStartTime returns when recovery debounce started for testing
func (m *Manager) GetRecoveryStartTime() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recoveryStartTime
}
