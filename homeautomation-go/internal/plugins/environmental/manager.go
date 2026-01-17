package environmental

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Attic humidity thresholds (RH percentage)
const (
	// Warning threshold - approaching mold risk
	AtticHumidityWarningThreshold = 55.0
	// Critical threshold - definite mold risk
	AtticHumidityCriticalThreshold = 65.0
	// Hysteresis: warning clears when humidity drops to this level
	AtticHumidityWarningClear = 50.0
	// Hysteresis: critical clears when humidity drops to this level
	AtticHumidityCriticalClear = 60.0

	// Debounce: condition must be sustained this long before alerting
	SustainedConditionDuration = 30 * time.Minute

	// Rate limiting
	WarningNotificationRateLimit  = 4 * time.Hour
	CriticalNotificationRateLimit = 1 * time.Hour

	// Sensor entity IDs
	AtticHighHumiditySensor = "sensor.attic_high_humidity_2"
	AtticLowHumiditySensor  = "sensor.attic_low_humidity_2"

	// Notification target
	NotificationTarget = "nicks_iphone"
)

// Manager handles environmental monitoring and alerts
type Manager struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	clock        clock.Clock

	// Shadow state tracking
	shadowTracker *shadowstate.EnvironmentalTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Attic humidity state tracking
	mu                sync.Mutex
	atticHighHumidity float64
	atticLowHumidity  float64
	highSensorValid   bool
	lowSensorValid    bool

	// Sustained condition tracking
	// Track when each sensor first exceeded the threshold
	highWarningStart  time.Time
	lowWarningStart   time.Time
	highCriticalStart time.Time
	lowCriticalStart  time.Time

	// Current alert level ("none", "warning", "critical")
	currentAlertLevel string

	// Rate limiting
	lastWarningNotification    time.Time
	lastCriticalNotification   time.Time
	lastResolutionNotification time.Time

	// Track if we've notified for the current incident (to send resolution only once)
	hasNotifiedForCurrentIncident bool
}

// NewManager creates a new environmental monitoring manager
func NewManager(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry) *Manager {
	shadowTracker := shadowstate.NewEnvironmentalTracker()

	return &Manager{
		haClient:          haClient,
		stateManager:      stateManager,
		logger:            logger.Named("environmental"),
		readOnly:          readOnly,
		clock:             clock.NewRealClock(),
		shadowTracker:     shadowTracker,
		subHelper:         shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "environmental", logger.Named("environmental")),
		currentAlertLevel: "none",
	}
}

// NewManagerWithClock creates a new environmental monitoring manager with a custom clock (for testing)
func NewManagerWithClock(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, c clock.Clock) *Manager {
	m := NewManager(haClient, stateManager, logger, readOnly, registry)
	m.clock = c
	return m
}

// GetShadowState returns the current shadow state
func (m *Manager) GetShadowState() *shadowstate.EnvironmentalShadowState {
	return m.shadowTracker.GetState()
}

// Start begins environmental monitoring
func (m *Manager) Start() error {
	m.logger.Info("Starting Environmental Monitoring Manager")

	// Subscribe to attic humidity sensors (shadow inputs captured automatically)
	if err := m.subHelper.SubscribeToEntity(AtticHighHumiditySensor, m.handleAtticHighHumidityChange); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", AtticHighHumiditySensor, err)
	}

	if err := m.subHelper.SubscribeToEntity(AtticLowHumiditySensor, m.handleAtticLowHumidityChange); err != nil {
		return fmt.Errorf("failed to subscribe to %s: %w", AtticLowHumiditySensor, err)
	}

	// Initialize shadow state with current input values (after subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	// Initialize current states
	m.logger.Info("Initializing environmental states from current HA entities")
	if err := m.initializeStates(); err != nil {
		m.logger.Warn("Failed to initialize some environmental states", zap.Error(err))
	}

	m.logger.Info("Environmental Monitoring Manager started successfully")
	return nil
}

// Stop stops the environmental monitoring manager
func (m *Manager) Stop() {
	m.logger.Info("Stopping Environmental Monitoring Manager")

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Environmental Monitoring Manager stopped")
}

