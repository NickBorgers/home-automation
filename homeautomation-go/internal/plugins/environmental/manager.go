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
	"homeautomation/internal/ntfy"
	"homeautomation/internal/shadowstate"
	"homeautomation/internal/state"

	"go.uber.org/zap"
)

// Humidity thresholds (RH percentage)
const (
	// Conditioned space thresholds (living areas with HVAC)
	HumidityWarningThreshold  = 55.0 // Warning - approaching mold risk
	HumidityCriticalThreshold = 65.0 // Critical - definite mold risk
	HumidityWarningClear      = 50.0 // Hysteresis: warning clears at this level
	HumidityCriticalClear     = 60.0 // Hysteresis: critical clears at this level

	// Unconditioned space thresholds (barns, attics, sheds)
	// These spaces naturally track outdoor humidity, so thresholds are higher.
	// Alert suppression also applies when tracking close to outdoor levels.
	UnconditionedWarningThreshold  = 75.0 // Warning for unconditioned spaces
	UnconditionedCriticalThreshold = 80.0 // Critical / absolute ceiling for unconditioned spaces
	UnconditionedWarningClear      = 70.0 // Hysteresis: warning clears at this level
	UnconditionedCriticalClear     = 75.0 // Hysteresis: critical clears at this level

	// Outdoor humidity comparison
	OutdoorHumidityEntityID = "sensor.weather_station_humidity"
	// OutdoorHumidityMargin is the margin above outdoor humidity within which
	// unconditioned spaces are considered to be simply tracking outdoor conditions.
	OutdoorHumidityMargin = 5.0

	// Debounce: condition must be sustained this long before alerting
	SustainedConditionDuration = 30 * time.Minute

	// Rate limiting
	WarningNotificationRateLimit  = 4 * time.Hour
	CriticalNotificationRateLimit = 1 * time.Hour
)

// HumiditySensor represents a discovered humidity sensor
type HumiditySensor struct {
	EntityID        string
	DeviceID        string
	FriendlyName    string
	IsIndoor        bool // true = alerts enabled, false = informational only
	IsUnconditioned bool // true = unconditioned space (barn, attic) with relaxed thresholds
	Value           float64
	Valid           bool
	WarningStart    time.Time
	CriticalStart   time.Time
}

// WaterLeakSensor represents a discovered water leak sensor
type WaterLeakSensor struct {
	EntityID     string
	DeviceID     string
	FriendlyName string
	State        string // "on" = leak, "off" = no leak
	LastChanged  time.Time
	// Track if we've sent notification for current leak
	NotificationSent bool
}

// Manager handles environmental monitoring and alerts (humidity and water leaks)
type Manager struct {
	haClient     ha.HAClient
	stateManager *state.Manager
	logger       *zap.Logger
	readOnly     bool
	clock        clock.Clock
	ntfyClient   ntfy.Notifier // nil if notifications disabled

	// Shadow state tracking
	shadowTracker *shadowstate.EnvironmentalTracker

	// Subscription helper for automatic shadow state input capture
	subHelper *shadowstate.SubscriptionHelper

	// Humidity sensors - keyed by entity_id
	mu      sync.Mutex
	sensors map[string]*HumiditySensor

	// Water leak sensors - keyed by entity_id
	waterLeakSensors map[string]*WaterLeakSensor

	// Current alert level for humidity ("none", "warning", "critical")
	currentAlertLevel string

	// Outdoor reference humidity (from weather station) for comparison
	outdoorHumidity      float64
	outdoorHumidityValid bool

	// Rate limiting for humidity
	lastWarningNotification    time.Time
	lastCriticalNotification   time.Time
	lastResolutionNotification time.Time

	// Track if we've notified for the current humidity incident (to send resolution only once)
	hasNotifiedForCurrentIncident bool

	// Names of sensors that triggered the current alert incident
	alertedSensorNames map[string]bool
}

