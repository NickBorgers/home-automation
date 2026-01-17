package environmental

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"homeautomation/internal/clock"
	"homeautomation/internal/ha"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Humidity thresholds (RH percentage)
const (
	// Warning threshold - approaching mold risk
	HumidityWarningThreshold = 55.0
	// Critical threshold - definite mold risk
	HumidityCriticalThreshold = 65.0
	// Hysteresis: warning clears when humidity drops to this level
	HumidityWarningClear = 50.0
	// Hysteresis: critical clears when humidity drops to this level
	HumidityCriticalClear = 60.0

	// Debounce: condition must be sustained this long before alerting
	SustainedConditionDuration = 30 * time.Minute

	// Rate limiting
	WarningNotificationRateLimit  = 4 * time.Hour
	CriticalNotificationRateLimit = 1 * time.Hour

	// Label used to identify indoor devices
	IndoorLabel = "indoor"

	// Notification target
	NotificationTarget = "nicks_iphone"
)

// HumiditySensor represents a discovered humidity sensor
type HumiditySensor struct {
	EntityID      string
	DeviceID      string
	FriendlyName  string
	IsIndoor      bool // true = alerts enabled, false = informational only
	Value         float64
	Valid         bool
	WarningStart  time.Time
	CriticalStart time.Time
}

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

	// Humidity sensors - keyed by entity_id
	mu      sync.Mutex
	sensors map[string]*HumiditySensor

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
		sensors:           make(map[string]*HumiditySensor),
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

	// Discover humidity sensors dynamically
	if err := m.discoverHumiditySensors(); err != nil {
		m.logger.Warn("Failed to discover some humidity sensors", zap.Error(err))
		// Continue anyway - may have discovered some sensors
	}

	m.mu.Lock()
	sensorCount := len(m.sensors)
	indoorCount := 0
	for _, s := range m.sensors {
		if s.IsIndoor {
			indoorCount++
		}
	}
	m.mu.Unlock()

	if sensorCount == 0 {
		m.logger.Warn("No humidity sensors discovered - environmental monitoring will be inactive")
	} else {
		m.logger.Info("Humidity sensors discovered",
			zap.Int("total", sensorCount),
			zap.Int("indoor_alertable", indoorCount),
			zap.Int("outdoor_informational", sensorCount-indoorCount))
	}

	// Initialize shadow state with current input values (after subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	m.logger.Info("Environmental Monitoring Manager started successfully")
	return nil
}