// initializeStates fetches current HA entity states and initializes monitoring
func (m *Manager) initializeStates() error {
	var errs []error

	// Get high sensor state
	highState, err := m.haClient.GetState(AtticHighHumiditySensor)
	if err == nil && highState != nil {
		m.handleAtticHighHumidityChange(AtticHighHumiditySensor, nil, highState)
	} else if err != nil {
		m.logger.Warn("Failed to get initial attic high humidity state", zap.Error(err))
		errs = append(errs, err)
	}

	// Get low sensor state
	lowState, err := m.haClient.GetState(AtticLowHumiditySensor)
	if err == nil && lowState != nil {
		m.handleAtticLowHumidityChange(AtticLowHumiditySensor, nil, lowState)
	} else if err != nil {
		m.logger.Warn("Failed to get initial attic low humidity state", zap.Error(err))
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// handleAtticHighHumidityChange processes changes to the high attic humidity sensor
func (m *Manager) handleAtticHighHumidityChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Parse humidity value
	humidity, err := parseHumidity(newState.State)
	if err != nil {
		m.logger.Debug("Invalid humidity value from high sensor",
			zap.String("entity_id", entityID),
			zap.String("state", newState.State),
			zap.Error(err))
		m.mu.Lock()
		m.highSensorValid = false
		m.mu.Unlock()
		return
	}

	m.logger.Debug("Attic high humidity sensor changed",
		zap.String("entity_id", entityID),
		zap.Float64("humidity", humidity))

	m.mu.Lock()
	m.atticHighHumidity = humidity
	m.highSensorValid = true
	m.mu.Unlock()

	// Update shadow state
	m.shadowTracker.UpdateAtticHumidity(humidity, m.atticLowHumidity)

	// Evaluate conditions
	m.evaluateAtticHumidity()
}

// handleAtticLowHumidityChange processes changes to the low attic humidity sensor
func (m *Manager) handleAtticLowHumidityChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Parse humidity value
	humidity, err := parseHumidity(newState.State)
	if err != nil {
		m.logger.Debug("Invalid humidity value from low sensor",
			zap.String("entity_id", entityID),
			zap.String("state", newState.State),
			zap.Error(err))
		m.mu.Lock()
		m.lowSensorValid = false
		m.mu.Unlock()
		return
	}

	m.logger.Debug("Attic low humidity sensor changed",
		zap.String("entity_id", entityID),
		zap.Float64("humidity", humidity))

	m.mu.Lock()
	m.atticLowHumidity = humidity
	m.lowSensorValid = true
	m.mu.Unlock()

	// Update shadow state
	m.shadowTracker.UpdateAtticHumidity(m.atticHighHumidity, humidity)

	// Evaluate conditions
	m.evaluateAtticHumidity()
}

// evaluateAtticHumidity evaluates current humidity levels and sends alerts if needed
func (m *Manager) evaluateAtticHumidity() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock.Now()

	// Determine which sensors are elevated
	highIsWarning := m.highSensorValid && m.atticHighHumidity >= AtticHumidityWarningThreshold
	highIsCritical := m.highSensorValid && m.atticHighHumidity >= AtticHumidityCriticalThreshold
	lowIsWarning := m.lowSensorValid && m.atticLowHumidity >= AtticHumidityWarningThreshold
	lowIsCritical := m.lowSensorValid && m.atticLowHumidity >= AtticHumidityCriticalThreshold

	// Track when conditions started for each sensor
	m.updateConditionTracking(now, highIsWarning, highIsCritical, lowIsWarning, lowIsCritical)

	// Calculate the highest sustained level
	newAlertLevel := m.calculateSustainedAlertLevel(now)

	// Check for condition resolution (hysteresis)
	if m.currentAlertLevel != "none" && newAlertLevel == "none" {
		m.checkConditionResolved(now)
		return
	}

	// Update current alert level
	previousLevel := m.currentAlertLevel
	m.currentAlertLevel = newAlertLevel

	// Update shadow state
	conditionStart := m.getConditionStartTime()
	isSustained := newAlertLevel != "none"
	m.shadowTracker.UpdateAlertLevel(newAlertLevel, conditionStart, isSustained)

	// Send notification if level increased and is sustained
	if newAlertLevel != "none" && (newAlertLevel != previousLevel || !m.hasNotifiedForCurrentIncident) {
		m.sendAlertNotification(now, newAlertLevel)
	}
}

// updateConditionTracking updates the start times for warning/critical conditions
func (m *Manager) updateConditionTracking(now time.Time, highIsWarning, highIsCritical, lowIsWarning, lowIsCritical bool) {
	// High sensor warning tracking
	if highIsWarning {
		if m.highWarningStart.IsZero() {
			m.highWarningStart = now
		}
	} else {
		m.highWarningStart = time.Time{}
	}

	// High sensor critical tracking
	if highIsCritical {
		if m.highCriticalStart.IsZero() {
			m.highCriticalStart = now
		}
	} else {
		m.highCriticalStart = time.Time{}
	}

	// Low sensor warning tracking
	if lowIsWarning {
		if m.lowWarningStart.IsZero() {
			m.lowWarningStart = now
		}
	} else {
		m.lowWarningStart = time.Time{}
	}

	// Low sensor critical tracking
	if lowIsCritical {
		if m.lowCriticalStart.IsZero() {
			m.lowCriticalStart = now
		}
	} else {
		m.lowCriticalStart = time.Time{}
	}
}