// NewManager creates a new environmental monitoring manager.
// ntfyClient can be nil if notifications are disabled (NTFY_TOPIC_URL not set).
func NewManager(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier) *Manager {
	shadowTracker := shadowstate.NewEnvironmentalTracker()

	namedLogger := logger.Named("environmental")
	if ntfyClient == nil {
		namedLogger.Warn("ntfy client not configured - notifications will be disabled")
	}

	return &Manager{
		haClient:           haClient,
		stateManager:       stateManager,
		logger:             namedLogger,
		readOnly:           readOnly,
		clock:              clock.NewRealClock(),
		ntfyClient:         ntfyClient,
		shadowTracker:      shadowTracker,
		subHelper:          shadowstate.NewSubscriptionHelper(haClient, stateManager, registry, shadowTracker, "environmental", namedLogger),
		sensors:            make(map[string]*HumiditySensor),
		waterLeakSensors:   make(map[string]*WaterLeakSensor),
		currentAlertLevel:  "none",
		alertedSensorNames: make(map[string]bool),
	}
}

// NewManagerWithClock creates a new environmental monitoring manager with a custom clock (for testing)
func NewManagerWithClock(haClient ha.HAClient, stateManager *state.Manager, logger *zap.Logger, readOnly bool, registry *shadowstate.SubscriptionRegistry, ntfyClient ntfy.Notifier, c clock.Clock) *Manager {
	m := NewManager(haClient, stateManager, logger, readOnly, registry, ntfyClient)
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
	unconditionedCount := 0
	for _, s := range m.sensors {
		if s.IsIndoor {
			indoorCount++
		}
		if s.IsUnconditioned {
			unconditionedCount++
		}
	}
	m.mu.Unlock()

	if sensorCount == 0 {
		m.logger.Warn("No humidity sensors discovered - humidity monitoring will be inactive")
	} else {
		m.logger.Info("Humidity sensors discovered",
			zap.Int("total", sensorCount),
			zap.Int("indoor_alertable", indoorCount),
			zap.Int("unconditioned", unconditionedCount),
			zap.Int("outdoor_informational", sensorCount-indoorCount))
	}

	// Discover water leak sensors
	if err := m.discoverWaterLeakSensors(); err != nil {
		m.logger.Warn("Failed to discover some water leak sensors", zap.Error(err))
	}

	m.mu.Lock()
	waterLeakCount := len(m.waterLeakSensors)
	m.mu.Unlock()

	if waterLeakCount == 0 {
		m.logger.Warn("No water leak sensors discovered")
	} else {
		m.logger.Info("Water leak sensors discovered", zap.Int("count", waterLeakCount))
	}

	// Initialize shadow state with current input values (after subscriptions registered)
	m.subHelper.CaptureInitialInputs()

	// Update shadow state with discovered sensors
	m.updateShadowSensors()
	m.updateShadowWaterLeakSensors()

	// Evaluate initial water leak state
	m.evaluateWaterLeaks()

	m.logger.Info("Environmental Monitoring Manager started successfully")
	return nil
}