// discoverHumiditySensors discovers all humidity sensors and classifies them as indoor/outdoor
func (m *Manager) discoverHumiditySensors() error {
	var errs []error

	// Step 1: Get all entity states
	states, err := m.haClient.GetAllStates()
	if err != nil {
		return fmt.Errorf("failed to get entity states: %w", err)
	}

	// Step 2: Filter for humidity sensors (device_class: humidity)
	var humiditySensors []*ha.State
	for _, state := range states {
		if !strings.HasPrefix(state.EntityID, "sensor.") {
			continue
		}
		if deviceClass, ok := state.Attributes["device_class"].(string); ok {
			if deviceClass == "humidity" {
				humiditySensors = append(humiditySensors, state)
			}
		}
	}

	m.logger.Info("Found humidity sensors", zap.Int("count", len(humiditySensors)))

	if len(humiditySensors) == 0 {
		return nil
	}

	// Step 3: Get entity registry to map entity_id -> device_id
	entityRegistry, err := m.haClient.GetEntityRegistry()
	if err != nil {
		m.logger.Warn("Failed to get entity registry - all sensors will be treated as outdoor",
			zap.Error(err))
		errs = append(errs, err)
	}

	entityToDevice := make(map[string]string)
	for _, entry := range entityRegistry {
		entityToDevice[entry.EntityID] = entry.DeviceID
	}

	// Step 4: Get device registry to check for "indoor" label
	devices, err := m.haClient.GetDevices()
	if err != nil {
		m.logger.Warn("Failed to get device registry - all sensors will be treated as outdoor",
			zap.Error(err))
		errs = append(errs, err)
	}

	indoorDevices := make(map[string]bool)
	for _, device := range devices {
		for _, label := range device.Labels {
			// Case-insensitive comparison to handle "Indoor", "indoor", "INDOOR", etc.
			if strings.EqualFold(label, IndoorLabel) {
				indoorDevices[device.ID] = true
				break
			}
		}
	}

	m.logger.Info("Indoor devices discovered", zap.Int("count", len(indoorDevices)))

	// Step 5: Create HumiditySensor structs and subscribe to each
	m.mu.Lock()
	for _, state := range humiditySensors {
		deviceID := entityToDevice[state.EntityID]
		isIndoor := indoorDevices[deviceID]

		friendlyName := state.EntityID
		if name, ok := state.Attributes["friendly_name"].(string); ok {
			friendlyName = name
		}

		sensor := &HumiditySensor{
			EntityID:     state.EntityID,
			DeviceID:     deviceID,
			FriendlyName: friendlyName,
			IsIndoor:     isIndoor,
			Valid:        false,
		}

		// Parse initial value
		if value, err := parseHumidity(state.State); err == nil {
			sensor.Value = value
			sensor.Valid = true
		}

		m.sensors[state.EntityID] = sensor

		m.logger.Debug("Discovered humidity sensor",
			zap.String("entity_id", state.EntityID),
			zap.String("friendly_name", friendlyName),
			zap.String("device_id", deviceID),
			zap.Bool("is_indoor", isIndoor),
			zap.Float64("current_value", sensor.Value))
	}
	m.mu.Unlock()

	// Subscribe to each sensor (outside the lock to avoid deadlock)
	for entityID := range m.sensors {
		if err := m.subHelper.SubscribeToEntity(entityID, m.handleHumidityChange); err != nil {
			m.logger.Warn("Failed to subscribe to humidity sensor",
				zap.String("entity_id", entityID),
				zap.Error(err))
			errs = append(errs, err)
		}
	}

	// Update shadow state with all discovered sensors
	m.updateShadowSensors()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// Stop stops the environmental monitoring manager
func (m *Manager) Stop() {
	m.logger.Info("Stopping Environmental Monitoring Manager")

	// Unsubscribe from all subscriptions
	m.subHelper.UnsubscribeAll()

	m.logger.Info("Environmental Monitoring Manager stopped")
}

// handleHumidityChange processes changes to any humidity sensor
func (m *Manager) handleHumidityChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	// Parse humidity value
	humidity, err := parseHumidity(newState.State)
	if err != nil {
		m.logger.Debug("Invalid humidity value",
			zap.String("entity_id", entityID),
			zap.String("state", newState.State),
			zap.Error(err))
		m.mu.Lock()
		if sensor, ok := m.sensors[entityID]; ok {
			sensor.Valid = false
		}
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	sensor, ok := m.sensors[entityID]
	if !ok {
		m.mu.Unlock()
		m.logger.Warn("Received update for unknown sensor", zap.String("entity_id", entityID))
		return
	}

	sensor.Value = humidity
	sensor.Valid = true
	m.mu.Unlock()

	m.logger.Debug("Humidity sensor changed",
		zap.String("entity_id", entityID),
		zap.String("friendly_name", sensor.FriendlyName),
		zap.Float64("humidity", humidity),
		zap.Bool("is_indoor", sensor.IsIndoor))

	// Update shadow state
	m.updateShadowSensors()

	// Only evaluate alerts for indoor sensors
	if sensor.IsIndoor {
		m.evaluateHumidity()
	}
}

// updateShadowSensors updates the shadow state with current sensor data
func (m *Manager) updateShadowSensors() {
	m.mu.Lock()
	defer m.mu.Unlock()

	sensorData := make([]shadowstate.HumiditySensorData, 0, len(m.sensors))
	for _, sensor := range m.sensors {
		sensorData = append(sensorData, shadowstate.HumiditySensorData{
			EntityID:     sensor.EntityID,
			FriendlyName: sensor.FriendlyName,
			DeviceID:     sensor.DeviceID,
			IsIndoor:     sensor.IsIndoor,
			Value:        sensor.Value,
			Valid:        sensor.Valid,
		})
	}

	m.shadowTracker.UpdateHumiditySensors(sensorData)
}

// evaluateHumidity evaluates current humidity levels for indoor sensors and sends alerts if needed
func (m *Manager) evaluateHumidity() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock.Now()

	// Track warning/critical for each indoor sensor
	for _, sensor := range m.sensors {
		if !sensor.IsIndoor || !sensor.Valid {
			continue
		}

		isWarning := sensor.Value >= HumidityWarningThreshold
		isCritical := sensor.Value >= HumidityCriticalThreshold

		// Warning tracking
		if isWarning {
			if sensor.WarningStart.IsZero() {
				sensor.WarningStart = now
			}
		} else {
			sensor.WarningStart = time.Time{}
		}

		// Critical tracking
		if isCritical {
			if sensor.CriticalStart.IsZero() {
				sensor.CriticalStart = now
			}
		} else {
			sensor.CriticalStart = time.Time{}
		}
	}

	// Calculate the highest sustained level across all indoor sensors
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

// calculateSustainedAlertLevel determines the alert level based on sustained conditions
func (m *Manager) calculateSustainedAlertLevel(now time.Time) string {
	// Check for sustained critical (any indoor sensor)
	for _, sensor := range m.sensors {
		if !sensor.IsIndoor || !sensor.Valid {
			continue
		}
		if !sensor.CriticalStart.IsZero() && now.Sub(sensor.CriticalStart) >= SustainedConditionDuration {
			return "critical"
		}
	}

	// Check for sustained warning (any indoor sensor)
	for _, sensor := range m.sensors {
		if !sensor.IsIndoor || !sensor.Valid {
			continue
		}
		if !sensor.WarningStart.IsZero() && now.Sub(sensor.WarningStart) >= SustainedConditionDuration {
			return "warning"
		}
	}

	return "none"
}

// getConditionStartTime returns the earliest condition start time across all sensors
func (m *Manager) getConditionStartTime() time.Time {
	var earliest time.Time

	for _, sensor := range m.sensors {
		if !sensor.IsIndoor {
			continue
		}
		for _, t := range []time.Time{sensor.WarningStart, sensor.CriticalStart} {
			if !t.IsZero() && (earliest.IsZero() || t.Before(earliest)) {
				earliest = t
			}
		}
	}

	return earliest
}

// checkConditionResolved checks if the alert condition has resolved with hysteresis
func (m *Manager) checkConditionResolved(now time.Time) {
	// Determine clear threshold based on current alert level
	clearThreshold := HumidityWarningClear
	if m.currentAlertLevel == "critical" {
		clearThreshold = HumidityCriticalClear
	}

	// Check if ALL indoor sensors are below their clear thresholds
	allCleared := true
	for _, sensor := range m.sensors {
		if !sensor.IsIndoor {
			continue
		}
		if sensor.Valid && sensor.Value >= clearThreshold {
			allCleared = false
			break
		}
	}

	if allCleared {
		m.logger.Info("Humidity condition resolved",
			zap.String("previous_level", m.currentAlertLevel))

		// Send resolution notification (only once per incident)
		if m.hasNotifiedForCurrentIncident {
			m.sendResolutionNotification(now)
		}

		// Reset state
		m.currentAlertLevel = "none"
		m.hasNotifiedForCurrentIncident = false
		for _, sensor := range m.sensors {
			sensor.WarningStart = time.Time{}
			sensor.CriticalStart = time.Time{}
		}

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

	// Build sensor location list and find max humidity
	var sensorLocations []string
	var maxHumidity float64

	for _, sensor := range m.sensors {
		if !sensor.IsIndoor || !sensor.Valid {
			continue
		}
		if sensor.Value >= HumidityWarningThreshold {
			sensorLocations = append(sensorLocations, sensor.FriendlyName)
			if sensor.Value > maxHumidity {
				maxHumidity = sensor.Value
			}
		}
	}

	// Build notification
	var title, message string
	var importance string
	var sticky bool

	if level == "critical" {
		title = "High Humidity Critical"
		message = fmt.Sprintf("Humidity at %.0f%% (%s) for 30+ minutes. Mold risk - take action!",
			maxHumidity, formatSensorLocations(sensorLocations))
		importance = "high"
		sticky = true
	} else {
		title = "High Humidity Warning"
		message = fmt.Sprintf("Humidity at %.0f%% (%s) for 30+ minutes. Check ventilation.",
			maxHumidity, formatSensorLocations(sensorLocations))
		importance = "default"
		sticky = false
	}

	notification := &ha.Notification{
		Title:   title,
		Message: message,
		Data: &ha.NotificationData{
			Tag:        fmt.Sprintf("environmental-humidity-%s", level),
			Group:      "environmental",
			Importance: importance,
			Sticky:     sticky,
		},
	}

	m.logger.Info("Sending humidity alert notification",
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
		m.logger.Error("Failed to send humidity notification",
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
	// Build summary of current indoor sensor values
	var sensorSummary []string
	for _, sensor := range m.sensors {
		if !sensor.IsIndoor || !sensor.Valid {
			continue
		}
		sensorSummary = append(sensorSummary, fmt.Sprintf("%s: %.0f%%", sensor.FriendlyName, sensor.Value))
	}

	message := fmt.Sprintf("Humidity has returned to safe levels. Current readings: %s",
		strings.Join(sensorSummary, ", "))

	notification := &ha.Notification{
		Title:   "Humidity Resolved",
		Message: message,
		Data: &ha.NotificationData{
			Tag:        "environmental-humidity-resolved",
			Group:      "environmental",
			Importance: "default",
		},
	}

	m.logger.Info("Sending humidity resolution notification")

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
	if len(locations) == 2 {
		return locations[0] + " and " + locations[1]
	}
	return fmt.Sprintf("%d sensors", len(locations))
}

// Reset resets the manager state (for testing)
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, sensor := range m.sensors {
		sensor.Value = 0
		sensor.Valid = false
		sensor.WarningStart = time.Time{}
		sensor.CriticalStart = time.Time{}
	}
	m.currentAlertLevel = "none"
	m.lastWarningNotification = time.Time{}
	m.lastCriticalNotification = time.Time{}
	m.lastResolutionNotification = time.Time{}
	m.hasNotifiedForCurrentIncident = false
}

// GetCurrentState returns the current alert level (for testing)
func (m *Manager) GetCurrentState() (alertLevel string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentAlertLevel
}

// GetSensors returns a copy of the current sensors map (for testing)
func (m *Manager) GetSensors() map[string]*HumiditySensor {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]*HumiditySensor)
	for k, v := range m.sensors {
		// Make a copy
		sensor := *v
		result[k] = &sensor
	}
	return result
}

// AddSensor adds a sensor directly (for testing)
func (m *Manager) AddSensor(sensor *HumiditySensor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sensors[sensor.EntityID] = sensor
}

// SimulateSensorChange simulates a sensor value change (for testing)
func (m *Manager) SimulateSensorChange(entityID string, humidity float64) {
	m.handleHumidityChange(entityID, nil, &ha.State{
		EntityID: entityID,
		State:    fmt.Sprintf("%.1f", humidity),
	})
}