// calculateSustainedAlertLevel determines the alert level based on sustained conditions
func (m *Manager) calculateSustainedAlertLevel(now time.Time) string {
	// Check for sustained critical (either sensor)
	highCriticalSustained := !m.highCriticalStart.IsZero() && now.Sub(m.highCriticalStart) >= SustainedConditionDuration
	lowCriticalSustained := !m.lowCriticalStart.IsZero() && now.Sub(m.lowCriticalStart) >= SustainedConditionDuration

	if highCriticalSustained || lowCriticalSustained {
		return "critical"
	}

	// Check for sustained warning (either sensor)
	highWarningSustained := !m.highWarningStart.IsZero() && now.Sub(m.highWarningStart) >= SustainedConditionDuration
	lowWarningSustained := !m.lowWarningStart.IsZero() && now.Sub(m.lowWarningStart) >= SustainedConditionDuration

	if highWarningSustained || lowWarningSustained {
		return "warning"
	}

	return "none"
}

// getConditionStartTime returns the earliest condition start time
func (m *Manager) getConditionStartTime() time.Time {
	var earliest time.Time

	times := []time.Time{m.highWarningStart, m.lowWarningStart, m.highCriticalStart, m.lowCriticalStart}
	for _, t := range times {
		if !t.IsZero() && (earliest.IsZero() || t.Before(earliest)) {
			earliest = t
		}
	}

	return earliest
}

// checkConditionResolved checks if the alert condition has resolved with hysteresis
func (m *Manager) checkConditionResolved(now time.Time) {
	// Apply hysteresis based on current alert level
	highClearThreshold := AtticHumidityWarningClear
	lowClearThreshold := AtticHumidityWarningClear

	if m.currentAlertLevel == "critical" {
		highClearThreshold = AtticHumidityCriticalClear
		lowClearThreshold = AtticHumidityCriticalClear
	}

	// Check if both sensors are below their clear thresholds
	highCleared := !m.highSensorValid || m.atticHighHumidity < highClearThreshold
	lowCleared := !m.lowSensorValid || m.atticLowHumidity < lowClearThreshold

	if highCleared && lowCleared {
		m.logger.Info("Attic humidity condition resolved",
			zap.String("previous_level", m.currentAlertLevel),
			zap.Float64("high_humidity", m.atticHighHumidity),
			zap.Float64("low_humidity", m.atticLowHumidity))

		// Send resolution notification (only once per incident)
		if m.hasNotifiedForCurrentIncident {
			m.sendResolutionNotification(now)
		}

		// Reset state
		m.currentAlertLevel = "none"
		m.hasNotifiedForCurrentIncident = false
		m.highWarningStart = time.Time{}
		m.lowWarningStart = time.Time{}
		m.highCriticalStart = time.Time{}
		m.lowCriticalStart = time.Time{}

		// Update shadow state
		m.shadowTracker.UpdateAlertLevel("none", time.Time{}, false)
	}
}