// discoverHumiditySensors discovers all humidity sensors and classifies them as indoor/outdoor.
func (m *Manager) discoverHumiditySensors() error {
	var errs []error

	// Get all entity states
	states, err := m.haClient.GetAllStates()
	if err != nil {
		return fmt.Errorf("failed to get entity states: %w", err)
	}

	// Filter for humidity sensors (device_class: humidity)
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

	// Get entity registry to map entity_id -> device_id
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

	// Get device registry to check for "indoor" label
	devices, err := m.haClient.GetDevices()
	if err != nil {
		m.logger.Warn("Failed to get device registry - all sensors will be treated as outdoor",
			zap.Error(err))
		errs = append(errs, err)
	}

	labelChecker := ha.NewDeviceLabelChecker(devices)

	indoorDevices := make(map[string]bool)
	unconditionedDevices := make(map[string]bool)
	for _, device := range devices {
		if labelChecker.HasLabelIgnoreCase(device.ID, ha.IndoorLabel) {
			indoorDevices[device.ID] = true
		}
		if labelChecker.HasLabelIgnoreCase(device.ID, ha.UnconditionedLabel) {
			unconditionedDevices[device.ID] = true
			// Unconditioned label implies indoor monitoring (with relaxed thresholds)
			indoorDevices[device.ID] = true
		}
	}

	m.logger.Info("Indoor devices discovered",
		zap.Int("count", len(indoorDevices)),
		zap.Int("unconditioned", len(unconditionedDevices)))

	// Create HumiditySensor structs and subscribe to each
	m.mu.Lock()
	for _, state := range humiditySensors {
		deviceID := entityToDevice[state.EntityID]
		isIndoor := indoorDevices[deviceID]

		friendlyName := state.EntityID
		if name, ok := state.Attributes["friendly_name"].(string); ok {
			friendlyName = name
		}

		sensor := &HumiditySensor{
			EntityID:        state.EntityID,
			DeviceID:        deviceID,
			FriendlyName:    friendlyName,
			IsIndoor:        isIndoor,
			IsUnconditioned: unconditionedDevices[deviceID],
			Valid:           false,
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
			zap.Bool("is_unconditioned", sensor.IsUnconditioned),
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

	// Initialize outdoor reference humidity from weather station sensor
	m.mu.Lock()
	if sensor, ok := m.sensors[OutdoorHumidityEntityID]; ok && sensor.Valid {
		m.outdoorHumidity = sensor.Value
		m.outdoorHumidityValid = true
		m.logger.Info("Outdoor reference sensor found for humidity comparison",
			zap.Float64("humidity", sensor.Value))
	} else if _, ok := m.sensors[OutdoorHumidityEntityID]; ok {
		m.logger.Info("Outdoor reference sensor found but value not yet available")
	} else {
		m.logger.Info("Outdoor reference sensor not found - unconditioned spaces will use absolute thresholds only")
	}
	m.mu.Unlock()

	// Update shadow state with all discovered sensors
	m.updateShadowSensors()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// discoverWaterLeakSensors discovers all water leak binary sensors
func (m *Manager) discoverWaterLeakSensors() error {
	var errs []error

	// Get all entity states
	states, err := m.haClient.GetAllStates()
	if err != nil {
		return fmt.Errorf("failed to get entity states: %w", err)
	}

	// Filter for water leak sensors (binary_sensor with device_class: moisture or entity_id containing "water_leak")
	var waterLeakStates []*ha.State
	for _, state := range states {
		if !strings.HasPrefix(state.EntityID, "binary_sensor.") {
			continue
		}
		// Check device_class first
		if deviceClass, ok := state.Attributes["device_class"].(string); ok {
			if deviceClass == "moisture" {
				waterLeakStates = append(waterLeakStates, state)
				continue
			}
		}
		// Also match by entity_id pattern
		if strings.Contains(strings.ToLower(state.EntityID), "water_leak") {
			waterLeakStates = append(waterLeakStates, state)
		}
	}

	m.logger.Info("Found water leak sensors", zap.Int("count", len(waterLeakStates)))

	if len(waterLeakStates) == 0 {
		return nil
	}

	// Get entity registry for device IDs
	entityRegistry, err := m.haClient.GetEntityRegistry()
	if err != nil {
		m.logger.Warn("Failed to get entity registry", zap.Error(err))
		errs = append(errs, err)
	}

	entityToDevice := make(map[string]string)
	for _, entry := range entityRegistry {
		entityToDevice[entry.EntityID] = entry.DeviceID
	}

	// Get device registry for label checking
	devices, err := m.haClient.GetDevices()
	if err != nil {
		m.logger.Warn("Failed to get device registry", zap.Error(err))
		errs = append(errs, err)
	}

	labelChecker := ha.NewDeviceLabelChecker(devices)

	// Create sensor structs and subscribe
	m.mu.Lock()
	for _, state := range waterLeakStates {
		deviceID := entityToDevice[state.EntityID]

		// Check if device has the monitoring ignore label
		if labelChecker.ShouldIgnoreForMonitoring(deviceID) {
			m.logger.Info("Skipping water leak sensor with monitoring_ignore label on device",
				zap.String("entity_id", state.EntityID),
				zap.String("device_id", deviceID))
			continue
		}

		friendlyName := state.EntityID
		if name, ok := state.Attributes["friendly_name"].(string); ok {
			friendlyName = name
		}

		sensor := &WaterLeakSensor{
			EntityID:     state.EntityID,
			DeviceID:     deviceID,
			FriendlyName: friendlyName,
			State:        state.State,
			LastChanged:  state.LastChanged,
		}

		m.waterLeakSensors[state.EntityID] = sensor

		m.logger.Debug("Discovered water leak sensor",
			zap.String("entity_id", state.EntityID),
			zap.String("friendly_name", friendlyName),
			zap.String("state", state.State))
	}
	m.mu.Unlock()

	// Subscribe to each sensor
	for entityID := range m.waterLeakSensors {
		if err := m.subHelper.SubscribeToEntity(entityID, m.handleWaterLeakChange); err != nil {
			m.logger.Warn("Failed to subscribe to water leak sensor",
				zap.String("entity_id", entityID),
				zap.Error(err))
			errs = append(errs, err)
		}
	}

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
		if entityID == OutdoorHumidityEntityID {
			m.outdoorHumidityValid = false
			m.shadowTracker.UpdateOutdoorHumidity(0, false)
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

	// Track outdoor reference humidity from weather station
	isOutdoorRef := entityID == OutdoorHumidityEntityID
	if isOutdoorRef {
		m.outdoorHumidity = humidity
		m.outdoorHumidityValid = true
	}
	m.mu.Unlock()

	m.logger.Debug("Humidity sensor changed",
		zap.String("entity_id", entityID),
		zap.String("friendly_name", sensor.FriendlyName),
		zap.Float64("humidity", humidity),
		zap.Bool("is_indoor", sensor.IsIndoor))

	// Update shadow state
	m.updateShadowSensors()
	if isOutdoorRef {
		m.shadowTracker.UpdateOutdoorHumidity(humidity, true)
	}

	// Evaluate alerts for indoor sensors, or when outdoor reference changes
	// (outdoor changes can affect suppression for unconditioned sensors)
	if sensor.IsIndoor || isOutdoorRef {
		m.evaluateHumidity()
	}
}

// handleWaterLeakChange processes water leak sensor state changes
func (m *Manager) handleWaterLeakChange(entityID string, oldState, newState *ha.State) {
	if newState == nil {
		return
	}

	m.mu.Lock()
	sensor, ok := m.waterLeakSensors[entityID]
	if !ok {
		m.mu.Unlock()
		m.logger.Warn("Received update for unknown water leak sensor", zap.String("entity_id", entityID))
		return
	}

	oldSensorState := sensor.State
	sensor.State = newState.State
	sensor.LastChanged = newState.LastChanged

	// If leak is cleared, reset notification flag
	if newState.State == "off" && oldSensorState == "on" {
		sensor.NotificationSent = false
	}
	m.mu.Unlock()

	m.logger.Debug("Water leak sensor changed",
		zap.String("entity_id", entityID),
		zap.String("friendly_name", sensor.FriendlyName),
		zap.String("old_state", oldSensorState),
		zap.String("new_state", newState.State))

	// Update shadow state
	m.updateShadowWaterLeakSensors()

	// Evaluate and potentially send notification
	m.evaluateWaterLeaks()
}

// updateShadowSensors updates the shadow state with current humidity sensor data
func (m *Manager) updateShadowSensors() {
	m.mu.Lock()
	defer m.mu.Unlock()

	sensorData := make([]shadowstate.HumiditySensorData, 0, len(m.sensors))
	for _, sensor := range m.sensors {
		sensorData = append(sensorData, shadowstate.HumiditySensorData{
			EntityID:        sensor.EntityID,
			FriendlyName:    sensor.FriendlyName,
			DeviceID:        sensor.DeviceID,
			IsIndoor:        sensor.IsIndoor,
			IsUnconditioned: sensor.IsUnconditioned,
			Value:           sensor.Value,
			Valid:           sensor.Valid,
		})
	}

	m.shadowTracker.UpdateHumiditySensors(sensorData)
}

// updateShadowWaterLeakSensors updates the shadow state with current water leak sensor data
func (m *Manager) updateShadowWaterLeakSensors() {
	m.mu.Lock()
	defer m.mu.Unlock()

	waterLeakData := make([]shadowstate.WaterLeakSensorData, 0, len(m.waterLeakSensors))
	for _, sensor := range m.waterLeakSensors {
		waterLeakData = append(waterLeakData, shadowstate.WaterLeakSensorData{
			EntityID:     sensor.EntityID,
			FriendlyName: sensor.FriendlyName,
			DeviceID:     sensor.DeviceID,
			State:        sensor.State,
			LastChanged:  sensor.LastChanged,
		})
	}
	m.shadowTracker.UpdateWaterLeakSensors(waterLeakData)
}

// evaluateWaterLeaks checks for active water leaks and sends notifications
func (m *Manager) evaluateWaterLeaks() {
	m.mu.Lock()
	var activeLeaks []shadowstate.WaterLeakAlert
	var toNotify []*WaterLeakSensor

	for _, sensor := range m.waterLeakSensors {
		if sensor.State == "on" {
			alert := shadowstate.WaterLeakAlert{
				EntityID:         sensor.EntityID,
				FriendlyName:     sensor.FriendlyName,
				DetectedAt:       sensor.LastChanged,
				NotificationSent: sensor.NotificationSent,
			}
			activeLeaks = append(activeLeaks, alert)

			if !sensor.NotificationSent {
				toNotify = append(toNotify, sensor)
			}
		}
	}
	m.mu.Unlock()

	// Update shadow state with active leaks
	m.shadowTracker.UpdateActiveWaterLeaks(activeLeaks)

	// Send notifications for new leaks
	for _, sensor := range toNotify {
		m.sendWaterLeakNotification(sensor)
	}
}

// sendWaterLeakNotification sends a notification for a water leak
func (m *Manager) sendWaterLeakNotification(sensor *WaterLeakSensor) {
	message := fmt.Sprintf("Water leak detected at %s", sensor.FriendlyName)

	m.logger.Warn("Water leak detected",
		zap.String("entity_id", sensor.EntityID),
		zap.String("friendly_name", sensor.FriendlyName))

	// Record notification in shadow state
	m.shadowTracker.RecordWaterLeakNotification(sensor.EntityID, sensor.FriendlyName, message)

	// Mark as notified
	m.mu.Lock()
	sensor.NotificationSent = true
	m.mu.Unlock()

	if m.readOnly {
		m.logger.Info("Skipping water leak notification send in read-only mode",
			zap.String("entity_id", sensor.EntityID),
			zap.String("message", message))
		return
	}

	if m.ntfyClient == nil {
		m.logger.Warn("ntfy client not configured, cannot send water leak notification",
			zap.String("entity_id", sensor.EntityID))
		return
	}

	// Send notification via ntfy
	if err := m.ntfyClient.Send(&ntfy.Message{
		Title:    "Water Leak Detected",
		Body:     message,
		Priority: ntfy.PriorityUrgent,
		Tags:     []string{"warning", "droplet"},
	}); err != nil {
		m.logger.Error("Failed to send water leak notification",
			zap.String("entity_id", sensor.EntityID),
			zap.Error(err))
	} else {
		m.logger.Info("Water leak notification sent", zap.String("message", message))
	}
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

		// Determine effective thresholds based on sensor type
		warningThreshold := HumidityWarningThreshold
		criticalThreshold := HumidityCriticalThreshold
		if sensor.IsUnconditioned {
			warningThreshold = UnconditionedWarningThreshold
			criticalThreshold = UnconditionedCriticalThreshold
		}

		// Suppress unconditioned sensors that are simply tracking outdoor humidity.
		// Suppression applies when:
		// 1. Sensor is in an unconditioned space
		// 2. Outdoor humidity reference is available
		// 3. Indoor reading is within margin of outdoor reading
		// 4. Indoor reading is below the absolute ceiling (80%)
		suppressed := sensor.IsUnconditioned && m.outdoorHumidityValid &&
			sensor.Value <= m.outdoorHumidity+OutdoorHumidityMargin &&
			sensor.Value < UnconditionedCriticalThreshold

		isWarning := !suppressed && sensor.Value >= warningThreshold
		isCritical := !suppressed && sensor.Value >= criticalThreshold

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
	// Check if ALL indoor sensors are below their clear thresholds
	allCleared := true
	for _, sensor := range m.sensors {
		if !sensor.IsIndoor {
			continue
		}

		// Suppressed unconditioned sensors (tracking outdoor humidity) are considered cleared
		if sensor.IsUnconditioned && sensor.Valid && m.outdoorHumidityValid &&
			sensor.Value <= m.outdoorHumidity+OutdoorHumidityMargin &&
			sensor.Value < UnconditionedCriticalThreshold {
			continue
		}

		// Determine per-sensor clear threshold
		clearThreshold := HumidityWarningClear
		if m.currentAlertLevel == "critical" {
			clearThreshold = HumidityCriticalClear
		}
		if sensor.IsUnconditioned {
			clearThreshold = UnconditionedWarningClear
			if m.currentAlertLevel == "critical" {
				clearThreshold = UnconditionedCriticalClear
			}
		}

		// An alerted sensor going unavailable means we lost visibility, not that it cleared
		if !sensor.Valid && m.alertedSensorNames[sensor.FriendlyName] {
			allCleared = false
			break
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
		m.alertedSensorNames = make(map[string]bool)
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
	// Build sensor location list and find max humidity
	// (must happen before rate limit check so alertedSensorNames is always populated)
	var sensorLocations []string
	var maxHumidity float64

	for _, sensor := range m.sensors {
		if !sensor.IsIndoor || !sensor.Valid {
			continue
		}
		// Use per-sensor warning threshold to determine which sensors to include
		threshold := HumidityWarningThreshold
		if sensor.IsUnconditioned {
			threshold = UnconditionedWarningThreshold
		}
		if sensor.Value >= threshold {
			sensorLocations = append(sensorLocations, sensor.FriendlyName)
			if sensor.Value > maxHumidity {
				maxHumidity = sensor.Value
			}
		}
	}

	// Track which sensors are part of this alert incident
	for _, name := range sensorLocations {
		m.alertedSensorNames[name] = true
	}

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
		// Mark as notified even when rate-limited — a notification was already sent recently,
		// so we consider this incident as having been communicated to the user
		m.hasNotifiedForCurrentIncident = true
		return
	}

	// Build notification message with optional outdoor humidity context
	var title, message string
	var priority int
	var tags []string

	outdoorContext := ""
	if m.outdoorHumidityValid {
		outdoorContext = fmt.Sprintf(" Outdoor: %.0f%%.", m.outdoorHumidity)
	}

	if level == "critical" {
		title = "High Humidity Critical"
		message = fmt.Sprintf("Humidity at %.0f%% (%s) for 30+ minutes.%s Mold risk - take action!",
			maxHumidity, formatSensorLocations(sensorLocations), outdoorContext)
		priority = ntfy.PriorityHigh
		tags = []string{"rotating_light", "droplet"}
	} else {
		title = "High Humidity Warning"
		message = fmt.Sprintf("Humidity at %.0f%% (%s) for 30+ minutes.%s Check ventilation.",
			maxHumidity, formatSensorLocations(sensorLocations), outdoorContext)
		priority = ntfy.PriorityDefault
		tags = []string{"warning", "droplet"}
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

	// Check if ntfy client is configured
	if m.ntfyClient == nil {
		m.logger.Warn("Cannot send humidity notification - ntfy client not configured",
			zap.String("level", level))
		return
	}

	// Send notification via ntfy
	if err := m.ntfyClient.Send(&ntfy.Message{
		Title:    title,
		Body:     message,
		Priority: priority,
		Tags:     tags,
	}); err != nil {
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
	// Build summary of only the sensors that were previously alerted
	var sensorSummary []string
	for _, sensor := range m.sensors {
		if !sensor.IsIndoor {
			continue
		}
		if m.alertedSensorNames[sensor.FriendlyName] {
			if sensor.Valid {
				sensorSummary = append(sensorSummary, fmt.Sprintf("%s: %.0f%%", sensor.FriendlyName, sensor.Value))
			} else {
				sensorSummary = append(sensorSummary, fmt.Sprintf("%s: unavailable", sensor.FriendlyName))
			}
		}
	}

	// Fallback: if no tracked names (shouldn't happen), list all indoor sensors
	if len(sensorSummary) == 0 {
		for _, sensor := range m.sensors {
			if !sensor.IsIndoor || !sensor.Valid {
				continue
			}
			sensorSummary = append(sensorSummary, fmt.Sprintf("%s: %.0f%%", sensor.FriendlyName, sensor.Value))
		}
	}

	var message string
	if len(sensorSummary) == 1 {
		// Single sensor: "Air Quality Tracker SEN55 Humidity has returned to safe levels (31%)"
		message = fmt.Sprintf("%s has returned to safe levels", sensorSummary[0])
	} else {
		message = fmt.Sprintf("Humidity has returned to safe levels. Resolved sensors: %s",
			strings.Join(sensorSummary, ", "))
	}

	m.logger.Info("Sending humidity resolution notification")

	// Record in shadow state
	m.shadowTracker.RecordResolutionNotice(message)

	if m.readOnly {
		m.logger.Info("Skipping resolution notification send in read-only mode",
			zap.String("message", message))
		return
	}

	// Check if ntfy client is configured
	if m.ntfyClient == nil {
		m.logger.Warn("Cannot send resolution notification - ntfy client not configured")
		return
	}

	// Send notification via ntfy
	if err := m.ntfyClient.Send(&ntfy.Message{
		Title:    "Humidity Resolved",
		Body:     message,
		Priority: ntfy.PriorityDefault,
		Tags:     []string{"white_check_mark", "droplet"},
	}); err != nil {
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
	// For 3+ sensors, list all names joined with commas
	return strings.Join(locations, ", ")
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
	for _, sensor := range m.waterLeakSensors {
		sensor.State = "off"
		sensor.NotificationSent = false
	}
	m.currentAlertLevel = "none"
	m.outdoorHumidity = 0
	m.outdoorHumidityValid = false
	m.lastWarningNotification = time.Time{}
	m.lastCriticalNotification = time.Time{}
	m.lastResolutionNotification = time.Time{}
	m.hasNotifiedForCurrentIncident = false
	m.alertedSensorNames = make(map[string]bool)
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

// SetOutdoorHumidity sets the outdoor reference humidity for testing
func (m *Manager) SetOutdoorHumidity(humidity float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outdoorHumidity = humidity
	m.outdoorHumidityValid = true
}

// ClearOutdoorHumidity clears the outdoor reference humidity for testing
func (m *Manager) ClearOutdoorHumidity() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outdoorHumidity = 0
	m.outdoorHumidityValid = false
}

// SimulateSensorUnavailable simulates a sensor going unavailable (for testing)
func (m *Manager) SimulateSensorUnavailable(entityID string) {
	m.handleHumidityChange(entityID, nil, &ha.State{
		EntityID: entityID,
		State:    "unavailable",
	})
}

// GetWaterLeakSensors returns all water leak sensors for testing
func (m *Manager) GetWaterLeakSensors() map[string]*WaterLeakSensor {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make(map[string]*WaterLeakSensor, len(m.waterLeakSensors))
	for k, v := range m.waterLeakSensors {
		copy := *v
		result[k] = &copy
	}
	return result
}

// AddWaterLeakSensor adds a water leak sensor for testing
func (m *Manager) AddWaterLeakSensor(sensor *WaterLeakSensor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waterLeakSensors[sensor.EntityID] = sensor
}

// SimulateWaterLeakChange simulates a water leak sensor state change for testing
func (m *Manager) SimulateWaterLeakChange(entityID, state string) {
	m.mu.Lock()
	sensor, ok := m.waterLeakSensors[entityID]
	if !ok {
		m.mu.Unlock()
		return
	}
	oldState := sensor.State
	sensor.State = state
	sensor.LastChanged = m.clock.Now()
	if state == "off" && oldState == "on" {
		sensor.NotificationSent = false
	}
	m.mu.Unlock()

	m.updateShadowWaterLeakSensors()
	m.evaluateWaterLeaks()
}

// GetActiveWaterLeakCount returns the count of active water leaks for testing
func (m *Manager) GetActiveWaterLeakCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, sensor := range m.waterLeakSensors {
		if sensor.State == "on" {
			count++
		}
	}
	return count
}