// sendAlertNotification sends an alert notification with rate limiting
func (m *Manager) sendAlertNotification(now time.Time, level string) {
	// Check rate limiting
	var rateLimit time.Duration
	var lastNotification time.Time

	if level == "critical" {
		rateLimit = CriticalNotificationRateLimit
		lastNotification = m.lastCriticalNotification
	} else {
		rateLimit = WarningNotificationRateLimit
		lastNotification = m.lastWarningNotification
	}

	if !lastNotification.IsZero() && now.Sub(lastNotification) < rateLimit {
		m.logger.Debug("Skipping notification due to rate limit",
			zap.String("level", level),
			zap.Duration("time_since_last", now.Sub(lastNotification)),
			zap.Duration("rate_limit", rateLimit))
		return
	}

	// Build sensor location list
	var sensorLocations []string
	var maxHumidity float64

	if m.highSensorValid && m.atticHighHumidity >= AtticHumidityWarningThreshold {
		sensorLocations = append(sensorLocations, "high sensor")
		if m.atticHighHumidity > maxHumidity {
			maxHumidity = m.atticHighHumidity
		}
	}
	if m.lowSensorValid && m.atticLowHumidity >= AtticHumidityWarningThreshold {
		sensorLocations = append(sensorLocations, "low sensor")
		if m.atticLowHumidity > maxHumidity {
			maxHumidity = m.atticLowHumidity
		}
	}

	// Build notification
	var title, message string
	var importance string
	var sticky bool

	if level == "critical" {
		title = "Attic Humidity Critical"
		message = fmt.Sprintf("Humidity at %.0f%% (%s) for 30+ minutes. Mold risk - take action!",
			maxHumidity, formatSensorLocations(sensorLocations))
		importance = "high"
		sticky = true
	} else {
		title = "Attic Humidity Warning"
		message = fmt.Sprintf("Humidity at %.0f%% (%s) for 30+ minutes. Check ventilation.",
			maxHumidity, formatSensorLocations(sensorLocations))
		importance = "default"
		sticky = false
	}

	notification := &ha.Notification{
		Title:   title,
		Message: message,
		Data: &ha.NotificationData{
			Tag:        fmt.Sprintf("environmental-attic-humidity-%s", level),
			Group:      "environmental",
			Importance: importance,
			Sticky:     sticky,
		},
	}

	m.logger.Info("Sending attic humidity alert notification",
		zap.String("level", level),
		zap.Float64("humidity", maxHumidity),
		zap.Strings("sensors", sensorLocations))

	// Record in shadow state before sending
	m.shadowTracker.RecordNotification(level, message, sensorLocations)

	if m.readOnly {
		m.logger.Info("Skipping notification send in read-only mode",
			zap.String("title", title),
			zap.String("message", message))
		return
	}

	// Send notification
	if err := m.haClient.SendNotification(NotificationTarget, notification); err != nil {
		m.logger.Error("Failed to send attic humidity notification",
			zap.String("level", level),
			zap.Error(err))
		return
	}

	// Update rate limiting timestamps
	if level == "critical" {
		m.lastCriticalNotification = now
	} else {
		m.lastWarningNotification = now
	}
	m.hasNotifiedForCurrentIncident = true
}

// sendResolutionNotification sends a notification that the condition has resolved
func (m *Manager) sendResolutionNotification(now time.Time) {
	message := fmt.Sprintf("Attic humidity has returned to safe levels (high: %.0f%%, low: %.0f%%).",
		m.atticHighHumidity, m.atticLowHumidity)

	notification := &ha.Notification{
		Title:   "Attic Humidity Resolved",
		Message: message,
		Data: &ha.NotificationData{
			Tag:        "environmental-attic-humidity-resolved",
			Group:      "environmental",
			Importance: "default",
		},
	}

	m.logger.Info("Sending attic humidity resolution notification",
		zap.Float64("high_humidity", m.atticHighHumidity),
		zap.Float64("low_humidity", m.atticLowHumidity))

	// Record in shadow state
	m.shadowTracker.RecordResolutionNotice(message)

	if m.readOnly {
		m.logger.Info("Skipping resolution notification send in read-only mode",
			zap.String("message", message))
		return
	}

	// Send notification
	if err := m.haClient.SendNotification(NotificationTarget, notification); err != nil {
		m.logger.Error("Failed to send resolution notification", zap.Error(err))
		return
	}

	m.lastResolutionNotification = now
}

// parseHumidity parses a humidity value from a state string
func parseHumidity(s string) (float64, error) {
	if s == "" || s == "unknown" || s == "unavailable" {
		return 0, fmt.Errorf("invalid humidity state: %s", s)
	}
	return strconv.ParseFloat(s, 64)
}

// formatSensorLocations formats sensor locations for display
func formatSensorLocations(locations []string) string {
	if len(locations) == 0 {
		return "unknown"
	}
	if len(locations) == 1 {
		return locations[0]
	}
	return "both sensors"
}

// Reset resets the manager state (for testing)
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.atticHighHumidity = 0
	m.atticLowHumidity = 0
	m.highSensorValid = false
	m.lowSensorValid = false
	m.highWarningStart = time.Time{}
	m.lowWarningStart = time.Time{}
	m.highCriticalStart = time.Time{}
	m.lowCriticalStart = time.Time{}
	m.currentAlertLevel = "none"
	m.lastWarningNotification = time.Time{}
	m.lastCriticalNotification = time.Time{}
	m.lastResolutionNotification = time.Time{}
	m.hasNotifiedForCurrentIncident = false
}

// GetCurrentState returns the current humidity values and alert level (for testing)
func (m *Manager) GetCurrentState() (highHumidity, lowHumidity float64, alertLevel string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.atticHighHumidity, m.atticLowHumidity, m.currentAlertLevel
}
